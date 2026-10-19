package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/ami/pjsip"
	"painel-ramais/internal/ami/sip"
	"painel-ramais/internal/model"
)

const (
	resyncPoll       = 250 * time.Millisecond
	techRetryWait    = 30 * time.Second
	nameRefreshEvery = 6 * time.Hour // ciclo completo de nomes (decisao 4)

	// Releitura do inventario de ramais (ramal criado, apagado ou renomeado no PBX).
	reloadQuiet             = 3 * time.Second  // silencio depois do ultimo evento de recarga, antes de reler
	reloadMaxWait           = 15 * time.Second // teto: rajada que nao acalma nao adia a releitura para sempre
	rereadTimeout           = 2 * time.Minute  // uma releitura inteira (inclui a confirmacao de lista vazia)
	rereadRetry             = 30 * time.Second // releitura que falhou (sem a conexao cair) tenta de novo
	defaultInventoryRefresh = 10 * time.Minute // usado quando ami.inventory_refresh nao foi informado
)

// reloadEvents lista os eventos AMI que dizem "o Asterisk recarregou (ou terminou de subir)":
// qualquer um deles agenda a releitura dos ramais. Os nomes exatos no Asterisk 18 do Issabel
// ainda serao confirmados na captura da homologacao (isReloadEvent tambem aceita qualquer nome
// que contenha "reload"). Cada um precisa estar no eventfilter do manager_custom.conf.
//   - Reload: modulo recarregado (Apply Config do IssabelPBX, "module reload", "core reload").
//   - ModuleLoadReport: fim do carregamento de modulos.
//   - FullyBooted: Asterisk terminou de subir (tambem chega logo apos o Login; o live ignora o
//     que foi lido antes do snapshot da sessao).
var reloadEvents = []string{"Reload", "ModuleLoadReport", "FullyBooted"}

// asteriskSignal diz se o evento e um sinal do ciclo de vida do Asterisk que abre a janela de
// evento do sistema do relatorio de registro (CONTRATOS 7.2 e 11.1): Shutdown (desligando) ou
// FullyBooted (terminou de subir). "" para qualquer outro evento.
func asteriskSignal(name string) string {
	switch {
	case strings.EqualFold(name, "Shutdown"):
		return model.SignalShutdown
	case strings.EqualFold(name, "FullyBooted"):
		return model.SignalFullyBooted
	}
	return ""
}

func isReloadEvent(name string) bool {
	for _, e := range reloadEvents {
		if strings.EqualFold(name, e) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(name), "reload")
}

// debounce agrupa a rajada de eventos de recarga (um Apply Config gera varios): a releitura vale
// reloadQuiet depois do ULTIMO evento, ou reloadMaxWait depois do PRIMEIRO se a rajada nao acalmar.
type debounce struct{ first, last time.Time }

func (d *debounce) note(now time.Time) {
	if d.first.IsZero() {
		d.first = now
	}
	d.last = now
}

func (d *debounce) due(now time.Time) bool {
	return !d.first.IsZero() && (now.Sub(d.last) >= reloadQuiet || now.Sub(d.first) >= reloadMaxWait)
}

// adapter e a interface comum dos adaptadores sip e pjsip (declarada onde e consumida).
type adapter interface {
	Snapshot(ctx context.Context, c *ami.Client) error
	// Reread rele a lista de ramais (sem refazer as chamadas) e devolve quantos entraram.
	Reread(ctx context.Context, c *ami.Client, reason string) (fresh int, err error)
	Translate(m ami.Message) []model.Event
}

// supervisor mantem a conexao AMI unica: conecta com backoff, detecta a
// tecnologia, sincroniza, traduz eventos ao vivo e avisa quando cai.
type supervisor struct {
	cfg        ami.Config
	technology string // configurado: auto, sip ou pjsip
	fillPerSec int    // teto de consultas de nome por segundo
	pattern    *regexp.Regexp
	log        *slog.Logger
	// refreshEvery: releitura periodica do inventario (ami.inventory_refresh); zero = padrao.
	refreshEvery time.Duration

	mu       sync.Mutex
	status   model.AMIStatus
	lockedTo string // tecnologia detectada na primeira conexao
	cur      *ami.Client
	resyncs  uint64 // vezes que a fila estourou e o estado foi refeito
	// onLogin avisa que o resultado do Login mudou (a verificacao de dependencias reroda ja).
	onLogin func()
}

func newSupervisor(cfg ami.Config, technology string, fillPerSec int, pattern *regexp.Regexp, log *slog.Logger) *supervisor {
	cfg.PingInterval = ami.DefaultPingInterval
	// O cliente avisa em Logf (fila cheia etc.) e recusa action fora da allowlist em OnRefused: a
	// recusa e o controle compensatorio da decisao 1 e sai em Error, visivel com log_level info.
	cfg.Logf = func(f string, a ...any) { log.Warn(fmt.Sprintf(f, a...)) }
	cfg.OnRefused = func(action string) {
		log.Error("action AMI recusada: fora da lista fixa (nada foi enviado ao Asterisk)", "action", action)
	}
	return &supervisor{cfg: cfg, technology: technology, fillPerSec: fillPerSec, pattern: pattern, log: log}
}

// Status implementa model.Collector. Overflows conta ressincronizacoes disparadas
// por fila cheia (CONTRATOS 4.4); a fila e a do leitor AMI, a unica que pode estourar.
func (s *supervisor) Status() model.AMIStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.QueueCap = ami.DefaultQueueSize
	if s.cur != nil {
		st.QueueLen, st.QueueCap = s.cur.QueueLen(), s.cur.QueueCap()
	}
	st.Overflows = s.resyncs
	return st
}

// isExt e a regra unica de ramal (extension_pattern + endpoints internos do Issabel).
func (s *supervisor) isExt(name string) bool { return model.IsExtension(s.pattern, name) }

func (s *supervisor) update(f func(*model.AMIStatus)) {
	s.mu.Lock()
	f(&s.status)
	s.mu.Unlock()
}

// setLogin grava o resultado do Login e so avisa o depcheck quando ele mudou.
func (s *supervisor) setLogin(ok bool, detail string) {
	s.mu.Lock()
	changed := !s.status.LoginKnown || s.status.LoginOK != ok
	s.status.LoginKnown, s.status.LoginOK, s.status.LoginDetail, s.status.LoginAt = true, ok, detail, time.Now()
	s.mu.Unlock()
	if changed && s.onLogin != nil {
		go s.onLogin()
	}
}

// Run implementa model.Collector. Retorna erro so em falha que reconectar nao
// resolve (Asterisk abaixo do minimo); o resto e retentado.
func (s *supervisor) Run(ctx context.Context, events chan<- model.Event) error {
	retry := ami.DefaultBackoff()
	for ctx.Err() == nil {
		c, err := ami.ConnectRetry(ctx, s.cfg, ami.DefaultBackoff(), s.onConnectFail)
		if err != nil {
			return nil // ctx terminou
		}
		s.setLogin(true, "")
		started := time.Now()
		err = s.session(ctx, c, events)
		s.endSession(ctx, c, events, err)
		if time.Since(started) > time.Minute {
			retry.Reset() // sessao estavel: recomeca o backoff
		}
		if errors.Is(err, ami.ErrVersionTooOld) {
			return err
		}
		s.afterSession(ctx, err, retry)
	}
	return nil
}

func (s *supervisor) afterSession(ctx context.Context, err error, retry *ami.Backoff) {
	switch {
	case errors.Is(err, ami.ErrTechnologyChanged):
		s.log.Error("tecnologia mudou depois da reconexao; reinicie o servico (systemctl restart painel-ramais)", "erro", err.Error())
		s.sleep(ctx, techRetryWait)
	case ctx.Err() == nil:
		wait := retry.Next()
		s.log.Warn("sessao AMI terminou", "erro", fmt.Sprint(err), "nova_tentativa_em", wait.String())
		s.sleep(ctx, wait)
	}
}

func (s *supervisor) onConnectFail(err error, wait time.Duration) {
	if errors.Is(err, ami.ErrLoginFailed) {
		s.setLogin(false, err.Error())
	}
	s.log.Warn("AMI indisponivel, nova tentativa", "erro", err.Error(), "espera", wait.String())
}

func (s *supervisor) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

func (s *supervisor) endSession(ctx context.Context, c *ami.Client, events chan<- model.Event, cause error) {
	c.Close()
	s.mu.Lock()
	s.cur = nil
	s.mu.Unlock()
	s.update(func(st *model.AMIStatus) {
		st.Connected, st.ConnectedSince = false, time.Time{}
		st.Reconnects++
	})
	reason := "desconectado"
	if cause != nil {
		reason = cause.Error()
	}
	select { // bloqueia ate o estado consumir: a queda nunca se perde
	case events <- model.AMIDisconnected{At: time.Now(), Reason: reason}:
	case <-ctx.Done():
	}
}

// enricher roda o enriquecimento do SIP (nome e user agent) em segundo plano; um novo
// sync cancela o anterior e recomeca, para o resync nao perder esses campos.
type enricher struct {
	run    func(ctx context.Context)
	cancel context.CancelFunc
}

func (e *enricher) restart(parent context.Context) {
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	if e.run == nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	e.cancel = cancel
	go e.run(ctx)
}

// session roda uma conexao do login ate cair. Tudo que ela dispara (emit, Enrich)
// usa sctx, cancelado ao sair: nada de uma sessao vaza para a seguinte.
func (s *supervisor) session(ctx context.Context, c *ami.Client, events chan<- model.Event) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	version, tech, err := s.detect(sctx, c)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cur = c
	s.mu.Unlock()
	emit := func(ev model.Event) {
		select {
		case events <- ev:
		case <-sctx.Done():
		}
	}
	var ad adapter
	enr := &enricher{}
	if tech == model.TechPJSIP {
		pa := pjsip.New(emit)
		ad = pa
		enr.run = func(ectx context.Context) {
			s.fillNames(ectx, "PJSIP", pa.Enrich(ectx, c, s.fillPerSec, s.isExt))
		}
	} else {
		sa := sip.New(emit)
		ad = sa
		enr.run = func(ectx context.Context) {
			s.fillNames(ectx, "SIP", sa.Enrich(ectx, c, s.fillPerSec, s.isExt))
		}
	}
	since := time.Now() // recarga lida antes disto (ex.: o FullyBooted do Login) ja esta no snapshot
	if err := s.sync(sctx, c, ad, emit, tech, version, model.SyncConnect); err != nil {
		return err
	}
	s.update(func(st *model.AMIStatus) {
		st.Connected, st.Technology, st.AsteriskVersion, st.ConnectedSince = true, tech, version, time.Now()
	})
	s.log.Info("AMI conectado", "asterisk", version, "tecnologia", string(tech))
	enr.restart(sctx)
	return s.live(sctx, c, ad, emit, tech, version, enr, since)
}

// detect confere a versao do Asterisk e a tecnologia (e que ela nao mudou).
func (s *supervisor) detect(ctx context.Context, c *ami.Client) (string, model.Technology, error) {
	version, err := c.CoreVersion(ctx)
	if err != nil {
		return "", "", err
	}
	if err := ami.CheckVersion(version); err != nil {
		return "", "", err
	}
	det, err := c.DetectTechnology(ctx, s.technology, s.isExt)
	if err != nil {
		return "", "", err
	}
	if det.Notice != "" {
		s.log.Info(det.Notice)
	}
	if err := ami.EnsureSameTechnology(s.lockedTo, det.Technology); err != nil {
		return "", "", err
	}
	s.lockedTo = det.Technology
	return version, model.Technology(det.Technology), nil
}

func (s *supervisor) sync(ctx context.Context, c *ami.Client, ad adapter, emit func(model.Event), tech model.Technology, version string, reason model.SyncReason) error {
	emit(model.SyncStarted{At: time.Now(), Technology: tech, AsteriskVersion: version, Reason: reason})
	if err := ad.Snapshot(ctx, c); err != nil {
		return err
	}
	emit(model.SyncFinished{At: time.Now()})
	return nil
}

// live traduz os eventos da fila do cliente ate a conexao cair. Fila que estourou: refaz o estado
// inteiro (e o enriquecimento) sem derrubar a conexao. Tambem mantem o inventario de ramais
// atualizado: evento de recarga (com debounce), ciclo periodico ou nova tentativa de uma releitura
// que falhou disparam s.reread. TUDO roda nesta goroutine, em ordem: a releitura nao se cruza com
// a traducao dos eventos (o que chegar durante ela fica na fila e e traduzido depois, mais novo
// que a lista) nem com o resync por overflow. since: so conta evento de recarga lido depois dele.
func (s *supervisor) live(ctx context.Context, c *ami.Client, ad adapter, emit func(model.Event), tech model.Technology, version string, enr *enricher, since time.Time) error {
	poll := time.NewTicker(resyncPoll)
	defer poll.Stop()
	refresh := time.NewTicker(nameRefreshEvery)
	defer refresh.Stop()
	every := s.refreshEvery
	if every <= 0 {
		every = defaultInventoryRefresh
	}
	inventory := time.NewTicker(every)
	defer inventory.Stop()
	var (
		deb     debounce  // eventos de recarga ainda nao tratados
		retryAt time.Time // releitura que falhou: quando tentar de novo
	)
	for {
		reason := "" // preenchido quando uma releitura deve rodar agora
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m, ok := <-c.Events():
			if !ok {
				return c.Err()
			}
			if sig := asteriskSignal(m.Event()); sig != "" { // inclusive o FullyBooted logo apos o Login
				emit(model.AsteriskSignal{At: m.At, Kind: sig})
			}
			if isReloadEvent(m.Event()) && m.At.After(since) {
				s.log.Debug("evento de recarga do Asterisk; releitura agendada", "evento", m.Event())
				deb.note(time.Now())
			}
			for _, ev := range ad.Translate(m) {
				emit(ev)
			}
		case <-refresh.C:
			enr.restart(ctx) // ciclo completo a cada 6 h
		case <-inventory.C:
			reason = "periodica"
		case <-poll.C:
			now := time.Now()
			resynced, err := s.resyncIfNeeded(ctx, c, ad, emit, tech, version, enr)
			if err != nil {
				return err
			}
			switch {
			case resynced:
				deb, retryAt = debounce{}, time.Time{} // o resync ja releu tudo
			case deb.due(now):
				reason = "reload"
			case !retryAt.IsZero() && !now.Before(retryAt):
				reason = "nova tentativa"
			}
		case <-c.Done():
			// Esvazia o que ainda esta na fila antes de reportar a queda (o Shutdown costuma ser o
			// ultimo evento antes de o socket fechar).
			for m := range c.Events() {
				if sig := asteriskSignal(m.Event()); sig != "" {
					emit(model.AsteriskSignal{At: m.At, Kind: sig})
				}
				for _, ev := range ad.Translate(m) {
					emit(ev)
				}
			}
			return c.Err()
		}
		if reason == "" {
			continue
		}
		deb, retryAt = debounce{}, time.Time{}
		retry, err := s.reread(ctx, c, ad, enr, reason)
		if err != nil {
			return err
		}
		if retry {
			retryAt = time.Now().Add(rereadRetry)
		}
	}
}

// reread refaz o inventario de ramais e os nomes dos que entraram. Devolve err so quando a sessao
// acabou (ctx cancelado ou conexao caida: o Run reconecta); falha isolada vira retry=true e um
// aviso (o estado segue como estava: nada e removido por causa de um erro). O log com as
// contagens (entraram, sairam, ficaram) sai do estado, que e quem conhece a diferenca.
func (s *supervisor) reread(ctx context.Context, c *ami.Client, ad adapter, enr *enricher, reason string) (retry bool, err error) {
	rctx, cancel := context.WithTimeout(ctx, rereadTimeout)
	defer cancel()
	started := time.Now()
	fresh, err := ad.Reread(rctx, c, reason)
	if err == nil {
		s.log.Debug("inventario de ramais relido", "motivo", reason, "novos_endpoints", fresh, "duracao", time.Since(started).String())
		if reason == "reload" || fresh > 0 {
			enr.restart(ctx) // Reload: ciclo completo de nomes (decisao 4); os novos vao primeiro
		}
		return false, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	select {
	case <-c.Done():
		return false, c.Err()
	default:
	}
	s.log.Warn("releitura do inventario de ramais falhou", "motivo", reason, "erro", err.Error(), "nova_tentativa_em", rereadRetry.String())
	return true, nil
}

// resyncIfNeeded refaz o estado inteiro (e os nomes) se a fila do leitor estourou.
func (s *supervisor) resyncIfNeeded(ctx context.Context, c *ami.Client, ad adapter, emit func(model.Event), tech model.Technology, version string, enr *enricher) (resynced bool, err error) {
	if !c.TakeResync() {
		return false, nil
	}
	s.mu.Lock()
	s.resyncs++
	s.mu.Unlock()
	s.log.Warn("fila AMI estourou; refazendo o estado", "descartados", c.Overflows())
	if err := s.sync(ctx, c, ad, emit, tech, version, model.SyncOverflow); err != nil {
		return false, err
	}
	enr.restart(ctx)
	return true, nil
}

// fillNames registra o fim (ou a falha) do preenchimento de nomes em segundo plano.
func (s *supervisor) fillNames(ctx context.Context, tech string, err error) {
	if err != nil && ctx.Err() == nil {
		s.log.Warn("preenchimento de nomes interrompido", "tecnologia", tech, "erro", err.Error())
	}
}

var _ model.Collector = (*supervisor)(nil)
