package handlers

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/hcchien/reviz-accounting/internal/auth"
	"github.com/hcchien/reviz-accounting/internal/mcp"
	"github.com/hcchien/reviz-accounting/internal/models"
	"github.com/hcchien/reviz-accounting/internal/money"
	filestore "github.com/hcchien/reviz-accounting/internal/storage"
)

// Server wires the DB, templates, and routes.
type Server struct {
	DB              *sql.DB
	templates       map[string]*template.Template
	SimpanyTemplate []byte // raw .xlsx bytes of the Simpany template
	Attachments     filestore.Store
}

// NewServer parses the embedded templates and returns a ready Server.
// Each page template is parsed in its own template tree alongside base.html,
// so the page-specific {{define "content"}} blocks do not collide.
func NewServer(d *sql.DB, embedFS embed.FS, attachments filestore.Store) (*Server, error) {
	funcs := template.FuncMap{
		"money":    money.FormatCentsThousands,
		"moneyRaw": money.FormatCents,
		"dict":     dict,
		"add":      func(a, b int) int { return a + b },
		"sub":      func(a, b int) int { return a - b },
		"mul":      func(a, b int) int { return a * b },
		"mod":      func(a, b int) int { return a % b },
		"addi":     func(a, b int) int { return a + b },
		"divf":     func(a, b int) float64 { return float64(a) / float64(b) },
		"pct": func(a, b int64) int {
			if b == 0 {
				return 0
			}
			return int(a * 100 / b)
		},
		"seq":            seq,
		"hasPrefix":      stringHasPrefix,
		"contains":       stringContains,
		"yearMonths":     yearMonths,
		"monthLabel":     monthLabel,
		"signClass":      signClass,
		"toneClass":      toneClass,
		"groupLabel":     groupLabel,
		"kindLabel":      kindLabel,
		"int64":          func(n int) int64 { return int64(n) },
		"intIdx":         func(arr [13]int64, i int) int64 { return arr[i] },
		"attachmentSize": formatAttachmentSize,
	}
	entries, err := fs.ReadDir(embedFS, "web/templates")
	if err != nil {
		return nil, fmt.Errorf("read templates dir: %w", err)
	}
	tpls := map[string]*template.Template{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".html") || name == "base.html" {
			continue
		}
		t, err := template.New(name).Funcs(funcs).ParseFS(
			embedFS,
			path.Join("web/templates", "base.html"),
			path.Join("web/templates", name),
		)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		tpls[name] = t
	}
	return &Server{DB: d, templates: tpls, Attachments: attachments}, nil
}

// Routes registers all HTTP routes onto the given mux.
//
// Route privilege levels:
//   - public            : /login (GET/POST), /logout
//   - any authenticated : all GET pages
//   - accountant+       : all POST routes that mutate accounting data
//   - owner only        : /users and POST /users/* (user management)
func (s *Server) Routes(mux *http.ServeMux) {
	mcpServer := &mcp.Server{DB: s.DB, Attachments: s.Attachments}
	mux.Handle("GET /.well-known/oauth-authorization-server", http.HandlerFunc(mcpServer.Metadata))
	mux.Handle("GET /.well-known/oauth-protected-resource", http.HandlerFunc(mcpServer.ProtectedResource))
	mux.Handle("POST /oauth/register", http.HandlerFunc(mcpServer.Register))
	mux.Handle("GET /oauth/authorize", http.HandlerFunc(mcpServer.Authorize))
	mux.Handle("POST /oauth/authorize", auth.RequireAuth(http.HandlerFunc(mcpServer.Approve)))
	mux.Handle("POST /oauth/token", http.HandlerFunc(mcpServer.Token))
	mux.Handle("/mcp", http.HandlerFunc(mcpServer.MCP))
	// public
	mux.Handle("GET /login", http.HandlerFunc(s.loginPage))
	mux.Handle("POST /login", http.HandlerFunc(s.loginSubmit))
	mux.Handle("POST /logout", http.HandlerFunc(s.logout))
	mux.Handle("GET /logout", http.HandlerFunc(s.logout))

	// any authenticated user (viewer+)
	view := func(h http.HandlerFunc) http.Handler { return auth.RequireAuth(http.HandlerFunc(h)) }
	mux.Handle("GET /{$}", view(s.dashboard))
	mux.Handle("GET /dashboard", view(s.dashboard))
	mux.Handle("GET /journal", view(s.journalList))
	mux.Handle("GET /journal/new", view(s.journalNew))
	mux.Handle("GET /journal/{id}/edit", view(s.journalEdit))
	mux.Handle("GET /attachments/{id}", view(s.attachmentDownload))
	mux.Handle("GET /accounts", view(s.accountsList))
	mux.Handle("GET /categories", view(s.categoriesList))
	mux.Handle("GET /projects", view(s.projectsList))
	mux.Handle("GET /counterparties", view(s.counterpartiesList))
	mux.Handle("GET /pnl", view(s.pnl))
	mux.Handle("GET /settings", view(s.settingsPage))
	mux.Handle("GET /export/transactions.csv", view(s.exportCSV))
	mux.Handle("GET /export/monthly.xlsx", view(s.exportMonthlyXLSX))
	mux.Handle("GET /import", view(s.importPage))
	mux.Handle("GET /profile/password", view(s.passwordPage))
	mux.Handle("POST /profile/password", view(s.passwordUpdate))

	// accountant + owner can mutate
	acct := func(h http.HandlerFunc) http.Handler {
		return auth.RequireRole(auth.RoleAccountant, http.HandlerFunc(h))
	}
	mux.Handle("POST /journal", acct(s.journalCreate))
	mux.Handle("POST /journal/{id}", acct(s.journalUpdate))
	mux.Handle("POST /journal/{id}/delete", acct(s.journalDelete))
	mux.Handle("POST /journal/{id}/attachments", acct(s.attachmentUpload))
	mux.Handle("POST /attachments/{id}/delete", acct(s.attachmentDelete))
	mux.Handle("POST /accounts", acct(s.accountCreate))
	mux.Handle("POST /accounts/{id}", acct(s.accountUpdate))
	mux.Handle("POST /accounts/{id}/delete", acct(s.accountDelete))
	mux.Handle("POST /categories", acct(s.categoryCreate))
	mux.Handle("POST /categories/{id}", acct(s.categoryUpdate))
	mux.Handle("POST /categories/{id}/delete", acct(s.categoryDelete))
	mux.Handle("POST /projects", acct(s.projectCreate))
	mux.Handle("POST /projects/{id}", acct(s.projectUpdate))
	mux.Handle("POST /projects/{id}/delete", acct(s.projectDelete))
	mux.Handle("POST /counterparties", acct(s.counterpartyCreate))
	mux.Handle("POST /counterparties/{id}", acct(s.counterpartyUpdate))
	mux.Handle("POST /counterparties/{id}/delete", acct(s.counterpartyDelete))
	mux.Handle("POST /settings", acct(s.settingsSave))
	mux.Handle("POST /import", acct(s.importCSV))

	// owner only
	owner := func(h http.HandlerFunc) http.Handler {
		return auth.RequireRole(auth.RoleOwner, http.HandlerFunc(h))
	}
	mux.Handle("GET /users", owner(s.usersList))
	mux.Handle("POST /users", owner(s.userCreate))
	mux.Handle("POST /users/{id}", owner(s.userUpdate))
	mux.Handle("POST /users/{id}/delete", owner(s.userDelete))
}

// render writes a template inside the common chrome (header + nav).
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	s.renderWithChrome(w, r, name, data, true)
}

// renderStandalone renders a template without the navigation chrome (used by
// /login).
func (s *Server) renderStandalone(w http.ResponseWriter, name string, data map[string]any) {
	s.renderWithChrome(w, nil, name, data, false)
}

func (s *Server) renderWithChrome(w http.ResponseWriter, r *http.Request, name string, data map[string]any, chrome bool) {
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["Title"]; !ok {
		data["Title"] = "Reviz 帳簿"
	}
	if _, ok := data["CompanyName"]; !ok {
		c, _ := models.GetSetting(s.DB, "company_name")
		data["CompanyName"] = c
	}
	if _, ok := data["FiscalYear"]; !ok {
		data["FiscalYear"], _ = models.GetSetting(s.DB, "fiscal_year")
	}
	data["ShowChrome"] = chrome
	if r != nil {
		data["CurrentUser"] = auth.FromContext(r.Context())
	} else {
		data["CurrentUser"] = nil
	}
	// Default crumbs if not supplied
	if _, ok := data["Crumbs"]; !ok {
		title, _ := data["Title"].(string)
		company, _ := data["CompanyName"].(string)
		data["Crumbs"] = []string{company, title}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, ok := s.templates[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) error500(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// ----- template helpers -----

func dict(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		k, _ := kv[i].(string)
		m[k] = kv[i+1]
	}
	return m
}

func seq(start, end int) []int {
	if end < start {
		return nil
	}
	out := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, i)
	}
	return out
}

func stringHasPrefix(s, p string) bool {
	if len(s) < len(p) {
		return false
	}
	return s[:len(p)] == p
}

func stringContains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}

func yearMonths(year int) []string {
	out := make([]string, 12)
	for i := 0; i < 12; i++ {
		out[i] = fmt.Sprintf("%d-%02d", year, i+1)
	}
	return out
}

func monthLabel(i int) string {
	names := []string{"1月", "2月", "3月", "4月", "5月", "6月", "7月", "8月", "9月", "10月", "11月", "12月"}
	if i < 0 || i >= 12 {
		return ""
	}
	return names[i]
}

func signClass(c int64) string {
	switch {
	case c > 0:
		return "text-emerald-700"
	case c < 0:
		return "text-rose-700"
	default:
		return "text-slate-500"
	}
}

// toneClass returns a colour class appropriate for a P&L cell given its
// section tone ("income" green, "cost"/"expense" red) and value (zero values
// are muted regardless of section).
func toneClass(tone string, v int64) string {
	if v == 0 {
		return "text-slate-300"
	}
	switch tone {
	case "income":
		return "text-emerald-700"
	case "cost", "expense":
		return "text-rose-700"
	default:
		return signClass(v)
	}
}

func groupLabel(g string) string {
	return map[string]string{
		"income":  "收入",
		"cost":    "成本",
		"expense": "費用",
		"equity":  "股東權益",
		"other":   "其他",
	}[g]
}

func kindLabel(k string) string {
	return map[string]string{
		"asset":     "資產",
		"liability": "負債",
	}[k]
}

// fmtMoney renders cents as a thousand-separated, 2-decimal string.
func fmtMoney(c int64) string {
	return money.FormatCentsThousands(c)
}

// splitMoney returns the integer part of FormatCentsThousands (no decimals),
// useful for the design's split-display where the .xx is faded.
func splitMoney(c int64) string {
	s := money.FormatCentsThousands(c)
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[:i]
	}
	return s
}
