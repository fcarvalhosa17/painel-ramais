package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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

type sessionData struct {
	Subject   string      `json:"subject"`
	Kind      string      `json:"kind"`
	ExpiresAt stampString `json:"expires_at"`
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
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: cookie, Path: cookiePath, MaxAge: int(cookieMaxAge.Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	s.log.Info("sessao criada", requestIDKey, requestID(r), "kind", kind, "sub", subject)
	writeOK(w, sessionData{Subject: subject, Kind: kind, ExpiresAt: stampString(se.ExpiresAt)}, nil)
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
	if !s.Limiter.Reserve(key) {
		s.log.Warn("login bloqueado", requestIDKey, requestID(r), "user", who)
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Muitas tentativas; aguarde alguns minutos")
		return
	}
	u, err := s.Store.UserByName(r.Context(), in.Username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	if !auth.CheckPassword(u.Hash, in.Password) { // Hash vazio: compara contra hash falso
		// O log guarda so tamanho e hash curto do que foi digitado: quem erra o campo
		// costuma colar a senha no lugar do usuario.
		s.log.Warn("login recusado", requestIDKey, requestID(r), "user", who)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Usuario ou senha invalidos")
		return
	}
	s.Limiter.Reset(key)
	s.startSession(w, r, "password", u.Username, &u.ID)
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
