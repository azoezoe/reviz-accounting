package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hcchien/reviz-accounting/internal/auth"
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

func (s *Server) projectManagementWriteForUser(u *auth.User, name string, a map[string]any) (any, error) {
	switch name {
	case "create_quote":
		return s.createStandaloneQuote(u, a)
	case "update_quote":
		return s.updateStandaloneQuote(a)
	case "delete_quote":
		return s.deleteStandaloneQuote(a)
	case "create_standalone_quote_item":
		return s.createStandaloneQuoteItem(a)
	case "update_standalone_quote_item":
		return s.updateStandaloneQuoteItem(a)
	case "delete_standalone_quote_item":
		return s.deleteStandaloneQuoteItem(a)
	case "revise_quote":
		return s.reviseStandaloneQuote(a)
	case "accept_quote":
		return s.acceptStandaloneQuote(u, a)
	case "create_project_quote":
		return s.createProjectQuote(a)
	case "create_quote_item":
		return s.createQuoteItem(a)
	case "revise_project_quote":
		id, err := models.ReviseProjectQuote(s.DB, numID(a, "quote_id"), numID(a, "project_id"))
		return content(map[string]any{"quote_id": id, "version_created": true}, err)
	case "accept_project_quote":
		id, err := models.AcceptQuoteAndCreateProject(s.DB, numID(a, "quote_id"), numID(a, "project_id"), str(a, "project_name"))
		if err == nil && u.Role != auth.RoleOwner {
			err = models.GrantProjectAccess(s.DB, id, u.ID, "write")
		}
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

func (s *Server) standaloneQuote(id int64) (map[string]any, error) {
	var q struct {
		ID                                                                   int64
		QuoteNo, Title, Client, Issuer, Currency, DiscountType, Note, Status string
		Discount, Tax                                                        float64
		Version                                                              int
		ProjectID                                                            int64
	}
	err := s.DB.QueryRow(`SELECT id,quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,status,version_no,COALESCE(project_id,0) FROM quotes WHERE id=$1`, id).Scan(&q.ID, &q.QuoteNo, &q.Title, &q.Client, &q.Issuer, &q.Currency, &q.DiscountType, &q.Discount, &q.Tax, &q.Note, &q.Status, &q.Version, &q.ProjectID)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(`SELECT id,description,quantity,unit,unit_price_cents FROM quote_items WHERE quote_id=$1 ORDER BY sort_order,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	var subtotal int64
	for rows.Next() {
		var iid, price int64
		var desc, unit string
		var qty float64
		if err := rows.Scan(&iid, &desc, &qty, &unit, &price); err != nil {
			return nil, err
		}
		line := int64(qty * float64(price))
		subtotal += line
		items = append(items, map[string]any{"id": iid, "description": desc, "quantity": qty, "unit": unit, "unit_price_cents": price, "line_total_cents": line})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	discount := int64(q.Discount * 100)
	if q.DiscountType == "percent" {
		discount = int64(float64(subtotal) * q.Discount / 100)
	}
	taxable := subtotal - discount
	tax := int64(float64(taxable) * q.Tax / 100)
	return map[string]any{"id": q.ID, "quote_no": q.QuoteNo, "title": q.Title, "client_name": q.Client, "issuer_name": q.Issuer, "currency": q.Currency, "status": q.Status, "version_no": q.Version, "project_id": q.ProjectID, "subtotal_cents": subtotal, "discount_cents": discount, "tax_cents": tax, "total_cents": taxable + tax, "items": items}, nil
}

func (s *Server) createStandaloneQuote(u *auth.User, a map[string]any) (any, error) {
	no := strings.TrimSpace(str(a, "quote_no"))
	if no == "" {
		var n int
		_ = s.DB.QueryRow(`SELECT COUNT(*)+1 FROM quotes`).Scan(&n)
		no = fmt.Sprintf("Q-%d-%03d", time.Now().Year(), n)
	}
	if strings.TrimSpace(str(a, "title")) == "" {
		return nil, fmtErr("title 必填")
	}
	discountType := defaultText(str(a, "discount_type"), "amount")
	if discountType != "amount" && discountType != "percent" {
		return nil, fmtErr("discount_type 必須是 amount 或 percent")
	}
	tax := num(a, "tax_rate")
	if _, ok := a["tax_rate"]; !ok {
		tax = 5
	}
	var id int64
	err := s.DB.QueryRow(`INSERT INTO quotes(quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,created_by_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, no, str(a, "title"), str(a, "client_name"), str(a, "issuer_name"), defaultText(str(a, "currency"), "TWD"), discountType, num(a, "discount_value"), tax, str(a, "note"), u.ID).Scan(&id)
	return content(map[string]any{"quote_id": id, "quote_no": no}, err)
}

func (s *Server) updateStandaloneQuote(a map[string]any) (any, error) {
	id := numID(a, "quote_id")
	if id <= 0 {
		return nil, fmtErr("quote_id 必填")
	}
	var status string
	if err := s.DB.QueryRow(`SELECT status FROM quotes WHERE id=?`, id).Scan(&status); err != nil {
		return nil, err
	}
	if status != "draft" {
		return nil, fmtErr("只有草稿報價單可以修改")
	}
	targetID := id
	allowedText := map[string]string{
		"title": "title", "client_name": "client_name", "issuer_name": "issuer_name", "currency": "currency",
		"discount_type": "discount_type", "note": "note", "quote_date": "quote_date", "valid_until": "valid_until",
		"issuer_contact": "issuer_contact", "issuer_email": "issuer_email", "issuer_tax_id": "issuer_tax_id",
		"project_content": "project_content", "terms": "terms", "signature_label": "signature_label",
		"quote_language": "quote_language", "quote_type": "quote_type", "personal_name": "personal_name", "personal_contact": "personal_contact",
	}
	sets, args := make([]string, 0, len(allowedText)+4), make([]any, 0, len(allowedText)+5)
	for key, column := range allowedText {
		if _, ok := a[key]; ok {
			sets, args = append(sets, column+"=?"), append(args, str(a, key))
		}
	}
	for _, key := range []string{"discount_value", "tax_rate"} {
		if _, ok := a[key]; ok {
			sets, args = append(sets, key+"=?"), append(args, num(a, key))
		}
	}
	if _, ok := a["show_unit_price"]; ok {
		sets, args = append(sets, "show_unit_price=?"), append(args, boolToInt(boolean(a, "show_unit_price")))
	}
	if _, ok := a["contact_user_id"]; ok {
		contactID := numID(a, "contact_user_id")
		if contactID > 0 {
			var active bool
			if err := s.DB.QueryRow(`SELECT active=1 FROM users WHERE id=?`, contactID).Scan(&active); err != nil || !active {
				return nil, fmtErr("contact_user_id 不存在或使用者已停用")
			}
			sets, args = append(sets, "contact_user_id=?"), append(args, contactID)
		} else {
			sets = append(sets, "contact_user_id=NULL")
		}
	}
	if len(sets) == 0 {
		return nil, fmtErr("至少提供一個要更新的欄位")
	}
	if _, ok := a["discount_value"]; ok && num(a, "discount_value") < 0 {
		return nil, fmtErr("discount_value 不可為負數")
	}
	if _, ok := a["tax_rate"]; ok && num(a, "tax_rate") < 0 {
		return nil, fmtErr("tax_rate 不可為負數")
	}
	if _, ok := a["discount_type"]; ok && str(a, "discount_type") != "amount" && str(a, "discount_type") != "percent" {
		return nil, fmtErr("discount_type 必須是 amount 或 percent")
	}
	if boolean(a, "create_new_version") {
		var err error
		targetID, err = s.cloneStandaloneQuoteVersion(id)
		if err != nil {
			return nil, err
		}
	}
	sets = append(sets, "updated_at=CAST(CURRENT_TIMESTAMP AS TEXT)")
	args = append(args, targetID)
	result, err := s.DB.Exec(`UPDATE quotes SET `+strings.Join(sets, ",")+` WHERE id=? AND status='draft'`, args...)
	if err != nil {
		return nil, err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return nil, fmtErr("報價單已不是可修改的草稿")
	}
	return content(map[string]any{"quote_id": targetID, "version_created": targetID != id}, nil)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Server) cloneStandaloneQuoteVersion(id int64) (int64, error) {
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var quoteNo, status string
	var version int
	if err = tx.QueryRow(`SELECT quote_no,version_no,status FROM quotes WHERE id=?`, id).Scan(&quoteNo, &version, &status); err != nil {
		return 0, err
	}
	if status != "draft" {
		return 0, fmtErr("只有草稿報價單可以建立新版")
	}
	var newID int64
	newNo := fmt.Sprintf("%s-R%d", strings.Split(quoteNo, "-R")[0], version+1)
	err = tx.QueryRow(`INSERT INTO quotes(quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,version_no,parent_quote_id,quote_date,valid_until,issuer_contact,issuer_email,issuer_tax_id,project_content,terms,signature_label,quote_language,quote_type,show_unit_price,personal_name,personal_contact,contact_user_id,created_by_id) SELECT ?,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,?,id,quote_date,valid_until,issuer_contact,issuer_email,issuer_tax_id,project_content,terms,signature_label,quote_language,quote_type,show_unit_price,personal_name,personal_contact,contact_user_id,created_by_id FROM quotes WHERE id=? RETURNING id`, newNo, version+1, id).Scan(&newID)
	if err == nil {
		_, err = tx.Exec(`INSERT INTO quote_items(quote_id,description,detail,quantity,unit,unit_price_cents,sort_order) SELECT ?,description,detail,quantity,unit,unit_price_cents,sort_order FROM quote_items WHERE quote_id=?`, newID, id)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO quote_specifications(quote_id,feature,use_case,capability,implementation_steps,sort_order) SELECT ?,feature,use_case,capability,implementation_steps,sort_order FROM quote_specifications WHERE quote_id=?`, newID, id)
	}
	if err == nil {
		var result sql.Result
		result, err = tx.Exec(`UPDATE quotes SET status='sent' WHERE id=? AND status='draft'`, id)
		if err == nil {
			rows, rowsErr := result.RowsAffected()
			if rowsErr != nil || rows != 1 {
				return 0, fmtErr("報價單已不是可建立新版的草稿")
			}
		}
	}
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return newID, nil
}

func (s *Server) deleteStandaloneQuote(a map[string]any) (any, error) {
	id := numID(a, "quote_id")
	if id <= 0 {
		return nil, fmtErr("quote_id 必填")
	}
	var status string
	if err := s.DB.QueryRow(`SELECT status FROM quotes WHERE id=?`, id).Scan(&status); err != nil {
		return nil, err
	}
	if status != "draft" {
		return nil, fmtErr("只有草稿報價單可以刪除")
	}
	rows, err := s.DB.Query(`SELECT storage_key FROM quote_attachments WHERE quote_id=?`, id)
	if err != nil {
		return nil, err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, key := range keys {
		if err := s.Attachments.Delete(context.Background(), key); err != nil {
			return nil, err
		}
	}
	result, err := s.DB.Exec(`DELETE FROM quotes WHERE id=? AND status='draft'`, id)
	if err != nil {
		return nil, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return nil, fmtErr("報價單已不是可刪除的草稿")
	}
	return content(map[string]any{"quote_id": id, "deleted": true}, nil)
}
func (s *Server) createStandaloneQuoteItem(a map[string]any) (any, error) {
	id := numID(a, "quote_id")
	desc := strings.TrimSpace(str(a, "description"))
	qty := num(a, "quantity")
	if _, ok := a["quantity"]; !ok {
		qty = 1
	}
	price := numID(a, "unit_price_cents")
	if id <= 0 || desc == "" || qty <= 0 || price < 0 {
		return nil, fmtErr("quote_id、description、正數 quantity 與非負 unit_price_cents 必填")
	}
	var draft bool
	if err := s.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM quotes WHERE id=$1 AND status='draft')`, id).Scan(&draft); err != nil {
		return nil, err
	}
	if !draft {
		return nil, fmtErr("報價不存在或版本已鎖定")
	}
	var itemID int64
	err := s.DB.QueryRow(`INSERT INTO quote_items(quote_id,description,quantity,unit,unit_price_cents,sort_order) SELECT $1,$2,$3,$4,$5,COUNT(*) FROM quote_items WHERE quote_id=$1 RETURNING id`, id, desc, qty, defaultText(str(a, "unit"), "式"), price).Scan(&itemID)
	return content(map[string]any{"item_id": itemID, "quote_id": id}, err)
}

func (s *Server) updateStandaloneQuoteItem(a map[string]any) (any, error) {
	quoteID, itemID := numID(a, "quote_id"), numID(a, "item_id")
	if quoteID <= 0 || itemID <= 0 {
		return nil, fmtErr("quote_id 與 item_id 必填")
	}
	sets, args := []string{}, []any{}
	for _, field := range []string{"description", "detail", "unit"} {
		if _, ok := a[field]; ok {
			value := str(a, field)
			if field == "description" && strings.TrimSpace(value) == "" {
				return nil, fmtErr("description 不可為空")
			}
			if field == "unit" && strings.TrimSpace(value) == "" {
				value = "式"
			}
			sets, args = append(sets, field+"=?"), append(args, value)
		}
	}
	if _, ok := a["quantity"]; ok {
		if num(a, "quantity") <= 0 {
			return nil, fmtErr("quantity 必須大於 0")
		}
		sets, args = append(sets, "quantity=?"), append(args, num(a, "quantity"))
	}
	if _, ok := a["unit_price_cents"]; ok {
		if num(a, "unit_price_cents") < 0 {
			return nil, fmtErr("unit_price_cents 不可為負數")
		}
		sets, args = append(sets, "unit_price_cents=?"), append(args, numID(a, "unit_price_cents"))
	}
	if len(sets) == 0 {
		return nil, fmtErr("至少提供一個要更新的欄位")
	}
	args = append(args, itemID, quoteID)
	result, err := s.DB.Exec(`UPDATE quote_items SET `+strings.Join(sets, ",")+` WHERE id=? AND quote_id=? AND EXISTS (SELECT 1 FROM quotes WHERE id=? AND status='draft')`, append(args, quoteID)...)
	if err != nil {
		return nil, err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return nil, fmtErr("報價項目不存在或報價單已鎖定")
	}
	return content(map[string]any{"quote_id": quoteID, "item_id": itemID, "updated": true}, nil)
}

func (s *Server) deleteStandaloneQuoteItem(a map[string]any) (any, error) {
	quoteID, itemID := numID(a, "quote_id"), numID(a, "item_id")
	if quoteID <= 0 || itemID <= 0 {
		return nil, fmtErr("quote_id 與 item_id 必填")
	}
	result, err := s.DB.Exec(`DELETE FROM quote_items WHERE id=? AND quote_id=? AND EXISTS (SELECT 1 FROM quotes WHERE id=? AND status='draft')`, itemID, quoteID, quoteID)
	if err != nil {
		return nil, err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return nil, fmtErr("報價項目不存在或報價單已鎖定")
	}
	return content(map[string]any{"quote_id": quoteID, "item_id": itemID, "deleted": true}, nil)
}
func (s *Server) reviseStandaloneQuote(a map[string]any) (any, error) {
	id := numID(a, "quote_id")
	newID, err := s.cloneStandaloneQuoteVersion(id)
	return content(map[string]any{"quote_id": newID, "version_created": true}, err)
}
func (s *Server) acceptStandaloneQuote(u *auth.User, a map[string]any) (any, error) {
	id := numID(a, "quote_id")
	q, err := s.standaloneQuote(id)
	if err != nil {
		return nil, err
	}
	if q["status"] == "accepted" || q["project_id"].(int64) > 0 {
		return nil, fmtErr("此報價已建立專案")
	}
	name := strings.TrimSpace(str(a, "project_name"))
	if name == "" {
		name = q["title"].(string)
	}
	if name == "" {
		name = q["quote_no"].(string)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var pid int64
	if err = tx.QueryRow(`INSERT INTO projects(name,note) VALUES($1,$2) RETURNING id`, name, "由報價 "+q["quote_no"].(string)+" 客戶同意後建立").Scan(&pid); err == nil {
		_, err = tx.Exec(`INSERT INTO project_budgets(project_id,total_amount_cents,note) VALUES($1,$2,$3)`, pid, q["total_cents"].(int64), "由報價單自動建立")
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE quotes SET status='accepted',project_id=$1 WHERE id=$2`, pid, id)
	}
	if err == nil && u.Role != auth.RoleOwner {
		_, err = tx.Exec(`INSERT INTO project_permissions(project_id,user_id,access_level) VALUES($1,$2,'write')`, pid, u.ID)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return content(map[string]any{"execution_project_id": pid, "quote_accepted": true, "budget_allocated": true}, nil)
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
