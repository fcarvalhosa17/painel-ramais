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
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxTokenTTL e o teto de vida de um token (60 s) mais 5 s de folga de relogio.
	MaxTokenTTL   = 60 * time.Second
	tokenSkew     = 5 * time.Second
	maxTokenBytes = 1024
	subMax        = 128

	// Gramatica do sub assinado com a chave do Issabel (ADR-004 item 3, plano C1).
	IssabelPrefix  = "issabel:"
	AdminPrefix    = "issabel:admin:"
	UserPrefix     = "issabel:user:"
	issabelUserMax = 64 // runas do <usuario>
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

// HasIssabelPrefix diz se o sub comeca com o prefixo reservado (sem diferenciar maiusculas).
func HasIssabelPrefix(sub string) bool {
	return len(sub) >= len(IssabelPrefix) && strings.EqualFold(sub[:len(IssabelPrefix)], IssabelPrefix)
}

// ValidIssabelSub confere a gramatica issabel:admin:<usuario> | issabel:user:<usuario>:
// <usuario> de 1 a 64 runas, UTF-8 valido, sem controle (Cc), formatacao (Cf: bidi, largura
// zero, soft hyphen, BOM) nem separador de linha/paragrafo (Zl, Zp); pode ter ':' e espaco.
func ValidIssabelSub(sub string) bool {
	var name string
	switch {
	case strings.HasPrefix(sub, AdminPrefix):
		name = sub[len(AdminPrefix):]
	case strings.HasPrefix(sub, UserPrefix):
		name = sub[len(UserPrefix):]
	default:
		return false
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > issabelUserMax || !utf8.ValidString(name) {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool {
		return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
	})
}

// IsAdmin: o papel de administrador deriva do subject gravado (nenhuma coluna propria).
func IsAdmin(kind, subject string) bool {
	return kind == "token" && strings.HasPrefix(subject, AdminPrefix)
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
// Tem duas chaves: o segredo do daemon (sub livre, sem o prefixo issabel:) e a
// chave do Issabel (so sub issabel:admin:/issabel:user:), esta opcional e
// trocavel em tempo de execucao.
type Verifier struct {
	secret  []byte
	issabel atomic.Pointer[[]byte] // nil = integracao desligada
	Now     func() time.Time

	mu   sync.Mutex
	seen map[string]int64 // jti -> exp
}

// NewVerifier cria o verificador para o segredo informado.
func NewVerifier(secret []byte) *Verifier {
	return &Verifier{secret: secret, Now: time.Now, seen: map[string]int64{}}
}

// SetIssabelKey liga (chave nao vazia) ou desliga (nil) a integracao com o Issabel.
// A chave e copiada; vale para o proximo Verify.
func (v *Verifier) SetIssabelKey(key []byte) {
	if len(key) == 0 {
		v.issabel.Store(nil)
		return
	}
	k := append([]byte(nil), key...)
	v.issabel.Store(&k)
}

// Verify confere o token na ordem do ADR-002 e do plano C1 (formato, HMAC com as duas
// chaves, JSON estrito, regra do sub, exp, jti) e consome o jti. Qualquer falha
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
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	// As duas comparacoes sempre rodam (sem curto-circuito).
	viaSecret := hmac.Equal(sig, mac(v.secret, parts[0]))
	viaIssabel := false
	if k := v.issabel.Load(); k != nil {
		viaIssabel = hmac.Equal(sig, mac(*k, parts[0]))
	}
	if !viaSecret && !viaIssabel {
		return Claims{}, ErrInvalidToken
	}
	payload, err := b64.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	c, err := decodeClaims(payload)
	if err != nil {
		return Claims{}, err
	}
	// Chave do Issabel: so a gramatica issabel:. Segredo do daemon: nunca issabel:.
	if !(viaIssabel && ValidIssabelSub(c.Sub)) && !(viaSecret && !HasIssabelPrefix(c.Sub)) {
		return Claims{}, ErrInvalidToken
	}
	return c, nil
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
