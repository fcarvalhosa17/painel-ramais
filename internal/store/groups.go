package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// GroupRow e uma faixa lida do CSV; Line aponta a linha de origem nas mensagens.
type GroupRow struct {
	Line       int
	Name       string
	Start, End int64
}

// Range e uma faixa gravada.
type Range struct{ Start, End int64 }

// Group e um grupo com suas faixas.
type Group struct {
	Name   string
	Ranges []Range
}

// RangeRow e uma faixa com o nome do grupo (para o daemon montar a busca por ramal).
type RangeRow struct {
	Start, End int64
	Name       string
}

// ImportGroups grava as linhas de uma vez (tudo ou nada). Com replace, apaga
// todos os grupos antes, na mesma transacao, e checa sobreposicao so dentro do
// arquivo (ja feita por ParseGroupsCSV). Sem replace, tambem checa o que existe.
func (s *Store) ImportGroups(ctx context.Context, rows []GroupRow, replace bool, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if replace {
		if _, err := tx.ExecContext(ctx, "DELETE FROM groups"); err != nil {
			return err
		}
	} else if err := checkExisting(ctx, tx, rows); err != nil {
		return err
	}
	for _, r := range rows {
		var gid int64
		if err := tx.QueryRowContext(ctx,
			"INSERT INTO groups(name, created_at) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET name = name RETURNING id",
			r.Name, now.Unix()).Scan(&gid); err != nil {
			return fmt.Errorf("linha %d: grupo %q: %w", r.Line, r.Name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO group_ranges(group_id, range_start, range_end) VALUES(?,?,?)",
			gid, r.Start, r.End); err != nil {
			if strings.Contains(err.Error(), "faixa sobreposta") {
				return fmt.Errorf("linha %d: %w", r.Line, ErrOverlap)
			}
			return fmt.Errorf("linha %d: %w", r.Line, err)
		}
	}
	return tx.Commit()
}

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
	var msgs []string
	for _, r := range rows {
		for _, h := range have {
			if r.Start <= h.End && h.Start <= r.End {
				msgs = append(msgs, fmt.Sprintf("linha %d: %d-%d sobrepoe %d-%d do grupo %q ja gravado", r.Line, r.Start, r.End, h.Start, h.End, h.Name))
			}
		}
	}
	if len(msgs) > 0 {
		return fmt.Errorf("%w:\n%s", ErrOverlap, strings.Join(limit(msgs, 20), "\n"))
	}
	return nil
}

func limit(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return append(in[:n:n], fmt.Sprintf("... e mais %d", len(in)-n))
}

// ListGroups devolve os grupos ordenados por nome, com faixas ordenadas.
func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.GroupRanges(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]*Group{}
	var out []*Group
	g, err := s.db.QueryContext(ctx, "SELECT name FROM groups")
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
func (s *Store) GroupRanges(ctx context.Context) ([]RangeRow, error) {
	rs, err := s.db.QueryContext(ctx,
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

// DeleteGroup remove o grupo (e as faixas, por cascade); ErrNotFound se nao existe.
func (s *Store) DeleteGroup(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM groups WHERE name = ?", name)
	return affected(res, err)
}
