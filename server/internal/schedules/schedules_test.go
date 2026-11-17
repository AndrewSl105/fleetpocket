package schedules_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/schedules"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

type fixture struct {
	t   *testing.T
	ctx context.Context
	st  *store.Store
	svc *schedules.Service
	now time.Time
	got []string
}

func (f *fixture) ScheduleFired(_ context.Context, _ *store.Schedule, fr *store.ScheduleFiring) {
	f.got = append(f.got, fr.Outcome)
}

func newFixture(t *testing.T) *fixture {
	st := testutil.DB(t)
	ctx := context.Background()
	if _, err := st.CreateProject(ctx, "p", store.ProjectFields{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddRepository(ctx, store.Repository{ProjectID: "p", FullName: "o/p", BaseBranch: "main", InstallationID: 1, IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, ctx: ctx, st: st, now: time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)}
	f.svc = &schedules.Service{Store: st, Tasks: &tasks.Service{Store: st}, Notify: f, Now: func() time.Time { return f.now }}
	return f
}

func (f *fixture) create(in schedules.Input) *store.Schedule {
	f.t.Helper()
	if in.Name == "" {
		in.Name = "Hourly"
	}
	in.ProjectID, in.Prompt = "p", "Update dependencies"
	if in.Cron == "" {
		in.Cron = "0 * * * *"
	}
	sc, err := f.svc.Create(f.ctx, in)
	if err != nil {
		f.t.Fatal(err)
	}
	return sc
}

func (f *fixture) history(id int64) []*store.ScheduleFiring {
	h, err := f.svc.History(f.ctx, id, 50)
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func TestValidation(t *testing.T) {
	f := newFixture(t)
	bad := []schedules.Input{
		{Name: "", ProjectID: "p", Prompt: "x", Cron: "@daily"},
		{Name: "n", ProjectID: "p", Prompt: "", Cron: "@daily"},
		{Name: "n", ProjectID: "p", Prompt: "x", Cron: "61 * * * *"},
		{Name: "n", ProjectID: "p", Prompt: "x", Cron: "@daily", Timezone: "Nowhere/City"},
		{Name: "n", ProjectID: "p", Prompt: "x", Cron: "@daily", Concurrency: "parallel"},
		{Name: "n", ProjectID: "nope", Prompt: "x", Cron: "@daily"},
		{Name: "n", ProjectID: "p", Prompt: "x", Cron: "@daily", Loop: &store.LoopConfig{MaxIterations: 3}},
	}
	for _, in := range bad {
		if _, err := f.svc.Create(f.ctx, in); !errors.Is(err, tasks.ErrInvalid) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	sc := f.create(schedules.Input{Cron: "0 9 * * 1-5", Timezone: "Europe/Kyiv"})
	// 09:30 UTC Tuesday = 11:30 in Kyiv: next is Wednesday 09:00 Kyiv = 07:00 UTC.
	if want := time.Date(2026, 11, 18, 7, 0, 0, 0, time.UTC); sc.NextRunAt == nil || !sc.NextRunAt.Equal(want) {
		t.Fatalf("next %v, want %v", sc.NextRunAt, want)
	}
	if sc.Concurrency != store.ConcurrencySkip || !sc.PROnlyIfChanges || !sc.Enabled {
		t.Fatalf("defaults: %+v", sc)
	}
	ends := f.now.Add(time.Hour)
	if sc := f.create(schedules.Input{Cron: "@daily", EndsAt: &ends}); sc.NextRunAt != nil {
		t.Fatal("next run after the end date")
	}
}

func TestFiringSkipPolicyAndMissed(t *testing.T) {
	f := newFixture(t)
	sc := f.create(schedules.Input{Loop: &store.LoopConfig{CheckCommand: "make test"}})
	f.svc.Tick(f.ctx) // 09:30: not due (next 10:00)
	if len(f.history(sc.ID)) != 0 {
		t.Fatal("fired early")
	}
	f.now = time.Date(2026, 11, 17, 10, 0, 5, 0, time.UTC)
	f.svc.Tick(f.ctx)
	f.svc.Tick(f.ctx) // the same firing only once
	h := f.history(sc.ID)
	if len(h) != 1 || h[0].Outcome != store.FiringStarted || h[0].TaskID == nil {
		t.Fatalf("history: %+v", h)
	}
	task, _ := f.st.GetTask(f.ctx, *h[0].TaskID)
	if task.Source != "schedule" || task.ScheduleID == nil || *task.ScheduleID != sc.ID || task.Title != "Hourly" || task.Loop == nil ||
		task.PRAlways {
		t.Fatalf("task: %+v", task)
	}
	evs, _ := f.st.LastEvents(f.ctx, task.ID, []string{schedules.EventSchedule}, 1)
	if len(evs) != 1 {
		t.Fatal("no schedule event on the task")
	}

	// 11:00 while the 10:00 task is still queued: skipped (default policy).
	f.now = time.Date(2026, 11, 17, 11, 0, 1, 0, time.UTC)
	f.svc.Tick(f.ctx)
	if h := f.history(sc.ID); len(h) != 2 || h[0].Outcome != store.FiringSkipped {
		t.Fatalf("skip: %+v", h[0])
	}

	// The server was down from 11:30 to 14:20: 12:00 was more than one interval ago: missed, no task.
	if _, err := f.st.SetStatus(f.ctx, task.ID, nil, tasks.Completed, "", true); err != nil {
		t.Fatal(err)
	}
	f.now = time.Date(2026, 11, 17, 14, 20, 0, 0, time.UTC)
	f.svc.Tick(f.ctx)
	h = f.history(sc.ID)
	if len(h) != 3 || h[0].Outcome != store.FiringMissed || h[0].TaskID != nil {
		t.Fatalf("missed: %+v", h[0])
	}
	got, _ := f.svc.Get(f.ctx, sc.ID)
	if want := time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC); !got.NextRunAt.Equal(want) {
		t.Fatalf("next after missed: %v", got.NextRunAt)
	}
	// Down for less than an interval: 15:00 runs once at 15:40.
	f.now = time.Date(2026, 11, 17, 15, 40, 0, 0, time.UTC)
	f.svc.Tick(f.ctx)
	if h := f.history(sc.ID); h[0].Outcome != store.FiringStarted || !h[0].ScheduledFor.Equal(time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("late firing: %+v", h[0])
	}
	if f.got[len(f.got)-1] != store.FiringStarted || len(f.got) != 4 {
		t.Fatalf("notified: %v", f.got)
	}
}

func TestQueuePolicy(t *testing.T) {
	f := newFixture(t)
	sc := f.create(schedules.Input{Concurrency: store.ConcurrencyQueue})
	first, err := f.svc.RunNow(f.ctx, sc.ID, "web")
	if err != nil || first.Outcome != store.FiringStarted || first.Trigger != store.TriggerManual {
		t.Fatalf("run now: %+v %v", first, err)
	}
	q, _ := f.svc.RunNow(f.ctx, sc.ID, "telegram")
	again, _ := f.svc.RunNow(f.ctx, sc.ID, "web")
	if q.Outcome != store.FiringQueued || again.Outcome != store.FiringSkipped {
		t.Fatalf("queue: %s %s", q.Outcome, again.Outcome)
	}
	f.svc.Tick(f.ctx) // still busy: stays queued
	if h := f.history(sc.ID); h[1].Outcome != store.FiringQueued {
		t.Fatalf("started while busy: %+v", h[1])
	}
	if _, err := f.st.SetStatus(f.ctx, *first.TaskID, nil, tasks.PROpen, "", false); err != nil {
		t.Fatal(err)
	}
	f.svc.Tick(f.ctx)
	h := f.history(sc.ID)
	if h[1].ID != q.ID || h[1].Outcome != store.FiringStarted || h[1].TaskID == nil || *h[1].TaskID == *first.TaskID {
		t.Fatalf("queued firing: %+v", h[1])
	}
}

func TestEnableDisableDuplicate(t *testing.T) {
	f := newFixture(t)
	sc := f.create(schedules.Input{})
	off, err := f.svc.SetEnabled(f.ctx, sc.ID, false)
	if err != nil || off.Enabled || off.NextRunAt != nil {
		t.Fatalf("disable: %+v %v", off, err)
	}
	f.now = f.now.Add(5 * time.Hour)
	f.svc.Tick(f.ctx)
	if len(f.history(sc.ID)) != 0 {
		t.Fatal("paused schedule fired")
	}
	on, _ := f.svc.SetEnabled(f.ctx, sc.ID, true)
	if !on.Enabled || on.NextRunAt == nil || !on.NextRunAt.After(f.now) {
		t.Fatalf("enable: %+v", on)
	}
	dup, err := f.svc.Duplicate(f.ctx, sc.ID)
	if err != nil || dup.ID == sc.ID || dup.Name != "Hourly (copy)" || dup.Enabled || dup.Prompt != sc.Prompt || dup.Cron != sc.Cron {
		t.Fatalf("duplicate: %+v %v", dup, err)
	}
	if _, err := f.svc.Get(f.ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestQueuedCronFiringOfAPausedSchedule(t *testing.T) {
	f := newFixture(t)
	sc := f.create(schedules.Input{Concurrency: store.ConcurrencyQueue})
	first, _ := f.svc.RunNow(f.ctx, sc.ID, "web")
	f.now = time.Date(2026, 11, 17, 10, 0, 5, 0, time.UTC)
	f.svc.Tick(f.ctx) // 10:00 while the first task is queued: the firing waits
	if h := f.history(sc.ID); h[0].Outcome != store.FiringQueued || h[0].Trigger != store.TriggerCron {
		t.Fatalf("queued: %+v", h[0])
	}
	if _, err := f.svc.SetEnabled(f.ctx, sc.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.SetStatus(f.ctx, *first.TaskID, nil, tasks.Completed, "", true); err != nil {
		t.Fatal(err)
	}
	f.svc.Tick(f.ctx)
	if h := f.history(sc.ID); h[0].Outcome != store.FiringSkipped || h[0].TaskID != nil {
		t.Fatalf("paused: %+v", h[0])
	}
}
