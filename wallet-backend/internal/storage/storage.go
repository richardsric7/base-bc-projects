// Package storage abstracts blob storage for uploaded files (profile
// pictures, KYC documents, ...). The upstream project used Firebase Storage
// / GCS directly, wired in as a boot-time hard dependency alongside push
// notifications. Here it's a small interface with a local-disk default, so
// the service runs with no cloud project configured; swap in an S3/GCS/Azure
// Blob implementation for production by implementing Blob.
package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Blob is the minimal file storage contract components depend on.
type Blob interface {
	// Put stores data under key and returns a URL/path the caller can hand
	// back to a client.
	Put(key string, data io.Reader) (string, error)
	// Get retrieves previously stored data.
	Get(key string) (io.ReadCloser, error)
}

// LocalDiskBlob stores files under a local directory - fine for development
// and single-instance deployments, not for anything horizontally scaled.
type LocalDiskBlob struct {
	baseDir string
	baseURL string
}

// NewLocalDiskBlob returns a Blob backed by baseDir. baseURL is prefixed to
// keys when building the URL Put returns (e.g. "/files").
func NewLocalDiskBlob(baseDir, baseURL string) (*LocalDiskBlob, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, err
	}
	return &LocalDiskBlob{baseDir: baseDir, baseURL: baseURL}, nil
}

func (b *LocalDiskBlob) Put(key string, data io.Reader) (string, error) {
	fullPath := filepath.Join(b.baseDir, filepath.Clean("/"+key))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(fullPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, data); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s", b.baseURL, key), nil
}

func (b *LocalDiskBlob) Get(key string) (io.ReadCloser, error) {
	fullPath := filepath.Join(b.baseDir, filepath.Clean("/"+key))
	return os.Open(fullPath)
}
