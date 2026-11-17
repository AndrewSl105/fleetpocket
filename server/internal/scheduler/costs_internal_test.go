package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/costs"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

func costScheduler(t *testing.T) (*Scheduler, context.Context, int64) {
	st := testutil.DB(t)
	ctx := context.Background()
	if _, err := st.CreateProject(ctx, "demo", store.ProjectFields{}); err != nil {
		t.Fatal(err)
	}
	r, err := st.AddRepository(ctx, store.Repository{ProjectID: "demo", FullName: "o/r", BaseBranch: "dev", InstallationID: 1})
	if err != nil {
		t.Fatal(err)
	}
	return &Scheduler{Cfg: &config.Config{}, Store: st, Hub: events.NewHub()}, ctx, r.ID
}

func newTask(t *testing.T, s *Scheduler, repo int64, agent string) *store.Task {
	task, err := s.Store.CreateTask(context.Background(), store.Task{ProjectID: "demo", RepositoryID: repo, Title: "t", Prompt: "p", Source: "api", Agent: agent}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// startRun starts the task's pending run and returns it.
func startRun(t *testing.T, s *Scheduler, task *store.Task) *store.Run {
	run, err := s.Store.PendingRun(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.MarkRunStarting(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	return run
}

func usageEv(cost float64, in, out int64) runnermgr.Event {
	return runnermgr.Event{Type: runnermgr.EvUsage, CostUSDTotal: cost, InputTokens: in, OutputTokens: out, CacheReadTokens: in / 2, Turns: 1}
}

func TestRunUsageReportedAndEstimated(t *testing.T) {
	s, ctx, repo := costScheduler(t)

	// Claude reports cumulative session cost and tokens: a follow-up keeps
	// only its own share.
	c := newTask(t, s, repo, "claude")
	first := startRun(t, s, c)
	s.recordUsage(ctx, c, first.ID, usageEv(0.40, 1000, 100), s.usageBase(ctx, c, first))
	if _, err := s.Store.Requeue(ctx, c.ID, nil, tasks.RunFollowup, "more"); err != nil {
		t.Fatal(err)
	}
	second := startRun(t, s, c)
	if second.Kind != tasks.RunFollowup {
		t.Fatalf("kind %s", second.Kind)
	}
	s.recordUsage(ctx, c, second.ID, usageEv(0.65, 1500, 160), s.usageBase(ctx, c, second))
	r, _ := s.Store.GetRun(ctx, second.ID)
	if r.CostUSD != 0.25 || r.InputTokens != 500 || r.OutputTokens != 60 || *r.CostSource != costs.SourceReported {
		t.Fatalf("follow-up run: %+v", r)
	}
	if got, _ := s.Store.GetTask(ctx, c.ID); got.CostUSDTotal != 0.65 || got.CostEstimated {
		t.Fatalf("claude task: %+v", got)
	}

	// Codex reports tokens only: priced from the table, marked estimated.
	x := newTask(t, s, repo, "codex")
	xr := startRun(t, s, x)
	base := s.usageBase(ctx, x, xr)
	s.handleEvent(ctx, x, xr.ID, base, runnermgr.Event{Type: runnermgr.EvStarted, Seq: 1, Model: "gpt-5.5", Raw: json.RawMessage(`{}`)}, &runOutcome{})
	s.recordUsage(ctx, x, xr.ID, usageEv(0, 1_000_000, 100_000), base)
	r, _ = s.Store.GetRun(ctx, xr.ID)
	// 500k plain × $5 + 500k cached × $0.50 + 100k out × $30 = 2.5 + 0.25 + 3.
	if *r.CostSource != costs.SourceEstimated || r.CostUSD != 5.75 || *r.Model != "gpt-5.5" || *r.Agent != "codex" {
		t.Fatalf("codex run: %+v", r)
	}
	// Its `done` (cost 0) leaves the estimate alone.
	s.recordUsage(ctx, x, xr.ID, runnermgr.Event{Type: runnermgr.EvDone}, base)
	if got, _ := s.Store.GetTask(ctx, x.ID); got.CostUSDTotal != 5.75 || !got.CostEstimated {
		t.Fatalf("codex task: %+v", got)
	}

	// Cursor "auto" has no price: unknown, cost 0, tokens kept.
	u := newTask(t, s, repo, "cursor")
	ur := startRun(t, s, u)
	ub := s.usageBase(ctx, u, ur)
	ub.model = "auto"
	s.recordUsage(ctx, u, ur.ID, usageEv(0, 2000, 10), ub)
	r, _ = s.Store.GetRun(ctx, ur.ID)
	if *r.CostSource != costs.SourceUnknown || r.CostUSD != 0 || r.InputTokens != 2000 {
		t.Fatalf("cursor run: %+v", r)
	}
	if got, _ := s.Store.GetTask(ctx, u.ID); !got.CostUnknown || got.CostUSDTotal != 0 {
		t.Fatalf("cursor task: %+v", got)
	}
	if st, _ := s.Store.GetStats(ctx, time.Now()); !st.CostUnknownToday || !st.CostUnknownMonth {
		t.Fatalf("stats: %+v", st)
	}
	if any, by, _ := s.Store.MonthUnknown(ctx, costs.MonthStart(time.Now())); !any || !by["demo"] {
		t.Fatalf("month unknown: %v %v", any, by)
	}
	// A "*" row the owner adds prices it from then on.
	tbl := costs.PriceTable{Updated: "2026-11-17", Prices: []costs.Price{{Agent: "cursor", Model: "*", InputUSD: 1, OutputUSD: 1}}}
	if err := s.Store.SetCostSettings(ctx, store.CostSettings{PriceTable: &tbl}); err != nil {
		t.Fatal(err)
	}
	s.recordUsage(ctx, u, ur.ID, usageEv(0, 1_000_000, 0), ub)
	if r, _ = s.Store.GetRun(ctx, ur.ID); *r.CostSource != costs.SourceEstimated || r.CostUSD != 1 {
		t.Fatalf("cursor with a * row: %+v", r)
	}
	if got, _ := s.Store.GetTask(ctx, u.ID); got.CostUnknown || !got.CostEstimated {
		t.Fatalf("cursor task once priced: %+v", got)
	}
}

func budgetEvents(t *testing.T, s *Scheduler, taskID int64) []map[string]any {
	evs, err := s.Store.LastEvents(context.Background(), taskID, []string{"budget"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range evs {
		var p map[string]any
		_ = json.Unmarshal(e.Payload, &p)
		out = append(out, p)
	}
	return out
}

func TestBudgetThresholdNotifications(t *testing.T) {
	s, ctx, repo := costScheduler(t)
	alerts := map[string]int{}
	s.Hooks.BudgetAlert = func(scope string, level int) { alerts[scope] += level }
	ten := 10.0
	if _, err := s.Store.UpdateProject(ctx, "demo", store.ProjectFields{Budget: &store.ProjectBudget{MonthlyUSD: &ten}}); err != nil {
		t.Fatal(err)
	}
	task := newTask(t, s, repo, "claude")
	run := startRun(t, s, task)
	base := s.usageBase(ctx, task, run)

	s.recordUsage(ctx, task, run.ID, usageEv(7, 0, 0), base)
	if evs := budgetEvents(t, s, task.ID); len(evs) != 0 {
		t.Fatalf("70%%: %+v", evs)
	}
	s.recordUsage(ctx, task, run.ID, usageEv(8.5, 0, 0), base)
	s.recordUsage(ctx, task, run.ID, usageEv(9, 0, 0), base) // still 80%: once per month
	evs := budgetEvents(t, s, task.ID)
	if len(evs) != 1 || evs[0]["phase"] != "warning" || evs[0]["scope"] != "project" || evs[0]["spent_usd"] != 8.5 {
		t.Fatalf("80%%: %+v", evs)
	}
	s.recordUsage(ctx, task, run.ID, usageEv(10.2, 0, 0), base)
	evs = budgetEvents(t, s, task.ID)
	if len(evs) != 2 || evs[1]["phase"] != "reached" || evs[1]["limit_usd"] != 10.0 {
		t.Fatalf("100%%: %+v", evs)
	}
	if alerts["project"] != 180 || alerts["global"] != 0 {
		t.Fatalf("metrics: %+v", alerts)
	}
	// The running run is not touched by the cap.
	if got, _ := s.Store.GetTask(ctx, task.ID); got.Status != tasks.Queued || got.BudgetWait != nil {
		t.Fatalf("task after the cap: %+v", got)
	}
}

func TestBudgetGateAndOverride(t *testing.T) {
	s, ctx, repo := costScheduler(t)
	held := 0
	s.Hooks.BudgetHeld = func(string, string) { held++ }
	five := 5.0
	if err := s.Store.SetCostSettings(ctx, store.CostSettings{MonthlyBudgetUSD: &five}); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Now() }
	spender := newTask(t, s, repo, "claude")
	run := startRun(t, s, spender)
	s.recordUsage(ctx, spender, run.ID, usageEv(6, 0, 0), s.usageBase(ctx, spender, run))
	if _, err := s.Store.SetStatus(ctx, spender.ID, nil, tasks.Completed, "", true); err != nil {
		t.Fatal(err)
	}

	waiting := newTask(t, s, repo, "claude")
	msgs, unsub := s.Hub.Subscribe(64)
	defer unsub()
	gate := s.budgetGate(ctx)
	if gate.GlobalUSD != 5 {
		t.Fatalf("gate: %+v", gate)
	}
	if got, _ := s.Store.ClaimNext(ctx, 5, nil, gate); got != nil {
		t.Fatalf("claimed %d over the global cap", got.ID)
	}
	s.markBudgetWaits(ctx, gate)
	s.markBudgetWaits(ctx, gate)
	got, _ := s.Store.GetTask(ctx, waiting.ID)
	if got.BudgetWait == nil || *got.BudgetWait != "global" || held != 1 {
		t.Fatalf("waiting: %+v held=%d", got, held)
	}
	evs := budgetEvents(t, s, waiting.ID)
	if len(evs) != 1 || evs[0]["phase"] != "waiting" || evs[0]["spent_usd"] != 6.0 || evs[0]["limit_usd"] != 5.0 {
		t.Fatalf("waiting events: %+v", evs)
	}
	published := false
	for len(msgs) > 0 {
		if m := <-msgs; m.Event != nil && m.Event.Type == "budget" {
			published = true
		}
	}
	if !published {
		t.Fatal("the budget event was not published")
	}

	svc := &tasks.Service{Store: s.Store, Hub: s.Hub}
	overrides := 0
	svc.OnBudgetOverride = func(string) { overrides++ }
	if _, err := svc.OverrideBudget(ctx, waiting.ID, "web"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Store.ClaimNext(ctx, 5, nil, gate); c == nil || c.ID != waiting.ID || overrides != 1 {
		t.Fatalf("override not claimed: %+v", c)
	}
	if _, err := svc.OverrideBudget(ctx, waiting.ID, "web"); err == nil {
		t.Fatal("override of a task that is not queued")
	}
}
