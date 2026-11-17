package scheduler_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

func ownerGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=o", "-c", "user.email=o@o"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newOwnerRepo is a repository on the owner's disk, on main, with a dirty
// tracked file and an untracked file.
func newOwnerRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "site")
	ownerGit(t, filepath.Dir(dir), "init", "-q", "-b", "main", dir)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("site\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ownerGit(t, dir, "add", "-A")
	ownerGit(t, dir, "commit", "-q", "-m", "init")
	real, _ := filepath.EvalSymlinks(dir)
	return real
}

func (e *env) addLocalRepo(path string) *store.Repository {
	e.t.Helper()
	r, err := e.st.AddRepository(context.Background(), store.Repository{
		ProjectID: "demo", FullName: filepath.Base(path), BaseBranch: "main", Source: store.SourceLocal, Path: path,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.svc.LocalHost = func(t *store.Task) tasks.LocalBranches { return e.sched.Hosts.For(t.Node) }
	return r
}

func (e *env) lastPayload(id int64, typ string) map[string]any {
	e.t.Helper()
	evs, err := e.st.LastEvents(context.Background(), id, []string{typ}, 1)
	if err != nil || len(evs) == 0 {
		e.t.Fatalf("no %s event: %v", typ, err)
	}
	var p map[string]any
	_ = json.Unmarshal(evs[0].Payload, &p)
	return p
}

// A task on a local repository: copy with the uncommitted changes, run,
// deliver jm/<id>-<slug> without touching the owner's tree, follow up
// (fast-forward), refuse to merge into a dirty tree, then merge.
func TestLocalRepoTaskToBranchReadyAndMerge(t *testing.T) {
	e := newEnv(t, time.Minute, nil)
	ctx := context.Background()
	repo := newOwnerRepo(t)
	r := e.addLocalRepo(repo)
	// Uncommitted work the task starts from.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("site\nwip line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "draft.txt"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	statusBefore := ownerGit(t, repo, "status", "--porcelain")

	v, err := e.svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", RepositoryID: r.ID, Prompt: "Say hello", StartFrom: workspace.StartWorkingTree, Source: "api"})
	if err != nil {
		t.Fatal(err)
	}
	e.answerOpen(v.ID, "hello")
	done := e.waitStatus(v.ID, tasks.BranchReady, time.Minute)
	want := "jm/" + itoa(v.ID) + "-say-hello"
	if done.LocalBranch == nil || *done.LocalBranch != want {
		t.Fatalf("local branch %v, want %s", done.LocalBranch, want)
	}
	// The owner's tree is as it was; the branch has the WIP commit and the agent's commit.
	if ownerGit(t, repo, "symbolic-ref", "--short", "HEAD") != "main" || ownerGit(t, repo, "status", "--porcelain") != statusBefore {
		t.Fatal("delivery touched the owner's working tree")
	}
	log := ownerGit(t, repo, "log", "--format=%s", "main.."+want)
	if !strings.Contains(log, workspace.WIPMessage) || !strings.Contains(log, "Say hello") {
		t.Fatalf("branch log:\n%s", log)
	}
	if got := ownerGit(t, repo, "show", want+":draft.txt"); got != "draft" {
		t.Fatalf("untracked file on the branch: %q", got)
	}
	p := e.lastPayload(v.ID, "branch_ready")
	if p["branch"] != want || p["commits"] != float64(2) || p["updated"] != false || !strings.Contains(p["diffstat"].(string), "changed") {
		t.Fatalf("branch_ready payload %v", p)
	}

	// A follow-up continues the task branch and fast-forwards jm/....
	if _, err := e.svc.Message(ctx, v.ID, "And add a footer", nil); err != nil {
		t.Fatal(err)
	}
	e.answerOpen(v.ID, "hello")
	e.waitStatus(v.ID, tasks.BranchReady, time.Minute)
	if p := e.lastPayload(v.ID, "branch_ready"); p["branch"] != want || p["updated"] != true || p["commits"] != float64(3) {
		t.Fatalf("follow-up payload %v", p)
	}

	// Merge refuses the dirty tree, then works once it is clean.
	if _, err := e.svc.MergeBranch(ctx, v.ID); !errors.Is(err, workspace.ErrDirty) {
		t.Fatalf("merge into a dirty tree: %v", err)
	}
	ownerGit(t, repo, "checkout", "-q", "--", "README.md")
	// The untracked draft is on the branch too (WIP commit): git refuses to
	// overwrite it and nothing changes.
	if _, err := e.svc.MergeBranch(ctx, v.ID); !errors.Is(err, workspace.ErrGitRefused) || !strings.Contains(err.Error(), "draft.txt") {
		t.Fatalf("merge over an untracked file: %v", err)
	}
	if err := os.Remove(filepath.Join(repo, "draft.txt")); err != nil {
		t.Fatal(err)
	}
	merged, err := e.svc.MergeBranch(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Status != tasks.Completed || !strings.Contains(deref(merged.StatusReason), "merged into main") {
		t.Fatalf("after merge: %s %q", merged.Status, deref(merged.StatusReason))
	}
	if ownerGit(t, repo, "rev-parse", "main") != ownerGit(t, repo, "rev-parse", want) {
		t.Fatal("main was not fast-forwarded to the task branch")
	}
	if p := e.lastPayload(v.ID, "branch_action"); p["action"] != "merged" || p["fast_forward"] != true || p["into"] != "main" {
		t.Fatalf("branch_action %v", p)
	}
}

func TestLocalRepoDiscardAndPushWithoutGitHub(t *testing.T) {
	e := newEnv(t, time.Minute, nil)
	ctx := context.Background()
	repo := newOwnerRepo(t)
	r := e.addLocalRepo(repo)
	v, err := e.svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", RepositoryID: r.ID, Prompt: "Tidy up", Source: "api"})
	if err != nil {
		t.Fatal(err)
	}
	e.answerOpen(v.ID, "hello")
	done := e.waitStatus(v.ID, tasks.BranchReady, time.Minute)
	branch := *done.LocalBranch

	// No GitHub remote: Push & open PR is refused.
	if _, err := e.svc.PushBranch(ctx, v.ID); !errors.Is(err, tasks.ErrNoGitHubRemote) {
		t.Fatalf("push without a GitHub remote: %v", err)
	}
	// Check out, which makes Discard refuse; back on main it deletes the branch.
	if _, err := e.svc.CheckoutBranch(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DiscardBranch(ctx, v.ID); !errors.Is(err, workspace.ErrCurrentBranch) {
		t.Fatalf("discard the checked-out branch: %v", err)
	}
	ownerGit(t, repo, "checkout", "-q", "main")
	out, err := e.svc.DiscardBranch(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != tasks.Completed || out.LocalBranch != nil {
		t.Fatalf("after discard: %s %v", out.Status, out.LocalBranch)
	}
	if b := ownerGit(t, repo, "branch", "--list", branch); b != "" {
		t.Fatalf("branch still there: %s", b)
	}
	// Starting from the working tree is only for local repositories.
	if _, err := e.svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", Prompt: "x", StartFrom: workspace.StartWorkingTree}); !errors.Is(err, tasks.ErrInvalid) {
		t.Fatalf("working_tree on a GitHub repository: %v", err)
	}
}

// A moved folder: the delivery waits in pushing until the folder is back.
func TestLocalRepoFolderMissingWaits(t *testing.T) {
	e := newEnv(t, time.Minute, nil)
	ctx := context.Background()
	repo := newOwnerRepo(t)
	r := e.addLocalRepo(repo)
	v, err := e.svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", RepositoryID: r.ID, Prompt: "Move me", Source: "api"})
	if err != nil {
		t.Fatal(err)
	}
	e.answerOpen(v.ID, "hello")
	moved := repo + "-moved"
	if err := os.Rename(repo, moved); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		tk, _ := e.st.GetTask(ctx, v.ID)
		if tk.Status == tasks.Pushing && strings.HasPrefix(deref(tk.StatusReason), "repository folder not found") {
			break
		}
		if time.Now().After(deadline) || tasks.IsTerminal(tk.Status) {
			t.Fatalf("status %s %q", tk.Status, deref(tk.StatusReason))
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := e.st.UpdateRepository(ctx, "demo", r.ID, store.RepoUpdate{Path: &moved}); err != nil {
		t.Fatal(err)
	}
	e.sched.ResumeDeliveries(ctx, r.ID)
	done := e.waitStatus(v.ID, tasks.BranchReady, time.Minute)
	if b := ownerGit(t, moved, "branch", "--list", *done.LocalBranch); b == "" {
		t.Fatal("not delivered into the located folder")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
