package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	// MaxTokenTTL e o teto de vida de um token (60 s) mais 5 s de folga de relogio.
	MaxTokenTTL   = 60 * time.Second
	tokenSkew     = 5 * time.Second
	maxTokenBytes = 1024
	subMax        = 128
)

var b64 = base64.RawURLEncoding

// Claims e o payload do token: exatamente sub, exp e jti.
type Claims struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
	JTI string `json:"jti"`
}

type rawClaims struct {
	Sub *string `json:"sub"`
	Exp *int64  `json:"exp"`
	JTI *string `json:"jti"`
}

// IssueToken gera um token assinado (CLI de teste; o emissor da fase 3 faz o mesmo).
func IssueToken(secret []byte, sub string, ttl time.Duration, now time.Time) (string, error) {
	if ttl <= 0 || ttl > MaxTokenTTL {
		return "", errors.New("ttl fora de 1ns a 60s")
	}
	if n := len(sub); n < 1 || n > subMax {
		return "", errors.New("sub deve ter de 1 a 128 caracteres")
	}
	var r [16]byte
	if _, err := rand.Read(r[:]); err != nil {
		return "", err
	}
	payload, err := json.Marshal(Claims{Sub: sub, Exp: now.Add(ttl).Unix(), JTI: b64.EncodeToString(r[:])})
	if err != nil {
		return "", err
	}
	return sign(secret, payload), nil
}

func sign(secret, payload []byte) string {
	p := b64.EncodeToString(payload)
	return p + "." + b64.EncodeToString(mac(secret, p))
}

func mac(secret []byte, p string) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(p))
	return h.Sum(nil)
}

// Verifier valida tokens e guarda os jti ja usados (uso unico, em memoria).
type Verifier struct {
	secret []byte
	Now    func() time.Time

	mu   sync.Mutex
	seen map[string]int64 // jti -> exp
}

// NewVerifier cria o verificador para o segredo informado.
func NewVerifier(secret []byte) *Verifier {
	return &Verifier{secret: secret, Now: time.Now, seen: map[string]int64{}}
}

// Verify confere o token na ordem do ADR-002 e consome o jti. Qualquer falha
// devolve ErrInvalidToken, sem dizer o motivo.
func (v *Verifier) Verify(token string) (Claims, error) {
	c, err := v.parse(token)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	now := v.Now()
	exp := time.Unix(c.Exp, 0)
	if !now.Before(exp) || exp.After(now.Add(MaxTokenTTL+tokenSkew)) {
		return Claims{}, ErrInvalidToken
	}
	if !v.consume(c, now) {
		return Claims{}, ErrInvalidToken
	}
	return c, nil
}

func (v *Verifier) parse(token string) (Claims, error) {
	if len(token) > maxTokenBytes {
		return Claims{}, ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Claims{}, ErrInvalidToken
	}
	sig, err := b64.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, mac(v.secret, parts[0])) {
		return Claims{}, ErrInvalidToken
	}
	payload, err := b64.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	return decodeClaims(payload)
}

func decodeClaims(payload []byte) (Claims, error) {
	var r rawClaims
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil || dec.More() || r.Sub == nil || r.Exp == nil || r.JTI == nil {
		return Claims{}, ErrInvalidToken
	}
	c := Claims{Sub: *r.Sub, Exp: *r.Exp, JTI: *r.JTI}
	if n := len(c.Sub); n < 1 || n > subMax {
		return Claims{}, ErrInvalidToken
	}
	if n := len(c.JTI); n < 16 || n > 64 {
		return Claims{}, ErrInvalidToken
	}
	if _, err := b64.DecodeString(c.JTI); err != nil {
		return Claims{}, ErrInvalidToken
	}
	return c, nil
}

// consume marca o jti como usado e limpa os vencidos; false se ja foi visto.
func (v *Verifier) consume(c Claims, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for j, exp := range v.seen {
		if exp <= now.Unix() {
			delete(v.seen, j)
		}
	}
	if _, dup := v.seen[c.JTI]; dup {
		return false
	}
	v.seen[c.JTI] = c.Exp
	return true
}
