package githubapp

import (
	"strings"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/scheduler"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// Team mode names who asked for the PR; single-owner mode does not.
func TestPRBodyRequestedBy(t *testing.T) {
	p := &Publisher{}
	in := scheduler.PublishInput{Task: &store.Task{ID: 1, Prompt: "do it"}}
	if strings.Contains(p.body(in, ""), "Requested by") {
		t.Fatal("single-owner body names a requester")
	}
	in.RequestedBy = "Mia Member (@mia)"
	if !strings.Contains(p.body(in, ""), "Requested by Mia Member (@mia)") {
		t.Fatal("team body misses the requester")
	}
}
