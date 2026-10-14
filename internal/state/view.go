package state

import (
	"regexp"
	"sort"
	"strconv"
	"time"

	"painel-ramais/internal/model"
)

type extState struct {
	number  string
	name    string
	reg     model.Registration
	ip, ua  string
	latency *int
	device  model.DeviceState
	group   string
	calls   []model.Call
}

// view e um conjunto completo de estado. O estado mantem uma view publicada e,
// durante a sincronizacao, uma segunda em montagem.
type view struct {
	pattern  *regexp.Regexp
	groups   []model.GroupRange
	tech     model.Technology
	asterisk string
	at       time.Time // hora do evento em processamento

	exts   map[string]*extState
	trunks map[string]struct{}
	dirty  map[string]struct{}
	// changes conta mutacoes efetivas desde o ultimo Flush; vira a versao.
	changes uint64

	chans       map[string]*channel
	byLinked    map[string]map[string]struct{}
	byEndpoint  map[string]map[string]struct{}
	bridges     map[string]map[string]struct{}
	localHalves map[string]*[2]string
	isBuilding  bool
	hints       map[string]string         // cache de nomes (compartilhado com o State)
	onName      func(number, name string) // chamado quando um nome novo e aprendido ao vivo
	hungup      map[string]struct{}       // evita ressuscitar canal que caiu durante a sincronizacao
}

func newView(p *regexp.Regexp, groups []model.GroupRange) *view {
	return &view{
		pattern: p, groups: groups,
		exts: map[string]*extState{}, trunks: map[string]struct{}{}, dirty: map[string]struct{}{},
		chans: map[string]*channel{}, byLinked: map[string]map[string]struct{}{},
		byEndpoint: map[string]map[string]struct{}{}, bridges: map[string]map[string]struct{}{},
		localHalves: map[string]*[2]string{}, hungup: map[string]struct{}{},
	}
}

func (v *view) takeChanges() uint64 {
	n := v.changes
	v.changes = 0
	return n
}

func (v *view) markDirty(number string) {
	v.dirty[number] = struct{}{}
	v.changes++
}

func (v *view) isExtension(name string) bool { return name != "" && v.pattern.MatchString(name) }

// ensureExt devolve o ramal (criando-o) ou nil se name e tronco (que fica registrado).
func (v *view) ensureExt(name string) (e *extState, created bool) {
	if name == "" {
		return nil, false
	}
	if !v.isExtension(name) {
		v.trunks[name] = struct{}{}
		return nil, false
	}
	if e = v.exts[name]; e != nil {
		return e, false
	}
	e = &extState{number: name, name: v.hints[name], reg: model.RegUnknown, device: model.DevUnknown, group: groupFor(v.groups, name)}
	v.exts[name] = e
	v.markDirty(name)
	v.recalc(map[string]struct{}{name: {}})
	return e, true
}

func (v *view) apply(ev model.Event) {
	switch e := ev.(type) {
	case model.EndpointSeen:
		v.onEndpointSeen(e)
	case model.RegistrationChanged:
		v.onRegistration(e)
	case model.DeviceStateChanged:
		if x, _ := v.ensureExt(e.Name); x != nil && x.device != e.State {
			x.device = e.State
			v.markDirty(x.number)
		}
	default:
		v.applyChannel(ev)
	}
}

func (v *view) onEndpointSeen(e model.EndpointSeen) {
	x, _ := v.ensureExt(e.Name)
	if x == nil {
		return
	}
	e.CallerIDName = model.CleanName(e.CallerIDName)
	changed := false
	if e.CallerIDName != "" && x.name != e.CallerIDName {
		x.name, changed = e.CallerIDName, true
		v.learned(x.number, x.name)
	}
	if x.reg == model.RegUnknown {
		x.reg, changed = model.RegUnregistered, true // endpoint listado sem contato
	}
	if e.Technology != "" && v.tech == "" {
		v.tech = e.Technology
	}
	if changed {
		v.markDirty(x.number)
	}
}

func (v *view) onRegistration(e model.RegistrationChanged) {
	x, _ := v.ensureExt(e.Name)
	if x == nil {
		return
	}
	if x.reg == e.State && x.ip == e.IP && x.ua == e.UserAgent && intPtrEq(x.latency, e.LatencyMs) {
		return
	}
	x.reg, x.ip, x.ua, x.latency = e.State, e.IP, e.UserAgent, e.LatencyMs
	v.markDirty(x.number)
}

// learned avisa o dono do cache que um nome real foi visto.
func (v *view) learned(number, name string) {
	if v.onName != nil {
		v.onName(number, name)
	}
}

// applyHints preenche, a partir do cache, os ramais que ainda estao sem nome.
func (v *view) applyHints() {
	for n, x := range v.exts {
		if h := v.hints[n]; x.name == "" && h != "" {
			x.name = h
			v.markDirty(n)
		}
	}
}

func intPtrEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// markUnknown implementa a queda do AMI: nada de "nao registrado" por falta de AMI.
func (v *view) markUnknown() {
	for _, x := range v.exts {
		x.reg, x.device, x.latency, x.calls = model.RegUnknown, model.DevUnknown, nil, nil
	}
	v.chans = map[string]*channel{}
	v.byLinked = map[string]map[string]struct{}{}
	v.byEndpoint = map[string]map[string]struct{}{}
	v.bridges = map[string]map[string]struct{}{}
	v.localHalves = map[string]*[2]string{}
	v.dirty = map[string]struct{}{}
	v.changes = 0
}

func (v *view) setGroups(g []model.GroupRange) {
	v.groups = g
	for _, x := range v.exts {
		x.group = groupFor(g, x.number)
	}
}

// groupFor acha o grupo por busca binaria. Ramal nao numerico, com zero a
// esquerda ou fora de faixa fica em "Sem grupo".
func groupFor(groups []model.GroupRange, number string) string {
	if len(number) == 0 || len(number) > 18 || (number[0] == '0' && len(number) > 1) {
		return model.NoGroup
	}
	n, err := strconv.ParseInt(number, 10, 64)
	if err != nil {
		return model.NoGroup
	}
	i := sort.Search(len(groups), func(i int) bool { return groups[i].Start > n }) - 1
	if i >= 0 && n <= groups[i].End {
		return groups[i].Name
	}
	return model.NoGroup
}

func (x *extState) model(tech model.Technology) model.Extension {
	calls := x.calls
	if calls == nil {
		calls = []model.Call{}
	}
	return model.Extension{
		Number: x.number, Name: model.StrPtr(x.name), Technology: tech, Group: x.group,
		Registration: x.reg, IP: model.StrPtr(x.ip), UserAgent: model.StrPtr(x.ua), LatencyMs: x.latency,
		DeviceState: x.device, Calls: calls,
	}
}

func (v *view) allExtensions() []model.Extension {
	out := make([]model.Extension, 0, len(v.exts))
	for _, x := range v.exts {
		out = append(out, x.model(v.tech))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

func (v *view) dirtyExtensions() []model.Extension {
	out := make([]model.Extension, 0, len(v.dirty))
	for n := range v.dirty {
		if x := v.exts[n]; x != nil {
			out = append(out, x.model(v.tech))
		}
	}
	v.dirty = map[string]struct{}{}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

func (v *view) counts(groups []model.GroupRange) (model.Counts, map[string]model.Counts) {
	var total model.Counts
	by := map[string]model.Counts{model.NoGroup: {}}
	for _, g := range groups {
		by[g.Name] = model.Counts{}
	}
	for _, x := range v.exts {
		c := by[x.group]
		add(&c, x)
		by[x.group] = c
		add(&total, x)
	}
	return total, by
}

func add(c *model.Counts, x *extState) {
	switch x.reg {
	case model.RegRegistered:
		c.Registered++
	case model.RegUnregistered:
		c.Unregistered++
	case model.RegUnreachable:
		c.Unreachable++
	default:
		c.Unknown++
	}
	if len(x.calls) > 0 {
		c.InCall++
	}
}

// activeCalls conta chamadas distintas (por Linkedid) com canal de ramal ou tronco.
func (v *view) activeCalls() int {
	seen := map[string]struct{}{}
	for _, c := range v.chans {
		if !c.isLocal && c.endpoint != "" {
			seen[c.linkedID] = struct{}{}
		}
	}
	return len(seen)
}
