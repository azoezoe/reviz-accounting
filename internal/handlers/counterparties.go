package handlers

import (
	"net/http"
	"strings"

	"github.com/hcchien/reviz-accounting/internal/models"
)

func (s *Server) counterpartiesList(w http.ResponseWriter, r *http.Request) {
	items, err := models.ListCounterparties(s.DB, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		s.error500(w, err)
		return
	}
	s.render(w, r, "counterparties.html", map[string]any{
		"Title": "交易對象", "Crumbs": []string{"交易對象"}, "Counterparties": items,
		"Search": r.URL.Query().Get("q"), "Active": "counterparties",
	})
}

func counterpartyFromForm(r *http.Request) *models.Counterparty {
	return &models.Counterparty{Name: strings.TrimSpace(r.FormValue("name")), TaxID: strings.TrimSpace(r.FormValue("tax_id")), ContactName: strings.TrimSpace(r.FormValue("contact_name")), Phone: strings.TrimSpace(r.FormValue("phone")), Address: strings.TrimSpace(r.FormValue("address")), Email: strings.TrimSpace(r.FormValue("email")), BankName: strings.TrimSpace(r.FormValue("bank_name")), BankAccountName: strings.TrimSpace(r.FormValue("bank_account_name")), BankAccountNo: strings.TrimSpace(r.FormValue("bank_account_no"))}
}

func (s *Server) counterpartyCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.error500(w, err)
		return
	}
	c := counterpartyFromForm(r)
	if c.Name == "" {
		http.Error(w, "交易對象名稱不可空白", http.StatusBadRequest)
		return
	}
	if _, err := models.CreateCounterparty(s.DB, c); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/counterparties", http.StatusSeeOther)
}

func (s *Server) counterpartyUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.error500(w, err)
		return
	}
	c := counterpartyFromForm(r)
	c.ID = parseInt64(r.PathValue("id"))
	if c.Name == "" {
		http.Error(w, "交易對象名稱不可空白", http.StatusBadRequest)
		return
	}
	if err := models.UpdateCounterparty(s.DB, c); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/counterparties", http.StatusSeeOther)
}

func (s *Server) counterpartyDelete(w http.ResponseWriter, r *http.Request) {
	if err := models.DeleteCounterparty(s.DB, parseInt64(r.PathValue("id"))); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/counterparties", http.StatusSeeOther)
}
