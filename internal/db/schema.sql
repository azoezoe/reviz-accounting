CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS accounts (
 id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL UNIQUE, kind TEXT NOT NULL CHECK (kind IN ('asset','liability')),
 active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)), sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS categories (
 id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL UNIQUE, group_name TEXT NOT NULL CHECK (group_name IN ('income','cost','expense','equity','other')),
 sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS projects (
 id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL UNIQUE, start_date TEXT, end_date TEXT, note TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS counterparties (
 id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL UNIQUE, tax_id TEXT NOT NULL DEFAULT '', contact_name TEXT NOT NULL DEFAULT '',
 phone TEXT NOT NULL DEFAULT '', address TEXT NOT NULL DEFAULT '', email TEXT NOT NULL DEFAULT '', bank_name TEXT NOT NULL DEFAULT '',
 bank_account_name TEXT NOT NULL DEFAULT '', bank_account_no TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text,
 updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text
);
CREATE TABLE IF NOT EXISTS users (
 id BIGSERIAL PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
 role TEXT NOT NULL CHECK (role IN ('owner','accountant','viewer')), active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text, last_login_at TEXT
);
CREATE TABLE IF NOT EXISTS transactions (
 id BIGSERIAL PRIMARY KEY, code TEXT NOT NULL UNIQUE, tx_date TEXT NOT NULL, description TEXT NOT NULL,
 counterparty_id BIGINT REFERENCES counterparties(id) ON DELETE SET NULL, category_id BIGINT REFERENCES categories(id) ON DELETE RESTRICT,
 amount_cents BIGINT NOT NULL CHECK (amount_cents > 0), from_account_id BIGINT REFERENCES accounts(id) ON DELETE RESTRICT,
 to_account_id BIGINT REFERENCES accounts(id) ON DELETE RESTRICT, project_id BIGINT REFERENCES projects(id) ON DELETE SET NULL,
 note TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text,
 CHECK (from_account_id IS NOT NULL OR to_account_id IS NOT NULL)
);
CREATE TABLE IF NOT EXISTS project_budgets (
 id BIGSERIAL PRIMARY KEY, project_id BIGINT NOT NULL UNIQUE REFERENCES projects(id) ON DELETE CASCADE,
 total_amount_cents BIGINT NOT NULL DEFAULT 0 CHECK (total_amount_cents >= 0), note TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text
);
CREATE TABLE IF NOT EXISTS project_milestones (
 id BIGSERIAL PRIMARY KEY, project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 name TEXT NOT NULL, planned_income_cents BIGINT NOT NULL DEFAULT 0 CHECK (planned_income_cents >= 0), sort_order INTEGER NOT NULL DEFAULT 0, note TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS project_budget_allocations (
 id BIGSERIAL PRIMARY KEY, project_id BIGINT REFERENCES projects(id) ON DELETE CASCADE, milestone_id BIGINT REFERENCES project_milestones(id) ON DELETE CASCADE,
 recipient_kind TEXT NOT NULL CHECK (recipient_kind IN ('labor_compensation','company_reserve','cost_expense')), counterparty_id BIGINT REFERENCES counterparties(id) ON DELETE SET NULL,
 recipient_name TEXT NOT NULL, planned_amount_cents BIGINT NOT NULL DEFAULT 0 CHECK (planned_amount_cents >= 0)
);
ALTER TABLE project_budget_allocations ADD COLUMN IF NOT EXISTS project_id BIGINT REFERENCES projects(id) ON DELETE CASCADE;
ALTER TABLE project_budget_allocations ALTER COLUMN milestone_id DROP NOT NULL;
UPDATE project_budget_allocations a SET project_id=m.project_id FROM project_milestones m WHERE a.milestone_id=m.id AND a.project_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_budget_alloc_project ON project_budget_allocations(project_id);
CREATE TABLE IF NOT EXISTS transaction_budget_allocations (
 id BIGSERIAL PRIMARY KEY, transaction_id BIGINT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
 milestone_id BIGINT REFERENCES project_milestones(id) ON DELETE SET NULL,
 budget_allocation_id BIGINT REFERENCES project_budget_allocations(id) ON DELETE SET NULL,
 allocation_kind TEXT NOT NULL CHECK (allocation_kind IN ('income','partner_payout','cost_expense','company_reserve','company_shared_cost')),
 amount_cents BIGINT NOT NULL CHECK (amount_cents > 0), note TEXT NOT NULL DEFAULT ''
);
-- Existing deployments used company/partner labels. Preserve their intent
-- while upgrading to budget-purpose categories.
ALTER TABLE project_budget_allocations DROP CONSTRAINT IF EXISTS project_budget_allocations_recipient_kind_check;
UPDATE project_budget_allocations SET recipient_kind='company_reserve' WHERE recipient_kind='company';
UPDATE project_budget_allocations SET recipient_kind='labor_compensation' WHERE recipient_kind='partner';
ALTER TABLE project_budget_allocations ADD CONSTRAINT project_budget_allocations_recipient_kind_check CHECK (recipient_kind IN ('labor_compensation','company_reserve','cost_expense'));
ALTER TABLE transaction_budget_allocations DROP CONSTRAINT IF EXISTS transaction_budget_allocations_allocation_kind_check;
ALTER TABLE transaction_budget_allocations ADD CONSTRAINT transaction_budget_allocations_allocation_kind_check CHECK (allocation_kind IN ('income','partner_payout','cost_expense','company_reserve','company_shared_cost'));
CREATE TABLE IF NOT EXISTS sessions (
 id TEXT PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text,
 expires_at TEXT NOT NULL, user_agent TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS transaction_attachments (
 id BIGSERIAL PRIMARY KEY, transaction_id BIGINT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE, storage_key TEXT NOT NULL UNIQUE,
 original_filename TEXT NOT NULL, content_type TEXT NOT NULL, size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
 uploaded_by_id BIGINT REFERENCES users(id) ON DELETE SET NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text
);
CREATE TABLE IF NOT EXISTS mcp_oauth_clients (
 id TEXT PRIMARY KEY, redirect_uris TEXT NOT NULL, client_name TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text
);
CREATE TABLE IF NOT EXISTS mcp_authorization_codes (
 code_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL REFERENCES mcp_oauth_clients(id) ON DELETE CASCADE,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE, redirect_uri TEXT NOT NULL, code_challenge TEXT NOT NULL,
 expires_at TEXT NOT NULL, used_at TEXT
);
CREATE TABLE IF NOT EXISTS mcp_access_tokens (
 token_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL REFERENCES mcp_oauth_clients(id) ON DELETE CASCADE,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE, expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text
);
CREATE TABLE IF NOT EXISTS mcp_audit_log (
 id BIGSERIAL PRIMARY KEY, user_id BIGINT REFERENCES users(id) ON DELETE SET NULL, client_id TEXT NOT NULL DEFAULT '', tool_name TEXT NOT NULL,
 outcome TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text
);
CREATE INDEX IF NOT EXISTS idx_tx_date ON transactions(tx_date);
CREATE INDEX IF NOT EXISTS idx_tx_category ON transactions(category_id);
CREATE INDEX IF NOT EXISTS idx_tx_from ON transactions(from_account_id);
CREATE INDEX IF NOT EXISTS idx_tx_to ON transactions(to_account_id);
CREATE INDEX IF NOT EXISTS idx_tx_project ON transactions(project_id);
CREATE INDEX IF NOT EXISTS idx_milestones_project ON project_milestones(project_id);
CREATE INDEX IF NOT EXISTS idx_budget_alloc_milestone ON project_budget_allocations(milestone_id);
CREATE INDEX IF NOT EXISTS idx_tx_budget_alloc_tx ON transaction_budget_allocations(transaction_id);
CREATE INDEX IF NOT EXISTS idx_tx_counterparty ON transactions(counterparty_id);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_exp ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_attachments_transaction ON transaction_attachments(transaction_id);
CREATE INDEX IF NOT EXISTS idx_mcp_tokens_user ON mcp_access_tokens(user_id);
