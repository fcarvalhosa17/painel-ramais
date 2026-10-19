package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"painel-ramais/internal/store"
)

// Relatorio de registro (rc.7, ADR-007, CONTRATOS 4.7): quatro GET de leitura, sessao de qualquer papel.

const (
	regDBTimeout    = 10 * time.Second // limite de cada consulta ao banco
	regSummaryTop   = 10               // itens do bloco de instabilidade
	regEventsLimit  = 200              // ocorrencias brutas por ramal (o gravador guarda no maximo isso)
	regSignalsLimit = 100              // sinais do sistema em /series
	maxCursorLen    = 64
)

// regPeriod e o periodo pedido (CONTRATOS 4.7, "Periodo"): from e o inicio, to e sempre agora.
type regPeriod struct {
	name   string
	from   time.Time
	step   string // "hour" ou "day": passo do grafico
	points int
}

// parseRegPeriod interpreta ?period (24h padrao, 7d, 30d). Os dias cortam a meia-noite do fuso do servidor
// (time.Local); o 24h comeca na hora cheia local atual menos 23 h.
func parseRegPeriod(v string, now time.Time) (regPeriod, bool) {
	l := now.In(time.Local)
	switch v {
	case "", "24h":
		h := time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), 0, 0, 0, time.Local)
		return regPeriod{"24h", h.Add(-23 * time.Hour), "hour", 24}, true
	case "7d":
		return regPeriod{"7d", time.Date(l.Year(), l.Month(), l.Day()-6, 0, 0, 0, 0, time.Local), "day", 7}, true
	case "30d":
		return regPeriod{"30d", time.Date(l.Year(), l.Month(), l.Day()-29, 0, 0, 0, 0, time.Local), "day", 30}, true
	}
	return regPeriod{}, false
}

// starts devolve o inicio de cada intervalo do grafico (uma hora ou um dia local).
func (p regPeriod) starts() []time.Time {
	out := make([]time.Time, p.points)
	l := p.from.In(time.Local)
	for i := range out {
		if p.step == "hour" {
			out[i] = p.from.Add(time.Duration(i) * time.Hour)
		} else {
			out[i] = time.Date(l.Year(), l.Month(), l.Day()+i, 0, 0, 0, 0, time.Local)
		}
	}
	return out
}

func (p regPeriod) fromHour() int64 { return hourFloor(p.from.Unix()) }

func hourFloor(unix int64) int64 { return unix - unix%3600 }

func stamp(t time.Time) string { return t.UTC().Format(stampLayout) }

func stampPtr(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	s := stamp(*t)
	return &s
}

func unixStampPtr(unix int64) *string {
	if unix <= 0 {
		return nil
	}
	s := stamp(time.Unix(unix, 0))
	return &s
}

func (s *Server) regCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), regDBTimeout)
}

// regHead e o cabecalho comum de todo data: period, from, to, since.
type regHead struct {
	Period string `json:"period"`
	From   string `json:"from"`
	To     string `json:"to"`
	Since  string `json:"since"`
}

func (s *Server) regHead(ctx context.Context, p regPeriod, now time.Time) (regHead, error) {
	since, err := s.Store.RegSince(ctx)
	if err != nil {
		return regHead{}, err
	}
	return regHead{Period: p.name, From: stamp(p.from), To: stamp(now), Since: stamp(since)}, nil
}

// regRow e a linha de ramal (4.7): a mesma em /registrations e /registrations/extension.
type regRow struct {
	Number        string   `json:"number"`
	Name          *string  `json:"name"`
	Groups        []string `json:"groups"`
	Removed       bool     `json:"removed"`
	Lost          int64    `json:"lost_registration"`
	Unreachable   int64    `json:"became_unreachable"`
	Registered    int64    `json:"registered"`
	Reachable     int64    `json:"reachable_again"`
	SystemEvents  int64    `json:"system_events"`
	LastDropAt    *string  `json:"last_drop_at"`
	LastIP        *string  `json:"last_ip"`
	LastPort      *int     `json:"last_port"`
	DownS         int64    `json:"down_s"`
	DownOpenSince *string  `json:"down_open_since"`
}

// buildRegRow junta o agregado do banco com o estado atual do ramal (nome, grupos, intervalo aberto).
// Ramal fora da projecao = removed: sem grupos e sem intervalo aberto; o nome e o ultimo gravado.
func buildRegRow(r store.RegRow, info ExtInfo, present bool) regRow {
	row := regRow{
		Number: r.Number, Groups: []string{}, Removed: !present,
		Lost: r.Lost, Unreachable: r.Unreachable, Registered: r.Registered, Reachable: r.Reachable,
		SystemEvents: r.SystemEvents, DownS: r.DownS, LastDropAt: unixStampPtr(r.LastDropAt),
	}
	name := r.Name
	if present {
		row.Groups = info.Groups
		row.DownOpenSince = stampPtr(info.DownSince)
		if info.Name != "" {
			name = info.Name
		}
	}
	if name != "" {
		row.Name = &name
	}
	if r.LastIP != "" {
		ip := r.LastIP
		row.LastIP = &ip
		if r.LastPort > 0 {
			port := r.LastPort
			row.LastPort = &port
		}
	}
	return row
}

// listed diz se a linha entra na tabela: algum dos quatro contadores, tempo fora do ar fechado ou aberto.
func (r regRow) listed() bool {
	return r.Lost+r.Unreachable+r.Registered+r.Reachable > 0 || r.DownS > 0 || r.DownOpenSince != nil
}

// regCursor e o cursor opaco de /api/registrations: "<inicio da hora em unix>.<revisao>".
func regCursor(hour, rev int64) string {
	return strconv.FormatInt(hour, 10) + "." + strconv.FormatInt(rev, 10)
}

func parseRegCursor(c string) (hour, rev int64, ok bool) {
	a, b, found := strings.Cut(c, ".")
	if !found {
		return 0, 0, false
	}
	hour, err1 := strconv.ParseInt(a, 10, 64)
	rev, err2 := strconv.ParseInt(b, 10, 64)
	return hour, rev, err1 == nil && err2 == nil && hour >= 0 && rev >= 0
}

// ---- GET /api/registrations ----

// registrations: tabela da aba Registros (4.7.2). Sem filtro de grupo nem busca (a tela filtra). Com cursor
// valido devolve so os ramais com linha gravada depois dele (full false); senao a tabela inteira (full true).
func (s *Server) registrations(w http.ResponseWriter, r *http.Request, _ store.Session) {
	now := s.Now()
	q := r.URL.Query()
	p, ok := parseRegPeriod(q.Get("period"), now)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "period deve ser 24h, 7d ou 30d")
		return
	}
	cursor := q.Get("cursor")
	if utf8.RuneCountInString(cursor) > maxCursorLen {
		writeError(w, http.StatusBadRequest, "bad_request", "cursor acima de 64 caracteres")
		return
	}
	infos, ok := s.hub.Infos()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "degraded", "Servico parando")
		return
	}
	ctx, cancel := s.regCtx(r)
	defer cancel()
	head, err := s.regHead(ctx, p, now)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// A revisao e lida ANTES das linhas: o que for gravado no meio volta na chamada seguinte (repetir e inofensivo).
	maxRev, err := s.Store.MaxRegRev(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	thisHour := hourFloor(now.Unix())
	hour, rev, valid := parseRegCursor(cursor)
	full := !valid || hour != thisHour || rev > maxRev // sem cursor, janela andou, ou banco limpo/restaurado
	var after int64
	if !full {
		after = rev
	}
	dbRows, err := s.Store.RegRows(ctx, p.fromHour(), after, "", false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	rows := make([]regRow, 0, len(dbRows))
	seen := make(map[string]struct{}, len(dbRows))
	for _, d := range dbRows {
		seen[d.Number] = struct{}{}
		info, present := infos[d.Number]
		if row := buildRegRow(d, info, present); row.listed() {
			rows = append(rows, row)
		}
	}
	if full { // ramal caido agora que nao tem linha no periodo (caiu antes dele) tambem entra
		for n, info := range infos {
			if _, ok := seen[n]; !ok && info.DownSince != nil {
				rows = append(rows, buildRegRow(store.RegRow{Number: n}, info, true))
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Number < rows[j].Number })
	}
	writeOK(w, struct {
		regHead
		Cursor string   `json:"cursor"`
		Full   bool     `json:"full"`
		Rows   []regRow `json:"rows"`
	}{head, regCursor(thisHour, maxRev), full, rows}, map[string]int{"count": len(rows)})
}

// ---- GET /api/registrations/summary ----

type summaryItem struct {
	Number      string  `json:"number"`
	Name        *string `json:"name"`
	Removed     bool    `json:"removed"`
	Lost        int64   `json:"lost_registration"`
	Unreachable int64   `json:"became_unreachable"`
}

// registrationsSummary: bloco "Instabilidade de registro (24 h)" (4.7.1). Pertenca ATUAL ao grupo; ate 10
// ramais com perdeu + ficou inalcancavel > 0, do mais instavel para o menos.
func (s *Server) registrationsSummary(w http.ResponseWriter, r *http.Request, _ store.Session) {
	now := s.Now()
	q := r.URL.Query()
	group := q.Get("group")
	if utf8.RuneCountInString(group) > store.MaxGroupNameRunes {
		writeError(w, http.StatusBadRequest, "bad_request", "group acima de 64 caracteres")
		return
	}
	p, _ := parseRegPeriod("24h", now)
	infos, ok := s.hub.Infos()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "degraded", "Servico parando")
		return
	}
	ctx, cancel := s.regCtx(r)
	defer cancel()
	head, err := s.regHead(ctx, p, now)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	dbRows, err := s.Store.RegRows(ctx, p.fromHour(), 0, "", true)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	items := make([]summaryItem, 0, regSummaryTop)
	for _, d := range dbRows {
		info, present := infos[d.Number]
		if group != "" && !(present && inGroup(info.Groups, group)) { // removido nao pertence a grupo nenhum
			continue
		}
		row := buildRegRow(d, info, present)
		items = append(items, summaryItem{d.Number, row.Name, row.Removed, d.Lost, d.Unreachable})
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if sa, sb := a.Lost+a.Unreachable, b.Lost+b.Unreachable; sa != sb {
			return sa > sb
		}
		if a.Lost != b.Lost {
			return a.Lost > b.Lost
		}
		return a.Number < b.Number
	})
	if len(items) > regSummaryTop {
		items = items[:regSummaryTop]
	}
	var g any
	if group != "" {
		g = group
	}
	writeOK(w, struct {
		regHead
		Group any           `json:"group"`
		Items []summaryItem `json:"items"`
	}{head, g, items}, map[string]int{"count": len(items)})
}

func inGroup(groups []string, g string) bool {
	for _, x := range groups {
		if x == g {
			return true
		}
	}
	return false
}

// ---- GET /api/registrations/extension ----

type regEventJSON struct {
	At        string  `json:"at"`
	Kind      string  `json:"kind"`
	System    bool    `json:"system"`
	IP        *string `json:"ip"`
	Port      *int    `json:"port"`
	UserAgent *string `json:"user_agent"`
}

// registrationsExtension: linha do tempo de um ramal (4.7.3): a linha do periodo mais as ocorrencias brutas.
func (s *Server) registrationsExtension(w http.ResponseWriter, r *http.Request, _ store.Session) {
	now := s.Now()
	q := r.URL.Query()
	number := q.Get("number")
	if number == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Parametro number obrigatorio")
		return
	}
	if err := store.ValidNumber(number); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	p, ok := parseRegPeriod(q.Get("period"), now)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "period deve ser 24h, 7d ou 30d")
		return
	}
	info, present, ok := s.hub.Info(number)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "degraded", "Servico parando")
		return
	}
	ctx, cancel := s.regCtx(r)
	defer cancel()
	head, err := s.regHead(ctx, p, now)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	dbRows, err := s.Store.RegRows(ctx, p.fromHour(), 0, number, false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	d := store.RegRow{Number: number}
	if len(dbRows) > 0 {
		d = dbRows[0]
	}
	evs, err := s.Store.RegEvents(ctx, number, p.from.Unix(), regEventsLimit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	events := make([]regEventJSON, 0, len(evs))
	for _, e := range evs {
		ev := regEventJSON{At: stamp(time.Unix(e.At, 0)), Kind: e.Kind, System: e.System}
		if e.IP != "" {
			ip := e.IP
			ev.IP = &ip
			if e.Port > 0 {
				port := e.Port
				ev.Port = &port
			}
		}
		if e.UserAgent != "" {
			ua := e.UserAgent
			ev.UserAgent = &ua
		}
		events = append(events, ev)
	}
	writeOK(w, struct {
		regHead
		Extension   regRow         `json:"extension"`
		Events      []regEventJSON `json:"events"`
		EventsLimit int            `json:"events_limit"`
	}{head, buildRegRow(d, info, present), events, regEventsLimit}, map[string]int{"count": len(events)})
}

// ---- GET /api/registrations/series ----

type seriesPoint struct {
	T           string `json:"t"`
	Lost        *int64 `json:"lost_registration"`
	Unreachable *int64 `json:"became_unreachable"`
}

type signalJSON struct {
	At   string `json:"at"`
	Kind string `json:"kind"`
}

// registrationsSeries: grafico da aba Registros (4.7.4): quedas por hora (24h) ou por dia (7d, 30d), do total
// ou de um grupo, a partir de history (pertenca ao grupo NO minuto em que aconteceu).
func (s *Server) registrationsSeries(w http.ResponseWriter, r *http.Request, _ store.Session) {
	now := s.Now()
	q := r.URL.Query()
	group := q.Get("group")
	if utf8.RuneCountInString(group) > store.MaxGroupNameRunes {
		writeError(w, http.StatusBadRequest, "bad_request", "group acima de 64 caracteres")
		return
	}
	p, ok := parseRegPeriod(q.Get("period"), now)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "period deve ser 24h, 7d ou 30d")
		return
	}
	ctx, cancel := s.regCtx(r)
	defer cancel()
	head, err := s.regHead(ctx, p, now)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	mins, err := s.Store.RegSeries(ctx, group, p.from.Unix(), now.Unix()+1)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sigs, err := s.Store.RegSignals(ctx, p.from.Unix(), regSignalsLimit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	starts := p.starts()
	type acc struct {
		lost, drop int64
		has        bool
	}
	buckets := make([]acc, len(starts))
	for _, m := range mins {
		i := sort.Search(len(starts), func(i int) bool { return starts[i].Unix() > m.Minute }) - 1
		if i < 0 {
			continue
		}
		buckets[i].lost += m.Lost
		buckets[i].drop += m.Unreachable
		buckets[i].has = true
	}
	points := make([]seriesPoint, len(starts))
	for i, st := range starts {
		points[i] = seriesPoint{T: stamp(st)}
		if buckets[i].has { // sem nenhum minuto com dado no intervalo: null
			lost, drop := buckets[i].lost, buckets[i].drop
			points[i].Lost, points[i].Unreachable = &lost, &drop
		}
	}
	events := make([]signalJSON, len(sigs))
	for i, sg := range sigs {
		events[i] = signalJSON{At: stamp(time.Unix(sg.At, 0)), Kind: sg.Kind}
	}
	var g any
	if group != "" {
		g = group
	}
	writeOK(w, struct {
		regHead
		Group        any           `json:"group"`
		Step         string        `json:"step"`
		Points       []seriesPoint `json:"points"`
		SystemEvents []signalJSON  `json:"system_events"`
	}{head, g, p.step, points, events}, map[string]int{"count": len(points)})
}
