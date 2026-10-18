package docs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

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
		Content:  []byte("<title>Weekly</title><p>x</p>"),
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
	d, err := svc.Create(ctx, Upload{Content: []byte("<p>v1</p>"), Fields: Fields{Title: ptr("Kept"), Description: ptr("d")}}, "a")
	if err != nil {
		t.Fatal(err)
	}
	old := d.BlobSHA256

	d2, err := svc.Replace(ctx, d.ID, Upload{Content: []byte("<title>Ignored</title><p>v2</p>")}, "b")
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
	a, _ := svc.Create(ctx, Upload{Content: content, Fields: described}, "x")
	b, _ := svc.Create(ctx, Upload{Content: content, Fields: described}, "x")
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

	if _, err := svc.Create(ctx, Upload{Content: png, Filename: "chart.png"}, "x"); !errors.As(err, &verr) {
		t.Errorf("image without title: %v", err)
	}
	if _, err := svc.Create(ctx, Upload{Content: png, Filename: "chart.png", Fields: Fields{Title: ptr("Chart")}}, "x"); !errors.As(err, &verr) {
		t.Errorf("image without description: %v", err)
	}
	if _, err := svc.Create(ctx, Upload{Content: []byte("<title>T</title><p>x</p>")}, "x"); !errors.As(err, &verr) {
		t.Errorf("html without description: %v", err)
	}

	img, err := svc.Create(ctx, Upload{Content: png, Filename: "chart.png", Fields: described}, "x")
	if err != nil || img.ContentType != "image/png" {
		t.Fatalf("image: %+v %v", img, err)
	}
	log, err := svc.Create(ctx, Upload{Content: []byte("10:00 INFO start\n"), Filename: "build.log", Fields: described}, "x")
	if err != nil || log.ContentType != "text/plain" {
		t.Fatalf("log: %+v %v", log, err)
	}
	// Replacing may change the type; existing metadata is kept.
	re, err := svc.Replace(ctx, log.ID, Upload{Content: png, Filename: "now.png"}, "x")
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
	if _, err := svc.Create(ctx, Upload{Content: make([]byte, 2<<20)}, "x"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	bad := store.Visibility("secret")
	if _, err := svc.Create(ctx, Upload{Content: []byte("<p>x</p>"), Fields: Fields{Visibility: &bad}}, "x"); !errors.As(err, &verr) {
		t.Errorf("bad visibility: %v", err)
	}
	if _, err := svc.Update(ctx, "nonexistent1", Fields{}, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}
	if _, err := svc.Get(ctx, "../../etc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("malformed id: %v", err)
	}
}
