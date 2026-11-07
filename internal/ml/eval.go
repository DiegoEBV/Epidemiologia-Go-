package ml

import (
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/data"
)

// Metricas de clasificación sobre el set de prueba.
type Metricas struct {
	N                int
	TP, FP, TN, FN   int
	Accuracy         float64
	Precision        float64
	Recall           float64
	F1               float64
	Especificidad    float64
	AUC              float64
	AccuracyBaseline float64 // predecir siempre "no fallece"
}

// PredecirParalelo calcula ŷ para cada índice. Cada goroutine escribe en un
// rango disjunto del slice de salida: no se necesita sincronizar escrituras.
func PredecirParalelo(m *Modelo, recs []data.Record, idx []int32) []float64 {
	out := make([]float64, len(idx))
	nw := runtime.NumCPU()
	paso := (len(idx) + nw - 1) / nw
	var wg sync.WaitGroup
	for w := 0; w < nw; w++ {
		ini, fin := w*paso, (w+1)*paso
		if ini >= len(idx) {
			break
		}
		if fin > len(idx) {
			fin = len(idx)
		}
		wg.Add(1)
		go func(ini, fin int) {
			defer wg.Done()
			for i := ini; i < fin; i++ {
				out[i] = m.Prob(&recs[idx[i]])
			}
		}(ini, fin)
	}
	wg.Wait()
	return out
}

// Evaluar calcula la matriz de confusión (umbral 0.5) y el AUC-ROC.
func Evaluar(m *Modelo, recs []data.Record, test []int32) Metricas {
	probs := PredecirParalelo(m, recs, test)
	var mt Metricas
	mt.N = len(test)
	pos := 0
	for i, k := range test {
		y := recs[k].Label == 1
		if y {
			pos++
		}
		pred := probs[i] >= 0.5
		switch {
		case pred && y:
			mt.TP++
		case pred && !y:
			mt.FP++
		case !pred && !y:
			mt.TN++
		default:
			mt.FN++
		}
	}
	div := func(a, b int) float64 {
		if b == 0 {
			return 0
		}
		return float64(a) / float64(b)
	}
	mt.Accuracy = div(mt.TP+mt.TN, mt.N)
	mt.Precision = div(mt.TP, mt.TP+mt.FP)
	mt.Recall = div(mt.TP, mt.TP+mt.FN)
	mt.Especificidad = div(mt.TN, mt.TN+mt.FP)
	if mt.Precision+mt.Recall > 0 {
		mt.F1 = 2 * mt.Precision * mt.Recall / (mt.Precision + mt.Recall)
	}
	mt.AccuracyBaseline = div(mt.N-pos, mt.N)
	mt.AUC = auc(probs, recs, test, pos)
	return mt
}

// auc: estadístico de Mann-Whitney sobre los rangos (maneja empates).
func auc(probs []float64, recs []data.Record, test []int32, pos int) float64 {
	neg := len(test) - pos
	if pos == 0 || neg == 0 {
		return 0
	}
	ord := make([]int, len(probs))
	for i := range ord {
		ord[i] = i
	}
	sort.Slice(ord, func(a, b int) bool { return probs[ord[a]] < probs[ord[b]] })
	var sumaRangos float64
	for i := 0; i < len(ord); {
		j := i
		for j+1 < len(ord) && probs[ord[j+1]] == probs[ord[i]] {
			j++
		}
		rango := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			if recs[test[ord[k]]].Label == 1 {
				sumaRangos += rango
			}
		}
		i = j + 1
	}
	return (sumaRangos - float64(pos)*float64(pos+1)/2) / (float64(pos) * float64(neg))
}

// RiesgoProvincia es una fila del análisis de impacto social.
type RiesgoProvincia struct {
	Departamento    string
	Provincia       string
	Positivos       int
	GravesEsperados float64 // Σ letalidad esperada de cada caso
	TasaEsperada    float64 // por cada 1000 casos
	Edad60Mas       float64 // % de casos con 60+ años
}

// RiesgoPorProvincia responde la pregunta de impacto: dados los casos
// positivos recientes (desde mesDesde), ¿cuántos casos graves (fallecimiento)
// se esperan en cada provincia según el perfil de sus pacientes?
// Los casos sin provincia asignada ("EN INVESTIGACIÓN" o vacía) se excluyen del
// ranking y se devuelven contados aparte.
func RiesgoPorProvincia(m *Modelo, recs []data.Record, mesDesde int32) ([]RiesgoProvincia, int) {
	type key struct {
		d int8
		p string
	}
	acc := map[key]*RiesgoProvincia{}
	mayores := map[key]int{}
	excluidos := 0
	for i := range recs {
		r := &recs[i]
		if r.Label != 0 || r.YYYYMM < mesDesde {
			continue
		}
		if r.Provincia == "" || strings.Contains(r.Provincia, "INVESTIGACI") {
			excluidos++
			continue
		}
		k := key{r.Dept, r.Provincia}
		rp := acc[k]
		if rp == nil {
			rp = &RiesgoProvincia{Departamento: data.Departamentos[r.Dept], Provincia: r.Provincia}
			acc[k] = rp
		}
		rp.Positivos++
		rp.GravesEsperados += m.LetalidadEsperada(r)
		if r.Edad >= 60 {
			mayores[k]++
		}
	}
	out := make([]RiesgoProvincia, 0, len(acc))
	for k, rp := range acc {
		rp.TasaEsperada = 1000 * rp.GravesEsperados / float64(rp.Positivos)
		rp.Edad60Mas = 100 * float64(mayores[k]) / float64(rp.Positivos)
		out = append(out, *rp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GravesEsperados > out[j].GravesEsperados })
	return out, excluidos
}
