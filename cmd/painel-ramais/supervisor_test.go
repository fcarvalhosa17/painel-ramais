package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/amisim"
)

func newTestSupervisor() *supervisor {
	return newSupervisor(ami.Config{}, "auto", 20, regexp.MustCompile(`^[0-9]+$`), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condicao nao ocorreu")
}

// O depcheck so reroda quando o resultado do Login muda (nao a cada falha de conexao).
func TestLoginChangedOnlyWhenResultChanges(t *testing.T) {
	s := newTestSupervisor()
	var n atomic.Int32
	s.onLogin = func() { n.Add(1) }

	s.onConnectFail(io.EOF, time.Second) // conexao recusada: nada a ver com login
	s.setLogin(true, "")
	waitFor(t, func() bool { return n.Load() == 1 }) // primeiro resultado conhecido
	s.setLogin(true, "")
	s.setLogin(true, "")
	s.setLogin(false, "Authentication failed") // mudou: falha
	waitFor(t, func() bool { return n.Load() == 2 })
	s.setLogin(false, "Authentication failed")
	s.setLogin(true, "") // recuperacao tambem avisa
	waitFor(t, func() bool { return n.Load() == 3 })
	time.Sleep(50 * time.Millisecond)
	if got := n.Load(); got != 3 {
		t.Errorf("disparos = %d, queria 3", got)
	}
	if st := s.Status(); !st.LoginKnown || !st.LoginOK {
		t.Errorf("status: %+v", st)
	}
}

// Um novo sync cancela o enriquecimento anterior (e o fim da sessao cancela o ultimo).
func TestEnricherRestartCancelsPrevious(t *testing.T) {
	ctxs := make(chan context.Context, 4)
	e := &enricher{run: func(ctx context.Context) { ctxs <- ctx }}
	parent, cancel := context.WithCancel(context.Background())
	e.restart(parent)
	first := <-ctxs
	e.restart(parent)
	second := <-ctxs
	if first.Err() == nil {
		t.Error("o enriquecimento anterior deveria ter sido cancelado")
	}
	if second.Err() != nil {
		t.Error("o novo deveria estar vivo")
	}
	cancel()
	if second.Err() == nil {
		t.Error("fim da sessao cancela o enriquecimento")
	}
	(&enricher{}).restart(parent) // pjsip: sem enriquecimento, nao faz nada
}

// queue.overflows conta ressincronizacoes disparadas por fila cheia (CONTRATOS 4.4).
func TestStatusOverflowsCountsResyncs(t *testing.T) {
	s := newTestSupervisor()
	if st := s.Status(); st.QueueCap != ami.DefaultQueueSize || st.Overflows != 0 {
		t.Errorf("inicial: %+v", st)
	}
	s.mu.Lock()
	s.resyncs = 3
	s.mu.Unlock()
	if st := s.Status(); st.Overflows != 3 {
		t.Errorf("overflows = %d", st.Overflows)
	}
}

// Controle compensatorio da decisao 1: a recusa de action fora da lista sai em nivel
// ERROR (visivel com log_level info), so com o nome da action, e nada chega ao Asterisk.
func TestRefusedActionIsLoggedAtErrorLevel(t *testing.T) {
	sim, err := amisim.New(amisim.Config{Mode: amisim.ModePJSIP, Exts: 3, User: "u", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Listen("127.0.0.1:0"); err != nil {
		t.Skip(err)
	}
	defer sim.Close()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})) // padrao de producao
	s := newSupervisor(ami.Config{Address: sim.Addr(), Username: "u", Secret: "s"}, "auto", 20, regexp.MustCompile(`^[0-9]+$`), log)
	c, err := ami.Connect(context.Background(), s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Do(context.Background(), "Command", map[string]string{"Command": "core stop now", "Secret": "nao-logar"})
	if !errors.Is(err, ami.ErrActionNotAllowed) {
		t.Fatalf("esperava recusa: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "action=Command") {
		t.Errorf("log: %q", out)
	}
	if strings.Contains(out, "core stop") || strings.Contains(out, "nao-logar") {
		t.Errorf("parametros no log: %q", out)
	}
	if counts, viol := sim.Actions(); viol != nil || counts["Command"] != 0 {
		t.Errorf("o simulador recebeu a action: %v %v", counts, viol)
	}
}
