package model

import (
	"context"
	"time"
)

// Event e um evento normalizado, consumido pelo estado numa goroutine unica.
// At e a hora em que o leitor tirou a mensagem do socket.
type Event interface{ EventTime() time.Time }

// SyncReason diz por que uma sincronizacao comecou.
type SyncReason string

const (
	SyncConnect  SyncReason = "connect"
	SyncOverflow SyncReason = "overflow"
)

// ChannelState e o estado de canal reduzido que o estado entende.
type ChannelState string

const (
	ChanDown    ChannelState = "down"
	ChanRing    ChannelState = "ring"
	ChanRinging ChannelState = "ringing"
	ChanUp      ChannelState = "up"
	ChanBusy    ChannelState = "busy"
	ChanOther   ChannelState = "other"
)

// Controle (emitidos pelo supervisor da conexao e pelo cmd).
type (
	AMIDisconnected struct {
		At     time.Time
		Reason string
	}
	SyncStarted struct {
		At              time.Time
		Technology      Technology
		AsteriskVersion string
		Reason          SyncReason
	}
	SyncFinished struct{ At time.Time }
	// NamesLoaded entrega o cache de nomes (numero -> callerid) lido do banco na subida;
	// so vale para ramal que ainda nao tem nome.
	NamesLoaded struct {
		At    time.Time
		Names map[string]string
	}
	// GroupsLoaded entrega o catalogo COMPLETO de grupos (inclui grupo sem ramal e sem faixa) e os
	// vinculos manuais ramal -> grupo (NoGroup = "Sem grupo" manual; ausente = sem vinculo manual).
	// Quem envia nao pode alterar Manual depois de enviar. Nao muda o epoch.
	GroupsLoaded struct {
		At     time.Time
		Groups []Group
		Manual map[string]string
	}
	// InventoryListed e a lista COMPLETA de endpoints/peers do PBX numa releitura (recarga do
	// Asterisk ou ciclo periodico). Vem ANTES dos EndpointSeen/RegistrationChanged da mesma
	// leitura. O estado remove os ramais que nao estao na lista (sem mudar o epoch); os que entram
	// sao criados pelos eventos seguintes. Names inclui troncos: o estado filtra com IsExtension.
	InventoryListed struct {
		At     time.Time
		Reason string // "reload", "periodica" ou "nova tentativa" (so para o log)
		Names  []string
	}
)

// Registro e dispositivo. Name e o nome do endpoint/peer sem prefixo de tecnologia.
type (
	EndpointSeen struct {
		At           time.Time
		Name         string
		CallerIDName string
		Technology   Technology
	}
	// RegistrationChanged substitui State, IP, UserAgent e LatencyMs do ramal.
	RegistrationChanged struct {
		At        time.Time
		Name      string
		State     Registration
		IP        string
		UserAgent string
		LatencyMs *int
	}
	DeviceStateChanged struct {
		At    time.Time
		Name  string
		State DeviceState
	}
)

// Chamadas, em nivel de canal.
type (
	ChannelCreated struct {
		At         time.Time
		Uniqueid   string
		Linkedid   string
		Channel    string // nome completo, ex.: PJSIP/2001-0000002a ou Local/x@ctx-0000001a;1
		Endpoint   string // vazio se Local ou outro tipo
		IsLocal    bool
		CallerNum  string
		CallerName string
		Exten      string
		Up         bool
		BridgeID   string        // so no snapshot (CoreShowChannel)
		Duration   time.Duration // so no snapshot (CoreShowChannel)
		// So no snapshot: o Asterisk marca o canal destino de um Dial com Application=AppDial
		// ("(Outgoing Line)") e o canal que executa o Dial com Application=Dial + os argumentos.
		Dialed   bool
		DialData string
	}
	ChannelStateChanged struct {
		At       time.Time
		Uniqueid string
		State    ChannelState
	}
	CallerIDChanged struct {
		At       time.Time
		Uniqueid string
		Num      string
		Name     string
	}
	ConnectedLineChanged struct {
		At       time.Time
		Uniqueid string
		Num      string
		Name     string
	}
	DialStarted struct {
		At                       time.Time
		SrcUniqueid, DstUniqueid string
	}
	DialEnded struct {
		At                       time.Time
		SrcUniqueid, DstUniqueid string
		Status                   string
	}
	BridgeEntered struct {
		At       time.Time
		BridgeID string
		Uniqueid string
	}
	BridgeLeft struct {
		At       time.Time
		BridgeID string
		Uniqueid string
	}
	HoldChanged struct {
		At       time.Time
		Uniqueid string
		OnHold   bool
	}
	ChannelHungup struct {
		At       time.Time
		Uniqueid string
		Cause    string
	}
)

func (e AMIDisconnected) EventTime() time.Time      { return e.At }
func (e SyncStarted) EventTime() time.Time          { return e.At }
func (e SyncFinished) EventTime() time.Time         { return e.At }
func (e GroupsLoaded) EventTime() time.Time         { return e.At }
func (e NamesLoaded) EventTime() time.Time          { return e.At }
func (e InventoryListed) EventTime() time.Time      { return e.At }
func (e EndpointSeen) EventTime() time.Time         { return e.At }
func (e RegistrationChanged) EventTime() time.Time  { return e.At }
func (e DeviceStateChanged) EventTime() time.Time   { return e.At }
func (e ChannelCreated) EventTime() time.Time       { return e.At }
func (e ChannelStateChanged) EventTime() time.Time  { return e.At }
func (e CallerIDChanged) EventTime() time.Time      { return e.At }
func (e ConnectedLineChanged) EventTime() time.Time { return e.At }
func (e DialStarted) EventTime() time.Time          { return e.At }
func (e DialEnded) EventTime() time.Time            { return e.At }
func (e BridgeEntered) EventTime() time.Time        { return e.At }
func (e BridgeLeft) EventTime() time.Time           { return e.At }
func (e HoldChanged) EventTime() time.Time          { return e.At }
func (e ChannelHungup) EventTime() time.Time        { return e.At }

// Output e o que o estado entrega ao hub: Batch ou Reset.
type Output interface{ isOutput() }

// Batch e um lote de mudancas (ramais coalescidos, estado final completo). Removed lista os
// numeros que saíram do estado neste lote (ordenados); nunca repete um numero de Extensions.
type Batch struct {
	Epoch        string
	FromVersion  uint64
	ToVersion    uint64
	AMIConnected bool
	Extensions   []Extension
	Removed      []string
	// Groups e o catalogo completo, so quando ele mudou neste lote (nil = nao mudou).
	Groups []Group
}

// Reset e o estado inteiro, emitido a cada epoch novo. Groups acompanha o
// Reset para o hub servir /api/groups sem consultar o banco.
type Reset struct {
	Epoch        string
	Version      uint64
	AMIConnected bool
	Technology   Technology
	Extensions   []Extension
	Groups       []Group
}

func (Batch) isOutput() {}
func (Reset) isOutput() {}

// AMIStatus e o que o coletor informa ao /api/health e ao depcheck.
type AMIStatus struct {
	Connected       bool
	Technology      Technology
	AsteriskVersion string
	ConnectedSince  time.Time // zero se desconectado
	Reconnects      int
	QueueLen        int
	QueueCap        int
	Overflows       uint64
	LoginKnown      bool // houve ao menos uma tentativa de Login
	LoginOK         bool
	LoginDetail     string
	LoginAt         time.Time
}

// Collector e o coletor AMI (supervisor + adaptador). Run bloqueia ate ctx
// terminar, reconectando com backoff, e entrega os eventos normalizados em
// events sem nunca bloquear a leitura do socket.
type Collector interface {
	Run(ctx context.Context, events chan<- Event) error
	Status() AMIStatus
}
