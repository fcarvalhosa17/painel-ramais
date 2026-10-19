// Package reglog e o gravador do relatorio de registro (ADR-007, CONTRATOS 11.5): recebe as
// ocorrencias que o estado produz, junta e grava em lote numa transacao, fora do caminho do AMI. A
// goroutine do estado so chama Enqueue, que nunca bloqueia; fila cheia descarta e conta.
package reglog

import (
	"context"
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
	Retention      = 30 * 24 * time.Hour
	PurgeEvery     = 24 * time.Hour
	writeTimeout   = 15 * time.Second // limite de uma gravacao de lote (busy_timeout do SQLite e 5 s)
	finalFlushTime = 10 * time.Second // limite da gravacao do que sobrou no desligamento
)

// Storer e a parte do store que o gravador usa.
type Storer interface {
	WriteRegBatch(ctx context.Context, rev int64, b store.RegBatch) error
	MaxRegRev(ctx context.Context) (int64, error)
	PurgeReg(ctx context.Context, before time.Time) (int64, error)
}

// Writer e o gravador. Crie com New; Run bloqueia ate o ctx terminar.
type Writer struct {
	st  Storer
	log func(msg string, args ...any)
	now func() time.Time
	ch  chan model.RegNote

	dropped     atomic.Uint64
	writeErrors atomic.Uint64
	rev         int64 // so a goroutine de Run
}

// New cria o gravador. log recebe os avisos (Warn); nil descarta.
func New(st Storer, log func(msg string, args ...any)) *Writer {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &Writer{st: st, log: log, now: time.Now, ch: make(chan model.RegNote, QueueCap)}
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

// Status e a foto da fila para o /api/health. Seguro para qualquer goroutine.
func (w *Writer) Status() model.RegStatus {
	return model.RegStatus{QueueLen: len(w.ch), QueueCap: cap(w.ch), Dropped: w.dropped.Load(), WriteErrors: w.writeErrors.Load()}
}

// Run grava em lote (a cada BatchEvery ou BatchSize itens) e limpa a retencao uma vez por dia e na subida.
// Ao fim do ctx grava o que sobrou.
func (w *Writer) Run(ctx context.Context) {
	if rev, err := w.st.MaxRegRev(ctx); err != nil {
		w.log("relatorio de registro: ler a maior revisao falhou; recomeca da 0", "erro", err.Error())
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

// write grava um lote; falha = rollback, write_errors++, aviso no log e o lote e descartado.
func (w *Writer) write(ctx context.Context, notes []model.RegNote) {
	b := toBatch(notes)
	if b.Empty() {
		return
	}
	w.rev++
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := w.st.WriteRegBatch(wctx, w.rev, b); err != nil {
		w.writeErrors.Add(1)
		w.log("relatorio de registro: lote descartado (falha ao gravar)", "ocorrencias", len(notes), "erro", err.Error())
	}
}

func (w *Writer) purge(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	n, err := w.st.PurgeReg(pctx, w.now().Add(-Retention))
	if err != nil && ctx.Err() == nil {
		w.log("relatorio de registro: limpeza da retencao falhou", "erro", err.Error())
	} else if n > 0 {
		w.log("relatorio de registro: linhas antigas removidas", "n", n)
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
