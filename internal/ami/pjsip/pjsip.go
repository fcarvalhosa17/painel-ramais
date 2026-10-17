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
	lastUA   map[string]string             // endpoint|ip -> ultimo user agent conhecido (o ContactStatus do Asterisk 18 nao traz UserAgent)
	names    []string                      // endpoints da ultima leitura (base do Enrich e do Reread)
	fresh    map[string]struct{}           // entraram na ultima leitura: o Enrich busca o nome deles antes
}

// New cria o adaptador. emit recebe os eventos do Snapshot.
func New(emit func(model.Event)) *Adapter {
	return &Adapter{emit: emit, contacts: map[string]map[string]contact{}, lastUA: map[string]string{}}
}

// EndpointFromChannel: "PJSIP/2001-0000002a" -> "2001".
func (a *Adapter) EndpointFromChannel(channel string) (string, bool) {
	return calls.EndpointFromChannel(channel, prefix)
}

// Snapshot emite o estado inicial: endpoints, contatos (ou AORs), device
// states e chamadas em andamento. Chamar de novo refaz tudo do zero.
func (a *Adapter) Snapshot(ctx context.Context, c *ami.Client) error {
	eps, cts, err := a.list(ctx, c)
	if err != nil {
		return err
	}
	names, out := a.load(eps, cts, false)
	for _, ev := range out {
		a.emit(ev)
	}
	if err := calls.SnapshotDeviceStates(ctx, c, prefix, calls.OnlyListed(names, a.emit)); err != nil {
		return err
	}
	return calls.SnapshotChannels(ctx, c, a.EndpointFromChannel, a.emit)
}

// Reread rele a lista de endpoints, os contatos e os device states, sem tocar nas chamadas (os
// eventos ao vivo as mantem). Emite primeiro InventoryListed (o estado remove quem sumiu) e
// depois os mesmos eventos do Snapshot, que criam os novos e confirmam os que ficaram. Quem chama
// roda isto na mesma goroutine que consome os eventos AMI, entao nada se cruza com o Translate
// (e o que chegou durante a leitura e traduzido depois, mais novo que a lista). Lista vazia
// quando ja havia ramais e confirmada uma segunda vez antes de valer. TODA leitura (endpoints,
// contatos, device states) acontece antes de emitir qualquer coisa e antes de trocar o estado do
// adaptador: se uma falhar, nada foi emitido e nada mudou (a releitura inteira se repete). O
// registro que so difere na latencia mantem a anterior (nao marca o ramal como alterado a cada
// ciclo). Devolve quantos endpoints entraram (o supervisor refaz os nomes quando ha novos).
func (a *Adapter) Reread(ctx context.Context, c *ami.Client, reason string) (fresh int, err error) {
	eps, cts, err := a.list(ctx, c)
	if err != nil {
		return 0, err
	}
	if !hasEndpoints(eps) && a.known() > 0 {
		if err := calls.Pause(ctx, calls.EmptyRecheck); err != nil {
			return 0, err
		}
		if eps, cts, err = a.list(ctx, c); err != nil {
			return 0, err
		}
	}
	var devs []model.Event
	if err := calls.SnapshotDeviceStates(ctx, c, prefix, func(ev model.Event) { devs = append(devs, ev) }); err != nil {
		return 0, err
	}
	names, out := a.load(eps, cts, true)
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
	n := 0
	for _, name := range a.names {
		if !model.IsInternalEndpoint(name) { // servidor so com dummy_endpoint = sem ramais: sem pausa de confirmacao
			n++
		}
	}
	return n
}

// freshCount diz quantos endpoints entraram na ultima leitura e ainda esperam o nome.
func (a *Adapter) freshCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.fresh)
}

// hasEndpoints diz se a lista tem algum endpoint real (o dummy_endpoint do Issabel nao conta: lista
// transitoria so com ele nao pode passar por "tem ramais" e esvaziar a tela).
func hasEndpoints(eps []ami.Message) bool {
	for _, it := range eps {
		if it.Event() == "EndpointList" && !model.IsInternalEndpoint(it.Get("ObjectName")) {
			return true
		}
	}
	return false
}

// list le endpoints e contatos. Lista vazia ("No endpoints found") e zero ramais, nao erro.
func (a *Adapter) list(ctx context.Context, c *ami.Client) (eps, cts []ami.Message, err error) {
	r, err := c.DoList(ctx, "PJSIPShowEndpoints", nil)
	if err != nil {
		return nil, nil, fmt.Errorf("snapshot pjsip endpoints: %w", err)
	}
	cts, err = a.loadContacts(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	return r.Items, cts, nil
}

// load troca contatos e nomes pela leitura e devolve os nomes de todos os endpoints (ramais e
// troncos) e os eventos EndpointSeen/RegistrationChanged. Os que nao estavam na leitura anterior
// ficam em fresh: o Enrich busca o nome deles primeiro.
// keepLatency (so na releitura): se o registro agregado so muda na latencia, mantem a anterior.
func (a *Adapter) load(eps, cts []ami.Message, keepLatency bool) (names []string, out []model.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.contacts
	a.contacts = map[string]map[string]contact{}
	a.lastUA = map[string]string{} // a leitura traz o user agent de verdade: recomeca a memoria dele
	for _, it := range cts {
		a.addLocked(it)
	}
	known := make(map[string]struct{}, len(a.names))
	for _, n := range a.names {
		known[n] = struct{}{}
	}
	a.names, a.fresh = a.names[:0], map[string]struct{}{}
	for _, it := range eps {
		name := it.Get("ObjectName")
		if it.Event() != "EndpointList" || name == "" {
			continue
		}
		a.names = append(a.names, name)
		if _, ok := known[name]; !ok {
			a.fresh[name] = struct{}{}
		}
		reg := a.aggregateLocked(name, it.At)
		if keepLatency {
			if prev := aggregate(old[name], name, it.At); prev.State == reg.State && prev.IP == reg.IP && prev.UserAgent == reg.UserAgent && prev.LatencyMs != nil {
				reg.LatencyMs = prev.LatencyMs
			}
		}
		out = append(out,
			model.EndpointSeen{At: it.At, Name: name, CallerIDName: calls.CallerName(it.Get("Callerid")), Technology: model.TechPJSIP},
			reg)
	}
	return append([]string(nil), a.names...), out
}

// loadContacts usa PJSIPShowContacts e, se a action não existir nesta versão
// (resposta de erro), cai para PJSIPShowAors. "No ... found" nas duas e lista vazia.
func (a *Adapter) loadContacts(ctx context.Context, c *ami.Client) ([]ami.Message, error) {
	r, err := c.DoList(ctx, "PJSIPShowContacts", nil)
	var ae *ami.ActionError
	if errors.As(err, &ae) {
		r, err = c.DoList(ctx, "PJSIPShowAors", nil)
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
		c := parseContact(m, m.Get("ContactStatus"), m.Get("ViaAddress"))
		if c.ua == "" { // evento sem UserAgent nao apaga o que o snapshot (ou um evento anterior) ja trouxe
			c.ua = a.lastUA[name+"|"+c.ip]
		}
		a.setLocked(name, m.Get("URI"), c)
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
	if c.ua != "" {
		a.lastUA[name+"|"+c.ip] = c.ua
	}
	if a.contacts[name] == nil {
		a.contacts[name] = map[string]contact{}
	}
	a.contacts[name][uri] = c
}

// aggregateLocked: algum contato registrado = registered; todos
// inalcançáveis = unreachable; nenhum = unregistered (CONTRATOS.md 7.3).
func (a *Adapter) aggregateLocked(name string, at time.Time) model.RegistrationChanged {
	return aggregate(a.contacts[name], name, at)
}

// aggregate e a regra de aggregateLocked sobre um conjunto de contatos qualquer.
func aggregate(cs map[string]contact, name string, at time.Time) model.RegistrationChanged {
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

// uriHost extrai o host de "sip:2001@192.0.2.23:5060;ob".
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
