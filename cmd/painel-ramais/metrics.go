package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"time"
)

// envRuntimeDir e definida pelo systemd quando a unit tem RuntimeDirectory= (/run/painel-ramais).
// Sem ela (execucao fora do systemd) o arquivo de metricas nao e escrito.
const (
	envRuntimeDir  = "RUNTIME_DIRECTORY"
	metricsFile    = "metrics.json"
	metricsEvery   = 30 * time.Second
	heapLiveMetric = "/gc/heap/live:bytes"
)

// memMetrics e o conteudo de metrics.json, lido por `instalar.py status`.
// Nao ha porta nem socket: o arquivo fica em /run/painel-ramais (0750, dono painel-ramais), modo 0600.
type memMetrics struct {
	PID       int    `json:"pid"`
	Updated   int64  `json:"updated"`    // epoch, segundos
	HeapLive  uint64 `json:"heap_live"`  // bytes vivos apos o ultimo GC (sem forcar GC)
	HeapInuse uint64 `json:"heap_inuse"` // bytes em spans em uso
}

// readMemMetrics le o heap vivo do ultimo GC natural; nao forca GC (custo zero para o daemon).
func readMemMetrics(now time.Time) memMetrics {
	s := []metrics.Sample{{Name: heapLiveMetric}}
	metrics.Read(s)
	var live uint64
	if s[0].Value.Kind() == metrics.KindUint64 {
		live = s[0].Value.Uint64()
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return memMetrics{PID: os.Getpid(), Updated: now.Unix(), HeapLive: live, HeapInuse: m.HeapInuse}
}

// writeMetrics grava de forma atomica (temp + rename) com modo 0600.
func writeMetrics(dir string, m memMetrics) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, metricsFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, metricsFile))
}

// startMetrics escreve o arquivo a cada metricsEvery ate ctx terminar. No-op sem RUNTIME_DIRECTORY.
func startMetrics(ctx context.Context, log *slog.Logger) {
	dir := os.Getenv(envRuntimeDir)
	if dir == "" {
		return
	}
	go func() {
		t := time.NewTicker(metricsEvery)
		defer t.Stop()
		for {
			if err := writeMetrics(dir, readMemMetrics(time.Now())); err != nil {
				log.Warn("metricas de memoria nao gravadas", "erro", err.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
