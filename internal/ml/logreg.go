// Package ml implementa la Parte B del TF: regresión logística entrenada con
// descenso de gradiente por mini-lotes en paralelo (data parallelism) con un
// coordinador tipo Parameter Server.
//
// En la PC3 los "nodos" son goroutines que se comunican por canales. La
// interfaz entre coordinador y workers (mensajes Pesos / Gradiente) es la
// misma que en la PC4 viajará serializada por TCP entre contenedores.
package ml

import (
	"math"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/data"
)

// Índices del vector de características (codificación dispersa):
//
//	0      edad estandarizada (z)
//	1      z² (captura el crecimiento no lineal del riesgo con la edad)
//	2      sexo masculino (0/1)
//	3..27  one-hot de departamento (25)
//	28..31 one-hot de periodo epidemiológico (4 olas)
const (
	fEdad   = 0
	fEdad2  = 1
	fSexo   = 2
	fDept   = 3
	fOla    = fDept + 25
	NumFeat = fOla + data.NumOlas
)

// NombresFeat sirve para reportar los pesos aprendidos.
func NombresFeat() []string {
	n := []string{"edad_z", "edad_z^2", "sexo_masculino"}
	for _, d := range data.Departamentos {
		n = append(n, "dept_"+d)
	}
	return append(n, data.OlaNombres...)
}

// Modelo es el estado completo de la regresión logística.
type Modelo struct {
	W         []float64
	B         float64
	MediaEdad float64
	DesvEdad  float64
	PesoPos   float64 // peso de la clase positiva usado al entrenar
}

func NuevoModelo(mediaEdad, desvEdad, pesoPos float64) *Modelo {
	return &Modelo{W: make([]float64, NumFeat), MediaEdad: mediaEdad, DesvEdad: desvEdad, PesoPos: pesoPos}
}

func sigmoide(z float64) float64 {
	if z >= 0 {
		return 1 / (1 + math.Exp(-z))
	}
	ez := math.Exp(z)
	return ez / (1 + ez)
}

// Logit calcula z = wᵀx + b aprovechando que x es disperso (solo 5 entradas
// distintas de cero por registro).
func Logit(w []float64, b, media, desv float64, r *data.Record) float64 {
	ez := (float64(r.Edad) - media) / desv
	return b + w[fEdad]*ez + w[fEdad2]*ez*ez + w[fSexo]*float64(r.Sexo) +
		w[fDept+int(r.Dept)] + w[fOla+int(r.Ola)]
}

// Prob devuelve ŷ = σ(z) según el modelo entrenado (con el sesgo de la
// ponderación de clases incluido: útil para clasificar con umbral 0.5).
func (m *Modelo) Prob(r *data.Record) float64 {
	return sigmoide(Logit(m.W, m.B, m.MediaEdad, m.DesvEdad, r))
}

// LetalidadEsperada corrige el intercepto por la ponderación de clases y
// devuelve las odds p/(1-p). Como los controles son *todos* los casos
// positivos, estas odds estiman directamente la letalidad (fallecidos /
// positivos) de un caso con ese perfil (diseño "caso-base").
func (m *Modelo) LetalidadEsperada(r *data.Record) float64 {
	z := Logit(m.W, m.B, m.MediaEdad, m.DesvEdad, r) - math.Log(m.PesoPos)
	return math.Exp(z)
}

// gradienteLote acumula ∂L/∂w y ∂L/∂b sobre recs[idx] para la entropía
// cruzada ponderada:
//
//	L = -Σ ωᵢ [ yᵢ log ŷᵢ + (1-yᵢ) log(1-ŷᵢ) ]
//	∂L/∂w = Σ ωᵢ (ŷᵢ - yᵢ) xᵢ      ∂L/∂b = Σ ωᵢ (ŷᵢ - yᵢ)
//
// Devuelve además la pérdida y el peso total del lote (para promediar).
func gradienteLote(recs []data.Record, idx []int32, w []float64, b float64,
	media, desv, pesoPos float64, gw []float64) (gb, perdida, pesoTotal float64) {

	for i := range gw {
		gw[i] = 0
	}
	const eps = 1e-12
	for _, k := range idx {
		r := &recs[k]
		ez := (float64(r.Edad) - media) / desv
		z := b + w[fEdad]*ez + w[fEdad2]*ez*ez + w[fSexo]*float64(r.Sexo) +
			w[fDept+int(r.Dept)] + w[fOla+int(r.Ola)]
		p := sigmoide(z)
		y := float64(r.Label)
		om := 1.0
		if r.Label == 1 {
			om = pesoPos
		}
		e := om * (p - y)
		gw[fEdad] += e * ez
		gw[fEdad2] += e * ez * ez
		gw[fSexo] += e * float64(r.Sexo)
		gw[fDept+int(r.Dept)] += e
		gw[fOla+int(r.Ola)] += e
		gb += e
		perdida -= om * (y*math.Log(p+eps) + (1-y)*math.Log(1-p+eps))
		pesoTotal += om
	}
	return
}
