package storage

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestLocalStoreRoundTrip(t *testing.T) {
	s, err := New(context.Background(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key := "attachments/42/receipt.pdf"
	if err := s.Put(context.Background(), key, "application/pdf", strings.NewReader("receipt")); err != nil {
		t.Fatal(err)
	}
	r, err := s.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != "receipt" {
		t.Fatalf("got %q", got)
	}
	if err := s.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(context.Background(), key); err == nil {
		t.Fatal("object remained after delete")
	}
}
