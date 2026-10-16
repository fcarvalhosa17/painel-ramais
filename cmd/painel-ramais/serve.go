package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/api"
	"painel-ramais/internal/auth"
	"painel-ramais/internal/config"
	"painel-ramais/internal/depcheck"
	"painel-ramais/internal/history"
	"painel-ramais/internal/model"
	"painel-ramais/internal/state"
	"painel-ramais/internal/store"
)

const (
	eventQueueSize  = ami.DefaultQueueSize
	depCheckEvery   = 60 * time.Second
	housekeepEvery  = time.Hour
	shutdownTimeout = 10 * time.Second
	readTimeout     = 15 * time.Second // leitura do request inteiro; o handler do WS limpa o prazo
)

func (c *cli) serve(args []string) int {
	// SIGHUP primeiro de tudo: sem handler, um reload durante a subida mataria o processo.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	fs, cfgPath := c.flagSet("serve")
	if _, err := parseArgs(fs, args); err != nil || fs.NArg() != 0 {
		return c.usage()
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		c.errf("%v", err)
		return exitEnv
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runDaemon(ctx, cfg, hup); err != nil {
		c.errf("%v", err)
		if errors.Is(err, ami.ErrVersionTooOld) {
			return exitVersion // a unit nao deve reiniciar: RestartPreventExitStatus=4
		}
		return exitEnv
	}
	return exitOK
}

func newLogger(w *os.File, level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l}))
}

// daemon reune as pecas montadas por newDaemon.
type daemon struct {
	cfg     *config.Config
	log     *slog.Logger
	st      *store.Store
	state   *state.State
	hub     *api.Hub
	sup     *supervisor
	deps    *depcheck.Checker
	srv     *api.Server
	http    *http.Server
	ln      net.Listener
	events  chan model.Event
	names   chan [2]string // nomes aprendidos ao vivo, a caminho do cache em SQLite
	prune   chan []string  // ramais existentes apos cada sincronizacao (expurgo do cache)
	wg      sync.WaitGroup
	fatal   chan error
	cancel  context.CancelFunc
	runCtx  context.Context
	hupChan <-chan os.Signal
}

// newDaemon valida segredos, abre o banco e liga as pecas (nada roda ainda).
func newDaemon(cfg *config.Config, hup <-chan os.Signal) (*daemon, error) {
	log := newLogger(os.Stdout, cfg.LogLevel)
	amiSecret, err := cfg.AMISecret()
	if err != nil {
		return nil, err
	}
	authSecret, err := cfg.AuthSecret()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(context.Background(), cfg.DataDir)
	if err != nil {
		return nil, err
	}
	d := &daemon{cfg: cfg, log: log, st: st, hupChan: hup, fatal: make(chan error, 8),
		events: make(chan model.Event, eventQueueSize), names: make(chan [2]string, 4096), prune: make(chan []string, 1)}
	d.state = state.New(cfg.Pattern, d.events, state.Options{OnName: d.onName, OnSync: d.onSync, OnNoPeer: d.onNoPeer})
	d.hub = api.NewHub(log, nil)
	d.sup = newSupervisor(ami.Config{Address: cfg.AMI.Address, Username: cfg.AMI.Username, Secret: amiSecret},
		cfg.Technology, cfg.AMI.NameFillPerSecond, cfg.Pattern, log)
	d.deps = &depcheck.Checker{Log: log.Warn, Login: func() depcheck.LoginResult {
		a := d.sup.Status()
		return depcheck.LoginResult{Known: a.LoginKnown, OK: a.LoginOK, Detail: a.LoginDetail, At: a.LoginAt}
	}}
	d.sup.onLogin = func() { d.deps.Check() }
	d.srv = api.New(api.Options{
		Store: st, Verifier: auth.NewVerifier(authSecret), Limiter: auth.NewLimiter(), Hub: d.hub,
		Stats: d.state.Stats, AMI: d.sup.Status, Deps: d.deps.Results,
		SessionTTL: cfg.Auth.SessionTTL, RetentionDays: cfg.History.RetentionDays, Version: version, Log: log,
	})
	d.http = &http.Server{
		Addr: cfg.Listen, Handler: d.srv.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: readTimeout, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 16 << 10,
	}
	return d, nil
}

// run lanca uma goroutine; erro dela (fora do desligamento) derruba o daemon.
func (d *daemon) run(name string, f func() error) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		if err := f(); err != nil && d.runCtx.Err() == nil {
			d.fatal <- fmt.Errorf("%s: %w", name, err)
		}
	}()
}

func (d *daemon) start() {
	ctx := d.runCtx
	d.run("estado", func() error { return d.state.Run(ctx) })
	d.run("hub", func() error { d.hub.Run(ctx, d.state.Out()); return nil })
	d.run("coletor", func() error { return d.sup.Run(ctx, d.events) })
	d.run("http", func() error {
		if err := d.http.Serve(d.ln); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	d.run("depcheck", func() error { d.deps.Run(ctx, depCheckEvery); return nil })
	d.run("historico", func() error { runHistory(ctx, d.st, d.state, d.cfg, d.log); return nil })
	d.run("nomes", func() error { runNameCache(ctx, d.st, d.names, d.prune, d.log); return nil })
	d.run("manutencao", func() error { housekeep(ctx, d.st, d.log); return nil })
	go onSIGHUP(ctx, d.hupChan, d.st, d.events, d.srv, d.log)
	startDebug(ctx, d.log)
}

// stop para de aceitar conexoes, drena as em voo e fecha o resto (os WebSockets
// sao fechados pelo hub com 1001 ao cancelar o contexto).
func (d *daemon) stop() {
	sctx, done := context.WithTimeout(context.Background(), shutdownTimeout)
	defer done()
	if err := d.http.Shutdown(sctx); err != nil {
		d.log.Warn("shutdown http", "erro", err.Error())
	}
	d.cancel()
	ok := make(chan struct{})
	go func() { d.wg.Wait(); close(ok) }()
	select {
	case <-ok:
	case <-sctx.Done():
		d.log.Warn("goroutines nao terminaram no prazo")
	}
}

// runDaemon monta tudo e bloqueia ate ctx terminar (SIGTERM) ou algo fatal acontecer.
func runDaemon(ctx context.Context, cfg *config.Config, hup <-chan os.Signal) error {
	d, err := newDaemon(cfg, hup)
	if err != nil {
		return err
	}
	defer d.st.Close()
	d.runCtx, d.cancel = context.WithCancel(ctx)
	defer d.cancel()
	ln, activated, err := openListener(cfg.Listen, systemActivation())
	if err != nil {
		return err
	}
	d.ln = ln
	d.log.Info("escutando", "endereco", ln.Addr().String(), "socket_activation", activated, "versao", version)
	if err := loadGroups(d.runCtx, d.st, d.events); err != nil {
		ln.Close()
		return err
	}
	loadNames(d.runCtx, d.st, d.events, d.log)
	d.start()
	var cause error
	select {
	case <-ctx.Done():
	case cause = <-d.fatal:
	}
	d.log.Info("parando", "motivo", errString(cause))
	d.stop()
	return cause
}

func errString(err error) string {
	if err == nil {
		return "sinal"
	}
	return err.Error()
}

// loadGroups le as faixas do banco e entrega ao estado (epoch novo).
func loadGroups(ctx context.Context, st *store.Store, events chan<- model.Event) error {
	rows, err := st.GroupRanges(ctx)
	if err != nil {
		return err
	}
	ranges := make([]model.GroupRange, len(rows))
	for i, r := range rows {
		ranges[i] = model.GroupRange{Start: r.Start, End: r.End, Name: r.Name}
	}
	select {
	case events <- model.GroupsLoaded{At: time.Now(), Ranges: ranges}:
	case <-ctx.Done():
	}
	return nil
}

// onSIGHUP: a CLI mudou grupos ou usuarios. Relê os grupos (epoch novo) e fecha
// os WebSockets cuja sessao sumiu. O canal ja existia desde o inicio do processo.
func onSIGHUP(ctx context.Context, hup <-chan os.Signal, st *store.Store, events chan<- model.Event, srv *api.Server, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			if err := loadGroups(ctx, st, events); err != nil {
				log.Error("SIGHUP: ler grupos", "erro", err.Error())
			}
			n := srv.Revalidate(ctx)
			log.Info("SIGHUP: grupos recarregados", "websockets_fechados", n)
		}
	}
}

func runHistory(ctx context.Context, st *store.Store, s *state.State, cfg *config.Config, log *slog.Logger) {
	sm := &history.Sampler{
		Store: st, RetentionDays: cfg.History.RetentionDays, Log: log.Warn,
		Source: func() history.Sample {
			stats := s.Stats()
			return history.Sample{
				Total: stats.Total, Groups: stats.ByGroup,
				Gap: history.IsGap(stats.AMIConnected, stats.LastDisconnect, time.Now().Truncate(time.Minute)),
			}
		},
	}
	sm.Run(ctx)
}

// housekeep expurga as sessoes vencidas a cada hora.
func housekeep(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(housekeepEvery)
	defer t.Stop()
	for {
		if n, err := st.PurgeSessions(ctx, time.Now()); err != nil && ctx.Err() == nil {
			log.Warn("expurgo de sessoes", "erro", err.Error())
		} else if n > 0 {
			log.Info("sessoes vencidas removidas", "n", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// onNoPeer e chamado pela goroutine do estado quando uma chamada Up ficou 1 s sem outro lado resolvido.
func (d *daemon) onNoPeer(uniqueid, linkedid string) {
	d.log.Info("chamada em conversa sem outro lado resolvido", "uniqueid", uniqueid, "linkedid", linkedid)
}

// onName e chamado pela goroutine do estado: nunca bloqueia (o cache e so otimizacao).
func (d *daemon) onName(number, name string) {
	select {
	case d.names <- [2]string{number, name}:
	default:
	}
}

// onSync guarda so a lista mais recente (a anterior, se ainda pendente, e descartada).
func (d *daemon) onSync(numbers []string) {
	select {
	case <-d.prune:
	default:
	}
	select {
	case d.prune <- numbers:
	default:
	}
}

// loadNames entrega o cache de nomes ao estado antes do primeiro snapshot, para os
// nomes aparecerem logo apos um restart. Falha de leitura so e registrada.
func loadNames(ctx context.Context, st *store.Store, events chan<- model.Event, log *slog.Logger) {
	names, err := st.LoadNames(ctx)
	if err != nil {
		log.Warn("cache de nomes ilegivel; seguindo sem ele", "erro", err.Error())
		return
	}
	select {
	case events <- model.NamesLoaded{At: time.Now(), Names: names}:
	case <-ctx.Done():
	}
}

const nameCacheFlushEvery = 5 * time.Second

// runNameCache grava em lote os nomes aprendidos; ao encerrar, grava o que sobrou.
func runNameCache(ctx context.Context, st *store.Store, in <-chan [2]string, prune <-chan []string, log *slog.Logger) {
	pending := map[string]string{}
	flush := func() {
		if len(pending) == 0 {
			return
		}
		// contexto proprio: no desligamento o ctx ja acabou e ainda queremos gravar.
		wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := st.UpsertNames(wctx, pending, time.Now()); err != nil {
			log.Warn("gravar cache de nomes", "erro", err.Error())
		}
		pending = map[string]string{}
	}
	t := time.NewTicker(nameCacheFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case kv := <-in:
			pending[kv[0]] = kv[1]
		case keep := <-prune:
			flush()
			pruneNames(ctx, st, keep, log)
		case <-t.C:
			flush()
		}
	}
}

// pruneNames tira do cache os ramais que nao existem mais no PBX (snapshot completo).
func pruneNames(ctx context.Context, st *store.Store, keep []string, log *slog.Logger) {
	n, err := st.PruneNames(ctx, keep)
	if err != nil && ctx.Err() == nil {
		log.Warn("expurgar cache de nomes", "erro", err.Error())
	} else if n > 0 {
		log.Info("cache de nomes: ramais removidos do PBX", "n", n)
	}
}
