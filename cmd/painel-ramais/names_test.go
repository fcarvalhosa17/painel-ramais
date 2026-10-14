package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"painel-ramais/internal/model"
)

func TestNameCachePersistsAndReloads(t *testing.T) {
	r := newRig(t)
	st := r.store()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	in := make(chan [2]string, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runNameCache(ctx, st, in, nil, log); close(done) }()
	in <- [2]string{"2001", "Maria"}
	in <- [2]string{"2001", "Maria Souza"} // o ultimo vence
	in <- [2]string{"2002", "Joao"}
	time.Sleep(50 * time.Millisecond)
	cancel() // desligamento: grava o que sobrou
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("runNameCache nao terminou")
	}
	events := make(chan model.Event, 1)
	loadNames(context.Background(), st, events, log)
	ev := (<-events).(model.NamesLoaded)
	if ev.Names["2001"] != "Maria Souza" || ev.Names["2002"] != "Joao" || len(ev.Names) != 2 {
		t.Errorf("cache: %v", ev.Names)
	}
}

// Sincronizacao completa: ramal que nao existe mais no PBX sai do cache.
func TestNameCachePrunesRemovedExtensions(t *testing.T) {
	r := newRig(t)
	st := r.store()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st.UpsertNames(context.Background(), map[string]string{"2001": "Maria", "2002": "Joao"}, time.Now())
	prune := make(chan []string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runNameCache(ctx, st, make(chan [2]string), prune, log); close(done) }()
	prune <- []string{"2001"}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := st.LoadNames(context.Background()); len(got) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if got, _ := st.LoadNames(context.Background()); len(got) != 1 || got["2001"] != "Maria" {
		t.Errorf("cache: %v", got)
	}
}
