package store_test

import (
	"context"
	"testing"
	"time"

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
	// 00012: MCP servers, egress, run gateway tokens.
	if _, err := st.CreateMCPServer(ctx, &store.MCPServer{Name: "g", Kind: store.MCPRemote, ToolsMode: "all"}); err != nil {
		t.Fatalf("mcp_servers: %v", err)
	}
	if err := st.SetProjectEgress(ctx, "p", store.Egress{Mode: store.EgressAllowlist}); err != nil {
		t.Fatalf("egress: %v", err)
	}
	// 00013: costs and budgets.
	ten := 10.0
	if p, err := st.UpdateProject(ctx, "p", store.ProjectFields{Budget: &store.ProjectBudget{MonthlyUSD: &ten}}); err != nil || p.Budget.Cap() != 10 {
		t.Fatalf("budget: %+v %v", p, err)
	}
	if err := st.SetCostSettings(ctx, store.CostSettings{MonthlyBudgetUSD: &ten}); err != nil {
		t.Fatalf("cost settings: %v", err)
	}
	if _, err := st.AddBudgetAlert(ctx, "global", "", time.Now(), 80); err != nil {
		t.Fatalf("budget_alerts: %v", err)
	}
}
