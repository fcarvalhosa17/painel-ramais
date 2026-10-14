package depcheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const goodCustom = "[outro]\n; BEGIN painel-ramais\n[painel-ramais]\nsecret = x\n; END painel-ramais\n"

func setup(t *testing.T, manager, custom string) *Checker {
	t.Helper()
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
	if !c.OK() || len(c.Results()) != 3 {
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
