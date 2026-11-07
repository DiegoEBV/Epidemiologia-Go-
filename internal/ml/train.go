package ml

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/data"
)

// Config del entrenamiento.
type Config struct {
	Workers     int     // número de nodos de cómputo (goroutines en PC3, contenedores en PC4)
	Epocas      int     // pasadas completas sobre el set de entrenamiento
	LoteGlobal  int     // tamaño del mini-lote global por paso (se reparte entre workers)
	TasaAprend  float64 // learning rate inicial η0
	Decaimiento float64 // η_t = η0 / (1 + decaimiento·época)
	L2          float64 // regularización λ
	Semilla     int64
}

func ConfigPorDefecto() Config {
	return Config{Workers: 4, Epocas: 6, LoteGlobal: 16384, TasaAprend: 0.8, Decaimiento: 0.5, L2: 1e-5, Semilla: 42}
}

// Mensajes del protocolo coordinador <-> worker. En la PC4 estos dos structs
// se serializan (encoding/gob o binario) sobre conexiones TCP.
type MsgPesos struct {
	Epoca, Paso int
	W           []float64
	B           float64
	Fin         bool
}

type MsgGradiente struct {
	Worker    int
	Paso      int
	GW        []float64
	GB        float64
	Perdida   float64
	PesoTotal float64
	Muestras  int
}

// HistEpoca registra la evolución del entrenamiento.
type HistEpoca struct {
	Epoca    int
	Perdida  float64
	Duracion time.Duration
}

// Resultado del entrenamiento.
type Resultado struct {
	Modelo    *Modelo
	Historial []HistEpoca
	Duracion  time.Duration
	Pasos     int
}

// worker: mantiene su fragmento (shard) de índices y responde cada mensaje de
// pesos con el gradiente de su siguiente mini-lote local. No escribe en
// ningún estado compartido: solo lee recs (inmutable) y usa sus buffers.
func worker(id int, recs []data.Record, shard []int32, loteLocal int, media, desv, pesoPos float64,
	entrada <-chan MsgPesos, salida chan<- MsgGradiente, wg *sync.WaitGroup) {

	defer wg.Done()
	gw := make([]float64, NumFeat)
	rng := rand.New(rand.NewSource(int64(1000 + id)))
	for msg := range entrada {
		if msg.Fin {
			return
		}
		if msg.Paso == 0 { // nueva época: baraja el shard local
			rng.Shuffle(len(shard), func(i, j int) { shard[i], shard[j] = shard[j], shard[i] })
		}
		ini := msg.Paso * loteLocal
		fin := ini + loteLocal
		if fin > len(shard) {
			fin = len(shard)
		}
		gb, perd, pt := gradienteLote(recs, shard[ini:fin], msg.W, msg.B, media, desv, pesoPos, gw)
		out := make([]float64, NumFeat)
		copy(out, gw)
		salida <- MsgGradiente{Worker: id, Paso: msg.Paso, GW: out, GB: gb, Perdida: perd, PesoTotal: pt, Muestras: fin - ini}
	}
}

// EntrenarParalelo implementa SGD síncrono con Parameter Server:
//
//  1. El coordinador reparte el set de entrenamiento en P shards (uno por worker).
//  2. En cada paso envía los pesos actuales a los P workers (broadcast).
//  3. Cada worker calcula el gradiente de su mini-lote local (LoteGlobal/P) en
//     paralelo y lo devuelve por el canal de salida.
//  4. El coordinador espera los P gradientes (barrera), los suma, promedia
//     por el peso total y actualiza w ← w − η(∇ + λw).
//
// Solo el coordinador escribe los pesos, así que no hay condiciones de
// carrera por diseño (no hay mutex: la sincronización es el propio canal).
func EntrenarParalelo(recs []data.Record, train []int32, cfg Config) *Resultado {
	inicio := time.Now()
	P := cfg.Workers
	if P < 1 {
		P = 1
	}
	media, desv, pesoPos := estadisticos(recs, train)
	m := NuevoModelo(media, desv, pesoPos)

	// Barajado global y partición en shards contiguos.
	idx := make([]int32, len(train))
	copy(idx, train)
	rng := rand.New(rand.NewSource(cfg.Semilla))
	rng.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	shards := make([][]int32, P)
	paso := len(idx) / P
	for w := 0; w < P; w++ {
		ini, fin := w*paso, (w+1)*paso
		if w == P-1 {
			fin = len(idx)
		}
		shards[w] = idx[ini:fin]
	}
	loteLocal := cfg.LoteGlobal / P
	if loteLocal < 1 {
		loteLocal = 1
	}
	pasosEpoca := paso / loteLocal
	if pasosEpoca < 1 {
		pasosEpoca = 1
	}

	entradas := make([]chan MsgPesos, P)
	salida := make(chan MsgGradiente, P)
	var wg sync.WaitGroup
	for w := 0; w < P; w++ {
		entradas[w] = make(chan MsgPesos, 1)
		wg.Add(1)
		go worker(w, recs, shards[w], loteLocal, media, desv, pesoPos, entradas[w], salida, &wg)
	}

	res := &Resultado{Modelo: m}
	sumGW := make([]float64, NumFeat)
	for ep := 0; ep < cfg.Epocas; ep++ {
		t0 := time.Now()
		eta := cfg.TasaAprend / (1 + cfg.Decaimiento*float64(ep))
		perdEp, pesoEp := 0.0, 0.0
		for s := 0; s < pasosEpoca; s++ {
			// Broadcast de pesos (cada worker recibe su propia copia).
			for w := 0; w < P; w++ {
				wc := make([]float64, NumFeat)
				copy(wc, m.W)
				entradas[w] <- MsgPesos{Epoca: ep, Paso: s, W: wc, B: m.B}
			}
			// Barrera: reducción de los P gradientes.
			for i := range sumGW {
				sumGW[i] = 0
			}
			sumGB, pesoTot := 0.0, 0.0
			for w := 0; w < P; w++ {
				g := <-salida
				for i, v := range g.GW {
					sumGW[i] += v
				}
				sumGB += g.GB
				pesoTot += g.PesoTotal
				perdEp += g.Perdida
				pesoEp += g.PesoTotal
			}
			// Actualización (solo el coordinador escribe los pesos).
			for i := range m.W {
				m.W[i] -= eta * (sumGW[i]/pesoTot + cfg.L2*m.W[i])
			}
			m.B -= eta * sumGB / pesoTot
			res.Pasos++
		}
		res.Historial = append(res.Historial, HistEpoca{Epoca: ep + 1, Perdida: perdEp / pesoEp, Duracion: time.Since(t0)})
	}
	for w := 0; w < P; w++ {
		entradas[w] <- MsgPesos{Fin: true}
		close(entradas[w])
	}
	wg.Wait()
	res.Duracion = time.Since(inicio)
	return res
}

// estadisticos: media y desviación de la edad y peso de la clase positiva
// (n_neg / n_pos) para compensar el desbalance (~5% de fallecidos).
func estadisticos(recs []data.Record, train []int32) (media, desv, pesoPos float64) {
	var s, s2, pos float64
	for _, k := range train {
		e := float64(recs[k].Edad)
		s += e
		s2 += e * e
		pos += float64(recs[k].Label)
	}
	n := float64(len(train))
	media = s / n
	desv = math.Sqrt(s2/n - media*media)
	if desv == 0 {
		desv = 1
	}
	pesoPos = 1
	if pos > 0 {
		pesoPos = (n - pos) / pos
	}
	return
}

// DividirTrainTest separa índices estratificando por clase (80/20).
func DividirTrainTest(recs []data.Record, fraccTest float64, semilla int64) (train, test []int32) {
	rng := rand.New(rand.NewSource(semilla))
	for i := range recs {
		if rng.Float64() < fraccTest {
			test = append(test, int32(i))
		} else {
			train = append(train, int32(i))
		}
	}
	return
}
