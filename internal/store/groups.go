package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// GroupRow e uma linha do CSV; Line aponta a linha de origem nas mensagens. Manual=false e uma
// faixa Start-End do grupo Name. Manual=true e um ramal unico (Number, o texto digitado) vinculado
// a mao ao grupo Name; Start e End valem o numero do ramal. Skip = a linha nao muda nada e sai como
// ignorada: repetida no arquivo, ou "Sem grupo,N" (arquivo antigo; "Sem grupo" e implicito).
type GroupRow struct {
	Line       int
	Name       string
	Start, End int64
	Manual     bool
	Number     string
	Skip       bool
}

// Range e uma faixa gravada.
type Range struct{ Start, End int64 }

// Group e um grupo com suas faixas (pode ter zero).
type Group struct {
	Name   string
	Ranges []Range
}

// GroupData e o catalogo completo mais os vinculos manuais, lidos numa transacao so.
// Manual mapeia numero -> nomes dos grupos onde ha vinculo manual (rc.7: N:N), ordenados por nome.
type GroupData struct {
	Groups []Group
	Manual map[string][]string
}

// RangeRow e uma faixa com o nome do grupo (para o daemon montar a busca por ramal).
type RangeRow struct {
	Start, End int64
	Name       string
}

// ImportResult resume uma importacao: Rows = linhas de dados lidas; Ignored = linhas que nao
// mudaram nada (repetidas no arquivo, faixa ou vinculo que ja existia, "Sem grupo,N").
type ImportResult struct{ Rows, Ignored int }

// ImportGroups grava as linhas de uma vez (tudo ou nada). Com replace, apaga todos os grupos
// (faixas e vinculos manuais saem por cascade) antes, na mesma transacao. Sem replace SO ACRESCENTA
// (ADR-006): nunca apaga nem troca faixa ou vinculo existente, e confere as faixas do mesmo grupo que
// ja existem (faixa identica e ignorada; faixa que cruza e erro). A auditoria vai na mesma transacao.
func (s *Store) ImportGroups(ctx context.Context, subject string, rows []GroupRow, replace bool, now time.Time) (ImportResult, error) {
	res := ImportResult{Rows: len(rows)}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	work := append([]GroupRow(nil), rows...) // skipExistingRanges marca Skip: nao mexe na lista do chamador
	if replace {
		if _, err := tx.ExecContext(ctx, "DELETE FROM groups"); err != nil { // cascade apaga faixas e vinculos
			return res, err
		}
	} else if err := skipExistingRanges(ctx, tx, work); err != nil {
		return res, err
	}
	nRanges, nManual := 0, 0
	touched := map[int64]string{} // grupos que receberam faixa nova: o teto de faixas vale depois de gravar
	for _, r := range work {
		if r.Skip {
			res.Ignored++
			continue
		}
		var gid int64
		if err := tx.QueryRowContext(ctx,
			"INSERT INTO groups(name, created_at) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET name = name RETURNING id",
			r.Name, now.Unix()).Scan(&gid); err != nil {
			return res, fmt.Errorf("linha %d: visão %q: %w", r.Line, r.Name, err)
		}
		if r.Manual {
			out, err := tx.ExecContext(ctx, insertManualSQL, r.Number, gid, now.Unix(), subject)
			if err != nil {
				return res, fmt.Errorf("linha %d: %w", r.Line, err)
			}
			if n, _ := out.RowsAffected(); n == 0 { // o vinculo ja existia
				res.Ignored++
			} else {
				nManual++
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO group_ranges(group_id, range_start, range_end) VALUES(?,?,?)",
			gid, r.Start, r.End); err != nil {
			if strings.Contains(err.Error(), "faixa sobreposta") { // rede de seguranca: o trigger do banco
				return res, fmt.Errorf("linha %d: %w", r.Line, ErrOverlap)
			}
			return res, fmt.Errorf("linha %d: %w", r.Line, err)
		}
		nRanges++
		touched[gid] = r.Name
	}
	for gid, name := range touched {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM group_ranges WHERE group_id = ?", gid).Scan(&n); err != nil {
			return res, err
		}
		if n > MaxGroupRanges {
			return res, invalidf("a visão %q ficaria com %d faixas; o máximo é %d por visão. Nada foi gravado", name, n, MaxGroupRanges)
		}
	}
	if err := auditTx(ctx, tx, now, subject, "import", map[string]any{
		"rows": len(rows), "replace": replace, "ranges": nRanges, "manual": nManual, "ignored": res.Ignored}); err != nil {
		return res, err
	}
	return res, tx.Commit()
}

// insertManualSQL grava o vinculo manual ramal -> grupo; o par que ja existe nao e regravado
// (RowsAffected 0), para o chamador contar so o que mudou de fato.
const insertManualSQL = `INSERT INTO extension_groups(number, group_id, updated_at, updated_by) VALUES(?,?,?,?)
	ON CONFLICT(number, group_id) DO NOTHING`

// skipExistingRanges confere as faixas do arquivo contra as que o MESMO grupo ja tem no banco (faixas
// de grupos diferentes podem se sobrepor, ADR-006): faixa identica a uma existente vira Skip; faixa
// que cruza outra e erro. Guarda so as primeiras mensagens e CONTA o resto: linhas x faixas pode
// dar milhoes de cruzamentos e juntar todas as mensagens esgotaria a memoria.
func skipExistingRanges(ctx context.Context, tx *sql.Tx, rows []GroupRow) error {
	have, err := groupRanges(ctx, tx)
	if err != nil {
		return err
	}
	byGroup := map[string][]RangeRow{}
	for _, h := range have {
		k := nameKey(h.Name)
		byGroup[k] = append(byGroup[k], h)
	}
	const maxMsgs = 20
	var msgs []string
	total := 0
	for i := range rows {
		r := &rows[i]
		if r.Manual || r.Skip {
			continue
		}
		for _, h := range byGroup[nameKey(r.Name)] {
			if r.Start == h.Start && r.End == h.End {
				r.Skip = true
				break
			}
			if r.Start <= h.End && h.Start <= r.End {
				total++
				if len(msgs) < maxMsgs {
					msgs = append(msgs, fmt.Sprintf("linha %d: faixa %d-%d cruza a faixa %d-%d ja gravada na visão %q", r.Line, r.Start, r.End, h.Start, h.End, h.Name))
				}
			}
		}
	}
	if total > 0 {
		if total > maxMsgs {
			msgs = append(msgs, fmt.Sprintf("... e mais %d", total-maxMsgs))
		}
		return fmt.Errorf("%w:\n%s", ErrOverlap, strings.Join(msgs, "\n"))
	}
	return nil
}

func limit(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return append(in[:n:n], fmt.Sprintf("... e mais %d", len(in)-n))
}

// queryer e o que *sql.DB e *sql.Tx tem em comum.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// ListGroups devolve os grupos ordenados por nome, com faixas ordenadas.
func (s *Store) ListGroups(ctx context.Context) ([]Group, error) { return listGroups(ctx, s.db) }

func listGroups(ctx context.Context, q queryer) ([]Group, error) {
	rows, err := groupRanges(ctx, q)
	if err != nil {
		return nil, err
	}
	byName := map[string]*Group{}
	var out []*Group
	g, err := q.QueryContext(ctx, "SELECT name FROM groups")
	if err != nil {
		return nil, err
	}
	defer g.Close()
	for g.Next() {
		x := &Group{}
		if err := g.Scan(&x.Name); err != nil {
			return nil, err
		}
		byName[x.Name] = x
		out = append(out, x)
	}
	if err := g.Err(); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if x := byName[r.Name]; x != nil {
			x.Ranges = append(x.Ranges, Range{r.Start, r.End})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	res := make([]Group, len(out))
	for i, x := range out {
		res[i] = *x
	}
	return res, nil
}

// GroupRanges devolve todas as faixas ordenadas por inicio.
func (s *Store) GroupRanges(ctx context.Context) ([]RangeRow, error) { return groupRanges(ctx, s.db) }

func groupRanges(ctx context.Context, q queryer) ([]RangeRow, error) {
	rs, err := q.QueryContext(ctx,
		"SELECT g.name, r.range_start, r.range_end FROM group_ranges r JOIN groups g ON g.id = r.group_id ORDER BY r.range_start")
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []RangeRow
	for rs.Next() {
		var x RangeRow
		if err := rs.Scan(&x.Name, &x.Start, &x.End); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rs.Err()
}

// LoadGroupData le o catalogo (inclui grupos sem faixa) e os vinculos manuais na mesma
// transacao de leitura: o daemon nunca ve faixas de um estado e manuais de outro.
func (s *Store) LoadGroupData(ctx context.Context) (GroupData, error) {
	// ReadOnly: com _txlock=immediate no DSN, leitura nao pega o lock de escrita.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return GroupData{}, err
	}
	defer tx.Rollback()
	groups, err := listGroups(ctx, tx)
	if err != nil {
		return GroupData{}, err
	}
	rs, err := tx.QueryContext(ctx, "SELECT e.number, g.name FROM extension_groups e JOIN groups g ON g.id = e.group_id ORDER BY e.number, g.name")
	if err != nil {
		return GroupData{}, err
	}
	defer rs.Close()
	manual := map[string][]string{}
	for rs.Next() {
		var number, name string
		if err := rs.Scan(&number, &name); err != nil {
			return GroupData{}, err
		}
		manual[number] = append(manual[number], name)
	}
	if err := rs.Err(); err != nil {
		return GroupData{}, err
	}
	return GroupData{Groups: groups, Manual: manual}, nil
}
