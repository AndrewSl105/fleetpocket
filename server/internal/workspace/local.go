package workspace

// Local repositories (MODE=local, docs/SPEC-server.md "Repository sources"):
// the task workspace is a copy of a folder on the owner's disk, and the
// result goes back into that folder as a branch. The original repository is
// only read, except for the delivered jm/... branch and the owner's explicit
// Merge / Check out / Discard actions; it is never mounted into a runner.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Start points of a local task's workspace.
const (
	StartLastCommit  = "last_commit"
	StartWorkingTree = "working_tree"
)

// WIPMessage is the commit that carries the owner's uncommitted changes.
const WIPMessage = "WIP: working tree at start"

// The identity of the commits Jungle Monkey itself makes in a workspace (the
// WIP commit) and, when the owner has no git identity, of a merge commit.
const (
	localAuthorName  = "Jungle Monkey"
	localAuthorEmail = "jungle-monkey@localhost"
)

var (
	// ErrDirty: the owner's working tree has uncommitted changes to tracked files.
	ErrDirty = errors.New("your working tree has uncommitted changes; commit or stash them first")
	// ErrDetached: the owner's repository has no current branch.
	ErrDetached = errors.New("your repository is on a detached HEAD; check out a branch first")
	// ErrNotRepo: the folder is not a usable git work tree.
	ErrNotRepo = errors.New("not a git work tree")
	// ErrRepoMissing: the local repository's folder is gone (moved or deleted).
	ErrRepoMissing = errors.New("repository folder not found")
	// ErrCurrentBranch: the action needs another branch checked out.
	ErrCurrentBranch = errors.New("the branch is checked out; check out another branch first")
	// ErrBranchGone: the delivered branch no longer exists in the repository.
	ErrBranchGone = errors.New("the branch no longer exists in your repository")
)

// MergeConflictError lists the files a merge could not combine. The merge
// was aborted, so the owner's branch is as it was.
type MergeConflictError struct{ Files []string }

func (e *MergeConflictError) Error() string {
	return "merge conflict in " + strings.Join(e.Files, ", ") + "; the merge was aborted and your branch is unchanged"
}

// localGit runs git in the owner's repository. GIT_OPTIONAL_LOCKS=0 keeps
// read commands (status, diff) from refreshing the owner's index; automatic
// gc/maintenance is off so delivery never repacks their repository.
func localGit(ctx context.Context, dir string, stdin io.Reader, args ...string) (string, error) {
	full := append([]string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(redact(stderr.String())))
	}
	return stdout.String(), nil
}

// LocalRepoInfo describes a folder that can be added as a local repository.
type LocalRepoInfo struct {
	Path          string // the work tree's top level, symlinks resolved
	Name          string // folder name
	DefaultBranch string // the checked-out branch ("" on a detached HEAD)
	RemoteURL     string // origin, if any
}

// InspectLocal checks that dir is the top level of a non-bare git work tree
// outside forbidden (the data directory) and describes it.
func InspectLocal(ctx context.Context, dir, forbidden string) (*LocalRepoInfo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrRepoMissing
		}
		return nil, err
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a folder", ErrNotRepo, abs)
	}
	if forbidden != "" {
		if f, err := filepath.EvalSymlinks(forbidden); err == nil && within(real, f) {
			return nil, fmt.Errorf("%w: %s is inside the Jungle Monkey data directory", ErrNotRepo, abs)
		}
	}
	out, err := localGit(ctx, real, nil, "rev-parse", "--is-bare-repository", "--is-inside-work-tree", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotRepo, abs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 || lines[0] != "false" || lines[1] != "true" {
		return nil, fmt.Errorf("%w: %s is a bare repository or not a work tree", ErrNotRepo, abs)
	}
	top, err := filepath.EvalSymlinks(lines[2])
	if err != nil {
		return nil, err
	}
	if top != real {
		return nil, fmt.Errorf("%w: %s is inside the repository %s; pick its top folder", ErrNotRepo, abs, top)
	}
	if _, err := localGit(ctx, real, nil, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		return nil, fmt.Errorf("%w: %s has no commits yet", ErrNotRepo, abs)
	}
	info := &LocalRepoInfo{Path: real, Name: filepath.Base(real)}
	if b, err := localGit(ctx, real, nil, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		info.DefaultBranch = strings.TrimSpace(b)
	}
	if u, err := localGit(ctx, real, nil, "config", "--get", "remote.origin.url"); err == nil {
		info.RemoteURL = strings.TrimSpace(u)
	}
	return info, nil
}

// within reports whether path is dir or inside it (both cleaned, absolute).
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// LocalStatus is the state of the owner's repository.
type LocalStatus struct {
	Exists     bool      `json:"exists"`
	Branch     string    `json:"branch"` // "" on a detached HEAD
	Detached   bool      `json:"detached"`
	Dirty      bool      `json:"dirty"` // uncommitted changes to tracked files
	LastCommit time.Time `json:"last_commit"`
}

// StatusLocal reads the branch, dirty state and last commit date without
// touching the index.
func StatusLocal(ctx context.Context, path string) (*LocalStatus, error) {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &LocalStatus{}, nil
		}
		return nil, err
	}
	st := &LocalStatus{Exists: true}
	if b, err := localGit(ctx, path, nil, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		st.Branch = strings.TrimSpace(b)
	} else {
		st.Detached = true
	}
	dirty, err := isDirty(ctx, path)
	if err != nil {
		return nil, err
	}
	st.Dirty = dirty
	if out, err := localGit(ctx, path, nil, "log", "-1", "--format=%ct"); err == nil {
		if sec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil {
			st.LastCommit = time.Unix(sec, 0).UTC()
		}
	}
	return st, nil
}

func isDirty(ctx context.Context, path string) (bool, error) {
	out, err := localGit(ctx, path, nil, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func requireRepo(path string) error {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return ErrRepoMissing
	}
	return nil
}

func branchExists(ctx context.Context, path, branch string) bool {
	_, err := localGit(ctx, path, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// prepareLocal clones the owner's repository into the task workspace. The
// clone copies objects (--no-hardlinks) so nothing in the workspace shares
// files with the original. The task branch starts from:
//   - resume (a delivered branch, after the workspace was cleaned up), else
//   - the commit checked out in the original plus its uncommitted changes
//     and untracked files (StartWorkingTree), committed as WIPMessage, else
//   - origin/<base> (StartLastCommit).
func (m *Manager) prepareLocal(ctx context.Context, taskID int64, repo Repo, branch string) error {
	if err := requireRepo(repo.LocalPath); err != nil {
		return err
	}
	dir := m.Path(taskID)
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(dir)
	if _, err := git(ctx, m.Dir, nil, "clone", "--quiet", "--local", "--no-hardlinks", "--no-tags", repo.LocalPath, dir); err != nil {
		return fmt.Errorf("copy %s: %w", repo.LocalPath, err)
	}
	switch {
	case repo.Resume != "" && remoteBranch(ctx, dir, repo.Resume):
		_, err := git(ctx, dir, nil, "checkout", "-q", "-b", branch, "origin/"+repo.Resume)
		return err
	case repo.StartFrom == StartWorkingTree:
		// The clone checked out the original's HEAD (branch or detached).
		if _, err := git(ctx, dir, nil, "checkout", "-q", "-b", branch); err != nil {
			return err
		}
		return copyWorkingTree(ctx, repo.LocalPath, dir)
	default:
		_, err := git(ctx, dir, nil, "checkout", "-q", "-b", branch, "origin/"+repo.BaseBranch)
		return err
	}
}

func remoteBranch(ctx context.Context, dir, branch string) bool {
	_, err := git(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	return err == nil
}

// copyWorkingTree applies the original's `git diff --binary HEAD` and copies
// its untracked, not ignored files into dir, then commits them as one WIP
// commit (none when the tree was clean).
func copyWorkingTree(ctx context.Context, src, dir string) error {
	diff, err := localGit(ctx, src, nil, "diff", "--binary", "--no-color", "--no-ext-diff", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) != "" {
		if _, err := localGit(ctx, dir, strings.NewReader(diff), "apply", "--binary", "--index", "--whitespace=nowarn", "-"); err != nil {
			return fmt.Errorf("apply uncommitted changes: %w", err)
		}
	}
	list, err := localGit(ctx, src, nil, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	for _, rel := range strings.Split(list, "\x00") {
		if rel == "" {
			continue
		}
		if err := copyEntry(filepath.Join(src, rel), filepath.Join(dir, rel)); err != nil {
			return fmt.Errorf("copy untracked %s: %w", rel, err)
		}
	}
	if _, err := git(ctx, dir, nil, "add", "-A"); err != nil {
		return err
	}
	if out, err := git(ctx, dir, nil, "status", "--porcelain"); err != nil || strings.TrimSpace(out) == "" {
		return err
	}
	_, err = git(ctx, dir, identity(), "commit", "-q", "--no-verify", "-m", WIPMessage)
	return err
}

func identity() []string {
	return []string{"-c", "user.name=" + localAuthorName, "-c", "user.email=" + localAuthorEmail}
}

// copyEntry copies a regular file (keeping its mode) or recreates a symlink.
// Other file types are skipped.
func copyEntry(from, to string) error {
	st, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(from)
		if err != nil {
			return err
		}
		return os.Symlink(target, to)
	case st.Mode().IsRegular():
		in, err := os.Open(from)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return nil
}

// Delivery is what Deliver put into the owner's repository.
type Delivery struct {
	Branch   string // jm/<id>-<slug>[-n]
	Commits  int    // commits not on the base branch
	DiffStat string // last line of `git diff --stat`
	Updated  bool   // an earlier delivery was fast-forwarded
	Previous string // the earlier branch, when the update went to a new name
}

// LocalBranch is "jm/<id>-<slug>".
func LocalBranch(taskID int64, title string) string {
	if slug := Slug(title); slug != "" {
		return fmt.Sprintf("jm/%d-%s", taskID, slug)
	}
	return fmt.Sprintf("jm/%d", taskID)
}

// Deliver fetches the workspace's task branch into the owner's repository as
// a branch of its own. It never touches the owner's checked-out branch,
// index or working tree:
//   - current (an earlier delivery) is fast-forwarded (forced when force is
//     set: a checkpoint restore rewrote the task branch);
//   - when current is checked out somewhere, or cannot be fast-forwarded
//     because the owner committed on it, the update goes to a new name and
//     Previous says so;
//   - a new name is want, then want-2, want-3… until one is free.
func (m *Manager) Deliver(ctx context.Context, taskID int64, repoPath, base, branch, want, current string, force bool) (*Delivery, error) {
	if err := requireRepo(repoPath); err != nil {
		return nil, err
	}
	ws := m.Path(taskID)
	d := &Delivery{}
	fetch := func(name string, force bool) error {
		spec := branch + ":refs/heads/" + name
		if force {
			spec = "+" + spec
		}
		_, err := localGit(ctx, repoPath, nil, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head",
			"--no-recurse-submodules", ws, spec)
		return err
	}
	if current != "" && branchExists(ctx, repoPath, current) {
		if err := fetch(current, force); err == nil {
			d.Branch, d.Updated = current, true
		} else {
			d.Previous = current
		}
	}
	if d.Branch == "" {
		name := want
		for n := 2; branchExists(ctx, repoPath, name); n++ {
			name = fmt.Sprintf("%s-%d", want, n)
		}
		if err := fetch(name, false); err != nil {
			return nil, fmt.Errorf("deliver %s: %w", name, err)
		}
		d.Branch = name
	}
	// What is delivered counts as pushed: the next run only re-delivers new commits.
	if _, err := git(ctx, ws, nil, "update-ref", "refs/remotes/origin/"+branch, "HEAD"); err != nil {
		return nil, err
	}
	if n, err := git(ctx, ws, nil, "rev-list", "--count", "origin/"+base+"..HEAD"); err == nil {
		d.Commits, _ = strconv.Atoi(strings.TrimSpace(n))
	}
	if stat, err := git(ctx, ws, nil, "diff", "--shortstat", "origin/"+base+"...HEAD"); err == nil {
		d.DiffStat = strings.TrimSpace(stat)
	}
	return d, nil
}

// MergeResult is what MergeLocal did.
type MergeResult struct {
	Into        string
	FastForward bool
}

// MergeLocal merges branch into the owner's current branch: a fast-forward
// when possible, else a merge commit. It refuses on a dirty tree or a
// detached HEAD; on a conflict it aborts and returns *MergeConflictError.
func MergeLocal(ctx context.Context, path, branch string) (*MergeResult, error) {
	if err := requireRepo(path); err != nil {
		return nil, err
	}
	if !branchExists(ctx, path, branch) {
		return nil, ErrBranchGone
	}
	cur, err := localGit(ctx, path, nil, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return nil, ErrDetached
	}
	cur = strings.TrimSpace(cur)
	if cur == branch {
		return nil, ErrCurrentBranch
	}
	if dirty, err := isDirty(ctx, path); err != nil {
		return nil, err
	} else if dirty {
		return nil, ErrDirty
	}
	res := &MergeResult{Into: cur}
	if _, err := localGit(ctx, path, nil, "merge-base", "--is-ancestor", "HEAD", branch); err == nil {
		res.FastForward = true
		if _, err := localGit(ctx, path, nil, "merge", "--quiet", "--ff-only", branch); err != nil {
			return nil, err
		}
		return res, nil
	}
	args := []string{"merge", "--quiet", "--no-ff", "--no-edit", "-m", fmt.Sprintf("Merge branch '%s' into %s", branch, cur), branch}
	if !hasIdentity(ctx, path) {
		args = append(identity(), args...)
	}
	if _, err := localGit(ctx, path, nil, args...); err != nil {
		files, _ := localGit(ctx, path, nil, "diff", "--name-only", "--diff-filter=U")
		if _, abortErr := localGit(ctx, path, nil, "merge", "--abort"); abortErr != nil {
			return nil, fmt.Errorf("%v; and the merge could not be aborted: %w", err, abortErr)
		}
		if list := strings.Fields(files); len(list) > 0 {
			return nil, &MergeConflictError{Files: list}
		}
		return nil, err
	}
	return res, nil
}

func hasIdentity(ctx context.Context, path string) bool {
	name, _ := localGit(ctx, path, nil, "config", "--get", "user.name")
	email, _ := localGit(ctx, path, nil, "config", "--get", "user.email")
	return strings.TrimSpace(name) != "" && strings.TrimSpace(email) != ""
}

// CheckoutLocal checks out branch in the owner's repository; it refuses on a
// dirty tree.
func CheckoutLocal(ctx context.Context, path, branch string) error {
	if err := requireRepo(path); err != nil {
		return err
	}
	if !branchExists(ctx, path, branch) {
		return ErrBranchGone
	}
	if dirty, err := isDirty(ctx, path); err != nil {
		return err
	} else if dirty {
		return ErrDirty
	}
	_, err := localGit(ctx, path, nil, "checkout", "--quiet", branch)
	return err
}

// DiscardLocal deletes branch from the owner's repository; it refuses while
// the branch is checked out. A branch that is already gone is not an error.
func DiscardLocal(ctx context.Context, path, branch string) error {
	if err := requireRepo(path); err != nil {
		return err
	}
	if !branchExists(ctx, path, branch) {
		return nil
	}
	if cur, err := localGit(ctx, path, nil, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil && strings.TrimSpace(cur) == branch {
		return ErrCurrentBranch
	}
	_, err := localGit(ctx, path, nil, "branch", "--quiet", "-D", branch)
	return err
}
