package main

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"painel-ramais/internal/amisim"
)

type View = amisim.ExtView

// ---- estatistica

type Stats struct {
	N                  int
	P50, P90, P99, Max time.Duration
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted))*p+0.999999) - 1 // nearest rank
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func statsOf(d []time.Duration) Stats {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return Stats{N: len(s), P50: percentile(s, 0.50), P90: percentile(s, 0.90), P99: percentile(s, 0.99), Max: percentile(s, 1)}
}

// ---- rastreador de latencia evento -> delta
//
// O simulador chama OnCommit (antes de gravar no socket) com a verdade do ramal
// depois da mutacao; o carimbo e preenchido quando o lote vai para o socket. O
// cliente WS chama Observe com cada ramal recebido (snapshot ou delta). A
// latencia de uma mutacao e a hora da primeira observacao do estado dela (ou de
// um estado posterior, se foi coalescida) menos a hora de gravacao no socket.

type pend struct {
	truth View
	fp    uint64
	st    *amisim.Stamp
	phase string
}

type Tracker struct {
	mu         sync.Mutex
	pending    map[string][]pend
	baseline   map[string]View
	phase      string
	lat        map[string][]time.Duration
	unexpected []string
	nUnexp     int
	nGrace     int
	nTaint     int
	nTorn      int
	tainted    map[string]bool
	grace      time.Time
	observed   int
	// "em conversa" sem outro lado: contado depois de noPeerFrom (fim do aquecimento), 1 vez por chamada.
	noPeerFrom    time.Time
	noPeerSeen    map[string]bool
	nNoPeer       int
	noPeerSamples []string
}

func NewTracker() *Tracker {
	return &Tracker{pending: map[string][]pend{}, baseline: map[string]View{}, tainted: map[string]bool{}, lat: map[string][]time.Duration{}}
}

// Grace ignora (conta a parte) estados inesperados ate `until`: janela de ressincronizacao.
func (t *Tracker) Grace(until time.Time) { t.mu.Lock(); t.grace = until; t.mu.Unlock() }

// Taint marca ramais cujo estado de chamada veio de uma restauracao por snapshot (reconexao):
// divergencias deles contam a parte ate a lista de chamadas esvaziar.
func (t *Tracker) Taint(nums []string) {
	t.mu.Lock()
	for _, n := range nums {
		t.tainted[n] = true
	}
	t.mu.Unlock()
}

func (t *Tracker) Tainted() int { t.mu.Lock(); defer t.mu.Unlock(); return t.nTaint }

func (t *Tracker) Graced() int { t.mu.Lock(); defer t.mu.Unlock(); return t.nGrace }

func (t *Tracker) SetPhase(p string) { t.mu.Lock(); t.phase = p; t.mu.Unlock() }

func (t *Tracker) Phase() string { t.mu.Lock(); defer t.mu.Unlock(); return t.phase }

// Hook e o amisim.Sim.OnCommit.
func (t *Tracker) Hook(v View, st *amisim.Stamp) {
	t.mu.Lock()
	t.pending[v.Number] = append(t.pending[v.Number], pend{truth: v, fp: amisim.Fingerprint(v), st: st, phase: t.phase})
	t.mu.Unlock()
}

// Reset descarta pendencias e baseline (depois de reconexao/epoch novo).
func (t *Tracker) Reset() {
	t.mu.Lock()
	t.pending = map[string][]pend{}
	t.baseline = map[string]View{}
	t.mu.Unlock()
}

// SetBaseline registra o estado conhecido de um ramal sem medir latencia.
func (t *Tracker) SetBaseline(v View) {
	t.mu.Lock()
	t.baseline[v.Number] = v
	t.mu.Unlock()
}

// Observe processa um ramal recebido do daemon. measure=false so atualiza o estado conhecido.
func (t *Tracker) Observe(v View, recv time.Time, measure bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.observed++
	t.noteNoPeer(v, recv, measure)
	if len(v.Calls) == 0 {
		delete(t.tainted, v.Number)
	}
	list := t.pending[v.Number]
	for i := len(list) - 1; i >= 0; i-- {
		if amisim.FingerprintLike(v, list[i].truth) != list[i].fp {
			continue
		}
		if measure {
			for _, p := range list[:i+1] {
				at := atomic.LoadInt64(&p.st.At)
				d := time.Duration(0)
				if at != 0 && recv.UnixNano() > at {
					d = time.Duration(recv.UnixNano() - at)
				}
				t.lat[p.phase] = append(t.lat[p.phase], d)
			}
		}
		t.baseline[v.Number] = list[i].truth
		if i+1 == len(list) {
			delete(t.pending, v.Number)
		} else {
			t.pending[v.Number] = list[i+1:]
		}
		return
	}
	b, ok := t.baseline[v.Number]
	if !ok {
		t.baseline[v.Number] = v // sem referencia (depois de Reset): adota
		return
	}
	if amisim.FingerprintLike(v, b) == amisim.Fingerprint(b) {
		return // reenvio do estado ja conhecido
	}
	if !measure {
		return
	}
	if recv.Before(t.grace) {
		t.nGrace++
		return
	}
	cands := []View{b}
	for _, p := range list {
		cands = append(cands, p.truth)
	}
	if torn(v, cands) {
		t.nTorn++ // dispositivo/registro de um instante e chamadas de outro: eventos AMI separados, lote no meio
		return
	}
	if t.tainted[v.Number] {
		t.nTaint++ // estado errado herdado de uma restauracao por snapshot, nao de um evento ao vivo
		return
	}
	t.nUnexp++
	if len(t.unexpected) < 20 {
		t.unexpected = append(t.unexpected, fmt.Sprintf("[fase %s] ramal %s: daemon mostrou reg=%s dev=%s calls=%s; pendentes=%d; verdade esperada=%s", t.phase, v.Number, v.Registration, v.DeviceState, callsStr(v.Calls), len(list), truthsStr(list, b)))
	}
}

// CountNoPeerFrom liga a contagem de "em conversa" sem outro lado a partir de from (fim do aquecimento).
func (t *Tracker) CountNoPeerFrom(from time.Time) {
	t.mu.Lock()
	t.noPeerFrom = from
	t.mu.Unlock()
}

// noteNoPeer conta chamada "talking" com outro lado "unknown" vista no WS. O daemon nunca deve
// publicar isso: a protecao de 1 s tambem conta (ela publica exatamente esse estado). Quem chama segura t.mu.
func (t *Tracker) noteNoPeer(v View, recv time.Time, measure bool) {
	if !measure || t.noPeerFrom.IsZero() || recv.Before(t.noPeerFrom) || recv.Before(t.grace) {
		return
	}
	for _, c := range v.Calls {
		if c.State != "talking" || c.Peer.Kind != "unknown" || t.noPeerSeen[c.ID] {
			continue
		}
		if t.noPeerSeen == nil {
			t.noPeerSeen = map[string]bool{}
		}
		t.noPeerSeen[c.ID] = true
		t.nNoPeer++
		if len(t.noPeerSamples) < 10 {
			t.noPeerSamples = append(t.noPeerSamples, fmt.Sprintf("[fase %s] ramal %s: %s", t.phase, v.Number, callsStr([]amisim.CallView{c})))
		}
	}
}

// NoPeer devolve (ligado, ocorrencias, amostras).
func (t *Tracker) NoPeer() (bool, int, []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.noPeerFrom.IsZero(), t.nNoPeer, append([]string(nil), t.noPeerSamples...)
}

// torn: o estado so difere da verdade por combinar registro/dispositivo de um instante com as
// chamadas de outro (DeviceStateChange e eventos de canal sao mensagens AMI separadas).
func torn(v View, cands []View) bool {
	regs, devs := map[string]bool{}, map[string]bool{}
	for _, c := range cands {
		regs[c.Registration], devs[c.DeviceState] = true, true
	}
	if !regs[v.Registration] || !devs[v.DeviceState] {
		return false
	}
	for _, c := range cands {
		w := v
		w.Registration, w.DeviceState = c.Registration, c.DeviceState
		if amisim.FingerprintLike(w, c) == amisim.Fingerprint(c) {
			return true
		}
	}
	return false
}

func (t *Tracker) Torn() int { t.mu.Lock(); defer t.mu.Unlock(); return t.nTorn }

func truthsStr(list []pend, base View) string {
	o := "base" + callsStr(base.Calls)
	for _, p := range list {
		o += " | " + p.truth.DeviceState + callsStr(p.truth.Calls)
	}
	return o
}

func callsStr(cs []amisim.CallView) string {
	s := ""
	for _, c := range cs {
		s += fmt.Sprintf("[%s %s %s %s:%s]", c.ID, c.Direction, c.State, c.Peer.Kind, c.Peer.Number)
	}
	return s
}

// Latencies devolve as amostras de uma fase (copia).
func (t *Tracker) Latencies(phase string) []time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]time.Duration(nil), t.lat[phase]...)
}

// AllLatencies junta as fases dadas.
func (t *Tracker) AllLatencies(phases ...string) []time.Duration {
	var o []time.Duration
	for _, p := range phases {
		o = append(o, t.Latencies(p)...)
	}
	return o
}

func (t *Tracker) PendingCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, l := range t.pending {
		n += len(l)
	}
	return n
}

func (t *Tracker) Unexpected() (int, []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.nUnexp, append([]string(nil), t.unexpected...)
}

// ---- comparacao daemon x verdade

// Diff descreve uma divergencia.
type Diff struct{ Ext, What string }

func timeNear(a, b string, tol time.Duration) bool {
	ta, e1 := time.Parse(time.RFC3339Nano, a)
	tb, e2 := time.Parse(time.RFC3339Nano, b)
	if e1 != nil || e2 != nil {
		return false
	}
	d := ta.Sub(tb)
	return d < tol && d > -tol
}

// compareView compara o ramal do daemon com a verdade. Ignora nome (o PJSIP nao
// o traz na lista de endpoints), horarios dentro de 3 s e o outro lado quando ambiguo.
func compareView(got, want View, tech string) []Diff {
	var d []Diff
	add := func(f string, a ...any) { d = append(d, Diff{want.Number, fmt.Sprintf(f, a...)}) }
	if got.Technology != tech {
		add("technology %q, esperado %q", got.Technology, tech)
	}
	if got.Group != "Sem grupo" {
		add("group %q, esperado \"Sem grupo\"", got.Group)
	}
	if got.Registration != want.Registration {
		add("registration %q, esperado %q", got.Registration, want.Registration)
	}
	if got.DeviceState != want.DeviceState {
		add("device_state %q, esperado %q", got.DeviceState, want.DeviceState)
	}
	if want.Registration != "unregistered" && got.IP != want.IP {
		add("ip %q, esperado %q", got.IP, want.IP)
	}
	if len(got.Calls) != len(want.Calls) {
		add("%d chamadas (%s), esperado %d (%s)", len(got.Calls), callsStr(got.Calls), len(want.Calls), callsStr(want.Calls))
		return d
	}
	wc := map[string]amisim.CallView{}
	for _, c := range want.Calls {
		wc[c.ID] = c
	}
	for _, c := range got.Calls {
		w, ok := wc[c.ID]
		if !ok {
			add("chamada %s inesperada", c.ID)
			continue
		}
		if c.Direction != w.Direction || c.State != w.State {
			add("chamada %s: %s/%s, esperado %s/%s", c.ID, c.Direction, c.State, w.Direction, w.State)
		}
		if !w.Ambiguous && (c.Peer.Kind != w.Peer.Kind || c.Peer.Number != w.Peer.Number) {
			add("chamada %s: com quem fala %s:%s, esperado %s:%s", c.ID, c.Peer.Kind, c.Peer.Number, w.Peer.Kind, w.Peer.Number)
		}
		if !timeNear(c.StartedAt, w.StartedAt, 3*time.Second) {
			add("chamada %s: started_at %s, esperado perto de %s", c.ID, c.StartedAt, w.StartedAt)
		}
		if (w.AnsweredAt != "") != (c.AnsweredAt != "") {
			add("chamada %s: answered_at %q, esperado %q", c.ID, c.AnsweredAt, w.AnsweredAt)
		}
	}
	return d
}
