package sip

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/ami/amitest"
	"painel-ramais/internal/model"
)

const td = "../testdata/"

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

func connect(t *testing.T, replies map[string]string) (*ami.Client, context.Context) {
	t.Helper()
	addr, _ := amitest.Serve(t, replies)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, err := ami.Connect(ctx, ami.Config{Address: addr, Username: "u", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, ctx
}

func TestSnapshotSIPpeers(t *testing.T) {
	c, ctx := connect(t, map[string]string{
		"SIPpeers":         td + "sip/peers.txt",
		"DeviceStateList":  td + "sip/devicestate.txt",
		"CoreShowChannels": td + "sip/channels.txt",
	})
	var got []string
	a := New(func(ev model.Event) { got = append(got, amitest.Render(ev)) })
	if err := a.Snapshot(ctx, c); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"EndpointSeen{Name=2001 Technology=sip}",
		"RegistrationChanged{Name=2001 State=registered IP=192.0.2.23 LatencyMs=12}",
		"EndpointSeen{Name=2002 Technology=sip}",
		"RegistrationChanged{Name=2002 State=unreachable IP=192.0.2.24}",
		"EndpointSeen{Name=2003 Technology=sip}",
		"RegistrationChanged{Name=2003 State=unregistered}",
		"EndpointSeen{Name=2004 Technology=sip}",
		"RegistrationChanged{Name=2004 State=registered IP=192.0.2.30 LatencyMs=850}",
		"EndpointSeen{Name=trunk-acme Technology=sip}",
		"RegistrationChanged{Name=trunk-acme State=registered IP=203.0.113.3 LatencyMs=30}",
		"DeviceStateChanged{Name=2001 State=inuse}",
		"DeviceStateChanged{Name=2002 State=unavailable}",
		"DeviceStateChanged{Name=2004 State=not_inuse}",
		"ChannelCreated{Uniqueid=1791902000.10 Linkedid=1791902000.10 Channel=SIP/2001-00000010 Endpoint=2001 CallerNum=2001 CallerName=Maria Souza Exten=011987654321 Up=true BridgeID=7c1e9a2e-0001 Duration=2m5s}",
		"ConnectedLineChanged{Uniqueid=1791902000.10 Num=11987654321 Name=ACME LTDA}",
		"ChannelCreated{Uniqueid=1791902000.11 Linkedid=1791902000.10 Channel=SIP/trunk-acme-00000011 Endpoint=trunk-acme CallerNum=2001 CallerName=Maria Souza Exten=s Up=true BridgeID=7c1e9a2e-0001 Duration=2m5s}",
		"ConnectedLineChanged{Uniqueid=1791902000.11 Num=11987654321}",
		"ChannelCreated{Uniqueid=1791902100.12 Linkedid=1791902100.12 Channel=SIP/2002-00000012 Endpoint=2002 CallerNum=2002 CallerName=João Lima Exten=2004 Duration=3s}",
	}
	diff(t, got, want)
}

func TestEventosDeRegistroAoVivo(t *testing.T) {
	a := New(func(model.Event) {})
	// o peer 2005 já existia com user agent (veio do Enrich)
	a.last["2005"] = model.RegistrationChanged{Name: "2005", UserAgent: "Fanvil X3"}
	f, err := os.Open(td + "sip/live_registration.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	msgs, err := ami.ParseAll(f)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range msgs {
		for _, ev := range a.Translate(m) {
			got = append(got, amitest.Render(ev))
		}
	}
	want := []string{
		"RegistrationChanged{Name=2005 State=registered IP=192.0.2.50 UserAgent=Fanvil X3}",
		"RegistrationChanged{Name=2005 State=registered IP=192.0.2.50 UserAgent=Fanvil X3 LatencyMs=9}",
		"RegistrationChanged{Name=2005 State=registered IP=192.0.2.50 UserAgent=Fanvil X3 LatencyMs=700}",
		"RegistrationChanged{Name=2005 State=unreachable IP=192.0.2.50 UserAgent=Fanvil X3}",
		"RegistrationChanged{Name=2005 State=unregistered}",
		"RegistrationChanged{Name=2006 State=unregistered}",
		"DeviceStateChanged{Name=2005 State=onhold}",
	}
	diff(t, got, want)
}

func TestPeerStatusUnknownEIgnorado(t *testing.T) {
	a := New(func(model.Event) {})
	if evs := a.Translate(ami.NewMessage("Event", "PeerStatus", "Peer", "SIP/2007", "PeerStatus", "Unknown")); evs != nil {
		t.Fatalf("Unknown não traz informação: %v", evs)
	}
	if got, ok := a.EndpointFromChannel("SIP/2001-00000001"); !ok || got != "2001" {
		t.Fatalf("got %q %v", got, ok)
	}
}

func TestEnrichCompletaNomeEUserAgent(t *testing.T) {
	c, ctx := connect(t, map[string]string{
		"SIPshowpeer": td + "sip/showpeer_2001.txt",
	})
	var got []string
	a := New(func(ev model.Event) { got = append(got, amitest.Render(ev)) })
	a.last["2001"] = model.RegistrationChanged{Name: "2001", State: model.RegRegistered, IP: "192.0.2.23"}
	a.last["trunk-acme"] = model.RegistrationChanged{Name: "trunk-acme"}
	isExt := func(n string) bool { return n != "trunk-acme" }
	if err := a.Enrich(ctx, c, 1000, isExt); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"EndpointSeen{Name=2001 CallerIDName=Maria Souza Technology=sip}",
		"RegistrationChanged{Name=2001 State=registered IP=192.0.2.23 UserAgent=Yealink SIP-T46U 108.86.0.20}",
	}
	diff(t, got, want)
	// e o PeerStatus seguinte preserva o user agent
	evs := a.Translate(ami.NewMessage("Event", "PeerStatus", "Peer", "SIP/2001", "PeerStatus", "Reachable", "Time", "5"))
	if r := evs[0].(model.RegistrationChanged); r.UserAgent == "" || r.IP != "192.0.2.23" {
		t.Fatalf("perdeu dados no PeerStatus: %+v", r)
	}
}

// Regra de produto: o IP exibido e o IPaddress que o Asterisk devolve; valor fora do padrao IPv4
// aparece como texto e NAO interrompe o processamento dos demais peers.
func TestIPForaDoPadraoIPv4EExibidoComoTexto(t *testing.T) {
	body := "Response: Success\r\nEventList: start\r\n\r\n"
	for _, p := range []struct{ name, ip string }{
		{"2001", "(Unspecified)"}, {"2002", "pbx.exemplo.invalid"}, {"2003", "2001:db8::1"}, {"2004", "192.0.2.31"},
	} {
		body += "Event: PeerEntry\r\nChanneltype: SIP\r\nObjectName: " + p.name + "\r\nIPaddress: " + p.ip + "\r\nStatus: OK (12 ms)\r\n\r\n"
	}
	body += "Event: PeerlistComplete\r\nEventList: Complete\r\nListItems: 4\r\n\r\n"
	f := t.TempDir() + "/peers.txt"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, ctx := connect(t, map[string]string{
		"SIPpeers": f, "DeviceStateList": td + "sip/devicestate.txt", "CoreShowChannels": td + "sip/channels.txt",
	})
	var got []string
	a := New(func(ev model.Event) { got = append(got, amitest.Render(ev)) })
	if err := a.Snapshot(ctx, c); err != nil {
		t.Fatal(err)
	}
	has := func(want string) bool {
		for _, g := range got {
			if g == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{
		"RegistrationChanged{Name=2001 State=registered IP=(Unspecified) LatencyMs=12}",
		"RegistrationChanged{Name=2002 State=registered IP=pbx.exemplo.invalid LatencyMs=12}",
		"RegistrationChanged{Name=2003 State=registered IP=2001:db8::1 LatencyMs=12}",
		"RegistrationChanged{Name=2004 State=registered IP=192.0.2.31 LatencyMs=12}",
		"DeviceStateChanged{Name=2001 State=inuse}", // o restante do snapshot seguiu
	} {
		if !has(want) {
			t.Errorf("faltou %q em:\n%v", want, got)
		}
	}
}
