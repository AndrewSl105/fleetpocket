package tasks

import (
	"context"
	"errors"
	"fmt"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// LocalBranches is what the branch actions need from a task's host
// (hosts.Host; a node refuses them).
type LocalBranches interface {
	MergeLocal(ctx context.Context, path, branch string) (*workspace.MergeResult, error)
	CheckoutLocal(ctx context.Context, path, branch string) error
	DiscardLocal(ctx context.Context, path, branch string) error
}

// ErrNoGitHubRemote: "Push & open PR" needs a GitHub origin remote.
var ErrNoGitHubRemote = errors.New("this repository has no GitHub remote")

// branchTask loads a branch_ready task with a delivered branch and its local repository.
func (s *Service) branchTask(ctx context.Context, taskID int64) (*store.Task, *store.Repository, error) {
	t, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	if t.Status != BranchReady || t.LocalBranch == nil {
		return nil, nil, fmt.Errorf("%w: only a task whose branch is ready can do this (task is %s)", ErrConflict, t.Status)
	}
	repo, err := s.Store.GetRepository(ctx, t.RepositoryID)
	if err != nil {
		return nil, nil, err
	}
	if !repo.Local() {
		return nil, nil, fmt.Errorf("%w: not a local repository", ErrInvalid)
	}
	if s.LocalHost == nil {
		return nil, nil, fmt.Errorf("%w: local repositories are not available", ErrInvalid)
	}
	return t, repo, nil
}

func (s *Service) branchEvent(ctx context.Context, taskID int64, payload map[string]any) {
	if ev, err := s.Store.AddBackendEvent(ctx, taskID, nil, "branch_action", payload); err == nil {
		s.publishEvent(ev)
	}
}

// MergeBranch merges the delivered branch into the owner's current branch
// and completes the task. Refused on a dirty tree or a detached HEAD; a
// conflict aborts the merge (workspace.MergeConflictError).
func (s *Service) MergeBranch(ctx context.Context, taskID int64) (*View, error) {
	t, repo, err := s.branchTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	res, err := s.LocalHost(t).MergeLocal(ctx, repo.Path, *t.LocalBranch)
	if err != nil {
		return nil, err
	}
	s.branchEvent(ctx, taskID, map[string]any{"action": "merged", "branch": *t.LocalBranch, "into": res.Into, "fast_forward": res.FastForward})
	if nt, err := s.Store.SetStatus(ctx, taskID, []string{BranchReady}, Completed, "branch merged into "+res.Into, true); err == nil {
		s.publishTask(nt)
		if s.Executor != nil {
			_ = s.Executor.Cancel(ctx, taskID) // removes idle sidecars
		}
	} else if !errors.Is(err, ErrConflict) {
		return nil, err
	}
	return s.Get(ctx, taskID)
}

// CheckoutBranch checks the delivered branch out in the owner's repository
// (refused on a dirty tree). The task stays branch_ready.
func (s *Service) CheckoutBranch(ctx context.Context, taskID int64) (*View, error) {
	t, repo, err := s.branchTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := s.LocalHost(t).CheckoutLocal(ctx, repo.Path, *t.LocalBranch); err != nil {
		return nil, err
	}
	s.branchEvent(ctx, taskID, map[string]any{"action": "checked_out", "branch": *t.LocalBranch})
	return s.Get(ctx, taskID)
}

// DiscardBranch deletes the delivered branch and completes the task.
func (s *Service) DiscardBranch(ctx context.Context, taskID int64) (*View, error) {
	t, repo, err := s.branchTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := s.LocalHost(t).DiscardLocal(ctx, repo.Path, *t.LocalBranch); err != nil {
		return nil, err
	}
	if err := s.Store.SetLocalBranch(ctx, taskID, nil); err != nil {
		return nil, err
	}
	s.branchEvent(ctx, taskID, map[string]any{"action": "discarded", "branch": *t.LocalBranch})
	if nt, err := s.Store.SetStatus(ctx, taskID, []string{BranchReady}, Completed, "branch discarded", true); err == nil {
		s.publishTask(nt)
		if s.Executor != nil {
			_ = s.Executor.Cancel(ctx, taskID)
		}
	} else if !errors.Is(err, ErrConflict) {
		return nil, err
	}
	return s.Get(ctx, taskID)
}

// PushBranch pushes the task branch to the repository's GitHub remote and
// opens a PR; the task then follows the normal PR flow (pr_open, CI).
func (s *Service) PushBranch(ctx context.Context, taskID int64) (*View, error) {
	_, repo, err := s.branchTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if repo.GitHubName() == "" {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, ErrNoGitHubRemote)
	}
	if s.PushLocal == nil {
		return nil, ErrGitHubDisabled
	}
	if err := s.PushLocal(ctx, taskID); err != nil {
		return nil, err
	}
	return s.Get(ctx, taskID)
}
