package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

// 00020 goes down and up again on its own, with users and authorship.
func TestTeamsMigrationDownUp(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	u, err := st.EnableTeam(ctx, store.NewUser{Email: "a@b.co", Name: "Ann", PasswordHash: "$2a$x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ts, _ := st.TeamSettings(ctx); !ts.Enabled || u.Role != "admin" || !u.IsOwner {
		t.Fatalf("enable: %+v %+v", ts, u)
	}
	if err := st.MigrateDownTo(ctx, 19); err != nil {
		t.Fatalf("down: %v", err)
	}
	if ts, _ := st.Settings(ctx); ts["team"] != "" {
		t.Fatal("team row survived the down migration")
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if ts, _ := st.TeamSettings(ctx); ts.Enabled {
		t.Fatal("team on after a fresh up")
	}
	if _, err := st.CreateUser(ctx, store.NewUser{Email: "x@y.co", Name: "X", Role: "member", PasswordHash: "h"}, nil); err != nil {
		t.Fatal(err)
	}
}

// A person's monthly budget holds their queued tasks (scope "user") and
// costs are reported by person.
func TestUserBudgetGateAndCostsByPerson(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	month := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.Local)
	u, _ := st.CreateUser(ctx, store.NewUser{Email: "m@b.co", Name: "Mia Member", Role: "member", PasswordHash: "h"}, nil)
	other, _ := st.CreateUser(ctx, store.NewUser{Email: "o@b.co", Name: "Oli", Role: "member", PasswordHash: "h"}, nil)
	ten := 10.0
	if _, err := st.UpdateUser(ctx, u.ID, store.UserFields{MonthlyBudgetUSD: &ten}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProject(ctx, "p", store.ProjectFields{}); err != nil {
		t.Fatal(err)
	}
	repo, _ := st.AddRepository(ctx, store.Repository{ProjectID: "p", FullName: "o/r", BaseBranch: "main", InstallationID: 1})
	mk := func(by int64) *store.Task {
		tk, err := st.CreateTask(ctx, store.Task{ProjectID: "p", RepositoryID: repo.ID, Title: "t", Prompt: "p", Source: "api", CreatedByID: &by}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	spent := mk(u.ID)
	run, _ := st.PendingRun(ctx, spent.ID)
	_ = st.MarkRunStarting(ctx, run.ID)
	if err := st.SetRunUser(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetRunUsage(ctx, run.ID, store.RunUsage{CostUSD: 12, Source: "reported"}); err != nil {
		t.Fatal(err)
	}
	_, _ = st.SetStatus(ctx, spent.ID, nil, "completed", "", true)

	held := mk(u.ID)
	free := mk(other.ID)
	gate := store.BudgetGate{MonthStart: month}
	got, err := st.ClaimNext(ctx, 10, nil, gate)
	if err != nil || got == nil || got.ID != free.ID {
		t.Fatalf("claim: %+v %v", got, err)
	}
	if got, _ := st.ClaimNext(ctx, 10, nil, gate); got != nil {
		t.Fatalf("over-budget user's task claimed: %d", got.ID)
	}
	changes, err := st.MarkBudgetWaits(ctx, gate, nil)
	if err != nil {
		t.Fatal(err)
	}
	var waits string
	for _, c := range changes {
		if c.Task.ID == held.ID && c.Task.BudgetWait != nil {
			waits = *c.Task.BudgetWait
		}
	}
	if waits != "user" {
		t.Fatalf("budget wait scope: %q", waits)
	}
	if v, _ := st.UserMonthSpend(ctx, u.ID, month); v != 12 {
		t.Fatalf("user spend: %v", v)
	}
	people, err := st.CostsByPerson(ctx, month, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(people) == 0 || people[0].UserID == nil || *people[0].UserID != u.ID || people[0].CostUSD != 12 || people[0].Initials != "MM" {
		t.Fatalf("by person: %+v", people)
	}
}

func TestInitials(t *testing.T) {
	for in, want := range map[string]string{"ann lee": "AL", "Ann": "A", "  ": "?", "émile zola x": "ÉZ"} {
		if got := store.Initials(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
