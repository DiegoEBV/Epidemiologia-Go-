package cluster

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/data"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/ml"
)

// Cluster es el coordinador: conoce a todos los nodos, vigila su salud con
// latidos y reparte el trabajo (entrenamiento e impacto) entre los activos.
type Cluster struct {
	Nodos []*NodoRemoto
}

// NuevoCluster crea el coordinador a partir de direcciones "host:puerto".
// El ID de cada nodo es el host (node1, node2, ...).
func NuevoCluster(addrs []string) *Cluster {
	c := &Cluster{}
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		id := a
		if i := strings.LastIndex(a, ":"); i > 0 {
			id = a[:i]
		}
		c.Nodos = append(c.Nodos, NuevoNodoRemoto(id, a))
	}
	return c
}

// PingTodos hace un latido concurrente a todos los nodos (fan-out) y devuelve
// cuántos respondieron.
func (c *Cluster) PingTodos(ctx context.Context) int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for _, n := range c.Nodos {
		wg.Add(1)
		go func(n *NodoRemoto) {
			defer wg.Done()
			if _, _, err := n.Ping(ctx); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(n)
	}
	wg.Wait()
	return ok
}

// Esperar reintenta hasta que respondan al menos min nodos o venza el plazo.
func (c *Cluster) Esperar(ctx context.Context, min int, plazo time.Duration) (int, error) {
	limite := time.Now().Add(plazo)
	for {
		ok := c.PingTodos(ctx)
		if ok >= min {
			return ok, nil
		}
		if time.Now().After(limite) {
			return ok, fmt.Errorf("solo %d de %d nodos respondieron", ok, len(c.Nodos))
		}
		select {
		case <-ctx.Done():
			return ok, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Latidos ejecuta PingTodos periódicamente y entrega el estado del clúster.
func (c *Cluster) Latidos(ctx context.Context, cada time.Duration, cb func([]InfoNodo)) {
	t := time.NewTicker(cada)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.PingTodos(ctx)
			if cb != nil {
				cb(c.Info())
			}
		}
	}
}

// Info devuelve el estado de todos los nodos.
func (c *Cluster) Info() []InfoNodo {
	out := make([]InfoNodo, len(c.Nodos))
	for i, n := range c.Nodos {
		out[i] = n.Info()
	}
	return out
}

// Activos devuelve los nodos cuyo último intercambio fue exitoso.
func (c *Cluster) Activos() []*NodoRemoto {
	var out []*NodoRemoto
	for _, n := range c.Nodos {
		if n.Conectado() {
			out = append(out, n)
		}
	}
	return out
}

// DetalleNodo resume la participación de un nodo en un entrenamiento.
type DetalleNodo struct {
	ID        string  `json:"id" bson:"id"`
	Worker    int     `json:"worker" bson:"worker"`
	Registros int     `json:"registros" bson:"registros"`
	EnvioMs   float64 `json:"envio_ms" bson:"envio_ms"`
	CargaMs   float64 `json:"carga_ms" bson:"carga_ms"`
}

// InfoEntrenamiento describe cómo se distribuyó un entrenamiento.
type InfoEntrenamiento struct {
	Nodos         []DetalleNodo    `json:"nodos" bson:"nodos"`
	LoteLocal     int              `json:"lote_local" bson:"lote_local"`
	PasosEpoca    int              `json:"pasos_epoca" bson:"pasos_epoca"`
	DistribucionS float64          `json:"distribucion_s" bson:"distribucion_s"`
	Norm          ml.Normalizacion `json:"normalizacion" bson:"normalizacion"`
}

// Entrenar distribuye el set de entrenamiento entre nNodos nodos activos y
// ejecuta el Parameter Server síncrono sobre TCP.
//
//  1. Partición: PartirShards (misma semilla que la PC3) → P fragmentos.
//  2. Distribución: cada fragmento se envía en paralelo (una goroutine por
//     nodo) en formato columnar.
//  3. Entrenamiento: ml.Coordinar usa los NodoRemoto como Calculador.
func (c *Cluster) Entrenar(ctx context.Context, trabajo string, recs []data.Record, train []int32,
	cfg ml.Config, nNodos int, onEpoca func(ml.Progreso)) (*ml.Resultado, *InfoEntrenamiento, error) {

	act := c.Activos()
	if len(act) == 0 {
		return nil, nil, errors.New("no hay nodos activos en el clúster")
	}
	if nNodos <= 0 || nNodos > len(act) {
		nNodos = len(act)
	}
	act = act[:nNodos]
	norm := ml.Estadisticos(recs, train)
	shards := ml.PartirShards(train, nNodos, cfg.Semilla)
	loteLocal, pasos := ml.Plan(len(train), nNodos, cfg.LoteGlobal)
	info := &InfoEntrenamiento{LoteLocal: loteLocal, PasosEpoca: pasos, Norm: norm, Nodos: make([]DetalleNodo, nNodos)}

	t0 := time.Now()
	errs := make([]error, nNodos)
	var wg sync.WaitGroup
	for w := range act {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ts := time.Now()
			msg := &MsgShard{Trabajo: trabajo, Worker: w, LoteLocal: loteLocal, Norm: norm,
				Datos: AColumnas(recs, shards[w], nil)}
			ok, err := act[w].CargarShard(ctx, msg)
			if err != nil {
				errs[w] = err
				return
			}
			info.Nodos[w] = DetalleNodo{ID: act[w].ID, Worker: w, Registros: ok.Registros,
				EnvioMs: float64(time.Since(ts).Microseconds()) / 1000, CargaMs: ok.CargaMs}
		}(w)
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, info, fmt.Errorf("distribuir fragmentos: %w", err)
	}
	info.DistribucionS = time.Since(t0).Seconds()

	calc := make([]ml.Calculador, nNodos)
	for i, n := range act {
		calc[i] = n
	}
	res, err := ml.Coordinar(ctx, calc, norm, pasos, cfg, onEpoca)
	return res, info, err
}

// Impacto responde la pregunta de impacto social de forma distribuida
// (map-reduce): los casos positivos recientes se reparten entre los nodos,
// cada nodo suma la letalidad esperada por provincia y el coordinador combina
// las sumas parciales. Devuelve el ranking completo y los excluidos (sin
// provincia).
func (c *Cluster) Impacto(ctx context.Context, m *ml.Modelo, recs []data.Record, mesDesde int32) ([]ml.RiesgoProvincia, int, error) {
	act := c.Activos()
	if len(act) == 0 {
		return nil, 0, errors.New("no hay nodos activos en el clúster")
	}
	type clave struct {
		d int8
		p string
	}
	dicc := map[clave]uint16{}
	var claves []clave
	var idx []int32
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
		k := clave{r.Dept, r.Provincia}
		if _, ok := dicc[k]; !ok {
			dicc[k] = uint16(len(claves))
			claves = append(claves, k)
		}
		idx = append(idx, int32(i))
	}
	provID := func(r *data.Record) uint16 { return dicc[clave{r.Dept, r.Provincia}] }
	norm := ml.Normalizacion{MediaEdad: m.MediaEdad, DesvEdad: m.DesvEdad, PesoPos: m.PesoPos}

	P := len(act)
	parts := make([]*MsgImpactoOK, P)
	errs := make([]error, P)
	paso := (len(idx) + P - 1) / P
	var wg sync.WaitGroup
	for w := 0; w < P; w++ {
		ini, fin := w*paso, (w+1)*paso
		if fin > len(idx) {
			fin = len(idx)
		}
		if ini >= fin {
			continue
		}
		wg.Add(1)
		go func(w, ini, fin int) {
			defer wg.Done()
			msg := &MsgImpacto{W: m.W, B: m.B, Norm: norm, NumProvs: len(claves), Datos: AColumnas(recs, idx[ini:fin], provID)}
			parts[w], errs[w] = act[w].Impacto(ctx, msg)
		}(w, ini, fin)
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, excluidos, err
	}
	// Reduce.
	out := make([]ml.RiesgoProvincia, len(claves))
	may := make([]int32, len(claves))
	for i, k := range claves {
		out[i] = ml.RiesgoProvincia{Departamento: data.Departamentos[k.d], Provincia: k.p}
	}
	for _, p := range parts {
		if p == nil {
			continue
		}
		for i := range claves {
			out[i].Positivos += int(p.Positivos[i])
			out[i].GravesEsperados += p.Graves[i]
			may[i] += p.Mayores60[i]
		}
	}
	for i := range out {
		if out[i].Positivos > 0 {
			out[i].TasaEsperada = 1000 * out[i].GravesEsperados / float64(out[i].Positivos)
			out[i].Edad60Mas = 100 * float64(may[i]) / float64(out[i].Positivos)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GravesEsperados > out[j].GravesEsperados })
	return out, excluidos, nil
}
