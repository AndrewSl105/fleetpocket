package store_test

import (
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/costs"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func TestSchedulesCRUDAndFirings(t *testing.T) {
	f := newCostFixture(t, "p")
	ctx, st := f.ctx, f.st
	month := costs.MonthStart(time.Now())
	next := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	cap5 := 5.0
	id, err := st.CreateSchedule(ctx, store.ScheduleFields{Name: "Nightly deps", ProjectID: "p", Prompt: "update deps", Cron: "0 3 * * *",
		Timezone: "Europe/Kyiv", Concurrency: store.ConcurrencySkip, PROnlyIfChanges: true, Enabled: true, NextRunAt: &next,
		MonthlyBudgetUSD: &cap5, Skills: []string{"go"}, MCPServers: []string{"linear"},
		Loop: &store.LoopConfig{CheckCommand: "go test ./...", MaxIterations: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSchedule(ctx, store.ScheduleFields{Name: "x", ProjectID: "nope", Prompt: "p", Cron: "* * * * *", Timezone: "UTC", Concurrency: "skip"}); err != store.ErrNotFound {
		t.Fatalf("unknown project: %v", err)
	}
	sc, err := st.GetSchedule(ctx, id, month)
	if err != nil || sc.Name != "Nightly deps" || sc.Loop == nil || sc.Loop.MaxIterations != 3 || sc.MCPServers[0] != "linear" ||
		sc.NextRunAt == nil || !sc.NextRunAt.Equal(next) || sc.Busy || sc.LastFiring != nil {
		t.Fatalf("get: %+v %v", sc, err)
	}

	// Due only once next_run_at has passed.
	if due, _ := st.DueSchedules(ctx, time.Now(), month); len(due) != 0 {
		t.Fatalf("due early: %d", len(due))
	}
	if due, _ := st.DueSchedules(ctx, next.Add(time.Second), month); len(due) != 1 {
		t.Fatalf("due: %d", len(due))
	}
	later := next.Add(24 * time.Hour)
	if ok, _ := st.AdvanceSchedule(ctx, id, next, &later); !ok {
		t.Fatal("advance")
	}
	if ok, _ := st.AdvanceSchedule(ctx, id, next, &later); ok {
		t.Fatal("advanced twice from the same firing")
	}

	// A task of the schedule makes it busy; a firing records it.
	sid := id
	task, err := st.CreateTask(ctx, store.Task{ProjectID: "p", RepositoryID: f.repo["p"], Title: "t", Prompt: "p", Source: "schedule",
		ScheduleID: &sid, MCPServers: []string{"linear"}, PRAlways: true,
		Loop: &store.LoopConfig{CheckCommand: "go test ./...", MaxIterations: 3}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.ScheduleID == nil || *task.ScheduleID != id || !task.PRAlways || task.LoopState == nil || *task.LoopState != store.LoopRunning ||
		task.LoopIteration != 1 {
		t.Fatalf("task: %+v", task)
	}
	if run, _ := st.PendingRun(ctx, task.ID); run.LoopIteration == nil || *run.LoopIteration != 1 {
		t.Fatalf("initial run iteration: %+v", run)
	}
	if busy, _ := st.ScheduleBusy(ctx, id); !busy {
		t.Fatal("not busy")
	}
	if _, err := st.AddFiring(ctx, store.ScheduleFiring{ScheduleID: id, ScheduledFor: next, Trigger: store.TriggerCron, Outcome: store.FiringStarted, TaskID: &task.ID}); err != nil {
		t.Fatal(err)
	}
	q, err := st.AddFiring(ctx, store.ScheduleFiring{ScheduleID: id, ScheduledFor: later, Trigger: store.TriggerManual, Outcome: store.FiringQueued})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.HasQueuedFiring(ctx, id); !ok {
		t.Fatal("no queued firing")
	}
	if qs, _ := st.QueuedFirings(ctx); len(qs) != 1 || qs[0].ID != q.ID {
		t.Fatalf("queued: %+v", qs)
	}
	if err := st.FiringStartedTask(ctx, q.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	hist, _ := st.ScheduleFirings(ctx, id, 10)
	if len(hist) != 2 || hist[0].Outcome != store.FiringStarted || hist[0].TaskStatus == nil || *hist[0].TaskStatus != "queued" {
		t.Fatalf("history: %+v", hist)
	}
	list, _ := st.ListSchedules(ctx, month)
	if len(list) != 1 || !list[0].Busy || list[0].LastFiring == nil || list[0].LastFiring.ID != q.ID {
		t.Fatalf("list: %+v", list)
	}

	if err := st.SetScheduleState(ctx, id, false, nil); err != nil {
		t.Fatal(err)
	}
	if due, _ := st.DueSchedules(ctx, later.Add(time.Hour), month); len(due) != 0 {
		t.Fatal("disabled schedule due")
	}
	if err := st.DeleteSchedule(ctx, id); err != nil {
		t.Fatal(err)
	}
	if nt, _ := st.GetTask(ctx, task.ID); nt.ScheduleID != nil {
		t.Fatal("task keeps a deleted schedule")
	}
}

func TestScheduleMonthlyBudgetGate(t *testing.T) {
	f := newCostFixture(t, "p")
	ctx, st := f.ctx, f.st
	gate := store.BudgetGate{MonthStart: costs.MonthStart(time.Now())}
	one := 1.0
	id, err := st.CreateSchedule(ctx, store.ScheduleFields{Name: "s", ProjectID: "p", Prompt: "p", Cron: "@daily", Timezone: "UTC",
		Concurrency: store.ConcurrencyQueue, MonthlyBudgetUSD: &one, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(sched bool) *store.Task {
		tk := store.Task{ProjectID: "p", RepositoryID: f.repo["p"], Title: "t", Prompt: "p", Source: "api"}
		if sched {
			tk.ScheduleID = &id
		}
		nt, err := st.CreateTask(ctx, tk, nil)
		if err != nil {
			t.Fatal(err)
		}
		return nt
	}
	first := mk(true)
	f.spend(first, store.RunUsage{CostUSD: 1.5, Source: costs.SourceReported})
	if _, err := st.SetStatus(ctx, first.ID, nil, "completed", "", true); err != nil {
		t.Fatal(err)
	}
	held := mk(true)
	other := mk(false)
	got, err := st.ClaimNext(ctx, 5, nil, gate)
	if err != nil || got == nil || got.ID != other.ID {
		t.Fatalf("claim: %+v %v", got, err)
	}
	if got, _ := st.ClaimNext(ctx, 5, nil, gate); got != nil {
		t.Fatalf("schedule over its cap claimed: %d", got.ID)
	}
	changes, err := st.MarkBudgetWaits(ctx, gate, nil)
	if err != nil || len(changes) != 1 || changes[0].Task.ID != held.ID || *changes[0].Task.BudgetWait != costs.ScopeSchedule {
		t.Fatalf("waits: %+v %v", changes, err)
	}
	if _, _, err := st.OverrideBudget(ctx, held.ID, "web"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ClaimNext(ctx, 5, nil, gate); got == nil || got.ID != held.ID {
		t.Fatal("override did not let the task start")
	}
}

func TestLoopIterations(t *testing.T) {
	f := newCostFixture(t, "p")
	ctx, st := f.ctx, f.st
	budget := 2.0
	task, err := st.CreateTask(ctx, store.Task{ProjectID: "p", RepositoryID: f.repo["p"], Title: "t", Prompt: "p", Source: "api",
		Loop: &store.LoopConfig{CheckCommand: "make lint", MaxIterations: 3, BudgetUSD: &budget}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	task = f.spend(task, store.RunUsage{CostUSD: 0.5, Source: costs.SourceReported})
	if _, err := st.SetStatus(ctx, task.ID, nil, "running", "", false); err != nil {
		t.Fatal(err)
	}
	left := 1.5
	nt, ev, err := st.StartLoopIteration(ctx, store.LoopNext{TaskID: task.ID, From: []string{"running"}, Iteration: 2, Idle: 1,
		Input: "check failed", BudgetUSD: &left, Payload: map[string]any{"max_iterations": 3}})
	if err != nil {
		t.Fatal(err)
	}
	if nt.Status != "queued" || nt.LoopIteration != 2 || nt.LoopIdle != 1 || ev.Type != "loop" {
		t.Fatalf("next: %+v %+v", nt, ev)
	}
	run, _ := st.PendingRun(ctx, task.ID)
	if run.Kind != "loop" || *run.LoopIteration != 2 || run.MaxBudgetUSD == nil || *run.MaxBudgetUSD != 1.5 || run.Input != "check failed" {
		t.Fatalf("run: %+v", run)
	}
	f.spend(nt, store.RunUsage{CostUSD: 0.25, Source: costs.SourceReported})
	if v, _ := st.LoopSpent(ctx, task.ID); v != 0.75 {
		t.Fatalf("spent %v", v)
	}
	done, ev, err := st.EndLoop(ctx, task.ID, store.LoopPassed, map[string]any{"iteration": 2})
	if err != nil || *done.LoopState != store.LoopPassed || ev.Type != "loop" {
		t.Fatalf("end: %+v %v", done, err)
	}
	if _, _, err := st.EndLoop(ctx, task.ID, store.LoopBudget, nil); err != store.ErrConflict {
		t.Fatalf("ended twice: %v", err)
	}
	if _, _, err := st.StartLoopIteration(ctx, store.LoopNext{TaskID: task.ID, From: []string{"queued"}, Iteration: 3}); err != store.ErrConflict {
		t.Fatalf("iteration after the end: %v", err)
	}
	if r, err := st.RestartLoop(ctx, task.ID); err != nil || *r.LoopState != store.LoopRunning || r.LoopIteration != 1 {
		t.Fatalf("restart: %+v %v", r, err)
	}
}
