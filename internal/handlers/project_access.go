package handlers

import (
	"net/http"

	"github.com/hcchien/reviz-accounting/internal/auth"
	"github.com/hcchien/reviz-accounting/internal/models"
)

func (s *Server) requireProjectAccess(write bool, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.FromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if u.Role == auth.RoleOwner {
			next(w, r)
			return
		}
		ok, err := models.CanAccessProject(s.DB, parseInt64(r.PathValue("id")), u.ID, write)
		if err != nil {
			s.error500(w, err)
			return
		}
		if !ok {
			auth.WriteAlertRedirect(w, r, http.StatusForbidden, "您沒有此專案的存取權", "/projects")
			return
		}
		next(w, r)
	})
}

func (s *Server) projectRead(next http.HandlerFunc) http.Handler {
	return s.requireProjectAccess(false, next)
}
func (s *Server) projectWrite(next http.HandlerFunc) http.Handler {
	return s.requireProjectAccess(true, next)
}

func (s *Server) requireQuoteAccess(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.FromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if u.Role == auth.RoleOwner {
			next(w, r)
			return
		}
		var ok bool
		err := s.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM quotes WHERE id=$1 AND created_by_id=$2)`, parseInt64(r.PathValue("id")), u.ID).Scan(&ok)
		if err != nil {
			s.error500(w, err)
			return
		}
		if !ok {
			auth.WriteAlertRedirect(w, r, http.StatusForbidden, "您沒有此報價單的存取權", "/quotes")
			return
		}
		next(w, r)
	})
}

func (s *Server) requireTransactionAccess(write bool, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.FromContext(r.Context())
		if u == nil || u.Role == auth.RoleOwner {
			next(w, r)
			return
		}
		ok, err := models.CanAccessTransaction(s.DB, parseInt64(r.PathValue("id")), u.ID, write)
		if err != nil {
			s.error500(w, err)
			return
		}
		if !ok {
			auth.WriteAlertRedirect(w, r, http.StatusForbidden, "您沒有此交易的存取權", "/journal")
			return
		}
		next(w, r)
	})
}
