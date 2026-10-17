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
// faixa Start-End do grupo Name. Manual=true e um ramal unico (Number, o texto digitado) atribuido a
// mao ao grupo Name; Name == "Sem grupo" e o "Sem grupo" manual. Start e End valem o numero do ramal.
type GroupRow struct {
	Line       int
	Name       string
	Start, End int64
	Manual     bool
	Number     string
}

// Range e uma faixa gravada.
type Range struct{ Start, End int64 }

// Group e um grupo com suas faixas (pode ter zero).
type Group struct {
	Name   string
	Ranges []Range
}

// GroupData e o catalogo completo mais os vinculos manuais, lidos numa transacao so.
// Manual mapeia numero -> nome do grupo, ou "Sem grupo" quando o vinculo manual e "sem grupo".
type GroupData struct {
	Groups []Group
	Manual map[string]string
}

// RangeRow e uma faixa com o nome do grupo (para o daemon montar a busca por ramal).
type RangeRow struct {
	Start, End int64
	Name       string
}

// ImportGroups grava as linhas de uma vez (tudo ou nada). Com replace, apaga
// todos os grupos e vinculos manuais antes, na mesma transacao, e checa sobreposicao so dentro do
// arquivo (ja feita por ParseGroupsCSV). Sem replace, tambem checa as faixas que existem; um
// vinculo manual do arquivo SOBRESCREVE o manual que o ramal ja tinha. A auditoria vai na mesma transacao.
func (s *Store) ImportGroups(ctx context.Context, subject string, rows []GroupRow, replace bool, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if replace {
		if _, err := tx.ExecContext(ctx, "DELETE FROM groups"); err != nil { // cascade apaga os manuais com grupo
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM extension_groups"); err != nil { // e os "Sem grupo" manuais
			return err
		}
	} else if err := checkExisting(ctx, tx, rows); err != nil {
		return err
	}
	nManual := 0
	for _, r := range rows {
		var gid any // nil = NULL = "Sem grupo" manual
		if !r.Manual || !strings.EqualFold(r.Name, noGroupName) {
			var id int64
			if err := tx.QueryRowContext(ctx,
				"INSERT INTO groups(name, created_at) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET name = name RETURNING id",
				r.Name, now.Unix()).Scan(&id); err != nil {
				return fmt.Errorf("linha %d: grupo %q: %w", r.Line, r.Name, err)
			}
			gid = id
		}
		if r.Manual {
			nManual++
			if _, err := tx.ExecContext(ctx, upsertManualSQL, r.Number, gid, now.Unix(), subject); err != nil {
				return fmt.Errorf("linha %d: %w", r.Line, err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO group_ranges(group_id, range_start, range_end) VALUES(?,?,?)",
			gid, r.Start, r.End); err != nil {
			if strings.Contains(err.Error(), "faixa sobreposta") {
				return fmt.Errorf("linha %d: %w", r.Line, ErrOverlap)
			}
			return fmt.Errorf("linha %d: %w", r.Line, err)
		}
	}
	if err := auditTx(ctx, tx, now, subject, "import", map[string]any{
		"rows": len(rows), "replace": replace, "ranges": len(rows) - nManual, "manual": nManual}); err != nil {
		return err
	}
	return tx.Commit()
}

// upsertManualSQL grava (ou troca) o vinculo manual de um ramal.
const upsertManualSQL = `INSERT INTO extension_groups(number, group_id, updated_at, updated_by) VALUES(?,?,?,?)
	ON CONFLICT(number) DO UPDATE SET group_id = excluded.group_id, updated_at = excluded.updated_at, updated_by = excluded.updated_by`

func checkExisting(ctx context.Context, tx *sql.Tx, rows []GroupRow) error {
	rs, err := tx.QueryContext(ctx, "SELECT g.name, r.range_start, r.range_end FROM group_ranges r JOIN groups g ON g.id = r.group_id")
	if err != nil {
		return err
	}
	var have []RangeRow
	for rs.Next() {
		var x RangeRow
		if err := rs.Scan(&x.Name, &x.Start, &x.End); err != nil {
			rs.Close()
			return err
		}
		have = append(have, x)
	}
	if err := rs.Err(); err != nil {
		return err
	}
	rs.Close()
	// Guarda so as primeiras mensagens e CONTA o resto: linhas x faixas pode dar milhoes de
	// sobreposicoes e juntar todas as mensagens esgotaria a memoria.
	const maxMsgs = 20
	var msgs []string
	total := 0
	for _, r := range rows {
		if r.Manual { // vinculo manual vence a faixa: nao conflita com ela
			continue
		}
		for _, h := range have {
			if r.Start <= h.End && h.Start <= r.End {
				total++
				if len(msgs) < maxMsgs {
					msgs = append(msgs, fmt.Sprintf("linha %d: %d-%d sobrepoe %d-%d do grupo %q ja gravado", r.Line, r.Start, r.End, h.Start, h.End, h.Name))
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
	rs, err := tx.QueryContext(ctx, "SELECT e.number, g.name FROM extension_groups e LEFT JOIN groups g ON g.id = e.group_id")
	if err != nil {
		return GroupData{}, err
	}
	defer rs.Close()
	manual := map[string]string{}
	for rs.Next() {
		var number string
		var name sql.NullString
		if err := rs.Scan(&number, &name); err != nil {
			return GroupData{}, err
		}
		if name.Valid {
			manual[number] = name.String
		} else {
			manual[number] = noGroupName
		}
	}
	if err := rs.Err(); err != nil {
		return GroupData{}, err
	}
	return GroupData{Groups: groups, Manual: manual}, nil
}
