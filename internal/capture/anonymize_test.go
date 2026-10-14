package capture

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// Captura sintetica com dados pessoais em campos de varios tipos: depois de anonimizar,
// nenhum valor original pode sobrar no arquivo, nem como substring.
func TestAnonymizedCaptureLeavesNoOriginalValue(t *testing.T) {
	frames := [][]byte{
		frame("Event: ContactStatus", "URI: sip:2001@[2001:db8::77]:5060", "ContactStatus: Reachable", "AOR: 2001",
			"RoundtripUsec: 12345678", "Via: SIP/2.0/UDP 203.0.113.45:5060;branch=z9hG4bK77", "EndpointName: 1133334444"),
		frame("Event: EndpointDetail", "ObjectName: 1133334444", "Username: 1133334444", "AccountCode: cliente-acme-sa",
			"Callerid: \"Maria Souza\" <11987654321>", "Mailbox: maria.souza@empresa.com.br", "Domain: pbx.empresa.com.br",
			"Contacts: 1133334444/sip:1133334444@198.51.100.7:5060", "Transport: transport-udp"),
		frame("Event: DialBegin", "Channel: PJSIP/operadora-0000002a", "CallerIDNum: 11987654321", "CallerIDName: ACME LTDA",
			"ConnectedLineName: Joao Pereira", "DestCallerIDName: Joao Pereira", "DialString: operadora/01133334444",
			"Peer: PJSIP/1133334444", "Uniqueid: 1791901712.4521", "Linkedid: 1791901712.4521"),
		frame("Event: VarSet", "Variable: CALLERID(name)", "Value: Carlos Eduardo Lima", "Uniqueid: 1791901712.4521"),
		frame("Event: VarSet", "Variable: CDR(dst)", "Value: 21999887766", "Uniqueid: 1791901712.4521"),
		frame("Response: Success", "Server: pbx-matriz.empresa.com.br", "Address: [fe80::1c2b:3d4e:5f60:7182]:5060",
			"Host: 2001:db8:85a3::8a2e:370:7334", "Message: ok"),
		[]byte("Asterisk Call Manager/5.0.1 host 198.51.100.7\r\n"),
	}
	originals := []string{
		"2001:db8::77", "203.0.113.45", "1133334444", "cliente-acme-sa", "Maria Souza", "11987654321", "maria.souza@empresa.com.br",
		"pbx.empresa.com.br", "198.51.100.7", "ACME LTDA", "Joao Pereira", "01133334444", "Carlos Eduardo Lima", "21999887766",
		"pbx-matriz.empresa.com.br", "fe80::1c2b:3d4e:5f60:7182", "2001:db8:85a3::8a2e:370:7334", "empresa.com.br", "db8",
	}

	var buf bytes.Buffer
	w, err := NewWriter(&buf, NewSanitizer(SanitizeOptions{Anonymize: true}), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if err := w.WriteFrame(DirRecv, time.Now(), f); err != nil {
			t.Fatal(err)
		}
	}
	w.Close(nil)
	out := buf.String()
	for _, o := range originals {
		if strings.Contains(out, o) {
			t.Errorf("sobrou o original %q:\n%s", o, out)
		}
	}

	// A estrutura que o replay precisa continua: chaves, ids, sufixo de canal, valores curtos, tronco.
	for _, kept := range []string{
		"Event: ContactStatus", "RoundtripUsec: 12345678", "Uniqueid: 1791901712.4521", "Linkedid: 1791901712.4521",
		"Channel: PJSIP/operadora-0000002a", "AOR: 2001", "ContactStatus: Reachable", "Transport: transport-udp", "Variable: CALLERID(name)",
	} {
		if !strings.Contains(out, kept) {
			t.Errorf("deveria manter %q:\n%s", kept, out)
		}
	}
	// Consistencia entre campos: o mesmo DID aparece igual em Exten/DialString/Peer.
	if strings.Count(out, fakeOf(out, "Username")) < 3 {
		t.Errorf("o mesmo numero deve virar sempre o mesmo ficticio:\n%s", out)
	}
}

func fakeOf(out, key string) string {
	for _, ln := range strings.Split(out, "\r\n") {
		if v, ok := strings.CutPrefix(ln, key+": "); ok {
			return v
		}
	}
	return "?"
}

// O usuario do Login e o da conta do painel: continua legivel (e util para o replay).
func TestLoginUsernameIsKeptButSecretIsMasked(t *testing.T) {
	s := NewSanitizer(SanitizeOptions{Anonymize: true})
	out := string(s.Frame(frame("Action: Login", "ActionID: p1", "Username: painel-ramais", "Secret: s3gredo")))
	if !strings.Contains(out, "Username: painel-ramais") || strings.Contains(out, "s3gredo") || !strings.Contains(out, "Secret: ***") {
		t.Errorf("%s", out)
	}
}

// Horas e versoes nao podem ser lidas como IPv6/IPv4 de pessoa.
func TestAnonymizeDoesNotMangleTimesAndDurations(t *testing.T) {
	s := NewSanitizer(SanitizeOptions{Anonymize: true})
	out := string(s.Frame(frame("Event: CoreShowChannel", "Duration: 00:12:45", "CoreStartupTime: 14:30:05", "Message: At 14:30:05 and 00:0b:82:ab:cd:ef")))
	for _, kept := range []string{"Duration: 00:12:45", "CoreStartupTime: 14:30:05", "At 14:30:05", "00:0b:82:ab:cd:ef"} {
		if !strings.Contains(out, kept) {
			t.Errorf("alterou %q:\n%s", kept, out)
		}
	}
}
