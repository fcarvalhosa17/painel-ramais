package capture

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"
)

func frame(lines ...string) []byte { return []byte(strings.Join(lines, "\r\n") + "\r\n\r\n") }

func TestSecretsAreAlwaysMasked(t *testing.T) {
	s := NewSanitizer(SanitizeOptions{})
	in := frame("Action: Login", "ActionID: 1", "Username: painel-ramais", "Secret: s3gredo-real", "Md5Cred: abcdef", "Password: p4ss", "Uniqueid: 1791901712.4521")
	got := string(s.Frame(in))
	for _, leaked := range []string{"s3gredo-real", "abcdef", "p4ss"} {
		if strings.Contains(got, leaked) {
			t.Errorf("vazou %q:\n%s", leaked, got)
		}
	}
	for _, kept := range []string{"Username: painel-ramais", "Uniqueid: 1791901712.4521", "Secret: ***", "Password: ***", "Md5Cred: ***"} {
		if !strings.Contains(got, kept) {
			t.Errorf("faltou %q:\n%s", kept, got)
		}
	}
	if !strings.HasSuffix(got, "\r\n\r\n") {
		t.Error("o quadro deve manter o terminador")
	}
}

func TestAnonymizeIsConsistentAndKeepsStructure(t *testing.T) {
	s := NewSanitizer(SanitizeOptions{Anonymize: true})
	a := string(s.Frame(frame(
		"Event: Newchannel", "Channel: PJSIP/operadora-00000012", "CallerIDNum: 11987654321", `CallerIDName: ACME LTDA`,
		"ConnectedLineNum: 2001", "ConnectedLineName: Maria Souza", "Exten: 01133334444", "Uniqueid: 1791901712.4521", "Linkedid: 1791901712.4521",
		`Callerid: "Maria Souza" <2001>`, "Contacts: 2001/sip:2001@192.0.2.23:5060", "Address: 127.0.0.1", "Duration: 00:01:12")))
	b := string(s.Frame(frame("Event: Dial", "CallerIDNum: 11987654321", "DestCallerIDName: ACME LTDA", "DialString: PJSIP/01133334444@operadora", "Peer: 192.0.2.23")))

	for _, leaked := range []string{"11987654321", "ACME LTDA", "Maria Souza", "01133334444", "192.0.2.23"} {
		if strings.Contains(a+b, leaked) {
			t.Errorf("vazou %q:\n%s\n%s", leaked, a, b)
		}
	}
	for _, kept := range []string{"PJSIP/operadora-00000012", "ConnectedLineNum: 2001", "1791901712.4521", "Address: 127.0.0.1", "Duration: 00:01:12", "<2001>", "sip:2001@"} {
		if !strings.Contains(a, kept) {
			t.Errorf("deveria manter %q:\n%s", kept, a)
		}
	}
	// Consistencia: o mesmo original vira sempre o mesmo ficticio, entre campos e entre quadros.
	fake := func(s, key string) string {
		for _, ln := range strings.Split(s, "\r\n") {
			if strings.HasPrefix(ln, key+": ") {
				return strings.TrimPrefix(ln, key+": ")
			}
		}
		return ""
	}
	if fake(a, "CallerIDNum") != fake(b, "CallerIDNum") || len(fake(a, "CallerIDNum")) != 11 || fake(a, "CallerIDNum") == "11987654321" {
		t.Errorf("numero: %q vs %q", fake(a, "CallerIDNum"), fake(b, "CallerIDNum"))
	}
	if fake(a, "CallerIDName") == "" || fake(a, "CallerIDName") != fake(b, "DestCallerIDName") {
		t.Errorf("nome: %q vs %q", fake(a, "CallerIDName"), fake(b, "DestCallerIDName"))
	}
	if !strings.HasPrefix(fake(a, "Exten"), "0") || len(fake(a, "Exten")) != 11 {
		t.Errorf("o fictício mantém tamanho e o primeiro digito: %q", fake(a, "Exten"))
	}
	if !strings.Contains(fake(b, "DialString"), fake(a, "Exten")) {
		t.Errorf("mesmo numero no Exten e no DialString: %q / %q", fake(a, "Exten"), fake(b, "DialString"))
	}
	if !strings.Contains(a, `Callerid: "Pessoa `) || strings.Count(a, "10.200.0.1") != 1 {
		t.Errorf("callerid/ip:\n%s", a)
	}
	// Dois originais diferentes nunca viram o mesmo ficticio.
	x := NewSanitizer(SanitizeOptions{Anonymize: true})
	n1 := x.fakeNumber("11987654321")
	n2 := x.fakeNumber("11987654322")
	if n1 == n2 || x.fakeNumber("11987654321") != n1 {
		t.Errorf("%s %s", n1, n2)
	}
}

func TestSplitterAndRoundTripOverPipe(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, NewSanitizer(SanitizeOptions{}), map[string]any{"versao": "x"})
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := net.Pipe()
	tapped := Tap(c1, w, func(e error) { t.Error(e) })
	go func() { // "Asterisk": banner, depois uma resposta fatiada no meio de uma linha
		c2.Write([]byte("Asterisk Call Manager/5.0.1\r\nResponse: Succ"))
		time.Sleep(10 * time.Millisecond)
		c2.Write([]byte("ess\r\nActionID: 1\r\n\r\nEvent: Hangup\r\nUniqueid: 1\r\n\r\n"))
	}()
	go func() {
		tapped.Write([]byte("Action: Login\r\nSecret: segredo\r\n\r\n"))
	}()
	got := make([]byte, 0, 200)
	tmp := make([]byte, 200)
	deadline := time.Now().Add(2 * time.Second)
	for w.Frames() < 4 && time.Now().Before(deadline) {
		tapped.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		n, _ := tapped.Read(tmp)
		got = append(got, tmp[:n]...)
		c2.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		c2.Read(tmp) // esvazia o lado "Asterisk" (a escrita do daemon)
	}
	w.Close(nil)
	meta, frames, end, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if meta["versao"] != "x" || end["quadros"].(float64) != 4 || len(frames) != 4 {
		t.Fatalf("meta=%v end=%v frames=%d", meta, end, len(frames))
	}
	var recv, sent int
	for _, f := range frames {
		switch f.Dir {
		case DirRecv:
			recv++
		case DirSent:
			sent++
			if bytes.Contains(f.Data, []byte("segredo")) || !bytes.Contains(f.Data, []byte("Secret: ***")) {
				t.Errorf("Login sem mascara: %q", f.Data)
			}
		}
		if f.At.IsZero() {
			t.Error("sem data")
		}
	}
	if recv != 3 || sent != 1 || !bytes.HasPrefix(frames[0].Data, []byte("Asterisk Call Manager")) && !bytes.HasPrefix(frames[1].Data, []byte("Asterisk Call Manager")) {
		t.Errorf("recv=%d sent=%d", recv, sent)
	}
	_ = got
}

func TestReadRejectsGarbage(t *testing.T) {
	if _, _, _, err := Read(strings.NewReader("nada\n")); err == nil {
		t.Error("sem cabecalho")
	}
	if _, _, _, err := Read(strings.NewReader(Magic + "\n< 2026-10-13T14:00:00Z 50\ncurto\n")); err == nil {
		t.Error("quadro truncado")
	}
	if _, _, _, err := Read(strings.NewReader(Magic + "\n? 2026-10-13T14:00:00Z 1\nx\n")); err == nil {
		t.Error("direcao invalida")
	}
}
