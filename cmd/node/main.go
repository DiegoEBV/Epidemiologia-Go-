// node es un nodo de cómputo del clúster ML (PC4). Escucha en un puerto TCP,
// recibe su fragmento de datos del coordinador y responde gradientes y
// puntuaciones de impacto.
//
// Uso:
//
//	NODE_ID=node1 go run ./cmd/node -addr :9000
package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/UPC-CC65-202620/tf-caso1-epidemiologia-go/internal/cluster"
)

func main() {
	addr := flag.String("addr", envOr("NODE_ADDR", ":9000"), "dirección TCP de escucha")
	id := flag.String("id", envOr("NODE_ID", hostname()), "identificador del nodo")
	flag.Parse()

	logger := log.New(os.Stdout, "["+*id+"] ", log.Ltime|log.Lmicroseconds)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		logger.Fatal(err)
	}
	logger.Printf("CPUs %d · GOMAXPROCS %d · %s", runtime.NumCPU(), runtime.GOMAXPROCS(0), runtime.Version())

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		logger.Print("apagando nodo")
		ln.Close()
	}()
	if err := cluster.NuevoNodo(*id, logger).Servir(ln); err != nil {
		logger.Fatal(err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "node"
	}
	return h
}
