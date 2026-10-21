// Package store e o SQLite do daemon (CONTRATOS.md secao 10): usuarios, sessoes,
// grupos e historico. Nao importa pacote interno; devolve os proprios tipos.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // driver "sqlite", Go puro (sem CGO)
)

var (
	ErrNotFound   = errors.New("nao encontrado")
	ErrUserExists = errors.New("usuario ja existe")
	ErrOverlap    = errors.New("faixa sobreposta")
	ErrExists     = errors.New("visão já existe")
	ErrInvalid    = errors.New("entrada invalida") // a mensagem do erro e segura para o cliente (ver invalidf)
)

// Store e o banco aberto. O pool tem uma conexao so: serializa as escritas do
// proprio processo e evita SQLITE_BUSY interno; o outro processo (CLI) espera
// pelo busy_timeout.
// ponytail: uma conexao; subir o pool se a leitura de historico atrapalhar o login.
type Store struct {
	db     *sql.DB
	path   string
	report MigrationReport
}

// MigrationReport traz o que a migracao descartou, para quem abriu o banco (daemon ou CLI) registrar
// no log. A migracao 3->4 nao leva os vinculos manuais de "Sem grupo" (group_id NULL): "Sem grupo" e
// implicito desde a rc.7 (ADR-006). Sample guarda ate 20 numeros.
type MigrationReport struct {
	NoGroupDropped int
	NoGroupSample  []string
}

// MigrationReport devolve o que a migracao feita por este Open descartou (zero se nao migrou).
func (s *Store) MigrationReport() MigrationReport { return s.report }

const migrationSampleMax = 20

// Open abre (e migra) <dataDir>/painel.db. Banco com esquema mais novo que o
// binario recusa abrir (downgrade nao suportado).
func Open(ctx context.Context, dataDir string) (*Store, error) {
	path := filepath.Join(dataDir, "painel.db")
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrations[i] leva o banco da versao i para i+1. So para frente.
var migrations = []string{ddlV1, ddlV2, ddlV3, ddlV4, ddlV5, ddlV6}

// SchemaVersion e o PRAGMA user_version que este binario conhece (uma migracao por versao).
var SchemaVersion = len(migrations)

func (s *Store) migrate(ctx context.Context) error {
	var cur int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&cur); err != nil {
		return fmt.Errorf("ler user_version: %w", err)
	}
	if cur > SchemaVersion {
		return fmt.Errorf("banco com esquema %d, este binario conhece ate %d (downgrade nao suportado)", cur, SchemaVersion)
	}
	if cur == SchemaVersion {
		return nil
	}
	if cur > 0 {
		if err := s.backup(ctx, cur); err != nil {
			return err
		}
	}
	for v := cur; v < SchemaVersion; v++ {
		if err := s.applyMigration(ctx, v); err != nil {
			return fmt.Errorf("migracao %d->%d: %w", v, v+1, err)
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, from int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rep MigrationReport
	if from == 3 { // v4: antes de reconstruir extension_groups, conta o que nao migra
		if rep, err = noGroupLinks(ctx, tx); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, migrations[from]); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", from+1)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if rep.NoGroupDropped > 0 {
		s.report = rep
	}
	return nil
}

// noGroupLinks le os vinculos "Sem grupo" manual (group_id NULL) da v3, na transacao da migracao.
func noGroupLinks(ctx context.Context, tx *sql.Tx) (MigrationReport, error) {
	var rep MigrationReport
	rs, err := tx.QueryContext(ctx, "SELECT number FROM extension_groups WHERE group_id IS NULL ORDER BY number")
	if err != nil {
		return rep, fmt.Errorf("ler vinculos Sem grupo: %w", err)
	}
	defer rs.Close()
	for rs.Next() {
		var n string
		if err := rs.Scan(&n); err != nil {
			return rep, err
		}
		if rep.NoGroupDropped++; len(rep.NoGroupSample) < migrationSampleMax {
			rep.NoGroupSample = append(rep.NoGroupSample, n)
		}
	}
	return rep, rs.Err()
}

// backup copia o banco antes de migrar: painel.db.bak-v<versao antiga>.
func (s *Store) backup(ctx context.Context, version int) error {
	dst := fmt.Sprintf("%s.bak-v%d", s.path, version)
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(filepath.ToSlash(dst), "'", "''")+"'"); err != nil {
		return fmt.Errorf("backup antes da migracao: %w", err)
	}
	return os.Chmod(dst, 0o600) // tem hashes de senha
}

const ddlV1 = `
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  username      TEXT    NOT NULL COLLATE NOCASE UNIQUE
                CHECK (length(username) BETWEEN 1 AND 64),
  password_hash TEXT    NOT NULL CHECK (password_hash LIKE '$2%'),
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE sessions (
  id_hash    BLOB    PRIMARY KEY CHECK (length(id_hash) = 32),
  kind       TEXT    NOT NULL CHECK (kind IN ('password', 'token')),
  user_id    INTEGER REFERENCES users(id) ON DELETE CASCADE,
  subject    TEXT    NOT NULL CHECK (length(subject) BETWEEN 1 AND 128),
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  CHECK ((kind = 'password') = (user_id IS NOT NULL)),
  CHECK (expires_at > created_at)
) STRICT, WITHOUT ROWID;
CREATE INDEX sessions_user_id    ON sessions(user_id);
CREATE INDEX sessions_expires_at ON sessions(expires_at);

CREATE TABLE groups (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL COLLATE NOCASE UNIQUE
             CHECK (length(name) BETWEEN 1 AND 64 AND name = trim(name) AND name <> 'Sem grupo'),
  created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE group_ranges (
  id          INTEGER PRIMARY KEY,
  group_id    INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  range_start INTEGER NOT NULL CHECK (range_start >= 0),
  range_end   INTEGER NOT NULL,
  CHECK (range_end >= range_start)
) STRICT;
CREATE INDEX group_ranges_group_id ON group_ranges(group_id);
CREATE INDEX group_ranges_start    ON group_ranges(range_start);

CREATE TRIGGER group_ranges_no_overlap BEFORE INSERT ON group_ranges
WHEN EXISTS (SELECT 1 FROM group_ranges r
             WHERE NEW.range_start <= r.range_end AND r.range_start <= NEW.range_end)
BEGIN SELECT RAISE(ABORT, 'faixa sobreposta'); END;

CREATE TRIGGER group_ranges_immutable BEFORE UPDATE ON group_ranges
BEGIN SELECT RAISE(ABORT, 'faixa imutavel'); END;

CREATE TABLE history (
  minute       INTEGER NOT NULL CHECK (minute % 60 = 0),
  group_name   TEXT    NOT NULL,
  registered   INTEGER,
  unregistered INTEGER,
  unreachable  INTEGER,
  in_call      INTEGER,
  gap          INTEGER NOT NULL CHECK (gap IN (0, 1)),
  CHECK (
    (gap = 1 AND registered IS NULL AND unregistered IS NULL AND unreachable IS NULL AND in_call IS NULL)
    OR
    (gap = 0 AND registered >= 0 AND unregistered >= 0 AND unreachable >= 0 AND in_call >= 0)
  ),
  PRIMARY KEY (group_name, minute)
) STRICT, WITHOUT ROWID;
CREATE INDEX history_minute ON history(minute);
`

// v2: cache dos nomes (callerid) dos ramais, para aparecerem logo apos um restart.
const ddlV2 = `
CREATE TABLE extension_names (
  number     TEXT    PRIMARY KEY CHECK (length(number) BETWEEN 1 AND 64),
  name       TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
  updated_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
`

// v3: grupos editaveis pela tela. extension_groups e o vinculo MANUAL ramal -> grupo, por numero
// (texto): sobrevive a ramal apagado e recriado. group_id NULL = "Sem grupo" manual (vence a faixa).
// ON DELETE CASCADE: apagar o grupo desfaz as atribuicoes dele (o ramal volta para a faixa ou para
// "Sem grupo"). group_audit guarda quem, o que e quando de cada alteracao de grupo (mesma transacao).
const ddlV3 = `
CREATE TABLE extension_groups (
  number     TEXT    PRIMARY KEY CHECK (length(number) BETWEEN 1 AND 64),
  group_id   INTEGER REFERENCES groups(id) ON DELETE CASCADE,
  updated_at INTEGER NOT NULL,
  updated_by TEXT    NOT NULL CHECK (length(updated_by) BETWEEN 1 AND 128)
) STRICT, WITHOUT ROWID;
CREATE INDEX extension_groups_group_id ON extension_groups(group_id);

CREATE TABLE group_audit (
  id      INTEGER PRIMARY KEY,
  at      INTEGER NOT NULL,
  subject TEXT    NOT NULL CHECK (length(subject) BETWEEN 1 AND 128),
  action  TEXT    NOT NULL CHECK (action IN ('create', 'rename', 'ranges', 'delete', 'assign', 'import')),
  detail  TEXT    NOT NULL CHECK (json_valid(detail))
) STRICT;
CREATE INDEX group_audit_at ON group_audit(at);

-- Auditoria so cresce: UPDATE e barrado; DELETE continua permitido (expurgo por retencao).
CREATE TRIGGER group_audit_immutable BEFORE UPDATE ON group_audit
BEGIN SELECT RAISE(ABORT, 'auditoria imutavel'); END;
`

// v4 (rc.7): ramal em N grupos (ADR-006) e relatorio de registro (ADR-007), numa migracao so, com o
// backup painel.db.bak-v3 antes. Identica ao bloco SQL da versao 4 da secao 10 do CONTRATOS.
const ddlV4 = `
-- 1) N:N. Chave (number, group_id), sem NULL: "Sem grupo" manual deixa de existir.
CREATE TABLE extension_groups_v4 (
  number     TEXT    NOT NULL CHECK (length(number) BETWEEN 1 AND 64),
  group_id   INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  updated_at INTEGER NOT NULL,
  updated_by TEXT    NOT NULL CHECK (length(updated_by) BETWEEN 1 AND 128),
  PRIMARY KEY (number, group_id)
) STRICT, WITHOUT ROWID;
INSERT INTO extension_groups_v4 (number, group_id, updated_at, updated_by)
  SELECT number, group_id, updated_at, updated_by FROM extension_groups WHERE group_id IS NOT NULL;
DROP TABLE extension_groups;
ALTER TABLE extension_groups_v4 RENAME TO extension_groups;
CREATE INDEX extension_groups_group_id ON extension_groups(group_id);

-- 2) Faixas de grupos diferentes podem se sobrepor; do mesmo grupo, nao.
--    A mensagem continua contendo "faixa sobreposta" (o codigo procura esse texto).
DROP TRIGGER group_ranges_no_overlap;
CREATE TRIGGER group_ranges_no_overlap_same_group BEFORE INSERT ON group_ranges
WHEN EXISTS (SELECT 1 FROM group_ranges r
             WHERE r.group_id = NEW.group_id
               AND NEW.range_start <= r.range_end AND r.range_start <= NEW.range_end)
BEGIN SELECT RAISE(ABORT, 'faixa sobreposta no mesmo grupo'); END;

-- 3) history: quedas por minuto (grupo e total '') para o grafico da aba Registros.
--    NULL nas linhas anteriores a v4 e em minuto de lacuna (gap = 1); >= 0 nos demais.
ALTER TABLE history ADD COLUMN lost_registration  INTEGER CHECK (lost_registration  IS NULL OR lost_registration  >= 0);
ALTER TABLE history ADD COLUMN became_unreachable INTEGER CHECK (became_unreachable IS NULL OR became_unreachable >= 0);

-- 4) Relatorio de registro.
-- Data de instalacao da versao com relatorio ("Dados desde ..."), gravada uma vez pela migracao.
CREATE TABLE reg_meta (
  id    INTEGER PRIMARY KEY CHECK (id = 1),
  since INTEGER NOT NULL
) STRICT;
INSERT INTO reg_meta (id, since) VALUES (1, CAST(strftime('%s', 'now') AS INTEGER));

-- Ocorrencia bruta. No maximo 200 por ramal (o gravador apaga as mais antigas a cada lote).
-- system = 1: transicao dentro da janela de 120 s apos Shutdown, FullyBooted ou conexao do AMI.
CREATE TABLE reg_events (
  id         INTEGER PRIMARY KEY,
  at         INTEGER NOT NULL,
  number     TEXT    NOT NULL CHECK (length(number) BETWEEN 1 AND 64),
  kind       TEXT    NOT NULL CHECK (kind IN ('lost_registration', 'registered', 'became_unreachable', 'reachable_again')),
  system     INTEGER NOT NULL CHECK (system IN (0, 1)),
  ip         TEXT    CHECK (ip IS NULL OR length(ip) BETWEEN 7 AND 15),
  port       INTEGER CHECK (port IS NULL OR port BETWEEN 1 AND 65535),
  user_agent TEXT    CHECK (user_agent IS NULL OR length(user_agent) BETWEEN 1 AND 256)
) STRICT;
CREATE INDEX reg_events_number_id ON reg_events(number, id);
CREATE INDEX reg_events_at        ON reg_events(at);

-- Agregado por hora e ramal. Contadores so de system = 0; system_events conta os de sistema.
-- name: ultimo nome conhecido (o relatorio nao depende do cache extension_names, que o pruneNames apaga).
-- rev: lote de gravacao que tocou a linha por ultimo (cursor da secao 4.7.2).
CREATE TABLE reg_hourly (
  number             TEXT    NOT NULL CHECK (length(number) BETWEEN 1 AND 64),
  hour               INTEGER NOT NULL CHECK (hour % 3600 = 0),
  name               TEXT    CHECK (name IS NULL OR length(name) BETWEEN 1 AND 128),
  lost_registration  INTEGER NOT NULL DEFAULT 0 CHECK (lost_registration  >= 0),
  registered         INTEGER NOT NULL DEFAULT 0 CHECK (registered         >= 0),
  became_unreachable INTEGER NOT NULL DEFAULT 0 CHECK (became_unreachable >= 0),
  reachable_again    INTEGER NOT NULL DEFAULT 0 CHECK (reachable_again    >= 0),
  system_events      INTEGER NOT NULL DEFAULT 0 CHECK (system_events      >= 0),
  down_s             INTEGER NOT NULL DEFAULT 0 CHECK (down_s BETWEEN 0 AND 3600),
  last_drop_at       INTEGER,
  last_ip            TEXT    CHECK (last_ip IS NULL OR length(last_ip) BETWEEN 7 AND 15),
  last_port          INTEGER CHECK (last_port IS NULL OR last_port BETWEEN 1 AND 65535),
  rev                INTEGER NOT NULL CHECK (rev >= 1),
  PRIMARY KEY (number, hour)
) STRICT, WITHOUT ROWID;
CREATE INDEX reg_hourly_hour ON reg_hourly(hour);
CREATE INDEX reg_hourly_rev  ON reg_hourly(rev);

-- Sinais do sistema que abrem a janela de supressao.
CREATE TABLE reg_system_events (
  id   INTEGER PRIMARY KEY,
  at   INTEGER NOT NULL,
  kind TEXT    NOT NULL CHECK (kind IN ('shutdown', 'fully_booted', 'ami_connected'))
) STRICT;
CREATE INDEX reg_system_events_at ON reg_system_events(at);
`

// v5 (rc.8): nome da fila dado pelo painel (ADR-009). So aditiva. Identica ao bloco SQL da versao 5 da secao 10 do CONTRATOS.
const ddlV5 = `
-- Nome dado pelo painel a uma fila do Asterisk (a chave e o identificador do AMI, ex.: '600').
-- Sem linha = sem nome (a tela mostra "Fila 600"). updated_by = subject da sessao que gravou.
CREATE TABLE queue_names (
  queue      TEXT    PRIMARY KEY CHECK (length(queue) BETWEEN 1 AND 64),
  name       TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 64 AND name = trim(name)),
  updated_at INTEGER NOT NULL,
  updated_by TEXT    NOT NULL CHECK (length(updated_by) BETWEEN 1 AND 128)
) STRICT, WITHOUT ROWID;
`

// v6 (rc.9): estado "fora do ar" e ultimo registro persistentes (reg_state) e a coluna de lacuna em reg_hourly
// (ADR-010). So aditiva. Identica ao bloco SQL da versao 6 da secao 10 do CONTRATOS.
const ddlV6 = `
-- Estado "fora do ar" e ultimo registro de cada ramal ja visto (ADR-010). Fora da retencao de 30 dias e do corte de 200.
-- down_since: inicio do "fora" atual (unregistered ou unreachable); NULL = registrado e alcancavel (ou nunca visto fora).
-- down_kind: observed (hora vista), unknown_start (caiu com painel ou AMI parado: down_since e limite),
--            before_install (ja estava fora na primeira vez que o painel o viu, com a tabela vazia).
-- down_seg: inicio do trecho aberto ainda nao somado em reg_hourly.down_s (difere de down_since depois de lacuna).
-- last_reg: ultima vez que ficou registrado; last_reg_exact = 0 quando e limite ("antes de").
-- rev: lote de gravacao que tocou a linha por ultimo (cursor da secao 4.7.2).
CREATE TABLE reg_state (
  number         TEXT    PRIMARY KEY CHECK (length(number) BETWEEN 1 AND 64),
  down_since     INTEGER,
  down_kind      TEXT    CHECK (down_kind IN ('observed', 'unknown_start', 'before_install')),
  down_seg       INTEGER,
  last_reg       INTEGER,
  last_reg_exact INTEGER NOT NULL DEFAULT 0 CHECK (last_reg_exact IN (0, 1)),
  rev            INTEGER NOT NULL CHECK (rev >= 1),
  CHECK ((down_since IS NULL) = (down_kind IS NULL)),
  CHECK ((down_since IS NULL) = (down_seg IS NULL)),
  CHECK (down_seg IS NULL OR down_seg >= down_since),
  CHECK (last_reg IS NOT NULL OR last_reg_exact = 0)
) STRICT, WITHOUT ROWID;
CREATE INDEX reg_state_rev ON reg_state(rev);

-- Lacuna: a hora recebeu segundos de um trecho cortado porque o painel ou o AMI estavam parados.
ALTER TABLE reg_hourly ADD COLUMN down_gap INTEGER NOT NULL DEFAULT 0 CHECK (down_gap IN (0, 1));
`
