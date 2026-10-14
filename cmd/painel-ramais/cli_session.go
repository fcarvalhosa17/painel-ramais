package main

import (
	"context"
	"fmt"
)

// session purge [--all]: apaga as sessoes vencidas, ou todas com --all (forca novo login).
func (c *cli) session(args []string) int {
	if len(args) == 0 || args[0] != "purge" {
		return c.usage()
	}
	fs, cfgPath := c.flagSet("session purge")
	all := fs.Bool("all", false, "apagar tambem as sessoes ainda validas")
	pos, err := parseArgs(fs, args[1:])
	if err != nil || len(pos) != 0 {
		return c.usage()
	}
	_, st, code := c.open(*cfgPath)
	if code != exitOK {
		return code
	}
	defer st.Close()
	var n int64
	if *all {
		n, err = st.DeleteAllSessions(context.Background())
	} else {
		n, err = st.PurgeSessions(context.Background(), now())
	}
	if err != nil {
		c.errf("banco: %v", err)
		return exitEnv
	}
	fmt.Fprintf(c.stdout, "%d sessão(ões) removida(s)\n", n)
	if *all && n > 0 {
		c.notify(c.stderr) // o daemon fecha os WebSockets das sessoes que sumiram
	}
	return exitOK
}
