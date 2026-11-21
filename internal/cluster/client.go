package cluster

import (
	"bufio"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/ml"
)

// NodoRemoto es el proxy, dentro del coordinador, de un nodo del clúster.
// Mantiene una conexión TCP persistente; el mutex garantiza que una sola
// petición esté en vuelo por conexión (petición-respuesta estricta), así un
// latido nunca se mezcla con un gradiente.
type NodoRemoto struct {
	ID   string
	Addr string

	mu   sync.Mutex
	conn net.Conn
	bw   *bufio.Writer
	enc  *gob.Encoder
	dec  *gob.Decoder

	estadoMu   sync.RWMutex
	ultEstado  EstadoNodo
	ultLatido  time.Time
	rtt        time.Duration
	ultError   string
	conectado  bool
	trabajoAct string
}

// NuevoNodoRemoto crea el proxy sin conectar todavía.
func NuevoNodoRemoto(id, addr string) *NodoRemoto {
	return &NodoRemoto{ID: id, Addr: addr}
}

func (n *NodoRemoto) conectarLocked(ctx context.Context) error {
	if n.conn != nil {
		return nil
	}
	d := net.Dialer{Timeout: 3 * time.Second, KeepAlive: 15 * time.Second}
	c, err := d.DialContext(ctx, "tcp", n.Addr)
	if err != nil {
		return err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	n.conn = c
	n.bw = bufio.NewWriterSize(c, 64<<10)
	n.enc = gob.NewEncoder(n.bw)
	n.dec = gob.NewDecoder(bufio.NewReaderSize(c, 64<<10))
	return nil
}

func (n *NodoRemoto) cerrarLocked() {
	if n.conn != nil {
		_ = n.conn.Close()
	}
	n.conn, n.bw, n.enc, n.dec = nil, nil, nil, nil
}

// Llamar envía un sobre y espera la respuesta. Si la conexión falla se
// descarta y la siguiente llamada vuelve a marcar (reconexión perezosa).
func (n *NodoRemoto) Llamar(ctx context.Context, in *Sobre, timeout time.Duration) (*Sobre, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.conectarLocked(ctx); err != nil {
		n.marcarError(err)
		return nil, err
	}
	limite := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(limite) {
		limite = dl
	}
	_ = n.conn.SetDeadline(limite)
	var out Sobre
	err := n.enc.Encode(in)
	if err == nil {
		err = n.bw.Flush()
	}
	if err == nil {
		err = n.dec.Decode(&out)
	}
	if err != nil {
		n.cerrarLocked()
		n.marcarError(err)
		return nil, fmt.Errorf("%s (%s): %w", n.ID, n.Addr, err)
	}
	if out.Tipo == TError {
		return nil, fmt.Errorf("%s: %s", n.ID, out.Error)
	}
	return &out, nil
}

func (n *NodoRemoto) marcarError(err error) {
	n.estadoMu.Lock()
	n.conectado = false
	n.ultError = err.Error()
	n.estadoMu.Unlock()
}

// Ping mide el tiempo de ida y vuelta y guarda las métricas del nodo.
func (n *NodoRemoto) Ping(ctx context.Context) (EstadoNodo, time.Duration, error) {
	t0 := time.Now()
	out, err := n.Llamar(ctx, &Sobre{Tipo: TPing}, 3*time.Second)
	if err != nil {
		return EstadoNodo{}, 0, err
	}
	rtt := time.Since(t0)
	if out.Estado == nil {
		return EstadoNodo{}, rtt, errors.New("PONG sin estado")
	}
	n.estadoMu.Lock()
	n.ultEstado, n.rtt, n.ultLatido, n.conectado, n.ultError = *out.Estado, rtt, time.Now(), true, ""
	n.estadoMu.Unlock()
	return *out.Estado, rtt, nil
}

// CargarShard envía el fragmento de entrenamiento al nodo.
func (n *NodoRemoto) CargarShard(ctx context.Context, m *MsgShard) (*MsgShardOK, error) {
	out, err := n.Llamar(ctx, &Sobre{Tipo: TShard, Shard: m}, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	n.estadoMu.Lock()
	n.trabajoAct = m.Trabajo
	n.estadoMu.Unlock()
	return out.ShardOK, nil
}

// Gradiente implementa ml.Calculador: el coordinador lo usa igual que a un
// worker local, pero cada llamada es un viaje de ida y vuelta por TCP.
func (n *NodoRemoto) Gradiente(ctx context.Context, msg ml.MsgPesos) (ml.MsgGradiente, error) {
	out, err := n.Llamar(ctx, &Sobre{Tipo: TPesos, Pesos: &msg}, 30*time.Second)
	if err != nil {
		return ml.MsgGradiente{}, err
	}
	if out.Gradiente == nil {
		return ml.MsgGradiente{}, errors.New("respuesta sin gradiente")
	}
	return *out.Gradiente, nil
}

// Impacto envía un bloque de casos a puntuar.
func (n *NodoRemoto) Impacto(ctx context.Context, m *MsgImpacto) (*MsgImpactoOK, error) {
	out, err := n.Llamar(ctx, &Sobre{Tipo: TImpacto, Impacto: m}, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	return out.ImpactoOK, nil
}

// Cerrar libera la conexión.
func (n *NodoRemoto) Cerrar() {
	n.mu.Lock()
	n.cerrarLocked()
	n.mu.Unlock()
}

// InfoNodo es la vista pública del nodo para la API / panel de administración.
type InfoNodo struct {
	ID           string     `json:"id"`
	Addr         string     `json:"direccion"`
	Conectado    bool       `json:"conectado"`
	RTTms        float64    `json:"rtt_ms"`
	UltimoLatido *time.Time `json:"ultimo_latido,omitempty"`
	Error        string     `json:"error,omitempty"`
	Estado       EstadoNodo `json:"estado"`
}

// Info devuelve la última información conocida del nodo.
func (n *NodoRemoto) Info() InfoNodo {
	n.estadoMu.RLock()
	defer n.estadoMu.RUnlock()
	i := InfoNodo{ID: n.ID, Addr: n.Addr, Conectado: n.conectado, RTTms: float64(n.rtt.Microseconds()) / 1000,
		Error: n.ultError, Estado: n.ultEstado}
	if !n.ultLatido.IsZero() {
		t := n.ultLatido
		i.UltimoLatido = &t
	}
	return i
}

// Conectado indica si el último intercambio con el nodo tuvo éxito.
func (n *NodoRemoto) Conectado() bool {
	n.estadoMu.RLock()
	defer n.estadoMu.RUnlock()
	return n.conectado
}
