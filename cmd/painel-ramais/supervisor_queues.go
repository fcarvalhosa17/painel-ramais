package main

import (
	"context"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/ami/queues"
	"painel-ramais/internal/model"
)

// Leitura das filas (rc.8, ADR-008, CONTRATOS 7.2 e 11.6). TUDO roda na goroutine do laco ao vivo (live), a mesma
// que traduz os eventos e rele o inventario: o que chega durante a QueueStatus fica na fila do cliente AMI e e
// aplicado depois da lista (os eventos de fila sao idempotentes no estado).
const (
	queueFullEvery   = 10 * time.Minute // reconciliacao completa periodica
	queueMinGap      = 30 * time.Second // minimo entre duas leituras da mesma fila (completa ou por fila)
	queueFullTimeout = 30 * time.Second // prazo da QueueStatus completa
	queueOneTimeout  = 10 * time.Second // prazo da QueueStatus de uma fila
)

// queueSched guarda, por sessao, as filas que esperam a QueueStatus por fila e quando cada uma foi lida.
type queueSched struct {
	pending           map[string]struct{}
	last              map[string]time.Time
	lastFull          time.Time
	unsupportedLogged bool
}

func newQueueSched() *queueSched {
	return &queueSched{pending: map[string]struct{}{}, last: map[string]time.Time{}}
}

// mark agenda a QueueStatus da fila (chamador saiu ou ligacao do agente acabou).
func (q *queueSched) mark(queue string) {
	if queue != "" {
		q.pending[queue] = struct{}{}
	}
}

// QueueInfo devolve a foto da leitura de filas para o /api/health. Seguro para qualquer goroutine.
func (s *supervisor) QueueInfo() model.QueueHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.qhealth
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

// queuesFull le todas as filas e entrega a lista ao estado (substitui tudo). A completa cobre as pendentes.
func (s *supervisor) queuesFull(ctx context.Context, c *ami.Client, emit func(model.Event), q *queueSched) {
	rctx, cancel := context.WithTimeout(ctx, queueFullTimeout)
	defer cancel()
	ev, err := queues.Status(rctx, c, "")
	if err != nil {
		s.queueFail(ctx, c, "", err)
		return
	}
	now := time.Now()
	q.lastFull, q.last, q.pending = now, map[string]time.Time{}, map[string]struct{}{}
	s.mu.Lock()
	s.qhealth.LastFullAt, s.qhealth.Unsupported = now, ev.Unsupported
	s.mu.Unlock()
	if ev.Unsupported && !q.unsupportedLogged {
		q.unsupportedLogged = true
		s.log.Info("modulo de filas nao carregado no Asterisk (QueueStatus desconhecida); lista de filas vazia")
	}
	emit(ev)
}

// queuesPending le, uma a uma, as filas pendentes cuja ultima leitura (completa ou por fila) tem queueMinGap ou mais.
func (s *supervisor) queuesPending(ctx context.Context, c *ami.Client, emit func(model.Event), q *queueSched, now time.Time) {
	for name := range q.pending {
		last := q.lastFull
		if t := q.last[name]; t.After(last) {
			last = t
		}
		if now.Sub(last) < queueMinGap {
			continue
		}
		delete(q.pending, name)
		rctx, cancel := context.WithTimeout(ctx, queueOneTimeout)
		ev, err := queues.Status(rctx, c, name)
		cancel()
		if err != nil {
			s.queueFail(ctx, c, name, err)
			continue
		}
		q.last[name] = time.Now()
		emit(ev)
	}
}
