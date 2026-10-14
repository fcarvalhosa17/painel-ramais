package amisim

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// Step e uma fase de um cenario de chamada; Fn roda sob s.mu numa Tx.
type Step struct {
	Name string
	Wait time.Duration // espera antes da fase
	Fn   func(t *Tx)
}

// Script e um cenario de chamada pronto para rodar passo a passo (testes) ou em tempo.
type Script struct {
	s     *Sim
	Kind  string
	Exts  []*ext
	Steps []Step
	chans []*chanT
	done  bool
}

// Timing controla as esperas de um cenario sorteado.
type Timing struct {
	RingMin, RingMax time.Duration
	TalkMin, TalkMax time.Duration
}

var DefaultTiming = Timing{RingMin: 500 * time.Millisecond, RingMax: 3 * time.Second, TalkMin: 5 * time.Second, TalkMax: 30 * time.Second}

// Kinds lista os cenarios. Participantes (ordem de Parts.Exts):
//
//	internal, internal-hold, internal-noanswer, local-internal: chamador, destino
//	inbound, local-inbound: destino
//	outbound: chamador
//	blind-internal, attended-merge-internal, attended-link-internal: transferido, transferidor, alvo
//	blind-inbound/outbound, attended-merge-inbound/outbound, attended-link-inbound/outbound: transferidor, alvo
var Kinds = []string{
	"internal", "internal-hold", "internal-noanswer", "local-internal",
	"inbound", "local-inbound", "outbound",
	"blind-internal", "blind-inbound", "blind-outbound",
	"attended-merge-internal", "attended-merge-inbound", "attended-merge-outbound",
	"attended-link-internal", "attended-link-inbound", "attended-link-outbound",
}

var weights = map[string]int{
	"internal": 24, "internal-hold": 5, "internal-noanswer": 5, "local-internal": 4,
	"inbound": 18, "local-inbound": 8, "outbound": 14,
	"blind-internal": 3, "blind-inbound": 3, "blind-outbound": 2,
	"attended-merge-internal": 3, "attended-merge-inbound": 2, "attended-merge-outbound": 1,
	"attended-link-internal": 3, "attended-link-inbound": 2, "attended-link-outbound": 1,
}

// ExtsNeeded diz quantos ramais o cenario usa.
func ExtsNeeded(kind string) int {
	switch kind {
	case "inbound", "local-inbound", "outbound":
		return 1
	case "blind-inbound", "blind-outbound", "attended-merge-inbound", "attended-merge-outbound",
		"attended-link-inbound", "attended-link-outbound",
		"internal", "internal-hold", "internal-noanswer", "local-internal":
		return 2
	}
	return 3
}

// Parts define quem participa. Trunk vazio = primeiro tronco. XNum/XName: parte externa.
type Parts struct {
	Exts        []string
	Trunk       string
	XNum, XName string
	DID         string
}

// NewScript monta o cenario com ramais escolhidos (reserva-os). Sem Timing, todas as esperas sao 0.
func (s *Sim) NewScript(kind string, p Parts, tm *Timing) (*Script, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(p.Exts) != ExtsNeeded(kind) {
		return nil, fmt.Errorf("cenario %s precisa de %d ramais", kind, ExtsNeeded(kind))
	}
	var es []*ext
	for _, n := range p.Exts {
		e := s.byNum[n]
		if e == nil {
			return nil, fmt.Errorf("ramal %s nao existe", n)
		}
		if e.busy {
			return nil, fmt.Errorf("ramal %s ja esta em um cenario", n)
		}
		es = append(es, e)
	}
	for _, e := range es {
		s.takeFree(e)
	}
	return s.build(kind, es, p, tm), nil
}

var extNames = []string{"ACME LTDA", "Joao Pereira", "Maria Clara", "Padaria Estrela", "Transportes Rio", "", "Clinica Vida", "", "Banco Digital SA", "Loja do Ze"}

// RandomScript sorteia ramais livres e registrados. Devolve nil se nao ha ramais livres.
func (s *Sim) RandomScript(kind string, tm *Timing) *Script {
	s.mu.Lock()
	defer s.mu.Unlock()
	var es []*ext
	for i := 0; i < ExtsNeeded(kind); i++ {
		e := s.pickFree()
		if e == nil {
			for _, x := range es {
				s.releaseExt(x)
			}
			return nil
		}
		es = append(es, e)
	}
	p := Parts{
		Trunk: s.trunks[s.rng.Intn(len(s.trunks))],
		XNum:  "119" + fmt.Sprintf("%08d", s.rng.Intn(100000000)),
		XName: extNames[s.rng.Intn(len(extNames))],
		DID:   "3" + fmt.Sprintf("%07d", s.rng.Intn(10000000)),
	}
	return s.build(kind, es, p, tm)
}

// PickKind sorteia um cenario pelo peso.
func (s *Sim) PickKind(r *rand.Rand) string {
	tot := 0
	for _, k := range Kinds {
		tot += weights[k]
	}
	n := r.Intn(tot)
	for _, k := range Kinds {
		if n < weights[k] {
			return k
		}
		n -= weights[k]
	}
	return "internal"
}

type vars struct {
	x, y, z, b2, l1, l2 *chanT
	br2                 string
}

func jitter(r *rand.Rand, lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(r.Int63n(int64(hi-lo)))
}

func (s *Sim) build(kind string, es []*ext, p Parts, tm *Timing) *Script {
	sc := &Script{s: s, Kind: kind, Exts: es}
	var ring, talk, gap time.Duration
	if tm != nil {
		ring = jitter(s.rng, tm.RingMin, tm.RingMax)
		talk = jitter(s.rng, tm.TalkMin, tm.TalkMax)
		gap = 200 * time.Millisecond
	}
	trunk := p.Trunk
	if trunk == "" {
		trunk = s.trunks[0]
	}
	xnum, xname, did := p.XNum, p.XName, p.DID
	if xnum == "" {
		xnum = "11987654321"
	}
	if did == "" {
		did = "30001000"
	}
	dialStr := func(num string) string {
		if s.cfg.Mode == ModeSIP {
			return trunk + "/" + num
		}
		return num + "@" + trunk
	}
	v := &vars{}
	add := func(name string, w time.Duration, fn func(t *Tx)) { sc.Steps = append(sc.Steps, Step{name, w, fn}) }

	// base: monta ring+answer da chamada que depois sera transferida; v.x = transferido, v.y = perna do transferidor
	base := func(variant string, tferee, tferer *ext) {
		switch variant {
		case "internal":
			add("ring", 0, func(t *Tx) {
				v.x = t.extLeg(tferee, tferer.num, "")
				t.state(v.x, 4)
				t.track(v.x, tferee, "internal", "extension", tferer.num, tferer.name, false)
				v.y = t.dialExt(v.x, tferer, "internal", "extension", tferee.num, tferee.name)
			})
		case "inbound":
			add("ring", 0, func(t *Tx) {
				v.x = t.trunkLeg(trunk, xnum, xname, did, "inbound", "")
				t.state(v.x, 4)
				v.y = t.dialExt(v.x, tferer, "inbound", "external", xnum, xname)
			})
		case "outbound":
			add("ring", 0, func(t *Tx) {
				v.y = t.extLeg(tferer, xnum, "")
				t.state(v.y, 4)
				t.track(v.y, tferer, "outbound", "external", xnum, "", false)
				v.x = t.trunkLeg(trunk, tferer.num, tferer.name, "", "outbound", v.y.linked)
				v.x.rnum = xnum
				t.dial(v.y, v.x, dialStr(xnum))
				t.state(v.x, 5)
				t.state(v.y, 5)
			})
		}
		add("answer", ring, func(t *Tx) {
			if variant == "outbound" {
				t.answer(v.y, v.x)
				t.connected(v.y, xnum, "")
				t.connected(v.x, xnum, "")
			} else {
				t.answer(v.x, v.y)
			}
		})
	}
	hangAll := func(cs ...**chanT) func(t *Tx) {
		return func(t *Tx) {
			for _, c := range cs {
				if *c != nil {
					t.hangup(*c)
				}
			}
		}
	}

	switch kind {
	case "internal", "internal-hold", "internal-noanswer":
		a, b := es[0], es[1]
		add("ring", 0, func(t *Tx) {
			v.x = t.extLeg(a, b.num, "")
			t.state(v.x, 4)
			t.track(v.x, a, "internal", "extension", b.num, b.name, false)
			v.y = t.dialExt(v.x, b, "internal", "extension", a.num, a.name)
		})
		if kind == "internal-noanswer" {
			add("cancel", ring, func(t *Tx) {
				t.dialEnd(v.x, v.y, "CANCEL")
				t.hangup(v.y)
				t.hangup(v.x)
			})
			break
		}
		add("answer", ring, func(t *Tx) { t.answer(v.x, v.y) })
		if kind == "internal-hold" {
			add("hold", talk/3, func(t *Tx) { t.setHold(v.y, true) })
			add("unhold", talk/3, func(t *Tx) { t.setHold(v.y, false) })
		}
		add("hangup", talk, hangAll(&v.x, &v.y))
	case "local-internal":
		a, b := es[0], es[1]
		add("ring", 0, func(t *Tx) {
			v.x = t.extLeg(a, b.num, "")
			t.state(v.x, 4)
			t.track(v.x, a, "internal", "extension", b.num, b.name, false).soft = true
			v.l1, v.l2, v.y = t.dialLocal(v.x, b, "internal", "extension", a.num, a.name)
		})
		add("answer", ring, func(t *Tx) { t.answerLocal(v.x, v.l1, v.l2, v.y) })
		add("optimize", gap, func(t *Tx) { t.optimizeLocal(v.x, v.l1, v.l2, v.y) })
		add("hangup", talk, hangAll(&v.x, &v.y, &v.l1, &v.l2))
	case "inbound":
		base("inbound", nil, es[0])
		add("hangup", talk, hangAll(&v.x, &v.y))
	case "local-inbound":
		b := es[0]
		add("ring", 0, func(t *Tx) {
			v.x = t.trunkLeg(trunk, xnum, xname, did, "inbound", "")
			t.state(v.x, 4)
			v.l1, v.l2, v.y = t.dialLocal(v.x, b, "inbound", "external", xnum, xname)
		})
		add("answer", ring, func(t *Tx) { t.answerLocal(v.x, v.l1, v.l2, v.y) })
		add("optimize", gap, func(t *Tx) { t.optimizeLocal(v.x, v.l1, v.l2, v.y) })
		add("hangup", talk, hangAll(&v.x, &v.y, &v.l1, &v.l2))
	case "outbound":
		base("outbound", nil, es[0])
		add("hangup", talk, hangAll(&v.y, &v.x))
	default:
		sc.buildTransfer(kind, es, v, base, add, hangAll, talk, gap, ring)
	}
	return sc
}

// buildTransfer monta os cenarios de transferencia cega e assistida.
func (sc *Script) buildTransfer(kind string, es []*ext, v *vars, base func(string, *ext, *ext),
	add func(string, time.Duration, func(*Tx)), hangAll func(...**chanT) func(*Tx), talk, gap, ring time.Duration) {
	var variant string
	var tferee, tferer, target *ext
	switch {
	case hasSuffix(kind, "-internal"):
		variant, tferee, tferer, target = "internal", es[0], es[1], es[2]
	case hasSuffix(kind, "-inbound"):
		variant, tferer, target = "inbound", es[0], es[1]
	default:
		variant, tferer, target = "outbound", es[0], es[1]
	}
	base(variant, tferee, tferer)
	add("talk", talk/2, func(t *Tx) {})
	retarget := func(t *Tx) {
		dir, pk, pn, pname := partyOf(v.x)
		t.retarget(v.z, dir, pk, pn, pname)
		if v.x.e != nil {
			t.retarget(v.x, "internal", "extension", target.num, target.name)
		}
		if v.x.e != nil {
			t.connected(v.x, target.num, target.name)
		}
		t.connected(v.z, pn, pname)
	}
	switch {
	case hasPrefix(kind, "blind"):
		add("blind-transfer", 0, func(t *Tx) {
			t.hangup(v.y)
			t.leave(v.x)
			v.x.exten = target.num // o redirect leva o canal do transferido ao destino no plano de discagem
			dir, pk, pn, pname := partyOf(v.x)
			v.z = t.dialExt(v.x, target, dir, pk, pn, pname)
			if v.x.e != nil {
				// o transferido volta a "ringing": a nova ponta ainda nao atendeu (contrato 2.2)
				t.retarget(v.x, "internal", "extension", target.num, target.name)
				t.reRing(v.x)
				t.connected(v.x, target.num, target.name)
			}
		})
		add("target-answer", ring, func(t *Tx) {
			t.up(v.z)
			t.upTrack(v.x)
			t.dialEnd(v.x, v.z, "ANSWER")
			br := t.newBridge()
			t.enter(br, v.x)
			t.enter(br, v.z)
		})
		add("hangup", talk, hangAll(&v.x, &v.z))
	default:
		add("hold-transferee", gap, func(t *Tx) { t.setHold(v.y, true) })
		add("consult-ring", gap, func(t *Tx) {
			yext := v.y.e
			v.b2 = t.extLeg(yext, target.num, "")
			t.state(v.b2, 4)
			t.track(v.b2, yext, "internal", "extension", target.num, target.name, false)
			v.z = t.dialExt(v.b2, target, "internal", "extension", yext.num, yext.name)
		})
		add("consult-answer", ring, func(t *Tx) { v.br2 = t.answer(v.b2, v.z) })
		if hasPrefix(kind, "attended-merge") {
			add("complete-merge", gap, func(t *Tx) {
				t.hangup(v.y)
				t.hangup(v.b2)
				t.leave(v.x)
				t.enter(v.br2, v.x)
				retarget(t)
			})
			add("hangup", talk, hangAll(&v.x, &v.z))
		} else {
			add("complete-link", gap, func(t *Tx) {
				br1 := v.x.bridge
				t.hangup(v.y)
				t.hangup(v.b2)
				n1, n2 := t.localNames(target.num, "transfer")
				v.l1 = t.newChan(n1, 6, "", "", target.num, "transfer", v.x.linked)
				v.l2 = t.newChan(n2, 6, "", "", target.num, "transfer", v.x.linked)
				t.enter(br1, v.l1)
				t.enter(v.br2, v.l2)
				retarget(t)
			})
			add("hangup", talk, hangAll(&v.x, &v.l1, &v.l2, &v.z))
		}
	}
}

func hasSuffix(s, suf string) bool { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }
func hasPrefix(s, p string) bool   { return len(s) >= len(p) && s[:len(p)] == p }

// Do executa a fase i na hora (sem esperar). Para testes deterministas.
func (sc *Script) Do(i int) {
	sc.s.tx(func(t *Tx) {
		t.sc = sc
		sc.Steps[i].Fn(t)
	})
}

// Run executa todas as fases respeitando as esperas (multiplicadas por scale).
// Cancelado, derruba os canais que sobraram. Sempre libera os ramais.
func (sc *Script) Run(ctx context.Context, scale float64) {
	for i := range sc.Steps {
		if w := time.Duration(float64(sc.Steps[i].Wait) * scale); w > 0 {
			tm := time.NewTimer(w)
			select {
			case <-ctx.Done():
				tm.Stop()
				sc.Abort()
				return
			case <-tm.C:
			}
		}
		sc.Do(i)
	}
	sc.Abort()
}

// Abort derruba os canais do cenario que ainda existem e libera os ramais.
func (sc *Script) Abort() {
	s := sc.s
	s.tx(func(t *Tx) {
		if sc.done {
			return
		}
		sc.done = true
		for i := len(sc.chans) - 1; i >= 0; i-- {
			t.hangup(sc.chans[i])
		}
		for _, e := range sc.Exts {
			s.releaseExt(e)
		}
	})
}

// StartLoad mantem n cenarios simultaneos ate ctx terminar. Devolve quando todos acabarem.
func (s *Sim) StartLoad(ctx context.Context, n int, tm Timing) *sync.WaitGroup {
	wg := &sync.WaitGroup{}
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(s.cfg.Seed*1000 + int64(w)))
			// espalha o inicio para nao nascer tudo no mesmo instante
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter(r, 0, tm.RingMax+tm.TalkMin)):
			}
			for ctx.Err() == nil {
				s.mu.Lock()
				kind := s.PickKind(r)
				s.mu.Unlock()
				sc := s.RandomScript(kind, &tm)
				if sc == nil {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				sc.Run(ctx, 1)
			}
		}(w)
	}
	return wg
}

// Idle diz se nao ha nenhum canal vivo.
func (s *Sim) Idle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.chans) == 0
}
