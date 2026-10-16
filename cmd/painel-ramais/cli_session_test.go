package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"painel-ramais/internal/store"
)

func TestGroupImportFromStdin(t *testing.T) {
	r := newRig(t)
	if code := r.run("Suporte;2000-2099\nVendas;3000\n", "group", "import", "--replace", "-", "--config", r.cfg); code != 0 {
		t.Fatalf("import -: %d %q", code, r.err.String())
	}
	r.run("", "group", "list", "--config", r.cfg)
	if r.out.String() != "Suporte\t2000-2099\nVendas\t3000\n" {
		t.Errorf("list: %q", r.out.String())
	}
	if code := r.run("", "group", "import", "-", "--config", r.cfg); code != 1 {
		t.Errorf("stdin vazio deve rejeitar: %d", code)
	}
}

func TestSessionPurge(t *testing.T) {
	r := newRig(t)
	st := r.store()
	ctx := context.Background()
	n := time.Now()
	st.CreateSession(ctx, store.Session{IDHash: [32]byte{1}, Kind: "token", Subject: "velha", CreatedAt: n.Add(-2 * time.Hour), ExpiresAt: n.Add(-time.Hour)})
	st.CreateSession(ctx, store.Session{IDHash: [32]byte{2}, Kind: "token", Subject: "viva", CreatedAt: n, ExpiresAt: n.Add(time.Hour)})
	if code := r.run("", "session", "purge", "--config", r.cfg); code != 0 || !strings.HasPrefix(r.out.String(), "1 sess") || r.notified != 0 {
		t.Fatalf("purge: %d %q notified=%d", code, r.out.String(), r.notified)
	}
	if _, err := st.GetSession(ctx, [32]byte{2}, n); err != nil {
		t.Error("sessao valida nao pode sair no purge simples")
	}
	if code := r.run("", "session", "purge", "--all", "--config", r.cfg); code != 0 || !strings.HasPrefix(r.out.String(), "1 sess") || r.notified != 1 {
		t.Fatalf("purge --all: %d %q notified=%d", code, r.out.String(), r.notified)
	}
	if _, err := st.GetSession(ctx, [32]byte{2}, n); err == nil {
		t.Error("--all apaga tudo")
	}
	if code := r.run("", "session", "bogus"); code != 1 {
		t.Errorf("subcomando invalido: %d", code)
	}
}

func TestSessionPurgeIssabel(t *testing.T) {
	r := newRig(t)
	st := r.store()
	ctx := context.Background()
	n := time.Now()
	mk := func(b byte, sub string) {
		st.CreateSession(ctx, store.Session{IDHash: [32]byte{b}, Kind: "token", Subject: sub, CreatedAt: n, ExpiresAt: n.Add(time.Hour)})
	}
	mk(1, "issabel:admin:ana")
	mk(2, "issabel:user:ana")
	mk(3, "issabel:user:bia")
	mk(4, "ana") // token da CLI (segredo do daemon) com o mesmo nome: nao e do Issabel
	if code := r.run("", "session", "purge", "--issabel", "ana", "--config", r.cfg); code != 0 || !strings.HasPrefix(r.out.String(), "2 sess") || r.notified != 1 {
		t.Fatalf("purge --issabel ana: %d %q notified=%d", code, r.out.String(), r.notified)
	}
	for b, want := range map[byte]bool{1: false, 2: false, 3: true, 4: true} {
		if _, err := st.GetSession(ctx, [32]byte{b}, n); (err == nil) != want {
			t.Errorf("sessao %d: err=%v, deveria existir=%v", b, err, want)
		}
	}
	// Ninguem removido: nao avisa o daemon.
	if code := r.run("", "session", "purge", "--issabel", "ana", "--config", r.cfg); code != 0 || !strings.HasPrefix(r.out.String(), "0 sess") || r.notified != 1 {
		t.Errorf("segunda vez: %d %q notified=%d", code, r.out.String(), r.notified)
	}
	// Erros de uso (codigo 1), antes de abrir o banco.
	for _, args := range [][]string{
		{"--all", "--issabel", "x"}, {"--issabel", "x", "--all"}, {"--issabel", ""}, {"--issabel"},
		{"--issabel", strings.Repeat("a", 65)}, {"--issabel", "a\nb"},
	} {
		before := r.notified
		if code := r.run("", append([]string{"session", "purge"}, append(args, "--config", r.cfg)...)...); code != 1 || r.notified != before {
			t.Errorf("%v: codigo %d", args, code)
		}
	}
	// Nada foi apagado pelos erros de uso.
	if _, err := st.GetSession(ctx, [32]byte{3}, n); err != nil {
		t.Error("erro de uso nao pode apagar sessao")
	}
}
