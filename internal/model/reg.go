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
	Signal    string
}

// RegCounts acumula, desde a subida do daemon, as ocorrencias contadas (system = 0).
type RegCounts struct{ Lost, Unreachable uint64 }

// RegStatus e a foto da fila do gravador de registros (vai para /api/health).
type RegStatus struct {
	QueueLen, QueueCap int
	Dropped            uint64
	WriteErrors        uint64
}
