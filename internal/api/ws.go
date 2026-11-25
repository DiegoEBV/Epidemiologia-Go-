package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Evento es lo que se envía a los clientes WebSocket (y por Redis pub/sub).
type Evento struct {
	Tipo  string    `json:"tipo"` // entrenamiento | cluster | prediccion | estado
	Ts    time.Time `json:"ts"`
	Datos any       `json:"datos"`
}

// Hub difunde eventos a todos los clientes conectados. Patrón clásico de Go:
// una sola goroutine (run) es dueña del mapa de clientes y recibe altas, bajas
// y mensajes por canales, así no hace falta mutex sobre el mapa. Cada cliente
// tiene su propia goroutine escritora con un canal con buffer; si un cliente
// lento llena su buffer, se le desconecta en lugar de frenar a los demás.
type Hub struct {
	alta     chan *clienteWS
	baja     chan *clienteWS
	difundir chan []byte
	nClients chan chan int
}

type clienteWS struct {
	conn  *websocket.Conn
	envio chan []byte
}

func NuevoHub() *Hub {
	return &Hub{alta: make(chan *clienteWS), baja: make(chan *clienteWS),
		difundir: make(chan []byte, 256), nClients: make(chan chan int)}
}

func (h *Hub) Correr() {
	clientes := map[*clienteWS]struct{}{}
	for {
		select {
		case c := <-h.alta:
			clientes[c] = struct{}{}
		case c := <-h.baja:
			if _, ok := clientes[c]; ok {
				delete(clientes, c)
				close(c.envio)
			}
		case msg := <-h.difundir:
			for c := range clientes {
				select {
				case c.envio <- msg:
				default: // cliente lento: se descarta
					delete(clientes, c)
					close(c.envio)
				}
			}
		case resp := <-h.nClients:
			resp <- len(clientes)
		}
	}
}

// Emitir encola un evento para todos los clientes (no bloquea).
func (h *Hub) Emitir(e Evento) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	h.EmitirBytes(b)
}

func (h *Hub) EmitirBytes(b []byte) {
	select {
	case h.difundir <- b:
	default:
	}
}

// Clientes devuelve cuántos clientes hay conectados.
func (h *Hub) Clientes() int {
	r := make(chan int)
	h.nClients <- r
	return <-r
}

var actualizador = websocket.Upgrader{
	ReadBufferSize: 1024, WriteBufferSize: 4096,
	Subprotocols: []string{"epidemiologia"},
}

// manejarWS: GET /ws?token=JWT
func (s *Servidor) manejarWS(w http.ResponseWriter, r *http.Request) {
	conn, err := actualizador.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws: %v", err)
		return
	}
	c := &clienteWS{conn: conn, envio: make(chan []byte, 64)}
	// Mensaje de bienvenida con el estado actual.
	if b, err := json.Marshal(Evento{Tipo: "estado", Ts: time.Now(), Datos: s.app.EstadoResumen()}); err == nil {
		c.envio <- b
	}
	s.hub.alta <- c

	// Escritor: única goroutine que escribe en la conexión.
	go func() {
		ping := time.NewTicker(25 * time.Second)
		defer func() { ping.Stop(); conn.Close() }()
		vencimiento := time.NewTimer(time.Until(time.Unix(usuarioDe(r.Context()).Exp, 0)))
		defer vencimiento.Stop()
		for {
			select {
			case <-vencimiento.C:
				return
			case msg, ok := <-c.envio:
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if !ok {
					_ = conn.WriteMessage(websocket.CloseMessage, nil)
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
			case <-ping.C:
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()
	// Lector: detecta el cierre del cliente.
	conn.SetReadLimit(4096)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	s.hub.baja <- c
}

// suscribirRedis reenvía al hub los eventos publicados en Redis por cualquier
// réplica de la API (nivel colaborativo).
func (s *Servidor) suscribirRedis() {
	if s.app.redis == nil {
		return
	}
	sub := s.app.redis.C.Subscribe(s.app.ctx, canalEventos)
	ch := sub.Channel()
	var once sync.Once
	go func() {
		<-s.app.ctx.Done()
		once.Do(func() { sub.Close() })
	}()
	for m := range ch {
		s.hub.EmitirBytes([]byte(m.Payload))
	}
}
