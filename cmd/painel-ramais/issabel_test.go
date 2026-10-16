package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"painel-ramais/internal/auth"
)

// A verificacao de dependencias e a fonte unica da chave do Issabel: criar, estragar a
// permissao, corrigir e apagar a chave vale na proxima verificacao, sem reiniciar o daemon.
func TestIssabelKeyReachesVerifierThroughDepcheck(t *testing.T) {
	r := newRig(t)
	cfg, _ := daemonConfig(t, r, freePort(t))
	keyPath := filepath.Join(r.dir, "chave-do-daemon.key")
	cfg.Auth.IssabelKeyFile = keyPath
	d, err := newDaemon(cfg, make(chan os.Signal))
	if err != nil {
		t.Fatal(err)
	}
	defer d.st.Close()
	ts := httptest.NewServer(d.srv.Handler())
	defer ts.Close()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(0x20 + i)
	}
	post := func(sub string) int {
		t.Helper()
		tok, err := auth.IssueToken(key, sub, 30*time.Second, time.Now()) // jti novo a cada token
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.Post(ts.URL+"/api/token", "application/json", strings.NewReader(`{"token":"`+tok+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode == 200 {
			b, _ := io.ReadAll(res.Body)
			wantAdmin := strings.HasPrefix(sub, auth.AdminPrefix)
			if !strings.Contains(string(b), `"admin":`+map[bool]string{true: "true", false: "false"}[wantAdmin]) {
				t.Errorf("papel de %q: %s", sub, b)
			}
		}
		return res.StatusCode
	}
	item := func() (status, detail string) {
		t.Helper()
		for _, it := range d.deps.Results() {
			if it.Name == "issabel_key" {
				return it.Status, it.Detail
			}
		}
		t.Fatal("sem o item issabel_key")
		return
	}
	writeKey := func(mode os.FileMode) {
		t.Helper()
		os.Remove(keyPath)
		if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(keyPath, mode); err != nil {
			t.Fatal(err)
		}
		d.deps.Check()
	}

	// Sem a chave: item ok ("desligada"), V1 da 401.
	if st, det := item(); st != "ok" || !strings.Contains(det, "desligada") {
		t.Errorf("sem chave: %s %s", st, det)
	}
	if code := post("issabel:admin:ana"); code != 401 {
		t.Errorf("sem chave o token issabel: deveria dar 401, deu %d", code)
	}
	// Criada (0640): vale na hora da proxima verificacao, administrador e operador.
	writeKey(0o640)
	if st, det := item(); st != "ok" || !strings.Contains(det, "ligada") {
		t.Errorf("com chave: %s %s", st, det)
	}
	if code := post("issabel:admin:ana"); code != 200 {
		t.Errorf("chave criada: queria 200, deu %d", code)
	}
	if code := post("issabel:user:bia"); code != 200 {
		t.Errorf("operador: queria 200, deu %d", code)
	}
	// Permissao errada (o Windows nao tem modo; a regra de permissao roda no Linux): fail e 401.
	if runtime.GOOS != "windows" {
		writeKey(0o644)
		if st, det := item(); st != "fail" || !strings.Contains(det, "chmod 0640") {
			t.Errorf("0644: %s %s", st, det)
		}
		if code := post("issabel:admin:ana"); code != 401 {
			t.Errorf("chave recusada deve desligar a integracao: %d", code)
		}
		writeKey(0o640)
		if code := post("issabel:admin:ana"); code != 200 {
			t.Errorf("corrigida para 0640: %d", code)
		}
	}
	// Apagada: volta ao desligado.
	os.Remove(keyPath)
	d.deps.Check()
	if st, _ := item(); st != "ok" {
		t.Errorf("apagada: %s", st)
	}
	if code := post("issabel:admin:ana"); code != 401 {
		t.Errorf("chave apagada: queria 401, deu %d", code)
	}

	// O /api/health traz os itens novos (o segredo do daemon nunca entra nesse caminho).
	res, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var h struct {
		Data struct {
			Deps []struct{ Name, Status string }
		}
	}
	if err := json.NewDecoder(res.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, dep := range h.Data.Deps {
		names[dep.Name] = true
	}
	if !names["issabel_key"] || !names["manager_custom_perm"] {
		t.Errorf("deps no health: %v", h.Data.Deps)
	}
}

// Chave do Issabel igual ao segredo do daemon: quem le o segredo emitiria admin do Issabel.
// O item fica fail e o token issabel: da 401.
func TestIssabelKeyEqualToDaemonSecretIsRefused(t *testing.T) {
	r := newRig(t)
	cfg, _ := daemonConfig(t, r, freePort(t))
	secretPath := filepath.Join(r.dir, "secret")
	same, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Auth.IssabelKeyFile = filepath.Join(r.dir, "igual.key")
	if err := os.WriteFile(cfg.Auth.IssabelKeyFile, same, 0o640); err != nil {
		t.Fatal(err)
	}
	d, err := newDaemon(cfg, make(chan os.Signal))
	if err != nil {
		t.Fatal(err)
	}
	defer d.st.Close()
	ts := httptest.NewServer(d.srv.Handler())
	defer ts.Close()

	var it struct{ status, detail string }
	for _, x := range d.deps.Results() {
		if x.Name == "issabel_key" {
			it.status, it.detail = x.Status, x.Detail
		}
	}
	if it.status != "fail" || !strings.Contains(it.detail, "igual ao auth.secret_file") {
		t.Errorf("issabel_key: %+v", it)
	}
	secret, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(string(same)))
	tok, _ := auth.IssueToken(secret, "issabel:admin:ana", 30*time.Second, time.Now())
	res, err := http.Post(ts.URL+"/api/token", "application/json", strings.NewReader(`{"token":"`+tok+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Errorf("V1 com chaves iguais deve dar 401, deu %d", res.StatusCode)
	}
}
