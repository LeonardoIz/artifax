// Package blobs stores document contents on disk, addressed by SHA-256.
package blobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// Put writes data and returns its hex SHA-256. Existing blobs are left untouched.
func (s *Store) Put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	dst := s.path(key)
	if _, err := os.Stat(dst); err == nil {
		return key, nil
	}

	tmp, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), "blob-*")
	if err != nil {
		return "", fmt.Errorf("create temp blob: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write blob: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("sync blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close blob: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return "", fmt.Errorf("create blob shard: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", fmt.Errorf("store blob: %w", err)
	}
	return key, nil
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
