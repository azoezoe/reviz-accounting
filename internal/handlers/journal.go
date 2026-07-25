package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/hcchien/reviz-accounting/internal/models"
	"github.com/hcchien/reviz-accounting/internal/money"
)

const pageSize = 50

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
	cats, _ := models.ListCategories(s.DB)
	accs, _ := models.ListAccounts(s.DB, true)
	projs, _ := models.ListProjects(s.DB)
	counterparties, _ := models.ListCounterparties(s.DB, "")

	// Build month options from distinct YYYY-MM in transactions.
	monthOpts := s.distinctMonths()

	s.render(w, r, "journal_list.html", map[string]any{
		"Title":          "日記帳",
		"Crumbs":         []string{"日記帳"},
		"Transactions":   txs,
		"Total":          total,
		"Filter":         f,
		"Categories":     cats,
		"Accounts":       accs,
		"Projects":       projs,
		"Counterparties": counterparties,
		"MonthOptions":   monthOpts,
		"NextOffset":     f.Offset + pageSize,
		"PrevOffset":     max(0, f.Offset-pageSize),
		"Active":         "journal",
	})
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
	cats, _ := models.ListCategories(s.DB)
	accs, _ := models.ListAccounts(s.DB, true)
	projs, _ := models.ListProjects(s.DB)
	counterparties, _ := models.ListCounterparties(s.DB, "")
	attachments, _ := models.ListAttachments(s.DB, id)

	s.render(w, r, "journal_form.html", map[string]any{
		"Title":          "編輯交易",
		"Crumbs":         []string{"日記帳", "編輯", t.Code},
		"Mode":           "edit",
		"Tx":             t,
		"AmountText":     money.FormatCents(t.AmountCents),
		"Categories":     cats,
		"Accounts":       accs,
		"Projects":       projs,
		"Counterparties": counterparties,
		"Attachments":    attachments,
		"Active":         "journal",
	})
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
