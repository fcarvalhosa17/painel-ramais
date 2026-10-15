package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDebugListenerSoLoopback(t *testing.T) {
	for _, a := range []string{"0.0.0.0:0", "192.168.1.5:6060", ":6060", "localhost:6060", "lixo"} {
		if ln, err := debugListener(a); err == nil {
			ln.Close()
			t.Errorf("%q deveria ser recusado", a)
		}
	}
	ln, err := debugListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
}

func TestDebugHandler(t *testing.T) {
	h := debugHandler()
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/debug/mem", nil))
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"heap_inuse"`) {
		t.Fatalf("mem: %d %s", r.Code, r.Body)
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/debug/pprof/heap?gc=1", nil))
	if r.Code != http.StatusOK || r.Body.Len() == 0 {
		t.Fatalf("heap: %d", r.Code)
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/debug/pprof/goroutine", nil))
	if r.Code != http.StatusNotFound {
		t.Fatalf("so heap deveria existir, veio %d", r.Code)
	}
}
