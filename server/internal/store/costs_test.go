package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/costs"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

type costFixture struct {
	t    *testing.T
	st   *store.Store
	ctx  context.Context
	repo map[string]int64
}

func newCostFixture(t *testing.T, projects ...string) *costFixture {
	f := &costFixture{t: t, st: testutil.DB(t), ctx: context.Background(), repo: map[string]int64{}}
	for _, p := range projects {
		if _, err := f.st.CreateProject(f.ctx, p, store.ProjectFields{}); err != nil {
			t.Fatal(err)
		}
		r, err := f.st.AddRepository(f.ctx, store.Repository{ProjectID: p, FullName: "o/" + p, BaseBranch: "dev", InstallationID: 1})
		if err != nil {
			t.Fatal(err)
		}
		f.repo[p] = r.ID
	}
	return f
}

func (f *costFixture) task(project string) *store.Task {
	t, err := f.st.CreateTask(f.ctx, store.Task{ProjectID: project, RepositoryID: f.repo[project], Title: "t", Prompt: "p", Source: "api"}, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return t
}

// spend starts the task's pending run and records usage for it.
func (f *costFixture) spend(t *store.Task, u store.RunUsage) *store.Task {
	run, err := f.st.PendingRun(f.ctx, t.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.st.MarkRunStarting(f.ctx, run.ID); err != nil {
		f.t.Fatal(err)
	}
	nt, err := f.st.SetRunUsage(f.ctx, run.ID, u)
	if err != nil {
		f.t.Fatal(err)
	}
	return nt
}

func TestRunUsageTotals(t *testing.T) {
	f := newCostFixture(t, "p")
	task := f.task("p")
	turns := 3
	nt := f.spend(task, store.RunUsage{CostUSD: 0.5, Source: costs.SourceReported, Turns: &turns, TokensKnown: true,
		Tokens: costs.Tokens{Input: 100, Output: 10, CacheRead: 40}})
	if nt.CostUSDTotal != 0.5 || nt.Turns != 3 || nt.CostEstimated || nt.AutoFixCostUSD != 0 {
		t.Fatalf("after one run: %+v", nt)
	}
	run, _ := f.st.LatestRun(f.ctx, task.ID)
	if err := f.st.SetRunAgent(f.ctx, run.ID, "claude", "claude-x", costs.AuthSubscription); err != nil {
		t.Fatal(err)
	}
	// `done` carries no tokens: they stay.
	nt, err := f.st.SetRunUsage(f.ctx, run.ID, store.RunUsage{CostUSD: 0.6, Source: costs.SourceReported})
	if err != nil || nt.CostUSDTotal != 0.6 || !nt.CostAPIEquivalent || nt.Turns != 3 {
		t.Fatalf("done: %+v %v", nt, err)
	}
	run, _ = f.st.GetRun(f.ctx, run.ID)
	if run.InputTokens != 100 || run.CacheReadTokens != 40 || *run.Model != "claude-x" || *run.CostSource != "reported" {
		t.Fatalf("run: %+v", run)
	}
	// An auto-fix attempt with an estimated cost.
	if _, err := f.st.SetStatus(f.ctx, task.ID, nil, "pr_open", "", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.st.StartAutoFix(f.ctx, store.AutoFixStart{TaskID: task.ID, Attempt: 1, MaxAttempts: 2, Input: "fix"}); err != nil {
		t.Fatal(err)
	}
	nt = f.spend(task, store.RunUsage{CostUSD: 0.25, Source: costs.SourceEstimated, TokensKnown: true})
	if nt.CostUSDTotal != 0.85 || nt.AutoFixCostUSD != 0.25 || !nt.CostEstimated {
		t.Fatalf("with auto-fix: %+v", nt)
	}
	// The session baseline is what the earlier runs recorded.
	pending, _ := f.st.LatestRun(f.ctx, task.ID)
	c, tok, err := f.st.SessionBaseline(f.ctx, task.ID, pending.ID+1)
	if err != nil || c != 0.85 || tok.Input != 100 {
		t.Fatalf("baseline: %v %+v %v", c, tok, err)
	}
}

func TestBudgetGate(t *testing.T) {
	f := newCostFixture(t, "a", "b")
	ctx := f.ctx
	gate := store.BudgetGate{MonthStart: costs.MonthStart(time.Now())}
	ten := 10.0
	if _, err := f.st.UpdateProject(ctx, "a", store.ProjectFields{Budget: &store.ProjectBudget{MonthlyUSD: &ten}}); err != nil {
		t.Fatal(err)
	}
	// Project a has spent its 10 dollars (a run that already finished).
	done := f.spend(f.task("a"), store.RunUsage{CostUSD: 10, Source: costs.SourceReported})
	if _, err := f.st.SetStatus(ctx, done.ID, nil, "completed", "", true); err != nil {
		t.Fatal(err)
	}
	ta, tb := f.task("a"), f.task("b")

	got, err := f.st.ClaimNext(ctx, 5, nil, gate)
	if err != nil || got == nil || got.ID != tb.ID {
		t.Fatalf("claimed %+v, %v; want only project b's task", got, err)
	}
	if got, _ := f.st.ClaimNext(ctx, 5, nil, gate); got != nil {
		t.Fatalf("claimed %d over project a's budget", got.ID)
	}
	changes, err := f.st.MarkBudgetWaits(ctx, gate, func(string, *store.Task) (float64, float64) { return 10, 10 })
	if err != nil || len(changes) != 1 || changes[0].Task.ID != ta.ID || changes[0].Event == nil {
		t.Fatalf("waits: %+v %v", changes, err)
	}
	w := changes[0].Task
	if w.BudgetWait == nil || *w.BudgetWait != "project" || w.StatusReason == nil || *w.StatusReason != store.BudgetWaitReason {
		t.Fatalf("waiting task: %+v", w)
	}
	// Marking again changes nothing (no second event).
	if again, _ := f.st.MarkBudgetWaits(ctx, gate, nil); len(again) != 0 {
		t.Fatalf("marked twice: %+v", again)
	}
	// A global cap holds project b too; nothing is marked for running tasks.
	global := gate
	global.GlobalUSD = 5
	tb2 := f.task("b")
	if got, _ := f.st.ClaimNext(ctx, 5, nil, global); got != nil {
		t.Fatalf("claimed %d over the global cap", got.ID)
	}
	changes, _ = f.st.MarkBudgetWaits(ctx, global, nil)
	if len(changes) != 1 || changes[0].Task.ID != tb2.ID || *changes[0].Task.BudgetWait != "global" {
		t.Fatalf("global waits: %+v", changes)
	}

	// The owner's override lets task a start despite the cap.
	ov, ev, err := f.st.OverrideBudget(ctx, ta.ID, "web")
	if err != nil || ov.BudgetWait != nil || ov.BudgetOverrideAt == nil || ov.StatusReason != nil || ev.Type != "budget" {
		t.Fatalf("override: %+v %+v %v", ov, ev, err)
	}
	if got, _ := f.st.ClaimNext(ctx, 5, nil, global); got == nil || got.ID != ta.ID || got.BudgetWait != nil {
		t.Fatalf("override not claimed: %+v", got)
	}
	if _, _, err := f.st.OverrideBudget(ctx, ta.ID, "web"); err != store.ErrConflict {
		t.Fatalf("override of a running task: %v", err)
	}
	// In a new month nothing has been spent yet: the wait ends.
	if changes, _ := f.st.MarkBudgetWaits(ctx, store.BudgetGate{MonthStart: time.Now().Add(time.Hour), GlobalUSD: 5}, nil); len(changes) != 1 || changes[0].Task.BudgetWait != nil {
		t.Fatalf("new month: %+v", changes)
	}
	// Without the global cap the task is claimed.
	if got, _ := f.st.ClaimNext(ctx, 5, nil, gate); got == nil || got.ID != tb2.ID {
		t.Fatalf("released task not claimed: %+v", got)
	}
}

func TestBudgetAlertsOncePerMonth(t *testing.T) {
	f := newCostFixture(t)
	nov := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i, want := range []bool{true, false} {
		if ok, err := f.st.AddBudgetAlert(f.ctx, "project", "a", nov, 80); err != nil || ok != want {
			t.Fatalf("try %d: %v %v", i, ok, err)
		}
	}
	if ok, _ := f.st.AddBudgetAlert(f.ctx, "project", "a", nov, 100); !ok {
		t.Fatal("100% after 80% is new")
	}
	if ok, _ := f.st.AddBudgetAlert(f.ctx, "project", "a", nov.AddDate(0, 1, 0), 80); !ok {
		t.Fatal("a new month alerts again")
	}
	if ok, _ := f.st.AddBudgetAlert(f.ctx, "global", "", nov, 80); !ok {
		t.Fatal("another scope alerts on its own")
	}
}
