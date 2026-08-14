package auth

import (
	"database/sql"
	"net/http"
)

// Attach is the outermost middleware: it loads the current user from the
// session cookie (if any) and attaches it to the request context, but does
// NOT enforce login. Use it once around the whole mux.
func Attach(d *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, _ := LoadUser(d, r); u != nil {
			r = r.WithContext(WithUser(r.Context(), u))
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth redirects to /login when no user is attached to the request.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if FromContext(r.Context()) == nil {
			next := r.URL.Path
			if r.URL.RawQuery != "" {
				next += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, "/login?next="+next, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole returns a middleware that responds with an alert + redirect if
// the user role is below the given minimum (so the user keeps seeing the page
// they were on rather than being thrown to a bare 403 screen).
func RequireRole(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := FromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if !u.AtLeast(role) {
			redirect := "/dashboard"
			if role == RoleOwner {
				redirect = "/projects"
			}
			WriteAlertRedirect(w, r, http.StatusForbidden, "權限不足", redirect)
			return
		}
		next.ServeHTTP(w, r)
	})
}
