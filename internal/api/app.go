package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/cluster"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/data"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/ml"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/store"
)

const canalEventos = store.CanalEventos

// Config de la API coordinadora (se llena desde variables de entorno).
type Config struct {
	WebDir              string
	FuenteDatos         string
	Puerto              string
	MongoURI, MongoDB   string
	RedisAddr           string
	Nodos               []string
	ArchPositivos       string
	ArchFallecidos      string
	SecretoJWT          []byte
	AdminUser           string
	AdminPass           string
	AnalistaUser        string
	AnalistaPass        string
	ForzarEntrenamiento bool
	MesesImpacto        int
	Epocas              int
}

// Estados del arranque del coordinador.
const (
	EstIniciando     = "iniciando"
	EstConectando    = "conectando_nodos"
	EstCargando      = "cargando_datos"
	EstEntrenando    = "entrenando"
	EstListo         = "listo"
	EstErrorArranque = "error"
)

type modeloActivo struct {
	Doc *store.Modelo
	M   *ml.Modelo
}

type dataset struct {
	recs     []data.Record
	train    []int32
	test     []int32
	mesDesde int32
	huella   string
	resumen  *store.Dataset
}

// App es el estado del coordinador. Los campos que se leen desde muchas
// goroutines (modelo, datos, estado) son atomic.Pointer/atomic.Value: el
// handler de predicción nunca toma un lock.
type App struct {
	cfg     Config
	ctx     context.Context
	mongo   *store.Mongo
	redis   *store.Redis
	cluster *cluster.Cluster
	hub     *Hub

	estado     atomic.Value // string
	errArranq  atomic.Value // string
	modelo     atomic.Pointer[modeloActivo]
	datos      atomic.Pointer[dataset]
	entrenando atomic.Bool
	trabajoAct atomic.Value // string: id del entrenamiento en curso

	logPred     chan store.Prediccion
	predTotal   atomic.Int64
	cacheHits   atomic.Int64
	descartadas atomic.Int64
	latidos     atomic.Int64
	inicio      time.Time
}

// NuevaApp arma la aplicación con sus dependencias ya conectadas.
func NuevaApp(ctx context.Context, cfg Config, m *store.Mongo, r *store.Redis, c *cluster.Cluster, hub *Hub) *App {
	a := &App{cfg: cfg, ctx: ctx, mongo: m, redis: r, cluster: c, hub: hub,
		logPred: make(chan store.Prediccion, 16384), inicio: time.Now()}
	a.estado.Store(EstIniciando)
	a.errArranq.Store("")
	a.trabajoAct.Store("")
	return a
}

func (a *App) setEstado(e string) {
	a.estado.Store(e)
	log.Printf("estado del coordinador: %s", e)
	a.emitir("estado", a.EstadoResumen())
}

// Estado devuelve la fase actual del coordinador.
func (a *App) Estado() string { return a.estado.Load().(string) }

// EstadoResumen es lo que devuelve /health y el saludo del WebSocket.
func (a *App) EstadoResumen() map[string]any {
	out := map[string]any{
		"estado":        a.Estado(),
		"uptime_s":      math.Round(time.Since(a.inicio).Seconds()),
		"nodos_activos": len(a.cluster.Activos()),
		"nodos_total":   len(a.cluster.Nodos),
		"entrenando":    a.entrenando.Load(),
	}
	if m := a.modelo.Load(); m != nil {
		out["modelo_version"] = m.Doc.Version
	}
	if e := a.errArranq.Load().(string); e != "" {
		out["error"] = e
	}
	return out
}

// emitir publica un evento en Redis (lo reciben todas las réplicas de la API
// y sus clientes WebSocket). Si Redis falla, se difunde solo localmente.
func (a *App) emitir(tipo string, datos any) {
	e := Evento{Tipo: tipo, Ts: time.Now(), Datos: datos}
	if a.redis != nil {
		ctx, cancel := context.WithTimeout(a.ctx, 300*time.Millisecond)
		err := a.redis.Publicar(ctx, e)
		cancel()
		if err == nil {
			return
		}
	}
	a.hub.Emitir(e)
}

// ---------------------------------------------------------------------------
// Arranque
// ---------------------------------------------------------------------------

// SembrarUsuarios crea las cuentas iniciales si no existen.
func (a *App) SembrarUsuarios(ctx context.Context) error {
	for _, u := range []struct{ user, pass, rol string }{
		{a.cfg.AdminUser, a.cfg.AdminPass, "admin"},
		{a.cfg.AnalistaUser, a.cfg.AnalistaPass, "analista"},
	} {
		if u.user == "" {
			continue
		}
		h, s, err := HashPassword(u.pass)
		if err != nil {
			return err
		}
		creado, err := a.mongo.CrearUsuarioSiNoExiste(ctx, store.Usuario{Username: u.user, PassHash: h, Salt: s, Iter: IterPBKDF2, Rol: u.rol, Creado: time.Now()})
		if err != nil {
			return err
		}
		if creado {
			log.Printf("usuario %q (%s) creado", u.user, u.rol)
		}
	}
	return nil
}

// Inicializar ejecuta el pipeline de arranque del coordinador. Corre en una
// goroutine aparte: el servidor HTTP ya responde /health mientras tanto.
func (a *App) Inicializar() {
	if err := a.inicializar(); err != nil {
		log.Printf("ERROR de arranque: %v", err)
		a.errArranq.Store(err.Error())
		a.setEstado(EstErrorArranque)
	}
}

func (a *App) inicializar() error {
	ctx := a.ctx
	go a.escritorPredicciones()
	_ = a.mongo.MarcarEntrenamientosHuerfanos(ctx)

	// 1. Clúster: esperar a los nodos y arrancar los latidos.
	a.setEstado(EstConectando)
	n, err := a.cluster.Esperar(ctx, len(a.cluster.Nodos), 60*time.Second)
	if n == 0 {
		return fmt.Errorf("ningún nodo del clúster responde: %w", err)
	}
	if err != nil {
		log.Printf("aviso: %v; se continúa con %d nodos", err, n)
	}
	for _, i := range a.cluster.Info() {
		log.Printf("  nodo %-8s %-16s conectado=%v rtt=%.2fms cpus=%d", i.ID, i.Addr, i.Conectado, i.RTTms, i.Estado.CPUs)
	}
	go a.cluster.Latidos(ctx, 2*time.Second, a.alLatido)

	// 2. Datos: carga concurrente + limpieza (Parte A de la PC3).
	a.setEstado(EstCargando)
	if err := a.cargarDatos(ctx); err != nil {
		return err
	}

	// 3. Modelo: reutiliza el activo si se entrenó con los mismos archivos.
	d := a.datos.Load()
	doc, err := a.mongo.ModeloActivo(ctx)
	switch {
	case err == nil && doc.Huella == d.huella && !a.cfg.ForzarEntrenamiento:
		log.Printf("modelo v%d encontrado en MongoDB (AUC %.4f): se reutiliza", doc.Version, doc.Metricas.AUC)
		a.activar(doc)
		if top, _ := a.mongo.ImpactoTop(ctx, doc.Version, "", 1); len(top) == 0 {
			if err := a.recalcularImpacto(ctx, doc); err != nil {
				return err
			}
		}
	case err != nil && !errors.Is(err, store.ErrNoEncontrado):
		return err
	default:
		a.setEstado(EstEntrenando)
		cfg := ml.ConfigPorDefecto()
		if a.cfg.Epocas > 0 {
			cfg.Epocas = a.cfg.Epocas
		}
		job, err := a.NuevoEntrenamiento(ctx, "sistema", cfg, 0)
		if err != nil {
			return err
		}
		a.EjecutarEntrenamiento(job)
		if job.Estado != store.EstadoCompletado {
			return fmt.Errorf("entrenamiento inicial: %s", job.Error)
		}
	}
	a.setEstado(EstListo)
	return nil
}

func huellaArchivos(paths ...string) string {
	var b strings.Builder
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d;", filepath.Base(p), st.Size(), st.ModTime().Unix())
	}
	return b.String()
}

func (a *App) cargarDatos(ctx context.Context) error {
	t0 := time.Now()
	w := runtime.NumCPU()
	recsP, stP, err := data.CargarConcurrente(a.cfg.ArchPositivos, data.Positivos, w)
	if err != nil {
		return fmt.Errorf("cargar positivos: %w", err)
	}
	recsF, stF, err := data.CargarConcurrente(a.cfg.ArchFallecidos, data.Fallecidos, w)
	if err != nil {
		return fmt.Errorf("cargar fallecidos: %w", err)
	}
	recs := append(recsP, recsF...)
	recsP, recsF = nil, nil
	e := data.CalcularEDA(recs)
	train, test := ml.DividirTrainTest(recs, 0.2, 2026)
	meses := e.MesesOrdenados()
	mesDesde := meses[0]
	if k := a.cfg.MesesImpacto; k > 0 && len(meses) > k {
		mesDesde = meses[len(meses)-k]
	}
	res := &store.Dataset{
		Cargado: time.Now(), Huella: huellaArchivos(a.cfg.ArchPositivos, a.cfg.ArchFallecidos),
		Archivos:  []string{filepath.Base(a.cfg.ArchPositivos), filepath.Base(a.cfg.ArchFallecidos)},
		Positivos: int(e.Total[0]), Fallecidos: int(e.Total[1]), Provincias: e.Provincias,
		NTrain: len(train), NTest: len(test), MesDesde: mesDesde, CargaS: time.Since(t0).Seconds(),
		LetalEdad: map[string]float64{},
	}
	if e.Total[0] > 0 {
		res.Letalidad = float64(e.Total[1]) / float64(e.Total[0])
	}
	for i, g := range data.GruposEdad {
		res.LetalEdad[g] = e.Letalidad[i]
	}
	for _, st := range []data.Stats{stP, stF} {
		l := store.LimpiezaArchivo{Fuente: st.Fuente, Leidas: st.Leidas, Validas: st.Validas,
			Descartes: map[string]int64{}, DuracionS: st.Duracion.Seconds(), FilasPorSeg: st.FilasPorSeg}
		for i, n := range st.Descartes {
			l.Descartes[data.DescarteNombres[i]] = n
		}
		res.Limpieza = append(res.Limpieza, l)
	}
	a.datos.Store(&dataset{recs: recs, train: train, test: test, mesDesde: mesDesde, huella: res.Huella, resumen: res})
	log.Printf("datos: %d registros (%d positivos, %d fallecidos) en %.2fs · train %d · test %d",
		len(recs), e.Total[0], e.Total[1], res.CargaS, len(train), len(test))
	return a.mongo.GuardarDataset(ctx, res)
}

// alLatido se ejecuta tras cada ronda de latidos: guarda el último estado de
// cada nodo en Redis (con TTL), una muestra en MongoDB cada 10 s y avisa a la SPA.
func (a *App) alLatido(infos []cluster.InfoNodo) {
	k := a.latidos.Add(1)
	ctx, cancel := context.WithTimeout(a.ctx, time.Second)
	defer cancel()
	var muestras []store.MetricaCluster
	for _, i := range infos {
		if i.Conectado && a.redis != nil {
			_ = a.redis.SetJSON(ctx, store.KeyNodo(i.ID), i, store.TTLLatido)
		}
		muestras = append(muestras, store.MetricaCluster{Ts: time.Now(), Nodo: i.ID, Conectado: i.Conectado, RTTms: i.RTTms,
			CPUPct: i.Estado.CPUPct, HeapMB: i.Estado.HeapMB, Registros: i.Estado.Registros, Pasos: i.Estado.Pasos})
	}
	if k%5 == 0 {
		_ = a.mongo.InsertarMetricasCluster(ctx, muestras)
	}
	a.emitir("cluster", infos)
}

// activar publica el modelo para las predicciones (swap atómico).
func (a *App) activar(doc *store.Modelo) {
	a.modelo.Store(&modeloActivo{Doc: doc, M: doc.AML()})
	if a.redis != nil {
		ctx, cancel := context.WithTimeout(a.ctx, 2*time.Second)
		defer cancel()
		_ = a.redis.SetJSON(ctx, store.KeyModeloActivo, doc, 0)
	}
}

// ---------------------------------------------------------------------------
// Entrenamiento distribuido
// ---------------------------------------------------------------------------

var ErrOcupado = errors.New("ya hay un entrenamiento en curso")

func nuevoID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// NuevoEntrenamiento reserva el clúster (solo un trabajo a la vez) y registra
// el trabajo en MongoDB.
func (a *App) NuevoEntrenamiento(ctx context.Context, solicitante string, cfg ml.Config, nodos int) (*store.Entrenamiento, error) {
	if a.datos.Load() == nil {
		return nil, errors.New("los datos aún no están cargados")
	}
	if !a.entrenando.CompareAndSwap(false, true) {
		return nil, ErrOcupado
	}
	if nodos <= 0 || nodos > len(a.cluster.Activos()) {
		nodos = len(a.cluster.Activos())
	}
	cfg.Workers = nodos
	job := &store.Entrenamiento{ID: nuevoID(), Estado: store.EstadoEnCola, Solicitante: solicitante, Config: cfg,
		NodosSolicitados: nodos, Creado: time.Now(), Historial: []store.Epoca{}}
	if err := a.mongo.GuardarEntrenamiento(ctx, job); err != nil {
		a.entrenando.Store(false)
		return nil, err
	}
	a.trabajoAct.Store(job.ID)
	return job, nil
}

func (a *App) guardarJob(job *store.Entrenamiento) {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	if err := a.mongo.GuardarEntrenamiento(ctx, job); err != nil {
		log.Printf("guardar entrenamiento %s: %v", job.ID, err)
	}
	a.emitir("entrenamiento", job)
}

// EjecutarEntrenamiento corre el trabajo completo: distribuir → entrenar por
// TCP → evaluar → impacto distribuido → persistir y activar.
func (a *App) EjecutarEntrenamiento(job *store.Entrenamiento) {
	defer a.entrenando.Store(false)
	defer a.trabajoAct.Store("")
	d := a.datos.Load()
	ctx := a.ctx
	t0 := time.Now()
	job.Inicio = &t0
	job.Estado = store.EstadoDistribuyendo
	a.guardarJob(job)
	log.Printf("entrenamiento %s: %d nodos, %d épocas, lote global %d", job.ID, job.NodosSolicitados, job.Config.Epocas, job.Config.LoteGlobal)

	fallar := func(err error) {
		fin := time.Now()
		job.Estado, job.Error, job.Fin, job.DuracionS = store.EstadoError, err.Error(), &fin, time.Since(t0).Seconds()
		log.Printf("entrenamiento %s falló: %v", job.ID, err)
		a.guardarJob(job)
	}

	res, info, err := a.cluster.Entrenar(ctx, job.ID, d.recs, d.train, job.Config, job.NodosSolicitados, func(p ml.Progreso) {
		job.Estado = store.EstadoEntrenando
		job.Historial = append(job.Historial, store.Epoca{Epoca: p.Epoca, Perdida: p.Perdida, DuracionS: p.Duracion.Seconds()})
		log.Printf("  época %d/%d  pérdida %.5f  (%.2fs)", p.Epoca, p.Epocas, p.Perdida, p.Duracion.Seconds())
		a.guardarJob(job)
	})
	job.Distribucion = info
	if err != nil {
		fallar(err)
		return
	}

	job.Estado = store.EstadoEvaluando
	a.guardarJob(job)
	met := ml.Evaluar(res.Modelo, d.recs, d.test)
	ver, err := a.mongo.SiguienteVersion(ctx)
	if err != nil {
		fallar(err)
		return
	}
	pesos := map[string]float64{}
	for i, n := range ml.NombresFeat() {
		pesos[n] = res.Modelo.W[i]
	}
	doc := &store.Modelo{
		Version: ver, Creado: time.Now(), Algoritmo: "Regresión logística distribuida (Parameter Server síncrono sobre TCP)",
		TrabajoID: job.ID, Config: job.Config, W: res.Modelo.W, B: res.Modelo.B,
		Norm:  ml.Normalizacion{MediaEdad: res.Modelo.MediaEdad, DesvEdad: res.Modelo.DesvEdad, PesoPos: res.Modelo.PesoPos},
		Pesos: pesos, Metricas: met, Historial: store.HistDesdeML(res.Historial), Distribucion: info,
		DuracionS: res.Duracion.Seconds(), NTrain: len(d.train), NTest: len(d.test), Huella: d.huella,
	}
	if err := a.mongo.GuardarModeloActivo(ctx, doc); err != nil {
		fallar(err)
		return
	}
	if err := a.recalcularImpacto(ctx, doc); err != nil {
		fallar(err)
		return
	}
	a.activar(doc)
	if a.redis != nil {
		_, _ = a.redis.BorrarPatron(ctx, "pred:*")
		_, _ = a.redis.BorrarPatron(ctx, "impacto:*")
	}
	fin := time.Now()
	job.Estado, job.ModeloVersion, job.Metricas, job.Fin, job.DuracionS = store.EstadoCompletado, ver, &met, &fin, time.Since(t0).Seconds()
	log.Printf("entrenamiento %s completado: modelo v%d · AUC %.4f · %.2fs (distribución %.2fs)",
		job.ID, ver, met.AUC, job.DuracionS, info.DistribucionS)
	a.guardarJob(job)
}

// recalcularImpacto ejecuta el map-reduce de impacto en el clúster y guarda
// el ranking en MongoDB.
func (a *App) recalcularImpacto(ctx context.Context, doc *store.Modelo) error {
	d := a.datos.Load()
	t0 := time.Now()
	riesgo, excl, err := a.cluster.Impacto(ctx, doc.AML(), d.recs, d.mesDesde)
	if err != nil {
		return fmt.Errorf("impacto distribuido: %w", err)
	}
	filas := make([]store.Impacto, len(riesgo))
	for i, r := range riesgo {
		filas[i] = store.Impacto{ID: fmt.Sprintf("v%d|%s|%s", doc.Version, r.Departamento, r.Provincia),
			ModeloVersion: doc.Version, Ranking: i + 1, Departamento: r.Departamento, Provincia: r.Provincia,
			Positivos: r.Positivos, GravesEsperados: r.GravesEsperados, TasaPor1000: r.TasaEsperada,
			PctMayores60: r.Edad60Mas, MesDesde: d.mesDesde}
	}
	log.Printf("impacto: %d provincias puntuadas en el clúster en %.0f ms (%d casos sin provincia excluidos)",
		len(filas), float64(time.Since(t0).Milliseconds()), excl)
	return a.mongo.ReemplazarImpacto(ctx, doc.Version, filas)
}

// ---------------------------------------------------------------------------
// Predicción
// ---------------------------------------------------------------------------

// Factor es el aporte de una variable al logit.
type Factor struct {
	Variable string  `json:"variable"`
	Valor    string  `json:"valor"`
	Aporte   float64 `json:"aporte_logit"`
}

// RespuestaPrediccion es el cuerpo de POST /api/predicciones.
type RespuestaPrediccion struct {
	Entrada          store.EntradaPrediccion `json:"entrada"`
	ProbabilidadClas float64                 `json:"probabilidad_clasificador"`
	Clasificacion    string                  `json:"clasificacion"`
	Letalidad        float64                 `json:"letalidad_estimada"`
	LetalidadPct     string                  `json:"letalidad_estimada_pct"`
	Nivel            string                  `json:"nivel_riesgo"`
	Periodo          string                  `json:"periodo_epidemiologico"`
	Factores         []Factor                `json:"factores"`
	Recomendacion    string                  `json:"recomendacion"`
	ModeloVersion    int                     `json:"modelo_version"`
	Cache            bool                    `json:"cache"`
	LatenciaMs       float64                 `json:"latencia_ms"`
}

// ErrValidacion envuelve errores de entrada (HTTP 400).
type ErrValidacion struct{ Msg string }

func (e ErrValidacion) Error() string { return e.Msg }

var ErrSinModelo = errors.New("el modelo aún no está disponible; el coordinador está en fase de arranque")

// aRecord valida la entrada y la convierte al registro que entiende el modelo.
func aRecord(e *store.EntradaPrediccion) (data.Record, error) {
	var r data.Record
	if e.Edad < 0 || e.Edad > 110 {
		return r, ErrValidacion{"edad debe estar entre 0 y 110"}
	}
	switch strings.ToUpper(strings.TrimSpace(e.Sexo)) {
	case "M", "MASCULINO", "HOMBRE":
		r.Sexo, e.Sexo = 1, "M"
	case "F", "FEMENINO", "MUJER":
		r.Sexo, e.Sexo = 0, "F"
	default:
		return r, ErrValidacion{"sexo debe ser M o F"}
	}
	d := data.DeptIdx(e.Departamento)
	if d < 0 {
		return r, ErrValidacion{"departamento desconocido: " + e.Departamento}
	}
	e.Departamento = data.Departamentos[d]
	r.Dept = d
	r.Edad = uint8(e.Edad)
	r.Ola = data.PostOlas
	if e.Fecha != "" {
		t, err := time.Parse("2006-01-02", e.Fecha)
		if err != nil {
			return r, ErrValidacion{"fecha debe tener formato AAAA-MM-DD"}
		}
		r.YYYYMM = int32(t.Year()*100 + int(t.Month()))
		r.Ola = data.OlaDe(r.YYYYMM)
	}
	e.Provincia = strings.ToUpper(strings.TrimSpace(e.Provincia))
	return r, nil
}

func nivelDe(let float64) (string, string) {
	switch {
	case let < 0.01:
		return "BAJO", "Seguimiento ambulatorio estándar."
	case let < 0.05:
		return "MODERADO", "Monitoreo de saturación de oxígeno y control a las 48 horas."
	case let < 0.15:
		return "ALTO", "Priorizar evaluación presencial y prever disponibilidad de oxígeno."
	default:
		return "MUY ALTO", "Prioridad máxima: reservar cama hospitalaria/UCI y oxígeno medicinal."
	}
}

// calcular aplica el modelo (microsegundos: 5 multiplicaciones por la
// codificación dispersa).
func calcular(ma *modeloActivo, e store.EntradaPrediccion, r *data.Record) *RespuestaPrediccion {
	p := ma.M.Prob(r)
	let := math.Min(ma.M.LetalidadEsperada(r), 1)
	nivel, rec := nivelDe(let)
	ap := ma.M.Explicar(r)
	clas := "BAJO RIESGO"
	if p >= 0.5 {
		clas = "ALTO RIESGO"
	}
	sexo := "F"
	if r.Sexo == 1 {
		sexo = "M"
	}
	return &RespuestaPrediccion{
		Entrada: e, ProbabilidadClas: p, Clasificacion: clas, Letalidad: let,
		LetalidadPct: fmt.Sprintf("%.2f%%", 100*let), Nivel: nivel, Periodo: data.OlaNombres[r.Ola],
		Recomendacion: rec, ModeloVersion: ma.Doc.Version,
		Factores: []Factor{
			{"edad", fmt.Sprint(r.Edad), ap.Edad},
			{"sexo", sexo, ap.Sexo},
			{"departamento", data.Departamentos[r.Dept], ap.Departamento},
			{"periodo", data.OlaNombres[r.Ola], ap.Periodo},
		},
	}
}

// Predecir responde una consulta individual. Camino crítico (< 100 ms):
//  1. modelo: lectura atómica, sin locks;
//  2. Redis GET de la respuesta precalculada (≈0.2 ms);
//  3. si no está, se calcula (µs) y se guarda en Redis con TTL;
//  4. la auditoría en MongoDB se encola en un canal y la escribe en lotes
//     otra goroutine: la petición nunca espera a MongoDB.
func (a *App) Predecir(ctx context.Context, e store.EntradaPrediccion, usuario string) (*RespuestaPrediccion, error) {
	t0 := time.Now()
	ma := a.modelo.Load()
	if ma == nil {
		return nil, ErrSinModelo
	}
	r, err := aRecord(&e)
	if err != nil {
		return nil, err
	}
	key := store.KeyPrediccion(ma.Doc.Version, int(r.Edad), int(r.Sexo), int(r.Dept), int(r.Ola))
	var resp *RespuestaPrediccion
	if a.redis != nil {
		var c RespuestaPrediccion
		cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		if err := a.redis.GetJSON(cctx, key, &c); err == nil {
			c.Entrada, c.Cache = e, true
			resp = &c
			a.cacheHits.Add(1)
		}
		cancel()
	}
	if resp == nil {
		resp = calcular(ma, e, &r)
		if a.redis != nil {
			cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
			_ = a.redis.SetJSON(cctx, key, resp, store.TTLPrediccion)
			cancel()
		}
	}
	resp.LatenciaMs = ms(time.Since(t0))
	a.predTotal.Add(1)
	a.registrar(store.Prediccion{Ts: time.Now(), Usuario: usuario, Entrada: e, Probabilidad: resp.ProbabilidadClas,
		Letalidad: resp.Letalidad, Nivel: resp.Nivel, ModeloVersion: resp.ModeloVersion,
		LatenciaUs: time.Since(t0).Microseconds(), Cache: resp.Cache})
	return resp, nil
}

// PredecirLote puntúa varios casos (sin caché: cada caso cuesta microsegundos).
func (a *App) PredecirLote(entradas []store.EntradaPrediccion, usuario string) ([]*RespuestaPrediccion, []string, error) {
	ma := a.modelo.Load()
	if ma == nil {
		return nil, nil, ErrSinModelo
	}
	out := make([]*RespuestaPrediccion, len(entradas))
	errs := make([]string, len(entradas))
	for i := range entradas {
		r, err := aRecord(&entradas[i])
		if err != nil {
			errs[i] = err.Error()
			continue
		}
		out[i] = calcular(ma, entradas[i], &r)
		a.registrar(store.Prediccion{Ts: time.Now(), Usuario: usuario, Entrada: entradas[i], Probabilidad: out[i].ProbabilidadClas,
			Letalidad: out[i].Letalidad, Nivel: out[i].Nivel, ModeloVersion: ma.Doc.Version})
	}
	a.predTotal.Add(int64(len(entradas)))
	return out, errs, nil
}

func (a *App) registrar(p store.Prediccion) {
	select {
	case a.logPred <- p:
	default:
		a.descartadas.Add(1) // canal lleno: se prioriza la latencia sobre la auditoría
	}
}

// escritorPredicciones vacía el canal en lotes (hasta 500 documentos o cada
// 500 ms) con InsertMany y actualiza el contador global en Redis.
func (a *App) escritorPredicciones() {
	lote := make([]store.Prediccion, 0, 500)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	volcar := func() {
		if len(lote) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := a.mongo.InsertarPredicciones(ctx, lote); err != nil {
			log.Printf("guardar predicciones: %v", err)
		}
		if a.redis != nil {
			a.redis.C.IncrBy(ctx, store.KeyStatsPred, int64(len(lote)))
		}
		cancel()
		lote = lote[:0]
	}
	for {
		select {
		case <-a.ctx.Done():
			volcar()
			return
		case p := <-a.logPred:
			lote = append(lote, p)
			if len(lote) == cap(lote) {
				volcar()
			}
		case <-t.C:
			volcar()
		}
	}
}
