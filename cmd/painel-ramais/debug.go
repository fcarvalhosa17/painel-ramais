//go:build debug

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"time"
)

// envDebugAddr liga o endpoint de diagnostico de memoria. Desligado por padrao
// (variavel ausente ou vazia). So aceita endereco de loopback; qualquer outro valor
// e recusado e o daemon segue sem o endpoint. Existe para medir o heap Go pos-GC
// numa VM de homologacao; compilado so com -tags debug (fora do binario de release).
const envDebugAddr = "PAINEL_DEBUG_ADDR"

// debugHandler serve /debug/mem (heap pos-GC em JSON) e /debug/pprof/heap?gc=1.
// Mux proprio: nada vai para o DefaultServeMux nem para a porta publica do painel.
func debugHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/mem", func(w http.ResponseWriter, _ *http.Request) {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]uint64{
			"heap_inuse": m.HeapInuse, "heap_alloc": m.HeapAlloc, "heap_sys": m.HeapSys,
			"heap_objects": m.HeapObjects, "num_gc": uint64(m.NumGC),
		})
	})
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap")) // ?gc=1 forca GC antes do perfil
	return mux
}

// debugListener abre o listener se addr for loopback; erro caso contrario.
func debugListener(addr string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, errors.New("so aceita IP de loopback literal (ex.: 127.0.0.1:6060)")
	}
	return net.Listen("tcp", addr)
}

// startDebug sobe o endpoint se a variavel estiver definida; encerra com ctx.
func startDebug(ctx context.Context, log *slog.Logger) {
	addr := os.Getenv(envDebugAddr)
	if addr == "" {
		return
	}
	ln, err := debugListener(addr)
	if err != nil {
		log.Warn("endpoint de debug recusado", "var", envDebugAddr, "erro", err.Error())
		return
	}
	srv := &http.Server{Handler: debugHandler(), ReadHeaderTimeout: 5 * time.Second}
	log.Warn("endpoint de debug de memoria LIGADO (so loopback)", "addr", ln.Addr().String())
	go func() { _ = srv.Serve(ln) }()
	go func() { <-ctx.Done(); _ = srv.Close() }()
}
