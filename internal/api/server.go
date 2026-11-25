// Package api implementa la Parte C del TF: la API REST/WebSocket que además
// es el coordinador del clúster de nodos ML.
package api

import (
	"embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

//go:embed swagger/openapi.yaml swagger/index.html
var swaggerFS embed.FS

// Servidor agrupa el enrutador HTTP y sus dependencias.
type Servidor struct {
	cfg Config
	app *App
	hub *Hub
	lat *RegistroLatencias
	mux *http.ServeMux
}

// NuevoServidor registra todas las rutas (patrones con método de Go 1.22+).
func NuevoServidor(cfg Config, app *App, hub *Hub) *Servidor {
	s := &Servidor{cfg: cfg, app: app, hub: hub, lat: NuevoRegistroLatencias(), mux: http.NewServeMux()}
	admin := "admin"

	// Públicas
	s.mux.HandleFunc("GET /health", s.salud)
	s.mux.HandleFunc("POST /api/auth/login", s.login)
	s.mux.HandleFunc("GET /swagger/", s.swagger)
	if _, err := os.Stat(filepath.Join(cfg.WebDir, "index.html")); cfg.WebDir != "" && err == nil {
		s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(cfg.WebDir, "index.html"))
		})
		s.mux.Handle("GET /assets/", http.FileServer(http.Dir(cfg.WebDir)))
	} else {
		s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/swagger/", http.StatusFound)
		})
	}

	// Protegidas con JWT
	s.mux.HandleFunc("POST /api/predicciones", s.conAuth(s.predecir))
	s.mux.HandleFunc("POST /api/predicciones/lote", s.conAuth(s.predecirLote))
	s.mux.HandleFunc("GET /api/predicciones", s.conAuth(s.listarPredicciones))
	s.mux.HandleFunc("GET /api/modelo", s.conAuth(s.modeloActivo))
	s.mux.HandleFunc("GET /api/modelos", s.conAuth(s.listarModelos))
	s.mux.HandleFunc("GET /api/impacto", s.conAuth(s.impacto))
	s.mux.HandleFunc("GET /api/impacto/departamentos", s.conAuth(s.impactoDepartamentos))
	s.mux.HandleFunc("GET /api/dataset", s.conAuth(s.dataset))
	s.mux.HandleFunc("POST /api/entrenamientos", s.conAuth(s.crearEntrenamiento, admin))
	s.mux.HandleFunc("GET /api/entrenamientos", s.conAuth(s.listarEntrenamientos))
	s.mux.HandleFunc("GET /api/entrenamientos/{id}", s.conAuth(s.obtenerEntrenamiento))
	s.mux.HandleFunc("GET /api/cluster", s.conAuth(s.estadoCluster))
	s.mux.HandleFunc("GET /api/cluster/metricas", s.conAuth(s.metricasCluster))
	s.mux.HandleFunc("GET /api/metricas", s.conAuth(s.metricasAPI, admin))
	s.mux.HandleFunc("GET /ws", s.conAuth(s.manejarWS))
	return s
}

// Handler devuelve el enrutador con la cadena de middleware:
// recuperación de pánicos → cabeceras de seguridad → medición → rutas.
func (s *Servidor) Handler() http.Handler {
	return s.recuperar(proteger(s.medir(s.mux)))
}

// Correr arranca el hub de WebSocket y la suscripción a Redis.
func (s *Servidor) Correr() {
	go s.hub.Correr()
	go s.suscribirRedis()
}

func (s *Servidor) medir(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g := &grabador{ResponseWriter: w, inicio: time.Now(), status: http.StatusOK}
		next.ServeHTTP(g, r)
		ruta := r.Pattern // patrón que atendió la petición, p. ej. "POST /api/predicciones"
		if ruta == "" {
			ruta = r.Method + " (sin ruta)"
		}
		d := time.Since(g.inicio)
		s.lat.Registrar(ruta, d, g.status)
		if g.status >= 500 || d > 100*time.Millisecond && ruta != "GET /ws" {
			log.Printf("%s %s → %d en %.2f ms", r.Method, r.URL.Path, g.status, ms(d))
		}
	})
}

func proteger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if r.URL.Path == "/" {
			h.Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		}
		if r.URL.Path == "/ws" || strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Servidor) recuperar(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("pánico en %s %s: %v\n%s", r.Method, r.URL.Path, v, debug.Stack())
				responderError(w, http.StatusInternalServerError, "error interno")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func responderJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func responderError(w http.ResponseWriter, status int, msg string) {
	responderJSON(w, status, map[string]any{"error": msg, "status": status})
}
