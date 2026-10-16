package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"painel-ramais/internal/amisim"
	"painel-ramais/internal/config"
	"painel-ramais/internal/store"
)

// freePort reserva uma porta de loopback livre (a janela ate o uso e minima).
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// daemonConfig monta um config.yaml de teste apontando o AMI para amiAddr.
func daemonConfig(t *testing.T, r *rig, amiAddr string) (*config.Config, string) {
	t.Helper()
	secretAMI := filepath.Join(r.dir, "ami-secret")
	os.WriteFile(secretAMI, []byte("segredo\n"), 0o600)
	listen := freePort(t)
	y := "listen: " + listen + "\ndata_dir: " + filepath.ToSlash(r.dir) + "\nami:\n  address: " + amiAddr +
		"\n  secret_file: " + filepath.ToSlash(secretAMI) + "\nauth:\n  secret_file: " + filepath.ToSlash(filepath.Join(r.dir, "secret")) + "\n"
	os.WriteFile(r.cfg, []byte(y), 0o600)
	cfg, err := config.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	return cfg, listen
}

// O daemon sobe sem Asterisk (AMI recusando), responde no health como degradado
// e termina limpo quando o contexto acaba (o que o SIGTERM faz).
func TestDaemonGracefulShutdownWithoutAMI(t *testing.T) {
	r := newRig(t)
	cfg, listen := daemonConfig(t, r, freePort(t))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runDaemon(ctx, cfg, make(chan os.Signal)) }()

	var body []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get("http://" + listen + "/api/health")
		if err == nil {
			body, _ = io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("sem AMI o health deve ser 503, veio %d", res.StatusCode)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(body) == 0 {
		t.Fatal("daemon nao respondeu")
	}
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("shutdown com erro: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("daemon nao terminou")
	}
	t.Logf("shutdown em %s", time.Since(start))
	if _, err := http.Get("http://" + listen + "/api/health"); err == nil {
		t.Error("porta ainda aceita conexao")
	}
}

// Asterisk abaixo do minimo: o serve recusa iniciar com codigo proprio (4), para a
// unit nao reiniciar em laco (RestartPreventExitStatus=4).
func TestServeExitsWithCode4OnOldAsterisk(t *testing.T) {
	sim, err := amisim.New(amisim.Config{Mode: amisim.ModePJSIP, Exts: 5, User: "painel-ramais", Secret: "segredo", Asterisk: "11.25.0"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Listen("127.0.0.1:0"); err != nil {
		t.Skipf("sem loopback: %v", err)
	}
	defer sim.Close()
	r := newRig(t)
	daemonConfig(t, r, sim.Addr())
	done := make(chan int, 1)
	go func() { done <- r.run("", "serve", "--config", r.cfg) }()
	select {
	case code := <-done:
		if code != exitVersion || !strings.Contains(r.err.String(), "versão mínima") {
			t.Errorf("codigo %d, stderr %q", code, r.err.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve nao encerrou")
	}
}

// SIGHUP (a CLI mudou grupos) recarrega os grupos e gera epoch novo.
func TestSIGHUPReloadsGroups(t *testing.T) {
	r := newRig(t)
	cfg, _ := daemonConfig(t, r, freePort(t))
	hup := make(chan os.Signal, 1)
	d, err := newDaemon(cfg, hup)
	if err != nil {
		t.Fatal(err)
	}
	defer d.st.Close()
	ctx, cancel := context.WithCancel(context.Background())
	d.runCtx, d.cancel = ctx, cancel
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		t.Fatal(err)
	}
	d.ln = ln
	d.start()
	defer d.stop()
	time.Sleep(400 * time.Millisecond)
	before := d.state.Stats().Epoch

	rows, _ := store.ParseGroupsCSV(strings.NewReader("Suporte,2000-2099\n"))
	if err := d.st.ImportGroups(ctx, rows, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	hup <- syscall.SIGHUP
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if d.state.Stats().Epoch != before {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("epoch nao mudou depois do SIGHUP")
}

// A linha de log da protecao de 1 s tem de trazer uniqueid e linkedid (e e a unica saida do hook onNoPeer).
func TestOnNoPeerLogsUniqueidAndLinkedid(t *testing.T) {
	var buf bytes.Buffer
	d := &daemon{log: slog.New(slog.NewTextHandler(&buf, nil))}
	d.onNoPeer("u-1", "l-1")
	out := buf.String()
	t.Log(strings.TrimSpace(out))
	for _, want := range []string{"chamada em conversa sem outro lado resolvido", "uniqueid=u-1", "linkedid=l-1", "level=INFO"} {
		if !strings.Contains(out, want) {
			t.Errorf("log sem %q: %s", want, out)
		}
	}
}
