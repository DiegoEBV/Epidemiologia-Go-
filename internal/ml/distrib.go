package ml

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/data"
)

// Este archivo separa el entrenamiento en dos piezas que ya no tienen por qué
// vivir en el mismo proceso (PC4):
//
//   - ShardLocal: lo que hace un worker/nodo. Guarda su fragmento de datos y
//     responde cada MsgPesos con el MsgGradiente de su siguiente mini-lote.
//   - Coordinar: lo que hace el Parameter Server. Difunde los pesos, espera los
//     P gradientes (barrera), los reduce y actualiza el modelo.
//
// La interfaz Calculador es el punto de corte: en la PC3 la implementaba una
// goroutine con canales; en la PC4 la implementa un cliente TCP hacia un
// contenedor (internal/cluster). El algoritmo no cambia.

// Calculador es cualquier nodo capaz de devolver el gradiente de su mini-lote.
type Calculador interface {
	Gradiente(ctx context.Context, msg MsgPesos) (MsgGradiente, error)
}

// Normalizacion son los estadísticos globales que todos los nodos deben
// compartir para codificar igual cada registro.
type Normalizacion struct {
	MediaEdad float64
	DesvEdad  float64
	PesoPos   float64
}

// Estadisticos calcula la normalización sobre el set de entrenamiento.
func Estadisticos(recs []data.Record, train []int32) Normalizacion {
	m, d, p := estadisticos(recs, train)
	return Normalizacion{MediaEdad: m, DesvEdad: d, PesoPos: p}
}

// PartirShards baraja el set de entrenamiento con la semilla de la config y lo
// reparte en P fragmentos contiguos. Es idéntico al reparto de EntrenarParalelo,
// por eso el resultado distribuido coincide con el de la PC3.
func PartirShards(train []int32, P int, semilla int64) [][]int32 {
	if P < 1 {
		P = 1
	}
	idx := make([]int32, len(train))
	copy(idx, train)
	rng := rand.New(rand.NewSource(semilla))
	rng.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	shards := make([][]int32, P)
	paso := len(idx) / P
	for w := 0; w < P; w++ {
		ini, fin := w*paso, (w+1)*paso
		if w == P-1 {
			fin = len(idx)
		}
		shards[w] = idx[ini:fin]
	}
	return shards
}

// Plan reúne el tamaño del mini-lote local y los pasos por época.
func Plan(nTrain, P, loteGlobal int) (loteLocal, pasosEpoca int) {
	if P < 1 {
		P = 1
	}
	loteLocal = loteGlobal / P
	if loteLocal < 1 {
		loteLocal = 1
	}
	pasosEpoca = (nTrain / P) / loteLocal
	if pasosEpoca < 1 {
		pasosEpoca = 1
	}
	return
}

// ShardLocal es el estado de un worker: sus registros, su orden de lectura y
// sus buffers. Solo lo toca la goroutine que atiende a ese worker.
type ShardLocal struct {
	ID        int
	recs      []data.Record
	idx       []int32
	loteLocal int
	norm      Normalizacion
	rng       *rand.Rand
	gw        []float64
	Pasos     int // pasos atendidos (métrica del nodo)
}

// NuevoShardLocal crea el worker sobre recs[idx]. idx se baraja en sitio al
// inicio de cada época, con la misma semilla (1000+id) que la PC3.
func NuevoShardLocal(id int, recs []data.Record, idx []int32, loteLocal int, norm Normalizacion) *ShardLocal {
	return &ShardLocal{
		ID: id, recs: recs, idx: idx, loteLocal: loteLocal, norm: norm,
		rng: rand.New(rand.NewSource(int64(1000 + id))),
		gw:  make([]float64, NumFeat),
	}
}

// Registros devuelve el tamaño del fragmento.
func (s *ShardLocal) Registros() int { return len(s.idx) }

// Responder calcula el gradiente del mini-lote que corresponde a msg.Paso.
func (s *ShardLocal) Responder(msg MsgPesos) MsgGradiente {
	if msg.Paso == 0 { // nueva época: baraja el fragmento local
		s.rng.Shuffle(len(s.idx), func(i, j int) { s.idx[i], s.idx[j] = s.idx[j], s.idx[i] })
	}
	ini := msg.Paso * s.loteLocal
	fin := ini + s.loteLocal
	if fin > len(s.idx) {
		fin = len(s.idx)
	}
	if ini > fin {
		ini = fin
	}
	gb, perd, pt := gradienteLote(s.recs, s.idx[ini:fin], msg.W, msg.B,
		s.norm.MediaEdad, s.norm.DesvEdad, s.norm.PesoPos, s.gw)
	out := make([]float64, NumFeat)
	copy(out, s.gw)
	s.Pasos++
	return MsgGradiente{Worker: s.ID, Paso: msg.Paso, GW: out, GB: gb, Perdida: perd, PesoTotal: pt, Muestras: fin - ini}
}

// Gradiente permite usar un ShardLocal directamente como Calculador
// (útil en pruebas y para correr el coordinador sin red).
func (s *ShardLocal) Gradiente(_ context.Context, msg MsgPesos) (MsgGradiente, error) {
	return s.Responder(msg), nil
}

// Progreso se emite al final de cada época.
type Progreso struct {
	Epoca, Epocas int
	Perdida       float64
	Duracion      time.Duration
	Pasos         int
}

// Coordinar ejecuta SGD síncrono (Parameter Server) sobre P calculadores.
// En cada paso lanza una goroutine por nodo para enviar los pesos y esperar su
// gradiente; el WaitGroup es la barrera. La reducción se hace en orden de
// worker, de modo que el resultado es determinista. Solo esta función escribe
// los pesos del modelo.
func Coordinar(ctx context.Context, nodos []Calculador, norm Normalizacion, pasosEpoca int,
	cfg Config, onEpoca func(Progreso)) (*Resultado, error) {

	inicio := time.Now()
	P := len(nodos)
	if P == 0 {
		return nil, errors.New("no hay nodos para entrenar")
	}
	m := NuevoModelo(norm.MediaEdad, norm.DesvEdad, norm.PesoPos)
	res := &Resultado{Modelo: m}
	grads := make([]MsgGradiente, P)
	errs := make([]error, P)
	sumGW := make([]float64, NumFeat)

	for ep := 0; ep < cfg.Epocas; ep++ {
		t0 := time.Now()
		eta := cfg.TasaAprend / (1 + cfg.Decaimiento*float64(ep))
		perdEp, pesoEp := 0.0, 0.0
		for s := 0; s < pasosEpoca; s++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// Broadcast: cada nodo recibe su propia copia de los pesos.
			var wg sync.WaitGroup
			for w := 0; w < P; w++ {
				wc := make([]float64, NumFeat)
				copy(wc, m.W)
				msg := MsgPesos{Epoca: ep, Paso: s, W: wc, B: m.B}
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					grads[w], errs[w] = nodos[w].Gradiente(ctx, msg)
				}(w)
			}
			wg.Wait() // barrera
			for w, err := range errs {
				if err != nil {
					return nil, fmt.Errorf("nodo %d, época %d, paso %d: %w", w, ep+1, s, err)
				}
			}
			// Reducción en orden de worker (determinista).
			for i := range sumGW {
				sumGW[i] = 0
			}
			sumGB, pesoTot := 0.0, 0.0
			for _, g := range grads {
				for i, v := range g.GW {
					sumGW[i] += v
				}
				sumGB += g.GB
				pesoTot += g.PesoTotal
				perdEp += g.Perdida
				pesoEp += g.PesoTotal
			}
			if pesoTot == 0 {
				continue
			}
			for i := range m.W {
				m.W[i] -= eta * (sumGW[i]/pesoTot + cfg.L2*m.W[i])
			}
			m.B -= eta * sumGB / pesoTot
			res.Pasos++
		}
		h := HistEpoca{Epoca: ep + 1, Perdida: perdEp / pesoEp, Duracion: time.Since(t0)}
		res.Historial = append(res.Historial, h)
		if onEpoca != nil {
			onEpoca(Progreso{Epoca: ep + 1, Epocas: cfg.Epocas, Perdida: h.Perdida, Duracion: h.Duracion, Pasos: res.Pasos})
		}
	}
	res.Duracion = time.Since(inicio)
	return res, nil
}

// Aportes descompone el logit de un registro en la contribución de cada
// variable (lo usa la API para explicar la predicción).
type Aportes struct {
	Base, Edad, Sexo, Departamento, Periodo float64
}

// Explicar devuelve los aportes al logit z = b + Σ wᵢxᵢ.
func (m *Modelo) Explicar(r *data.Record) Aportes {
	ez := (float64(r.Edad) - m.MediaEdad) / m.DesvEdad
	return Aportes{
		Base:         m.B,
		Edad:         m.W[fEdad]*ez + m.W[fEdad2]*ez*ez,
		Sexo:         m.W[fSexo] * float64(r.Sexo),
		Departamento: m.W[fDept+int(r.Dept)],
		Periodo:      m.W[fOla+int(r.Ola)],
	}
}
