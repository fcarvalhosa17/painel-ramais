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
	ErrExists     = errors.New("grupo ja existe")
	ErrInvalid    = errors.New("entrada invalida") // a mensagem do erro e segura para o cliente (ver invalidf)
)

// Store e o banco aberto. O pool tem uma conexao so: serializa as escritas do
// proprio processo e evita SQLITE_BUSY interno; o outro processo (CLI) espera
// pelo busy_timeout.
// ponytail: uma conexao; subir o pool se a leitura de historico atrapalhar o login.
type Store struct {
	db   *sql.DB
	path string
}

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
var migrations = []string{ddlV1, ddlV2, ddlV3}

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
	if _, err := tx.ExecContext(ctx, migrations[from]); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", from+1)); err != nil {
		return err
	}
	return tx.Commit()
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
