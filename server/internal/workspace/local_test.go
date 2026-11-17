package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func lgit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func lwrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newLocalRepo is the owner's repository: main with two files and a .gitignore.
func newLocalRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "my app")
	lgit(t, filepath.Dir(dir), "init", "-q", "-b", "main", dir)
	lwrite(t, filepath.Join(dir, "README.md"), "hello\n")
	lwrite(t, filepath.Join(dir, "src", "app.txt"), "one\ntwo\nthree\n")
	lwrite(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	lgit(t, dir, "add", "-A")
	lgit(t, dir, "commit", "-q", "-m", "init")
	real, _ := filepath.EvalSymlinks(dir)
	return real
}

// snapshot is everything delivery must leave alone: HEAD, the checked-out
// branch, the index file and the working tree status.
func snapshot(t *testing.T, repo string) string {
	t.Helper()
	idx, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(idx)
	return strings.Join([]string{
		lgit(t, repo, "rev-parse", "HEAD"),
		lgit(t, repo, "symbolic-ref", "-q", "HEAD"),
		string(sum[:]),
		lgit(t, repo, "status", "--porcelain", "--untracked-files=all"),
	}, "\n")
}

// contents is snapshot with the index's entries instead of its bytes: an
// owner action (merge) may rewrite the index's stat cache.
func contents(t *testing.T, repo string) string {
	t.Helper()
	return strings.Join([]string{
		lgit(t, repo, "rev-parse", "HEAD"),
		lgit(t, repo, "symbolic-ref", "-q", "HEAD"),
		lgit(t, repo, "ls-files", "--stage"),
		lgit(t, repo, "status", "--porcelain", "--untracked-files=all"),
	}, "\n")
}

// agentCommit simulates a run: one commit on the workspace's task branch.
func agentCommit(t *testing.T, ws, file, content string) {
	t.Helper()
	lwrite(t, filepath.Join(ws, file), content)
	lgit(t, ws, "add", "-A")
	lgit(t, ws, "commit", "-q", "-m", "agent: "+file)
}

func TestInspectLocal(t *testing.T) {
	ctx := context.Background()
	repo := newLocalRepo(t)
	lgit(t, repo, "remote", "add", "origin", "git@github.com:o/my-app.git")
	info, err := InspectLocal(ctx, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != repo || info.Name != "my app" || info.DefaultBranch != "main" || info.RemoteURL != "git@github.com:o/my-app.git" {
		t.Fatalf("%+v", info)
	}
	// A subfolder, a bare repository, a plain folder, the data dir, a missing folder.
	if _, err := InspectLocal(ctx, filepath.Join(repo, "src"), ""); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("subfolder: %v", err)
	}
	bare := filepath.Join(t.TempDir(), "b.git")
	lgit(t, filepath.Dir(bare), "init", "-q", "--bare", bare)
	if _, err := InspectLocal(ctx, bare, ""); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("bare: %v", err)
	}
	if _, err := InspectLocal(ctx, t.TempDir(), ""); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("plain: %v", err)
	}
	if _, err := InspectLocal(ctx, repo, filepath.Dir(repo)); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("inside the data dir: %v", err)
	}
	if _, err := InspectLocal(ctx, filepath.Join(repo, "nope"), ""); !errors.Is(err, ErrRepoMissing) {
		t.Fatalf("missing: %v", err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	lgit(t, filepath.Dir(empty), "init", "-q", empty)
	if _, err := InspectLocal(ctx, empty, ""); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("no commits: %v", err)
	}
}

func TestPrepareLocalFromLastCommit(t *testing.T) {
	ctx := context.Background()
	repo := newLocalRepo(t)
	lwrite(t, filepath.Join(repo, "src", "app.txt"), "changed\n")
	lwrite(t, filepath.Join(repo, "notes.txt"), "untracked\n")
	before := snapshot(t, repo)
	m := &Manager{Dir: t.TempDir()}
	if err := m.Prepare(ctx, 1, Repo{LocalPath: repo, BaseBranch: "main", StartFrom: StartLastCommit}, "agent/1-x"); err != nil {
		t.Fatal(err)
	}
	ws := m.Path(1)
	if b, _ := os.ReadFile(filepath.Join(ws, "src", "app.txt")); string(b) != "one\ntwo\nthree\n" {
		t.Fatalf("workspace has uncommitted changes: %q", b)
	}
	if _, err := os.Stat(filepath.Join(ws, "notes.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked file copied")
	}
	if lgit(t, ws, "rev-parse", "--abbrev-ref", "HEAD") != "agent/1-x" {
		t.Fatal("branch")
	}
	if snapshot(t, repo) != before {
		t.Fatal("prepare changed the original repository")
	}
	// Objects are copied, not hard-linked or shared through alternates.
	if _, err := os.Stat(filepath.Join(ws, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Fatal("workspace borrows objects from the original")
	}
}

func TestPrepareLocalWithWorkingTree(t *testing.T) {
	ctx := context.Background()
	repo := newLocalRepo(t)
	lwrite(t, filepath.Join(repo, "src", "app.txt"), "one\nTWO\nthree\n") // unstaged
	lwrite(t, filepath.Join(repo, "staged.txt"), "staged\n")
	lgit(t, repo, "add", "staged.txt")                                    // staged
	lwrite(t, filepath.Join(repo, "new", "untracked.txt"), "untracked\n") // untracked
	lwrite(t, filepath.Join(repo, "debug.log"), "ignored\n")              // ignored
	if err := os.WriteFile(filepath.Join(repo, "bin.dat"), []byte{0, 1, 2, 255}, 0o755); err != nil {
		t.Fatal(err)
	}
	lgit(t, repo, "rm", "-q", "README.md") // a deletion
	before := snapshot(t, repo)

	m := &Manager{Dir: t.TempDir()}
	if err := m.Prepare(ctx, 2, Repo{LocalPath: repo, BaseBranch: "main", StartFrom: StartWorkingTree}, "agent/2-y"); err != nil {
		t.Fatal(err)
	}
	ws := m.Path(2)
	if msg := lgit(t, ws, "log", "-1", "--format=%s"); msg != WIPMessage {
		t.Fatalf("last commit %q", msg)
	}
	for file, want := range map[string]string{
		"src/app.txt": "one\nTWO\nthree\n", "staged.txt": "staged\n", "new/untracked.txt": "untracked\n", "bin.dat": "\x00\x01\x02\xff",
	} {
		if b, _ := os.ReadFile(filepath.Join(ws, file)); string(b) != want {
			t.Errorf("%s = %q", file, b)
		}
	}
	if st, _ := os.Stat(filepath.Join(ws, "bin.dat")); st == nil || st.Mode().Perm()&0o100 == 0 {
		t.Error("executable bit lost")
	}
	if _, err := os.Stat(filepath.Join(ws, "debug.log")); !os.IsNotExist(err) {
		t.Error("ignored file copied")
	}
	if _, err := os.Stat(filepath.Join(ws, "README.md")); !os.IsNotExist(err) {
		t.Error("deletion not applied")
	}
	if out := lgit(t, ws, "status", "--porcelain"); out != "" {
		t.Errorf("workspace not clean after the WIP commit: %s", out)
	}
	if snapshot(t, repo) != before {
		t.Fatal("prepare changed the original repository")
	}

	// A clean tree makes no WIP commit.
	clean := newLocalRepo(t)
	if err := m.Prepare(ctx, 3, Repo{LocalPath: clean, BaseBranch: "main", StartFrom: StartWorkingTree}, "agent/3"); err != nil {
		t.Fatal(err)
	}
	if msg := lgit(t, m.Path(3), "log", "-1", "--format=%s"); msg != "init" {
		t.Fatalf("clean tree got commit %q", msg)
	}
}

func TestDeliverNeverTouchesTheOwnersTree(t *testing.T) {
	ctx := context.Background()
	repo := newLocalRepo(t)
	// The owner is mid-work: a dirty tracked file, a staged file and an untracked one.
	lwrite(t, filepath.Join(repo, "src", "app.txt"), "owner edit\n")
	lwrite(t, filepath.Join(repo, "wip.txt"), "staged\n")
	lgit(t, repo, "add", "wip.txt")
	lwrite(t, filepath.Join(repo, "scratch.txt"), "mine\n")
	before := snapshot(t, repo)

	m := &Manager{Dir: t.TempDir()}
	if err := m.Prepare(ctx, 42, Repo{LocalPath: repo, BaseBranch: "main"}, "agent/42-fix"); err != nil {
		t.Fatal(err)
	}
	agentCommit(t, m.Path(42), "src/new.txt", "agent\n")
	d, err := m.Deliver(ctx, 42, repo, "main", "agent/42-fix", LocalBranch(42, "Fix login"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Branch != "jm/42-fix-login" || d.Commits != 1 || d.Updated || !strings.Contains(d.DiffStat, "1 file changed") {
		t.Fatalf("%+v", d)
	}
	if snapshot(t, repo) != before {
		t.Fatal("delivery changed HEAD, the index or the working tree")
	}
	if got := lgit(t, repo, "show", "jm/42-fix-login:src/new.txt"); got != "agent" {
		t.Fatalf("branch content %q", got)
	}

	// A follow-up fast-forwards the same branch.
	if n, _ := m.UnpushedCommits(ctx, 42, "main", "agent/42-fix"); n != 0 {
		t.Fatalf("unpushed after delivery = %d", n)
	}
	agentCommit(t, m.Path(42), "src/more.txt", "more\n")
	if n, _ := m.UnpushedCommits(ctx, 42, "main", "agent/42-fix"); n != 1 {
		t.Fatalf("unpushed after a follow-up = %d", n)
	}
	d, err = m.Deliver(ctx, 42, repo, "main", "agent/42-fix", LocalBranch(42, "Fix login"), "jm/42-fix-login", false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Branch != "jm/42-fix-login" || !d.Updated || d.Commits != 2 {
		t.Fatalf("%+v", d)
	}
	if snapshot(t, repo) != before {
		t.Fatal("re-delivery changed the owner's tree")
	}
}

func TestDeliverBranchNameCollisions(t *testing.T) {
	ctx := context.Background()
	repo := newLocalRepo(t)
	lgit(t, repo, "branch", "jm/7-x")
	lgit(t, repo, "branch", "jm/7-x-2")
	m := &Manager{Dir: t.TempDir()}
	if err := m.Prepare(ctx, 7, Repo{LocalPath: repo, BaseBranch: "main"}, "agent/7-x"); err != nil {
		t.Fatal(err)
	}
	agentCommit(t, m.Path(7), "a.txt", "a\n")
	d, err := m.Deliver(ctx, 7, repo, "main", "agent/7-x", "jm/7-x", "", false)
	if err != nil || d.Branch != "jm/7-x-3" {
		t.Fatalf("%+v %v", d, err)
	}
	// The owner checked the branch out: the next update goes to a new name.
	lgit(t, repo, "checkout", "-q", "jm/7-x-3")
	before := snapshot(t, repo)
	agentCommit(t, m.Path(7), "b.txt", "b\n")
	d, err = m.Deliver(ctx, 7, repo, "main", "agent/7-x", "jm/7-x", "jm/7-x-3", false)
	if err != nil || d.Branch != "jm/7-x-4" || d.Previous != "jm/7-x-3" || d.Updated {
		t.Fatalf("%+v %v", d, err)
	}
	if snapshot(t, repo) != before {
		t.Fatal("delivery touched the checked-out branch")
	}
	// The owner committed on the delivered branch: not fast-forwardable.
	lgit(t, repo, "checkout", "-q", "main")
	lgit(t, repo, "checkout", "-q", "jm/7-x-4")
	lwrite(t, filepath.Join(repo, "owner.txt"), "o\n")
	lgit(t, repo, "add", "owner.txt")
	lgit(t, repo, "commit", "-q", "-m", "owner")
	lgit(t, repo, "checkout", "-q", "main")
	agentCommit(t, m.Path(7), "c.txt", "c\n")
	d, err = m.Deliver(ctx, 7, repo, "main", "agent/7-x", "jm/7-x", "jm/7-x-4", false)
	if err != nil || d.Branch != "jm/7-x-5" || d.Previous != "jm/7-x-4" {
		t.Fatalf("%+v %v", d, err)
	}
	if lgit(t, repo, "log", "-1", "--format=%s", "jm/7-x-4") != "owner" {
		t.Fatal("the owner's commit was overwritten")
	}
}

func deliverOne(t *testing.T, repo string, id int64, file, content string) string {
	t.Helper()
	ctx := context.Background()
	m := &Manager{Dir: t.TempDir()}
	branch := Branch(id, "t")
	if err := m.Prepare(ctx, id, Repo{LocalPath: repo, BaseBranch: "main"}, branch); err != nil {
		t.Fatal(err)
	}
	agentCommit(t, m.Path(id), file, content)
	d, err := m.Deliver(ctx, id, repo, "main", branch, LocalBranch(id, "t"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	return d.Branch
}

func TestMergeLocal(t *testing.T) {
	ctx := context.Background()
	repo := newLocalRepo(t)
	b := deliverOne(t, repo, 1, "src/a.txt", "a\n")

	// Refusals: dirty tree, detached HEAD, the branch itself checked out.
	lwrite(t, filepath.Join(repo, "README.md"), "dirty\n")
	if _, err := MergeLocal(ctx, repo, b); !errors.Is(err, ErrDirty) {
		t.Fatalf("dirty: %v", err)
	}
	lgit(t, repo, "checkout", "-q", "--", "README.md")
	lgit(t, repo, "checkout", "-q", "--detach")
	if _, err := MergeLocal(ctx, repo, b); !errors.Is(err, ErrDetached) {
		t.Fatalf("detached: %v", err)
	}
	lgit(t, repo, "checkout", "-q", "main")
	// Untracked files do not count as dirty.
	lwrite(t, filepath.Join(repo, "scratch.txt"), "x\n")

	res, err := MergeLocal(ctx, repo, b)
	if err != nil || !res.FastForward || res.Into != "main" {
		t.Fatalf("%+v %v", res, err)
	}
	if lgit(t, repo, "rev-parse", "main") != lgit(t, repo, "rev-parse", b) {
		t.Fatal("not fast-forwarded")
	}

	// main moved on: a merge commit.
	b2 := deliverOne(t, repo, 2, "src/b.txt", "b\n")
	lwrite(t, filepath.Join(repo, "README.md"), "owner\n")
	lgit(t, repo, "commit", "-q", "-am", "owner change")
	res, err = MergeLocal(ctx, repo, b2)
	if err != nil || res.FastForward {
		t.Fatalf("%+v %v", res, err)
	}
	if parents := strings.Fields(lgit(t, repo, "log", "-1", "--format=%p")); len(parents) != 2 {
		t.Fatalf("not a merge commit: %v", parents)
	}
}

func TestMergeLocalConflictAborts(t *testing.T) {
	ctx := context.Background()
	repo := newLocalRepo(t)
	b := deliverOne(t, repo, 3, "src/app.txt", "agent version\n")
	lwrite(t, filepath.Join(repo, "src", "app.txt"), "owner version\n")
	lgit(t, repo, "commit", "-q", "-am", "owner")
	before := contents(t, repo)
	_, err := MergeLocal(ctx, repo, b)
	var conflict *MergeConflictError
	if !errors.As(err, &conflict) || len(conflict.Files) != 1 || conflict.Files[0] != "src/app.txt" {
		t.Fatalf("%v", err)
	}
	if after := contents(t, repo); after != before {
		t.Fatalf("the aborted merge left changes behind:\n%q\n%q", before, after)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Fatal("merge still in progress")
	}
}

func TestCheckoutAndDiscardLocal(t *testing.T) {
	ctx := context.Background()
	repo := newLocalRepo(t)
	b := deliverOne(t, repo, 4, "x.txt", "x\n")

	lwrite(t, filepath.Join(repo, "README.md"), "dirty\n")
	if err := CheckoutLocal(ctx, repo, b); !errors.Is(err, ErrDirty) {
		t.Fatalf("dirty checkout: %v", err)
	}
	lgit(t, repo, "checkout", "-q", "--", "README.md")
	if err := CheckoutLocal(ctx, repo, b); err != nil {
		t.Fatal(err)
	}
	if lgit(t, repo, "symbolic-ref", "--short", "HEAD") != b {
		t.Fatal("not checked out")
	}
	if err := DiscardLocal(ctx, repo, b); !errors.Is(err, ErrCurrentBranch) {
		t.Fatalf("discard current: %v", err)
	}
	lgit(t, repo, "checkout", "-q", "main")
	if err := DiscardLocal(ctx, repo, b); err != nil {
		t.Fatal(err)
	}
	if branchExists(ctx, repo, b) {
		t.Fatal("branch still there")
	}
	if err := DiscardLocal(ctx, repo, b); err != nil {
		t.Fatalf("discarding a missing branch: %v", err)
	}
	if err := CheckoutLocal(ctx, repo, b); !errors.Is(err, ErrBranchGone) {
		t.Fatalf("checkout gone: %v", err)
	}
}

func TestLocalRepoMissing(t *testing.T) {
	ctx := context.Background()
	repo := newLocalRepo(t)
	m := &Manager{Dir: t.TempDir()}
	if err := m.Prepare(ctx, 5, Repo{LocalPath: repo, BaseBranch: "main"}, "agent/5"); err != nil {
		t.Fatal(err)
	}
	agentCommit(t, m.Path(5), "y.txt", "y\n")
	moved := repo + "-moved"
	if err := os.Rename(repo, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Deliver(ctx, 5, repo, "main", "agent/5", "jm/5", "", false); !errors.Is(err, ErrRepoMissing) {
		t.Fatalf("deliver: %v", err)
	}
	if _, err := MergeLocal(ctx, repo, "jm/5"); !errors.Is(err, ErrRepoMissing) {
		t.Fatalf("merge: %v", err)
	}
	st, err := StatusLocal(ctx, repo)
	if err != nil || st.Exists {
		t.Fatalf("%+v %v", st, err)
	}
	st, err = StatusLocal(ctx, moved)
	if err != nil || !st.Exists || st.Branch != "main" || st.Dirty || st.LastCommit.IsZero() {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestGitHubFullName(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/AndrewSl105/jungle_monkey.git": "AndrewSl105/jungle_monkey",
		"https://github.com/o/r":                           "o/r",
		"git@github.com:o/r.git":                           "o/r",
		"ssh://git@github.com/o/r.git":                     "o/r",
		"https://gitlab.com/o/r.git":                       "",
		"/home/me/repo":                                    "",
		"https://github.com/o":                             "",
		"":                                                 "",
	} {
		if got := GitHubFullName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// "Push & open PR": a local repository's workspace pushes to its GitHub
// repository, not to the folder it was copied from.
func TestLocalPushGoesToGitHub(t *testing.T) {
	ctx := context.Background()
	repo := newLocalRepo(t)
	bare := filepath.Join(t.TempDir(), "gh.git")
	lgit(t, filepath.Dir(bare), "init", "-q", "--bare", bare)
	m := &Manager{Dir: t.TempDir(), RemoteURL: func(full string) string {
		if full != "o/my-app" {
			t.Fatalf("remote for %q", full)
		}
		return bare
	}}
	r := Repo{LocalPath: repo, BaseBranch: "main", FullName: "o/my-app"}
	if err := m.Prepare(ctx, 9, r, "agent/9-x"); err != nil {
		t.Fatal(err)
	}
	agentCommit(t, m.Path(9), "p.txt", "p\n")
	if err := m.Push(ctx, 9, r, "agent/9-x"); err != nil {
		t.Fatal(err)
	}
	if lgit(t, bare, "rev-parse", "agent/9-x") != lgit(t, m.Path(9), "rev-parse", "HEAD") {
		t.Fatal("not pushed to the GitHub remote")
	}
	if branchExists(ctx, repo, "agent/9-x") {
		t.Fatal("pushed into the owner's folder")
	}
}

func TestMergeLocalRefusesToOverwriteUntracked(t *testing.T) {
	ctx := context.Background()
	repo := newLocalRepo(t)
	b := deliverOne(t, repo, 6, "new.txt", "agent\n")
	lwrite(t, filepath.Join(repo, "new.txt"), "mine\n")
	_, err := MergeLocal(ctx, repo, b)
	if !errors.Is(err, ErrGitRefused) || !strings.Contains(err.Error(), "new.txt") {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "new.txt")); string(b) != "mine\n" {
		t.Fatal("the owner's file was overwritten")
	}
}
