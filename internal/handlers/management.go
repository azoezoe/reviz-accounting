package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hcchien/reviz-accounting/internal/auth"
	"github.com/hcchien/reviz-accounting/internal/models"
	"github.com/hcchien/reviz-accounting/internal/money"
)

func (s *Server) projectManagementPage(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	project, err := models.GetProject(s.DB, id)
	if err != nil {
		http.Error(w, "找不到專案", http.StatusNotFound)
		return
	}
	quotes, err := models.ListProjectQuotes(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	proposalQuotes, err := s.loadProjectProposalQuotes(id)
	if err != nil {
		s.error500(w, err)
		return
	}
	roles, err := models.ListProjectRoles(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	entries, err := models.ListTimeEntries(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	receivables, err := models.ListProjectReceivables(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	costs, err := models.ListProjectCostItems(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	permissions, err := models.ListProjectPermissions(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	users, err := auth.ListUsers(s.DB)
	if err != nil {
		s.error500(w, err)
		return
	}
	var summary models.ProjectManagementSummary
	if len(proposalQuotes) > 0 {
		summary.QuoteTotalCents = proposalQuotes[0].TotalCents
	} else if len(quotes) > 0 {
		summary.QuoteTotalCents = quotes[0].TotalCents
	}
	for _, x := range entries {
		summary.EstimatedMinutes += x.EstimatedMinutes
		summary.ActualMinutes += x.ActualMinutes
		summary.EstimatedLaborCents += x.EstimatedCostCents
		summary.ActualLaborCents += x.ActualCostCents
	}
	for _, x := range receivables {
		summary.ReceivableCents += x.AmountCents
		if x.Received {
			summary.ReceivedCents += x.AmountCents
		}
	}
	for _, x := range costs {
		summary.CostCents += x.TWDCents
	}
	s.render(w, r, "project_management.html", map[string]any{
		"Title": "專案營運", "Crumbs": []string{"專案", project.Name, "營運"},
		"Active": "projects", "Project": project, "Quotes": quotes, "ProposalQuotes": proposalQuotes, "Roles": roles,
		"Entries": entries, "Receivables": receivables, "Costs": costs, "Summary": summary,
		"Permissions": permissions, "Users": users,
		"NextQuoteNo": models.NextQuoteNo(s.DB), "Today": time.Now().Format("2006-01-02"),
	})
}

func (s *Server) loadProjectProposalQuotes(projectID int64) ([]quoteView, error) {
	rows, err := s.DB.Query(`SELECT id FROM quotes WHERE project_id=$1 ORDER BY id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	quotes := make([]quoteView, 0, len(ids))
	for _, id := range ids {
		quote, err := s.loadQuote(id)
		if err != nil {
			return nil, err
		}
		quotes = append(quotes, quote)
	}
	return quotes, nil
}

func (s *Server) projectPermissionSave(w http.ResponseWriter, r *http.Request) {
	projectID, userID := parseInt64(r.PathValue("id")), parseInt64(r.FormValue("user_id"))
	level := r.FormValue("access_level")
	if userID <= 0 || (level != "read" && level != "write") {
		http.Error(w, "權限欄位格式錯誤", http.StatusBadRequest)
		return
	}
	if err := models.GrantProjectAccess(s.DB, projectID, userID, level); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/projects/"+r.PathValue("id")+"/management", http.StatusSeeOther)
}

func (s *Server) projectPermissionDelete(w http.ResponseWriter, r *http.Request) {
	projectID, userID := parseInt64(r.PathValue("id")), parseInt64(r.PathValue("userID"))
	if userID <= 0 {
		http.Error(w, "使用者不存在", http.StatusBadRequest)
		return
	}
	if err := models.RevokeProjectAccess(s.DB, projectID, userID); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/projects/"+r.PathValue("id")+"/management#permissions", http.StatusSeeOther)
}

func (s *Server) projectQuoteCreate(w http.ResponseWriter, r *http.Request) {
	pid := parseInt64(r.PathValue("id"))
	discount, err := strconv.ParseFloat(zeroIfEmpty(r.FormValue("discount_value")), 64)
	tax, taxErr := strconv.ParseFloat(zeroIfEmpty(r.FormValue("tax_rate")), 64)
	if strings.TrimSpace(r.FormValue("quote_no")) == "" || err != nil || taxErr != nil || discount < 0 || tax < 0 {
		http.Error(w, "報價單欄位格式錯誤", http.StatusBadRequest)
		return
	}
	q := &models.ProjectQuote{ProjectID: pid, QuoteNo: r.FormValue("quote_no"), Title: r.FormValue("title"),
		ClientName: r.FormValue("client_name"), IssuerName: r.FormValue("issuer_name"),
		Currency: defaultString(r.FormValue("currency"), "TWD"), DiscountType: defaultString(r.FormValue("discount_type"), "amount"),
		DiscountValue: discount, TaxRate: tax, Note: r.FormValue("note"), Status: "draft"}
	if _, err := s.DB.Exec(`SELECT 1 FROM projects WHERE id=$1`, pid); err != nil {
		s.error500(w, err)
		return
	}
	if _, err := models.CreateProjectQuote(s.DB, q); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectQuotePrint(w http.ResponseWriter, r *http.Request) {
	projectID, quoteID := parseInt64(r.PathValue("id")), parseInt64(r.PathValue("quoteID"))
	quotes, err := models.ListProjectQuotes(s.DB, projectID)
	if err != nil {
		s.error500(w, err)
		return
	}
	for _, quote := range quotes {
		if quote.ID == quoteID {
			s.renderStandalone(w, "project_quote_print.html", map[string]any{
				"Title": "報價單 " + quote.QuoteNo,
				"Quote": quote,
			})
			return
		}
	}
	http.Error(w, "找不到報價單", http.StatusNotFound)
}

func (s *Server) projectQuoteDelete(w http.ResponseWriter, r *http.Request) {
	if err := models.DeleteProjectQuote(s.DB, parseInt64(r.PathValue("quoteID")), parseInt64(r.PathValue("id"))); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectQuoteAccept(w http.ResponseWriter, r *http.Request) {
	projectID, err := models.AcceptQuoteAndCreateProject(s.DB, parseInt64(r.PathValue("quoteID")),
		parseInt64(r.PathValue("id")), r.FormValue("project_name"))
	if err != nil {
		http.Error(w, "建立執行專案失敗："+err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/projects/"+strconv.FormatInt(projectID, 10)+"/management", http.StatusSeeOther)
}

func (s *Server) projectQuoteRevise(w http.ResponseWriter, r *http.Request) {
	if _, err := models.ReviseProjectQuote(s.DB, parseInt64(r.PathValue("quoteID")), parseInt64(r.PathValue("id"))); err != nil {
		http.Error(w, "建立修訂版失敗："+err.Error(), http.StatusConflict)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectQuoteItemCreate(w http.ResponseWriter, r *http.Request) {
	pid, quoteID := parseInt64(r.PathValue("id")), parseInt64(r.PathValue("quoteID"))
	qty, err := strconv.ParseFloat(defaultString(r.FormValue("quantity"), "1"), 64)
	price, priceErr := money.ParseCents(r.FormValue("unit_price"))
	if strings.TrimSpace(r.FormValue("description")) == "" || err != nil || priceErr != nil || qty <= 0 || price < 0 {
		http.Error(w, "報價項目格式錯誤", http.StatusBadRequest)
		return
	}
	var editable bool
	if err := s.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_quotes WHERE id=$1 AND project_id=$2 AND status='draft')`, quoteID, pid).Scan(&editable); err != nil || !editable {
		http.Error(w, "找不到報價單", http.StatusNotFound)
		return
	}
	if _, err := models.AddQuoteItem(s.DB, &models.QuoteItem{QuoteID: quoteID, Description: r.FormValue("description"), Quantity: qty, Unit: defaultString(r.FormValue("unit"), "式"), UnitPriceCents: price}); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectRoleCreate(w http.ResponseWriter, r *http.Request) {
	pid := parseInt64(r.PathValue("id"))
	rate, e1 := money.ParseCents(zeroIfEmpty(r.FormValue("hourly_rate")))
	flat, e2 := money.ParseCents(zeroIfEmpty(r.FormValue("flat_fee")))
	if strings.TrimSpace(r.FormValue("name")) == "" || e1 != nil || e2 != nil || rate < 0 || flat < 0 {
		http.Error(w, "角色欄位格式錯誤", http.StatusBadRequest)
		return
	}
	if _, err := models.CreateProjectRole(s.DB, &models.ProjectRole{ProjectID: pid, Name: r.FormValue("name"), HourlyRateCents: rate, FlatFeeCents: flat, IsSelf: r.FormValue("is_self") == "1"}); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectRoleDelete(w http.ResponseWriter, r *http.Request) {
	if err := models.DeleteProjectRole(s.DB, parseInt64(r.PathValue("roleID")), parseInt64(r.PathValue("id"))); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectTimeEntryCreate(w http.ResponseWriter, r *http.Request) {
	pid := parseInt64(r.PathValue("id"))
	estimated, e1 := parseHours(r.FormValue("estimated_hours"))
	actual, e2 := parseHours(r.FormValue("actual_hours"))
	if parseInt64(r.FormValue("role_id")) == 0 || r.FormValue("work_date") == "" || e1 != nil || e2 != nil {
		http.Error(w, "工時欄位格式錯誤", http.StatusBadRequest)
		return
	}
	_, err := models.CreateTimeEntry(s.DB, &models.TimeEntry{ProjectID: pid, RoleID: parseInt64(r.FormValue("role_id")),
		WorkDate: r.FormValue("work_date"), Description: r.FormValue("description"), EstimatedMinutes: estimated, ActualMinutes: actual})
	if err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectTimeEntryDelete(w http.ResponseWriter, r *http.Request) {
	if err := models.DeleteTimeEntry(s.DB, parseInt64(r.PathValue("entryID")), parseInt64(r.PathValue("id"))); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectReceivableCreate(w http.ResponseWriter, r *http.Request) {
	amount, err := money.ParseCents(r.FormValue("amount"))
	if strings.TrimSpace(r.FormValue("name")) == "" || err != nil || amount < 0 {
		http.Error(w, "應收款格式錯誤", 400)
		return
	}
	if _, err := models.CreateProjectReceivable(s.DB, &models.ProjectReceivable{ProjectID: parseInt64(r.PathValue("id")), Name: r.FormValue("name"), AmountCents: amount, ExpectedDate: r.FormValue("expected_date"), Note: r.FormValue("note")}); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectReceivableToggle(w http.ResponseWriter, r *http.Request) {
	if err := models.ToggleProjectReceivable(s.DB, parseInt64(r.PathValue("receivableID")), parseInt64(r.PathValue("id"))); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectReceivableDelete(w http.ResponseWriter, r *http.Request) {
	if err := models.DeleteProjectReceivable(s.DB, parseInt64(r.PathValue("receivableID")), parseInt64(r.PathValue("id"))); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectCostCreate(w http.ResponseWriter, r *http.Request) {
	amount, e1 := money.ParseCents(r.FormValue("amount"))
	rate, e2 := strconv.ParseFloat(defaultString(r.FormValue("exchange_rate"), "1"), 64)
	if strings.TrimSpace(r.FormValue("name")) == "" || e1 != nil || e2 != nil || amount < 0 || rate <= 0 {
		http.Error(w, "成本欄位格式錯誤", 400)
		return
	}
	if _, err := models.CreateProjectCostItem(s.DB, &models.ProjectCostItem{ProjectID: parseInt64(r.PathValue("id")), Name: r.FormValue("name"), AmountCents: amount, Currency: defaultString(r.FormValue("currency"), "TWD"), ExchangeRate: rate, IsLabor: r.FormValue("is_labor") == "1", Note: r.FormValue("note")}); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectCostToggle(w http.ResponseWriter, r *http.Request) {
	if err := models.ToggleProjectCostItem(s.DB, parseInt64(r.PathValue("costID")), parseInt64(r.PathValue("id"))); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func (s *Server) projectCostDelete(w http.ResponseWriter, r *http.Request) {
	if err := models.DeleteProjectCostItem(s.DB, parseInt64(r.PathValue("costID")), parseInt64(r.PathValue("id"))); err != nil {
		s.error500(w, err)
		return
	}
	redirectManagement(w, r)
}

func parseHours(raw string) (int, error) {
	n, err := strconv.ParseFloat(zeroIfEmpty(raw), 64)
	if err != nil || n < 0 {
		return 0, strconv.ErrSyntax
	}
	return int(n*60 + 0.5), nil
}

func zeroIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0"
	}
	return s
}
func defaultString(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
func redirectManagement(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/projects/"+r.PathValue("id")+"/management", http.StatusSeeOther)
}
