package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// WorkspaceRepo is what a task's host checks out and pushes. A local
// repository is copied from its folder; its FullName is the GitHub
// repository of its origin remote, where "Push & open PR" pushes.
func WorkspaceRepo(r *store.Repository, t *store.Task) workspace.Repo {
	w := workspace.Repo{FullName: r.FullName, InstallationID: r.InstallationID, BaseBranch: r.BaseBranch}
	if r.Local() {
		w.LocalPath, w.StartFrom, w.Resume = r.Path, t.StartFrom, deref(t.LocalBranch, "")
		w.FullName = workspace.GitHubFullName(r.RemoteURL)
	}
	return w
}

// GitHubRepo is r as the GitHub API sees it: a local repository's FullName
// becomes its origin's "owner/name" ("" when it has no GitHub remote).
func GitHubRepo(r *store.Repository) *store.Repository {
	if !r.Local() {
		return r
	}
	cp := *r
	cp.FullName = r.GitHubName()
	return &cp
}

// FolderMissing is the status_reason of a delivery that waits for the
// owner to locate a moved or deleted repository folder.
const FolderMissing = "repository folder not found"

// deliverLocal validates the diff like a push would, then fetches the task
// branch into the owner's repository (jm/<id>-<slug>) and marks the task
// branch_ready. A missing folder keeps the task pushing until the owner
// locates it (ResumeDeliveries).
func (s *Scheduler) deliverLocal(ctx context.Context, h hosts.Host, t *store.Task, repo *store.Repository, branch string, force bool, limit string) {
	if t.Status != tasks.Pushing {
		if _, err := s.Tasks.SetStatus(ctx, t.ID, tasks.Pushing, ""); err != nil {
			s.log(t, 0).Error("set pushing", "err", err)
			return
		}
	}
	if err := h.Validate(ctx, t.ID, repo.BaseBranch, s.Cfg.SecretsScan == "gitleaks"); err != nil {
		if errors.Is(err, workspace.ErrInvalidDiff) {
			s.addEvent(ctx, t.ID, nil, "diff_rejected", map[string]any{"reason": err.Error()})
		}
		s.fail(ctx, t, err.Error())
		return
	}
	d, err := h.DeliverLocal(ctx, t.ID, hosts.LocalDelivery{
		Path: repo.Path, Base: repo.BaseBranch, Branch: branch,
		Want: workspace.LocalBranch(t.ID, t.Title), Current: deref(t.LocalBranch, ""), Force: force,
	})
	if errors.Is(err, workspace.ErrRepoMissing) {
		s.log(t, 0).Warn("local repository folder missing; delivery waits", "path", repo.Path)
		reason := FolderMissing + ": " + repo.Path
		if deref(t.StatusReason, "") != reason {
			if nt, err := s.Store.SetStatus(ctx, t.ID, []string{tasks.Pushing}, tasks.Pushing, reason, false); err == nil {
				s.Hub.PublishTask(nt)
			}
		}
		return
	}
	if err != nil {
		s.fail(ctx, t, err.Error())
		return
	}
	if err := s.Store.SetLocalBranch(ctx, t.ID, &d.Branch); err != nil {
		s.fail(ctx, t, err.Error())
		return
	}
	payload := map[string]any{"branch": d.Branch, "commits": d.Commits, "diffstat": d.DiffStat, "updated": d.Updated}
	if d.Previous != "" {
		payload["previous"] = d.Previous
	}
	s.addEvent(ctx, t.ID, nil, "branch_ready", payload)
	if _, err := s.Tasks.SetStatus(ctx, t.ID, tasks.BranchReady, limit); err != nil {
		s.log(t, 0).Error("set branch_ready", "err", err)
	}
}

// ResumeDeliveries retries the deliveries that wait for repository repoID's
// folder (after "Locate…").
func (s *Scheduler) ResumeDeliveries(ctx context.Context, repoID int64) {
	list, err := s.Store.TasksByStatus(ctx, tasks.Pushing)
	if err != nil {
		return
	}
	for _, t := range list {
		if t.RepositoryID != repoID || !strings.HasPrefix(deref(t.StatusReason, ""), FolderMissing) {
			continue
		}
		s.wg.Add(1)
		go func(t *store.Task) {
			defer s.wg.Done()
			s.afterRun(context.WithoutCancel(ctx), t, "", 0, "")
		}(t)
	}
}

// PushLocal is "Push & open PR" on a branch_ready task: the task branch is
// pushed to the local repository's GitHub remote with the owner's token
// (held by this process, never by the runner) and a PR is opened; the task
// then follows the normal PR flow (pr_open, CI, auto-fix). On failure the
// task goes back to branch_ready.
func (s *Scheduler) PushLocal(ctx context.Context, taskID int64) error {
	pub := s.publisher()
	if pub == nil {
		return tasks.ErrGitHubDisabled
	}
	t, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	repo, err := s.Store.GetRepository(ctx, t.RepositoryID)
	if err != nil {
		return err
	}
	project, err := s.Store.GetProject(ctx, t.ProjectID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.pushing[t.ID] {
		s.mu.Unlock()
		return fmt.Errorf("%w: the task is already being pushed", tasks.ErrConflict)
	}
	s.pushing[t.ID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pushing, t.ID)
		s.mu.Unlock()
	}()
	nt, err := s.Store.SetStatus(ctx, t.ID, []string{tasks.BranchReady}, tasks.Pushing, "push & open PR", false)
	if err != nil {
		return err
	}
	s.Hub.PublishTask(nt)
	t = nt
	branch := deref(t.Branch, "")
	s.recordPush(ctx, t)
	res, err := pub.Publish(ctx, PublishInput{
		Host: s.host(t), Task: t, Repo: GitHubRepo(repo), Project: project, Branch: branch,
		Summary: s.lastSummary(ctx, t.ID), CostUSD: t.CostUSDTotal, Turns: t.Turns,
		RequestedBy: s.requestedBy(ctx, t),
	})
	if err != nil {
		if errors.Is(err, ErrRejected) {
			s.addEvent(ctx, t.ID, nil, "diff_rejected", map[string]any{"reason": err.Error()})
		}
		if bt, serr := s.Store.SetStatus(ctx, t.ID, []string{tasks.Pushing}, tasks.BranchReady, "push failed: "+err.Error(), false); serr == nil {
			s.Hub.PublishTask(bt)
		}
		return fmt.Errorf("push & open PR: %w", err)
	}
	if err := s.Store.SetPR(ctx, t.ID, res.PRNumber, res.PRURL); err != nil {
		s.log(t, 0).Error("set pr", "err", err)
	}
	s.addEvent(ctx, t.ID, nil, "branch_action", map[string]any{"action": "pushed", "branch": branch})
	typ := "pr_updated"
	if res.Created {
		typ = "pr_opened"
	}
	s.addEvent(ctx, t.ID, nil, typ, map[string]any{"number": res.PRNumber, "url": res.PRURL})
	s.afterPush(ctx, t, nil)
	_, err = s.Tasks.SetStatus(ctx, t.ID, tasks.PROpen, "")
	return err
}
