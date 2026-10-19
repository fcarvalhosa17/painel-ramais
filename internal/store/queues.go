package store

import (
	"context"
	"time"
)

// Nome da fila dado pelo painel (rc.8, ADR-009, CONTRATOS 4.8 e 10 versao 5): tabela queue_names, chave =
// identificador da fila no AMI ("600"). Sem linha = sem nome.

// ValidQueueName confere o nome de uma fila: as regras de caractere do nome de grupo (1 a 64 caracteres, sem
// controle nem formatacao invisivel, so o espaco U+0020, sem espaco nas pontas), sem nome reservado. O nome
// vazio (apagar) e tratado por quem chama, nao passa por aqui.
func ValidQueueName(name string) error { return validLabel(name, "nome da fila") }

// LoadQueueNames devolve todos os nomes (fila -> nome).
func (s *Store) LoadQueueNames(ctx context.Context) (map[string]string, error) {
	rs, err := s.db.QueryContext(ctx, "SELECT queue, name FROM queue_names")
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := map[string]string{}
	for rs.Next() {
		var q, n string
		if err := rs.Scan(&q, &n); err != nil {
			return nil, err
		}
		out[q] = n
	}
	return out, rs.Err()
}

// SetQueueName da o nome a uma fila (upsert) ou, com name == "", apaga a linha. Mesmo valor (ou apagar o que
// nao existe) nao grava: changed = false. Uma transacao so; updated_at e updated_by = quem gravou.
func (s *Store) SetQueueName(ctx context.Context, subject, queue, name string, now time.Time) (changed bool, err error) {
	if err := ValidNumber(queue); err != nil { // mesma regra do identificador: 1 a 64 caracteres sem controle
		return false, err
	}
	if name != "" {
		if err := ValidQueueName(name); err != nil {
			return false, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var n int64
	if name == "" {
		res, err := tx.ExecContext(ctx, "DELETE FROM queue_names WHERE queue = ?", queue)
		if err != nil {
			return false, err
		}
		n, _ = res.RowsAffected()
	} else {
		// O WHERE do upsert faz o RowsAffected contar so a mudanca de fato.
		res, err := tx.ExecContext(ctx, `INSERT INTO queue_names(queue, name, updated_at, updated_by) VALUES(?,?,?,?)
			ON CONFLICT(queue) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at, updated_by = excluded.updated_by
			WHERE queue_names.name IS NOT excluded.name`, queue, name, now.Unix(), subject)
		if err != nil {
			return false, err
		}
		n, _ = res.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}
