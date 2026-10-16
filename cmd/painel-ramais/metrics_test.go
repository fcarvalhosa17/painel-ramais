package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteMetricsAtomicoEModo(t *testing.T) {
	dir := t.TempDir()
	m := readMemMetrics(time.Unix(1000, 0))
	if err := writeMetrics(dir, m); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, metricsFile))
	if err != nil {
		t.Fatal(err)
	}
	var got memMetrics
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.PID != os.Getpid() || got.Updated != 1000 || got.HeapInuse == 0 {
		t.Fatalf("conteudo inesperado: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, metricsFile+".tmp")); err == nil {
		t.Fatal("sobrou arquivo temporario")
	}
	if fi, _ := os.Stat(filepath.Join(dir, metricsFile)); fi.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Fatalf("modo %v, esperado 0600", fi.Mode().Perm())
	}
}

func TestStartMetricsEscreveSoComRuntimeDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Setenv(envRuntimeDir, "")
	startMetrics(ctx, slog.Default()) // no-op: nao pode panicar nem escrever
	dir := t.TempDir()
	t.Setenv(envRuntimeDir, dir)
	startMetrics(ctx, slog.Default())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, metricsFile)); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("metrics.json nao foi escrito na primeira iteracao")
}
