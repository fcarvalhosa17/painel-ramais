// Package depcheck verifica a cada 60 s as dependencias da integracao com o
// Issabel: login AMI, include de manager_custom.conf e conf do Apache.
// Somente leitura; nao importa pacote interno.
package depcheck

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	StatusOK      = "ok"
	StatusFail    = "fail"
	StatusUnknown = "unknown"

	DefaultManagerConf       = "/etc/asterisk/manager.conf"
	DefaultManagerCustomConf = "/etc/asterisk/manager_custom.conf"
	DefaultApacheConf        = "/etc/httpd/conf.d/painel-ramais.conf"

	beginMarker = "; BEGIN painel-ramais"
	endMarker   = "; END painel-ramais"
)

// Item e o resultado de uma verificacao (formato de /api/health deps[]).
type Item struct {
	Name      string
	Status    string
	Detail    string
	CheckedAt time.Time
}

// LoginResult e o ultimo Login da conexao AMI unica (nao abre segunda conexao).
type LoginResult struct {
	Known  bool
	OK     bool
	Detail string
	At     time.Time
}

// Checker roda as verificacoes. Campos vazios usam os caminhos padrao.
type Checker struct {
	ManagerConf, ManagerCustomConf, ApacheConf string
	Login                                      func() LoginResult
	Now                                        func() time.Time
	Log                                        func(msg string, args ...any)

	mu   sync.RWMutex
	last []Item
}

// Check executa as tres verificacoes e guarda o resultado.
func (c *Checker) Check() []Item {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	t := now()
	items := []Item{c.checkLogin(t), c.checkInclude(t), c.checkApache(t)}
	c.mu.Lock()
	prev := c.last
	c.last = items
	c.mu.Unlock()
	c.logChanges(prev, items)
	return items
}

// Results devolve o ultimo resultado (vazio antes da primeira verificacao).
func (c *Checker) Results() []Item {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Item(nil), c.last...)
}

// OK diz se ha resultado e todos os itens estao ok.
func (c *Checker) OK() bool {
	r := c.Results()
	for _, it := range r {
		if it.Status != StatusOK {
			return false
		}
	}
	return len(r) > 0
}

// Run verifica ja e depois a cada interval, ate ctx terminar.
func (c *Checker) Run(ctx context.Context, interval time.Duration) {
	c.Check()
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			c.Check()
		}
	}
}

func (c *Checker) logChanges(prev, cur []Item) {
	if c.Log == nil {
		return
	}
	old := map[string]string{}
	for _, it := range prev {
		old[it.Name] = it.Status
	}
	for _, it := range cur {
		if it.Status != StatusOK && old[it.Name] != it.Status {
			c.Log("verificacao de dependencia falhou", "dep", it.Name, "status", it.Status, "detail", it.Detail)
		} else if it.Status == StatusOK && old[it.Name] != "" && old[it.Name] != StatusOK {
			c.Log("dependencia voltou ao normal", "dep", it.Name)
		}
	}
}

func (c *Checker) checkLogin(t time.Time) Item {
	it := Item{Name: "ami_user", CheckedAt: t}
	var r LoginResult
	if c.Login != nil {
		r = c.Login()
	}
	switch {
	case !r.Known:
		it.Status, it.Detail = StatusUnknown, "ainda sem tentativa de login no AMI; confira ami.address e o segredo em ami-secret"
	case r.OK:
		it.Status, it.Detail = StatusOK, "login aceito em "+r.At.UTC().Format(time.RFC3339)
	default:
		it.Status = StatusFail
		it.Detail = "login recusado pelo AMI: " + r.Detail + ". Confira o bloco [painel-ramais] em manager_custom.conf e o ami-secret"
	}
	return it
}

func (c *Checker) checkInclude(t time.Time) Item {
	it := Item{Name: "manager_custom_include", CheckedAt: t}
	conf := pick(c.ManagerConf, DefaultManagerConf)
	included, err := hasInclude(conf)
	if err != nil {
		return c.readFailed(it, conf, err, "rode: ls -l /etc/asterisk/manager*.conf")
	}
	if !included {
		it.Status, it.Detail = StatusFail, "manager.conf nao inclui manager_custom.conf; o IssabelPBX pode ter regerado o arquivo. Reinstale o pacote para restaurar"
		return it
	}
	custom := pick(c.ManagerCustomConf, DefaultManagerCustomConf)
	block, err := hasBlock(custom)
	if err != nil {
		return c.readFailed(it, custom, err, "rode: ls -l /etc/asterisk/manager_custom.conf")
	}
	if !block {
		it.Status, it.Detail = StatusFail, "bloco painel-ramais ausente em manager_custom.conf. Reinstale o pacote para restaurar"
		return it
	}
	it.Status, it.Detail = StatusOK, "manager.conf inclui manager_custom.conf e o bloco painel-ramais esta presente"
	return it
}

// readFailed decide o veredito quando um arquivo do Asterisk nao pode ser lido.
// O %post tira o acesso de "outros" (o-rwx) e o servico nao fica no grupo asterisk:
// EACCES e esperado, e quem prova que o bloco esta ativo e o Login AMI aceito.
func (c *Checker) readFailed(it Item, path string, err error, hint string) Item {
	if !errors.Is(err, fs.ErrPermission) {
		return failRead(it, path, err, hint)
	}
	var r LoginResult
	if c.Login != nil {
		r = c.Login()
	}
	switch {
	case r.Known && r.OK:
		it.Status = StatusOK
		it.Detail = filepath.Base(path) + " nao legivel pelo servico (esperado, o-rwx); login AMI aceito confirma o usuario"
	case r.Known:
		it.Status = StatusFail
		it.Detail = filepath.Base(path) + " nao legivel pelo servico e o login AMI foi recusado: " + r.Detail + ". Reinstale o pacote para restaurar o bloco"
	default:
		it.Status = StatusUnknown
		it.Detail = filepath.Base(path) + " nao legivel pelo servico (esperado, o-rwx) e ainda nao houve login AMI para confirmar o usuario"
	}
	return it
}

func (c *Checker) checkApache(t time.Time) Item {
	it := Item{Name: "apache_proxy_conf", CheckedAt: t}
	p := pick(c.ApacheConf, DefaultApacheConf)
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			it.Status, it.Detail = StatusFail, p+" ausente; reinstale o pacote (rpm -U --replacepkgs) para recriar e rode httpd -t"
			return it
		}
		return failRead(it, p, err, "confira as permissoes de /etc/httpd/conf.d")
	}
	it.Status, it.Detail = StatusOK, p+" presente"
	return it
}

func failRead(it Item, path string, err error, hint string) Item {
	if errors.Is(err, fs.ErrNotExist) {
		it.Status, it.Detail = StatusFail, path+" nao existe"
		return it
	}
	it.Status = StatusUnknown
	it.Detail = fmt.Sprintf("sem leitura de %s (%v); %s", path, errors.Unwrap(err), hint)
	return it
}

func pick(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// hasInclude procura "#include manager_custom.conf" nao comentado.
func hasInclude(path string) (bool, error) {
	return scan(path, func(line string) bool {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "#include") && !strings.HasPrefix(l, "#tryinclude") {
			return false
		}
		return strings.Contains(l, "manager_custom.conf")
	})
}

// hasBlock confere se a secao [painel-ramais] esta entre os marcadores.
func hasBlock(path string) (bool, error) {
	inside, found := false, false
	_, err := scan(path, func(line string) bool {
		l := strings.TrimSpace(line)
		switch {
		case l == beginMarker:
			inside = true
		case l == endMarker:
			inside = false
		case inside && l == "[painel-ramais]":
			found = true
		}
		return false
	})
	return found, err
}

// openFile e trocavel nos testes (simular EACCES sem depender de permissao do sistema).
var openFile = os.Open

func scan(path string, match func(string) bool) (bool, error) {
	f, err := openFile(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	hit := false
	for sc.Scan() {
		if match(sc.Text()) {
			hit = true
		}
	}
	return hit, sc.Err()
}
