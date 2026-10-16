package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// Vetores V0 a V3 do plano da fase 2 (C1) e do CONTRATOS.md 3.3. Chaves de exemplo,
// nunca usar em producao.
const (
	vecSecret  = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	vecIssabel = "ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8="

	v0Payload = `{"sub":"issabel:admin","exp":1791902460,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}`
	v0Secret  = "eyJzdWIiOiJpc3NhYmVsOmFkbWluIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.1FevVYbrsil2HqMU283VGL948fcwdxThKkYL6xtxxq0"

	v1Payload = `{"sub":"issabel:admin:fabio","exp":1791902460,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}`
	v1Issabel = "eyJzdWIiOiJpc3NhYmVsOmFkbWluOmZhYmlvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.BvaFo9ur4_kzEeYjvM0EiRPhpy_6qmlZsU7nio54VU4"
	v1Secret  = "eyJzdWIiOiJpc3NhYmVsOmFkbWluOmZhYmlvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.PbqEoW5r-kgT30bYkuhechQCldj_pGcp1iUCVi9haRQ"

	v2Payload = `{"sub":"issabel:user:maria","exp":1791902460,"jti":"r8Wg2b0aU9d7l1Kq4sXvYB"}`
	v2Issabel = "eyJzdWIiOiJpc3NhYmVsOnVzZXI6bWFyaWEiLCJleHAiOjE3OTE5MDI0NjAsImp0aSI6InI4V2cyYjBhVTlkN2wxS3E0c1h2WUIifQ.Q4LwgNiSLChzFaL2PT8hSU-vtRnAGdAUcacuuCe6MYc"

	v3Payload = `{"sub":"roteiro","exp":1791902460,"jti":"s7Vf1a9ZT8c6k0Jp3rWuXC"}`
	v3Secret  = "eyJzdWIiOiJyb3RlaXJvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJzN1ZmMWE5WlQ4YzZrMEpwM3JXdVhDIn0.CpvxeT22yxalo6wlbU5qUNnNvdADVR9DfzJ31NA2Yfc"
	v3Issabel = "eyJzdWIiOiJyb3RlaXJvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJzN1ZmMWE5WlQ4YzZrMEpwM3JXdVhDIn0.DqPJ2WjOE9vIsaS0PehNvhUPxX8OSDdU_6DqhxD6QXY"
)

func mustKey(t *testing.T, b64s string) []byte {
	t.Helper()
	k, err := base64.StdEncoding.DecodeString(b64s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// vecVerifier cria um verificador novo (jti limpo) com o segredo do daemon e,
// se withIssabel, com a chave do Issabel.
func vecVerifier(t *testing.T, at time.Time, withIssabel bool) *Verifier {
	t.Helper()
	v := NewVerifier(mustKey(t, vecSecret))
	if withIssabel {
		v.SetIssabelKey(mustKey(t, vecIssabel))
	}
	v.Now = func() time.Time { return at }
	return v
}

// 30 s antes do exp dos vetores (exp = 1791902460).
var vecNow = time.Unix(1791902460, 0).Add(-30 * time.Second)

// Cada linha da tabela do C1; o papel e conferido por IsAdmin.
func TestVectorsV0toV3(t *testing.T) {
	cases := []struct {
		name  string
		token string
		sub   string
		ok    bool
		admin bool
	}{
		{"V1 chave do Issabel: aceito, administrador", v1Issabel, "issabel:admin:fabio", true, true},
		{"V1 segredo do daemon: recusado (prefixo reservado)", v1Secret, "", false, false},
		{"V2 chave do Issabel: aceito, operador", v2Issabel, "issabel:user:maria", true, false},
		{"V3 segredo do daemon: aceito, operador", v3Secret, "roteiro", true, false},
		{"V3 chave do Issabel: recusado (sem prefixo)", v3Issabel, "", false, false},
		{"V0 (sub issabel:admin) com o segredo do daemon: agora recusado", v0Secret, "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := vecVerifier(t, vecNow, true).Verify(c.token)
			if (err == nil) != c.ok {
				t.Fatalf("err = %v, ok esperado = %v", err, c.ok)
			}
			if !c.ok {
				if err != ErrInvalidToken {
					t.Errorf("recusa deve ser ErrInvalidToken, veio %v", err)
				}
				return
			}
			if got.Sub != c.sub || got.Exp != 1791902460 {
				t.Errorf("claims %+v", got)
			}
			if IsAdmin("token", got.Sub) != c.admin {
				t.Errorf("IsAdmin = %v, queria %v", !c.admin, c.admin)
			}
		})
	}
}

// Os vetores sao o que as chaves produzem para o payload dado (confere o calculo).
func TestVectorSignatures(t *testing.T) {
	sec, iss := mustKey(t, vecSecret), mustKey(t, vecIssabel)
	for _, c := range []struct {
		key             []byte
		payload, expect string
	}{
		{sec, v0Payload, v0Secret},
		{iss, v1Payload, v1Issabel}, {sec, v1Payload, v1Secret},
		{iss, v2Payload, v2Issabel},
		{sec, v3Payload, v3Secret}, {iss, v3Payload, v3Issabel},
	} {
		if got := sign(c.key, []byte(c.payload)); got != c.expect {
			t.Errorf("assinatura divergente do vetor:\n%s\n%s", got, c.expect)
		}
	}
}

// Sem a chave do Issabel, V1 e V2 dao 401; carregada depois (sem recriar o verificador),
// passam; descarregada (nil), voltam a dar 401. V3 vale nos tres estados.
func TestIssabelKeyHotSwap(t *testing.T) {
	v := vecVerifier(t, vecNow, false)
	if _, err := v.Verify(v1Issabel); err == nil {
		t.Fatal("sem chave do Issabel, V1 deve ser recusado")
	}
	v.SetIssabelKey(mustKey(t, vecIssabel))
	if c, err := v.Verify(v1Issabel); err != nil || c.Sub != "issabel:admin:fabio" {
		t.Fatalf("chave carregada, V1 deve valer (a recusa anterior nao queima o jti): %+v %v", c, err)
	}
	v.SetIssabelKey(nil)
	if _, err := v.Verify(v2Issabel); err == nil {
		t.Fatal("chave descarregada, V2 deve ser recusado")
	}
	if _, err := v.Verify(v3Secret); err != nil {
		t.Fatalf("segredo do daemon independe da chave do Issabel: %v", err)
	}
}

// Recusa por regra do sub nao consome o jti: o mesmo jti ainda vale com a chave certa.
func TestRejectedTokenDoesNotBurnJTI(t *testing.T) {
	v := vecVerifier(t, vecNow, true)
	if _, err := v.Verify(v1Secret); err == nil {
		t.Fatal("V1/segredo deve ser recusado")
	}
	if _, err := v.Verify(v1Issabel); err != nil {
		t.Fatalf("mesmo jti com a chave certa deve valer: %v", err)
	}
	if _, err := v.Verify(v1Issabel); err == nil {
		t.Error("segundo uso do V1 deve falhar (jti de uso unico)")
	}
}

func TestIssabelSubGrammar(t *testing.T) {
	a64 := strings.Repeat("a", 64)
	cases := map[string]bool{
		"issabel:admin:ana":                        true,
		"issabel:user:ana":                         true,
		"issabel:admin:a:b:c":                      true, // ':' permitido no usuario
		"issabel:admin:" + a64:                     true,
		"issabel:admin:" + a64 + "a":               false, // 65 runas
		"issabel:admin:" + strings.Repeat("ç", 64): true,  // 64 runas, 128 bytes no usuario
		"issabel:admin:":                           false,
		"issabel:user:":                            false,
		"issabel:admin:a\nb":                       false,
		"issabel:admin:a\x00b":                     false,
		"issabel:admin:a\u0085b":                   false, // controle C1
		"issabel:admin:\xff":                       false, // UTF-8 invalido
		"issabel:root:ana":                         false,
		"issabel:admin":                            false,
		"ISSABEL:admin:ana":                        false, // lado do Issabel e exato
		"ana":                                      false,
		"issabel:user:Ana Maria":                   true,  // espaco comum (Zs) vale
		"issabel:admin:Ana\u202eatsiB":             false, // Cf: inversao de direcao (RLO)
		"issabel:admin:\u2066ana\u2069":            false, // Cf: isolamento bidi
		"issabel:admin:a\u200bna":                  false, // Cf: espaco de largura zero
		"issabel:admin:\ufeffana":                  false, // Cf: BOM
		"issabel:user:an\u00ada":                   false, // Cf: soft hyphen
		"issabel:user:ana\u2028":                   false, // Zl
		"issabel:user:ana\u2029":                   false, // Zp
		"issabel:user:\u00e7\u00e3o":               true,
	}
	for sub, want := range cases {
		if got := ValidIssabelSub(sub); got != want {
			t.Errorf("ValidIssabelSub(%q) = %v, queria %v", sub, got, want)
		}
	}
	// Assinado com a chave do Issabel, o verificador tambem so aceita a gramatica
	// (a faixa de 128 bytes do sub total e conferida a parte).
	iss := mustKey(t, vecIssabel)
	for sub, want := range map[string]bool{
		"issabel:admin:":             false,
		"issabel:admin:" + a64 + "a": false,
		"issabel:user:bia":           true,
		"issabel:admin:a\x01b":       false,
	} {
		tok, err := IssueToken(iss, sub, 30*time.Second, vecNow)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := vecVerifier(t, vecNow, true).Verify(tok); (err == nil) != want {
			t.Errorf("verify %q: err = %v, ok esperado = %v", sub, err, want)
		}
	}
}

// O prefixo reservado vale sem diferenciar maiusculas no lado do segredo do daemon.
func TestSecretCannotUseIssabelPrefix(t *testing.T) {
	sec := mustKey(t, vecSecret)
	for _, sub := range []string{"issabel:", "Issabel:user:x", "ISSABEL:ADMIN:x", "issabel:admin"} {
		tok, err := IssueToken(sec, sub, 30*time.Second, vecNow)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := vecVerifier(t, vecNow, true).Verify(tok); err == nil {
			t.Errorf("sub %q com o segredo do daemon deve ser recusado", sub)
		}
	}
	for _, sub := range []string{"issabelx", "issab", "roteiro:issabel:", "a"} {
		tok, _ := IssueToken(sec, sub, 30*time.Second, vecNow)
		if _, err := vecVerifier(t, vecNow, true).Verify(tok); err != nil {
			t.Errorf("sub %q com o segredo do daemon deve valer: %v", sub, err)
		}
	}
}

func TestIsAdmin(t *testing.T) {
	for _, c := range []struct {
		kind, sub string
		want      bool
	}{
		{"token", "issabel:admin:fabio", true}, {"token", "issabel:user:fabio", false},
		{"password", "issabel:admin:fabio", false}, {"token", "roteiro", false}, {"password", "emergencia", false},
	} {
		if IsAdmin(c.kind, c.sub) != c.want {
			t.Errorf("IsAdmin(%q,%q) = %v", c.kind, c.sub, !c.want)
		}
	}
}
