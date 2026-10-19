package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"painel-ramais/internal/auth"
	"painel-ramais/internal/store"
)

type sessionHandler func(w http.ResponseWriter, r *http.Request, sess store.Session)

// session exige cookie de sessao valido; sem ele, 401 unauthorized.
func (s *Server) session(h sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Sessao ausente ou expirada")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		sess, err := s.Store.GetSession(ctx, auth.HashSessionID(c.Value), s.Now())
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusUnauthorized, "unauthorized", "Sessao ausente ou expirada")
		case err != nil:
			s.internalError(w, r, err)
		default:
			sess = s.slide(ctx, sess)
			h(w, r, sess)
		}
	}
}

// EmergencyUser e o unico usuario de senha aceito em POST /api/login (ADR-004 item 5);
// o instalador cria o usuario com este mesmo nome.
const EmergencyUser = "emergencia"

type sessionData struct {
	Subject   string      `json:"subject"`
	Kind      string      `json:"kind"`
	Admin     bool        `json:"admin"`
	ExpiresAt stampString `json:"expires_at"`
}

func newSessionData(kind, subject string, expires time.Time) sessionData {
	return sessionData{Subject: subject, Kind: kind, Admin: auth.IsAdmin(kind, subject), ExpiresAt: stampString(expires)}
}

// sessionInfo: GET /api/session (a tela descobre papel e validade; renova como todo GET autenticado).
func (s *Server) sessionInfo(w http.ResponseWriter, _ *http.Request, sess store.Session) {
	writeOK(w, newSessionData(sess.Kind, sess.Subject, sess.ExpiresAt), nil)
}

// adminOnly exige sessao de administrador do Issabel; senao 403 forbidden (rotas de grupo).
func (s *Server) adminOnly(h sessionHandler) sessionHandler {
	return s.adminOnlyMsg("Apenas administradores do Issabel podem alterar grupos", h)
}

// adminOnlyMsg e o adminOnly com a mensagem do 403 da rota (a checagem vem antes de ler o corpo).
func (s *Server) adminOnlyMsg(msg string, h sessionHandler) sessionHandler {
	return func(w http.ResponseWriter, r *http.Request, sess store.Session) {
		if !auth.IsAdmin(sess.Kind, sess.Subject) {
			s.log.Warn("acesso negado: so administrador", requestIDKey, requestID(r), "path", r.URL.Path, "sub", sess.Subject)
			writeError(w, http.StatusForbidden, "forbidden", msg)
			return
		}
		if s.StillAdmin != nil {
			user := strings.TrimPrefix(sess.Subject, auth.AdminPrefix)
			ok, err := s.StillAdmin(r.Context(), user)
			if err != nil || !ok { // falha fechado: sem leitura do acl.db, nenhuma escrita
				s.log.Warn("escrita recusada: usuario fora do grupo administrator do Issabel ou acl.db ilegivel",
					requestIDKey, requestID(r), "path", r.URL.Path, "sub", sess.Subject, "err", err)
				writeError(w, http.StatusForbidden, "forbidden", "Seu usuario nao esta mais no grupo administrator do Issabel (ou o Issabel nao pode ser consultado); a visualizacao continua")
				return
			}
		}
		h(w, r, sess)
	}
}

// stampString serializa como RFC 3339 UTC com milissegundos.
type stampString time.Time

func (t stampString) MarshalJSON() ([]byte, error) {
	return quote(time.Time(t).UTC().Format(stampLayout)), nil
}

// startSession grava a sessao, entrega o cookie e responde 200.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, kind, subject string, userID *int64) {
	cookie, idHash, err := auth.NewSessionID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	now := s.Now()
	se := store.Session{IDHash: idHash, Kind: kind, UserID: userID, Subject: subject, CreatedAt: now, ExpiresAt: now.Add(s.SessionTTL)}
	if err := s.Store.CreateSession(r.Context(), se); err != nil {
		s.internalError(w, r, err)
		return
	}
	c := &http.Cookie{Name: cookieName, Value: cookie, Path: cookiePath, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}
	if kind != "token" { // sessao de token (menu do Issabel): cookie de sessao do navegador, some ao fechar; o servidor ainda vence por inatividade
		c.MaxAge = int(cookieMaxAge.Seconds())
	}
	http.SetCookie(w, c)
	s.log.Info("sessao criada", requestIDKey, requestID(r), "kind", kind, "sub", subject)
	writeOK(w, newSessionData(kind, subject, se.ExpiresAt), nil)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if n := utf8.RuneCountInString(in.Username); n < 1 || n > 64 || len(in.Password) < 1 || len(in.Password) > auth.MaxPasswordBytes {
		writeError(w, http.StatusBadRequest, "bad_request", "Usuario (1 a 64 caracteres) e senha (1 a 72 bytes) sao obrigatorios")
		return
	}
	key := strings.ToLower(in.Username)
	who := userTag(key)
	// Teto de bcrypt simultaneo: cada comparacao custa centenas de ms de CPU.
	select {
	case s.loginSem <- struct{}{}:
		defer func() { <-s.loginSem }()
	default:
		s.log.Warn("login: bcrypt saturado", requestIDKey, requestID(r))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Muitas tentativas; aguarde alguns instantes")
		return
	}
	// A tentativa e contada ANTES do bcrypt (reserva atomica); o acerto desfaz com Reset.
	// Dois limites: por IP do cliente (5 em 5 min; quem erra de um lugar nao trava o usuario
	// para os outros) e por nome (teto maior, contra tentativa distribuida em varios IPs).
	ip := clientIP(r)
	if !s.IPLimiter.Reserve(ip) || !s.Limiter.Reserve(key) {
		s.log.Warn("login bloqueado", requestIDKey, requestID(r), "user", who, "ip", ip)
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Muitas tentativas; aguarde alguns minutos")
		return
	}
	// So o usuario reservado existe para o login; qualquer outro nome segue o caminho do
	// usuario inexistente (Hash vazio, bcrypt falso), para o tempo nao revelar nada.
	var u store.User
	if strings.EqualFold(in.Username, EmergencyUser) {
		var err error
		if u, err = s.Store.UserByName(r.Context(), in.Username); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.internalError(w, r, err)
			return
		}
	}
	if !auth.CheckPassword(u.Hash, in.Password) { // Hash vazio: compara contra hash falso
		// O log guarda so tamanho e hash curto do que foi digitado: quem erra o campo
		// costuma colar a senha no lugar do usuario.
		s.log.Warn("login recusado", requestIDKey, requestID(r), "user", who, "ip", ip)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Usuario ou senha invalidos")
		return
	}
	s.Limiter.Reset(key)
	s.IPLimiter.Reset(ip)
	s.startSession(w, r, "password", u.Username, &u.ID)
}

// clientIP e o IP do cliente para o limite de login: o ultimo valor do X-Forwarded-For (o
// Apache acrescenta o endereco que viu no fim; valores a esquerda sao do cliente e nao
// valem) ou, sem o cabecalho (acesso direto em loopback), o RemoteAddr.
func clientIP(r *http.Request) string {
	if xs := r.Header.Values("X-Forwarded-For"); len(xs) > 0 {
		last := xs[len(xs)-1]
		if i := strings.LastIndexByte(last, ','); i >= 0 {
			last = last[i+1:]
		}
		if ip := net.ParseIP(strings.TrimSpace(last)); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// userTag identifica o usuario digitado sem gravar o texto: tamanho e 8 hex do SHA-256.
func userTag(typed string) string {
	h := sha256.Sum256([]byte(typed))
	return fmt.Sprintf("len=%d sha=%x", len(typed), h[:4])
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		if err := s.Store.DeleteSession(r.Context(), auth.HashSessionID(c.Value)); err != nil {
			s.log.Error("logout", requestIDKey, requestID(r), "erro", err.Error())
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: cookiePath, MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	writeOK(w, nil, nil)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	claims, err := s.Verifier.Verify(in.Token)
	if err != nil {
		s.log.Warn("token recusado", requestIDKey, requestID(r))
		writeError(w, http.StatusUnauthorized, "invalid_token", "Token invalido ou expirado")
		return
	}
	s.startSession(w, r, "token", claims.Sub, nil)
}

// SessionValid diz se a sessao do hash ainda vale (usado ao revalidar WebSockets).
func (s *Server) SessionValid(ctx context.Context, hash [32]byte) bool {
	_, err := s.Store.GetSession(ctx, hash, s.Now())
	return err == nil
}

// Revalidate fecha com 4001 os WebSockets cuja sessao sumiu (SIGHUP depois de user del/passwd).
func (s *Server) Revalidate(ctx context.Context) int {
	closed := 0
	for _, c := range s.hub.Clients() {
		if !s.SessionValid(ctx, c.hash) {
			c.kick(closeSession, "revoked")
			closed++
		}
	}
	return closed
}

// slide renova a sessao quando passou da metade da inatividade permitida (uma escrita
// a cada TTL/2 por sessao, nao a cada requisicao). Falha ao renovar nao derruba o pedido.
func (s *Server) slide(ctx context.Context, sess store.Session) store.Session {
	now := s.Now()
	if sess.ExpiresAt.Sub(now) >= s.SessionTTL/2 {
		return sess
	}
	next := now.Add(s.SessionTTL)
	if err := s.Store.RenewSession(ctx, sess.IDHash, now, next); err != nil {
		return sess
	}
	sess.ExpiresAt = next
	return sess
}
