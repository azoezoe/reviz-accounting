package models

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCrossProjectBudgetPostingsShareOnePaymentLimit(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE transactions (id INTEGER PRIMARY KEY, project_id INTEGER, amount_cents INTEGER NOT NULL);
		CREATE TABLE project_budget_allocations (id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL, recipient_kind TEXT NOT NULL, counterparty_id INTEGER, recipient_name TEXT NOT NULL, planned_amount_cents INTEGER NOT NULL);
		CREATE TABLE transaction_budget_allocations (id INTEGER PRIMARY KEY, transaction_id INTEGER NOT NULL, milestone_id INTEGER, budget_allocation_id INTEGER, allocation_kind TEXT NOT NULL, amount_cents INTEGER NOT NULL, note TEXT NOT NULL DEFAULT '');
		INSERT INTO projects(id,name) VALUES(1,'專案 A'),(2,'專案 B');
		INSERT INTO transactions(id,amount_cents) VALUES(10,30000);
		INSERT INTO project_budget_allocations(id,project_id,recipient_kind,recipient_name,planned_amount_cents) VALUES(101,1,'labor_compensation','A 夥伴',12000),(202,2,'labor_compensation','A 夥伴',18000);
		INSERT INTO transaction_budget_allocations(transaction_id,budget_allocation_id,allocation_kind,amount_cents) VALUES(10,101,'partner_payout',12000),(10,202,'partner_payout',18000);
	`)
	if err != nil {
		t.Fatal(err)
	}
	used, err := SumCashBudgetPostings(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if used != 30000 {
		t.Fatalf("cross-project cash split = %d, want 30000", used)
	}
	postings, err := ListBudgetPostings(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 2 || postings[0].ProjectName != "專案 A" || postings[1].ProjectName != "專案 B" {
		t.Fatalf("postings = %#v; want two project-labelled splits", postings)
	}
	counts, err := BudgetPostingCountsForProject(db, 2)
	if err != nil {
		t.Fatal(err)
	}
	if counts[10] != 2 {
		t.Fatalf("project B posting count = %d, want 2", counts[10])
	}
}
