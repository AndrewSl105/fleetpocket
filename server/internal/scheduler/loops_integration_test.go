package scheduler_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

// answerIteration answers the fake runner's question of loop iteration n.
func (e *env) answerIteration(id int64, n int, text string) {
	e.t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		view, err := e.svc.Get(context.Background(), id)
		if err != nil {
			e.t.Fatal(err)
		}
		if view.Status == tasks.WaitingInput && view.LoopIteration == n && view.OpenQuestion != nil {
			if _, err := e.svc.Answer(context.Background(), id, view.OpenQuestion.ID, text, "web"); err != nil {
				e.t.Fatal(err)
			}
			return
		}
		if tasks.IsTerminal(view.Status) || time.Now().After(deadline) {
			e.t.Fatalf("task %d: status %s iteration %d, want iteration %d waiting", id, view.Status, view.LoopIteration, n)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// The check fails in iteration 1 (the fake agent writes its prompt to
// FLEETPOCKET_FAKE.md; only iteration 2's prompt names it) and passes in
// iteration 2: two runs, one push at the end, an iteration checkpoint each.
func TestLoopUntilCheckPasses(t *testing.T) {
	e := newEnv(t, time.Minute, nil)
	ctx := context.Background()
	if err := e.st.SetCheckpointSettings(ctx, store.CheckpointSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", Prompt: "Make the check pass", Source: "api",
		Loop: &store.LoopConfig{CheckCommand: "echo checking; grep -q 'Loop iteration 2' FLEETPOCKET_FAKE.md || { echo 'FAIL: not yet'; exit 1; }", MaxIterations: 3}})
	if err != nil {
		t.Fatal(err)
	}
	e.answerIteration(v.ID, 1, "hello")
	e.answerIteration(v.ID, 2, "hi")
	done := e.waitStatus(v.ID, tasks.Completed, 2*time.Minute)
	if deref(done.LoopState) != store.LoopPassed || done.LoopIteration != 2 {
		t.Fatalf("loop: state %s iteration %d", deref(done.LoopState), done.LoopIteration)
	}
	runs, _ := e.st.ListRuns(ctx, v.ID, 0, 100)
	if len(runs) != 2 || runs[0].Kind != tasks.RunInitial || runs[1].Kind != tasks.RunLoop ||
		*runs[0].LoopIteration != 1 || *runs[1].LoopIteration != 2 {
		t.Fatalf("runs: %+v", runs)
	}
	if !strings.Contains(runs[1].Input, "Loop iteration 2 of 3") || !strings.Contains(runs[1].Input, "FAIL: not yet") {
		t.Fatalf("iteration 2 input: %s", runs[1].Input)
	}
	checks := e.payloads(v.ID, "check")
	if len(checks) != 2 || checks[0]["exit_code"] != 1.0 || checks[1]["exit_code"] != 0.0 ||
		checks[0]["iteration"] != 1.0 || checks[1]["iteration"] != 2.0 || checks[1]["max_iterations"] != 3.0 {
		t.Fatalf("checks: %+v", checks)
	}
	loopsEv := e.payloads(v.ID, "loop")
	if len(loopsEv) != 3 || loopsEv[0]["phase"] != "iteration" || loopsEv[1]["iteration"] != 2.0 ||
		loopsEv[2]["phase"] != "stopped" || loopsEv[2]["state"] != store.LoopPassed {
		t.Fatalf("loop events: %+v", loopsEv)
	}
	iter := 0
	for _, cp := range e.payloads(v.ID, "checkpoint") {
		if cp["reason"] == "iteration" {
			iter++
		}
	}
	if iter != 2 {
		t.Fatalf("iteration checkpoints: %d", iter)
	}
	// One commit per iteration, both on the branch; nothing was pushed in between
	// (no publisher here: completed "committed locally" once, at the end).
	if n := git(t, e.ws.Path(v.ID), "rev-list", "--count", "dev..HEAD"); n != "2" {
		t.Fatalf("commits: %s", n)
	}
	statuses := 0
	for _, s := range e.payloads(v.ID, "status") {
		if s["status"] == tasks.Completed {
			statuses++
		}
	}
	if statuses != 1 {
		t.Fatalf("completed %d times", statuses)
	}
}
