package calls

import (
	"net"
	"strings"
)

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

// HostOnly tira a porta de "10.0.5.23:5060" (sem porta, devolve o próprio texto).
func HostOnly(s string) string {
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
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
