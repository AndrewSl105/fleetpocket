package cifix_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/attachments"
	"github.com/andrewsl105/fleetpocket/server/internal/autofix"
	"github.com/andrewsl105/fleetpocket/server/internal/cifix"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

// fakeGitHub answers CheckRuns and JobLog from maps; it never sees a runner.
type fakeGitHub struct {
	mu     sync.Mutex
	checks map[string][]autofix.Check // by sha
	logs   map[int64]string           // by job id
	calls  int
}

func (f *fakeGitHub) CheckRuns(_ context.Context, _ string, _ int64, sha string) ([]autofix.Check, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.checks[sha], nil
}

func (f *fakeGitHub) JobLog(_ context.Context, _ string, _ int64, id int64) (string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logs[id], []string{"Run go test ./..."}, nil
}

func (f *fakeGitHub) set(sha string, checks ...autofix.Check) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks[sha] = checks
}

func failing(id int64, name string) autofix.Check {
	return autofix.Check{ID: id, Name: name, Status: "completed", Conclusion: "failure", App: "github-actions", URL: "https://github.com/o/r/runs/1"}
}

type fixture struct {
	st    *store.Store
	svc   *tasks.Service
	fixer *cifix.Service
	gh    *fakeGitHub
	id    int64
}

// newFixture: a task whose PR Jungle Monkey opened at commit "sha1", CI
// auto-fix on (2 attempts) for its project.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := testutil.DB(t)
	ctx := context.Background()
	hub := events.NewHub()
	svc := &tasks.Service{Store: st, Hub: hub}
	ps := &projects.Service{Store: st}
	name, budget, on, two := "P", 1.0, true, 2
	if _, err := ps.Create(ctx, projects.CreateInput{ID: "p", ProjectFields: store.ProjectFields{
		Name: &name, MaxBudgetUSD: &budget, CIAutoFix: &store.AutoFixOverrides{Enabled: &on, MaxAttempts: &two},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.AddRepository(ctx, "p", projects.RepoInput{FullName: "o/r", InstallationID: 1}); err != nil {
		t.Fatal(err)
	}
	v, err := svc.Create(ctx, tasks.CreateInput{ProjectID: "p", Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetBranch(ctx, v.ID, "agent/1-x")
	_ = st.SetPR(ctx, v.ID, 9, "https://github.com/o/r/pull/9")
	_ = st.SetPushedSHA(ctx, v.ID, "sha1")
	r, _ := st.PendingRun(ctx, v.ID)
	_ = st.MarkRunStarting(ctx, r.ID)
	for _, s := range []string{tasks.Preparing, tasks.Running, tasks.Pushing, tasks.PROpen} {
		if _, err := svc.SetStatus(ctx, v.ID, s, ""); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	gh := &fakeGitHub{checks: map[string][]autofix.Check{}, logs: map[int64]string{
		1: "setup\n--- FAIL: TestX (0.01s)\n    x_test.go:12: error: want 1, got 2\nFAIL\n",
		2: "lint\nerror: unused variable y\n",
	}}
	fixer := &cifix.Service{Store: st, Hub: hub, GitHub: gh, Files: &attachments.Files{Dir: filepath.Join(t.TempDir(), "att")}, DefaultBudgetUSD: 4}
	svc.AutoFix = fixer
	return &fixture{st: st, svc: svc, fixer: fixer, gh: gh, id: v.ID}
}

func (f *fixture) task(t *testing.T) *store.Task {
	t.Helper()
	tk, err := f.st.GetTask(context.Background(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func (f *fixture) autofixEvents(t *testing.T) []map[string]any {
	t.Helper()
	evs, _ := f.st.LastEvents(context.Background(), f.id, []string{"autofix"}, 50)
	var out []map[string]any
	for _, e := range evs {
		var p map[string]any
		_ = json.Unmarshal(e.Payload, &p)
		out = append(out, p)
	}
	return out
}

func TestFailureStartsAttemptOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.gh.set("sha1", failing(1, "test"), autofix.Check{ID: 3, Name: "build", Status: "completed", Conclusion: "success"})

	// check_suite and workflow_run deliveries of one failure: one attempt.
	for range 2 {
		if err := f.fixer.Evaluate(ctx, f.id, "sha1", true); err != nil {
			t.Fatal(err)
		}
	}
	tk := f.task(t)
	if tk.Status != tasks.Queued || deref(tk.AutoFixState) != store.AutoFixFixing || tk.AutoFixAttempts != 1 || deref(tk.AutoFixMaxAttempts) != 2 {
		t.Fatalf("task %s state %v attempts %d", tk.Status, deref(tk.AutoFixState), tk.AutoFixAttempts)
	}
	run, err := f.st.PendingRun(ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if run.Kind != tasks.RunFollowup || run.AutoFixAttempt == nil || *run.AutoFixAttempt != 1 {
		t.Fatalf("run %+v", run)
	}
	// Half the project's task budget ($1).
	if run.MaxBudgetUSD == nil || *run.MaxBudgetUSD != 0.5 {
		t.Fatalf("budget %v", run.MaxBudgetUSD)
	}
	for _, want := range []string{"CI auto-fix attempt 1/2", "Failing checks: test", "ci-logs-attempt-1.txt", "do not edit CI configuration"} {
		if !strings.Contains(run.Input, want) {
			t.Errorf("instruction lacks %q: %s", want, run.Input)
		}
	}
	// The trimmed log is attached to the attempt's run.
	cur, _, err := f.st.AttachmentsForRun(ctx, f.id, run.ID, false)
	if err != nil || len(cur) != 1 || cur[0].Name != "ci-logs-attempt-1.txt" {
		t.Fatalf("attachments %+v %v", cur, err)
	}
	evs := f.autofixEvents(t)
	if len(evs) != 1 || evs[0]["phase"] != "started" || evs[0]["signature"] == "" || evs[0]["attachments"] == nil {
		t.Fatalf("events %v", evs)
	}
}

func TestHumanPushIsSkippedAndCancels(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.gh.set("human", failing(1, "test"))

	// No loop yet: a failure on someone else's commit does nothing.
	if err := f.fixer.Evaluate(ctx, f.id, "human", true); err != nil {
		t.Fatal(err)
	}
	if tk := f.task(t); tk.Status != tasks.PROpen || tk.AutoFixState != nil || f.gh.calls != 0 {
		t.Fatalf("status %s state %v calls %d", tk.Status, tk.AutoFixState, f.gh.calls)
	}
	// A loop waiting for CI is cancelled by a human push.
	if _, err := f.st.SetAutoFixState(ctx, f.id, nil, store.AutoFixWaiting); err != nil {
		t.Fatal(err)
	}
	if err := f.fixer.Evaluate(ctx, f.id, "human", true); err != nil {
		t.Fatal(err)
	}
	if tk := f.task(t); deref(tk.AutoFixState) != store.AutoFixCancelled || tk.Status != tasks.PROpen {
		t.Fatalf("state %v status %s", deref(tk.AutoFixState), tk.Status)
	}
	if evs := f.autofixEvents(t); len(evs) != 1 || evs[0]["phase"] != "cancelled" {
		t.Fatalf("events %v", evs)
	}
	// Later failures on the system's commit are ignored until it pushes again.
	f.gh.set("sha1", failing(1, "test"))
	_ = f.fixer.Evaluate(ctx, f.id, "sha1", true)
	if _, err := f.st.PendingRun(ctx, f.id); err == nil {
		t.Fatal("a cancelled loop started an attempt")
	}
}

func TestAttemptCap(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.gh.set("sha1", failing(1, "test"))
	_, _ = f.st.Pool.Exec(ctx, `UPDATE tasks SET autofix_state = 'waiting', autofix_attempts = 2, autofix_max_attempts = 2,
		autofix_signature = 'other' WHERE id = $1`, f.id)
	if err := f.fixer.Evaluate(ctx, f.id, "sha1", true); err != nil {
		t.Fatal(err)
	}
	tk := f.task(t)
	if deref(tk.AutoFixState) != store.AutoFixNeedsYou || tk.Status != tasks.PROpen {
		t.Fatalf("state %v status %s", deref(tk.AutoFixState), tk.Status)
	}
	evs := f.autofixEvents(t)
	if len(evs) != 1 || evs[0]["phase"] != "needs_you" || !strings.Contains(evs[0]["reason"].(string), "after 2 fix attempts") {
		t.Fatalf("events %v", evs)
	}
	// The notification links the failing checks' logs.
	if checks, _ := evs[0]["checks"].([]any); len(checks) != 1 {
		t.Fatalf("checks %v", evs[0]["checks"])
	}
}

func TestSameSignatureStops(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.gh.set("sha1", failing(1, "test"))
	sig := autofix.Signature("test", autofix.TrimLog(f.gh.logs[1], autofix.TailLines))
	_, _ = f.st.Pool.Exec(ctx, `UPDATE tasks SET autofix_state = 'waiting', autofix_attempts = 1, autofix_max_attempts = 2,
		autofix_signature = $2 WHERE id = $1`, f.id, sig)
	if err := f.fixer.Evaluate(ctx, f.id, "sha1", true); err != nil {
		t.Fatal(err)
	}
	if tk := f.task(t); deref(tk.AutoFixState) != store.AutoFixNeedsYou {
		t.Fatalf("state %v", deref(tk.AutoFixState))
	}
	evs := f.autofixEvents(t)
	if len(evs) != 1 || !strings.Contains(evs[0]["reason"].(string), "same failure") {
		t.Fatalf("events %v", evs)
	}
}

func TestPendingChecksWaitAndGreenStops(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, _ = f.st.Pool.Exec(ctx, `UPDATE tasks SET autofix_state = 'waiting', autofix_attempts = 1, autofix_max_attempts = 2 WHERE id = $1`, f.id)
	f.gh.set("sha1", failing(1, "test"), autofix.Check{ID: 2, Name: "lint", Status: "in_progress"})
	_ = f.fixer.Evaluate(ctx, f.id, "sha1", true)
	if tk := f.task(t); deref(tk.AutoFixState) != store.AutoFixWaiting || tk.Status != tasks.PROpen {
		t.Fatalf("acted while checks ran: %v %s", deref(tk.AutoFixState), tk.Status)
	}
	f.gh.set("sha1", autofix.Check{ID: 1, Name: "test", Status: "completed", Conclusion: "success"})
	_ = f.fixer.Evaluate(ctx, f.id, "sha1", false)
	if tk := f.task(t); deref(tk.AutoFixState) != store.AutoFixGreen {
		t.Fatalf("state %v", deref(tk.AutoFixState))
	}
	if evs := f.autofixEvents(t); len(evs) != 1 || evs[0]["phase"] != "green" || evs[0]["fixes"] != float64(1) {
		t.Fatalf("events %v", evs)
	}
}

func TestOffByDefaultAndChecksFilter(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.gh.set("sha1", failing(2, "lint"))
	// Only "test" is in scope: a lint failure is left alone.
	checks := []string{"test"}
	off := false
	p, _ := f.st.GetProject(ctx, "p")
	o := p.CIAutoFix
	o.Checks = &checks
	_, _ = f.st.UpdateProject(ctx, "p", store.ProjectFields{CIAutoFix: &o})
	_ = f.fixer.Evaluate(ctx, f.id, "sha1", true)
	if tk := f.task(t); tk.Status != tasks.PROpen || tk.AutoFixState != nil {
		t.Fatalf("out-of-scope check fixed: %s %v", tk.Status, tk.AutoFixState)
	}
	// Off: nothing happens, whatever fails.
	o.Checks, o.Enabled = nil, &off
	_, _ = f.st.UpdateProject(ctx, "p", store.ProjectFields{CIAutoFix: &o})
	f.gh.set("sha1", failing(1, "test"))
	_ = f.fixer.Evaluate(ctx, f.id, "sha1", true)
	if tk := f.task(t); tk.Status != tasks.PROpen || tk.AutoFixState != nil {
		t.Fatalf("auto-fix off but acted: %s", tk.Status)
	}
	// A project without overrides follows the global default.
	_, _ = f.st.UpdateProject(ctx, "p", store.ProjectFields{CIAutoFix: &store.AutoFixOverrides{}})
	on := true
	if err := f.st.SetAutoFixDefaults(ctx, store.AutoFixOverrides{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	_ = f.fixer.Evaluate(ctx, f.id, "sha1", true)
	if tk := f.task(t); tk.Status != tasks.Queued || deref(tk.AutoFixMaxAttempts) != autofix.DefaultMaxAttempts {
		t.Fatalf("global default not applied: %s %v", tk.Status, tk.AutoFixMaxAttempts)
	}
}

func TestManualFollowUpCancels(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.st.SetAutoFixState(ctx, f.id, nil, store.AutoFixWaiting); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Message(ctx, f.id, "Rename the flag instead", nil); err != nil {
		t.Fatal(err)
	}
	tk := f.task(t)
	if deref(tk.AutoFixState) != store.AutoFixCancelled || tk.Status != tasks.Queued {
		t.Fatalf("state %v status %s", deref(tk.AutoFixState), tk.Status)
	}
	if evs := f.autofixEvents(t); len(evs) != 1 || evs[0]["phase"] != "cancelled" || !strings.Contains(evs[0]["reason"].(string), "follow-up") {
		t.Fatalf("events %v", evs)
	}
	// The follow-up run is the owner's, not an attempt.
	if run, _ := f.st.PendingRun(ctx, f.id); run == nil || run.AutoFixAttempt != nil {
		t.Fatalf("run %+v", run)
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
