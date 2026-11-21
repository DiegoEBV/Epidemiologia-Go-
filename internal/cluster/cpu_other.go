//go:build !unix

package cluster

import "time"

// tiempoCPU no está disponible fuera de sistemas Unix; el nodo reporta 0 %.
func tiempoCPU() time.Duration { return 0 }
