// Package queues traduz os eventos de fila e agente do AMI e le a QueueStatus (rc.8, ADR-008, CONTRATOS
// 7.2, 11.6). E comum a sip e pjsip. So le: a unica action usada e QueueStatus (allowlist em internal/ami).
// Importa so internal/ami e internal/model (CONTRATOS 12).
package queues

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/model"
)

// maxIfaceRunes e o teto do texto de interface de membro (Local/2001@from-queue/n cabe com folga).
const maxIfaceRunes = 128

func clean(s string) string {
	s = strings.TrimSpace(s)
	if s == "<unknown>" || s == "<Unknown>" {
		return ""
	}
	return model.CleanName(s)
}

func cleanKey(s string, max int) string { return model.CleanText(strings.TrimSpace(s), max) }

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// flag le "1"/"0" (e "true"/"false") do AMI.
func flag(s string) bool {
	s = strings.TrimSpace(s)
	return s == "1" || strings.EqualFold(s, "true") || strings.EqualFold(s, "yes")
}

// member traduz os campos de membro de um evento (Interface, MemberName) ou de um item da QueueStatus
// (Location, Name). Os dois formatos sao aceitos em cada caso.
func member(m ami.Message) model.QueueMember {
	return model.QueueMember{
		Interface:      cleanKey(first(m.Get("Interface"), m.Get("Location")), maxIfaceRunes),
		StateInterface: cleanKey(m.Get("StateInterface"), maxIfaceRunes),
		Name:           clean(first(m.Get("MemberName"), m.Get("Name"))),
		PausedReason:   clean(first(m.Get("PausedReason"), m.Get("Reason"))),
		Status:         atoi(m.Get("Status")),
		CallsTaken:     atoi(m.Get("CallsTaken")),
		Paused:         flag(m.Get("Paused")),
		InCall:         flag(m.Get("InCall")),
	}
}

func first(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// Translate traduz um evento ao vivo de fila ou agente. Devolve nil para outro evento e para os que o
// painel le e descarta (AgentCalled, AgentComplete, AgentRingNoAnswer: o estado do agente vem do
// QueueMemberStatus; o supervisor agenda a QueueStatus da fila no AgentComplete, CONTRATOS 11.6).
func Translate(m ami.Message) []model.Event {
	at := m.At
	q := cleanKey(m.Get("Queue"), 64)
	if model.IgnoredQueue(q) { // fila "default" do queues.conf de exemplo: nunca entra no estado (ADR-011 decisao 9)
		return nil
	}
	switch m.Event() {
	case "QueueCallerJoin":
		return []model.Event{model.QueueCallerJoined{At: at, Queue: q, Uniqueid: m.Get("Uniqueid"),
			CallerNum: clean(m.Get("CallerIDNum")), CallerName: clean(m.Get("CallerIDName"))}}
	case "QueueCallerLeave", "QueueCallerAbandon":
		return []model.Event{model.QueueCallerLeft{At: at, Queue: q, Uniqueid: m.Get("Uniqueid")}}
	case "QueueMemberStatus", "QueueMemberPause", "QueueMemberAdded":
		return []model.Event{model.QueueMemberChanged{At: at, Queue: q, Member: member(m)}}
	case "QueueMemberRemoved":
		return []model.Event{model.QueueMemberRemoved{At: at, Queue: q, Interface: cleanKey(m.Get("Interface"), maxIfaceRunes)}}
	case "AgentConnect":
		return []model.Event{model.QueueAgentConnected{At: at, Queue: q, Uniqueid: m.Get("Uniqueid")}}
	}
	return nil
}

// QueueOf devolve a fila que um evento de fila cita ("" se nao e evento de fila). O supervisor a usa para
// marcar a fila pendente de QueueStatus (QueueCallerLeave, QueueCallerAbandon, AgentComplete).
func QueueOf(m ami.Message) string { return cleanKey(m.Get("Queue"), 64) }

// NeedsRefresh diz se o evento pede uma QueueStatus da fila dele: o chamador saiu (QueueCallerLeave,
// QueueCallerAbandon) ou a ligacao do agente acabou (AgentComplete); sao os eventos que mudam os contadores.
func NeedsRefresh(event string) bool {
	switch event {
	case "QueueCallerLeave", "QueueCallerAbandon", "AgentComplete":
		return true
	}
	return false
}

// unsupported diz se err e a resposta de action desconhecida (modulo de filas nao carregado).
func unsupported(err error) bool {
	var ae *ami.ActionError
	if !errors.As(err, &ae) {
		return false
	}
	return strings.Contains(strings.ToLower(ae.Message), "invalid/unknown command")
}

// Status roda a QueueStatus e devolve a lista. queue == "": todas as filas (Full = true); senao so a fila
// pedida (Full = false, Queue = ela; sem QueueParams na resposta a fila nao existe mais). Na completa, a
// resposta "Invalid/unknown command" volta como Unsupported = true, sem erro (modulo de filas ausente).
// Qualquer outro erro (ou prazo do ctx) volta como erro: o estado nao muda.
func Status(ctx context.Context, c *ami.Client, queue string) (model.QueuesListed, error) {
	var params map[string]string
	if queue != "" {
		params = map[string]string{"Queue": queue}
	}
	r, err := c.Do(ctx, "QueueStatus", params)
	out := model.QueuesListed{At: time.Now(), Full: queue == "", Queue: queue}
	if err != nil {
		if queue == "" && unsupported(err) {
			out.Unsupported = true
			return out, nil
		}
		return model.QueuesListed{}, fmt.Errorf("QueueStatus: %w", err)
	}
	out.Queues, out.IgnoredDefault = parse(r.Items)
	return out, nil
}

// parse monta as filas da lista da QueueStatus: cada QueueParams abre uma fila; QueueMember e QueueEntry
// entram na fila do campo Queue (fila sem QueueParams e ignorada).
// A fila "default" (model.IgnoredQueue) e pulada com todos os itens dela e marca ignored.
func parse(items []ami.Message) (out []model.QueueSnapshot, ignored bool) {
	idx := map[string]int{}
	for _, it := range items {
		q := cleanKey(it.Get("Queue"), 64)
		if model.IgnoredQueue(q) {
			ignored = true
			continue
		}
		switch it.Event() {
		case "QueueParams":
			if q == "" {
				continue
			}
			idx[q] = len(out)
			out = append(out, model.QueueSnapshot{
				Queue: q, Completed: atoi(it.Get("Completed")), Abandoned: atoi(it.Get("Abandoned")),
				ServiceLevel: atoi(it.Get("ServiceLevel")), ServiceLevelPerf: perf(it.Get("ServicelevelPerf")),
			})
		case "QueueMember":
			if i, ok := idx[q]; ok {
				out[i].Members = append(out[i].Members, member(it))
			}
		case "QueueEntry":
			if i, ok := idx[q]; ok {
				out[i].Entries = append(out[i].Entries, model.QueueEntry{
					Uniqueid: it.Get("Uniqueid"), CallerNum: clean(it.Get("CallerIDNum")), CallerName: clean(it.Get("CallerIDName")),
					Wait: time.Duration(atoi(it.Get("Wait"))) * time.Second,
				})
			}
		}
	}
	return out, ignored
}

func perf(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 {
		return 0
	}
	return f
}
