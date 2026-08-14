CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO settings(key,value) VALUES
 ('company_name','睿藝有限公司 ReViz'),
 ('company_contact','簡信昌'),
 ('company_email','hcchien@gmail.com'),
 ('company_tax_id','62228678')
ON CONFLICT(key) DO NOTHING;

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
CREATE TABLE IF NOT EXISTS project_quotes (
 id BIGSERIAL PRIMARY KEY, project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 quote_no TEXT NOT NULL UNIQUE, title TEXT NOT NULL DEFAULT '', client_name TEXT NOT NULL DEFAULT '',
 issuer_name TEXT NOT NULL DEFAULT '', proposal_key TEXT NOT NULL DEFAULT '',
 version_no INTEGER NOT NULL DEFAULT 1 CHECK (version_no > 0),
 parent_quote_id BIGINT REFERENCES project_quotes(id) ON DELETE SET NULL, currency TEXT NOT NULL DEFAULT 'TWD',
 discount_type TEXT NOT NULL DEFAULT 'amount' CHECK (discount_type IN ('amount','percent')),
 discount_value NUMERIC(12,2) NOT NULL DEFAULT 0 CHECK (discount_value >= 0),
 tax_rate NUMERIC(6,3) NOT NULL DEFAULT 5 CHECK (tax_rate >= 0),
 note TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','sent','accepted','rejected')),
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text
);
-- A proposal exists before an accounting project.  It deliberately has no
-- foreign key to projects. An accepted version creates one exactly once.
CREATE TABLE IF NOT EXISTS quotes (
 id BIGSERIAL PRIMARY KEY, quote_no TEXT NOT NULL UNIQUE, title TEXT NOT NULL DEFAULT '',
 client_name TEXT NOT NULL DEFAULT '', issuer_name TEXT NOT NULL DEFAULT '', currency TEXT NOT NULL DEFAULT 'TWD',
 discount_type TEXT NOT NULL DEFAULT 'amount' CHECK (discount_type IN ('amount','percent')),
 discount_value NUMERIC(12,2) NOT NULL DEFAULT 0 CHECK (discount_value >= 0),
 tax_rate NUMERIC(6,3) NOT NULL DEFAULT 5 CHECK (tax_rate >= 0), note TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','sent','accepted','rejected')),
 version_no INTEGER NOT NULL DEFAULT 1, parent_quote_id BIGINT REFERENCES quotes(id) ON DELETE SET NULL,
 project_id BIGINT UNIQUE REFERENCES projects(id) ON DELETE SET NULL,
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text
);
CREATE TABLE IF NOT EXISTS quote_items (
 id BIGSERIAL PRIMARY KEY, quote_id BIGINT NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
 description TEXT NOT NULL, quantity NUMERIC(12,2) NOT NULL DEFAULT 1, unit TEXT NOT NULL DEFAULT '式',
 unit_price_cents BIGINT NOT NULL DEFAULT 0, sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS quote_specifications (
 id BIGSERIAL PRIMARY KEY, quote_id BIGINT NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
 feature TEXT NOT NULL, use_case TEXT NOT NULL DEFAULT '', capability TEXT NOT NULL DEFAULT '', implementation_steps TEXT NOT NULL DEFAULT '', sort_order INTEGER NOT NULL DEFAULT 0
);
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS quote_date TEXT NOT NULL DEFAULT CURRENT_DATE::text;
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS valid_until TEXT;
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS issuer_contact TEXT NOT NULL DEFAULT '';
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS issuer_email TEXT NOT NULL DEFAULT '';
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS issuer_tax_id TEXT NOT NULL DEFAULT '';
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS project_content TEXT NOT NULL DEFAULT '';
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS terms TEXT NOT NULL DEFAULT '';
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS signature_label TEXT NOT NULL DEFAULT '簽核';
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS quote_language TEXT NOT NULL DEFAULT 'zh-TW';
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS quote_type TEXT NOT NULL DEFAULT 'company';
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS show_unit_price INTEGER NOT NULL DEFAULT 0;
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS personal_name TEXT NOT NULL DEFAULT '';
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS personal_contact TEXT NOT NULL DEFAULT '';
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS accepted_choice_label TEXT NOT NULL DEFAULT '';
ALTER TABLE quote_items ADD COLUMN IF NOT EXISTS detail TEXT NOT NULL DEFAULT '';
ALTER TABLE quote_items ADD COLUMN IF NOT EXISTS is_choice INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_quote_specifications_quote ON quote_specifications(quote_id);
ALTER TABLE project_quotes ADD COLUMN IF NOT EXISTS proposal_key TEXT NOT NULL DEFAULT '';
ALTER TABLE project_quotes ADD COLUMN IF NOT EXISTS version_no INTEGER NOT NULL DEFAULT 1;
ALTER TABLE project_quotes ADD COLUMN IF NOT EXISTS parent_quote_id BIGINT REFERENCES project_quotes(id) ON DELETE SET NULL;
UPDATE project_quotes SET proposal_key=quote_no WHERE proposal_key='';
CREATE UNIQUE INDEX IF NOT EXISTS idx_quotes_proposal_version ON project_quotes(proposal_key,version_no);
CREATE TABLE IF NOT EXISTS project_quote_items (
 id BIGSERIAL PRIMARY KEY, quote_id BIGINT NOT NULL REFERENCES project_quotes(id) ON DELETE CASCADE,
 description TEXT NOT NULL, quantity NUMERIC(12,2) NOT NULL DEFAULT 1 CHECK (quantity > 0),
 unit TEXT NOT NULL DEFAULT '式', unit_price_cents BIGINT NOT NULL DEFAULT 0 CHECK (unit_price_cents >= 0),
 sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS project_roles (
 id BIGSERIAL PRIMARY KEY, project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 name TEXT NOT NULL, hourly_rate_cents BIGINT NOT NULL DEFAULT 0 CHECK (hourly_rate_cents >= 0),
 flat_fee_cents BIGINT NOT NULL DEFAULT 0 CHECK (flat_fee_cents >= 0),
 is_self INTEGER NOT NULL DEFAULT 0 CHECK (is_self IN (0,1)), UNIQUE(project_id,name)
);
CREATE TABLE IF NOT EXISTS project_time_entries (
 id BIGSERIAL PRIMARY KEY, project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 role_id BIGINT NOT NULL REFERENCES project_roles(id) ON DELETE CASCADE, work_date TEXT NOT NULL,
 description TEXT NOT NULL DEFAULT '', estimated_minutes INTEGER NOT NULL DEFAULT 0 CHECK (estimated_minutes >= 0),
 actual_minutes INTEGER NOT NULL DEFAULT 0 CHECK (actual_minutes >= 0),
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP::text
);
CREATE TABLE IF NOT EXISTS project_receivables (
 id BIGSERIAL PRIMARY KEY, project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 name TEXT NOT NULL, amount_cents BIGINT NOT NULL DEFAULT 0 CHECK (amount_cents >= 0),
 expected_date TEXT, received INTEGER NOT NULL DEFAULT 0 CHECK (received IN (0,1)),
 received_date TEXT, note TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS project_cost_items (
 id BIGSERIAL PRIMARY KEY, project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 name TEXT NOT NULL, amount_cents BIGINT NOT NULL DEFAULT 0 CHECK (amount_cents >= 0),
 currency TEXT NOT NULL DEFAULT 'TWD', exchange_rate NUMERIC(14,6) NOT NULL DEFAULT 1 CHECK (exchange_rate > 0),
 is_labor INTEGER NOT NULL DEFAULT 0 CHECK (is_labor IN (0,1)),
 paid INTEGER NOT NULL DEFAULT 0 CHECK (paid IN (0,1)), paid_date TEXT, note TEXT NOT NULL DEFAULT ''
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
CREATE INDEX IF NOT EXISTS idx_quotes_project ON project_quotes(project_id);
CREATE INDEX IF NOT EXISTS idx_quote_items_quote ON project_quote_items(quote_id);
CREATE INDEX IF NOT EXISTS idx_project_roles_project ON project_roles(project_id);
CREATE INDEX IF NOT EXISTS idx_time_entries_project_date ON project_time_entries(project_id,work_date);
CREATE INDEX IF NOT EXISTS idx_receivables_project ON project_receivables(project_id);
CREATE INDEX IF NOT EXISTS idx_cost_items_project ON project_cost_items(project_id);
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
CREATE TABLE IF NOT EXISTS quote_attachments (
 id BIGSERIAL PRIMARY KEY, quote_id BIGINT NOT NULL REFERENCES quotes(id) ON DELETE CASCADE, storage_key TEXT NOT NULL UNIQUE,
 original_filename TEXT NOT NULL, content_type TEXT NOT NULL DEFAULT 'application/pdf',
 size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
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
CREATE INDEX IF NOT EXISTS idx_quote_attachments_quote ON quote_attachments(quote_id);
CREATE INDEX IF NOT EXISTS idx_mcp_tokens_user ON mcp_access_tokens(user_id);
