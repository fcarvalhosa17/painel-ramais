package api

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"painel-ramais/internal/depcheck"
	"painel-ramais/internal/model"
	"painel-ramais/internal/store"
)

// ---- snapshot e groups ----

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request, _ store.Session) {
	snap, ok := s.hub.Snapshot()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "degraded", "Servico parando")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	body := snap.raw
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip") // ja comprimido e em cache; o middleware nao recomprime
		body = snap.Gzip()
	}
	w.Write(body)
}

func (s *Server) groups(w http.ResponseWriter, r *http.Request, _ store.Session) {
	raw, ok := s.hub.Groups()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "degraded", "Servico parando")
		return
	}
	writeJSON(w, http.StatusOK, raw)
}

// ---- history ----

const (
	historyStep    = time.Minute
	historyMaxSpan = 30 * 24 * time.Hour
)

type historyPoint struct {
	T            string `json:"t"`
	Registered   *int   `json:"registered"`
	Unregistered *int   `json:"unregistered"`
	Unreachable  *int   `json:"unreachable"`
	InCall       *int   `json:"in_call"`
	Gap          bool   `json:"gap"`
}

func (s *Server) history(w http.ResponseWriter, r *http.Request, _ store.Session) {
	q := r.URL.Query()
	now := s.Now()
	from, to, group, msg := parseHistoryQuery(q.Get("from"), q.Get("to"), q.Get("group"), now)
	if msg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	rows, err := s.Store.QueryHistory(r.Context(), group, from, to)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	byMinute := make(map[int64]store.HistoryRow, len(rows))
	for _, row := range rows {
		byMinute[row.Minute.Unix()] = row
	}
	points := make([]historyPoint, 0, int(to.Sub(from)/historyStep)+1)
	for m := from; m.Before(to); m = m.Add(historyStep) {
		points = append(points, point(m, byMinute))
	}
	data := map[string]any{"group": groupOrNull(q, group), "step_s": int(historyStep.Seconds()), "points": points}
	writeOK(w, data, map[string]int{"retention_days": s.RetentionDays, "count": len(points)})
}

func groupOrNull(q map[string][]string, group string) any {
	if _, present := q["group"]; present && group != "" {
		return group
	}
	return nil
}

func point(m time.Time, by map[int64]store.HistoryRow) historyPoint {
	p := historyPoint{T: m.UTC().Format(stampLayout), Gap: true}
	row, ok := by[m.Unix()]
	if !ok || row.Gap {
		return p
	}
	p.Gap = false
	p.Registered, p.Unregistered, p.Unreachable, p.InCall = &row.Registered, &row.Unregistered, &row.Unreachable, &row.InCall
	return p
}

// parseHistoryQuery aplica as regras da secao 4.3; msg != "" e o erro 400.
func parseHistoryQuery(fromS, toS, group string, now time.Time) (from, to time.Time, g, msg string) {
	to = now
	if toS != "" {
		t, err := time.Parse(time.RFC3339, toS)
		if err != nil {
			return from, to, "", "Parametro to invalido (use RFC 3339)"
		}
		to = t
	}
	from = to.Add(-time.Hour)
	if fromS != "" {
		t, err := time.Parse(time.RFC3339, fromS)
		if err != nil {
			return from, to, "", "Parametro from invalido (use RFC 3339)"
		}
		from = t
	}
	from = from.UTC().Truncate(time.Minute)
	switch {
	case !to.After(from):
		return from, to, "", "to precisa ser maior que from"
	case to.Sub(from) > historyMaxSpan:
		return from, to, "", "Intervalo acima de 30 dias"
	case len([]rune(group)) > 64:
		return from, to, "", "group acima de 64 caracteres"
	}
	return from, to.UTC(), group, ""
}

// ---- health ----

type healthAMI struct {
	Connected      bool    `json:"connected"`
	Technology     *string `json:"technology"`
	Asterisk       *string `json:"asterisk_version"`
	ConnectedSince *string `json:"connected_since"`
	Reconnects     int     `json:"reconnects"`
}

type healthDep struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
	CheckedAt string `json:"checked_at"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	st, ami := s.Stats(), s.AMI()
	pub := s.hub.Pub()
	deps := s.Deps()
	status := "ok"
	if !ami.Connected {
		status = "degraded"
	}
	out := make([]healthDep, len(deps))
	for i, d := range deps {
		out[i] = healthDep{d.Name, d.Status, d.Detail, d.CheckedAt.UTC().Format(stampLayout)}
		if d.Status != depcheck.StatusOK {
			status = "degraded"
		}
	}
	tech := string(ami.Technology)
	if tech == "" {
		tech = string(st.Technology)
	}
	data := map[string]any{
		"status": status, "daemon_version": s.Version, "uptime_s": int64(s.Now().Sub(s.started).Seconds()),
		"ami": healthAMI{
			Connected: ami.Connected, Technology: model.StrPtr(tech), Asterisk: model.StrPtr(ami.AsteriskVersion),
			ConnectedSince: sinceOrNil(ami.ConnectedSince), Reconnects: ami.Reconnects,
		},
		"events_per_s": float64(int(st.EventsPerSec*10+0.5)) / 10,
		"queue":        map[string]any{"len": ami.QueueLen, "cap": ami.QueueCap, "overflows": ami.Overflows},
		"lag_ms":       st.LagMs, "epoch": pub.Epoch, "version": pub.Version,
		"extensions": map[string]int{
			"total": st.Total.Total(), "registered": st.Total.Registered, "unregistered": st.Total.Unregistered,
			"unreachable": st.Total.Unreachable, "unknown": st.Total.Unknown,
		},
		"trunks": st.Trunks, "active_calls": st.ActiveCalls, "ws_clients": pub.Clients,
		"registrations": s.healthRegistrations(st.SuppressedUntil), "live_calls": st.LiveCalls,
		"queues": s.healthQueues(st.Queues), "deps": out,
	}
	code := http.StatusOK
	if status != "ok" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, mustJSON(envelope{Success: true, Data: data}))
}

// healthRegistrations e o bloco "registrations" do /api/health (fila do gravador do relatorio; nao muda o status).
func (s *Server) healthRegistrations(suppressedUntil time.Time) map[string]any {
	var rs model.RegStatus
	if s.RegStatus != nil {
		rs = s.RegStatus()
	}
	return map[string]any{
		"queue_len": rs.QueueLen, "queue_cap": rs.QueueCap, "dropped": rs.Dropped, "write_errors": rs.WriteErrors,
		"lost_events": rs.LostEvents, "lost_since": sinceOrNil(rs.LostSince), "lost_until": sinceOrNil(rs.LostUntil),
		"suppressed_until": sinceOrNil(suppressedUntil),
	}
}

// healthQueues e o bloco "queues" do /api/health (rc.8): filas na projecao e a leitura da QueueStatus; nao muda o status.
func (s *Server) healthQueues(count int) map[string]any {
	var qi model.QueueHealth
	if s.QueueInfo != nil {
		qi = s.QueueInfo()
	}
	return map[string]any{"count": count, "last_full_at": sinceOrNil(qi.LastFullAt), "refresh_errors": qi.RefreshErrors, "unsupported": qi.Unsupported}
}

func sinceOrNil(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(stampLayout)
	return &s
}

// ---- debug ----

var debugTmpl = template.Must(template.New("debug").Parse(`<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8"><meta http-equiv="refresh" content="2">
<title>painel-ramais debug</title>
<style>body{font:13px monospace;margin:1em}td,th{padding:2px 10px;text-align:left}</style></head><body>
<h3>painel-ramais {{.Version}}: epoch {{.Epoch}} versao {{.HubVersion}} AMI {{.AMI}} tecnologia {{.Tech}} clientes WS {{.Clients}}</h3>
<h4>Ramais por estado</h4>
<table><tr><th>total</th><th>registered</th><th>unregistered</th><th>unreachable</th><th>unknown</th><th>com chamada</th><th>troncos</th></tr>
<tr><td>{{.Total}}</td><td>{{.Reg}}</td><td>{{.Unreg}}</td><td>{{.Unreach}}</td><td>{{.Unknown}}</td><td>{{.InCall}}</td><td>{{.Trunks}}</td></tr></table>
<h4>Chamadas ativas ({{len .Calls}})</h4>
<table><tr><th>ramal</th><th>direcao</th><th>estado</th><th>outro lado</th><th>duracao</th></tr>
{{range .Calls}}<tr><td>{{.Ext}}</td><td>{{.Dir}}</td><td>{{.State}}</td><td>{{.Peer}}</td><td>{{.Dur}}</td></tr>
{{end}}</table>
<h4>Ultimos lotes</h4>
<table><tr><th>de</th><th>ate</th><th>ramais</th><th>bytes</th><th>quais</th></tr>
{{range .Batches}}<tr><td>{{.From}}</td><td>{{.To}}</td><td>{{.N}}</td><td>{{.Bytes}}</td><td>{{.Nums}}</td></tr>
{{end}}</table></body></html>`))

type debugCall struct{ Ext, Dir, State, Peer, Dur string }
type debugBatch struct {
	From, To uint64
	N, Bytes int
	Nums     string
}

func (s *Server) debug(w http.ResponseWriter, r *http.Request, _ store.Session) {
	d, ok := s.hub.Debug()
	if !ok {
		http.Error(w, "servico parando", http.StatusServiceUnavailable)
		return
	}
	st := s.Stats()
	now := s.Now()
	v := map[string]any{
		"Version": s.Version, "Epoch": d.Epoch, "HubVersion": d.Version, "AMI": st.AMIConnected, "Tech": st.Technology,
		"Clients": d.Clients, "Total": st.Total.Total(), "Reg": st.Total.Registered, "Unreg": st.Total.Unregistered,
		"Unreach": st.Total.Unreachable, "Unknown": st.Total.Unknown, "InCall": st.Total.InCall, "Trunks": st.Trunks,
		"Calls": debugCalls(d.Calls, now), "Batches": debugBatches(d.Batches),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := debugTmpl.Execute(w, v); err != nil {
		s.log.Error("debug", requestIDKey, requestID(r), "erro", err.Error())
	}
}

func debugCalls(calls []DebugCall, now time.Time) []debugCall {
	out := make([]debugCall, 0, len(calls))
	for _, c := range calls {
		peer := string(c.Call.Peer.Kind)
		if c.Call.Peer.Number != nil {
			peer += " " + *c.Call.Peer.Number
		}
		if c.Call.Peer.Name != nil {
			peer += " (" + *c.Call.Peer.Name + ")"
		}
		from := c.Call.StartedAt.Time
		if c.Call.AnsweredAt != nil {
			from = c.Call.AnsweredAt.Time
		}
		out = append(out, debugCall{c.Extension, string(c.Call.Direction), string(c.Call.State), peer,
			now.Sub(from).Truncate(time.Second).String()})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ext < out[j].Ext })
	return out
}

func debugBatches(items []ringItem) []debugBatch {
	out := make([]debugBatch, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- { // mais novo primeiro
		it := items[i]
		nums := it.nums
		more := ""
		if len(nums) > 10 {
			more = fmt.Sprintf(" ... +%d", len(nums)-10)
			nums = nums[:10]
		}
		out = append(out, debugBatch{it.from, it.to, it.n, len(it.data), strings.Join(nums, " ") + more})
	}
	return out
}
