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

// Enrich completa o nome (callerid) dos endpoints que want aceita com PJSIPShowEndpoint,
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
	a.mu.Unlock()
	sort.Strings(names)
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
			continue
		}
		if err != nil {
			return err
		}
		a.emitName(n, r)
	}
	return nil
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
