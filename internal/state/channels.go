package state

import (
	"strings"
	"time"

	"painel-ramais/internal/model"
)

type channel struct {
	uid, linkedID, name string
	endpoint            string // ramal ou tronco; vazio se Local/outro
	isLocal             bool
	callerNum, callerNm string
	connNum, connNm     string
	exten               string
	up, onHold          bool
	dialed              bool   // foi destino de um Dial
	dialData            string // argumentos do Dial que este canal executa (so vindo do snapshot)
	fromSnap            bool   // veio do CoreShowChannels, sem os DialBegin que o originaram
	dialSrc             string // quem discou para este canal
	dialDsts            map[string]struct{}
	bridge              string
	startedAt           time.Time
	talkingAt           time.Time // primeira vez que a chamada saiu de "ringing"
	// Ultimo "com quem fala" resolvido: usado enquanto o outro lado troca de perna (Hangup antes
	// do nosso, Local channel sendo otimizado) e farEnd nao acha ninguem por alguns eventos.
	knownOK    bool
	knownDir   model.Direction
	knownPeer  model.Peer
	noPeerAt   time.Time // desde quando esta Up sem nenhum outro lado e sem historico
	noPeerSeen bool      // ja logou a protecao de 1 s para este canal
}

func (v *view) applyChannel(ev model.Event) {
	switch e := ev.(type) {
	case model.ChannelCreated:
		v.onChannelCreated(e)
	case model.ChannelStateChanged:
		v.mutate([]string{e.Uniqueid}, func() { v.chans[e.Uniqueid].up = e.State == model.ChanUp })
	case model.CallerIDChanged:
		v.mutate([]string{e.Uniqueid}, func() {
			c := v.chans[e.Uniqueid]
			c.callerNum, c.callerNm = cleanID(e.Num), cleanID(e.Name)
		})
	case model.ConnectedLineChanged:
		v.mutate([]string{e.Uniqueid}, func() {
			c := v.chans[e.Uniqueid]
			c.connNum, c.connNm = cleanID(e.Num), cleanID(e.Name)
		})
	case model.HoldChanged:
		v.mutate([]string{e.Uniqueid}, func() { v.chans[e.Uniqueid].onHold = e.OnHold })
	case model.DialStarted:
		v.mutate([]string{e.SrcUniqueid, e.DstUniqueid}, func() {
			v.dialStart(e.SrcUniqueid, e.DstUniqueid)
			v.learnNameFromDialer(e.SrcUniqueid)
		})
	case model.DialEnded:
		v.mutate([]string{e.SrcUniqueid, e.DstUniqueid}, func() { v.dialEnd(e) })
	case model.BridgeEntered:
		v.mutate([]string{e.Uniqueid}, func() { v.bridgeJoin(e.BridgeID, e.Uniqueid) })
	case model.BridgeLeft:
		v.mutate([]string{e.Uniqueid}, func() { v.bridgeLeave(e.BridgeID, e.Uniqueid) })
	case model.ChannelHungup:
		v.onHangup(e.Uniqueid)
	}
}

// cleanID descarta os valores "sem identificacao" que o Asterisk manda.
func cleanID(s string) string {
	s = model.CleanName(s) // texto vindo de fora: sem controle/bidi, no maximo 64 caracteres
	if s == "<unknown>" || s == "unknown" {
		return ""
	}
	return s
}

// mutate roda fn se todos os canais citados existem, e recalcula os ramais
// ligados a eles antes e depois da mudanca (uma saida de bridge afeta quem ficou).
func (v *view) mutate(uids []string, fn func()) {
	for _, u := range uids {
		if v.chans[u] == nil {
			return
		}
	}
	aff := map[string]struct{}{}
	for _, u := range uids {
		v.relatedExts(v.chans[u], aff)
	}
	fn()
	for _, u := range uids {
		if c := v.chans[u]; c != nil {
			v.relatedExts(c, aff)
		}
	}
	v.recalc(aff)
}

func (v *view) onChannelCreated(e model.ChannelCreated) {
	if _, gone := v.hungup[e.Uniqueid]; gone {
		return
	}
	if c := v.chans[e.Uniqueid]; c != nil {
		v.mutate([]string{e.Uniqueid}, func() {
			c.up = c.up || e.Up
			if e.BridgeID != "" {
				v.bridgeJoin(e.BridgeID, c.uid)
			}
		})
		return
	}
	started := e.At.Add(-e.Duration)
	c := &channel{
		uid: e.Uniqueid, linkedID: e.Linkedid, name: e.Channel, endpoint: e.Endpoint, isLocal: e.IsLocal,
		callerNum: cleanID(e.CallerNum), callerNm: cleanID(e.CallerName), exten: e.Exten, up: e.Up,
		dialDsts: map[string]struct{}{}, startedAt: started,
		dialed: e.Dialed, dialData: e.DialData, fromSnap: e.Duration > 0 || e.Dialed || e.DialData != "",
	}
	if e.Up && e.Duration > 0 {
		c.talkingAt = started
	}
	v.chans[c.uid] = c
	addIdx(v.byLinked, c.linkedID, c.uid)
	if c.endpoint != "" && v.isExtension(c.endpoint) {
		addIdx(v.byEndpoint, c.endpoint, c.uid)
	} else if c.endpoint != "" {
		v.trunks[c.endpoint] = struct{}{}
	}
	if c.isLocal {
		v.localJoin(c)
	}
	aff := map[string]struct{}{}
	if e.BridgeID != "" {
		v.bridgeJoin(e.BridgeID, c.uid)
	}
	v.relatedExts(c, aff)
	v.recalc(aff)
}

func (v *view) onHangup(uid string) {
	c := v.chans[uid]
	if c == nil {
		if v.building() {
			v.hungup[uid] = struct{}{}
		}
		return
	}
	aff := map[string]struct{}{}
	v.relatedExts(c, aff)
	v.bridgeLeave(c.bridge, uid)
	delIdx(v.byLinked, c.linkedID, uid)
	delIdx(v.byEndpoint, c.endpoint, uid)
	if s := v.chans[c.dialSrc]; s != nil {
		delete(s.dialDsts, uid)
	}
	for d := range c.dialDsts {
		if dc := v.chans[d]; dc != nil && dc.dialSrc == uid {
			dc.dialSrc = ""
		}
	}
	if c.isLocal {
		v.localLeave(c)
	}
	delete(v.chans, uid)
	delete(v.noPeer, uid)
	if v.building() {
		v.hungup[uid] = struct{}{}
	}
	v.recalc(aff)
}

// building diz se esta view e a de montagem. A view de montagem so existe durante
// a sincronizacao; a publicada nunca precisa lembrar de Hangups.
func (v *view) building() bool { return v.isBuilding }

func (v *view) dialStart(src, dst string) {
	s, d := v.chans[src], v.chans[dst]
	s.dialDsts[dst] = struct{}{}
	d.dialSrc = src
	d.dialed = true
}

func (v *view) dialEnd(e model.DialEnded) {
	if strings.EqualFold(e.Status, "ANSWER") {
		return
	}
	// Dial que nao foi atendido: a relacao some, o canal destino cai em seguida.
	if s := v.chans[e.SrcUniqueid]; s != nil {
		delete(s.dialDsts, e.DstUniqueid)
	}
	if d := v.chans[e.DstUniqueid]; d != nil && d.dialSrc == e.SrcUniqueid {
		d.dialSrc = ""
	}
}

func (v *view) bridgeJoin(id, uid string) {
	c := v.chans[uid]
	if id == "" || c == nil {
		return
	}
	if c.bridge != "" && c.bridge != id {
		v.bridgeLeave(c.bridge, uid)
	}
	c.bridge = id
	addIdx(v.bridges, id, uid)
}

func (v *view) bridgeLeave(id, uid string) {
	if id == "" {
		return
	}
	if c := v.chans[uid]; c != nil && c.bridge == id {
		c.bridge = ""
	}
	delIdx(v.bridges, id, uid)
}

// Local channels: "Local/x@ctx-0000001a;1" e ";2" formam um par.
func localBase(name string) (base string, half int) {
	i := strings.LastIndexByte(name, ';')
	if i < 0 || i+2 != len(name) {
		return name, 0
	}
	return name[:i], int(name[i+1] - '0')
}

func (v *view) localJoin(c *channel) {
	base, half := localBase(c.name)
	if half != 1 && half != 2 {
		return
	}
	p := v.localHalves[base]
	if p == nil {
		p = &[2]string{}
		v.localHalves[base] = p
	}
	p[half-1] = c.uid
}

func (v *view) localLeave(c *channel) {
	base, half := localBase(c.name)
	if p := v.localHalves[base]; p != nil && (half == 1 || half == 2) {
		p[half-1] = ""
		if p[0] == "" && p[1] == "" {
			delete(v.localHalves, base)
		}
	}
}

func (v *view) localPartner(c *channel) *channel {
	base, half := localBase(c.name)
	p := v.localHalves[base]
	if p == nil || (half != 1 && half != 2) {
		return nil
	}
	return v.chans[p[2-half]]
}

func addIdx(m map[string]map[string]struct{}, k, uid string) {
	if k == "" {
		return
	}
	s := m[k]
	if s == nil {
		s = map[string]struct{}{}
		m[k] = s
	}
	s[uid] = struct{}{}
}

func delIdx(m map[string]map[string]struct{}, k, uid string) {
	if s := m[k]; s != nil {
		delete(s, uid)
		if len(s) == 0 {
			delete(m, k)
		}
	}
}

// relatedExts junta em into os ramais cujas chamadas podem mudar quando c muda:
// o proprio, quem divide Linkedid ou bridge, e os pares de Dial e de Local.
func (v *view) relatedExts(c *channel, into map[string]struct{}) {
	add := func(x *channel) {
		if x != nil && x.endpoint != "" && v.isExtension(x.endpoint) {
			into[x.endpoint] = struct{}{}
		}
	}
	add(c)
	for u := range v.byLinked[c.linkedID] {
		add(v.chans[u])
	}
	for u := range v.bridges[c.bridge] {
		add(v.chans[u])
	}
	add(v.chans[c.dialSrc])
	for u := range c.dialDsts {
		add(v.chans[u])
	}
	if c.isLocal {
		add(v.localPartner(c))
	}
}

// learnNameFromDialer aproveita o CallerIDName de quem origina uma chamada: o canal que disca,
// com o proprio numero como identidade, carrega o callerid do ramal. So preenche nome vazio.
func (v *view) learnNameFromDialer(srcUID string) {
	src := v.chans[srcUID]
	if src == nil || src.callerNm == "" || src.endpoint == "" || src.callerNum != src.endpoint {
		return
	}
	if x := v.exts[src.endpoint]; x != nil && x.name == "" {
		x.name = src.callerNm
		v.markDirty(x.number)
		v.learned(x.number, x.name)
	}
}
