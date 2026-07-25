package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed schema.sql
var schemaSQL string

// Open connects to PostgreSQL (including Cloud SQL through its Unix socket)
// and applies the idempotent schema. DATABASE_URL is the only database
// configuration accepted by the application.
func Open(dsn string) (*sql.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	d, err := sql.Open("reviz-pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, err
	}
	for _, statement := range strings.Split(schemaSQL, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := d.Exec(statement); err != nil {
			d.Close()
			return nil, fmt.Errorf("apply schema: %w", err)
		}
	}
	return d, nil
}

func SeedIfEmpty(d *sql.DB) error {
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range map[string]string{"company_name": "我的公司", "fiscal_year": "2026"} {
		if _, err := tx.Exec(`INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
			return err
		}
	}
	type catRow struct{ name, group string }
	cats := []catRow{{"營業收入 / 銷貨收入 - 軟體開發", "income"}, {"營業收入 / 銷貨收入 - 維護費", "income"}, {"營業收入 / 銷貨收入 - 諮詢會議", "income"}, {"勞務收入 / 設計收入 / 技術服務收入", "income"}, {"其他營業收入 - 其他勞務收入", "income"}, {"非營業收入 - 投資收入", "income"}, {"非營業收入 - 利息收入", "income"}, {"銷貨成本 - 商品種類 1", "cost"}, {"銷貨成本 - 商品種類 2", "cost"}, {"銷貨成本 - 商品種類 3", "cost"}, {"勞務成本 / 設計成本 / 技術服務成本", "cost"}, {"其他勞務成本", "cost"}, {"推銷費用 / 廣告費用", "expense"}, {"薪資費用", "expense"}, {"租金費用", "expense"}, {"差旅費用", "expense"}, {"郵寄費用", "expense"}, {"修繕費用", "expense"}, {"文具用品 / 辦公用品費用", "expense"}, {"水電瓦斯費", "expense"}, {"保險費用", "expense"}, {"交際費用", "expense"}, {"稅費", "expense"}, {"伙食費", "expense"}, {"員工福利費用", "expense"}, {"佣金支出", "expense"}, {"呆帳損失", "expense"}, {"利息費用", "expense"}, {"投資損失", "expense"}, {"財務費用 / 銀行費用", "expense"}, {"其他費用", "expense"}, {"雲端服務費用", "expense"}, {"軟體使用費用", "expense"}, {"實收資本", "equity"}, {"轉帳沖銷", "other"}, {"前期結轉", "other"}}
	for i, c := range cats {
		if _, err := tx.Exec(`INSERT INTO categories(name,group_name,sort_order) VALUES($1,$2,$3)`, c.name, c.group, i); err != nil {
			return err
		}
	}
	for i, a := range []struct{ name, kind string }{{"銀行帳戶", "asset"}, {"零用金", "asset"}, {"信用卡", "liability"}} {
		if _, err := tx.Exec(`INSERT INTO accounts(name,kind,sort_order) VALUES($1,$2,$3)`, a.name, a.kind, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}
