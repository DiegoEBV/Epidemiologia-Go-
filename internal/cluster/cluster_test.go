package cluster

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net"
	"testing"
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/data"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/ml"
)

func sinteticos(n int, seed int64) []data.Record {
	rng := rand.New(rand.NewSource(seed))
	provs := []string{"LIMA", "CALLAO", "AREQUIPA", "CUSCO", "PIURA", "EN INVESTIGACIÓN"}
	recs := make([]data.Record, n)
	for i := range recs {
		e := uint8(rng.Intn(100))
		s := int8(rng.Intn(2))
		z := -7 + 0.09*float64(e) + 0.5*float64(s)
		var y uint8
		if rng.Float64() < 1/(1+math.Exp(-z)) {
			y = 1
		}
		recs[i] = data.Record{Edad: e, Sexo: s, Dept: int8(rng.Intn(25)), Ola: int8(rng.Intn(4)), Label: y,
			YYYYMM: int32(202001 + rng.Intn(4)*100 + rng.Intn(12)), Provincia: provs[rng.Intn(len(provs))]}
	}
	return recs
}

// levantar inicia P nodos en puertos efímeros de 127.0.0.1 y devuelve el
// coordinador conectado a ellos.
func levantar(t *testing.T, P int) *Cluster {
	t.Helper()
	var addrs []string
	for i := 0; i < P; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go NuevoNodo(fmt.Sprintf("node%d", i+1), nil).Servir(ln)
		addrs = append(addrs, ln.Addr().String())
	}
	c := NuevoCluster(addrs)
	t.Cleanup(func() {
		for _, n := range c.Nodos {
			n.Cerrar()
		}
	})
	if _, err := c.Esperar(context.Background(), P, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	return c
}

// El entrenamiento distribuido por TCP debe producir el mismo modelo que el
// entrenamiento con goroutines de la PC3 (mismos shards, mismas semillas).
func TestDistribuidoIgualLocal(t *testing.T) {
	recs := sinteticos(60000, 7)
	train, _ := ml.DividirTrainTest(recs, 0.2, 3)
	for _, P := range []int{1, 2, 3} {
		cfg := ml.ConfigPorDefecto()
		cfg.Workers, cfg.Epocas, cfg.LoteGlobal = P, 3, 2048
		local := ml.EntrenarParalelo(recs, train, cfg)

		c := levantar(t, P)
		dist, info, err := c.Entrenar(context.Background(), "test", recs, train, cfg, P, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(info.Nodos) != P || local.Pasos != dist.Pasos {
			t.Fatalf("P=%d: nodos %d, pasos local %d dist %d", P, len(info.Nodos), local.Pasos, dist.Pasos)
		}
		for i := range local.Modelo.W {
			if math.Abs(local.Modelo.W[i]-dist.Modelo.W[i]) > 1e-9 {
				t.Fatalf("P=%d: peso %d difiere: local %.12f dist %.12f", P, i, local.Modelo.W[i], dist.Modelo.W[i])
			}
		}
		if math.Abs(local.Modelo.B-dist.Modelo.B) > 1e-9 {
			t.Fatalf("P=%d: sesgo difiere", P)
		}
	}
}

// El impacto calculado en el clúster (map-reduce por TCP) debe coincidir con
// el cálculo local de la PC3.
func TestImpactoDistribuidoIgualLocal(t *testing.T) {
	recs := sinteticos(30000, 11)
	train, _ := ml.DividirTrainTest(recs, 0.2, 3)
	cfg := ml.ConfigPorDefecto()
	cfg.Epocas, cfg.LoteGlobal = 2, 2048
	m := ml.EntrenarParalelo(recs, train, cfg).Modelo

	local, exLocal := ml.RiesgoPorProvincia(m, recs, 202101)
	c := levantar(t, 3)
	dist, exDist, err := c.Impacto(context.Background(), m, recs, 202101)
	if err != nil {
		t.Fatal(err)
	}
	if exLocal != exDist || len(local) != len(dist) {
		t.Fatalf("excluidos %d/%d, provincias %d/%d", exLocal, exDist, len(local), len(dist))
	}
	idx := map[string]ml.RiesgoProvincia{}
	for _, r := range local {
		idx[r.Departamento+"|"+r.Provincia] = r
	}
	for _, r := range dist {
		l := idx[r.Departamento+"|"+r.Provincia]
		if l.Positivos != r.Positivos || math.Abs(l.GravesEsperados-r.GravesEsperados) > 1e-6 ||
			math.Abs(l.Edad60Mas-r.Edad60Mas) > 1e-9 {
			t.Fatalf("%s/%s: local %+v dist %+v", r.Departamento, r.Provincia, l, r)
		}
	}
}

// Si un nodo cae, el latido lo marca como desconectado y el entrenamiento
// usa solo los activos.
func TestNodoCaido(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go NuevoNodo("vivo", nil).Servir(ln)
	defer ln.Close()
	muerto, _ := net.Listen("tcp", "127.0.0.1:0")
	addrMuerto := muerto.Addr().String()
	muerto.Close()

	c := NuevoCluster([]string{ln.Addr().String(), addrMuerto})
	if ok := c.PingTodos(context.Background()); ok != 1 {
		t.Fatalf("esperaba 1 nodo vivo, hubo %d", ok)
	}
	if len(c.Activos()) != 1 || c.Info()[1].Conectado {
		t.Fatal("el nodo caído no se marcó como desconectado")
	}
	recs := sinteticos(5000, 1)
	train, _ := ml.DividirTrainTest(recs, 0.2, 1)
	cfg := ml.ConfigPorDefecto()
	cfg.Epocas, cfg.LoteGlobal = 1, 512
	if _, info, err := c.Entrenar(context.Background(), "t", recs, train, cfg, 0, nil); err != nil || len(info.Nodos) != 1 {
		t.Fatalf("entrenar con nodo caído: %v", err)
	}
}
