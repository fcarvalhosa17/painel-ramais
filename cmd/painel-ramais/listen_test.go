package main

import (
	"net"
	"os"
	"testing"
)

type fakeEnv map[string]string

func (f fakeEnv) get(k string) string  { return f[k] }
func (f fakeEnv) unset(k string) error { delete(f, k); return nil }

// act monta uma activation com um listener TCP real no lugar do descritor 3.
func act(t *testing.T, env fakeEnv, ln net.Listener) activation {
	t.Helper()
	tmp, err := os.CreateTemp(t.TempDir(), "fd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tmp.Close() })
	return activation{
		getenv: env.get, unsetenv: env.unset, pid: 4242,
		newFile:      func(fd uintptr, name string) *os.File { return tmp },
		fileListener: func(*os.File) (net.Listener, error) { return ln, nil },
	}
}

func TestSocketActivationUsesInheritedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	env := fakeEnv{"LISTEN_PID": "4242", "LISTEN_FDS": "1", "LISTEN_FDNAMES": "x"}
	got, activated, err := openListener("127.0.0.1:1", act(t, env, ln)) // addr do config e ignorado
	if err != nil || !activated || got != ln {
		t.Fatalf("activated=%v err=%v", activated, err)
	}
	if len(env) != 0 {
		t.Errorf("LISTEN_* devem sair do ambiente: %v", env)
	}
}

func TestOpensOwnPortOutsideSystemd(t *testing.T) {
	probe, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := probe.Addr().String()
	probe.Close()
	for name, env := range map[string]fakeEnv{
		"sem variaveis":    {},
		"pid de outro":     {"LISTEN_PID": "1", "LISTEN_FDS": "1"}, // herdado de um processo pai
		"zero descritores": {"LISTEN_PID": "4242", "LISTEN_FDS": "0"},
		"pid ilegivel":     {"LISTEN_PID": "abc", "LISTEN_FDS": "1"},
	} {
		a := act(t, env, nil)
		ln, activated, err := openListener(addr, a)
		if err != nil || activated {
			t.Fatalf("%s: activated=%v err=%v", name, activated, err)
		}
		ln.Close()
	}
}

func TestSocketActivationRefusesBadSetups(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	if _, _, err := openListener("x", act(t, fakeEnv{"LISTEN_PID": "4242", "LISTEN_FDS": "2"}, ln)); err == nil {
		t.Error("2 sockets: recusar")
	}
	if _, _, err := openListener("x", act(t, fakeEnv{"LISTEN_PID": "4242", "LISTEN_FDS": "-1"}, ln)); err == nil {
		t.Error("LISTEN_FDS negativo: recusar")
	}
	// Unit com ListenStream publico nao pode expor o painel.
	pub, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skip(err)
	}
	defer pub.Close()
	if _, _, err := openListener("x", act(t, fakeEnv{"LISTEN_PID": "4242", "LISTEN_FDS": "1"}, pub)); err == nil {
		t.Error("socket fora do loopback: recusar")
	}
}
