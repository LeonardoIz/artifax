package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Token is an API token. Only the hash of the secret is stored.
type Token struct {
	ID         string
	Name       string
	Prefix     string
	SecretHash string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

const tokenColumns = `id, name, prefix, secret_hash, created_at, last_used_at, revoked_at`

func scanToken(row interface{ Scan(...any) error }) (*Token, error) {
	var t Token
	var created int64
	var used, revoked sql.NullInt64
	if err := row.Scan(&t.ID, &t.Name, &t.Prefix, &t.SecretHash, &created, &used, &revoked); err != nil {
		return nil, err
	}
	t.CreatedAt = fromMillis(created)
	t.LastUsedAt = nullMillis(used)
	t.RevokedAt = nullMillis(revoked)
	return &t, nil
}

// InsertToken stores a new token.
func (s *Store) InsertToken(ctx context.Context, t *Token) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tokens (id, name, prefix, secret_hash, created_at)
		VALUES (?, ?, ?, ?, ?)`, t.ID, t.Name, t.Prefix, t.SecretHash, toMillis(t.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert token: %w", err)
	}
	return nil
}

// ActiveTokens returns non-revoked tokens, newest first.
func (s *Store) ActiveTokens(ctx context.Context) ([]Token, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+tokenColumns+` FROM tokens
		WHERE revoked_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("list tokens: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// TokenByHash returns the active token whose secret hashes to hash.
func (s *Store) TokenByHash(ctx context.Context, hash string) (*Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx, `SELECT `+tokenColumns+` FROM tokens
		WHERE secret_hash = ? AND revoked_at IS NULL`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}
	return t, nil
}

// TouchToken records a use of the token.
func (s *Store) TouchToken(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tokens SET last_used_at = ? WHERE id = ?`, toMillis(at), id)
	if err != nil {
		return fmt.Errorf("touch token: %w", err)
	}
	return nil
}

// RevokeToken disables an active token.
func (s *Store) RevokeToken(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE tokens SET revoked_at = ?
		WHERE id = ? AND revoked_at IS NULL`, toMillis(at), id)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	return affectedOne(res)
}

// CountTokens returns how many tokens were ever created, revoked ones included.
func (s *Store) CountTokens(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tokens`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count tokens: %w", err)
	}
	return n, nil
}
