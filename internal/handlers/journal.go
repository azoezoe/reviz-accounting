package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/hcchien/reviz-accounting/internal/models"
	"github.com/hcchien/reviz-accounting/internal/money"
)

const pageSize = 50

type budgetPostingForm struct {
	Kind         string
	ProjectID    string
	AllocationID string
	Amount       string
	Note         string
	Error        string
	ErrorField   string
}

func (s *Server) journalList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := models.TxFilter{
		YearMonth:  q.Get("month"),
		Year:       q.Get("year"),
		CategoryID: parseInt64(q.Get("category_id")),
		ProjectID:  parseInt64(q.Get("project_id")),
		AccountID:  parseInt64(q.Get("account_id")),
		SearchText: q.Get("q"),
		Limit:      pageSize,
		Offset:     int(parseInt64(q.Get("offset"))),
	}
	txs, total, err := models.ListTransactions(s.DB, f)
	if err != nil {
		s.error500(w, err)
		return
	}
	balances, err := models.AccountBalances(s.DB)
	if err != nil {
		s.error500(w, err)
		return
	}
	allAccounts, err := models.ListAccounts(s.DB, false)
	if err != nil {
		s.error500(w, err)
		return
	}
	// Running balances must be derived from the complete ledger, not just the
	// current filter/page; otherwise a project filter or page two would show a
	// misleading historical balance.
	allTxs, _, err := models.ListTransactions(s.DB, models.TxFilter{})
	if err != nil {
		s.error500(w, err)
		return
	}
	allViews := journalTransactionViews(allTxs, balances)
	byID := make(map[int64]journalTransactionView, len(allViews))
	for _, view := range allViews {
		byID[view.ID] = view
	}
	txViews := make([]journalTransactionView, 0, len(txs))
	for _, tx := range txs {
		txViews = append(txViews, byID[tx.ID])
	}
	type accountBalanceView struct {
		models.Account
		Balance int64
	}
	accountViews := make([]accountBalanceView, 0, len(allAccounts))
	for _, account := range allAccounts {
		accountViews = append(accountViews, accountBalanceView{Account: account, Balance: balances[account.ID]})
	}
	cats, _ := models.ListCategories(s.DB)
	accs, _ := models.ListAccounts(s.DB, true)
	projs, _ := models.ListProjects(s.DB)
	counterparties, _ := models.ListCounterparties(s.DB, "")

	// Build month options from distinct YYYY-MM in transactions.
	monthOpts := s.distinctMonths()

	s.render(w, r, "journal_list.html", map[string]any{
		"Title":           "日記帳",
		"Crumbs":          []string{"日記帳"},
		"Transactions":    txViews,
		"Total":           total,
		"Filter":          f,
		"Categories":      cats,
		"Accounts":        accs,
		"AccountBalances": accountViews,
		"Projects":        projs,
		"Counterparties":  counterparties,
		"MonthOptions":    monthOpts,
		"NextOffset":      f.Offset + pageSize,
		"PrevOffset":      max(0, f.Offset-pageSize),
		"Active":          "journal",
	})
}

// journalTransactionView carries the balance immediately after each listed
// transaction. The list is newest-first, so we start from today's balances and
// reverse each row as we move backward through time.
type journalTransactionView struct {
	models.Transaction
	FromBalanceAfter int64
	ToBalanceAfter   int64
	HasFromBalance   bool
	HasToBalance     bool
}

func journalTransactionViews(txs []models.Transaction, current map[int64]int64) []journalTransactionView {
	balance := make(map[int64]int64, len(current))
	for id, amount := range current {
		balance[id] = amount
	}
	views := make([]journalTransactionView, 0, len(txs))
	for _, tx := range txs {
		v := journalTransactionView{Transaction: tx}
		if tx.FromAccountID.Valid {
			v.HasFromBalance = true
			v.FromBalanceAfter = balance[tx.FromAccountID.Int64]
			balance[tx.FromAccountID.Int64] += tx.AmountCents
		}
		if tx.ToAccountID.Valid {
			v.HasToBalance = true
			v.ToBalanceAfter = balance[tx.ToAccountID.Int64]
			balance[tx.ToAccountID.Int64] -= tx.AmountCents
		}
		views = append(views, v)
	}
	return views
}

func (s *Server) distinctMonths() []string {
	rows, err := s.DB.Query(`SELECT DISTINCT substr(tx_date,1,7) AS m FROM transactions ORDER BY m DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return out
		}
		out = append(out, m)
	}
	return out
}

func (s *Server) journalNew(w http.ResponseWriter, r *http.Request) {
	cats, _ := models.ListCategories(s.DB)
	accs, _ := models.ListAccounts(s.DB, true)
	projs, _ := models.ListProjects(s.DB)
	counterparties, _ := models.ListCounterparties(s.DB, "")
	today := time.Now().Format("2006-01-02")

	s.render(w, r, "journal_form.html", map[string]any{
		"Title":          "新增交易",
		"Crumbs":         []string{"日記帳", "新增交易"},
		"Mode":           "new",
		"Tx":             &models.Transaction{Date: today},
		"Categories":     cats,
		"Accounts":       accs,
		"Projects":       projs,
		"Counterparties": counterparties,
		"Active":         "journal",
	})
}

func (s *Server) journalEdit(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	t, err := models.GetTransaction(s.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.renderJournalEdit(w, r, t, &budgetPostingForm{Kind: "partner_payout"})
}

func (s *Server) renderJournalEdit(w http.ResponseWriter, r *http.Request, t *models.Transaction, postingForm *budgetPostingForm) {
	cats, _ := models.ListCategories(s.DB)
	accs, _ := models.ListAccounts(s.DB, true)
	projs, _ := models.ListProjects(s.DB)
	counterparties, _ := models.ListCounterparties(s.DB, "")
	attachments, _ := models.ListAttachments(s.DB, t.ID)
	postings, _ := models.ListBudgetPostings(s.DB, t.ID)
	allocations, _ := models.ListAllProjectBudgetAllocations(s.DB)

	s.render(w, r, "journal_form.html", map[string]any{
		"Title":             "編輯交易",
		"Crumbs":            []string{"日記帳", "編輯", t.Code},
		"Mode":              "edit",
		"Tx":                t,
		"AmountText":        money.FormatCents(t.AmountCents),
		"Categories":        cats,
		"Accounts":          accs,
		"Projects":          projs,
		"Counterparties":    counterparties,
		"Attachments":       attachments,
		"BudgetPostings":    postings,
		"BudgetAllocations": allocations,
		"BudgetPostingForm": postingForm,
		"Active":            "journal",
	})
}

func (s *Server) journalBudgetPostingError(w http.ResponseWriter, r *http.Request, t *models.Transaction, form *budgetPostingForm, field, message string) {
	form.Error = message
	form.ErrorField = field
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.renderJournalEdit(w, r, t, form)
}

func (s *Server) journalBudgetPostingCreate(w http.ResponseWriter, r *http.Request) {
	txID := parseInt64(r.PathValue("id"))
	t, err := models.GetTransaction(s.DB, txID)
	if err != nil {
		http.Error(w, "找不到交易", 404)
		return
	}
	form := &budgetPostingForm{Kind: r.FormValue("allocation_kind"), ProjectID: r.FormValue("project_id"), AllocationID: r.FormValue("budget_allocation_id"), Amount: r.FormValue("amount"), Note: r.FormValue("note")}
	amt, err := money.ParseCents(r.FormValue("amount"))
	if err != nil || amt <= 0 {
		s.journalBudgetPostingError(w, r, t, form, "amount", "請輸入大於 0 的有效分攤金額")
		return
	}
	kind := r.FormValue("allocation_kind")
	if kind != "income" && kind != "partner_payout" && kind != "cost_expense" && kind != "company_reserve" && kind != "company_shared_cost" {
		s.journalBudgetPostingError(w, r, t, form, "kind", "請選擇有效的分攤類型")
		return
	}
	p := &models.BudgetPosting{TransactionID: txID, Kind: kind, AmountCents: amt, Note: r.FormValue("note")}
	if aid := parseInt64(r.FormValue("budget_allocation_id")); aid > 0 {
		p.AllocationID = aid
		p.AllocationValid = true
	}
	if p.AllocationValid {
		projectID := parseInt64(r.FormValue("project_id"))
		if projectID <= 0 {
			s.journalBudgetPostingError(w, r, t, form, "project", "請選擇此筆分攤所屬專案")
			return
		}
		ok, e := models.BudgetAllocationBelongsToProject(s.DB, p.AllocationID, projectID)
		if e != nil {
			s.error500(w, e)
			return
		}
		if !ok {
			s.journalBudgetPostingError(w, r, t, form, "allocation", "選擇的預算項目不屬於此分攤專案")
			return
		}
	}
	if (kind == "partner_payout" || kind == "cost_expense") && !p.AllocationValid {
		s.journalBudgetPostingError(w, r, t, form, "allocation", "勞務報酬或成本支出必須對應一個專案預算項目")
		return
	}
	if p.AllocationValid {
		allocationKind, e := models.BudgetAllocationKind(s.DB, p.AllocationID)
		if e != nil {
			s.error500(w, e)
			return
		}
		if (kind == "partner_payout" && allocationKind != "labor_compensation") || (kind == "cost_expense" && allocationKind != "cost_expense") || (kind == "company_reserve" && allocationKind != "company_reserve") {
			s.journalBudgetPostingError(w, r, t, form, "allocation", "分攤類型必須對應相同用途類別的專案預算項目")
			return
		}
	}
	// A company reserve is a reporting attribution of income, not a second
	// cash movement. All other types share the same payment amount, including
	// rows attributed to different projects.
	if kind != "company_reserve" {
		used, err := models.SumCashBudgetPostings(s.DB, txID)
		if err != nil {
			s.error500(w, err)
			return
		}
		if used+amt > t.AmountCents {
			s.journalBudgetPostingError(w, r, t, form, "amount", "此類型的分攤總額不能超過交易金額")
			return
		}
	}
	if _, err = models.CreateBudgetPosting(s.DB, p); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/journal/"+r.PathValue("id")+"/edit", 303)
}
func (s *Server) journalBudgetPostingDelete(w http.ResponseWriter, r *http.Request) {
	_ = models.DeleteBudgetPosting(s.DB, parseInt64(r.PathValue("postingID")))
	http.Redirect(w, r, "/journal/"+r.PathValue("id")+"/edit", 303)
}

func (s *Server) journalCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.error500(w, err)
		return
	}
	t, err := s.buildTransactionFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	code, err := models.GenerateCode(s.DB)
	if err != nil {
		s.error500(w, err)
		return
	}
	t.Code = code
	if _, err := models.CreateTransaction(s.DB, t); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/journal", http.StatusSeeOther)
}

func (s *Server) journalUpdate(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	if err := r.ParseForm(); err != nil {
		s.error500(w, err)
		return
	}
	t, err := s.buildTransactionFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t.ID = id
	if err := models.UpdateTransaction(s.DB, t); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/journal", http.StatusSeeOther)
}

func (s *Server) journalDelete(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	if err := models.DeleteTransaction(s.DB, id); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/journal", http.StatusSeeOther)
}

func (s *Server) buildTransactionFromForm(r *http.Request) (*models.Transaction, error) {
	amtCents, err := money.ParseCents(r.FormValue("amount"))
	if err != nil {
		return nil, err
	}
	if amtCents < 0 {
		amtCents = -amtCents
	}
	from := models.NullInt64From(parseInt64(r.FormValue("from_account_id")))
	to := models.NullInt64From(parseInt64(r.FormValue("to_account_id")))
	cat := models.NullInt64From(parseInt64(r.FormValue("category_id")))
	proj := models.NullInt64From(parseInt64(r.FormValue("project_id")))

	if !from.Valid && !to.Valid {
		return nil, errBadInput("請至少指定『轉出帳戶』或『轉入帳戶』")
	}
	if from.Valid && to.Valid && from.Int64 == to.Int64 {
		return nil, errBadInput("『轉出帳戶』與『轉入帳戶』不能相同")
	}
	if amtCents == 0 {
		return nil, errBadInput("金額不可為 0")
	}
	date := r.FormValue("tx_date")
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, errBadInput("日期格式錯誤")
	}
	counterpartyID, err := models.GetOrCreateCounterparty(s.DB, r.FormValue("counterparty"))
	if err != nil {
		return nil, err
	}
	return &models.Transaction{
		Date:           date,
		Description:    r.FormValue("description"),
		CounterpartyID: counterpartyID,
		CategoryID:     cat,
		AmountCents:    amtCents,
		FromAccountID:  from,
		ToAccountID:    to,
		ProjectID:      proj,
		Note:           r.FormValue("note"),
	}, nil
}

type inputError struct{ msg string }

func (e inputError) Error() string { return e.msg }
func errBadInput(s string) error   { return inputError{s} }

// silence unused import if database/sql ends up unused
var _ = sql.ErrNoRows
