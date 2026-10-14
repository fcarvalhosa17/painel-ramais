package calls

import (
	"os"
	"reflect"
	"testing"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/ami/amitest"
	"painel-ramais/internal/model"
)

func pjsipEP(ch string) (string, bool) { return EndpointFromChannel(ch, "PJSIP") }

func TestEndpointFromChannel(t *testing.T) {
	cases := []struct {
		ch, prefix, want string
		ok               bool
	}{
		{"PJSIP/2001-0000002a", "PJSIP", "2001", true},
		{"SIP/2001-00000001", "SIP", "2001", true},
		{"PJSIP/trunk-acme-00000012", "PJSIP", "trunk-acme", true},
		{"SIP/2001-00000001", "PJSIP", "", false},
		{"Local/2002@from-internal-0000001a;1", "PJSIP", "", false},
		{"PJSIP/semsufixo", "PJSIP", "", false},
		{"PJSIP/2001-zzzz", "PJSIP", "", false},
	}
	for _, c := range cases {
		got, ok := EndpointFromChannel(c.ch, c.prefix)
		if got != c.want || ok != c.ok {
			t.Errorf("%s/%s: got %q,%v", c.prefix, c.ch, got, ok)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"00:02:05": 2*time.Minute + 5*time.Second, "100:00:01": 100*time.Hour + time.Second,
		"": 0, "abc": 0, "1:2": 0, "00:-1:00": 0,
	}
	for in, want := range cases {
		if got := parseDuration(in); got != want {
			t.Errorf("%q: %v, esperava %v", in, got, want)
		}
	}
}

func TestDeviceState(t *testing.T) {
	cases := map[string]model.DeviceState{
		"NOT_INUSE": model.DevNotInUse, "Not in use": model.DevNotInUse, "INUSE": model.DevInUse,
		"In use": model.DevInUse, "UNAVAILABLE": model.DevUnavailable, "RINGING": model.DevRinging,
		"RINGINUSE": model.DevRingInUse, "ONHOLD": model.DevOnHold, "BUSY": model.DevBusy,
		"INVALID": model.DevInvalid, "UNKNOWN": model.DevUnknown, "algo novo": model.DevUnknown,
	}
	for in, want := range cases {
		if got := deviceState(in); got != want {
			t.Errorf("%q: %v, esperava %v", in, got, want)
		}
	}
}

// Ciclo de chamada completo (transcrição em testdata/calls/internal_call.txt).
func TestCicloDeChamada(t *testing.T) {
	f, err := os.Open("../testdata/calls/internal_call.txt")
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
		for _, ev := range Translate(m, pjsipEP) {
			got = append(got, amitest.Render(ev))
		}
	}
	want := []string{
		"ChannelCreated{Uniqueid=1791902000.10 Linkedid=1791902000.10 Channel=PJSIP/2001-00000010 Endpoint=2001 CallerNum=2001 CallerName=Maria Souza Exten=2002}",
		"ChannelCreated{Uniqueid=1791902000.11 Linkedid=1791902000.10 Channel=PJSIP/2002-00000011 Endpoint=2002 CallerNum=2002 CallerName=João Lima Exten=s}",
		"DialStarted{SrcUniqueid=1791902000.10 DstUniqueid=1791902000.11}",
		"ChannelStateChanged{Uniqueid=1791902000.11 State=ringing}",
		"ConnectedLineChanged{Uniqueid=1791902000.10 Num=2002 Name=João Lima}",
		"CallerIDChanged{Uniqueid=1791902000.11 Num=2001 Name=Maria Souza}",
		"ChannelStateChanged{Uniqueid=1791902000.11 State=up}",
		"DialEnded{SrcUniqueid=1791902000.10 DstUniqueid=1791902000.11 Status=ANSWER}",
		"BridgeEntered{BridgeID=7c1e9a2e-0001 Uniqueid=1791902000.10}",
		"BridgeEntered{BridgeID=7c1e9a2e-0001 Uniqueid=1791902000.11}",
		"HoldChanged{Uniqueid=1791902000.11 OnHold=true}",
		"HoldChanged{Uniqueid=1791902000.11}",
		"BridgeLeft{BridgeID=7c1e9a2e-0001 Uniqueid=1791902000.10}",
		"ChannelHungup{Uniqueid=1791902000.10 Cause=16}",
		// canal Local: sem Endpoint, IsLocal
		"ChannelCreated{Uniqueid=1791902000.20 Linkedid=1791902000.10 Channel=Local/2002@from-internal-0000001a;1 IsLocal=true CallerNum=2001 CallerName=Maria Souza Exten=2002}",
		// tronco: Endpoint é o nome do tronco (o state decide pelo extension_pattern)
		"ChannelCreated{Uniqueid=1791902000.21 Linkedid=1791902000.21 Channel=PJSIP/trunk-acme-00000012 Endpoint=trunk-acme CallerNum=11987654321 CallerName=ACME LTDA Exten=s}",
	}
	if !reflect.DeepEqual(got, want) {
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
}

func TestEventoDesconhecidoViraNil(t *testing.T) {
	if Translate(ami.NewMessage("Event", "VarSet", "Variable", "X"), pjsipEP) != nil {
		t.Fatal("VarSet não é evento de chamada")
	}
}

func TestFromCoreShowChannelTrazBridgeEDuracao(t *testing.T) {
	m := ami.NewMessage("Event", "CoreShowChannel", "Channel", "PJSIP/2001-00000010", "ChannelStateDesc", "Up",
		"CallerIDNum", "2001", "ConnectedLineNum", "119", "ConnectedLineName", "ACME",
		"Uniqueid", "1.1", "Linkedid", "1.1", "Duration", "00:02:05", "BridgeId", "b-1")
	evs := FromCoreShowChannel(m, pjsipEP)
	if len(evs) != 2 {
		t.Fatalf("esperava canal + linha conectada, veio %d", len(evs))
	}
	cc := evs[0].(model.ChannelCreated)
	if !cc.Up || cc.BridgeID != "b-1" || cc.Duration != 125*time.Second || cc.Endpoint != "2001" {
		t.Fatalf("%+v", cc)
	}
}

// CoreShowChannels marca o destino de um Dial (AppDial) e guarda os argumentos no canal que disca.
func TestCoreShowChannelDialMarkers(t *testing.T) {
	dest := ami.NewMessage("Event", "CoreShowChannel", "Channel", "PJSIP/operadora-0002", "Uniqueid", "t1", "Linkedid", "e1",
		"ChannelStateDesc", "Up", "Application", "AppDial", "ApplicationData", "(Outgoing Line)", "Duration", "00:00:20")
	src := ami.NewMessage("Event", "CoreShowChannel", "Channel", "PJSIP/2001-0001", "Uniqueid", "e1", "Linkedid", "e1",
		"ChannelStateDesc", "Up", "Application", "Dial", "ApplicationData", "PJSIP/operadora/0113,30,tr", "Duration", "00:00:20")
	other := ami.NewMessage("Event", "CoreShowChannel", "Channel", "PJSIP/2002-0003", "Uniqueid", "x", "Linkedid", "x",
		"Application", "Playback", "ApplicationData", "hello-world", "Duration", "00:00:01")
	d := FromCoreShowChannel(dest, pjsipEP)[0].(model.ChannelCreated)
	s := FromCoreShowChannel(src, pjsipEP)[0].(model.ChannelCreated)
	o := FromCoreShowChannel(other, pjsipEP)[0].(model.ChannelCreated)
	if !d.Dialed || d.DialData != "" {
		t.Errorf("destino: %+v", d)
	}
	if s.Dialed || s.DialData != "PJSIP/operadora/0113,30,tr" {
		t.Errorf("origem: %+v", s)
	}
	if o.Dialed || o.DialData != "" {
		t.Errorf("outro: %+v", o)
	}
}
