package state

import (
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"painel-ramais/internal/model"
)

// Projecao de filas (rc.8, ADR-008 e ADR-009, CONTRATOS 2.4 e 11.6). Mora no State, fora da view. Eventos de
// fila e a QueueStatus alimentam; cada fila alterada fica suja e o Flush a remonta inteira.

type qEntry struct {
	number, name string
	since        time.Time
}

type queueRec struct {
	completed, abandoned, level int
	perf                        float64
	statsAt                     time.Time // zero = a fila so apareceu por evento
	entries                     map[string]*qEntry
	members                     map[string]model.QueueMember
	pub                         model.Queue
	published                   bool
}

// queueProj reune as filas, os nomes dados pelo painel e as filas sujas.
type queueProj struct {
	queues   map[string]*queueRec
	names    map[string]string
	dirty    map[string]struct{}
	pubNames map[string]struct{} // filas que o cliente ja recebeu (so elas vao em queues_removed)
	allDirty bool
}

func newQueueProj() *queueProj {
	return &queueProj{queues: map[string]*queueRec{}, names: map[string]string{}, dirty: map[string]struct{}{},
		pubNames: map[string]struct{}{}}
}

// reset esvazia as filas (queda do AMI); os nomes ficam (vem do banco do painel).
func (q *queueProj) reset() {
	q.queues, q.dirty, q.pubNames, q.allDirty = map[string]*queueRec{}, map[string]struct{}{}, map[string]struct{}{}, false
}

func newQueueRec() *queueRec {
	return &queueRec{entries: map[string]*qEntry{}, members: map[string]model.QueueMember{}}
}

func (s *State) queueRecFor(name string) *queueRec {
	q := s.qproj
	r := q.queues[name]
	if r == nil {
		r = newQueueRec()
		q.queues[name] = r
	}
	q.dirty[name] = struct{}{}
	return r
}

// markCallOf marca a chamada do canal uid (se ele existe na visao publicada).
func (s *State) markCallOf(uid string) {
	if c := s.cur.chans[uid]; c != nil {
		s.proj.markLinked(c.linkedID)
	}
}

func (s *State) dropEntry(queue, uid string) {
	if queue == "" {
		queue = s.proj.entryOf[uid]
	}
	if r := s.qproj.queues[queue]; r != nil {
		if _, ok := r.entries[uid]; ok {
			delete(r.entries, uid)
			s.qproj.dirty[queue] = struct{}{}
		}
	}
	if s.proj.entryOf[uid] == queue {
		delete(s.proj.entryOf, uid)
	}
	s.markCallOf(uid)
}

// applyQueue aplica um evento de fila (idempotente, CONTRATOS 11.6).
func (s *State) applyQueue(ev model.Event) {
	switch e := ev.(type) {
	case model.QueueCallerJoined:
		if e.Queue == "" || e.Uniqueid == "" {
			return
		}
		if old := s.proj.entryOf[e.Uniqueid]; old != "" && old != e.Queue {
			s.dropEntry(old, e.Uniqueid)
		}
		r := s.queueRecFor(e.Queue)
		if en := r.entries[e.Uniqueid]; en != nil {
			en.number, en.name = e.CallerNum, e.CallerName // repetido: atualiza, mantem o since
		} else {
			r.entries[e.Uniqueid] = &qEntry{number: e.CallerNum, name: e.CallerName, since: e.At}
		}
		s.proj.entryOf[e.Uniqueid] = e.Queue
		s.markCallOf(e.Uniqueid)
	case model.QueueCallerLeft:
		s.dropEntry(e.Queue, e.Uniqueid)
	case model.QueueMemberChanged:
		if e.Queue == "" || e.Member.Interface == "" {
			return
		}
		s.queueRecFor(e.Queue).members[e.Member.Interface] = e.Member
	case model.QueueMemberRemoved:
		if r := s.qproj.queues[e.Queue]; r != nil {
			if _, ok := r.members[e.Interface]; ok {
				delete(r.members, e.Interface)
				s.qproj.dirty[e.Queue] = struct{}{}
			}
		}
	case model.QueueAgentConnected:
		if e.Queue != "" && e.Uniqueid != "" {
			s.proj.viaQueue[e.Uniqueid] = e.Queue
			s.markCallOf(e.Uniqueid)
		}
	case model.QueuesListed:
		s.applyQueuesListed(e)
	}
}

// applyQueuesListed aplica a resposta da QueueStatus: completa substitui todas as filas (as ausentes saem);
// por fila substitui so ela (sem QueueParams na resposta, a fila sai); Unsupported esvazia a lista.
func (s *State) applyQueuesListed(e model.QueuesListed) {
	q := s.qproj
	switch {
	case e.Unsupported:
		for name := range q.queues {
			s.removeQueue(name)
		}
		return
	case e.Full:
		keep := make(map[string]struct{}, len(e.Queues))
		for _, snap := range e.Queues {
			keep[snap.Queue] = struct{}{}
		}
		for name := range q.queues {
			if _, ok := keep[name]; !ok {
				s.removeQueue(name)
			}
		}
		for _, snap := range e.Queues {
			s.replaceQueue(snap, e.At)
		}
	default:
		found := false
		for _, snap := range e.Queues {
			if snap.Queue == e.Queue {
				s.replaceQueue(snap, e.At)
				found = true
			}
		}
		if !found {
			s.removeQueue(e.Queue)
		}
	}
}

// removeQueue tira a fila (e as entradas dela do indice) e suja as chamadas de quem aguardava.
func (s *State) removeQueue(name string) {
	q := s.qproj
	r := q.queues[name]
	if r == nil {
		return
	}
	for uid := range r.entries {
		if s.proj.entryOf[uid] == name {
			delete(s.proj.entryOf, uid)
		}
		s.markCallOf(uid)
	}
	delete(q.queues, name)
	q.dirty[name] = struct{}{}
}

// replaceQueue troca contadores, membros e quem aguarda pela resposta da QueueStatus. A entrada que ja
// existia mantem o since (o do evento e mais exato que agora - Wait).
func (s *State) replaceQueue(snap model.QueueSnapshot, at time.Time) {
	if snap.Queue == "" {
		return
	}
	r := s.queueRecFor(snap.Queue)
	r.completed, r.abandoned, r.level, r.perf, r.statsAt = snap.Completed, snap.Abandoned, snap.ServiceLevel, snap.ServiceLevelPerf, at
	r.members = make(map[string]model.QueueMember, len(snap.Members))
	for _, m := range snap.Members {
		if m.Interface != "" {
			r.members[m.Interface] = m
		}
	}
	entries := make(map[string]*qEntry, len(snap.Entries))
	for _, en := range snap.Entries {
		if en.Uniqueid == "" {
			continue
		}
		if old := r.entries[en.Uniqueid]; old != nil {
			old.number, old.name = en.CallerNum, en.CallerName
			entries[en.Uniqueid] = old
		} else {
			entries[en.Uniqueid] = &qEntry{number: en.CallerNum, name: en.CallerName, since: at.Add(-en.Wait)}
		}
	}
	for uid := range r.entries { // saiu da fila desde o ultimo estado
		if _, still := entries[uid]; !still {
			if s.proj.entryOf[uid] == snap.Queue {
				delete(s.proj.entryOf, uid)
			}
			s.markCallOf(uid)
		}
	}
	for uid := range entries {
		s.proj.entryOf[uid] = snap.Queue
		s.markCallOf(uid)
	}
	r.entries = entries
}

// applyQueueNames troca os nomes dados pelo painel; so as filas cujo nome mudou ficam sujas.
func (s *State) applyQueueNames(e model.QueueNamesLoaded) {
	q := s.qproj
	next := make(map[string]string, len(e.Names))
	for k, v := range e.Names {
		next[k] = v
	}
	for name := range q.queues {
		if q.names[name] != next[name] {
			q.dirty[name] = struct{}{}
		}
	}
	q.names = next
}

// ---- montagem ----

// extensionOf extrai o ramal do inventario de "Local/2001@from-queue/n", "PJSIP/2001" ou "hint:2001@ext-local": o
// texto entre a primeira "/" ou ":" e a primeira "@" ou "/" seguinte. "" se nao for ramal do inventario.
func (s *State) extensionOf(iface string) string {
	i := strings.IndexAny(iface, "/:")
	if i < 0 {
		return ""
	}
	rest := iface[i+1:]
	if j := strings.IndexAny(rest, "@/"); j >= 0 {
		rest = rest[:j]
	}
	if rest != "" && s.cur.exts[rest] != nil {
		return rest
	}
	return ""
}

func memberState(m model.QueueMember) model.MemberState {
	switch {
	case m.Status == 4 || m.Status == 5:
		return model.MemberUnavailable
	case m.InCall || m.Status == 2 || m.Status == 3 || (m.Status >= 6 && m.Status <= 8):
		return model.MemberBusy
	case m.Paused:
		return model.MemberPaused
	}
	return model.MemberFree
}

// assembleQueue monta a fila inteira (entradas por since, membros por interface).
func (s *State) assembleQueue(name string, r *queueRec) model.Queue {
	out := model.Queue{
		Queue: name, Name: model.StrPtr(s.qproj.names[name]), Completed: r.completed, Abandoned: r.abandoned,
		ServiceLevelS: r.level, ServiceLevelPerf: math.Round(r.perf*10) / 10,
		Entries: make([]model.QueueEntryView, 0, len(r.entries)), Members: make([]model.QueueMemberView, 0, len(r.members)),
	}
	if !r.statsAt.IsZero() {
		t := model.NewStamp(r.statsAt)
		out.StatsAt = &t
	}
	uids := make([]string, 0, len(r.entries))
	for uid := range r.entries {
		uids = append(uids, uid)
	}
	sort.Slice(uids, func(i, j int) bool {
		a, b := r.entries[uids[i]], r.entries[uids[j]]
		if !a.since.Equal(b.since) {
			return a.since.Before(b.since)
		}
		return uids[i] < uids[j]
	})
	for i, uid := range uids {
		en := r.entries[uid]
		out.Entries = append(out.Entries, model.QueueEntryView{Uniqueid: uid, Position: i + 1,
			Number: model.StrPtr(en.number), Name: model.StrPtr(en.name), Since: model.NewStamp(en.since)})
	}
	ifaces := make([]string, 0, len(r.members))
	for k := range r.members {
		ifaces = append(ifaces, k)
	}
	sort.Strings(ifaces)
	for _, k := range ifaces {
		m := r.members[k]
		ext := s.extensionOf(m.StateInterface)
		if ext == "" {
			ext = s.extensionOf(m.Interface)
		}
		nm := m.Name
		if nm == "" {
			nm = m.Interface
		}
		out.Members = append(out.Members, model.QueueMemberView{Interface: m.Interface, Name: nm, Extension: model.StrPtr(ext),
			State: memberState(m), Paused: m.Paused, PauseReason: model.StrPtr(m.PausedReason), CallsTaken: m.CallsTaken})
	}
	return out
}

// flushQueues remonta as filas sujas e devolve as que mudaram (ou entraram) e as que sairam, ordenadas.
func (s *State) flushQueues() (changed []model.Queue, removed []string) {
	q := s.qproj
	if q.allDirty {
		for name := range q.queues {
			q.dirty[name] = struct{}{}
		}
		q.allDirty = false
	}
	for name := range q.dirty {
		r := q.queues[name]
		if r == nil { // saiu; so vai em queues_removed se o cliente ja a recebeu
			if _, was := q.pubNames[name]; was {
				delete(q.pubNames, name)
				removed = append(removed, name)
			}
			continue
		}
		v := s.assembleQueue(name, r)
		if !r.published || !reflect.DeepEqual(r.pub, v) {
			r.pub, r.published = v, true
			q.pubNames[name] = struct{}{}
			changed = append(changed, v)
		}
	}
	q.dirty = map[string]struct{}{}
	sort.Slice(changed, func(i, j int) bool { return changed[i].Queue < changed[j].Queue })
	sort.Strings(removed)
	return changed, removed
}

// allQueues devolve todas as filas publicadas (Reset e snapshot), ordenadas por fila.
func (s *State) allQueues() []model.Queue {
	out := make([]model.Queue, 0, len(s.qproj.queues))
	for _, r := range s.qproj.queues {
		if r.published {
			out = append(out, r.pub)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Queue < out[j].Queue })
	return out
}
