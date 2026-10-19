package calls

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"
)

// EmptyRecheck e a espera antes de confirmar uma lista vazia na releitura quando ja havia
// ramais: a lista vazia durante uma recarga pode ser transitoria, e acreditar nela esvaziaria
// a tela inteira.
const EmptyRecheck = 3 * time.Second

// Pause espera d ou o fim de ctx.
func Pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CallerName tira o nome de `"Maria Souza" <2001>`. Só número ou vazio = "".
func CallerName(cid string) string {
	cid = strings.TrimSpace(cid)
	if i := strings.IndexByte(cid, '<'); i >= 0 {
		cid = cid[:i]
	}
	cid = strings.Trim(strings.TrimSpace(cid), `"`)
	if cid == "<unknown>" {
		return ""
	}
	return cid
}

// HostOnly tira a porta de "192.0.2.23:5060" (sem porta, devolve o próprio texto).
func HostOnly(s string) string {
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
}

// HostPort separa "192.0.2.23:5060" em host e porta (1 a 65535). Sem porta valida a porta vale 0 e o
// host e o proprio texto.
func HostPort(s string) (host string, port int) {
	h, p, err := net.SplitHostPort(s)
	if err != nil {
		return s, 0
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return h, 0
	}
	return h, n
}

// First devolve o primeiro texto não vazio.
func First(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
