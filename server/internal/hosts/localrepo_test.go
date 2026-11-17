package hosts

import (
	"context"
	"errors"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// Local repositories live on the server's disk: a node refuses every call
// before anything is sent to it.
func TestRemoteRefusesLocalRepositories(t *testing.T) {
	ctx := context.Background()
	r := &Remote{}
	if err := r.PrepareWorkspace(ctx, 1, workspace.Repo{LocalPath: "/home/me/app", BaseBranch: "main"}, "agent/1"); !errors.Is(err, ErrLocalRepoOnNode) {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := r.DeliverLocal(ctx, 1, LocalDelivery{Path: "/home/me/app"}); !errors.Is(err, ErrLocalRepoOnNode) {
		t.Fatalf("deliver: %v", err)
	}
	if _, err := r.MergeLocal(ctx, "/home/me/app", "jm/1"); !errors.Is(err, ErrLocalRepoOnNode) {
		t.Fatalf("merge: %v", err)
	}
	if err := r.CheckoutLocal(ctx, "/home/me/app", "jm/1"); !errors.Is(err, ErrLocalRepoOnNode) {
		t.Fatalf("checkout: %v", err)
	}
	if err := r.DiscardLocal(ctx, "/home/me/app", "jm/1"); !errors.Is(err, ErrLocalRepoOnNode) {
		t.Fatalf("discard: %v", err)
	}
}
