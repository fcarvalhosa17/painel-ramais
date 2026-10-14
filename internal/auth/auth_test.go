package auth

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func init() { BcryptCost = bcrypt.MinCost }

// Vetor de teste do CONTRATOS.md secao 3.3 (segredo de exemplo, nunca usar em producao).
const (
	vecSecret  = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	vecPayload = `{"sub":"issabel:admin","exp":1791902460,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}`
	vecToken   = "eyJzdWIiOiJpc3NhYmVsOmFkbWluIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.1FevVYbrsil2HqMU283VGL948fcwdxThKkYL6xtxxq0"
)

func vecVerifier(t *testing.T, at time.Time) *Verifier {
	t.Helper()
	secret, err := base64.StdEncoding.DecodeString(vecSecret)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier(secret)
	v.Now = func() time.Time { return at }
	return v
}

func TestVectorValidatesOnceOnly(t *testing.T) {
	exp := time.Unix(1791902460, 0)
	v := vecVerifier(t, exp.Add(-30*time.Second))
	c, err := v.Verify(vecToken)
	if err != nil || c.Sub != "issabel:admin" || c.Exp != 1791902460 {
		t.Fatalf("vetor deveria validar: %+v %v", c, err)
	}
	if _, err := v.Verify(vecToken); err == nil {
		t.Error("segundo uso do mesmo token deve falhar (jti de uso unico)")
	}
	// O payload exato do vetor e o que foi assinado.
	secret, _ := base64.StdEncoding.DecodeString(vecSecret)
	if got := sign(secret, []byte(vecPayload)); got != vecToken {
		t.Errorf("assinatura divergente do vetor:\n%s\n%s", got, vecToken)
	}
}

func TestTokenRejections(t *testing.T) {
	secret, _ := base64.StdEncoding.DecodeString(vecSecret)
	now := time.Unix(1791902400, 0)
	mk := func(payload string) string { return sign(secret, []byte(payload)) }
	flip := vecToken[:len(vecToken)-1] + "A"
	cases := map[string]string{
		"assinatura trocada": flip,
		"sem ponto":          strings.ReplaceAll(vecToken, ".", ""),
		"tres partes":        vecToken + ".x",
		"vazio":              "",
		"exp passado":        mk(`{"sub":"a","exp":1791902399,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}`),
		"exp igual a agora":  mk(`{"sub":"a","exp":1791902400,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}`),
		"exp alem de 65s":    mk(`{"sub":"a","exp":1791902466,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}`),
		"campo extra":        mk(`{"sub":"a","exp":1791902430,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA","x":1}`),
		"sem jti":            mk(`{"sub":"a","exp":1791902430}`),
		"jti curto":          mk(`{"sub":"a","exp":1791902430,"jti":"abc"}`),
		"jti nao base64url":  mk(`{"sub":"a","exp":1791902430,"jti":"q9Xh3c1bV0e8m2Lr5t!!!!"}`),
		"sub vazio":          mk(`{"sub":"","exp":1791902430,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}`),
		"lixo depois":        mk(`{"sub":"a","exp":1791902430,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}{}`),
		"muito grande":       mk(`{"sub":"` + strings.Repeat("a", 2000) + `","exp":1791902430,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}`),
	}
	for name, tok := range cases {
		if _, err := vecVerifier(t, now).Verify(tok); err != ErrInvalidToken {
			t.Errorf("%s: err = %v, queria ErrInvalidToken", name, err)
		}
	}
	// Limites aceitos: exp = agora + 65 s ainda vale.
	ok := mk(`{"sub":"a","exp":1791902465,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}`)
	if _, err := vecVerifier(t, now).Verify(ok); err != nil {
		t.Errorf("exp = agora+65s deve valer: %v", err)
	}
	// Segredo diferente.
	other := NewVerifier([]byte(strings.Repeat("x", 32)))
	other.Now = func() time.Time { return now }
	if _, err := other.Verify(ok); err == nil {
		t.Error("segredo errado deve falhar")
	}
}

func TestIssueAndVerify(t *testing.T) {
	secret := []byte(strings.Repeat("k", 32))
	now := time.Now()
	tok, err := IssueToken(secret, "issabel:admin", 60*time.Second, now)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier(secret)
	if c, err := v.Verify(tok); err != nil || c.Sub != "issabel:admin" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := IssueToken(secret, "a", 61*time.Second, now); err == nil {
		t.Error("ttl acima de 60 s deve ser recusado")
	}
	if _, err := IssueToken(secret, "", time.Second, now); err == nil {
		t.Error("sub vazio deve ser recusado")
	}
}

func TestPasswords(t *testing.T) {
	if _, err := HashPassword("curta"); err == nil {
		t.Error("senha curta")
	}
	if _, err := HashPassword(strings.Repeat("a", 73)); err == nil {
		t.Error("senha acima de 72 bytes")
	}
	h, err := HashPassword("uma-senha-longa")
	if err != nil || !strings.HasPrefix(h, "$2") {
		t.Fatalf("hash: %q %v", h, err)
	}
	if !CheckPassword(h, "uma-senha-longa") || CheckPassword(h, "outra-senha-longa") {
		t.Error("CheckPassword")
	}
	if CheckPassword("", "uma-senha-longa") {
		t.Error("usuario inexistente nunca autentica")
	}
}

func TestSessionID(t *testing.T) {
	c, h, err := NewSessionID()
	if err != nil || len(c) != 43 || h != HashSessionID(c) {
		t.Fatalf("%q %v", c, err)
	}
	c2, _, _ := NewSessionID()
	if c == c2 {
		t.Error("ids repetidos")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Now()
	l := NewLimiter()
	l.Now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if !l.Reserve("fabio") {
			t.Fatalf("bloqueou na tentativa %d", i)
		}
	}
	if l.Reserve("fabio") || !l.Reserve("outro") {
		t.Error("5 tentativas bloqueiam so o proprio usuario")
	}
	now = now.Add(5*time.Minute + time.Second)
	if !l.Reserve("fabio") {
		t.Error("janela passou")
	}
	l.Reset("fabio")
	if _, ok := l.fails["fabio"]; ok {
		t.Error("Reset limpa")
	}
}

// Reserve e atomico: com 200 goroutines disputando, exatamente Max passam.
func TestLimiterReserveIsAtomic(t *testing.T) {
	l := NewLimiter()
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Reserve("alvo") {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 5 {
		t.Errorf("%d reservas, queria 5", ok.Load())
	}
}

// Mapa cheio: o usuario novo entra e sai a chave mais antiga (memoria limitada).
func TestLimiterEvictsOldestWhenFull(t *testing.T) {
	now := time.Now()
	l := NewLimiter()
	l.Now = func() time.Time { now = now.Add(time.Millisecond); return now }
	for i := 0; i < maxTrackedUsers; i++ {
		l.Reserve(fmt.Sprint("fake", i))
	}
	if !l.Reserve("alvo") {
		t.Error("com o mapa cheio, usuario novo deve entrar")
	}
	if len(l.fails) != maxTrackedUsers {
		t.Errorf("mapa passou do teto: %d", len(l.fails))
	}
	if _, ok := l.fails["fake0"]; ok {
		t.Error("a chave mais antiga deveria ter sido descartada")
	}
	if _, ok := l.fails["fake1"]; !ok {
		t.Error("so uma chave sai")
	}
}
