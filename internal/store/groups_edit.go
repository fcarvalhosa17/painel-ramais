package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limites de entrada das edicoes de grupo (API e CLI).
const (
	MaxGroupNameRunes = 64    // igual ao CHECK de groups.name
	MaxNumberRunes    = 64    // igual ao CHECK de extension_groups.number
	MaxGroupRanges    = 1000  // faixas por grupo: criacao, edicao e importacao (M2)
	MaxImportRows     = 5000  // linhas de dados por importacao de CSV (M2)
	MaxAssignNumbers  = 20000 // ramais por atribuicao em massa
	maxRangeValue     = 999_999_999_999_999_999
	auditSample       = 20 // numeros/faixas guardados por linha de auditoria; o resto vira so a contagem
)

// invalidError e uma entrada recusada: a mensagem e segura e vai ao cliente como esta.
type invalidError struct{ msg string }

func (e *invalidError) Error() string        { return e.msg }
func (e *invalidError) Is(target error) bool { return target == ErrInvalid }

func invalidf(format string, a ...any) error { return &invalidError{fmt.Sprintf(format, a...)} }

// AllGroupsName e o nome que a tela reserva para a visao de todos os ramais (D17 do desvios.md); a
// partir da rc.8 o daemon recusa grupo novo com esse nome (CONTRATOS 9). Grupo antigo com o nome continua.
const AllGroupsName = "Todos os ramais"

// validLabel confere o texto de um rotulo dado por pessoa (nome de grupo, nome de fila): 1 a 64
// caracteres, sem controle/formatacao invisivel, sem espaco que nao seja o U+0020 (NBSP, U+3000...) e sem
// espaco nas pontas. what entra na mensagem ("nome do grupo", "nome da fila").
func validLabel(name, what string) error {
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxGroupNameRunes {
		return invalidf("%s deve ter de 1 a %d caracteres", what, MaxGroupNameRunes)
	}
	if strings.ContainsFunc(name, badNameRune) {
		return invalidf("%s com caractere de controle ou de formatacao invisivel", what)
	}
	if strings.ContainsFunc(name, func(r rune) bool { return r != ' ' && unicode.Is(unicode.Zs, r) }) {
		return invalidf("%s so aceita o espaco comum (U+0020), nao NBSP, U+3000 ou outros espacos", what)
	}
	if name != strings.TrimSpace(name) {
		return invalidf("%s nao pode ter espaco nas pontas", what)
	}
	return nil
}

// ValidGroupName e a regra unica de nome de grupo (CSV, tela e CLI): validLabel mais os nomes
// reservados "Sem grupo" e "Todos os ramais" (sem diferenciar maiusculas). Vale para o nome NOVO:
// update e delete acham o grupo atual sem validar o nome dele.
func ValidGroupName(name string) error {
	if err := validLabel(name, "nome do grupo"); err != nil {
		return err
	}
	if strings.EqualFold(name, noGroupName) {
		return invalidf("%q e nome reservado", noGroupName)
	}
	if strings.EqualFold(name, AllGroupsName) {
		return invalidf("%q e nome reservado", AllGroupsName)
	}
	return nil
}

// ValidNumber e a regra de numero de ramal nas atribuicoes: 1 a 64 caracteres, sem controle/formatacao.
func ValidNumber(number string) error {
	if n := utf8.RuneCountInString(number); n < 1 || n > MaxNumberRunes {
		return invalidf("ramal deve ter de 1 a %d caracteres", MaxNumberRunes)
	}
	if strings.ContainsFunc(number, badNameRune) {
		return invalidf("ramal %q com caractere de controle ou de formatacao invisivel", number)
	}
	return nil
}

// normalizeRanges valida e devolve as faixas ordenadas por inicio. Sobreposicao entre as
// proprias faixas do grupo e entrada invalida; faixas de grupos diferentes podem se sobrepor (ADR-006).
func normalizeRanges(in []Range) ([]Range, error) {
	if len(in) > MaxGroupRanges {
		return nil, invalidf("no maximo %d faixas por grupo", MaxGroupRanges)
	}
	rs := append([]Range(nil), in...)
	for _, r := range rs {
		if r.Start < 0 || r.End < r.Start || r.End > maxRangeValue {
			return nil, invalidf("faixa %d-%d invalida (inicio >= 0, fim >= inicio, ate 18 digitos)", r.Start, r.End)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Start < rs[j].Start })
	for i := 1; i < len(rs); i++ {
		if rs[i].Start <= rs[i-1].End {
			return nil, invalidf("faixas %d-%d e %d-%d se sobrepoem", rs[i-1].Start, rs[i-1].End, rs[i].Start, rs[i].End)
		}
	}
	return rs, nil
}

func rangeStrings(rs []Range) []string {
	out := make([]string, 0, min(len(rs), auditSample))
	for _, r := range rs[:min(len(rs), auditSample)] {
		out = append(out, strconv.FormatInt(r.Start, 10)+"-"+strconv.FormatInt(r.End, 10))
	}
	return out
}

// auditTx grava a linha de auditoria na transacao do chamador: sem ela, sem alteracao.
func auditTx(ctx context.Context, tx *sql.Tx, now time.Time, subject, action string, detail map[string]any) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO group_audit(at, subject, action, detail) VALUES(?,?,?,?)",
		now.Unix(), subject, action, string(b))
	return err
}

// findGroup acha o grupo pelo nome (sem diferenciar maiusculas) e devolve o id e o nome gravado.
func findGroup(ctx context.Context, tx *sql.Tx, name string) (int64, string, error) {
	var id int64
	var stored string
	err := tx.QueryRowContext(ctx, "SELECT id, name FROM groups WHERE name = ?", name).Scan(&id, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	return id, stored, err
}

func insertRanges(ctx context.Context, tx *sql.Tx, gid int64, rs []Range) error {
	for _, r := range rs {
		if _, err := tx.ExecContext(ctx, "INSERT INTO group_ranges(group_id, range_start, range_end) VALUES(?,?,?)",
			gid, r.Start, r.End); err != nil {
			if strings.Contains(err.Error(), "faixa sobreposta") { // rede de seguranca: o trigger do banco
				return fmt.Errorf("%w: %d-%d", ErrOverlap, r.Start, r.End)
			}
			return err
		}
	}
	return nil
}

// uniqueToExists traduz a violacao de UNIQUE do nome (corrida entre dois pedidos) em ErrExists.
func uniqueToExists(err error, name string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("%w: %q", ErrExists, name)
	}
	return err
}

// CreateGroup cria o grupo e suas faixas (opcionais) numa transacao, com a auditoria.
// ErrExists se o nome ja existe (sem diferenciar maiusculas). Faixa que cruza a de OUTRO grupo e aceita.
func (s *Store) CreateGroup(ctx context.Context, subject, name string, ranges []Range, now time.Time) error {
	if err := ValidGroupName(name); err != nil {
		return err
	}
	rs, err := normalizeRanges(ranges)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	switch _, _, err := findGroup(ctx, tx, name); {
	case err == nil:
		return fmt.Errorf("%w: %q", ErrExists, name)
	case !errors.Is(err, ErrNotFound):
		return err
	}
	var gid int64
	if err := tx.QueryRowContext(ctx, "INSERT INTO groups(name, created_at) VALUES(?,?) RETURNING id", name, now.Unix()).Scan(&gid); err != nil {
		return uniqueToExists(err, name)
	}
	if err := insertRanges(ctx, tx, gid, rs); err != nil {
		return err
	}
	if err := auditTx(ctx, tx, now, subject, "create", map[string]any{
		"name": name, "ranges": len(rs), "sample": rangeStrings(rs)}); err != nil {
		return err
	}
	return tx.Commit()
}

// GroupEdit e o que UpdateGroup muda: nome novo e/ou a lista COMPLETA de faixas (substitui as atuais).
type GroupEdit struct {
	NewName *string
	Ranges  *[]Range
}

// UpdateGroup renomeia e/ou troca as faixas do grupo numa transacao so, com a auditoria; devolve
// o nome final. Renomear leva o historico junto (history.group_name). ErrNotFound, ErrExists.
func (s *Store) UpdateGroup(ctx context.Context, subject, name string, ed GroupEdit, now time.Time) (string, error) {
	if ed.NewName == nil && ed.Ranges == nil {
		return "", invalidf("informe new_name ou ranges")
	}
	if ed.NewName != nil {
		if err := ValidGroupName(*ed.NewName); err != nil {
			return "", err
		}
	}
	var rs []Range
	if ed.Ranges != nil {
		var err error
		if rs, err = normalizeRanges(*ed.Ranges); err != nil {
			return "", err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	gid, final, err := findGroup(ctx, tx, name)
	if err != nil {
		return "", err
	}
	if ed.NewName != nil && *ed.NewName != final {
		newName := *ed.NewName
		switch other, _, err := findGroup(ctx, tx, newName); {
		case err == nil && other != gid:
			return "", fmt.Errorf("%w: %q", ErrExists, newName)
		case err != nil && !errors.Is(err, ErrNotFound):
			return "", err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE groups SET name = ? WHERE id = ?", newName, gid); err != nil {
			return "", uniqueToExists(err, newName)
		}
		if err := renameHistory(ctx, tx, final, newName); err != nil {
			return "", err
		}
		if err := auditTx(ctx, tx, now, subject, "rename", map[string]any{"from": final, "to": newName}); err != nil {
			return "", err
		}
		final = newName
	}
	if ed.Ranges != nil {
		have, err := rangesOf(ctx, tx, gid)
		if err != nil {
			return "", err
		}
		if !sameRanges(have, rs) {
			// O trigger group_ranges_immutable barra UPDATE; DELETE + INSERT na mesma transacao.
			if _, err := tx.ExecContext(ctx, "DELETE FROM group_ranges WHERE group_id = ?", gid); err != nil {
				return "", err
			}
			if err := insertRanges(ctx, tx, gid, rs); err != nil {
				return "", err
			}
			if err := auditTx(ctx, tx, now, subject, "ranges", map[string]any{
				"name": final, "ranges": len(rs), "before": len(have), "sample": rangeStrings(rs)}); err != nil {
				return "", err
			}
		}
	}
	return final, tx.Commit()
}

func rangesOf(ctx context.Context, tx *sql.Tx, gid int64) ([]Range, error) {
	rows, err := tx.QueryContext(ctx, "SELECT range_start, range_end FROM group_ranges WHERE group_id = ? ORDER BY range_start", gid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Range
	for rows.Next() {
		var r Range
		if err := rows.Scan(&r.Start, &r.End); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func sameRanges(a, b []Range) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// renameHistory passa o historico de from para to sem perder minuto: se to ja tem linha no mesmo
// minuto (historico de um grupo apagado que usava esse nome), soma onde os dois tem dado, fica com o
// que tem dado onde so um tem, e so entao move o resto. history tem PK (group_name, minute).
func renameHistory(ctx context.Context, tx *sql.Tx, from, to string) error {
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE history AS d SET registered = d.registered + o.registered, unregistered = d.unregistered + o.unregistered,
			unreachable = d.unreachable + o.unreachable, in_call = d.in_call + o.in_call,
			lost_registration = CASE WHEN d.lost_registration IS NULL AND o.lost_registration IS NULL THEN NULL
				ELSE COALESCE(d.lost_registration, 0) + COALESCE(o.lost_registration, 0) END,
			became_unreachable = CASE WHEN d.became_unreachable IS NULL AND o.became_unreachable IS NULL THEN NULL
				ELSE COALESCE(d.became_unreachable, 0) + COALESCE(o.became_unreachable, 0) END
			FROM history AS o WHERE d.group_name = ? AND o.group_name = ? AND d.minute = o.minute AND d.gap = 0 AND o.gap = 0`,
			[]any{to, from}},
		{`UPDATE history AS d SET registered = o.registered, unregistered = o.unregistered,
			unreachable = o.unreachable, in_call = o.in_call, gap = 0,
			lost_registration = o.lost_registration, became_unreachable = o.became_unreachable
			FROM history AS o WHERE d.group_name = ? AND o.group_name = ? AND d.minute = o.minute AND d.gap = 1 AND o.gap = 0`,
			[]any{to, from}},
		{`DELETE FROM history WHERE group_name = ? AND minute IN (SELECT minute FROM history WHERE group_name = ?)`,
			[]any{from, to}},
		{`UPDATE history SET group_name = ? WHERE group_name = ?`, []any{to, from}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return fmt.Errorf("renomear historico: %w", err)
		}
	}
	return nil
}

// DeleteGroup remove o grupo (faixas e vinculos manuais dele saem por cascade: os ramais voltam
// para a faixa ou para "Sem grupo") com a auditoria; ErrNotFound se nao existe.
func (s *Store) DeleteGroup(ctx context.Context, subject, name string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	gid, stored, err := findGroup(ctx, tx, name)
	if err != nil {
		return err
	}
	var nRanges, nManual int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM group_ranges WHERE group_id = ?),
		(SELECT count(*) FROM extension_groups WHERE group_id = ?)`, gid, gid).Scan(&nRanges, &nManual); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM groups WHERE id = ?", gid); err != nil {
		return err
	}
	if err := auditTx(ctx, tx, now, subject, "delete", map[string]any{"name": stored, "ranges": nRanges, "manual": nManual}); err != nil {
		return err
	}
	return tx.Commit()
}

// MaxAssignGroups e MaxAssignPairs limitam uma atribuicao em massa (CONTRATOS 4.6).
const (
	MaxAssignGroups = 20
	MaxAssignPairs  = 100_000 // ramais x grupos
)

// validNumbers confere a lista de ramais de uma atribuicao: de 1 a MaxAssignNumbers, cada um valido, sem repeticao.
func validNumbers(numbers []string) error {
	if len(numbers) < 1 || len(numbers) > MaxAssignNumbers {
		return invalidf("numbers deve ter de 1 a %d ramais", MaxAssignNumbers)
	}
	seen := make(map[string]struct{}, len(numbers))
	for _, n := range numbers {
		if err := ValidNumber(n); err != nil {
			return err
		}
		if _, dup := seen[n]; dup {
			return invalidf("ramal %q repetido em numbers", n)
		}
		seen[n] = struct{}{}
	}
	return nil
}

// validAssignGroup confere o nome de um grupo numa atribuicao: 1 a 64 caracteres e diferente de "Sem grupo"
// (que nao e grupo: e onde fica o ramal sem nenhum vinculo). Grupo inexistente e ErrNotFound (404), la na transacao.
func validAssignGroup(name string) error {
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxGroupNameRunes {
		return invalidf("nome do grupo deve ter de 1 a %d caracteres", MaxGroupNameRunes)
	}
	if strings.EqualFold(name, noGroupName) {
		return invalidf("%s não é grupo: é onde fica o ramal sem nenhum vínculo", noGroupName)
	}
	return nil
}

// AddToGroups cria o vinculo MANUAL de cada ramal com cada grupo, numa transacao so (tudo ou nada).
// So acrescenta: o par que ja existe nao conta e nao e regravado. O ramal que ja esta no grupo pela
// faixa recebe o vinculo mesmo assim (passa a valer se a faixa mudar). Devolve os pares gravados agora;
// zero nao grava nem audita. ErrNotFound se algum grupo nao existe (nada e aplicado).
func (s *Store) AddToGroups(ctx context.Context, subject string, numbers, groups []string, now time.Time) (int, error) {
	if err := validNumbers(numbers); err != nil {
		return 0, err
	}
	if len(groups) < 1 || len(groups) > MaxAssignGroups {
		return 0, invalidf("groups deve ter de 1 a %d grupos", MaxAssignGroups)
	}
	if len(numbers)*len(groups) > MaxAssignPairs {
		return 0, invalidf("ramais x grupos acima de %d por pedido", MaxAssignPairs)
	}
	seen := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		if err := validAssignGroup(g); err != nil {
			return 0, err
		}
		if _, dup := seen[nameKey(g)]; dup {
			return 0, invalidf("grupo %q repetido em groups", g)
		}
		seen[nameKey(g)] = struct{}{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	ids := make([]int64, len(groups))
	stored := make([]string, len(groups))
	for i, g := range groups {
		if ids[i], stored[i], err = findGroup(ctx, tx, g); err != nil {
			return 0, err
		}
	}
	stmt, err := tx.PrepareContext(ctx, insertManualSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	changed := 0
	sample := make([]string, 0, auditSample)
	for _, n := range numbers {
		added := false
		for _, gid := range ids {
			res, err := stmt.ExecContext(ctx, n, gid, now.Unix(), subject)
			if err != nil {
				return 0, err
			}
			if k, _ := res.RowsAffected(); k > 0 {
				changed++
				added = true
			}
		}
		if added && len(sample) < auditSample {
			sample = append(sample, n)
		}
	}
	if changed == 0 {
		return 0, nil
	}
	if err := auditTx(ctx, tx, now, subject, "assign", map[string]any{
		"mode": "add", "groups": stored, "requested": len(numbers), "changed": changed, "numbers": sample}); err != nil {
		return 0, err
	}
	return changed, tx.Commit()
}

// RemoveFromGroup apaga o vinculo MANUAL de cada ramal com o grupo, numa transacao so. Ramal sem
// vinculo manual com o grupo (esta la pela faixa, ou nem esta) nao muda nada e conta em notManual:
// nao existe exclusao de faixa. Zero alterados nao grava nem audita. ErrNotFound se o grupo nao existe.
func (s *Store) RemoveFromGroup(ctx context.Context, subject string, numbers []string, group string, now time.Time) (changed, notManual int, err error) {
	if err := validNumbers(numbers); err != nil {
		return 0, 0, err
	}
	if err := validAssignGroup(group); err != nil {
		return 0, 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	gid, stored, err := findGroup(ctx, tx, group)
	if err != nil {
		return 0, 0, err
	}
	stmt, err := tx.PrepareContext(ctx, "DELETE FROM extension_groups WHERE number = ? AND group_id = ?")
	if err != nil {
		return 0, 0, err
	}
	defer stmt.Close()
	sample := make([]string, 0, auditSample)
	for _, n := range numbers {
		res, err := stmt.ExecContext(ctx, n, gid)
		if err != nil {
			return 0, 0, err
		}
		if k, _ := res.RowsAffected(); k > 0 {
			changed++
			if len(sample) < auditSample {
				sample = append(sample, n)
			}
		} else {
			notManual++
		}
	}
	if changed == 0 {
		return 0, notManual, nil
	}
	if err := auditTx(ctx, tx, now, subject, "assign", map[string]any{
		"mode": "remove", "groups": []string{stored}, "requested": len(numbers), "changed": changed, "numbers": sample}); err != nil {
		return 0, 0, err
	}
	return changed, notManual, tx.Commit()
}

// AuditRow e uma linha de group_audit.
type AuditRow struct {
	ID      int64
	At      time.Time
	Subject string
	Action  string
	Detail  string // JSON curto
}

// GroupAudit devolve as ultimas n linhas da auditoria de grupos, a mais nova primeiro.
func (s *Store) GroupAudit(ctx context.Context, n int) ([]AuditRow, error) {
	rs, err := s.db.QueryContext(ctx, "SELECT id, at, subject, action, detail FROM group_audit ORDER BY id DESC LIMIT ?", n)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []AuditRow
	for rs.Next() {
		var r AuditRow
		var at int64
		if err := rs.Scan(&r.ID, &at, &r.Subject, &r.Action, &r.Detail); err != nil {
			return nil, err
		}
		r.At = time.Unix(at, 0).UTC()
		out = append(out, r)
	}
	return out, rs.Err()
}

// PurgeGroupAudit apaga a auditoria anterior a before (mesma retencao do historico) e devolve quantas.
func (s *Store) PurgeGroupAudit(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM group_audit WHERE at < ?", before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
