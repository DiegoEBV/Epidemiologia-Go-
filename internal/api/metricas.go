package api

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// RegistroLatencias guarda las últimas N latencias por ruta en un buffer
// circular protegido por mutex (las escrituras son O(1); los percentiles se
// calculan solo cuando alguien consulta /api/metricas).
type RegistroLatencias struct {
	mu     sync.Mutex
	rutas  map[string]*serieLatencia
	inicio time.Time
}

type serieLatencia struct {
	muestras []time.Duration
	pos      int
	lleno    bool
	total    int64
	errores  int64
	max      time.Duration
}

const muestrasPorRuta = 8192

func NuevoRegistroLatencias() *RegistroLatencias {
	return &RegistroLatencias{rutas: map[string]*serieLatencia{}, inicio: time.Now()}
}

func (r *RegistroLatencias) Registrar(ruta string, d time.Duration, status int) {
	r.mu.Lock()
	s := r.rutas[ruta]
	if s == nil {
		s = &serieLatencia{muestras: make([]time.Duration, muestrasPorRuta)}
		r.rutas[ruta] = s
	}
	s.muestras[s.pos] = d
	s.pos++
	if s.pos == muestrasPorRuta {
		s.pos, s.lleno = 0, true
	}
	s.total++
	if status >= 500 {
		s.errores++
	}
	if d > s.max {
		s.max = d
	}
	r.mu.Unlock()
}

// ResumenRuta son las estadísticas de latencia de una ruta (en ms).
type ResumenRuta struct {
	Ruta       string  `json:"ruta"`
	Peticiones int64   `json:"peticiones"`
	Errores5xx int64   `json:"errores_5xx"`
	PromedioMs float64 `json:"promedio_ms"`
	P50Ms      float64 `json:"p50_ms"`
	P95Ms      float64 `json:"p95_ms"`
	P99Ms      float64 `json:"p99_ms"`
	MaxMs      float64 `json:"max_ms"`
	Bajo100ms  float64 `json:"pct_bajo_100ms"`
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Resumen devuelve los percentiles de cada ruta.
func (r *RegistroLatencias) Resumen() []ResumenRuta {
	r.mu.Lock()
	copias := map[string][]time.Duration{}
	base := map[string]ResumenRuta{}
	for ruta, s := range r.rutas {
		n := s.pos
		if s.lleno {
			n = muestrasPorRuta
		}
		c := make([]time.Duration, n)
		copy(c, s.muestras[:n])
		copias[ruta] = c
		base[ruta] = ResumenRuta{Ruta: ruta, Peticiones: s.total, Errores5xx: s.errores, MaxMs: ms(s.max)}
	}
	r.mu.Unlock()

	out := make([]ResumenRuta, 0, len(copias))
	for ruta, c := range copias {
		res := base[ruta]
		if len(c) > 0 {
			sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
			var suma time.Duration
			bajo := 0
			for _, d := range c {
				suma += d
				if d < 100*time.Millisecond {
					bajo++
				}
			}
			p := func(q float64) float64 { return ms(c[int(q*float64(len(c)-1))]) }
			res.PromedioMs = ms(suma / time.Duration(len(c)))
			res.P50Ms, res.P95Ms, res.P99Ms = p(0.50), p(0.95), p(0.99)
			res.Bajo100ms = 100 * float64(bajo) / float64(len(c))
		}
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Peticiones > out[j].Peticiones })
	return out
}

// grabador envuelve el ResponseWriter para conocer el status y escribir la
// cabecera X-Response-Time justo antes de enviar los encabezados.
type grabador struct {
	http.ResponseWriter
	inicio  time.Time
	status  int
	escrito bool
}

func (g *grabador) WriteHeader(code int) {
	if g.escrito {
		return
	}
	g.escrito, g.status = true, code
	d := time.Since(g.inicio)
	g.Header().Set("X-Response-Time", strconv.FormatFloat(ms(d), 'f', 3, 64)+"ms")
	g.Header().Set("Server-Timing", "app;dur="+strconv.FormatFloat(ms(d), 'f', 3, 64))
	g.ResponseWriter.WriteHeader(code)
}

func (g *grabador) Write(b []byte) (int, error) {
	if !g.escrito {
		g.WriteHeader(http.StatusOK)
	}
	return g.ResponseWriter.Write(b)
}

// Hijack permite que el WebSocket tome la conexión a través del envoltorio.
func (g *grabador) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := g.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack no soportado")
	}
	g.escrito, g.status = true, http.StatusSwitchingProtocols
	return h.Hijack()
}

func (g *grabador) Flush() {
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *grabador) Unwrap() http.ResponseWriter { return g.ResponseWriter }
