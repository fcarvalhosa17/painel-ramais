package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/ami/pjsip"
	"painel-ramais/internal/ami/sip"
	"painel-ramais/internal/capture"
	"painel-ramais/internal/config"
	"painel-ramais/internal/model"
)

const (
	maxCaptureMinutes = 720
	captureNameSample = 30 // endpoints consultados por nome, so para ter amostra de PJSIPShowEndpoint/SIPshowpeer
	captureFinalSnap  = 2 * time.Minute
)

// capturar grava frames brutos do AMI real (somente leitura, so a allowlist) para a
// homologacao: o simulador reproduz o arquivo e os testes de "com quem fala" rodam sobre ele.
func (c *cli) capturar(args []string) int {
	fs, cfgPath := c.flagSet("capturar")
	minutes := fs.Float64("minutos", 0, "duracao da captura ao vivo, em minutos (obrigatorio)")
	out := fs.String("saida", "", "arquivo de saida (obrigatorio; nao pode existir; - = stdout)")
	anon := fs.Bool("anonimizar", false, "troca numeros externos, nomes e IPs por valores ficticios (LGPD)")
	digits := fs.Int("digitos-externos", 7, "tamanho minimo de um numero externo ao anonimizar")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 || *minutes <= 0 || *minutes > maxCaptureMinutes || *out == "" || *digits < 3 {
		return c.usage()
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		c.errf("%v", err)
		return exitEnv
	}
	secret, err := cfg.AMISecret()
	if err != nil {
		c.errf("%v", err)
		return exitEnv
	}
	var dst io.Writer = c.stdout // --saida - : o chamador (instalar.py homologar, como root) recebe pelo pipe, sem ler arquivo do usuario do servico
	summary := c.stdout
	if *out == "-" {
		summary = c.stderr
	} else {
		f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // tem dados de chamadas
		if err != nil {
			c.errf("arquivo de saida: %v", err)
			return exitEnv
		}
		defer f.Close()
		dst = f
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cw, err := capture.NewWriter(dst, capture.NewSanitizer(capture.SanitizeOptions{Anonymize: *anon, ExternalDigits: *digits}), map[string]any{
		"versao_daemon": version, "inicio": time.Now().UTC().Format(time.RFC3339), "minutos": *minutes,
		"anonimizado": *anon, "digitos_externos": *digits,
	})
	if err != nil {
		c.errf("%v", err)
		return exitEnv
	}
	info, err := runCapture(ctx, cfg, secret, cw, time.Duration(*minutes*float64(time.Minute)))
	cw.Close(info)
	if err != nil {
		c.errf("captura: %v", err)
		return exitEnv
	}
	fmt.Fprintf(summary, "%d quadros gravados em %s (revise o arquivo antes de compartilhar)\n", cw.Frames(), *out)
	return exitOK
}

// runCapture conecta (so a allowlist), faz o snapshot, grava o trafego ao vivo por live
// e termina com um snapshot final (estado com as chamadas do roteiro). info vai no #fim.
func runCapture(ctx context.Context, cfg *config.Config, secret string, cw *capture.Writer, live time.Duration) (map[string]any, error) {
	info := map[string]any{}
	var tapErr atomic.Value // erro de gravacao vindo das goroutines do Tap
	dial := func(ctx context.Context, addr string) (net.Conn, error) {
		d := net.Dialer{Timeout: 5 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		return capture.Tap(conn, cw, func(e error) { tapErr.Store(e) }), nil
	}
	cl, err := ami.Connect(ctx, ami.Config{Address: cfg.AMI.Address, Username: cfg.AMI.Username, Secret: secret,
		Dial: dial, PingInterval: ami.DefaultPingInterval})
	if err != nil {
		return info, err
	}
	defer cl.Close()
	ad, enrich, err := captureAdapter(ctx, cfg, cl, info)
	if err != nil {
		return info, err
	}
	if err := ad.Snapshot(ctx, cl); err != nil {
		return info, err
	}
	enrich(ctx)
	info["encerrado_por"] = waitLive(ctx, cl, live)
	sctx, cancel := context.WithTimeout(context.Background(), captureFinalSnap) // vale mesmo apos Ctrl-C
	defer cancel()
	if err := ad.Snapshot(sctx, cl); err != nil && cl.Err() == nil {
		return info, err
	}
	if e, _ := tapErr.Load().(error); e != nil {
		return info, fmt.Errorf("gravar captura: %w", e)
	}
	return info, nil
}

// captureAdapter detecta versao e tecnologia e devolve o adaptador e uma amostra de nomes.
func captureAdapter(ctx context.Context, cfg *config.Config, cl *ami.Client, info map[string]any) (adapter, func(context.Context), error) {
	version, err := cl.CoreVersion(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := ami.CheckVersion(version); err != nil {
		return nil, nil, err
	}
	det, err := cl.DetectTechnology(ctx, cfg.Technology, cfg.Pattern)
	if err != nil {
		return nil, nil, err
	}
	info["asterisk"], info["tecnologia"] = version, det.Technology
	discard := func(model.Event) {}
	sample := func() func(string) bool { // so uma amostra de endpoints, nao o plantel inteiro
		n := 0
		return func(name string) bool {
			if n >= captureNameSample || !cfg.Pattern.MatchString(name) {
				return false
			}
			n++
			return true
		}
	}
	if det.Technology == string(model.TechPJSIP) {
		pa := pjsip.New(discard)
		return pa, func(ctx context.Context) { pa.Enrich(ctx, cl, cfg.AMI.NameFillPerSecond, sample()) }, nil
	}
	sa := sip.New(discard)
	return sa, func(ctx context.Context) { sa.Enrich(ctx, cl, cfg.AMI.NameFillPerSecond, sample()) }, nil
}

// waitLive descarta os eventos (o Tap ja os gravou) ate o prazo, Ctrl-C ou queda do AMI.
func waitLive(ctx context.Context, cl *ami.Client, d time.Duration) string {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return "interrupcao"
		case <-timer.C:
			return "prazo"
		case <-cl.Done():
			return "conexao: " + fmt.Sprint(cl.Err())
		case _, ok := <-cl.Events():
			if !ok {
				return "conexao"
			}
		}
	}
}
