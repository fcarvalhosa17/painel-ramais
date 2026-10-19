// Package calls traduz mensagens AMI de chamada e de estado de dispositivo em
// eventos normalizados. É comum a sip e pjsip: a única diferença entre as
// tecnologias aqui é o prefixo do nome do canal (CONTRATOS.md 11.3).
package calls

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/model"
)

// EndpointFunc extrai o nome do endpoint do nome de um canal
// ("PJSIP/2001-0000002a" -> "2001"). ok=false se o canal não é da tecnologia.
type EndpointFunc func(channel string) (name string, ok bool)

// EndpointFromChannel implementa EndpointFunc para um prefixo ("PJSIP" ou "SIP").
// Corta no último '-': o nome do tronco pode ter hífen ("trunk-acme-0000001").
func EndpointFromChannel(channel, prefix string) (string, bool) {
	rest, found := strings.CutPrefix(channel, prefix+"/")
	if !found {
		return "", false
	}
	i := strings.LastIndexByte(rest, '-')
	if i <= 0 || !isHex(rest[i+1:]) {
		return "", false
	}
	return rest[:i], true
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// clean descarta os marcadores de vazio do Asterisk.
func clean(s string) string {
	if s == "<unknown>" || s == "<Unknown>" {
		return ""
	}
	return s
}

func chanState(desc string) model.ChannelState {
	switch strings.ToLower(desc) {
	case "down":
		return model.ChanDown
	case "ring":
		return model.ChanRing
	case "ringing":
		return model.ChanRinging
	case "up":
		return model.ChanUp
	case "busy":
		return model.ChanBusy
	}
	return model.ChanOther
}

// auxPrefixes sao os prefixos de canal auxiliar (rc.9, ADR-011 decisao 4): espionagem, conferencia, mídia de bridge,
// mensagem e RTP; nunca sao chamada, ramal nem externo. Comparacao exata de maiusculas, como o Asterisk escreve.
var auxPrefixes = [...]string{"Snoop/", "CBAnn/", "CBRec/", "Announcer/", "Recorder/", "Bridge/", "Message/", "MulticastRTP/", "UnicastRTP/"}

// IsAuxChannel diz se o nome do canal e de um canal auxiliar. Ponto unico: Translate (pelo Channel e, no
// DialBegin, tambem pelo DestChannel) e FromCoreShowChannel descartam todo evento de canal auxiliar, que assim
// nunca entra no estado.
func IsAuxChannel(name string) bool {
	for _, p := range auxPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Translate traduz um evento ao vivo de chamada. Devolve nil para evento que
// não é de chamada (quem chama tenta os outros tradutores) e para evento de canal auxiliar.
func Translate(m ami.Message, ep EndpointFunc) []model.Event {
	if IsAuxChannel(m.Get("Channel")) || (m.Event() == "DialBegin" && IsAuxChannel(m.Get("DestChannel"))) {
		return nil
	}
	at := m.At
	uid := m.Get("Uniqueid")
	lid := m.Get("Linkedid") // rc.8: todo evento de canal que o traga atualiza o Linkedid no estado
	switch m.Event() {
	case "Newchannel":
		return []model.Event{newChannel(m, ep)}
	case "Newstate":
		return []model.Event{model.ChannelStateChanged{At: at, Uniqueid: uid, Linkedid: lid, State: chanState(m.Get("ChannelStateDesc"))}}
	case "NewCallerid":
		return []model.Event{model.CallerIDChanged{At: at, Uniqueid: uid, Linkedid: lid, Num: clean(m.Get("CallerIDNum")), Name: clean(m.Get("CallerIDName"))}}
	case "NewConnectedLine":
		return []model.Event{model.ConnectedLineChanged{At: at, Uniqueid: uid, Linkedid: lid, Num: clean(m.Get("ConnectedLineNum")), Name: clean(m.Get("ConnectedLineName"))}}
	case "DialBegin":
		return []model.Event{model.DialStarted{At: at, SrcUniqueid: uid, DstUniqueid: m.Get("DestUniqueid"),
			SrcLinkedid: lid, DstLinkedid: m.Get("DestLinkedid"), DialString: m.Get("DialString")}}
	case "DialEnd":
		return []model.Event{model.DialEnded{At: at, SrcUniqueid: uid, DstUniqueid: m.Get("DestUniqueid"), Status: m.Get("DialStatus")}}
	case "BridgeEnter":
		return []model.Event{model.BridgeEntered{At: at, BridgeID: m.Get("BridgeUniqueid"), Uniqueid: uid, Linkedid: lid}}
	case "BridgeLeave":
		return []model.Event{model.BridgeLeft{At: at, BridgeID: m.Get("BridgeUniqueid"), Uniqueid: uid, Linkedid: lid}}
	case "Hold":
		return []model.Event{model.HoldChanged{At: at, Uniqueid: uid, Linkedid: lid, OnHold: true}}
	case "Unhold":
		return []model.Event{model.HoldChanged{At: at, Uniqueid: uid, Linkedid: lid, OnHold: false}}
	case "Hangup":
		return []model.Event{model.ChannelHungup{At: at, Uniqueid: uid, Cause: m.Get("Cause")}}
	}
	return nil
}

func newChannel(m ami.Message, ep EndpointFunc) model.ChannelCreated {
	ch := m.Get("Channel")
	name, _ := ep(ch)
	return model.ChannelCreated{
		At: m.At, Uniqueid: m.Get("Uniqueid"), Linkedid: m.Get("Linkedid"),
		Channel: ch, Endpoint: name, IsLocal: strings.HasPrefix(ch, "Local/"),
		CallerNum: clean(m.Get("CallerIDNum")), CallerName: clean(m.Get("CallerIDName")),
		Exten: m.Get("Exten"), Up: strings.EqualFold(m.Get("ChannelStateDesc"), "Up"),
	}
}

// FromCoreShowChannel traduz um item de CoreShowChannels: o canal (com
// BridgeId e Duration) e, se houver, a linha conectada.
func FromCoreShowChannel(m ami.Message, ep EndpointFunc) []model.Event {
	if IsAuxChannel(m.Get("Channel")) {
		return nil
	}
	cc := newChannel(m, ep)
	cc.BridgeID = m.Get("BridgeId")
	cc.Duration = parseDuration(m.Get("Duration"))
	switch app, data := m.Get("Application"), m.Get("ApplicationData"); {
	case strings.EqualFold(app, "AppDial") || strings.Contains(data, "(Outgoing Line)"):
		cc.Dialed = true
	case strings.EqualFold(app, "Dial"):
		cc.DialData = data
	case strings.EqualFold(app, "Queue"): // ainda na aplicacao Queue: aguardando ou ja atendido pela fila
		if q := strings.TrimSpace(strings.SplitN(data, ",", 2)[0]); !model.IgnoredQueue(q) { // a fila "default" nao existe: sem fila
			cc.Queue = q
		}
	}
	out := []model.Event{cc}
	if num, name := clean(m.Get("ConnectedLineNum")), clean(m.Get("ConnectedLineName")); num != "" || name != "" {
		out = append(out, model.ConnectedLineChanged{At: m.At, Uniqueid: cc.Uniqueid, Num: num, Name: name})
	}
	return out
}

// parseDuration lê "HH:MM:SS" (horas podem passar de 99). Ilegível = 0.
func parseDuration(s string) time.Duration {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return 0
		}
		n[i] = v
	}
	return time.Duration(n[0])*time.Hour + time.Duration(n[1])*time.Minute + time.Duration(n[2])*time.Second
}

// SnapshotChannels roda CoreShowChannels e emite as chamadas já em andamento.
func SnapshotChannels(ctx context.Context, c *ami.Client, ep EndpointFunc, emit func(model.Event)) error {
	r, err := c.Do(ctx, "CoreShowChannels", nil)
	if err != nil {
		return fmt.Errorf("snapshot de canais: %w", err)
	}
	for _, it := range r.Items {
		if it.Event() != "CoreShowChannel" {
			continue
		}
		for _, ev := range FromCoreShowChannel(it, ep) {
			emit(ev)
		}
	}
	return nil
}

// deviceState normaliza "NOT_INUSE", "Not in use" etc. para o enum do contrato.
func deviceState(s string) model.DeviceState {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	switch k := model.DeviceState(b.String()); k {
	case "notinuse":
		return model.DevNotInUse
	case model.DevInUse, model.DevBusy, model.DevInvalid, model.DevUnavailable,
		model.DevRinging, model.DevRingInUse, model.DevOnHold:
		return k
	}
	return model.DevUnknown
}

// DeviceStateEvent traduz DeviceStateChange (ao vivo ou item de DeviceStateList)
// de um dispositivo com o prefixo dado ("PJSIP" ou "SIP"). ok=false para
// outros dispositivos (filas, hints, Custom:).
func DeviceStateEvent(m ami.Message, prefix string) (model.DeviceStateChanged, bool) {
	name, ok := strings.CutPrefix(m.Get("Device"), prefix+"/")
	if !ok || name == "" {
		return model.DeviceStateChanged{}, false
	}
	return model.DeviceStateChanged{At: m.At, Name: name, State: deviceState(m.Get("State"))}, true
}

// OnlyListed devolve um emit que descarta DeviceStateChanged de dispositivo fora de names (os
// endpoints/peers que a lista do PBX trouxe). O DeviceStateList do Asterisk guarda dispositivos de
// endpoints ja apagados; o inventario manda, e eles nao podem ressuscitar o ramal removido. Vale
// para TODOS os caminhos de carga (Snapshot de connect/overflow e Reread).
func OnlyListed(names []string, emit func(model.Event)) func(model.Event) {
	listed := make(map[string]struct{}, len(names))
	for _, n := range names {
		listed[n] = struct{}{}
	}
	return func(ev model.Event) {
		if d, ok := ev.(model.DeviceStateChanged); ok {
			if _, in := listed[d.Name]; !in {
				return
			}
		}
		emit(ev)
	}
}

// SnapshotDeviceStates roda DeviceStateList e emite o estado inicial.
func SnapshotDeviceStates(ctx context.Context, c *ami.Client, prefix string, emit func(model.Event)) error {
	r, err := c.Do(ctx, "DeviceStateList", nil)
	if err != nil {
		return fmt.Errorf("snapshot de device state: %w", err)
	}
	for _, it := range r.Items {
		if ev, ok := DeviceStateEvent(it, prefix); ok {
			emit(ev)
		}
	}
	return nil
}
