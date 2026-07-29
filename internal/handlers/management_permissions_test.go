package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hcchien/reviz-accounting/internal/auth"
)

func TestManagementWebWritesRequireAccountant(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.Routes(mux)
	paths := []string{
		"/projects/1/quotes",
		"/projects/1/quotes/2/items",
		"/projects/1/quotes/2/revise",
		"/projects/1/quotes/2/accept",
		"/projects/1/quotes/2/delete",
		"/projects/1/roles",
		"/projects/1/roles/2/delete",
		"/projects/1/time-entries",
		"/projects/1/time-entries/2/delete",
		"/projects/1/receivables",
		"/projects/1/receivables/2/toggle",
		"/projects/1/receivables/2/delete",
		"/projects/1/costs",
		"/projects/1/costs/2/toggle",
		"/projects/1/costs/2/delete",
	}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req = req.WithContext(auth.WithUser(context.Background(), &auth.User{Role: auth.RoleViewer}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("viewer POST %s status = %d, want %d", path, rec.Code, http.StatusForbidden)
		}
	}
}
