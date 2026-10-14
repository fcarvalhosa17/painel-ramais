// Package auth reune senha (bcrypt), id de sessao, token HMAC de uso unico
// (ADR-002) e o limite de tentativas de login. Nao importa pacote interno.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost e o custo do hash (contrato: 12). Variavel so para os testes baixarem.
var BcryptCost = 12

const (
	MinPasswordLen   = 10
	MaxPasswordBytes = 72 // limite do bcrypt
)

// ValidatePassword aplica a regra de criacao de senha (CLI).
func ValidatePassword(pw string) error {
	if len(pw) > MaxPasswordBytes {
		return fmt.Errorf("senha acima de %d bytes", MaxPasswordBytes)
	}
	if len([]rune(pw)) < MinPasswordLen {
		return fmt.Errorf("senha com menos de %d caracteres", MinPasswordLen)
	}
	return nil
}

// HashPassword gera o hash bcrypt.
func HashPassword(pw string) (string, error) {
	if err := ValidatePassword(pw); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), BcryptCost)
	return string(h), err
}

// CheckPassword compara senha e hash; hash vazio compara contra um hash falso
// de mesmo custo, para o tempo nao revelar se o usuario existe.
func CheckPassword(hash, pw string) bool {
	if len(pw) > MaxPasswordBytes {
		return false
	}
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(pw))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

var (
	dummyOnce sync.Once
	dummy     []byte
)

func dummyHash() []byte {
	dummyOnce.Do(func() { dummy, _ = bcrypt.GenerateFromPassword([]byte("painel-ramais-dummy"), BcryptCost) })
	return dummy
}

// NewSessionID gera o valor do cookie (32 bytes aleatorios em base64url) e o
// hash SHA-256 que vai para o banco.
func NewSessionID() (cookie string, idHash [32]byte, err error) {
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", idHash, err
	}
	cookie = base64.RawURLEncoding.EncodeToString(raw[:])
	return cookie, HashSessionID(cookie), nil
}

// HashSessionID e o SHA-256 do valor bruto do cookie.
func HashSessionID(cookie string) [32]byte { return sha256.Sum256([]byte(cookie)) }

// Limiter bloqueia o usuario depois de Max falhas dentro de Window.
type Limiter struct {
	Max    int
	Window time.Duration
	Now    func() time.Time

	mu    sync.Mutex
	fails map[string][]time.Time
}

// NewLimiter cria o limite do contrato: 5 falhas em 5 minutos.
func NewLimiter() *Limiter {
	return &Limiter{Max: 5, Window: 5 * time.Minute, Now: time.Now, fails: map[string][]time.Time{}}
}

const maxTrackedUsers = 10000

// Reserve conta uma tentativa de key ANTES da operacao cara (bcrypt), de forma atomica:
// false se o limite ja foi atingido. Quem acerta chama Reset, que desfaz a contagem.
func (l *Limiter) Reserve(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.prune(key)) >= l.Max {
		return false
	}
	return l.add(key)
}

// add grava uma tentativa. Com o mapa cheio, descarta primeiro as chaves vencidas e,
// se ainda estiver cheio, a chave cuja ultima tentativa e a mais antiga (memoria
// limitada sem negar o usuario novo).
// ponytail: o descarte reinicia a contagem da vitima; um atacante precisa de ~10 mil
// nomes falsos (cada um custa um bcrypt dummy) para ganhar 5 tentativas.
func (l *Limiter) add(key string) bool {
	if _, tracked := l.fails[key]; !tracked && len(l.fails) >= maxTrackedUsers {
		l.evict()
	}
	l.fails[key] = append(l.prune(key), l.Now())
	return true
}

func (l *Limiter) evict() {
	var oldestKey string
	var oldest time.Time
	for k := range l.fails {
		ts := l.prune(k)
		if len(ts) == 0 {
			continue // prune ja apagou a vencida
		}
		if last := ts[len(ts)-1]; oldestKey == "" || last.Before(oldest) {
			oldestKey, oldest = k, last
		}
	}
	if len(l.fails) >= maxTrackedUsers {
		delete(l.fails, oldestKey)
	}
}

// Reset limpa as falhas de key (login certo).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}

func (l *Limiter) prune(key string) []time.Time {
	cut := l.Now().Add(-l.Window)
	ts := l.fails[key]
	i := 0
	for i < len(ts) && !ts[i].After(cut) {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = ts
	return ts
}

// ErrInvalidToken e o unico erro de token; o motivo nao sai da funcao.
var ErrInvalidToken = errors.New("token invalido")
