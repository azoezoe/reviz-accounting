package handlers

import (
	"net/http"
	"time"

	"github.com/hcchien/reviz-accounting/internal/models"
)

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	all, err := models.AllSettings(s.DB)
	if err != nil {
		s.error500(w, err)
		return
	}
	s.render(w, r, "settings.html", map[string]any{
		"Title":        "設定",
		"Crumbs":       []string{"設定"},
		"Settings":     all,
		"CurrentMonth": time.Now().Format("2006-01"),
		"Active":       "settings",
	})
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.error500(w, err)
		return
	}
	for _, k := range []string{"company_name", "company_contact", "company_email", "company_tax_id", "fiscal_year"} {
		if v := r.FormValue(k); v != "" {
			if err := models.SetSetting(s.DB, k, v); err != nil {
				s.error500(w, err)
				return
			}
		}
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
