// loadtest mide la latencia de la API bajo carga concurrente (evidencia del
// requisito "< 100 ms"). Inicia sesión, lanza C clientes concurrentes durante
// D segundos contra los endpoints indicados y guarda percentiles e histograma.
//
// Uso:
//
//	go run ./cmd/loadtest -url http://localhost:8080 -c 32 -d 20s -out results/latencia.json
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

var departamentos = []string{"AMAZONAS", "ANCASH", "APURIMAC", "AREQUIPA", "AYACUCHO", "CAJAMARCA", "CALLAO",
	"CUSCO", "HUANCAVELICA", "HUANUCO", "ICA", "JUNIN", "LA LIBERTAD", "LAMBAYEQUE", "LIMA", "LORETO",
	"MADRE DE DIOS", "MOQUEGUA", "PASCO", "PIURA", "PUNO", "SAN MARTIN", "TACNA", "TUMBES", "UCAYALI"}

type muestra struct {
	ruta   string
	dur    time.Duration
	status int
}

type Resumen struct {
	Ruta       string    `json:"ruta"`
	Peticiones int       `json:"peticiones"`
	Errores    int       `json:"errores"`
	RPS        float64   `json:"rps"`
	PromedioMs float64   `json:"promedio_ms"`
	P50Ms      float64   `json:"p50_ms"`
	P90Ms      float64   `json:"p90_ms"`
	P95Ms      float64   `json:"p95_ms"`
	P99Ms      float64   `json:"p99_ms"`
	MaxMs      float64   `json:"max_ms"`
	Bajo100ms  float64   `json:"pct_bajo_100ms"`
	Histograma []int     `json:"histograma"`
	LimitesMs  []float64 `json:"limites_ms"`
}

type Salida struct {
	Fecha       string    `json:"fecha"`
	URL         string    `json:"url"`
	Concurrency int       `json:"concurrencia"`
	DuracionS   float64   `json:"duracion_s"`
	Resumenes   []Resumen `json:"resumenes"`
}

var limites = []float64{0.5, 1, 2, 5, 10, 20, 50, 100, 200}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func resumir(ruta string, ds []time.Duration, errores int, seg float64) Resumen {
	r := Resumen{Ruta: ruta, Peticiones: len(ds), Errores: errores, LimitesMs: limites, Histograma: make([]int, len(limites)+1)}
	if len(ds) == 0 {
		return r
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	var suma time.Duration
	bajo := 0
	for _, d := range ds {
		suma += d
		if d < 100*time.Millisecond {
			bajo++
		}
		v, k := ms(d), 0
		for k < len(limites) && v >= limites[k] {
			k++
		}
		r.Histograma[k]++
	}
	p := func(q float64) float64 { return ms(ds[int(q*float64(len(ds)-1))]) }
	r.RPS = float64(len(ds)) / seg
	r.PromedioMs = ms(suma / time.Duration(len(ds)))
	r.P50Ms, r.P90Ms, r.P95Ms, r.P99Ms, r.MaxMs = p(.5), p(.9), p(.95), p(.99), ms(ds[len(ds)-1])
	r.Bajo100ms = 100 * float64(bajo) / float64(len(ds))
	return r
}

func main() {
	base := flag.String("url", "http://localhost:8080", "URL base de la API")
	c := flag.Int("c", 32, "clientes concurrentes")
	dur := flag.Duration("d", 20*time.Second, "duración de la prueba")
	user := flag.String("user", "admin", "usuario")
	pass := flag.String("pass", "admin123", "contraseña")
	out := flag.String("out", "results/latencia.json", "archivo de salida")
	flag.Parse()

	cli := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: *c * 2}}
	body, _ := json.Marshal(map[string]string{"username": *user, "password": *pass})
	resp, err := cli.Post(*base+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatal(err)
	}
	var tok struct{ Token string }
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	if tok.Token == "" {
		log.Fatalf("login falló: %s", resp.Status)
	}

	// Mezcla de tráfico: 80 % predicciones, 10 % impacto, 10 % estado del clúster.
	hacer := func(rng *rand.Rand) muestra {
		var req *http.Request
		x := rng.Float64()
		switch {
		case x < 0.8:
			b, _ := json.Marshal(map[string]any{
				"edad": rng.Intn(100), "sexo": []string{"M", "F"}[rng.Intn(2)],
				"departamento": departamentos[rng.Intn(len(departamentos))],
				"fecha":        fmt.Sprintf("%d-%02d-15", 2020+rng.Intn(4), 1+rng.Intn(12)),
			})
			req, _ = http.NewRequest("POST", *base+"/api/predicciones", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
		case x < 0.9:
			req, _ = http.NewRequest("GET", *base+"/api/impacto?top=15", nil)
		default:
			req, _ = http.NewRequest("GET", *base+"/api/cluster", nil)
		}
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		t0 := time.Now()
		r, err := cli.Do(req)
		d := time.Since(t0)
		ruta := req.Method + " " + req.URL.Path
		if err != nil {
			return muestra{ruta, d, 0}
		}
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
		return muestra{ruta, d, r.StatusCode}
	}

	log.Printf("carga: %d clientes durante %s contra %s", *c, *dur, *base)
	fin := time.Now().Add(*dur)
	res := make(chan []muestra, *c)
	var wg sync.WaitGroup
	t0 := time.Now()
	for i := 0; i < *c; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(i)))
			var local []muestra
			for time.Now().Before(fin) {
				local = append(local, hacer(rng))
			}
			res <- local
		}(i)
	}
	wg.Wait()
	close(res)
	seg := time.Since(t0).Seconds()

	porRuta := map[string][]time.Duration{}
	errores := map[string]int{}
	var todas []time.Duration
	totalErr := 0
	for l := range res {
		for _, m := range l {
			if m.status != 200 {
				errores[m.ruta]++
				totalErr++
				continue
			}
			porRuta[m.ruta] = append(porRuta[m.ruta], m.dur)
			todas = append(todas, m.dur)
		}
	}
	sal := Salida{Fecha: time.Now().Format(time.RFC3339), URL: *base, Concurrency: *c, DuracionS: seg}
	sal.Resumenes = append(sal.Resumenes, resumir("TOTAL", todas, totalErr, seg))
	for ruta, ds := range porRuta {
		sal.Resumenes = append(sal.Resumenes, resumir(ruta, ds, errores[ruta], seg))
	}
	fmt.Printf("\n%-28s %8s %6s %8s %8s %8s %8s %8s %9s\n", "ruta", "req", "err", "req/s", "p50 ms", "p95 ms", "p99 ms", "max ms", "<100ms")
	for _, r := range sal.Resumenes {
		fmt.Printf("%-28s %8d %6d %8.0f %8.2f %8.2f %8.2f %8.2f %8.2f%%\n",
			r.Ruta, r.Peticiones, r.Errores, r.RPS, r.P50Ms, r.P95Ms, r.P99Ms, r.MaxMs, r.Bajo100ms)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	_ = enc.Encode(sal)
	f.Close()
	fmt.Printf("\nResultados guardados en %s\n", *out)
}
