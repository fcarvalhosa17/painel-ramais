package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// User e uma linha de users.
type User struct {
	ID       int64
	Username string
	Hash     string
}

// Session e uma linha de sessions. UserID e nil para sessao de token.
type Session struct {
	IDHash    [32]byte
	Kind      string // "password" ou "token"
	UserID    *int64
	Subject   string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// CreateUser insere o usuario; ErrUserExists se o nome ja existe (sem diferenciar maiusculas).
func (s *Store) CreateUser(ctx context.Context, username, hash string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO users(username, password_hash, created_at, updated_at) VALUES(?,?,?,?)",
		username, hash, now.Unix(), now.Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrUserExists
	}
	return err
}

// UserByName busca por nome (sem diferenciar maiusculas).
func (s *Store) UserByName(ctx context.Context, username string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, "SELECT id, username, password_hash FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &u.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// DeleteUser apaga o usuario; as sessoes saem por ON DELETE CASCADE.
func (s *Store) DeleteUser(ctx context.Context, username string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM users WHERE username = ?", username)
	return affected(res, err)
}

// SetPassword troca o hash e apaga as sessoes do usuario, numa transacao so.
func (s *Store) SetPassword(ctx context.Context, username, hash string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, "UPDATE users SET password_hash = ?, updated_at = ? WHERE username = ? RETURNING id",
		hash, now.Unix(), username).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateSession grava a sessao (o id guardado e so o hash do cookie).
func (s *Store) CreateSession(ctx context.Context, se Session) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO sessions(id_hash, kind, user_id, subject, created_at, expires_at) VALUES(?,?,?,?,?,?)",
		se.IDHash[:], se.Kind, se.UserID, se.Subject, se.CreatedAt.Unix(), se.ExpiresAt.Unix())
	return err
}

// GetSession devolve a sessao ainda valida em now, ou ErrNotFound.
func (s *Store) GetSession(ctx context.Context, idHash [32]byte, now time.Time) (Session, error) {
	se := Session{IDHash: idHash}
	var created, expires int64
	err := s.db.QueryRowContext(ctx,
		"SELECT kind, user_id, subject, created_at, expires_at FROM sessions WHERE id_hash = ? AND expires_at > ?",
		idHash[:], now.Unix()).Scan(&se.Kind, &se.UserID, &se.Subject, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	se.CreatedAt, se.ExpiresAt = time.Unix(created, 0), time.Unix(expires, 0)
	return se, err
}

// DeleteSession apaga uma sessao; inexistente nao e erro (logout idempotente).
func (s *Store) DeleteSession(ctx context.Context, idHash [32]byte) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id_hash = ?", idHash[:])
	return err
}

// PurgeSessions apaga as vencidas e devolve quantas.
func (s *Store) PurgeSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", now.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteAllSessions encerra todas as sessoes (CLI session purge --all).
func (s *Store) DeleteAllSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RenewSession estende a validade de uma sessao ainda valida em now (expiracao por
// inatividade). Nunca encurta; ErrNotFound se a sessao sumiu ou ja venceu.
func (s *Store) RenewSession(ctx context.Context, idHash [32]byte, now, expires time.Time) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET expires_at = max(expires_at, ?) WHERE id_hash = ? AND expires_at > ?",
		expires.Unix(), idHash[:], now.Unix())
	return affected(res, err)
}
