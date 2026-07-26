package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
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
	_, _ = s.DB.Exec(`INSERT INTO mcp_audit_log(user_id,client_id,tool_name,outcome) VALUES(?,?,?,?)`, u.ID, client, req.Method, outcome)
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
	switch name {
	case "list_accounts":
		v, e := models.ListAccounts(s.DB, true)
		return content(v, e)
	case "list_categories":
		v, e := models.ListCategories(s.DB)
		return content(v, e)
	case "list_projects":
		v, e := models.ListProjects(s.DB)
		return content(v, e)
	case "create_project":
		if !u.AtLeast(auth.RoleAccountant) {
			return nil, fmtErr("權限不足")
		}
		return s.createProject(a)
	case "get_project_budget":
		return s.projectBudget(numID(a, "project_id"))
	case "list_transactions":
		f := models.TxFilter{YearMonth: str(a, "year_month"), SearchText: str(a, "search"), Limit: asInt(num(a, "limit"))}
		if f.Limit == 0 {
			f.Limit = 50
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
		t, e := s.tx(a)
		if e != nil {
			return nil, e
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
		return s.uploadReceipt(u, a)
	case "save_project_budget", "create_project_milestone", "create_budget_allocation", "create_budget_posting":
		if !u.AtLeast(auth.RoleAccountant) {
			return nil, fmtErr("權限不足")
		}
		switch name {
		case "save_project_budget":
			return s.saveProjectBudget(a)
		case "create_project_milestone":
			return s.createProjectMilestone(a)
		case "create_budget_allocation":
			return s.createBudgetAllocation(a)
		default:
			return s.createBudgetPosting(a)
		}
	}
	return nil, fmtErr("unknown tool")
}

func tools() []map[string]any {
	obj := map[string]any{"type": "object"}
	req := func(keys ...string) map[string]any { return map[string]any{"type": "object", "required": keys} }
	return []map[string]any{
		{"name": "list_accounts", "description": "列出帳戶", "inputSchema": obj},
		{"name": "list_categories", "description": "列出收入、成本與費用分類", "inputSchema": obj},
		{"name": "list_projects", "description": "列出專案", "inputSchema": obj},
		{"name": "create_project", "description": "建立專案。傳 name；可選 start_date、end_date（YYYY-MM-DD）與 note。", "inputSchema": req("name")},
		{"name": "get_project_budget", "description": "取得專案的總預算、請款批次、各對象預計/實際及公司共用池。傳 project_id。", "inputSchema": req("project_id")},
		{"name": "list_transactions", "description": "查詢交易，可帶 year_month、search、limit", "inputSchema": obj},
		{"name": "create_transaction", "description": "新增交易；amount 是分，傳 date、description、amount、from_account_id/to_account_id、category_id、counterparty、note。", "inputSchema": req("date", "description", "amount")},
		{"name": "update_transaction", "description": "更新既有交易；傳 id 及 create_transaction 欄位。", "inputSchema": req("id", "date", "description", "amount")},
		{"name": "upload_receipt", "description": "上傳並附加單據到既有交易。傳 transaction_id、filename、mime_type 與 content_base64；只接受 PDF、JPG、PNG、WebP，最大 20 MB。", "inputSchema": req("transaction_id", "filename", "mime_type", "content_base64")},
		{"name": "save_project_budget", "description": "新增或更新專案總預算；amount 為分。傳 project_id、total_amount，可選 note。", "inputSchema": req("project_id", "total_amount")},
		{"name": "create_project_milestone", "description": "建立專案請款批次；金額為分。傳 project_id、name、planned_income，可選 note、sort_order。", "inputSchema": req("project_id", "name", "planned_income")},
		{"name": "create_budget_allocation", "description": "建立批次的公司保留或夥伴分配；金額為分。傳 milestone_id、recipient_kind(company|partner)、recipient_name、planned_amount，可選 counterparty_id。", "inputSchema": req("milestone_id", "recipient_kind", "recipient_name", "planned_amount")},
		{"name": "create_budget_posting", "description": "把既有日記帳交易分攤至預算。傳 transaction_id、allocation_kind(income|partner_payout|company_reserve|company_shared_cost)、amount(分)。income/company_reserve/partner_payout 要有 milestone_id；partner_payout 還要 budget_allocation_id。company_shared_cost 不帶 milestone_id。", "inputSchema": req("transaction_id", "allocation_kind", "amount")},
	}
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

func (s *Server) createProject(a map[string]any) (any, error) {
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
	return content(map[string]any{"id": id, "name": name}, nil)
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
	ms, err := models.ListMilestones(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	actual, err := models.GetProjectBudgetActuals(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	type milestoneReport struct {
		models.Milestone
		Allocations   []models.BudgetAllocation `json:"allocations"`
		ActualIncome  int64                     `json:"actual_income_cents"`
		ActualReserve int64                     `json:"actual_reserve_cents"`
		ActualPaid    map[int64]int64           `json:"actual_paid_by_allocation_cents"`
	}
	report := make([]milestoneReport, 0, len(ms))
	for _, m := range ms {
		allocations, e := models.ListBudgetAllocations(s.DB, m.ID)
		if e != nil {
			return nil, e
		}
		paid := map[int64]int64{}
		for _, allocation := range allocations {
			paid[allocation.ID] = actual.PaidByAllocation[allocation.ID]
		}
		report = append(report, milestoneReport{Milestone: m, Allocations: allocations, ActualIncome: actual.IncomeByMilestone[m.ID], ActualReserve: actual.ReserveByMilestone[m.ID], ActualPaid: paid})
	}
	return content(map[string]any{
		"project":    p,
		"budget":     b,
		"milestones": report,
	}, nil)
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
	milestoneID, amount := numID(a, "milestone_id"), numID(a, "planned_amount")
	kind, name := str(a, "recipient_kind"), strings.TrimSpace(str(a, "recipient_name"))
	if milestoneID <= 0 || amount < 0 || name == "" || (kind != "company" && kind != "partner") {
		return nil, fmtErr("milestone_id、recipient_kind、recipient_name 與非負的 planned_amount（分）必填")
	}
	if _, err := models.ListBudgetAllocations(s.DB, milestoneID); err != nil {
		return nil, fmtErr("找不到請款批次")
	}
	x := &models.BudgetAllocation{MilestoneID: milestoneID, RecipientKind: kind, RecipientName: name, PlannedAmountCents: amount}
	if cp := numID(a, "counterparty_id"); cp > 0 {
		x.CounterpartyID, x.CounterpartyValid = cp, true
	}
	id, err := models.CreateBudgetAllocation(s.DB, x)
	return content(map[string]any{"id": id, "milestone_id": milestoneID}, err)
}

func (s *Server) createBudgetPosting(a map[string]any) (any, error) {
	txID, amount := numID(a, "transaction_id"), numID(a, "amount")
	kind := str(a, "allocation_kind")
	if txID <= 0 || amount <= 0 || (kind != "income" && kind != "partner_payout" && kind != "company_reserve" && kind != "company_shared_cost") {
		return nil, fmtErr("transaction_id、正數 amount（分）及有效 allocation_kind 必填")
	}
	t, err := models.GetTransaction(s.DB, txID)
	if err != nil {
		return nil, fmtErr("找不到交易")
	}
	p := &models.BudgetPosting{TransactionID: txID, Kind: kind, AmountCents: amount, Note: str(a, "note")}
	if milestoneID := numID(a, "milestone_id"); milestoneID > 0 {
		p.MilestoneID, p.MilestoneValid = milestoneID, true
	}
	if allocationID := numID(a, "budget_allocation_id"); allocationID > 0 {
		p.AllocationID, p.AllocationValid = allocationID, true
	}
	if kind == "company_shared_cost" && p.MilestoneValid {
		return nil, fmtErr("company_shared_cost 不可歸屬請款批次")
	}
	if kind != "company_shared_cost" && !p.MilestoneValid {
		return nil, fmtErr("此分攤類型必須提供 milestone_id")
	}
	if p.MilestoneValid {
		if !t.ProjectID.Valid {
			return nil, fmtErr("此交易未指定專案，不能歸屬請款批次")
		}
		ok, e := models.MilestoneBelongsToProject(s.DB, p.MilestoneID, t.ProjectID.Int64)
		if e != nil || !ok {
			return nil, fmtErr("請款批次不屬於交易專案")
		}
	}
	if kind == "partner_payout" && !p.AllocationValid {
		return nil, fmtErr("partner_payout 必須提供 budget_allocation_id")
	}
	if p.AllocationValid {
		ok, e := models.BudgetAllocationBelongsToMilestone(s.DB, p.AllocationID, p.MilestoneID)
		if e != nil || !ok {
			return nil, fmtErr("預算分配不屬於所選請款批次")
		}
	}
	if kind != "company_reserve" {
		used, e := models.SumBudgetPostingsByKind(s.DB, txID, kind)
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
