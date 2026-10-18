// Package docs implements artifact operations on top of the store and blob storage.
package docs

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LeonardoIz/artifax/internal/blobs"
	"github.com/LeonardoIz/artifax/internal/ids"
	"github.com/LeonardoIz/artifax/internal/media"
	"github.com/LeonardoIz/artifax/internal/store"
)

const (
	MaxTitleLen       = 200
	MaxDescriptionLen = 1000
	maxFilenameLen    = 255
)

// ValidationError reports invalid input. Its message is safe to show to clients.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

var (
	// ErrNotFound is returned for unknown artifact ids.
	ErrNotFound = store.ErrNotFound
	// ErrTooLarge is returned for uploads over the size limit.
	ErrTooLarge = errors.New("file exceeds the upload limit")
)

// Service manages documents. Writes are serialized so blob garbage collection
// never races with an upload of the same content.
type Service struct {
	store    *store.Store
	blobs    *blobs.Store
	maxBytes int64
	log      *slog.Logger
	now      func() time.Time
	mu       sync.Mutex
}

// NewService returns a document service.
func NewService(st *store.Store, bl *blobs.Store, maxBytes int64, log *slog.Logger) *Service {
	return &Service{store: st, blobs: bl, maxBytes: maxBytes, log: log, now: now}
}

// now matches what the store persists (UTC, millisecond precision), so responses
// are identical before and after a round trip.
func now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

// MaxBytes is the upload size limit.
func (s *Service) MaxBytes() int64 { return s.maxBytes }

// Fields carries optional metadata. A nil pointer leaves the value unchanged.
type Fields struct {
	Title       *string
	Description *string
	Visibility  *store.Visibility
}

// Upload is new content plus optional metadata.
type Upload struct {
	Content     []byte
	Filename    string
	ContentType string // as declared by the client; the stored type is detected
	Fields
}

// Create stores a new artifact. Visibility defaults to private and the title defaults
// to the page's <title> for HTML, or to the file name otherwise.
func (s *Service) Create(ctx context.Context, up Upload, actor string) (*store.Doc, error) {
	ct, err := s.detect(up)
	if err != nil {
		return nil, err
	}
	now := s.now()
	d := &store.Doc{
		ID:          ids.DocID(),
		Visibility:  store.Private,
		ContentType: ct,
		CreatedBy:   actor,
		CreatedAt:   now,
	}
	if err := applyFields(d, up.Fields); err != nil {
		return nil, err
	}
	if up.Title == nil {
		d.Title = defaultTitle(ct, up)
	}
	// Arbitrary files carry no usable metadata, so new artifacts must be described.
	if d.Title == "" {
		return nil, &ValidationError{Msg: "title is required (HTML pages may use their <title> instead)"}
	}
	if d.Description == "" {
		return nil, &ValidationError{Msg: "description is required"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.setContent(d, up, actor, now); err != nil {
		return nil, err
	}
	if err := s.store.InsertDoc(ctx, d); err != nil {
		s.collect(ctx, d.BlobSHA256)
		return nil, err
	}
	return d, nil
}

// Replace swaps the content of an existing document, keeping its id.
func (s *Service) Replace(ctx context.Context, id string, up Upload, actor string) (*store.Doc, error) {
	ct, err := s.detect(up)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.store.GetDoc(ctx, id)
	if err != nil {
		return nil, err
	}
	oldBlob := d.BlobSHA256
	d.ContentType = ct
	if err := applyFields(d, up.Fields); err != nil {
		return nil, err
	}
	if up.Title == nil && d.Title == "" {
		d.Title = defaultTitle(ct, up)
	}
	if err := s.setContent(d, up, actor, s.now()); err != nil {
		return nil, err
	}
	if err := s.store.UpdateDoc(ctx, d); err != nil {
		s.collect(ctx, d.BlobSHA256)
		return nil, err
	}
	if oldBlob != d.BlobSHA256 {
		s.collect(ctx, oldBlob)
	}
	return d, nil
}

// Update changes metadata only.
func (s *Service) Update(ctx context.Context, id string, f Fields, actor string) (*store.Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.store.GetDoc(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := applyFields(d, f); err != nil {
		return nil, err
	}
	// Leaving a field untouched is fine (older artifacts may lack a description),
	// but it can't be cleared.
	if f.Title != nil && d.Title == "" {
		return nil, &ValidationError{Msg: "title cannot be empty"}
	}
	if f.Description != nil && d.Description == "" {
		return nil, &ValidationError{Msg: "description cannot be empty"}
	}
	d.UpdatedBy = actor
	d.UpdatedAt = s.now()
	if err := s.store.UpdateDoc(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// Delete permanently removes a document and, if unused, its blob.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.store.GetDoc(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteDoc(ctx, id); err != nil {
		return err
	}
	s.collect(ctx, d.BlobSHA256)
	return nil
}

// Get returns a document by id. Malformed ids are reported as not found.
func (s *Service) Get(ctx context.Context, id string) (*store.Doc, error) {
	if !ids.ValidDocID(id) {
		return nil, ErrNotFound
	}
	return s.store.GetDoc(ctx, id)
}

// List returns a page of documents.
func (s *Service) List(ctx context.Context, p store.ListParams) ([]store.Doc, string, error) {
	return s.store.ListDocs(ctx, p)
}

// Stats returns the document count and storage used.
func (s *Service) Stats(ctx context.Context) (int, int64, error) {
	return s.store.DocStats(ctx)
}

// Open returns the document content. The caller must close it.
func (s *Service) Open(d *store.Doc) (*os.File, error) {
	return s.blobs.Open(d.BlobSHA256)
}

// detect checks the upload and returns its content type. Any non-empty file within
// the size limit is accepted.
func (s *Service) detect(up Upload) (string, error) {
	switch {
	case len(up.Content) == 0:
		return "", &ValidationError{Msg: "file is empty"}
	case int64(len(up.Content)) > s.maxBytes:
		return "", ErrTooLarge
	}
	return media.Detect(up.Filename, up.ContentType, up.Content), nil
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// defaultTitle is the <title> of HTML pages; other files have none.
func defaultTitle(ct string, up Upload) string {
	if media.Classify(ct) != media.HTML {
		return ""
	}
	m := titleRe.FindSubmatch(up.Content[:min(len(up.Content), 64<<10)])
	if m == nil {
		return ""
	}
	return truncate(strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " "), MaxTitleLen)
}

func (s *Service) setContent(d *store.Doc, up Upload, actor string, now time.Time) error {
	sum, err := s.blobs.Put(up.Content)
	if err != nil {
		return err
	}
	d.BlobSHA256 = sum
	d.ByteSize = int64(len(up.Content))
	if name := cleanFilename(up.Filename); name != "" {
		d.SourceFilename = name
	}
	d.UpdatedBy = actor
	d.UpdatedAt = now
	return nil
}

// collect deletes a blob once no document references it. Failures only leak disk space.
func (s *Service) collect(ctx context.Context, sha string) {
	used, err := s.store.BlobReferenced(ctx, sha)
	if err == nil && !used {
		err = s.blobs.Delete(sha)
	}
	if err != nil {
		s.log.Warn("blob cleanup failed", "blob", sha, "err", err)
	}
}

func applyFields(d *store.Doc, f Fields) error {
	if f.Title != nil {
		t := strings.TrimSpace(*f.Title)
		if utf8.RuneCountInString(t) > MaxTitleLen {
			return &ValidationError{Msg: fmt.Sprintf("title must be at most %d characters", MaxTitleLen)}
		}
		d.Title = t
	}
	if f.Description != nil {
		desc := strings.TrimSpace(*f.Description)
		if utf8.RuneCountInString(desc) > MaxDescriptionLen {
			return &ValidationError{Msg: fmt.Sprintf("description must be at most %d characters", MaxDescriptionLen)}
		}
		d.Description = desc
	}
	if f.Visibility != nil {
		if !f.Visibility.Valid() {
			return &ValidationError{Msg: "visibility must be public, unlisted or private"}
		}
		d.Visibility = *f.Visibility
	}
	return nil
}

// cleanFilename keeps only the base name of an uploaded file, for display.
func cleanFilename(name string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	if name == "." || name == "/" {
		return ""
	}
	return truncate(name, maxFilenameLen)
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
