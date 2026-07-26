package models

import "database/sql"

type ProjectBudget struct {
	ID, ProjectID, TotalAmountCents int64
	Note                            string
}
type Milestone struct {
	ID, ProjectID, PlannedIncomeCents int64
	Name, Note                        string
	SortOrder                         int
}
type BudgetAllocation struct {
	ID, MilestoneID, CounterpartyID, PlannedAmountCents int64
	RecipientKind, RecipientName                        string
	CounterpartyValid                                   bool
}
type BudgetPosting struct {
	ID, TransactionID, MilestoneID, AllocationID, AmountCents int64
	Kind, Note                                                string
	MilestoneValid, AllocationValid                           bool
}

// ProjectBudgetActuals is deliberately calculated from journal postings.  It
// never stores a second, manually maintained "actual" balance.
type ProjectBudgetActuals struct {
	IncomeByMilestone    map[int64]int64
	ReserveByMilestone   map[int64]int64
	PaidByAllocation     map[int64]int64
	GlobalCompanyReserve int64
	CompanySharedCost    int64
}

func GetProjectBudget(d *sql.DB, projectID int64) (*ProjectBudget, error) {
	b := &ProjectBudget{}
	err := d.QueryRow(`SELECT id,project_id,total_amount_cents,note FROM project_budgets WHERE project_id=?`, projectID).Scan(&b.ID, &b.ProjectID, &b.TotalAmountCents, &b.Note)
	if err == sql.ErrNoRows {
		return &ProjectBudget{ProjectID: projectID}, nil
	}
	return b, err
}
func SaveProjectBudget(d *sql.DB, b *ProjectBudget) error {
	_, err := d.Exec(`INSERT INTO project_budgets(project_id,total_amount_cents,note) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET total_amount_cents=excluded.total_amount_cents,note=excluded.note,updated_at=CURRENT_TIMESTAMP::text`, b.ProjectID, b.TotalAmountCents, b.Note)
	return err
}
func ListMilestones(d *sql.DB, projectID int64) ([]Milestone, error) {
	rows, err := d.Query(`SELECT id,project_id,name,planned_income_cents,sort_order,note FROM project_milestones WHERE project_id=? ORDER BY sort_order,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Milestone
	for rows.Next() {
		var m Milestone
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.Name, &m.PlannedIncomeCents, &m.SortOrder, &m.Note); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func CreateMilestone(d *sql.DB, m *Milestone) (int64, error) {
	var id int64
	err := d.QueryRow(`INSERT INTO project_milestones(project_id,name,planned_income_cents,sort_order,note) VALUES(?,?,?,?,?) RETURNING id`, m.ProjectID, m.Name, m.PlannedIncomeCents, m.SortOrder, m.Note).Scan(&id)
	return id, err
}
func DeleteMilestone(d *sql.DB, id int64) error {
	_, e := d.Exec(`DELETE FROM project_milestones WHERE id=?`, id)
	return e
}
func ListBudgetAllocations(d *sql.DB, milestoneID int64) ([]BudgetAllocation, error) {
	rows, e := d.Query(`SELECT id,milestone_id,recipient_kind,COALESCE(counterparty_id,0),counterparty_id IS NOT NULL,recipient_name,planned_amount_cents FROM project_budget_allocations WHERE milestone_id=? ORDER BY id`, milestoneID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []BudgetAllocation
	for rows.Next() {
		var a BudgetAllocation
		if e := rows.Scan(&a.ID, &a.MilestoneID, &a.RecipientKind, &a.CounterpartyID, &a.CounterpartyValid, &a.RecipientName, &a.PlannedAmountCents); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func CreateBudgetAllocation(d *sql.DB, a *BudgetAllocation) (int64, error) {
	var cp any
	if a.CounterpartyValid {
		cp = a.CounterpartyID
	}
	var id int64
	e := d.QueryRow(`INSERT INTO project_budget_allocations(milestone_id,recipient_kind,counterparty_id,recipient_name,planned_amount_cents) VALUES(?,?,?,?,?) RETURNING id`, a.MilestoneID, a.RecipientKind, cp, a.RecipientName, a.PlannedAmountCents).Scan(&id)
	return id, e
}
func DeleteBudgetAllocation(d *sql.DB, id int64) error {
	_, e := d.Exec(`DELETE FROM project_budget_allocations WHERE id=?`, id)
	return e
}

func ListBudgetPostings(d *sql.DB, transactionID int64) ([]BudgetPosting, error) {
	rows, e := d.Query(`SELECT id,transaction_id,COALESCE(milestone_id,0),milestone_id IS NOT NULL,COALESCE(budget_allocation_id,0),budget_allocation_id IS NOT NULL,allocation_kind,amount_cents,note FROM transaction_budget_allocations WHERE transaction_id=? ORDER BY id`, transactionID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []BudgetPosting
	for rows.Next() {
		var p BudgetPosting
		if e := rows.Scan(&p.ID, &p.TransactionID, &p.MilestoneID, &p.MilestoneValid, &p.AllocationID, &p.AllocationValid, &p.Kind, &p.AmountCents, &p.Note); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func CreateBudgetPosting(d *sql.DB, p *BudgetPosting) (int64, error) {
	var mid, aid any
	if p.MilestoneValid {
		mid = p.MilestoneID
	}
	if p.AllocationValid {
		aid = p.AllocationID
	}
	var id int64
	e := d.QueryRow(`INSERT INTO transaction_budget_allocations(transaction_id,milestone_id,budget_allocation_id,allocation_kind,amount_cents,note) VALUES(?,?,?,?,?,?) RETURNING id`, p.TransactionID, mid, aid, p.Kind, p.AmountCents, p.Note).Scan(&id)
	return id, e
}
func DeleteBudgetPosting(d *sql.DB, id int64) error {
	_, e := d.Exec(`DELETE FROM transaction_budget_allocations WHERE id=?`, id)
	return e
}

// SumBudgetPostingsByKind keeps cash-backed posting types from exceeding the
// underlying journal amount. Company reserve is an internal attribution of an
// income posting, so it is intentionally not added to that cash total.
func SumBudgetPostingsByKind(d *sql.DB, transactionID int64, kind string) (int64, error) {
	var total int64
	err := d.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM transaction_budget_allocations WHERE transaction_id=? AND allocation_kind=?`, transactionID, kind).Scan(&total)
	return total, err
}

// GetProjectBudgetActuals aggregates only the journal allocations belonging
// to this project.  Company shared costs are intentionally global: they are
// paid from the company pool, not charged to an arbitrary project.
func GetProjectBudgetActuals(d *sql.DB, projectID int64) (ProjectBudgetActuals, error) {
	a := ProjectBudgetActuals{
		IncomeByMilestone:  map[int64]int64{},
		ReserveByMilestone: map[int64]int64{},
		PaidByAllocation:   map[int64]int64{},
	}
	rows, err := d.Query(`SELECT p.milestone_id,p.allocation_kind,SUM(p.amount_cents)
		FROM transaction_budget_allocations p
		JOIN project_milestones m ON m.id=p.milestone_id
		WHERE m.project_id=?
		GROUP BY p.milestone_id,p.allocation_kind`, projectID)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var milestoneID, amount int64
		var kind string
		if err := rows.Scan(&milestoneID, &kind, &amount); err != nil {
			return a, err
		}
		switch kind {
		case "income":
			a.IncomeByMilestone[milestoneID] += amount
		case "company_reserve":
			a.ReserveByMilestone[milestoneID] += amount
		}
	}
	if err := rows.Err(); err != nil {
		return a, err
	}
	rows, err = d.Query(`SELECT p.budget_allocation_id,SUM(p.amount_cents)
		FROM transaction_budget_allocations p
		JOIN project_budget_allocations a ON a.id=p.budget_allocation_id
		JOIN project_milestones m ON m.id=a.milestone_id
		WHERE m.project_id=? AND p.allocation_kind='partner_payout'
		GROUP BY p.budget_allocation_id`, projectID)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var allocationID, amount int64
		if err := rows.Scan(&allocationID, &amount); err != nil {
			return a, err
		}
		a.PaidByAllocation[allocationID] = amount
	}
	if err := rows.Err(); err != nil {
		return a, err
	}
	if err = d.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM transaction_budget_allocations WHERE allocation_kind='company_reserve'`).Scan(&a.GlobalCompanyReserve); err != nil {
		return a, err
	}
	err = d.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM transaction_budget_allocations WHERE allocation_kind='company_shared_cost' AND milestone_id IS NULL`).Scan(&a.CompanySharedCost)
	return a, err
}

// BudgetAllocationBelongsToMilestone prevents a journal posting from being
// accidentally mapped to a recipient in a different project/milestone.
func BudgetAllocationBelongsToMilestone(d *sql.DB, allocationID, milestoneID int64) (bool, error) {
	var found bool
	err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_budget_allocations WHERE id=? AND milestone_id=?)`, allocationID, milestoneID).Scan(&found)
	return found, err
}
