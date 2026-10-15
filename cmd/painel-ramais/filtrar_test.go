package main

import (
	"strings"
	"testing"

	"painel-ramais/internal/ami"
)

func TestFiltrarMasksSecretsAndAnonymizesOnRequest(t *testing.T) {
	r := newRig(t)
	in := "Secret: S3gredo\nlinha sem quebra final 203.0.113.45 password=S3gredo"
	if code := r.run(in, "filtrar"); code != 0 {
		t.Fatalf("filtrar: %d %q", code, r.err.String())
	}
	if got := r.out.String(); got != "Secret: ***\nlinha sem quebra final 203.0.113.45 password=***" {
		t.Errorf("sem --anonimizar: %q", got)
	}
	if code := r.run(in, "filtrar", "--anonimizar"); code != 0 {
		t.Fatalf("filtrar: %d", code)
	}
	if got := r.out.String(); strings.Contains(got, "S3gredo") || strings.Contains(got, "203.0.113.45") || !strings.Contains(got, "10.200.") {
		t.Errorf("--anonimizar: %q", got)
	}
}

func TestFiltrarRejectsArgsAndRoot(t *testing.T) {
	r := newRig(t)
	if code := r.run("", "filtrar", "sobra"); code != exitUsage {
		t.Errorf("argumento solto: %d", code)
	}
	r.euid = 0
	if code := r.run("x", "filtrar"); code != exitUsage {
		t.Errorf("root: %d", code)
	}
}

func TestActionsListsTheAllowlist(t *testing.T) {
	r := newRig(t)
	if code := r.run("", "actions"); code != 0 {
		t.Fatalf("actions: %d", code)
	}
	if got, want := strings.Fields(r.out.String()), ami.AllowedActions(); strings.Join(got, ",") != strings.Join(want, ",") || len(got) != 13 {
		t.Errorf("got %v want %v", got, want)
	}
	if code := r.run("", "actions", "x"); code != exitUsage {
		t.Errorf("argumento solto: %d", code)
	}
}

func TestFiltrarHostRepetivelEHelp(t *testing.T) {
	r := newRig(t)
	in := "out 15 pbx-matriz svc: de pbx-matriz.empresa.com.br e outro.exemplo.com\n"
	if code := r.run(in, "filtrar", "--anonimizar", "--host", "pbx-matriz", "--host", "pbx-matriz.empresa.com.br"); code != 0 {
		t.Fatalf("filtrar: %d %q", code, r.err.String())
	}
	if got := r.out.String(); strings.Contains(got, "matriz") || strings.Count(got, ".invalid") != 2 {
		t.Errorf("hosts nao trocados: %q", got)
	}
	if code := r.run("", "filtrar", "--help"); code != 0 || !strings.Contains(r.err.String(), "NAO cobre") || !strings.Contains(r.err.String(), "varias linhas") {
		t.Errorf("--help deve avisar o que nao e coberto: %d %q", code, r.err.String())
	}
}
