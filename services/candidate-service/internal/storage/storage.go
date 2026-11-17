// Package storage stores resume files. Week 1 uses the local filesystem (a
// Docker volume); swapping in S3/MinIO later only means a new BlobStore impl.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BlobStore saves and loads opaque files by key.
type BlobStore interface {
	Save(ctx context.Context, key string, r io.Reader) (int64, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// ErrNotFound is returned when a key has no file.
var ErrNotFound = errors.New("blob not found")

// Local stores files under a root directory.
type Local struct{ root string }

// NewLocal creates root if needed.
func NewLocal(root string) (*Local, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	return &Local{root: root}, nil
}

// path resolves key inside root and refuses anything that escapes it
// (defence against path traversal such as "../../etc/passwd").
func (l *Local) path(key string) (string, error) {
	p := filepath.Join(l.root, filepath.Clean("/"+key))
	if !strings.HasPrefix(p, filepath.Clean(l.root)+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid storage key %q", key)
	}
	return p, nil
}

// Save writes to a temp file then renames it, so readers never see a
// half-written file even if the process dies mid-upload.
func (l *Local) Save(_ context.Context, key string, r io.Reader) (int64, error) {
	p, err := l.path(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return 0, err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		_ = os.Remove(tmp.Name())
		return 0, err
	}
	return n, nil
}

func (l *Local) Open(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

func (l *Local) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
