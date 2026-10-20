package docs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeonardoIz/artifax/internal/blobs"
	"github.com/LeonardoIz/artifax/internal/store"
)

func newService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	blobDir := filepath.Join(dir, "blobs")
	bl, err := blobs.New(blobDir)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(st, bl, 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil))), blobDir
}

func blobExists(t *testing.T, dir, sha string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, sha[:2], sha))
	return err == nil
}

func ptr[T any](v T) *T { return &v }

var described = Fields{Title: ptr("T"), Description: ptr("D")}

func TestCreateDefaults(t *testing.T) {
	svc, _ := newService(t)
	d, err := svc.Create(context.Background(), Upload{
		Body:     bytes.NewReader([]byte("<title>Weekly</title><p>x</p>")),
		Filename: `C:\Users\me\weekly.html`,
		Fields:   Fields{Description: ptr("Weekly notes")},
	}, "token:test")
	if err != nil {
		t.Fatal(err)
	}
	if d.Visibility != store.Private || d.Title != "Weekly" || d.SourceFilename != "weekly.html" {
		t.Errorf("doc = %+v", d)
	}
	if d.CreatedBy != "token:test" || d.UpdatedBy != "token:test" {
		t.Errorf("actors = %q %q", d.CreatedBy, d.UpdatedBy)
	}
}

func TestReplaceCollectsOldBlob(t *testing.T) {
	ctx := context.Background()
	svc, dir := newService(t)
	d, err := svc.Create(ctx, Upload{Body: bytes.NewReader([]byte("<p>v1</p>")), Fields: Fields{Title: ptr("Kept"), Description: ptr("d")}}, "a")
	if err != nil {
		t.Fatal(err)
	}
	old := d.BlobSHA256

	d2, err := svc.Replace(ctx, d.ID, Upload{Body: bytes.NewReader([]byte("<title>Ignored</title><p>v2</p>"))}, "b")
	if err != nil {
		t.Fatal(err)
	}
	if d2.ID != d.ID || d2.Title != "Kept" || d2.UpdatedBy != "b" || d2.CreatedBy != "a" {
		t.Errorf("replaced doc = %+v", d2)
	}
	if blobExists(t, dir, old) {
		t.Error("old blob was not collected")
	}
	if !blobExists(t, dir, d2.BlobSHA256) {
		t.Error("new blob missing")
	}
}

func TestSharedBlobSurvivesDelete(t *testing.T) {
	ctx := context.Background()
	svc, dir := newService(t)
	content := []byte("<p>same</p>")
	a, _ := svc.Create(ctx, Upload{Body: bytes.NewReader(content), Fields: described}, "x")
	b, _ := svc.Create(ctx, Upload{Body: bytes.NewReader(content), Fields: described}, "x")
	if a.BlobSHA256 != b.BlobSHA256 {
		t.Fatal("identical content should share a blob")
	}
	if err := svc.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if !blobExists(t, dir, b.BlobSHA256) {
		t.Fatal("blob still used by b was deleted")
	}
	if err := svc.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if blobExists(t, dir, b.BlobSHA256) {
		t.Fatal("unused blob was not deleted")
	}
}

func TestArbitraryFiles(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	var verr *ValidationError

	if _, err := svc.Create(ctx, Upload{Body: bytes.NewReader(png), Filename: "chart.png"}, "x"); !errors.As(err, &verr) {
		t.Errorf("image without title: %v", err)
	}
	if _, err := svc.Create(ctx, Upload{Body: bytes.NewReader(png), Filename: "chart.png", Fields: Fields{Title: ptr("Chart")}}, "x"); !errors.As(err, &verr) {
		t.Errorf("image without description: %v", err)
	}
	if _, err := svc.Create(ctx, Upload{Body: bytes.NewReader([]byte("<title>T</title><p>x</p>"))}, "x"); !errors.As(err, &verr) {
		t.Errorf("html without description: %v", err)
	}

	img, err := svc.Create(ctx, Upload{Body: bytes.NewReader(png), Filename: "chart.png", Fields: described}, "x")
	if err != nil || img.ContentType != "image/png" {
		t.Fatalf("image: %+v %v", img, err)
	}
	log, err := svc.Create(ctx, Upload{Body: bytes.NewReader([]byte("10:00 INFO start\n")), Filename: "build.log", Fields: described}, "x")
	if err != nil || log.ContentType != "text/plain" {
		t.Fatalf("log: %+v %v", log, err)
	}
	// Replacing may change the type; existing metadata is kept.
	re, err := svc.Replace(ctx, log.ID, Upload{Body: bytes.NewReader(png), Filename: "now.png"}, "x")
	if err != nil || re.ContentType != "image/png" || re.Title != "T" || re.Description != "D" {
		t.Fatalf("replace: %+v %v", re, err)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	var verr *ValidationError

	if _, err := svc.Create(ctx, Upload{}, "x"); !errors.As(err, &verr) {
		t.Errorf("empty: %v", err)
	}
	if _, err := svc.Create(ctx, Upload{Body: bytes.NewReader(make([]byte, 2<<20))}, "x"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	bad := store.Visibility("secret")
	if _, err := svc.Create(ctx, Upload{Body: bytes.NewReader([]byte("<p>x</p>")), Fields: Fields{Visibility: &bad}}, "x"); !errors.As(err, &verr) {
		t.Errorf("bad visibility: %v", err)
	}
	if _, err := svc.Update(ctx, "nonexistent1", Fields{}, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}
	if _, err := svc.Get(ctx, "../../etc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("malformed id: %v", err)
	}
}

// Uploads bigger than the sniffed head are streamed intact, and the limit holds.
func TestStreamedUpload(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	described := Fields{Title: ptr("Big"), Description: ptr("d")}
	content := bytes.Repeat([]byte("0123456789abcdef\n"), (headBytes*3)/17)

	d, err := svc.Create(ctx, Upload{Body: bytes.NewReader(content), Filename: "big.log", Fields: described}, "x")
	if err != nil {
		t.Fatal(err)
	}
	f, err := svc.Open(d)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if !bytes.Equal(got, content) || d.ByteSize != int64(len(content)) {
		t.Errorf("stored %d bytes, want %d", len(got), len(content))
	}

	over := io.MultiReader(bytes.NewReader(make([]byte, headBytes)), bytes.NewReader(make([]byte, 1<<20)))
	if _, err := svc.Create(ctx, Upload{Body: over, Filename: "x.bin", Fields: described}, "x"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("over the limit after the head: %v", err)
	}
}

// Sweep removes temp files and unreferenced old blobs, and keeps everything in use.
func TestSweep(t *testing.T) {
	svc, dir := newService(t)
	ctx := context.Background()
	d, err := svc.Create(ctx, Upload{Body: bytes.NewReader([]byte("<p>kept</p>")), Fields: Fields{Title: ptr("T"), Description: ptr("d")}}, "x")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	orphan := strings.Repeat("ab", 32)
	fresh := strings.Repeat("cd", 32)
	for _, key := range []string{orphan, fresh} {
		os.MkdirAll(filepath.Join(dir, key[:2]), 0o750)
		os.WriteFile(filepath.Join(dir, key[:2], key), []byte("x"), 0o640)
	}
	os.Chtimes(filepath.Join(dir, orphan[:2], orphan), old, old)
	os.Chtimes(filepath.Join(dir, d.BlobSHA256[:2], d.BlobSHA256), old, old)
	os.WriteFile(filepath.Join(dir, "tmp", "blob-123"), []byte("partial"), 0o640)

	n, err := svc.Sweep(ctx)
	if err != nil || n != 2 {
		t.Fatalf("sweep = %d %v, want 2 (temp file + old orphan)", n, err)
	}
	if blobExists(t, dir, orphan) || !blobExists(t, dir, fresh) || !blobExists(t, dir, d.BlobSHA256) {
		t.Error("sweep removed the wrong files")
	}
}
