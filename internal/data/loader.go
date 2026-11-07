package data

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Stats resume la limpieza de un archivo. Cada worker acumula sus propias
// Stats y al final se suman (fan-in), así no hay memoria compartida mutable.
type Stats struct {
	Fuente      string
	Leidas      int64
	Validas     int64
	Descartes   [NumDescartes]int64
	Workers     int
	Bloques     int
	Duracion    time.Duration
	FilasPorSeg float64
}

func (s *Stats) sumar(o *Stats) {
	s.Leidas += o.Leidas
	s.Validas += o.Validas
	for i := range s.Descartes {
		s.Descartes[i] += o.Descartes[i]
	}
}

// Lote es la unidad que viaja por el canal: un slice de registros ya limpios.
// Mandar lotes (y no registros sueltos) reduce la contención del canal.
type Lote struct {
	Bloque int
	Recs   []Record
}

const tamLote = 8192

// bloque es un rango de bytes [ini, fin) del archivo.
type bloque struct {
	id       int
	ini, fin int64
}

// Leer encabezado y armar esquema.
func leerEsquema(path string, f Fuente) (*Esquema, int64, int64, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return nil, 0, 0, err
	}
	br := bufio.NewReader(fh)
	header, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, 0, err
	}
	e, err := NuevoEsquema(header, f)
	if err != nil {
		return nil, 0, 0, err
	}
	return e, int64(len(header)), st.Size(), nil
}

// partir divide el cuerpo del archivo en n bloques de bytes de tamaño parecido.
func partir(inicio, total int64, n int) []bloque {
	cuerpo := total - inicio
	if n < 1 {
		n = 1
	}
	if cuerpo < int64(n) {
		n = 1
	}
	paso := cuerpo / int64(n)
	bs := make([]bloque, n)
	for i := 0; i < n; i++ {
		ini := inicio + int64(i)*paso
		fin := ini + paso
		if i == n-1 {
			fin = total
		}
		bs[i] = bloque{id: i, ini: ini, fin: fin}
	}
	return bs
}

// procesarBloque lee las líneas que *empiezan* dentro de [ini, fin). La regla
// resuelve el problema de las líneas cortadas en el borde de dos bloques:
//   - si ini no es el comienzo del cuerpo, se descarta el pedazo hasta el
//     primer '\n' (esa línea pertenece al bloque anterior);
//   - se sigue leyendo mientras la línea empiece antes de fin, aunque termine
//     después (se completa leyendo más allá del borde).
func procesarBloque(path string, e *Esquema, b bloque, primerByte int64,
	out chan<- Lote, contador *atomic.Int64) (Stats, error) {

	var st Stats
	fh, err := os.Open(path)
	if err != nil {
		return st, err
	}
	defer fh.Close()

	pos := b.ini
	if b.ini > primerByte {
		// Retrocede un byte: si el byte anterior es '\n', la línea empieza
		// justo en ini y le pertenece a este bloque.
		pos = b.ini - 1
	}
	if _, err := fh.Seek(pos, io.SeekStart); err != nil {
		return st, err
	}
	br := bufio.NewReaderSize(fh, 1<<20)
	if b.ini > primerByte {
		salto, err := br.ReadSlice('\n')
		pos += int64(len(salto))
		if err != nil {
			return st, nil // no hay líneas que empiecen en este bloque
		}
	}

	p := NuevoParser(e)
	lote := make([]Record, 0, tamLote)
	var r Record
	for pos < b.fin {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// Línea anómala más larga que el buffer: se consume y se descarta.
			pos += int64(len(line))
			for errors.Is(err, bufio.ErrBufferFull) {
				line, err = br.ReadSlice('\n')
				pos += int64(len(line))
			}
			st.Leidas++
			st.Descartes[DescColumnas]++
			continue
		}
		pos += int64(len(line))
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > 0 {
			st.Leidas++
			if motivo := p.Parse(line, &r); motivo >= 0 {
				st.Descartes[motivo]++
			} else {
				st.Validas++
				lote = append(lote, r)
				if len(lote) == tamLote {
					out <- Lote{Bloque: b.id, Recs: lote}
					contador.Add(int64(len(lote)))
					lote = make([]Record, 0, tamLote)
				}
			}
		}
		if err != nil { // EOF
			break
		}
	}
	if len(lote) > 0 {
		out <- Lote{Bloque: b.id, Recs: lote}
		contador.Add(int64(len(lote)))
	}
	return st, nil
}

// CargarConcurrente implementa el patrón worker pool + fan-in:
//
//	main ──► cola de bloques ──► W goroutines (parsean y limpian)
//	                                   │  chan Lote (buffered)
//	                                   ▼
//	                              colector (append a un único slice)
//
// Se crean más bloques que workers (4×) para balancear la carga: si un bloque
// tiene más líneas inválidas o más largas, otro worker toma el siguiente.
func CargarConcurrente(path string, f Fuente, workers int) ([]Record, Stats, error) {
	inicio := time.Now()
	e, primerByte, total, err := leerEsquema(path, f)
	if err != nil {
		return nil, Stats{}, err
	}
	if workers < 1 {
		workers = 1
	}
	bloques := partir(primerByte, total, workers*4)

	cola := make(chan bloque, len(bloques))
	for _, b := range bloques {
		cola <- b
	}
	close(cola)

	out := make(chan Lote, workers*4) // canal con buffer: desacopla workers y colector
	statsW := make([]Stats, workers)  // un slot por worker: sin escrituras compartidas
	errs := make(chan error, workers)
	var contador atomic.Int64
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for b := range cola {
				st, err := procesarBloque(path, e, b, primerByte, out, &contador)
				if err != nil {
					errs <- fmt.Errorf("worker %d, bloque %d: %w", id, b.id, err)
					return
				}
				statsW[id].sumar(&st)
			}
		}(w)
	}
	// Cierra el canal de salida cuando todos los workers terminan.
	go func() { wg.Wait(); close(out); close(errs) }()

	// Colector (fan-in). Estimamos capacidad para evitar realocaciones.
	recs := make([]Record, 0, total/60)
	for lote := range out {
		recs = append(recs, lote.Recs...)
	}
	if err := <-errs; err != nil {
		return nil, Stats{}, err
	}

	st := Stats{Fuente: f.String(), Workers: workers, Bloques: len(bloques)}
	for i := range statsW {
		st.sumar(&statsW[i])
	}
	st.Duracion = time.Since(inicio)
	st.FilasPorSeg = float64(st.Leidas) / st.Duracion.Seconds()
	return recs, st, nil
}

// CargarSecuencial es la línea base: un solo hilo, línea por línea.
func CargarSecuencial(path string, f Fuente) ([]Record, Stats, error) {
	inicio := time.Now()
	fh, err := os.Open(path)
	if err != nil {
		return nil, Stats{}, err
	}
	defer fh.Close()
	br := bufio.NewReaderSize(fh, 1<<20)
	header, err := br.ReadString('\n')
	if err != nil {
		return nil, Stats{}, err
	}
	e, err := NuevoEsquema(header, f)
	if err != nil {
		return nil, Stats{}, err
	}
	p := NuevoParser(e)
	st := Stats{Fuente: f.String(), Workers: 1, Bloques: 1}
	var recs []Record
	var r Record
	for {
		line, err := br.ReadSlice('\n')
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > 0 {
			st.Leidas++
			if motivo := p.Parse(line, &r); motivo >= 0 {
				st.Descartes[motivo]++
			} else {
				st.Validas++
				recs = append(recs, r)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if !errors.Is(err, bufio.ErrBufferFull) {
				return nil, Stats{}, err
			}
		}
	}
	st.Duracion = time.Since(inicio)
	st.FilasPorSeg = float64(st.Leidas) / st.Duracion.Seconds()
	return recs, st, nil
}
