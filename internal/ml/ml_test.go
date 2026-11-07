package ml

import (
	"math"
	"math/rand"
	"testing"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/data"
)

// datosSinteticos genera registros con un riesgo conocido que crece con la edad.
func datosSinteticos(n int, seed int64) []data.Record {
	rng := rand.New(rand.NewSource(seed))
	recs := make([]data.Record, n)
	for i := range recs {
		e := uint8(rng.Intn(100))
		s := int8(rng.Intn(2))
		z := -7 + 0.09*float64(e) + 0.5*float64(s)
		var y uint8
		if rng.Float64() < 1/(1+math.Exp(-z)) {
			y = 1
		}
		recs[i] = data.Record{Edad: e, Sexo: s, Dept: int8(rng.Intn(25)), Ola: int8(rng.Intn(4)), Label: y, YYYYMM: 202101, Provincia: "P"}
	}
	return recs
}

// Verificación numérica del gradiente analítico (diferencias finitas).
func TestGradienteNumerico(t *testing.T) {
	recs := datosSinteticos(300, 1)
	idx := make([]int32, len(recs))
	for i := range idx {
		idx[i] = int32(i)
	}
	rng := rand.New(rand.NewSource(2))
	w := make([]float64, NumFeat)
	for i := range w {
		w[i] = rng.NormFloat64() * 0.1
	}
	b := -1.0
	gw := make([]float64, NumFeat)
	_, _, _ = gradienteLote(recs, idx, w, b, 50, 29, 3, gw)
	perdida := func(w []float64, b float64) float64 {
		tmp := make([]float64, NumFeat)
		_, l, _ := gradienteLote(recs, idx, w, b, 50, 29, 3, tmp)
		return l
	}
	const h = 1e-6
	for _, j := range []int{fEdad, fEdad2, fSexo, fDept + 4, fOla + 2} {
		wp := append([]float64(nil), w...)
		wm := append([]float64(nil), w...)
		wp[j] += h
		wm[j] -= h
		num := (perdida(wp, b) - perdida(wm, b)) / (2 * h)
		if math.Abs(num-gw[j]) > 1e-4*math.Max(1, math.Abs(num)) {
			t.Errorf("feature %d: analítico %.6f, numérico %.6f", j, gw[j], num)
		}
	}
}

// El entrenamiento paralelo debe converger igual con 1, 2 o 4 workers.
func TestParaleloConverge(t *testing.T) {
	recs := datosSinteticos(60_000, 3)
	train, test := DividirTrainTest(recs, 0.2, 4)
	cfg := ConfigPorDefecto()
	cfg.LoteGlobal = 2048
	cfg.Epocas = 5
	var aucs []float64
	for _, p := range []int{1, 2, 4} {
		cfg.Workers = p
		res := EntrenarParalelo(recs, train, cfg)
		m := Evaluar(res.Modelo, recs, test)
		if m.AUC < 0.85 {
			t.Errorf("workers=%d: AUC %.3f demasiado baja", p, m.AUC)
		}
		if res.Modelo.W[fEdad] <= 0 {
			t.Errorf("workers=%d: el peso de la edad debería ser positivo", p)
		}
		aucs = append(aucs, m.AUC)
	}
	for _, a := range aucs[1:] {
		if math.Abs(a-aucs[0]) > 0.01 {
			t.Errorf("AUC difiere entre configuraciones: %v", aucs)
		}
	}
}

func TestLetalidadEsperadaCalibrada(t *testing.T) {
	recs := datosSinteticos(80_000, 5)
	train, _ := DividirTrainTest(recs, 0.2, 6)
	cfg := ConfigPorDefecto()
	cfg.LoteGlobal = 2048
	cfg.Epocas = 8
	res := EntrenarParalelo(recs, train, cfg)
	// Suma de probabilidades corregidas ≈ número de positivos observados.
	var esperado, observado float64
	for _, k := range train {
		r := &recs[k]
		o := res.Modelo.LetalidadEsperada(r)
		esperado += o / (1 + o)
		observado += float64(r.Label)
	}
	if math.Abs(esperado-observado)/observado > 0.15 {
		t.Errorf("esperado %.0f vs observado %.0f", esperado, observado)
	}
}
