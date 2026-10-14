package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
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
	nameRefreshEvery = 6 * time.Hour   // ciclo completo de nomes (decisao 4)
	reloadQuiet      = 5 * time.Second // espera o Reload do Asterisk acalmar antes de refazer os nomes
)

// adapter e a interface comum dos adaptadores sip e pjsip (declarada onde e consumida).
type adapter interface {
	Snapshot(ctx context.Context, c *ami.Client) error
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
			s.fillNames(ectx, "PJSIP", pa.Enrich(ectx, c, s.fillPerSec, s.pattern.MatchString))
		}
	} else {
		sa := sip.New(emit)
		ad = sa
		enr.run = func(ectx context.Context) {
			s.fillNames(ectx, "SIP", sa.Enrich(ectx, c, s.fillPerSec, s.pattern.MatchString))
		}
	}
	if err := s.sync(sctx, c, ad, emit, tech, version, model.SyncConnect); err != nil {
		return err
	}
	s.update(func(st *model.AMIStatus) {
		st.Connected, st.Technology, st.AsteriskVersion, st.ConnectedSince = true, tech, version, time.Now()
	})
	s.log.Info("AMI conectado", "asterisk", version, "tecnologia", string(tech))
	enr.restart(sctx)
	return s.live(sctx, c, ad, emit, tech, version, enr)
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
	det, err := c.DetectTechnology(ctx, s.technology, s.pattern)
	if err != nil {
		return "", "", err
	}
	if det.Warning != "" {
		s.log.Warn(det.Warning)
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

// live traduz os eventos da fila do cliente ate a conexao cair. Fila que
// estourou: refaz o estado inteiro (e o enriquecimento) sem derrubar a conexao.
func (s *supervisor) live(ctx context.Context, c *ami.Client, ad adapter, emit func(model.Event), tech model.Technology, version string, enr *enricher) error {
	poll := time.NewTicker(resyncPoll)
	defer poll.Stop()
	refresh := time.NewTicker(nameRefreshEvery)
	defer refresh.Stop()
	var reloadAt time.Time // ultimo evento Reload do Asterisk ainda nao tratado
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m, ok := <-c.Events():
			if !ok {
				return c.Err()
			}
			if m.Event() == "Reload" {
				reloadAt = time.Now()
			}
			for _, ev := range ad.Translate(m) {
				emit(ev)
			}
		case <-refresh.C:
			enr.restart(ctx) // ciclo completo a cada 6 h
		case <-poll.C:
			if !reloadAt.IsZero() && time.Since(reloadAt) >= reloadQuiet {
				reloadAt = time.Time{} // um reload dispara varios eventos: espera acalmar
				s.log.Info("Reload do Asterisk: refazendo os nomes dos ramais")
				enr.restart(ctx)
			}
			if err := s.resyncIfNeeded(ctx, c, ad, emit, tech, version, enr); err != nil {
				return err
			}
		case <-c.Done():
			// Esvazia o que ainda esta na fila antes de reportar a queda.
			for m := range c.Events() {
				for _, ev := range ad.Translate(m) {
					emit(ev)
				}
			}
			return c.Err()
		}
	}
}

// resyncIfNeeded refaz o estado inteiro (e os nomes) se a fila do leitor estourou.
func (s *supervisor) resyncIfNeeded(ctx context.Context, c *ami.Client, ad adapter, emit func(model.Event), tech model.Technology, version string, enr *enricher) error {
	if !c.TakeResync() {
		return nil
	}
	s.mu.Lock()
	s.resyncs++
	s.mu.Unlock()
	s.log.Warn("fila AMI estourou; refazendo o estado", "descartados", c.Overflows())
	if err := s.sync(ctx, c, ad, emit, tech, version, model.SyncOverflow); err != nil {
		return err
	}
	enr.restart(ctx)
	return nil
}

// fillNames registra o fim (ou a falha) do preenchimento de nomes em segundo plano.
func (s *supervisor) fillNames(ctx context.Context, tech string, err error) {
	if err != nil && ctx.Err() == nil {
		s.log.Warn("preenchimento de nomes interrompido", "tecnologia", tech, "erro", err.Error())
	}
}

var _ model.Collector = (*supervisor)(nil)
