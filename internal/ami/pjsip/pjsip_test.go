package pjsip

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/ami/amitest"
	"painel-ramais/internal/model"
)

const td = "../testdata/"

func snapshot(t *testing.T, replies map[string]string) (events []string, actions []string) {
	t.Helper()
	addr, seen := amitest.Serve(t, replies)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := ami.Connect(ctx, ami.Config{Address: addr, Username: "u", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := New(func(ev model.Event) { events = append(events, amitest.Render(ev)) })
	if err := a.Snapshot(ctx, c); err != nil {
		t.Fatal(err)
	}
	return events, seen()
}

func diff(t *testing.T, got, want []string) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	for i := 0; i < len(got) || i < len(want); i++ {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Errorf("evento %d:\n got %s\nwant %s", i, g, w)
		}
	}
}

func TestSnapshotComContatos(t *testing.T) {
	got, actions := snapshot(t, map[string]string{
		"PJSIPShowEndpoints": td + "pjsip/endpoints.txt",
		"PJSIPShowContacts":  td + "pjsip/contacts.txt",
		"DeviceStateList":    td + "pjsip/devicestate.txt",
		"CoreShowChannels":   td + "pjsip/channels.txt",
	})
	want := []string{
		"EndpointSeen{Name=2001 CallerIDName=Maria Souza Technology=pjsip}",
		"RegistrationChanged{Name=2001 State=registered IP=10.0.5.23 UserAgent=Yealink SIP-T46U 108.86.0.20 LatencyMs=12}",
		"EndpointSeen{Name=2002 CallerIDName=João Lima Technology=pjsip}",
		"RegistrationChanged{Name=2002 State=unreachable IP=10.0.5.24 UserAgent=Grandstream GXP1625}",
		"EndpointSeen{Name=2003 Technology=pjsip}",
		"RegistrationChanged{Name=2003 State=unregistered}",
		"EndpointSeen{Name=2004 CallerIDName=Dois Contatos Technology=pjsip}",
		"RegistrationChanged{Name=2004 State=registered IP=10.0.5.31 UserAgent=Softphone B}",
		"EndpointSeen{Name=trunk-acme Technology=pjsip}",
		"RegistrationChanged{Name=trunk-acme State=unregistered}",
		"DeviceStateChanged{Name=2001 State=inuse}",
		"DeviceStateChanged{Name=2002 State=unavailable}",
		"DeviceStateChanged{Name=2004 State=not_inuse}",
		"ChannelCreated{Uniqueid=1791902000.10 Linkedid=1791902000.10 Channel=PJSIP/2001-00000010 Endpoint=2001 CallerNum=2001 CallerName=Maria Souza Exten=011987654321 Up=true BridgeID=7c1e9a2e-0001 Duration=2m5s}",
		"ConnectedLineChanged{Uniqueid=1791902000.10 Num=11987654321 Name=ACME LTDA}",
		"ChannelCreated{Uniqueid=1791902000.11 Linkedid=1791902000.10 Channel=PJSIP/trunk-acme-00000011 Endpoint=trunk-acme CallerNum=2001 CallerName=Maria Souza Exten=s Up=true BridgeID=7c1e9a2e-0001 Duration=2m5s}",
		"ConnectedLineChanged{Uniqueid=1791902000.11 Num=11987654321}",
		"ChannelCreated{Uniqueid=1791902100.12 Linkedid=1791902100.12 Channel=PJSIP/2002-00000012 Endpoint=2002 CallerNum=2002 CallerName=João Lima Exten=2004 Duration=3s}",
	}
	diff(t, got, want)
	if wantActions := []string{"Login", "PJSIPShowEndpoints", "PJSIPShowContacts", "DeviceStateList", "CoreShowChannels"}; !slices.Equal(actions, wantActions) {
		t.Errorf("actions %v", actions)
	}
}

// Versão do Asterisk sem PJSIPShowContacts: cai para PJSIPShowAors.
func TestSnapshotFallbackAors(t *testing.T) {
	got, actions := snapshot(t, map[string]string{
		"PJSIPShowEndpoints": td + "pjsip/endpoints.txt",
		"PJSIPShowAors":      td + "pjsip/aors.txt",
		"DeviceStateList":    td + "pjsip/devicestate.txt",
		"CoreShowChannels":   td + "pjsip/channels.txt",
	})
	if !slices.Contains(actions, "PJSIPShowAors") {
		t.Fatalf("não usou o fallback: %v", actions)
	}
	if got[1] != "RegistrationChanged{Name=2001 State=registered IP=10.0.5.23}" {
		t.Errorf("2001 pelo AOR: %s", got[1])
	}
	if got[5] != "RegistrationChanged{Name=2003 State=unregistered}" {
		t.Errorf("2003 pelo AOR: %s", got[5])
	}
}

func liveEvents(t *testing.T, a *Adapter, file string) []string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	msgs, err := ami.ParseAll(f)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range msgs {
		for _, ev := range a.Translate(m) {
			out = append(out, amitest.Render(ev))
		}
	}
	return out
}

// Registro, qualify, desregistro e devicestate ao vivo.
func TestEventosDeRegistroAoVivo(t *testing.T) {
	a := New(func(model.Event) {})
	got := liveEvents(t, a, td+"pjsip/live_registration.txt")
	want := []string{
		"RegistrationChanged{Name=2005 State=registered IP=10.0.5.50 UserAgent=Fanvil X3}",
		"RegistrationChanged{Name=2005 State=registered IP=10.0.5.50 UserAgent=Fanvil X3 LatencyMs=8}",
		"RegistrationChanged{Name=2005 State=unreachable IP=10.0.5.50 UserAgent=Fanvil X3}",
		"RegistrationChanged{Name=2005 State=unregistered}",
		"DeviceStateChanged{Name=2005 State=ringing}",
	}
	diff(t, got, want)
}

// Dois contatos: sair um não desregistra o endpoint; sair os dois, sim.
func TestVariosContatosAgregam(t *testing.T) {
	a := New(func(model.Event) {})
	status := func(uri, st string) model.RegistrationChanged {
		evs := a.Translate(ami.NewMessage("Event", "ContactStatus", "URI", uri, "ContactStatus", st,
			"EndpointName", "2010", "ViaAddress", "10.0.0.1:5060"))
		return evs[0].(model.RegistrationChanged)
	}
	steps := []struct {
		uri, st string
		want    model.Registration
	}{
		{"sip:2010@a", "Reachable", model.RegRegistered},
		{"sip:2010@b", "Unreachable", model.RegRegistered}, // um alcançável basta
		{"sip:2010@a", "Removed", model.RegUnreachable},    // sobrou só o inalcançável
		{"sip:2010@b", "Removed", model.RegUnregistered},
	}
	for i, s := range steps {
		if got := status(s.uri, s.st).State; got != s.want {
			t.Errorf("passo %d (%s %s): %s, esperava %s", i, s.uri, s.st, got, s.want)
		}
	}
}

func TestDeviceStateDeTroncoSipNaoEhPjsip(t *testing.T) {
	a := New(func(model.Event) {})
	if evs := a.Translate(ami.NewMessage("Event", "DeviceStateChange", "Device", "SIP/2001", "State", "INUSE")); evs != nil {
		t.Fatalf("device SIP/ não pertence ao adaptador pjsip: %v", evs)
	}
	if got, ok := a.EndpointFromChannel("PJSIP/2001-0000002a"); !ok || got != "2001" {
		t.Fatalf("got %q %v", got, ok)
	}
}

// Preenchimento de nomes (decisao 4): PJSIPShowEndpoint por endpoint, com taxa limitada.
func TestEnrichFillsNamesAtLimitedRate(t *testing.T) {
	addr, seen := amitest.Serve(t, map[string]string{
		"PJSIPShowEndpoints": td + "pjsip/endpoints.txt",
		"PJSIPShowContacts":  td + "pjsip/contacts.txt",
		"DeviceStateList":    td + "pjsip/devicestate.txt",
		"CoreShowChannels":   td + "pjsip/channels.txt",
		"PJSIPShowEndpoint":  td + "pjsip/endpoint_detail.txt",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := ami.Connect(ctx, ami.Config{Address: addr, Username: "u", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var names []model.EndpointSeen
	a := New(func(ev model.Event) {})
	if err := a.Snapshot(ctx, c); err != nil {
		t.Fatal(err)
	}
	a.emit = func(ev model.Event) {
		if e, ok := ev.(model.EndpointSeen); ok {
			names = append(names, e)
		}
	}
	start := time.Now()
	only2xxx := func(n string) bool { return n != "trunk-acme" }
	if err := a.Enrich(ctx, c, 20, only2xxx); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	n := 0
	for _, act := range seen() {
		if act == "PJSIPShowEndpoint" {
			n++
		}
	}
	if n == 0 || len(names) != n || names[0].CallerIDName != "Maria Souza" || names[0].Technology != model.TechPJSIP {
		t.Fatalf("consultas=%d nomes=%+v", n, names)
	}
	if min := time.Duration(n-1) * (time.Second / 20); took < min*9/10 {
		t.Errorf("%d consultas em %s: acima de 20/s (minimo %s)", n, took, min)
	}
	// Cancelar para o preenchimento sem erro de rede.
	cctx, cc := context.WithCancel(ctx)
	cc()
	if err := a.Enrich(cctx, c, 20, only2xxx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelamento: %v", err)
	}
}
