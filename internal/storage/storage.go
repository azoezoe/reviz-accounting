// Package storage persists receipt source files outside the accounting DB.
package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"cloud.google.com/go/storage"
)

type Store interface {
	Put(context.Context, string, string, io.Reader) error
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
	Close() error
}

// New uses GCS when bucket is configured. Without GCS_BUCKET it stores files
// locally, which keeps local development and tests self-contained.
func New(ctx context.Context, bucket, localDir string) (Store, error) {
	if bucket == "" {
		return &localStore{dir: localDir}, nil
	}
	c, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create GCS client: %w", err)
	}
	return &gcsStore{client: c, bucket: bucket}, nil
}

type gcsStore struct {
	client *storage.Client
	bucket string
}

func (s *gcsStore) Put(ctx context.Context, key, contentType string, r io.Reader) error {
	w := s.client.Bucket(s.bucket).Object(key).NewWriter(ctx)
	w.ContentType = contentType
	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}
func (s *gcsStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.client.Bucket(s.bucket).Object(key).NewReader(ctx)
}
func (s *gcsStore) Delete(ctx context.Context, key string) error {
	return s.client.Bucket(s.bucket).Object(key).Delete(ctx)
}
func (s *gcsStore) Close() error { return s.client.Close() }

type localStore struct{ dir string }

func (s *localStore) path(key string) string { return filepath.Join(s.dir, filepath.FromSlash(key)) }
func (s *localStore) Put(_ context.Context, key, _ string, r io.Reader) error {
	p := s.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
func (s *localStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	return os.Open(s.path(key))
}
func (s *localStore) Delete(_ context.Context, key string) error {
	err := os.Remove(s.path(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func (s *localStore) Close() error { return nil }
