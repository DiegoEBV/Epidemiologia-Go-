package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/ml"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/store"
)

const maxCuerpo = 1 << 20 // 1 MB

func leerJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCuerpo))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return ErrValidacion{"JSON inválido: " + err.Error()}
	}
	return nil
}

func limite(r *http.Request, def, max int64) int64 {
	n, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func (s *Servidor) errorDe(w http.ResponseWriter, err error) {
	var ev ErrValidacion
	switch {
	case errors.As(err, &ev):
		responderError(w, http.StatusBadRequest, ev.Msg)
	case errors.Is(err, ErrSinModelo):
		w.Header().Set("Retry-After", "10")
		responderError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, store.ErrNoEncontrado):
		responderError(w, http.StatusNotFound, "recurso no encontrado")
	case errors.Is(err, ErrOcupado):
		responderError(w, http.StatusConflict, err.Error())
	default:
		responderError(w, http.StatusInternalServerError, err.Error())
	}
}

func ctxCorto(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 3*time.Second)
}

// GET /health
func (s *Servidor) salud(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	out := s.app.EstadoResumen()
	out["mongo"] = s.app.mongo.Ping(ctx) == nil
	out["redis"] = s.app.redis != nil && s.app.redis.C.Ping(ctx).Err() == nil
	out["clientes_ws"] = s.hub.Clientes()
	status := http.StatusOK
	if s.app.Estado() == EstErrorArranque {
		status = http.StatusServiceUnavailable
	}
	responderJSON(w, status, out)
}

// POST /api/auth/login
func (s *Servidor) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := leerJSON(w, r, &in); err != nil {
		s.errorDe(w, err)
		return
	}
	ctx, cancel := ctxCorto(r)
	defer cancel()
	u, err := s.app.mongo.BuscarUsuario(ctx, in.Username)
	if err != nil || !VerificarPassword(in.Password, u.PassHash, u.Salt, u.Iter) {
		responderError(w, http.StatusUnauthorized, "usuario o contraseña incorrectos")
		return
	}
	ahora := time.Now()
	exp := ahora.Add(8 * time.Hour)
	tok, err := FirmarJWT(s.cfg.SecretoJWT, Claims{Sub: u.Username, Rol: u.Rol, Iat: ahora.Unix(), Exp: exp.Unix()})
	if err != nil {
		s.errorDe(w, err)
		return
	}
	responderJSON(w, http.StatusOK, map[string]any{"token": tok, "tipo": "Bearer", "expira": exp, "usuario": u.Username, "rol": u.Rol})
}

// POST /api/predicciones
func (s *Servidor) predecir(w http.ResponseWriter, r *http.Request) {
	var in store.EntradaPrediccion
	if err := leerJSON(w, r, &in); err != nil {
		s.errorDe(w, err)
		return
	}
	resp, err := s.app.Predecir(r.Context(), in, usuarioDe(r.Context()).Sub)
	if err != nil {
		s.errorDe(w, err)
		return
	}
	responderJSON(w, http.StatusOK, resp)
}

// POST /api/predicciones/lote
func (s *Servidor) predecirLote(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Casos []store.EntradaPrediccion `json:"casos"`
	}
	if err := leerJSON(w, r, &in); err != nil {
		s.errorDe(w, err)
		return
	}
	if len(in.Casos) == 0 || len(in.Casos) > 1000 {
		responderError(w, http.StatusBadRequest, "casos debe tener entre 1 y 1000 elementos")
		return
	}
	t0 := time.Now()
	res, errs, err := s.app.PredecirLote(in.Casos, usuarioDe(r.Context()).Sub)
	if err != nil {
		s.errorDe(w, err)
		return
	}
	type item struct {
		*RespuestaPrediccion
		Error string `json:"error,omitempty"`
	}
	items := make([]item, len(res))
	resumen := map[string]int{}
	for i := range res {
		items[i] = item{res[i], errs[i]}
		if res[i] != nil {
			resumen[res[i].Nivel]++
		}
	}
	responderJSON(w, http.StatusOK, map[string]any{"total": len(res), "por_nivel": resumen,
		"latencia_ms": ms(time.Since(t0)), "resultados": items})
}

// GET /api/predicciones?limit=&departamento=
func (s *Servidor) listarPredicciones(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxCorto(r)
	defer cancel()
	ps, err := s.app.mongo.ListarPredicciones(ctx, strings.ToUpper(r.URL.Query().Get("departamento")), limite(r, 50, 500))
	if err != nil {
		s.errorDe(w, err)
		return
	}
	total, _ := s.app.mongo.ContarPredicciones(ctx)
	responderJSON(w, http.StatusOK, map[string]any{"total_historico": total, "predicciones": ps})
}

// GET /api/modelo
func (s *Servidor) modeloActivo(w http.ResponseWriter, r *http.Request) {
	ma := s.app.modelo.Load()
	if ma == nil {
		s.errorDe(w, ErrSinModelo)
		return
	}
	responderJSON(w, http.StatusOK, ma.Doc)
}

// GET /api/modelos
func (s *Servidor) listarModelos(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxCorto(r)
	defer cancel()
	ms, err := s.app.mongo.ListarModelos(ctx, limite(r, 20, 100))
	if err != nil {
		s.errorDe(w, err)
		return
	}
	responderJSON(w, http.StatusOK, ms)
}

// GET /api/impacto?top=&departamento=
// Lee primero de Redis (ranking precalculado) y si no está, de MongoDB.
func (s *Servidor) impacto(w http.ResponseWriter, r *http.Request) {
	ma := s.app.modelo.Load()
	if ma == nil {
		s.errorDe(w, ErrSinModelo)
		return
	}
	top, err := strconv.Atoi(r.URL.Query().Get("top"))
	if err != nil || top <= 0 || top > 300 {
		top = 15
	}
	dept := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("departamento")))
	ctx, cancel := ctxCorto(r)
	defer cancel()
	key := store.KeyImpacto(ma.Doc.Version, dept, top)
	type resp struct {
		ModeloVersion int             `json:"modelo_version"`
		MesDesde      int32           `json:"casos_desde"`
		Pregunta      string          `json:"pregunta"`
		Provincias    []store.Impacto `json:"provincias"`
		TotalGraves   float64         `json:"total_graves_esperados"`
		Cache         bool            `json:"cache"`
	}
	var out resp
	if s.app.redis != nil && s.app.redis.GetJSON(ctx, key, &out) == nil {
		out.Cache = true
		responderJSON(w, http.StatusOK, out)
		return
	}
	filas, err := s.app.mongo.ImpactoTop(ctx, ma.Doc.Version, dept, int64(top))
	if err != nil {
		s.errorDe(w, err)
		return
	}
	out = resp{ModeloVersion: ma.Doc.Version, Provincias: filas,
		Pregunta: "¿En qué provincias se concentrará el mayor número de casos graves según el perfil de sus casos positivos recientes?"}
	for _, f := range filas {
		out.TotalGraves += f.GravesEsperados
		out.MesDesde = f.MesDesde
	}
	if s.app.redis != nil {
		_ = s.app.redis.SetJSON(ctx, key, out, store.TTLImpacto)
	}
	responderJSON(w, http.StatusOK, out)
}

// GET /api/impacto/departamentos
func (s *Servidor) impactoDepartamentos(w http.ResponseWriter, r *http.Request) {
	ma := s.app.modelo.Load()
	if ma == nil {
		s.errorDe(w, ErrSinModelo)
		return
	}
	ctx, cancel := ctxCorto(r)
	defer cancel()
	out, err := s.app.mongo.ImpactoPorDepartamento(ctx, ma.Doc.Version)
	if err != nil {
		s.errorDe(w, err)
		return
	}
	responderJSON(w, http.StatusOK, out)
}

// GET /api/dataset
func (s *Servidor) dataset(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxCorto(r)
	defer cancel()
	d, err := s.app.mongo.ObtenerDataset(ctx)
	if err != nil {
		s.errorDe(w, err)
		return
	}
	responderJSON(w, http.StatusOK, struct {
		*store.Dataset
		Fuente string `json:"fuente_datos,omitempty"`
	}{d, s.cfg.FuenteDatos})
}

// POST /api/entrenamientos (solo admin) → 202 Accepted y el trabajo corre en segundo plano.
func (s *Servidor) crearEntrenamiento(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Nodos       int     `json:"nodos"`
		Epocas      int     `json:"epocas"`
		LoteGlobal  int     `json:"lote_global"`
		TasaAprend  float64 `json:"tasa_aprendizaje"`
		Decaimiento float64 `json:"decaimiento"`
		L2          float64 `json:"l2"`
	}
	if err := leerJSON(w, r, &in); err != nil {
		s.errorDe(w, err)
		return
	}
	cfg := ml.ConfigPorDefecto()
	if in.Epocas != 0 {
		cfg.Epocas = in.Epocas
	}
	if in.LoteGlobal != 0 {
		cfg.LoteGlobal = in.LoteGlobal
	}
	if in.TasaAprend != 0 {
		cfg.TasaAprend = in.TasaAprend
	}
	if in.Decaimiento != 0 {
		cfg.Decaimiento = in.Decaimiento
	}
	if in.L2 != 0 {
		cfg.L2 = in.L2
	}
	if cfg.Epocas < 1 || cfg.Epocas > 50 || cfg.LoteGlobal < 256 || cfg.LoteGlobal > 1<<20 ||
		cfg.TasaAprend <= 0 || cfg.TasaAprend > 10 || in.Nodos < 0 {
		responderError(w, http.StatusBadRequest, "parámetros fuera de rango (epocas 1-50, lote_global 256-1048576, tasa_aprendizaje 0-10)")
		return
	}
	ctx, cancel := ctxCorto(r)
	defer cancel()
	job, err := s.app.NuevoEntrenamiento(ctx, usuarioDe(r.Context()).Sub, cfg, in.Nodos)
	if err != nil {
		s.errorDe(w, err)
		return
	}
	go s.app.EjecutarEntrenamiento(job)
	w.Header().Set("Location", "/api/entrenamientos/"+job.ID)
	responderJSON(w, http.StatusAccepted, job)
}

// GET /api/entrenamientos
func (s *Servidor) listarEntrenamientos(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxCorto(r)
	defer cancel()
	js, err := s.app.mongo.ListarEntrenamientos(ctx, limite(r, 20, 100))
	if err != nil {
		s.errorDe(w, err)
		return
	}
	responderJSON(w, http.StatusOK, js)
}

// GET /api/entrenamientos/{id}
func (s *Servidor) obtenerEntrenamiento(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxCorto(r)
	defer cancel()
	j, err := s.app.mongo.ObtenerEntrenamiento(ctx, r.PathValue("id"))
	if err != nil {
		s.errorDe(w, err)
		return
	}
	responderJSON(w, http.StatusOK, j)
}

// GET /api/cluster
func (s *Servidor) estadoCluster(w http.ResponseWriter, r *http.Request) {
	infos := s.app.cluster.Info()
	activos := 0
	for _, i := range infos {
		if i.Conectado {
			activos++
		}
	}
	responderJSON(w, http.StatusOK, map[string]any{
		"topologia":            "estrella (coordinador ↔ nodos, TCP persistente, gob)",
		"nodos_total":          len(infos),
		"nodos_activos":        activos,
		"entrenando":           s.app.entrenando.Load(),
		"entrenamiento_actual": s.app.trabajoAct.Load(),
		"nodos":                infos,
	})
}

// GET /api/cluster/metricas?nodo=&limit=
func (s *Servidor) metricasCluster(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxCorto(r)
	defer cancel()
	m, err := s.app.mongo.MetricasCluster(ctx, r.URL.Query().Get("nodo"), limite(r, 100, 2000))
	if err != nil {
		s.errorDe(w, err)
		return
	}
	responderJSON(w, http.StatusOK, m)
}

// GET /api/metricas: latencias de la API, caché y colecciones.
func (s *Servidor) metricasAPI(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxCorto(r)
	defer cancel()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	total, hits := s.app.predTotal.Load(), s.app.cacheHits.Load()
	tasa := 0.0
	if total > 0 {
		tasa = float64(hits) / float64(total)
	}
	var global int64
	if s.app.redis != nil {
		global, _ = s.app.redis.C.Get(ctx, store.KeyStatsPred).Int64()
	}
	responderJSON(w, http.StatusOK, map[string]any{
		"uptime_s":             time.Since(s.app.inicio).Seconds(),
		"goroutines":           runtime.NumGoroutine(),
		"heap_mb":              float64(mem.HeapAlloc) / (1 << 20),
		"predicciones_proceso": total,
		"predicciones_redis":   global,
		"cache_hits":           hits,
		"tasa_cache":           tasa,
		"auditoria_descartada": s.app.descartadas.Load(),
		"colecciones_mongo":    s.app.mongo.Conteos(ctx),
		"latencias":            s.lat.Resumen(),
	})
}

// GET /swagger/ (UI) y /swagger/openapi.yaml (especificación).
func (s *Servidor) swagger(w http.ResponseWriter, r *http.Request) {
	sub, _ := fs.Sub(swaggerFS, "swagger")
	http.StripPrefix("/swagger/", http.FileServer(http.FS(sub))).ServeHTTP(w, r)
}
