// Package api serve o REST e o WebSocket em loopback (CONTRATOS.md secoes 1 a 5).
package api

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"painel-ramais/internal/auth"
	"painel-ramais/internal/depcheck"
	"painel-ramais/internal/model"
	"painel-ramais/internal/state"
	"painel-ramais/internal/store"
)

const (
	cookieName   = "painel_session"
	cookiePath   = "/painel-ramais/"
	maxBodyBytes = 4096
	// maxGroupsBodyBytes vale para /api/groups/import, create, update e delete (CSV dentro do JSON).
	maxGroupsBodyBytes = 1 << 20
	// maxAssignBodyBytes vale so para /api/groups/assign: 20000 ramais de ate 64 caracteres ASCII
	// custam 20000 x (64 + 2 aspas + 1 virgula) = ~1,34 MB, acima de 1 MiB; 2 MiB cobre esse pior
	// caso com folga (10.000 ramais de 4 a 6 digitos ocupam ~90 KB).
	maxAssignBodyBytes = 2 << 20
	// cookieMaxAge so precisa sobreviver a sessao: quem manda e o banco (inatividade). Com a
	// tela aberta so por WebSocket nenhuma resposta REST renova o cookie.
	cookieMaxAge = 7 * 24 * time.Hour
	requestIDKey = "request_id"
	headerReqID  = "X-Request-Id"
)

// localOnlyStatus e o que /api/health e /debug respondem a requisicao vinda do
// proxy (CONTRATOS.md secoes 1 e 1.2: 404, para nao revelar a rota).
// maxConcurrentLogins e o teto de bcrypt simultaneo no login; acima disso, 429.
const maxConcurrentLogins = 4

const localOnlyStatus = http.StatusNotFound

// Options reune as dependencias do servidor.
type Options struct {
	Store         *store.Store
	Verifier      *auth.Verifier
	Limiter       *auth.Limiter // por nome de usuario (teto maior que o do IP)
	IPLimiter     *auth.Limiter // por IP do cliente; nil = padrao (5 falhas em 5 min)
	Hub           *Hub
	Stats         func() state.Stats
	AMI           func() model.AMIStatus
	Deps          func() []depcheck.Item
	SessionTTL    time.Duration // inatividade maxima; a sessao e renovada enquanto ha uso
	RenewEvery    time.Duration // intervalo de renovacao pelo WebSocket (padrao TTL/6)
	RetentionDays int
	Version       string
	Log           *slog.Logger
	Now           func() time.Time
	// Static e a tela embutida (index.html, acesso.html, assets/); nil desliga as rotas da tela.
	Static fs.FS
	// ReloadGroups relê os grupos do banco e os entrega ao estado (o caminho do SIGHUP);
	// nil nos testes que nao precisam dele.
	ReloadGroups func(context.Context) error
	// StillAdmin confere no acl.db do Issabel, a cada escrita de grupo, se o usuario continua
	// no grupo administrator (IssabelACL). nil so nos testes; o serve.go sempre liga.
	StillAdmin func(ctx context.Context, user string) (bool, error)
}

// Server e o handler HTTP do daemon.
type Server struct {
	Options
	hub      *Hub
	log      *slog.Logger
	started  time.Time
	loginSem chan struct{} // limita comparacoes bcrypt simultaneas
}

// New monta o servidor; Options.Now e Log tem padrao.
func New(o Options) *Server {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.IPLimiter == nil {
		o.IPLimiter = auth.NewLimiter()
	}
	return &Server{Options: o, hub: o.Hub, log: o.Log, started: o.Now(), loginSem: make(chan struct{}, maxConcurrentLogins)}
}

// Handler devolve o roteador com request id, log e recuperacao de panic.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", s.api(http.MethodPost, s.login))
	mux.HandleFunc("/api/logout", s.api(http.MethodPost, s.logout))
	mux.HandleFunc("/api/token", s.api(http.MethodPost, s.token))
	mux.HandleFunc("/api/session", s.api(http.MethodGet, s.session(s.sessionInfo)))
	mux.HandleFunc("/api/snapshot", s.api(http.MethodGet, s.session(s.snapshot)))
	mux.HandleFunc("/api/groups", s.api(http.MethodGet, s.session(s.groups)))
	mux.HandleFunc("/api/groups/create", s.api(http.MethodPost, s.session(s.adminOnly(s.groupsCreate))))
	mux.HandleFunc("/api/groups/update", s.api(http.MethodPost, s.session(s.adminOnly(s.groupsUpdate))))
	mux.HandleFunc("/api/groups/assign", s.api(http.MethodPost, s.session(s.adminOnly(s.groupsAssign))))
	mux.HandleFunc("/api/groups/import", s.api(http.MethodPost, s.session(s.adminOnly(s.groupsImport))))
	mux.HandleFunc("/api/groups/delete", s.api(http.MethodPost, s.session(s.adminOnly(s.groupsDelete))))
	mux.HandleFunc("/api/history", s.api(http.MethodGet, s.session(s.history)))
	mux.HandleFunc("/api/health", s.api(http.MethodGet, s.localOnly(s.health)))
	mux.HandleFunc("/debug", s.method(http.MethodGet, s.localOnly(s.session(s.debug))))
	mux.HandleFunc("/ws", s.method(http.MethodGet, s.session(s.ws)))
	mux.HandleFunc("/acesso", s.page("/acesso", s.acesso))
	mux.HandleFunc("/emergencia", s.page("/emergencia", s.emergenciaRedirect))
	mux.HandleFunc("/emergencia/", s.page("/emergencia/", s.emergencia))
	mux.HandleFunc("/assets", s.page("/assets", s.asset)) // 404 (sem isso o ServeMux redireciona para /assets/)
	mux.HandleFunc("/assets/", s.page("", s.asset))
	mux.HandleFunc("/", s.page("/", s.tela))
	return s.middleware(noDotDot(mux))
}

// noDotDot responde 404 no envelope a caminho com segmento "..": sem isso o ServeMux
// redirecionaria (301) /assets/../index.html para /index.html.
func noDotDot(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, seg := range strings.Split(r.URL.Path, "/") {
			if seg == ".." {
				writeError(w, http.StatusNotFound, "not_found", "Rota inexistente")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---- envelope ----

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Success bool      `json:"success"`
	Data    any       `json:"data"`
	Error   *apiError `json:"error"`
	Meta    any       `json:"meta"`
}

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(body)
}

func writeOK(w http.ResponseWriter, data, meta any) {
	writeJSON(w, http.StatusOK, mustJSON(envelope{Success: true, Data: data, Meta: meta}))
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, mustJSON(envelope{Error: &apiError{Code: code, Message: msg}}))
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("erro interno", requestIDKey, requestID(r), "path", r.URL.Path, "erro", err.Error())
	writeError(w, http.StatusInternalServerError, "internal", "Erro interno")
}

// ---- middleware ----

type ctxKey struct{}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(ctxKey{}).(string)
	return id
}

// statusWriter registra o status e deixa o upgrade de WebSocket passar.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack nao suportado")
	}
	w.status = http.StatusSwitchingProtocols
	return h.Hijack()
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [8]byte
		rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		w.Header().Set(headerReqID, id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, id))
		sw := &statusWriter{ResponseWriter: w}
		start := s.Now()
		defer func() {
			if p := recover(); p != nil {
				s.log.Error("panic no handler", requestIDKey, id, "path", r.URL.Path, "panic", fmt.Sprint(p))
				if sw.status == 0 {
					writeError(sw, http.StatusInternalServerError, "internal", "Erro interno")
				}
			}
			s.log.Info("request", requestIDKey, id, "method", r.Method, "path", r.URL.Path,
				"status", sw.status, "dur_ms", s.Now().Sub(start).Milliseconds())
		}()
		next.ServeHTTP(sw, r)
	})
}

// method rejeita metodo errado com 405 no envelope.
func (s *Server) method(m string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != m {
			w.Header().Set("Allow", m)
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Metodo nao permitido")
			return
		}
		h(w, r)
	}
}

// api = method + gzip (quando o cliente aceita). O WS e o /debug nao passam por aqui.
func (s *Server) api(m string, h http.HandlerFunc) http.HandlerFunc {
	return s.method(m, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.Close()
		h(gw, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	zw      *gzip.Writer
	encoded bool
	started bool
}

func (g *gzipWriter) WriteHeader(code int) {
	if !g.started {
		g.started = true
		// Resposta que ja veio comprimida (snapshot em cache) ou sem corpo nao recomprime.
		if g.Header().Get("Content-Encoding") == "" && code != http.StatusNoContent {
			g.Header().Set("Content-Encoding", "gzip")
			g.Header().Del("Content-Length")
			g.encoded = true
		}
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.started {
		g.WriteHeader(http.StatusOK)
	}
	if !g.encoded {
		return g.ResponseWriter.Write(b)
	}
	if g.zw == nil {
		g.zw = gzip.NewWriter(g.ResponseWriter)
	}
	return g.zw.Write(b)
}

func (g *gzipWriter) Close() {
	if g.zw != nil {
		g.zw.Close()
	}
}

func (s *Server) localOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Host") != "" {
			writeError(w, localOnlyStatus, "not_found", "Rota inexistente")
			return
		}
		h(w, r)
	}
}

// ---- entrada JSON ----

// decodeBody valida Content-Type e tamanho e decodifica o JSON estrito em v.
// Devolve false depois de responder o erro.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeBodyMax(w, r, v, maxBodyBytes)
}

func sizeText(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d KiB", n>>10)
}

// decodeBodyMax e o decodeBody com teto de corpo proprio.
func decodeBodyMax(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	// Mutacao so da propria origem: Sec-Fetch-Site ausente (cliente sem o cabecalho, curl), same-origin
	// ou none (acao direta do usuario). same-site (subdominio) e cross-site: recusa.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		writeError(w, http.StatusForbidden, "forbidden", "Requisicao de outro site nao permitida")
		return false
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Use Content-Type: application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err = dec.Decode(v)
	if err == nil && dec.More() {
		err = errors.New("dados apos o JSON")
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Corpo acima de "+sizeText(limit))
	case err != nil:
		writeError(w, http.StatusBadRequest, "bad_request", "JSON invalido")
	default:
		return true
	}
	return false
}
