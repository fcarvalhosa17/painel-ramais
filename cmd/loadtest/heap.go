package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Medicao do heap Go pos-GC via endpoint de debug do daemon (PAINEL_DEBUG_ADDR,
// loopback, desligado por padrao). RSS mistura heap, pilhas e memoria que o runtime
// ainda nao devolveu ao SO; HeapInuse/HeapAlloc depois de runtime.GC() mostram so o
// que o programa retem de fato.

// heapEvery: 1 amostra por minuto; variavel so para o teste de fumaca (flag -heap-every).
var heapEvery = time.Minute

const (
	heapMinSamples = 10 // abaixo disso a regressao nao diz nada: REPROVA
	// heapMaxSlope: crescimento tolerado do heap pos-GC. O painel roda meses sem reiniciar.
	heapMaxSlopeMiBh = 1.0
)

type HeapSample struct {
	T      time.Time
	Inuse  uint64
	Alloc  uint64
	Warmed bool // true: depois do aquecimento (entra na regressao)
}

type HeapRes struct {
	On                    bool
	N                     int
	InuseSlope, InuseR2   float64 // MiB/h
	AllocSlope, AllocR2   float64 // MiB/h
	Verdict               string  // "APROVA" ou "REPROVA"
	Why                   string
	Err                   string
	InicioPB, FimPB, Diff string
}

// linreg: minimos quadrados de y sobre x; devolve inclinacao e R2.
func linreg(xs, ys []float64) (slope, r2 float64) {
	n := float64(len(xs))
	if len(xs) < 2 || len(xs) != len(ys) {
		return 0, 0
	}
	var sx, sy, sxx, sxy, syy float64
	for i := range xs {
		sx, sy, sxx, sxy, syy = sx+xs[i], sy+ys[i], sxx+xs[i]*xs[i], sxy+xs[i]*ys[i], syy+ys[i]*ys[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, 0
	}
	slope = (n*sxy - sx*sy) / den
	if vy := n*syy - sy*sy; vy > 0 {
		r := (n*sxy - sx*sy) / (math.Sqrt(den * vy))
		r2 = r * r
	} else {
		r2 = 1 // y constante: reta perfeita de inclinacao 0
	}
	return slope, r2
}

// evalHeap calcula as inclinacoes (MiB/h) so com as amostras pos-aquecimento e decide.
// Sempre APROVA ou REPROVA: sem amostras suficientes ou com erro, REPROVA (sem medida nao ha aprovacao).
func evalHeap(all []HeapSample, errMsg string) HeapRes {
	h := HeapRes{On: true, Err: errMsg}
	var xs, yi, ya []float64
	var t0 time.Time
	for _, s := range all {
		if !s.Warmed {
			continue
		}
		if t0.IsZero() {
			t0 = s.T
		}
		xs = append(xs, s.T.Sub(t0).Hours())
		yi = append(yi, float64(s.Inuse)/mib)
		ya = append(ya, float64(s.Alloc)/mib)
	}
	h.N = len(xs)
	if h.N >= 2 {
		h.InuseSlope, h.InuseR2 = linreg(xs, yi)
		h.AllocSlope, h.AllocR2 = linreg(xs, ya)
	}
	switch {
	case h.N < heapMinSamples:
		h.Verdict, h.Why = "REPROVA", fmt.Sprintf("apenas %d amostras pos-aquecimento (minimo %d): sem medida nao ha aprovacao", h.N, heapMinSamples)
	case h.InuseSlope >= heapMaxSlopeMiBh || h.AllocSlope >= heapMaxSlopeMiBh:
		h.Verdict, h.Why = "REPROVA", fmt.Sprintf("inclinacao >= %.0f MiB/h (HeapInuse %.2f, HeapAlloc %.2f)", heapMaxSlopeMiBh, h.InuseSlope, h.AllocSlope)
	default:
		h.Verdict, h.Why = "APROVA", fmt.Sprintf("inclinacao < %.0f MiB/h (HeapInuse %.2f, HeapAlloc %.2f)", heapMaxSlopeMiBh, h.InuseSlope, h.AllocSlope)
	}
	return h
}

func (h HeapRes) ok() bool { return h.Verdict == "APROVA" }

// HeapStats le heap pos-GC do daemon.
func (d *Daemon) HeapStats() (inuse, alloc uint64, err error) {
	resp, err := d.hc.Get("http://" + d.dbg + "/debug/mem")
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("/debug/mem: HTTP %d", resp.StatusCode)
	}
	var m struct {
		Inuse uint64 `json:"heap_inuse"`
		Alloc uint64 `json:"heap_alloc"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return 0, 0, err
	}
	return m.Inuse, m.Alloc, nil
}

// HeapProfile salva o perfil de heap (pb.gz) com GC forcado.
func (d *Daemon) HeapProfile(path string) error {
	resp, err := d.hc.Get("http://" + d.dbg + "/debug/pprof/heap?gc=1")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/debug/pprof/heap: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// writeHeapCSV grava as amostras (uma por minuto) para o artefato.
func writeHeapCSV(dir, mode string, ss []HeapSample) error {
	f, err := os.Create(filepath.Join(dir, "heap-"+mode+".csv"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"timestamp_utc", "heap_inuse_bytes", "heap_alloc_bytes", "pos_aquecimento"})
	for _, s := range ss {
		_ = w.Write([]string{s.T.UTC().Format(time.RFC3339), strconv.FormatUint(s.Inuse, 10), strconv.FormatUint(s.Alloc, 10), strconv.FormatBool(s.Warmed)})
	}
	w.Flush()
	return w.Error()
}
