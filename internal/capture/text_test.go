package capture

import (
	"strings"
	"testing"
)

// Segredo plantado em varios formatos de texto livre: nunca sobra, com ou sem anonimizar.
func TestTextMasksSecretsInEveryFormat(t *testing.T) {
	in := strings.Join([]string{
		`Secret: S3gredoAMI`,
		`ami login falhou secret=S3gredoAMI user=painel-ramais`,
		`{"secret":"S3gredoAMI","password": "S3gredoAMI","ok":true}`,
		`Authorization token: S3gredoAMI`,
		`md5=S3gredoAMI; challenge=S3gredoAMI&key=1`,
		`secret: "S3gredoAMI"`,
	}, "\r\n") + "\n"
	for _, anon := range []bool{false, true} {
		out := string(NewSanitizer(SanitizeOptions{Anonymize: anon}).Text([]byte(in)))
		if strings.Contains(out, "S3gredoAMI") {
			t.Errorf("anon=%v: segredo vazou:\n%s", anon, out)
		}
		if strings.Count(out, "***") < 6 {
			t.Errorf("anon=%v: esperava mascara em todas as linhas:\n%s", anon, out)
		}
		if strings.Count(out, "\r\n") != 5 || !strings.HasSuffix(out, "\n") {
			t.Errorf("anon=%v: quebras de linha alteradas: %q", anon, out)
		}
	}
}

// Sem --anonimizar, so segredos mudam; o resto do texto passa intacto.
func TestTextWithoutAnonymizeKeepsEverythingElse(t *testing.T) {
	in := "Oct 15 14:30:00 pbx painel-ramais[88]: contato 2001 em 192.0.2.23 num=11987654321 token=abc\n"
	out := string(NewSanitizer(SanitizeOptions{}).Text([]byte(in)))
	want := "Oct 15 14:30:00 pbx painel-ramais[88]: contato 2001 em 192.0.2.23 num=11987654321 token=***\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// Anonimizando: o mesmo original vira o mesmo ficticio em textos diferentes (painel x Asterisk),
// ramais curtos e estrutura ficam, e nada do original sobra.
func TestTextAnonymizeIsConsistentAcrossLines(t *testing.T) {
	s := NewSanitizer(SanitizeOptions{Anonymize: true})
	panel := string(s.Text([]byte(`{"number":"2001","name":"Maria Souza","ip":"203.0.113.45","peer":{"kind":"external","number":"11987654321","name":"ACME LTDA"}}` + "\n")))
	ast := string(s.Text([]byte("Contact:  2001/sip:2001@203.0.113.45:5060;ob  a1b2c3 Avail 12.3\nPJSIP/trunk-00000011!from-trunk!s!1!Up!Dial!x!11987654321!!!3!125!br-1!1791902000.10\n")))
	for _, orig := range []string{"Maria Souza", "ACME LTDA", "203.0.113.45", "11987654321"} {
		if strings.Contains(panel+ast, orig) {
			t.Errorf("sobrou %q:\n%s%s", orig, panel, ast)
		}
	}
	fakeIP := between(panel, `"ip":"`, `"`)
	if !strings.HasPrefix(fakeIP, "10.200.") || !strings.Contains(ast, "@"+fakeIP+":5060") {
		t.Errorf("IP deveria ser o mesmo ficticio nos dois textos: %q\n%s", fakeIP, ast)
	}
	fakeNum := between(panel, `"number":"1`, `"`)
	if len(fakeNum) != 10 || !strings.Contains(ast, "!1"+fakeNum+"!") {
		t.Errorf("numero externo deveria ser o mesmo ficticio: %q\n%s", fakeNum, ast)
	}
	if !strings.Contains(panel, `"number":"2001"`) || !strings.Contains(panel, `"name":"Pessoa 001"`) || !strings.Contains(panel, `"name":"Pessoa 002"`) {
		t.Errorf("estrutura/nomes inesperados: %s", panel)
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	if j := strings.Index(s, b); j >= 0 {
		return s[:j]
	}
	return ""
}
