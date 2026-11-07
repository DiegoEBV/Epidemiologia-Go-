package data

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const headerPos = "FECHA_CORTE;DEPARTAMENTO;PROVINCIA;DISTRITO;METODODX;EDAD;SEXO;FECHA_RESULTADO;UBIGEO;id_persona"

func TestParseLimpieza(t *testing.T) {
	e, err := NuevoEsquema(headerPos, Positivos)
	if err != nil {
		t.Fatal(err)
	}
	p := NuevoParser(e)
	casos := []struct {
		linea  string
		motivo int
	}{
		{"20231231;LIMA;LIMA;MIRAFLORES;PCR;45;FEMENINO;20210315;150122;1", -1},
		{"20231231;lima;lima;MIRAFLORES;PCR;45.0;M;2021-03-15;150122;1", -1},
		{"20231231;LIMA;LIMA;MIRAFLORES;PCR;;FEMENINO;20210315;150122;1", DescEdadNula},
		{"20231231;LIMA;LIMA;MIRAFLORES;PCR;150;FEMENINO;20210315;150122;1", DescEdadFueraRango},
		{"20231231;LIMA;LIMA;MIRAFLORES;PCR;45;;20210315;150122;1", DescSexoInvalido},
		{"20231231;EN INVESTIGACIÓN;X;X;PCR;45;FEMENINO;20210315;150122;1", DescDeptInvalido},
		{"20231231;LIMA;LIMA;MIRAFLORES;PCR;45;FEMENINO;;150122;1", DescFechaInvalida},
		{"20231231;LIMA;LIMA;MIRAFLORES;PCR;45;FEMENINO;20210315", DescColumnas},
	}
	for _, c := range casos {
		var r Record
		if got := p.Parse([]byte(c.linea), &r); got != c.motivo {
			t.Errorf("%q: motivo=%d, se esperaba %d", c.linea, got, c.motivo)
		}
	}
	var r Record
	p.Parse([]byte(casos[0].linea), &r)
	if r.Edad != 45 || r.Sexo != 0 || Departamentos[r.Dept] != "LIMA" || r.YYYYMM != 202103 || r.Ola != Ola2 || r.Label != 0 {
		t.Errorf("registro mal parseado: %+v", r)
	}
}

func TestEsquemaFallecidos(t *testing.T) {
	h := "FECHA_CORTE;FECHA_FALLECIMIENTO;EDAD_DECLARADA;SEXO;CLASIFICACION_DEF;DEPARTAMENTO;PROVINCIA;DISTRITO;UBIGEO;UUID"
	e, err := NuevoEsquema(h, Fallecidos)
	if err != nil {
		t.Fatal(err)
	}
	var r Record
	if m := NuevoParser(e).Parse([]byte("20240203;20200610;76;MASCULINO;Criterio SINADEF;LIMA;CAÑETE;CAÑETE;150101;ab"), &r); m != -1 {
		t.Fatalf("motivo %d", m)
	}
	if r.Label != 1 || r.Edad != 76 || r.Sexo != 1 || r.Provincia != "CAÑETE" {
		t.Errorf("registro: %+v", r)
	}
}

// escribirArchivo crea un CSV de prueba con n filas (algunas inválidas) y
// líneas de longitud variable para forzar cortes en medio de una línea.
func escribirArchivo(t *testing.T, n int, crlf, sinSaltoFinal bool) string {
	t.Helper()
	nl := "\n"
	if crlf {
		nl = "\r\n"
	}
	rng := rand.New(rand.NewSource(1))
	var sb strings.Builder
	sb.WriteString(headerPos + nl)
	for i := 0; i < n; i++ {
		edad := fmt.Sprint(rng.Intn(100))
		if i%17 == 0 {
			edad = ""
		}
		prov := strings.Repeat("X", 1+rng.Intn(30))
		sb.WriteString(fmt.Sprintf("20231231;%s;%s;D;PCR;%s;FEMENINO;2021%02d01;1;%d",
			Departamentos[i%25], prov, edad, 1+i%12, i))
		if i < n-1 || !sinSaltoFinal {
			sb.WriteString(nl)
		}
	}
	path := filepath.Join(t.TempDir(), "test.csv")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func firma(recs []Record) []string {
	s := make([]string, len(recs))
	for i, r := range recs {
		s[i] = fmt.Sprintf("%d|%d|%d|%d|%s", r.Edad, r.Dept, r.YYYYMM, r.Sexo, r.Provincia)
	}
	sort.Strings(s)
	return s
}

// El cargador concurrente debe producir exactamente el mismo conjunto de
// registros que el secuencial, para cualquier número de workers (incluidos
// casos donde los bordes de bloque caen en medio de una línea).
func TestConcurrenteIgualSecuencial(t *testing.T) {
	for _, cfg := range []struct{ crlf, sinSalto bool }{{false, false}, {true, false}, {false, true}} {
		path := escribirArchivo(t, 5000, cfg.crlf, cfg.sinSalto)
		ref, stRef, err := CargarSecuencial(path, Positivos)
		if err != nil {
			t.Fatal(err)
		}
		if stRef.Leidas != 5000 {
			t.Fatalf("secuencial leyó %d filas", stRef.Leidas)
		}
		fRef := firma(ref)
		for _, w := range []int{1, 2, 3, 4, 7, 8, 16, 64} {
			recs, st, err := CargarConcurrente(path, Positivos, w)
			if err != nil {
				t.Fatal(err)
			}
			if st.Leidas != stRef.Leidas || st.Validas != stRef.Validas || st.Descartes != stRef.Descartes {
				t.Fatalf("crlf=%v w=%d: stats %+v != %+v", cfg.crlf, w, st, stRef)
			}
			f := firma(recs)
			for i := range f {
				if f[i] != fRef[i] {
					t.Fatalf("crlf=%v w=%d: registro %d distinto", cfg.crlf, w, i)
				}
			}
		}
	}
}

func TestEDA(t *testing.T) {
	recs := []Record{
		{Edad: 85, Label: 0, Dept: 14, YYYYMM: 202101, Provincia: "LIMA"},
		{Edad: 85, Label: 0, Dept: 14, YYYYMM: 202101, Provincia: "LIMA"},
		{Edad: 85, Label: 1, Dept: 14, YYYYMM: 202101, Provincia: "LIMA"},
		{Edad: 20, Label: 0, Dept: 3, YYYYMM: 202101, Provincia: "AREQUIPA"},
	}
	e := CalcularEDA(recs)
	if e.Total[0] != 3 || e.Total[1] != 1 || e.Provincias != 2 {
		t.Fatalf("totales %+v provincias %d", e.Total, e.Provincias)
	}
	if e.Letalidad[7] != 50 {
		t.Errorf("letalidad 80+ = %v", e.Letalidad[7])
	}
}

func BenchmarkCargaConcurrente(b *testing.B) {
	path := "../../data/positivos_covid.csv"
	if _, err := os.Stat(path); err != nil {
		b.Skip("no existe data/positivos_covid.csv")
	}
	for _, w := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("workers=%d", w), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, _, err := CargarConcurrente(path, Positivos, w); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
