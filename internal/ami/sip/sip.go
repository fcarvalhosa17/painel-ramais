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
	emit  func(model.Event)
	mu    sync.Mutex
	last  map[string]model.RegistrationChanged
	fresh map[string]struct{} // entraram na ultima leitura: o Enrich busca o nome deles antes
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
	items, err := a.list(ctx, c)
	if err != nil {
		return err
	}
	names, out := a.load(items, false)
	for _, ev := range out {
		a.emit(ev)
	}
	if err := calls.SnapshotDeviceStates(ctx, c, prefix, calls.OnlyListed(names, a.emit)); err != nil {
		return err
	}
	return calls.SnapshotChannels(ctx, c, a.EndpointFromChannel, a.emit)
}

// Reread rele os peers e os device states, sem tocar nas chamadas. Mesma ordem de eventos e mesmas
// garantias do pjsip.Adapter.Reread: InventoryListed primeiro, depois EndpointSeen e
// RegistrationChanged de cada peer; lista vazia com ramais conhecidos e confirmada duas vezes. Toda
// leitura (peers e device states) vem antes de emitir ou trocar o estado do adaptador: falha em
// qualquer uma nao deixa nada pela metade. Registro que so difere na latencia mantem a anterior.
// Devolve quantos peers entraram.
func (a *Adapter) Reread(ctx context.Context, c *ami.Client, reason string) (fresh int, err error) {
	items, err := a.list(ctx, c)
	if err != nil {
		return 0, err
	}
	if len(items) == 0 && a.known() > 0 {
		if err := calls.Pause(ctx, calls.EmptyRecheck); err != nil {
			return 0, err
		}
		if items, err = a.list(ctx, c); err != nil {
			return 0, err
		}
	}
	var devs []model.Event
	if err := calls.SnapshotDeviceStates(ctx, c, prefix, func(ev model.Event) { devs = append(devs, ev) }); err != nil {
		return 0, err
	}
	names, out := a.load(items, true)
	a.emit(model.InventoryListed{At: time.Now(), Reason: reason, Names: names})
	for _, ev := range out {
		a.emit(ev)
	}
	only := calls.OnlyListed(names, a.emit)
	for _, ev := range devs {
		only(ev)
	}
	return a.freshCount(), nil
}

func (a *Adapter) known() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.last)
}

// freshCount diz quantos peers entraram na ultima leitura e ainda esperam o nome.
func (a *Adapter) freshCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.fresh)
}

// list le os peers (PeerEntry). Lista vazia (sem peers, ou "No ... found") e zero ramais, nao erro.
func (a *Adapter) list(ctx context.Context, c *ami.Client) ([]ami.Message, error) {
	r, err := c.DoList(ctx, "SIPpeers", nil)
	if err != nil {
		return nil, fmt.Errorf("snapshot sip peers: %w", err)
	}
	items := r.Items[:0:0]
	for _, it := range r.Items {
		if it.Event() == "PeerEntry" {
			items = append(items, it)
		}
	}
	return items, nil
}

// load troca o ultimo estado de cada peer pela leitura e devolve os nomes e os eventos. O
// SIPpeers nao traz user agent: o do Enrich e mantido enquanto o peer segue registrado no mesmo IP.
// Peers que nao estavam na leitura anterior ficam em fresh (o Enrich busca o nome deles antes).
// keepLatency (so na releitura): se o registro so muda na latencia, mantem a anterior.
func (a *Adapter) load(items []ami.Message, keepLatency bool) (names []string, out []model.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	prev, wasFresh := a.last, a.fresh
	a.last, a.fresh = make(map[string]model.RegistrationChanged, len(items)), map[string]struct{}{}
	for _, it := range items {
		reg := peerEntry(it)
		if reg.Name == "" {
			continue
		}
		old, known := prev[reg.Name]
		if known && reg.State == model.RegRegistered && reg.IP == old.IP {
			reg.UserAgent = old.UserAgent
		}
		if keepLatency && known && reg.State == old.State && reg.IP == old.IP && old.LatencyMs != nil {
			reg.LatencyMs = old.LatencyMs
		}
		if _, pending := wasFresh[reg.Name]; !known || pending { // peer marcado em peerStatus segue novo ate o nome chegar
			a.fresh[reg.Name] = struct{}{}
		}
		a.last[reg.Name] = reg
		names = append(names, reg.Name)
		out = append(out, model.EndpointSeen{At: it.At, Name: reg.Name, Technology: model.TechSIP}, reg)
	}
	return names, out
}

// peerEntry traduz um PeerEntry. Status "UNREACHABLE" = unreachable; sem IP =
// unregistered; senão registered (com a latência de "OK (12 ms)" quando houver).
func peerEntry(m ami.Message) model.RegistrationChanged {
	reg := model.RegistrationChanged{At: m.At, Name: m.Get("ObjectName"), State: model.RegUnregistered}
	if ip := m.Get("IPaddress"); ip != "-none-" {
		reg.IP = ip
		if p, err := strconv.Atoi(m.Get("IPport")); err == nil && p >= 1 && p <= 65535 {
			reg.Port = p
		}
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
	reg, known := a.last[name]
	if !known { // peer novo que registrou antes da releitura: o Enrich busca o nome dele primeiro
		if a.fresh == nil {
			a.fresh = map[string]struct{}{}
		}
		a.fresh[name] = struct{}{}
	}
	reg.At, reg.Name, reg.LatencyMs = m.At, name, nil
	reg.Live = true // so o PeerStatus ao vivo conta no relatorio de registro (ADR-007)
	switch m.Get("PeerStatus") {
	case "Registered":
		reg.State = model.RegRegistered
		if host, port := calls.HostPort(m.Get("Address")); host != "" {
			reg.IP, reg.Port = host, port
		}
	case "Reachable", "Lagged":
		reg.State = model.RegRegistered
		if ms, err := strconv.Atoi(m.Get("Time")); err == nil {
			reg.LatencyMs = &ms
		}
	case "Unreachable":
		reg.State = model.RegUnreachable
	case "Unregistered", "Rejected":
		reg.State, reg.IP, reg.Port, reg.UserAgent = model.RegUnregistered, "", 0, ""
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
	// Peer que entrou na ultima leitura vai primeiro: aparece com nome sem esperar o ciclo todo.
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
		r, err := c.Do(ctx, "SIPshowpeer", map[string]string{"Peer": n})
		var ae *ami.ActionError
		if errors.As(err, &ae) {
			a.consumed(n)
			continue
		}
		if err != nil {
			return err
		}
		a.consumed(n)
		a.apply(n, r.Head)
	}
	return nil
}

// consumed tira n de fresh: o nome dele ja foi buscado (ou o peer nao existe mais).
func (a *Adapter) consumed(n string) {
	a.mu.Lock()
	delete(a.fresh, n)
	a.mu.Unlock()
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
