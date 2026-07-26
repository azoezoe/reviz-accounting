package handlers

import (
	"database/sql"
	"testing"

	"github.com/hcchien/reviz-accounting/internal/models"
)

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
