package models

import (
	"database/sql"
	"fmt"
	"sort"
)

// PnLRow represents a single category line in the P&L statement.
type PnLRow struct {
	CategoryID   int64
	CategoryName string
	Group        string
	Monthly      [12]int64 // cents, by month index 0..11
	YTD          int64
}

// PnL holds the full P&L for a given year and optional project filter.
type PnL struct {
	Year    int
	Project int64 // 0 for all

	Income       []PnLRow
	Cost         []PnLRow
	Expense      []PnLRow
	IncomeTotal  [13]int64 // 12 months + YTD
	CostTotal    [13]int64
	ExpenseTotal [13]int64
	GrossProfit  [13]int64 // Income - Cost
	NetProfit    [13]int64 // Gross - Expense
}

// ComputePnL builds a P&L statement for the given year. Set project=0 for all
// projects. The calculation treats:
//   - income categories: SUM(amount_cents WHERE to_account_id IS NOT NULL)
//   - cost / expense categories: SUM(amount_cents WHERE from_account_id IS NOT NULL)
//
// Transfer rows (where both accounts are set) are ignored unless their
// category is income/cost/expense (which would be unusual configuration).
func ComputePnL(d *sql.DB, year int, project int64) (*PnL, error) {
	p := &PnL{Year: year, Project: project}

	cats, err := ListCategories(d)
	if err != nil {
		return nil, err
	}

	rowsByGroup := map[string]*[]PnLRow{
		"income":  &p.Income,
		"cost":    &p.Cost,
		"expense": &p.Expense,
	}

	yearPrefix := fmt.Sprintf("%04d", year)
	for _, c := range cats {
		bucket, ok := rowsByGroup[c.Group]
		if !ok {
			continue
		}
		row := PnLRow{CategoryID: c.ID, CategoryName: c.Name, Group: c.Group}

		// Sum signed amounts by month for this category.
		var sign string
		switch c.Group {
		case "income":
			// money flowing in: to_account_id set
			sign = "to_account_id IS NOT NULL"
		default:
			// cost / expense: money flowing out
			sign = "from_account_id IS NOT NULL"
		}
		q := `
            SELECT substr(tx_date,6,2) AS m, COALESCE(SUM(amount_cents),0)
            FROM transactions
            WHERE substr(tx_date,1,4)=?
              AND category_id=?
              AND ` + sign
		args := []any{yearPrefix, c.ID}
		if project > 0 {
			q += ` AND project_id=?`
			args = append(args, project)
		}
		q += ` GROUP BY m`

		rows, err := d.Query(q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var mStr string
			var amt int64
			if err := rows.Scan(&mStr, &amt); err != nil {
				rows.Close()
				return nil, err
			}
			var m int
			fmt.Sscanf(mStr, "%d", &m)
			if m >= 1 && m <= 12 {
				row.Monthly[m-1] = amt
				row.YTD += amt
			}
		}
		rows.Close()

		*bucket = append(*bucket, row)
	}

	addTotals := func(rows []PnLRow, totals *[13]int64) {
		for _, r := range rows {
			for i, v := range r.Monthly {
				totals[i] += v
			}
			totals[12] += r.YTD
		}
	}
	addTotals(p.Income, &p.IncomeTotal)
	addTotals(p.Cost, &p.CostTotal)
	addTotals(p.Expense, &p.ExpenseTotal)
	// Put categories with activity first. The seeded chart of accounts contains
	// useful zero-balance placeholders; displaying those before actual costs
	// makes a report look empty even when its totals are correct.
	for _, group := range []*[]PnLRow{&p.Income, &p.Cost, &p.Expense} {
		sort.SliceStable(*group, func(i, j int) bool {
			a, b := (*group)[i], (*group)[j]
			if (a.YTD != 0) != (b.YTD != 0) {
				return a.YTD != 0
			}
			return a.CategoryName < b.CategoryName
		})
	}
	for i := 0; i < 13; i++ {
		p.GrossProfit[i] = p.IncomeTotal[i] - p.CostTotal[i]
		p.NetProfit[i] = p.GrossProfit[i] - p.ExpenseTotal[i]
	}
	return p, nil
}

// AvailableYears returns distinct years that have at least one transaction.
func AvailableYears(d *sql.DB) ([]int, error) {
	rows, err := d.Query(
		`SELECT DISTINCT substr(tx_date,1,4) AS y FROM transactions ORDER BY y DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		var y int
		fmt.Sscanf(s, "%d", &y)
		out = append(out, y)
	}
	return out, rows.Err()
}
