package cifix_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/attachments"
	"github.com/andrewsl105/fleetpocket/server/internal/autofix"
	"github.com/andrewsl105/fleetpocket/server/internal/cifix"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/githubapp"
	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/scheduler"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// pushPublisher pushes to the test's bare repository and pretends to open PR #5.
type pushPublisher struct{}

func (pushPublisher) Publish(ctx context.Context, in scheduler.PublishInput) (*scheduler.PublishResult, error) {
	repo := workspace.Repo{FullName: in.Repo.FullName, InstallationID: in.Repo.InstallationID, BaseBranch: in.Repo.BaseBranch}
	if err := in.Host.Push(ctx, in.Task.ID, repo, in.Branch); err != nil {
		return nil, err
	}
	return &scheduler.PublishResult{PRNumber: 5, PRURL: "https://github.com/o/demo/pull/5", Created: in.Task.PRNumber == nil}, nil
}

func runnerImage() string {
	if img := os.Getenv("FLEETPOCKET_TEST_RUNNER_IMAGE"); img != "" {
		return img
	}
	return "fleetpocket-agent:dev"
}

func bareRepo(t *testing.T) string {
	dir := t.TempDir()
	bare, work := filepath.Join(dir, "remote.git"), filepath.Join(dir, "seed")
	for _, args := range [][]string{
		{"init", "-q", "--bare", "-b", "dev", bare}, {"init", "-q", "-b", "dev", work},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	_ = os.WriteFile(filepath.Join(work, "README.md"), []byte("hi\n"), 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init"}, {"push", "-q", bare, "dev"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	return bare
}

// TestWebhookToGreenPR drives the whole loop with the fake runner: a failed
// check_suite webhook starts attempt 1, which is pushed; the next failure
// starts attempt 2, whose diff skips a test, so it waits for the owner;
// "Push anyway" pushes it and a passing suite ends the loop green.
func TestWebhookToGreenPR(t *testing.T) {
	st := testutil.DB(t)
	dc, err := runnermgr.NewDockerClient("")
	if err != nil {
		t.Skipf("docker client: %v", err)
	}
	docker := &runnermgr.Manager{Docker: dc, SidecarMemory: "256m"}
	ctx := context.Background()
	if err := docker.Ping(ctx); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	if err := docker.HasImage(ctx, runnerImage()); err != nil {
		t.Skipf("runner image missing: %v", err)
	}
	bare := bareRepo(t)
	ws := &workspace.Manager{Dir: filepath.Join(t.TempDir(), "workspaces"), RemoteURL: func(string) string { return bare }}
	cfg := &config.Config{
		RunnerImage: runnerImage(), RunnerFake: true, MaxConcurrentTasks: 2, RunnerMemory: "512m", RunnerCPUs: 1,
		DefaultMaxTurns: 10, DefaultMaxBudgetUSD: 1, DefaultMaxDuration: 2 * time.Minute, QuestionTimeout: time.Minute,
		RunnerUser: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
	}
	hub := events.NewHub()
	files := &attachments.Files{Dir: filepath.Join(t.TempDir(), "attachments")}
	svc := &tasks.Service{Store: st, Hub: hub, Files: files}
	local := &hosts.Local{Docker: docker, Workspace: ws, AttachmentsDir: files.TaskDir}
	sched := scheduler.New(&scheduler.Scheduler{Cfg: cfg, Store: st, Hub: hub, Tasks: svc, Hosts: &hosts.Registry{Local: local}, Files: files, Publisher: pushPublisher{}})
	svc.Executor = sched
	gh := &fakeGitHub{checks: map[string][]autofix.Check{}, logs: map[int64]string{
		1: "--- FAIL: TestGreeting (0.00s)\n    greet_test.go:9: error: want hello, got hi\n",
		2: "lint\nerror: line too long in FLEETPOCKET_FAKE.md\n",
	}}
	fixer := &cifix.Service{Store: st, Hub: hub, GitHub: gh, Files: files, Wake: sched.Wake, Holder: sched, DefaultBudgetUSD: 1}
	svc.AutoFix = fixer
	hook := &githubapp.Webhook{Secret: []byte("whsec"), Store: st, Hub: hub, Tasks: svc,
		OnCIFailed: fixer.Failed, OnCI: fixer.Changed, OnPRClosed: fixer.PRClosed}

	ps := &projects.Service{Store: st}
	name, on, two := "Demo", true, 2
	if _, err := ps.Create(ctx, projects.CreateInput{ID: "demo", ProjectFields: store.ProjectFields{
		Name: &name, CIAutoFix: &store.AutoFixOverrides{Enabled: &on, MaxAttempts: &two},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.AddRepository(ctx, "demo", projects.RepoInput{FullName: "o/demo", InstallationID: 1, BaseBranch: "dev"}); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = sched.Run(runCtx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		fixer.Wait()
		all, _ := st.ListTasks(context.Background(), store.TaskFilter{Limit: 10})
		for _, tk := range all {
			_ = docker.RemoveAll(context.Background(), tk.ID)
			_ = docker.RemoveVolume(context.Background(), tk.ID)
		}
	})

	wait := func(id int64, cond func(*store.Task) bool, what string) *store.Task {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Minute); ; time.Sleep(200 * time.Millisecond) {
			tk, err := st.GetTask(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if cond(tk) {
				return tk
			}
			if time.Now().After(deadline) || tk.Status == tasks.Failed {
				t.Fatalf("waiting for %s: status %s (%v), autofix %v", what, tk.Status, deref(tk.StatusReason), deref(tk.AutoFixState))
			}
		}
	}
	status := func(s string) func(*store.Task) bool { return func(tk *store.Task) bool { return tk.Status == s } }
	answer := func(id int64, text string) {
		t.Helper()
		v, err := svc.Get(ctx, id)
		if err != nil || v.OpenQuestion == nil {
			t.Fatalf("no open question: %v", err)
		}
		if _, err := svc.Answer(ctx, id, v.OpenQuestion.ID, text, "web"); err != nil {
			t.Fatal(err)
		}
	}
	webhook := func(branch, sha, action, conclusion string) {
		t.Helper()
		status := "completed"
		if action == "requested" {
			status = "queued"
		}
		body := fmt.Sprintf(`{"action":%q,"check_suite":{"head_branch":%q,"head_sha":%q,"status":%q,"conclusion":%q,"app":{"name":"GitHub Actions"}},"repository":{"full_name":"o/demo"}}`,
			action, branch, sha, status, conclusion)
		m := hmac.New(sha256.New, []byte("whsec"))
		m.Write([]byte(body))
		req := httptest.NewRequest("POST", "/webhooks/github", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", "check_suite")
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(m.Sum(nil)))
		rec := httptest.NewRecorder()
		hook.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("webhook: %d", rec.Code)
		}
		fixer.Wait()
	}

	v, err := svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", Prompt: "Say hello", Source: "api"})
	if err != nil {
		t.Fatal(err)
	}
	wait(v.ID, status(tasks.WaitingInput), "first question")
	answer(v.ID, "hi")
	tk := wait(v.ID, status(tasks.PROpen), "PR")
	sha1 := deref(tk.LastPushedSHA)
	if sha1 == "" || tk.PRNumber == nil {
		t.Fatalf("pushed sha %q pr %v", sha1, tk.PRNumber)
	}

	// 1. CI fails on the pushed commit: attempt 1 runs with the logs attached.
	gh.set(sha1, failing(1, "test"))
	webhook(*tk.Branch, sha1, "completed", "failure")
	wait(v.ID, status(tasks.WaitingInput), "attempt 1")
	view, _ := svc.Get(ctx, v.ID)
	if view.LatestRun.AutoFixAttempt == nil || *view.LatestRun.AutoFixAttempt != 1 || !strings.Contains(view.LatestRun.Input, "CI auto-fix attempt 1/2") {
		t.Fatalf("attempt run %+v", view.LatestRun)
	}
	answer(v.ID, "hello")
	tk = wait(v.ID, func(tk *store.Task) bool {
		return tk.Status == tasks.PROpen && deref(tk.AutoFixState) == store.AutoFixWaiting
	}, "attempt 1 pushed")
	sha2 := deref(tk.LastPushedSHA)
	if sha2 == sha1 {
		t.Fatal("pushed commit not updated")
	}
	// The agent got the trimmed log at /attachments (the fake writes its prompt).
	b, _ := os.ReadFile(filepath.Join(ws.Path(v.ID), "FLEETPOCKET_FAKE.md"))
	if !strings.Contains(string(b), "/attachments/") || !strings.Contains(string(b), "ci-logs-attempt-1.txt") {
		t.Fatalf("prompt without the log attachment: %s", b)
	}

	// 2. Another failure: attempt 2 skips a test, so the push waits for the owner.
	webhook(*tk.Branch, sha2, "requested", "")
	gh.set(sha2, failing(2, "lint"))
	webhook(*tk.Branch, sha2, "completed", "failure")
	wait(v.ID, status(tasks.WaitingInput), "attempt 2")
	if err := os.WriteFile(filepath.Join(ws.Path(v.ID), "greet_test.go"),
		[]byte("package greet\n\nimport \"testing\"\n\nfunc TestGreeting(t *testing.T) {\n\tt.Skip(\"flaky\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	answer(v.ID, "hello")
	tk = wait(v.ID, func(tk *store.Task) bool { return deref(tk.AutoFixState) == store.AutoFixHeld }, "held fix")
	if tk.Status != tasks.PROpen || deref(tk.LastPushedSHA) != sha2 {
		t.Fatalf("held fix: status %s pushed %s", tk.Status, deref(tk.LastPushedSHA))
	}
	view, _ = svc.Get(ctx, v.ID)
	if view.OpenQuestion == nil || !strings.HasPrefix(view.OpenQuestion.Text, scheduler.GuardQuestionText) ||
		len(view.OpenQuestion.Options) != 2 || !strings.Contains(view.OpenQuestion.Text, "skips a test in greet_test.go") {
		t.Fatalf("guard question %+v", view.OpenQuestion)
	}
	if out, _ := exec.Command("git", "--git-dir", bare, "log", "--format=%s", *tk.Branch).Output(); strings.Count(string(out), "\n") != 3 {
		t.Fatalf("held fix reached the remote:\n%s", out)
	}
	answer(v.ID, scheduler.GuardPush)
	tk = wait(v.ID, func(tk *store.Task) bool {
		return tk.Status == tasks.PROpen && deref(tk.AutoFixState) == store.AutoFixWaiting
	}, "approved fix pushed")
	sha3 := deref(tk.LastPushedSHA)

	// 3. All checks pass on the fix: green after 2 fixes.
	gh.set(sha3, autofix.Check{ID: 1, Name: "test", Status: "completed", Conclusion: "success"},
		autofix.Check{ID: 2, Name: "lint", Status: "completed", Conclusion: "success"})
	webhook(*tk.Branch, sha3, "requested", "")
	webhook(*tk.Branch, sha3, "completed", "success")
	tk = wait(v.ID, func(tk *store.Task) bool { return deref(tk.AutoFixState) == store.AutoFixGreen }, "green")
	if tk.AutoFixAttempts != 2 {
		t.Fatalf("attempts %d", tk.AutoFixAttempts)
	}
	var phases []string
	for _, e := range (&fixture{st: st, id: v.ID}).autofixEvents(t) {
		phases = append(phases, e["phase"].(string))
	}
	if got := strings.Join(phases, ","); got != "started,pushed,started,held,pushed,green" {
		t.Fatalf("phases %s", got)
	}
}
