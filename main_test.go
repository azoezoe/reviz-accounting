package main

import (
	"testing"

	"github.com/hcchien/reviz-accounting/internal/handlers"
)

// A broken Go template is otherwise discovered only when a new Cloud Run
// revision starts. Parse all embedded pages in CI before building the image.
func TestEmbeddedTemplatesParse(t *testing.T) {
	if _, err := handlers.NewServer(nil, templatesFS, nil); err != nil {
		t.Fatalf("parse embedded templates: %v", err)
	}
}
