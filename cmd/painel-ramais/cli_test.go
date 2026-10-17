package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"painel-ramais/internal/auth"
	"painel-ramais/internal/store"
)

func init() { auth.BcryptCost = bcrypt.MinCost }

type rig struct {
	t        *testing.T
	dir, cfg string
	out, err bytes.Buffer
	notified int
	euid     int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	d := t.TempDir()
	secret := filepath.Join(d, "secret")
	os.WriteFile(secret, []byte("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=\n"), 0o600)
	issabel := filepath.Join(d, "issabel.key") // bytes 0x20 a 0x3f, a chave de exemplo do plano (C1)
	os.WriteFile(issabel, []byte("ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8=\n"), 0o600)
	cfg := filepath.Join(d, "config.yaml")
	y := "data_dir: " + filepath.ToSlash(d) + "\nauth:\n  secret_file: " + filepath.ToSlash(secret) +
		"\n  issabel_key_file: " + filepath.ToSlash(issabel) + "\n"
	os.WriteFile(cfg, []byte(y), 0o600)
	return &rig{t: t, dir: d, cfg: cfg, euid: 1000}
}

// run executa a CLI com stdin dado e devolve o codigo de saida.
func (r *rig) run(stdin string, args ...string) int {
	r.out.Reset()
	r.err.Reset()
	c := &cli{
		stdin: strings.NewReader(stdin), stdout: &r.out, stderr: &r.err, euid: func() int { return r.euid },
		notify:  func(io.Writer) { r.notified++ },
		readTTY: func(string) (string, error) { return "", os.ErrInvalid },
	}
	return c.run(args)
}

func (r *rig) store() *store.Store {
	r.t.Helper()
	s, err := store.Open(context.Background(), r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { s.Close() })
	return s
}

func TestVersionAndRootRefusal(t *testing.T) {
	r := newRig(t)
	if code := r.run("", "version"); code != 0 || r.out.String() != fmt.Sprintf("painel-ramais %s schema %d\n", version, store.SchemaVersion) {
		t.Errorf("version: %d %q", code, r.out.String())
	}
	r.euid = 0
	if code := r.run("", "version"); code != 0 {
		t.Error("version funciona como root")
	}
	for _, args := range [][]string{{"serve"}, {"user", "add", "x"}, {"group", "list"}, {"token", "--sub", "a"}} {
		if code := r.run("", append(args, "--config", r.cfg)...); code != 1 || !strings.Contains(r.err.String(), "não rode como root") {
			t.Errorf("%v como root: %d %q", args, code, r.err.String())
		}
	}
	r.euid = 1000
	if code := r.run("", "bogus"); code != 1 {
		t.Errorf("subcomando desconhecido: %d", code)
	}
}

func TestUserLifecycle(t *testing.T) {
	r := newRig(t)
	if code := r.run("uma-senha-longa\n", "user", "add", "fabio", "--password-stdin", "--config", r.cfg); code != 0 || r.notified != 1 {
		t.Fatalf("add: %d %q notified=%d", code, r.err.String(), r.notified)
	}
	u, err := r.store().UserByName(context.Background(), "fabio")
	if err != nil || !auth.CheckPassword(u.Hash, "uma-senha-longa") {
		t.Fatalf("senha nao gravada em bcrypt: %v", err)
	}
	// Ja existe: codigo 3, nada alterado, sem aviso ao daemon.
	if code := r.run("outra-senha-longa\n", "user", "add", "FABIO", "--password-stdin", "--config", r.cfg); code != 3 || r.notified != 1 {
		t.Errorf("add duplicado: %d", code)
	}
	u2, _ := r.store().UserByName(context.Background(), "fabio")
	if u2.Hash != u.Hash {
		t.Error("senha nao pode mudar no add duplicado")
	}
	// Flags antes do usuario tambem valem; senha curta = 1.
	if code := r.run("curta\n", "user", "add", "--password-stdin", "--config", r.cfg, "maria"); code != 1 || !strings.Contains(r.err.String(), "menos de 10") {
		t.Errorf("senha curta: %d %q", code, r.err.String())
	}
	if code := r.run("", "user", "add", "maria", "--password-stdin", "--config", r.cfg); code != 1 {
		t.Errorf("stdin vazio: %d", code)
	}
	if code := r.run("nova-senha-longa", "user", "passwd", "fabio", "--password-stdin", "--config", r.cfg); code != 0 {
		t.Fatalf("passwd: %d %q", code, r.err.String())
	}
	if u3, _ := r.store().UserByName(context.Background(), "fabio"); !auth.CheckPassword(u3.Hash, "nova-senha-longa") {
		t.Error("passwd nao trocou")
	}
	if code := r.run("x-x-x-x-x-x-x-x", "user", "passwd", "ninguem", "--password-stdin", "--config", r.cfg); code != 1 {
		t.Errorf("passwd de inexistente: %d", code)
	}
	if code := r.run("", "user", "del", "fabio", "--config", r.cfg); code != 0 {
		t.Errorf("del: %d", code)
	}
	if code := r.run("", "user", "del", "fabio", "--config", r.cfg); code != 1 {
		t.Errorf("del repetido: %d", code)
	}
	if code := r.run("", "user", "add", "x\ty", "--config", r.cfg); code != 1 {
		t.Errorf("usuario invalido: %d", code)
	}
}

func TestGroupCommands(t *testing.T) {
	r := newRig(t)
	csv := filepath.Join(r.dir, "g.csv")
	os.WriteFile(csv, []byte("grupo,ramal\nSuporte,2000-2099\nSuporte,2150\nVendas,3000-3999\n"), 0o600)
	if code := r.run("", "group", "import", csv, "--config", r.cfg); code != 0 || r.notified != 1 {
		t.Fatalf("import: %d %q", code, r.err.String())
	}
	if code := r.run("", "group", "list", "--config", r.cfg); code != 0 || r.out.String() != "Suporte\t2000-2099\t2150\nVendas\t3000-3999\n" || r.notified != 1 {
		t.Errorf("list: %d %q", code, r.out.String())
	}
	// Sobreposicao com o que existe: rejeita e cita a linha.
	bad := filepath.Join(r.dir, "bad.csv")
	os.WriteFile(bad, []byte("Novo,5000\nOutro,2050-2060\n"), 0o600)
	if code := r.run("", "group", "import", bad, "--config", r.cfg); code != 1 || !strings.Contains(r.err.String(), "linha 2") {
		t.Errorf("sobreposicao: %d %q", code, r.err.String())
	}
	// --replace troca tudo, e o mesmo arquivo de novo da o mesmo resultado.
	for i := 0; i < 2; i++ {
		if code := r.run("", "group", "import", "--replace", csv, "--config", r.cfg); code != 0 {
			t.Fatalf("replace %d: %d %q", i, code, r.err.String())
		}
	}
	os.WriteFile(bad, []byte("Novo,5000\n"), 0o600)
	r.run("", "group", "import", "--replace", bad, "--config", r.cfg)
	r.run("", "group", "list", "--config", r.cfg)
	if r.out.String() != "Novo\t\t5000\n" {
		t.Errorf("replace deve apagar o resto: %q", r.out.String())
	}
	if code := r.run("", "group", "del", "Novo", "--config", r.cfg); code != 0 {
		t.Errorf("del: %d", code)
	}
	if code := r.run("", "group", "del", "Novo", "--config", r.cfg); code != 1 {
		t.Errorf("del repetido: %d", code)
	}
	if code := r.run("", "group", "import", filepath.Join(r.dir, "nao-existe.csv"), "--config", r.cfg); code != 2 {
		t.Errorf("CSV ausente: %d", code)
	}
	if code := r.run("", "group", "list", "--replace", "--config", r.cfg); code != 1 {
		t.Errorf("--replace fora do import: %d", code)
	}
}

func TestTokenCommand(t *testing.T) {
	r := newRig(t)
	if code := r.run("", "token", "--sub", "roteiro", "--config", r.cfg); code != 0 {
		t.Fatalf("token: %d %q", code, r.err.String())
	}
	tok := strings.TrimSpace(r.out.String())
	if strings.Contains(tok, " ") || strings.Count(tok, ".") != 1 || r.err.Len() != 0 {
		t.Errorf("so o token no stdout: %q / %q", tok, r.err.String())
	}
	secret := mustSecret(t)
	v := auth.NewVerifier(secret)
	if c, err := v.Verify(tok); err != nil || c.Sub != "roteiro" {
		t.Errorf("token invalido: %v", err)
	}
	if c, _ := v.Verify(tok); c.Sub != "" {
		t.Error("segundo uso")
	}
	if code := r.run("", "token", "--sub", "a", "--ttl", "61s", "--config", r.cfg); code != 1 {
		t.Errorf("ttl > 60s: %d", code)
	}
	if code := r.run("", "token", "--config", r.cfg); code != 1 {
		t.Errorf("sem --sub: %d", code)
	}
	if code := r.run("", "token", "--sub", "a", "--config", filepath.Join(r.dir, "nada.yaml")); code != 2 {
		t.Errorf("config ausente: %d", code)
	}
}

func mustSecret(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func mustIssabelKey() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(0x20 + i)
	}
	return b
}

func TestTokenIssabel(t *testing.T) {
	r := newRig(t)
	// Com --issabel: assinado com a chave do Issabel; o verificador aceita como administrador.
	if code := r.run("", "token", "--issabel", "--sub", "issabel:admin:ana", "--config", r.cfg); code != 0 {
		t.Fatalf("token --issabel: %d %q", code, r.err.String())
	}
	tok := strings.TrimSpace(r.out.String())
	v := auth.NewVerifier(mustSecret(t))
	if _, err := v.Verify(tok); err == nil {
		t.Fatal("sem a chave do Issabel carregada o verificador deve recusar")
	}
	v.SetIssabelKey(mustIssabelKey())
	c, err := v.Verify(tok)
	if err != nil || c.Sub != "issabel:admin:ana" || !auth.IsAdmin("token", c.Sub) {
		t.Fatalf("token do Issabel: %+v %v", c, err)
	}
	// Operador.
	r.run("", "token", "--issabel", "--sub", "issabel:user:bia", "--config", r.cfg)
	if c, err := v.Verify(strings.TrimSpace(r.out.String())); err != nil || auth.IsAdmin("token", c.Sub) {
		t.Errorf("operador: %+v %v", c, err)
	}
	// Sub fora da gramatica com --issabel, e sub issabel: sem --issabel: erro de uso.
	for _, sub := range []string{"roteiro", "issabel:admin:", "issabel:root:x", "issabel:admin"} {
		if code := r.run("", "token", "--issabel", "--sub", sub, "--config", r.cfg); code != 1 || r.out.Len() != 0 {
			t.Errorf("--issabel --sub %q: %d", sub, code)
		}
	}
	for _, sub := range []string{"issabel:x", "issabel:admin:ana", "ISSABEL:user:ana", "issabel:"} {
		if code := r.run("", "token", "--sub", sub, "--config", r.cfg); code != 1 || !strings.Contains(r.err.String(), "use --issabel para sub issabel:") || r.out.Len() != 0 {
			t.Errorf("sem --issabel --sub %q: %d %q", sub, code, r.err.String())
		}
	}
	// Chave ausente: erro de ambiente (2), sem token.
	os.Remove(filepath.Join(r.dir, "issabel.key"))
	if code := r.run("", "token", "--issabel", "--sub", "issabel:admin:ana", "--config", r.cfg); code != 2 || r.out.Len() != 0 {
		t.Errorf("chave ausente: %d %q", code, r.err.String())
	}
}
