package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// Vetores V1 e V2 do plano (C1), assinados com a chave do Issabel de exemplo.
const (
	tokV1Issabel = "eyJzdWIiOiJpc3NhYmVsOmFkbWluOmZhYmlvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.BvaFo9ur4_kzEeYjvM0EiRPhpy_6qmlZsU7nio54VU4"
	tokV1Secret  = "eyJzdWIiOiJpc3NhYmVsOmFkbWluOmZhYmlvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.PbqEoW5r-kgT30bYkuhechQCldj_pGcp1iUCVi9haRQ"
	tokV2Issabel = "eyJzdWIiOiJpc3NhYmVsOnVzZXI6bWFyaWEiLCJleHAiOjE3OTE5MDI0NjAsImp0aSI6InI4V2cyYjBhVTlkN2wxS3E0c1h2WUIifQ.Q4LwgNiSLChzFaL2PT8hSU-vtRnAGdAUcacuuCe6MYc"
)

var testStatic = fstest.MapFS{
	"index.html":        {Data: []byte("<!doctype html><title>tela</title>")},
	"acesso.html":       {Data: []byte("<!doctype html><title>aviso</title>")},
	"assets/x.js":       {Data: []byte("export const x = 1;")},
	"assets/x.css":      {Data: []byte("body{margin:0}")},
	"assets/x.svg":      {Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
	"assets/x.txt":      {Data: []byte("segredo")},
	"assets/sem-ext":    {Data: []byte("x")},
	"assets/sub/y.js":   {Data: []byte("y")},
	"assets/.oculto.js": {Data: []byte("z")},
}

// CSP exatas do plano (C3); escritas por extenso de proposito: mudar a politica e decisao de seguranca.
const (
	wantCSPTela = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'"
	wantCSPEmer = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	wantCSPAvis = "default-src 'none'; style-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
)

// tokenCookie troca um token por sessao e devolve o cabecalho Cookie.
func (e *env) tokenCookie(tok string) map[string]string {
	e.t.Helper()
	res := e.do("POST", "/api/token", `{"token":"`+tok+`"}`, jsonHdr)
	for _, c := range res.Cookies() {
		if c.Name == cookieName {
			e.expect(res, 200, "")
			return map[string]string{"Cookie": cookieName + "=" + c.Value}
		}
	}
	e.t.Fatalf("token recusado: %d", res.StatusCode)
	return nil
}

type sessData struct {
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
	Admin   bool   `json:"admin"`
}

func (e *env) sessionOf(cookie map[string]string) sessData {
	e.t.Helper()
	b := e.expect(e.do("GET", "/api/session", "", cookie), 200, "")
	var d sessData
	if err := json.Unmarshal(b.Data, &d); err != nil {
		e.t.Fatal(err)
	}
	return d
}

// ---- B3: sessao, papel e login so da emergencia ----

func TestSessionEndpointAndRole(t *testing.T) {
	e := newEnv(t)
	e.expect(e.do("GET", "/api/session", "", nil), 401, "unauthorized")
	e.expect(e.do("GET", "/api/session", "", map[string]string{"Cookie": cookieName + "=inventado"}), 401, "unauthorized")
	e.expect(e.do("POST", "/api/session", "", nil), 405, "method_not_allowed")

	if d := e.sessionOf(e.login()); d != (sessData{"emergencia", "password", false}) {
		t.Errorf("emergencia: %+v", d)
	}
	if d := e.sessionOf(e.tokenCookie(tokV1Issabel)); d != (sessData{"issabel:admin:fabio", "token", true}) {
		t.Errorf("V1: %+v", d)
	}
	if d := e.sessionOf(e.tokenCookie(tokV2Issabel)); d != (sessData{"issabel:user:maria", "token", false}) {
		t.Errorf("V2: %+v", d)
	}
}

// O corpo do login e do token tambem traz admin (a tela nao precisa de uma chamada a mais).
func TestLoginAndTokenResponsesCarryAdmin(t *testing.T) {
	e := newEnv(t)
	e.addUser("emergencia", "uma-senha-longa")
	b := e.expect(e.do("POST", "/api/login", `{"username":"emergencia","password":"uma-senha-longa"}`, jsonHdr), 200, "")
	if !strings.Contains(string(b.Data), `"admin":false`) {
		t.Errorf("login: %s", b.Data)
	}
	b = e.expect(e.do("POST", "/api/token", `{"token":"`+tokV1Issabel+`"}`, jsonHdr), 200, "")
	if !strings.Contains(string(b.Data), `"admin":true`) {
		t.Errorf("token: %s", b.Data)
	}
}

// Usuario de senha diferente de emergencia nao entra, mesmo com a senha certa (usuarios da fase 1).
func TestLoginOnlyEmergencyUser(t *testing.T) {
	e := newEnv(t)
	e.addUser("fabio", "uma-senha-longa")
	e.addUser("emergencia", "outra-senha-longa")
	e.expect(e.do("POST", "/api/login", `{"username":"fabio","password":"uma-senha-longa"}`, jsonHdr), 401, "invalid_credentials")
	e.expect(e.do("POST", "/api/login", `{"username":"emergencia","password":"uma-senha-longa"}`, jsonHdr), 401, "invalid_credentials")
	e.expect(e.do("POST", "/api/login", `{"username":"EMERGENCIA","password":"outra-senha-longa"}`, jsonHdr), 200, "")
}

// O usuario recusado conta no limitador como qualquer outro (5 erros travam o nome).
func TestLoginOtherUserStillCountsInLimiter(t *testing.T) {
	e := newEnv(t)
	e.addUser("fabio", "uma-senha-longa")
	ok := `{"username":"fabio","password":"uma-senha-longa"}`
	for i := 0; i < 5; i++ {
		e.expect(e.do("POST", "/api/login", ok, jsonHdr), 401, "invalid_credentials")
	}
	e.expect(e.do("POST", "/api/login", ok, jsonHdr), 429, "too_many_attempts")
}

// V1 assinado com o segredo do daemon (prefixo reservado) e rejeitado pela rota.
func TestTokenRouteRejectsReservedPrefixWithSecret(t *testing.T) {
	e := newEnv(t)
	e.expect(e.do("POST", "/api/token", `{"token":"`+tokV1Secret+`"}`, jsonHdr), 401, "invalid_token")
}

// ---- B4: tela, aviso, emergencia e assets ----

func TestRootDecidesBySecFetchDest(t *testing.T) {
	e := newEnv(t)
	for _, dest := range []string{"document", "empty", "script", "frame", "IFRAME"} {
		res := e.do("GET", "/", "", map[string]string{"Sec-Fetch-Dest": dest})
		if res.StatusCode != 303 || res.Header.Get("Location") != "/painel-ramais/acesso#" || res.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d Location=%q Cache-Control=%q", dest, res.StatusCode, res.Header.Get("Location"), res.Header.Get("Cache-Control"))
		}
		if b := readAll(t, res); len(b) != 0 {
			t.Errorf("%s: corpo do 303 deve ser vazio: %q", dest, b)
		}
	}
	for name, hdr := range map[string]map[string]string{"iframe": {"Sec-Fetch-Dest": "iframe"}, "sem cabecalho": nil} {
		res := e.do("GET", "/", "", hdr)
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d", name, res.StatusCode)
		}
		want := map[string]string{
			"Content-Type":            "text/html; charset=utf-8",
			"Cache-Control":           "no-store",
			"Referrer-Policy":         "no-referrer",
			"Vary":                    "Sec-Fetch-Dest",
			"X-Frame-Options":         "SAMEORIGIN",
			"Content-Security-Policy": wantCSPTela,
			"X-Content-Type-Options":  "nosniff",
		}
		for k, v := range want {
			if got := res.Header.Get(k); got != v {
				t.Errorf("%s: %s = %q, queria %q", name, k, got, v)
			}
		}
		if res.Header.Get("X-Request-Id") == "" {
			t.Errorf("%s: sem X-Request-Id", name)
		}
		if b := string(readAll(t, res)); b != "<!doctype html><title>tela</title>" {
			t.Errorf("%s: corpo %q", name, b)
		}
	}
}

// curl -I (HEAD) tem de ver o mesmo 303 e o mesmo 200.
func TestRootHead(t *testing.T) {
	e := newEnv(t)
	res := e.do("HEAD", "/", "", map[string]string{"Sec-Fetch-Dest": "document"})
	if res.StatusCode != 303 || res.Header.Get("Location") != "/painel-ramais/acesso#" {
		t.Errorf("HEAD document: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	res = e.do("HEAD", "/", "", map[string]string{"Sec-Fetch-Dest": "iframe"})
	if res.StatusCode != 200 || res.Header.Get("Content-Security-Policy") != wantCSPTela {
		t.Errorf("HEAD iframe: %d", res.StatusCode)
	}
	e.expect(e.do("POST", "/", "", nil), 405, "method_not_allowed")
	e.expect(e.do("POST", "/nao-existe", "", nil), 404, "not_found")
}

func TestEmergenciaRoute(t *testing.T) {
	e := newEnv(t)
	for _, dest := range []string{"", "document", "iframe", "empty"} { // nao olha o Sec-Fetch-Dest
		hdr := map[string]string{}
		if dest != "" {
			hdr["Sec-Fetch-Dest"] = dest
		}
		res := e.do("GET", "/emergencia/", "", hdr)
		if res.StatusCode != 200 {
			t.Fatalf("dest %q: %d", dest, res.StatusCode)
		}
		if got := res.Header.Get("Content-Security-Policy"); got != wantCSPEmer {
			t.Errorf("CSP = %q", got)
		}
		if res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("Vary") != "" ||
			res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("cabecalhos: %v", res.Header)
		}
		if b := string(readAll(t, res)); b != "<!doctype html><title>tela</title>" {
			t.Errorf("deve ser a mesma index.html: %q", b)
		}
	}
	res := e.do("GET", "/emergencia", "", nil)
	if res.StatusCode != 301 || res.Header.Get("Location") != "/painel-ramais/emergencia/" {
		t.Errorf("/emergencia: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	e.expect(e.do("GET", "/emergencia/x", "", nil), 404, "not_found")
	e.expect(e.do("POST", "/emergencia/", "", nil), 405, "method_not_allowed")
}

func TestAcessoNotice(t *testing.T) {
	e := newEnv(t)
	res := e.do("GET", "/acesso", "", map[string]string{"Sec-Fetch-Dest": "document"})
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	want := map[string]string{
		"Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store", "Referrer-Policy": "no-referrer",
		"X-Frame-Options": "SAMEORIGIN", "Content-Security-Policy": wantCSPAvis,
	}
	for k, v := range want {
		if got := res.Header.Get(k); got != v {
			t.Errorf("%s = %q, queria %q", k, got, v)
		}
	}
	if b := string(readAll(t, res)); b != "<!doctype html><title>aviso</title>" {
		t.Errorf("corpo %q", b)
	}
	e.expect(e.do("GET", "/acesso/x", "", nil), 404, "not_found")
}

func TestAssets(t *testing.T) {
	e := newEnv(t)
	for path, ctype := range map[string]string{
		"/assets/x.js":  "text/javascript; charset=utf-8",
		"/assets/x.css": "text/css; charset=utf-8",
		"/assets/x.svg": "image/svg+xml",
	} {
		res := e.do("GET", path, "", nil)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != ctype || res.Header.Get("Cache-Control") != "no-cache" ||
			res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: %d %v", path, res.StatusCode, res.Header)
		}
		etag := res.Header.Get("ETag")
		res.Body.Close()
		if etag == "" {
			t.Fatalf("%s: sem ETag", path)
		}
		again := e.do("GET", path, "", map[string]string{"If-None-Match": etag})
		if again.StatusCode != 304 {
			t.Errorf("%s: revalidacao deu %d", path, again.StatusCode)
		}
		again.Body.Close()
	}
	for _, path := range []string{
		"/assets/", "/assets", "/assets/../index.html", "/assets/%2e%2e/index.html", "/assets/..%2findex.html",
		"/assets/x.txt", "/assets/sem-ext", "/assets/sub/y.js", "/assets/nao.js", "/assets/.oculto.js", "/assets/x.js/",
		"/assets/..\\index.html", "/assets/x.JS",
	} {
		e.expect(e.do("GET", path, "", nil), 404, "not_found")
	}
}

// Sem Static (testes e builds sem tela) as rotas da tela respondem 404 no envelope.
func TestNoStaticMeansNotFound(t *testing.T) {
	e := newEnv(t)
	e.srv.Static = nil
	for _, p := range []string{"/", "/acesso", "/emergencia/", "/assets/x.js"} {
		e.expect(e.do("GET", p, "", nil), 404, "not_found")
	}
}

// ---- B6: grupos pela tela ----

const csvOK = "grupo,ramal\nSuporte,2000-2099\nVendas,3000-3999\n"

func groupsBody(csv string, replace bool) string {
	b, _ := json.Marshal(map[string]any{"csv": csv, "replace": replace})
	return string(b)
}

func (e *env) groupNames() []string {
	e.t.Helper()
	gs, err := e.st.ListGroups(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	var names []string
	for _, g := range gs {
		names = append(names, g.Name)
	}
	return names
}

func TestGroupsRoutesAuthAndRole(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/groups/import", "/api/groups/delete"} {
		e.expect(e.do("POST", p, groupsBody(csvOK, false), jsonHdr), 401, "unauthorized")
		e.expect(e.do("GET", p, "", nil), 405, "method_not_allowed")
	}
	// operador do Issabel (V2) e emergencia: 403, mesmo com corpo invalido (o papel vem antes do corpo).
	for name, cookie := range map[string]map[string]string{"V2 operador": e.tokenCookie(tokV2Issabel), "emergencia": e.login()} {
		h := map[string]string{"Content-Type": "application/json", "Cookie": cookie["Cookie"]}
		for _, p := range []string{"/api/groups/import", "/api/groups/delete"} {
			b := e.expect(e.do("POST", p, groupsBody(csvOK, true), h), 403, "forbidden")
			if b.Error.Message != "Apenas administradores do Issabel podem alterar grupos" {
				t.Errorf("%s: mensagem %q", name, b.Error.Message)
			}
			e.expect(e.do("POST", p, "nao-e-json", h), 403, "forbidden")
		}
	}
	if len(e.groupNames()) != 0 || e.reloads != 0 {
		t.Errorf("403 nao pode gravar nem recarregar: %v reloads=%d", e.groupNames(), e.reloads)
	}
}

func TestGroupsImportAsAdmin(t *testing.T) {
	e := newEnv(t)
	h := e.adminHdr()
	b := e.expect(e.do("POST", "/api/groups/import", groupsBody(csvOK, true), h), 200, "")
	if string(b.Data) != `{"replace":true,"rows":2}` || string(b.Meta) != "null" {
		t.Errorf("data=%s meta=%s", b.Data, b.Meta)
	}
	if got := e.groupNames(); len(got) != 2 || e.reloads != 1 {
		t.Errorf("grupos %v, reloads %d (queria 2 grupos e 1 recarga)", got, e.reloads)
	}
	// sem substituir: acrescenta; replace padrao e false.
	b = e.expect(e.do("POST", "/api/groups/import", groupsBody("Novo,5000\n", false), h), 200, "")
	if string(b.Data) != `{"replace":false,"rows":1}` || len(e.groupNames()) != 3 || e.reloads != 2 {
		t.Errorf("acrescentar: %s %v %d", b.Data, e.groupNames(), e.reloads)
	}
	e.expect(e.do("POST", "/api/groups/import", `{"csv":"Outro,6000\n"}`, h), 200, "")
	if len(e.groupNames()) != 4 {
		t.Errorf("replace omitido deve acrescentar: %v", e.groupNames())
	}
	// replace troca tudo.
	e.expect(e.do("POST", "/api/groups/import", groupsBody("Unico,7000\n", true), h), 200, "")
	if got := e.groupNames(); len(got) != 1 || got[0] != "Unico" {
		t.Errorf("replace: %v", got)
	}
}

func TestGroupsImportInvalid(t *testing.T) {
	e := newEnv(t)
	h := e.adminHdr()
	e.expect(e.do("POST", "/api/groups/import", groupsBody(csvOK, true), h), 200, "")
	before, reloads := e.groupNames(), e.reloads

	// Sobreposicao dentro do arquivo: tudo ou nada, mensagem do parser.
	b := e.expect(e.do("POST", "/api/groups/import", groupsBody("A,1-10\nB,5-20\n", true), h), 400, "invalid_csv")
	if !strings.Contains(b.Error.Message, "linha") {
		t.Errorf("mensagem deve trazer as linhas: %q", b.Error.Message)
	}
	// Sobreposicao com o que ja existe (sem replace).
	e.expect(e.do("POST", "/api/groups/import", groupsBody("Conflito,2050\n", false), h), 400, "invalid_csv")
	e.expect(e.do("POST", "/api/groups/import", groupsBody("", true), h), 400, "invalid_csv")
	e.expect(e.do("POST", "/api/groups/import", groupsBody("sem-virgula\n", true), h), 400, "invalid_csv")

	if got := e.groupNames(); strings.Join(got, ",") != strings.Join(before, ",") || e.reloads != reloads {
		t.Errorf("CSV invalido nao grava nem recarrega: %v reloads=%d", got, e.reloads)
	}
	// Corpo: campo obrigatorio, campo desconhecido, tipo de conteudo.
	e.expect(e.do("POST", "/api/groups/import", `{"replace":true}`, h), 400, "bad_request")
	e.expect(e.do("POST", "/api/groups/import", `{"csv":"A,1\n","x":1}`, h), 400, "bad_request")
	e.expect(e.do("POST", "/api/groups/import", `{"csv":1}`, h), 400, "bad_request")
	text := map[string]string{"Content-Type": "text/plain", "Cookie": h["Cookie"]}
	e.expect(e.do("POST", "/api/groups/import", csvOK, text), 415, "unsupported_media_type")
}

// Corpo ate 1 MiB nas duas rotas (o resto da API segue com 4 KiB); 1 MiB + 1 da 413.
func TestGroupsBodyLimit(t *testing.T) {
	e := newEnv(t)
	h := e.adminHdr()
	var big strings.Builder
	for i := 0; i < 600; i++ { // ~6 KiB: acima dos 4 KiB do resto da API
		fmt.Fprintf(&big, "G%d,%d\n", i, 10000+i)
	}
	if big.Len() < 4096 {
		t.Fatal("CSV de teste deveria passar de 4 KiB")
	}
	e.expect(e.do("POST", "/api/groups/import", groupsBody(big.String(), true), h), 200, "")
	if len(e.groupNames()) != 600 {
		t.Errorf("grupos gravados: %d", len(e.groupNames()))
	}

	pad := strings.Repeat("a", 1<<20) // so o campo ja passa do teto com o JSON em volta
	e.expect(e.do("POST", "/api/groups/import", `{"csv":"`+pad+`"}`, h), 413, "payload_too_large")
	e.expect(e.do("POST", "/api/groups/delete", `{"name":"`+pad+`"}`, h), 413, "payload_too_large")
	// Exatamente no teto: o corpo inteiro (JSON incluido) tem 1<<20 bytes e passa do limite de leitura? Nao: e aceito.
	head, tail := `{"name":"`, `"}`
	atLimit := head + strings.Repeat("b", (1<<20)-len(head)-len(tail)) + tail
	e.expect(e.do("POST", "/api/groups/delete", atLimit, h), 400, "bad_request") // le inteiro e recusa o nome de 64+
	// Outras rotas seguem em 4 KiB.
	e.expect(e.do("POST", "/api/login", `{"username":"x","password":"`+strings.Repeat("a", 5000)+`"}`, jsonHdr), 413, "payload_too_large")
}

func TestGroupsDelete(t *testing.T) {
	e := newEnv(t)
	h := e.adminHdr()
	e.expect(e.do("POST", "/api/groups/import", groupsBody(csvOK, true), h), 200, "")
	reloads := e.reloads
	b := e.expect(e.do("POST", "/api/groups/delete", `{"name":"Suporte"}`, h), 200, "")
	if string(b.Data) != `{"name":"Suporte"}` || string(b.Meta) != "null" {
		t.Errorf("data=%s meta=%s", b.Data, b.Meta)
	}
	if got := e.groupNames(); len(got) != 1 || got[0] != "Vendas" || e.reloads != reloads+1 {
		t.Errorf("depois do delete: %v reloads=%d", got, e.reloads)
	}
	e.expect(e.do("POST", "/api/groups/delete", `{"name":"Suporte"}`, h), 404, "not_found")
	e.expect(e.do("POST", "/api/groups/delete", `{"name":""}`, h), 400, "bad_request")
	e.expect(e.do("POST", "/api/groups/delete", `{"name":"`+strings.Repeat("a", 65)+`"}`, h), 400, "bad_request")
	e.expect(e.do("POST", "/api/groups/delete", `{}`, h), 400, "bad_request")
	e.expect(e.do("POST", "/api/groups/delete", `{"name":"x","y":1}`, h), 400, "bad_request")
	if e.reloads != reloads+1 {
		t.Errorf("falhas nao recarregam: %d", e.reloads)
	}
}

// Falha na recarga: os grupos ja estao no banco, a resposta e 500 e nao finge sucesso.
func TestGroupsReloadFailure(t *testing.T) {
	e := newEnv(t)
	h := e.adminHdr()
	e.reloadErr = context.DeadlineExceeded
	e.expect(e.do("POST", "/api/groups/import", groupsBody(csvOK, true), h), 500, "internal")
	if len(e.groupNames()) != 2 {
		t.Errorf("gravado antes da recarga: %v", e.groupNames())
	}
}

// adminHdr = sessao de administrador do Issabel (V1) com Content-Type JSON.
func (e *env) adminHdr() map[string]string {
	e.t.Helper()
	c := e.tokenCookie(tokV1Issabel)
	return map[string]string{"Content-Type": "application/json", "Cookie": c["Cookie"]}
}

// ---- ajustes pos-revisao ----

func (e *env) loginFrom(xff, user, pass string) *http.Response {
	hdr := map[string]string{"Content-Type": "application/json"}
	if xff != "" {
		hdr["X-Forwarded-For"] = xff
	}
	return e.do("POST", "/api/login", `{"username":"`+user+`","password":"`+pass+`"}`, hdr)
}

// 5 falhas do IP A nao bloqueiam a senha certa do IP B; o IP A fica bloqueado.
func TestLoginLimitIsPerClientIP(t *testing.T) {
	e := newEnv(t)
	e.addUser("emergencia", "uma-senha-longa")
	for i := 0; i < 5; i++ {
		e.expect(e.loginFrom("203.0.113.1", "emergencia", "errada-errada"), 401, "invalid_credentials")
	}
	e.expect(e.loginFrom("203.0.113.1", "emergencia", "errada-errada"), 429, "too_many_attempts")
	e.expect(e.loginFrom("203.0.113.1", "emergencia", "uma-senha-longa"), 429, "too_many_attempts") // IP A segue travado
	e.expect(e.loginFrom("203.0.113.2", "emergencia", "uma-senha-longa"), 200, "")                  // IP B entra
}

// Vale o ULTIMO valor do X-Forwarded-For (o do Apache); valores a esquerda sao do cliente e nao contam.
func TestLoginIPIsLastForwardedValue(t *testing.T) {
	e := newEnv(t)
	e.addUser("emergencia", "uma-senha-longa")
	for i := 0; i < 5; i++ { // o atacante varia o valor falso a esquerda
		e.expect(e.loginFrom(fmt.Sprintf("10.9.9.%d, 203.0.113.1", i), "emergencia", "errada-errada"), 401, "invalid_credentials")
	}
	e.expect(e.loginFrom("10.1.1.1, 203.0.113.1", "emergencia", "errada-errada"), 429, "too_many_attempts")
	// XFF invalido cai no RemoteAddr (127.0.0.1), que tem a propria contagem.
	e.expect(e.loginFrom("lixo", "emergencia", "uma-senha-longa"), 200, "")
}

// Tentativa distribuida em varios IPs ainda esbarra no limite por nome (20 em 5 min).
func TestLoginLimitPerNameAcrossIPs(t *testing.T) {
	e := newEnv(t)
	e.addUser("emergencia", "uma-senha-longa")
	for i := 0; i < 20; i++ {
		e.expect(e.loginFrom(fmt.Sprintf("198.51.100.%d", i+1), "emergencia", "errada-errada"), 401, "invalid_credentials")
	}
	e.expect(e.loginFrom("198.51.100.99", "emergencia", "uma-senha-longa"), 429, "too_many_attempts")
}

// Sessao de token: cookie de sessao do navegador (sem Max-Age). Login de emergencia mantem os 7 dias.
func TestTokenSessionCookieHasNoMaxAge(t *testing.T) {
	e := newEnv(t)
	res := e.do("POST", "/api/token", `{"token":"`+tokV1Issabel+`"}`, jsonHdr)
	sc := res.Header.Get("Set-Cookie")
	res.Body.Close()
	if !strings.Contains(sc, cookieName+"=") || strings.Contains(sc, "Max-Age") || strings.Contains(sc, "Expires") ||
		!strings.Contains(sc, "HttpOnly") || !strings.Contains(sc, "Secure") || !strings.Contains(sc, "SameSite=Strict") {
		t.Errorf("Set-Cookie do token: %q", sc)
	}
	e.addUser("emergencia", "uma-senha-longa")
	res = e.loginFrom("", "emergencia", "uma-senha-longa")
	res.Body.Close()
	if !strings.Contains(res.Header.Get("Set-Cookie"), "Max-Age=604800") {
		t.Errorf("login: %q", res.Header.Get("Set-Cookie"))
	}
}

// POST declarado como vindo de outro site e recusado (403) antes de qualquer processamento.
func TestCrossSitePostRefused(t *testing.T) {
	e := newEnv(t)
	e.addUser("emergencia", "uma-senha-longa")
	cross := map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"}
	e.expect(e.do("POST", "/api/login", `{"username":"emergencia","password":"uma-senha-longa"}`, cross), 403, "forbidden")
	e.expect(e.do("POST", "/api/token", `{"token":"`+tokV1Issabel+`"}`, cross), 403, "forbidden")
	h := e.adminHdr()
	h["Sec-Fetch-Site"] = "cross-site"
	e.expect(e.do("POST", "/api/groups/import", groupsBody(csvOK, true), h), 403, "forbidden")
	if len(e.groupNames()) != 0 {
		t.Error("nada pode ser gravado")
	}
	for _, site := range []string{"same-origin", "same-site", "none", ""} {
		h := map[string]string{"Content-Type": "application/json"}
		if site != "" {
			h["Sec-Fetch-Site"] = site
		}
		res := e.do("POST", "/api/login", `{"username":"emergencia","password":"errada-errada"}`, h)
		if res.StatusCode != 401 {
			t.Errorf("Sec-Fetch-Site %q: %d", site, res.StatusCode)
		}
		res.Body.Close()
	}
}

func TestSVGAssetHasLockedCSP(t *testing.T) {
	e := newEnv(t)
	res := e.do("GET", "/assets/x.svg", "", nil)
	res.Body.Close()
	if got := res.Header.Get("Content-Security-Policy"); got != "default-src 'none'" {
		t.Errorf("svg CSP = %q", got)
	}
	res = e.do("GET", "/assets/x.js", "", nil)
	res.Body.Close()
	if res.Header.Get("Content-Security-Policy") != "" {
		t.Errorf("js nao leva CSP propria: %q", res.Header.Get("Content-Security-Policy"))
	}
}

// Teto de linhas do CSV pela tela: 20.000. Exatamente 20.000 (com ou sem quebra final) passa; 20.001 da 400.
// Linhas de comentario enchem o arquivo sem custar 20.000 inserts no SQLite.
func TestGroupsImportLineCap(t *testing.T) {
	e := newEnv(t)
	h := e.adminHdr()
	filler := strings.Repeat("# x\n", maxGroupsCSVLines-1)
	exact := filler + "Suporte,2000\n" // 20.000 linhas, quebra final
	b := e.expect(e.do("POST", "/api/groups/import", groupsBody(exact, true), h), 200, "")
	if string(b.Data) != `{"replace":true,"rows":1}` || e.reloads != 1 {
		t.Fatalf("20.000 linhas devem passar: %s reloads=%d", b.Data, e.reloads)
	}
	e.expect(e.do("POST", "/api/groups/import", groupsBody(strings.TrimSuffix(exact, "\n"), true), h), 200, "")

	over := filler + "# x\nSuporte,2000\n" // 20.001 linhas
	m := e.expect(e.do("POST", "/api/groups/import", groupsBody(over, true), h), 400, "invalid_csv")
	if !strings.Contains(m.Error.Message, "20001") || !strings.Contains(m.Error.Message, "20000") {
		t.Errorf("mensagem: %q", m.Error.Message)
	}
	if e.reloads != 2 {
		t.Errorf("o 400 nao recarrega: %d", e.reloads)
	}
}

// Grupo com marca bidirecional pela tela: 400 invalid_csv.
func TestGroupsImportRefusesBidiName(t *testing.T) {
	e := newEnv(t)
	h := e.adminHdr()
	for _, name := range []string{"Supo\u202erte", "Su\u200bporte", "Su\u200eporte", "Su\u061cporte", "Su\ufeffporte", "Su\u2028porte", "Su\u2029porte"} {
		e.expect(e.do("POST", "/api/groups/import", groupsBody(name+",2000\n", true), h), 400, "invalid_csv")
	}
	if len(e.groupNames()) != 0 {
		t.Error("nada pode ser gravado")
	}
}
