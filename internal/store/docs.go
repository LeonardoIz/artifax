package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Visibility controls who can see a document.
type Visibility string

const (
	Public   Visibility = "public"
	Unlisted Visibility = "unlisted"
	Private  Visibility = "private"
)

// Visibilities lists every visibility, most open first.
var Visibilities = []Visibility{Public, Unlisted, Private}

// Valid reports whether v is a known visibility.
func (v Visibility) Valid() bool {
	return v == Public || v == Unlisted || v == Private
}

// Doc is a published HTML document.
type Doc struct {
	ID             string
	Title          string
	Description    string
	Visibility     Visibility
	BlobSHA256     string
	ByteSize       int64
	SourceFilename string
	CreatedBy      string
	UpdatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const docColumns = `id, title, description, visibility, blob_sha256, byte_size,
	source_filename, created_by, updated_by, created_at, updated_at`

func scanDoc(row interface{ Scan(...any) error }) (*Doc, error) {
	var d Doc
	var created, updated int64
	err := row.Scan(&d.ID, &d.Title, &d.Description, &d.Visibility, &d.BlobSHA256, &d.ByteSize,
		&d.SourceFilename, &d.CreatedBy, &d.UpdatedBy, &created, &updated)
	if err != nil {
		return nil, err
	}
	d.CreatedAt, d.UpdatedAt = fromMillis(created), fromMillis(updated)
	return &d, nil
}

// InsertDoc stores a new document.
func (s *Store) InsertDoc(ctx context.Context, d *Doc) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO docs (`+docColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Title, d.Description, d.Visibility, d.BlobSHA256, d.ByteSize,
		d.SourceFilename, d.CreatedBy, d.UpdatedBy, toMillis(d.CreatedAt), toMillis(d.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert doc: %w", err)
	}
	return nil
}

// GetDoc returns the document with the given id.
func (s *Store) GetDoc(ctx context.Context, id string) (*Doc, error) {
	d, err := scanDoc(s.db.QueryRowContext(ctx, `SELECT `+docColumns+` FROM docs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get doc: %w", err)
	}
	return d, nil
}

// UpdateDoc overwrites every mutable column of an existing document.
func (s *Store) UpdateDoc(ctx context.Context, d *Doc) error {
	res, err := s.db.ExecContext(ctx, `UPDATE docs SET title = ?, description = ?, visibility = ?,
		blob_sha256 = ?, byte_size = ?, source_filename = ?, updated_by = ?, updated_at = ?
		WHERE id = ?`,
		d.Title, d.Description, d.Visibility, d.BlobSHA256, d.ByteSize, d.SourceFilename,
		d.UpdatedBy, toMillis(d.UpdatedAt), d.ID)
	if err != nil {
		return fmt.Errorf("update doc: %w", err)
	}
	return affectedOne(res)
}

// DeleteDoc removes a document.
func (s *Store) DeleteDoc(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM docs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete doc: %w", err)
	}
	return affectedOne(res)
}

// BlobReferenced reports whether any document still points at the blob.
func (s *Store) BlobReferenced(ctx context.Context, sha string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM docs WHERE blob_sha256 = ?)`, sha).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check blob: %w", err)
	}
	return n == 1, nil
}

// ListParams filters and paginates ListDocs.
type ListParams struct {
	Query        string
	Visibilities []Visibility // empty means all
	Limit        int
	Cursor       string
}

// ErrBadCursor is returned for malformed pagination cursors.
var ErrBadCursor = errors.New("invalid cursor")

// ListDocs returns documents ordered by most recently updated, plus the cursor of the next page.
func (s *Store) ListDocs(ctx context.Context, p ListParams) ([]Doc, string, error) {
	var where []string
	var args []any

	if len(p.Visibilities) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(p.Visibilities)), ",")
		where = append(where, "visibility IN ("+marks+")")
		for _, v := range p.Visibilities {
			args = append(args, v)
		}
	}
	if q := strings.TrimSpace(p.Query); q != "" {
		like := "%" + escapeLike(q) + "%"
		where = append(where, `(title LIKE ? ESCAPE '\' OR description LIKE ? ESCAPE '\' OR id = ?)`)
		args = append(args, like, like, q)
	}
	if p.Cursor != "" {
		at, id, err := decodeCursor(p.Cursor)
		if err != nil {
			return nil, "", err
		}
		where = append(where, "(updated_at < ? OR (updated_at = ? AND id < ?))")
		args = append(args, at, at, id)
	}

	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT ` + docColumns + ` FROM docs`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY updated_at DESC, id DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list docs: %w", err)
	}
	defer rows.Close()

	docs := make([]Doc, 0, limit)
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, "", fmt.Errorf("list docs: %w", err)
		}
		docs = append(docs, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list docs: %w", err)
	}

	var next string
	if len(docs) > limit {
		docs = docs[:limit]
		last := docs[limit-1]
		next = encodeCursor(toMillis(last.UpdatedAt), last.ID)
	}
	return docs, next, nil
}

// DocStats returns the number of documents and the bytes used by distinct blobs.
func (s *Store) DocStats(ctx context.Context) (count int, bytes int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM docs),
		(SELECT COALESCE(SUM(byte_size), 0) FROM (SELECT DISTINCT blob_sha256, byte_size FROM docs))`,
	).Scan(&count, &bytes)
	if err != nil {
		return 0, 0, fmt.Errorf("doc stats: %w", err)
	}
	return count, bytes, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func encodeCursor(at int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at, 10) + ":" + id))
}

func decodeCursor(c string) (int64, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, "", ErrBadCursor
	}
	at, id, ok := strings.Cut(string(raw), ":")
	if !ok || id == "" {
		return 0, "", ErrBadCursor
	}
	ms, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return 0, "", ErrBadCursor
	}
	return ms, id, nil
}
