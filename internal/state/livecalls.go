package state

import (
	"sort"
	"strings"
	"time"

	"painel-ramais/internal/model"
)

// Projecao de chamadas ao vivo (rc.8, ADR-008, CONTRATOS 2.3 e 11.6): uma linha por Linkedid. Mora no State,
// fora da view: le os canais da visao publicada (v.chans, v.byLinked, v.bridges) mas nunca os altera. Cada
// evento de canal so marca como sujo o Linkedid dos canais citados (antes e depois) e os da bridge deles; o Flush
// remonta so os sujos, cada um em O(k) nos k canais da chamada. Nada varre todas as chamadas por evento.

// localNoPeerGrace: tempo que uma chamada com canal Local fica sem o outro lado antes do log (CONTRATOS 2.3).
const localNoPeerGrace = time.Second

// callRec e o que a projecao guarda de uma chamada: o que e fixado na primeira montagem (origem, inicio) e a
// ultima linha publicada.
type callRec struct {
	lid       string
	originExt bool      // a origem e ramal do inventario (guardado na primeira montagem, nao muda)
	started   time.Time // inicio do canal de origem (guardado)
	originNum string    // ultimo CallerID conhecido do canal de origem externo (a origem pode desligar na transferencia)
	pub       model.LiveCall
	published bool
	// "Local sem par": desde quando a chamada esta nesse estado, o canal Local que a mostra e se ja logou.
	noPeerSince time.Time
	localUID    string
	localName   string
	noPeerLog   bool
}

// callProj reune a projecao de chamadas e o que as filas dizem sobre elas.
type callProj struct {
	calls         map[string]*callRec
	dirty         map[string]struct{}
	allDirty      bool
	watch         map[string]struct{} // chamadas esperando o prazo do "Local sem par"
	entryOf       map[string]string   // Uniqueid do chamador -> fila em que aguarda
	viaQueue      map[string]string   // Uniqueid -> fila que atendeu (AgentConnect), ate o Hangup
	onLocalNoPeer func(uniqueid, linkedid, channel string)
}

func newCallProj(onLocalNoPeer func(uniqueid, linkedid, channel string)) *callProj {
	return &callProj{calls: map[string]*callRec{}, dirty: map[string]struct{}{}, watch: map[string]struct{}{},
		entryOf: map[string]string{}, viaQueue: map[string]string{}, onLocalNoPeer: onLocalNoPeer}
}

// reset esvazia a projecao (queda do AMI): a lista de chamadas some. entryOf e viaQueue tambem: o AMI fora
// derruba as ligacoes.
func (p *callProj) reset() {
	p.calls, p.dirty, p.watch = map[string]*callRec{}, map[string]struct{}{}, map[string]struct{}{}
	p.entryOf, p.viaQueue = map[string]string{}, map[string]string{}
	p.allDirty = false
}

func (p *callProj) markLinked(lid string) {
	if lid != "" {
		p.dirty[lid] = struct{}{}
	}
}

// markChan marca o Linkedid do canal uid (se existe na visao v) e os dos canais da bridge dele.
func (p *callProj) markChan(v *view, uid string) {
	c := v.chans[uid]
	if c == nil {
		return
	}
	p.markLinked(c.linkedID)
	p.markBridge(v, c.bridge)
}

func (p *callProj) markBridge(v *view, bridge string) {
	if bridge == "" {
		return
	}
	for u := range v.bridges[bridge] {
		if c := v.chans[u]; c != nil {
			p.markLinked(c.linkedID)
		}
	}
}

// citedChannels devolve os Uniqueid que o evento de canal cita (ate dois) e a bridge que ele nomeia.
func citedChannels(ev model.Event) (a, b, bridge string, ok bool) {
	switch e := ev.(type) {
	case model.ChannelCreated:
		return e.Uniqueid, "", e.BridgeID, true
	case model.ChannelStateChanged:
		return e.Uniqueid, "", "", true
	case model.CallerIDChanged:
		return e.Uniqueid, "", "", true
	case model.ConnectedLineChanged:
		return e.Uniqueid, "", "", true
	case model.HoldChanged:
		return e.Uniqueid, "", "", true
	case model.DialStarted:
		return e.SrcUniqueid, e.DstUniqueid, "", true
	case model.DialEnded:
		return e.SrcUniqueid, e.DstUniqueid, "", true
	case model.BridgeEntered:
		return e.Uniqueid, "", e.BridgeID, true
	case model.BridgeLeft:
		return e.Uniqueid, "", e.BridgeID, true
	case model.ChannelHungup:
		return e.Uniqueid, "", "", true
	}
	return "", "", "", false
}

// touch marca como sujos os Linkedid que o evento de canal pode mudar. Roda antes e depois de aplicar o evento
// na visao publicada (o canal pode ter mudado de Linkedid ou de bridge, ou ter sumido no Hangup).
func (p *callProj) touch(v *view, ev model.Event) {
	a, b, bridge, ok := citedChannels(ev)
	if !ok {
		return
	}
	p.markChan(v, a)
	if b != "" {
		p.markChan(v, b)
	}
	p.markBridge(v, bridge)
}

// ---- montagem de uma chamada ----

// assemble remonta a linha de lid a partir dos canais vivos da visao v. ok = false quando nao ha canal nao
// Local (a chamada nao existe: grupo vazio ou so Local). Cria o registro na primeira montagem.
func (s *State) assemble(lid string) (model.LiveCall, bool) {
	v, p := s.cur, s.proj
	var chans, nonLocal []*channel
	hasLocal := false
	for uid := range v.byLinked[lid] {
		c := v.chans[uid]
		if c == nil {
			continue
		}
		chans = append(chans, c)
		if c.isLocal {
			hasLocal = true
		} else {
			nonLocal = append(nonLocal, c)
		}
	}
	if len(nonLocal) == 0 {
		return model.LiveCall{}, false
	}
	sortChans(chans)
	sortChans(nonLocal)
	origin := v.chans[lid]
	if origin != nil && (origin.linkedID != lid || origin.isLocal) {
		origin = nil
	}
	rec := p.calls[lid]
	if rec == nil { // primeira montagem: origem e inicio ficam guardados ate a linha sumir
		first := origin
		if first == nil {
			first = nonLocal[0]
		}
		rec = &callRec{lid: lid, originExt: v.isInventoryExt(first), started: first.startedAt}
		p.calls[lid] = rec
	}
	var exts, externals []*channel
	for _, c := range nonLocal {
		if v.isInventoryExt(c) {
			exts = append(exts, c)
		} else {
			externals = append(externals, c)
		}
	}
	lc := model.LiveCall{ID: lid, StartedAt: model.NewStamp(rec.started), Extensions: extensionList(exts, origin, rec.originExt)}
	switch {
	case !rec.originExt:
		lc.Direction = model.DirInbound
	case len(externals) > 0:
		lc.Direction = model.DirOutbound
	default:
		lc.Direction = model.DirInternal
	}
	queued, via := s.queueOfCall(chans, nonLocal)
	switch {
	case queued != "":
		lc.State, lc.Queue = model.LiveQueued, model.StrPtr(queued)
	case anyHold(nonLocal):
		lc.State, lc.Queue = model.LiveHold, model.StrPtr(via)
	case talking(chans, nonLocal):
		lc.State, lc.Queue = model.LiveTalking, model.StrPtr(via)
	default:
		lc.State = model.LiveRinging
	}
	switch lc.Direction {
	case model.DirInbound:
		if origin != nil {
			rec.originNum = origin.callerNum
		} else if rec.originNum == "" && len(externals) > 0 { // origem ja desligada na primeira montagem: o canal externo mais antigo (2.3)
			rec.originNum = externals[0].callerNum
		}
		lc.External = model.StrPtr(rec.originNum)
		switch {
		case origin != nil:
			lc.Trunk = model.StrPtr(trunkLabel(origin.name))
		case len(externals) > 0:
			lc.Trunk = model.StrPtr(trunkLabel(externals[0].name))
		}
	case model.DirOutbound:
		lc.External = model.StrPtr(v.dialedOf(externals[0], origin))
		lc.Trunk = model.StrPtr(trunkLabel(externals[0].name))
	}
	s.noteLocalNoPeer(rec, lc, chans, hasLocal)
	return lc, true
}

// sortChans ordena por inicio do canal e Uniqueid (ordem deterministica do contrato).
func sortChans(cs []*channel) {
	sort.Slice(cs, func(i, j int) bool {
		if !cs[i].startedAt.Equal(cs[j].startedAt) {
			return cs[i].startedAt.Before(cs[j].startedAt)
		}
		return cs[i].uid < cs[j].uid
	})
}

// extensionList: numeros dos ramais com canal vivo na chamada, sem repeticao; o ramal da origem primeiro (se a
// origem e ramal e esta viva), depois por inicio do canal e Uniqueid. Nunca nil.
func extensionList(exts []*channel, origin *channel, originExt bool) []string {
	out := make([]string, 0, len(exts))
	seen := make(map[string]struct{}, len(exts))
	add := func(c *channel) {
		if _, dup := seen[c.endpoint]; !dup {
			seen[c.endpoint] = struct{}{}
			out = append(out, c.endpoint)
		}
	}
	if originExt && origin != nil {
		for _, c := range exts {
			if c == origin {
				add(c)
			}
		}
	}
	for _, c := range exts {
		add(c)
	}
	return out
}

func anyHold(nonLocal []*channel) bool {
	for _, c := range nonLocal {
		if c.onHold {
			return true
		}
	}
	return false
}

// talking: alguma bridge tem 2 ou mais canais da chamada, ou nenhum canal nao Local esta fora de Up.
func talking(chans, nonLocal []*channel) bool {
	per := map[string]int{}
	for _, c := range chans {
		if c.bridge != "" {
			if per[c.bridge]++; per[c.bridge] >= 2 {
				return true
			}
		}
	}
	for _, c := range nonLocal {
		if !c.up {
			return false
		}
	}
	return true
}

// queueOfCall devolve a fila em que a chamada aguarda (entrada viva de algum canal dela) e, senao, a fila
// que a atendeu (AgentConnect; no snapshot, canal ainda na aplicacao Queue sem entrada viva).
func (s *State) queueOfCall(chans, nonLocal []*channel) (queued, via string) {
	for _, c := range chans {
		if q := s.proj.entryOf[c.uid]; q != "" {
			return q, ""
		}
	}
	for _, c := range chans {
		if q := s.proj.viaQueue[c.uid]; q != "" {
			return "", q
		}
	}
	for _, c := range nonLocal {
		if c.queueApp != "" {
			return "", c.queueApp
		}
	}
	return "", ""
}

// noteLocalNoPeer registra o "Local sem par" (CONTRATOS 2.3): chamada com canal Local que continua sem o
// outro lado (interna com um ramal so, ou entrante sem ramal, ambas fora de fila). Passado o prazo o Flush
// loga uma vez por chamada.
func (s *State) noteLocalNoPeer(rec *callRec, lc model.LiveCall, chans []*channel, hasLocal bool) {
	noPeer := hasLocal && lc.Queue == nil && lc.State != model.LiveQueued &&
		((lc.Direction == model.DirInternal && len(lc.Extensions) == 1) ||
			(lc.Direction == model.DirInbound && len(lc.Extensions) == 0))
	if !noPeer {
		rec.noPeerSince = time.Time{}
		delete(s.proj.watch, rec.lid)
		return
	}
	if rec.noPeerLog || !rec.noPeerSince.IsZero() {
		return
	}
	for _, c := range chans {
		if c.isLocal {
			rec.localUID, rec.localName = c.uid, c.name
			break
		}
	}
	rec.noPeerSince = s.now()
	s.proj.watch[rec.lid] = struct{}{}
}

// checkLocalNoPeer loga, uma vez por chamada, as que passaram de localNoPeerGrace sem o outro lado.
func (s *State) checkLocalNoPeer(now time.Time) {
	p := s.proj
	for lid := range p.watch {
		rec := p.calls[lid]
		if rec == nil || rec.noPeerSince.IsZero() {
			delete(p.watch, lid)
			continue
		}
		if now.Sub(rec.noPeerSince) < localNoPeerGrace {
			continue
		}
		delete(p.watch, lid)
		if !rec.noPeerLog {
			rec.noPeerLog = true
			if p.onLocalNoPeer != nil {
				p.onLocalNoPeer(rec.localUID, lid, rec.localName)
			}
		}
	}
}

// ---- numero e tronco ----

// dialedOf acha o numero discado de uma chamada de saida: o DialString do DialBegin que criou o canal externo
// (ext); sem ele (snapshot), o primeiro destino do Dial do canal que discou; sem os dois, o Exten do canal de
// origem (se diferente de "s"). "" quando nenhum casa.
func (v *view) dialedOf(ext, origin *channel) string {
	if n := dialNumber(ext.dialStr); n != "" {
		return n
	}
	if d := v.chans[ext.dialSrc]; d != nil {
		if ts := dialTargets(d.dialData); len(ts) > 0 {
			t := ts[0]
			if i := strings.IndexByte(t, '/'); i >= 0 { // tira o prefixo de tecnologia
				t = t[i+1:]
			}
			if n := dialNumber(t); n != "" {
				return n
			}
		}
	}
	if origin != nil && origin.exten != "s" {
		return dialNumber(origin.exten)
	}
	return ""
}

// dialNumber extrai o numero discado de um destino de Dial: com "@", o texto antes dele; sem "@" e com "/", o
// texto depois da ultima "/"; senao o texto inteiro. Vale so se casar ^[0-9+*#]{1,32}$.
func dialNumber(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '@'); i >= 0 {
		s = s[:i]
	} else if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) < 1 || len(s) > 32 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !(c >= '0' && c <= '9' || c == '+' || c == '*' || c == '#') {
			return ""
		}
	}
	return s
}

// trunkLabel e o rotulo do tronco a partir do nome do canal (CONTRATOS 2.3): tira o sufixo de sequencia (o
// ultimo "-" seguido so de digitos hexadecimais ate o fim) e fica so ate a segunda "/".
func trunkLabel(name string) string {
	if i := strings.LastIndexByte(name, '-'); i > 0 && isHexText(name[i+1:]) {
		name = name[:i]
	}
	if i := strings.IndexByte(name, '/'); i >= 0 {
		if j := strings.IndexByte(name[i+1:], '/'); j >= 0 {
			name = name[:i+1+j]
		}
	}
	return name
}

func isHexText(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// ---- Flush ----

func sameLive(a, b model.LiveCall) bool {
	if a.ID != b.ID || a.Direction != b.Direction || a.State != b.State || a.StartedAt != b.StartedAt ||
		!strPtrEq(a.External, b.External) || !strPtrEq(a.Trunk, b.Trunk) || !strPtrEq(a.Queue, b.Queue) ||
		len(a.Extensions) != len(b.Extensions) {
		return false
	}
	for i := range a.Extensions {
		if a.Extensions[i] != b.Extensions[i] {
			return false
		}
	}
	return true
}

// flushCalls remonta so as chamadas sujas e devolve as que mudaram (ou entraram) e as que sairam, ordenadas.
func (s *State) flushCalls() (changed []model.LiveCall, removed []string) {
	p := s.proj
	if p.allDirty {
		for lid := range s.cur.byLinked {
			p.dirty[lid] = struct{}{}
		}
		for lid := range p.calls {
			p.dirty[lid] = struct{}{}
		}
		p.allDirty = false
	}
	for lid := range p.dirty {
		lc, ok := s.assemble(lid)
		rec := p.calls[lid]
		if !ok {
			if rec != nil {
				delete(p.calls, lid)
				delete(p.watch, lid)
				if rec.published {
					removed = append(removed, lid)
				}
			}
			continue
		}
		if rec = p.calls[lid]; !rec.published || !sameLive(rec.pub, lc) {
			rec.pub, rec.published = lc, true
			changed = append(changed, lc)
		}
	}
	p.dirty = map[string]struct{}{}
	sort.Slice(changed, func(i, j int) bool { return changed[i].ID < changed[j].ID })
	sort.Strings(removed)
	s.checkLocalNoPeer(s.now())
	return changed, removed
}

// allCalls devolve todas as linhas publicadas, ordenadas por inicio e id (o Reset e o snapshot).
func (s *State) allCalls() []model.LiveCall {
	out := make([]model.LiveCall, 0, len(s.proj.calls))
	for _, r := range s.proj.calls {
		if r.published {
			out = append(out, r.pub)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt.Time) {
			return out[i].StartedAt.Before(out[j].StartedAt.Time)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// rebuildCalls refaz a projecao inteira da visao nova (fim da sincronizacao): descarta o que havia e marca todos
// os Linkedid da visao. O Reset que a sincronizacao gera leva tudo. Tira de viaQueue o que ja nao tem canal.
func (s *State) rebuildCalls() {
	p := s.proj
	p.calls, p.watch = map[string]*callRec{}, map[string]struct{}{}
	p.dirty = make(map[string]struct{}, len(s.cur.byLinked))
	for lid := range s.cur.byLinked {
		p.dirty[lid] = struct{}{}
	}
	p.allDirty = false
	for uid := range p.viaQueue {
		if s.cur.chans[uid] == nil {
			delete(p.viaQueue, uid)
		}
	}
}
