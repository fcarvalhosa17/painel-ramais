package depcheck

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const goodCustom = "[outro]\n; BEGIN painel-ramais\n[painel-ramais]\nsecret = x\n; END painel-ramais\n"

// fakeMode faz o statFile devolver o modo dado (o stat real do Windows nao tem o modo do Linux).
func fakeMode(t *testing.T, mode fs.FileMode) {
	t.Helper()
	old := statFile
	statFile = func(string) (fs.FileInfo, error) {
		return fstest.MapFS{"f": &fstest.MapFile{Mode: mode}}.Stat("f")
	}
	t.Cleanup(func() { statFile = old })
}

// fakeMenu faz o menuItemExists devolver o resultado dado (o menu.db real so existe no Issabel).
func fakeMenu(t *testing.T, found bool, err error) {
	t.Helper()
	old := menuItemExists
	menuItemExists = func(context.Context, string) (bool, error) { return found, err }
	t.Cleanup(func() { menuItemExists = old })
}

func setup(t *testing.T, manager, custom string) *Checker {
	t.Helper()
	fakeMode(t, 0o640)
	fakeMenu(t, true, nil)
	d := t.TempDir()
	c := &Checker{
		ManagerConf: filepath.Join(d, "manager.conf"), ManagerCustomConf: filepath.Join(d, "manager_custom.conf"),
		ApacheConf: filepath.Join(d, "painel-ramais.conf"),
		Login:      func() LoginResult { return LoginResult{Known: true, OK: true, At: time.Now()} },
	}
	if manager != "" {
		os.WriteFile(c.ManagerConf, []byte(manager), 0o600)
	}
	if custom != "" {
		os.WriteFile(c.ManagerCustomConf, []byte(custom), 0o600)
	}
	os.WriteFile(c.ApacheConf, []byte("x"), 0o600)
	return c
}

func status(items []Item) map[string]string {
	m := map[string]string{}
	for _, it := range items {
		m[it.Name] = it.Status
	}
	return m
}

func TestAllOK(t *testing.T) {
	c := setup(t, "[general]\n#include manager_custom.conf\n", goodCustom)
	for _, it := range c.Check() {
		if it.Status != StatusOK {
			t.Errorf("%+v", it)
		}
	}
	if !c.OK() || len(c.Results()) != 6 {
		t.Error("OK()/Results()")
	}
}

func TestDetectsWhatDisappeared(t *testing.T) {
	cases := []struct {
		name, manager, custom, dep string
		rmApache                   bool
	}{
		{"include comentado", "; #include manager_custom.conf\n", goodCustom, "manager_custom_include", false},
		{"manager.conf regerado sem include", "[general]\n", goodCustom, "manager_custom_include", false},
		{"bloco sumiu", "#include manager_custom.conf\n", "[outro]\n", "manager_custom_include", false},
		{"secao fora dos marcadores", "#include manager_custom.conf\n", "[painel-ramais]\n", "manager_custom_include", false},
		{"manager.conf ausente", "", goodCustom, "manager_custom_include", false},
		{"apache sumiu", "#include manager_custom.conf\n", goodCustom, "apache_proxy_conf", true},
	}
	for _, tc := range cases {
		c := setup(t, tc.manager, tc.custom)
		if tc.rmApache {
			os.Remove(c.ApacheConf)
		}
		if got := status(c.Check())[tc.dep]; got != StatusFail || c.OK() {
			t.Errorf("%s: %s=%s", tc.name, tc.dep, got)
		}
	}
}

func TestLoginStates(t *testing.T) {
	c := setup(t, "#include manager_custom.conf\n", goodCustom)
	c.Login = func() LoginResult { return LoginResult{} }
	if s := status(c.Check())["ami_user"]; s != StatusUnknown {
		t.Errorf("sem tentativa: %s", s)
	}
	c.Login = func() LoginResult { return LoginResult{Known: true, Detail: "Authentication failed"} }
	it := c.Check()[0]
	if it.Status != StatusFail || !strings.Contains(it.Detail, "Authentication failed") {
		t.Errorf("%+v", it)
	}
	c.Login = nil
	if s := status(c.Check())["ami_user"]; s != StatusUnknown {
		t.Error("sem fonte de login")
	}
}

func TestLogsOnlyOnChange(t *testing.T) {
	c := setup(t, "[general]\n", goodCustom)
	var n int
	c.Log = func(string, ...any) { n++ }
	c.Check()
	c.Check()
	if n != 1 {
		t.Errorf("esperava 1 log (so a mudanca), veio %d", n)
	}
}

// O %post tira o acesso de outros (o-rwx) e o servico nao le manager_custom.conf:
// EACCES nao pode deixar o /api/health degradado para sempre.
func TestUnreadableByServiceIsJudgedByAMILogin(t *testing.T) {
	denied := func(path string) (*os.File, error) {
		if strings.HasSuffix(path, "manager_custom.conf") || strings.HasSuffix(path, "manager.conf") {
			return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
		}
		return os.Open(path)
	}
	old := openFile
	openFile = denied
	defer func() { openFile = old }()

	cases := []struct {
		name  string
		login LoginResult
		want  string
	}{
		{"login aceito", LoginResult{Known: true, OK: true, At: time.Now()}, StatusOK},
		{"login recusado", LoginResult{Known: true, Detail: "Authentication failed"}, StatusFail},
		{"sem login ainda", LoginResult{}, StatusUnknown},
	}
	for _, tc := range cases {
		c := setup(t, "#include manager_custom.conf\n", goodCustom)
		c.Login = func() LoginResult { return tc.login }
		got := c.Check()[1]
		if got.Status != tc.want {
			t.Errorf("%s: %+v", tc.name, got)
		}
		if tc.want == StatusOK && !strings.Contains(got.Detail, "login AMI aceito confirma o usuario") {
			t.Errorf("detalhe: %s", got.Detail)
		}
	}
	// manager.conf legivel e so o custom bloqueado: mesmo veredito.
	openFile = func(p string) (*os.File, error) {
		if strings.HasSuffix(p, "manager_custom.conf") {
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission}
		}
		return os.Open(p)
	}
	c := setup(t, "#include manager_custom.conf\n", goodCustom)
	if got := c.Check()[1]; got.Status != StatusOK {
		t.Errorf("so custom bloqueado: %+v", got)
	}
}

func byName(items []Item, name string) Item {
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	return Item{}
}

// Pendencia 3: o modo do manager_custom.conf e vigiado; so le o stat.
func TestManagerCustomPerm(t *testing.T) {
	cases := []struct {
		mode fs.FileMode
		want string
	}{
		{0o640, StatusOK}, {0o600, StatusOK}, {0o660, StatusOK}, {0o440, StatusOK}, {0o750, StatusOK},
		{0o644, StatusFail}, {0o604, StatusFail}, {0o601, StatusFail}, {0o666, StatusFail}, {0o777, StatusFail},
	}
	for _, tc := range cases {
		c := setup(t, "#include manager_custom.conf\n", goodCustom)
		fakeMode(t, tc.mode)
		it := byName(c.Check(), "manager_custom_perm")
		if it.Status != tc.want {
			t.Errorf("modo %04o: %+v", tc.mode, it)
		}
		if tc.want == StatusFail {
			want := fmt.Sprintf("(modo %04o)", tc.mode)
			if !strings.Contains(it.Detail, "chmod o-rwx ") || !strings.Contains(it.Detail, want) || !strings.Contains(it.Detail, "senha AMI") {
				t.Errorf("modo %04o: detalhe sem a dica: %q", tc.mode, it.Detail)
			}
		}
	}
}

// stat falho (ausente, EACCES no diretorio) e unknown, nunca ok.
func TestManagerCustomPermStatFailureIsUnknown(t *testing.T) {
	for name, serr := range map[string]error{
		"ausente": &fs.PathError{Op: "stat", Path: "x", Err: fs.ErrNotExist},
		"eacces":  &fs.PathError{Op: "stat", Path: "x", Err: fs.ErrPermission},
	} {
		c := setup(t, "#include manager_custom.conf\n", goodCustom)
		statFile = func(string) (fs.FileInfo, error) { return nil, serr }
		it := byName(c.Check(), "manager_custom_perm")
		if it.Status != StatusUnknown || it.Detail == "" {
			t.Errorf("%s: %+v", name, it)
		}
	}
	// Mudanca de estado sai no log (logChanges).
	c := setup(t, "#include manager_custom.conf\n", goodCustom)
	var logs []string
	c.Log = func(msg string, args ...any) { logs = append(logs, fmt.Sprint(msg, args)) }
	c.Check()
	fakeMode(t, 0o644)
	c.Check()
	if len(logs) != 1 || !strings.Contains(logs[0], "manager_custom_perm") {
		t.Errorf("logs: %v", logs)
	}
}

func TestIssabelKeyItem(t *testing.T) {
	c := setup(t, "#include manager_custom.conf\n", goodCustom)
	c.IssabelKeyFile = "/etc/painel-ramais-issabel.key"
	var delivered []byte
	var calls int
	c.OnIssabelKey = func(k []byte) { delivered, calls = k, calls+1 }
	key := []byte("0123456789abcdef0123456789abcdef")
	notExist := &fs.PathError{Op: "lstat", Path: "k", Err: fs.ErrNotExist}
	rejected := fmt.Errorf("auth.issabel_key_file: %w: permissao 0644, o maximo aceito e 0640", ErrKeyInvalid)
	cases := []struct {
		name, want string
		key        []byte
		err        error
		detail     string
	}{
		{"valida", StatusOK, key, nil, "integracao ligada"},
		{"ausente", StatusOK, nil, notExist, "integracao com o menu desligada"},
		{"recusada", StatusFail, nil, rejected, "chmod 0640 /etc/painel-ramais-issabel.key"},
		{"erro de leitura", StatusUnknown, nil, &fs.PathError{Op: "open", Path: "k", Err: fs.ErrPermission}, "sem leitura da chave"},
		{"volta a valida", StatusOK, key, nil, "integracao ligada"},
	}
	for i, tc := range cases {
		c.IssabelKey = func() ([]byte, error) { return tc.key, tc.err }
		it := byName(c.Check(), "issabel_key")
		if it.Status != tc.want || !strings.Contains(it.Detail, tc.detail) {
			t.Errorf("%s: %+v", tc.name, it)
		}
		if calls != i+1 || string(delivered) != string(tc.key) {
			t.Errorf("%s: o verificador deve receber a chave (ou nil) a cada verificacao: calls=%d key=%q", tc.name, calls, delivered)
		}
	}
	if d := byName(c.Check(), "issabel_key").Detail; !strings.Contains(d, "ligada") {
		t.Errorf("detalhe: %s", d)
	}
	// Recusada tambem traz o problema vindo do erro.
	c.IssabelKey = func() ([]byte, error) { return nil, rejected }
	if d := byName(c.Check(), "issabel_key").Detail; !strings.Contains(d, "permissao 0644") || !strings.Contains(d, "chown painel-ramais:") {
		t.Errorf("recusada: %s", d)
	}
	// Sem fonte de chave configurada: integracao desligada, nao falha.
	c.IssabelKey = nil
	if it := byName(c.Check(), "issabel_key"); it.Status != StatusOK || delivered != nil {
		t.Errorf("sem fonte: %+v", it)
	}
}
