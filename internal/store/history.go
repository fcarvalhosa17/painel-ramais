package store

import (
	"context"
	"database/sql"
	"time"
)

// HistoryRow e um minuto de historico. TotalGroup ("") e o total da instalacao.
// Em gap as contagens nao valem (gravadas como NULL). LostRegistration e BecameUnreachable (v4) sao
// as ocorrencias de registro do minuto (relatorio de registro, ADR-007).
type HistoryRow struct {
	Minute                                        time.Time
	Group                                         string
	Registered, Unregistered, Unreachable, InCall int
	LostRegistration, BecameUnreachable           int
	Gap                                           bool
}

// TotalGroup e o group_name do total da instalacao.
const TotalGroup = ""

// InsertHistory grava as linhas numa transacao so; repetir o minuto substitui.
func (s *Store) InsertHistory(ctx context.Context, rows []HistoryRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO history
		(minute, group_name, registered, unregistered, unreachable, in_call, gap, lost_registration, became_unreachable)
		VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, r := range rows {
		var reg, unreg, unre, call, lost, drop any // nil = NULL (minuto de lacuna)
		gap := 1
		if !r.Gap {
			reg, unreg, unre, call, gap = r.Registered, r.Unregistered, r.Unreachable, r.InCall, 0
			lost, drop = r.LostRegistration, r.BecameUnreachable
		}
		if _, err := st.ExecContext(ctx, r.Minute.Unix(), r.Group, reg, unreg, unre, call, gap, lost, drop); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// QueryHistory devolve as linhas de group em [from, to), ordenadas. Minutos sem
// linha nao aparecem (a API preenche como gap).
func (s *Store) QueryHistory(ctx context.Context, group string, from, to time.Time) ([]HistoryRow, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT minute, registered, unregistered, unreachable, in_call, gap
		FROM history WHERE group_name = ? AND minute >= ? AND minute < ? ORDER BY minute`,
		group, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []HistoryRow
	for rs.Next() {
		var min, gap int64
		var reg, unreg, unre, call sql.NullInt64
		if err := rs.Scan(&min, &reg, &unreg, &unre, &call, &gap); err != nil {
			return nil, err
		}
		out = append(out, HistoryRow{
			Minute: time.Unix(min, 0).UTC(), Group: group, Gap: gap == 1,
			Registered: int(reg.Int64), Unregistered: int(unreg.Int64), Unreachable: int(unre.Int64), InCall: int(call.Int64),
		})
	}
	return out, rs.Err()
}

// PurgeHistory apaga os minutos anteriores a before e devolve quantos.
func (s *Store) PurgeHistory(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM history WHERE minute < ?", before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
