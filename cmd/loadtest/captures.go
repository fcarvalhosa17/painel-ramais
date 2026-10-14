package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"painel-ramais/internal/amisim"
)

// "Com quem fala" sobre capturas: o simulador reproduz o arquivo (modo -replay do amisim), o daemon
// consome como se fosse o Asterisk, e em cada ponto do arquivo (checkpoint do .esperado.json) o
// estado do cliente WebSocket tem de igualar o oraculo gravado.

type CapRow struct {
	Label  string
	Frames int
	OK     bool
	Conv   time.Duration
	Detail string
}

type CapResult struct {
	Name, Mode, Err string
	Rows            []CapRow
	Violations      []string
	Actions         map[string]int
}

func (c *CapResult) okCount() (ok, n int) {
	for _, r := range c.Rows {
		n++
		if r.OK {
			ok++
		}
	}
	return
}

// compareReplay compara o estado do daemon com o esperado da captura. Os horarios da captura
// nao sao os da execucao: ficam de fora. O nome vem do EndpointDetail/SIPshowpeer gravado.
func compareReplay(got, want View, tech string) []Diff {
	w := want
	w.Calls = append([]amisim.CallView(nil), want.Calls...)
	for i := range w.Calls {
		for _, g := range got.Calls {
			if g.ID == w.Calls[i].ID {
				w.Calls[i].StartedAt = g.StartedAt
			}
		}
	}
	ds := compareView(got, w, tech)
	if want.Name != "" && got.Name != want.Name {
		ds = append(ds, Diff{want.Number, fmt.Sprintf("name %q, esperado %q", got.Name, want.Name)})
	}
	return ds
}

func listCaptures(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	var out []string
	for _, p := range m {
		if _, err := os.Stat(strings.TrimSuffix(p, ".txt") + ".esperado.json"); err == nil {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func runCapture(o opts, path string, logf func(string, ...any)) *CapResult {
	res := &CapResult{Name: strings.TrimSuffix(filepath.Base(path), ".txt")}
	exp, err := amisim.LoadExpected(strings.TrimSuffix(path, ".txt") + ".esperado.json")
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.Mode = exp.Tecnologia
	// um checkpoint por quantidade de quadros (o ultimo rotulo vale)
	byN := map[int][]amisim.Checkpoint{}
	var ns []int
	for _, c := range exp.Checkpoints {
		if _, ok := byN[c.Quadros]; !ok {
			ns = append(ns, c.Quadros)
		}
		byN[c.Quadros] = append(byN[c.Quadros], c)
	}
	sort.Ints(ns)
	rp, err := amisim.NewReplayer(path, o.replaySpeed, ns)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	if err := rp.Listen("127.0.0.1:0"); err != nil {
		res.Err = err.Error()
		return res
	}
	defer rp.Close()
	d, err := startDaemon(o, rp.Addr(), amiUser, amiSecret, "auto")
	if err != nil {
		res.Err = "subir o daemon: " + err.Error()
		return res
	}
	defer func() {
		d.Stop()
		if res.Err == "" {
			d.Cleanup()
		}
	}()
	var h *Health
	if !waitUntil(60*time.Second, func() bool {
		h, err = d.Health()
		return err == nil && h.AMI.Connected
	}) {
		res.Err = "daemon nao conectou ao AMI reproduzido: " + d.LogTail()
		return res
	}
	if h.AMI.Technology != exp.Tecnologia {
		res.Err = fmt.Sprintf("modo auto detectou %q, a captura e %q", h.AMI.Technology, exp.Tecnologia)
		return res
	}
	trk := NewTracker()
	ws := NewWSClient(d, trk, trk.Phase)
	defer ws.Stop()
	if _, err := ws.Resync(); err != nil {
		res.Err = "abrir /ws: " + err.Error()
		return res
	}
	for _, n := range ns {
		select {
		case got := <-rp.PauseCh():
			if got != n {
				res.Err = fmt.Sprintf("pausa em %d quadros, esperada em %d", got, n)
				return res
			}
		case <-time.After(90 * time.Second):
			res.Err = fmt.Sprintf("a reproducao nao chegou ao ponto de %d quadros em 90 s (o daemon nao fez as actions esperadas?)", n)
			return res
		}
		for _, cp := range byN[n] {
			t0 := time.Now()
			var last string
			ok := waitUntil(8*time.Second, func() bool {
				last = ""
				for num, want := range cp.Ramais {
					g, _ := ws.View(num)
					if ds := compareReplay(g, want, exp.Tecnologia); len(ds) > 0 {
						last = ds[0].Ext + ": " + ds[0].What
						return false
					}
				}
				return true
			})
			res.Rows = append(res.Rows, CapRow{cp.Rotulo, n, ok, time.Since(t0), last})
		}
		rp.Resume()
	}
	select {
	case <-rp.Done():
	case <-time.After(30 * time.Second):
		res.Err = "a reproducao nao terminou em 30 s"
	}
	counts, viol := rp.Actions()
	res.Actions = counts
	for _, v := range viol {
		res.Violations = append(res.Violations, v.Action)
	}
	return res
}
