package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=a", "-c", "user.email=a@a"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// checkpoint does what the runner does: the working tree into a temporary
// index, commit-tree on HEAD, update-ref. HEAD and the index never move.
func checkpoint(t *testing.T, ws, ref string) string {
	t.Helper()
	idx := filepath.Join(t.TempDir(), "index")
	env := []string{"GIT_INDEX_FILE=" + idx}
	gitT(t, ws, env, "read-tree", "HEAD")
	gitT(t, ws, env, "add", "-A")
	tree := gitT(t, ws, env, "write-tree")
	sha := gitT(t, ws, nil, "commit-tree", tree, "-p", "HEAD", "-m", "checkpoint")
	gitT(t, ws, nil, "update-ref", ref, sha)
	return sha
}

func commitAll(t *testing.T, ws, msg string) {
	t.Helper()
	gitT(t, ws, nil, "add", "-A")
	gitT(t, ws, nil, "commit", "-q", "-m", msg)
}

func write(t *testing.T, ws, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setup: a task branch pushed with run 1's commit; run 2 took checkpoint 1
// after editing a.txt, then went on (b.txt, c.txt untracked), committed and
// pushed.
func setupRestore(t *testing.T) (*Manager, Repo, string, string, string, string) {
	ctx := context.Background()
	bare := newBareRepo(t, "dev")
	m := &Manager{Dir: t.TempDir(), RemoteURL: func(string) string { return bare }}
	repo := Repo{FullName: "o/r", BaseBranch: "dev"}
	branch := Branch(1, "Do it")
	if err := m.Prepare(ctx, 1, repo, branch); err != nil {
		t.Fatal(err)
	}
	ws := m.Path(1)
	write(t, ws, "a.txt", "run1\n")
	commitAll(t, ws, "run 1")
	if err := m.Push(ctx, 1, repo, branch); err != nil {
		t.Fatal(err)
	}
	run1 := gitT(t, ws, nil, "rev-parse", "HEAD")
	write(t, ws, "a.txt", "checkpoint\n")
	ref := "refs/jm/checkpoints/2/1"
	sha := checkpoint(t, ws, ref)
	write(t, ws, "a.txt", "later\n")
	write(t, ws, "b.txt", "later\n")
	commitAll(t, ws, "run 2")
	if err := m.Push(ctx, 1, repo, branch); err != nil {
		t.Fatal(err)
	}
	write(t, ws, "c.txt", "untracked\n")
	return m, repo, branch, ref, sha, run1
}

func TestRestoreCheckpointResetsFiles(t *testing.T) {
	ctx := context.Background()
	m, _, _, ref, sha, run1 := setupRestore(t)
	ws := m.Path(1)

	d, _, err := m.CheckpointDiff(ctx, 1, ref, sha, 1<<20)
	if err != nil || !strings.Contains(d, "+later") || !strings.Contains(d, "b.txt") {
		t.Fatalf("diff %q %v", d, err)
	}
	if err := m.RestoreCheckpoint(ctx, 1, ref, sha); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(got) != "checkpoint\n" {
		t.Fatalf("a.txt %q", got)
	}
	for _, f := range []string{"b.txt", "c.txt"} {
		if _, err := os.Stat(filepath.Join(ws, f)); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone", f)
		}
	}
	if head := gitT(t, ws, nil, "rev-parse", "HEAD"); head != run1 {
		t.Fatalf("HEAD %s, want the checkpoint's parent %s", head, run1)
	}
	// The next commit carries the checkpoint's tree, not the checkpoint commit.
	commitAll(t, ws, "restore run")
	if out := gitT(t, ws, nil, "diff", sha, "HEAD"); out != "" {
		t.Fatalf("tree differs: %s", out)
	}
	cmd := exec.Command("git", "merge-base", "--is-ancestor", sha, "HEAD")
	cmd.Dir = ws
	if cmd.Run() == nil {
		t.Fatal("checkpoint commit is in the branch history")
	}
	// A wrong or missing ref is gone.
	if err := m.RestoreCheckpoint(ctx, 1, ref, "0000000000000000000000000000000000000000"); !errors.Is(err, ErrCheckpointGone) {
		t.Fatalf("stale sha: %v", err)
	}
	if _, _, err := m.CheckpointDiff(ctx, 1, "refs/jm/checkpoints/9/9", sha, 10); !errors.Is(err, ErrCheckpointGone) {
		t.Fatalf("missing ref: %v", err)
	}
}

func TestForcePushAfterRestoreOnlyOnAgentBranches(t *testing.T) {
	ctx := context.Background()
	m, repo, branch, ref, sha, _ := setupRestore(t)
	ws := m.Path(1)
	if err := m.RestoreCheckpoint(ctx, 1, ref, sha); err != nil {
		t.Fatal(err)
	}
	commitAll(t, ws, "restore run")
	if d, _ := m.Diverged(ctx, 1, branch); !d {
		t.Fatal("restored branch should have diverged")
	}
	// A plain push is refused (non-fast-forward).
	if err := m.Push(ctx, 1, repo, branch); err == nil {
		t.Fatal("plain push of a rewritten branch must fail")
	}
	// Not ours: refused before anything is sent.
	if _, err := m.ForcePush(ctx, 1, repo, "feature/x"); !errors.Is(err, ErrNotAgentBranch) {
		t.Fatalf("foreign branch: %v", err)
	}
	if _, err := m.ForcePush(ctx, 1, repo, "agent/12-other-task"); !errors.Is(err, ErrNotAgentBranch) {
		t.Fatalf("another task's branch: %v", err)
	}
	old, err := m.ForcePush(ctx, 1, repo, branch)
	if err != nil || old == "" {
		t.Fatalf("force push: %q %v", old, err)
	}
	if d, _ := m.Diverged(ctx, 1, branch); d {
		t.Fatal("still diverged after the force push")
	}
	// The remote never receives checkpoint refs, and the branch history
	// does not contain the checkpoint commit.
	bare := m.RemoteURL("")
	if refs := gitT(t, bare, nil, "for-each-ref", "--format=%(refname)"); strings.Contains(refs, "refs/jm") {
		t.Fatalf("remote has checkpoint refs: %s", refs)
	}
	cmd := exec.Command("git", "merge-base", "--is-ancestor", sha, "refs/heads/"+branch)
	cmd.Dir = bare
	if cmd.Run() == nil {
		t.Fatal("checkpoint commit reached the remote branch")
	}
}

func TestForcePushLeaseFailsWhenRemoteMoved(t *testing.T) {
	ctx := context.Background()
	m, repo, branch, ref, sha, _ := setupRestore(t)
	ws := m.Path(1)
	// Someone else pushes to the branch.
	other := filepath.Join(t.TempDir(), "other")
	gitT(t, filepath.Dir(other), nil, "clone", "-q", "-b", branch, m.RemoteURL(""), other)
	write(t, other, "z.txt", "theirs\n")
	commitAll(t, other, "theirs")
	gitT(t, other, nil, "push", "-q", "origin", branch)

	if err := m.RestoreCheckpoint(ctx, 1, ref, sha); err != nil {
		t.Fatal(err)
	}
	commitAll(t, ws, "restore run")
	if _, err := m.ForcePush(ctx, 1, repo, branch); err == nil {
		t.Fatal("the lease must refuse to overwrite a commit this workspace never pushed")
	}
}

func TestIsAgentBranch(t *testing.T) {
	for b, want := range map[string]bool{"agent/7": true, "agent/7-do-it": true, "agent/70-x": false, "agent/7x": false, "main": false, "agent/8-x": false} {
		if IsAgentBranch(7, b) != want {
			t.Errorf("%s: want %v", b, want)
		}
	}
}
