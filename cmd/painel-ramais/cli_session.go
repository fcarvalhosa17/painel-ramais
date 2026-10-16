package main

import (
	"context"
	"flag"
	"fmt"

	"painel-ramais/internal/auth"
)

// session purge [--all | --issabel <usuario>]: apaga as sessoes vencidas; --all apaga todas
// (forca novo login); --issabel apaga as de token issabel:admin:<usuario> e issabel:user:<usuario>.
func (c *cli) session(args []string) int {
	if len(args) == 0 || args[0] != "purge" {
		return c.usage()
	}
	fs, cfgPath := c.flagSet("session purge")
	all := fs.Bool("all", false, "apagar tambem as sessoes ainda validas")
	issabel := fs.String("issabel", "", "apagar as sessoes do usuario do Issabel")
	pos, err := parseArgs(fs, args[1:])
	if err != nil || len(pos) != 0 {
		return c.usage()
	}
	byUser := false
	fs.Visit(func(f *flag.Flag) { byUser = byUser || f.Name == "issabel" }) // --issabel "" tambem conta (e e recusado)
	if *all && byUser {
		c.errf("--all e --issabel não podem ser usados juntos")
		return exitUsage
	}
	admin, user := auth.AdminPrefix+*issabel, auth.UserPrefix+*issabel
	if byUser && (!auth.ValidIssabelSub(admin) || !auth.ValidIssabelSub(user)) {
		c.errf("--issabel: usuário de 1 a 64 caracteres, sem caractere de controle")
		return exitUsage
	}
	_, st, code := c.open(*cfgPath)
	if code != exitOK {
		return code
	}
	defer st.Close()
	var n int64
	switch {
	case *all:
		n, err = st.DeleteAllSessions(context.Background())
	case byUser:
		n, err = st.DeleteTokenSessionsBySubject(context.Background(), admin, user)
	default:
		n, err = st.PurgeSessions(context.Background(), now())
	}
	if err != nil {
		c.errf("banco: %v", err)
		return exitEnv
	}
	fmt.Fprintf(c.stdout, "%d sessão(ões) removida(s)\n", n)
	if (*all || byUser) && n > 0 {
		c.notify(c.stderr) // o daemon fecha os WebSockets das sessoes que sumiram
	}
	return exitOK
}
