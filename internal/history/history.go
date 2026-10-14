// Package history grava a serie por minuto (total e por grupo) e expurga o que
// passou da retencao. Minuto sem AMI sai como lacuna (gap), nunca como queda.
package history

import (
	"context"
	"time"

	"painel-ramais/internal/model"
	"painel-ramais/internal/store"
)

// Sample e a foto de um instante: contagens do total e de cada grupo.
type Sample struct {
	Total  model.Counts
	Groups map[string]model.Counts
	Gap    bool
}

// Writer e a parte do store que o amostrador usa.
type Writer interface {
	InsertHistory(ctx context.Context, rows []store.HistoryRow) error
	PurgeHistory(ctx context.Context, before time.Time) (int64, error)
}

// Sampler grava uma amostra na virada de cada minuto.
type Sampler struct {
	Store         Writer
	Source        func() Sample
	RetentionDays int
	Now           func() time.Time
	Log           func(msg string, args ...any)
}

// IsGap diz se o minuto que comeca em minute e lacuna: AMI fora agora, ou
// queda desde a amostra anterior.
func IsGap(connected bool, lastDown, minute time.Time) bool {
	return !connected || !lastDown.Before(minute.Add(-time.Minute))
}

// Tick grava a amostra do minuto de now (truncado).
func (s *Sampler) Tick(ctx context.Context, now time.Time) error {
	minute := now.Truncate(time.Minute)
	smp := s.Source()
	rows := make([]store.HistoryRow, 0, len(smp.Groups)+1)
	rows = append(rows, row(minute, store.TotalGroup, smp.Total, smp.Gap))
	for name, c := range smp.Groups {
		rows = append(rows, row(minute, name, c, smp.Gap))
	}
	return s.Store.InsertHistory(ctx, rows)
}

func row(minute time.Time, group string, c model.Counts, gap bool) store.HistoryRow {
	// Unknown nao tem coluna: so existe com o AMI fora, que ja e gap.
	return store.HistoryRow{
		Minute: minute, Group: group, Gap: gap,
		Registered: c.Registered, Unregistered: c.Unregistered, Unreachable: c.Unreachable, InCall: c.InCall,
	}
}

// Purge apaga o que passou da retencao.
func (s *Sampler) Purge(ctx context.Context, now time.Time) (int64, error) {
	return s.Store.PurgeHistory(ctx, now.Add(-time.Duration(s.RetentionDays)*24*time.Hour))
}

// Run amostra a cada virada de minuto e expurga a cada hora, ate ctx terminar.
func (s *Sampler) Run(ctx context.Context) {
	now := s.Now
	if now == nil {
		now = time.Now
	}
	s.purgeLogged(ctx, now())
	lastPurge := now()
	for {
		t := now()
		timer := time.NewTimer(t.Truncate(time.Minute).Add(time.Minute).Sub(t))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		t = now()
		if err := s.Tick(ctx, t); err != nil && s.Log != nil {
			s.Log("falha ao gravar historico", "erro", err.Error())
		}
		if t.Sub(lastPurge) >= time.Hour {
			s.purgeLogged(ctx, t)
			lastPurge = t
		}
	}
}

func (s *Sampler) purgeLogged(ctx context.Context, t time.Time) {
	n, err := s.Purge(ctx, t)
	if s.Log == nil {
		return
	}
	if err != nil {
		s.Log("falha ao expurgar historico", "erro", err.Error())
	} else if n > 0 {
		s.Log("historico expurgado", "linhas", n)
	}
}
