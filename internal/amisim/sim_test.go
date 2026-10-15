package amisim

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// cliente AMI minimo e independente do simulador, so para os testes.
type tclient struct {
	t  *testing.T
	nc net.Conn
	br *bufio.Reader
}

func dial(t *testing.T, s *Sim) *tclient {
	t.Helper()
	nc, err := net.DialTimeout("tcp", s.Addr(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	c := &tclient{t: t, nc: nc, br: bufio.NewReaderSize(nc, 1<<20)}
	banner, err := c.br.ReadString('\n')
	if err != nil || !strings.HasPrefix(banner, "Asterisk Call Manager/") {
		t.Fatalf("banner invalido: %q %v", banner, err)
	}
	return c
}

type msg map[string]string

func (c *tclient) read() msg {
	c.nc.SetReadDeadline(time.Now().Add(10 * time.Second))
	m := msg{}
	for {
		line, err := c.br.ReadString('\n')
		if err != nil {
			c.t.Fatalf("leitura: %v (msg parcial %v)", err, m)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return m
		}
		k, v, _ := strings.Cut(line, ": ")
		m[k] = v
	}
}

func (c *tclient) send(kv ...string) {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(&b, "%s: %s\r\n", kv[i], kv[i+1])
	}
	b.WriteString("\r\n")
	c.nc.Write([]byte(b.String()))
}

// action envia e devolve a resposta; se for lista, le ate o evento *Complete.
func (c *tclient) action(name string, kv ...string) (resp msg, evs []msg) {
	id := fmt.Sprintf("t-%d", time.Now().UnixNano())
	c.send(append([]string{"Action", name, "ActionID", id}, kv...)...)
	for {
		m := c.read()
		if m["ActionID"] != id {
			continue // evento ao vivo
		}
		if m["Response"] != "" {
			resp = m
			if m["EventList"] != "start" {
				return
			}
			continue
		}
		evs = append(evs, m)
		if m["EventList"] == "Complete" {
			return
		}
	}
}

func (c *tclient) login(s *Sim) msg {
	r, _ := c.action("Login", "Username", s.cfg.User, "Secret", s.cfg.Secret)
	return r
}

func newSim(t *testing.T, cfg Config) *Sim {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// Criterio da tarefa 1: login por TCP e EventList completo, nos dois modos, com 10.000 ramais.
func TestLoginEListasCompletas(t *testing.T) {
	for _, mode := range []string{ModeSIP, ModePJSIP} {
		t.Run(mode, func(t *testing.T) {
			s := newSim(t, Config{Mode: mode})
			c := dial(t, s)
			if r := c.login(s); r["Response"] != "Success" {
				t.Fatalf("login: %v", r)
			}
			if r, _ := c.action("CoreSettings"); r["AsteriskVersion"] != "18.20.0" {
				t.Fatalf("CoreSettings: %v", r)
			}
			mod := map[string]string{ModeSIP: "chan_sip", ModePJSIP: "chan_pjsip"}
			if r, _ := c.action("ModuleCheck", "Module", mod[mode]); r["Response"] != "Success" {
				t.Fatalf("ModuleCheck do modulo carregado: %v", r)
			}
			other := map[string]string{ModeSIP: "chan_pjsip", ModePJSIP: "chan_sip"}
			if r, _ := c.action("ModuleCheck", "Module", other[mode]); r["Response"] != "Error" {
				t.Fatalf("ModuleCheck do modulo ausente deveria dar Error: %v", r)
			}
			total := 10000 + 4
			if mode == ModePJSIP {
				r, evs := c.action("PJSIPShowEndpoints")
				if r["Response"] != "Success" || r["EventList"] != "start" {
					t.Fatalf("PJSIPShowEndpoints: %v", r)
				}
				last := evs[len(evs)-1]
				if last["Event"] != "EndpointListComplete" || last["ListItems"] != fmt.Sprint(total) || len(evs) != total+1 {
					t.Fatalf("lista incompleta: %d eventos, fim %v", len(evs), last)
				}
				if evs[0]["Event"] != "EndpointList" || evs[0]["ObjectName"] != "2000" || evs[0]["DeviceState"] == "" {
					t.Fatalf("primeiro EndpointList: %v", evs[0])
				}
				_, cts := c.action("PJSIPShowContacts")
				if cts[len(cts)-1]["Event"] != "ContactListComplete" || cts[0]["Event"] != "ContactList" {
					t.Fatalf("contatos: %v", cts[len(cts)-1])
				}
				if r, _ := c.action("SIPpeers"); r["Response"] != "Error" || !strings.Contains(r["Message"], "Invalid/unknown command") {
					t.Fatalf("SIPpeers em modo pjsip deveria ser comando desconhecido: %v", r)
				}
			} else {
				r, evs := c.action("SIPpeers")
				if r["Response"] != "Success" {
					t.Fatalf("SIPpeers: %v", r)
				}
				last := evs[len(evs)-1]
				if last["Event"] != "PeerlistComplete" || last["ListItems"] != fmt.Sprint(total) || len(evs) != total+1 {
					t.Fatalf("lista incompleta: %d eventos, fim %v", len(evs), last)
				}
				if r, _ := c.action("PJSIPShowEndpoints"); r["Response"] != "Error" {
					t.Fatalf("PJSIPShowEndpoints em modo sip deveria falhar: %v", r)
				}
				if r, _ := c.action("SIPshowpeer", "Peer", "2000"); r["Response"] != "Success" || !strings.Contains(r["Callerid"], "2000") {
					t.Fatalf("SIPshowpeer: %v", r)
				}
			}
			_, ds := c.action("DeviceStateList")
			if ds[len(ds)-1]["Event"] != "DeviceStateListComplete" || len(ds) < 10000 {
				t.Fatalf("DeviceStateList incompleta: %d", len(ds))
			}
			_, cs := c.action("CoreShowChannels")
			if cs[len(cs)-1]["Event"] != "CoreShowChannelsComplete" || cs[len(cs)-1]["ListItems"] != "0" {
				t.Fatalf("CoreShowChannels: %v", cs[len(cs)-1])
			}
			counts, viol := s.Actions()
			if len(viol) != 0 || counts["Login"] != 1 {
				t.Fatalf("actions: %v violacoes %v", counts, viol)
			}
		})
	}
}

func TestLoginFalhaEPermissoes(t *testing.T) {
	s := newSim(t, Config{Mode: ModePJSIP, Exts: 20})
	c := dial(t, s)
	if r, _ := c.action("CoreSettings"); r["Response"] != "Error" {
		t.Fatalf("action antes do login deveria falhar: %v", r)
	}
	if r, _ := c.action("Login", "Username", "painel-ramais", "Secret", "errado"); r["Response"] != "Error" || r["Message"] != "Authentication failed" {
		t.Fatalf("login errado: %v", r)
	}
	// usuario sem classe de escrita: actions que a exigem sao negadas
	s2 := newSim(t, Config{Mode: ModePJSIP, Exts: 20, Write: "reporting"})
	c2 := dial(t, s2)
	c2.login(s2)
	if r, _ := c2.action("PJSIPShowEndpoints"); r["Message"] != "Permission denied" {
		t.Fatalf("PJSIPShowEndpoints sem write=system deveria negar: %v", r)
	}
	if r, _ := c2.action("CoreShowChannels"); r["Response"] != "Success" {
		t.Fatalf("CoreShowChannels com reporting: %v", r)
	}
}

func TestRecusaCommandEEscrita(t *testing.T) {
	s := newSim(t, Config{Mode: ModePJSIP, Exts: 20})
	c := dial(t, s)
	c.login(s)
	for _, a := range []string{"Command", "Originate", "Hangup", "Redirect", "Setvar", "Reload", "Filter", "NaoExiste"} {
		if r, _ := c.action(a); r["Response"] != "Error" {
			t.Fatalf("%s deveria ser recusada: %v", a, r)
		}
	}
	_, viol := s.Actions()
	if len(viol) != 8 {
		t.Fatalf("violacoes registradas: %d (%v)", len(viol), viol)
	}
}

// drain le eventos ao vivo ate n chegarem (sem ActionID).
func drain(c *tclient, n int) []msg {
	var o []msg
	for len(o) < n {
		o = append(o, c.read())
	}
	return o
}

func TestRajadaGeraEventosDoModo(t *testing.T) {
	for _, mode := range []string{ModeSIP, ModePJSIP} {
		t.Run(mode, func(t *testing.T) {
			s := newSim(t, Config{Mode: mode, Exts: 500})
			c := dial(t, s)
			c.login(s)
			c.read() // FullyBooted
			var stamps []*Stamp
			s.OnCommit = func(v ExtView, st *Stamp) { stamps = append(stamps, st) }
			nums := s.Burst(100)
			if len(nums) != 100 {
				t.Fatalf("rajada: %d", len(nums))
			}
			want, ev := 200, "ContactStatus" // removed + DeviceStateChange por ramal
			if mode == ModeSIP {
				ev = "PeerStatus"
			}
			got := map[string]int{}
			for _, m := range drain(c, want) {
				got[m["Event"]]++
			}
			if got[ev] != 100 || got["DeviceStateChange"] != 100 {
				t.Fatalf("eventos: %v", got)
			}
			if len(stamps) != 100 {
				t.Fatalf("OnCommit chamado %d vezes", len(stamps))
			}
			time.Sleep(50 * time.Millisecond)
			for i, st := range stamps {
				if atomic.LoadInt64(&st.At) == 0 {
					t.Fatalf("carimbo %d sem hora de gravacao", i)
				}
			}
			reg, _, _ := s.Counts()
			if reg["unregistered"] < 100 {
				t.Fatalf("verdade nao refletiu a rajada: %v", reg)
			}
		})
	}
}

func TestQuedaERetornoDoAMI(t *testing.T) {
	s := newSim(t, Config{Mode: ModePJSIP, Exts: 20})
	addr := s.Addr()
	c := dial(t, s)
	c.login(s)
	if err := s.Down(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	c.nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, err := c.br.ReadByte(); err != nil {
			break
		}
	}
	if nc, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
		nc.Close()
		t.Fatal("porta deveria estar recusando enquanto fora do ar")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		nc, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			nc.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("simulador nao voltou")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitFor evita sleep fixo: espera a condicao.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout esperando: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func checkExt(t *testing.T, ctx string, s *Sim, num string, calls ...[4]string) {
	t.Helper()
	v := s.TruthOf(num)[num]
	if len(v.Calls) != len(calls) {
		t.Fatalf("%s: ramal %s tem %d chamadas, esperado %d: %+v", ctx, num, len(v.Calls), len(calls), v.Calls)
	}
	for _, w := range calls { // {direcao, estado, kind:numero}
		found := false
		for _, c := range v.Calls {
			if c.Direction == w[0] && c.State == w[1] && c.Peer.Kind+":"+c.Peer.Number == w[2] {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: ramal %s sem chamada %v; tem %+v", ctx, num, w, v.Calls)
		}
	}
}

// Cenarios de chamada: "com quem fala" na verdade, passo a passo, e canais zerados no fim.
func TestCenarios(t *testing.T) {
	const X = "11987654321"
	type exp struct {
		step  string
		ext   string
		calls [][4]string // direcao, estado, peer
	}
	ext := func(n string) string { return "extension:" + n }
	cases := []struct {
		kind  string
		exts  []string
		steps []exp
	}{
		{"internal", []string{"2001", "2002"}, []exp{
			{"ring", "2001", [][4]string{{"internal", "ringing", ext("2002")}}},
			{"ring", "2002", [][4]string{{"internal", "ringing", ext("2001")}}},
			{"answer", "2002", [][4]string{{"internal", "talking", ext("2001")}}},
		}},
		{"internal-hold", []string{"2001", "2002"}, []exp{
			{"hold", "2002", [][4]string{{"internal", "hold", ext("2001")}}},
			{"hold", "2001", [][4]string{{"internal", "talking", ext("2002")}}},
			{"unhold", "2002", [][4]string{{"internal", "talking", ext("2001")}}},
		}},
		{"inbound", []string{"2003"}, []exp{
			{"ring", "2003", [][4]string{{"inbound", "ringing", "external:" + X}}},
			{"answer", "2003", [][4]string{{"inbound", "talking", "external:" + X}}},
		}},
		{"outbound", []string{"2004"}, []exp{
			{"ring", "2004", [][4]string{{"outbound", "ringing", "external:" + X}}},
			{"answer", "2004", [][4]string{{"outbound", "talking", "external:" + X}}},
		}},
		{"local-inbound", []string{"2005"}, []exp{
			{"answer", "2005", [][4]string{{"inbound", "talking", "external:" + X}}},
			{"optimize", "2005", [][4]string{{"inbound", "talking", "external:" + X}}},
		}},
		{"local-internal", []string{"2006", "2007"}, []exp{
			{"answer", "2007", [][4]string{{"internal", "talking", ext("2006")}}},
			{"answer", "2006", [][4]string{{"internal", "talking", ext("2007")}}},
		}},
		{"blind-internal", []string{"2008", "2009", "2010"}, []exp{
			{"answer", "2009", [][4]string{{"internal", "talking", ext("2008")}}},
			{"blind-transfer", "2009", nil},
			{"blind-transfer", "2008", [][4]string{{"internal", "ringing", ext("2010")}}},
			{"blind-transfer", "2010", [][4]string{{"internal", "ringing", ext("2008")}}},
			{"target-answer", "2010", [][4]string{{"internal", "talking", ext("2008")}}},
			{"target-answer", "2008", [][4]string{{"internal", "talking", ext("2010")}}},
		}},
		{"blind-inbound", []string{"2011", "2012"}, []exp{
			{"blind-transfer", "2011", nil},
			{"blind-transfer", "2012", [][4]string{{"inbound", "ringing", "external:" + X}}},
			{"target-answer", "2012", [][4]string{{"inbound", "talking", "external:" + X}}},
		}},
		{"blind-outbound", []string{"2013", "2014"}, []exp{
			{"blind-transfer", "2013", nil},
			{"target-answer", "2014", [][4]string{{"outbound", "talking", "external:" + X}}},
		}},
		{"attended-merge-internal", []string{"2015", "2016", "2017"}, []exp{
			{"hold-transferee", "2016", [][4]string{{"internal", "hold", ext("2015")}}},
			{"consult-ring", "2016", [][4]string{{"internal", "hold", ext("2015")}, {"internal", "ringing", ext("2017")}}},
			{"consult-ring", "2017", [][4]string{{"internal", "ringing", ext("2016")}}},
			{"consult-answer", "2017", [][4]string{{"internal", "talking", ext("2016")}}},
			{"complete-merge", "2016", nil},
			{"complete-merge", "2015", [][4]string{{"internal", "talking", ext("2017")}}},
			{"complete-merge", "2017", [][4]string{{"internal", "talking", ext("2015")}}},
		}},
		{"attended-link-internal", []string{"2018", "2019", "2020"}, []exp{
			{"complete-link", "2019", nil},
			{"complete-link", "2018", [][4]string{{"internal", "talking", ext("2020")}}},
			{"complete-link", "2020", [][4]string{{"internal", "talking", ext("2018")}}},
		}},
		{"attended-merge-inbound", []string{"2022", "2023"}, []exp{
			{"consult-ring", "2022", [][4]string{{"inbound", "hold", "external:" + X}, {"internal", "ringing", ext("2023")}}},
			{"complete-merge", "2022", nil},
			{"complete-merge", "2023", [][4]string{{"inbound", "talking", "external:" + X}}},
		}},
		{"attended-link-outbound", []string{"2024", "2025"}, []exp{
			{"complete-link", "2024", nil},
			{"complete-link", "2025", [][4]string{{"outbound", "talking", "external:" + X}}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			s := newSim(t, Config{Mode: ModePJSIP, Exts: 50})
			for _, e := range s.exts {
				e.reg = "registered"
			}
			c := dial(t, s)
			c.login(s)
			sc, err := s.NewScript(tc.kind, Parts{Exts: tc.exts, Trunk: "trunk1", XNum: X, XName: "ACME LTDA"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			byName := map[string]int{}
			last := map[string]int{}
			for i, st := range sc.Steps {
				byName[st.Name] = i
				last[st.Name] = i
			}
			done := -1
			for _, e := range tc.steps {
				for done < byName[e.step] {
					done++
					sc.Do(done)
				}
				checkExt(t, tc.kind+"/"+e.step, s, e.ext, e.calls...)
			}
			for done < len(sc.Steps)-1 {
				done++
				sc.Do(done)
			}
			sc.Abort()
			if !s.Idle() {
				t.Fatalf("canais sobraram: %d", s.ActiveChannels())
			}
			for _, n := range tc.exts {
				checkExt(t, tc.kind+"/fim", s, n)
				if v := s.TruthOf(n)[n]; v.DeviceState != "not_inuse" {
					t.Fatalf("ramal %s terminou em %s", n, v.DeviceState)
				}
			}
			// toda Newchannel teve Hangup (o cliente le o fluxo AMI pelo socket)
			c.nc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			cnt := map[string]int{}
			for {
				line, err := c.br.ReadString('\n')
				if err != nil {
					break
				}
				if strings.HasPrefix(line, "Event: ") {
					cnt[strings.TrimSpace(line[7:])]++
				}
			}
			if cnt["Newchannel"] == 0 || cnt["Newchannel"] != cnt["Hangup"] {
				t.Fatalf("Newchannel=%d Hangup=%d (%v)", cnt["Newchannel"], cnt["Hangup"], cnt)
			}
			if cnt["BridgeEnter"] != cnt["BridgeLeave"] {
				t.Fatalf("BridgeEnter=%d BridgeLeave=%d", cnt["BridgeEnter"], cnt["BridgeLeave"])
			}
		})
	}
}

func TestCoreShowChannelsComChamada(t *testing.T) {
	s := newSim(t, Config{Mode: ModePJSIP, Exts: 20})
	for _, e := range s.exts {
		e.reg = "registered"
	}
	sc, _ := s.NewScript("local-inbound", Parts{Exts: []string{"2001"}, Trunk: "trunk2", XNum: "11999990000", XName: "X"}, nil)
	sc.Do(0)
	sc.Do(1)
	c := dial(t, s)
	c.login(s)
	_, evs := c.action("CoreShowChannels")
	if n := evs[len(evs)-1]["ListItems"]; n != "4" {
		t.Fatalf("esperava 4 canais (tronco, Local;1, Local;2, ramal), veio %s", n)
	}
	bridged := 0
	for _, e := range evs[:len(evs)-1] {
		if e["BridgeId"] != "" {
			bridged++
		}
		if !strings.HasPrefix(e["Duration"], "00:") {
			t.Fatalf("Duration: %v", e)
		}
	}
	if bridged != 4 {
		t.Fatalf("canais em bridge: %d", bridged)
	}
	sc.Abort()
}

func TestCargaAleatoriaTerminaLimpa(t *testing.T) {
	s := newSim(t, Config{Mode: ModeSIP, Exts: 300})
	for _, e := range s.exts {
		e.reg = "registered"
	}
	tm := Timing{RingMin: time.Millisecond, RingMax: 5 * time.Millisecond, TalkMin: 5 * time.Millisecond, TalkMax: 20 * time.Millisecond}
	ctx, cancel := contextWithTimeout(300 * time.Millisecond)
	defer cancel()
	wg := s.StartLoad(ctx, 40, tm)
	wg.Wait()
	waitFor(t, "canais zerados", s.Idle)
	reg, inCall, calls := s.Counts()
	if inCall != 0 || calls != 0 || reg["registered"] != 300 {
		t.Fatalf("estado final: %v inCall=%d calls=%d", reg, inCall, calls)
	}
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// ---- reproducao de captura

func capturaTeste(t *testing.T, mode string) string {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "capturas", "sintetica-transferencia-"+mode+".txt")
	if _, err := os.Stat(p); err != nil {
		t.Skip("capturas sinteticas ausentes: ", err)
	}
	return p
}

// A reproducao devolve as respostas gravadas (com o ActionID da pergunta), na ordem de fila,
// e os eventos gravados depois que o daemon fez as actions anteriores a eles.
func TestReplayReproduzSnapshotEEventos(t *testing.T) {
	for _, mode := range []string{ModeSIP, ModePJSIP} {
		t.Run(mode, func(t *testing.T) {
			p := capturaTeste(t, mode)
			exp, err := LoadExpected(strings.TrimSuffix(p, ".txt") + ".esperado.json")
			if err != nil {
				t.Fatal(err)
			}
			last := exp.Checkpoints[len(exp.Checkpoints)-1].Quadros
			first := exp.Checkpoints[0].Quadros
			r, err := NewReplayer(p, 0, []int{first, last})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Listen("127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			nc, err := net.Dial("tcp", r.Addr())
			if err != nil {
				t.Fatal(err)
			}
			defer nc.Close()
			c := &tclient{t: t, nc: nc, br: bufio.NewReaderSize(nc, 1<<20)}
			if b, _ := c.br.ReadString('\n'); !strings.HasPrefix(b, "Asterisk Call Manager/") {
				t.Fatalf("banner: %q", b)
			}
			if resp, _ := c.action("Login", "Username", "x", "Secret", "qualquer"); resp["Response"] != "Success" {
				t.Fatalf("login: %v", resp)
			}
			if resp, _ := c.action("CoreSettings"); resp["AsteriskVersion"] != "18.20.0" {
				t.Fatalf("CoreSettings: %v", resp)
			}
			list := "PJSIPShowEndpoints"
			if mode == ModeSIP {
				list = "SIPpeers"
			}
			_, evs := c.action(list)
			if n := len(evs); n != 40+4+1 {
				t.Fatalf("%s: %d eventos, esperado 45", list, n)
			}
			c.action("DeviceStateList")
			c.action("CoreShowChannels")
			if mode == ModePJSIP {
				resp, evs := c.action("PJSIPShowEndpoint", "Endpoint", "2003")
				if resp["Response"] != "Success" || evs[0]["Event"] != "EndpointDetail" || evs[0]["Callerid"] != "\"Ramal 2003\" <2003>" {
					t.Fatalf("EndpointDetail: %v %v", resp, evs[0])
				}
			} else if resp, _ := c.action("SIPshowpeer", "Peer", "2003"); !strings.Contains(resp["Callerid"], "Ramal 2003") {
				t.Fatalf("SIPshowpeer: %v", resp)
			}
			if resp, _ := c.action("Command", "Command", "core show channels"); resp["Response"] != "Error" {
				t.Fatalf("Command deveria ser recusada: %v", resp)
			}
			if _, viol := r.Actions(); len(viol) != 1 || viol[0].Action != "Command" {
				t.Fatalf("violacoes: %v", viol)
			}
		})
	}
}

// Pausas: a reproducao para antes do primeiro evento depois do checkpoint e continua com Resume.
func TestReplayPausaNosCheckpoints(t *testing.T) {
	p := capturaTeste(t, ModePJSIP)
	exp, _ := LoadExpected(strings.TrimSuffix(p, ".txt") + ".esperado.json")
	cps := []int{exp.Checkpoints[1].Quadros, exp.Checkpoints[2].Quadros, exp.Checkpoints[len(exp.Checkpoints)-1].Quadros}
	r, err := NewReplayer(p, 0, cps)
	if err != nil {
		t.Fatal(err)
	}
	r.Listen("127.0.0.1:0")
	defer r.Close()
	nc, _ := net.Dial("tcp", r.Addr())
	defer nc.Close()
	c := &tclient{t: t, nc: nc, br: bufio.NewReaderSize(nc, 1<<20)}
	c.br.ReadString('\n')
	c.action("Login", "Username", "x", "Secret", "y")
	// o daemon faz as mesmas actions do arquivo: sem isso os eventos nao saem
	for _, a := range [][]string{{"CoreSettings"}, {"ModuleCheck", "Module", "chan_pjsip.so"}, {"ModuleCheck", "Module", "chan_sip.so"}, {"PJSIPShowEndpoints"}, {"PJSIPShowContacts"}, {"DeviceStateList"}, {"CoreShowChannels"}} {
		c.action(a[0], a[1:]...)
	}
	select {
	case n := <-r.PauseCh():
		t.Fatalf("pausou em %d antes de o daemon pedir os nomes", n)
	case <-time.After(200 * time.Millisecond):
	}
	for i := 0; i < 40; i++ {
		c.action("PJSIPShowEndpoint", "Endpoint", fmt.Sprint(2000+i))
	}
	for i, want := range cps {
		select {
		case n := <-r.PauseCh():
			if n != want {
				t.Fatalf("pausa %d: %d, esperado %d", i, n, want)
			}
			r.Resume()
		case <-time.After(5 * time.Second):
			t.Fatalf("sem pausa %d (%d)", i, want)
		}
	}
	select {
	case <-r.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("reproducao nao terminou")
	}
}

func TestPJSIPShowEndpointComCallerid(t *testing.T) {
	s := newSim(t, Config{Mode: ModePJSIP, Exts: 20})
	c := dial(t, s)
	c.login(s)
	c.read() // FullyBooted
	resp, evs := c.action("PJSIPShowEndpoint", "Endpoint", "2003")
	if resp["Response"] != "Success" || evs[0]["Event"] != "EndpointDetail" || evs[0]["Callerid"] != "\"Ramal 2003\" <2003>" || evs[len(evs)-1]["Event"] != "EndpointDetailComplete" {
		t.Fatalf("EndpointDetail: %v %v", resp, evs)
	}
	if resp, _ := c.action("PJSIPShowEndpoint", "Endpoint", "9999"); resp["Response"] != "Error" {
		t.Fatalf("endpoint inexistente: %v", resp)
	}
	if _, viol := s.Actions(); len(viol) != 0 {
		t.Fatalf("PJSIPShowEndpoint nao e violacao: %v", viol)
	}
	s.EmitReload()
	if m := c.read(); m["Event"] != "Reload" {
		t.Fatalf("Reload: %v", m)
	}
	// em modo sip a action e desconhecida
	s2 := newSim(t, Config{Mode: ModeSIP, Exts: 20})
	c2 := dial(t, s2)
	c2.login(s2)
	if resp, _ := c2.action("PJSIPShowEndpoint", "Endpoint", "2003"); resp["Response"] != "Error" {
		t.Fatalf("modo sip: %v", resp)
	}
}
