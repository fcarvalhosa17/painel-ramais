package main

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	"painel-ramais/internal/reglog"
	"painel-ramais/internal/state"
	"painel-ramais/internal/store"
	"painel-ramais/internal/web"
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
	reg     *reglog.Writer    // gravador do relatorio de registro (fila propria, fora da goroutine do estado)
	dec     *reglog.Decisions // log das decisoes de contagem (rc.9, canal proprio, fora da goroutine do estado)
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
	if rep := st.MigrationReport(); rep.NoGroupDropped > 0 {
		log.Warn("vinculos \"Sem grupo\" manual descartados na migracao (o ramal volta a seguir as faixas)",
			"quantidade", rep.NoGroupDropped, "ramais", strings.Join(rep.NoGroupSample, ","))
	}
	d := &daemon{cfg: cfg, log: log, st: st, hupChan: hup, fatal: make(chan error, 8),
		events: make(chan model.Event, eventQueueSize), names: make(chan [2]string, 4096), prune: make(chan []string, 1)}
	d.reg = reglog.New(st, log.Warn, log.Info)
	d.dec = reglog.NewDecisions(log.Info)
	d.state = state.New(cfg.Pattern, d.events, state.Options{OnName: d.onName, OnSync: d.onSync, OnNoPeer: d.onNoPeer,
		OnInventory: d.onInventory, OnReg: d.reg.Enqueue, OnLocalNoPeer: d.onLocalNoPeer,
		OnDecision: d.dec.Enqueue, OnInfo: log.Info}) // os Enqueue nunca bloqueiam: fila cheia descarta e conta
	d.hub = api.NewHub(log, nil)
	d.sup = newSupervisor(ami.Config{Address: cfg.AMI.Address, Username: cfg.AMI.Username, Secret: amiSecret},
		cfg.Technology, cfg.AMI.NameFillPerSecond, cfg.Pattern, log)
	d.sup.refreshEvery = cfg.AMI.InventoryRefresh
	verifier := auth.NewVerifier(authSecret)
	// A verificacao de dependencias e a fonte unica da chave do Issabel: o que o /api/health
	// mostra e exatamente o que o verificador de tokens usa (ADR-004 item 2).
	d.deps = &depcheck.Checker{
		Log: log.Warn, IssabelKeyFile: cfg.Auth.IssabelKeyFile, OnIssabelKey: verifier.SetIssabelKey,
		IssabelKey: func() ([]byte, error) { return issabelKey(cfg, authSecret) },
		Login: func() depcheck.LoginResult {
			a := d.sup.Status()
			return depcheck.LoginResult{Known: a.LoginKnown, OK: a.LoginOK, Detail: a.LoginDetail, At: a.LoginAt}
		},
	}
	d.sup.onLogin = func() { d.deps.Check() }
	d.deps.Check() // chave do Issabel carregada antes de aceitar o primeiro token
	d.srv = api.New(api.Options{
		Store: st, Verifier: verifier, Limiter: nameLimiter(), Hub: d.hub,
		Stats: d.state.Stats, AMI: d.sup.Status, Deps: d.deps.Results, RegStatus: d.regStatus, QueueInfo: d.sup.QueueInfo,
		ReloadQueueNames: func(ctx context.Context) error { return reloadQueueNamesAfterRequest(ctx, st, d.events) },
		SessionTTL:       cfg.Auth.SessionTTL, RetentionDays: cfg.History.RetentionDays, Version: version, Log: log,
		Static:       web.Static(),
		ReloadGroups: func(ctx context.Context) error { return reloadAfterRequest(ctx, st, d.events) },
		StillAdmin:   api.IssabelACL(api.DefaultIssabelACLDB),
	})
	d.http = &http.Server{
		Addr: cfg.Listen, Handler: d.srv.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: readTimeout, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 16 << 10,
	}
	return d, nil
}

// nameLimiter e o limite de login por nome de usuario: 20 falhas em 5 min, acima do limite de
// 5 por IP do cliente, para que quem erra de um IP nao trave a senha certa de outro.
func nameLimiter() *auth.Limiter {
	l := auth.NewLimiter()
	l.Max = 20
	return l
}

// keyRule marca o erro da chave do Issabel como regra violada para o depcheck, sem mudar a mensagem.
type keyRule struct{ error }

func (e keyRule) Unwrap() error      { return e.error }
func (keyRule) Is(target error) bool { return target == depcheck.ErrKeyInvalid }

// issabelKey le a chave do Issabel e traduz a recusa de regra para a linguagem do depcheck.
// Chave igual ao segredo do daemon e recusada: quem lesse o segredo (token livre da CLI)
// emitiria tokens de administrador do Issabel, e a separacao das duas chaves perderia o sentido.
func issabelKey(cfg *config.Config, authSecret []byte) ([]byte, error) {
	k, err := cfg.IssabelKey()
	if err == nil && hmac.Equal(k, authSecret) {
		k, err = nil, fmt.Errorf("auth.issabel_key_file: %w: igual ao auth.secret_file; gere uma chave diferente", config.ErrKeyInvalid)
	}
	if errors.Is(err, config.ErrKeyInvalid) {
		err = keyRule{err}
	}
	return k, err
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
	d.run("registros", func() error { d.reg.Run(ctx); return nil })
	d.run("decisoes", func() error { d.dec.Run(ctx); return nil })
	d.run("historico", func() error { runHistory(ctx, d.st, d.state, d.cfg, d.log); return nil })
	d.run("nomes", func() error { runNameCache(ctx, d.st, d.names, d.prune, d.log); return nil })
	d.run("manutencao", func() error { housekeep(ctx, d.st, d.log, d.cfg.History.RetentionDays); return nil })
	go onSIGHUP(ctx, d.hupChan, d.st, d.events, d.srv, d.log)
	startDebug(ctx, d.log)
	startMetrics(ctx, d.log)
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
	warnReservedGroup(d.runCtx, d.st, d.log)
	if err := loadQueueNames(d.runCtx, d.st, d.events); err != nil {
		ln.Close()
		return err
	}
	loadRegState(d.runCtx, d.st, d.events, d.log)
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

// loadGroupsMu serializa leitura do banco + envio ao estado: sem ele, a recarga do import
// web e a do SIGHUP podem se cruzar (A le, B grava e le e envia, A envia o velho por cima)
// e o estado fica com os grupos antigos ate a proxima recarga.
var loadGroupsMu sync.Mutex

// groupsReadHook roda entre a leitura e o envio (so os testes o definem, para abrir a janela da corrida).
var groupsReadHook func()

// loadGroups le o catalogo de grupos e os vinculos manuais do banco e entrega ao estado, que
// publica o que mudou por delta (o epoch nao muda). Devolve ctx.Err() se o contexto acabar antes
// da entrega: quem chama nao pode achar que o estado recebeu.
func loadGroups(ctx context.Context, st *store.Store, events chan<- model.Event) error {
	loadGroupsMu.Lock()
	defer loadGroupsMu.Unlock()
	data, err := st.LoadGroupData(ctx)
	if err != nil {
		return err
	}
	groups := make([]model.Group, len(data.Groups))
	for i, g := range data.Groups {
		ranges := make([]model.Range, len(g.Ranges))
		for j, r := range g.Ranges {
			ranges[j] = model.Range{Start: r.Start, End: r.End}
		}
		groups[i] = model.Group{Name: g.Name, Ranges: ranges}
	}
	if groupsReadHook != nil {
		groupsReadHook()
	}
	select {
	case events <- model.GroupsLoaded{At: time.Now(), Groups: groups, Manual: data.Manual}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// loadQueueNames le queue_names do banco e entrega ao estado (QueueNamesLoaded: sem epoch novo, a fila renomeada
// vai no proximo delta). Devolve ctx.Err() se o contexto acabar antes da entrega.
func loadQueueNames(ctx context.Context, st *store.Store, events chan<- model.Event) error {
	names, err := st.LoadQueueNames(ctx)
	if err != nil {
		return err
	}
	select {
	case events <- model.QueueNamesLoaded{At: time.Now(), Names: names}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// regStatus junta o estado do gravador do relatorio com o do log de decisoes (RegStatus do /api/health).
func (d *daemon) regStatus() model.RegStatus {
	rs := d.reg.Status()
	d.dec.Fill(&rs)
	return rs
}

// loadRegState le reg_state (estado "fora do ar" e ultimo registro, ADR-010) e o ultimo minuto de history com
// group_name vazio e gap = 0 (o batimento do daemon com o AMI no ar) e entrega ao estado, antes de d.start(): a primeira SyncFinished reconcilia.
// Falha de leitura: Warn e segue com Fresh = false e nenhuma linha (um erro nunca vira "before_install").
func loadRegState(ctx context.Context, st *store.Store, events chan<- model.Event, log *slog.Logger) {
	ev := model.RegStateLoaded{At: time.Now(), Rows: map[string]model.RegState{}}
	rows, err := st.LoadRegState(ctx)
	if err != nil {
		log.Warn("estado de registro ilegivel; seguindo sem ele (nenhum ramal vira antes da instalacao por causa do erro)", "erro", err.Error())
	} else {
		ev.Fresh = len(rows) == 0
		for _, r := range rows {
			ev.Rows[r.Number] = model.RegState{DownSince: r.DownSince, DownSeg: r.DownSeg, LastReg: r.LastReg, DownKind: r.DownKind, LastRegExact: r.LastRegExact}
		}
	}
	if alive, err := st.LastAliveMinute(ctx); err != nil {
		log.Warn("ultimo minuto do historico ilegivel; o trecho fora do ar recomeca como lacuna sem somar tempo", "erro", err.Error())
	} else if !alive.IsZero() {
		ev.LastAlive = alive // a linha do minuto M e gravada em M: M e o ultimo instante provado (sem somar 60 s)
	}
	select {
	case events <- ev:
	case <-ctx.Done():
	}
}

// reloadQueueNamesAfterRequest e o ReloadQueueNames da API: o nome ja foi gravado, entao a recarga nao morre com o
// cancelamento do pedido; limitada a 10 s.
func reloadQueueNamesAfterRequest(ctx context.Context, st *store.Store, events chan<- model.Event) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return loadQueueNames(ctx, st, events)
}

// warnReservedGroup avisa, uma vez na subida, se o catalogo tem o grupo "Todos os ramais" (nome reservado da tela
// desde a rc.8; grupo antigo continua valendo ate ser renomeado pela tela).
func warnReservedGroup(ctx context.Context, st *store.Store, log *slog.Logger) {
	groups, err := st.ListGroups(ctx)
	if err != nil {
		log.Warn("ler visões para conferir nome reservado", "erro", err.Error())
		return
	}
	for _, g := range groups {
		if strings.EqualFold(g.Name, store.AllGroupsName) {
			log.Warn("visão com nome reservado \"Todos os ramais\"; renomeie pela tela", "grupo", g.Name)
		}
	}
}

// reloadAfterRequest e o ReloadGroups da API: o pedido HTTP ja gravou os grupos, entao a
// recarga nao pode morrer com o cancelamento do pedido (cliente que fechou a aba); fica
// limitada a 10 s para nao pendurar se o estado parou.
func reloadAfterRequest(ctx context.Context, st *store.Store, events chan<- model.Event) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return loadGroups(ctx, st, events)
}

// onSIGHUP: a CLI mudou grupos ou usuarios. Relê os grupos (delta, sem epoch novo) e fecha
// os WebSockets cuja sessao sumiu. O canal ja existia desde o inicio do processo.
func onSIGHUP(ctx context.Context, hup <-chan os.Signal, st *store.Store, events chan<- model.Event, srv *api.Server, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			if err := loadGroups(ctx, st, events); err != nil && ctx.Err() == nil {
				log.Error("SIGHUP: ler visões", "erro", err.Error())
			}
			n := srv.Revalidate(ctx)
			log.Info("SIGHUP: visões recarregadas", "websockets_fechados", n)
		}
	}
}

func runHistory(ctx context.Context, st *store.Store, s *state.State, cfg *config.Config, log *slog.Logger) {
	sm := &history.Sampler{
		Store: st, RetentionDays: cfg.History.RetentionDays, Log: log.Warn,
		Source: func() history.Sample {
			stats := s.Stats()
			return history.Sample{
				Total: stats.Total, Groups: stats.ByGroup, RegTotal: stats.RegTotal, RegByGroup: stats.RegByGroup,
				Gap: history.IsGap(stats.AMIConnected, stats.LastDisconnect, time.Now().Truncate(time.Minute)),
			}
		},
	}
	sm.Run(ctx)
}

// housekeep expurga as sessoes vencidas e a auditoria de grupos fora da retencao do historico, a cada hora.
func housekeep(ctx context.Context, st *store.Store, log *slog.Logger, retentionDays int) {
	t := time.NewTicker(housekeepEvery)
	defer t.Stop()
	for {
		if n, err := st.PurgeSessions(ctx, time.Now()); err != nil && ctx.Err() == nil {
			log.Warn("expurgo de sessoes", "erro", err.Error())
		} else if n > 0 {
			log.Info("sessoes vencidas removidas", "n", n)
		}
		cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
		if n, err := st.PurgeGroupAudit(ctx, cutoff); err != nil && ctx.Err() == nil {
			log.Warn("expurgo da auditoria de visões", "erro", err.Error())
		} else if n > 0 {
			log.Info("auditoria de visões antiga removida", "n", n)
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

// onLocalNoPeer e chamado pela goroutine do estado, uma vez por chamada com canal Local que segue 1 s sem o outro lado.
func (d *daemon) onLocalNoPeer(uniqueid, linkedid, channel string) {
	d.log.Info("chamada com Local sem par", "uniqueid", uniqueid, "linkedid", linkedid, "canal", channel)
}

// onInventory e chamado pela goroutine do estado a cada releitura completa dos ramais do PBX.
func (d *daemon) onInventory(reason string, added, removed, kept, pending int) {
	d.log.Info("inventario de ramais relido", "motivo", reason, "entraram", added, "sairam", removed, "ficaram", kept, "aguardando_fim_de_chamada", pending)
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
