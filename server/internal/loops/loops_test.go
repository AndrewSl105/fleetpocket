package loops

import (
	"fmt"
	"strings"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func failed() *Check { return &Check{Command: "npm test", ExitCode: 1, Output: "FAIL x"} }

func TestDecideStopConditions(t *testing.T) {
	pass := &Check{Command: "npm test"}
	cases := []struct {
		name  string
		st    State
		next  bool
		state string
		idle  int
	}{
		{"check passed", State{Iteration: 1, MaxIterations: 3, Changed: true, Check: pass}, false, store.LoopPassed, 0},
		{"passed on the last iteration", State{Iteration: 3, MaxIterations: 3, Check: pass, Idle: 1}, false, store.LoopPassed, 0},
		{"failed, changes: next", State{Iteration: 1, MaxIterations: 3, Changed: true, Check: failed()}, true, "", 0},
		{"failed, first idle: next", State{Iteration: 1, MaxIterations: 3, Check: failed()}, true, "", 1},
		{"two idle in a row", State{Iteration: 2, MaxIterations: 5, Idle: 1, Check: failed()}, false, store.LoopNoChanges, 0},
		{"idle then changes resets", State{Iteration: 2, MaxIterations: 5, Idle: 1, Changed: true, Check: failed()}, true, "", 0},
		{"max iterations", State{Iteration: 3, MaxIterations: 3, Changed: true, Check: failed()}, false, store.LoopMaxIterations, 0},
		{"loop budget", State{Iteration: 1, MaxIterations: 3, Changed: true, Check: failed(), SpentUSD: 2, BudgetUSD: 2}, false, store.LoopBudget, 0},
		{"under budget", State{Iteration: 1, MaxIterations: 3, Changed: true, Check: failed(), SpentUSD: 1, BudgetUSD: 2}, true, "", 0},
		{"limit with budget used", State{Iteration: 1, MaxIterations: 3, LimitHit: true, SpentUSD: 3, BudgetUSD: 2}, false, store.LoopBudget, 0},
		{"limit", State{Iteration: 1, MaxIterations: 3, LimitHit: true, Limit: "max_turns"}, false, store.LoopStopped, 0},
		{"no check result", State{Iteration: 1, MaxIterations: 3, Changed: true}, false, store.LoopStopped, 0},
		{"timed out counts as failed", State{Iteration: 1, MaxIterations: 3, Changed: true, Check: &Check{ExitCode: 0, TimedOut: true}}, true, "", 0},
	}
	for _, c := range cases {
		d := Decide(c.st)
		if d.Next != c.next || d.State != c.state || (c.next && d.Idle != c.idle) || d.Reason == "" {
			t.Errorf("%s: %+v", c.name, d)
		}
	}
}

func TestNormalize(t *testing.T) {
	if c, err := Normalize(nil); c != nil || err != nil {
		t.Fatal("nil")
	}
	c, err := Normalize(&store.LoopConfig{CheckCommand: "  go test ./...  "})
	if err != nil || c.CheckCommand != "go test ./..." || c.MaxIterations != DefaultMaxIterations {
		t.Fatalf("%+v %v", c, err)
	}
	zero := 0.0
	if c, _ := Normalize(&store.LoopConfig{CheckCommand: "x", BudgetUSD: &zero}); c.BudgetUSD != nil {
		t.Fatal("zero budget kept")
	}
	for _, bad := range []store.LoopConfig{{}, {CheckCommand: "x", MaxIterations: 11}, {CheckCommand: "x", MaxIterations: -1},
		{CheckCommand: strings.Repeat("x", 2001)}, {CheckCommand: "x", TimeoutSec: 4000}} {
		if _, err := Normalize(&bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestTrimOutput(t *testing.T) {
	var b strings.Builder
	b.WriteString("\x1b[31mERROR early failure\x1b[0m\n")
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&b, "ok line %d\n", i)
	}
	b.WriteString("--- FAIL: TestX\n")
	got := TrimOutput(b.String())
	if strings.Contains(got, "\x1b[") || !strings.Contains(got, "ERROR early failure") || !strings.Contains(got, "--- FAIL: TestX") ||
		strings.Contains(got, "ok line 10\n") || !strings.Contains(got, "…") {
		t.Fatalf("trim: %q", got[:200])
	}
	huge := strings.Repeat("error: é very long line\n", 10000)
	got = TrimOutput(huge)
	if len(got) > MaxOutputBytes+4 || !strings.HasPrefix(got, "…\n") || !strings.HasSuffix(got, "line\n") {
		t.Fatalf("cap: %d %q", len(got), got[:40])
	}
}

func TestInput(t *testing.T) {
	in := Input(2, 3, Check{Command: "make lint", ExitCode: 2, Output: "lint.go:3: unused\n"})
	for _, want := range []string{"Loop iteration 2 of 3", "`make lint` failed (exit code 2)", "lint.go:3: unused"} {
		if !strings.Contains(in, want) {
			t.Errorf("missing %q in %s", want, in)
		}
	}
	if !strings.Contains(Input(2, 3, Check{Command: "x", TimedOut: true, ExitCode: 124}), "timed out") {
		t.Error("timeout text")
	}
}
