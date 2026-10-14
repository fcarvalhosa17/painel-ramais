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
	groups        []model.GroupRange
	hints         map[string]string
	onName        func(number, name string)
	onSync        func(numbers []string)
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
	s := &State{pattern: pattern, in: in, hints: map[string]string{}, onName: opt.OnName, onSync: opt.OnSync, out: make(chan model.Output, 64), now: opt.Now, interval: opt.Interval}
	if s.now == nil {
		s.now = time.Now
	}
	if s.interval <= 0 {
		s.interval = 250 * time.Millisecond
	}
	s.epochBase = s.now().UnixMilli()
	s.cur = s.newView(nil)
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
		s.building = s.newView(s.groups)
		s.building.isBuilding = true
		s.building.tech = e.Technology
		s.building.asterisk = e.AsteriskVersion
	case model.SyncFinished:
		s.onSyncFinished(e)
	case model.GroupsLoaded:
		s.onGroups(e)
	case model.NamesLoaded:
		s.onNames(e)
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

func (s *State) onGroups(e model.GroupsLoaded) {
	s.groups = sortedRanges(e.Ranges)
	s.cur.setGroups(s.groups)
	if s.building != nil {
		s.building.setGroups(s.groups)
	}
	s.bumpEpoch()
}

func (s *State) newView(groups []model.GroupRange) *view {
	v := newView(s.pattern, groups)
	v.hints, v.onName = s.hints, s.onName
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
	if s.needReset {
		s.needReset = false
		s.cur.takeChanges() // o Reset ja carrega tudo; a versao so avanca 1 por epoch
		s.version++
		s.cur.dirty = map[string]struct{}{}
		return model.Reset{
			Epoch: s.epoch, Version: s.version, AMIConnected: s.connected,
			Technology: s.cur.tech, Extensions: s.cur.allExtensions(), Groups: append([]model.GroupRange(nil), s.groups...),
		}
	}
	n := s.cur.takeChanges()
	if n == 0 || len(s.cur.dirty) == 0 {
		return nil
	}
	b := model.Batch{Epoch: s.epoch, FromVersion: s.version, AMIConnected: s.connected, Extensions: s.cur.dirtyExtensions()}
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
	st.Total, st.ByGroup = s.cur.counts(s.groups)
	st.ActiveCalls = s.cur.activeCalls()
	s.stats.Store(st)
}
