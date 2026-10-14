// Package pjsip é o adaptador chan_pjsip: traduz as mensagens AMI do PJSIP em
// eventos normalizados (CONTRATOS.md seções 7 e 11). O endpoint pode ter
// vários contatos; o adaptador agrega e entrega um RegistrationChanged por endpoint.
package pjsip

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/ami/calls"
	"painel-ramais/internal/model"
)

const prefix = "PJSIP"

type contact struct {
	state model.Registration // registered ou unreachable
	ip    string
	ua    string
	lat   *int
}

// Adapter guarda os contatos por endpoint para poder agregar. Snapshot e
// Translate podem ser chamados de goroutines diferentes.
type Adapter struct {
	emit     func(model.Event)
	mu       sync.Mutex
	contacts map[string]map[string]contact // endpoint -> URI -> contato
	names    []string                      // endpoints do ultimo Snapshot (base do Enrich)
}

// New cria o adaptador. emit recebe os eventos do Snapshot.
func New(emit func(model.Event)) *Adapter {
	return &Adapter{emit: emit, contacts: map[string]map[string]contact{}}
}

// EndpointFromChannel: "PJSIP/2001-0000002a" -> "2001".
func (a *Adapter) EndpointFromChannel(channel string) (string, bool) {
	return calls.EndpointFromChannel(channel, prefix)
}

// Snapshot emite o estado inicial: endpoints, contatos (ou AORs), device
// states e chamadas em andamento. Chamar de novo refaz tudo do zero.
func (a *Adapter) Snapshot(ctx context.Context, c *ami.Client) error {
	eps, err := c.Do(ctx, "PJSIPShowEndpoints", nil)
	if err != nil {
		return fmt.Errorf("snapshot pjsip endpoints: %w", err)
	}
	cts, err := a.loadContacts(ctx, c)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.contacts = map[string]map[string]contact{}
	for _, it := range cts {
		a.addLocked(it)
	}
	var out []model.Event
	a.names = a.names[:0]
	for _, it := range eps.Items {
		if it.Event() != "EndpointList" {
			continue
		}
		name := it.Get("ObjectName")
		a.names = append(a.names, name)
		out = append(out,
			model.EndpointSeen{At: it.At, Name: name, CallerIDName: calls.CallerName(it.Get("Callerid")), Technology: model.TechPJSIP},
			a.aggregateLocked(name, it.At))
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

// loadContacts usa PJSIPShowContacts e, se a action não existir nesta versão
// (resposta de erro), cai para PJSIPShowAors.
func (a *Adapter) loadContacts(ctx context.Context, c *ami.Client) ([]ami.Message, error) {
	r, err := c.Do(ctx, "PJSIPShowContacts", nil)
	var ae *ami.ActionError
	if errors.As(err, &ae) {
		r, err = c.Do(ctx, "PJSIPShowAors", nil)
		if err != nil {
			return nil, fmt.Errorf("snapshot pjsip aors: %w", err)
		}
		return aorsToContacts(r.Items), nil
	}
	if err != nil {
		return nil, fmt.Errorf("snapshot pjsip contacts: %w", err)
	}
	return r.Items, nil
}

// aorsToContacts converte AorList (campo Contacts: "aor/uri, aor/uri") em
// itens parecidos com ContactList. O AOR não traz status: contato listado = registrado.
func aorsToContacts(items []ami.Message) []ami.Message {
	var out []ami.Message
	for _, it := range items {
		if it.Event() != "AorList" {
			continue
		}
		for _, entry := range strings.Split(it.Get("Contacts"), ",") {
			_, uri, ok := strings.Cut(strings.TrimSpace(entry), "/")
			if !ok || uri == "" {
				continue
			}
			m := ami.NewMessage("Event", "ContactList", "Endpoint", it.Get("ObjectName"), "Uri", uri, "Status", "Unknown")
			m.At = it.At
			out = append(out, m)
		}
	}
	return out
}

// Translate traduz um evento ao vivo. Eventos que não são do painel viram nil.
func (a *Adapter) Translate(m ami.Message) []model.Event {
	switch m.Event() {
	case "ContactStatus":
		return a.contactStatus(m)
	case "DeviceStateChange":
		if ev, ok := calls.DeviceStateEvent(m, prefix); ok {
			return []model.Event{ev}
		}
		return nil
	}
	return calls.Translate(m, a.EndpointFromChannel)
}

func (a *Adapter) contactStatus(m ami.Message) []model.Event {
	name := calls.First(m.Get("EndpointName"), m.Get("AOR"))
	if name == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.EqualFold(m.Get("ContactStatus"), "Removed") {
		delete(a.contacts[name], m.Get("URI"))
	} else {
		a.setLocked(name, m.Get("URI"), parseContact(m, m.Get("ContactStatus"), m.Get("ViaAddress")))
	}
	return []model.Event{a.aggregateLocked(name, m.At)}
}

func (a *Adapter) addLocked(it ami.Message) {
	if it.Event() != "ContactList" {
		return
	}
	name := calls.First(it.Get("Endpoint"), it.Get("EndpointName"), strings.SplitN(it.Get("ObjectName"), ";@", 2)[0])
	status := calls.First(it.Get("Status"), it.Get("ContactStatus"))
	if strings.EqualFold(status, "Removed") || name == "" {
		return
	}
	a.setLocked(name, it.Get("Uri"), parseContact(it, status, it.Get("ViaAddr")))
}

func (a *Adapter) setLocked(name, uri string, c contact) {
	if a.contacts[name] == nil {
		a.contacts[name] = map[string]contact{}
	}
	a.contacts[name][uri] = c
}

// aggregateLocked: algum contato registrado = registered; todos
// inalcançáveis = unreachable; nenhum = unregistered (CONTRATOS.md 7.3).
func (a *Adapter) aggregateLocked(name string, at time.Time) model.RegistrationChanged {
	cs := a.contacts[name]
	ev := model.RegistrationChanged{At: at, Name: name, State: model.RegUnregistered}
	if len(cs) == 0 {
		return ev
	}
	uris := make([]string, 0, len(cs))
	for u := range cs {
		uris = append(uris, u)
	}
	sort.Strings(uris) // escolha determinística do contato que dá IP e user agent
	best := cs[uris[0]]
	ev.State = model.RegUnreachable
	for _, u := range uris {
		if cs[u].state == model.RegRegistered {
			best, ev.State = cs[u], model.RegRegistered
			break
		}
	}
	ev.IP, ev.UserAgent, ev.LatencyMs = best.ip, best.ua, best.lat
	return ev
}

func parseContact(m ami.Message, status, via string) contact {
	c := contact{state: model.RegRegistered, ua: m.Get("UserAgent")}
	if strings.EqualFold(status, "Unreachable") {
		c.state = model.RegUnreachable
	}
	c.ip = calls.HostOnly(via)
	if c.ip == "" {
		c.ip = uriHost(calls.First(m.Get("URI"), m.Get("Uri")))
	}
	if us, err := strconv.ParseInt(m.Get("RoundtripUsec"), 10, 64); err == nil && us > 0 {
		ms := int((us + 500) / 1000)
		c.lat = &ms
	}
	return c
}

// uriHost extrai o host de "sip:2001@10.0.5.23:5060;ob".
func uriHost(uri string) string {
	_, rest, ok := strings.Cut(uri, ":")
	if !ok {
		return ""
	}
	if _, after, found := strings.Cut(rest, "@"); found {
		rest = after
	}
	if i := strings.IndexAny(rest, ";?>"); i >= 0 {
		rest = rest[:i]
	}
	return calls.HostOnly(rest)
}
