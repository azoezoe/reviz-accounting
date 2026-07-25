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
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil {
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
		return map[string]any{"tools": []map[string]any{{"name": "list_accounts", "description": "列出帳戶", "inputSchema": map[string]any{"type": "object"}}, {"name": "list_categories", "description": "列出分類", "inputSchema": map[string]any{"type": "object"}}, {"name": "list_transactions", "description": "查詢交易，可帶 year_month、search、limit", "inputSchema": map[string]any{"type": "object"}}, {"name": "create_transaction", "description": "新增交易；傳 date、description、amount、from_account_id/to_account_id、category_id、counterparty、note", "inputSchema": map[string]any{"type": "object"}}, {"name": "update_transaction", "description": "更新既有交易；傳 id 及 create_transaction 欄位", "inputSchema": map[string]any{"type": "object"}}, {"name": "upload_receipt", "description": "上傳並附加單據到既有交易。傳 transaction_id、filename、mime_type 與 content_base64；只接受 PDF、JPG、PNG、WebP，最大 20 MB。", "inputSchema": map[string]any{"type": "object", "required": []string{"transaction_id", "filename", "mime_type", "content_base64"}}}}}, nil
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
	}
	return nil, fmtErr("unknown tool")
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
func valid(v int64) bool                    { return v > 0 }
func str(m map[string]any, k string) string { v, _ := m[k].(string); return v }
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
