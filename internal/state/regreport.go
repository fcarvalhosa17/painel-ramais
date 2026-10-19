package state

import (
	"net/netip"
	"time"

	"painel-ramais/internal/model"
)

// Relatorio de registro (ADR-007, CONTRATOS 11.5). Tudo aqui roda na goroutine do estado e NUNCA
// bloqueia nem toca em banco: o resultado sai por Options.OnReg, que so enfileira.

// systemWindow e a janela de evento do sistema depois de Shutdown, FullyBooted ou conexao do AMI:
// transicao de registro dentro dela e gravada como evento do sistema (fora dos contadores).
const systemWindow = 120 * time.Second

// maxReportRuneUA e o teto do user agent guardado no relatorio.
const maxReportRuneUA = model.MaxUserAgentRunes

// regCtx e o relatorio de registro compartilhado entre o State e as suas views (a visao troca a cada
// sincronizacao, mas a janela e os contadores acumulados valem para o processo inteiro).
type regCtx struct {
	now        func() time.Time
	onReg      func(model.RegNote)
	until      time.Time // fim da janela de evento do sistema (zero = sem janela)
	total      model.RegCounts
	byGroup    map[string]model.RegCounts
	syncReason model.SyncReason // do SyncStarted em curso: so connect abre a janela
}

func newRegCtx(now func() time.Time, onReg func(model.RegNote)) *regCtx {
	return &regCtx{now: now, onReg: onReg, byGroup: map[string]model.RegCounts{}}
}

func (r *regCtx) emit(n model.RegNote) {
	if r.onReg != nil {
		r.onReg(n)
	}
}

// openWindow abre (ou estende) a janela a partir de at e gera o sinal do sistema.
func (r *regCtx) openWindow(at time.Time, signal string) {
	if end := at.Add(systemWindow); end.After(r.until) {
		r.until = end
	}
	r.emit(model.RegNote{At: at, Signal: signal})
}

// suppressedUntil devolve o fim da janela se ela ainda esta aberta em now; zero se nao.
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

// noteRegistration roda ANTES de a visao gravar o novo estado do ramal (precisa do anterior). So vale na
// visao publicada: a visao em montagem (sincronizacao) nunca conta.
func (v *view) noteRegistration(x *extState, e model.RegistrationChanged) {
	if v.rc == nil || v.isBuilding {
		return
	}
	if e.Live && x.reg != e.State {
		v.noteTransition(x, e)
	}
	// Voltou a registrar sem ocorrencia contada (releitura, evento que nao e ao vivo): o intervalo fecha
	// mesmo assim, para um ramal registrado nunca ficar "fora do ar".
	if e.State == model.RegRegistered && !x.downSince.IsZero() {
		v.rc.emit(model.RegNote{At: e.At, Number: x.number, Name: x.name, DownFrom: x.downSince})
		x.downSince = time.Time{}
	}
}

// noteTransition aplica a tabela de transicoes a uma mudanca de registro ao vivo.
func (v *view) noteTransition(x *extState, e model.RegistrationChanged) {
	kind := transitionKind(x.reg, e.State)
	if kind == "" {
		return
	}
	system := e.At.Before(v.rc.until)
	ip, port := e.IP, e.Port
	if ip == "" { // queda: o evento nao traz endereco; vale o do estado anterior
		ip, port = x.ip, x.port
	}
	ip, port = ipv4(ip, port)
	n := model.RegNote{At: e.At, Number: x.number, Name: x.name, Kind: kind, System: system, IP: ip, Port: port,
		UserAgent: model.CleanText(e.UserAgent, maxReportRuneUA)}
	switch kind {
	case model.RegKindLost, model.RegKindUnreachable:
		if !system { // transicao de sistema nunca abre intervalo fora do ar nem entra nos contadores
			if x.downSince.IsZero() {
				x.downSince = e.At
			}
			v.rc.count(kind, x.groups)
		}
	default: // registered e reachable_again fecham o intervalo, de sistema ou nao
		if !x.downSince.IsZero() {
			n.DownFrom, x.downSince = x.downSince, time.Time{}
		}
	}
	v.rc.emit(n)
}

// closeDown fecha o intervalo fora do ar de x em at, sem ocorrencia (ramal removido do estado).
func (v *view) closeDown(x *extState, at time.Time) {
	if v.rc == nil || x.downSince.IsZero() {
		return
	}
	if at.IsZero() {
		at = v.rc.now()
	}
	v.rc.emit(model.RegNote{At: at, Number: x.number, Name: x.name, DownFrom: x.downSince})
	x.downSince = time.Time{}
}

// carryDown passa os intervalos fora do ar abertos da visao antiga para a nova ao fim de uma
// sincronizacao: se o ramal voltou registrado fecha ali (sem ocorrencia); se sumiu do PBX tambem.
func (r *regCtx) carryDown(old, cur *view, at time.Time) {
	for n, ox := range old.exts {
		if ox.downSince.IsZero() {
			continue
		}
		nx := cur.exts[n]
		if nx == nil || nx.reg == model.RegRegistered {
			r.emit(model.RegNote{At: at, Number: n, Name: ox.name, DownFrom: ox.downSince})
			continue
		}
		nx.downSince = ox.downSince
	}
}
