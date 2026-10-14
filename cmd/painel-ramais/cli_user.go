package main

import (
	"context"
	"errors"

	"painel-ramais/internal/store"
)

func (c *cli) user(args []string) int {
	if len(args) == 0 {
		return c.usage()
	}
	sub := args[0]
	fs, cfgPath := c.flagSet("user " + sub)
	stdin := fs.Bool("password-stdin", false, "ler a senha de uma linha do stdin")
	pos, err := parseArgs(fs, args[1:])
	if err != nil || len(pos) != 1 || (sub != "add" && sub != "passwd" && sub != "del") || (sub == "del" && *stdin) {
		return c.usage()
	}
	if err := validUsername(pos[0]); err != nil {
		c.errf("%v", err)
		return exitUsage
	}
	_, st, code := c.open(*cfgPath)
	if code != exitOK {
		return code
	}
	defer st.Close()
	code = c.userOp(st, sub, pos[0], *stdin)
	if code == exitOK {
		c.notify(c.stderr)
	}
	return code
}

func (c *cli) userOp(st *store.Store, sub, name string, stdin bool) int {
	ctx := context.Background()
	switch sub {
	case "add":
		if _, err := st.UserByName(ctx, name); err == nil {
			c.errf("usuário %q já existe; nada foi alterado (use: user passwd)", name)
			return exitExists
		}
		h, code := c.hashNewPassword(stdin)
		if code != exitOK {
			return code
		}
		if err := st.CreateUser(ctx, name, h, now()); err != nil {
			if errors.Is(err, store.ErrUserExists) {
				c.errf("usuário %q já existe; nada foi alterado", name)
				return exitExists
			}
			c.errf("banco: %v", err)
			return exitEnv
		}
	case "passwd":
		h, code := c.hashNewPassword(stdin)
		if code != exitOK {
			return code
		}
		return c.userResult(st.SetPassword(ctx, name, h, now()), name)
	case "del":
		return c.userResult(st.DeleteUser(ctx, name), name)
	}
	return exitOK
}

func (c *cli) userResult(err error, name string) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, store.ErrNotFound):
		c.errf("usuário %q não existe", name)
		return exitUsage
	}
	c.errf("banco: %v", err)
	return exitEnv
}
