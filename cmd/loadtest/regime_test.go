package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A amostra final de heap nao pode vir logo depois do GET /api/snapshot: o cache do
// snapshot (~3 MiB) fica residente e a regressao ve um degrau que nao e vazamento.
// O daemon falso soma 3 MiB ao heap enquanto o cache existe; um "batch" a cada 40 ms o zera.
func TestRegimeAmostraFinalNaoCarregaCacheDoSnapshot(t *testing.T) {
	var cached atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/snapshot", func(w http.ResponseWriter, _ *http.Request) {
		cached.Store(true)
		_, _ = w.Write([]byte(`{"success":true,"data":{"extensions":[{"number":"2001","name":"Ramal 2001"}]}}`))
	})
	mux.HandleFunc("/debug/mem", func(w http.ResponseWriter, _ *http.Request) {
		b := uint64(100 * mib)
		if cached.Load() {
			b += uint64(3 * mib)
		}
		_, _ = w.Write([]byte(`{"heap_inuse":` + strconv.FormatUint(b, 10) + `,"heap_alloc":` + strconv.FormatUint(b, 10) + `}`))
	})
	mux.HandleFunc("/debug/pprof/heap", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("pb")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(40 * time.Millisecond):
				cached.Store(false)
			}
		}
	}()

	oldH, oldR := heapEvery, regimeEvery
	heapEvery, regimeEvery = 150*time.Millisecond, 150*time.Millisecond
	defer func() { heapEvery, regimeEvery = oldH, oldR }()

	addr := strings.TrimPrefix(srv.URL, "http://")
	x := &runner{
		o:    opts{heap: true, warmup: 0, duration: 3 * time.Second, fillTimeout: time.Minute, outDir: t.TempDir()},
		r:    &Result{Mode: "sip", Phases: map[string]time.Duration{}},
		d:    &Daemon{addr: addr, dbg: addr, hc: srv.Client()},
		trk:  NewTracker(),
		logf: func(string, ...any) {},
	}
	x.regime()
	if h := x.r.Heap; h.Verdict != "APROVA" {
		t.Fatalf("heap plano com snapshot no fim deveria APROVAR: %+v", h)
	}
}
