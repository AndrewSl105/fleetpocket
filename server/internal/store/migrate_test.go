package store_test

import (
	"context"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

// Every migration's Down section works: all the way down, then up again.
func TestMigrationsUpDownUp(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	if err := st.MigrateDownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	// The newest columns exist after the second up.
	if _, err := st.CreateProject(ctx, "p", store.ProjectFields{}); err != nil {
		t.Fatal(err)
	}
	on, two := true, 2
	p, err := st.UpdateProject(ctx, "p", store.ProjectFields{CIAutoFix: &store.AutoFixOverrides{Enabled: &on, MaxAttempts: &two}})
	if err != nil || p.CIAutoFix.Enabled == nil || !*p.CIAutoFix.Enabled || *p.CIAutoFix.MaxAttempts != 2 {
		t.Fatalf("ci_autofix: %+v %v", p, err)
	}
}
