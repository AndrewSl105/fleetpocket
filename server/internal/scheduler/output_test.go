package scheduler_test

import (
	"strings"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/scheduler"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func TestSystemPromptOutputContract(t *testing.T) {
	repo := &store.Repository{FullName: "o/r", BaseBranch: "main"}
	pr := scheduler.SystemPrompt(&store.Task{ID: 1, Output: "pr"}, repo, "b", "")
	if strings.Contains(pr, ".fleetpocket/out") {
		t.Fatal("pr tasks get no output contract")
	}
	art := scheduler.SystemPrompt(&store.Task{ID: 1, Output: "artifacts"}, repo, "b", "")
	for _, want := range []string{".fleetpocket/out/", "manifest.json", "erDiagram", "Do not modify repository files"} {
		if !strings.Contains(art, want) {
			t.Errorf("artifacts prompt misses %q", want)
		}
	}
	both := scheduler.SystemPrompt(&store.Task{ID: 1, Output: "pr_and_artifacts"}, repo, "b", "")
	if !strings.Contains(both, "manifest.json") || strings.Contains(both, "Do not modify repository files") {
		t.Fatal(both)
	}
}
