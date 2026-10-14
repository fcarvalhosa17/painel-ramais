// Package amitest serve transcrições de respostas AMI para testes dos
// adaptadores. É só apoio de teste: nenhum código de produção o importa.
package amitest

import (
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"painel-ramais/internal/ami"
)

// Serve sobe um Asterisk de mentira em 127.0.0.1. Login e Ping são aceitos.
// Para as demais actions, replies[action] é o nome de um arquivo com a
// resposta como o Asterisk a enviaria (blocos separados por linha em branco,
// sem ActionID: o servidor insere o da action). Action sem entrada recebe
// "Response: Error". Actions recebidas ficam em Seen.
func Serve(t *testing.T, replies map[string]string) (addr string, seen func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("sem loopback TCP: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	var actions []string
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.WriteString(c, "Asterisk Call Manager/5.0.1\r\n")
		p := ami.NewParser(c)
		for {
			m, err := p.Next()
			if err != nil {
				return
			}
			a := m.Get("Action")
			mu.Lock()
			actions = append(actions, a)
			mu.Unlock()
			io.WriteString(c, reply(t, replies, a, m.ActionID()))
		}
	}()
	return ln.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), actions...)
	}
}

func reply(t *testing.T, replies map[string]string, action, id string) string {
	switch action {
	case "Login", "Ping", "Logoff":
		return "Response: Success\r\nActionID: " + id + "\r\n\r\n"
	}
	file, ok := replies[action]
	if !ok {
		return "Response: Error\r\nActionID: " + id + "\r\nMessage: Invalid/unknown command: " + action + "\r\n\r\n"
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Errorf("amitest: %v", err)
		return ""
	}
	var out strings.Builder
	for _, block := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" || strings.HasPrefix(block, "#") {
			continue
		}
		// o ActionID entra na segunda linha, depois de Response:/Event:
		lines := strings.SplitN(block, "\n", 2)
		out.WriteString(lines[0] + "\r\nActionID: " + id + "\r\n")
		if len(lines) > 1 {
			out.WriteString(strings.ReplaceAll(lines[1], "\n", "\r\n") + "\r\n")
		}
		out.WriteString("\r\n")
	}
	return out.String()
}
