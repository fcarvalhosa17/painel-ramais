package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultsAndPattern(t *testing.T) {
	c, err := Parse([]byte("log_level: debug\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8790" || c.Auth.SessionTTL != 12*time.Hour || c.AMI.NameFillPerSecond != 20 || c.AMI.InventoryRefresh != 10*time.Minute || c.History.RetentionDays != 30 || c.LogLevel != "debug" {
		t.Errorf("padroes: %+v", c)
	}
	if !c.Pattern.MatchString("2001") || c.Pattern.MatchString("2001a") || c.Pattern.MatchString("") {
		t.Error("pattern deve casar o nome inteiro")
	}
	// Alternancia sem ancora tambem casa inteiro: ^(?:1|20)$ nao casa "201".
	c, _ = Parse([]byte("extension_pattern: '1|20'\n"))
	if c.Pattern.MatchString("201") {
		t.Error("pattern com | nao pode casar parcial")
	}
}

func TestInvalid(t *testing.T) {
	cases := map[string]string{
		"chave desconhecida":     "listen_typo: x\n",
		"tipo errado":            "history:\n  retention_days: abc\n",
		"listen publico":         "listen: 0.0.0.0:8790\n",
		"listen nome":            "listen: localhost:8790\n",
		"listen sem porta":       "listen: 127.0.0.1\n",
		"ami nao loopback":       "ami:\n  address: 192.0.2.1:5038\n",
		"technology":             "technology: iax\n",
		"log_level":              "log_level: trace\n",
		"regex":                  "extension_pattern: '('\n",
		"data_dir relativo":      "data_dir: dados\n",
		"ttl curto":              "auth:\n  session_ttl: 1m\n",
		"ttl longo":              "auth:\n  session_ttl: 48h\n",
		"nomes por segundo 0":    "ami:\n  name_fill_per_second: 0\n",
		"nomes por segundo alto": "ami:\n  name_fill_per_second: 500\n",
		"releitura curta":        "ami:\n  inventory_refresh: 10s\n",
		"releitura longa":        "ami:\n  inventory_refresh: 48h\n",
		"retencao":               "history:\n  retention_days: 0\n",
		"username":               "ami:\n  username: ''\n",
	}
	for name, y := range cases {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%s: deveria recusar", name)
		}
	}
	if _, err := Parse([]byte("listen: '[::1]:9000'\n")); err != nil {
		t.Errorf("::1 deve valer: %v", err)
	}
}

func TestSecrets(t *testing.T) {
	d := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(d, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	c := Defaults()
	c.AMI.SecretFile = write("ami", "  s3cret \n")
	if s, err := c.AMISecret(); err != nil || s != "s3cret" {
		t.Errorf("ami secret = %q, %v", s, err)
	}
	c.AMI.SecretFile = write("ami-vazio", " \n")
	if _, err := c.AMISecret(); err == nil {
		t.Error("segredo vazio deve falhar")
	}
	c.Auth.SecretFile = write("sec", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=\n")
	if b, err := c.AuthSecret(); err != nil || len(b) != 32 {
		t.Errorf("auth secret: %d, %v", len(b), err)
	}
	c.Auth.SecretFile = write("curto", "AAECAwQ=")
	if _, err := c.AuthSecret(); err == nil || !strings.Contains(err.Error(), "minimo 32") {
		t.Errorf("segredo curto: %v", err)
	}
	c.Auth.SecretFile = write("lixo", "%%%")
	if _, err := c.AuthSecret(); err == nil {
		t.Error("base64 invalido deve falhar")
	}
	c.Auth.SecretFile = filepath.Join(d, "nao-existe")
	if _, err := c.AuthSecret(); err == nil {
		t.Error("arquivo ausente deve falhar")
	}
}

func TestIssabelKeyFileConfig(t *testing.T) {
	c, err := Parse([]byte(""))
	if err != nil || c.Auth.IssabelKeyFile != "/etc/painel-ramais-issabel.key" {
		t.Fatalf("padrao: %+v %v", c, err)
	}
	if c, err = Parse([]byte("auth:\n  issabel_key_file: /x/chave\n")); err != nil || c.Auth.IssabelKeyFile != "/x/chave" {
		t.Errorf("caminho proprio: %v", err)
	}
	for _, y := range []string{"auth:\n  issabel_key_file: ''\n", "auth:\n  issabel_key_file: chave\n"} {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%q deveria ser recusado (vazio ou relativo)", y)
		}
	}
}

// Regra de permissao da chave do Issabel (plano C1), testada sobre o modo para rodar em qualquer SO.
func TestKeyModeProblem(t *testing.T) {
	const me = 1000
	cases := []struct {
		name string
		mode fs.FileMode
		uid  int
		ok   bool
	}{
		{"0640 aceito", 0o640, me, true},
		{"0600 aceito", 0o600, me, true},
		{"0440 aceito", 0o440, me, true},
		{"0400 aceito", 0o400, me, true},
		{"0644 recusado (leitura de outros)", 0o644, me, false},
		{"0660 recusado (escrita de grupo)", 0o660, me, false},
		{"0650 recusado (execucao de grupo)", 0o650, me, false},
		{"0604 recusado", 0o604, me, false},
		{"0777 recusado", 0o777, me, false},
		{"symlink recusado", fs.ModeSymlink | 0o640, me, false},
		{"diretorio recusado", fs.ModeDir | 0o640, me, false},
		{"dono diferente recusado", 0o640, 0, false},
	}
	for _, c := range cases {
		if got := keyModeProblem(c.mode, c.uid, me); (got == "") != c.ok {
			t.Errorf("%s: problema = %q", c.name, got)
		}
	}
}

// Conteudo e ausencia (roda no Windows tambem; permissao fica no config_unix_test.go).
func TestIssabelKeyContentAndAbsence(t *testing.T) {
	d := t.TempDir()
	c := Defaults()
	c.Auth.IssabelKeyFile = filepath.Join(d, "chave")
	_, err := c.IssabelKey()
	if !errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrKeyInvalid) {
		t.Fatalf("ausente deve casar com fs.ErrNotExist e nao com ErrKeyInvalid: %v", err)
	}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(c.Auth.IssabelKeyFile, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("  ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8=\n")
	if k, err := c.IssabelKey(); err != nil || len(k) != 32 || k[0] != 0x20 || k[31] != 0x3f {
		t.Errorf("chave valida: %d bytes, %v", len(k), err)
	}
	for name, content := range map[string]string{
		"curta (menos de 32 bytes)": "AAECAwQ=",
		"base64 invalido":           "%%%",
		"vazia":                     " \n",
	} {
		write(content)
		if _, err := c.IssabelKey(); !errors.Is(err, ErrKeyInvalid) {
			t.Errorf("%s: err = %v, queria ErrKeyInvalid", name, err)
		}
	}
	write("AAECAwQ=")
	if _, err := c.IssabelKey(); err == nil || !strings.Contains(err.Error(), "minimo 32") {
		t.Errorf("mensagem da chave curta: %v", err)
	}
}
