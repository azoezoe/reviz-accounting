package models

import (
	"database/sql"
	"fmt"
	"time"
)

type Account struct {
	ID        int64
	Name      string
	Kind      string // "asset" | "liability"
	Active    bool
	SortOrder int
}

type Category struct {
	ID        int64
	Name      string
	Group     string // "income" | "cost" | "expense" | "equity" | "other"
	SortOrder int
}

type Project struct {
	ID        int64
	Name      string
	StartDate sql.NullString
	EndDate   sql.NullString
	Note      string
}

// Counterparty is a customer, supplier, or person involved in a transaction.
// Only its name is required while recording a transaction; the other details
// can be completed later from the management page.
type Counterparty struct {
	ID              int64
	Name            string
	TaxID           string
	ContactName     string
	Phone           string
	Address         string
	Email           string
	BankName        string
	BankAccountName string
	BankAccountNo   string
}

// Transaction always stores a positive amount; direction is implicit in the
// presence of FromAccountID / ToAccountID:
//
//	income    -> ToAccountID set, FromAccountID nil
//	expense   -> FromAccountID set, ToAccountID nil
//	transfer  -> both set
type Transaction struct {
	ID             int64
	Code           string
	Date           string // YYYY-MM-DD
	Description    string
	CounterpartyID sql.NullInt64
	CategoryID     sql.NullInt64
	AmountCents    int64
	FromAccountID  sql.NullInt64
	ToAccountID    sql.NullInt64
	ProjectID      sql.NullInt64
	Note           string

	// Joined fields populated by list queries
	CategoryName     string
	FromAccountName  string
	ToAccountName    string
	ProjectName      string
	CounterpartyName string
}

type Attachment struct {
	ID               int64
	TransactionID    int64
	StorageKey       string
	OriginalFilename string
	ContentType      string
	SizeBytes        int64
	UploadedByID     sql.NullInt64
	CreatedAt        string
	UploadedByName   string
}

// Type reports the inferred kind of transaction.
func (t *Transaction) Type() string {
	switch {
	case t.FromAccountID.Valid && t.ToAccountID.Valid:
		return "transfer"
	case t.ToAccountID.Valid:
		return "income"
	default:
		return "expense"
	}
}

// SignedAmountCents returns the cents value signed from the company's
// perspective: positive for income, negative for expense, 0 for transfer.
func (t *Transaction) SignedAmountCents() int64 {
	switch t.Type() {
	case "income":
		return t.AmountCents
	case "expense":
		return -t.AmountCents
	default:
		return 0
	}
}

// GenerateCode returns the next T-prefixed sequential transaction code.
func GenerateCode(d *sql.DB) (string, error) {
	var max sql.NullInt64
	err := d.QueryRow(`SELECT MAX(id) FROM transactions`).Scan(&max)
	if err != nil {
		return "", err
	}
	n := int64(1)
	if max.Valid {
		n = max.Int64 + 1
	}
	return fmt.Sprintf("T%04d", n), nil
}

// ParseDate accepts YYYY-MM-DD and returns time.Time in UTC.
func ParseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}
