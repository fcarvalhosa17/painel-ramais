package model

import "time"

// Tipos de ocorrencia do relatorio de registro (ADR-007), no formato gravado em reg_events.kind.
const (
	RegKindLost        = "lost_registration"  // registered -> unregistered
	RegKindRegistered  = "registered"         // unregistered -> registered
	RegKindUnreachable = "became_unreachable" // registered -> unreachable
	RegKindReachable   = "reachable_again"    // unreachable -> registered
)

// Sinais do sistema (reg_system_events.kind e AsteriskSignal.Kind).
const (
	SignalShutdown    = "shutdown"
	SignalFullyBooted = "fully_booted"
	SignalAMIConnect  = "ami_connected"
)

// RegNote e o que o estado entrega ao gravador do relatorio (CONTRATOS 11.5): uma ocorrencia (Kind
// preenchido, opcionalmente com DownFrom quando ela fecha um intervalo fora do ar), um fechamento
// de intervalo sem ocorrencia (Kind vazio, DownFrom nao zero, At = fim) ou um sinal do sistema
// (Signal preenchido, Number vazio). IP e Port so existem quando o endereco e IPv4.
type RegNote struct {
	At        time.Time
	Number    string
	Name      string
	Kind      string
	System    bool
	IP        string
	Port      int
	UserAgent string
	DownFrom  time.Time
	DownGap   bool // rc.9: o trecho fechado foi cortado porque o painel ou o AMI estavam parados (reg_hourly.down_gap)
	Signal    string
	State     *RegState // rc.9: nota de estado (Number preenchido, o resto vazio): upsert em reg_state
}

// RegState e o estado "fora do ar" e ultimo registro de um ramal (ADR-010, linha de reg_state). Hora zero = nulo.
// DownKind: observed, unknown_start ou before_install; vazio junto com DownSince zero.
type RegState struct {
	DownSince    time.Time
	DownSeg      time.Time
	LastReg      time.Time
	DownKind     string
	LastRegExact bool
}

// Tipos de "fora do ar" (reg_state.down_kind).
const (
	DownObserved      = "observed"
	DownUnknownStart  = "unknown_start"
	DownBeforeInstall = "before_install"
)

// Motivos de uma decisao de contagem (CONTRATOS 11.5, item 4).
const (
	ReasonCounted       = "contada"
	ReasonPause         = "pausa"
	ReasonFirstState    = "primeiro_estado"
	ReasonReread        = "releitura"
	ReasonReconcile     = "reconciliacao"
	ReasonOutOfTable    = "fora_da_tabela"
	PauseAsteriskReboot = "asterisk_restart" // pause_reason da 4.4 e da 4.7
)

// RegDecision e uma decisao de contagem do estado: uma por transicao de registration na visao publicada e por
// mudanca ok/fora da reconciliacao. Vai ao log por um canal sem bloqueio (internal/reglog/decisions.go).
type RegDecision struct {
	At       time.Time
	Number   string
	From, To Registration
	Counted  bool
	Reason   string
}

// RegCounts acumula, desde a subida do daemon, as ocorrencias contadas (system = 0).
type RegCounts struct{ Lost, Unreachable uint64 }

// RegStatus e a foto da fila do gravador de registros (vai para /api/health). rc.8: WriteErrors conta
// sublotes perdidos; LostEvents = notas perdidas neles, desde a subida; LostSince e LostUntil = menor e maior At
// entre elas (zero = nenhuma perda).
type RegStatus struct {
	QueueLen, QueueCap int
	Dropped            uint64
	WriteErrors        uint64
	LostEvents         uint64
	LostSince          time.Time
	LostUntil          time.Time
	// rc.9: DecisionLines = linhas Info de decisao escritas; DecisionSummarized = motivo -> decisoes que ficaram so
	// no resumo (limite por segundo ou canal cheio), so motivos acima de zero (copia).
	DecisionLines      uint64
	DecisionSummarized map[string]uint64
}

// QueueHealth e a foto da leitura de filas (QueueStatus) para o /api/health (rc.8, CONTRATOS 4.4): LastFullAt =
// ultima QueueStatus completa lida com sucesso (zero = nenhuma); RefreshErrors = QueueStatus (completa ou por
// fila) que falharam desde a subida; Unsupported = a ultima completa respondeu que a action nao existe.
type QueueHealth struct {
	LastFullAt    time.Time
	RefreshErrors uint64
	Unsupported   bool
	// rc.9 (ADR-011 decisao 3 e 7): FullReads = completas lidas com sucesso; LastFullMs = duracao da ultima; FullMinGap =
	// intervalo minimo atual entre completas; HeldDropped = eventos de fila guardados e descartados por passar do teto;
	// EventsReceived = nome do evento -> quantidade, contado antes de qualquer descarte (copia).
	FullReads      uint64
	LastFullMs     int64
	FullMinGap     time.Duration
	HeldDropped    uint64
	EventsReceived map[string]uint64
}
