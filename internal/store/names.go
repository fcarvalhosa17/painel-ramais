package store

import (
	"context"
	"time"
)

const maxNameLen = 128

// UpsertNames grava (ou atualiza) o cache de nomes numa transacao so. Nome vazio
// ou longo demais e ignorado: o cache e so otimizacao e nao pode quebrar a gravacao.
func (s *Store) UpsertNames(ctx context.Context, names map[string]string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO extension_names(number, name, updated_at) VALUES(?,?,?)
		ON CONFLICT(number) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`)
	if err != nil {
		return err
	}
	defer st.Close()
	for number, name := range names {
		if number == "" || len(number) > 64 || name == "" || len(name) > maxNameLen {
			continue
		}
		if _, err := st.ExecContext(ctx, number, name, now.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LoadNames devolve o cache inteiro (numero -> nome).
func (s *Store) LoadNames(ctx context.Context) (map[string]string, error) {
	rs, err := s.db.QueryContext(ctx, "SELECT number, name FROM extension_names")
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := map[string]string{}
	for rs.Next() {
		var n, name string
		if err := rs.Scan(&n, &name); err != nil {
			return nil, err
		}
		out[n] = name
	}
	return out, rs.Err()
}

// PruneNames apaga do cache os ramais que nao estao em keep (ramal removido do PBX). Lista
// vazia nao apaga nada: sincronizacao sem ramais nao pode esvaziar o cache.
func (s *Store) PruneNames(ctx context.Context, keep []string) (int64, error) {
	if len(keep) == 0 {
		return 0, nil
	}
	have, err := s.LoadNames(ctx)
	if err != nil {
		return 0, err
	}
	live := make(map[string]struct{}, len(keep))
	for _, n := range keep {
		live[n] = struct{}{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var removed int64
	for n := range have {
		if _, ok := live[n]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM extension_names WHERE number = ?", n); err != nil {
			return 0, err
		}
		removed++
	}
	return removed, tx.Commit()
}
