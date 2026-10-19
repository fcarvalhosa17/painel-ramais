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
	// vinculos manuais ramal -> grupos (rc.7, N:N; ausente = sem vinculo manual). Quem envia nao pode
	// alterar Manual depois de enviar. Nao muda o epoch.
	GroupsLoaded struct {
		At     time.Time
		Groups []Group
		Manual map[string][]string
	}
	// AsteriskSignal e o aviso de que o Asterisk esta desligando (SignalShutdown) ou terminou de subir
	// (SignalFullyBooted), lido pelo supervisor no laco ao vivo (CONTRATOS 11.1). Abre a janela de
	// evento do sistema do relatorio de registro. Nao muda epoch nem versao.
	// rc.9 (ADR-011 decisao 1): no FullyBooted, Uptime e LastReload (segundos desde a subida e desde o ultimo
	// reload; HasUptime e HasLastReload = o campo veio e e inteiro valido). So o Uptime decide a pausa.
	AsteriskSignal struct {
		At            time.Time
		Kind          string
		Uptime        time.Duration
		HasUptime     bool
		LastReload    time.Duration
		HasLastReload bool
	}
	// RegStateLoaded entrega o estado "fora do ar" gravado em reg_state (rc.9, ADR-010) e o ultimo minuto de
	// history com group_name = '' e gap = 0 (LastAlive, sem somar 60 s; zero sem linha ou com leitura falha). Fresh = reg_state vazia. Emitido pelo cmd
	// na subida e consumido so pela primeira SyncFinished. Nao muda epoch nem versao.
	RegStateLoaded struct {
		At        time.Time
		Rows      map[string]RegState
		Fresh     bool
		LastAlive time.Time
	}
	// QueueNamesLoaded entrega os nomes das filas dados pelo painel (fila -> nome; ausente = sem nome), lidos
	// de queue_names na subida e depois de cada POST /api/queues/rename. Quem envia nao pode alterar Names
	// depois. Nao muda o epoch: a fila renomeada vai no proximo delta.
	QueueNamesLoaded struct {
		At    time.Time
		Names map[string]string
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
	// RegistrationChanged substitui State, IP, UserAgent e LatencyMs do ramal. Port (0 = desconhecida)
	// so alimenta o relatorio de registro. Live = true SO no evento ao vivo (ContactStatus e
	// PeerStatus); snapshot, releitura e qualquer outro caminho deixam false e nunca contam no
	// relatorio (ADR-007).
	RegistrationChanged struct {
		At        time.Time
		Name      string
		State     Registration
		IP        string
		Port      int
		UserAgent string
		LatencyMs *int
		Live      bool
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
		BridgeID   string // so no snapshot (CoreShowChannel)
		// Queue (rc.8): so no snapshot, o primeiro argumento do ApplicationData quando Application=Queue
		// (o canal ainda esta na aplicacao Queue: aguardando ou ja atendido pela fila).
		Queue    string
		Duration time.Duration // so no snapshot (CoreShowChannel)
		// So no snapshot: o Asterisk marca o canal destino de um Dial com Application=AppDial
		// ("(Outgoing Line)") e o canal que executa o Dial com Application=Dial + os argumentos.
		Dialed   bool
		DialData string
	}
	// Linkedid (rc.8, ADR-008): o do evento; vazio = sem mudanca. O estado move o canal de grupo quando ele muda.
	ChannelStateChanged struct {
		At       time.Time
		Uniqueid string
		Linkedid string
		State    ChannelState
	}
	CallerIDChanged struct {
		At       time.Time
		Uniqueid string
		Linkedid string
		Num      string
		Name     string
	}
	ConnectedLineChanged struct {
		At       time.Time
		Uniqueid string
		Linkedid string
		Num      string
		Name     string
	}
	// DialStarted: SrcLinkedid e DstLinkedid (rc.8) valem para o canal de origem e o de destino; DialString
	// e o destino do Dial ("011987654321@operadora_x", "operadora_x/0119...", "g0/0119...").
	DialStarted struct {
		At                       time.Time
		SrcUniqueid, DstUniqueid string
		SrcLinkedid, DstLinkedid string
		DialString               string
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
		Linkedid string
	}
	BridgeLeft struct {
		At       time.Time
		BridgeID string
		Uniqueid string
		Linkedid string
	}
	HoldChanged struct {
		At       time.Time
		Uniqueid string
		Linkedid string
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
func (e AsteriskSignal) EventTime() time.Time       { return e.At }
func (e RegStateLoaded) EventTime() time.Time       { return e.At }
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

func (e QueueNamesLoaded) EventTime() time.Time { return e.At }

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
	// rc.8 (ADR-008): chamadas e filas mudadas (estado completo) e as que sairam; vazio = nada daquele tipo.
	Calls         []LiveCall
	CallsRemoved  []string
	Queues        []Queue
	QueuesRemoved []string
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
	// rc.8: todas as chamadas ao vivo e todas as filas (nunca nil).
	Calls  []LiveCall
	Queues []Queue
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
	// rc.9 (ADR-011 decisao 3): Resyncs = ressincronizacoes por estouro feitas; ResyncMinGap = intervalo minimo
	// atual entre duas; LastResyncAt = a ultima (zero = nenhuma).
	Resyncs      uint64
	ResyncMinGap time.Duration
	LastResyncAt time.Time
}

// Collector e o coletor AMI (supervisor + adaptador). Run bloqueia ate ctx
// terminar, reconectando com backoff, e entrega os eventos normalizados em
// events sem nunca bloquear a leitura do socket.
type Collector interface {
	Run(ctx context.Context, events chan<- Event) error
	Status() AMIStatus
}
