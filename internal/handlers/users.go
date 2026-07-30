package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/hcchien/reviz-accounting/internal/auth"
)

func (s *Server) usersList(w http.ResponseWriter, r *http.Request) {
	users, err := auth.ListUsers(s.DB)
	if err != nil {
		s.error500(w, err)
		return
	}
	flash := r.URL.Query().Get("ok")
	s.render(w, r, "users.html", map[string]any{
		"Title":   "使用者",
		"Crumbs":  []string{"使用者"},
		"Users":   users,
		"Roles":   []string{auth.RoleOwner, auth.RoleAccountant, auth.RoleViewer},
		"Active":  "users",
		"Flash":   flash,
		"Current": auth.FromContext(r.Context()),
	})
}

func (s *Server) userCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.error500(w, err)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	role := r.FormValue("role")
	if _, err := auth.CreateUserWithName(s.DB, username, password, role, r.FormValue("full_name")); err != nil {
		s.userError(w, r, err)
		return
	}
	http.Redirect(w, r, "/users?ok=created", http.StatusSeeOther)
}

func (s *Server) userUpdate(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	if err := r.ParseForm(); err != nil {
		s.error500(w, err)
		return
	}
	current := auth.FromContext(r.Context())
	if _, ok := r.Form["full_name"]; ok {
		if err := auth.UpdateUserFullName(s.DB, id, r.FormValue("full_name")); err != nil {
			s.userError(w, r, err)
			return
		}
	}
	if role := r.FormValue("role"); role != "" {
		if err := s.guardLastOwner(id, role, true); err != nil {
			s.userError(w, r, err)
			return
		}
		if err := auth.UpdateUserRole(s.DB, id, role); err != nil {
			s.userError(w, r, err)
			return
		}
	}
	if v := r.FormValue("active_set"); v != "" {
		active := v == "1"
		if !active {
			if err := s.guardLastOwner(id, "", false); err != nil {
				s.userError(w, r, err)
				return
			}
			if current != nil && current.ID == id {
				s.userError(w, r, errors.New("不能停用自己的帳號"))
				return
			}
		}
		if err := auth.SetUserActive(s.DB, id, active); err != nil {
			s.userError(w, r, err)
			return
		}
		if !active {
			_ = auth.DeleteSessionsForUser(s.DB, id)
		}
	}
	if pw := r.FormValue("password"); pw != "" {
		if err := auth.UpdateUserPassword(s.DB, id, pw); err != nil {
			s.userError(w, r, err)
			return
		}
		// Logging the user out of other devices after a password change.
		_ = auth.DeleteSessionsForUser(s.DB, id)
	}
	http.Redirect(w, r, "/users?ok=updated", http.StatusSeeOther)
}

func (s *Server) userDelete(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	current := auth.FromContext(r.Context())
	if current != nil && current.ID == id {
		s.userError(w, r, errors.New("不能刪除自己的帳號"))
		return
	}
	if err := s.guardLastOwner(id, "", false); err != nil {
		s.userError(w, r, err)
		return
	}
	if err := auth.DeleteUser(s.DB, id); err != nil {
		s.userError(w, r, err)
		return
	}
	http.Redirect(w, r, "/users?ok=deleted", http.StatusSeeOther)
}

// guardLastOwner prevents demoting/disabling/deleting the only remaining
// active owner. When newRoleIfChange is non-empty, the operation is a role
// change; otherwise it is deactivation/deletion of user id.
func (s *Server) guardLastOwner(id int64, newRoleIfChange string, _ bool) error {
	u, err := auth.GetUser(s.DB, id)
	if err != nil {
		return err
	}
	if u.Role != auth.RoleOwner || !u.Active {
		return nil
	}
	n, err := auth.CountActiveOwners(s.DB)
	if err != nil {
		return err
	}
	if n <= 1 {
		if newRoleIfChange == auth.RoleOwner {
			return nil
		}
		return fmt.Errorf("無法執行：系統至少需保留一位 owner")
	}
	return nil
}

func (s *Server) userError(w http.ResponseWriter, r *http.Request, err error) {
	auth.WriteAlertRedirect(w, r, http.StatusBadRequest, err.Error(), "/users")
}
