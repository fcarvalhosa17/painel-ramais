package main

import (
	"fmt"
	"time"

	"painel-ramais/internal/auth"
	"painel-ramais/internal/config"
)

func (c *cli) token(args []string) int {
	fs, cfgPath := c.flagSet("token")
	sub := fs.String("sub", "", "identificacao de quem entra (obrigatorio)")
	ttl := fs.Duration("ttl", auth.MaxTokenTTL, "validade do token (maximo 60s)")
	issabel := fs.Bool("issabel", false, "assinar com a chave do Issabel (sub issabel:admin:<usuario> ou issabel:user:<usuario>)")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 || *sub == "" {
		return c.usage()
	}
	if *ttl <= 0 || *ttl > auth.MaxTokenTTL {
		c.errf("--ttl deve ficar entre 1ns e 60s")
		return exitUsage
	}
	switch {
	case *issabel && !auth.ValidIssabelSub(*sub):
		c.errf("com --issabel o --sub deve ser issabel:admin:<usuario> ou issabel:user:<usuario> (usuário de 1 a 64 caracteres)")
		return exitUsage
	case !*issabel && auth.HasIssabelPrefix(*sub):
		c.errf("use --issabel para sub issabel:")
		return exitUsage
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		c.errf("%v", err)
		return exitEnv
	}
	var secret []byte
	if *issabel {
		secret, err = cfg.IssabelKey()
	} else {
		secret, err = cfg.AuthSecret()
	}
	if err != nil {
		c.errf("%v", err)
		return exitEnv
	}
	tok, err := auth.IssueToken(secret, *sub, *ttl, time.Now())
	if err != nil {
		c.errf("%v", err)
		return exitUsage
	}
	fmt.Fprintln(c.stdout, tok)
	return exitOK
}
