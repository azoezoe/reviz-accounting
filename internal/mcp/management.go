package mcp

import (
	"strings"

	"github.com/hcchien/reviz-accounting/internal/models"
)

func (s *Server) projectManagement(projectID int64) (any, error) {
	if projectID <= 0 {
		return nil, fmtErr("project_id 必填")
	}
	project, err := models.GetProject(s.DB, projectID)
	if err != nil {
		return nil, fmtErr("找不到專案")
	}
	quotes, err := models.ListProjectQuotes(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	roles, err := models.ListProjectRoles(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	entries, err := models.ListTimeEntries(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	receivables, err := models.ListProjectReceivables(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	costs, err := models.ListProjectCostItems(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	return content(map[string]any{
		"project":      project,
		"quotes":       quotes,
		"roles":        roles,
		"time_entries": entries,
		"receivables":  receivables,
		"costs":        costs,
	}, nil)
}

func (s *Server) projectManagementWrite(name string, a map[string]any) (any, error) {
	switch name {
	case "create_project_quote":
		return s.createProjectQuote(a)
	case "create_quote_item":
		return s.createQuoteItem(a)
	case "revise_project_quote":
		id, err := models.ReviseProjectQuote(s.DB, numID(a, "quote_id"), numID(a, "project_id"))
		return content(map[string]any{"quote_id": id, "version_created": true}, err)
	case "accept_project_quote":
		id, err := models.AcceptQuoteAndCreateProject(s.DB, numID(a, "quote_id"), numID(a, "project_id"), str(a, "project_name"))
		return content(map[string]any{"execution_project_id": id, "quote_accepted": true, "budget_allocated": true}, err)
	case "create_project_role":
		return s.createProjectRole(a)
	case "create_time_entry":
		return s.createTimeEntry(a)
	case "create_project_receivable":
		return s.createProjectReceivable(a)
	case "toggle_project_receivable":
		err := models.ToggleProjectReceivable(s.DB, numID(a, "receivable_id"), numID(a, "project_id"))
		return content(map[string]any{"receivable_id": numID(a, "receivable_id"), "toggled": true}, err)
	case "create_project_cost":
		return s.createProjectCost(a)
	case "toggle_project_cost":
		err := models.ToggleProjectCostItem(s.DB, numID(a, "cost_id"), numID(a, "project_id"))
		return content(map[string]any{"cost_id": numID(a, "cost_id"), "toggled": true}, err)
	}
	return nil, fmtErr("unknown project management tool")
}

func (s *Server) createProjectQuote(a map[string]any) (any, error) {
	projectID := numID(a, "project_id")
	if projectID <= 0 {
		return nil, fmtErr("project_id 必填")
	}
	if _, err := models.GetProject(s.DB, projectID); err != nil {
		return nil, fmtErr("找不到專案")
	}
	quoteNo := strings.TrimSpace(str(a, "quote_no"))
	if quoteNo == "" {
		quoteNo = models.NextQuoteNo(s.DB)
	}
	discountType := defaultText(str(a, "discount_type"), "amount")
	if discountType != "amount" && discountType != "percent" {
		return nil, fmtErr("discount_type 必須是 amount 或 percent")
	}
	discount, tax := num(a, "discount_value"), num(a, "tax_rate")
	if _, ok := a["tax_rate"]; !ok {
		tax = 5
	}
	if discount < 0 || tax < 0 {
		return nil, fmtErr("discount_value 與 tax_rate 不可為負數")
	}
	id, err := models.CreateProjectQuote(s.DB, &models.ProjectQuote{
		ProjectID:     projectID,
		QuoteNo:       quoteNo,
		Title:         str(a, "title"),
		ClientName:    str(a, "client_name"),
		IssuerName:    str(a, "issuer_name"),
		Currency:      defaultText(str(a, "currency"), "TWD"),
		DiscountType:  discountType,
		DiscountValue: discount,
		TaxRate:       tax,
		Note:          str(a, "note"),
		Status:        "draft",
	})
	return content(map[string]any{"quote_id": id, "quote_no": quoteNo, "version_no": 1}, err)
}

func (s *Server) createQuoteItem(a map[string]any) (any, error) {
	projectID, quoteID := numID(a, "project_id"), numID(a, "quote_id")
	description := strings.TrimSpace(str(a, "description"))
	quantity, unitPrice := num(a, "quantity"), numID(a, "unit_price_cents")
	if _, ok := a["quantity"]; !ok {
		quantity = 1
	}
	if projectID <= 0 || quoteID <= 0 || description == "" || quantity <= 0 || unitPrice < 0 {
		return nil, fmtErr("project_id、quote_id、description、正數 quantity 與非負 unit_price_cents 必填")
	}
	var editable bool
	if err := s.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_quotes WHERE id=$1 AND project_id=$2 AND status='draft')`, quoteID, projectID).Scan(&editable); err != nil {
		return nil, err
	}
	if !editable {
		return nil, fmtErr("報價不存在或版本已鎖定")
	}
	id, err := models.AddQuoteItem(s.DB, &models.QuoteItem{
		QuoteID: quoteID, Description: description, Quantity: quantity,
		Unit: defaultText(str(a, "unit"), "式"), UnitPriceCents: unitPrice,
	})
	return content(map[string]any{"item_id": id, "quote_id": quoteID}, err)
}

func (s *Server) createProjectRole(a map[string]any) (any, error) {
	projectID, name := numID(a, "project_id"), strings.TrimSpace(str(a, "name"))
	rate, flat := numID(a, "hourly_rate_cents"), numID(a, "flat_fee_cents")
	if projectID <= 0 || name == "" || rate < 0 || flat < 0 {
		return nil, fmtErr("project_id、name 與非負金額必填")
	}
	id, err := models.CreateProjectRole(s.DB, &models.ProjectRole{
		ProjectID: projectID, Name: name, HourlyRateCents: rate,
		FlatFeeCents: flat, IsSelf: boolean(a, "is_self"),
	})
	return content(map[string]any{"role_id": id, "project_id": projectID}, err)
}

func (s *Server) createTimeEntry(a map[string]any) (any, error) {
	projectID, roleID := numID(a, "project_id"), numID(a, "role_id")
	estimated, actual := int(num(a, "estimated_minutes")), int(num(a, "actual_minutes"))
	if projectID <= 0 || roleID <= 0 || strings.TrimSpace(str(a, "work_date")) == "" || estimated < 0 || actual < 0 {
		return nil, fmtErr("project_id、role_id、work_date 與非負分鐘數必填")
	}
	id, err := models.CreateTimeEntry(s.DB, &models.TimeEntry{
		ProjectID: projectID, RoleID: roleID, WorkDate: str(a, "work_date"),
		Description: str(a, "description"), EstimatedMinutes: estimated, ActualMinutes: actual,
	})
	return content(map[string]any{"time_entry_id": id, "project_id": projectID}, err)
}

func (s *Server) createProjectReceivable(a map[string]any) (any, error) {
	projectID, amount := numID(a, "project_id"), numID(a, "amount_cents")
	name := strings.TrimSpace(str(a, "name"))
	if projectID <= 0 || name == "" || amount < 0 {
		return nil, fmtErr("project_id、name 與非負 amount_cents 必填")
	}
	id, err := models.CreateProjectReceivable(s.DB, &models.ProjectReceivable{
		ProjectID: projectID, Name: name, AmountCents: amount,
		ExpectedDate: str(a, "expected_date"), Note: str(a, "note"),
	})
	return content(map[string]any{"receivable_id": id, "project_id": projectID}, err)
}

func (s *Server) createProjectCost(a map[string]any) (any, error) {
	projectID, amount := numID(a, "project_id"), numID(a, "amount_cents")
	name, rate := strings.TrimSpace(str(a, "name")), num(a, "exchange_rate")
	if _, ok := a["exchange_rate"]; !ok {
		rate = 1
	}
	if projectID <= 0 || name == "" || amount < 0 || rate <= 0 {
		return nil, fmtErr("project_id、name、非負 amount_cents 與正數 exchange_rate 必填")
	}
	id, err := models.CreateProjectCostItem(s.DB, &models.ProjectCostItem{
		ProjectID: projectID, Name: name, AmountCents: amount,
		Currency: defaultText(str(a, "currency"), "TWD"), ExchangeRate: rate,
		IsLabor: boolean(a, "is_labor"), Note: str(a, "note"),
	})
	return content(map[string]any{"cost_id": id, "project_id": projectID}, err)
}

func defaultText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func boolean(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}
