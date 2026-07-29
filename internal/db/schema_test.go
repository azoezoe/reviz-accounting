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

func TestManagementTablesFollowProjects(t *testing.T) {
	projects := strings.Index(schemaSQL, "CREATE TABLE IF NOT EXISTS projects")
	for _, table := range []string{"project_quotes", "project_roles", "project_time_entries", "project_receivables", "project_cost_items"} {
		if pos := strings.Index(schemaSQL, "CREATE TABLE IF NOT EXISTS "+table); pos < projects {
			t.Fatalf("%s must be created after projects", table)
		}
	}
	if strings.Index(schemaSQL, "CREATE TABLE IF NOT EXISTS project_quote_items") <
		strings.Index(schemaSQL, "CREATE TABLE IF NOT EXISTS project_quotes") {
		t.Fatal("project_quote_items must be created after project_quotes")
	}
}
