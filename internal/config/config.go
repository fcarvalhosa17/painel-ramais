// Package config le e valida /etc/painel-ramais/config.yaml (CONTRATOS.md secao 6).
// Chave desconhecida, tipo errado ou valor fora da regra: o daemon nao sobe.
package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config e o config.yaml ja validado.
type Config struct {
	Listen           string `yaml:"listen"`
	Technology       string `yaml:"technology"`
	ExtensionPattern string `yaml:"extension_pattern"`
	DataDir          string `yaml:"data_dir"`
	LogLevel         string `yaml:"log_level"`
	AMI              AMI    `yaml:"ami"`
	Auth             Auth   `yaml:"auth"`
	History          struct {
		RetentionDays int `yaml:"retention_days"`
	} `yaml:"history"`

	// Pattern e o extension_pattern compilado, envolvido em ^(?:...)$.
	Pattern *regexp.Regexp `yaml:"-"`
}

type AMI struct {
	Address    string `yaml:"address"`
	Username   string `yaml:"username"`
	SecretFile string `yaml:"secret_file"`
	// NameFillPerSecond limita as consultas de nome (PJSIPShowEndpoint/SIPshowpeer) em segundo plano.
	NameFillPerSecond int `yaml:"name_fill_per_second"`
}

type Auth struct {
	SecretFile string        `yaml:"secret_file"`
	SessionTTL time.Duration `yaml:"session_ttl"`
}

// Defaults devolve a configuracao padrao do contrato.
func Defaults() Config {
	c := Config{
		Listen: "127.0.0.1:8790", Technology: "auto", ExtensionPattern: `^[0-9]+$`,
		DataDir: "/var/lib/painel-ramais", LogLevel: "info",
		AMI:  AMI{Address: "127.0.0.1:5038", Username: "painel-ramais", SecretFile: "/etc/painel-ramais/ami-secret", NameFillPerSecond: 20},
		Auth: Auth{SecretFile: "/etc/painel-ramais/secret", SessionTTL: 12 * time.Hour},
	}
	c.History.RetentionDays = 30
	return c
}

// Load le o arquivo, aplica padroes e valida.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ler config: %w", err)
	}
	return Parse(b)
}

// Parse valida o conteudo de um config.yaml.
func Parse(b []byte) (*Config, error) {
	c := Defaults()
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("config invalida: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("config invalida: %w", err)
	}
	return &c, nil
}

func (c *Config) validate() error {
	if err := loopback("listen", c.Listen); err != nil {
		return err
	}
	if err := loopback("ami.address", c.AMI.Address); err != nil {
		return err
	}
	switch c.Technology {
	case "auto", "sip", "pjsip":
	default:
		return fmt.Errorf("technology: %q (use auto, sip ou pjsip)", c.Technology)
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level: %q (use debug, info, warn ou error)", c.LogLevel)
	}
	re, err := regexp.Compile("^(?:" + c.ExtensionPattern + ")$")
	if err != nil {
		return fmt.Errorf("extension_pattern: %w", err)
	}
	c.Pattern = re
	if !strings.HasPrefix(c.DataDir, "/") && !isWindowsAbs(c.DataDir) {
		return fmt.Errorf("data_dir: %q precisa ser caminho absoluto", c.DataDir)
	}
	if n := len(c.AMI.Username); n < 1 || n > 64 {
		return errors.New("ami.username: de 1 a 64 caracteres")
	}
	if c.AMI.NameFillPerSecond < 1 || c.AMI.NameFillPerSecond > 200 {
		return fmt.Errorf("ami.name_fill_per_second: %d fora de 1 a 200", c.AMI.NameFillPerSecond)
	}
	if c.AMI.SecretFile == "" || c.Auth.SecretFile == "" {
		return errors.New("ami.secret_file e auth.secret_file sao obrigatorios")
	}
	if c.Auth.SessionTTL < 15*time.Minute || c.Auth.SessionTTL > 24*time.Hour {
		return fmt.Errorf("auth.session_ttl: %s fora de 15m a 24h", c.Auth.SessionTTL)
	}
	if c.History.RetentionDays < 1 || c.History.RetentionDays > 365 {
		return fmt.Errorf("history.retention_days: %d fora de 1 a 365", c.History.RetentionDays)
	}
	return nil
}

// isWindowsAbs deixa os testes e o desenvolvimento no Windows usarem C:\...
func isWindowsAbs(p string) bool {
	return len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// loopback exige host IP de loopback (127.0.0.0/8 ou ::1) e porta valida.
func loopback(key, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s: %q nao e host:porta", key, addr)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%s: %q precisa ser loopback (127.0.0.0/8 ou ::1)", key, host)
	}
	if p, err := net.LookupPort("tcp", port); err != nil || p == 0 {
		return fmt.Errorf("%s: porta %q invalida", key, port)
	}
	return nil
}

// AMISecret le o segredo do AMI (arquivo 0600, dono do servico, nao vazio).
func (c *Config) AMISecret() (string, error) {
	b, err := readSecret(c.AMI.SecretFile)
	if err != nil {
		return "", fmt.Errorf("ami.secret_file: %w", err)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", errors.New("ami.secret_file: arquivo vazio")
	}
	return s, nil
}

// AuthSecret le o segredo HMAC (base64 de 32 bytes ou mais, arquivo 0600).
func (c *Config) AuthSecret() ([]byte, error) {
	b, err := readSecret(c.Auth.SecretFile)
	if err != nil {
		return nil, fmt.Errorf("auth.secret_file: %w", err)
	}
	return DecodeSecret(string(b))
}

// DecodeSecret decodifica o conteudo do arquivo de segredo (base64, minimo 32 bytes).
func DecodeSecret(s string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, errors.New("auth.secret_file: base64 invalido")
	}
	if len(raw) < 32 {
		return nil, fmt.Errorf("auth.secret_file: %d bytes, minimo 32", len(raw))
	}
	return raw, nil
}

func readSecret(path string) ([]byte, error) {
	if err := checkSecretPerm(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return b, nil
}
