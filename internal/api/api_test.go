package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/bcrypt"

	"painel-ramais/internal/auth"
	"painel-ramais/internal/depcheck"
	"painel-ramais/internal/model"
	"painel-ramais/internal/state"
	"painel-ramais/internal/store"
)

func init() { auth.BcryptCost = bcrypt.MinCost }

type env struct {
	t     *testing.T
	st    *store.Store
	hub   *Hub
	in    chan model.Output
	srv   *Server
	ts    *httptest.Server
	stats state.Stats
	ami   model.AMIStatus
	now   time.Time
	// reloads conta as chamadas a ReloadGroups; reloadErr, se nao nil, e o erro devolvido.
	reloads   int
	reloadErr error
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, in: make(chan model.Output, 128), now: time.Date(2026, 10, 13, 14, 30, 0, 0, time.UTC)}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.hub = NewHub(log, func() time.Time { return e.now })
	go e.hub.Run(ctx, e.in)
	secret, _ := base64.StdEncoding.DecodeString("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	ver := auth.NewVerifier(secret)
	ver.Now = func() time.Time { return time.Unix(1791902430, 0) }
	issabel, _ := base64.StdEncoding.DecodeString("ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8=")
	ver.SetIssabelKey(issabel)
	e.srv = New(Options{
		Static:       testStatic,
		ReloadGroups: func(context.Context) error { e.reloads++; return e.reloadErr },
		Store:        st, Verifier: ver, Limiter: nameLimiterForTest(), Hub: e.hub, SessionTTL: 8 * time.Hour, RetentionDays: 30,
		Stats: func() state.Stats { return e.stats }, AMI: func() model.AMIStatus { return e.ami },
		Deps:    func() []depcheck.Item { return []depcheck.Item{{Name: "ami_user", Status: "ok", CheckedAt: e.now}} },
		Version: "0.1.0-test", Log: log, Now: func() time.Time { return e.now },
	})
	e.ts = httptest.NewServer(e.srv.Handler())
	t.Cleanup(func() { e.ts.Close(); cancel(); st.Close() })
	return e
}

func (e *env) addUser(name, pw string) {
	e.t.Helper()
	h, err := auth.HashPassword(pw)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.CreateUser(context.Background(), name, h, e.now); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) do(method, path, body string, hdr map[string]string) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultTransport.RoundTrip(req) // sem gzip automatico nem cookie jar
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func readAll(t *testing.T, res *http.Response) []byte {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type body struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *apiError       `json:"error"`
	Meta    json.RawMessage `json:"meta"`
}

func parseBody(t *testing.T, res *http.Response) body {
	t.Helper()
	var b body
	raw := readAll(t, res)
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("corpo nao e envelope JSON (%d): %q", res.StatusCode, raw)
	}
	return b
}

func (e *env) expect(res *http.Response, status int, code string) body {
	e.t.Helper()
	b := parseBody(e.t, res)
	if res.StatusCode != status || (code != "" && (b.Error == nil || b.Error.Code != code)) {
		e.t.Fatalf("status %d (queria %d), erro %+v, corpo %s", res.StatusCode, status, b.Error, b.Data)
	}
	return b
}

// login devolve o cabecalho Cookie para as chamadas seguintes.
func (e *env) login() map[string]string {
	e.t.Helper()
	e.addUser("emergencia", "uma-senha-longa")
	res := e.do("POST", "/api/login", `{"username":"emergencia","password":"uma-senha-longa"}`, map[string]string{"Content-Type": "application/json"})
	e.expect(res, 200, "")
	for _, c := range res.Cookies() {
		if c.Name == cookieName {
			return map[string]string{"Cookie": cookieName + "=" + c.Value}
		}
	}
	e.t.Fatal("sem cookie")
	return nil
}

func ext(n, group string, reg model.Registration) model.Extension {
	return model.Extension{Number: n, Technology: model.TechPJSIP, Groups: []string{group}, Registration: reg, DeviceState: model.DevNotInUse, Calls: []model.Call{}}
}

func (e *env) reset(epoch string, version uint64, exts ...model.Extension) {
	e.in <- model.Reset{Epoch: epoch, Version: version, AMIConnected: true, Technology: model.TechPJSIP, Extensions: exts,
		Groups: []model.Group{{Name: "Suporte", Ranges: []model.Range{{Start: 2000, End: 2999}}}}}
	e.waitVersion(epoch, version)
}

func (e *env) batch(epoch string, from, to uint64, exts ...model.Extension) {
	e.in <- model.Batch{Epoch: epoch, FromVersion: from, ToVersion: to, AMIConnected: true, Extensions: exts}
	e.waitVersion(epoch, to)
}

func (e *env) waitVersion(epoch string, v uint64) {
	e.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p := e.hub.Pub(); p.Epoch == epoch && p.Version == v {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	e.t.Fatalf("hub nao chegou a %s/%d", epoch, v)
}

var jsonHdr = map[string]string{"Content-Type": "application/json"}

func TestAuthRequiredEverywhere(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/snapshot", "/api/groups", "/api/history", "/ws"} {
		e.expect(e.do("GET", p, "", nil), 401, "unauthorized")
	}
	e.expect(e.do("GET", "/api/snapshot", "", map[string]string{"Cookie": cookieName + "=inventado"}), 401, "unauthorized")
	e.expect(e.do("GET", "/nao-existe", "", nil), 404, "not_found")
	e.expect(e.do("GET", "/api/login", "", nil), 405, "method_not_allowed")
	e.expect(e.do("POST", "/api/snapshot", "", nil), 405, "method_not_allowed")
}

func TestLoginCookieAndErrors(t *testing.T) {
	e := newEnv(t)
	e.addUser("emergencia", "uma-senha-longa")
	res := e.do("POST", "/api/login", `{"username":"EMERGENCIA","password":"uma-senha-longa"}`, jsonHdr)
	if sc := res.Header.Get("Set-Cookie"); !strings.Contains(sc, "Secure") || !strings.Contains(sc, "HttpOnly") ||
		!strings.Contains(sc, "SameSite=Strict") || !strings.Contains(sc, "Path=/painel-ramais/") || !strings.Contains(sc, "Max-Age=604800") {
		t.Errorf("Set-Cookie: %s", sc)
	}
	b := e.expect(res, 200, "")
	var d struct {
		Subject   string
		Kind      string
		ExpiresAt string `json:"expires_at"`
	}
	json.Unmarshal(b.Data, &d)
	if d.Subject != "emergencia" || d.Kind != "password" || d.ExpiresAt != "2026-10-13T22:30:00.000Z" {
		t.Errorf("data: %s", b.Data)
	}
	// O banco guarda so o hash (32 bytes) do cookie.
	cookie := strings.SplitN(strings.SplitN(res.Header.Get("Set-Cookie"), "=", 2)[1], ";", 2)[0]
	if _, err := e.st.GetSession(context.Background(), auth.HashSessionID(cookie), e.now); err != nil {
		t.Errorf("sessao nao gravada pelo hash: %v", err)
	}

	e.expect(e.do("POST", "/api/login", `{"username":"emergencia","password":"errada-errada"}`, jsonHdr), 401, "invalid_credentials")
	e.expect(e.do("POST", "/api/login", `{"username":"ninguem","password":"errada-errada"}`, jsonHdr), 401, "invalid_credentials")
	e.expect(e.do("POST", "/api/login", `{"username":"emergencia","password":"x"}`, map[string]string{"Content-Type": "text/plain"}), 415, "unsupported_media_type")
	e.expect(e.do("POST", "/api/login", `{"username":"emergencia","password":"x","extra":1}`, jsonHdr), 400, "bad_request")
	e.expect(e.do("POST", "/api/login", `{"username":"","password":"x"}`, jsonHdr), 400, "bad_request")
	e.expect(e.do("POST", "/api/login", `{"username":"emergencia","password":"`+strings.Repeat("a", 5000)+`"}`, jsonHdr), 413, "payload_too_large")
	e.expect(e.do("POST", "/api/login", `nao-e-json`, jsonHdr), 400, "bad_request")
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	e.addUser("emergencia", "uma-senha-longa")
	bad := `{"username":"emergencia","password":"errada-errada"}`
	for i := 0; i < 5; i++ {
		e.expect(e.do("POST", "/api/login", bad, jsonHdr), 401, "invalid_credentials")
	}
	e.expect(e.do("POST", "/api/login", bad, jsonHdr), 429, "too_many_attempts")
	// Mesmo a senha certa fica bloqueada na janela.
	e.expect(e.do("POST", "/api/login", `{"username":"emergencia","password":"uma-senha-longa"}`, jsonHdr), 429, "too_many_attempts")
}

func TestLogoutIsIdempotentAndKillsSession(t *testing.T) {
	e := newEnv(t)
	cookie := e.login()
	e.expect(e.do("GET", "/api/groups", "", cookie), 200, "")
	res := e.do("POST", "/api/logout", "", cookie)
	if !strings.Contains(res.Header.Get("Set-Cookie"), "Max-Age=0") {
		t.Errorf("cookie deve expirar: %s", res.Header.Get("Set-Cookie"))
	}
	e.expect(res, 200, "")
	e.expect(e.do("GET", "/api/groups", "", cookie), 401, "unauthorized")
	e.expect(e.do("POST", "/api/logout", "", nil), 200, "") // sem sessao tambem e 200
}

func TestTokenExchangeIsSingleUse(t *testing.T) {
	e := newEnv(t)
	tok := `{"token":"` + tokV1Issabel + `"}`
	res := e.do("POST", "/api/token", tok, jsonHdr)
	var cookie string
	for _, c := range res.Cookies() {
		cookie = c.Name + "=" + c.Value
	}
	b := e.expect(res, 200, "")
	if !strings.Contains(string(b.Data), `"kind":"token"`) || !strings.Contains(string(b.Data), `"subject":"issabel:admin:fabio"`) || !strings.Contains(string(b.Data), `"admin":true`) {
		t.Errorf("data: %s", b.Data)
	}
	e.expect(e.do("GET", "/api/groups", "", map[string]string{"Cookie": cookie}), 200, "")
	e.expect(e.do("POST", "/api/token", tok, jsonHdr), 401, "invalid_token")
	e.expect(e.do("POST", "/api/token", `{"token":"lixo"}`, jsonHdr), 401, "invalid_token")
}

func TestSnapshotGzipAndPlain(t *testing.T) {
	e := newEnv(t)
	cookie := e.login()
	e.reset("100-1", 10, ext("2002", "Suporte", model.RegUnreachable), ext("2001", "Suporte", model.RegRegistered), ext("1500", model.NoGroup, model.RegUnregistered))

	h := map[string]string{"Cookie": cookie["Cookie"], "Accept-Encoding": "gzip"}
	res := e.do("GET", "/api/snapshot", "", h)
	if res.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("sem gzip: %v", res.Header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(readAll(t, res)))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Epoch        string            `json:"epoch"`
			Version      uint64            `json:"version"`
			ServerTime   string            `json:"server_time"`
			Technology   *string           `json:"technology"`
			AMIConnected bool              `json:"ami_connected"`
			Extensions   []json.RawMessage `json:"extensions"`
		} `json:"data"`
		Meta struct{ Count int } `json:"meta"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	d := env.Data
	if !env.Success || d.Epoch != "100-1" || d.Version != 10 || d.ServerTime != "2026-10-13T14:30:00.000Z" || *d.Technology != "pjsip" || !d.AMIConnected || env.Meta.Count != 3 {
		t.Errorf("snapshot: %s", raw)
	}
	if !strings.Contains(string(d.Extensions[0]), `"number":"1500"`) || !strings.Contains(string(d.Extensions[2]), `"number":"2002"`) {
		t.Errorf("ordenado por number: %s", raw)
	}
	if !strings.Contains(string(d.Extensions[0]), `"calls":[]`) || !strings.Contains(string(d.Extensions[0]), `"name":null`) {
		t.Errorf("calls [] e name null: %s", d.Extensions[0])
	}
	// Sem Accept-Encoding vem JSON puro.
	res = e.do("GET", "/api/snapshot", "", map[string]string{"Cookie": cookie["Cookie"]})
	if res.Header.Get("Content-Encoding") != "" {
		t.Error("nao deveria comprimir")
	}
	e.expect(res, 200, "")
	// Outras rotas /api tambem saem com gzip.
	res = e.do("GET", "/api/groups", "", h)
	if res.Header.Get("Content-Encoding") != "gzip" {
		t.Error("groups com gzip")
	}
	res.Body.Close()
}

func TestGroupsFromProjection(t *testing.T) {
	e := newEnv(t)
	cookie := e.login()
	e.reset("100-1", 1, ext("2001", "Suporte", model.RegRegistered), ext("1500", model.NoGroup, model.RegRegistered))
	b := e.expect(e.do("GET", "/api/groups", "", cookie), 200, "")
	want := `[{"name":"Suporte","ranges":[{"start":2000,"end":2999}],"extensions":["2001"]},{"name":"Sem grupo","ranges":[],"extensions":["1500"]}]`
	if string(b.Data) != want {
		t.Errorf("groups:\n%s\n%s", b.Data, want)
	}
}

func dialWS(t *testing.T, e *env, cookie map[string]string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	if hdr == nil {
		hdr = http.Header{}
	}
	hdr.Set("Cookie", cookie["Cookie"])
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.ts.URL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: hdr})
}

func hello(t *testing.T, c *websocket.Conn, epoch string, version uint64) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "hello", "epoch": epoch, "version": version})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

type frame struct {
	Type       string `json:"type"`
	Epoch      string `json:"epoch"`
	From       uint64 `json:"from_version"`
	To         uint64 `json:"to_version"`
	Reason     string `json:"reason"`
	AMI        bool   `json:"ami_connected"`
	Extensions []struct{ Number, Registration string }
	ServerTime string `json:"server_time"`
}

func readFrame(t *testing.T, c *websocket.Conn) frame {
	t.Helper()
	return readFrameWithin(t, c, 2*time.Second)
}

func readFrameWithin(t *testing.T, c *websocket.Conn, d time.Duration) frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("leitura: %v", err)
	}
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func expectClose(t *testing.T, c *websocket.Conn, code websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if got := websocket.CloseStatus(err); got != code {
		t.Fatalf("fechou com %d (%v), queria %d", got, err, code)
	}
}

func TestWSDeltaReplayAndReload(t *testing.T) {
	e := newEnv(t)
	cookie := e.login()
	e.reset("100-1", 10, ext("2001", "Suporte", model.RegRegistered))

	// Ao vivo: hello na versao atual, depois um lote.
	c, _, err := dialWS(t, e, cookie, nil)
	if err != nil {
		t.Fatal(err)
	}
	hello(t, c, "100-1", 10)
	e.batch("100-1", 10, 13, ext("2001", "Suporte", model.RegUnreachable))
	f := readFrame(t, c)
	if f.Type != "delta" || f.From != 10 || f.To != 13 || f.Epoch != "100-1" || !f.AMI || len(f.Extensions) != 1 ||
		f.Extensions[0].Number != "2001" || f.Extensions[0].Registration != "unreachable" || f.ServerTime == "" {
		t.Fatalf("delta: %+v", f)
	}

	// Replay: cliente parou em 10 e volta; recebe o lote que faltou, sem reload.
	e.batch("100-1", 13, 14, ext("2001", "Suporte", model.RegRegistered))
	c2, _, _ := dialWS(t, e, cookie, nil)
	hello(t, c2, "100-1", 10)
	if f := readFrame(t, c2); f.Type != "delta" || f.From != 10 || f.To != 13 {
		t.Fatalf("replay 1: %+v", f)
	}
	if f := readFrame(t, c2); f.Type != "delta" || f.From != 13 || f.To != 14 {
		t.Fatalf("replay 2: %+v", f)
	}

	// Epoch antigo no hello: reload + 4000.
	c3, _, _ := dialWS(t, e, cookie, nil)
	hello(t, c3, "99-9", 14)
	if f := readFrame(t, c3); f.Type != "reload" || f.Reason != "epoch_changed" || f.Epoch != "100-1" {
		t.Fatalf("reload epoch: %+v", f)
	}
	expectClose(t, c3, 4000)

	// Versao a frente do servidor.
	c4, _, _ := dialWS(t, e, cookie, nil)
	hello(t, c4, "100-1", 999)
	if f := readFrame(t, c4); f.Reason != "version_ahead" {
		t.Fatalf("ahead: %+v", f)
	}
	expectClose(t, c4, 4000)

	// Epoch novo no meio da conexao: todos os clientes ligados recebem reload.
	e.reset("100-2", 15, ext("2001", "Suporte", model.RegUnknown))
	if f := readFrame(t, c); f.Type != "delta" || f.To != 14 { // c estava ao vivo quando o lote 13->14 saiu
		t.Fatalf("delta ao vivo: %+v", f)
	}
	for _, cc := range []*websocket.Conn{c, c2} {
		if f := readFrame(t, cc); f.Type != "reload" || f.Reason != "epoch_changed" || f.Epoch != "100-2" {
			t.Fatalf("reload ao vivo: %+v", f)
		}
		expectClose(t, cc, 4000)
	}
}

func TestWSReloadOnGap(t *testing.T) {
	e := newEnv(t)
	cookie := e.login()
	e.reset("100-1", 0)
	for i := uint64(0); i < 50; i++ { // ring guarda 40
		e.batch("100-1", i, i+1, ext("2001", "Suporte", model.RegRegistered))
	}
	c, _, _ := dialWS(t, e, cookie, nil)
	hello(t, c, "100-1", 5) // mais velho que o ring (10..50)
	if f := readFrame(t, c); f.Type != "reload" || f.Reason != "version_gap" {
		t.Fatalf("gap: %+v", f)
	}
	expectClose(t, c, 4000)
	// No limite do ring ainda ha replay.
	c2, _, _ := dialWS(t, e, cookie, nil)
	hello(t, c2, "100-1", 10)
	n := 0
	for ; n < 40; n++ {
		if f := readFrame(t, c2); f.Type != "delta" {
			t.Fatalf("esperava delta: %+v", f)
		}
	}
}

func TestWSProtocolErrorsAndOrigin(t *testing.T) {
	e := newEnv(t)
	cookie := e.login()
	e.reset("100-1", 1)

	c, _, _ := dialWS(t, e, cookie, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Write(ctx, websocket.MessageText, []byte(`{"type":"oi"}`))
	expectClose(t, c, 4003)

	// Mensagem extra depois do hello.
	c2, _, _ := dialWS(t, e, cookie, nil)
	hello(t, c2, "100-1", 1)
	c2.Write(ctx, websocket.MessageText, []byte(`mais`))
	expectClose(t, c2, 4003)

	// Origin de outro host: o upgrade e recusado (403).
	_, res, err := dialWS(t, e, cookie, http.Header{"Origin": []string{"http://evil.example"}})
	if err == nil || res == nil || res.StatusCode != 403 {
		t.Fatalf("Origin deveria dar 403: %v %v", res, err)
	}
	// Origin igual ao Host passa.
	c3, _, err := dialWS(t, e, cookie, http.Header{"Origin": []string{e.ts.URL}})
	if err != nil {
		t.Fatalf("Origin do proprio host: %v", err)
	}
	c3.CloseNow()
}

func TestWSClosedWhenSessionRemoved(t *testing.T) {
	e := newEnv(t)
	cookie := e.login()
	e.reset("100-1", 1)
	c, _, _ := dialWS(t, e, cookie, nil)
	hello(t, c, "100-1", 1)
	deadline := time.Now().Add(time.Second)
	for e.hub.Pub().Clients != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := e.st.DeleteUser(context.Background(), "emergencia"); err != nil {
		t.Fatal(err)
	}
	if n := e.srv.Revalidate(context.Background()); n != 1 {
		t.Fatalf("revalidate fechou %d", n)
	}
	if f := readFrame(t, c); f.Type != "session_expired" || f.Reason != "revoked" { // aviso explicito antes de fechar
		t.Fatalf("aviso: %+v", f)
	}
	expectClose(t, c, 4001)
}

func TestHubDropsSlowClientWithoutBlocking(t *testing.T) {
	e := newEnv(t)
	e.reset("100-1", 0)
	slow := newClient([32]byte{1}, e.now.Add(time.Hour))
	fast := newClient([32]byte{2}, e.now.Add(time.Hour))
	for _, c := range []*client{slow, fast} {
		if reason, _ := e.hub.Join(c, "100-1", 0); reason != "" {
			t.Fatal(reason)
		}
	}
	// "slow" nunca le: depois de 64 lotes a fila enche e ele e desconectado com 4002.
	start := time.Now()
	for i := uint64(0); i < 70; i++ {
		e.batch("100-1", i, i+1, ext("2001", "Suporte", model.RegRegistered))
		for len(fast.out) > 0 {
			<-fast.out
		}
	}
	if time.Since(start) > time.Second {
		t.Error("broadcast demorou: bloqueou?")
	}
	select {
	case <-slow.closed:
		if slow.code != closeSlow {
			t.Errorf("codigo %d", slow.code)
		}
	default:
		t.Fatal("cliente lento deveria ter sido desconectado")
	}
	select {
	case <-fast.closed:
		t.Fatal("cliente rapido nao pode ser afetado")
	default:
	}
	if e.hub.Pub().Clients != 1 {
		t.Errorf("clientes = %d", e.hub.Pub().Clients)
	}
}

func TestHealthAndDebugAreLocalOnly(t *testing.T) {
	e := newEnv(t)
	cookie := e.login()
	e.reset("100-1", 7, ext("2001", "Suporte", model.RegRegistered))
	e.stats = state.Stats{Technology: model.TechPJSIP, Total: model.Counts{Registered: 1}, Trunks: 2, ActiveCalls: 3, EventsPerSec: 12.34, LagMs: 4}
	e.ami = model.AMIStatus{Connected: true, Technology: model.TechPJSIP, AsteriskVersion: "18.20.0", QueueCap: 65536}

	for _, p := range []string{"/api/health", "/debug"} {
		for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host"} {
			e.expect(e.do("GET", p, "", map[string]string{h: "192.0.2.9"}), localOnlyStatus, "not_found")
		}
	}
	b := e.expect(e.do("GET", "/api/health", "", nil), 200, "")
	var d struct {
		Status     string
		AMI        struct{ Connected bool }
		Extensions struct{ Total, Registered int }
		Trunks     int
		Events     float64 `json:"events_per_s"`
		Epoch      string
		Version    uint64
		Deps       []struct{ Name, Status string }
	}
	json.Unmarshal(b.Data, &d)
	if d.Status != "ok" || !d.AMI.Connected || d.Extensions.Total != 1 || d.Trunks != 2 || d.Events != 12.3 || d.Epoch != "100-1" || d.Version != 7 || len(d.Deps) != 1 {
		t.Errorf("health: %s", b.Data)
	}
	// AMI fora: 503 com o mesmo corpo.
	e.ami.Connected = false
	res := e.do("GET", "/api/health", "", nil)
	if res.StatusCode != 503 {
		t.Errorf("degraded deveria ser 503, veio %d", res.StatusCode)
	}
	if b := parseBody(t, res); !strings.Contains(string(b.Data), `"status":"degraded"`) {
		t.Errorf("corpo: %s", b.Data)
	}
	e.expect(e.do("GET", "/debug", "", nil), 401, "unauthorized") // /debug exige sessao (auditoria L1)
	res = e.do("GET", "/debug", "", cookie)
	html := string(readAll(t, res))
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "text/html") ||
		!strings.Contains(html, `http-equiv="refresh"`) || !strings.Contains(html, "100-1") || strings.Contains(html, "<script") {
		t.Errorf("debug: %d %s", res.StatusCode, html)
	}
}

func TestHistoryDenseSeriesWithGap(t *testing.T) {
	e := newEnv(t)
	cookie := e.login()
	m := time.Date(2026, 10, 13, 13, 0, 0, 0, time.UTC)
	e.st.InsertHistory(context.Background(), []store.HistoryRow{
		{Minute: m, Group: "Suporte", Registered: 180, Unregistered: 15, Unreachable: 2, InCall: 40},
		{Minute: m.Add(time.Minute), Group: "Suporte", Gap: true},
		{Minute: m.Add(3 * time.Minute), Group: "Suporte", Registered: 179},
	})
	b := e.expect(e.do("GET", "/api/history?group=Suporte&from=2026-10-13T13:00:00Z&to=2026-10-13T13:04:00Z", "", cookie), 200, "")
	var d struct {
		Group  string
		StepS  int `json:"step_s"`
		Points []struct {
			T          string
			Registered *int
			InCall     *int `json:"in_call"`
			Gap        bool
		}
	}
	json.Unmarshal(b.Data, &d)
	if d.Group != "Suporte" || d.StepS != 60 || len(d.Points) != 4 {
		t.Fatalf("history: %s", b.Data)
	}
	p := d.Points
	if p[0].Gap || *p[0].Registered != 180 || *p[0].InCall != 40 || !p[1].Gap || p[1].Registered != nil || !p[2].Gap || p[3].Gap || p[0].T != "2026-10-13T13:00:00.000Z" {
		t.Errorf("pontos: %s", b.Data)
	}
	if !strings.Contains(string(b.Meta), `"retention_days":30`) || !strings.Contains(string(b.Meta), `"count":4`) {
		t.Errorf("meta: %s", b.Meta)
	}
	// Grupo inexistente: 200 com tudo gap. Sem group: total (group null).
	b = e.expect(e.do("GET", "/api/history?group=Nada&from=2026-10-13T13:00:00Z&to=2026-10-13T13:02:00Z", "", cookie), 200, "")
	if strings.Contains(string(b.Data), `"gap":false`) {
		t.Errorf("sem dados deve ser gap: %s", b.Data)
	}
	b = e.expect(e.do("GET", "/api/history", "", cookie), 200, "")
	if !strings.Contains(string(b.Data), `"group":null`) || !strings.Contains(string(b.Meta), `"count":60`) {
		t.Errorf("padrao 60 min: %s %s", b.Data, b.Meta)
	}
	for _, q := range []string{"from=ontem", "to=amanha", "from=2026-10-13T13:00:00Z&to=2026-10-13T13:00:00Z",
		"from=2026-09-01T00:00:00Z&to=2026-10-13T00:00:00Z", "group=" + strings.Repeat("a", 65)} {
		e.expect(e.do("GET", "/api/history?"+q, "", cookie), 400, "bad_request")
	}
}

// Integracao com o estado real: varias mudancas do mesmo ramal na janela de
// 250 ms (aqui 30 ms) viram uma so no WebSocket, e o estado final e o correto.
func TestWSDeltaIsCoalescedFromRealState(t *testing.T) {
	e := newEnv(t)
	cookie := e.login()
	ev := make(chan model.Event, 1024)
	s := state.New(regexpExt(), ev, state.Options{Interval: 30 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	go func() {
		for o := range s.Out() { // alimenta o hub do ambiente
			e.in <- o
		}
	}()
	at := time.Now()
	ev <- model.SyncStarted{At: at, Technology: model.TechPJSIP, Reason: model.SyncConnect}
	ev <- model.EndpointSeen{At: at, Name: "2001"}
	ev <- model.EndpointSeen{At: at, Name: "2002"}
	ev <- model.SyncFinished{At: at}
	snap := waitSnapshot(t, e, cookie, "pjsip")

	c, _, _ := dialWS(t, e, cookie, nil)
	hello(t, c, snap.epoch, snap.version)
	for _, st := range []model.Registration{model.RegRegistered, model.RegUnreachable, model.RegUnregistered, model.RegRegistered} {
		ev <- model.RegistrationChanged{At: at, Name: "2001", State: st, IP: "192.0.2.1"}
	}
	ev <- model.RegistrationChanged{At: at, Name: "2002", State: model.RegRegistered}
	f := readFrame(t, c)
	if f.Type != "delta" || len(f.Extensions) != 2 || f.Extensions[0].Number != "2001" || f.Extensions[0].Registration != "registered" {
		t.Fatalf("delta coalescido: %+v", f)
	}
	if f.From != snap.version {
		t.Errorf("from_version %d, snapshot %d", f.From, snap.version)
	}
}

type snapInfo struct {
	epoch   string
	version uint64
}

func waitSnapshot(t *testing.T, e *env, cookie map[string]string, tech string) snapInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res := e.do("GET", "/api/snapshot", "", cookie)
		var b struct {
			Data struct {
				Epoch      string
				Version    uint64
				Technology string
				Extensions []json.RawMessage
			}
		}
		json.Unmarshal(readAll(t, res), &b)
		if b.Data.Technology == tech && len(b.Data.Extensions) == 2 {
			return snapInfo{b.Data.Epoch, b.Data.Version}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("snapshot nao chegou")
	return snapInfo{}
}

func regexpExt() *regexp.Regexp { return regexp.MustCompile(`^[0-9]+$`) }

// nameLimiterForTest espelha o limite por nome do daemon (20 em 5 min); o limite por IP e o padrao (5).
func nameLimiterForTest() *auth.Limiter {
	l := auth.NewLimiter()
	l.Max = 20
	return l
}
