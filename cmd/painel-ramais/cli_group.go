package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"painel-ramais/internal/store"
)

func (c *cli) group(args []string) int {
	if len(args) == 0 {
		return c.usage()
	}
	sub := args[0]
	fs, cfgPath := c.flagSet("group " + sub)
	replace := fs.Bool("replace", false, "trocar todos os grupos")
	pos, err := parseArgs(fs, args[1:])
	wantArgs := map[string]int{"import": 1, "list": 0, "del": 1}
	n, known := wantArgs[sub]
	if err != nil || !known || len(pos) != n || (*replace && sub != "import") {
		return c.usage()
	}
	_, st, code := c.open(*cfgPath)
	if code != exitOK {
		return code
	}
	defer st.Close()
	switch sub {
	case "import":
		code = c.groupImport(st, pos[0], *replace)
	case "list":
		code = c.groupList(st)
	default:
		code = c.groupDel(st, pos[0])
	}
	if code == exitOK && sub != "list" {
		c.notify(c.stderr)
	}
	return code
}

func (c *cli) groupImport(st *store.Store, path string, replace bool) int {
	in := c.stdin
	if path != "-" { // "-" le o CSV do stdin
		f, err := os.Open(path)
		if err != nil {
			c.errf("abrir CSV: %v", err)
			return exitEnv
		}
		defer f.Close()
		in = f
	}
	rows, err := store.ParseGroupsCSV(in)
	if err != nil {
		c.errf("CSV rejeitado, nada foi gravado:\n%v", err)
		return exitUsage
	}
	if err := st.ImportGroups(context.Background(), rows, replace, now()); err != nil {
		if errors.Is(err, store.ErrOverlap) {
			c.errf("CSV rejeitado, nada foi gravado:\n%v", err)
			return exitUsage
		}
		c.errf("banco: %v", err)
		return exitEnv
	}
	fmt.Fprintf(c.stdout, "%d faixa(s) importada(s)\n", len(rows))
	return exitOK
}

func (c *cli) groupList(st *store.Store) int {
	groups, err := st.ListGroups(context.Background())
	if err != nil {
		c.errf("banco: %v", err)
		return exitEnv
	}
	for _, g := range groups {
		parts := make([]string, len(g.Ranges))
		for i, r := range g.Ranges {
			parts[i] = strconv.FormatInt(r.Start, 10)
			if r.End != r.Start {
				parts[i] += "-" + strconv.FormatInt(r.End, 10)
			}
		}
		fmt.Fprintf(c.stdout, "%s\t%s\n", g.Name, strings.Join(parts, ","))
	}
	return exitOK
}

func (c *cli) groupDel(st *store.Store, name string) int {
	switch err := st.DeleteGroup(context.Background(), name); {
	case err == nil:
		return exitOK
	case errors.Is(err, store.ErrNotFound):
		c.errf("grupo %q não existe", name)
		return exitUsage
	default:
		c.errf("banco: %v", err)
		return exitEnv
	}
}
