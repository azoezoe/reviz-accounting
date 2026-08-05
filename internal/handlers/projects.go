package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/hcchien/reviz-accounting/internal/auth"
	"github.com/hcchien/reviz-accounting/internal/models"
	"github.com/hcchien/reviz-accounting/internal/money"
)

func (s *Server) projectsList(w http.ResponseWriter, r *http.Request) {
	projs, err := models.ListProjects(s.DB)
	if err != nil {
		s.error500(w, err)
		return
	}
	// Owners see every project. Other roles see only explicit grants.
	if u := auth.FromContext(r.Context()); u != nil && u.Role != auth.RoleOwner {
		filtered := projs[:0]
		for _, p := range projs {
			if ok, _ := models.CanAccessProject(s.DB, p.ID, u.ID, false); ok {
				filtered = append(filtered, p)
			}
		}
		projs = filtered
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
	allocations, err := models.ListProjectBudgetAllocations(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	report, err := models.GetProjectBudgetReport(s.DB, id)
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
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"project":      map[string]any{"id": p.ID, "name": p.Name, "start_date": p.StartDate.String, "end_date": p.EndDate.String, "note": p.Note},
		"budget":       map[string]any{"total_cents": b.TotalAmountCents, "actual_income_cents": report.IncomeCents},
		"allocations":  allocations,
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
	allocations, err := models.ListProjectBudgetAllocations(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	report, err := models.GetProjectBudgetReport(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	filterID := parseInt64(r.URL.Query().Get("allocation_id"))
	filter := models.TxFilter{ProjectID: id}
	if filterID == -1 {
		filter.UnallocatedProjectExpense = true
	} else if filterID > 0 {
		for _, allocation := range allocations {
			if allocation.ID == filterID {
				if allocation.RecipientKind == "company_reserve" {
					filter.ProjectIncomeOnly = true
				} else {
					filter.BudgetAllocationID = filterID
				}
				break
			}
		}
	}
	transactions, _, err := models.ListTransactions(s.DB, filter)
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
		Accrued    int64
	}
	views := make([]allocationView, 0, len(allocations))
	var plannedTotal, plannedCompany int64
	for _, a := range allocations {
		accrued := int64(0)
		if b.TotalAmountCents > 0 {
			accrued = a.PlannedAmountCents * report.IncomeCents / b.TotalAmountCents
		}
		plannedTotal += a.PlannedAmountCents
		if a.RecipientKind == "company_reserve" {
			plannedCompany += a.PlannedAmountCents
		}
		views = append(views, allocationView{BudgetAllocation: a, ActualPaid: report.PaidByAllocation[a.ID], Accrued: accrued})
	}
	cps, _ := models.ListCounterparties(s.DB, "")
	s.render(w, r, "project_budget.html", map[string]any{"Title": "專案預算", "Crumbs": []string{"專案", p.Name, "預算"}, "Project": p, "Budget": b, "Allocations": views, "ProjectTransactions": txViews, "JournalAllocationFilter": filterID, "Counterparties": cps, "ActualIncome": report.IncomeCents, "PlannedTotal": plannedTotal, "PlannedCompany": plannedCompany, "Active": "projects"})
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
	a := &models.BudgetAllocation{ProjectID: parseInt64(pid), RecipientKind: kind, RecipientName: name, PlannedAmountCents: amt}
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
	projectID, err := models.CreateProject(s.DB, &models.Project{
		Name:      name,
		StartDate: models.NullStringFrom(r.FormValue("start_date")),
		EndDate:   models.NullStringFrom(r.FormValue("end_date")),
		Note:      r.FormValue("note"),
	})
	if err != nil {
		s.error500(w, err)
		return
	}
	if u := auth.FromContext(r.Context()); u != nil && u.Role != auth.RoleOwner {
		if err := models.GrantProjectAccess(s.DB, projectID, u.ID, "write"); err != nil {
			s.error500(w, err)
			return
		}
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
