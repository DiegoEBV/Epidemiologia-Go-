//go:build unix

package cluster

import (
	"syscall"
	"time"
)

// tiempoCPU devuelve el tiempo de CPU (usuario + sistema) consumido por el proceso.
func tiempoCPU() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
