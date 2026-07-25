package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/hcchien/reviz-accounting/internal/auth"
)

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if auth.FromContext(r.Context()) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	data := map[string]any{
		"Title":           "登入",
		"Next":            r.URL.Query().Get("next"),
		"PasswordUpdated": r.URL.Query().Get("password_updated") == "1",
		"Active":          "",
	}
	s.renderStandalone(w, "login.html", data)
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.error500(w, err)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	next := r.FormValue("next")
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/dashboard"
	}

	u, hash, err := auth.GetUserByUsername(s.DB, username)
	if errors.Is(err, sql.ErrNoRows) || !u.Active || !auth.VerifyPassword(hash, password) {
		s.renderStandalone(w, "login.html", map[string]any{
			"Title":    "登入",
			"Next":     next,
			"Error":    "帳號或密碼錯誤",
			"Username": username,
			"Active":   "",
		})
		return
	}
	if err != nil {
		s.error500(w, err)
		return
	}
	sid, err := auth.CreateSession(s.DB, u.ID, r.UserAgent(), clientIP(r))
	if err != nil {
		s.error500(w, err)
		return
	}
	auth.SetSessionCookie(w, sid)
	// Defensively re-parse next as a URL path to avoid open redirect.
	if parsed, err := url.Parse(next); err == nil && parsed.Host == "" {
		http.Redirect(w, r, parsed.RequestURI(), http.StatusSeeOther)
	} else {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	sid := auth.CookieValue(r)
	_ = auth.DeleteSession(s.DB, sid)
	auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) passwordPage(w http.ResponseWriter, r *http.Request) {
	s.renderPasswordPage(w, r, "")
}

func (s *Server) passwordUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.error500(w, err)
		return
	}
	current := auth.FromContext(r.Context())
	if current == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_, hash, err := auth.GetUserByUsername(s.DB, current.Username)
	if err != nil {
		s.error500(w, err)
		return
	}
	if !auth.VerifyPassword(hash, r.FormValue("current_password")) {
		s.renderPasswordPage(w, r, "目前密碼不正確")
		return
	}
	newPassword := r.FormValue("new_password")
	if newPassword != r.FormValue("confirm_password") {
		s.renderPasswordPage(w, r, "兩次輸入的新密碼不一致")
		return
	}
	if err := auth.UpdateUserPassword(s.DB, current.ID, newPassword); err != nil {
		s.renderPasswordPage(w, r, err.Error())
		return
	}
	// End every existing session, including this one, after a password change.
	_ = auth.DeleteSessionsForUser(s.DB, current.ID)
	auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/login?password_updated=1", http.StatusSeeOther)
}

func (s *Server) renderPasswordPage(w http.ResponseWriter, r *http.Request, errMsg string) {
	s.render(w, r, "password.html", map[string]any{
		"Title":  "變更密碼",
		"Crumbs": []string{"個人設定", "變更密碼"},
		"Active": "password",
		"Error":  errMsg,
	})
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.Index(v, ","); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	return r.RemoteAddr
}
