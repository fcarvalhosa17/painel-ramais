// loadtest: teste de carga do painel-ramais contra o simulador de AMI.
//
// O simulador (internal/amisim) e o cliente WebSocket rodam neste processo, com o
// mesmo relogio. O daemon sobe como processo separado (binario), configurado em
// technology: auto, e so e tocado por TCP (AMI), HTTP e WebSocket. Gera
// docs/relatorio-carga.md com os modos sip e pjsip lado a lado.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func main() {
	var (
		daemon   = flag.String("daemon", "", "binario do painel-ramais (padrao: dist/painel-ramais[.exe] ou ./painel-ramais[.exe])")
		modes    = flag.String("modes", "sip,pjsip", "modos a rodar")
		exts     = flag.Int("exts", 10000, "ramais")
		calls    = flag.Int("calls", 400, "chamadas simultaneas")
		burst    = flag.Int("burst", 3000, "ramais desregistrando de uma vez")
		duration = flag.Duration("duration", 60*time.Second, "tempo de regime sob carga (30m para o criterio de memoria, em Linux)")
		talk     = flag.Duration("talk", 20*time.Second, "duracao media da conversa")
		drop     = flag.Duration("drop", 4*time.Second, "tempo com o AMI fora do ar")
		out      = flag.String("out", "docs/relatorio-carga.md", "relatorio markdown")
		ast      = flag.String("asterisk", "18.20.0", "versao do Asterisk simulado")
		fillRate = flag.Int("fill-rate", 20, "name_fill_per_second do daemon")
		fillTO   = flag.Duration("fill-timeout", 15*time.Minute, "tempo maximo esperando o preenchimento de nomes")
		captures = flag.String("captures", "testdata/capturas", "pasta de capturas para o \"com quem fala\" por reproducao (vazio desliga)")
		replaySp = flag.Float64("replay-speed", 20, "velocidade da reproducao das capturas")
		noMatrix = flag.Bool("skip-matrix", false, "pula os cenarios passo a passo")
		heap     = flag.Bool("heap", false, "mede heap Go pos-GC (1/min), perfis pprof inicio/fim e veredito de crescimento; liga o endpoint de debug do daemon (loopback); exige o daemon compilado com -tags debug (o binario de release nao tem o endpoint)")
		warmup   = flag.Duration("warmup", 0, "aquecimento depois do preenchimento de nomes, fora da regressao do heap (10m no CI)")
		_        = flag.Duration("heap-every", time.Minute, "intervalo das amostras de heap (so para teste de fumaca; padrao 1 min)")
	)
	flag.Parse()
	if f := flag.Lookup("heap-every"); f != nil {
		if d, err := time.ParseDuration(f.Value.String()); err == nil && d > 0 {
			heapEvery = d
		}
	}
	o := opts{daemon: *daemon, exts: *exts, calls: *calls, burst: *burst, duration: *duration, talk: *talk, dropFor: *drop, asterisk: *ast, skipMatrix: *noMatrix, fillRate: *fillRate, fillTimeout: *fillTO, capturesDir: *captures, replaySpeed: *replaySp, heap: *heap, warmup: *warmup, outDir: filepath.Dir(*out)}
	for _, m := range strings.Split(*modes, ",") {
		if m = strings.TrimSpace(m); m != "" {
			o.modes = append(o.modes, m)
		}
	}
	if o.burst > o.exts*8/10 {
		log.Fatalf("burst %d grande demais para %d ramais (maximo 80%%)", o.burst, o.exts)
	}
	if o.daemon == "" {
		ext := ""
		if runtime.GOOS == "windows" {
			ext = ".exe"
		}
		for _, p := range []string{"dist/painel-ramais" + ext, "painel-ramais" + ext, "bin/painel-ramais" + ext} {
			if _, err := os.Stat(p); err == nil {
				o.daemon = p
				break
			}
		}
	}
	if o.daemon != "" {
		if _, err := os.Stat(o.daemon); err != nil {
			writeAndExit(*out, o, nil, fmt.Sprintf("binario do daemon nao encontrado em `%s`.", o.daemon))
		}
	} else {
		writeAndExit(*out, o, nil, "o binario do daemon (`cmd/painel-ramais`) ainda nao existe nesta arvore (nenhum `dist/painel-ramais`, `painel-ramais` ou `bin/painel-ramais`). O cliente de teste, o simulador e o gerador deste relatorio estao prontos e compilam; falta o daemon para a execucao ponta a ponta.")
	}
	if b, err := os.ReadFile(o.daemon); err == nil {
		sum := sha256.Sum256(b)
		o.daemonInfo = fmt.Sprintf("%d bytes, sha256 %s", len(b), hex.EncodeToString(sum[:]))
	}
	logf := func(f string, a ...any) { log.Printf(f, a...) }
	var rs []*Result
	bad := false
	for _, m := range o.modes {
		logf("=== modo %s ===", m)
		r := runMode(o, m, logf)
		rs = append(rs, r)
		if failed(r) {
			bad = true
		}
	}
	if o.capturesDir != "" {
		for _, p := range listCaptures(o.capturesDir) {
			logf("=== captura %s ===", p)
			c := runCapture(o, p, logf)
			capResults = append(capResults, c)
			if ok, n := c.okCount(); c.Err != "" || ok != n || len(c.Violations) > 0 {
				bad = true
			}
		}
	}
	if err := writeReport(*out, o, rs, ""); err != nil {
		log.Fatal(err)
	}
	log.Printf("relatorio: %s", *out)
	if bad {
		log.Print("ha criterios com FALHA; veja o relatorio")
		os.Exit(1)
	}
}

// capResults: resultado das capturas reproduzidas (secao propria do relatorio).
var capResults []*CapResult

func writeAndExit(path string, o opts, rs []*Result, why string) {
	if err := writeReport(path, o, rs, why); err != nil {
		log.Fatal(err)
	}
	log.Printf("pendente: %s (relatorio em %s)", why, path)
	os.Exit(3)
}
