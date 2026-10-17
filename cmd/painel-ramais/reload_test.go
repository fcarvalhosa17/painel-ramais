package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"painel-ramais/internal/model"
	"painel-ramais/internal/store"
)

// Recarga A le o banco; antes de A enviar, o import web grava o estado novo e a recarga B le e
// envia. Sem exclusao mutua A entregaria o estado velho por cima do novo e o painel ficaria com
// os grupos antigos; com ela, B espera A e a ultima entrega e a do estado final.
func TestConcurrentGroupReloadsEndWithLatestState(t *testing.T) {
	r := newRig(t)
	st := r.store()
	ctx := context.Background()
	set := func(start int64) {
		rows := []store.GroupRow{{Line: 1, Name: "G", Start: start, End: start + 5}}
		if err := st.ImportGroups(ctx, "t", rows, true, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	set(100) // estado velho

	events := make(chan model.Event, 4)
	bFinished := make(chan struct{})
	first := true
	groupsReadHook = func() {
		if !first {
			return
		}
		first = false
		select { // A espera B terminar; com o mutex B nao consegue entrar e o prazo vence
		case <-bFinished:
		case <-time.After(300 * time.Millisecond):
		}
	}
	defer func() { groupsReadHook = nil }()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // A: leu o estado velho
		defer wg.Done()
		if err := loadGroups(ctx, st, events); err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(50 * time.Millisecond)                   // A ja leu e esta no hook
	set(900)                                            // import web grava o estado novo
	if err := loadGroups(ctx, st, events); err != nil { // B
		t.Fatal(err)
	}
	close(bFinished)
	wg.Wait()
	close(events)

	var last []model.Range
	for ev := range events {
		if g := ev.(model.GroupsLoaded).Groups; len(g) == 1 {
			last = g[0].Ranges
		}
	}
	if len(last) != 1 || last[0].Start != 900 {
		t.Fatalf("a ultima entrega ao estado foi %+v; devia ser o estado novo (900)", last)
	}
}

// Contexto cancelado antes da entrega: loadGroups devolve o erro em vez de fingir que entregou.
func TestLoadGroupsReportsCancellation(t *testing.T) {
	r := newRig(t)
	st := r.store()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := loadGroups(ctx, st, make(chan model.Event)); err == nil {
		t.Fatal("deveria devolver ctx.Err()")
	}
	// O ReloadGroups da API sobrevive ao cancelamento do pedido.
	events := make(chan model.Event, 1)
	if err := reloadAfterRequest(ctx, st, events); err != nil || len(events) != 1 {
		t.Fatalf("recarga depois de pedido cancelado: err=%v enviados=%d", err, len(events))
	}
}
