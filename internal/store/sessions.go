package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session is a logged-in admin browser session.
type Session struct {
	ID        string
	TokenHash string
	Subject   string
	Email     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// InsertSession stores a new session.
func (s *Store) InsertSession(ctx context.Context, sess *Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id, token_hash, subject, email, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`, sess.ID, sess.TokenHash, sess.Subject, sess.Email,
		toMillis(sess.CreatedAt), toMillis(sess.ExpiresAt))
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// SessionByHash returns the unexpired session whose cookie hashes to hash.
func (s *Store) SessionByHash(ctx context.Context, hash string, now time.Time) (*Session, error) {
	var sess Session
	var created, expires int64
	err := s.db.QueryRowContext(ctx, `SELECT id, token_hash, subject, email, created_at, expires_at
		FROM sessions WHERE token_hash = ? AND expires_at > ?`, hash, toMillis(now)).
		Scan(&sess.ID, &sess.TokenHash, &sess.Subject, &sess.Email, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	sess.CreatedAt, sess.ExpiresAt = fromMillis(created), fromMillis(expires)
	return &sess, nil
}

// DeleteSessionByHash removes a session (logout).
func (s *Store) DeleteSessionByHash(ctx context.Context, hash string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions purges sessions that expired before now.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, toMillis(now))
	if err != nil {
		return 0, fmt.Errorf("purge sessions: %w", err)
	}
	return res.RowsAffected()
}
