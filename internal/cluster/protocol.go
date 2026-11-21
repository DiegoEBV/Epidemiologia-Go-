// Package cluster implementa la distribución del entrenamiento (PC4): un
// coordinador (la API) y N nodos de cómputo que se comunican por TCP.
//
// Topología en estrella: cada nodo mantiene UNA conexión TCP persistente con
// el coordinador y los nodos nunca hablan entre sí. Sobre esa conexión viajan
// mensajes encoding/gob en modo petición-respuesta (el coordinador pregunta,
// el nodo responde), lo que da un orden total por conexión sin necesidad de
// identificadores de correlación.
package cluster

import (
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/data"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/ml"
)

// Tipo identifica el mensaje dentro del sobre.
type Tipo uint8

const (
	TPing      Tipo = iota + 1 // coordinador → nodo: latido; responde TPong con métricas
	TPong                      // nodo → coordinador
	TShard                     // coordinador → nodo: fragmento de entrenamiento
	TShardOK                   // nodo → coordinador: fragmento cargado
	TPesos                     // coordinador → nodo: pesos del paso actual
	TGradiente                 // nodo → coordinador: gradiente del mini-lote local
	TImpacto                   // coordinador → nodo: modelo + casos recientes a puntuar
	TImpactoOK                 // nodo → coordinador: sumas parciales por provincia
	TError                     // nodo → coordinador: la petición falló
)

func (t Tipo) String() string {
	switch t {
	case TPing:
		return "PING"
	case TPong:
		return "PONG"
	case TShard:
		return "SHARD"
	case TShardOK:
		return "SHARD_OK"
	case TPesos:
		return "PESOS"
	case TGradiente:
		return "GRADIENTE"
	case TImpacto:
		return "IMPACTO"
	case TImpactoOK:
		return "IMPACTO_OK"
	case TError:
		return "ERROR"
	}
	return "?"
}

// Sobre es la única estructura que viaja por el socket. Solo uno de los
// punteros va lleno según Tipo (gob no transmite los campos nil).
type Sobre struct {
	Tipo      Tipo
	Shard     *MsgShard
	ShardOK   *MsgShardOK
	Pesos     *ml.MsgPesos
	Gradiente *ml.MsgGradiente
	Impacto   *MsgImpacto
	ImpactoOK *MsgImpactoOK
	Estado    *EstadoNodo
	Error     string
}

// Columnas es un bloque de registros en formato columnar. Así gob codifica
// cada columna []uint8/[]int8 como un único arreglo de bytes: ~5 bytes por
// registro en el cable (3.8M registros de entrenamiento ≈ 19 MB en total).
type Columnas struct {
	Edad  []uint8
	Sexo  []int8
	Dept  []int8
	Ola   []int8
	Label []uint8
	Prov  []uint16 // índice en el diccionario de provincias (solo para impacto)
}

// Len devuelve el número de registros.
func (c *Columnas) Len() int { return len(c.Edad) }

// AColumnas toma recs[idx] y los convierte a columnas. Si provID != nil
// también codifica la provincia con el índice que devuelve.
func AColumnas(recs []data.Record, idx []int32, provID func(*data.Record) uint16) Columnas {
	n := len(idx)
	c := Columnas{Edad: make([]uint8, n), Sexo: make([]int8, n), Dept: make([]int8, n),
		Ola: make([]int8, n), Label: make([]uint8, n)}
	if provID != nil {
		c.Prov = make([]uint16, n)
	}
	for i, k := range idx {
		r := &recs[k]
		c.Edad[i], c.Sexo[i], c.Dept[i], c.Ola[i], c.Label[i] = r.Edad, r.Sexo, r.Dept, r.Ola, r.Label
		if provID != nil {
			c.Prov[i] = provID(r)
		}
	}
	return c
}

// ARecords reconstruye los registros en el nodo (sin provincia).
func (c *Columnas) ARecords() []data.Record {
	recs := make([]data.Record, c.Len())
	for i := range recs {
		recs[i] = data.Record{Edad: c.Edad[i], Sexo: c.Sexo[i], Dept: c.Dept[i], Ola: c.Ola[i], Label: c.Label[i]}
	}
	return recs
}

// MsgShard asigna a un nodo su parte del set de entrenamiento para un trabajo.
type MsgShard struct {
	Trabajo   string
	Worker    int // posición del nodo en el trabajo (define su semilla de barajado)
	LoteLocal int
	Norm      ml.Normalizacion
	Datos     Columnas
}

type MsgShardOK struct {
	Registros int
	CargaMs   float64
}

// MsgImpacto pide al nodo puntuar un bloque de casos positivos recientes con
// el modelo entregado y sumar la letalidad esperada por provincia (map).
type MsgImpacto struct {
	W        []float64
	B        float64
	Norm     ml.Normalizacion
	Datos    Columnas
	NumProvs int
}

// MsgImpactoOK devuelve las sumas parciales por índice de provincia (reduce
// en el coordinador).
type MsgImpactoOK struct {
	Positivos []int32
	Graves    []float64
	Mayores60 []int32
	Ms        float64
}

// EstadoNodo es lo que el nodo informa en cada latido.
type EstadoNodo struct {
	ID         string
	Host       string
	CPUs       int
	Goroutines int
	HeapMB     float64
	CPUPct     float64 // uso de CPU del proceso desde el latido anterior
	Registros  int     // tamaño del fragmento cargado
	Trabajo    string
	Pasos      int64 // gradientes calculados desde el arranque
	Tareas     int64 // mensajes atendidos desde el arranque
	UptimeS    float64
}
