package scheduler

import (
	"regexp"
	"slices"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func TestSubagentDefaults(t *testing.T) {
	s := &Scheduler{Cfg: &config.Config{CheapModelClaude: "haiku", DefaultModel: "claude-opus-5-5"}}
	p := &store.Project{Subagents: true}

	got := s.subagents(&store.Task{Agent: "claude"}, p)
	name := regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	if len(got) != 3 {
		t.Fatalf("subagents = %+v", got)
	}
	want := map[string]struct {
		model string
		tools []string
	}{
		"explorer":    {"haiku", []string{"Read", "Grep", "Glob"}},
		"test-runner": {"haiku", []string{"Bash", "Read"}},
		"reviewer":    {"claude-opus-5-5", []string{"Read", "Grep", "Bash"}},
	}
	for _, a := range got {
		w, ok := want[a.Name]
		if !ok || !name.MatchString(a.Name) {
			t.Fatalf("unexpected subagent %q", a.Name)
		}
		if a.Model != w.model || !slices.Equal(a.Tools, w.tools) || a.Prompt == "" || a.Description == "" {
			t.Errorf("%s = %+v", a.Name, a)
		}
		if slices.Contains(a.Tools, "AskUserQuestion") {
			t.Errorf("%s may ask the user", a.Name)
		}
	}

	if got := s.subagents(&store.Task{Agent: "codex"}, p); got != nil {
		t.Errorf("codex got subagents: %+v", got)
	}
	if got := s.subagents(&store.Task{Agent: "claude"}, &store.Project{}); got != nil {
		t.Errorf("subagents off still sent: %+v", got)
	}
}
