package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

func TestMCPServersAndGlobals(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	newTask(t, st)
	demo := "demo"
	url := "https://mcp.example.com/mcp"
	own, err := st.CreateMCPServer(ctx, &store.MCPServer{ProjectID: &demo, Name: "linear", Kind: store.MCPRemote, URL: &url,
		Secrets: []byte{1, 2, 3}, SecretHints: map[string]string{"Authorization": "…abcd"}, ToolsMode: "all", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateMCPServer(ctx, &store.MCPServer{ProjectID: &demo, Name: "linear", Kind: store.MCPRemote, ToolsMode: "all"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	global, err := st.CreateMCPServer(ctx, &store.MCPServer{Name: "sentry", Kind: store.MCPRemote, URL: &url, ToolsMode: "deny", Tools: []string{"delete"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// A global and a project server may share a name.
	if _, err := st.CreateMCPServer(ctx, &store.MCPServer{Name: "linear", Kind: store.MCPRemote, ToolsMode: "all"}); err != nil {
		t.Fatalf("global with a project's name: %v", err)
	}
	eff, _ := st.EffectiveMCPServers(ctx, demo)
	if len(eff) != 1 || eff[0].ID != own.ID || string(eff[0].Secrets) != "\x01\x02\x03" {
		t.Fatalf("effective before enabling the global: %+v", eff)
	}
	if err := st.SetProjectMCPGlobals(ctx, demo, []int64{own.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a project server as global: %v", err)
	}
	if err := st.SetProjectMCPGlobals(ctx, demo, []int64{global.ID}); err != nil {
		t.Fatal(err)
	}
	eff, _ = st.EffectiveMCPServers(ctx, demo)
	if len(eff) != 2 {
		t.Fatalf("effective: %d", len(eff))
	}
	own.Enabled = false
	if _, err := st.UpdateMCPServer(ctx, own); err != nil {
		t.Fatal(err)
	}
	eff, _ = st.EffectiveMCPServers(ctx, demo)
	if len(eff) != 1 || eff[0].Name != "sentry" || eff[0].Tools[0] != "delete" {
		t.Fatalf("disabled server still effective: %+v", eff)
	}
}

func TestEgressPolicy(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	newTask(t, st)
	e, err := st.ProjectEgress(ctx, "demo")
	if err != nil || e.Mode != store.EgressOpen || len(e.Hosts) != 0 {
		t.Fatalf("default: %+v %v", e, err)
	}
	if err := st.SetProjectEgress(ctx, "demo", store.Egress{Mode: store.EgressAllowlist, Presets: []string{"npm"}}); err != nil {
		t.Fatal(err)
	}
	e, _ = st.AddEgressHost(ctx, "demo", "example.com")
	e, _ = st.AddEgressHost(ctx, "demo", "example.com")
	if e.Mode != store.EgressAllowlist || len(e.Hosts) != 1 || e.Presets[0] != "npm" {
		t.Fatalf("after allow: %+v", e)
	}
}

func TestRunGatewayToken(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	task := newTask(t, st)
	run, _ := st.PendingRun(ctx, task.ID)
	_ = st.MarkRunStarting(ctx, run.ID)
	if err := st.SetRunGateway(ctx, run.ID, "hash1", time.Now().Add(time.Hour), []int64{7, 9}); err != nil {
		t.Fatal(err)
	}
	g, err := st.RunByGatewayToken(ctx, "hash1")
	if err != nil || g.RunID != run.ID || g.TaskID != task.ID || g.ProjectID != "demo" || len(g.ServerIDs) != 2 {
		t.Fatalf("lookup: %+v %v", g, err)
	}
	if _, err := st.RunByGatewayToken(ctx, "other"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
	// Expired.
	_ = st.SetRunGateway(ctx, run.ID, "hash2", time.Now().Add(-time.Second), nil)
	if _, err := st.RunByGatewayToken(ctx, "hash2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired token: %v", err)
	}
	// Finishing the run revokes it.
	_ = st.SetRunGateway(ctx, run.ID, "hash3", time.Now().Add(time.Hour), nil)
	if _, err := st.FinishRun(ctx, run.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RunByGatewayToken(ctx, "hash3"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("token after FinishRun: %v", err)
	}
}
