package state

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"painel-ramais/internal/model"
)

var t0 = time.Date(2026, 10, 13, 14, 0, 0, 0, time.UTC)

type harness struct {
	t   *testing.T
	s   *State
	now time.Time
	seq int
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, now: t0}
	h.s = New(regexp.MustCompile(`^[0-9]+$`), nil, Options{Now: func() time.Time { return h.now }})
	return h
}

// step avanca 1 s e devolve o instante, para os eventos terem horas distintas.
func (h *harness) step() time.Time { h.now = h.now.Add(time.Second); return h.now }

func (h *harness) feed(evs ...model.Event) {
	for _, e := range evs {
		h.s.Apply(e)
	}
}

// online sincroniza com ramais 2001, 2002 e o tronco "operadora".
func (h *harness) online() {
	at := h.step()
	h.feed(
		model.SyncStarted{At: at, Technology: model.TechPJSIP, AsteriskVersion: "18.20.0", Reason: model.SyncConnect},
		model.EndpointSeen{At: at, Name: "2001", CallerIDName: "Maria", Technology: model.TechPJSIP},
		model.EndpointSeen{At: at, Name: "2002", CallerIDName: "Joao", Technology: model.TechPJSIP},
		model.EndpointSeen{At: at, Name: "operadora", Technology: model.TechPJSIP},
		model.RegistrationChanged{At: at, Name: "2001", State: model.RegRegistered, IP: "192.0.2.1"},
		model.RegistrationChanged{At: at, Name: "2002", State: model.RegRegistered, IP: "192.0.2.2"},
		model.SyncFinished{At: at},
	)
	h.s.Flush() // consome o Reset
}

func (h *harness) ext(n string) model.Extension {
	h.t.Helper()
	x := h.s.cur.exts[n]
	if x == nil {
		h.t.Fatalf("ramal %s inexistente", n)
	}
	return x.model(h.s.cur.tech)
}

func (h *harness) call(n string) model.Call {
	h.t.Helper()
	x := h.ext(n)
	if len(x.Calls) != 1 {
		h.t.Fatalf("ramal %s: %d chamadas, queria 1: %+v", n, len(x.Calls), x.Calls)
	}
	return x.Calls[0]
}

func chn(at time.Time, uid, linked, name, endpoint, num, nm, exten string) model.ChannelCreated {
	return model.ChannelCreated{At: at, Uniqueid: uid, Linkedid: linked, Channel: name, Endpoint: endpoint, CallerNum: num, CallerName: nm, Exten: exten}
}

func local(at time.Time, uid, linked, name string) model.ChannelCreated {
	return model.ChannelCreated{At: at, Uniqueid: uid, Linkedid: linked, Channel: name, IsLocal: true}
}

func up(at time.Time, uid string) model.ChannelStateChanged {
	return model.ChannelStateChanged{At: at, Uniqueid: uid, State: model.ChanUp}
}

func wantPeer(t *testing.T, c model.Call, kind model.PeerKind, number, name string) {
	t.Helper()
	got := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	if c.Peer.Kind != kind || got(c.Peer.Number) != number || got(c.Peer.Name) != name {
		t.Errorf("peer = %s/%q/%q, queria %s/%q/%q", c.Peer.Kind, got(c.Peer.Number), got(c.Peer.Name), kind, number, name)
	}
}

// As cinco situacoes de chamada: interna, entrada, saida, transferencia e Local channel.
func TestCallSituations(t *testing.T) {
	t.Run("interna toca e atende", func(t *testing.T) {
		h := newHarness(t)
		h.online()
		at := h.step()
		h.feed(
			chn(at, "u1", "u1", "PJSIP/2001-0001", "2001", "2001", "Maria", "2002"),
			chn(at, "u2", "u1", "PJSIP/2002-0002", "2002", "2002", "Joao", "2002"),
			up(at, "u1"),
			model.DialStarted{At: at, SrcUniqueid: "u1", DstUniqueid: "u2"},
		)
		a, b := h.call("2001"), h.call("2002")
		if a.Direction != model.DirInternal || a.State != model.CallRinging || a.AnsweredAt != nil {
			t.Errorf("origem tocando: %+v", a)
		}
		wantPeer(t, a, model.PeerExtension, "2002", "Joao")
		wantPeer(t, b, model.PeerExtension, "2001", "Maria")
		if b.Direction != model.DirInternal || b.State != model.CallRinging {
			t.Errorf("destino tocando: %+v", b)
		}
		at = h.step()
		h.feed(up(at, "u2"), model.BridgeEntered{At: at, BridgeID: "b1", Uniqueid: "u1"}, model.BridgeEntered{At: at, BridgeID: "b1", Uniqueid: "u2"})
		for _, n := range []string{"2001", "2002"} {
			c := h.call(n)
			if c.State != model.CallTalking || c.AnsweredAt == nil || !c.AnsweredAt.Equal(at) {
				t.Errorf("%s em conversa: %+v", n, c)
			}
		}
		at = h.step()
		h.feed(model.ChannelHungup{At: at, Uniqueid: "u1"}, model.ChannelHungup{At: at, Uniqueid: "u2"})
		if len(h.ext("2001").Calls) != 0 || len(h.ext("2002").Calls) != 0 {
			t.Error("depois do Hangup a lista deve ficar vazia")
		}
		if h.ext("2001").Calls == nil {
			t.Error("Calls nunca pode ser nil (JSON [])")
		}
	})

	t.Run("entrada pelo tronco", func(t *testing.T) {
		h := newHarness(t)
		h.online()
		at := h.step()
		h.feed(
			chn(at, "t1", "t1", "PJSIP/operadora-0001", "operadora", "11987654321", "ACME LTDA", "2001"),
			chn(at, "e1", "t1", "PJSIP/2001-0002", "2001", "2001", "Maria", "2001"),
			model.DialStarted{At: at, SrcUniqueid: "t1", DstUniqueid: "e1"},
		)
		c := h.call("2001")
		if c.Direction != model.DirInbound || c.State != model.CallRinging {
			t.Errorf("entrada tocando: %+v", c)
		}
		wantPeer(t, c, model.PeerExternal, "11987654321", "ACME LTDA")
		at = h.step()
		h.feed(up(at, "t1"), up(at, "e1"), model.BridgeEntered{At: at, BridgeID: "b", Uniqueid: "t1"}, model.BridgeEntered{At: at, BridgeID: "b", Uniqueid: "e1"})
		if c = h.call("2001"); c.State != model.CallTalking || c.Direction != model.DirInbound {
			t.Errorf("entrada em conversa: %+v", c)
		}
	})

	t.Run("saida pelo tronco", func(t *testing.T) {
		h := newHarness(t)
		h.online()
		at := h.step()
		e := chn(at, "e1", "e1", "PJSIP/2001-0001", "2001", "2001", "Maria", "01133334444")
		tr := chn(at, "t1", "e1", "PJSIP/operadora-0002", "operadora", "2001", "Maria", "01133334444")
		h.feed(e, up(at, "e1"), tr, model.DialStarted{At: at, SrcUniqueid: "e1", DstUniqueid: "t1"})
		c := h.call("2001")
		if c.Direction != model.DirOutbound || c.State != model.CallRinging {
			t.Errorf("saida chamando: %+v", c)
		}
		wantPeer(t, c, model.PeerExternal, "01133334444", "")
		at = h.step()
		h.feed(up(at, "t1"), model.BridgeEntered{At: at, BridgeID: "b", Uniqueid: "e1"}, model.BridgeEntered{At: at, BridgeID: "b", Uniqueid: "t1"})
		if c = h.call("2001"); c.State != model.CallTalking {
			t.Errorf("saida em conversa: %+v", c)
		}
	})

	t.Run("transferencia da entrada para outro ramal", func(t *testing.T) {
		h := newHarness(t)
		h.online()
		// 2001 atende o tronco externo (entrada)...
		at := h.step()
		h.feed(
			chn(at, "t1", "t1", "PJSIP/operadora-0001", "operadora", "11987654321", "ACME LTDA", "2001"),
			chn(at, "e1", "t1", "PJSIP/2001-0002", "2001", "2001", "Maria", "2001"),
			model.DialStarted{At: at, SrcUniqueid: "t1", DstUniqueid: "e1"}, up(at, "t1"), up(at, "e1"),
			model.BridgeEntered{At: at, BridgeID: "b1", Uniqueid: "t1"}, model.BridgeEntered{At: at, BridgeID: "b1", Uniqueid: "e1"},
		)
		// ...e transfere para 2002: o tronco sai da bridge 1 e entra na bridge do 2002.
		at = h.step()
		h.feed(
			chn(at, "e2", "t1", "PJSIP/2002-0003", "2002", "2002", "Joao", "2002"),
			model.DialStarted{At: at, SrcUniqueid: "e1", DstUniqueid: "e2"}, up(at, "e2"),
			model.BridgeLeft{At: at, BridgeID: "b1", Uniqueid: "t1"},
			model.BridgeEntered{At: at, BridgeID: "b2", Uniqueid: "t1"}, model.BridgeEntered{At: at, BridgeID: "b2", Uniqueid: "e2"},
			model.ChannelHungup{At: at, Uniqueid: "e1"},
		)
		if n := len(h.ext("2001").Calls); n != 0 {
			t.Errorf("quem transferiu ficou com %d chamadas", n)
		}
		c := h.call("2002")
		if c.Direction != model.DirInbound || c.State != model.CallTalking {
			t.Errorf("destino da transferencia: %+v", c)
		}
		wantPeer(t, c, model.PeerExternal, "11987654321", "ACME LTDA")
	})

	t.Run("fila via Local channel", func(t *testing.T) {
		h := newHarness(t)
		h.online()
		at := h.step()
		// tronco -> Dial(Local;1) ; Local;2 -> Dial(2001)
		h.feed(
			chn(at, "t1", "t1", "PJSIP/operadora-0001", "operadora", "11987654321", "ACME LTDA", "100"),
			local(at, "l1", "t1", "Local/2001@from-queue-0000001a;1"),
			local(at, "l2", "t1", "Local/2001@from-queue-0000001a;2"),
			chn(at, "e1", "t1", "PJSIP/2001-0002", "2001", "2001", "Maria", "2001"),
			model.DialStarted{At: at, SrcUniqueid: "t1", DstUniqueid: "l1"},
			model.DialStarted{At: at, SrcUniqueid: "l2", DstUniqueid: "e1"},
		)
		c := h.call("2001")
		if c.Direction != model.DirInbound || c.State != model.CallRinging {
			t.Errorf("toque via Local: %+v", c)
		}
		wantPeer(t, c, model.PeerExternal, "11987654321", "ACME LTDA")
		at = h.step()
		h.feed(up(at, "t1"), up(at, "e1"),
			model.BridgeEntered{At: at, BridgeID: "b1", Uniqueid: "t1"}, model.BridgeEntered{At: at, BridgeID: "b1", Uniqueid: "l1"},
			model.BridgeEntered{At: at, BridgeID: "b2", Uniqueid: "l2"}, model.BridgeEntered{At: at, BridgeID: "b2", Uniqueid: "e1"})
		c = h.call("2001")
		if c.State != model.CallTalking {
			t.Errorf("conversa via Local: %+v", c)
		}
		wantPeer(t, c, model.PeerExternal, "11987654321", "ACME LTDA")
		if len(h.s.cur.byEndpoint) != 1 {
			t.Error("Local nao pode virar chamada de ramal")
		}
	})
}

func TestHoldAndUnhold(t *testing.T) {
	h := newHarness(t)
	h.online()
	at := h.step()
	h.feed(
		chn(at, "t1", "t1", "PJSIP/operadora-0001", "operadora", "11999", "", "2001"),
		chn(at, "e1", "t1", "PJSIP/2001-0002", "2001", "2001", "Maria", "2001"),
		model.DialStarted{At: at, SrcUniqueid: "t1", DstUniqueid: "e1"}, up(at, "t1"), up(at, "e1"),
		model.BridgeEntered{At: at, BridgeID: "b", Uniqueid: "t1"}, model.BridgeEntered{At: at, BridgeID: "b", Uniqueid: "e1"},
		model.HoldChanged{At: at, Uniqueid: "e1", OnHold: true},
	)
	if c := h.call("2001"); c.State != model.CallHold {
		t.Errorf("em espera: %+v", c)
	}
	h.feed(model.HoldChanged{At: at, Uniqueid: "e1", OnHold: false})
	if c := h.call("2001"); c.State != model.CallTalking {
		t.Errorf("fora da espera: %+v", c)
	}
}

func TestWithoutAMIEverythingIsUnknown(t *testing.T) {
	h := newHarness(t)
	// Antes da primeira conexao: nada de ramal, ami_connected=false.
	if o, ok := h.s.Flush().(model.Reset); !ok || o.AMIConnected || len(o.Extensions) != 0 {
		t.Fatalf("Reset inicial: %+v", o)
	}
	h.online()
	at := h.step()
	h.feed(
		chn(at, "e1", "e1", "PJSIP/2001-0001", "2001", "2001", "", "2002"), up(at, "e1"),
		model.RegistrationChanged{At: at, Name: "2002", State: model.RegUnreachable},
	)
	epoch := h.s.epoch
	h.feed(model.AMIDisconnected{At: h.step(), Reason: "eof"})
	r, ok := h.s.Flush().(model.Reset)
	if !ok {
		t.Fatal("queda do AMI deve gerar Reset")
	}
	if r.AMIConnected || r.Epoch == epoch || len(r.Extensions) != 2 {
		t.Fatalf("Reset da queda: connected=%v epoch=%s/%s n=%d", r.AMIConnected, r.Epoch, epoch, len(r.Extensions))
	}
	for _, x := range r.Extensions {
		if x.Registration != model.RegUnknown || len(x.Calls) != 0 || x.DeviceState != model.DevUnknown {
			t.Errorf("%s: %+v (nunca unregistered por falta de AMI)", x.Number, x)
		}
	}
	// Reconexoes falhando nao geram epoch novo.
	e2 := h.s.epoch
	h.feed(model.AMIDisconnected{At: h.step(), Reason: "dial tcp: refused"})
	if h.s.epoch != e2 || h.s.Flush() != nil {
		t.Error("AMIDisconnected repetido nao deve gerar epoch nem Reset")
	}
	// Reconectou: sincroniza e epoch novo; a visao antiga fica publicada ate o SyncFinished.
	at = h.step()
	h.feed(model.SyncStarted{At: at, Technology: model.TechPJSIP, Reason: model.SyncConnect}, model.EndpointSeen{At: at, Name: "2001"})
	if h.s.Flush() != nil {
		t.Error("durante a sincronizacao nada e publicado")
	}
	h.feed(model.SyncFinished{At: at})
	r, _ = h.s.Flush().(model.Reset)
	if !r.AMIConnected || r.Epoch == e2 || len(r.Extensions) != 1 || r.Extensions[0].Registration != model.RegUnregistered {
		t.Fatalf("Reset da reconexao: %+v", r)
	}
	if st := h.s.Stats(); !st.AMIConnected || st.Total.Unregistered != 1 {
		t.Errorf("stats: %+v", st)
	}
}

func TestBatchCoalescesAndVersions(t *testing.T) {
	h := newHarness(t)
	h.online()
	v0 := h.s.version
	at := h.step()
	for _, st := range []model.Registration{model.RegUnreachable, model.RegUnregistered, model.RegRegistered, model.RegUnregistered} {
		h.feed(model.RegistrationChanged{At: at, Name: "2001", State: st})
	}
	h.feed(model.RegistrationChanged{At: at, Name: "2002", State: model.RegRegistered, IP: "192.0.2.2"}) // sem mudanca
	b, ok := h.s.Flush().(model.Batch)
	if !ok {
		t.Fatal("esperava Batch")
	}
	if len(b.Extensions) != 1 || b.Extensions[0].Number != "2001" || b.Extensions[0].Registration != model.RegUnregistered {
		t.Fatalf("2001 deve vir uma vez, com o estado final: %+v", b.Extensions)
	}
	if b.FromVersion != v0 || b.ToVersion <= b.FromVersion || b.Epoch != h.s.epoch || !b.AMIConnected {
		t.Fatalf("versoes: %+v (v0=%d)", b, v0)
	}
	if h.s.Flush() != nil {
		t.Error("sem mudanca nao ha lote")
	}
}

func TestGroupsAndUnknownEndpoints(t *testing.T) {
	h := newHarness(t)
	h.online()
	h.feed(model.GroupsLoaded{At: h.step(), Ranges: []model.GroupRange{{Start: 2000, End: 2001, Name: "Suporte"}, {Start: 1, End: 10, Name: "Baixos"}}})
	r, ok := h.s.Flush().(model.Reset)
	if !ok || len(r.Groups) != 2 {
		t.Fatalf("GroupsLoaded gera Reset com os grupos: %+v", r)
	}
	if h.ext("2001").Group != "Suporte" || h.ext("2002").Group != model.NoGroup {
		t.Errorf("grupos: %s, %s", h.ext("2001").Group, h.ext("2002").Group)
	}
	// Evento de endpoint desconhecido cria o ramal no epoch atual; tronco nao vira ramal.
	h.feed(model.RegistrationChanged{At: h.step(), Name: "2099", State: model.RegRegistered}, model.RegistrationChanged{At: h.step(), Name: "tronco2", State: model.RegRegistered})
	b, _ := h.s.Flush().(model.Batch)
	if len(b.Extensions) != 1 || b.Extensions[0].Number != "2099" {
		t.Fatalf("lote: %+v", b)
	}
	cases := map[string]string{"2000": "Suporte", "2001": "Suporte", "2002": model.NoGroup, "5": "Baixos", "05": model.NoGroup, "abc": model.NoGroup, "99999999999999999999": model.NoGroup}
	for n, want := range cases {
		if got := groupFor(h.s.groups, n); got != want {
			t.Errorf("groupFor(%q) = %q, queria %q", n, got, want)
		}
	}
	if st := h.s.Stats(); st.Trunks != 2 || st.ByGroup["Suporte"].Registered != 1 {
		t.Errorf("stats: trunks=%d suporte=%+v", st.Trunks, st.ByGroup["Suporte"])
	}
}

func TestSnapshotCallSurvivesHangupRaceDuringSync(t *testing.T) {
	h := newHarness(t)
	at := h.step()
	h.feed(model.SyncStarted{At: at, Technology: model.TechSIP, Reason: model.SyncConnect}, model.EndpointSeen{At: at, Name: "2001"})
	// Hangup ao vivo chega antes do CoreShowChannel do mesmo canal: nao pode ressuscitar.
	h.feed(model.ChannelHungup{At: at, Uniqueid: "e1"})
	c := chn(at, "e1", "e1", "SIP/2001-0001", "2001", "2001", "", "2002")
	c.Up, c.Duration = true, 30*time.Second
	h.feed(c, model.SyncFinished{At: at})
	if len(h.ext("2001").Calls) != 0 {
		t.Error("canal desligado durante a sincronizacao voltou como chamada")
	}
	// Chamada em curso no snapshot: started_at = agora - Duration.
	h.feed(model.SyncStarted{At: at, Technology: model.TechSIP, Reason: model.SyncOverflow}, model.EndpointSeen{At: at, Name: "2001"})
	h.feed(c, model.SyncFinished{At: at})
	got := h.call("2001")
	if !got.StartedAt.Equal(at.Add(-30 * time.Second)) {
		t.Errorf("started_at = %v", got.StartedAt)
	}
}

func TestStatsRateAndLag(t *testing.T) {
	h := newHarness(t)
	h.online()
	h.feed(model.DeviceStateChanged{At: h.now.Add(-300 * time.Millisecond), Name: "2001", State: model.DevInUse})
	h.s.Flush()
	st := h.s.Stats()
	if st.LagMs != 300 || st.EventsPerSec <= 0 {
		t.Errorf("lag=%d eps=%f", st.LagMs, st.EventsPerSec)
	}
}

// 10.000 ramais e rajada de 3.000 desregistros: o lote sai inteiro, em uma passada, sem perder ramal.
func TestBurstOf3000Unregistrations(t *testing.T) {
	h := newHarness(t)
	at := h.step()
	h.feed(model.SyncStarted{At: at, Technology: model.TechPJSIP, Reason: model.SyncConnect})
	for i := 0; i < 10000; i++ {
		n := strconv.Itoa(2000 + i)
		h.feed(model.EndpointSeen{At: at, Name: n}, model.RegistrationChanged{At: at, Name: n, State: model.RegRegistered, IP: "192.0.2.1"})
	}
	h.feed(model.SyncFinished{At: at})
	if r, ok := h.s.Flush().(model.Reset); !ok || len(r.Extensions) != 10000 {
		t.Fatal("Reset com 10000 ramais")
	}
	start := time.Now()
	for i := 0; i < 3000; i++ {
		h.feed(model.RegistrationChanged{At: at, Name: strconv.Itoa(2000 + i), State: model.RegUnregistered})
	}
	b, ok := h.s.Flush().(model.Batch)
	took := time.Since(start)
	if !ok || len(b.Extensions) != 3000 || b.ToVersion-b.FromVersion != 3000 {
		t.Fatalf("lote: %d ramais, versoes %d..%d", len(b.Extensions), b.FromVersion, b.ToVersion)
	}
	if st := h.s.Stats(); st.Total.Unregistered != 3000 || st.Total.Registered != 7000 {
		t.Errorf("contagens: %+v", st.Total)
	}
	t.Logf("3000 eventos + lote em %s", took)
	if took > time.Second {
		t.Errorf("lento demais: %s", took)
	}
}

// Evento de dados que chega com o AMI fora (sessao que ja caiu) e ignorado: nao
// pode criar ramal nem trocar "unknown" por outro estado.
func TestDataEventsIgnoredWhileDisconnected(t *testing.T) {
	h := newHarness(t)
	h.online()
	h.feed(model.AMIDisconnected{At: h.step(), Reason: "eof"})
	h.s.Flush()
	h.feed(
		model.RegistrationChanged{At: h.step(), Name: "2001", State: model.RegRegistered},
		model.EndpointSeen{At: h.step(), Name: "9999"},
		chn(h.now, "u1", "u1", "PJSIP/2001-0001", "2001", "2001", "", "2002"),
	)
	if got := h.ext("2001"); got.Registration != model.RegUnknown || len(got.Calls) != 0 {
		t.Errorf("2001: %+v", got)
	}
	if _, ok := h.s.cur.exts["9999"]; ok {
		t.Error("ramal novo criado com o AMI fora")
	}
	if h.s.Flush() != nil {
		t.Error("nada a publicar")
	}
}

// snapChan monta um canal como o CoreShowChannels entrega (Duration > 0).
func snapChan(at time.Time, uid, linked, name, endpoint, num, exten string, up bool) model.ChannelCreated {
	c := chn(at, uid, linked, name, endpoint, num, "", exten)
	c.Up, c.Duration = up, 20*time.Second
	return c
}

func syncWith(h *harness, evs ...model.Event) {
	at := h.step()
	h.feed(model.SyncStarted{At: at, Technology: model.TechPJSIP, Reason: model.SyncOverflow},
		model.EndpointSeen{At: at, Name: "2001"}, model.EndpointSeen{At: at, Name: "2002"})
	h.feed(evs...)
	h.feed(model.SyncFinished{At: at})
}

// Chamada de SAIDA em conversa restaurada por snapshot: o canal do tronco e marcado
// AppDial ("(Outgoing Line)"), entao a direcao e saida e o externo e o numero discado.
func TestSnapshotRestoresOutboundCall(t *testing.T) {
	h := newHarness(t)
	h.online()
	e := snapChan(h.now, "e1", "e1", "PJSIP/2001-0001", "2001", "2001", "01133334444", true)
	e.BridgeID, e.DialData = "b1", "PJSIP/operadora/01133334444,30,tr"
	tr := snapChan(h.now, "t1", "e1", "PJSIP/operadora-0002", "operadora", "2001", "01133334444", true)
	tr.BridgeID, tr.Dialed = "b1", true
	syncWith(h, e, model.ConnectedLineChanged{At: h.now, Uniqueid: "e1", Num: "01133334444"}, tr)
	c := h.call("2001")
	if c.Direction != model.DirOutbound || c.State != model.CallTalking {
		t.Errorf("saida restaurada: %+v", c)
	}
	wantPeer(t, c, model.PeerExternal, "01133334444", "")
}

// Chamada tocando restaurada por snapshot: o Dial e reconstruido pelo Linkedid e pelo
// argumento do Dial, entao o "com quem fala" e a direcao ja vem certos antes de atender.
func TestSnapshotRestoresRingingCalls(t *testing.T) {
	t.Run("entrada tocando", func(t *testing.T) {
		h := newHarness(t)
		h.online()
		tr := snapChan(h.now, "t1", "t1", "PJSIP/operadora-0001", "operadora", "11987654321", "2001", false)
		tr.DialData = "PJSIP/2001,30,tr"
		e := snapChan(h.now, "e1", "t1", "PJSIP/2001-0002", "2001", "2001", "2001", false)
		e.Dialed = true
		syncWith(h, tr, e)
		c := h.call("2001")
		if c.Direction != model.DirInbound || c.State != model.CallRinging {
			t.Errorf("%+v", c)
		}
		wantPeer(t, c, model.PeerExternal, "11987654321", "")
	})
	t.Run("saida tocando", func(t *testing.T) {
		h := newHarness(t)
		h.online()
		e := snapChan(h.now, "e1", "e1", "PJSIP/2001-0001", "2001", "2001", "01133334444", true)
		e.DialData = "PJSIP/operadora/01133334444,30,tr" // nome do tronco nao casa por prefixo: par unico
		tr := snapChan(h.now, "t1", "e1", "PJSIP/operadora-0002", "operadora", "2001", "01133334444", false)
		tr.Dialed = true
		syncWith(h, e, tr)
		c := h.call("2001")
		if c.Direction != model.DirOutbound || c.State != model.CallRinging {
			t.Errorf("%+v", c)
		}
		wantPeer(t, c, model.PeerExternal, "01133334444", "")
	})
	t.Run("interna tocando", func(t *testing.T) {
		h := newHarness(t)
		h.online()
		a := snapChan(h.now, "u1", "u1", "PJSIP/2001-0001", "2001", "2001", "2002", true)
		a.DialData = "PJSIP/2002,30,tr"
		b := snapChan(h.now, "u2", "u1", "PJSIP/2002-0002", "2002", "2002", "2002", false)
		b.Dialed = true
		syncWith(h, a, b)
		wantPeer(t, h.call("2001"), model.PeerExtension, "2002", "")
		wantPeer(t, h.call("2002"), model.PeerExtension, "2001", "")
	})
	t.Run("fila via Local", func(t *testing.T) {
		h := newHarness(t)
		h.online()
		tr := snapChan(h.now, "t1", "t1", "PJSIP/operadora-0001", "operadora", "11987654321", "100", true)
		tr.DialData = "Local/2001@from-queue/n,30"
		l1 := local(h.now, "l1", "t1", "Local/2001@from-queue-0000001a;1")
		l1.Duration, l1.Dialed = 20*time.Second, true
		l2 := local(h.now, "l2", "t1", "Local/2001@from-queue-0000001a;2")
		l2.Duration, l2.DialData = 20*time.Second, "PJSIP/2001,30,tr"
		e := snapChan(h.now, "e1", "t1", "PJSIP/2001-0002", "2001", "2001", "2001", false)
		e.Dialed = true
		syncWith(h, tr, l1, l2, e)
		c := h.call("2001")
		if c.Direction != model.DirInbound || c.State != model.CallRinging {
			t.Errorf("%+v", c)
		}
		wantPeer(t, c, model.PeerExternal, "11987654321", "")
	})
}

func nameOf(h *harness, n string) string {
	if p := h.ext(n).Name; p != nil {
		return *p
	}
	return ""
}

// Cache de nomes: aparece logo no primeiro snapshot, e o nome real do Asterisk vence.
func TestNameCacheHintsAndLearning(t *testing.T) {
	var learned [][2]string
	h := newHarness(t)
	h.s.onName = func(n, name string) { learned = append(learned, [2]string{n, name}) }
	h.s.cur.onName = h.s.onName
	h.feed(model.NamesLoaded{At: h.now, Names: map[string]string{"2001": "Maria (cache)", "2002": "Joao (cache)", "2003": ""}})

	at := h.step()
	h.feed(model.SyncStarted{At: at, Technology: model.TechPJSIP, Reason: model.SyncConnect},
		model.EndpointSeen{At: at, Name: "2001"}, model.EndpointSeen{At: at, Name: "2002", CallerIDName: "Joao Lima"}, model.EndpointSeen{At: at, Name: "2003"},
		model.SyncFinished{At: at})
	if nameOf(h, "2001") != "Maria (cache)" || nameOf(h, "2002") != "Joao Lima" || h.ext("2003").Name != nil {
		t.Fatalf("2001=%q 2002=%q 2003=%v", nameOf(h, "2001"), nameOf(h, "2002"), h.ext("2003").Name)
	}
	if len(learned) != 1 || learned[0] != [2]string{"2002", "Joao Lima"} {
		t.Errorf("so nome real vai para o cache, nao o que veio do cache: %v", learned)
	}
	// Oportunista: quem disca com o proprio numero entrega o callerid do ramal.
	h.s.Flush()
	at = h.step()
	h.feed(
		chn(at, "u1", "u1", "PJSIP/2003-0001", "2003", "2003", "Ana Paula", "2001"),
		chn(at, "u2", "u1", "PJSIP/2001-0002", "2001", "2001", "", "2001"),
		model.DialStarted{At: at, SrcUniqueid: "u1", DstUniqueid: "u2"},
	)
	if nameOf(h, "2003") != "Ana Paula" {
		t.Errorf("nome oportunista: %q", nameOf(h, "2003"))
	}
	if len(learned) != 2 || learned[1] != [2]string{"2003", "Ana Paula"} {
		t.Errorf("aprendidos: %v", learned)
	}
	// Quem recebe a chamada (callerid de outro) e quem ja tem nome nao mudam.
	if nameOf(h, "2001") != "Maria (cache)" {
		t.Errorf("destino da chamada nao deve ganhar nome do chamador: %q", nameOf(h, "2001"))
	}
	if b, ok := h.s.Flush().(model.Batch); !ok || len(b.Extensions) == 0 {
		t.Error("nome aprendido precisa sair em delta")
	}
}

// Regressao do vazamento de heap medido no carga: a view publicada herdava
// isBuilding=true da montagem e guardava para sempre o Uniqueid de cada Hangup.
func TestHangupAfterSyncDoesNotAccumulate(t *testing.T) {
	h := newHarness(t)
	h.online()
	for i := 0; i < 1000; i++ {
		uid := "u" + strconv.Itoa(i)
		at := h.step()
		h.feed(chn(at, uid, uid, "PJSIP/2001-"+uid, "2001", "2001", "", "2002"), model.ChannelHungup{At: at, Uniqueid: uid})
	}
	if n := len(h.s.cur.hungup); n != 0 {
		t.Errorf("view publicada lembrou %d Hangups; devia ser 0", n)
	}
	if len(h.s.cur.chans) != 0 {
		t.Errorf("canais residuais: %d", len(h.s.cur.chans))
	}
}

// Cada evento de chamada e tratado no modo da view que o recebe: montagem lembra
// Hangups para nao ressuscitar canal do snapshot; depois do sync, modo normal.
func TestCallEventsFollowViewMode(t *testing.T) {
	h := newHarness(t)
	h.online()
	if h.s.cur.isBuilding {
		t.Fatal("view publicada ainda em modo de montagem")
	}
	// Ressincronizacao: Hangup de canal desconhecido e lembrado, e o snapshot atrasado nao o ressuscita.
	at := h.step()
	h.feed(model.SyncStarted{At: at, Technology: model.TechPJSIP, Reason: model.SyncOverflow},
		model.EndpointSeen{At: at, Name: "2001"}, model.EndpointSeen{At: at, Name: "2002"},
		model.ChannelHungup{At: at, Uniqueid: "g1"},
		chn(at, "g1", "g1", "PJSIP/2001-g1", "2001", "2001", "", "2002"))
	if len(h.s.building.chans) != 0 || len(h.s.building.hungup) != 1 {
		t.Fatalf("montagem: chans=%d hungup=%d", len(h.s.building.chans), len(h.s.building.hungup))
	}
	h.feed(model.SyncFinished{At: at})
	if h.s.cur.isBuilding || len(h.s.cur.hungup) != 0 {
		t.Fatalf("publicada: isBuilding=%v hungup=%d", h.s.cur.isBuilding, len(h.s.cur.hungup))
	}
	// Normal: Hangup de desconhecido nao grava nada; canal criado e encerrado some sem rastro.
	at = h.step()
	h.feed(model.ChannelHungup{At: at, Uniqueid: "x1"},
		chn(at, "n1", "n1", "PJSIP/2001-n1", "2001", "2001", "", "2002"))
	if h.s.cur.chans["n1"] == nil || len(h.s.cur.hungup) != 0 {
		t.Fatalf("normal: n1=%v hungup=%d", h.s.cur.chans["n1"], len(h.s.cur.hungup))
	}
}

type noPeerLog struct{ uids [][2]string }

func (l *noPeerLog) hook(h *harness) {
	f := func(uid, linked string) { l.uids = append(l.uids, [2]string{uid, linked}) }
	h.s.onNoPeer, h.s.cur.onNoPeer = f, f
}

// Regra: nunca "em conversa" sem outro lado resolvido. Canal Up que nunca teve outro lado
// aparece "tocando"; so depois de 1 s (e de um Flush) vira "em conversa" com outro lado
// desconhecido, e isso e logado (Uniqueid e Linkedid). Nao ha evento que reavalie: quem o faz e o Flush.
func TestUpWithoutPeerRingsThenProtectionAfterOneSecond(t *testing.T) {
	h := newHarness(t)
	h.online()
	var lg noPeerLog
	lg.hook(h)
	at := h.step()
	c := chn(at, "v1", "L1", "PJSIP/2001-v1", "2001", "2001", "Maria", "97")
	c.Up = true
	h.feed(c)
	if got := h.call("2001"); got.State != model.CallRinging || got.AnsweredAt != nil {
		t.Fatalf("sem outro lado deve tocar: %+v", got)
	}
	h.now = h.now.Add(500 * time.Millisecond)
	h.s.Flush()
	if got := h.call("2001"); got.State != model.CallRinging || len(lg.uids) != 0 {
		t.Fatalf("antes de 1 s: %+v log=%v", got, lg.uids)
	}
	h.now = h.now.Add(600 * time.Millisecond)
	h.s.Flush()
	got := h.call("2001")
	if got.State != model.CallTalking || got.Peer.Kind != model.PeerUnknown {
		t.Fatalf("apos 1 s: %+v", got)
	}
	h.now = h.now.Add(time.Second)
	h.s.Flush()
	if len(lg.uids) != 1 || lg.uids[0] != [2]string{"v1", "L1"} {
		t.Fatalf("log da protecao: %v", lg.uids)
	}
}

// Restauracao por snapshot nao tem evento a esperar: Up sem outro lado e publicado na hora.
func TestSnapshotUpWithoutPeerIsPublishedAtOnceAndLogged(t *testing.T) {
	h := newHarness(t)
	h.online()
	var lg noPeerLog
	lg.hook(h)
	syncWith(h, snapChan(h.now, "s1", "s1", "PJSIP/2001-0001", "2001", "2001", "97", true))
	lg.hook(h) // a view publicada e a de montagem, criada no SyncStarted
	if got := h.call("2001"); got.State != model.CallTalking || got.Peer.Kind != model.PeerUnknown {
		t.Errorf("snapshot: %+v", got)
	}
	if len(lg.uids) != 1 || lg.uids[0] != [2]string{"s1", "s1"} {
		t.Errorf("log da protecao: %v", lg.uids)
	}
}

// O outro lado sai antes do nosso (Hangup do tronco, Local channel otimizado): a chamada nao pode
// virar "internal/desconhecido"; mantem direcao e com quem fala do ultimo estado resolvido.
func TestFarEndLeavingKeepsLastResolvedPeer(t *testing.T) {
	h := newHarness(t)
	h.online()
	at := h.step()
	tr := chn(at, "t1", "t1", "PJSIP/operadora-0001", "operadora", "11999990000", "Cliente", "2001")
	e := chn(at, "e1", "t1", "PJSIP/2001-0002", "2001", "2001", "Maria", "")
	h.feed(tr, e, up(at, "t1"), model.DialStarted{At: at, SrcUniqueid: "t1", DstUniqueid: "e1"}, up(at, "e1"),
		model.BridgeEntered{At: at, BridgeID: "b1", Uniqueid: "t1"}, model.BridgeEntered{At: at, BridgeID: "b1", Uniqueid: "e1"})
	if c := h.call("2001"); c.Direction != model.DirInbound || c.State != model.CallTalking {
		t.Fatalf("antes: %+v", c)
	}
	at = h.step()
	h.feed(model.BridgeLeft{At: at, BridgeID: "b1", Uniqueid: "t1"}, model.ChannelHungup{At: at, Uniqueid: "t1"})
	c := h.call("2001")
	if c.Direction != model.DirInbound || c.State != model.CallTalking {
		t.Errorf("outro lado saiu: %+v", c)
	}
	wantPeer(t, c, model.PeerExternal, "11999990000", "Cliente")
}

// O ultimo peer conhecido so vale por noPeerGrace: depois disso (reavaliado no Flush) a chamada
// segue "em conversa", com a direcao ja resolvida, mas com outro lado desconhecido, e e logada.
func TestKnownPeerFallbackExpiresAfterGrace(t *testing.T) {
	h := newHarness(t)
	h.online()
	var lg noPeerLog
	lg.hook(h)
	at := h.step()
	h.feed(chn(at, "t1", "t1", "PJSIP/operadora-0001", "operadora", "11999990000", "Cliente", "2001"),
		chn(at, "e1", "t1", "PJSIP/2001-0002", "2001", "2001", "Maria", ""),
		up(at, "t1"), model.DialStarted{At: at, SrcUniqueid: "t1", DstUniqueid: "e1"}, up(at, "e1"),
		model.BridgeEntered{At: at, BridgeID: "b1", Uniqueid: "t1"}, model.BridgeEntered{At: at, BridgeID: "b1", Uniqueid: "e1"})
	at = h.step()
	h.feed(model.BridgeLeft{At: at, BridgeID: "b1", Uniqueid: "t1"}, model.ChannelHungup{At: at, Uniqueid: "t1"})
	h.now = h.now.Add(500 * time.Millisecond)
	h.s.Flush()
	wantPeer(t, h.call("2001"), model.PeerExternal, "11999990000", "Cliente")
	h.now = h.now.Add(600 * time.Millisecond)
	h.s.Flush()
	c := h.call("2001")
	if c.Direction != model.DirInbound || c.State != model.CallTalking || c.Peer.Kind != model.PeerUnknown {
		t.Fatalf("apos o prazo: %+v", c)
	}
	h.now = h.now.Add(10 * time.Minute)
	h.s.Flush()
	if len(lg.uids) != 1 || lg.uids[0] != [2]string{"e1", "t1"} {
		t.Fatalf("log: %v", lg.uids)
	}
}

// Em espera nao ha o que proteger: ao tirar da espera, a protecao de 1 s recomeca (nao herda o prazo velho).
func TestUnholdRestartsNoPeerGrace(t *testing.T) {
	h := newHarness(t)
	h.online()
	at := h.step()
	c := chn(at, "v1", "L1", "PJSIP/2001-v1", "2001", "2001", "Maria", "97")
	c.Up = true
	h.feed(c)
	h.feed(model.HoldChanged{At: at.Add(100 * time.Millisecond), Uniqueid: "v1", OnHold: true})
	if got := h.call("2001"); got.State != model.CallHold {
		t.Fatalf("em espera: %+v", got)
	}
	h.feed(model.HoldChanged{At: at.Add(5 * time.Second), Uniqueid: "v1", OnHold: false})
	if got := h.call("2001"); got.State != model.CallRinging {
		t.Fatalf("depois do Unhold deve passar pela protecao de 1 s: %+v", got)
	}
}
