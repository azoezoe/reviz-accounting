package db

import (
	"strings"
	"testing"
)

// Schema is executed one semicolon-delimited statement at a time. Keep tables
// below the foreign-key targets they reference, including on a fresh database.
func TestBudgetTablesFollowForeignKeyTargets(t *testing.T) {
	for _, table := range []string{"project_budgets", "project_milestones", "project_budget_allocations", "transaction_budget_allocations"} {
		if strings.Index(schemaSQL, "CREATE TABLE IF NOT EXISTS "+table) < strings.Index(schemaSQL, "CREATE TABLE IF NOT EXISTS transactions") {
			t.Fatalf("%s must be created after transactions", table)
		}
	}
}
