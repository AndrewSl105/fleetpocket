package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/andrewsl105/fleetpocket/server/internal/autofix"
	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

// The diff-guard question of a CI auto-fix attempt (docs/SPEC-server.md "CI
// auto-fix"): stored like a runner question of the attempt's run, so the web
// card, Telegram buttons and POST /answer handle it; tasks.Service.Answer
// routes it to ResolveHeld instead of a runner.
const (
	GuardQuestionText = "The fix changes CI or tests. Push anyway?"
	GuardPush         = "Push anyway"
	GuardDiscard      = "Discard"
)

// maxGuardDiff bounds the fix diff the guard reads; a larger one needs the owner.
const maxGuardDiff = 1 << 20

// autoFixRun returns the run of the task's auto-fix attempt when the run that
// just ended is one and the loop still waits for it.
func (s *Scheduler) autoFixRun(ctx context.Context, t *store.Task) *store.Run {
	if t.AutoFixState == nil || *t.AutoFixState != store.AutoFixFixing {
		return nil
	}
	run, err := s.Store.LatestRun(ctx, t.ID)
	if err != nil || run.AutoFixAttempt == nil {
		return nil
	}
	return run
}

// guardFix lists why the fix commits (not yet on origin/<branch>) need the
// owner before they are pushed.
func (s *Scheduler) guardFix(ctx context.Context, h hosts.Host, t *store.Task, branch string) ([]string, error) {
	diff, truncated, err := h.Diff(ctx, t.ID, branch, maxGuardDiff)
	if err != nil {
		return nil, err
	}
	reasons := autofix.GuardDiff(autofix.ParseDiff(diff))
	if truncated {
		reasons = append(reasons, "the fix diff is too large to inspect")
	}
	return reasons, nil
}

// holdFix keeps the fix commits in the workspace and asks the owner. The task
// stays pr_open (no slot is held) with the open question.
func (s *Scheduler) holdFix(ctx context.Context, t *store.Task, run *store.Run, reasons []string) {
	nt, err := s.Store.SetAutoFixState(ctx, t.ID, []string{store.AutoFixFixing}, store.AutoFixHeld)
	if err != nil {
		s.log(t, run.ID).Error("hold auto-fix", "err", err)
		return
	}
	text := GuardQuestionText + "\n\n- " + strings.Join(reasons, "\n- ")
	options := []string{GuardPush, GuardDiscard}
	for _, r := range reasons {
		if strings.Contains(r, ".github/") {
			// workspace.Validate never lets a change under .github/ through.
			text += "\n\nJungle Monkey never pushes changes under .github/, so this fix can only be discarded."
			options = []string{GuardDiscard}
			break
		}
	}
	q := store.Question{ID: tasks.QuestionID(run.ID, tasks.GuardQuestionID), TaskID: t.ID, RunID: run.ID, Text: text, Options: options}
	if _, err := s.Store.CreateQuestion(ctx, q); err != nil {
		s.log(t, run.ID).Error("store guard question", "err", err)
	}
	s.addEvent(ctx, t.ID, nil, "autofix", map[string]any{
		"phase": "held", "attempt": nt.AutoFixAttempts, "max_attempts": deref(nt.AutoFixMaxAttempts, 0), "reasons": reasons,
	})
	s.addEvent(ctx, t.ID, &run.ID, "question", map[string]any{"question_id": tasks.GuardQuestionID, "text": text, "options": options})
	if _, err := s.Tasks.SetStatus(ctx, t.ID, tasks.PROpen, "needs you: the fix changes CI or tests"); err != nil {
		s.log(t, run.ID).Error("set pr_open", "err", err)
	}
	s.autoFixOutcome(t.ProjectID, store.AutoFixNeedsYou)
}

// stopAutoFix hands an active loop back to the owner.
func (s *Scheduler) stopAutoFix(ctx context.Context, t *store.Task, reason string) {
	nt, err := s.Store.SetAutoFixState(ctx, t.ID, store.AutoFixActive, store.AutoFixNeedsYou)
	if err != nil {
		return
	}
	s.Hub.PublishTask(nt)
	s.addEvent(ctx, t.ID, nil, "autofix", map[string]any{
		"phase": "needs_you", "reason": reason, "attempt": nt.AutoFixAttempts, "max_attempts": deref(nt.AutoFixMaxAttempts, 0),
	})
	s.autoFixOutcome(t.ProjectID, store.AutoFixNeedsYou)
}

func (s *Scheduler) autoFixOutcome(project, outcome string) {
	if s.Hooks.AutoFixOutcome != nil {
		s.Hooks.AutoFixOutcome(project, outcome)
	}
}

// ResolveHeld pushes ("push anyway") or discards a held fix.
func (s *Scheduler) ResolveHeld(ctx context.Context, taskID int64, push bool) error {
	t, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if t.AutoFixState == nil || *t.AutoFixState != store.AutoFixHeld || t.Status != tasks.PROpen {
		return fmt.Errorf("%w: no fix waits for approval", store.ErrConflict)
	}
	if !push {
		if err := s.host(t).RemoveWorkspace(ctx, taskID); err != nil {
			if hosts.IsOffline(err) {
				return err
			}
			return fmt.Errorf("discard the fix: %w", err)
		}
		nt, err := s.Store.SetAutoFixState(ctx, taskID, []string{store.AutoFixHeld}, store.AutoFixNeedsYou)
		if err != nil {
			return err
		}
		s.Hub.PublishTask(nt)
		s.addEvent(ctx, taskID, nil, "autofix", map[string]any{"phase": "discarded", "attempt": nt.AutoFixAttempts})
		return nil
	}
	nt, err := s.Store.SetAutoFixState(ctx, taskID, []string{store.AutoFixHeld}, store.AutoFixApproved)
	if err != nil {
		return err
	}
	s.Hub.PublishTask(nt)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		bg := context.WithoutCancel(ctx)
		s.afterRun(bg, nt, "CI auto-fix (pushed with the owner's approval).", 0, "")
		if cur, err := s.Store.GetTask(bg, taskID); err == nil && deref(cur.AutoFixState, "") == store.AutoFixApproved {
			// The push did not happen (e.g. rejected): the loop is over.
			s.stopAutoFix(bg, cur, "the approved fix could not be pushed")
		}
	}()
	return nil
}

// recordPush notes the commit about to be pushed, so CI results of any
// other commit are taken for a human push.
func (s *Scheduler) recordPush(ctx context.Context, t *store.Task) {
	sha, err := s.Store.LastCommittedSHA(ctx, t.ID)
	if err == nil && sha != "" {
		err = s.Store.SetPushedSHA(ctx, t.ID, sha)
	}
	if err != nil {
		s.log(t, 0).Warn("record pushed commit", "err", err)
	}
}

// afterPush moves the loop on once a push succeeded: a fix waits for CI; any
// other push starts afresh.
func (s *Scheduler) afterPush(ctx context.Context, t *store.Task, fix *store.Run) {
	state := deref(t.AutoFixState, "")
	if fix == nil && state != store.AutoFixApproved {
		if state != "" {
			_ = s.Store.ResetAutoFix(ctx, t.ID)
		}
		return
	}
	nt, err := s.Store.SetAutoFixState(ctx, t.ID, []string{store.AutoFixFixing, store.AutoFixApproved}, store.AutoFixWaiting)
	if errors.Is(err, store.ErrConflict) {
		return // stopped meanwhile (limit, cancel)
	}
	if err != nil {
		s.log(t, 0).Error("auto-fix pushed", "err", err)
		return
	}
	s.Hub.PublishTask(nt)
	s.addEvent(ctx, t.ID, nil, "autofix", map[string]any{
		"phase": "pushed", "attempt": nt.AutoFixAttempts, "max_attempts": deref(nt.AutoFixMaxAttempts, 0),
	})
}
