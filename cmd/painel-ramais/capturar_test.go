package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/amisim"
	"painel-ramais/internal/capture"
)

func runCaptureAgainstSim(t *testing.T, extra ...string) (string, map[string]any, []capture.Frame) {
	t.Helper()
	sim, err := amisim.New(amisim.Config{Mode: amisim.ModePJSIP, Exts: 30, User: "painel-ramais", Secret: "segredo-do-ami"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Listen("127.0.0.1:0"); err != nil {
		t.Skipf("sem loopback: %v", err)
	}
	defer sim.Close()
	r := newRig(t)
	cfg, _ := daemonConfig(t, r, sim.Addr())
	os.WriteFile(cfg.AMI.SecretFile, []byte("segredo-do-ami\n"), 0o600)
	out := filepath.Join(r.dir, "captura.txt")
	args := append([]string{"capturar", "--minutos", "0.02", "--saida", out, "--config", r.cfg}, extra...)
	if code := r.run("", args...); code != 0 {
		t.Fatalf("capturar: %d %q", code, r.err.String())
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	meta, frames, end, err := capture.Read(f)
	if err != nil {
		t.Fatal(err)
	}
	if end == nil || int(end["quadros"].(float64)) != len(frames) || end["tecnologia"] != "pjsip" || end["encerrado_por"] != "prazo" {
		t.Errorf("#fim: %v (frames=%d)", end, len(frames))
	}
	raw, _ := os.ReadFile(out)
	return string(raw), meta, frames
}

func TestCapturarRecordsOnlyAllowlistedActionsAndMasksSecrets(t *testing.T) {
	raw, meta, frames := runCaptureAgainstSim(t)
	if meta["anonimizado"] != false || meta["versao_daemon"] != version {
		t.Errorf("meta: %v", meta)
	}
	if strings.Contains(raw, "segredo-do-ami") || !strings.Contains(raw, "Secret: ***") {
		t.Error("segredo no arquivo de captura")
	}
	allowed := map[string]bool{}
	for _, a := range ami.AllowedActions() {
		allowed[a] = true
	}
	var sent, recv, snapshots int
	for _, f := range frames {
		switch f.Dir {
		case capture.DirSent:
			sent++
			action := ""
			for _, ln := range bytes.Split(f.Data, []byte("\r\n")) {
				if v, ok := bytes.CutPrefix(ln, []byte("Action: ")); ok {
					action = string(v)
				}
			}
			if !allowed[action] {
				t.Errorf("action fora da allowlist na captura: %q", action)
			}
			if action == "CoreShowChannels" {
				snapshots++
			}
		case capture.DirRecv:
			recv++
		}
	}
	if sent < 6 || recv < 30 || snapshots != 2 { // snapshot inicial e final
		t.Errorf("sent=%d recv=%d CoreShowChannels=%d", sent, recv, snapshots)
	}
	if !bytes.HasPrefix(frames[0].Data, []byte("Asterisk Call Manager")) {
		t.Errorf("o primeiro quadro e o banner: %q", frames[0].Data)
	}
}

func TestCapturarAnonymizesAndRefusesToOverwrite(t *testing.T) {
	raw, meta, _ := runCaptureAgainstSim(t, "--anonimizar")
	if meta["anonimizado"] != true {
		t.Errorf("meta: %v", meta)
	}
	if strings.Contains(raw, "10.1.1.") || !strings.Contains(raw, "10.200.0.") {
		t.Error("IPs do simulador (10.1.1.x) deveriam virar 10.200.x.y")
	}
	r := newRig(t)
	out := filepath.Join(r.dir, "ja-existe.txt")
	os.WriteFile(out, []byte("x"), 0o600)
	daemonConfig(t, r, freePort(t))
	if code := r.run("", "capturar", "--minutos", "1", "--saida", out, "--config", r.cfg); code != exitEnv {
		t.Errorf("nao pode sobrescrever: %d", code)
	}
	if code := r.run("", "capturar", "--minutos", "0", "--saida", out, "--config", r.cfg); code != exitUsage {
		t.Errorf("minutos 0: %d", code)
	}
	if code := r.run("", "capturar", "--minutos", "1", "--config", r.cfg); code != exitUsage {
		t.Errorf("sem --saida: %d", code)
	}
}

// --saida - escreve a captura no stdout (o instalar.py homologar le pelo pipe) e o resumo no stderr.
func TestCapturarSaidaStdout(t *testing.T) {
	sim, err := amisim.New(amisim.Config{Mode: amisim.ModePJSIP, Exts: 10, User: "painel-ramais", Secret: "segredo-do-ami"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Listen("127.0.0.1:0"); err != nil {
		t.Skipf("sem loopback: %v", err)
	}
	defer sim.Close()
	r := newRig(t)
	cfg, _ := daemonConfig(t, r, sim.Addr())
	os.WriteFile(cfg.AMI.SecretFile, []byte("segredo-do-ami\n"), 0o600)
	if code := r.run("", "capturar", "--minutos", "0.02", "--saida", "-", "--anonimizar", "--config", r.cfg); code != 0 {
		t.Fatalf("capturar: %d %q", code, r.err.String())
	}
	_, frames, end, err := capture.Read(strings.NewReader(r.out.String()))
	if err != nil || end == nil || len(frames) < 30 {
		t.Fatalf("stdout nao e uma captura valida: err=%v end=%v frames=%d", err, end, len(frames))
	}
	if strings.Contains(r.out.String(), "segredo-do-ami") || !strings.Contains(r.err.String(), "quadros gravados") {
		t.Errorf("segredo no stdout ou resumo fora do stderr: %q", r.err.String())
	}
}
