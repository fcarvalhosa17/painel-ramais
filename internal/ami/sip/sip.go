// Package sip é o adaptador chan_sip: traduz as mensagens AMI do SIP em
// eventos normalizados (CONTRATOS.md seções 7 e 11).
package sip

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/ami/calls"
	"painel-ramais/internal/model"
)

const prefix = "SIP"

var latencyRe = regexp.MustCompile(`\((\d+) ms\)`)

// Adapter guarda o último RegistrationChanged de cada peer: o PeerStatus ao
// vivo não traz user agent nem (em Reachable/Lagged/Unreachable) o IP.
type Adapter struct {
	emit func(model.Event)
	mu   sync.Mutex
	last map[string]model.RegistrationChanged
}

// New cria o adaptador. emit recebe os eventos do Snapshot e do Enrich
// (precisa ser seguro para chamadas concorrentes).
func New(emit func(model.Event)) *Adapter {
	return &Adapter{emit: emit, last: map[string]model.RegistrationChanged{}}
}

// EndpointFromChannel: "SIP/2001-0000002a" -> "2001".
func (a *Adapter) EndpointFromChannel(channel string) (string, bool) {
	return calls.EndpointFromChannel(channel, prefix)
}

// Snapshot emite o estado inicial: peers (SIPpeers), device states e chamadas
// em andamento. Nome (callerid) e user agent não vêm no SIPpeers; veja Enrich.
func (a *Adapter) Snapshot(ctx context.Context, c *ami.Client) error {
	r, err := c.Do(ctx, "SIPpeers", nil)
	if err != nil {
		return fmt.Errorf("snapshot sip peers: %w", err)
	}
	a.mu.Lock()
	a.last = map[string]model.RegistrationChanged{}
	var out []model.Event
	for _, it := range r.Items {
		if it.Event() != "PeerEntry" {
			continue
		}
		reg := peerEntry(it)
		a.last[reg.Name] = reg
		out = append(out, model.EndpointSeen{At: it.At, Name: reg.Name, Technology: model.TechSIP}, reg)
	}
	a.mu.Unlock()
	for _, ev := range out {
		a.emit(ev)
	}
	if err := calls.SnapshotDeviceStates(ctx, c, prefix, a.emit); err != nil {
		return err
	}
	return calls.SnapshotChannels(ctx, c, a.EndpointFromChannel, a.emit)
}

// peerEntry traduz um PeerEntry. Status "UNREACHABLE" = unreachable; sem IP =
// unregistered; senão registered (com a latência de "OK (12 ms)" quando houver).
func peerEntry(m ami.Message) model.RegistrationChanged {
	reg := model.RegistrationChanged{At: m.At, Name: m.Get("ObjectName"), State: model.RegUnregistered}
	if ip := m.Get("IPaddress"); ip != "-none-" {
		reg.IP = ip
	}
	status := m.Get("Status")
	switch {
	case strings.HasPrefix(strings.ToUpper(status), "UNREACHABLE"):
		reg.State = model.RegUnreachable
	case reg.IP != "":
		reg.State = model.RegRegistered
		reg.LatencyMs = latency(status)
	}
	return reg
}

func latency(s string) *int {
	sub := latencyRe.FindStringSubmatch(s)
	if sub == nil {
		return nil
	}
	n, _ := strconv.Atoi(sub[1])
	return &n
}

// Translate traduz um evento ao vivo. Eventos que não são do painel viram nil.
func (a *Adapter) Translate(m ami.Message) []model.Event {
	switch m.Event() {
	case "PeerStatus":
		return a.peerStatus(m)
	case "DeviceStateChange":
		if ev, ok := calls.DeviceStateEvent(m, prefix); ok {
			return []model.Event{ev}
		}
		return nil
	}
	return calls.Translate(m, a.EndpointFromChannel)
}

func (a *Adapter) peerStatus(m ami.Message) []model.Event {
	name, ok := strings.CutPrefix(m.Get("Peer"), prefix+"/")
	if !ok {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	reg := a.last[name]
	reg.At, reg.Name, reg.LatencyMs = m.At, name, nil
	switch m.Get("PeerStatus") {
	case "Registered":
		reg.State = model.RegRegistered
		reg.IP = calls.First(calls.HostOnly(m.Get("Address")), reg.IP)
	case "Reachable", "Lagged":
		reg.State = model.RegRegistered
		if ms, err := strconv.Atoi(m.Get("Time")); err == nil {
			reg.LatencyMs = &ms
		}
	case "Unreachable":
		reg.State = model.RegUnreachable
	case "Unregistered", "Rejected":
		reg.State, reg.IP, reg.UserAgent = model.RegUnregistered, "", ""
	default:
		return nil // Unknown (sem qualify): sem informação nova
	}
	a.last[name] = reg
	return []model.Event{reg}
}

// Enrich completa nome (callerid) e user agent dos peers que want aceita,
// com SIPshowpeer limitado a perSecond chamadas por segundo. Roda depois do
// Snapshot, fora do caminho crítico (10 mil ramais a 200/s levam cerca de 50 s).
// Erro de um peer (ex.: removido) é pulado; queda da conexão ou ctx encerram.
func (a *Adapter) Enrich(ctx context.Context, c *ami.Client, perSecond int, want func(name string) bool) error {
	a.mu.Lock()
	names := make([]string, 0, len(a.last))
	for n := range a.last {
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
		r, err := c.Do(ctx, "SIPshowpeer", map[string]string{"Peer": n})
		var ae *ami.ActionError
		if errors.As(err, &ae) {
			continue
		}
		if err != nil {
			return err
		}
		a.apply(n, r.Head)
	}
	return nil
}

func (a *Adapter) apply(name string, m ami.Message) {
	a.mu.Lock()
	reg, known := a.last[name]
	if known {
		reg.UserAgent = m.Get("Useragent")
		reg.At = m.At
		a.last[name] = reg
	}
	a.mu.Unlock()
	a.emit(model.EndpointSeen{At: m.At, Name: name, CallerIDName: calls.CallerName(m.Get("Callerid")), Technology: model.TechSIP})
	if known && reg.UserAgent != "" {
		a.emit(reg)
	}
}
