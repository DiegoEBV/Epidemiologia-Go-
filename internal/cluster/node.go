package cluster

import (
	"bufio"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/ml"
)

// Nodo es un worker del clúster. Escucha en un puerto TCP y atiende al
// coordinador: guarda su fragmento de datos, calcula gradientes y puntúa casos.
//
// Concurrencia dentro del nodo:
//   - una goroutine por conexión aceptada (patrón goroutine-per-connection);
//   - el fragmento activo se protege con un mutex porque podría llegar un
//     latido por otra conexión mientras se reemplaza el fragmento;
//   - la puntuación de impacto se reparte entre runtime.NumCPU() goroutines
//     (map local) y se combina al final (reduce local), igual que en la PC3.
type Nodo struct {
	ID     string
	inicio time.Time

	mu      sync.Mutex
	shard   *ml.ShardLocal
	trabajo string

	pasos  atomic.Int64
	tareas atomic.Int64
	cpu    *medidorCPU
	logger *log.Logger
}

// NuevoNodo crea un nodo con el identificador dado (p. ej. "node1").
func NuevoNodo(id string, logger *log.Logger) *Nodo {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Nodo{ID: id, inicio: time.Now(), cpu: nuevoMedidorCPU(), logger: logger}
}

// Servir acepta conexiones hasta que el listener se cierre.
func (n *Nodo) Servir(ln net.Listener) error {
	n.logger.Printf("nodo %s escuchando en %s", n.ID, ln.Addr())
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go n.atender(c)
	}
}

func (n *Nodo) atender(c net.Conn) {
	defer c.Close()
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true) // mensajes pequeños y frecuentes (gradientes)
		_ = tc.SetKeepAlive(true)
	}
	n.logger.Printf("coordinador conectado desde %s", c.RemoteAddr())
	bw := bufio.NewWriterSize(c, 64<<10)
	dec := gob.NewDecoder(bufio.NewReaderSize(c, 64<<10))
	enc := gob.NewEncoder(bw)
	for {
		var in Sobre
		if err := dec.Decode(&in); err != nil {
			if !errors.Is(err, io.EOF) {
				n.logger.Printf("conexión %s: %v", c.RemoteAddr(), err)
			}
			return
		}
		n.tareas.Add(1)
		out := n.procesar(&in)
		if err := enc.Encode(out); err != nil {
			n.logger.Printf("enviar respuesta: %v", err)
			return
		}
		if err := bw.Flush(); err != nil {
			return
		}
	}
}

func (n *Nodo) procesar(in *Sobre) *Sobre {
	switch in.Tipo {
	case TPing:
		e := n.Estado()
		return &Sobre{Tipo: TPong, Estado: &e}

	case TShard:
		t0 := time.Now()
		m := in.Shard
		recs := m.Datos.ARecords()
		idx := make([]int32, len(recs))
		for i := range idx {
			idx[i] = int32(i)
		}
		sl := ml.NuevoShardLocal(m.Worker, recs, idx, m.LoteLocal, m.Norm)
		n.mu.Lock()
		n.shard, n.trabajo = sl, m.Trabajo
		n.mu.Unlock()
		runtime.GC() // libera el fragmento anterior antes de entrenar
		ms := float64(time.Since(t0).Microseconds()) / 1000
		n.logger.Printf("trabajo %s: fragmento worker=%d con %d registros (lote local %d) en %.1f ms",
			m.Trabajo, m.Worker, len(recs), m.LoteLocal, ms)
		return &Sobre{Tipo: TShardOK, ShardOK: &MsgShardOK{Registros: len(recs), CargaMs: ms}}

	case TPesos:
		n.mu.Lock()
		sl := n.shard
		n.mu.Unlock()
		if sl == nil {
			return &Sobre{Tipo: TError, Error: "el nodo no tiene fragmento cargado"}
		}
		g := sl.Responder(*in.Pesos)
		n.pasos.Add(1)
		return &Sobre{Tipo: TGradiente, Gradiente: &g}

	case TImpacto:
		r := n.impacto(in.Impacto)
		return &Sobre{Tipo: TImpactoOK, ImpactoOK: r}
	}
	return &Sobre{Tipo: TError, Error: fmt.Sprintf("tipo de mensaje no soportado: %v", in.Tipo)}
}

// impacto puntúa los casos recibidos y suma por provincia. Map-reduce local:
// cada goroutine acumula en sus propios arreglos y luego se suman.
func (n *Nodo) impacto(m *MsgImpacto) *MsgImpactoOK {
	t0 := time.Now()
	modelo := &ml.Modelo{W: m.W, B: m.B, MediaEdad: m.Norm.MediaEdad, DesvEdad: m.Norm.DesvEdad, PesoPos: m.Norm.PesoPos}
	recs := m.Datos.ARecords()
	nw := runtime.NumCPU()
	type parcial struct {
		pos, may []int32
		gr       []float64
	}
	parciales := make([]parcial, nw)
	paso := (len(recs) + nw - 1) / nw
	var wg sync.WaitGroup
	for w := 0; w < nw; w++ {
		ini, fin := w*paso, (w+1)*paso
		if fin > len(recs) {
			fin = len(recs)
		}
		parciales[w] = parcial{make([]int32, m.NumProvs), make([]int32, m.NumProvs), make([]float64, m.NumProvs)}
		if ini >= fin {
			continue
		}
		wg.Add(1)
		go func(p *parcial, ini, fin int) {
			defer wg.Done()
			for i := ini; i < fin; i++ {
				pv := m.Datos.Prov[i]
				p.pos[pv]++
				p.gr[pv] += modelo.LetalidadEsperada(&recs[i])
				if recs[i].Edad >= 60 {
					p.may[pv]++
				}
			}
		}(&parciales[w], ini, fin)
	}
	wg.Wait()
	out := &MsgImpactoOK{Positivos: make([]int32, m.NumProvs), Graves: make([]float64, m.NumProvs), Mayores60: make([]int32, m.NumProvs)}
	for _, p := range parciales {
		for i := 0; i < m.NumProvs; i++ {
			out.Positivos[i] += p.pos[i]
			out.Graves[i] += p.gr[i]
			out.Mayores60[i] += p.may[i]
		}
	}
	out.Ms = float64(time.Since(t0).Microseconds()) / 1000
	n.logger.Printf("impacto: %d casos puntuados en %.1f ms", len(recs), out.Ms)
	return out
}

// Estado devuelve las métricas actuales del nodo (latido).
func (n *Nodo) Estado() EstadoNodo {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	host, _ := os.Hostname()
	n.mu.Lock()
	reg, trab := 0, n.trabajo
	if n.shard != nil {
		reg = n.shard.Registros()
	}
	n.mu.Unlock()
	return EstadoNodo{
		ID: n.ID, Host: host, CPUs: runtime.NumCPU(), Goroutines: runtime.NumGoroutine(),
		HeapMB: float64(ms.HeapAlloc) / (1 << 20), CPUPct: n.cpu.porcentaje(),
		Registros: reg, Trabajo: trab, Pasos: n.pasos.Load(), Tareas: n.tareas.Load(),
		UptimeS: time.Since(n.inicio).Seconds(),
	}
}

// medidorCPU calcula el % de CPU del proceso entre dos lecturas.
type medidorCPU struct {
	mu         sync.Mutex
	ultCPU     time.Duration
	ultMuestra time.Time
}

func nuevoMedidorCPU() *medidorCPU {
	return &medidorCPU{ultCPU: tiempoCPU(), ultMuestra: time.Now()}
}

func (m *medidorCPU) porcentaje() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	ahora, cpu := time.Now(), tiempoCPU()
	dt := ahora.Sub(m.ultMuestra)
	if dt <= 0 {
		return 0
	}
	pct := 100 * float64(cpu-m.ultCPU) / float64(dt)
	m.ultCPU, m.ultMuestra = cpu, ahora
	return pct
}
