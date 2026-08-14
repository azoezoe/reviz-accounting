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
	ID, ProjectID, MilestoneID, CounterpartyID, PlannedAmountCents int64
	RecipientKind, RecipientName, ProjectName                      string
	CounterpartyValid                                              bool
}

type ProjectBudgetReport struct {
	IncomeCents      int64
	PaidByAllocation map[int64]int64
}
type BudgetPosting struct {
	ID, TransactionID, MilestoneID, AllocationID, AmountCents int64
	Kind, Note, ProjectName, AllocationName                   string
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

func MilestoneBelongsToProject(d *sql.DB, milestoneID, projectID int64) (bool, error) {
	var found bool
	err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_milestones WHERE id=? AND project_id=?)`, milestoneID, projectID).Scan(&found)
	return found, err
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
	if a.ProjectID > 0 {
		e := d.QueryRow(`INSERT INTO project_budget_allocations(project_id,recipient_kind,counterparty_id,recipient_name,planned_amount_cents) VALUES(?,?,?,?,?) RETURNING id`, a.ProjectID, a.RecipientKind, cp, a.RecipientName, a.PlannedAmountCents).Scan(&id)
		return id, e
	}
	e := d.QueryRow(`INSERT INTO project_budget_allocations(milestone_id,recipient_kind,counterparty_id,recipient_name,planned_amount_cents) VALUES(?,?,?,?,?) RETURNING id`, a.MilestoneID, a.RecipientKind, cp, a.RecipientName, a.PlannedAmountCents).Scan(&id)
	return id, e
}

func ListProjectBudgetAllocations(d *sql.DB, projectID int64) ([]BudgetAllocation, error) {
	rows, err := d.Query(`SELECT id,project_id,recipient_kind,COALESCE(counterparty_id,0),counterparty_id IS NOT NULL,recipient_name,planned_amount_cents FROM project_budget_allocations WHERE project_id=? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BudgetAllocation
	for rows.Next() {
		var a BudgetAllocation
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.RecipientKind, &a.CounterpartyID, &a.CounterpartyValid, &a.RecipientName, &a.PlannedAmountCents); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAllProjectBudgetAllocations supplies the journal split form. Each
// allocation carries its owning project, so one payment can be shared safely
// across multiple projects without duplicating a transaction.
func ListAllProjectBudgetAllocations(d *sql.DB) ([]BudgetAllocation, error) {
	rows, err := d.Query(`SELECT a.id,a.project_id,p.name,a.recipient_kind,COALESCE(a.counterparty_id,0),a.counterparty_id IS NOT NULL,a.recipient_name,a.planned_amount_cents FROM project_budget_allocations a JOIN projects p ON p.id=a.project_id ORDER BY p.name,a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BudgetAllocation
	for rows.Next() {
		var a BudgetAllocation
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.ProjectName, &a.RecipientKind, &a.CounterpartyID, &a.CounterpartyValid, &a.RecipientName, &a.PlannedAmountCents); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func BudgetAllocationBelongsToProject(d *sql.DB, allocationID, projectID int64) (bool, error) {
	var ok bool
	err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_budget_allocations WHERE id=? AND project_id=?)`, allocationID, projectID).Scan(&ok)
	return ok, err
}

func GetProjectBudgetReport(d *sql.DB, projectID int64) (ProjectBudgetReport, error) {
	r := ProjectBudgetReport{PaidByAllocation: map[int64]int64{}}
	if err := d.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM transactions WHERE project_id=? AND to_account_id IS NOT NULL AND from_account_id IS NULL`, projectID).Scan(&r.IncomeCents); err != nil {
		return r, err
	}
	rows, err := d.Query(`SELECT p.budget_allocation_id,SUM(p.amount_cents) FROM transaction_budget_allocations p JOIN project_budget_allocations a ON a.id=p.budget_allocation_id WHERE a.project_id=? AND p.allocation_kind IN ('partner_payout','cost_expense') GROUP BY p.budget_allocation_id`, projectID)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, amount int64
		if err := rows.Scan(&id, &amount); err != nil {
			return r, err
		}
		r.PaidByAllocation[id] = amount
	}
	return r, rows.Err()
}
func DeleteBudgetAllocation(d *sql.DB, id int64) error {
	_, e := d.Exec(`DELETE FROM project_budget_allocations WHERE id=?`, id)
	return e
}

func ListBudgetPostings(d *sql.DB, transactionID int64) ([]BudgetPosting, error) {
	rows, e := d.Query(`SELECT p.id,p.transaction_id,COALESCE(p.milestone_id,0),p.milestone_id IS NOT NULL,COALESCE(p.budget_allocation_id,0),p.budget_allocation_id IS NOT NULL,p.allocation_kind,p.amount_cents,p.note,COALESCE(pr.name,''),COALESCE(a.recipient_name,'') FROM transaction_budget_allocations p LEFT JOIN project_budget_allocations a ON a.id=p.budget_allocation_id LEFT JOIN projects pr ON pr.id=a.project_id WHERE p.transaction_id=? ORDER BY p.id`, transactionID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []BudgetPosting
	for rows.Next() {
		var p BudgetPosting
		if e := rows.Scan(&p.ID, &p.TransactionID, &p.MilestoneID, &p.MilestoneValid, &p.AllocationID, &p.AllocationValid, &p.Kind, &p.AmountCents, &p.Note, &p.ProjectName, &p.AllocationName); e != nil {
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

// SumCashBudgetPostings keeps all cash-backed splits of one payment within
// its journal amount. Company reserve is an internal income attribution and
// is therefore excluded.
func SumCashBudgetPostings(d *sql.DB, transactionID int64) (int64, error) {
	var total int64
	err := d.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM transaction_budget_allocations WHERE transaction_id=? AND allocation_kind <> 'company_reserve'`, transactionID).Scan(&total)
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
		WHERE m.project_id=? AND p.allocation_kind IN ('partner_payout','cost_expense')
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

func BudgetAllocationKind(d *sql.DB, allocationID int64) (string, error) {
	var kind string
	err := d.QueryRow(`SELECT recipient_kind FROM project_budget_allocations WHERE id=?`, allocationID).Scan(&kind)
	return kind, err
}

// BudgetPostingCountsForProject identifies journal entries linked to a project
// that still need a budget/milestone allocation.
func BudgetPostingCountsForProject(d *sql.DB, projectID int64) (map[int64]int, error) {
	rows, err := d.Query(`SELECT t.id,COUNT(p.id)
		FROM transactions t
		LEFT JOIN transaction_budget_allocations p ON p.transaction_id=t.id
		WHERE t.project_id=? OR EXISTS (SELECT 1 FROM transaction_budget_allocations p2 JOIN project_budget_allocations a ON a.id=p2.budget_allocation_id WHERE p2.transaction_id=t.id AND a.project_id=?)
		GROUP BY t.id`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		out[id] = count
	}
	return out, rows.Err()
}
