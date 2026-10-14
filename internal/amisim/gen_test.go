package amisim

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"painel-ramais/internal/capture"
)

// Gerador das capturas sinteticas de testdata/capturas (nao roda no go test normal):
//
//	GERAR_CAPTURAS=1 go test -run TestGerarCapturas ./internal/amisim
//
// Cada captura grava, com o formato do CONTRATOS.md secao 18, o trafego entre um cliente que
// faz as mesmas actions do daemon e este simulador, mais o oraculo (.esperado.json) tirado da
// verdade do simulador depois de cada passo.

type recorder struct {
	t     *testing.T
	nc    net.Conn
	br    *bufio.Reader
	w     *capture.Writer
	clock time.Time
	seq   int
}

func (r *recorder) tick(d time.Duration) time.Time {
	r.clock = r.clock.Add(d)
	return r.clock
}

func (r *recorder) readFrame(banner bool) []byte {
	r.nc.SetReadDeadline(time.Now().Add(10 * time.Second))
	var b []byte
	for {
		ln, err := r.br.ReadString('\n')
		if err != nil {
			r.t.Fatal(err)
		}
		b = append(b, ln...)
		if banner || ln == "\r\n" {
			return b
		}
	}
}

func (r *recorder) rec(dir byte, raw []byte) {
	if err := r.w.WriteFrame(dir, r.tick(2*time.Millisecond), raw); err != nil {
		r.t.Fatal(err)
	}
}

func (r *recorder) send(record bool, kv ...string) string {
	r.seq++
	id := fmt.Sprintf("p%d", r.seq)
	var b strings.Builder
	b.WriteString("Action: " + kv[0] + "\r\nActionID: " + id + "\r\n")
	for i := 1; i+1 < len(kv); i += 2 {
		b.WriteString(kv[i] + ": " + kv[i+1] + "\r\n")
	}
	b.WriteString("\r\n")
	r.nc.Write([]byte(b.String()))
	if record {
		r.rec('>', []byte(b.String()))
	}
	return id
}

// action envia e grava a resposta inteira (lista incluida).
func (r *recorder) action(kv ...string) {
	id := r.send(true, kv...)
	list := false
	for {
		f := r.readFrame(false)
		r.rec('<', f)
		s := string(f)
		if strings.Contains(s, "ActionID: "+id+"\r\n") || strings.HasPrefix(s, "Response:") {
			if strings.Contains(s, "EventList: start") {
				list = true
			}
			if strings.Contains(s, "EventList: Complete") || (!list && strings.HasPrefix(s, "Response:")) {
				return
			}
		}
	}
}

// sync garante que tudo que o simulador emitiu ate agora foi lido (e gravado), sem gravar o Ping.
func (r *recorder) sync() {
	id := r.send(false, "Ping")
	for {
		f := r.readFrame(false)
		if strings.Contains(string(f), "ActionID: "+id+"\r\n") {
			return
		}
		r.rec('<', f)
	}
}

func TestGerarCapturas(t *testing.T) {
	out := os.Getenv("GERAR_CAPTURAS")
	if out == "" {
		t.Skip("defina GERAR_CAPTURAS=<diretorio> para gerar as capturas sinteticas")
	}
	groups := []struct {
		name  string
		kinds []string
	}{
		{"interna", []string{"internal", "internal-hold", "internal-noanswer"}},
		{"entrada", []string{"inbound"}},
		{"saida", []string{"outbound"}},
		{"local", []string{"local-inbound", "local-internal"}},
		{"transferencia", []string{"blind-internal", "blind-inbound", "blind-outbound", "attended-merge-internal", "attended-merge-inbound",
			"attended-merge-outbound", "attended-link-internal", "attended-link-inbound", "attended-link-outbound"}},
	}
	os.MkdirAll(out, 0o755)
	for _, mode := range []string{ModeSIP, ModePJSIP} {
		for _, g := range groups {
			s, err := New(Config{Mode: mode, Exts: 40, FirstExt: 2000, Seed: 9})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Listen("127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("sintetica-%s-%s", g.name, mode)
			f, err := os.Create(filepath.Join(out, name+".txt"))
			if err != nil {
				t.Fatal(err)
			}
			start := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
			w, err := capture.NewWriter(f, capture.NewSanitizer(capture.SanitizeOptions{}), map[string]any{
				"versao_daemon": "0.1.1", "inicio": start.Format(time.RFC3339), "minutos": 1, "anonimizado": true, "sintetica": true,
				"origem": "gerada pelo simulador (internal/amisim, gen_test.go); nao e trafego de Asterisk real"})
			if err != nil {
				t.Fatal(err)
			}
			nc, err := net.Dial("tcp", s.Addr())
			if err != nil {
				t.Fatal(err)
			}
			r := &recorder{t: t, nc: nc, br: bufio.NewReaderSize(nc, 1<<20), w: w, clock: start}
			r.rec('<', r.readFrame(true))
			r.action("Login", "Username", s.cfg.User, "Secret", s.cfg.Secret)
			r.sync()
			r.action("CoreSettings")
			r.action("ModuleCheck", "Module", "chan_pjsip.so")
			r.action("ModuleCheck", "Module", "chan_sip.so")
			if mode == ModePJSIP {
				r.action("PJSIPShowEndpoints")
				r.action("PJSIPShowContacts")
			} else {
				r.action("SIPpeers")
			}
			r.action("DeviceStateList")
			r.action("CoreShowChannels")
			for _, e := range s.exts {
				if mode == ModePJSIP {
					r.action("PJSIPShowEndpoint", "Endpoint", e.num)
				} else {
					r.action("SIPshowpeer", "Peer", e.num)
				}
			}
			exp := Expected{Tecnologia: mode}
			cp := func(label string, nums []string) {
				r.sync()
				m := map[string]ExtView{}
				for k, v := range s.TruthOf(nums...) {
					m[k] = v
				}
				exp.Checkpoints = append(exp.Checkpoints, Checkpoint{Quadros: w.Frames(), Rotulo: label, Ramais: m})
			}
			var regs []string
			for _, v := range s.Truth() {
				if v.Registration == "registered" {
					regs = append(regs, v.Number)
				}
			}
			cp("inicio", regs[:3])
			if g.name == "interna" {
				r.tick(700 * time.Millisecond)
				s.SetReg(regs[0], "unregistered")
				cp("ramal desregistrou", regs[:1])
				r.tick(700 * time.Millisecond)
				s.SetReg(regs[0], "registered")
				cp("ramal voltou", regs[:1])
			}
			pos := 3
			for i, kind := range g.kinds {
				need := ExtsNeeded(kind)
				exts := regs[pos : pos+need]
				pos += need
				sc, err := s.NewScript(kind, Parts{Exts: exts, Trunk: fmt.Sprintf("trunk%d", i%2+1), XNum: fmt.Sprintf("11980%06d", 100+i), XName: "Pessoa 001", DID: "30001000"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				for si, st := range sc.Steps {
					r.tick(700 * time.Millisecond)
					sc.Do(si)
					cp(kind+" / "+st.Name, exts)
				}
				sc.Abort()
				cp(kind+" / limpo", exts)
			}
			if err := w.Close(map[string]any{"tecnologia": mode, "asterisk": s.cfg.Asterisk, "encerrado_por": "prazo"}); err != nil {
				t.Fatal(err)
			}
			f.Close()
			nc.Close()
			s.Close()
			j, _ := json.MarshalIndent(exp, "", " ")
			if err := os.WriteFile(filepath.Join(out, name+".esperado.json"), j, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
