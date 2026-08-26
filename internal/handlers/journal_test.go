package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hcchien/reviz-accounting/internal/models"
	_ "modernc.org/sqlite"
)

func TestJournalProjectExpenseUsesBudgetItemPurposeInsteadOfTransactionCategory(t *testing.T) {
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.SetMaxOpenConns(1)
	_, err = d.Exec(`
		CREATE TABLE counterparties (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE transactions (
			id INTEGER PRIMARY KEY, code TEXT NOT NULL, tx_date TEXT NOT NULL,
			description TEXT NOT NULL, counterparty_id INTEGER, category_id INTEGER,
			amount_cents INTEGER NOT NULL, from_account_id INTEGER, to_account_id INTEGER,
			project_id INTEGER, note TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE project_budget_allocations (
			id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL,
			recipient_kind TEXT NOT NULL, recipient_name TEXT NOT NULL
		);
		CREATE TABLE transaction_budget_allocations (
			id INTEGER PRIMARY KEY AUTOINCREMENT, transaction_id INTEGER NOT NULL,
			milestone_id INTEGER, budget_allocation_id INTEGER,
			allocation_kind TEXT NOT NULL, amount_cents INTEGER NOT NULL,
			note TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO transactions
			(id,code,tx_date,description,category_id,amount_cents,from_account_id,note)
		VALUES(10,'TX-10','2026-08-26','專案出差',16,500000,1,'費用・差旅費用');
		INSERT INTO project_budget_allocations
			(id,project_id,recipient_kind,recipient_name)
		VALUES(101,7,'cost_expense','專案差旅費');
	`)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"allocation_kind":      {"project_expense"},
		"project_id":           {"7"},
		"budget_allocation_id": {"101"},
		"amount":               {"3,000"},
	}
	req := httptest.NewRequest(http.MethodPost, "/journal/10/budget-postings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "10")
	rec := httptest.NewRecorder()
	(&Server{DB: d}).journalBudgetPostingCreate(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("posting status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var kind string
	var allocationID, amount int64
	if err := d.QueryRow(`SELECT allocation_kind,budget_allocation_id,amount_cents FROM transaction_budget_allocations WHERE transaction_id=10`).Scan(&kind, &allocationID, &amount); err != nil {
		t.Fatal(err)
	}
	if kind != "cost_expense" || allocationID != 101 || amount != 300000 {
		t.Fatalf("posting = kind %q, allocation %d, amount %d", kind, allocationID, amount)
	}
}

func TestBudgetPostingKindForAllocation(t *testing.T) {
	tests := []struct {
		allocationKind string
		wantKind       string
		wantOK         bool
	}{
		{allocationKind: "labor_compensation", wantKind: "partner_payout", wantOK: true},
		{allocationKind: "cost_expense", wantKind: "cost_expense", wantOK: true},
		{allocationKind: "company_reserve", wantOK: false},
	}
	for _, tt := range tests {
		gotKind, gotOK := budgetPostingKindForAllocation(tt.allocationKind)
		if gotKind != tt.wantKind || gotOK != tt.wantOK {
			t.Fatalf("budgetPostingKindForAllocation(%q) = %q, %v; want %q, %v", tt.allocationKind, gotKind, gotOK, tt.wantKind, tt.wantOK)
		}
	}
}

func TestJournalTransactionViewsShowPostTransactionBalance(t *testing.T) {
	txs := []models.Transaction{
		{AmountCents: 300, ToAccountID: sql.NullInt64{Int64: 1, Valid: true}},
		{AmountCents: 100, FromAccountID: sql.NullInt64{Int64: 1, Valid: true}},
	}
	views := journalTransactionViews(txs, map[int64]int64{1: 200})
	if !views[0].HasToBalance || views[0].ToBalanceAfter != 200 {
		t.Fatalf("newest income balance = %d, want 200", views[0].ToBalanceAfter)
	}
	if !views[1].HasFromBalance || views[1].FromBalanceAfter != -100 {
		t.Fatalf("older expense balance = %d, want -100", views[1].FromBalanceAfter)
	}
}
