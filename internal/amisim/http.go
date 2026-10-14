package amisim

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Control agrupa o estado de controle exposto pelo endpoint HTTP exclusivo do simulador.
type Control struct {
	mu        sync.Mutex
	lastBurst []string
	cancel    context.CancelFunc
	wg        *sync.WaitGroup
}

// Handler devolve o endpoint HTTP do simulador (nao e parte do daemon):
//
//	GET  /truth            verdade do estado (todos os ramais)
//	GET  /actions          contagem de actions recebidas e violacoes da allowlist
//	GET  /status           resumo (modo, ramais por registro, chamadas)
//	POST /ctl/burst?n=3000 desregistra n ramais de uma vez
//	POST /ctl/restore      registra de volta os ramais da ultima rajada
//	POST /ctl/down?d=3s    derruba o AMI (porta recusada) por d
//	POST /ctl/kick         derruba as conexoes AMI, listener continua
//	POST /ctl/load?calls=400&talk=20s   liga carga de chamadas
//	POST /ctl/stopload     para a carga e derruba as chamadas
func (s *Sim) Handler() http.Handler {
	ctl := &Control{}
	mux := http.NewServeMux()
	js := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	post := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "use POST", http.StatusMethodNotAllowed)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/truth", func(w http.ResponseWriter, r *http.Request) {
		js(w, map[string]any{"mode": s.cfg.Mode, "extensions": s.Truth()})
	})
	mux.HandleFunc("/actions", func(w http.ResponseWriter, r *http.Request) {
		counts, viol := s.Actions()
		js(w, map[string]any{"counts": counts, "violations": viol, "allowlist_only": len(viol) == 0})
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		reg, inCall, calls := s.Counts()
		js(w, map[string]any{"mode": s.cfg.Mode, "registration": reg, "ext_in_call": inCall, "ext_calls": calls, "channels": s.ActiveChannels()})
	})
	mux.HandleFunc("/ctl/burst", post(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n <= 0 {
			n = 3000
		}
		nums := s.Burst(n)
		ctl.mu.Lock()
		ctl.lastBurst = nums
		ctl.mu.Unlock()
		js(w, map[string]int{"unregistered": len(nums)})
	}))
	mux.HandleFunc("/ctl/restore", post(func(w http.ResponseWriter, r *http.Request) {
		ctl.mu.Lock()
		nums := ctl.lastBurst
		ctl.lastBurst = nil
		ctl.mu.Unlock()
		s.Restore(nums)
		js(w, map[string]int{"restored": len(nums)})
	}))
	mux.HandleFunc("/ctl/down", post(func(w http.ResponseWriter, r *http.Request) {
		d, err := time.ParseDuration(r.URL.Query().Get("d"))
		if err != nil || d <= 0 {
			d = 3 * time.Second
		}
		if err := s.Down(d); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		js(w, map[string]string{"down_for": d.String()})
	}))
	mux.HandleFunc("/ctl/kick", post(func(w http.ResponseWriter, r *http.Request) {
		s.KickAll()
		js(w, map[string]bool{"kicked": true})
	}))
	mux.HandleFunc("/ctl/load", post(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		n, _ := strconv.Atoi(q.Get("calls"))
		if n <= 0 {
			n = 400
		}
		tm := DefaultTiming
		if d, err := time.ParseDuration(q.Get("talk")); err == nil && d > 0 {
			tm.TalkMin, tm.TalkMax = d/2, d*3/2
		}
		ctl.mu.Lock()
		defer ctl.mu.Unlock()
		if ctl.cancel != nil {
			http.Error(w, "carga ja ligada", http.StatusConflict)
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		ctl.cancel, ctl.wg = cancel, s.StartLoad(ctx, n, tm)
		js(w, map[string]int{"workers": n})
	}))
	mux.HandleFunc("/ctl/stopload", post(func(w http.ResponseWriter, r *http.Request) {
		ctl.mu.Lock()
		cancel, wg := ctl.cancel, ctl.wg
		ctl.cancel, ctl.wg = nil, nil
		ctl.mu.Unlock()
		if cancel != nil {
			cancel()
			wg.Wait()
		}
		js(w, map[string]bool{"stopped": cancel != nil})
	}))
	return mux
}
