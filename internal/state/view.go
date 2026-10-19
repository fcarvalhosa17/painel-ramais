package state

import (
	"regexp"
	"sort"
	"time"

	"painel-ramais/internal/model"
)

type extState struct {
	number  string
	name    string
	reg     model.Registration
	ip, ua  string
	port    int // porta do contato (0 = desconhecida); so alimenta o relatorio de registro
	latency *int
	device  model.DeviceState
	// Grupos (ADR-006): groups = uniao de rangeGroups e manualGroups, nunca vazia ("Sem grupo");
	// as listas nunca sao mutadas, so trocadas (o hub as le de outra goroutine).
	groups, rangeGroups, manualGroups []string
	calls                             []model.Call
	// gone: o ramal sumiu do PBX (releitura) mas ainda tem chamada; sai do estado no Hangup.
	gone bool
	// Estado "fora do ar" e ultimo registro (relatorio de registro, ADR-010; zero = nulo). downSince = inicio do
	// "fora" atual (unregistered ou unreachable); downKind = observed, unknown_start ou before_install; downSeg = inicio
	// do trecho ainda nao somado em down_s; lastReg = ultima vez que ficou registrado (lastRegExact = hora vista).
	// markUnknown (AMI fora) NAO mexe nestes campos.
	downSince, downSeg, lastReg time.Time
	downKind                    string
	lastRegExact                bool
}

// view e um conjunto completo de estado. O estado mantem uma view publicada e,
// durante a sincronizacao, uma segunda em montagem.
type view struct {
	pattern  *regexp.Regexp
	gi       *groupIdx // resolucao de grupos (N:N); compartilhada com o State, so leitura
	rc       *regCtx   // relatorio de registro; compartilhado com o State (nil so em teste de view solta)
	tech     model.Technology
	asterisk string
	at       time.Time // hora do evento em processamento

	exts   map[string]*extState
	trunks map[string]struct{}
	dirty  map[string]struct{}
	// removed: ramais que saíram do estado desde o ultimo Flush (viram o campo removed do delta);
	// nunca divide numero com dirty. tomb: ramais removidos pela releitura; evento ao vivo
	// atrasado (DeviceStateChange INVALID, ContactStatus Removed) nao os recria. Sai do tomb
	// quando uma releitura volta a listar o ramal.
	removed, tomb map[string]struct{}
	// changes conta mutacoes efetivas desde o ultimo Flush; vira a versao.
	changes uint64

	chans       map[string]*channel
	byLinked    map[string]map[string]struct{}
	byEndpoint  map[string]map[string]struct{}
	bridges     map[string]map[string]struct{}
	localHalves map[string]*[2]string
	isBuilding  bool
	hints       map[string]string          // cache de nomes (compartilhado com o State)
	onName      func(number, name string)  // chamado quando um nome novo e aprendido ao vivo
	hungup      map[string]struct{}        // evita ressuscitar canal que caiu durante a sincronizacao
	noPeer      map[string]struct{}        // canais Up sem outro lado, esperando a protecao de 1 s (ver calls.go)
	onNoPeer    func(uid, linkedID string) // chamado uma vez quando a protecao publica "em conversa" sem outro lado
	onPrune     func()                     // chamado quando um ramal gone sai no Hangup (expurgo do cache de nomes)
}

func newView(p *regexp.Regexp, gi *groupIdx) *view {
	return &view{
		pattern: p, gi: gi,
		exts: map[string]*extState{}, trunks: map[string]struct{}{}, dirty: map[string]struct{}{},
		removed: map[string]struct{}{}, tomb: map[string]struct{}{},
		chans: map[string]*channel{}, byLinked: map[string]map[string]struct{}{},
		byEndpoint: map[string]map[string]struct{}{}, bridges: map[string]map[string]struct{}{},
		localHalves: map[string]*[2]string{}, hungup: map[string]struct{}{}, noPeer: map[string]struct{}{},
	}
}

func (v *view) takeChanges() uint64 {
	n := v.changes
	v.changes = 0
	return n
}

// reconcile aplica a lista completa de endpoints do PBX (InventoryListed): quem nao esta nela
// sai do estado (ou fica marcado gone se tem chamada ativa); quem esta e some do tomb. Os novos
// NAO sao criados aqui: os EndpointSeen da mesma leitura vem logo depois. Devolve as contagens
// para o log: added (listados sem estado ainda), removed, kept (nos dois lados), pending (gone).
func (v *view) reconcile(listed []string) (added, removed, kept, pending int) {
	in := make(map[string]struct{}, len(listed))
	for _, n := range listed {
		if v.isExtension(n) {
			in[n] = struct{}{}
		}
	}
	for n := range in {
		delete(v.tomb, n)
		if x := v.exts[n]; x != nil {
			x.gone = false
			kept++
		} else {
			added++
		}
	}
	for n, x := range v.exts {
		if _, ok := in[n]; ok {
			continue
		}
		if len(x.calls) > 0 {
			x.gone = true
			pending++
			continue
		}
		v.removeExt(n)
		removed++
	}
	return added, removed, kept, pending
}

// removeExt tira o ramal do estado e registra a remocao para o proximo lote.
func (v *view) removeExt(n string) {
	if x := v.exts[n]; x != nil {
		v.closeDown(x, v.at) // ramal apagado do PBX: o intervalo fora do ar fecha na hora da remocao
	}
	delete(v.exts, n)
	delete(v.dirty, n)
	delete(v.hints, n) // numero reaproveitado por outra pessoa nao herda o nome antigo
	v.removed[n] = struct{}{}
	v.tomb[n] = struct{}{}
	v.changes++
}

// takeRemoved devolve (ordenados) e zera os ramais removidos desde o ultimo Flush.
func (v *view) takeRemoved() []string {
	var out []string
	for n := range v.removed {
		out = append(out, n)
	}
	sort.Strings(out)
	v.removed = map[string]struct{}{}
	return out
}

func (v *view) extNumbers() []string {
	out := make([]string, 0, len(v.exts))
	for n := range v.exts {
		out = append(out, n)
	}
	return out
}

func (v *view) markDirty(number string) {
	v.dirty[number] = struct{}{}
	v.changes++
}

func (v *view) isExtension(name string) bool { return model.IsExtension(v.pattern, name) }

// ensureExt devolve o ramal (criando-o) ou nil se name e tronco (que fica registrado), endpoint
// interno do Issabel (ignorado de vez) ou ramal removido pela releitura (tomb).
func (v *view) ensureExt(name string) (e *extState, created bool) {
	if name == "" || model.IsInternalEndpoint(name) {
		return nil, false
	}
	if !v.isExtension(name) {
		v.trunks[name] = struct{}{}
		return nil, false
	}
	if e = v.exts[name]; e != nil {
		return e, false
	}
	if _, dead := v.tomb[name]; dead {
		return nil, false
	}
	all, rng, man := v.gi.resolve(name)
	e = &extState{number: name, name: v.hints[name], reg: model.RegUnknown, device: model.DevUnknown,
		groups: all, rangeGroups: rng, manualGroups: man}
	v.exts[name] = e
	delete(v.removed, name) // saiu e voltou na mesma janela: o lote leva o estado inteiro, sem removed
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
		v.noteCreatedOut(x, e.At)                    // ramal criado agora na visao publicada: fora desde que apareceu
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
	v.noteRegistration(x, e) // relatorio de registro: precisa do estado anterior, antes de gravar o novo
	if e.IP != "" && e.Port != 0 {
		x.port = e.Port
	}
	if x.reg == e.State && x.ip == e.IP && x.ua == e.UserAgent && intPtrEq(x.latency, e.LatencyMs) {
		return
	}
	x.reg, x.ip, x.port, x.ua, x.latency = e.State, e.IP, e.Port, e.UserAgent, e.LatencyMs
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
	v.removed = map[string]struct{}{}
	v.changes = 0
}

// setGroups troca a resolucao de grupos; so os ramais cujo groups, range_groups ou manual_groups mudou
// ficam sujos (viram delta). Nao mexe no epoch.
func (v *view) setGroups(gi *groupIdx) {
	v.gi = gi
	for _, x := range v.exts {
		all, rng, man := gi.resolve(x.number)
		if !sameNames(all, x.groups) || !sameNames(rng, x.rangeGroups) || !sameNames(man, x.manualGroups) {
			x.groups, x.rangeGroups, x.manualGroups = all, rng, man
			v.markDirty(x.number)
		}
	}
}

func (x *extState) model(tech model.Technology) model.Extension {
	calls := x.calls
	if calls == nil {
		calls = []model.Call{}
	}
	e := model.Extension{
		Number: x.number, Name: model.StrPtr(x.name), Technology: tech,
		Groups: x.groups, RangeGroups: x.rangeGroups, ManualGroups: x.manualGroups,
		Registration: x.reg, IP: model.StrPtr(x.ip), UserAgent: model.StrPtr(x.ua), LatencyMs: x.latency,
		DeviceState: x.device, Calls: calls,
	}
	if !x.downSince.IsZero() {
		t, seg := x.downSince, x.downSeg
		e.DownSince, e.DownSeg, e.DownKind = &t, &seg, x.downKind
	}
	if !x.lastReg.IsZero() {
		t := x.lastReg
		e.LastReg, e.LastRegExact = &t, x.lastRegExact
	}
	return e
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

func (v *view) counts(catalog []model.Group) (model.Counts, map[string]model.Counts) {
	var total model.Counts
	by := map[string]model.Counts{model.NoGroup: {}}
	for _, g := range catalog {
		by[g.Name] = model.Counts{}
	}
	for _, x := range v.exts {
		for _, g := range x.groups { // o ramal conta em cada grupo dele (N:N) e uma vez no total
			c := by[g]
			add(&c, x)
			by[g] = c
		}
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
