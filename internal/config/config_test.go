package config

import (
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
	if c.Listen != "127.0.0.1:8790" || c.Auth.SessionTTL != 12*time.Hour || c.AMI.NameFillPerSecond != 20 || c.History.RetentionDays != 30 || c.LogLevel != "debug" {
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
		"ami nao loopback":       "ami:\n  address: 10.0.0.1:5038\n",
		"technology":             "technology: iax\n",
		"log_level":              "log_level: trace\n",
		"regex":                  "extension_pattern: '('\n",
		"data_dir relativo":      "data_dir: dados\n",
		"ttl curto":              "auth:\n  session_ttl: 1m\n",
		"ttl longo":              "auth:\n  session_ttl: 48h\n",
		"nomes por segundo 0":    "ami:\n  name_fill_per_second: 0\n",
		"nomes por segundo alto": "ami:\n  name_fill_per_second: 500\n",
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
