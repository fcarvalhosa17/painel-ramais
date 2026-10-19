package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Relatorio de registro (ADR-007, CONTRATOS 4.7 e 10, versao 4 do esquema).

// regMaxDownHours: o fechamento de um intervalo fora do ar distribui os segundos por, no maximo, as
// ultimas 720 horas (30 dias, a retencao).
const regMaxDownHours = 720

// RegEvent e uma ocorrencia de registro a gravar. IP e Port so quando ha endereco IPv4.
type RegEvent struct {
	At        time.Time
	Number    string
	Name      string
	Kind      string
	System    bool
	IP        string
	Port      int
	UserAgent string
}

// RegDown e um intervalo fora do ar que fechou ([From, To)).
type RegDown struct {
	Number   string
	Name     string
	From, To time.Time
}

// RegSignal e um sinal do sistema (shutdown, fully_booted, ami_connected).
type RegSignal struct {
	At   time.Time
	Kind string
}

// RegBatch e o que o gravador junta antes de gravar numa transacao so.
type RegBatch struct {
	Events  []RegEvent
	Downs   []RegDown
	Signals []RegSignal
}

// Empty diz se nao ha nada para gravar.
func (b RegBatch) Empty() bool { return len(b.Events) == 0 && len(b.Downs) == 0 && len(b.Signals) == 0 }

const insertRegEventSQL = `INSERT INTO reg_events(at, number, kind, system, ip, port, user_agent) VALUES(?,?,?,?,?,?,?)`

// upsertHourlySQL soma uma ocorrencia no agregado da hora. Contadores so de system = 0 (a ocorrencia
// de sistema entra em system_events); last_drop_at fica com a maior; last_ip e last_port so mudam quando
// a ocorrencia tem endereco; rev marca o lote (cursor da secao 4.7.2).
const upsertHourlySQL = `INSERT INTO reg_hourly
	(number, hour, name, lost_registration, registered, became_unreachable, reachable_again, system_events, down_s,
	 last_drop_at, last_ip, last_port, rev)
VALUES (?,?,?,?,?,?,?,?,0,?,?,?,?)
ON CONFLICT(number, hour) DO UPDATE SET
	name = COALESCE(excluded.name, reg_hourly.name),
	lost_registration = reg_hourly.lost_registration + excluded.lost_registration,
	registered = reg_hourly.registered + excluded.registered,
	became_unreachable = reg_hourly.became_unreachable + excluded.became_unreachable,
	reachable_again = reg_hourly.reachable_again + excluded.reachable_again,
	system_events = reg_hourly.system_events + excluded.system_events,
	last_drop_at = CASE WHEN excluded.last_drop_at IS NULL THEN reg_hourly.last_drop_at
		WHEN reg_hourly.last_drop_at IS NULL OR excluded.last_drop_at > reg_hourly.last_drop_at THEN excluded.last_drop_at
		ELSE reg_hourly.last_drop_at END,
	last_ip = COALESCE(excluded.last_ip, reg_hourly.last_ip),
	last_port = CASE WHEN excluded.last_ip IS NULL THEN reg_hourly.last_port ELSE excluded.last_port END,
	rev = excluded.rev`

// upsertDownSQL soma segundos fora do ar na hora; o teto de 3600 faz um erro de conta nunca derrubar o
// lote no CHECK da coluna.
const upsertDownSQL = `INSERT INTO reg_hourly(number, hour, name, down_s, rev) VALUES (?,?,?,?,?)
ON CONFLICT(number, hour) DO UPDATE SET
	down_s = min(3600, reg_hourly.down_s + excluded.down_s),
	name = COALESCE(excluded.name, reg_hourly.name),
	rev = excluded.rev`

// trimRegEventsSQL deixa so as 200 ocorrencias mais novas do ramal: a subconsulta pega
// o id da 200a mais nova (OFFSET 199) e e NULL com menos de 200, quando nada e apagado.
const trimRegEventsSQL = `DELETE FROM reg_events WHERE number = ? AND id < (
	SELECT id FROM reg_events WHERE number = ? ORDER BY id DESC LIMIT 1 OFFSET 199)`

func nullStr(s string, minLen, maxLen int) any {
	if n := utf8.RuneCountInString(s); n < minLen || n > maxLen {
		return nil
	}
	return s
}

func nullPort(p int) any {
	if p < 1 || p > 65535 {
		return nil
	}
	return p
}

func validRegNumber(n string) bool {
	c := utf8.RuneCountInString(n)
	return c >= 1 && c <= MaxNumberRunes
}

func hourOf(unix int64) int64 { return unix - unix%3600 }

// WriteRegBatch grava o lote numa transacao so (tudo ou nada): (1) ocorrencias brutas; (2) agregado
// por hora e ramal; (3) segundos fora do ar de cada intervalo fechado, distribuidos pelas horas;
// (4) sinais do sistema; (5) corte de 200 ocorrencias por ramal. rev e a revisao deste lote.
func (s *Store) WriteRegBatch(ctx context.Context, rev int64, b RegBatch) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	touched := map[string]struct{}{}
	for _, e := range b.Events {
		if !validRegNumber(e.Number) {
			continue
		}
		ip := nullStr(e.IP, 7, 15)
		port := nullPort(e.Port)
		if ip == nil {
			port = nil
		}
		sys := 0
		if e.System {
			sys = 1
		}
		if _, err := tx.ExecContext(ctx, insertRegEventSQL, e.At.Unix(), e.Number, e.Kind, sys, ip, port,
			nullStr(e.UserAgent, 1, 256)); err != nil {
			return err
		}
		var lost, reg, unre, again, sysEv int
		var drop any
		switch {
		case e.System:
			sysEv = 1
		case e.Kind == "lost_registration":
			lost, drop = 1, e.At.Unix()
		case e.Kind == "registered":
			reg = 1
		case e.Kind == "became_unreachable":
			unre, drop = 1, e.At.Unix()
		case e.Kind == "reachable_again":
			again = 1
		}
		if _, err := tx.ExecContext(ctx, upsertHourlySQL, e.Number, hourOf(e.At.Unix()), nullStr(e.Name, 1, 128),
			lost, reg, unre, again, sysEv, drop, ip, port, rev); err != nil {
			return err
		}
		touched[e.Number] = struct{}{}
	}
	for _, d := range b.Downs {
		if !validRegNumber(d.Number) {
			continue
		}
		from, to := d.From.Unix(), d.To.Unix()
		if floor := to - regMaxDownHours*3600; from < floor {
			from = floor
		}
		for h := hourOf(from); h < to; h += 3600 {
			lo, hi := max(h, from), min(h+3600, to)
			if hi <= lo {
				continue
			}
			if _, err := tx.ExecContext(ctx, upsertDownSQL, d.Number, h, nullStr(d.Name, 1, 128), hi-lo, rev); err != nil {
				return err
			}
		}
	}
	for _, sg := range b.Signals {
		if _, err := tx.ExecContext(ctx, "INSERT INTO reg_system_events(at, kind) VALUES(?,?)", sg.At.Unix(), sg.Kind); err != nil {
			return err
		}
	}
	for n := range touched {
		if _, err := tx.ExecContext(ctx, trimRegEventsSQL, n, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MaxRegRev devolve a maior revisao gravada em reg_hourly (0 sem linhas).
func (s *Store) MaxRegRev(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(rev), 0) FROM reg_hourly").Scan(&n)
	return n, err
}

// RegSince devolve quando a versao com o relatorio foi instalada (reg_meta.since, "Dados desde").
func (s *Store) RegSince(ctx context.Context) (time.Time, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx, "SELECT since FROM reg_meta WHERE id = 1").Scan(&n); err != nil {
		return time.Time{}, err
	}
	return time.Unix(n, 0).UTC(), nil
}

// regPurgeChunk e o tamanho de cada bloco da limpeza; regPurgeTimeout, o prazo de cada bloco (CONTRATOS 10).
const (
	regPurgeChunk   = 5000
	regPurgeTimeout = 15 * time.Second
)

// PurgeReg apaga o que passou da retencao (before) em reg_events, reg_hourly e reg_system_events e devolve quantas
// linhas. rc.8: em blocos de 5.000, cada um um comando (autocommit) com prazo de 15 s, repetido ate apagar menos
// de 5.000; entre blocos a conexao unica volta ao pool (o apagao de uma vez so a segurava por tempo demais). Para
// no primeiro erro e o devolve: dado velho a mais nao e perda, a limpeza tenta de novo no dia seguinte.
func (s *Store) PurgeReg(ctx context.Context, before time.Time) (int64, error) {
	var total int64
	for _, q := range []struct{ sql string }{
		{"DELETE FROM reg_events WHERE id IN (SELECT id FROM reg_events WHERE at < ? LIMIT 5000)"},
		{"DELETE FROM reg_system_events WHERE id IN (SELECT id FROM reg_system_events WHERE at < ? LIMIT 5000)"},
		{"DELETE FROM reg_hourly WHERE (number, hour) IN (SELECT number, hour FROM reg_hourly WHERE hour < ? LIMIT 5000)"},
	} {
		for {
			n, err := s.purgeChunk(ctx, q.sql, before.Unix())
			total += n
			if err != nil {
				return total, err
			}
			if n < regPurgeChunk {
				break
			}
		}
	}
	return total, nil
}

func (s *Store) purgeChunk(ctx context.Context, query string, before int64) (int64, error) {
	cctx, cancel := context.WithTimeout(ctx, regPurgeTimeout)
	defer cancel()
	res, err := s.db.ExecContext(cctx, query, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RegRow e o agregado de um ramal no periodo (soma de reg_hourly).
type RegRow struct {
	Number       string
	Name         string // ultimo nome gravado; "" se nenhum
	Lost         int64
	Registered   int64
	Unreachable  int64
	Reachable    int64
	SystemEvents int64
	DownS        int64
	LastDropAt   int64 // unix; 0 = nunca
	LastIP       string
	LastPort     int
}

// RegRows soma reg_hourly por ramal desde a hora fromHour (unix, multiplo de 3600), ordenado por numero.
// afterRev > 0: so os ramais com alguma linha do periodo gravada depois dessa revisao (cursor da 4.7.2),
// mas com a soma do periodo inteiro. number != "": so esse ramal. onlyDrops: so quem tem perdeu registro
// ou ficou inalcancavel (bloco de instabilidade).
// ponytail: o nome e o ultimo IP saem de subconsultas por ramal (PK number,hour); com medicao ruim, uma
// tabela de "ultimo estado" por ramal resolve.
func (s *Store) RegRows(ctx context.Context, fromHour, afterRev int64, number string, onlyDrops bool) ([]RegRow, error) {
	var sb strings.Builder
	var args []any
	sb.WriteString(`SELECT r.number,
	COALESCE((SELECT n.name FROM reg_hourly n WHERE n.number = r.number AND n.name IS NOT NULL ORDER BY n.hour DESC LIMIT 1), ''),
	sum(r.lost_registration), sum(r.registered), sum(r.became_unreachable), sum(r.reachable_again), sum(r.system_events), sum(r.down_s),
	COALESCE(max(r.last_drop_at), 0),
	COALESCE((SELECT a.last_ip || '|' || COALESCE(a.last_port, 0) FROM reg_hourly a
		WHERE a.number = r.number AND a.hour >= ? AND a.last_ip IS NOT NULL ORDER BY a.hour DESC LIMIT 1), '')
FROM reg_hourly r WHERE r.hour >= ?`)
	args = append(args, fromHour, fromHour)
	if number != "" {
		sb.WriteString(" AND r.number = ?")
		args = append(args, number)
	}
	if afterRev > 0 {
		sb.WriteString(" AND r.number IN (SELECT c.number FROM reg_hourly c WHERE c.hour >= ? AND c.rev > ?)")
		args = append(args, fromHour, afterRev)
	}
	sb.WriteString(" GROUP BY r.number")
	if onlyDrops {
		sb.WriteString(" HAVING sum(r.lost_registration) + sum(r.became_unreachable) > 0")
	}
	sb.WriteString(" ORDER BY r.number")
	rs, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []RegRow
	for rs.Next() {
		var r RegRow
		var addr string
		if err := rs.Scan(&r.Number, &r.Name, &r.Lost, &r.Registered, &r.Unreachable, &r.Reachable, &r.SystemEvents,
			&r.DownS, &r.LastDropAt, &addr); err != nil {
			return nil, err
		}
		if ip, port, ok := strings.Cut(addr, "|"); ok {
			r.LastIP = ip
			r.LastPort, _ = strconv.Atoi(port)
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// RegEventRow e uma ocorrencia bruta de um ramal.
type RegEventRow struct {
	At        int64
	Kind      string
	System    bool
	IP        string
	Port      int
	UserAgent string
}

// RegEvents devolve as ocorrencias do ramal com at >= fromUnix, a mais nova primeiro, ate limit
// (o gravador guarda no maximo 200 por ramal). Usa reg_events_number_id.
func (s *Store) RegEvents(ctx context.Context, number string, fromUnix int64, limit int) ([]RegEventRow, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT at, kind, system, ip, port, user_agent FROM reg_events
		WHERE number = ? AND at >= ? ORDER BY id DESC LIMIT ?`, number, fromUnix, limit)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []RegEventRow
	for rs.Next() {
		var r RegEventRow
		var sys int
		var ip, ua sql.NullString
		var port sql.NullInt64
		if err := rs.Scan(&r.At, &r.Kind, &sys, &ip, &port, &ua); err != nil {
			return nil, err
		}
		r.System, r.IP, r.Port, r.UserAgent = sys == 1, ip.String, int(port.Int64), ua.String
		out = append(out, r)
	}
	return out, rs.Err()
}

// RegMinute e um minuto de history com as quedas contadas (so devolve minuto com dado: gap = 0 e coluna nao nula).
type RegMinute struct {
	Minute      int64
	Lost        int64
	Unreachable int64
}

// RegSeries devolve os minutos de history do grupo (group "" = total) em [fromUnix, toUnix) que tem dado
// nas colunas de queda, ordenados. A API soma por intervalo. Usa a PK (group_name, minute).
func (s *Store) RegSeries(ctx context.Context, group string, fromUnix, toUnix int64) ([]RegMinute, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT minute, lost_registration, became_unreachable FROM history
		WHERE group_name = ? AND minute >= ? AND minute < ? AND gap = 0
		  AND (lost_registration IS NOT NULL OR became_unreachable IS NOT NULL) ORDER BY minute`, group, fromUnix, toUnix)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []RegMinute
	for rs.Next() {
		var m RegMinute
		var lost, drop sql.NullInt64
		if err := rs.Scan(&m.Minute, &lost, &drop); err != nil {
			return nil, err
		}
		m.Lost, m.Unreachable = lost.Int64, drop.Int64
		out = append(out, m)
	}
	return out, rs.Err()
}

// RegSignalRow e um sinal do sistema gravado.
type RegSignalRow struct {
	At   int64
	Kind string
}

// RegSignals devolve os sinais com at >= fromUnix, o mais novo primeiro, ate limit. Usa reg_system_events_at.
func (s *Store) RegSignals(ctx context.Context, fromUnix int64, limit int) ([]RegSignalRow, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT at, kind FROM reg_system_events WHERE at >= ? ORDER BY at DESC, id DESC LIMIT ?`,
		fromUnix, limit)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []RegSignalRow
	for rs.Next() {
		var r RegSignalRow
		if err := rs.Scan(&r.At, &r.Kind); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}
