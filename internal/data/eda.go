package data

import (
	"runtime"
	"sort"
	"sync"
)

type provKey struct {
	d int8
	p string
}

// GruposEdad usados para el análisis exploratorio.
var GruposEdad = []string{"0-17", "18-29", "30-39", "40-49", "50-59", "60-69", "70-79", "80+"}

func grupoEdad(e uint8) int {
	switch {
	case e < 18:
		return 0
	case e < 30:
		return 1
	case e < 40:
		return 2
	case e < 50:
		return 3
	case e < 60:
		return 4
	case e < 70:
		return 5
	case e < 80:
		return 6
	default:
		return 7
	}
}

// EDA contiene los conteos del análisis exploratorio, separados por fuente
// (índice 0 = positivos, 1 = fallecidos).
type EDA struct {
	Total      [2]int64
	PorEdad    [2][8]int64
	PorSexo    [2][2]int64
	PorDept    [2][25]int64
	PorOla     [2][NumOlas]int64
	PorMes     map[int32][2]int64
	SumaEdad   [2]float64
	EdadMedia  [2]float64
	Letalidad  [8]float64 // fallecidos / positivos por grupo de edad (%)
	LetalDept  [25]float64
	LetalOla   [NumOlas]float64
	LetalSexo  [2]float64
	Provincias int
}

// CalcularEDA recorre los registros en paralelo (map-reduce): cada goroutine
// cuenta sobre su tramo y al final se suman los parciales.
func CalcularEDA(recs []Record) *EDA {
	nw := runtime.NumCPU()
	parciales := make([]EDA, nw)
	meses := make([]map[int32][2]int64, nw)
	provs := make([]map[provKey]struct{}, nw)
	var wg sync.WaitGroup
	paso := (len(recs) + nw - 1) / nw
	for w := 0; w < nw; w++ {
		ini, fin := w*paso, (w+1)*paso
		if ini > len(recs) {
			ini = len(recs)
		}
		if fin > len(recs) {
			fin = len(recs)
		}
		wg.Add(1)
		go func(w, ini, fin int) {
			defer wg.Done()
			p := &parciales[w]
			m := map[int32][2]int64{}
			pv := map[provKey]struct{}{}
			for i := ini; i < fin; i++ {
				r := &recs[i]
				l := r.Label
				p.Total[l]++
				p.PorEdad[l][grupoEdad(r.Edad)]++
				p.PorSexo[l][r.Sexo]++
				p.PorDept[l][r.Dept]++
				p.PorOla[l][r.Ola]++
				p.SumaEdad[l] += float64(r.Edad)
				v := m[r.YYYYMM]
				v[l]++
				m[r.YYYYMM] = v
				pv[provKey{r.Dept, r.Provincia}] = struct{}{}
			}
			meses[w], provs[w] = m, pv
		}(w, ini, fin)
	}
	wg.Wait()

	out := &EDA{PorMes: map[int32][2]int64{}}
	todas := map[provKey]struct{}{}
	for w := range parciales {
		p := &parciales[w]
		for l := 0; l < 2; l++ {
			out.Total[l] += p.Total[l]
			out.SumaEdad[l] += p.SumaEdad[l]
			for i := range p.PorEdad[l] {
				out.PorEdad[l][i] += p.PorEdad[l][i]
			}
			for i := range p.PorSexo[l] {
				out.PorSexo[l][i] += p.PorSexo[l][i]
			}
			for i := range p.PorDept[l] {
				out.PorDept[l][i] += p.PorDept[l][i]
			}
			for i := range p.PorOla[l] {
				out.PorOla[l][i] += p.PorOla[l][i]
			}
		}
		for k, v := range meses[w] {
			o := out.PorMes[k]
			o[0] += v[0]
			o[1] += v[1]
			out.PorMes[k] = o
		}
		for k := range provs[w] {
			todas[k] = struct{}{}
		}
	}
	out.Provincias = len(todas)
	ratio := func(a, b int64) float64 {
		if b == 0 {
			return 0
		}
		return 100 * float64(a) / float64(b)
	}
	for l := 0; l < 2; l++ {
		if out.Total[l] > 0 {
			out.EdadMedia[l] = out.SumaEdad[l] / float64(out.Total[l])
		}
	}
	for i := range out.Letalidad {
		out.Letalidad[i] = ratio(out.PorEdad[1][i], out.PorEdad[0][i])
	}
	for i := range out.LetalDept {
		out.LetalDept[i] = ratio(out.PorDept[1][i], out.PorDept[0][i])
	}
	for i := range out.LetalOla {
		out.LetalOla[i] = ratio(out.PorOla[1][i], out.PorOla[0][i])
	}
	for i := range out.LetalSexo {
		out.LetalSexo[i] = ratio(out.PorSexo[1][i], out.PorSexo[0][i])
	}
	return out
}

// MesesOrdenados devuelve las claves AAAAMM en orden.
func (e *EDA) MesesOrdenados() []int32 {
	ks := make([]int32, 0, len(e.PorMes))
	for k := range e.PorMes {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	return ks
}
