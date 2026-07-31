package mcp

import (
	"testing"

	"github.com/hcchien/reviz-accounting/internal/auth"
)

func TestBudgetToolsAreAdvertised(t *testing.T) {
	want := map[string]bool{
		"list_projects":             false,
		"create_project":            false,
		"update_project":            false,
		"get_project_budget":        false,
		"list_project_transactions": false,
		"save_project_budget":       false,
		"create_budget_allocation":  false,
		"create_budget_posting":     false,
	}
	for _, tool := range tools() {
		if _, ok := want[tool["name"].(string)]; ok {
			want[tool["name"].(string)] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("budget MCP tool %q is not advertised", name)
		}
	}
}

func TestManagementToolsAreAdvertised(t *testing.T) {
	want := map[string]bool{
		"list_quotes":                  false,
		"get_quote":                    false,
		"create_quote":                 false,
		"update_quote":                 false,
		"delete_quote":                 false,
		"create_standalone_quote_item": false,
		"revise_quote":                 false,
		"accept_quote":                 false,
		"get_project_management":       false,
		"create_project_quote":         false,
		"create_quote_item":            false,
		"revise_project_quote":         false,
		"accept_project_quote":         false,
		"create_project_role":          false,
		"create_time_entry":            false,
		"create_project_receivable":    false,
		"toggle_project_receivable":    false,
		"create_project_cost":          false,
		"toggle_project_cost":          false,
	}
	for _, tool := range tools() {
		if _, ok := want[tool["name"].(string)]; ok {
			want[tool["name"].(string)] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("management MCP tool %q is not advertised", name)
		}
	}
}

func TestViewerCannotCallManagementWriteTool(t *testing.T) {
	s := &Server{}
	names := []string{
		"create_quote", "update_quote", "delete_quote", "create_standalone_quote_item", "revise_quote", "accept_quote",
		"create_project_quote", "create_quote_item", "revise_project_quote", "accept_project_quote",
		"create_project_role", "create_time_entry", "create_project_receivable",
		"toggle_project_receivable", "create_project_cost", "toggle_project_cost",
	}
	for _, name := range names {
		_, err := s.tool(&auth.User{Role: auth.RoleViewer}, name, map[string]any{"project_id": float64(1)})
		if err == nil || err.Error() != "權限不足" {
			t.Errorf("viewer %s error = %v, want 權限不足", name, err)
		}
	}
}

func TestAuditUsesActualMCPToolName(t *testing.T) {
	raw := []byte(`{"name":"accept_project_quote","arguments":{"project_id":1,"quote_id":2}}`)
	if got := auditToolName("tools/call", raw); got != "accept_project_quote" {
		t.Fatalf("audit name = %q", got)
	}
}
