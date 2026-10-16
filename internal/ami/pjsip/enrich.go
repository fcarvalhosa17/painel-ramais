package pjsip

import (
	"context"
	"errors"
	"sort"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/ami/calls"
	"painel-ramais/internal/model"
)

// Enrich completa o nome (callerid) dos endpoints que want aceita com PJSIPShowEndpoint (os recem-chegados primeiro),
// no maximo perSecond consultas por segundo, e nunca em rajada. Roda depois do Snapshot,
// fora do caminho critico (decisao 4). Erro de um endpoint (ex.: removido) e pulado;
// queda da conexao ou ctx encerram.
func (a *Adapter) Enrich(ctx context.Context, c *ami.Client, perSecond int, want func(name string) bool) error {
	a.mu.Lock()
	names := make([]string, 0, len(a.names))
	for _, n := range a.names {
		if want(n) {
			names = append(names, n)
		}
	}
	// Ramal que entrou na ultima leitura vai primeiro: aparece com nome sem esperar o ciclo todo.
	sort.Slice(names, func(i, j int) bool {
		_, fi := a.fresh[names[i]]
		_, fj := a.fresh[names[j]]
		if fi != fj {
			return fi
		}
		return names[i] < names[j]
	})
	a.mu.Unlock()
	tick := time.NewTicker(time.Second / time.Duration(max(perSecond, 1)))
	defer tick.Stop()
	for _, n := range names {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		r, err := c.Do(ctx, "PJSIPShowEndpoint", map[string]string{"Endpoint": n})
		var ae *ami.ActionError
		if errors.As(err, &ae) {
			a.consumed(n)
			continue
		}
		if err != nil {
			return err
		}
		a.consumed(n)
		a.emitName(n, r)
	}
	return nil
}

// consumed tira n de fresh: o nome dele ja foi buscado (ou o endpoint nao existe mais).
func (a *Adapter) consumed(n string) {
	a.mu.Lock()
	delete(a.fresh, n)
	a.mu.Unlock()
}

func (a *Adapter) emitName(name string, r ami.Response) {
	for _, it := range r.Items {
		if it.Event() != "EndpointDetail" {
			continue
		}
		if cn := calls.CallerName(it.Get("Callerid")); cn != "" {
			a.emit(model.EndpointSeen{At: it.At, Name: name, CallerIDName: cn, Technology: model.TechPJSIP})
		}
		return
	}
}
