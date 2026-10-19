// amisim: servidor TCP falso que imita o AMI do Asterisk (13/16/18) para testar o
// painel-ramais. Somente leitura: registra toda action recebida e recusa Command
// e qualquer action de escrita. O endpoint HTTP e exclusivo do simulador.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"painel-ramais/internal/amisim"
)

func main() {
	var (
		mode    = flag.String("mode", "pjsip", "tecnologia: sip ou pjsip")
		listen  = flag.String("listen", "127.0.0.1:5038", "endereco AMI")
		httpAdr = flag.String("http", "127.0.0.1:5039", "endereco HTTP de controle e verdade (vazio desliga)")
		exts    = flag.Int("exts", 10000, "quantidade de ramais")
		first   = flag.Int("first", 2000, "primeiro numero de ramal")
		trunks  = flag.Int("trunks", 4, "quantidade de troncos")
		user    = flag.String("user", "painel-ramais", "usuario AMI")
		secret  = flag.String("secret", "segredo-de-teste", "senha AMI")
		read    = flag.String("read", "system,call,agent", "classes de leitura do usuario AMI")
		write   = flag.String("write", "system,reporting", "classes de escrita do usuario AMI")
		ast     = flag.String("asterisk", "18.20.0", "versao do Asterisk reportada no CoreSettings")
		modules = flag.String("modules", "", "modulos carregados, separados por virgula (padrao: o da tecnologia)")
		noShow  = flag.Bool("no-showcontacts", false, "sem PJSIPShowContacts (forca o fallback PJSIPShowAors)")
		unfilt  = flag.Bool("unfiltered", false, "sem eventfilter: entrega tambem eventos fora da lista (ruido)")
		calls   = flag.Int("calls", 0, "liga carga com N chamadas simultaneas")
		talk    = flag.Duration("talk", 20*time.Second, "duracao media da conversa na carga")
		seed    = flag.Int64("seed", 1, "semente do sorteio")
		replay  = flag.String("replay", "", "reproduz uma captura (CONTRATOS.md secao 18) em vez de simular; ignora as demais opcoes de simulacao")
		speed   = flag.Float64("speed", 1, "velocidade da reproducao (1 = tempo gravado; 0 = sem esperas)")
	)
	flag.Parse()

	if *replay != "" {
		runReplay(*replay, *listen, *httpAdr, *speed)
		return
	}
	cfg := amisim.Config{Mode: *mode, Exts: *exts, FirstExt: *first, Trunks: *trunks, User: *user, Secret: *secret,
		Read: *read, Write: *write, Asterisk: *ast, NoShowContacts: *noShow, Unfiltered: *unfilt, Seed: *seed}
	if *modules != "" {
		cfg.Modules = strings.Split(*modules, ",")
	}
	s, err := amisim.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := s.Listen(*listen); err != nil {
		log.Fatal(err)
	}
	log.Printf("amisim: modo %s, %d ramais, AMI em %s", *mode, *exts, s.Addr())
	if *httpAdr != "" {
		go func() { log.Fatal(http.ListenAndServe(*httpAdr, s.Handler())) }()
		log.Printf("amisim: HTTP de controle em %s (/truth /actions /status /ctl/*)", *httpAdr)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *calls > 0 {
		tm := amisim.DefaultTiming
		tm.TalkMin, tm.TalkMax = *talk/2, *talk*3/2
		s.StartLoad(ctx, *calls, tm)
		log.Printf("amisim: carga de %d chamadas simultaneas", *calls)
	}
	<-ctx.Done()
	counts, viol := s.Actions()
	fmt.Fprintf(os.Stderr, "amisim: encerrando. actions recebidas: %v; fora da allowlist: %d\n", counts, len(viol))
	s.Close()
}

func runReplay(path, listen, httpAdr string, speed float64) {
	r, err := amisim.NewReplayer(path, speed, nil)
	if err != nil {
		log.Fatal(err)
	}
	if err := r.Listen(listen); err != nil {
		log.Fatal(err)
	}
	log.Printf("amisim: reproduzindo %s (%d quadros, velocidade %g) em %s", path, r.Frames, speed, r.Addr())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	select {
	case <-r.Done():
		log.Print("amisim: eventos da captura reproduzidos; conexao segue aberta ate Ctrl-C")
		<-ctx.Done()
	case <-ctx.Done():
	}
	counts, viol := r.Actions()
	fmt.Fprintf(os.Stderr, "amisim: encerrando. actions recebidas: %v; fora da allowlist: %d\n", counts, len(viol))
	r.Close()
}
