package main

import (
	"context"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/ami/queues"
	"painel-ramais/internal/model"
)

// Leitura das filas (rc.8; rc.9, ADR-011 decisao 3, CONTRATOS 7.2 e 11.6). A QueueStatus, completa ou por fila, roda
// numa goroutine propria, UMA de cada vez; o laco ao vivo nunca a espera: continua tirando eventos da fila do leitor
// AMI, entregando na hora os de registro e de chamada e GUARDANDO os de fila ate o resultado voltar (por canal),
// quando entrega o QueuesListed e, logo depois, os guardados na ordem em que chegaram. Era a leitura bloqueando o laco
// que enchia a fila do leitor e fechava o ciclo estouro, ressincronizacao e releitura.
const (
	queueFullEvery   = 10 * time.Minute // reconciliacao completa periodica
	queueMinGap      = 30 * time.Second // minimo entre duas leituras da MESMA fila (completa ou por fila)
	queueFullTimeout = 30 * time.Second // prazo da QueueStatus completa
	queueOneTimeout  = 10 * time.Second // prazo da QueueStatus de uma fila
	queueHeldCap     = 50000            // teto de eventos de fila guardados durante uma leitura

	fullGapBase   = 30 * time.Second // intervalo minimo entre completas (fim de uma, inicio da proxima)
	fullGapMax    = 10 * time.Minute
	resyncGapBase = 30 * time.Second // intervalo minimo entre ressincronizacoes por estouro
	resyncGapMax  = 5 * time.Minute
	gapGrowWindow = 10 * time.Minute // estouro ate este tempo depois da anterior dobra o intervalo
	gapDecayAfter = 30 * time.Minute // sem estouro por este tempo, o intervalo volta ao base
)

// gapBackoff e um intervalo minimo com espera crescente (ADR-011 decisao 3d): dobra, ate o teto, a cada estouro do
// leitor AMI que acontece ate gapGrowWindow depois da ultima leitura ou ressincronizacao; volta ao base depois de
// gapDecayAfter sem estouro. Quem usa guarda com o mutex do supervisor.
type gapBackoff struct {
	base, max    time.Duration
	cur          time.Duration
	lastOverflow time.Time
}

func newGapBackoff(base, max time.Duration) *gapBackoff {
	return &gapBackoff{base: base, max: max, cur: base}
}

// overflow registra um estouro em now; lastDone = fim da ultima leitura (ou ressincronizacao) desta fila de trabalho.
func (g *gapBackoff) overflow(now, lastDone time.Time) {
	if !lastDone.IsZero() && now.Sub(lastDone) <= gapGrowWindow {
		g.cur = min(g.cur*2, g.max)
	}
	g.lastOverflow = now
}

// current devolve o intervalo minimo vigente em now (com o decaimento aplicado).
func (g *gapBackoff) current(now time.Time) time.Duration {
	if g.cur != g.base && now.Sub(g.lastOverflow) >= gapDecayAfter {
		g.cur = g.base
	}
	return g.cur
}

// queueResult e o que a goroutine da QueueStatus devolve ao laco ao vivo.
type queueResult struct {
	ev     model.QueuesListed
	err    error
	queue  string // "" = completa
	reason string // motivo da completa (log)
	took   time.Duration
}

// queueReader coordena, por sessao AMI, as leituras de fila. So o laco ao vivo o toca (as leituras em si rodam na
// goroutine que ele dispara; o resultado volta pelo canal res).
type queueReader struct {
	s    *supervisor
	c    *ami.Client
	emit func(model.Event)
	ctx  context.Context

	res  chan queueResult
	busy bool
	held []model.Event // eventos de fila retidos durante uma leitura, em ordem

	pendingFull   bool
	pendingReason string
	pending       map[string]struct{} // filas esperando a QueueStatus por fila
	last          map[string]time.Time
	lastFullEnd   time.Time
	unsupportedOK bool // o Info "modulo de filas nao carregado" ja saiu nesta sessao
}

func newQueueReader(ctx context.Context, s *supervisor, c *ami.Client, emit func(model.Event)) *queueReader {
	return &queueReader{s: s, c: c, emit: emit, ctx: ctx, res: make(chan queueResult, 1),
		pending: map[string]struct{}{}, last: map[string]time.Time{}}
}

// requestFull pede uma QueueStatus completa (o motivo vai no log). Pedidos acumulados viram um so; o primeiro
// motivo vale. Roda quando nao ha leitura em curso e o intervalo minimo venceu (pump).
func (q *queueReader) requestFull(reason string) {
	if !q.pendingFull {
		q.pendingFull, q.pendingReason = true, reason
	}
}

// markQueue agenda a QueueStatus da fila (chamador saiu ou ligacao do agente acabou). Nunca a fila "default".
func (q *queueReader) markQueue(name string) {
	if name != "" && !model.IgnoredQueue(name) {
		q.pending[name] = struct{}{}
	}
}

// handle entrega um evento de fila: na hora, ou guardado se ha leitura em curso. Passando do teto, descarta os
// guardados (conta, Warn) e deixa uma completa pendente.
func (q *queueReader) handle(ev model.Event) {
	if !q.busy {
		q.emit(ev)
		return
	}
	q.held = append(q.held, ev)
	if len(q.held) > queueHeldCap {
		n := len(q.held)
		q.held = nil
		q.s.mu.Lock()
		q.s.qhealth.HeldDropped += uint64(n)
		q.s.mu.Unlock()
		q.s.log.Warn("filas: eventos de fila descartados durante a releitura", "quantidade", n)
		q.requestFull("descarte")
	}
}

// flushHeld entrega os eventos guardados, na ordem.
func (q *queueReader) flushHeld() {
	held := q.held
	q.held = nil
	for _, ev := range held {
		q.emit(ev)
	}
}

// pump dispara a proxima leitura, se nenhuma esta em curso: a completa pendente (respeitando o intervalo minimo
// atual) ou, na falta dela, a QueueStatus de uma fila pendente cuja ultima leitura tem queueMinGap ou mais.
func (q *queueReader) pump(now time.Time) {
	if q.busy {
		return
	}
	if q.pendingFull && now.Sub(q.lastFullEnd) >= q.s.fullGapNow(now) {
		reason := q.pendingReason
		q.pendingFull, q.pendingReason = false, ""
		// a completa cobre as marcas ate aqui; as que chegarem durante a leitura ficam para a proxima leitura por fila
		q.last, q.pending = map[string]time.Time{}, map[string]struct{}{}
		q.start("", reason, queueFullTimeout)
		return
	}
	for name := range q.pending {
		last := q.lastFullEnd
		if t := q.last[name]; t.After(last) {
			last = t
		}
		if now.Sub(last) < queueMinGap {
			continue
		}
		delete(q.pending, name)
		q.start(name, "", queueOneTimeout)
		return
	}
}

// start dispara a leitura numa goroutine; o resultado volta por q.res. A QueueStatus nunca roda no laco ao vivo.
func (q *queueReader) start(queue, reason string, timeout time.Duration) {
	q.busy = true
	go func() {
		rctx, cancel := context.WithTimeout(q.ctx, timeout)
		defer cancel()
		started := time.Now()
		ev, err := queues.Status(rctx, q.c, queue)
		select {
		case q.res <- queueResult{ev: ev, err: err, queue: queue, reason: reason, took: time.Since(started)}:
		default: // nunca acontece (uma de cada vez, buffer 1); nao bloqueia se acontecer
		}
	}()
}

// done trata o resultado de uma leitura (no laco ao vivo): entrega o QueuesListed, loga e solta os guardados. Erro:
// conta e avisa, e os guardados saem assim mesmo (o estado continua como estava, nada e apagado por erro).
func (q *queueReader) done(r queueResult) {
	q.busy = false
	now := time.Now()
	if r.err != nil {
		q.s.queueFail(q.ctx, q.c, r.queue, r.err)
		if r.queue == "" {
			q.lastFullEnd = now // erro tambem respeita o intervalo minimo
		}
		q.flushHeld()
		return
	}
	q.emit(r.ev)
	if r.ev.IgnoredDefault {
		q.s.noteDefaultIgnored()
	}
	if r.queue != "" {
		q.last[r.queue] = now
		q.flushHeld()
		return
	}
	q.lastFullEnd = now
	q.s.noteFullRead(r, now)
	if r.ev.Unsupported && !q.unsupportedOK {
		q.unsupportedOK = true
		q.s.log.Info("modulo de filas nao carregado no Asterisk (QueueStatus desconhecida); lista de filas vazia")
	}
	q.flushHeld()
}

// noteFullRead registra uma completa lida com sucesso: contadores do health e o Info de cada leitura.
func (s *supervisor) noteFullRead(r queueResult, now time.Time) {
	filas, membros, aguardando := len(r.ev.Queues), 0, 0
	for _, f := range r.ev.Queues {
		membros += len(f.Members)
		aguardando += len(f.Entries)
	}
	s.mu.Lock()
	s.qhealth.LastFullAt, s.qhealth.Unsupported = now, r.ev.Unsupported
	s.qhealth.FullReads++
	s.qhealth.LastFullMs = r.took.Milliseconds()
	gap := s.fullGap.current(now)
	s.mu.Unlock()
	reason := r.reason
	if reason == "" {
		reason = "periodica"
	}
	s.log.Info("filas: leitura completa", "motivo", reason, "duracao_ms", r.took.Milliseconds(), "filas", filas,
		"membros", membros, "aguardando", aguardando, "proximo_minimo", int(gap.Seconds()))
}

// fullGapNow devolve o intervalo minimo vigente entre completas.
func (s *supervisor) fullGapNow(now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fullGap.current(now)
}

// noteOverflow registra um estouro da fila do leitor AMI nos dois intervalos crescentes (completas e
// ressincronizacoes): cada um dobra se o estouro veio ate 10 min depois da ultima leitura desse tipo.
func (s *supervisor) noteOverflow(now, lastFullEnd time.Time) {
	s.mu.Lock()
	s.fullGap.overflow(now, lastFullEnd)
	s.resyncGap.overflow(now, s.lastResyncEnd)
	s.mu.Unlock()
}

// QueueInfo devolve a foto da leitura de filas para o /api/health. Seguro para qualquer goroutine.
func (s *supervisor) QueueInfo() model.QueueHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.qhealth
	h.FullMinGap = s.fullGap.current(time.Now())
	h.EventsReceived = make(map[string]uint64, len(s.qevents))
	for k, v := range s.qevents {
		h.EventsReceived[k] = v
	}
	return h
}

// queueFail conta e registra uma QueueStatus que falhou. Erro de sessao que acabou (ctx cancelado ou conexao
// caida) segue o caminho normal de reconexao e nao conta. O estado nao e tocado: nada e apagado por erro.
func (s *supervisor) queueFail(ctx context.Context, c *ami.Client, queue string, err error) {
	if ctx.Err() != nil {
		return
	}
	select {
	case <-c.Done():
		return
	default:
	}
	s.mu.Lock()
	s.qhealth.RefreshErrors++
	s.mu.Unlock()
	s.log.Warn("QueueStatus falhou; o estado das filas segue como estava", "fila", queue, "erro", err.Error())
}

// queueEventNames sao os onze eventos de fila e agente da secao 7.2, os unicos contados em queues.events_received.
var queueEventNames = map[string]struct{}{
	"QueueCallerJoin": {}, "QueueCallerLeave": {}, "QueueCallerAbandon": {}, "QueueMemberStatus": {}, "QueueMemberPause": {},
	"QueueMemberAdded": {}, "QueueMemberRemoved": {}, "AgentCalled": {}, "AgentConnect": {}, "AgentComplete": {}, "AgentRingNoAnswer": {},
}

// countQueueEvent conta o evento em queues.events_received ANTES de qualquer descarte do tradutor, exceto o da fila
// "default" (que so gera o Info uma vez por processo). Roda no laco ao vivo.
func (s *supervisor) countQueueEvent(m ami.Message) {
	name := m.Event()
	if _, ok := queueEventNames[name]; !ok {
		return
	}
	if model.IgnoredQueue(queues.QueueOf(m)) {
		s.noteDefaultIgnored()
		return
	}
	s.mu.Lock()
	if s.qevents == nil {
		s.qevents = map[string]uint64{}
	}
	s.qevents[name]++
	s.mu.Unlock()
}

// noteDefaultIgnored registra, uma vez por processo, que a fila "default" do exemplo do Asterisk esta sendo ignorada.
func (s *supervisor) noteDefaultIgnored() {
	s.mu.Lock()
	first := !s.defaultLogged
	s.defaultLogged = true
	s.mu.Unlock()
	if first {
		s.log.Info("fila \"default\" ignorada (exemplo do Asterisk, nao existe no Issabel)")
	}
}
