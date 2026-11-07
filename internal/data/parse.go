package data

import (
	"bytes"
	"fmt"
	"strings"
)

// Motivos de descarte que se contabilizan durante la limpieza.
const (
	DescEdadNula = iota
	DescEdadFueraRango
	DescSexoInvalido
	DescDeptInvalido
	DescFechaInvalida
	DescColumnas
	NumDescartes
)

var DescarteNombres = []string{
	"Edad vacía / no numérica",
	"Edad fuera de rango (<0 o >110)",
	"Sexo vacío o no reconocido",
	"Departamento vacío o fuera de catálogo",
	"Fecha vacía o inválida",
	"Número de columnas incorrecto",
}

// Esquema mapea los nombres de columna del encabezado a posiciones. Se arma a
// partir del header real del archivo, así el cargador no depende del orden de
// las columnas (el MINSA lo ha cambiado entre cortes).
type Esquema struct {
	Fuente   Fuente
	NCols    int
	Sep      byte
	idxEdad  int
	idxSexo  int
	idxDept  int
	idxProv  int
	idxFecha int
}

// NuevoEsquema detecta el separador y ubica las columnas necesarias.
func NuevoEsquema(header string, f Fuente) (*Esquema, error) {
	header = strings.TrimPrefix(strings.TrimRight(header, "\r\n"), "\ufeff")
	sep := byte(';')
	if strings.Count(header, ",") > strings.Count(header, ";") {
		sep = ','
	}
	cols := strings.Split(header, string(sep))
	pos := map[string]int{}
	for i, c := range cols {
		pos[strings.ToUpper(strings.Trim(strings.TrimSpace(c), `"`))] = i
	}
	find := func(nombres ...string) int {
		for _, n := range nombres {
			if i, ok := pos[n]; ok {
				return i
			}
		}
		return -1
	}
	e := &Esquema{Fuente: f, NCols: len(cols), Sep: sep}
	e.idxSexo = find("SEXO")
	e.idxDept = find("DEPARTAMENTO")
	e.idxProv = find("PROVINCIA")
	if f == Positivos {
		e.idxEdad = find("EDAD")
		e.idxFecha = find("FECHA_RESULTADO")
	} else {
		e.idxEdad = find("EDAD_DECLARADA", "EDAD")
		e.idxFecha = find("FECHA_FALLECIMIENTO")
	}
	for nombre, v := range map[string]int{"edad": e.idxEdad, "sexo": e.idxSexo,
		"departamento": e.idxDept, "provincia": e.idxProv, "fecha": e.idxFecha} {
		if v < 0 {
			return nil, fmt.Errorf("%s: no se encontró la columna de %s en el encabezado %q", f, nombre, header)
		}
	}
	return e, nil
}

// splitInto separa una línea en campos sin asignar memoria nueva (reutiliza
// dst). Quita comillas dobles envolventes si existieran.
func splitInto(dst [][]byte, line []byte, sep byte) [][]byte {
	dst = dst[:0]
	for {
		i := bytes.IndexByte(line, sep)
		if i < 0 {
			dst = append(dst, trimField(line))
			return dst
		}
		dst = append(dst, trimField(line[:i]))
		line = line[i+1:]
	}
}

func trimField(b []byte) []byte {
	b = bytes.TrimSpace(b)
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		b = b[1 : len(b)-1]
	}
	return b
}

func atoiBytes(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}
	// Algunas edades vienen como "45.0".
	if i := bytes.IndexByte(b, '.'); i >= 0 {
		b = b[:i]
	}
	n := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 1_000 {
			return n, true
		}
	}
	return n, true
}

// parseYYYYMM acepta AAAAMMDD, AAAA-MM-DD y DD/MM/AAAA.
func parseYYYYMM(b []byte) (int32, bool) {
	var y, m int
	var ok1, ok2 bool
	switch {
	case len(b) == 8:
		y, ok1 = atoiBytes(b[0:4])
		m, ok2 = atoiBytes(b[4:6])
	case len(b) >= 10 && b[4] == '-':
		y, ok1 = atoiBytes(b[0:4])
		m, ok2 = atoiBytes(b[5:7])
	case len(b) >= 10 && b[2] == '/':
		y, ok1 = atoiBytes(b[6:10])
		m, ok2 = atoiBytes(b[3:5])
	default:
		return 0, false
	}
	if !ok1 || !ok2 || y < 2020 || y > 2026 || m < 1 || m > 12 {
		return 0, false
	}
	return int32(y*100 + m), true
}

// Parser transforma líneas crudas en Records. Cada goroutine tiene su propio
// Parser (buffers e interning locales), por eso no necesita locks.
type Parser struct {
	E      *Esquema
	campos [][]byte
	intern map[string]string
}

func NuevoParser(e *Esquema) *Parser {
	return &Parser{E: e, campos: make([][]byte, 0, 16), intern: make(map[string]string, 256)}
}

// Parse limpia y valida una línea. Devuelve el motivo de descarte (>=0) o -1
// si el registro es válido.
func (p *Parser) Parse(line []byte, r *Record) int {
	e := p.E
	p.campos = splitInto(p.campos, line, e.Sep)
	c := p.campos
	if len(c) != e.NCols {
		return DescColumnas
	}
	edad, ok := atoiBytes(c[e.idxEdad])
	if !ok {
		return DescEdadNula
	}
	if edad > 110 {
		return DescEdadFueraRango
	}
	switch s := c[e.idxSexo]; {
	case len(s) > 0 && (s[0] == 'F' || s[0] == 'f'):
		r.Sexo = 0
	case len(s) > 0 && (s[0] == 'M' || s[0] == 'm'):
		r.Sexo = 1
	default:
		return DescSexoInvalido
	}
	d := DeptIdx(string(c[e.idxDept]))
	if d < 0 {
		return DescDeptInvalido
	}
	ym, ok := parseYYYYMM(c[e.idxFecha])
	if !ok {
		return DescFechaInvalida
	}
	r.Edad = uint8(edad)
	r.Dept = d
	r.YYYYMM = ym
	r.Ola = OlaDe(ym)
	if e.Fuente == Fallecidos {
		r.Label = 1
	} else {
		r.Label = 0
	}
	// Interning: evita guardar millones de copias de "LIMA", "AREQUIPA", etc.
	pb := bytes.ToUpper(c[e.idxProv])
	if s, ok := p.intern[string(pb)]; ok { // el compilador no asigna aquí
		r.Provincia = s
	} else {
		s := string(pb)
		p.intern[s] = s
		r.Provincia = s
	}
	return -1
}
