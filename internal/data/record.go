// Package data implementa la Parte A del TF: el cargador concurrente y la
// limpieza de los datasets abiertos del MINSA (casos positivos y fallecidos
// por COVID-19).
package data

import "strings"

// Departamentos es la lista fija de los 25 departamentos (incluye Callao)
// tal como aparecen en la columna DEPARTAMENTO de los CSV del MINSA.
// El índice en esta lista es el que usa la codificación one-hot del modelo.
var Departamentos = []string{
	"AMAZONAS", "ANCASH", "APURIMAC", "AREQUIPA", "AYACUCHO", "CAJAMARCA",
	"CALLAO", "CUSCO", "HUANCAVELICA", "HUANUCO", "ICA", "JUNIN",
	"LA LIBERTAD", "LAMBAYEQUE", "LIMA", "LORETO", "MADRE DE DIOS",
	"MOQUEGUA", "PASCO", "PIURA", "PUNO", "SAN MARTIN", "TACNA", "TUMBES",
	"UCAYALI",
}

var deptIndex = func() map[string]int8 {
	m := make(map[string]int8, len(Departamentos)+4)
	for i, d := range Departamentos {
		m[d] = int8(i)
	}
	// Variantes que aparecen en algunos cortes del dataset.
	m["LIMA REGION"] = m["LIMA"]
	m["LIMA METROPOLITANA"] = m["LIMA"]
	m["ÁNCASH"] = m["ANCASH"]
	m["APURÍMAC"] = m["APURIMAC"]
	m["HUÁNUCO"] = m["HUANUCO"]
	m["JUNÍN"] = m["JUNIN"]
	m["SAN MARTÍN"] = m["SAN MARTIN"]
	return m
}()

// DeptIdx devuelve el índice del departamento o -1 si no es válido.
func DeptIdx(s string) int8 {
	if i, ok := deptIndex[strings.ToUpper(strings.TrimSpace(s))]; ok {
		return i
	}
	return -1
}

// Periodos epidemiológicos (olas) usados como variable categórica.
const (
	Ola1     = iota // mar-2020 a nov-2020
	Ola2            // dic-2020 a oct-2021
	Ola3            // nov-2021 a abr-2022 (Ómicron)
	PostOlas        // may-2022 en adelante
	NumOlas
)

var OlaNombres = []string{"Ola 1 (2020)", "Ola 2 (dic20-oct21)", "Ola 3 Ómicron (nov21-abr22)", "Post-olas (may22+)"}

// OlaDe clasifica una fecha AAAAMM en su periodo epidemiológico.
func OlaDe(yyyymm int32) int8 {
	switch {
	case yyyymm <= 202011:
		return Ola1
	case yyyymm <= 202110:
		return Ola2
	case yyyymm <= 202204:
		return Ola3
	default:
		return PostOlas
	}
}

// Record es un caso ya limpio y listo para entrenar. Es compacto a propósito:
// con >2.5M filas, cada byte por registro cuenta.
type Record struct {
	Edad      uint8  // 0..110
	Sexo      int8   // 0 = femenino, 1 = masculino
	Dept      int8   // índice en Departamentos
	Ola       int8   // periodo epidemiológico
	Label     uint8  // 1 = fallecido (severidad máxima), 0 = caso positivo
	YYYYMM    int32  // mes de resultado / fallecimiento
	Provincia string // para el análisis de impacto por provincia
}

// Fuente identifica de qué archivo viene una fila.
type Fuente int

const (
	Positivos Fuente = iota
	Fallecidos
)

func (f Fuente) String() string {
	if f == Fallecidos {
		return "fallecidos"
	}
	return "positivos"
}
