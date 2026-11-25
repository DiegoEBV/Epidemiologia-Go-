package api

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// JWT HS256 implementado con la biblioteca estándar (crypto/hmac + sha256).
// Formato: base64url(header).base64url(claims).base64url(HMAC-SHA256(...)).

// Claims del token.
type Claims struct {
	Sub string `json:"sub"`
	Rol string `json:"rol"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

var b64 = base64.RawURLEncoding

const cabeceraJWT = `{"alg":"HS256","typ":"JWT"}`

// FirmarJWT emite un token.
func FirmarJWT(secreto []byte, c Claims) (string, error) {
	cuerpo, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	msg := b64.EncodeToString([]byte(cabeceraJWT)) + "." + b64.EncodeToString(cuerpo)
	mac := hmac.New(sha256.New, secreto)
	mac.Write([]byte(msg))
	return msg + "." + b64.EncodeToString(mac.Sum(nil)), nil
}

var (
	errTokenInvalido = errors.New("token inválido")
	errTokenExpirado = errors.New("token expirado")
)

// VerificarJWT valida firma (en tiempo constante) y expiración.
func VerificarJWT(secreto []byte, token string) (*Claims, error) {
	partes := strings.Split(token, ".")
	if len(partes) != 3 {
		return nil, errTokenInvalido
	}
	cab, err := b64.DecodeString(partes[0])
	if err != nil || string(cab) != cabeceraJWT {
		return nil, errTokenInvalido
	}
	mac := hmac.New(sha256.New, secreto)
	mac.Write([]byte(partes[0] + "." + partes[1]))
	firma, err := b64.DecodeString(partes[2])
	if err != nil || !hmac.Equal(firma, mac.Sum(nil)) {
		return nil, errTokenInvalido
	}
	cuerpo, err := b64.DecodeString(partes[1])
	if err != nil {
		return nil, errTokenInvalido
	}
	var c Claims
	if err := json.Unmarshal(cuerpo, &c); err != nil {
		return nil, errTokenInvalido
	}
	if time.Now().Unix() >= c.Exp {
		return nil, errTokenExpirado
	}
	return &c, nil
}

// Contraseñas: PBKDF2-HMAC-SHA256 con sal aleatoria de 16 bytes. El número
// de iteraciones se guarda junto al hash para poder subirlo en el futuro sin
// invalidar cuentas existentes. 60 000 iteraciones cuestan ~35 ms: frenan la
// fuerza bruta y mantienen el login por debajo de los 100 ms exigidos.
const IterPBKDF2 = 60_000

// HashPassword devuelve (hash, sal) en hexadecimal, con IterPBKDF2 iteraciones.
func HashPassword(pass string) (string, string, error) {
	sal := make([]byte, 16)
	if _, err := rand.Read(sal); err != nil {
		return "", "", err
	}
	k, err := pbkdf2.Key(sha256.New, pass, sal, IterPBKDF2, 32)
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(k), hex.EncodeToString(sal), nil
}

// VerificarPassword compara en tiempo constante. iter = 0 significa el valor
// usado antes de guardar las iteraciones (120 000).
func VerificarPassword(pass, hashHex, salHex string, iter int) bool {
	if iter <= 0 {
		iter = 120_000
	}
	sal, err := hex.DecodeString(salHex)
	if err != nil {
		return false
	}
	esperado, err := hex.DecodeString(hashHex)
	if err != nil {
		return false
	}
	k, err := pbkdf2.Key(sha256.New, pass, sal, iter, 32)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(k, esperado) == 1
}

type ctxClave struct{}

// usuarioDe devuelve los claims guardados por el middleware.
func usuarioDe(ctx context.Context) *Claims {
	c, _ := ctx.Value(ctxClave{}).(*Claims)
	return c
}

// tokenDe acepta Bearer y, en WebSocket, el subprotocolo de la SPA o la
// consulta usada por los clientes de PC4.
func tokenDe(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if r.URL.Path == "/ws" {
		for _, value := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
			if token, ok := strings.CutPrefix(strings.TrimSpace(value), "jwt."); ok {
				return token
			}
		}
		return r.URL.Query().Get("token")
	}
	return ""
}

// conAuth exige un JWT válido y, si se indican, alguno de los roles.
func (s *Servidor) conAuth(h http.HandlerFunc, roles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := VerificarJWT(s.cfg.SecretoJWT, tokenDe(r))
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
			responderError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if len(roles) > 0 {
			ok := false
			for _, rol := range roles {
				if c.Rol == rol {
					ok = true
				}
			}
			if !ok {
				responderError(w, http.StatusForbidden, "el rol "+c.Rol+" no tiene permiso para esta operación")
				return
			}
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxClave{}, c)))
	}
}
