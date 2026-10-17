// Package state e o dono unico do estado em memoria: ramais, canais, bridges,
// versao e epoch. Tudo roda numa goroutine (Run); nada aqui conhece AMI nem HTTP.
package state

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync/atomic"
	"time"

	"painel-ramais/internal/model"
)

// Options ajusta relogio e intervalo de lote (testes injetam os dois).
type Options struct {
	Now      func() time.Time
	Interval time.Duration // padrao 250 ms
	// OnName e chamado (na goroutine do estado, sem bloquear) quando um nome de ramal e aprendido.
	OnName func(number, name string)
	// OnSync recebe os numeros de todos os ramais ao fim de cada sincronizacao completa (o dono
	// do cache de nomes expurga os que sumiram). Chamado na goroutine do estado: nao pode bloquear.
	OnSync func(numbers []string)
	// OnNoPeer e chamado (na goroutine do estado, sem bloquear) quando uma chamada Up segue sem
	// outro lado resolvido por 1 s e passa a ser publicada "em conversa" com o outro lado desconhecido.
	OnNoPeer func(uniqueid, linkedid string)
	// OnInventory e chamado (na goroutine do estado, sem bloquear) a cada releitura completa do
	// inventario de ramais: reason (reload, periodica...), quantos entraram, sairam e ficaram, e
	// quantos apagados no PBX seguem na tela ate o fim da ultima chamada (pending).
	OnInventory func(reason string, added, removed, kept, pending int)
}

// Stats e a foto agregada publicada a cada lote, lida sem bloquear o estado.
type Stats struct {
	Epoch           string
	Version         uint64
	AMIConnected    bool
	Technology      model.Technology
	AsteriskVersion string
	ConnectedSince  time.Time // zero se desconectado
	LastDisconnect  time.Time
	Total           model.Counts
	ByGroup         map[string]model.Counts // inclui grupos sem ramal e "Sem grupo"
	Trunks          int
	ActiveCalls     int
	EventsPerSec    float64
	LagMs           int64 // atraso entre a leitura do evento e seu processamento
}

// State recebe eventos normalizados e emite Batch/Reset.
type State struct {
	pattern  *regexp.Regexp
	now      func() time.Time
	interval time.Duration
	in       <-chan model.Event
	out      chan model.Output
	stats    atomic.Pointer[Stats]

	// A partir daqui, so a goroutine de Run (ou o teste) toca nos campos.
	cur, building *view
	groups        []model.GroupRange // faixas de todos os grupos, ordenadas por inicio (busca binaria)
	catalog       []model.Group      // catalogo completo, inclui grupo sem faixa e sem ramal
	manual        map[string]string  // vinculo manual: numero -> grupo ("Sem grupo" incluso); nunca mutado, so trocado
	groupsChanged bool               // o catalogo mudou desde o ultimo lote: o proximo Batch o leva
	hints         map[string]string
	onName        func(number, name string)
	onSync        func(numbers []string)
	onNoPeer      func(uniqueid, linkedid string)
	onInventory   func(reason string, added, removed, kept, pending int)
	epochBase     int64
	epochSeq      int
	epoch         string
	version       uint64
	connected     bool
	connectedAt   time.Time
	lastDown      time.Time
	needReset     bool
	rate          [60]struct {
		sec int64
		n   uint64
	}
	lagMs int64
}

// New cria o estado. pattern ja deve casar o nome inteiro (config envolve em ^(?:...)$).
func New(pattern *regexp.Regexp, in <-chan model.Event, opt Options) *State {
	s := &State{pattern: pattern, in: in, hints: map[string]string{}, onName: opt.OnName, onSync: opt.OnSync, onNoPeer: opt.OnNoPeer, onInventory: opt.OnInventory, out: make(chan model.Output, 64), now: opt.Now, interval: opt.Interval}
	if s.now == nil {
		s.now = time.Now
	}
	if s.interval <= 0 {
		s.interval = 250 * time.Millisecond
	}
	s.epochBase = s.now().UnixMilli()
	s.cur = s.newView()
	s.bumpEpoch()
	s.needReset = true // Reset inicial: o hub nasce com a projecao do estado
	s.publishStats()
	return s
}

// Out entrega Batch e Reset em ordem. O canal e bufferizado; se o hub atrasar,
// Run espera e a fila de entrada enche (o coletor trata o overflow).
func (s *State) Out() <-chan model.Output { return s.out }

// Stats devolve a ultima foto publicada. Seguro para qualquer goroutine.
func (s *State) Stats() Stats { return *s.stats.Load() }

// Run processa eventos ate ctx terminar.
func (s *State) Run(ctx context.Context) error {
	tick := time.NewTicker(s.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-s.in:
			s.Apply(ev)
		case <-tick.C:
			if o := s.Flush(); o != nil {
				select {
				case s.out <- o:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

func (s *State) bumpEpoch() {
	s.epochSeq++
	s.epoch = fmt.Sprintf("%d-%d", s.epochBase, s.epochSeq)
	s.needReset = true
}

func (s *State) target() *view {
	if s.building != nil {
		return s.building
	}
	return s.cur
}

// Apply aplica um evento. Chamado so pela goroutine dona.
func (s *State) Apply(ev model.Event) {
	at := ev.EventTime()
	s.noteEvent(at)
	switch e := ev.(type) {
	case model.AMIDisconnected:
		s.onDisconnected(e)
	case model.SyncStarted:
		s.building = s.newView()
		s.building.isBuilding = true
		s.building.tech = e.Technology
		s.building.asterisk = e.AsteriskVersion
	case model.SyncFinished:
		s.onSyncFinished(e)
	case model.GroupsLoaded:
		s.onGroups(e)
	case model.NamesLoaded:
		s.onNames(e)
	case model.InventoryListed:
		s.onInventoryListed(e)
	default:
		if !s.connected && s.building == nil {
			return // evento solto de sessao que ja caiu (ex.: Enrich atrasado): nao cria estado com o AMI fora
		}
		v := s.target()
		v.at = at
		v.apply(ev)
	}
}

func (s *State) noteEvent(at time.Time) {
	now := s.now()
	if lag := now.Sub(at).Milliseconds(); lag > 0 {
		s.lagMs = lag
	} else {
		s.lagMs = 0
	}
	sec := now.Unix()
	b := &s.rate[sec%60]
	if b.sec != sec {
		b.sec, b.n = sec, 0
	}
	b.n++
}

func (s *State) onDisconnected(e model.AMIDisconnected) {
	s.building = nil
	if !s.connected {
		return // reconexoes falhando nao geram epoch (evita churn de clientes)
	}
	s.connected = false
	s.connectedAt = time.Time{}
	s.lastDown = e.At
	s.cur.markUnknown()
	s.bumpEpoch()
}

func (s *State) onSyncFinished(e model.SyncFinished) {
	if s.building == nil {
		return
	}
	s.cur, s.building = s.building, nil
	s.cur.isBuilding = false // virou a publicada: nao lembra mais de Hangups
	s.cur.hungup = nil
	s.cur.inferDialEdges()
	s.cur.recalcAll()
	if s.onSync != nil {
		nums := make([]string, 0, len(s.cur.exts))
		for n := range s.cur.exts {
			nums = append(nums, n)
		}
		s.onSync(nums)
	}
	s.connected = true
	s.connectedAt = e.At
	s.bumpEpoch()
}

// onInventoryListed reconcilia o estado com a lista completa do PBX SEM mudar o epoch: ramal
// removido vira removed no proximo lote, ramal novo chega pelos EndpointSeen que seguem esta
// lista (delta normal). Durante sincronizacao ou com o AMI fora a lista e ignorada: o
// sync completo traz o inventario inteiro.
func (s *State) onInventoryListed(e model.InventoryListed) {
	if !s.connected || s.building != nil {
		return
	}
	added, removed, kept, pending := s.cur.reconcile(e.Names)
	if removed > 0 && s.onSync != nil {
		s.onSync(s.cur.extNumbers()) // expurga do cache de nomes os ramais que sairam do PBX
	}
	if s.onInventory != nil {
		s.onInventory(e.Reason, added, removed, kept, pending)
	}
}

// onGroups troca catalogo e vinculos manuais SEM mudar o epoch: cada ramal cujo grupo (ou
// group_manual) mudou fica sujo e vai no proximo delta; o catalogo vai junto se mudou.
func (s *State) onGroups(e model.GroupsLoaded) {
	// Copia propria, nunca nil: Batch.Groups == nil significa "catalogo nao mudou", entao um catalogo
	// que ficou vazio (ultimo grupo apagado) precisa sair como [] e nao como nil.
	catalog := append([]model.Group{}, e.Groups...)
	if !sameCatalog(s.catalog, catalog) {
		s.groupsChanged = true
	}
	var flat []model.GroupRange
	for _, g := range catalog {
		for _, r := range g.Ranges {
			flat = append(flat, model.GroupRange{Start: r.Start, End: r.End, Name: g.Name})
		}
	}
	s.catalog, s.groups, s.manual = catalog, sortedRanges(flat), e.Manual
	s.cur.setGroups(s.groups, s.manual)
	if s.building != nil {
		s.building.setGroups(s.groups, s.manual)
	}
}

func sameCatalog(a, b []model.Group) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || len(a[i].Ranges) != len(b[i].Ranges) {
			return false
		}
		for j := range a[i].Ranges {
			if a[i].Ranges[j] != b[i].Ranges[j] {
				return false
			}
		}
	}
	return true
}

func (s *State) newView() *view {
	v := newView(s.pattern, s.groups, s.manual)
	v.hints, v.onName, v.onNoPeer = s.hints, s.onName, s.onNoPeer
	v.onPrune = func() {
		if s.onSync != nil {
			s.onSync(v.extNumbers())
		}
	}
	return v
}

func (s *State) onNames(e model.NamesLoaded) {
	for n, name := range e.Names {
		if name = model.CleanName(name); name != "" { // cache antigo pode ter nome sem limpeza
			s.hints[n] = name
		}
	}
	s.cur.applyHints()
	if s.building != nil {
		s.building.applyHints()
	}
}

func sortedRanges(in []model.GroupRange) []model.GroupRange {
	out := append([]model.GroupRange(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// Flush fecha o lote da janela: um Reset (se houve epoch novo), um Batch (se
// houve mudanca) ou nil. Chamado so pela goroutine dona.
func (s *State) Flush() model.Output {
	defer s.publishStats()
	s.cur.expireNoPeer(s.now())
	if s.needReset {
		s.needReset = false
		// o Reset ja leva o catalogo
		s.groupsChanged = false
		s.cur.takeChanges() // o Reset ja carrega tudo; a versao so avanca 1 por epoch
		s.cur.takeRemoved()
		s.version++
		s.cur.dirty = map[string]struct{}{}
		return model.Reset{
			Epoch: s.epoch, Version: s.version, AMIConnected: s.connected,
			Technology: s.cur.tech, Extensions: s.cur.allExtensions(), Groups: append([]model.Group{}, s.catalog...),
		}
	}
	n := s.cur.takeChanges()
	if !s.groupsChanged && (n == 0 || len(s.cur.dirty) == 0 && len(s.cur.removed) == 0) {
		return nil
	}
	if n == 0 {
		n = 1 // so o catalogo mudou: a versao avanca mesmo assim, senao quem reconecta nao recebe o replay
	}
	b := model.Batch{Epoch: s.epoch, FromVersion: s.version, AMIConnected: s.connected,
		Extensions: s.cur.dirtyExtensions(), Removed: s.cur.takeRemoved()}
	if s.groupsChanged {
		b.Groups, s.groupsChanged = append([]model.Group{}, s.catalog...), false
	}
	s.version += n
	b.ToVersion = s.version
	return b
}

func (s *State) publishStats() {
	now := s.now()
	var total uint64
	for _, b := range s.rate {
		if b.sec > now.Unix()-60 {
			total += b.n
		}
	}
	st := &Stats{
		Epoch: s.epoch, Version: s.version, AMIConnected: s.connected, Technology: s.cur.tech,
		AsteriskVersion: s.cur.asterisk, ConnectedSince: s.connectedAt, LastDisconnect: s.lastDown,
		Trunks: len(s.cur.trunks), EventsPerSec: float64(total) / 60, LagMs: s.lagMs,
	}
	st.Total, st.ByGroup = s.cur.counts(s.catalog)
	st.ActiveCalls = s.cur.activeCalls()
	s.stats.Store(st)
}
