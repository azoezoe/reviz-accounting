package mcp

import "testing"

func TestBudgetToolsAreAdvertised(t *testing.T) {
	want := map[string]bool{
		"list_projects":             false,
		"create_project":            false,
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
