// gensample genera una muestra SINTÉTICA con el mismo esquema (columnas,
// separador ';' y formatos de fecha) que los CSV del MINSA. Sirve para
// probar el pipeline y medir rendimiento sin depender de la descarga.
// Incluye "suciedad" controlada (edades vacías, sexos inválidos, etc.) para
// validar las reglas de limpieza. Los resultados obtenidos con esta muestra
// NO describen la epidemia real.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
)

type dep struct {
	nombre string
	peso   float64
	provs  []string
	efecto float64
}

var deps = []dep{
	{"LIMA", 0.43, []string{"LIMA", "HUAURA", "CAÑETE", "HUARAL", "BARRANCA"}, 0},
	{"CALLAO", 0.04, []string{"CALLAO"}, 0.05},
	{"AREQUIPA", 0.055, []string{"AREQUIPA", "CAYLLOMA", "ISLAY", "CAMANA"}, -0.1},
	{"LA LIBERTAD", 0.04, []string{"TRUJILLO", "PACASMAYO", "ASCOPE", "CHEPEN"}, 0.15},
	{"PIURA", 0.035, []string{"PIURA", "SULLANA", "TALARA", "PAITA"}, 0.25},
	{"JUNIN", 0.035, []string{"HUANCAYO", "TARMA", "CHANCHAMAYO", "SATIPO"}, 0.05},
	{"CUSCO", 0.035, []string{"CUSCO", "LA CONVENCION", "CANCHIS", "URUBAMBA"}, -0.2},
	{"ANCASH", 0.03, []string{"SANTA", "HUARAZ", "HUARI"}, 0.2},
	{"LAMBAYEQUE", 0.028, []string{"CHICLAYO", "LAMBAYEQUE", "FERREÑAFE"}, 0.3},
	{"ICA", 0.028, []string{"ICA", "CHINCHA", "PISCO", "NAZCA"}, 0.25},
	{"CAJAMARCA", 0.025, []string{"CAJAMARCA", "JAEN", "CHOTA"}, -0.05},
	{"PUNO", 0.02, []string{"PUNO", "SAN ROMAN", "AZANGARO"}, 0.1},
	{"SAN MARTIN", 0.018, []string{"MOYOBAMBA", "SAN MARTIN", "RIOJA"}, 0.0},
	{"LORETO", 0.018, []string{"MAYNAS", "ALTO AMAZONAS"}, 0.1},
	{"UCAYALI", 0.014, []string{"CORONEL PORTILLO", "PADRE ABAD"}, 0.05},
	{"AYACUCHO", 0.014, []string{"HUAMANGA", "HUANTA", "LUCANAS"}, -0.1},
	{"HUANUCO", 0.014, []string{"HUANUCO", "LEONCIO PRADO"}, 0.05},
	{"TACNA", 0.014, []string{"TACNA", "JORGE BASADRE"}, -0.1},
	{"MOQUEGUA", 0.01, []string{"MARISCAL NIETO", "ILO"}, -0.15},
	{"APURIMAC", 0.01, []string{"ABANCAY", "ANDAHUAYLAS"}, -0.05},
	{"TUMBES", 0.008, []string{"TUMBES", "ZARUMILLA"}, 0.2},
	{"AMAZONAS", 0.008, []string{"CHACHAPOYAS", "BAGUA", "UTCUBAMBA"}, 0.0},
	{"HUANCAVELICA", 0.007, []string{"HUANCAVELICA", "TAYACAJA"}, 0.1},
	{"PASCO", 0.006, []string{"PASCO", "OXAPAMPA"}, 0.05},
	{"MADRE DE DIOS", 0.006, []string{"TAMBOPATA"}, 0.1},
}

// Peso relativo de casos por mes (aprox. forma de las olas en Perú).
func pesoMes(y, m int) float64 {
	t := float64((y-2020)*12 + m - 3) // meses desde mar-2020
	g := func(c, w, h float64) float64 { return h * math.Exp(-(t-c)*(t-c)/(2*w*w)) }
	return 0.05 + g(5, 2.0, 1.0) + g(13, 2.0, 1.4) + g(22.5, 1.0, 2.2) + g(28, 1.5, 0.4) + g(33, 1.5, 0.2)
}

func olaEfecto(y, m int) float64 {
	ym := y*100 + m
	switch {
	case ym <= 202011:
		return 0.35
	case ym <= 202110:
		return 0.45
	case ym <= 202204:
		return -1.5
	default:
		return -1.9
	}
}

func main() {
	n := flag.Int("n", 2_500_000, "número de casos positivos a generar")
	out := flag.String("out", "data", "directorio de salida")
	seed := flag.Int64("seed", 7, "semilla")
	flag.Parse()
	rng := rand.New(rand.NewSource(*seed))
	if err := os.MkdirAll(*out, 0o755); err != nil {
		panic(err)
	}

	// Tabla acumulada de meses mar-2020 .. dic-2023.
	type mes struct{ y, m int }
	var meses []mes
	var acum []float64
	tot := 0.0
	for y := 2020; y <= 2023; y++ {
		for m := 1; m <= 12; m++ {
			if y == 2020 && m < 3 {
				continue
			}
			tot += pesoMes(y, m)
			meses = append(meses, mes{y, m})
			acum = append(acum, tot)
		}
	}
	elegirMes := func() mes {
		x := rng.Float64() * tot
		lo, hi := 0, len(acum)-1
		for lo < hi {
			mid := (lo + hi) / 2
			if acum[mid] < x {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return meses[lo]
	}
	var depAcum []float64
	t := 0.0
	for _, d := range deps {
		t += d.peso
		depAcum = append(depAcum, t)
	}
	elegirDep := func() int {
		x := rng.Float64() * t
		for i, a := range depAcum {
			if x <= a {
				return i
			}
		}
		return len(deps) - 1
	}
	edad := func() int {
		// Mezcla: adultos jóvenes dominan, cola de adultos mayores.
		var e float64
		switch u := rng.Float64(); {
		case u < 0.10:
			e = rng.Float64() * 18
		case u < 0.80:
			e = 18 + math.Abs(rng.NormFloat64())*22
		default:
			e = 55 + math.Abs(rng.NormFloat64())*16
		}
		if e > 104 {
			e = 104
		}
		return int(e)
	}

	fp, _ := os.Create(filepath.Join(*out, "positivos_covid.csv"))
	ff, _ := os.Create(filepath.Join(*out, "fallecidos_covid.csv"))
	defer fp.Close()
	defer ff.Close()
	wp := bufio.NewWriterSize(fp, 1<<20)
	wf := bufio.NewWriterSize(ff, 1<<20)
	defer wp.Flush()
	defer wf.Flush()
	fmt.Fprintln(wp, "FECHA_CORTE;DEPARTAMENTO;PROVINCIA;DISTRITO;METODODX;EDAD;SEXO;FECHA_RESULTADO;UBIGEO;id_persona")
	fmt.Fprintln(wf, "FECHA_CORTE;FECHA_FALLECIMIENTO;EDAD_DECLARADA;SEXO;CLASIFICACION_DEF;DEPARTAMENTO;PROVINCIA;DISTRITO;UBIGEO;UUID")
	metodos := []string{"PCR", "PR", "AG"}
	criterios := []string{"Criterio virológico", "Criterio SINADEF", "Criterio serológico", "Criterio investigación Epidemiológica", "Criterio radiológico"}
	muertes := 0
	for i := 0; i < *n; i++ {
		di := elegirDep()
		d := deps[di]
		prov := d.provs[rng.Intn(len(d.provs))]
		ms := elegirMes()
		dia := 1 + rng.Intn(28)
		e := edad()
		sexo := "MASCULINO"
		masc := 1.0
		if rng.Float64() < 0.49 {
			sexo, masc = "FEMENINO", 0
		}
		// Probabilidad de fallecer (modelo generador "verdadero").
		z := -8.4 + 0.098*float64(e) + 0.45*masc + olaEfecto(ms.y, ms.m) + d.efecto
		muere := rng.Float64() < 1/(1+math.Exp(-z))

		edadTxt := fmt.Sprint(e)
		sexoTxt, depTxt := sexo, d.nombre
		fecha := fmt.Sprintf("%d%02d%02d", ms.y, ms.m, dia)
		// Suciedad controlada (solo en positivos).
		switch u := rng.Float64(); {
		case u < 0.004:
			edadTxt = ""
		case u < 0.005:
			edadTxt = fmt.Sprint(111 + rng.Intn(80))
		case u < 0.007:
			sexoTxt = ""
		case u < 0.010:
			depTxt = "EN INVESTIGACIÓN"
		case u < 0.011:
			fecha = ""
		}
		fmt.Fprintf(wp, "20231231;%s;%s;%s;%s;%s;%s;%s;%06d;%d\n",
			depTxt, prov, prov, metodos[rng.Intn(3)], edadTxt, sexoTxt, fecha, 150101+di*100, 10_000_000+i)
		if muere {
			muertes++
			// Fallece entre 0 y 25 días después del resultado.
			dm := dia + rng.Intn(26)
			my, mm := ms.y, ms.m
			if dm > 28 {
				dm -= 28
				mm++
				if mm > 12 {
					mm, my = 1, my+1
				}
			}
			edadF := fmt.Sprint(e)
			if rng.Float64() < 0.002 {
				edadF = ""
			}
			fmt.Fprintf(wf, "20240203;%d%02d%02d;%s;%s;%s;%s;%s;%s;%06d;%x\n",
				my, mm, dm, edadF, sexo, criterios[rng.Intn(len(criterios))], d.nombre, prov, prov, 150101+di*100, rng.Uint64())
		}
	}
	fmt.Printf("positivos: %d filas, fallecidos: %d filas (%.2f%%)\n", *n, muertes, 100*float64(muertes)/float64(*n))
}
