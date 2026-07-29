package models

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type ProjectQuote struct {
	ID, ProjectID                                      int64
	QuoteNo, Title, ClientName, IssuerName             string
	ProposalKey                                        string
	VersionNo                                          int
	ParentQuoteID                                      sql.NullInt64
	Currency, DiscountType, Note, Status               string
	DiscountValue, TaxRate                             float64
	CreatedAt, UpdatedAt                               string
	Items                                              []QuoteItem
	SubtotalCents, DiscountCents, TaxCents, TotalCents int64
}

type QuoteItem struct {
	ID, QuoteID, UnitPriceCents int64
	Description, Unit           string
	Quantity                    float64
	SortOrder                   int
	LineTotalCents              int64
}

type ProjectRole struct {
	ID, ProjectID, HourlyRateCents, FlatFeeCents int64
	Name                                         string
	IsSelf                                       bool
}

type TimeEntry struct {
	ID, ProjectID, RoleID               int64
	RoleName, WorkDate, Description     string
	EstimatedMinutes, ActualMinutes     int
	EstimatedCostCents, ActualCostCents int64
}

type ProjectReceivable struct {
	ID, ProjectID, AmountCents             int64
	Name, ExpectedDate, ReceivedDate, Note string
	Received                               bool
}

type ProjectCostItem struct {
	ID, ProjectID, AmountCents, TWDCents int64
	Name, Currency, PaidDate, Note       string
	ExchangeRate                         float64
	IsLabor, Paid                        bool
}

type ProjectManagementSummary struct {
	QuoteTotalCents, ReceivableCents, ReceivedCents  int64
	CostCents, EstimatedLaborCents, ActualLaborCents int64
	EstimatedMinutes, ActualMinutes                  int
}

func NextQuoteNo(d *sql.DB) string {
	year := time.Now().Year()
	prefix := fmt.Sprintf("Q-%d-", year)
	var n int
	_ = d.QueryRow(`SELECT COUNT(*)+1 FROM project_quotes WHERE quote_no LIKE $1`, prefix+"%").Scan(&n)
	return fmt.Sprintf("%s%03d", prefix, n)
}

func ListProjectQuotes(d *sql.DB, projectID int64) ([]ProjectQuote, error) {
	rows, err := d.Query(`SELECT id,project_id,quote_no,title,client_name,issuer_name,proposal_key,version_no,parent_quote_id,currency,
	 discount_type,discount_value,tax_rate,note,status,created_at,updated_at
	 FROM project_quotes WHERE project_id=$1 ORDER BY id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectQuote
	for rows.Next() {
		var q ProjectQuote
		if err := rows.Scan(&q.ID, &q.ProjectID, &q.QuoteNo, &q.Title, &q.ClientName, &q.IssuerName,
			&q.ProposalKey, &q.VersionNo, &q.ParentQuoteID,
			&q.Currency, &q.DiscountType, &q.DiscountValue, &q.TaxRate, &q.Note, &q.Status,
			&q.CreatedAt, &q.UpdatedAt); err != nil {
			return nil, err
		}
		q.Items, err = ListQuoteItems(d, q.ID)
		if err != nil {
			return nil, err
		}
		calculateQuote(&q)
		out = append(out, q)
	}
	return out, rows.Err()
}

func ListQuoteItems(d *sql.DB, quoteID int64) ([]QuoteItem, error) {
	rows, err := d.Query(`SELECT id,quote_id,description,quantity,unit,unit_price_cents,sort_order
	 FROM project_quote_items WHERE quote_id=$1 ORDER BY sort_order,id`, quoteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuoteItem
	for rows.Next() {
		var item QuoteItem
		if err := rows.Scan(&item.ID, &item.QuoteID, &item.Description, &item.Quantity, &item.Unit,
			&item.UnitPriceCents, &item.SortOrder); err != nil {
			return nil, err
		}
		item.LineTotalCents = int64(item.Quantity * float64(item.UnitPriceCents))
		out = append(out, item)
	}
	return out, rows.Err()
}

func calculateQuote(q *ProjectQuote) {
	for _, item := range q.Items {
		q.SubtotalCents += item.LineTotalCents
	}
	if q.DiscountType == "percent" {
		q.DiscountCents = int64(float64(q.SubtotalCents) * q.DiscountValue / 100)
	} else {
		q.DiscountCents = int64(q.DiscountValue * 100)
	}
	if q.DiscountCents > q.SubtotalCents {
		q.DiscountCents = q.SubtotalCents
	}
	taxable := q.SubtotalCents - q.DiscountCents
	q.TaxCents = int64(float64(taxable) * q.TaxRate / 100)
	q.TotalCents = taxable + q.TaxCents
}

func CreateProjectQuote(d *sql.DB, q *ProjectQuote) (int64, error) {
	if q.ProposalKey == "" {
		q.ProposalKey = q.QuoteNo
	}
	if q.VersionNo == 0 {
		q.VersionNo = 1
	}
	return insertID(d, `INSERT INTO project_quotes(project_id,quote_no,title,client_name,issuer_name,proposal_key,version_no,parent_quote_id,currency,
	 discount_type,discount_value,tax_rate,note,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`,
		q.ProjectID, q.QuoteNo, q.Title, q.ClientName, q.IssuerName, q.ProposalKey, q.VersionNo, q.ParentQuoteID, q.Currency,
		q.DiscountType, q.DiscountValue, q.TaxRate, q.Note, q.Status)
}

func AddQuoteItem(d *sql.DB, item *QuoteItem) (int64, error) {
	return insertID(d, `INSERT INTO project_quote_items(quote_id,description,quantity,unit,unit_price_cents,sort_order)
	 VALUES($1,$2,$3,$4,$5,(SELECT COUNT(*) FROM project_quote_items WHERE quote_id=$1)) RETURNING id`,
		item.QuoteID, item.Description, item.Quantity, item.Unit, item.UnitPriceCents)
}

func DeleteProjectQuote(d *sql.DB, id, projectID int64) error {
	_, err := d.Exec(`DELETE FROM project_quotes WHERE id=$1 AND project_id=$2`, id, projectID)
	return err
}

func ReviseProjectQuote(d *sql.DB, quoteID, projectID int64) (int64, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var q ProjectQuote
	err = tx.QueryRow(`SELECT quote_no,title,client_name,issuer_name,proposal_key,version_no,currency,
	 discount_type,discount_value,tax_rate,note FROM project_quotes WHERE id=$1 AND project_id=$2`,
		quoteID, projectID).Scan(&q.QuoteNo, &q.Title, &q.ClientName, &q.IssuerName, &q.ProposalKey,
		&q.VersionNo, &q.Currency, &q.DiscountType, &q.DiscountValue, &q.TaxRate, &q.Note)
	if err != nil {
		return 0, err
	}
	var nextVersion int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(version_no),0)+1 FROM project_quotes WHERE proposal_key=$1`, q.ProposalKey).Scan(&nextVersion); err != nil {
		return 0, err
	}
	nextNo := fmt.Sprintf("%s-R%d", strings.Split(q.QuoteNo, "-R")[0], nextVersion)
	var newID int64
	err = tx.QueryRow(`INSERT INTO project_quotes(project_id,quote_no,title,client_name,issuer_name,proposal_key,version_no,parent_quote_id,
	 currency,discount_type,discount_value,tax_rate,note,status)
	 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'draft') RETURNING id`,
		projectID, nextNo, q.Title, q.ClientName, q.IssuerName, q.ProposalKey, nextVersion, quoteID,
		q.Currency, q.DiscountType, q.DiscountValue, q.TaxRate, q.Note).Scan(&newID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO project_quote_items(quote_id,description,quantity,unit,unit_price_cents,sort_order)
	 SELECT $1,description,quantity,unit,unit_price_cents,sort_order FROM project_quote_items WHERE quote_id=$2`, newID, quoteID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE project_quotes
	 SET status=CASE WHEN status='draft' THEN 'sent' ELSE status END,updated_at=CURRENT_TIMESTAMP::text
	 WHERE id=$1`, quoteID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return newID, nil
}

// AcceptQuoteAndCreateProject turns a proposal workspace into an execution
// project in one transaction. Roles, estimates, receivables and costs are
// copied, and the accepted quote total becomes a fully allocated budget.
func AcceptQuoteAndCreateProject(d *sql.DB, quoteID, sourceProjectID int64, requestedName string) (int64, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var quote ProjectQuote
	err = tx.QueryRow(`SELECT id,project_id,quote_no,title,client_name,issuer_name,proposal_key,version_no,parent_quote_id,currency,
	 discount_type,discount_value,tax_rate,note,status,created_at,updated_at
	 FROM project_quotes WHERE id=$1 AND project_id=$2 FOR UPDATE`, quoteID, sourceProjectID).
		Scan(&quote.ID, &quote.ProjectID, &quote.QuoteNo, &quote.Title, &quote.ClientName, &quote.IssuerName,
			&quote.ProposalKey, &quote.VersionNo, &quote.ParentQuoteID,
			&quote.Currency, &quote.DiscountType, &quote.DiscountValue, &quote.TaxRate, &quote.Note,
			&quote.Status, &quote.CreatedAt, &quote.UpdatedAt)
	if err != nil {
		return 0, err
	}
	if quote.Status == "accepted" {
		return 0, fmt.Errorf("quote %s was already accepted", quote.QuoteNo)
	}

	var sourceName, startDate, endDate, projectNote sql.NullString
	if err := tx.QueryRow(`SELECT name,start_date,end_date,note FROM projects WHERE id=$1`, sourceProjectID).
		Scan(&sourceName, &startDate, &endDate, &projectNote); err != nil {
		return 0, err
	}
	name := strings.TrimSpace(requestedName)
	if name == "" {
		name = strings.TrimSpace(quote.Title)
		if name == "" {
			name = sourceName.String + " · 執行"
		}
	}
	baseName := name
	for suffix := 2; ; suffix++ {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM projects WHERE name=$1)`, name).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			break
		}
		name = fmt.Sprintf("%s (%d)", baseName, suffix)
	}
	var newProjectID int64
	if err := tx.QueryRow(`INSERT INTO projects(name,start_date,end_date,note) VALUES($1,$2,$3,$4) RETURNING id`,
		name, startDate, endDate, projectNote).Scan(&newProjectID); err != nil {
		return 0, err
	}

	itemRows, err := tx.Query(`SELECT description,quantity,unit,unit_price_cents,sort_order FROM project_quote_items WHERE quote_id=$1 ORDER BY sort_order,id`, quoteID)
	if err != nil {
		return 0, err
	}
	for itemRows.Next() {
		var item QuoteItem
		if err := itemRows.Scan(&item.Description, &item.Quantity, &item.Unit, &item.UnitPriceCents, &item.SortOrder); err != nil {
			itemRows.Close()
			return 0, err
		}
		item.LineTotalCents = int64(item.Quantity * float64(item.UnitPriceCents))
		quote.Items = append(quote.Items, item)
	}
	if err := itemRows.Close(); err != nil {
		return 0, err
	}
	calculateQuote(&quote)

	var copiedQuoteID int64
	if err := tx.QueryRow(`INSERT INTO project_quotes(project_id,quote_no,title,client_name,issuer_name,proposal_key,version_no,parent_quote_id,currency,
	 discount_type,discount_value,tax_rate,note,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'accepted') RETURNING id`,
		newProjectID, quote.QuoteNo+"-EXEC", quote.Title, quote.ClientName, quote.IssuerName,
		quote.ProposalKey+"-EXEC", quote.VersionNo, quote.ID, quote.Currency,
		quote.DiscountType, quote.DiscountValue, quote.TaxRate, quote.Note).Scan(&copiedQuoteID); err != nil {
		return 0, err
	}
	for _, item := range quote.Items {
		if _, err := tx.Exec(`INSERT INTO project_quote_items(quote_id,description,quantity,unit,unit_price_cents,sort_order)
		 VALUES($1,$2,$3,$4,$5,$6)`, copiedQuoteID, item.Description, item.Quantity, item.Unit, item.UnitPriceCents, item.SortOrder); err != nil {
			return 0, err
		}
	}

	roleMap := map[int64]int64{}
	roleRows, err := tx.Query(`SELECT id,name,hourly_rate_cents,flat_fee_cents,is_self FROM project_roles WHERE project_id=$1`, sourceProjectID)
	if err != nil {
		return 0, err
	}
	type copiedRole struct {
		oldID, newID, rate, flat int64
		name                     string
	}
	var copiedRoles []copiedRole
	for roleRows.Next() {
		var role copiedRole
		var self bool
		if err := roleRows.Scan(&role.oldID, &role.name, &role.rate, &role.flat, &self); err != nil {
			roleRows.Close()
			return 0, err
		}
		if err := tx.QueryRow(`INSERT INTO project_roles(project_id,name,hourly_rate_cents,flat_fee_cents,is_self)
		 VALUES($1,$2,$3,$4,$5) RETURNING id`, newProjectID, role.name, role.rate, role.flat, self).Scan(&role.newID); err != nil {
			roleRows.Close()
			return 0, err
		}
		roleMap[role.oldID] = role.newID
		copiedRoles = append(copiedRoles, role)
	}
	if err := roleRows.Close(); err != nil {
		return 0, err
	}
	for oldID, newID := range roleMap {
		if _, err := tx.Exec(`INSERT INTO project_time_entries(project_id,role_id,work_date,description,estimated_minutes,actual_minutes)
		 SELECT $1,$2,work_date,description,estimated_minutes,0 FROM project_time_entries WHERE project_id=$3 AND role_id=$4`,
			newProjectID, newID, sourceProjectID, oldID); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO project_receivables(project_id,name,amount_cents,expected_date,note)
	 SELECT $1,name,amount_cents,expected_date,note FROM project_receivables WHERE project_id=$2`, newProjectID, sourceProjectID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO project_cost_items(project_id,name,amount_cents,currency,exchange_rate,is_labor,note)
	 SELECT $1,name,amount_cents,currency,exchange_rate,is_labor,note FROM project_cost_items WHERE project_id=$2`, newProjectID, sourceProjectID); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(`INSERT INTO project_budgets(project_id,total_amount_cents,note) VALUES($1,$2,$3)`,
		newProjectID, quote.TotalCents, "由 "+quote.QuoteNo+" 客戶接受後自動建立"); err != nil {
		return 0, err
	}
	var allocated int64
	for _, role := range copiedRoles {
		planned := role.flat
		if planned == 0 {
			var minutes int64
			if err := tx.QueryRow(`SELECT COALESCE(SUM(estimated_minutes),0) FROM project_time_entries WHERE project_id=$1 AND role_id=$2`, newProjectID, role.newID).Scan(&minutes); err != nil {
				return 0, err
			}
			planned = role.rate * minutes / 60
		}
		if planned > 0 {
			if _, err := tx.Exec(`INSERT INTO project_budget_allocations(project_id,recipient_kind,recipient_name,planned_amount_cents)
			 VALUES($1,'labor_compensation',$2,$3)`, newProjectID, role.name, planned); err != nil {
				return 0, err
			}
			allocated += planned
		}
	}
	costRows, err := tx.Query(`SELECT name,ROUND(amount_cents*exchange_rate)::bigint FROM project_cost_items WHERE project_id=$1`, newProjectID)
	if err != nil {
		return 0, err
	}
	for costRows.Next() {
		var costName string
		var amount int64
		if err := costRows.Scan(&costName, &amount); err != nil {
			costRows.Close()
			return 0, err
		}
		if amount > 0 {
			if _, err := tx.Exec(`INSERT INTO project_budget_allocations(project_id,recipient_kind,recipient_name,planned_amount_cents)
			 VALUES($1,'cost_expense',$2,$3)`, newProjectID, costName, amount); err != nil {
				costRows.Close()
				return 0, err
			}
			allocated += amount
		}
	}
	if err := costRows.Close(); err != nil {
		return 0, err
	}
	if reserve := quote.TotalCents - allocated; reserve > 0 {
		if _, err := tx.Exec(`INSERT INTO project_budget_allocations(project_id,recipient_kind,recipient_name,planned_amount_cents)
		 VALUES($1,'company_reserve','公司保留款',$2)`, newProjectID, reserve); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(`UPDATE project_quotes SET status='accepted',updated_at=CURRENT_TIMESTAMP::text WHERE id=$1`, quoteID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return newProjectID, nil
}

func ListProjectRoles(d *sql.DB, projectID int64) ([]ProjectRole, error) {
	rows, err := d.Query(`SELECT id,project_id,name,hourly_rate_cents,flat_fee_cents,is_self
	 FROM project_roles WHERE project_id=$1 ORDER BY is_self DESC,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectRole
	for rows.Next() {
		var x ProjectRole
		if err := rows.Scan(&x.ID, &x.ProjectID, &x.Name, &x.HourlyRateCents, &x.FlatFeeCents, &x.IsSelf); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func CreateProjectRole(d *sql.DB, x *ProjectRole) (int64, error) {
	return insertID(d, `INSERT INTO project_roles(project_id,name,hourly_rate_cents,flat_fee_cents,is_self)
	 VALUES($1,$2,$3,$4,$5) RETURNING id`, x.ProjectID, x.Name, x.HourlyRateCents, x.FlatFeeCents, x.IsSelf)
}

func DeleteProjectRole(d *sql.DB, id, projectID int64) error {
	_, err := d.Exec(`DELETE FROM project_roles WHERE id=$1 AND project_id=$2`, id, projectID)
	return err
}

func ListTimeEntries(d *sql.DB, projectID int64) ([]TimeEntry, error) {
	rows, err := d.Query(`SELECT e.id,e.project_id,e.role_id,r.name,e.work_date,e.description,
	 e.estimated_minutes,e.actual_minutes,r.hourly_rate_cents
	 FROM project_time_entries e JOIN project_roles r ON r.id=e.role_id
	 WHERE e.project_id=$1 ORDER BY e.work_date DESC,e.id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TimeEntry
	for rows.Next() {
		var x TimeEntry
		var rate int64
		if err := rows.Scan(&x.ID, &x.ProjectID, &x.RoleID, &x.RoleName, &x.WorkDate, &x.Description,
			&x.EstimatedMinutes, &x.ActualMinutes, &rate); err != nil {
			return nil, err
		}
		x.EstimatedCostCents = rate * int64(x.EstimatedMinutes) / 60
		x.ActualCostCents = rate * int64(x.ActualMinutes) / 60
		out = append(out, x)
	}
	return out, rows.Err()
}

func CreateTimeEntry(d *sql.DB, x *TimeEntry) (int64, error) {
	return insertID(d, `INSERT INTO project_time_entries(project_id,role_id,work_date,description,estimated_minutes,actual_minutes)
	 SELECT $1,$2,$3,$4,$5,$6 WHERE EXISTS(SELECT 1 FROM project_roles WHERE id=$2 AND project_id=$1) RETURNING id`,
		x.ProjectID, x.RoleID, x.WorkDate, x.Description, x.EstimatedMinutes, x.ActualMinutes)
}

func DeleteTimeEntry(d *sql.DB, id, projectID int64) error {
	_, err := d.Exec(`DELETE FROM project_time_entries WHERE id=$1 AND project_id=$2`, id, projectID)
	return err
}

func ListProjectReceivables(d *sql.DB, projectID int64) ([]ProjectReceivable, error) {
	rows, err := d.Query(`SELECT id,project_id,name,amount_cents,COALESCE(expected_date,''),received,
	 COALESCE(received_date,''),note FROM project_receivables WHERE project_id=$1 ORDER BY received,expected_date,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectReceivable
	for rows.Next() {
		var x ProjectReceivable
		if err := rows.Scan(&x.ID, &x.ProjectID, &x.Name, &x.AmountCents, &x.ExpectedDate, &x.Received, &x.ReceivedDate, &x.Note); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func CreateProjectReceivable(d *sql.DB, x *ProjectReceivable) (int64, error) {
	return insertID(d, `INSERT INTO project_receivables(project_id,name,amount_cents,expected_date,note)
	 VALUES($1,$2,$3,NULLIF($4,''),$5) RETURNING id`, x.ProjectID, x.Name, x.AmountCents, x.ExpectedDate, x.Note)
}

func ToggleProjectReceivable(d *sql.DB, id, projectID int64) error {
	_, err := d.Exec(`UPDATE project_receivables SET received=CASE received WHEN 1 THEN 0 ELSE 1 END,
	 received_date=CASE received WHEN 1 THEN NULL ELSE CURRENT_DATE::text END WHERE id=$1 AND project_id=$2`, id, projectID)
	return err
}

func DeleteProjectReceivable(d *sql.DB, id, projectID int64) error {
	_, err := d.Exec(`DELETE FROM project_receivables WHERE id=$1 AND project_id=$2`, id, projectID)
	return err
}

func ListProjectCostItems(d *sql.DB, projectID int64) ([]ProjectCostItem, error) {
	rows, err := d.Query(`SELECT id,project_id,name,amount_cents,currency,exchange_rate,is_labor,paid,
	 COALESCE(paid_date,''),note FROM project_cost_items WHERE project_id=$1 ORDER BY paid,id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectCostItem
	for rows.Next() {
		var x ProjectCostItem
		if err := rows.Scan(&x.ID, &x.ProjectID, &x.Name, &x.AmountCents, &x.Currency, &x.ExchangeRate,
			&x.IsLabor, &x.Paid, &x.PaidDate, &x.Note); err != nil {
			return nil, err
		}
		x.TWDCents = int64(float64(x.AmountCents) * x.ExchangeRate)
		out = append(out, x)
	}
	return out, rows.Err()
}

func CreateProjectCostItem(d *sql.DB, x *ProjectCostItem) (int64, error) {
	return insertID(d, `INSERT INTO project_cost_items(project_id,name,amount_cents,currency,exchange_rate,is_labor,note)
	 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, x.ProjectID, x.Name, x.AmountCents, x.Currency, x.ExchangeRate, x.IsLabor, x.Note)
}

func ToggleProjectCostItem(d *sql.DB, id, projectID int64) error {
	_, err := d.Exec(`UPDATE project_cost_items SET paid=CASE paid WHEN 1 THEN 0 ELSE 1 END,
	 paid_date=CASE paid WHEN 1 THEN NULL ELSE CURRENT_DATE::text END WHERE id=$1 AND project_id=$2`, id, projectID)
	return err
}

func DeleteProjectCostItem(d *sql.DB, id, projectID int64) error {
	_, err := d.Exec(`DELETE FROM project_cost_items WHERE id=$1 AND project_id=$2`, id, projectID)
	return err
}

func insertID(d *sql.DB, query string, args ...any) (int64, error) {
	var id int64
	err := d.QueryRow(query, args...).Scan(&id)
	return id, err
}
