package state

import (
	"fmt"
	"net/netip"
	"time"

	"painel-ramais/internal/model"
)

// Relatorio de registro (ADR-007, ADR-010 e ADR-011, CONTRATOS 11.5). Tudo aqui roda na goroutine do estado e
// NUNCA bloqueia nem toca em banco: o resultado sai por Options.OnReg e Options.OnDecision, que so enfileiram
// (OnInfo e so para as poucas linhas de log da pausa, que acontecem raramente).

// systemWindow e a pausa da contagem num reinicio real do Asterisk: transicao de registro dentro dela e gravada
// como evento do sistema (fora dos contadores).
const systemWindow = 120 * time.Second

// maxReportRuneUA e o teto do user agent guardado no relatorio.
const maxReportRuneUA = model.MaxUserAgentRunes

// regCtx e o relatorio de registro compartilhado entre o State e as suas views (a visao troca a cada
// sincronizacao, mas a pausa e os contadores acumulados valem para o processo inteiro).
type regCtx struct {
	now        func() time.Time
	onReg      func(model.RegNote)
	onDecision func(model.RegDecision)
	onInfo     func(msg string, args ...any)
	until      time.Time // fim da pausa da contagem (zero = sem pausa)
	total      model.RegCounts
	byGroup    map[string]model.RegCounts
	syncReason model.SyncReason // do SyncStarted em curso
	// noUptimeLogged: o aviso "FullyBooted sem Uptime" ja saiu nesta sessao AMI (so um por sessao).
	noUptimeLogged bool
}

func newRegCtx(now func() time.Time, o Options) *regCtx {
	return &regCtx{now: now, onReg: o.OnReg, onDecision: o.OnDecision, onInfo: o.OnInfo, byGroup: map[string]model.RegCounts{}}
}

func (r *regCtx) emit(n model.RegNote) {
	if r.onReg != nil {
		r.onReg(n)
	}
}

func (r *regCtx) info(msg string, args ...any) {
	if r.onInfo != nil {
		r.onInfo(msg, args...)
	}
}

// decide entrega uma decisao de contagem ao log (sem bloquear).
func (r *regCtx) decide(at time.Time, number string, from, to model.Registration, counted bool, reason string) {
	if r.onDecision != nil {
		r.onDecision(model.RegDecision{At: at, Number: number, From: from, To: to, Counted: counted, Reason: reason})
	}
}

// openPause abre (ou estende) a pausa ate `until` e grava o sinal do sistema na hora at.
func (r *regCtx) openPause(until, at time.Time, signal string) {
	if until.After(r.until) {
		r.until = until
	}
	r.emit(model.RegNote{At: at, Signal: signal})
}

// onSignal aplica a regra da pausa (CONTRATOS 11.5 item 2, ADR-011 decisao 1): Shutdown pausa 120 s; FullyBooted so
// quando o Asterisk acabou de subir (Uptime abaixo de 120 s); sem Uptime vale o comportamento da rc.8.
func (r *regCtx) onSignal(e model.AsteriskSignal) {
	switch e.Kind {
	case model.SignalShutdown:
		r.openPause(e.At.Add(systemWindow), e.At, model.SignalShutdown)
	case model.SignalFullyBooted:
		switch {
		case e.HasUptime && e.Uptime < systemWindow:
			until := e.At.Add(systemWindow - e.Uptime)
			r.openPause(until, e.At, model.SignalFullyBooted)
			reload := "n/d"
			if e.HasLastReload {
				reload = fmt.Sprint(int64(e.LastReload / time.Second))
			}
			r.info(fmt.Sprintf("contagem de registros pausada ate %s motivo=%s uptime_s=%d last_reload_s=%s",
				until.UTC().Format(time.RFC3339), model.PauseAsteriskReboot, int64(e.Uptime/time.Second), reload))
		case e.HasUptime:
			r.info(fmt.Sprintf("FullyBooted com Asterisk no ar ha %d s: contagem nao pausada", int64(e.Uptime/time.Second)))
		default:
			r.openPause(e.At.Add(systemWindow), e.At, model.SignalFullyBooted)
			if !r.noUptimeLogged {
				r.noUptimeLogged = true
				r.info("FullyBooted sem Uptime: pausa de 120 s mantida (comportamento da rc.8)")
			}
		}
	}
}

// suppressedUntil devolve o fim da pausa se ela ainda esta aberta em now; zero se nao.
func (r *regCtx) suppressedUntil(now time.Time) time.Time {
	if now.Before(r.until) {
		return r.until
	}
	return time.Time{}
}

// count soma uma ocorrencia contada (system = 0) no total e em cada grupo do ramal.
func (r *regCtx) count(kind string, groups []string) {
	bump := func(c *model.RegCounts) {
		if kind == model.RegKindLost {
			c.Lost++
		} else {
			c.Unreachable++
		}
	}
	bump(&r.total)
	for _, g := range groups {
		c := r.byGroup[g]
		bump(&c)
		r.byGroup[g] = c
	}
}

// byGroupCopy copia os contadores por grupo (o estado publica a copia, nunca o mapa vivo).
func (r *regCtx) byGroupCopy() map[string]model.RegCounts {
	out := make(map[string]model.RegCounts, len(r.byGroup))
	for g, c := range r.byGroup {
		out[g] = c
	}
	return out
}

// transitionKind e a tabela de transicoes do ADR-007; "" = nao conta (inclui unknown e
// inalcancavel <-> nao registrado).
func transitionKind(from, to model.Registration) string {
	switch {
	case from == model.RegRegistered && to == model.RegUnregistered:
		return model.RegKindLost
	case from == model.RegUnregistered && to == model.RegRegistered:
		return model.RegKindRegistered
	case from == model.RegRegistered && to == model.RegUnreachable:
		return model.RegKindUnreachable
	case from == model.RegUnreachable && to == model.RegRegistered:
		return model.RegKindReachable
	}
	return ""
}

// isOut diz se o ramal esta fora do ar: nao registrado ou inalcancavel (ADR-010 decisao 1). unknown nao e ok nem fora.
func isOut(r model.Registration) bool { return r == model.RegUnregistered || r == model.RegUnreachable }

// ipv4 devolve ip e porta se ip e IPv4 puro; senao vazio e 0 (IPv6 nunca e gravado). Porta fora de 1..65535 vira 0.
func ipv4(ip string, port int) (string, int) {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return "", 0
	}
	if port < 1 || port > 65535 {
		port = 0
	}
	return ip, port
}

// regState monta o estado "fora do ar" do ramal para a nota de estado.
func (x *extState) regState() *model.RegState {
	return &model.RegState{DownSince: x.downSince, DownSeg: x.downSeg, LastReg: x.lastReg, DownKind: x.downKind, LastRegExact: x.lastRegExact}
}

// emitState entrega a nota de estado do ramal (upsert em reg_state, fora do caminho do AMI).
func (v *view) emitState(x *extState, at time.Time) {
	v.rc.emit(model.RegNote{At: at, Number: x.number, State: x.regState()})
}

// openDown abre o "fora do ar" do ramal em at (observed ou o tipo dado).
func (x *extState) openDown(at time.Time, kind string) {
	x.downSince, x.downSeg, x.downKind = at, at, kind
}

// clearDown zera o "fora do ar" (lastReg fica).
func (x *extState) clearDown() { x.downSince, x.downSeg, x.downKind = time.Time{}, time.Time{}, "" }

// noteRegistration roda ANTES de a visao gravar o novo estado do ramal (precisa do anterior). So vale na visao
// publicada: a visao em montagem (sincronizacao) nunca conta; a reconciliacao do SyncFinished cobre.
func (v *view) noteRegistration(x *extState, e model.RegistrationChanged) {
	rc := v.rc
	if rc == nil || v.isBuilding {
		return
	}
	from, to := x.reg, e.State
	if from == to {
		return // refresh do mesmo estado: nada muda
	}
	kind := ""
	if e.Live {
		kind = transitionKind(from, to)
	}
	system := e.At.Before(rc.until)
	reason := model.ReasonCounted
	switch {
	case from == model.RegUnknown:
		reason = model.ReasonFirstState
	case !e.Live:
		reason = model.ReasonReread
	case kind == "":
		reason = model.ReasonOutOfTable
	case system:
		reason = model.ReasonPause
	}
	rc.decide(e.At, x.number, from, to, kind != "" && !system, reason)

	// Estado "fora do ar" (ADR-010), ao vivo ou nao, dentro da pausa ou nao.
	changed := false
	var down time.Time // trecho que fecha nesta transicao
	switch {
	case isOut(to) && x.downSince.IsZero():
		x.openDown(e.At, model.DownObserved)
		changed = true
	case to == model.RegRegistered && !x.downSince.IsZero():
		down = x.downSeg
		x.clearDown()
		changed = true
	}
	if to == model.RegRegistered {
		x.lastReg, x.lastRegExact = e.At, e.Live
		changed = true
	}

	if kind != "" {
		ip, port := e.IP, e.Port
		if ip == "" { // queda: o evento nao traz endereco; vale o do estado anterior
			ip, port = x.ip, x.port
		}
		ip, port = ipv4(ip, port)
		n := model.RegNote{At: e.At, Number: x.number, Name: x.name, Kind: kind, System: system, IP: ip, Port: port,
			UserAgent: model.CleanText(e.UserAgent, maxReportRuneUA)}
		if !down.IsZero() && e.At.After(down) {
			n.DownFrom = down
		}
		if !system && (kind == model.RegKindLost || kind == model.RegKindUnreachable) {
			rc.count(kind, x.groups) // dentro da pausa a ocorrencia vai em system_events, nao nos contadores
		}
		rc.emit(n)
	} else if !down.IsZero() && e.At.After(down) {
		rc.emit(model.RegNote{At: e.At, Number: x.number, Name: x.name, DownFrom: down})
	}
	if changed {
		v.emitState(x, e.At)
	}
}

// noteCreatedOut abre o "fora do ar" de um ramal criado agora na visao publicada (onEndpointSeen passa de
// desconhecido para nao registrado): fora desde que apareceu no PBX (observed).
func (v *view) noteCreatedOut(x *extState, at time.Time) {
	if v.rc == nil || v.isBuilding || !x.downSince.IsZero() {
		return
	}
	x.openDown(at, model.DownObserved)
	v.emitState(x, at)
}

// closeDown fecha o "fora do ar" do ramal removido do estado: o trecho [downSeg, at) fecha sem lacuna, down* zera
// e lastReg fica guardado (ADR-010 decisao 7).
func (v *view) closeDown(x *extState, at time.Time) {
	if v.rc == nil || x.downSince.IsZero() {
		return
	}
	if at.IsZero() {
		at = v.rc.now()
	}
	if at.After(x.downSeg) {
		v.rc.emit(model.RegNote{At: at, Number: x.number, Name: x.name, DownFrom: x.downSeg})
	}
	x.clearDown()
	v.emitState(x, at)
}

// ---- reconciliacao no SyncFinished (CONTRATOS 11.5 item 3; substitui o carryDown) ----

// regBefore e o "antes" de um ramal na reconciliacao: da visao antiga ou, so na primeira sincronizacao do
// processo, da linha de reg_state lida na subida.
type regBefore struct {
	present      bool
	downSince    time.Time
	downSeg      time.Time
	lastReg      time.Time
	downKind     string
	lastRegExact bool
}

func beforeFromView(x *extState) regBefore {
	return regBefore{present: true, downSince: x.downSince, downSeg: x.downSeg, lastReg: x.lastReg, downKind: x.downKind, lastRegExact: x.lastRegExact}
}

func beforeFromRow(r model.RegState) regBefore {
	return regBefore{present: true, downSince: r.DownSince, downSeg: r.DownSeg, lastReg: r.LastReg, downKind: r.DownKind, lastRegExact: r.LastRegExact}
}

// reconcileRegState compara o "antes" com a visao nova e acerta o estado "fora do ar" de cada numero. Nunca
// inventa tempo: o trecho fecha no ultimo instante em que o painel viu o ramal fora (cap), com lacuna quando o
// painel ou o AMI estavam parados.
func (s *State) reconcileRegState(old *view, syncAt time.Time) {
	rc := s.rc
	first := !s.regSyncDone
	before := map[string]regBefore{}
	fresh := false
	var lastAlive time.Time
	if first {
		if l := s.rsLoaded; l != nil {
			fresh, lastAlive = l.Fresh, l.LastAlive
			for n, r := range l.Rows {
				before[n] = beforeFromRow(r)
			}
		}
	} else {
		for n, x := range old.exts {
			before[n] = beforeFromView(x)
		}
	}
	s.regSyncDone, s.rsLoaded = true, nil // o RegStateLoaded vale so para a primeira sincronizacao
	cap := syncAt
	switch {
	case first:
		// sem referencia (leitura falhou ou nao ha minuto com gap = 0) o cap zero nao soma tempo: closeSeg fecha o
		// trecho com tamanho zero e lacuna, e o que continua fora recomeca em syncAt; syncAt como cap contaria o daemon parado.
		cap = time.Time{}
		if !lastAlive.IsZero() && !lastAlive.After(syncAt) {
			cap = lastAlive
		}
	case rc.syncReason != model.SyncOverflow: // connect depois de queda do AMI
		cap = time.Time{} // LastDisconnect zero = cap desconhecido: fecha com tamanho zero e lacuna, sem somar tempo
		if !s.lastDown.IsZero() {
			cap = s.lastDown
			if cap.After(syncAt) {
				cap = syncAt
			}
		}
	}
	rcn := reconCtx{at: syncAt, cap: cap, lacuna: rc.syncReason != model.SyncOverflow, fresh: fresh}
	for n, b := range before {
		s.reconcileNumber(n, b, s.cur.exts[n], rcn)
	}
	for n, nx := range s.cur.exts {
		if _, ok := before[n]; !ok {
			s.reconcileNumber(n, regBefore{}, nx, rcn)
		}
	}
}

type reconCtx struct {
	at, cap       time.Time
	lacuna, fresh bool
}

// reconcileNumber aplica a tabela da 11.5 item 3 a um numero. nx = o ramal na visao nova (nil = sumiu do PBX).
func (s *State) reconcileNumber(n string, b regBefore, nx *extState, c reconCtx) {
	rc := s.rc
	nowOut := nx != nil && isOut(nx.reg)
	nowOk := nx != nil && nx.reg == model.RegRegistered
	name := ""
	if nx != nil {
		name = nx.name
	}
	// Com cap desconhecido (zero) ou anterior a from, nenhum tempo e inventado: com lacuna fecha com tamanho zero
	// (DownFrom = At = from) para marcar down_gap; sem lacuna nao ha nota.
	closeSeg := func(from time.Time, gap bool) {
		switch {
		case c.cap.After(from):
			rc.emit(model.RegNote{At: c.cap, Number: n, Name: name, DownFrom: from, DownGap: gap})
		case gap:
			rc.emit(model.RegNote{At: from, Number: n, Name: name, DownFrom: from, DownGap: true})
		}
	}
	// carry passa para a visao nova o que a regra nao troca.
	carry := func() {
		if nx != nil {
			nx.downSince, nx.downSeg, nx.downKind, nx.lastReg, nx.lastRegExact = b.downSince, b.downSeg, b.downKind, b.lastReg, b.lastRegExact
		}
	}
	final := b // estado final a gravar quando mudou
	changed := false
	switch {
	case b.present && !b.downSince.IsZero(): // fora antes
		switch {
		case nx == nil: // sumiu do PBX
			closeSeg(b.downSeg, c.lacuna)
			final.downSince, final.downSeg, final.downKind = time.Time{}, time.Time{}, ""
			changed = true
			rc.decide(c.at, n, model.RegUnregistered, model.RegUnknown, false, model.ReasonReconcile)
		case nowOut:
			carry()
			if c.lacuna { // o painel ficou parado: fecha o trecho no cap e comeca outro agora
				closeSeg(b.downSeg, true)
				nx.downSeg = c.at
				changed = true
			}
		case nowOk:
			closeSeg(b.downSeg, c.lacuna)
			carry()
			nx.clearDown()
			nx.lastReg, nx.lastRegExact = c.at, false
			changed = true
			rc.decide(c.at, n, model.RegUnregistered, nx.reg, false, model.ReasonReconcile)
		default: // sem estado conhecido: leva o que havia
			carry()
		}
	case b.present: // ok antes
		carry()
		if nowOut {
			nx.openDown(c.at, model.DownUnknownStart)
			changed = true
			rc.decide(c.at, n, model.RegRegistered, nx.reg, false, model.ReasonReconcile)
		}
	default: // ausente
		if nx == nil {
			return
		}
		switch {
		case nowOut:
			kind := model.DownUnknownStart
			if c.fresh {
				kind = model.DownBeforeInstall
			}
			nx.openDown(c.at, kind)
			changed = true
			rc.decide(c.at, n, model.RegUnknown, nx.reg, false, model.ReasonReconcile)
		case nowOk:
			nx.lastReg, nx.lastRegExact = c.at, false
			changed = true
		}
	}
	if !changed {
		return
	}
	if nx != nil {
		rc.emit(model.RegNote{At: c.at, Number: n, State: nx.regState()})
		return
	}
	rc.emit(model.RegNote{At: c.at, Number: n, State: &model.RegState{LastReg: final.lastReg, LastRegExact: final.lastRegExact}})
}
