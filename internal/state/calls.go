package state

import (
	"sort"
	"time"

	"painel-ramais/internal/model"
)

const maxLocalDepth = 8

// recalcAll refaz as chamadas de todos os ramais (fim da sincronizacao).
func (v *view) recalcAll() {
	all := make(map[string]struct{}, len(v.exts))
	for n := range v.exts {
		all[n] = struct{}{}
	}
	v.recalc(all)
}

// recalc refaz as chamadas dos ramais citados e marca como sujo quem mudou.
func (v *view) recalc(numbers map[string]struct{}) {
	for n := range numbers {
		x := v.exts[n]
		if x == nil {
			continue
		}
		calls := v.buildCalls(n)
		if !callsEqual(x.calls, calls) {
			x.calls = calls
			v.markDirty(n)
		}
		if x.gone && len(x.calls) == 0 {
			v.removeExt(n) // apagado no PBX: a ultima chamada acabou, agora ele sai
			if v.onPrune != nil {
				v.onPrune()
			}
		}
	}
}

func (v *view) buildCalls(number string) []model.Call {
	calls := make([]model.Call, 0, len(v.byEndpoint[number]))
	for uid := range v.byEndpoint[number] {
		if c := v.chans[uid]; c != nil && !c.isLocal {
			calls = append(calls, v.callFor(c))
		}
	}
	sort.Slice(calls, func(i, j int) bool {
		if !calls[i].StartedAt.Equal(calls[j].StartedAt.Time) {
			return calls[i].StartedAt.Before(calls[j].StartedAt.Time)
		}
		return calls[i].ID < calls[j].ID
	})
	return calls
}

// noPeerGrace: tempo que uma chamada Up sem outro lado resolvido aparece como "tocando" antes
// de ser publicada "em conversa" com o outro lado desconhecido (e logada).
const noPeerGrace = time.Second

func (v *view) callFor(c *channel) model.Call {
	peer, viaBridge := v.farEnd(c)
	state := callState(c, peer, viaBridge)
	dir, p := v.describe(c, peer)
	switch {
	case peer != nil:
		c.noPeerAt, c.noPeerSeen = time.Time{}, false
		delete(v.noPeer, c.uid)
		if p.Kind != model.PeerUnknown {
			c.knownOK, c.knownDir, c.knownPeer = true, dir, p
		}
	case state != model.CallTalking:
		// tocando/em espera: a protecao recomeca do zero quando voltar a Up sem outro lado
		c.noPeerAt, c.noPeerSeen = time.Time{}, false
		delete(v.noPeer, c.uid)
	case c.knownOK:
		// O outro lado esta trocando de perna: mantem o ultimo conhecido, mas so por noPeerGrace.
		// Passado o prazo, publica "em conversa" com o outro lado desconhecido (e loga), preservando
		// a direcao ja resolvida para nao voltar a "internal".
		if v.waitNoPeer(c, false) {
			dir, p = c.knownDir, c.knownPeer
		} else {
			v.logNoPeer(c)
			dir = c.knownDir
		}
	default:
		if v.waitNoPeer(c, true) {
			state = model.CallRinging
		} else {
			v.logNoPeer(c)
		}
	}
	switch {
	case state == model.CallRinging:
		c.talkingAt = time.Time{} // answered_at so vale para a conversa atual
	case c.talkingAt.IsZero():
		c.talkingAt = v.at
	}
	call := model.Call{ID: c.uid, Direction: dir, State: state, Peer: p, StartedAt: model.NewStamp(c.startedAt)}
	if state != model.CallRinging {
		t := model.NewStamp(c.talkingAt)
		call.AnsweredAt = &t
	}
	return call
}

// waitNoPeer diz se o canal Up sem outro lado ainda esta dentro de noPeerGrace (e o registra para o
// Flush reavaliar). Na restauracao por snapshot (immediateOnSnap) nao ha evento a esperar: nao espera.
func (v *view) waitNoPeer(c *channel, immediateOnSnap bool) bool {
	if c.noPeerAt.IsZero() {
		c.noPeerAt = v.at
	}
	if !(immediateOnSnap && c.fromSnap) && v.at.Sub(c.noPeerAt) < noPeerGrace {
		v.noPeer[c.uid] = struct{}{}
		return true
	}
	delete(v.noPeer, c.uid)
	return false
}

// logNoPeer avisa (uma vez por canal) que a protecao publicou "em conversa" sem outro lado.
func (v *view) logNoPeer(c *channel) {
	if c.noPeerSeen {
		return
	}
	c.noPeerSeen = true
	if v.onNoPeer != nil {
		v.onNoPeer(c.uid, c.linkedID)
	}
}

// expireNoPeer reavalia, a cada Flush, os canais esperando a protecao de 1 s (sem evento novo
// ninguem os recalcularia).
func (v *view) expireNoPeer(now time.Time) {
	if len(v.noPeer) == 0 {
		return
	}
	aff := map[string]struct{}{}
	for uid := range v.noPeer {
		c := v.chans[uid]
		if c == nil {
			delete(v.noPeer, uid)
			continue
		}
		if now.Sub(c.noPeerAt) >= noPeerGrace {
			v.relatedExts(c, aff)
		}
	}
	if len(aff) > 0 {
		v.at = now
		v.recalc(aff)
	}
}

func callState(c, peer *channel, viaBridge bool) model.CallState {
	switch {
	case c.onHold:
		return model.CallHold
	case !c.up:
		return model.CallRinging
	case peer != nil && !viaBridge && !peer.up:
		return model.CallRinging // quem discou ja esta "up", mas o destino ainda nao atendeu
	}
	return model.CallTalking
}

// farEnd acha o canal do outro lado. Prefere quem esta na mesma bridge; sem
// bridge, usa as relacoes de Dial. Local channels sao transparentes.
func (v *view) farEnd(c *channel) (peer *channel, viaBridge bool) {
	if p := v.pick(v.peers(c, map[string]bool{c.uid: true}, 0, true)); p != nil {
		return p, true
	}
	return v.pick(v.peers(c, map[string]bool{c.uid: true}, 0, false)), false
}

func (v *view) peers(c *channel, seen map[string]bool, depth int, byBridge bool) []*channel {
	var out []*channel
	for _, n := range v.neighbors(c, byBridge) {
		out = append(out, v.through(n, seen, depth)...)
	}
	return out
}

func (v *view) neighbors(c *channel, byBridge bool) []*channel {
	var out []*channel
	if byBridge {
		for u := range v.bridges[c.bridge] {
			if x := v.chans[u]; x != nil && x != c {
				out = append(out, x)
			}
		}
		return out
	}
	if x := v.chans[c.dialSrc]; x != nil {
		out = append(out, x)
	}
	for u := range c.dialDsts {
		if x := v.chans[u]; x != nil {
			out = append(out, x)
		}
	}
	return out
}

// through devolve n, ou, se n e um Local channel, os pares do outro lado dele.
func (v *view) through(n *channel, seen map[string]bool, depth int) []*channel {
	if seen[n.uid] {
		return nil
	}
	seen[n.uid] = true
	if !n.isLocal {
		return []*channel{n}
	}
	p := v.localPartner(n)
	if p == nil || depth >= maxLocalDepth || seen[p.uid] {
		return nil
	}
	seen[p.uid] = true
	if out := v.peers(p, seen, depth+1, true); len(out) > 0 {
		return out
	}
	return v.peers(p, seen, depth+1, false)
}

// pick escolhe de forma deterministica: canal com endpoint primeiro, depois o mais antigo.
func (v *view) pick(cands []*channel) *channel {
	var best *channel
	for _, c := range cands {
		if best == nil || better(c, best) {
			best = c
		}
	}
	return best
}

func better(a, b *channel) bool {
	if (a.endpoint != "") != (b.endpoint != "") {
		return a.endpoint != ""
	}
	if !a.startedAt.Equal(b.startedAt) {
		return a.startedAt.Before(b.startedAt)
	}
	return a.uid < b.uid
}

// isInventoryExt diz se o canal e de um ramal: endpoint presente no inventario do estado (rc.8, ADR-008
// item 3). Canal Local nunca e ramal; qualquer outro (tronco, DAHDI, IAX2, Khomp, SIP/ com a tecnologia
// pjsip) e externo.
func (v *view) isInventoryExt(c *channel) bool {
	return c != nil && !c.isLocal && c.endpoint != "" && v.exts[c.endpoint] != nil
}

// describe resolve direcao e "com quem fala". A direcao olha o canal externo:
// destino de Dial = saida; nasceu fora = entrada (sobrevive a transferencia). Ramal e quem esta no
// inventario; o resto (que nao e Local) e externo.
func (v *view) describe(c, peer *channel) (model.Direction, model.Peer) {
	switch {
	case peer == nil:
		return model.DirInternal, model.Peer{Kind: model.PeerUnknown, Number: model.StrPtr(dialedNumber(c))}
	case v.isInventoryExt(peer):
		return model.DirInternal, model.Peer{Kind: model.PeerExtension, Number: model.StrPtr(peer.endpoint), Name: model.StrPtr(v.extName(peer))}
	case peer.dialed:
		return model.DirOutbound, model.Peer{
			Kind:   model.PeerExternal,
			Number: model.StrPtr(first(peer.connNum, otherThan(c.connNum, c.endpoint), dialedNumber(peer), dialedNumber(c))),
			Name:   model.StrPtr(first(peer.connNm, c.connNm)),
		}
	}
	return model.DirInbound, model.Peer{
		Kind:   model.PeerExternal,
		Number: model.StrPtr(first(peer.callerNum, otherThan(c.connNum, c.endpoint))),
		Name:   model.StrPtr(first(peer.callerNm, c.connNm)),
	}
}

func (v *view) extName(ch *channel) string {
	if x := v.exts[ch.endpoint]; x != nil && x.name != "" {
		return x.name
	}
	return ch.callerNm
}

// dialedNumber e o destino discado quando o canal ainda nao tem par ("s" nao e numero).
func dialedNumber(c *channel) string {
	if c.exten == "s" {
		return ""
	}
	return c.exten
}

func otherThan(s, self string) string {
	if s == self {
		return ""
	}
	return s
}

func first(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func callsEqual(a, b []model.Call) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ID != y.ID || x.Direction != y.Direction || x.State != y.State || x.StartedAt != y.StartedAt ||
			x.Peer.Kind != y.Peer.Kind || !strPtrEq(x.Peer.Number, y.Peer.Number) || !strPtrEq(x.Peer.Name, y.Peer.Name) ||
			(x.AnsweredAt == nil) != (y.AnsweredAt == nil) || (x.AnsweredAt != nil && *x.AnsweredAt != *y.AnsweredAt) {
			return false
		}
	}
	return true
}

func strPtrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
