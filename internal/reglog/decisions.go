package reglog

import (
	"context"
	"sync/atomic"
	"time"

	"painel-ramais/internal/model"
)

// Log das decisoes de contagem de registro (rc.9, ADR-011 decisao 2, CONTRATOS 11.5 "Log de decisoes"). O estado
// entrega model.RegDecision por Enqueue (sem bloqueio, canal proprio); uma goroutine escreve uma linha Info por
// transicao ate DecisionPerSecond por segundo, com rajada de DecisionBurst; o que passa do limite (ou nao cabe no
// canal) so soma um contador por motivo, e a cada DecisionSummaryEvery sai uma linha de resumo por motivo. Nada de
// I/O na goroutine do estado. Sem esquema novo.
const (
	DecisionQueueCap     = 4096
	DecisionPerSecond    = 20
	DecisionBurst        = 100
	DecisionSummaryEvery = 10 * time.Second
)

// decisionReasons sao os seis motivos do contrato (model.Reason*), na ordem do resumo.
var decisionReasons = [...]string{
	model.ReasonCounted, model.ReasonPause, model.ReasonFirstState, model.ReasonReread, model.ReasonReconcile, model.ReasonOutOfTable,
}

func reasonIndex(r string) int {
	for i, x := range decisionReasons {
		if x == r {
			return i
		}
	}
	return -1
}

// Decisions e o gravador do log de decisoes. Crie com NewDecisions; Run bloqueia ate o ctx terminar.
type Decisions struct {
	ch   chan model.RegDecision
	info func(msg string, args ...any)
	now  func() time.Time

	lines  atomic.Uint64
	window [len(decisionReasons)]atomic.Uint64 // so o resumo da janela corrente
	total  [len(decisionReasons)]atomic.Uint64 // desde a subida (decision_summarized do health)
}

// NewDecisions cria o gravador. info recebe as linhas (Info); nil descarta.
func NewDecisions(info func(msg string, args ...any)) *Decisions {
	if info == nil {
		info = func(string, ...any) {}
	}
	return &Decisions{ch: make(chan model.RegDecision, DecisionQueueCap), info: info, now: time.Now}
}

// Enqueue entrega uma decisao ao log SEM bloquear: canal cheio soma no contador do motivo (vai ao resumo).
func (d *Decisions) Enqueue(dec model.RegDecision) {
	select {
	case d.ch <- dec:
	default:
		d.summarize(dec.Reason)
	}
}

func (d *Decisions) summarize(reason string) {
	if i := reasonIndex(reason); i >= 0 {
		d.window[i].Add(1)
		d.total[i].Add(1)
	}
}

// Fill acrescenta ao RegStatus as linhas escritas e o resumido por motivo (copia; so motivos acima de zero).
func (d *Decisions) Fill(rs *model.RegStatus) {
	rs.DecisionLines = d.lines.Load()
	rs.DecisionSummarized = map[string]uint64{}
	for i, r := range decisionReasons {
		if n := d.total[i].Load(); n > 0 {
			rs.DecisionSummarized[r] = n
		}
	}
}

// Run escreve as linhas (balde de fichas: DecisionPerSecond por segundo, rajada DecisionBurst) e o resumo de
// DecisionSummaryEvery em DecisionSummaryEvery, ate o ctx terminar.
func (d *Decisions) Run(ctx context.Context) {
	tokens, last := float64(DecisionBurst), d.now()
	sum := time.NewTicker(DecisionSummaryEvery)
	defer sum.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case dec := <-d.ch:
			now := d.now()
			tokens = min(float64(DecisionBurst), tokens+now.Sub(last).Seconds()*DecisionPerSecond)
			last = now
			if tokens < 1 {
				d.summarize(dec.Reason)
				continue
			}
			tokens--
			d.lines.Add(1)
			counted := "nao"
			if dec.Counted {
				counted = "sim"
			}
			d.info("registro: transicao", "ramal", dec.Number, "de", string(dec.From), "para", string(dec.To),
				"contada", counted, "motivo", dec.Reason)
		case <-sum.C:
			for i, r := range decisionReasons {
				if n := d.window[i].Swap(0); n > 0 {
					d.info("registro: resumo de transicoes sem linha propria", "motivo", r, "quantidade", n,
						"janela_s", int(DecisionSummaryEvery/time.Second))
				}
			}
		}
	}
}
