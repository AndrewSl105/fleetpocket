package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/costs"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// maxCostRangeDays bounds GET /costs (one query of every run in range).
const maxCostRangeDays = 400

// costSettingsBody is GET/PUT /settings/costs: the global monthly cap and
// the effective price table (PUT with price_table null restores the
// built-in one).
type costSettingsBody struct {
	MonthlyBudgetUSD    *float64          `json:"monthly_budget_usd"`
	PriceTable          *costs.PriceTable `json:"price_table"`
	PriceTableIsDefault bool              `json:"price_table_is_default"`
}

func (s *Server) getCostSettings(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Store.CostSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, costSettingsView(cs))
}

func costSettingsView(cs store.CostSettings) costSettingsBody {
	p := cs.Prices()
	if p.Prices == nil {
		p.Prices = []costs.Price{}
	}
	return costSettingsBody{MonthlyBudgetUSD: cs.MonthlyBudgetUSD, PriceTable: &p, PriceTableIsDefault: cs.PriceTable == nil}
}

func (s *Server) putCostSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MonthlyBudgetUSD *float64          `json:"monthly_budget_usd"`
		PriceTable       *costs.PriceTable `json:"price_table"`
	}
	if !decode(w, r, &in) {
		return
	}
	if v := in.MonthlyBudgetUSD; v != nil && (*v < 0 || *v > projects.MaxMonthlyBudgetUSD) {
		writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("monthly_budget_usd must be 0 to %d", projects.MaxMonthlyBudgetUSD))
		return
	}
	if v := in.MonthlyBudgetUSD; v != nil && *v == 0 {
		in.MonthlyBudgetUSD = nil
	}
	if in.PriceTable != nil {
		if err := in.PriceTable.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "price_table."+err.Error())
			return
		}
		if in.PriceTable.Prices == nil {
			in.PriceTable.Prices = []costs.Price{}
		}
	}
	cs := store.CostSettings{MonthlyBudgetUSD: in.MonthlyBudgetUSD, PriceTable: in.PriceTable}
	if err := s.Store.SetCostSettings(r.Context(), cs); err != nil {
		writeErr(w, r, err)
		return
	}
	// A raised or removed cap lets waiting tasks start.
	if s.Tasks != nil && s.Tasks.Executor != nil {
		s.Tasks.Executor.Wake()
	}
	writeJSON(w, http.StatusOK, costSettingsView(cs))
}

// budgetView is a scope's monthly cap and month-to-date cost.
type budgetView struct {
	MonthlyUSD   *float64 `json:"monthly_usd"`
	MonthCostUSD float64  `json:"month_cost_usd"`
}

type projectBudgetView struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	budgetView
}

// costReport is GET /costs.
type costReport struct {
	From       string              `json:"from"`
	To         string              `json:"to"`
	MonthStart string              `json:"month_start"`
	Budget     budgetView          `json:"budget"`
	Projects   []projectBudgetView `json:"projects"`
	costs.Report
}

// GET /api/v1/costs?from=YYYY-MM-DD&to=YYYY-MM-DD&period=day|week|month:
// runs started from `from` through `to` (inclusive, server local time;
// default: this month to today).
func (s *Server) getCosts(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	loc := now.Location()
	q := r.URL.Query()
	day := func(name string, def time.Time) (time.Time, bool) {
		v := q.Get(name)
		if v == "" {
			return def, true
		}
		t, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", name+" must be YYYY-MM-DD")
			return t, false
		}
		return t, true
	}
	month := costs.MonthStart(now)
	from, ok := day("from", month)
	if !ok {
		return
	}
	last, ok := day("to", time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc))
	if !ok {
		return
	}
	to := last.AddDate(0, 0, 1)
	if !from.Before(to) || to.Sub(from) > maxCostRangeDays*24*time.Hour {
		writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("from must be on or before to, at most %d days apart", maxCostRangeDays))
		return
	}
	period := q.Get("period")
	if period == "" {
		period = "day"
	}
	valid := false
	for _, p := range costs.Periods {
		valid = valid || p == period
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid", "period must be day, week or month")
		return
	}
	ctx := r.Context()
	runs, err := s.Store.RunCosts(ctx, from, to)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	cs, err := s.Store.CostSettings(ctx)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	total, by, err := s.Store.MonthSpend(ctx, month)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	ps, err := s.Store.ListProjects(ctx, true, "", 10000)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := costReport{
		From: from.Format("2006-01-02"), To: last.Format("2006-01-02"), MonthStart: month.Format("2006-01-02"),
		Budget:   budgetView{MonthlyUSD: cs.MonthlyBudgetUSD, MonthCostUSD: roundCents(total)},
		Projects: []projectBudgetView{},
		Report:   costs.Aggregate(runs, from, to, period, loc),
	}
	for _, p := range ps {
		if p.Archived && by[p.ID] == 0 {
			continue
		}
		var cap *float64
		if p.Budget != nil {
			cap = p.Budget.MonthlyUSD
		}
		out.Projects = append(out.Projects, projectBudgetView{ProjectID: p.ID, Name: p.Name,
			budgetView: budgetView{MonthlyUSD: cap, MonthCostUSD: roundCents(by[p.ID])}})
	}
	writeJSON(w, http.StatusOK, out)
}

func roundCents(v float64) float64 { return float64(int64(v*1e4+0.5)) / 1e4 }

// withMonthCost fills in the projects' month-to-date cost.
func (s *Server) withMonthCost(ctx context.Context, ps ...*store.Project) error {
	_, by, err := s.Store.MonthSpend(ctx, costs.MonthStart(s.now()))
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p != nil {
			p.MonthCostUSD = roundCents(by[p.ID])
		}
	}
	return nil
}

// POST /api/v1/tasks/{id}/budget-override: start a task that waits for a
// monthly budget anyway.
func (s *Server) budgetOverride(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	v, err := s.Tasks.OverrideBudget(r.Context(), id, "web")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
