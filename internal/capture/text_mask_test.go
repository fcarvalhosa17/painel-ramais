package capture

import (
	"strings"
	"testing"
)

func textOf(anon bool, in string) string {
	return string(NewSanitizer(SanitizeOptions{Anonymize: anon}).Text([]byte(in)))
}

// Nada do valor secreto pode sobrar como sufixo: espaco, ponto e virgula, virgula, "&", aspas simples.
func TestTextMascaraSufixo(t *testing.T) {
	casos := map[string]string{
		"password=abc def ghi x=painel":          "password=***",
		"secret=abc;def,ghi&jkl":                 "secret=***",
		"pwd: 'com espaco; e ponto'":             "pwd: '***'",
		`{"secret":"ab\"c,d","ok":true}`:         `{"secret":"***","ok":true}`,
		"token=abc}]\r":                          "token=***\r",
		"login falhou password=ab cd, mais x":    "login falhou password=***",
		"a=1 senha=x y z b=2":                    "a=1 senha=***",
		"secret=\"sem fechar aspas e segue":      "secret=***",
		"AMPMGRPASS=s3gr3d0 AMPDBPASS=outro x":   "AMPMGRPASS=***",
		"md5=abc challenge=def\tx=1":             "md5=***",
		"secret=abc def=X":                       "secret=***",
		"password:":                              "password:",
		"x secret='fecha' y=1 token=\"a b\" z=2": "x secret='***' y=1 token=\"***\" z=2",
		"{'secret': 'X', 'ok': True}":            "{'secret': '***', 'ok': True}",
		`{"a": 1, 'pwd': "x y", 'n': 2}`:         `{"a": 1, 'pwd': "***", 'n': 2}`,
	}
	for in, want := range casos {
		for _, anon := range []bool{false, true} {
			if got := textOf(anon, in); got != want {
				t.Errorf("anon=%v\n in  %q\n got %q\nwant %q", anon, in, got, want)
			}
		}
	}
}

func TestTextChavesAmplas(t *testing.T) {
	for _, key := range []string{"passphrase", "hash", "md5hash", "expectedresponse", "pass", "pwd", "senha", "AMPMGRPASS", "AMPDBPASS", "auth", "api_auth", "session",
		"sessionid", "hmac", "signature", "credential", "credentials", "keyfile", "privatekey", "private_key", "db_password", "passwd"} {
		in := key + "=ValorSecreto123 resto=1"
		if got := textOf(false, in); strings.Contains(got, "ValorSecreto123") || strings.Contains(got, "resto") || got != key+"=***" {
			t.Errorf("%s: %q", key, got)
		}
	}
	// cabecalhos: a linha toda depois do ":"
	for _, h := range []string{"Cookie: sid=abc; outro=def", "Set-Cookie: painel_session=abc; Path=/; HttpOnly", "Authorization: Bearer abc.def.ghi",
		"Proxy-Authorization: Basic YWJjOmRlZg==", "authorization:Bearer xyz"} {
		got := textOf(false, h)
		if i := strings.Index(h, ":"); got != h[:i+1]+maskedValue && got != h[:i+1]+" "+maskedValue {
			t.Errorf("cabecalho %q -> %q", h, got)
		}
	}
	// sem falso positivo nas chaves estruturais e nas palavras comuns
	for _, ok := range []string{"Response: Success", "Event: PeerStatus", "Privilege: system,reporting", "passive=1 bypassed=2 passenger=3", "Uniqueid: 1791902000.10"} {
		if got := textOf(false, ok); got != ok {
			t.Errorf("nao devia mexer em %q: %q", ok, got)
		}
	}
}

func TestTextUserinfo(t *testing.T) {
	casos := map[string]string{
		"register => 2001:S3nha@pbx.exemplo.com/2001":       "register => 2001:***@pbx.exemplo.com/2001",
		"Contact: <sip:maria:Seg9@203.0.113.9:5060>":        "Contact: <sip:maria:***@203.0.113.9:5060>",
		"dial IAX2/usr:pw123@200.1.1.1/ramal":               "dial IAX2/usr:***@200.1.1.1/ramal",
		"curl https://admin:topsecret@host.exemplo/x":       "curl https://admin:***@host.exemplo/x",
		"Contact: 2001/sip:2001@192.0.2.23:5060;ob":         "Contact: 2001/sip:***@192.0.2.23:5060;ob",
		"From: <sip:2001@host>;tag=1 Mailbox: 2001@default": "From: <sip:2001@host>;tag=1 Mailbox: 2001@default",
	}
	for in, want := range casos {
		if got := textOf(false, in); got != want {
			t.Errorf("\n in  %q\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestTextDigest(t *testing.T) {
	in := `Authorization: Digest username="2001", realm="asterisk", nonce="1a2b3c", uri="sip:pbx", response="deadbeefcafe", algorithm=MD5`
	if got := textOf(false, in); strings.Contains(got, "deadbeef") || strings.Contains(got, "1a2b3c") {
		t.Errorf("header: %q", got)
	}
	// em outro cabecalho do mesmo pacote (sem chave Authorization), response/nonce de Digest tambem somem
	in2 := `WWW-Authenticate: Digest realm="asterisk", nonce="9f8e7d", opaque="o1p2", response="cafe01"`
	got := textOf(false, in2)
	for _, v := range []string{"9f8e7d", "o1p2", "cafe01"} {
		if strings.Contains(got, v) {
			t.Errorf("sobrou %s: %q", v, got)
		}
	}
	// "Response: Success" do AMI nao e Digest
	if got := textOf(false, "Response: Success"); got != "Response: Success" {
		t.Errorf("AMI: %q", got)
	}
}

func TestTextHostCadastrado(t *testing.T) {
	s := NewSanitizer(SanitizeOptions{Anonymize: true})
	s.AddHost("pbx-matriz")
	s.AddHost("PBX-Matriz.empresa.com.br")
	s.AddHost("localhost")
	out := string(s.Text([]byte("out 15 14:30:01 pbx-matriz painel-ramais[88]: de pbx-matriz.empresa.com.br e Contact: sip:2001@PBX-MATRIZ.empresa.com.br\n")))
	if strings.Contains(strings.ToLower(out), "matriz") || strings.Contains(out, "empresa") {
		t.Errorf("hostname sobrou: %q", out)
	}
	if strings.Count(out, ".invalid") != 3 || !strings.Contains(out, "painel-ramais[88]") {
		t.Errorf("esperava 3 hosts trocados e o resto intacto: %q", out)
	}
	// o mesmo FQDN e sempre o mesmo ficticio (texto livre e host de URI)
	a := strings.Index(out, "de ")
	b := strings.Index(out, "@")
	fq1 := strings.Fields(out[a:])[1]
	fq2 := out[b+1 : b+1+len(fq1)]
	if fq1 != fq2 {
		t.Errorf("ficticios diferentes para o mesmo FQDN: %q x %q", fq1, fq2)
	}
	// sem Anonymize o nome fica
	if got := textOf(false, "pbx-matriz"); got != "pbx-matriz" {
		t.Errorf("sem anonimizar nao troca: %q", got)
	}
}

// A senha pode ter "@" e "/": o trecho e ancorado no ULTIMO "@". Depois de "/" o token nunca e esquema.
func TestTextUserinfoUltimoArrobaEBarra(t *testing.T) {
	casos := map[string]string{
		"register => 1001:p@sX@host.exemplo/1001":        "register => 1001:***@host.exemplo/1001",
		"register => 1001:ab/X@host.exemplo/1001":        "register => 1001:***@host.exemplo/1001",
		"dial IAX2/tcp:X@host.exemplo/ramal":             "dial IAX2/tcp:***@host.exemplo/ramal",
		"dial IAX2/sip:X@host.exemplo/ramal":             "dial IAX2/sip:***@host.exemplo/ramal",
		"Contact: <sip:maria:S@gr/ed0@203.0.113.9:5060>": "Contact: <sip:maria:***@203.0.113.9:5060>",
		"Contact: <sip:2001@203.0.113.9:5060>":           "Contact: <sip:2001@203.0.113.9:5060>",
		"url https://u:p@ss@h.exemplo/x":                 "url https://u:***@h.exemplo/x",
		"Mailbox: 2001@default":                          "Mailbox: 2001@default",
		"ja mascarado 1001:***@host":                     "ja mascarado 1001:***@host",
	}
	for in, want := range casos {
		if got := textOf(false, in); got != want {
			t.Errorf("\n in  %q\n got %q\nwant %q", in, got, want)
		}
	}
}

// Segredo dentro de valor entre aspas, em URL com query e em repr de Python.
func TestTextSegredoEmAspasQueryERepr(t *testing.T) {
	casos := map[string]string{
		`erro="dial tcp: secret=X"`:                      `erro="dial tcp: secret=***"`,
		`msg='falhou token=abc123 em x'`:                 `msg='falhou token=***'`,
		"get https://h.exemplo/p?session=X":              "get https://h.exemplo/p?session=***",
		"get https://h.exemplo/p?a=1&token=X&b=2":        "get https://h.exemplo/p?a=1&token=***",
		`{"url":"https://h/p?password=X&y=1","ok":true}`: `{"url":"https://h/p?password=***","ok":true}`,
		`{'secret': 'X', 'token': "Y"}`:                  `{'secret': '***', 'token': "***"}`,
		`resp="ok" detalhe="sessionid=abc; path=/"`:      `resp="ok" detalhe="sessionid=***"`,
	}
	for in, want := range casos {
		for _, anon := range []bool{false, true} {
			if got := textOf(anon, in); got != want {
				t.Errorf("anon=%v\n in  %q\n got %q\nwant %q", anon, in, got, want)
			}
		}
	}
}

// Chaves que NAO sao segredo: PrivateTmp=/PrivateNetwork= (systemd-analyze security) e session_ttl.
func TestTextChavesQueNaoSaoSegredo(t *testing.T) {
	for _, ln := range []string{"PrivateTmp=yes", "PrivateNetwork=no", "PrivateDevices=yes", "PrivateUsers=no", "session_ttl: 12h",
		"session_timeout=30m", "SessionLimit: 5", "Hashing: no", "authority=x"} {
		want := ln
		if strings.HasPrefix(ln, "authority") { // "auth" ainda casa: prefere mascarar
			continue
		}
		if got := textOf(false, ln); got != want {
			t.Errorf("nao devia mascarar %q: %q", ln, got)
		}
	}
	for _, ln := range []string{"painel_session=abc", "sessionid=abc", "session_id: abc", "session=abc"} {
		if got := textOf(false, ln); strings.Contains(got, "abc") {
			t.Errorf("devia mascarar %q: %q", ln, got)
		}
	}
}
