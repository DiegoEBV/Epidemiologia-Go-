// pc3 ejecuta de punta a punta lo que pide el Entregable 1 (PC3):
//
//  1. Carga concurrente y limpieza de los CSV del MINSA (positivos + fallecidos)
//  2. Análisis exploratorio (EDA) en paralelo
//  3. Entrenamiento paralelo de la regresión logística (Parameter Server)
//  4. Evaluación en el set de prueba
//  5. Benchmarks secuencial vs. concurrente (carga y entrenamiento)
//  6. Análisis de impacto: casos graves esperados por provincia
//
// Uso:
//
//	go run ./cmd/pc3 -positivos data/positivos_covid.csv -fallecidos data/fallecidos_covid.csv
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/data"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/ml"
)

type BenchCarga struct {
	Modo        string  `json:"modo"`
	Workers     int     `json:"workers"`
	Segundos    float64 `json:"segundos"`
	FilasPorSeg float64 `json:"filas_por_seg"`
	Speedup     float64 `json:"speedup"`
}

type BenchTrain struct {
	Workers      int     `json:"workers"`
	Segundos     float64 `json:"segundos"`
	Speedup      float64 `json:"speedup"`
	Eficiencia   float64 `json:"eficiencia"`
	PerdidaFinal float64 `json:"perdida_final"`
	AUC          float64 `json:"auc"`
	Accuracy     float64 `json:"accuracy"`
}

type Salida struct {
	Fecha            string               `json:"fecha"`
	CPUs             int                  `json:"cpus"`
	GoVersion        string               `json:"go_version"`
	Archivos         []string             `json:"archivos"`
	Limpieza         []data.Stats         `json:"limpieza"`
	DescNombres      []string             `json:"descarte_nombres"`
	MemoriaMB        float64              `json:"memoria_heap_mb"`
	EDA              *data.EDA            `json:"eda"`
	BenchCarga       []BenchCarga         `json:"bench_carga"`
	BenchTrain       []BenchTrain         `json:"bench_train"`
	Config           ml.Config            `json:"config"`
	Historial        []ml.HistEpoca       `json:"historial"`
	Metricas         ml.Metricas          `json:"metricas"`
	Pesos            map[string]float64   `json:"pesos"`
	Sesgo            float64              `json:"sesgo"`
	MesDesde         int32                `json:"impacto_mes_desde"`
	Impacto          []ml.RiesgoProvincia `json:"impacto_top"`
	ImpactoDept      []ml.RiesgoProvincia `json:"impacto_departamentos"`
	ImpactoExcluidos int                  `json:"impacto_excluidos"`
	NTrain, NTest    int                  `json:"-"`
}

func medianaDur(f func() time.Duration, reps int) time.Duration {
	ds := make([]time.Duration, reps)
	for i := range ds {
		ds[i] = f()
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return ds[reps/2]
}

func main() {
	pos := flag.String("positivos", "data/positivos_covid.csv", "CSV de casos positivos (MINSA)")
	fal := flag.String("fallecidos", "data/fallecidos_covid.csv", "CSV de fallecidos (MINSA)")
	outDir := flag.String("out", "results", "directorio de resultados")
	workers := flag.Int("workers", 4, "workers para la corrida final")
	listaW := flag.String("bench", "1,2,4,8", "workers a comparar en los benchmarks")
	reps := flag.Int("reps", 3, "repeticiones por benchmark (se reporta la mediana)")
	epocas := flag.Int("epocas", 6, "épocas de entrenamiento")
	mesesImpacto := flag.Int("meses-impacto", 4, "ventana (meses más recientes) para el análisis de impacto")
	flag.Parse()
	log.SetFlags(log.Ltime)

	var ws []int
	for _, s := range strings.Split(*listaW, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			ws = append(ws, n)
		}
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}
	sal := &Salida{Fecha: time.Now().Format(time.RFC3339), CPUs: runtime.NumCPU(), GoVersion: runtime.Version(),
		Archivos: []string{*pos, *fal}, DescNombres: data.DescarteNombres}
	log.Printf("CPUs disponibles: %d · %s", sal.CPUs, sal.GoVersion)

	// ---------------- 1. Benchmark de carga ----------------
	log.Printf("== Benchmark de carga sobre %s (mediana de %d corridas) ==", *pos, *reps)
	seq := medianaDur(func() time.Duration {
		_, st, err := data.CargarSecuencial(*pos, data.Positivos)
		if err != nil {
			log.Fatal(err)
		}
		return st.Duracion
	}, *reps)
	_, stTmp, _ := data.CargarSecuencial(*pos, data.Positivos)
	filas := float64(stTmp.Leidas)
	sal.BenchCarga = append(sal.BenchCarga, BenchCarga{"secuencial", 1, seq.Seconds(), filas / seq.Seconds(), 1})
	log.Printf("  secuencial         %7.3f s  %10.0f filas/s", seq.Seconds(), filas/seq.Seconds())
	for _, w := range ws {
		d := medianaDur(func() time.Duration {
			_, st, err := data.CargarConcurrente(*pos, data.Positivos, w)
			if err != nil {
				log.Fatal(err)
			}
			return st.Duracion
		}, *reps)
		b := BenchCarga{"concurrente", w, d.Seconds(), filas / d.Seconds(), seq.Seconds() / d.Seconds()}
		sal.BenchCarga = append(sal.BenchCarga, b)
		log.Printf("  concurrente w=%-3d  %7.3f s  %10.0f filas/s  speedup %.2fx", w, b.Segundos, b.FilasPorSeg, b.Speedup)
	}

	// ---------------- 2. Carga final + limpieza ----------------
	log.Printf("== Carga concurrente final (%d workers) ==", *workers)
	recsP, stP, err := data.CargarConcurrente(*pos, data.Positivos, *workers)
	if err != nil {
		log.Fatal(err)
	}
	recsF, stF, err := data.CargarConcurrente(*fal, data.Fallecidos, *workers)
	if err != nil {
		log.Fatal(err)
	}
	sal.Limpieza = []data.Stats{stP, stF}
	for _, st := range sal.Limpieza {
		log.Printf("  %-10s leídas %9d  válidas %9d  descartadas %7d (%.2f%%)  %.2fs",
			st.Fuente, st.Leidas, st.Validas, st.Leidas-st.Validas,
			100*float64(st.Leidas-st.Validas)/float64(st.Leidas), st.Duracion.Seconds())
		for i, n := range st.Descartes {
			if n > 0 {
				log.Printf("      - %-40s %7d", data.DescarteNombres[i], n)
			}
		}
	}
	recs := append(recsP, recsF...)
	recsP, recsF = nil, nil
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	sal.MemoriaMB = float64(ms.HeapAlloc) / (1 << 20)
	log.Printf("  dataset unificado: %d registros · heap %.0f MB", len(recs), sal.MemoriaMB)

	// ---------------- 3. EDA ----------------
	sal.EDA = data.CalcularEDA(recs)
	e := sal.EDA
	log.Printf("== EDA == positivos %d · fallecidos %d · letalidad global %.2f%% · %d provincias",
		e.Total[0], e.Total[1], 100*float64(e.Total[1])/float64(e.Total[0]), e.Provincias)
	for i, g := range data.GruposEdad {
		log.Printf("  %-6s positivos %8d  fallecidos %7d  letalidad %6.2f%%", g, e.PorEdad[0][i], e.PorEdad[1][i], e.Letalidad[i])
	}

	// ---------------- 4. Entrenamiento + benchmark ----------------
	train, test := ml.DividirTrainTest(recs, 0.2, 2026)
	sal.NTrain, sal.NTest = len(train), len(test)
	log.Printf("== Entrenamiento == train %d · test %d", len(train), len(test))
	cfg := ml.ConfigPorDefecto()
	cfg.Epocas = *epocas
	var tSeq float64
	for _, w := range ws {
		cfg.Workers = w
		var res *ml.Resultado
		d := medianaDur(func() time.Duration { res = ml.EntrenarParalelo(recs, train, cfg); return res.Duracion }, *reps)
		m := ml.Evaluar(res.Modelo, recs, test)
		if w == 1 {
			tSeq = d.Seconds()
		}
		b := BenchTrain{Workers: w, Segundos: d.Seconds(), Speedup: tSeq / d.Seconds(),
			PerdidaFinal: res.Historial[len(res.Historial)-1].Perdida, AUC: m.AUC, Accuracy: m.Accuracy}
		b.Eficiencia = b.Speedup / float64(w)
		sal.BenchTrain = append(sal.BenchTrain, b)
		log.Printf("  workers=%-3d %7.3f s  speedup %.2fx  eficiencia %3.0f%%  pérdida %.4f  AUC %.4f",
			w, b.Segundos, b.Speedup, 100*b.Eficiencia, b.PerdidaFinal, b.AUC)
	}
	cfg.Workers = *workers
	res := ml.EntrenarParalelo(recs, train, cfg)
	sal.Config = cfg
	sal.Historial = res.Historial
	for _, h := range res.Historial {
		log.Printf("  época %d  pérdida %.5f  (%.2fs)", h.Epoca, h.Perdida, h.Duracion.Seconds())
	}

	// ---------------- 5. Evaluación ----------------
	sal.Metricas = ml.Evaluar(res.Modelo, recs, test)
	mt := sal.Metricas
	log.Printf("== Evaluación (test, umbral 0.5) ==")
	log.Printf("  AUC %.4f · accuracy %.4f · precisión %.4f · recall %.4f · F1 %.4f · especificidad %.4f",
		mt.AUC, mt.Accuracy, mt.Precision, mt.Recall, mt.F1, mt.Especificidad)
	log.Printf("  matriz: TP %d FP %d TN %d FN %d · baseline (siempre 0) accuracy %.4f", mt.TP, mt.FP, mt.TN, mt.FN, mt.AccuracyBaseline)
	sal.Pesos = map[string]float64{}
	for i, n := range ml.NombresFeat() {
		sal.Pesos[n] = res.Modelo.W[i]
	}
	sal.Sesgo = res.Modelo.B

	// ---------------- 6. Impacto social ----------------
	meses := e.MesesOrdenados()
	desde := meses[0]
	if len(meses) > *mesesImpacto {
		desde = meses[len(meses)-*mesesImpacto]
	}
	sal.MesDesde = desde
	riesgo, excl := ml.RiesgoPorProvincia(res.Modelo, recs, desde)
	sal.ImpactoExcluidos = excl
	if len(riesgo) > 15 {
		sal.Impacto = riesgo[:15]
	} else {
		sal.Impacto = riesgo
	}
	porDept := map[string]*ml.RiesgoProvincia{}
	for _, r := range riesgo {
		d := porDept[r.Departamento]
		if d == nil {
			d = &ml.RiesgoProvincia{Departamento: r.Departamento}
			porDept[r.Departamento] = d
		}
		d.Positivos += r.Positivos
		d.GravesEsperados += r.GravesEsperados
	}
	for _, d := range porDept {
		d.TasaEsperada = 1000 * d.GravesEsperados / float64(d.Positivos)
		sal.ImpactoDept = append(sal.ImpactoDept, *d)
	}
	sort.Slice(sal.ImpactoDept, func(i, j int) bool { return sal.ImpactoDept[i].GravesEsperados > sal.ImpactoDept[j].GravesEsperados })
	log.Printf("== Impacto: casos graves esperados por provincia (casos desde %d) ==", desde)
	for i, r := range sal.Impacto {
		if i == 10 {
			break
		}
		log.Printf("  %2d. %-14s %-16s positivos %6d  graves esperados %7.1f  (%.1f por 1000)  60+: %.1f%%",
			i+1, r.Departamento, r.Provincia, r.Positivos, r.GravesEsperados, r.TasaEsperada, r.Edad60Mas)
	}

	f, err := os.Create(filepath.Join(*outDir, "resultados.json"))
	if err != nil {
		log.Fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(sal); err != nil {
		log.Fatal(err)
	}
	f.Close()
	fmt.Printf("\nResultados guardados en %s\n", filepath.Join(*outDir, "resultados.json"))
}
