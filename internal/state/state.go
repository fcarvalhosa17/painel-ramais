// Package state e o dono unico do estado em memoria: ramais, canais, bridges,
// versao e epoch. Tudo roda numa goroutine (Run); nada aqui conhece AMI nem HTTP.
package state

import (
	"context"
	"fmt"
	"regexp"
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
	// OnReg recebe as ocorrencias do relatorio de registro (CONTRATOS 11.5), na goroutine do estado: so pode
	// enfileirar, nunca bloquear nem tocar em banco.
	OnReg func(model.RegNote)
	// OnLocalNoPeer e chamado (na goroutine do estado, sem bloquear) uma vez por chamada que tem canal Local e
	// segue 1 s sem o outro lado (CONTRATOS 2.3): uniqueid e nome do canal Local e o Linkedid da chamada.
	OnLocalNoPeer func(uniqueid, linkedid, channel string)
	// OnDecision recebe cada decisao de contagem de registro (rc.9, CONTRATOS 11.5 item 4), na goroutine do estado: so
	// pode enfileirar, nunca bloquear nem tocar em banco.
	OnDecision func(model.RegDecision)
	// OnInfo recebe as poucas linhas de log Info da pausa da contagem (CONTRATOS 11.5 item 2); acontecem raramente.
	OnInfo func(msg string, args ...any)
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
	ByGroup         map[string]model.Counts // inclui grupos sem ramal e "Sem grupo"; o ramal conta em cada grupo dele (N:N)
	// RegTotal e RegByGroup acumulam, desde a subida, as ocorrencias de registro contadas (system = 0);
	// o ramal soma em cada grupo dele no momento da ocorrencia. SuppressedUntil: fim da janela de evento
	// do sistema em curso (zero = sem janela).
	RegTotal        model.RegCounts
	RegByGroup      map[string]model.RegCounts
	SuppressedUntil time.Time
	// PauseReason (rc.9): "asterisk_restart" com a pausa em curso, senao "".
	PauseReason string
	// LiveCalls e Queues (rc.8): linhas da projecao de chamadas e filas na projecao (health live_calls e queues.count).
	LiveCalls    int
	Queues       int
	Trunks       int
	ActiveCalls  int
	EventsPerSec float64
	LagMs        int64 // atraso entre a leitura do evento e seu processamento
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
	gi            *groupIdx     // resolucao de grupos (faixas por grupo + vinculos manuais); imutavel, so trocada
	catalog       []model.Group // catalogo completo, inclui grupo sem faixa e sem ramal
	groupsChanged bool          // o catalogo mudou desde o ultimo lote: o proximo Batch o leva
	rc            *regCtx       // relatorio de registro (janela de sistema, contadores, saida)
	proj          *callProj     // projecao de chamadas ao vivo por Linkedid (rc.8)
	qproj         *queueProj    // projecao de filas (rc.8)
	// rc.9 (ADR-010): reg_state lido na subida, guardado ate a primeira SyncFinished (so ela o usa).
	rsLoaded    *model.RegStateLoaded
	regSyncDone bool // a primeira sincronizacao do processo ja reconciliou o estado "fora do ar"
	hints       map[string]string
	onName      func(number, name string)
	onSync      func(numbers []string)
	onNoPeer    func(uniqueid, linkedid string)
	onInventory func(reason string, added, removed, kept, pending int)
	epochBase   int64
	epochSeq    int
	epoch       string
	version     uint64
	connected   bool
	connectedAt time.Time
	lastDown    time.Time
	needReset   bool
	rate        [60]struct {
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
	s.gi = newGroupIdx(nil, nil)
	s.rc = newRegCtx(s.now, opt)
	s.proj, s.qproj = newCallProj(opt.OnLocalNoPeer), newQueueProj()
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
	case model.AsteriskSignal:
		s.rc.onSignal(e) // Shutdown, ou FullyBooted de reinicio real (Uptime): pausa da contagem
	case model.RegStateLoaded:
		if !s.regSyncDone {
			s.rsLoaded = &e
		}
	case model.SyncStarted:
		s.rc.syncReason = e.Reason
		if e.Reason == model.SyncConnect {
			s.rc.noUptimeLogged = false // o aviso "sem Uptime" sai uma vez por sessao AMI
		}
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
	case model.QueueNamesLoaded:
		s.applyQueueNames(e)
	case model.QueueCallerJoined, model.QueueCallerLeft, model.QueueMemberChanged, model.QueueMemberRemoved,
		model.QueueAgentConnected, model.QueuesListed:
		if !s.connected && s.building == nil {
			return // fila lida de sessao que ja caiu
		}
		s.applyQueue(ev)
	default:
		if !s.connected && s.building == nil {
			return // evento solto de sessao que ja caiu (ex.: Enrich atrasado): nao cria estado com o AMI fora
		}
		v := s.target()
		v.at = at
		if v != s.cur { // visao em montagem: a projecao inteira e refeita no SyncFinished
			v.apply(ev)
			return
		}
		s.applyPublished(v, ev)
	}
}

// applyPublished aplica o evento na visao publicada e marca como sujos so os Linkedid que ele pode mudar
// (antes e depois: o canal pode trocar de Linkedid, de bridge ou sumir). Ramal criado ou removido muda o
// inventario, que decide ramal ou externo: marca todas as chamadas e filas (CONTRATOS 11.6).
func (s *State) applyPublished(v *view, ev model.Event) {
	if h, ok := ev.(model.ChannelHungup); ok {
		s.onHangupQueue(h.Uniqueid) // antes: o canal ainda existe para marcar a chamada dele
	}
	s.proj.touch(v, ev)
	n0 := len(v.exts)
	v.apply(ev)
	s.proj.touch(v, ev)
	if len(v.exts) != n0 {
		s.proj.allDirty, s.qproj.allDirty = true, true
	}
}

// onHangupQueue tira do indice de filas o canal que desligou: nao fica mais "via fila" nem aguardando (o
// QueueCallerLeave que viria depois e idempotente).
func (s *State) onHangupQueue(uid string) {
	delete(s.proj.viaQueue, uid)
	if q := s.proj.entryOf[uid]; q != "" {
		s.dropEntry(q, uid)
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
	s.proj.reset() // AMI fora: a lista de chamadas e a de filas ficam vazias (o Reset leva [] nas duas)
	s.qproj.reset()
	s.bumpEpoch()
}

func (s *State) onSyncFinished(e model.SyncFinished) {
	if s.building == nil {
		return
	}
	old := s.cur
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
	s.rebuildCalls()                          // projecao de chamadas refeita da visao nova (o Reset leva tudo)
	s.qproj.allDirty = true                   // o inventario pode ter mudado: filas remontadas
	s.reconcileRegState(old, e.At)            // estado "fora do ar" reconciliado (substitui o carryDown)
	if s.rc.syncReason == model.SyncConnect { // conexao nova (inclusive a primeira): so o sinal informativo, sem pausa
		s.rc.emit(model.RegNote{At: e.At, Signal: model.SignalAMIConnect})
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
	s.cur.at = e.At
	n0 := len(s.cur.exts)
	added, removed, kept, pending := s.cur.reconcile(e.Names)
	if len(s.cur.exts) != n0 { // inventario mudou: ramal ou externo muda nas chamadas e nos agentes das filas
		s.proj.allDirty, s.qproj.allDirty = true, true
	}
	if removed > 0 && s.onSync != nil {
		s.onSync(s.cur.extNumbers()) // expurga do cache de nomes os ramais que sairam do PBX
	}
	if s.onInventory != nil {
		s.onInventory(e.Reason, added, removed, kept, pending)
	}
}

// onGroups troca catalogo e vinculos manuais SEM mudar o epoch: cada ramal cujo groups, range_groups
// ou manual_groups mudou fica sujo e vai no proximo delta; o catalogo vai junto se mudou.
func (s *State) onGroups(e model.GroupsLoaded) {
	// Copia propria, nunca nil: Batch.Groups == nil significa "catalogo nao mudou", entao um catalogo
	// que ficou vazio (ultimo grupo apagado) precisa sair como [] e nao como nil.
	catalog := append([]model.Group{}, e.Groups...)
	if !sameCatalog(s.catalog, catalog) {
		s.groupsChanged = true
	}
	s.catalog, s.gi = catalog, newGroupIdx(catalog, e.Manual)
	s.cur.setGroups(s.gi)
	if s.building != nil {
		s.building.setGroups(s.gi)
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
	v := newView(s.pattern, s.gi)
	v.rc = s.rc
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

// Flush fecha o lote da janela: um Reset (se houve epoch novo), um Batch (se
// houve mudanca) ou nil. Chamado so pela goroutine dona.
func (s *State) Flush() model.Output {
	defer s.publishStats()
	s.cur.expireNoPeer(s.now())
	calls, callsGone := s.flushCalls()
	queues, queuesGone := s.flushQueues()
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
			Calls: s.allCalls(), Queues: s.allQueues(),
		}
	}
	n := s.cur.takeChanges()
	extChanged := n > 0 && (len(s.cur.dirty) > 0 || len(s.cur.removed) > 0)
	proj := len(calls) + len(callsGone) + len(queues) + len(queuesGone)
	if !s.groupsChanged && !extChanged && proj == 0 { // o lote sai se qualquer das listas tiver item
		return nil
	}
	if !extChanged {
		n = 0
	}
	if n == 0 && proj == 0 {
		n = 1 // so o catalogo mudou: a versao avanca mesmo assim, senao quem reconecta nao recebe o replay
	}
	b := model.Batch{Epoch: s.epoch, FromVersion: s.version, AMIConnected: s.connected,
		Extensions: s.cur.dirtyExtensions(), Removed: s.cur.takeRemoved(),
		Calls: calls, CallsRemoved: callsGone, Queues: queues, QueuesRemoved: queuesGone}
	if s.groupsChanged {
		b.Groups, s.groupsChanged = append([]model.Group{}, s.catalog...), false
	}
	s.version += n + uint64(proj) // cada item das quatro listas novas conta uma mudanca
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
	st.RegTotal, st.RegByGroup, st.SuppressedUntil = s.rc.total, s.rc.byGroupCopy(), s.rc.suppressedUntil(now)
	if !st.SuppressedUntil.IsZero() {
		st.PauseReason = model.PauseAsteriskReboot
	}
	st.ActiveCalls = s.cur.activeCalls()
	st.LiveCalls, st.Queues = len(s.proj.calls), len(s.qproj.queues)
	s.stats.Store(st)
}
