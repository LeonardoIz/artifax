// Package blobs stores document contents on disk, addressed by SHA-256.
package blobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Store keeps blobs under dir/<first two hex chars>/<sha256>.
type Store struct {
	dir string
}

// New creates the blob directory if needed.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o750); err != nil {
		return nil, fmt.Errorf("create blob dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Staged is content written to a temporary file and hashed, but not stored yet.
// Staging can be slow (it reads from the network); Commit is quick, so callers can
// stage outside their locks and commit inside them.
type Staged struct {
	Key  string // hex SHA-256
	Size int64
	tmp  string // empty once committed or discarded
	dst  string
}

// Stage copies r to a temporary file while hashing it. Read errors are returned
// wrapped, so callers can inspect them with errors.Is/As.
func (s *Store) Stage(r io.Reader) (*Staged, error) {
	tmp, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), "blob-*")
	if err != nil {
		return nil, fmt.Errorf("create temp blob: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return nil, fmt.Errorf("write blob: %w", err)
	}
	key := hex.EncodeToString(h.Sum(nil))
	return &Staged{Key: key, Size: n, tmp: tmp.Name(), dst: s.path(key)}, nil
}

// Commit moves the staged content into place. Existing blobs are left untouched.
func (st *Staged) Commit() error {
	if st.tmp == "" {
		return errors.New("blob already committed or discarded")
	}
	defer st.Discard() // drops the temp file when the blob already existed
	if _, err := os.Stat(st.dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(st.dst), 0o750); err != nil {
		return fmt.Errorf("create blob shard: %w", err)
	}
	if err := os.Rename(st.tmp, st.dst); err != nil {
		return fmt.Errorf("store blob: %w", err)
	}
	st.tmp = ""
	return nil
}

// Discard removes the temporary file if it was not committed. It is safe to call
// more than once.
func (st *Staged) Discard() {
	if st.tmp != "" {
		os.Remove(st.tmp)
		st.tmp = ""
	}
}

// Open opens the blob for reading.
func (s *Store) Open(key string) (*os.File, error) {
	if !validKey(key) {
		return nil, fs.ErrNotExist
	}
	return os.Open(s.path(key))
}

// Delete removes the blob. Deleting a missing blob is not an error.
func (s *Store) Delete(key string) error {
	if !validKey(key) {
		return nil
	}
	if err := os.Remove(s.path(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete blob: %w", err)
	}
	return nil
}

// Sweep removes leftovers of interrupted uploads: every temporary file, and blobs
// older than minAge for which inUse reports false. It returns how many files it
// removed. Call it while no upload is running, e.g. at startup.
func (s *Store) Sweep(inUse func(key string) bool, minAge time.Duration) (int, error) {
	removed := 0
	tmps, err := os.ReadDir(filepath.Join(s.dir, "tmp"))
	if err != nil {
		return 0, fmt.Errorf("sweep blobs: %w", err)
	}
	for _, e := range tmps {
		if os.Remove(filepath.Join(s.dir, "tmp", e.Name())) == nil {
			removed++
		}
	}
	cutoff := time.Now().Add(-minAge)
	err = filepath.WalkDir(s.dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if e.Name() == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		key := e.Name()
		if !validKey(key) || inUse(key) {
			return nil
		}
		if info, err := e.Info(); err != nil || info.ModTime().After(cutoff) {
			return nil
		}
		if os.Remove(p) == nil {
			removed++
		}
		return nil
	})
	if err != nil {
		return removed, fmt.Errorf("sweep blobs: %w", err)
	}
	return removed, nil
}

func (s *Store) path(key string) string {
	return filepath.Join(s.dir, key[:2], key)
}

func validKey(key string) bool {
	if len(key) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}
