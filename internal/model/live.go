package model

import "time"

// Chamadas ao vivo e filas (rc.8, ADR-008, CONTRATOS 2.3, 2.4 e 11.6).

// LiveCallState e o estado de uma chamada ao vivo (CONTRATOS 2.3), em ordem de precedencia.
type LiveCallState string

const (
	LiveQueued  LiveCallState = "queued"
	LiveHold    LiveCallState = "hold"
	LiveTalking LiveCallState = "talking"
	LiveRinging LiveCallState = "ringing"
)

// LiveCall e uma linha da aba Chamadas: uma por chamada (Linkedid), nao por ramal nem por canal. Os
// campos anulaveis sao ponteiros; Extensions nunca e nil no JSON (o estado entrega a lista pronta).
type LiveCall struct {
	ID         string        `json:"id"`
	Direction  Direction     `json:"direction"`
	State      LiveCallState `json:"state"`
	Extensions []string      `json:"extensions"`
	External   *string       `json:"external"`
	Trunk      *string       `json:"trunk"`
	Queue      *string       `json:"queue"`
	StartedAt  Stamp         `json:"started_at"`
}

// MemberState e o estado de um agente de fila (CONTRATOS 2.4).
type MemberState string

const (
	MemberFree        MemberState = "free"
	MemberBusy        MemberState = "busy"
	MemberPaused      MemberState = "paused"
	MemberUnavailable MemberState = "unavailable"
)

// QueueEntryView e quem aguarda numa fila.
type QueueEntryView struct {
	Uniqueid string  `json:"uniqueid"`
	Position int     `json:"position"`
	Number   *string `json:"number"`
	Name     *string `json:"name"`
	Since    Stamp   `json:"since"`
}

// QueueMemberView e um agente da fila.
type QueueMemberView struct {
	Interface   string      `json:"interface"`
	Name        string      `json:"name"`
	Extension   *string     `json:"extension"`
	State       MemberState `json:"state"`
	Paused      bool        `json:"paused"`
	PauseReason *string     `json:"pause_reason"`
	CallsTaken  int         `json:"calls_taken"`
}

// Queue e uma fila do Asterisk como a tela a ve. Entries e Members nunca sao nil no JSON.
type Queue struct {
	Queue            string            `json:"queue"`
	Name             *string           `json:"name"`
	Completed        int               `json:"completed"`
	Abandoned        int               `json:"abandoned"`
	ServiceLevelS    int               `json:"service_level_s"`
	ServiceLevelPerf float64           `json:"service_level_perf"`
	StatsAt          *Stamp            `json:"stats_at"`
	Entries          []QueueEntryView  `json:"entries"`
	Members          []QueueMemberView `json:"members"`
}

// QueueMember e um agente como os eventos e a QueueStatus o trazem (Interface vem de Interface ou Location,
// Name de MemberName ou Name). Status e o enum do app_queue (0 desconhecido, 1 livre, 2 em uso, 3 ocupado, 4
// invalido, 5 indisponivel, 6 tocando, 7 tocando em uso, 8 em espera).
type QueueMember struct {
	Interface, StateInterface, Name, PausedReason string
	Status, CallsTaken                            int
	Paused, InCall                                bool
}

// QueueEntry e um chamador que aguarda, como a QueueStatus o traz (Wait = ha quanto tempo).
type QueueEntry struct {
	Uniqueid, CallerNum, CallerName string
	Wait                            time.Duration
}

// QueueSnapshot e uma fila na resposta da QueueStatus (QueueParams mais os QueueMember e QueueEntry dela).
type QueueSnapshot struct {
	Queue                              string
	Completed, Abandoned, ServiceLevel int
	ServiceLevelPerf                   float64
	Members                            []QueueMember
	Entries                            []QueueEntry
}

// Eventos normalizados de fila (CONTRATOS 11.6). Traduzidos por internal/ami/queues no laco ao vivo.
type (
	QueueCallerJoined struct {
		At                                     time.Time
		Queue, Uniqueid, CallerNum, CallerName string
	}
	// QueueCallerLeft: QueueCallerLeave e QueueCallerAbandon (o chamador saiu da fila).
	QueueCallerLeft struct {
		At              time.Time
		Queue, Uniqueid string
	}
	// QueueMemberChanged: QueueMemberStatus, QueueMemberPause e QueueMemberAdded (insere ou substitui pela Interface).
	QueueMemberChanged struct {
		At     time.Time
		Queue  string
		Member QueueMember
	}
	QueueMemberRemoved struct {
		At               time.Time
		Queue, Interface string
	}
	// QueueAgentConnected: AgentConnect (Uniqueid = canal do chamador; a chamada foi atendida via fila).
	QueueAgentConnected struct {
		At              time.Time
		Queue, Uniqueid string
	}
	// QueuesListed e a resposta da QueueStatus: Full = a lista completa (substitui todas as filas); senao
	// so a fila Queue (sem QueueParams na resposta: a fila sai). Unsupported = a action nao existe (modulo de
	// filas ausente): lista vazia, sem erro.
	QueuesListed struct {
		At          time.Time
		Full        bool
		Queue       string
		Queues      []QueueSnapshot
		Unsupported bool
		// IgnoredDefault: a resposta tinha a fila "default", que foi pulada (IgnoredQueue).
		IgnoredDefault bool
	}
)

func (e QueueCallerJoined) EventTime() time.Time  { return e.At }
func (e QueueCallerLeft) EventTime() time.Time    { return e.At }
func (e QueueMemberChanged) EventTime() time.Time { return e.At }
func (e QueueMemberRemoved) EventTime() time.Time { return e.At }
func (e QueueAgentConnected) EventTime() time.Time {
	return e.At
}
func (e QueuesListed) EventTime() time.Time { return e.At }
