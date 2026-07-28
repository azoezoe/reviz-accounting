package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/hcchien/reviz-accounting/internal/models"
	"github.com/hcchien/reviz-accounting/internal/money"
)

func (s *Server) projectsList(w http.ResponseWriter, r *http.Request) {
	projs, err := models.ListProjects(s.DB)
	if err != nil {
		s.error500(w, err)
		return
	}
	s.render(w, r, "projects.html", map[string]any{
		"Title":    "專案",
		"Crumbs":   []string{"專案"},
		"Projects": projs,
		"Active":   "projects",
	})
}

// projectSummary is a compact, read-only representation used by the journal
// drawer. Keeping it separate avoids rendering the full budget editor inside
// every journal row.
func (s *Server) projectSummary(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	p, err := models.GetProject(s.DB, id)
	if err != nil {
		http.Error(w, "找不到專案", http.StatusNotFound)
		return
	}
	b, err := models.GetProjectBudget(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	ms, err := models.ListMilestones(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	actuals, err := models.GetProjectBudgetActuals(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	transactions, _, err := models.ListTransactions(s.DB, models.TxFilter{ProjectID: id})
	if err != nil {
		s.error500(w, err)
		return
	}
	postingCounts, err := models.BudgetPostingCountsForProject(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	type transactionView struct {
		models.Transaction
		PostingCount int
	}
	txViews := make([]transactionView, 0, len(transactions))
	for _, tx := range transactions {
		txViews = append(txViews, transactionView{Transaction: tx, PostingCount: postingCounts[tx.ID]})
	}
	type allocation struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Planned int64  `json:"planned_cents"`
		Actual  int64  `json:"actual_cents"`
	}
	type milestone struct {
		Name          string       `json:"name"`
		PlannedIncome int64        `json:"planned_income_cents"`
		ActualIncome  int64        `json:"actual_income_cents"`
		ActualReserve int64        `json:"actual_reserve_cents"`
		Allocations   []allocation `json:"allocations"`
	}
	var actualIncome, actualReserve int64
	out := make([]milestone, 0, len(ms))
	for _, m := range ms {
		allocations, e := models.ListBudgetAllocations(s.DB, m.ID)
		if e != nil {
			s.error500(w, e)
			return
		}
		view := milestone{Name: m.Name, PlannedIncome: m.PlannedIncomeCents, ActualIncome: actuals.IncomeByMilestone[m.ID], ActualReserve: actuals.ReserveByMilestone[m.ID]}
		actualIncome += view.ActualIncome
		actualReserve += view.ActualReserve
		for _, a := range allocations {
			view.Allocations = append(view.Allocations, allocation{Name: a.RecipientName, Kind: a.RecipientKind, Planned: a.PlannedAmountCents, Actual: actuals.PaidByAllocation[a.ID]})
		}
		out = append(out, view)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"project":      map[string]any{"id": p.ID, "name": p.Name, "start_date": p.StartDate.String, "end_date": p.EndDate.String, "note": p.Note},
		"budget":       map[string]any{"total_cents": b.TotalAmountCents, "actual_income_cents": actualIncome, "actual_reserve_cents": actualReserve},
		"milestones":   out,
		"transactions": txViews,
	})
}

func (s *Server) projectBudgetPage(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	p, err := models.GetProject(s.DB, id)
	if err != nil {
		http.Error(w, "找不到專案", http.StatusNotFound)
		return
	}
	b, err := models.GetProjectBudget(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	ms, err := models.ListMilestones(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	actuals, err := models.GetProjectBudgetActuals(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	transactions, _, err := models.ListTransactions(s.DB, models.TxFilter{ProjectID: id})
	if err != nil {
		s.error500(w, err)
		return
	}
	postingCounts, err := models.BudgetPostingCountsForProject(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	type transactionView struct {
		models.Transaction
		PostingCount int
	}
	txViews := make([]transactionView, 0, len(transactions))
	for _, tx := range transactions {
		txViews = append(txViews, transactionView{Transaction: tx, PostingCount: postingCounts[tx.ID]})
	}
	type allocationView struct {
		models.BudgetAllocation
		ActualPaid int64
	}
	type milestoneView struct {
		models.Milestone
		Allocations      []allocationView
		PlannedAllocated int64
		ActualIncome     int64
		ActualReserve    int64
	}
	var plannedCompany, actualIncome, actualReserve int64
	views := make([]milestoneView, 0, len(ms))
	for _, m := range ms {
		a, e := models.ListBudgetAllocations(s.DB, m.ID)
		if e != nil {
			s.error500(w, e)
			return
		}
		var total int64
		av := make([]allocationView, 0, len(a))
		for _, x := range a {
			total += x.PlannedAmountCents
			if x.RecipientKind == "company_reserve" {
				plannedCompany += x.PlannedAmountCents
			}
			av = append(av, allocationView{BudgetAllocation: x, ActualPaid: actuals.PaidByAllocation[x.ID]})
		}
		income := actuals.IncomeByMilestone[m.ID]
		reserve := actuals.ReserveByMilestone[m.ID]
		actualIncome += income
		actualReserve += reserve
		views = append(views, milestoneView{Milestone: m, Allocations: av, PlannedAllocated: total, ActualIncome: income, ActualReserve: reserve})
	}
	cps, _ := models.ListCounterparties(s.DB, "")
	s.render(w, r, "project_budget.html", map[string]any{"Title": "專案預算", "Crumbs": []string{"專案", p.Name, "預算"}, "Project": p, "Budget": b, "Milestones": views, "ProjectTransactions": txViews, "Counterparties": cps, "ActualIncome": actualIncome, "ActualReserve": actualReserve, "PlannedCompany": plannedCompany, "Active": "projects"})
}

func (s *Server) projectBudgetSave(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	amt, err := money.ParseCents(r.FormValue("total_amount"))
	if err != nil || amt < 0 {
		http.Error(w, "總預算金額格式錯誤", 400)
		return
	}
	if err := models.SaveProjectBudget(s.DB, &models.ProjectBudget{ProjectID: id, TotalAmountCents: amt, Note: r.FormValue("note")}); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/projects/"+r.PathValue("id")+"/budget", 303)
}
func (s *Server) projectMilestoneCreate(w http.ResponseWriter, r *http.Request) {
	pid := parseInt64(r.PathValue("id"))
	amt, e := money.ParseCents(r.FormValue("planned_income"))
	if e != nil || amt < 0 || r.FormValue("name") == "" {
		http.Error(w, "請填寫批次名稱與金額", 400)
		return
	}
	ms, _ := models.ListMilestones(s.DB, pid)
	_, e = models.CreateMilestone(s.DB, &models.Milestone{ProjectID: pid, Name: r.FormValue("name"), PlannedIncomeCents: amt, SortOrder: len(ms), Note: r.FormValue("note")})
	if e != nil {
		s.error500(w, e)
		return
	}
	http.Redirect(w, r, "/projects/"+r.PathValue("id")+"/budget", 303)
}
func (s *Server) projectMilestoneDelete(w http.ResponseWriter, r *http.Request) {
	_ = models.DeleteMilestone(s.DB, parseInt64(r.PathValue("milestoneID")))
	http.Redirect(w, r, "/projects/"+r.PathValue("id")+"/budget", 303)
}
func (s *Server) projectAllocationCreate(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("id")
	amt, e := money.ParseCents(r.FormValue("planned_amount"))
	kind := r.FormValue("recipient_kind")
	name := r.FormValue("recipient_name")
	if e != nil || amt < 0 || (kind != "company_reserve" && kind != "labor_compensation" && kind != "cost_expense") || name == "" {
		http.Error(w, "請填寫分配項目與金額", 400)
		return
	}
	a := &models.BudgetAllocation{MilestoneID: parseInt64(r.PathValue("milestoneID")), RecipientKind: kind, RecipientName: name, PlannedAmountCents: amt}
	if cp := parseInt64(r.FormValue("counterparty_id")); cp > 0 {
		a.CounterpartyID = cp
		a.CounterpartyValid = true
	}
	if _, e = models.CreateBudgetAllocation(s.DB, a); e != nil {
		s.error500(w, e)
		return
	}
	http.Redirect(w, r, "/projects/"+pid+"/budget", 303)
}
func (s *Server) projectAllocationDelete(w http.ResponseWriter, r *http.Request) {
	_ = models.DeleteBudgetAllocation(s.DB, parseInt64(r.PathValue("allocationID")))
	http.Redirect(w, r, "/projects/"+r.PathValue("id")+"/budget", 303)
}

func (s *Server) projectCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.error500(w, err)
		return
	}
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	_, err := models.CreateProject(s.DB, &models.Project{
		Name:      name,
		StartDate: models.NullStringFrom(r.FormValue("start_date")),
		EndDate:   models.NullStringFrom(r.FormValue("end_date")),
		Note:      r.FormValue("note"),
	})
	if err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}

func (s *Server) projectUpdate(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	if err := r.ParseForm(); err != nil {
		s.error500(w, err)
		return
	}
	p, err := models.GetProject(s.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if v := r.FormValue("name"); v != "" {
		p.Name = v
	}
	p.StartDate = models.NullStringFrom(r.FormValue("start_date"))
	p.EndDate = models.NullStringFrom(r.FormValue("end_date"))
	p.Note = r.FormValue("note")
	if err := models.UpdateProject(s.DB, p); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}

func (s *Server) projectDelete(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	if err := models.DeleteProject(s.DB, id); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}
