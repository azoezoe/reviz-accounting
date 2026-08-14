package models

import (
	"database/sql"
	"fmt"
	"strings"
)

// ----- Settings -----

func GetSetting(d *sql.DB, key string) (string, error) {
	var v string
	err := d.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func SetSetting(d *sql.DB, key, value string) error {
	_, err := d.Exec(
		`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	return err
}

func AllSettings(d *sql.DB) (map[string]string, error) {
	rows, err := d.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// ----- Accounts -----

func ListAccounts(d *sql.DB, activeOnly bool) ([]Account, error) {
	q := `SELECT id, name, kind, active, sort_order FROM accounts`
	if activeOnly {
		q += ` WHERE active=1`
	}
	q += ` ORDER BY kind DESC, sort_order, id`
	rows, err := d.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		var active int
		if err := rows.Scan(&a.ID, &a.Name, &a.Kind, &active, &a.SortOrder); err != nil {
			return nil, err
		}
		a.Active = active == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

func GetAccount(d *sql.DB, id int64) (*Account, error) {
	var a Account
	var active int
	err := d.QueryRow(
		`SELECT id, name, kind, active, sort_order FROM accounts WHERE id=?`, id,
	).Scan(&a.ID, &a.Name, &a.Kind, &active, &a.SortOrder)
	if err != nil {
		return nil, err
	}
	a.Active = active == 1
	return &a, nil
}

func CreateAccount(d *sql.DB, a *Account) (int64, error) {
	var id int64
	err := d.QueryRow(`INSERT INTO accounts(name, kind, active, sort_order) VALUES(?,?,?,?) RETURNING id`, a.Name, a.Kind, boolInt(a.Active), a.SortOrder).Scan(&id)
	return id, err
}

func UpdateAccount(d *sql.DB, a *Account) error {
	_, err := d.Exec(
		`UPDATE accounts SET name=?, kind=?, active=?, sort_order=? WHERE id=?`,
		a.Name, a.Kind, boolInt(a.Active), a.SortOrder, a.ID,
	)
	return err
}

func DeleteAccount(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM accounts WHERE id=?`, id)
	return err
}

// AccountBalance computes the current balance (in cents) of the given account
// as: sum(amount when to_account=id) - sum(amount when from_account=id).
func AccountBalance(d *sql.DB, id int64) (int64, error) {
	var in, out sql.NullInt64
	if err := d.QueryRow(
		`SELECT COALESCE(SUM(amount_cents),0) FROM transactions WHERE to_account_id=?`, id,
	).Scan(&in); err != nil {
		return 0, err
	}
	if err := d.QueryRow(
		`SELECT COALESCE(SUM(amount_cents),0) FROM transactions WHERE from_account_id=?`, id,
	).Scan(&out); err != nil {
		return 0, err
	}
	return in.Int64 - out.Int64, nil
}

// AccountBalances returns a map[accountID]balance for all accounts.
func AccountBalances(d *sql.DB) (map[int64]int64, error) {
	out := map[int64]int64{}
	rows, err := d.Query(`SELECT id FROM accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		b, err := AccountBalance(d, id)
		if err != nil {
			return nil, err
		}
		out[id] = b
	}
	return out, nil
}

// ----- Categories -----

func ListCategories(d *sql.DB) ([]Category, error) {
	rows, err := d.Query(
		`SELECT id, name, group_name, sort_order FROM categories ORDER BY group_name, sort_order, id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Group, &c.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func GetCategory(d *sql.DB, id int64) (*Category, error) {
	var c Category
	err := d.QueryRow(
		`SELECT id, name, group_name, sort_order FROM categories WHERE id=?`, id,
	).Scan(&c.ID, &c.Name, &c.Group, &c.SortOrder)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func CreateCategory(d *sql.DB, c *Category) (int64, error) {
	var id int64
	err := d.QueryRow(`INSERT INTO categories(name, group_name, sort_order) VALUES(?,?,?) RETURNING id`, c.Name, c.Group, c.SortOrder).Scan(&id)
	return id, err
}

func UpdateCategory(d *sql.DB, c *Category) error {
	_, err := d.Exec(
		`UPDATE categories SET name=?, group_name=?, sort_order=? WHERE id=?`,
		c.Name, c.Group, c.SortOrder, c.ID,
	)
	return err
}

func DeleteCategory(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM categories WHERE id=?`, id)
	return err
}

// ----- Projects -----

func ListProjects(d *sql.DB) ([]Project, error) {
	rows, err := d.Query(
		`SELECT id, name, start_date, end_date, note FROM projects ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.StartDate, &p.EndDate, &p.Note); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func GetProject(d *sql.DB, id int64) (*Project, error) {
	var p Project
	err := d.QueryRow(
		`SELECT id, name, start_date, end_date, note FROM projects WHERE id=?`, id,
	).Scan(&p.ID, &p.Name, &p.StartDate, &p.EndDate, &p.Note)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func CreateProject(d *sql.DB, p *Project) (int64, error) {
	var id int64
	err := d.QueryRow(`INSERT INTO projects(name, start_date, end_date, note) VALUES(?,?,?,?) RETURNING id`, p.Name, nullableDate(p.StartDate), nullableDate(p.EndDate), p.Note).Scan(&id)
	return id, err
}

func UpdateProject(d *sql.DB, p *Project) error {
	_, err := d.Exec(
		`UPDATE projects SET name=?, start_date=?, end_date=?, note=? WHERE id=?`,
		p.Name, nullableDate(p.StartDate), nullableDate(p.EndDate), p.Note, p.ID,
	)
	return err
}

func DeleteProject(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM projects WHERE id=?`, id)
	return err
}

// ----- Counterparties -----

func ListCounterparties(d *sql.DB, search string) ([]Counterparty, error) {
	q := `SELECT id, name, tax_id, contact_name, phone, address, email, bank_name, bank_account_name, bank_account_no FROM counterparties`
	args := []any{}
	if search != "" {
		q += ` WHERE name LIKE ? OR tax_id LIKE ? OR contact_name LIKE ?`
		like := "%" + search + "%"
		args = append(args, like, like, like)
	}
	// PostgreSQL does not provide SQLite's NOCASE collation. Sorting the
	// lowercase value keeps the counterparties list case-insensitive.
	q += ` ORDER BY lower(name), id`
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Counterparty
	for rows.Next() {
		var c Counterparty
		if err := rows.Scan(&c.ID, &c.Name, &c.TaxID, &c.ContactName, &c.Phone, &c.Address, &c.Email, &c.BankName, &c.BankAccountName, &c.BankAccountNo); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func CreateCounterparty(d *sql.DB, c *Counterparty) (int64, error) {
	var id int64
	err := d.QueryRow(`INSERT INTO counterparties(name,tax_id,contact_name,phone,address,email,bank_name,bank_account_name,bank_account_no) VALUES(?,?,?,?,?,?,?,?,?) RETURNING id`, c.Name, c.TaxID, c.ContactName, c.Phone, c.Address, c.Email, c.BankName, c.BankAccountName, c.BankAccountNo).Scan(&id)
	return id, err
}

func UpdateCounterparty(d *sql.DB, c *Counterparty) error {
	_, err := d.Exec(`UPDATE counterparties SET name=?,tax_id=?,contact_name=?,phone=?,address=?,email=?,bank_name=?,bank_account_name=?,bank_account_no=?,updated_at=CURRENT_TIMESTAMP::text WHERE id=?`, c.Name, c.TaxID, c.ContactName, c.Phone, c.Address, c.Email, c.BankName, c.BankAccountName, c.BankAccountNo, c.ID)
	return err
}

func GetOrCreateCounterparty(d *sql.DB, name string) (sql.NullInt64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return sql.NullInt64{}, nil
	}
	var id int64
	err := d.QueryRow(`SELECT id FROM counterparties WHERE name=?`, name).Scan(&id)
	if err == nil {
		return sql.NullInt64{Int64: id, Valid: true}, nil
	}
	if err != sql.ErrNoRows {
		return sql.NullInt64{}, err
	}
	id, err = CreateCounterparty(d, &Counterparty{Name: name})
	if err != nil {
		return sql.NullInt64{}, err
	}
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

func DeleteCounterparty(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM counterparties WHERE id=?`, id)
	return err
}

// ----- Transactions -----

type TxFilter struct {
	YearMonth                 string // "" or "YYYY-MM"
	Year                      string // "" or "YYYY"
	CategoryID                int64
	ProjectID                 int64
	AccountID                 int64
	BudgetAllocationID        int64
	UnallocatedProjectExpense bool
	ProjectIncomeOnly         bool
	SearchText                string
	Limit                     int
	Offset                    int
	// ProjectUserID limits results to transactions related to projects that
	// have been granted to this user (directly or through a budget allocation).
	ProjectUserID int64
}

func ListTransactions(d *sql.DB, f TxFilter) ([]Transaction, int, error) {
	var (
		where []string
		args  []any
	)
	if f.YearMonth != "" {
		where = append(where, `substr(t.tx_date,1,7)=?`)
		args = append(args, f.YearMonth)
	} else if f.Year != "" {
		where = append(where, `substr(t.tx_date,1,4)=?`)
		args = append(args, f.Year)
	}
	if f.CategoryID > 0 {
		where = append(where, `t.category_id=?`)
		args = append(args, f.CategoryID)
	}
	if f.ProjectID > 0 {
		where = append(where, `(t.project_id=? OR EXISTS (SELECT 1 FROM transaction_budget_allocations bpa JOIN project_budget_allocations pba ON pba.id=bpa.budget_allocation_id WHERE bpa.transaction_id=t.id AND pba.project_id=?))`)
		args = append(args, f.ProjectID, f.ProjectID)
	}
	if f.ProjectUserID > 0 {
		where = append(where, `(EXISTS (SELECT 1 FROM project_permissions pp WHERE pp.project_id=t.project_id AND pp.user_id=? ) OR EXISTS (SELECT 1 FROM transaction_budget_allocations bpa JOIN project_budget_allocations pba ON pba.id=bpa.budget_allocation_id JOIN project_permissions pp ON pp.project_id=pba.project_id WHERE bpa.transaction_id=t.id AND pp.user_id=?))`)
		args = append(args, f.ProjectUserID, f.ProjectUserID)
	}
	if f.BudgetAllocationID > 0 {
		where = append(where, `EXISTS (SELECT 1 FROM transaction_budget_allocations bpa WHERE bpa.transaction_id=t.id AND bpa.budget_allocation_id=?)`)
		args = append(args, f.BudgetAllocationID)
	}
	if f.UnallocatedProjectExpense {
		where = append(where, `t.from_account_id IS NOT NULL AND t.to_account_id IS NULL AND NOT EXISTS (SELECT 1 FROM transaction_budget_allocations bpa WHERE bpa.transaction_id=t.id)`)
	}
	if f.ProjectIncomeOnly {
		where = append(where, `t.to_account_id IS NOT NULL AND t.from_account_id IS NULL`)
	}
	if f.AccountID > 0 {
		where = append(where, `(t.from_account_id=? OR t.to_account_id=?)`)
		args = append(args, f.AccountID, f.AccountID)
	}
	if f.SearchText != "" {
		where = append(where, `(t.description LIKE ? OR t.note LIKE ? OR t.code LIKE ? OR cp.name LIKE ?)`)
		like := "%" + f.SearchText + "%"
		args = append(args, like, like, like, like)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	// Count
	var total int
	countQ := `SELECT COUNT(*) FROM transactions t LEFT JOIN counterparties cp ON cp.id=t.counterparty_id` + clause
	if err := d.QueryRow(countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count: %w", err)
	}

	q := `
		SELECT t.id, t.code, t.tx_date, t.description, t.counterparty_id, t.category_id, t.amount_cents,
		       t.from_account_id, t.to_account_id, t.project_id, t.note,
		       COALESCE(c.name,''), COALESCE(fa.name,''), COALESCE(ta.name,''), COALESCE(p.name,''), COALESCE(cp.name,'')
        FROM transactions t
        LEFT JOIN categories c ON c.id=t.category_id
        LEFT JOIN accounts   fa ON fa.id=t.from_account_id
        LEFT JOIN accounts   ta ON ta.id=t.to_account_id
		LEFT JOIN projects   p  ON p.id=t.project_id
		LEFT JOIN counterparties cp ON cp.id=t.counterparty_id
    ` + clause + ` ORDER BY t.tx_date DESC, t.id DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", f.Limit, f.Offset)
	}
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var out []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(
			&t.ID, &t.Code, &t.Date, &t.Description, &t.CounterpartyID, &t.CategoryID, &t.AmountCents,
			&t.FromAccountID, &t.ToAccountID, &t.ProjectID, &t.Note,
			&t.CategoryName, &t.FromAccountName, &t.ToAccountName, &t.ProjectName, &t.CounterpartyName,
		); err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

func CanAccessTransaction(d *sql.DB, transactionID, userID int64, write bool) (bool, error) {
	level := ""
	if write {
		level = ` AND pp.access_level='write'`
	}
	var ok bool
	err := d.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM transactions t LEFT JOIN project_permissions pp ON pp.project_id=t.project_id AND pp.user_id=$2
		WHERE t.id=$1 AND (pp.user_id IS NOT NULL`+level+` OR EXISTS (SELECT 1 FROM transaction_budget_allocations bpa JOIN project_budget_allocations pba ON pba.id=bpa.budget_allocation_id JOIN project_permissions ap ON ap.project_id=pba.project_id AND ap.user_id=$2 WHERE bpa.transaction_id=t.id`+strings.Replace(level, "pp.", "ap.", 1)+`)))`, transactionID, userID).Scan(&ok)
	return ok, err
}

func GetTransaction(d *sql.DB, id int64) (*Transaction, error) {
	var t Transaction
	err := d.QueryRow(`
        SELECT t.id, t.code, t.tx_date, t.description, t.counterparty_id, t.category_id, t.amount_cents,
               t.from_account_id, t.to_account_id, t.project_id, t.note, COALESCE(cp.name,'')
        FROM transactions t LEFT JOIN counterparties cp ON cp.id=t.counterparty_id WHERE t.id=?`, id,
	).Scan(
		&t.ID, &t.Code, &t.Date, &t.Description, &t.CounterpartyID, &t.CategoryID, &t.AmountCents,
		&t.FromAccountID, &t.ToAccountID, &t.ProjectID, &t.Note, &t.CounterpartyName,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func CreateTransaction(d *sql.DB, t *Transaction) (int64, error) {
	var id int64
	err := d.QueryRow(`
        INSERT INTO transactions(
			code, tx_date, description, counterparty_id, category_id, amount_cents,
            from_account_id, to_account_id, project_id, note
		) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING id`,
		t.Code, t.Date, t.Description, t.CounterpartyID, t.CategoryID, t.AmountCents,
		t.FromAccountID, t.ToAccountID, t.ProjectID, t.Note,
	).Scan(&id)
	return id, err
}

func UpdateTransaction(d *sql.DB, t *Transaction) error {
	_, err := d.Exec(`
        UPDATE transactions SET
			tx_date=?, description=?, counterparty_id=?, category_id=?, amount_cents=?,
            from_account_id=?, to_account_id=?, project_id=?, note=?,
			updated_at=CURRENT_TIMESTAMP::text
        WHERE id=?`,
		t.Date, t.Description, t.CounterpartyID, t.CategoryID, t.AmountCents,
		t.FromAccountID, t.ToAccountID, t.ProjectID, t.Note, t.ID,
	)
	return err
}

func DeleteTransaction(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM transactions WHERE id=?`, id)
	return err
}

// ----- Transaction attachments -----

func ListAttachments(d *sql.DB, transactionID int64) ([]Attachment, error) {
	rows, err := d.Query(`SELECT a.id,a.transaction_id,a.storage_key,a.original_filename,a.content_type,a.size_bytes,a.uploaded_by_id,a.created_at,COALESCE(u.username,'') FROM transaction_attachments a LEFT JOIN users u ON u.id=a.uploaded_by_id WHERE a.transaction_id=? ORDER BY a.created_at DESC,a.id DESC`, transactionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.TransactionID, &a.StorageKey, &a.OriginalFilename, &a.ContentType, &a.SizeBytes, &a.UploadedByID, &a.CreatedAt, &a.UploadedByName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func GetAttachment(d *sql.DB, id int64) (*Attachment, error) {
	var a Attachment
	err := d.QueryRow(`SELECT id,transaction_id,storage_key,original_filename,content_type,size_bytes,uploaded_by_id,created_at FROM transaction_attachments WHERE id=?`, id).Scan(&a.ID, &a.TransactionID, &a.StorageKey, &a.OriginalFilename, &a.ContentType, &a.SizeBytes, &a.UploadedByID, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func CreateAttachment(d *sql.DB, a *Attachment) (int64, error) {
	var id int64
	err := d.QueryRow(`INSERT INTO transaction_attachments(transaction_id,storage_key,original_filename,content_type,size_bytes,uploaded_by_id) VALUES(?,?,?,?,?,?) RETURNING id`, a.TransactionID, a.StorageKey, a.OriginalFilename, a.ContentType, a.SizeBytes, a.UploadedByID).Scan(&id)
	return id, err
}

func DeleteAttachment(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM transaction_attachments WHERE id=?`, id)
	return err
}

// ----- Quote PDF attachments -----

func ListQuoteAttachments(d *sql.DB, quoteID int64) ([]QuoteAttachment, error) {
	rows, err := d.Query(`SELECT a.id,a.quote_id,a.storage_key,a.original_filename,a.content_type,a.size_bytes,a.uploaded_by_id,a.created_at,COALESCE(u.username,'') FROM quote_attachments a LEFT JOIN users u ON u.id=a.uploaded_by_id WHERE a.quote_id=? ORDER BY a.id`, quoteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuoteAttachment
	for rows.Next() {
		var a QuoteAttachment
		if err := rows.Scan(&a.ID, &a.QuoteID, &a.StorageKey, &a.OriginalFilename, &a.ContentType, &a.SizeBytes, &a.UploadedByID, &a.CreatedAt, &a.UploadedByName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func GetQuoteAttachment(d *sql.DB, id int64) (*QuoteAttachment, error) {
	var a QuoteAttachment
	err := d.QueryRow(`SELECT id,quote_id,storage_key,original_filename,content_type,size_bytes,uploaded_by_id,created_at FROM quote_attachments WHERE id=?`, id).
		Scan(&a.ID, &a.QuoteID, &a.StorageKey, &a.OriginalFilename, &a.ContentType, &a.SizeBytes, &a.UploadedByID, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func CreateQuoteAttachment(d *sql.DB, a *QuoteAttachment) (int64, error) {
	var id int64
	err := d.QueryRow(`INSERT INTO quote_attachments(quote_id,storage_key,original_filename,content_type,size_bytes,uploaded_by_id) VALUES(?,?,?,?,?,?) RETURNING id`, a.QuoteID, a.StorageKey, a.OriginalFilename, a.ContentType, a.SizeBytes, a.UploadedByID).Scan(&id)
	return id, err
}

func DeleteQuoteAttachment(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM quote_attachments WHERE id=?`, id)
	return err
}

// ----- Helpers -----

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullableDate(s sql.NullString) any {
	if !s.Valid || s.String == "" {
		return nil
	}
	return s.String
}

// NullStringFrom returns a sql.NullString that is invalid for "" and valid otherwise.
func NullStringFrom(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// NullInt64From returns a sql.NullInt64 that is invalid for 0 and valid otherwise.
func NullInt64From(n int64) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: n, Valid: true}
}
