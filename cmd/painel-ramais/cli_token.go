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
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 || *sub == "" {
		return c.usage()
	}
	if *ttl <= 0 || *ttl > auth.MaxTokenTTL {
		c.errf("--ttl deve ficar entre 1ns e 60s")
		return exitUsage
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		c.errf("%v", err)
		return exitEnv
	}
	secret, err := cfg.AuthSecret()
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
