package api

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/data"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/ml"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/store"
)

var secreto = []byte("secreto-de-prueba")

func TestJWT(t *testing.T) {
	ahora := time.Now()
	tok, err := FirmarJWT(secreto, Claims{Sub: "admin", Rol: "admin", Iat: ahora.Unix(), Exp: ahora.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	c, err := VerificarJWT(secreto, tok)
	if err != nil || c.Sub != "admin" || c.Rol != "admin" {
		t.Fatalf("token válido rechazado: %v %+v", err, c)
	}
	if _, err := VerificarJWT([]byte("otro"), tok); err == nil {
		t.Fatal("aceptó un token firmado con otro secreto")
	}
	// Alterar el payload (p. ej. subir el rol) invalida la firma.
	p := strings.Split(tok, ".")
	falso, _ := FirmarJWT([]byte("x"), Claims{Sub: "admin", Rol: "admin", Exp: ahora.Add(time.Hour).Unix()})
	p[1] = strings.Split(falso, ".")[1]
	if _, err := VerificarJWT(secreto, strings.Join(p, ".")); err == nil {
		t.Fatal("aceptó un payload alterado")
	}
	exp, _ := FirmarJWT(secreto, Claims{Sub: "a", Rol: "admin", Exp: ahora.Add(-time.Second).Unix()})
	if _, err := VerificarJWT(secreto, exp); err != errTokenExpirado {
		t.Fatalf("token expirado: %v", err)
	}
	if _, err := VerificarJWT(secreto, "basura"); err == nil {
		t.Fatal("aceptó un token mal formado")
	}
}

func TestPassword(t *testing.T) {
	h, s, err := HashPassword("clave-segura")
	if err != nil {
		t.Fatal(err)
	}
	if !VerificarPassword("clave-segura", h, s, IterPBKDF2) {
		t.Fatal("rechazó la contraseña correcta")
	}
	if VerificarPassword("otra", h, s, IterPBKDF2) {
		t.Fatal("aceptó una contraseña incorrecta")
	}
	// Hashes antiguos sin iteraciones guardadas (120 000).
	sal := []byte("0123456789abcdef")
	k, _ := pbkdf2.Key(sha256.New, "vieja", sal, 120_000, 32)
	if !VerificarPassword("vieja", hex.EncodeToString(k), hex.EncodeToString(sal), 0) {
		t.Fatal("no verificó un hash con el número de iteraciones anterior")
	}
}

func TestValidacionEntrada(t *testing.T) {
	casos := []struct {
		e    store.EntradaPrediccion
		ok   bool
		ola  int8
		sexo int8
	}{
		{store.EntradaPrediccion{Edad: 70, Sexo: "masculino", Departamento: "lima"}, true, data.PostOlas, 1},
		{store.EntradaPrediccion{Edad: 30, Sexo: "F", Departamento: "Junín", Fecha: "2021-01-10"}, true, data.Ola2, 0},
		{store.EntradaPrediccion{Edad: 5, Sexo: "F", Departamento: "CALLAO", Fecha: "2020-05-01"}, true, data.Ola1, 0},
		{store.EntradaPrediccion{Edad: -1, Sexo: "F", Departamento: "LIMA"}, false, 0, 0},
		{store.EntradaPrediccion{Edad: 111, Sexo: "F", Departamento: "LIMA"}, false, 0, 0},
		{store.EntradaPrediccion{Edad: 40, Sexo: "X", Departamento: "LIMA"}, false, 0, 0},
		{store.EntradaPrediccion{Edad: 40, Sexo: "M", Departamento: "ATLANTIDA"}, false, 0, 0},
		{store.EntradaPrediccion{Edad: 40, Sexo: "M", Departamento: "LIMA", Fecha: "15/04/2021"}, false, 0, 0},
	}
	for i, c := range casos {
		r, err := aRecord(&c.e)
		if (err == nil) != c.ok {
			t.Fatalf("caso %d: err=%v, esperaba ok=%v", i, err, c.ok)
		}
		if c.ok && (r.Ola != c.ola || r.Sexo != c.sexo) {
			t.Fatalf("caso %d: ola %d sexo %d", i, r.Ola, r.Sexo)
		}
	}
}

func TestNivelYCalculo(t *testing.T) {
	m := ml.NuevoModelo(40, 18, 20)
	for i := range m.W {
		m.W[i] = 0.1
	}
	m.W[0], m.B = 1.5, -2
	ma := &modeloActivo{Doc: &store.Modelo{Version: 3}, M: m}
	joven := data.Record{Edad: 20, Sexo: 0, Dept: 14, Ola: data.PostOlas}
	mayor := data.Record{Edad: 85, Sexo: 1, Dept: 14, Ola: data.Ola1}
	rj := calcular(ma, store.EntradaPrediccion{}, &joven)
	rm := calcular(ma, store.EntradaPrediccion{}, &mayor)
	if rm.Letalidad <= rj.Letalidad || rm.ProbabilidadClas <= rj.ProbabilidadClas {
		t.Fatalf("el riesgo no crece con la edad: joven %.4f mayor %.4f", rj.Letalidad, rm.Letalidad)
	}
	if rm.ModeloVersion != 3 || len(rm.Factores) != 4 || rm.Letalidad > 1 {
		t.Fatalf("respuesta incompleta: %+v", rm)
	}
	for _, c := range []struct {
		l float64
		n string
	}{{0.001, "BAJO"}, {0.02, "MODERADO"}, {0.1, "ALTO"}, {0.4, "MUY ALTO"}} {
		if n, _ := nivelDe(c.l); n != c.n {
			t.Fatalf("nivel de %.3f = %s, esperaba %s", c.l, n, c.n)
		}
	}
}

func TestPercentiles(t *testing.T) {
	r := NuevoRegistroLatencias()
	for i := 1; i <= 100; i++ {
		r.Registrar("GET /x", time.Duration(i)*time.Millisecond, 200)
	}
	r.Registrar("GET /x", 300*time.Millisecond, 500)
	res := r.Resumen()
	if len(res) != 1 {
		t.Fatal(res)
	}
	x := res[0]
	if x.Peticiones != 101 || x.Errores5xx != 1 || x.MaxMs != 300 || x.P50Ms != 51 || x.Bajo100ms < 98 {
		t.Fatalf("resumen inesperado: %+v", x)
	}
}

// Middleware de autenticación y rutas públicas sin depender de Mongo/Redis.
func TestRutasYAuth(t *testing.T) {
	cfg := Config{SecretoJWT: secreto}
	s := &Servidor{cfg: cfg, lat: NuevoRegistroLatencias(), mux: http.NewServeMux()}
	ok := func(w http.ResponseWriter, r *http.Request) {
		responderJSON(w, 200, map[string]string{"u": usuarioDe(r.Context()).Sub})
	}
	s.mux.HandleFunc("GET /protegido", s.conAuth(ok))
	s.mux.HandleFunc("POST /admin", s.conAuth(ok, "admin"))
	s.mux.HandleFunc("GET /swagger/", s.swagger)
	h := s.Handler()

	pedir := func(metodo, ruta, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(metodo, ruta, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	exp := time.Now().Add(time.Hour).Unix()
	admin, _ := FirmarJWT(secreto, Claims{Sub: "admin", Rol: "admin", Exp: exp})
	analista, _ := FirmarJWT(secreto, Claims{Sub: "ana", Rol: "analista", Exp: exp})

	if c := pedir("GET", "/protegido", "").Code; c != 401 {
		t.Fatalf("sin token: %d", c)
	}
	if rec := pedir("GET", "/protegido", analista); rec.Code != 200 || rec.Header().Get("X-Response-Time") == "" {
		t.Fatalf("con token: %d %v", rec.Code, rec.Header())
	}
	if c := pedir("POST", "/admin", analista).Code; c != 403 {
		t.Fatalf("analista en ruta admin: %d", c)
	}
	if c := pedir("POST", "/admin", admin).Code; c != 200 {
		t.Fatalf("admin: %d", c)
	}
	req := httptest.NewRequest(http.MethodOptions, "/admin", nil)
	req.Header.Set("Origin", "https://example.invalid")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight := httptest.NewRecorder()
	h.ServeHTTP(preflight, req)
	if preflight.Code != http.StatusMethodNotAllowed || preflight.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("preflight de un origen ajeno: %d %v", preflight.Code, preflight.Header())
	}
	rec := pedir("GET", "/swagger/openapi.yaml", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "openapi: 3.0.3") {
		t.Fatalf("swagger: %d", rec.Code)
	}
}
