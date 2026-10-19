// Package reglog e o gravador do relatorio de registro (ADR-007, CONTRATOS 11.5): recebe as
// ocorrencias que o estado produz, junta e grava em lote fora do caminho do AMI. A goroutine do estado so chama
// Enqueue, que nunca bloqueia; fila cheia descarta e conta. rc.8: o lote e dividido em sublotes por custo, cada
// um numa transacao propria, repetido uma vez se falhar; o que se perde nunca e silencioso (contador, periodo,
// log Warn e /api/health).
package reglog

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"painel-ramais/internal/model"
	"painel-ramais/internal/store"
)

// Constantes do contrato (nao configuraveis, de proposito).
const (
	QueueCap       = 16384           // fila entre o estado e o gravador
	BatchSize      = 500             // grava ao juntar este tanto...
	BatchEvery     = 2 * time.Second // ...ou a cada tanto, o que vier antes
	SubBatchCost   = 5000            // teto de comandos SQL de um sublote (CONTRATOS 10, "Gravacao de um lote, rc.8")
	Retention      = 30 * 24 * time.Hour
	PurgeEvery     = 24 * time.Hour
	writeTimeout   = 15 * time.Second // limite de uma gravacao de sublote (busy_timeout do SQLite e 5 s)
	finalFlushTime = 30 * time.Second // limite da gravacao do que sobrou no desligamento
	maxDownHours   = 720              // o fechamento de intervalo distribui os segundos por, no maximo, 720 horas
)

// Storer e a parte do store que o gravador usa.
type Storer interface {
	WriteRegBatch(ctx context.Context, rev int64, b store.RegBatch) error
	MaxRegRev(ctx context.Context) (int64, error)
	PurgeReg(ctx context.Context, before time.Time) (int64, error)
}

// Writer e o gravador. Crie com New; Run bloqueia ate o ctx terminar. Um unico escritor: usa o mesmo *store.Store
// do daemon (pool de uma conexao), sem abrir outra conexao de escrita.
type Writer struct {
	st   Storer
	warn func(msg string, args ...any)
	info func(msg string, args ...any)
	now  func() time.Time
	ch   chan model.RegNote

	dropped     atomic.Uint64
	writeErrors atomic.Uint64 // sublotes perdidos (falharam na tentativa e na repeticao)
	lostEvents  atomic.Uint64 // notas perdidas nesses sublotes
	mu          sync.Mutex    // protege lostSince e lostUntil
	lostSince   time.Time
	lostUntil   time.Time
	rev         int64 // so a goroutine de Run
}

// New cria o gravador. warn e info recebem os logs (Warn e Info); nil descarta.
func New(st Storer, warn, info func(msg string, args ...any)) *Writer {
	nop := func(string, ...any) {}
	if warn == nil {
		warn = nop
	}
	if info == nil {
		info = nop
	}
	return &Writer{st: st, warn: warn, info: info, now: time.Now, ch: make(chan model.RegNote, QueueCap)}
}

// Enqueue entrega uma ocorrencia ao gravador SEM bloquear: fila cheia descarta e soma em Dropped.
// Seguro para a goroutine do estado (e o OnReg do daemon).
func (w *Writer) Enqueue(n model.RegNote) {
	select {
	case w.ch <- n:
	default:
		w.dropped.Add(1)
	}
}

// Status e a foto da fila e das perdas para o /api/health. Seguro para qualquer goroutine.
func (w *Writer) Status() model.RegStatus {
	w.mu.Lock()
	since, until := w.lostSince, w.lostUntil
	w.mu.Unlock()
	return model.RegStatus{QueueLen: len(w.ch), QueueCap: cap(w.ch), Dropped: w.dropped.Load(), WriteErrors: w.writeErrors.Load(),
		LostEvents: w.lostEvents.Load(), LostSince: since, LostUntil: until}
}

// Run grava em lote (a cada BatchEvery ou BatchSize itens) e limpa a retencao uma vez por dia e na subida.
// Ao fim do ctx grava o que sobrou.
func (w *Writer) Run(ctx context.Context) {
	if rev, err := w.st.MaxRegRev(ctx); err != nil {
		w.warn("relatorio de registro: ler a maior revisao falhou; recomeca da 0", "erro", err.Error())
	} else {
		w.rev = rev
	}
	w.purge(ctx)
	flushT := time.NewTicker(BatchEvery)
	defer flushT.Stop()
	purgeT := time.NewTicker(PurgeEvery)
	defer purgeT.Stop()
	batch := make([]model.RegNote, 0, BatchSize)
	flush := func(c context.Context) {
		if len(batch) > 0 {
			w.write(c, batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case <-ctx.Done():
			for { // o que ainda esta na fila
				select {
				case n := <-w.ch:
					batch = append(batch, n)
				default:
					fctx, cancel := context.WithTimeout(context.Background(), finalFlushTime)
					flush(fctx)
					cancel()
					return
				}
			}
		case n := <-w.ch:
			if batch = append(batch, n); len(batch) >= BatchSize {
				flush(ctx)
			}
		case <-flushT.C:
			flush(ctx)
		case <-purgeT.C:
			w.purge(ctx)
		}
	}
}

// write divide o lote em sublotes por custo e grava cada um (transacao propria, rev propria).
func (w *Writer) write(ctx context.Context, notes []model.RegNote) {
	for _, sub := range splitByCost(notes) {
		w.writeSub(ctx, sub)
	}
}

// writeSub grava um sublote. Falhou (rollback): repete uma vez, em transacao nova e com a mesma rev. Falhou de
// novo: o sublote e perdido, mas a perda e contada e dita (CONTRATOS 10 e 4.4); nunca em silencio.
func (w *Writer) writeSub(ctx context.Context, notes []model.RegNote) {
	b := toBatch(notes)
	if b.Empty() {
		return
	}
	w.rev++
	err := w.try(ctx, b)
	if err == nil {
		return
	}
	if ctx.Err() == nil { // desligando nao ha segunda chance
		if err2 := w.try(ctx, b); err2 == nil {
			w.info("relatorio de registro: sublote gravado na segunda tentativa", "ocorrencias", len(notes), "erro_da_primeira", err.Error())
			return
		} else {
			err = err2
		}
	}
	from, to := span(notes)
	w.writeErrors.Add(1)
	w.lostEvents.Add(uint64(len(notes)))
	w.mu.Lock()
	if w.lostSince.IsZero() || from.Before(w.lostSince) {
		w.lostSince = from
	}
	if to.After(w.lostUntil) {
		w.lostUntil = to
	}
	w.mu.Unlock()
	w.warn("relatorio de registro: ocorrencias perdidas", "ocorrencias", len(notes),
		"de", from.UTC().Format(time.RFC3339), "ate", to.UTC().Format(time.RFC3339), "erro", err.Error())
}

func (w *Writer) try(ctx context.Context, b store.RegBatch) error {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return w.st.WriteRegBatch(wctx, w.rev, b)
}

// span devolve o menor e o maior At entre as notas.
func span(notes []model.RegNote) (from, to time.Time) {
	for i, n := range notes {
		if i == 0 || n.At.Before(from) {
			from = n.At
		}
		if n.At.After(to) {
			to = n.At
		}
	}
	return from, to
}

// noteCost e o custo de uma nota em comandos SQL (CONTRATOS 10): ocorrencia = 2 (insert e upsert); fechamento de
// intervalo = as horas entre DownFrom e At (no maximo 720); sinal = 1. O corte de 200 por ramal entra a parte (1 por
// ramal distinto do sublote).
func noteCost(n model.RegNote) int {
	if n.Signal != "" {
		return 1
	}
	c := 0
	if n.Kind != "" {
		c += 2
	}
	if !n.DownFrom.IsZero() && n.At.After(n.DownFrom) {
		h := int((hourStart(n.At.Unix()-1)-hourStart(n.DownFrom.Unix()))/3600) + 1
		c += min(h, maxDownHours)
	}
	return c
}

func hourStart(unix int64) int64 { return unix - unix%3600 }

// splitByCost parte as notas, na ordem em que chegaram, em sublotes de custo ate SubBatchCost. Uma nota nunca e
// partida; a que sozinha passa do teto vai num sublote so dela.
func splitByCost(notes []model.RegNote) [][]model.RegNote {
	var out [][]model.RegNote
	var cur []model.RegNote
	cost := 0
	seen := map[string]struct{}{}
	for _, n := range notes {
		c := noteCost(n)
		_, known := seen[n.Number]
		if n.Number != "" && !known {
			c++ // o corte de 200 do ramal novo no sublote
		}
		if len(cur) > 0 && cost+c > SubBatchCost {
			out = append(out, cur)
			cur, cost, seen = nil, 0, map[string]struct{}{}
			c = noteCost(n)
			if n.Number != "" {
				c++
			}
		}
		cur = append(cur, n)
		cost += c
		if n.Number != "" {
			seen[n.Number] = struct{}{}
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func (w *Writer) purge(ctx context.Context) {
	n, err := w.st.PurgeReg(ctx, w.now().Add(-Retention))
	switch {
	case err != nil && ctx.Err() == nil:
		// dado velho a mais nao e perda: tenta de novo no dia seguinte
		w.warn("relatorio de registro: limpeza da retencao falhou; tenta de novo amanha", "apagadas", n, "erro", err.Error())
	case n > 0:
		w.info("relatorio de registro: linhas antigas removidas", "n", n)
	}
}

// toBatch converte as notas do estado nos tipos do store: ocorrencia (Kind), fechamento de intervalo
// (DownFrom) e sinal (Signal). Uma ocorrencia que fecha intervalo gera as duas coisas.
func toBatch(notes []model.RegNote) store.RegBatch {
	var b store.RegBatch
	for _, n := range notes {
		if n.Signal != "" {
			b.Signals = append(b.Signals, store.RegSignal{At: n.At, Kind: n.Signal})
			continue
		}
		if n.Kind != "" {
			b.Events = append(b.Events, store.RegEvent{At: n.At, Number: n.Number, Name: n.Name, Kind: n.Kind,
				System: n.System, IP: n.IP, Port: n.Port, UserAgent: n.UserAgent})
		}
		if !n.DownFrom.IsZero() {
			b.Downs = append(b.Downs, store.RegDown{Number: n.Number, Name: n.Name, From: n.DownFrom, To: n.At})
		}
	}
	return b
}
