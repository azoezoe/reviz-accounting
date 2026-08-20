package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/hcchien/reviz-accounting/internal/auth"
	"github.com/hcchien/reviz-accounting/internal/models"
	"net/http"
	"path/filepath"
	"strings"
)

const maxReceiptBytes = 20 << 20

var receiptTypes = map[string]bool{"application/pdf": true, "image/jpeg": true, "image/png": true, "image/webp": true}

func (s *Server) MCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST required", 405)
		return
	}
	origin := r.Header.Get("Origin")
	if origin != "" && origin != "https://"+r.Host {
		http.Error(w, "invalid origin", 403)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var u auth.User
	var client string
	var exp string
	if token == "" || s.DB.QueryRow(`SELECT u.id,u.username,u.role,u.active,u.created_at,u.last_login_at,t.client_id,t.expires_at FROM mcp_access_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=? AND t.revoked_at IS NULL`, hash(token)).Scan(&u.ID, &u.Username, &u.Role, &u.Active, &u.CreatedAt, &u.LastLoginAt, &client, &exp) != nil || !u.Active || expired(exp) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="/.well-known/oauth-protected-resource"`)
		http.Error(w, "unauthorized", 401)
		return
	}
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      any             `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	// Base64 receipt payloads are roughly 4/3 the original file size.
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReceiptBytes*2)).Decode(&req) != nil {
		http.Error(w, "bad JSON", 400)
		return
	}
	result, err := s.call(&u, req.Method, req.Params)
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	_, _ = s.DB.Exec(`INSERT INTO mcp_audit_log(user_id,client_id,tool_name,outcome) VALUES(?,?,?,?)`, u.ID, client, auditToolName(req.Method, req.Params), outcome)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": err.Error()}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

func (s *Server) call(u *auth.User, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		return map[string]any{"protocolVersion": "2025-06-18", "serverInfo": map[string]string{"name": "reviz-accounting", "version": "1.0.0"}, "capabilities": map[string]any{"tools": map[string]any{}}}, nil
	case "tools/list":
		return map[string]any{"tools": tools()}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return s.tool(u, p.Name, p.Arguments)
	default:
		return nil, fmtErr("unsupported method")
	}
}

type fmtErr string

func (e fmtErr) Error() string { return string(e) }
func (s *Server) tool(u *auth.User, name string, a map[string]any) (any, error) {
	if u == nil {
		return nil, fmtErr("權限不足")
	}
	switch name {
	case "list_accounts":
		// Account IDs are required to create a transaction. The Web journal
		// exposes this same read-only lookup to accountants.
		if !u.AtLeast(auth.RoleAccountant) {
			return nil, fmtErr("權限不足")
		}
		v, e := models.ListAccounts(s.DB, true)
		return content(v, e)
	case "list_categories":
		// Category IDs are required to create a transaction; management of
		// categories remains owner-only in the Web app and MCP.
		if !u.AtLeast(auth.RoleAccountant) {
			return nil, fmtErr("權限不足")
		}
		v, e := models.ListCategories(s.DB)
		return content(v, e)
	case "list_projects":
		v, e := models.ListProjects(s.DB)
		if e == nil && u.Role != auth.RoleOwner {
			v = mcpAccessibleProjects(s, u, v)
		}
		return content(v, e)
	case "create_project":
		if !u.AtLeast(auth.RoleAccountant) {
			return nil, fmtErr("權限不足")
		}
		return s.createProjectForUser(u, a)
	case "update_project":
		if !u.AtLeast(auth.RoleAccountant) {
			return nil, fmtErr("權限不足")
		}
		if err := s.requireProject(u, numID(a, "project_id"), true); err != nil {
			return nil, err
		}
		return s.updateProject(a)
	case "get_project_budget":
		if err := s.requireProject(u, numID(a, "project_id"), false); err != nil {
			return nil, err
		}
		return s.projectBudget(numID(a, "project_id"))
	case "list_project_transactions":
		if err := s.requireProject(u, numID(a, "project_id"), false); err != nil {
			return nil, err
		}
		return s.projectTransactions(numID(a, "project_id"))
	case "get_project_management":
		if err := s.requireProject(u, numID(a, "project_id"), false); err != nil {
			return nil, err
		}
		return s.projectManagement(numID(a, "project_id"))
	case "list_quotes":
		query, args := `SELECT id FROM quotes`, []any{}
		if u.Role != auth.RoleOwner {
			query += ` WHERE created_by_id=?`
			args = append(args, u.ID)
		}
		rows, e := s.DB.Query(query+` ORDER BY id DESC`, args...)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		var quotes []map[string]any
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				return nil, e
			}
			q, e := s.standaloneQuote(id)
			if e != nil {
				return nil, e
			}
			quotes = append(quotes, q)
		}
		return content(quotes, rows.Err())
	case "get_quote":
		if err := s.requireQuote(u, numID(a, "quote_id")); err != nil {
			return nil, err
		}
		q, e := s.standaloneQuote(numID(a, "quote_id"))
		return content(q, e)
	case "list_transactions":
		f := models.TxFilter{YearMonth: str(a, "year_month"), SearchText: str(a, "search"), Limit: asInt(num(a, "limit"))}
		if f.Limit == 0 {
			f.Limit = 50
		}
		if u.Role != auth.RoleOwner {
			f.ProjectUserID = u.ID
		}
		v, n, e := models.ListTransactions(s.DB, f)
		if e != nil {
			return nil, e
		}
		return content(map[string]any{"total": n, "transactions": v}, nil)
	case "create_transaction", "update_transaction":
		if !u.AtLeast(auth.RoleAccountant) {
			return nil, fmtErr("權限不足")
		}
		if name == "update_transaction" {
			if err := s.requireTransaction(u, int64(num(a, "id")), true); err != nil {
				return nil, err
			}
		}
		t, e := s.tx(a)
		if e != nil {
			return nil, e
		}
		if err := s.requireTransactionProject(u, t.ProjectID, true); err != nil {
			return nil, err
		}
		if name == "create_transaction" {
			t.Code, e = models.GenerateCode(s.DB)
			if e == nil {
				var id int64
				id, e = models.CreateTransaction(s.DB, t)
				return content(map[string]any{"id": id, "code": t.Code}, e)
			}
		} else {
			t.ID = int64(num(a, "id"))
			e = models.UpdateTransaction(s.DB, t)
			return content(map[string]any{"id": t.ID}, e)
		}
		return nil, e
	case "upload_receipt":
		if !u.AtLeast(auth.RoleAccountant) {
			return nil, fmtErr("權限不足")
		}
		if err := s.requireTransaction(u, int64(num(a, "transaction_id")), true); err != nil {
			return nil, err
		}
		return s.uploadReceipt(u, a)
	case "save_project_budget", "create_budget_allocation", "create_budget_posting":
		if !u.AtLeast(auth.RoleAccountant) {
			return nil, fmtErr("權限不足")
		}
		projectID := numID(a, "project_id")
		if name == "create_budget_posting" {
			if err := s.requireTransaction(u, int64(num(a, "transaction_id")), true); err != nil {
				return nil, err
			}
		}
		if name == "create_budget_posting" && projectID == 0 {
			_ = s.DB.QueryRow(`SELECT pba.project_id FROM project_budget_allocations pba WHERE pba.id=?`, numID(a, "budget_allocation_id")).Scan(&projectID)
		}
		if err := s.requireProject(u, projectID, true); err != nil {
			return nil, err
		}
		switch name {
		case "save_project_budget":
			return s.saveProjectBudget(a)
		case "create_budget_allocation":
			return s.createBudgetAllocation(a)
		default:
			return s.createBudgetPosting(a)
		}
	case "create_quote", "update_quote", "delete_quote", "create_standalone_quote_item", "update_standalone_quote_item", "delete_standalone_quote_item", "revise_quote", "accept_quote", "create_project_quote", "create_quote_item", "revise_project_quote", "accept_project_quote",
		"create_project_role", "create_time_entry", "create_project_receivable",
		"toggle_project_receivable", "create_project_cost", "toggle_project_cost":
		if !u.AtLeast(auth.RoleAccountant) {
			return nil, fmtErr("權限不足")
		}
		if err := s.requireManagementWrite(u, name, a); err != nil {
			return nil, err
		}
		return s.projectManagementWriteForUser(u, name, a)
	}
	return nil, fmtErr("unknown tool")
}

func tools() []map[string]any {
	obj := map[string]any{"type": "object"}
	req := func(keys ...string) map[string]any { return map[string]any{"type": "object", "required": keys} }
	return []map[string]any{
		{"name": "list_accounts", "description": "列出可用帳戶與 ID；建立交易時需要 from_account_id 或 to_account_id。accountant 以上。", "inputSchema": obj},
		{"name": "list_categories", "description": "列出收入、成本與費用分類及 ID；建立交易時可帶 category_id。accountant 以上。", "inputSchema": obj},
		{"name": "list_projects", "description": "列出專案", "inputSchema": obj},
		{"name": "create_project", "description": "建立專案。傳 name；可選 start_date、end_date（YYYY-MM-DD）與 note。", "inputSchema": req("name")},
		{"name": "update_project", "description": "更新既有專案。傳 project_id；可更新 name、start_date、end_date、note，至少提供一個要更新的欄位。", "inputSchema": req("project_id")},
		{"name": "get_project_budget", "description": "取得專案的總預算、收入進度、預定分配及已連結日記帳交易與分攤狀態。傳 project_id。", "inputSchema": req("project_id")},
		{"name": "list_project_transactions", "description": "列出已連結到專案的日記帳交易，並標示每筆是否已有預算分攤。傳 project_id。", "inputSchema": req("project_id")},
		{"name": "get_project_management", "description": "取得專案報價版本、角色、預估/實際工時、應收款與成本。viewer 以上可讀取；傳 project_id。", "inputSchema": req("project_id")},
		{"name": "list_quotes", "description": "列出獨立報價提案；提案尚未建立正式專案。viewer 以上。", "inputSchema": obj},
		{"name": "get_quote", "description": "取得獨立報價與明細。傳 quote_id。viewer 以上。", "inputSchema": req("quote_id")},
		{"name": "create_quote", "description": "建立獨立報價提案，不會建立專案。傳 title；可選 quote_no、client_name、issuer_name、currency、折扣、稅率與 note。accountant 以上。", "inputSchema": req("title")},
		{"name": "update_quote", "description": "修改草稿報價。傳 quote_id 與至少一個欲更新欄位；可用 create_new_version=true 另存下一版，否則覆蓋目前版本。可更新 title、client_name、issuer_name、currency、discount_type、discount_value、tax_rate、note、quote_date、valid_until、issuer_contact、issuer_email、issuer_tax_id、project_content、terms、signature_label、quote_language、quote_type、show_unit_price、personal_name、personal_contact、contact_user_id。accountant 以上。", "inputSchema": req("quote_id")},
		{"name": "delete_quote", "description": "刪除草稿獨立報價及其明細、規格與附件。傳 quote_id。已送出或已同意版本不可刪除。accountant 以上。", "inputSchema": req("quote_id")},
		{"name": "create_standalone_quote_item", "description": "在草稿報價加入明細。傳 quote_id、description、unit_price_cents；可選 quantity、unit、is_choice。兩個以上 is_choice 項目會產生 A、B… 互斥方案總價。accountant 以上。", "inputSchema": req("quote_id", "description", "unit_price_cents")},
		{"name": "update_standalone_quote_item", "description": "修改草稿報價項目。傳 quote_id、item_id，並至少提供 description、detail、quantity、unit、unit_price_cents、is_choice 其中一項。accountant 以上。", "inputSchema": req("quote_id", "item_id")},
		{"name": "delete_standalone_quote_item", "description": "刪除草稿報價項目。傳 quote_id、item_id。accountant 以上。", "inputSchema": req("quote_id", "item_id")},
		{"name": "revise_quote", "description": "建立獨立報價的下一修訂版。傳 quote_id。accountant 以上。", "inputSchema": req("quote_id")},
		{"name": "accept_quote", "description": "客戶同意獨立報價後，一鍵建立正式專案與專案總預算。傳 quote_id；可選 project_name；有多個選擇方案時須傳 choice_label。accountant 以上。", "inputSchema": req("quote_id")},
		{"name": "create_project_quote", "description": "建立提案報價 V1。傳 project_id；可選 quote_no、title、client_name、issuer_name、currency、discount_type(amount|percent)、discount_value、tax_rate、note。accountant 以上。", "inputSchema": req("project_id")},
		{"name": "create_quote_item", "description": "在 draft 報價加入明細。傳 project_id、quote_id、description、quantity、unit、unit_price_cents。accountant 以上。", "inputSchema": req("project_id", "quote_id", "description", "unit_price_cents")},
		{"name": "revise_project_quote", "description": "複製指定報價與全部明細建立下一修訂版；舊 draft 會鎖定為 sent。傳 project_id、quote_id。accountant 以上。", "inputSchema": req("project_id", "quote_id")},
		{"name": "accept_project_quote", "description": "接受指定報價版本並原子化建立執行專案、複製角色/預估工時/應收/成本及完成預算分配。傳 project_id、quote_id；可選 project_name。accountant 以上。", "inputSchema": req("project_id", "quote_id")},
		{"name": "create_project_role", "description": "新增專案角色。金額為分；傳 project_id、name，可選 hourly_rate_cents、flat_fee_cents、is_self。accountant 以上。", "inputSchema": req("project_id", "name")},
		{"name": "create_time_entry", "description": "新增角色工時。傳 project_id、role_id、work_date、estimated_minutes、actual_minutes，可選 description。accountant 以上。", "inputSchema": req("project_id", "role_id", "work_date")},
		{"name": "create_project_receivable", "description": "新增專案應收款。金額為分；傳 project_id、name、amount_cents，可選 expected_date、note。accountant 以上。", "inputSchema": req("project_id", "name", "amount_cents")},
		{"name": "toggle_project_receivable", "description": "切換應收款是否已入帳。傳 project_id、receivable_id。accountant 以上。", "inputSchema": req("project_id", "receivable_id")},
		{"name": "create_project_cost", "description": "新增多幣別專案成本。金額為分；傳 project_id、name、amount_cents，可選 currency、exchange_rate、is_labor、note。accountant 以上。", "inputSchema": req("project_id", "name", "amount_cents")},
		{"name": "toggle_project_cost", "description": "切換專案成本是否已付款。傳 project_id、cost_id。accountant 以上。", "inputSchema": req("project_id", "cost_id")},
		{"name": "list_transactions", "description": "查詢交易，可帶 year_month、search、limit", "inputSchema": obj},
		{"name": "create_transaction", "description": "新增交易；amount 是分，傳 date、description、amount、from_account_id/to_account_id、category_id、counterparty、note。", "inputSchema": req("date", "description", "amount")},
		{"name": "update_transaction", "description": "更新既有交易；傳 id 及 create_transaction 欄位。", "inputSchema": req("id", "date", "description", "amount")},
		{"name": "upload_receipt", "description": "上傳並附加單據到既有交易。傳 transaction_id、filename、mime_type 與 content_base64；只接受 PDF、JPG、PNG、WebP，最大 20 MB。", "inputSchema": req("transaction_id", "filename", "mime_type", "content_base64")},
		{"name": "save_project_budget", "description": "新增或更新專案總預算；amount 為分。傳 project_id、total_amount，可選 note。", "inputSchema": req("project_id", "total_amount")},
		{"name": "create_budget_allocation", "description": "建立專案預定分配；金額為分。傳 project_id、recipient_kind(labor_compensation|company_reserve|cost_expense)、recipient_name、planned_amount。", "inputSchema": req("project_id", "recipient_kind", "recipient_name", "planned_amount")},
		{"name": "create_budget_posting", "description": "把既有付款拆分到一個專案預算項目。對同一 transaction_id 可重複呼叫以分攤至不同專案；所有現金分攤合計不得超過交易金額。傳 transaction_id、allocation_kind(partner_payout|cost_expense)、budget_allocation_id、amount(分)；可選 project_id 作為所選預算項目的歸屬驗證。", "inputSchema": req("transaction_id", "allocation_kind", "budget_allocation_id", "amount")},
	}
}

func auditToolName(method string, raw json.RawMessage) string {
	if method != "tools/call" {
		return method
	}
	var p struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &p) == nil && p.Name != "" {
		return p.Name
	}
	return method
}

func (s *Server) uploadReceipt(u *auth.User, a map[string]any) (any, error) {
	if s.Attachments == nil {
		return nil, fmtErr("單據儲存尚未設定")
	}
	txID := int64(num(a, "transaction_id"))
	if txID <= 0 {
		return nil, fmtErr("transaction_id 必填")
	}
	if _, err := models.GetTransaction(s.DB, txID); err != nil {
		return nil, fmtErr("找不到交易")
	}
	filename, contentType := filepath.Base(str(a, "filename")), str(a, "mime_type")
	if filename == "." || filename == "" || !receiptTypes[contentType] {
		return nil, fmtErr("只接受 PDF、JPG、PNG 或 WebP 單據")
	}
	b, err := base64.StdEncoding.DecodeString(str(a, "content_base64"))
	if err != nil || len(b) == 0 || len(b) > maxReceiptBytes {
		return nil, fmtErr("content_base64 無效或單據超過 20 MB")
	}
	actual := http.DetectContentType(b)
	if actual != contentType || !receiptTypes[actual] {
		return nil, fmtErr("檔案內容與 mime_type 不符")
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	key := fmt.Sprintf("attachments/%d/%x%s", txID, random, strings.ToLower(filepath.Ext(filename)))
	if err := s.Attachments.Put(context.Background(), key, contentType, bytes.NewReader(b)); err != nil {
		return nil, err
	}
	id, err := models.CreateAttachment(s.DB, &models.Attachment{TransactionID: txID, StorageKey: key, OriginalFilename: filename, ContentType: contentType, SizeBytes: int64(len(b)), UploadedByID: models.NullInt64From(u.ID)})
	if err != nil {
		_ = s.Attachments.Delete(context.Background(), key)
		return nil, err
	}
	return content(map[string]any{"attachment_id": id, "transaction_id": txID, "filename": filename, "size_bytes": len(b)}, nil)
}

func (s *Server) requireProject(u *auth.User, projectID int64, write bool) error {
	if projectID <= 0 {
		return fmtErr("project_id 必填")
	}
	if u.Role == auth.RoleOwner {
		return nil
	}
	ok, err := models.CanAccessProject(s.DB, projectID, u.ID, write)
	if err != nil {
		return err
	}
	if !ok {
		return fmtErr("權限不足")
	}
	return nil
}

func (s *Server) requireQuote(u *auth.User, quoteID int64) error {
	if quoteID <= 0 {
		return fmtErr("quote_id 必填")
	}
	if u.Role == auth.RoleOwner {
		return nil
	}
	var ok bool
	if err := s.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM quotes WHERE id=? AND created_by_id=?)`, quoteID, u.ID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return fmtErr("權限不足")
	}
	return nil
}

func (s *Server) requireTransaction(u *auth.User, transactionID int64, write bool) error {
	if transactionID <= 0 {
		return fmtErr("transaction_id 必填")
	}
	if u.Role == auth.RoleOwner {
		return nil
	}
	ok, err := models.CanAccessTransaction(s.DB, transactionID, u.ID, write)
	if err != nil {
		return err
	}
	if !ok {
		return fmtErr("權限不足")
	}
	return nil
}

func (s *Server) requireTransactionProject(u *auth.User, projectID sql.NullInt64, write bool) error {
	if u.Role == auth.RoleOwner {
		return nil
	}
	if !projectID.Valid {
		return fmtErr("accountant 必須將交易歸屬於有權限的專案")
	}
	return s.requireProject(u, projectID.Int64, write)
}

func (s *Server) requireManagementWrite(u *auth.User, name string, a map[string]any) error {
	switch name {
	case "create_quote":
		return nil
	case "update_quote", "delete_quote", "create_standalone_quote_item", "update_standalone_quote_item", "delete_standalone_quote_item", "revise_quote", "accept_quote":
		return s.requireQuote(u, numID(a, "quote_id"))
	default:
		return s.requireProject(u, numID(a, "project_id"), true)
	}
}

func mcpAccessibleProjects(s *Server, u *auth.User, projects []models.Project) []models.Project {
	out := projects[:0]
	for _, p := range projects {
		if ok, _ := models.CanAccessProject(s.DB, p.ID, u.ID, false); ok {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) createProjectForUser(u *auth.User, a map[string]any) (any, error) {
	name := strings.TrimSpace(str(a, "name"))
	if name == "" {
		return nil, fmtErr("name 必填")
	}
	id, err := models.CreateProject(s.DB, &models.Project{
		Name:      name,
		StartDate: models.NullStringFrom(str(a, "start_date")),
		EndDate:   models.NullStringFrom(str(a, "end_date")),
		Note:      str(a, "note"),
	})
	if err != nil {
		return nil, err
	}
	if u.Role != auth.RoleOwner {
		if err := models.GrantProjectAccess(s.DB, id, u.ID, "write"); err != nil {
			return nil, err
		}
	}
	return content(map[string]any{"id": id, "name": name}, nil)
}

func (s *Server) updateProject(a map[string]any) (any, error) {
	projectID := numID(a, "project_id")
	if projectID <= 0 {
		return nil, fmtErr("project_id 必填")
	}
	p, err := models.GetProject(s.DB, projectID)
	if err != nil {
		return nil, fmtErr("找不到專案")
	}
	updated := false
	if _, ok := a["name"]; ok {
		name := strings.TrimSpace(str(a, "name"))
		if name == "" {
			return nil, fmtErr("name 不可為空白")
		}
		p.Name = name
		updated = true
	}
	if _, ok := a["start_date"]; ok {
		p.StartDate = models.NullStringFrom(str(a, "start_date"))
		updated = true
	}
	if _, ok := a["end_date"]; ok {
		p.EndDate = models.NullStringFrom(str(a, "end_date"))
		updated = true
	}
	if _, ok := a["note"]; ok {
		p.Note = str(a, "note")
		updated = true
	}
	if !updated {
		return nil, fmtErr("請提供要更新的欄位")
	}
	if err := models.UpdateProject(s.DB, p); err != nil {
		return nil, err
	}
	return content(map[string]any{
		"id":         p.ID,
		"name":       p.Name,
		"start_date": p.StartDate.String,
		"end_date":   p.EndDate.String,
		"note":       p.Note,
	}, nil)
}

func (s *Server) projectBudget(projectID int64) (any, error) {
	if projectID <= 0 {
		return nil, fmtErr("project_id 必填")
	}
	p, err := models.GetProject(s.DB, projectID)
	if err != nil {
		return nil, fmtErr("找不到專案")
	}
	b, err := models.GetProjectBudget(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	allocations, err := models.ListProjectBudgetAllocations(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	report, err := models.GetProjectBudgetReport(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	transactions, err := s.projectTransactionsData(projectID)
	if err != nil {
		return nil, err
	}
	return content(map[string]any{
		"project":      p,
		"budget":       b,
		"income_cents": report.IncomeCents,
		"allocations":  allocations,
		"transactions": transactions,
	}, nil)
}

func (s *Server) projectTransactions(projectID int64) (any, error) {
	transactions, err := s.projectTransactionsData(projectID)
	return content(transactions, err)
}

func (s *Server) projectTransactionsData(projectID int64) ([]map[string]any, error) {
	if projectID <= 0 {
		return nil, fmtErr("project_id 必填")
	}
	if _, err := models.GetProject(s.DB, projectID); err != nil {
		return nil, fmtErr("找不到專案")
	}
	txs, _, err := models.ListTransactions(s.DB, models.TxFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	counts, err := models.BudgetPostingCountsForProject(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(txs))
	for _, tx := range txs {
		out = append(out, map[string]any{"id": tx.ID, "code": tx.Code, "date": tx.Date, "description": tx.Description, "counterparty": tx.CounterpartyName, "amount_cents": tx.AmountCents, "type": tx.Type(), "budget_posting_count": counts[tx.ID], "is_unallocated": counts[tx.ID] == 0})
	}
	return out, nil
}

func (s *Server) saveProjectBudget(a map[string]any) (any, error) {
	projectID, amount := numID(a, "project_id"), numID(a, "total_amount")
	if projectID <= 0 || amount < 0 {
		return nil, fmtErr("project_id 與非負的 total_amount（分）必填")
	}
	if _, err := models.GetProject(s.DB, projectID); err != nil {
		return nil, fmtErr("找不到專案")
	}
	err := models.SaveProjectBudget(s.DB, &models.ProjectBudget{ProjectID: projectID, TotalAmountCents: amount, Note: str(a, "note")})
	return content(map[string]any{"project_id": projectID, "total_amount_cents": amount}, err)
}

func (s *Server) createProjectMilestone(a map[string]any) (any, error) {
	projectID, amount := numID(a, "project_id"), numID(a, "planned_income")
	name := strings.TrimSpace(str(a, "name"))
	if projectID <= 0 || amount < 0 || name == "" {
		return nil, fmtErr("project_id、name 與非負的 planned_income（分）必填")
	}
	if _, err := models.GetProject(s.DB, projectID); err != nil {
		return nil, fmtErr("找不到專案")
	}
	sortOrder := int(num(a, "sort_order"))
	if _, ok := a["sort_order"]; !ok {
		ms, err := models.ListMilestones(s.DB, projectID)
		if err != nil {
			return nil, err
		}
		sortOrder = len(ms)
	}
	id, err := models.CreateMilestone(s.DB, &models.Milestone{ProjectID: projectID, Name: name, PlannedIncomeCents: amount, SortOrder: sortOrder, Note: str(a, "note")})
	return content(map[string]any{"id": id, "project_id": projectID}, err)
}

func (s *Server) createBudgetAllocation(a map[string]any) (any, error) {
	projectID, amount := numID(a, "project_id"), numID(a, "planned_amount")
	kind, name := str(a, "recipient_kind"), strings.TrimSpace(str(a, "recipient_name"))
	if projectID <= 0 || amount < 0 || name == "" || (kind != "company_reserve" && kind != "labor_compensation" && kind != "cost_expense") {
		return nil, fmtErr("project_id、recipient_kind、recipient_name 與非負的 planned_amount（分）必填")
	}
	if _, err := models.GetProject(s.DB, projectID); err != nil {
		return nil, fmtErr("找不到專案")
	}
	x := &models.BudgetAllocation{ProjectID: projectID, RecipientKind: kind, RecipientName: name, PlannedAmountCents: amount}
	if cp := numID(a, "counterparty_id"); cp > 0 {
		x.CounterpartyID, x.CounterpartyValid = cp, true
	}
	id, err := models.CreateBudgetAllocation(s.DB, x)
	return content(map[string]any{"id": id, "project_id": projectID}, err)
}

func (s *Server) createBudgetPosting(a map[string]any) (any, error) {
	txID, amount := numID(a, "transaction_id"), numID(a, "amount")
	kind := str(a, "allocation_kind")
	if txID <= 0 || amount <= 0 || (kind != "partner_payout" && kind != "cost_expense") {
		return nil, fmtErr("transaction_id、正數 amount（分）及有效 allocation_kind 必填")
	}
	t, err := models.GetTransaction(s.DB, txID)
	if err != nil {
		return nil, fmtErr("找不到交易")
	}
	p := &models.BudgetPosting{TransactionID: txID, Kind: kind, AmountCents: amount, Note: str(a, "note")}
	if allocationID := numID(a, "budget_allocation_id"); allocationID > 0 {
		p.AllocationID, p.AllocationValid = allocationID, true
	}
	if !p.AllocationValid {
		return nil, fmtErr("budget_allocation_id 必填")
	}
	if p.AllocationValid {
		if projectID := numID(a, "project_id"); projectID > 0 {
			ok, e := models.BudgetAllocationBelongsToProject(s.DB, p.AllocationID, projectID)
			if e != nil || !ok {
				return nil, fmtErr("預算分配不屬於指定專案")
			}
		}
		allocationKind, e := models.BudgetAllocationKind(s.DB, p.AllocationID)
		if e != nil {
			return nil, e
		}
		if (kind == "partner_payout" && allocationKind != "labor_compensation") || (kind == "cost_expense" && allocationKind != "cost_expense") || (kind == "company_reserve" && allocationKind != "company_reserve") {
			return nil, fmtErr("分攤類型必須對應相同用途類別的預算項目")
		}
	}
	if kind != "company_reserve" {
		used, e := models.SumCashBudgetPostings(s.DB, txID)
		if e != nil {
			return nil, e
		}
		if used+amount > t.AmountCents {
			return nil, fmtErr("此類型的分攤總額不能超過交易金額")
		}
	}
	id, err := models.CreateBudgetPosting(s.DB, p)
	return content(map[string]any{"id": id, "transaction_id": txID}, err)
}
func (s *Server) tx(a map[string]any) (*models.Transaction, error) {
	from, to := int64(num(a, "from_account_id")), int64(num(a, "to_account_id"))
	amount := int64(num(a, "amount"))
	if amount <= 0 || str(a, "date") == "" || (!valid(from) && !valid(to)) || from == to && valid(from) {
		return nil, fmtErr("日期、正數金額與至少一個不同帳戶為必填")
	}
	cp, e := models.GetOrCreateCounterparty(s.DB, str(a, "counterparty"))
	if e != nil {
		return nil, e
	}
	return &models.Transaction{Date: str(a, "date"), Description: str(a, "description"), AmountCents: amount, CounterpartyID: cp, CategoryID: models.NullInt64From(int64(num(a, "category_id"))), FromAccountID: models.NullInt64From(from), ToAccountID: models.NullInt64From(to), ProjectID: models.NullInt64From(int64(num(a, "project_id"))), Note: str(a, "note")}, nil
}
func valid(v int64) bool                     { return v > 0 }
func numID(m map[string]any, k string) int64 { return int64(num(m, k)) }
func str(m map[string]any, k string) string  { v, _ := m[k].(string); return v }
func num(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case json.Number:
		n, _ := v.Float64()
		return n
	}
	return 0
}
func asInt(v float64) int { return int(v) }
func content(v any, e error) (any, error) {
	if e != nil {
		return nil, e
	}
	b, _ := json.Marshal(v)
	return map[string]any{"content": []map[string]string{{"type": "text", "text": string(b)}}}, nil
}
