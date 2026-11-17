package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

// nodeHost is a node: it cannot enforce an allowlist (no Isolator). Any
// call other than ID would panic, proving nothing ran before the refusal.
type nodeHost struct{ hosts.Host }

func (nodeHost) ID() string { return "n1" }

func TestAllowlistOnNodeFailsClosed(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	p, _ := st.CreateProject(ctx, "demo", store.ProjectFields{})
	if err := st.SetProjectEgress(ctx, "demo", store.Egress{Mode: store.EgressAllowlist}); err != nil {
		t.Fatal(err)
	}
	s := &Scheduler{Cfg: &config.Config{}, Store: st}
	_, err := s.prepareIntegrations(ctx, nodeHost{}, &store.Task{ID: 1, ProjectID: "demo", Agent: "claude"}, &store.Run{ID: 1}, p)
	if !errors.Is(err, errNoIsolation) {
		t.Fatalf("allowlist on a node: %v", err)
	}
}
