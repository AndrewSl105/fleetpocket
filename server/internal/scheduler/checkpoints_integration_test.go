package scheduler_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/scheduler"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// pushPublisher pushes to the test remote like githubapp.Publisher (plain
// or forced with a lease) and pretends PR #1 exists.
type pushPublisher struct {
	mu     sync.Mutex
	forced []bool
}

func (p *pushPublisher) Publish(ctx context.Context, in scheduler.PublishInput) (*scheduler.PublishResult, error) {
	repo := workspace.Repo{FullName: in.Repo.FullName, InstallationID: in.Repo.InstallationID, BaseBranch: in.Repo.BaseBranch}
	p.mu.Lock()
	p.forced = append(p.forced, in.Force)
	p.mu.Unlock()
	res := &scheduler.PublishResult{PRNumber: 1, PRURL: "https://example.test/pull/1", Created: in.Task.PRNumber == nil}
	if in.Force {
		old, err := in.Host.ForcePush(ctx, in.Task.ID, repo, in.Branch)
		res.ForcedFrom = old
		return res, err
	}
	return res, in.Host.Push(ctx, in.Task.ID, repo, in.Branch)
}

func (e *env) answerOpen(id int64, text string) {
	e.t.Helper()
	e.waitStatus(id, tasks.WaitingInput, 2*time.Minute)
	view, _ := e.svc.Get(context.Background(), id)
	if _, err := e.svc.Answer(context.Background(), id, view.OpenQuestion.ID, text, "web"); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) payloads(id int64, typ string) []map[string]any {
	evs, _ := e.st.ListEvents(context.Background(), id, 0, 1000)
	var out []map[string]any
	for _, ev := range evs {
		if ev.Type == typ {
			var p map[string]any
			_ = json.Unmarshal(ev.Payload, &p)
			out = append(out, p)
		}
	}
	return out
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// First run with checkpoints on, then a restore to the checkpoint before the
// question: the workspace is reset, the run continues and the pushed branch
// is rewritten with a lease.
func runToPR(t *testing.T, agent string) (*env, *pushPublisher, int64, []*store.Checkpoint) {
	e := newEnv(t, time.Minute, nil)
	pub := &pushPublisher{}
	e.sched.Publisher = pub
	ctx := context.Background()
	if err := e.st.SetCheckpointSettings(ctx, store.CheckpointSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", Prompt: "Say hello", Agent: agent, Source: "api"})
	if err != nil {
		t.Fatal(err)
	}
	e.answerOpen(v.ID, "hello")
	e.waitStatus(v.ID, tasks.PROpen, time.Minute)
	cps, _ := e.st.ListCheckpoints(ctx, v.ID)
	if len(cps) != 2 || cps[0].Reason != "question" || cps[1].Reason != "turn" || cps[0].EventID == nil || cps[1].FilesChanged != 1 {
		t.Fatalf("checkpoints: %+v", cps)
	}
	ws := e.ws.Path(v.ID)
	if refs := git(t, ws, "for-each-ref", "--format=%(refname)", "refs/jm/"); strings.Count(refs, "refs/jm/checkpoints/") != 2 {
		t.Fatalf("refs: %s", refs)
	}
	if refs := git(t, e.bare, "for-each-ref", "--format=%(refname)"); strings.Contains(refs, "refs/jm") {
		t.Fatalf("checkpoint refs reached the remote: %s", refs)
	}
	return e, pub, v.ID, cps
}

func TestRestoreClaudeForksAndForcePushes(t *testing.T) {
	e, pub, id, cps := runToPR(t, "")
	ctx := context.Background()
	t0, _ := e.st.GetTask(ctx, id)
	branch := *t0.Branch
	before := git(t, e.bare, "rev-parse", "refs/heads/"+branch)
	firstSession := *t0.SessionID

	prev, err := e.svc.PreviewCheckpoint(ctx, id, cps[0].ID)
	if err != nil || !prev.Available || prev.Mode != tasks.RestoreFork {
		t.Fatalf("preview: %+v %v", prev, err)
	}
	if _, err := e.svc.Restore(ctx, id, cps[0].ID, "write it in French", "web"); err != nil {
		t.Fatal(err)
	}
	e.answerOpen(id, "bonjour")
	e.waitStatus(id, tasks.PROpen, time.Minute)

	// The fake runner forked the first session at the checkpoint's message.
	texts := e.payloads(id, "assistant_text")
	ref := cps[0].SessionRef
	at := ref[strings.Index(ref, "/")+1:]
	found := false
	for _, p := range texts {
		if p["text"] == "Forked session "+firstSession+" at "+at {
			found = true
		}
	}
	if !found {
		t.Fatalf("no fork echo for %s in %v", ref, texts)
	}
	tk, _ := e.st.GetTask(ctx, id)
	if !strings.HasPrefix(*tk.SessionID, "fake-fork-") {
		t.Fatalf("session %s", *tk.SessionID)
	}
	// Forced push with a lease, recorded in the timeline.
	forced := e.payloads(id, "branch_force_pushed")
	if len(forced) != 1 || forced[0]["from"] != before || forced[0]["branch"] != branch {
		t.Fatalf("branch_force_pushed: %v (was %s)", forced, before)
	}
	pub.mu.Lock()
	if len(pub.forced) != 2 || pub.forced[0] || !pub.forced[1] {
		t.Fatalf("publishes: %v", pub.forced)
	}
	pub.mu.Unlock()
	after := git(t, e.bare, "rev-parse", "refs/heads/"+branch)
	if out := git(t, e.bare, "show", after+":FLEETPOCKET_FAKE.md"); !strings.HasPrefix(out, "# bonjour") {
		t.Fatalf("remote file: %q", out)
	}
	// The old commit is gone from the branch, and no checkpoint commit is in it.
	for _, sha := range []string{before, cps[0].RefSHA, cps[1].RefSHA} {
		if exec.Command("git", "-C", e.bare, "merge-base", "--is-ancestor", sha, after).Run() == nil {
			t.Fatalf("%s is still in the branch history", sha)
		}
	}
	if git(t, e.bare, "rev-list", "--count", "dev.."+branch) != "1" {
		t.Fatal("the branch should hold just the restore run's commit")
	}
	// The restore and the discarded steps stay in the timeline.
	rest := e.payloads(id, "checkpoint_restored")
	if len(rest) != 1 || rest[0]["mode"] != "fork" || rest[0]["instruction"] != "write it in French" {
		t.Fatalf("checkpoint_restored: %v", rest)
	}
	if len(e.payloads(id, "committed")) != 2 {
		t.Fatal("the discarded run's commit event must stay in the timeline")
	}
}

func TestRestoreCodexStartsFreshWithSummary(t *testing.T) {
	e, _, id, cps := runToPR(t, "codex")
	ctx := context.Background()
	if _, err := e.svc.Restore(ctx, id, cps[0].ID, "", "web"); err != nil {
		t.Fatal(err)
	}
	e.answerOpen(id, "hi")
	e.waitStatus(id, tasks.PROpen, time.Minute)
	rest := e.payloads(id, "checkpoint_restored")
	if len(rest) != 1 || rest[0]["mode"] != "summary" {
		t.Fatalf("checkpoint_restored: %v", rest)
	}
	texts := e.payloads(id, "assistant_text")
	last := ""
	for _, p := range texts {
		if s, _ := p["text"].(string); strings.HasPrefix(s, "Working on: ") {
			last = s
		}
		if s, _ := p["text"].(string); strings.HasPrefix(s, "Forked session") {
			t.Fatalf("codex must not fork: %s", s)
		}
	}
	if !strings.Contains(last, "You are continuing task #") {
		t.Fatalf("the new session did not get the summary: %q", last)
	}
	tk, _ := e.st.GetTask(ctx, id)
	if strings.HasPrefix(*tk.SessionID, "fake-fork-") {
		t.Fatal("new session expected")
	}
}
