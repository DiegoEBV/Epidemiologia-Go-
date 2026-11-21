// Package store implementa los dos niveles de almacenamiento de la PC4:
//
//   - MongoDB (persistente): usuarios, versiones del modelo, entrenamientos,
//     predicciones, ranking de impacto por provincia, resumen del dataset y
//     métricas del clúster.
//   - Redis (caché / colaborativo): modelo activo, predicciones ya calculadas,
//     ranking de impacto precalculado y último estado de cada nodo.
package store

import (
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/cluster"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/ml"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Nombres de las colecciones.
const (
	ColUsuarios        = "usuarios"
	ColModelos         = "modelos"
	ColEntrenamientos  = "entrenamientos"
	ColPredicciones    = "predicciones"
	ColImpacto         = "impacto_provincias"
	ColDataset         = "dataset_resumen"
	ColMetricasCluster = "metricas_cluster"
)

// Usuario: cuenta que puede autenticarse en la API (JWT).
type Usuario struct {
	ID       bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Username string        `bson:"username" json:"username"`
	PassHash string        `bson:"pass_hash" json:"-"` // PBKDF2-SHA256
	Salt     string        `bson:"salt" json:"-"`
	Iter     int           `bson:"iter" json:"-"`  // iteraciones de PBKDF2
	Rol      string        `bson:"rol" json:"rol"` // admin | analista
	Creado   time.Time     `bson:"creado" json:"creado"`
}

// Epoca es una fila del historial de entrenamiento.
type Epoca struct {
	Epoca     int     `bson:"epoca" json:"epoca"`
	Perdida   float64 `bson:"perdida" json:"perdida"`
	DuracionS float64 `bson:"duracion_s" json:"duracion_s"`
}

// HistDesdeML convierte el historial del paquete ml.
func HistDesdeML(h []ml.HistEpoca) []Epoca {
	out := make([]Epoca, len(h))
	for i, e := range h {
		out[i] = Epoca{Epoca: e.Epoca, Perdida: e.Perdida, DuracionS: e.Duracion.Seconds()}
	}
	return out
}

// Modelo: una versión entrenada de la regresión logística.
type Modelo struct {
	Version      int                        `bson:"_id" json:"version"`
	Creado       time.Time                  `bson:"creado" json:"creado"`
	Algoritmo    string                     `bson:"algoritmo" json:"algoritmo"`
	Activo       bool                       `bson:"activo" json:"activo"`
	TrabajoID    string                     `bson:"trabajo_id" json:"trabajo_id"`
	Config       ml.Config                  `bson:"config" json:"config"`
	W            []float64                  `bson:"w" json:"w"`
	B            float64                    `bson:"b" json:"b"`
	Norm         ml.Normalizacion           `bson:"normalizacion" json:"normalizacion"`
	Pesos        map[string]float64         `bson:"pesos" json:"pesos"`
	Metricas     ml.Metricas                `bson:"metricas" json:"metricas"`
	Historial    []Epoca                    `bson:"historial" json:"historial"`
	Distribucion *cluster.InfoEntrenamiento `bson:"distribucion,omitempty" json:"distribucion,omitempty"`
	DuracionS    float64                    `bson:"duracion_s" json:"duracion_s"`
	NTrain       int                        `bson:"n_train" json:"n_train"`
	NTest        int                        `bson:"n_test" json:"n_test"`
	Huella       string                     `bson:"huella_datos" json:"huella_datos"`
}

// AML reconstruye el modelo listo para predecir.
func (m *Modelo) AML() *ml.Modelo {
	return &ml.Modelo{W: m.W, B: m.B, MediaEdad: m.Norm.MediaEdad, DesvEdad: m.Norm.DesvEdad, PesoPos: m.Norm.PesoPos}
}

// Estados de un entrenamiento.
const (
	EstadoEnCola        = "en_cola"
	EstadoDistribuyendo = "distribuyendo"
	EstadoEntrenando    = "entrenando"
	EstadoEvaluando     = "evaluando"
	EstadoCompletado    = "completado"
	EstadoError         = "error"
)

// Entrenamiento: un trabajo de entrenamiento distribuido.
type Entrenamiento struct {
	ID               string                     `bson:"_id" json:"id"`
	Estado           string                     `bson:"estado" json:"estado"`
	Solicitante      string                     `bson:"solicitante" json:"solicitante"`
	Config           ml.Config                  `bson:"config" json:"config"`
	NodosSolicitados int                        `bson:"nodos_solicitados" json:"nodos_solicitados"`
	Creado           time.Time                  `bson:"creado" json:"creado"`
	Inicio           *time.Time                 `bson:"inicio,omitempty" json:"inicio,omitempty"`
	Fin              *time.Time                 `bson:"fin,omitempty" json:"fin,omitempty"`
	Historial        []Epoca                    `bson:"historial" json:"historial"`
	Distribucion     *cluster.InfoEntrenamiento `bson:"distribucion,omitempty" json:"distribucion,omitempty"`
	ModeloVersion    int                        `bson:"modelo_version,omitempty" json:"modelo_version,omitempty"`
	Metricas         *ml.Metricas               `bson:"metricas,omitempty" json:"metricas,omitempty"`
	DuracionS        float64                    `bson:"duracion_s" json:"duracion_s"`
	Error            string                     `bson:"error,omitempty" json:"error,omitempty"`
}

// EntradaPrediccion es lo que envía el cliente.
type EntradaPrediccion struct {
	Edad         int    `bson:"edad" json:"edad"`
	Sexo         string `bson:"sexo" json:"sexo"`
	Departamento string `bson:"departamento" json:"departamento"`
	Provincia    string `bson:"provincia,omitempty" json:"provincia,omitempty"`
	Fecha        string `bson:"fecha,omitempty" json:"fecha,omitempty"` // AAAA-MM-DD
}

// Prediccion: registro de auditoría de cada consulta al modelo.
type Prediccion struct {
	ID            bson.ObjectID     `bson:"_id,omitempty" json:"id"`
	Ts            time.Time         `bson:"ts" json:"ts"`
	Usuario       string            `bson:"usuario" json:"usuario"`
	Entrada       EntradaPrediccion `bson:"entrada" json:"entrada"`
	Probabilidad  float64           `bson:"probabilidad" json:"probabilidad"`
	Letalidad     float64           `bson:"letalidad" json:"letalidad"`
	Nivel         string            `bson:"nivel" json:"nivel"`
	ModeloVersion int               `bson:"modelo_version" json:"modelo_version"`
	LatenciaUs    int64             `bson:"latencia_us" json:"latencia_us"`
	Cache         bool              `bson:"cache" json:"cache"`
}

// Impacto: casos graves esperados en una provincia según una versión del modelo.
type Impacto struct {
	ID              string  `bson:"_id" json:"-"`
	ModeloVersion   int     `bson:"modelo_version" json:"modelo_version"`
	Ranking         int     `bson:"ranking" json:"ranking"`
	Departamento    string  `bson:"departamento" json:"departamento"`
	Provincia       string  `bson:"provincia" json:"provincia"`
	Positivos       int     `bson:"positivos" json:"positivos"`
	GravesEsperados float64 `bson:"graves_esperados" json:"graves_esperados"`
	TasaPor1000     float64 `bson:"tasa_por_1000" json:"tasa_por_1000"`
	PctMayores60    float64 `bson:"pct_mayores_60" json:"pct_mayores_60"`
	MesDesde        int32   `bson:"mes_desde" json:"mes_desde"`
}

// LimpiezaArchivo resume la limpieza de un CSV.
type LimpiezaArchivo struct {
	Fuente      string           `bson:"fuente" json:"fuente"`
	Leidas      int64            `bson:"leidas" json:"leidas"`
	Validas     int64            `bson:"validas" json:"validas"`
	Descartes   map[string]int64 `bson:"descartes" json:"descartes"`
	DuracionS   float64          `bson:"duracion_s" json:"duracion_s"`
	FilasPorSeg float64          `bson:"filas_por_seg" json:"filas_por_seg"`
}

// Dataset: resumen de la última carga de los CSV del MINSA.
type Dataset struct {
	ID         string             `bson:"_id" json:"-"`
	Cargado    time.Time          `bson:"cargado" json:"cargado"`
	Huella     string             `bson:"huella" json:"huella"`
	Archivos   []string           `bson:"archivos" json:"archivos"`
	Limpieza   []LimpiezaArchivo  `bson:"limpieza" json:"limpieza"`
	Positivos  int                `bson:"positivos" json:"positivos"`
	Fallecidos int                `bson:"fallecidos" json:"fallecidos"`
	Letalidad  float64            `bson:"letalidad_global" json:"letalidad_global"`
	Provincias int                `bson:"provincias" json:"provincias"`
	NTrain     int                `bson:"n_train" json:"n_train"`
	NTest      int                `bson:"n_test" json:"n_test"`
	MesDesde   int32              `bson:"impacto_mes_desde" json:"impacto_mes_desde"`
	LetalEdad  map[string]float64 `bson:"letalidad_por_edad" json:"letalidad_por_edad"`
	CargaS     float64            `bson:"carga_s" json:"carga_s"`
}

// MetricaCluster: muestra periódica del estado de un nodo (serie temporal con TTL).
type MetricaCluster struct {
	Ts        time.Time `bson:"ts" json:"ts"`
	Nodo      string    `bson:"nodo" json:"nodo"`
	Conectado bool      `bson:"conectado" json:"conectado"`
	RTTms     float64   `bson:"rtt_ms" json:"rtt_ms"`
	CPUPct    float64   `bson:"cpu_pct" json:"cpu_pct"`
	HeapMB    float64   `bson:"heap_mb" json:"heap_mb"`
	Registros int       `bson:"registros" json:"registros"`
	Pasos     int64     `bson:"pasos" json:"pasos"`
}
