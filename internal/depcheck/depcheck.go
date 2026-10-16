// Package depcheck verifica a cada 60 s as dependencias da integracao com o
// Issabel: login AMI, include e permissao de manager_custom.conf, conf do Apache,
// chave do menu do Issabel e item do menu no menu.db. Somente leitura; nao importa pacote interno.
package depcheck

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // driver "sqlite" (o mesmo do store), para ler o menu.db do Issabel
)

const (
	StatusOK      = "ok"
	StatusFail    = "fail"
	StatusUnknown = "unknown"

	DefaultManagerConf       = "/etc/asterisk/manager.conf"
	DefaultManagerCustomConf = "/etc/asterisk/manager_custom.conf"
	DefaultApacheConf        = "/etc/httpd/conf.d/painel-ramais.conf"
	DefaultMenuDB            = "/var/www/db/menu.db"

	menuItemID = "painel_ramais" // menuid do item no menu do Issabel (ADR-004)

	beginMarker = "; BEGIN painel-ramais"
	endMarker   = "; END painel-ramais"
)

// ErrKeyInvalid: a leitura da chave do Issabel achou o arquivo, mas ele viola a regra
// (permissao, dono, tipo ou conteudo). Quem fornece Checker.IssabelKey embrulha o erro com ele.
var ErrKeyInvalid = errors.New("chave do Issabel recusada")

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
	MenuDB                                     string // padrao DefaultMenuDB
	Login                                      func() LoginResult
	Now                                        func() time.Time
	Log                                        func(msg string, args ...any)

	// IssabelKey le a chave do Issabel: erro que casa com fs.ErrNotExist = ausente;
	// com ErrKeyInvalid = recusada; qualquer outro = erro de leitura. IssabelKeyFile so
	// entra nas mensagens. OnIssabelKey recebe, a cada verificacao, a chave valida ou nil:
	// e a fonte unica da chave do verificador de tokens.
	IssabelKey     func() ([]byte, error)
	IssabelKeyFile string
	OnIssabelKey   func(key []byte)

	mu   sync.RWMutex
	last []Item
}

// Check executa as verificacoes e guarda o resultado.
func (c *Checker) Check() []Item {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	t := now()
	items := []Item{c.checkLogin(t), c.checkInclude(t), c.checkApache(t), c.checkIssabelKey(t), c.checkManagerPerm(t), c.checkIssabelMenu(t)}
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

func (c *Checker) checkIssabelKey(t time.Time) Item {
	it := Item{Name: "issabel_key", CheckedAt: t}
	var key []byte
	var err error
	if c.IssabelKey != nil {
		key, err = c.IssabelKey()
	} else {
		err = fs.ErrNotExist
	}
	switch {
	case err == nil:
		it.Status, it.Detail = StatusOK, "chave do Issabel valida: integracao ligada"
	case errors.Is(err, fs.ErrNotExist):
		key = nil
		it.Status, it.Detail = StatusOK, "chave do Issabel ausente: integracao com o menu desligada"
	case errors.Is(err, ErrKeyInvalid):
		key = nil
		p := pick(c.IssabelKeyFile, "<arquivo da chave>")
		it.Status = StatusFail
		it.Detail = fmt.Sprintf("%v. Corrija: chown painel-ramais:<grupo do PHP> %s; chmod 0640 %s", err, p, p)
	default:
		key = nil
		it.Status, it.Detail = StatusUnknown, fmt.Sprintf("sem leitura da chave do Issabel (%v); a integracao com o menu fica desligada", err)
	}
	if c.OnIssabelKey != nil {
		c.OnIssabelKey(key)
	}
	return it
}

// statFile e trocavel nos testes (o modo de arquivo do Windows nao representa o do Linux).
var statFile = os.Stat

// checkManagerPerm vigia o modo do manager_custom.conf (tem a senha AMI): o %post tira o
// acesso de outros e uma atualizacao do Issabel pode devolve-lo. So le o modo (stat);
// nunca altera o arquivo. Stat que falha (ausente, EACCES no diretorio) nunca vira ok.
func (c *Checker) checkManagerPerm(t time.Time) Item {
	it := Item{Name: "manager_custom_perm", CheckedAt: t}
	p := pick(c.ManagerCustomConf, DefaultManagerCustomConf)
	fi, err := statFile(p)
	if err != nil {
		it.Status = StatusUnknown
		it.Detail = fmt.Sprintf("sem stat de %s (%v); nao da para vigiar a permissao", p, errors.Unwrap(err))
		return it
	}
	mode := fi.Mode().Perm()
	if mode&0o007 != 0 {
		it.Status = StatusFail
		it.Detail = fmt.Sprintf("%s acessivel a outros (modo %04o): contem a senha AMI. Rode: chmod o-rwx %s", filepath.Base(p), mode, p)
		return it
	}
	it.Status, it.Detail = StatusOK, fmt.Sprintf("%s sem acesso para outros (modo %04o)", filepath.Base(p), mode)
	return it
}

// menuItemExists e trocavel nos testes (o menu.db real so existe no Issabel).
var menuItemExists = queryMenuItem

// queryMenuItem abre o menu.db em modo somente leitura (mode=ro, nunca cria o arquivo) com
// busy_timeout de 1 s e pergunta so se a linha do painel existe. Nao toca em acl.db.
func queryMenuItem(ctx context.Context, path string) (bool, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var one int
	switch err := db.QueryRowContext(ctx, "SELECT 1 FROM menu WHERE id = ?", menuItemID).Scan(&one); {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

// checkIssabelMenu confere que o item do painel esta registrado no menu do Issabel. Ausente e
// fail (o instalar.py menu registra); falha de abertura ou leitura e unknown, nunca ok.
func (c *Checker) checkIssabelMenu(t time.Time) Item {
	it := Item{Name: "issabel_menu", CheckedAt: t}
	p := pick(c.MenuDB, DefaultMenuDB)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	found, err := menuItemExists(ctx, p)
	switch {
	case err != nil:
		it.Status, it.Detail = StatusUnknown, fmt.Sprintf("sem leitura de %s (%v); nao da para conferir o item do menu do Issabel", p, err)
	case !found:
		it.Status, it.Detail = StatusFail, "item do menu do Issabel ausente; rode ./instalar.py menu"
	default:
		it.Status, it.Detail = StatusOK, "item "+menuItemID+" presente no menu do Issabel"
	}
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
