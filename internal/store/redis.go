package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Claves de Redis (nivel caché / colaborativo).
//
//	modelo:activo                 JSON del modelo activo (lo leen las réplicas de la API al arrancar)
//	pred:v{ver}:{edad}:{sexo}:{dept}:{ola}  respuesta de predicción ya calculada (TTL 1 h)
//	impacto:v{ver}:{dept}:{n}     ranking de impacto precalculado (TTL 10 min)
//	cluster:nodo:{id}             último latido de cada nodo (TTL 15 s: si expira, el nodo está caído)
//	stats:predicciones            contador global de predicciones (INCR atómico)
//	stats:cache_hits              contador de aciertos de caché
const (
	KeyModeloActivo   = "modelo:activo"
	KeyStatsPred      = "stats:predicciones"
	KeyStatsCacheHits = "stats:cache_hits"
	CanalEventos      = "eventos" // pub/sub: progreso de entrenamiento y latidos
	TTLPrediccion     = time.Hour
	TTLImpacto        = 10 * time.Minute
	TTLLatido         = 15 * time.Second
)

// KeyPrediccion arma la clave de caché de una predicción.
func KeyPrediccion(version, edad, sexo, dept, ola int) string {
	return fmt.Sprintf("pred:v%d:%d:%d:%d:%d", version, edad, sexo, dept, ola)
}

// KeyImpacto arma la clave del ranking precalculado.
func KeyImpacto(version int, dept string, n int) string {
	return fmt.Sprintf("impacto:v%d:%s:%d", version, dept, n)
}

// KeyNodo arma la clave del latido de un nodo.
func KeyNodo(id string) string { return "cluster:nodo:" + id }

// Redis envuelve el cliente con helpers JSON.
type Redis struct {
	C *redis.Client
}

// ErrCacheMiss indica que la clave no está en caché.
var ErrCacheMiss = errors.New("cache miss")

// ConectarRedis abre el cliente y reintenta hasta que responda.
func ConectarRedis(ctx context.Context, addr string, plazo time.Duration) (*Redis, error) {
	c := redis.NewClient(&redis.Options{Addr: addr, PoolSize: 50, MinIdleConns: 4,
		ReadTimeout: 500 * time.Millisecond, WriteTimeout: 500 * time.Millisecond})
	limite := time.Now().Add(plazo)
	for {
		cc, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := c.Ping(cc).Err()
		cancel()
		if err == nil {
			return &Redis{C: c}, nil
		}
		if time.Now().After(limite) {
			return nil, fmt.Errorf("redis no responde en %s: %w", addr, err)
		}
		time.Sleep(time.Second)
	}
}

// GetJSON lee y decodifica una clave.
func (r *Redis) GetJSON(ctx context.Context, key string, v any) error {
	b, err := r.C.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrCacheMiss
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// SetJSON codifica y guarda con TTL (0 = sin expiración).
func (r *Redis) SetJSON(ctx context.Context, key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return r.C.Set(ctx, key, b, ttl).Err()
}

// BorrarPatron elimina las claves que coinciden (p. ej. al cambiar de modelo).
func (r *Redis) BorrarPatron(ctx context.Context, patron string) (int, error) {
	n := 0
	iter := r.C.Scan(ctx, 0, patron, 500).Iterator()
	for iter.Next(ctx) {
		if err := r.C.Del(ctx, iter.Val()).Err(); err == nil {
			n++
		}
	}
	return n, iter.Err()
}

// Publicar envía un evento al canal pub/sub.
func (r *Redis) Publicar(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return r.C.Publish(ctx, CanalEventos, b).Err()
}
