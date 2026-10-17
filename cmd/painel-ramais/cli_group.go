package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"painel-ramais/internal/model"
	"painel-ramais/internal/store"
)

// cliSubject e o "quem" da auditoria de grupos alterados pela CLI.
const cliSubject = "cli"

func (c *cli) group(args []string) int {
	if len(args) == 0 {
		return c.usage()
	}
	sub := args[0]
	fs, cfgPath := c.flagSet("group " + sub)
	replace := fs.Bool("replace", false, "trocar todos os grupos")
	limit := fs.Int("limit", 50, "quantas linhas da auditoria mostrar (group audit)")
	pos, err := parseArgs(fs, args[1:])
	wantArgs := map[string]int{"import": 1, "list": 0, "del": 1, "audit": 0}
	n, known := wantArgs[sub]
	limitSet := false
	fs.Visit(func(f *flag.Flag) { limitSet = limitSet || f.Name == "limit" })
	if err != nil || !known || len(pos) != n || (*replace && sub != "import") || (limitSet && sub != "audit") {
		return c.usage()
	}
	if *limit < 1 || *limit > 1000 {
		c.errf("--limit deve ficar entre 1 e 1000")
		return exitUsage
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
	case "audit":
		code = c.groupAudit(st, *limit)
	default:
		code = c.groupDel(st, pos[0])
	}
	if code == exitOK && sub != "list" && sub != "audit" {
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
	if err := st.ImportGroups(context.Background(), cliSubject, rows, replace, now()); err != nil {
		if errors.Is(err, store.ErrOverlap) {
			c.errf("CSV rejeitado, nada foi gravado:\n%v", err)
			return exitUsage
		}
		c.errf("banco: %v", err)
		return exitEnv
	}
	fmt.Fprintf(c.stdout, "%d linha(s) importada(s)\n", len(rows))
	return exitOK
}

// groupList imprime "nome<TAB>faixas" e, quando o grupo tem ramais atribuidos a mao, uma terceira
// coluna com esses numeros. "Sem grupo" so aparece se houver ramal fixado nele a mao (sem faixas).
func (c *cli) groupList(st *store.Store) int {
	data, err := st.LoadGroupData(context.Background())
	if err != nil {
		c.errf("banco: %v", err)
		return exitEnv
	}
	manual := map[string][]string{} // grupo -> numeros atribuidos a mao
	for number, g := range data.Manual {
		manual[g] = append(manual[g], number)
	}
	line := func(name string, ranges []store.Range) {
		parts := make([]string, len(ranges))
		for i, r := range ranges {
			parts[i] = strconv.FormatInt(r.Start, 10)
			if r.End != r.Start {
				parts[i] += "-" + strconv.FormatInt(r.End, 10)
			}
		}
		out := printable(name) + "\t" + strings.Join(parts, ",")
		if nums := manual[name]; len(nums) > 0 {
			sort.Slice(nums, func(i, j int) bool {
				if len(nums[i]) != len(nums[j]) {
					return len(nums[i]) < len(nums[j])
				}
				return nums[i] < nums[j]
			})
			out += "\t" + printable(strings.Join(nums, ","))
		}
		fmt.Fprintln(c.stdout, out)
	}
	for _, g := range data.Groups {
		line(g.Name, g.Ranges)
	}
	if len(manual[model.NoGroup]) > 0 {
		line(model.NoGroup, nil)
	}
	return exitOK
}

// groupAudit mostra as ultimas linhas de group_audit (mais nova primeiro): data, quem, acao, detalhe.
func (c *cli) groupAudit(st *store.Store, limit int) int {
	rows, err := st.GroupAudit(context.Background(), limit)
	if err != nil {
		c.errf("banco: %v", err)
		return exitEnv
	}
	for _, r := range rows {
		fmt.Fprintf(c.stdout, "%s\t%s\t%s\t%s\n", r.At.Format(time.RFC3339), printable(r.Subject), printable(r.Action), printable(r.Detail))
	}
	return exitOK
}

// printable escapa como \uXXXX (ou \UXXXXXXXX, ou \xNN para byte invalido) as runas que enganam o
// terminal: controle (Cc: C0, DEL e C1), formatacao (Cf: bidi, largura zero) e separador de linha
// ou paragrafo (Zl, Zp). Nome de grupo e ramal vem da tela ou de CSV; subject vem de sessao.
func printable(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == utf8.RuneError && s[i] != 0xEF: // byte invalido (U+FFFD legitimo comeca por 0xEF)
			fmt.Fprintf(&b, "\\x%02X", s[i])
		case unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp):
			if r > 0xFFFF {
				fmt.Fprintf(&b, "\\U%08X", r)
			} else {
				fmt.Fprintf(&b, "\\u%04X", r)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (c *cli) groupDel(st *store.Store, name string) int {
	switch err := st.DeleteGroup(context.Background(), cliSubject, name, now()); {
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
