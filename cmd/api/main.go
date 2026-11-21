// api es el servidor REST/WebSocket y coordinador del clúster ML (PC4).
//
// Al arrancar: conecta a MongoDB y Redis, siembra los usuarios, espera a los
// nodos, carga los CSV del MINSA con el cargador concurrente, entrena el
// modelo de forma distribuida (o reutiliza el guardado) y sirve la API.
//
// Configuración por variables de entorno (ver docker-compose.yml).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/api"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/cluster"
	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/store"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetPrefix("[api] ")
	dir := env("DATA_DIR", "data")
	meses, _ := strconv.Atoi(env("MESES_IMPACTO", "4"))
	epocas, _ := strconv.Atoi(env("EPOCAS", "6"))
	cfg := api.Config{
		Puerto:              env("PORT", "8080"),
		MongoURI:            env("MONGO_URI", "mongodb://localhost:27017"),
		MongoDB:             env("MONGO_DB", "epidemiologia"),
		RedisAddr:           env("REDIS_ADDR", "localhost:6379"),
		Nodos:               strings.Split(env("NODES", "localhost:9001,localhost:9002"), ","),
		ArchPositivos:       env("POSITIVOS_CSV", dir+"/positivos_covid.csv"),
		ArchFallecidos:      env("FALLECIDOS_CSV", dir+"/fallecidos_covid.csv"),
		SecretoJWT:          []byte(env("JWT_SECRET", "cambiar-este-secreto-en-produccion")),
		AdminUser:           env("ADMIN_USER", "admin"),
		AdminPass:           env("ADMIN_PASS", "admin123"),
		AnalistaUser:        env("ANALISTA_USER", "analista"),
		AnalistaPass:        env("ANALISTA_PASS", "analista123"),
		ForzarEntrenamiento: env("FORCE_TRAIN", "false") == "true",
		MesesImpacto:        meses,
		Epocas:              epocas,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mongo, err := store.ConectarMongo(ctx, cfg.MongoURI, cfg.MongoDB, 60*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer mongo.Cerrar(context.Background())
	if err := mongo.CrearIndices(ctx); err != nil {
		log.Fatal(err)
	}
	redis, err := store.ConectarRedis(ctx, cfg.RedisAddr, 60*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("MongoDB (%s) y Redis (%s) conectados", cfg.MongoDB, cfg.RedisAddr)

	cl := cluster.NuevoCluster(cfg.Nodos)
	hub := api.NuevoHub()
	app := api.NuevaApp(ctx, cfg, mongo, redis, cl, hub)
	if err := app.SembrarUsuarios(ctx); err != nil {
		log.Fatal(err)
	}
	srv := api.NuevoServidor(cfg, app, hub)
	srv.Correr()
	go app.Inicializar()

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Puerto,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
	}()
	log.Printf("API escuchando en :%s (Swagger en /swagger/) · nodos %v", cfg.Puerto, cfg.Nodos)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
