package workspace

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrCheckpointGone means a checkpoint's commit is no longer in the
// workspace (it was cleaned up after WORKSPACE_RETENTION, or re-cloned).
var ErrCheckpointGone = errors.New("checkpoint is no longer in the workspace")

// ErrNotAgentBranch refuses a force push to a branch Jungle Monkey did not create.
var ErrNotAgentBranch = errors.New("not a branch Jungle Monkey created")

// IsAgentBranch reports whether branch is one Branch(taskID, …) creates:
// "agent/<id>" or "agent/<id>-<slug>". Only those may be force-pushed.
func IsAgentBranch(taskID int64, branch string) bool {
	p := "agent/" + strconv.FormatInt(taskID, 10)
	return branch == p || strings.HasPrefix(branch, p+"-")
}

// checkpointCommit resolves ref (refs/jm/checkpoints/<run>/<n>) and checks
// it still points at sha.
func (m *Manager) checkpointCommit(ctx context.Context, taskID int64, ref, sha string) error {
	if !m.Exists(taskID) {
		return ErrCheckpointGone
	}
	if !strings.HasPrefix(ref, "refs/jm/checkpoints/") {
		return fmt.Errorf("not a checkpoint ref: %s", ref)
	}
	out, err := git(ctx, m.Path(taskID), nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil || strings.TrimSpace(out) != sha {
		return ErrCheckpointGone
	}
	return nil
}

// CheckpointDiff is what restoring the checkpoint discards: `git diff
// <checkpoint> HEAD`, cut at limit bytes.
func (m *Manager) CheckpointDiff(ctx context.Context, taskID int64, ref, sha string, limit int) (string, bool, error) {
	if err := m.checkpointCommit(ctx, taskID, ref, sha); err != nil {
		return "", false, err
	}
	out, err := git(ctx, m.Path(taskID), nil, "diff", "--no-color", sha, "HEAD")
	if err != nil {
		return "", false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// RestoreCheckpoint resets the task branch to the commit the checkpoint was
// taken on (its parent) and puts the checkpoint's tree in the index and the
// working tree, uncommitted: the next run's commit records it, so the
// checkpoint commit never becomes part of the branch. Untracked files are
// removed; ignored ones (.fleetpocket/, dependencies) stay.
func (m *Manager) RestoreCheckpoint(ctx context.Context, taskID int64, ref, sha string) error {
	if err := m.checkpointCommit(ctx, taskID, ref, sha); err != nil {
		return err
	}
	dir := m.Path(taskID)
	if _, err := git(ctx, dir, nil, "rev-parse", "--verify", "--quiet", sha+"^"); err != nil {
		return fmt.Errorf("checkpoint %s has no parent commit", sha)
	}
	steps := [][]string{
		{"reset", "-q", "--hard", sha + "^"},
		{"clean", "-fdq"},
		{"read-tree", "-u", "--reset", sha},
	}
	for _, s := range steps {
		if _, err := git(ctx, dir, nil, s...); err != nil {
			return err
		}
	}
	return nil
}

// Diverged reports whether origin/<branch> exists and is not an ancestor of
// HEAD: a plain push would be rejected (a restore rewrote the branch).
func (m *Manager) Diverged(ctx context.Context, taskID int64, branch string) (bool, error) {
	dir := m.Path(taskID)
	ref := "refs/remotes/origin/" + branch
	if _, err := git(ctx, dir, nil, "rev-parse", "--verify", "--quiet", ref); err != nil {
		return false, nil // never pushed
	}
	if _, err := git(ctx, dir, nil, "merge-base", "--is-ancestor", ref, "HEAD"); err != nil {
		return true, nil
	}
	return false, nil
}

// ForcePush pushes HEAD to origin/<branch> with --force-with-lease, expecting
// the remote to still be at what this workspace last pushed (its
// remote-tracking ref). Only branches Jungle Monkey created may be
// rewritten. It returns the replaced remote commit.
func (m *Manager) ForcePush(ctx context.Context, taskID int64, repo Repo, branch string) (string, error) {
	if !IsAgentBranch(taskID, branch) {
		return "", fmt.Errorf("%w: %s", ErrNotAgentBranch, branch)
	}
	dir := m.Path(taskID)
	old, err := git(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	if err != nil {
		return "", fmt.Errorf("no record of what was pushed to %s", branch)
	}
	old = strings.TrimSpace(old)
	auth, err := m.authArgs(ctx, repo)
	if err != nil {
		return "", err
	}
	lease := "--force-with-lease=refs/heads/" + branch + ":" + old
	if _, err := git(ctx, dir, auth, "push", lease, m.pushTarget(repo), "HEAD:refs/heads/"+branch); err != nil {
		return "", err
	}
	_, _ = git(ctx, dir, nil, "update-ref", "refs/remotes/origin/"+branch, "HEAD")
	return old, nil
}
