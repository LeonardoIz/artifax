package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for range 2 {
		s, err := Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func TestDocsCRUDAndList(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := range 5 {
		vis := Public
		if i%2 == 1 {
			vis = Private
		}
		d := &Doc{
			ID: fmt.Sprintf("doc%09d", i), Title: fmt.Sprintf("Report %d", i), Visibility: vis,
			BlobSHA256: "sha", ByteSize: 10, CreatedBy: "t", UpdatedBy: "t",
			CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if err := s.InsertDoc(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	page, next, err := s.ListDocs(ctx, ListParams{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != "doc000000004" || next == "" {
		t.Fatalf("first page = %v next=%q", ids(page), next)
	}
	var all []Doc
	all = append(all, page...)
	for next != "" {
		page, next, err = s.ListDocs(ctx, ListParams{Limit: 2, Cursor: next})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page...)
	}
	if len(all) != 5 {
		t.Fatalf("paginated %d docs, want 5", len(all))
	}

	public, _, err := s.ListDocs(ctx, ListParams{Visibilities: []Visibility{Public}})
	if err != nil {
		t.Fatal(err)
	}
	if len(public) != 3 {
		t.Errorf("public docs = %v", ids(public))
	}

	found, _, err := s.ListDocs(ctx, ListParams{Query: "report 3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != "doc000000003" {
		t.Errorf("search = %v", ids(found))
	}
	if found, _, _ := s.ListDocs(ctx, ListParams{Query: "%"}); len(found) != 0 {
		t.Errorf("LIKE wildcard was not escaped: %v", ids(found))
	}

	if _, _, err := s.ListDocs(ctx, ListParams{Cursor: "garbage!"}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("bad cursor err = %v", err)
	}

	count, size, err := s.DocStats(ctx)
	if err != nil || count != 5 || size != 10 {
		t.Errorf("stats = %d %d %v (blobs are shared, size counts once)", count, size, err)
	}

	if err := s.DeleteDoc(ctx, "doc000000000"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDoc(ctx, "doc000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get deleted = %v", err)
	}
	if err := s.DeleteDoc(ctx, "doc000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice = %v", err)
	}
}

func TestSessionsExpire(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now()
	sess := &Session{ID: "s1", TokenHash: "h1", Subject: "admin", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.InsertSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByHash(ctx, "h1", now); err != nil {
		t.Fatalf("live session: %v", err)
	}
	if _, err := s.SessionByHash(ctx, "h1", now.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}
	if n, err := s.DeleteExpiredSessions(ctx, now.Add(2*time.Hour)); err != nil || n != 1 {
		t.Fatalf("purge = %d %v", n, err)
	}
}

func ids(docs []Doc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.ID
	}
	return out
}

// Migration 003 makes active token names unique; existing duplicates are renamed.
func TestTokenNameMigration(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_init.sql", "002_content_type.sql"} {
		body, _ := migrations.ReadFile("migrations/" + name)
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.ExecContext(ctx, `PRAGMA user_version = 2;
		INSERT INTO tokens (id, name, prefix, secret_hash, created_at, revoked_at) VALUES
		('aaaaaa1', 'laptop', 'ax_1', 'h1', 1, NULL),
		('bbbbbb2', 'Laptop', 'ax_2', 'h2', 2, NULL),
		('cccccc3', 'laptop', 'ax_3', 'h3', 3, 10)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := map[string]string{}
	for _, id := range []string{"aaaaaa1", "bbbbbb2", "cccccc3"} {
		var name string
		s.db.QueryRowContext(ctx, `SELECT name FROM tokens WHERE id = ?`, id).Scan(&name)
		got[id] = name
	}
	want := map[string]string{"aaaaaa1": "laptop", "bbbbbb2": "Laptop (bbbbbb)", "cccccc3": "laptop"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("names = %v, want %v", got, want)
	}
}
