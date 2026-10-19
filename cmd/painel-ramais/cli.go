package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"painel-ramais/internal/auth"
	"painel-ramais/internal/config"
	"painel-ramais/internal/store"
)

type cli struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	euid           func() int
	notify         func(stderr io.Writer) // avisa o daemon (SIGHUP) depois de gravar
	readTTY        func(prompt string) (string, error)
}

func (c *cli) errf(format string, a ...any) {
	fmt.Fprintf(c.stderr, "painel-ramais: "+format+"\n", a...)
}

// parseArgs aceita flags antes e depois dos argumentos posicionais (o contrato
// escreve `user add <usuario> --password-stdin`; o flag da stdlib para no primeiro posicional).
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func (c *cli) flagSet(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	return fs, fs.String("config", defaultConfig, "caminho do config.yaml")
}

// open carrega o config e abre o banco; devolve o codigo de saida se falhar.
func (c *cli) open(cfgPath string) (*config.Config, *store.Store, int) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		c.errf("%v", err)
		return nil, nil, exitEnv
	}
	st, err := store.Open(context.Background(), cfg.DataDir)
	if err != nil {
		c.errf("banco: %v", err)
		return nil, nil, exitEnv
	}
	if rep := st.MigrationReport(); rep.NoGroupDropped > 0 { // esta CLI foi quem migrou o banco
		c.errf("aviso: %d vinculo(s) \"Sem grupo\" manual descartado(s) na migracao: %s",
			rep.NoGroupDropped, strings.Join(rep.NoGroupSample, ","))
	}
	return cfg, st, exitOK
}

// readPassword le a senha: uma linha do stdin (--password-stdin) ou do terminal, duas vezes.
func (c *cli) readPassword(fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(io.LimitReader(c.stdin, 1024)).ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			return "", errors.New("senha ausente no stdin")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	p1, err := c.readTTY("Senha: ")
	if err != nil {
		return "", err
	}
	p2, err := c.readTTY("Repita a senha: ")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", errors.New("as senhas não conferem")
	}
	return p1, nil
}

func validUsername(u string) error {
	if n := utf8.RuneCountInString(u); n < 1 || n > 64 {
		return errors.New("usuário deve ter de 1 a 64 caracteres")
	}
	if strings.TrimSpace(u) != u || strings.ContainsAny(u, "\x00\r\n\t") {
		return errors.New("usuário não pode ter espaço nas pontas nem caractere de controle")
	}
	return nil
}

func now() time.Time { return time.Now() }

// hashNewPassword valida e gera o hash; devolve o codigo de saida se falhar.
func (c *cli) hashNewPassword(stdin bool) (string, int) {
	pw, err := c.readPassword(stdin)
	if err != nil {
		c.errf("%v", err)
		return "", exitUsage
	}
	h, err := auth.HashPassword(pw)
	if err != nil {
		c.errf("%v", err)
		return "", exitUsage
	}
	return h, exitOK
}
