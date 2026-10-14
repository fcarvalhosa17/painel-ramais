package history

import (
	"context"
	"testing"
	"time"

	"painel-ramais/internal/model"
	"painel-ramais/internal/store"
)

func TestSamplerWritesTotalAndGroupsAndGap(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	smp := Sample{
		Total:  model.Counts{Registered: 8, Unregistered: 1, Unreachable: 1, InCall: 2},
		Groups: map[string]model.Counts{"Suporte": {Registered: 5}, model.NoGroup: {Registered: 3, Unregistered: 1, Unreachable: 1, InCall: 2}},
	}
	s := &Sampler{Store: st, Source: func() Sample { return smp }, RetentionDays: 30}
	m := time.Date(2026, 10, 13, 14, 31, 0, 0, time.UTC)
	if err := s.Tick(ctx, m.Add(12*time.Second)); err != nil {
		t.Fatal(err)
	}
	smp.Gap = true
	smp.Total = model.Counts{Unknown: 10}
	if err := s.Tick(ctx, m.Add(time.Minute+3*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, _ := st.QueryHistory(ctx, store.TotalGroup, m, m.Add(2*time.Minute))
	if len(got) != 2 || got[0].Registered != 8 || got[0].InCall != 2 || got[0].Gap || !got[1].Gap {
		t.Fatalf("total: %+v", got)
	}
	g, _ := st.QueryHistory(ctx, "Suporte", m, m.Add(2*time.Minute))
	if len(g) != 2 || g[0].Registered != 5 || !g[1].Gap {
		t.Fatalf("grupo: %+v", g)
	}
	// 30 dias de retencao: minuto de 31 dias atras sai, o de agora fica.
	old := m.Add(-31 * 24 * time.Hour)
	st.InsertHistory(ctx, []store.HistoryRow{{Minute: old, Group: store.TotalGroup, Registered: 1}})
	n, err := s.Purge(ctx, m)
	if err != nil || n != 1 {
		t.Fatalf("purge = %d %v", n, err)
	}
	if left, _ := st.QueryHistory(ctx, store.TotalGroup, old, m.Add(time.Hour)); len(left) != 2 {
		t.Errorf("restaram %d", len(left))
	}
}

func TestIsGap(t *testing.T) {
	m := time.Date(2026, 10, 13, 14, 31, 0, 0, time.UTC)
	cases := []struct {
		name      string
		connected bool
		lastDown  time.Time
		want      bool
	}{
		{"conectado, nunca caiu", true, time.Time{}, false},
		{"desconectado", false, time.Time{}, true},
		{"caiu no minuto anterior", true, m.Add(-30 * time.Second), true},
		{"caiu e voltou ha mais de um minuto", true, m.Add(-61 * time.Second), false},
	}
	for _, c := range cases {
		if got := IsGap(c.connected, c.lastDown, m); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}
