// Package cifix runs the CI auto-fix loop (docs/SPEC-server.md "CI
// auto-fix"): when a check fails on the commit Jungle Monkey last pushed to a
// task's PR, it fetches the failed checks' logs on the server (installation
// token; the runner never gets one), attaches them to the task and starts a
// follow-up run that fixes the cause. It stops when CI is green or hands the
// task back to the owner. The decisions are in internal/autofix.
package cifix

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/andrewsl105/fleetpocket/server/internal/attachments"
	"github.com/andrewsl105/fleetpocket/server/internal/autofix"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/githubapp"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

// GitHub reads a commit's checks and job logs (githubapp.App).
type GitHub interface {
	CheckRuns(ctx context.Context, fullName string, installationID int64, sha string) ([]autofix.Check, error)
	JobLog(ctx context.Context, fullName string, installationID int64, jobID int64) (string, []string, error)
}

// Holder pushes or discards a fix that waits for the owner (the scheduler).
type Holder interface {
	ResolveHeld(ctx context.Context, taskID int64, push bool) error
}

// Outcomes counted by the Outcome hook.
const (
	OutcomeGreen     = "green"
	OutcomeNeedsYou  = "needs_you"
	OutcomeCancelled = "cancelled"
)

type Service struct {
	Store  *store.Store
	Hub    *events.Hub
	GitHub GitHub
	// Files stores the log attachment; nil starts the run without it.
	Files *attachments.Files
	// Wake nudges the scheduler once an attempt is queued.
	Wake func()
	// Holder resolves the diff-guard question; nil rejects it.
	Holder Holder
	// DefaultBudgetUSD is the task budget of projects without their own.
	DefaultBudgetUSD float64
	// Attempt and Outcome observe the loop (metrics).
	Attempt func(project string)
	Outcome func(project, outcome string)

	mu    sync.Mutex
	locks map[int64]*sync.Mutex
	wg    sync.WaitGroup
}

// Failed is the webhook's OnCIFailed hook. The work (GitHub calls) runs in
// the background so the delivery is acknowledged in time.
func (s *Service) Failed(ctx context.Context, f githubapp.CIFailure) {
	s.background(ctx, f.Task.ID, func(ctx context.Context) error { return s.Evaluate(ctx, f.Task.ID, f.HeadSHA, true) })
}

// Changed is the webhook's OnCI hook: new checks on a commit Jungle Monkey
// did not push cancel the loop (a human push); a passing suite may be the
// last one the loop waited for.
func (s *Service) Changed(ctx context.Context, u githubapp.CIUpdate) {
	switch u.Status {
	case "pending":
		if u.HeadSHA != "" && u.Task.LastPushedSHA != nil && u.HeadSHA != *u.Task.LastPushedSHA {
			s.Cancel(ctx, u.Task.ID, "a commit was pushed to the branch by someone else")
		}
	case "success":
		s.background(ctx, u.Task.ID, func(ctx context.Context) error { return s.Evaluate(ctx, u.Task.ID, u.HeadSHA, false) })
	}
}

// PRClosed is the webhook's OnPRClosed hook.
func (s *Service) PRClosed(ctx context.Context, taskID int64) {
	s.Cancel(ctx, taskID, "the PR was closed")
}

// Wait blocks until background evaluations are done (tests, shutdown).
func (s *Service) Wait() { s.wg.Wait() }

// background runs fn for a task, one at a time per task: the deliveries of
// one CI result arrive together.
func (s *Service) background(ctx context.Context, taskID int64, fn func(context.Context) error) {
	ctx = context.WithoutCancel(ctx)
	m := s.taskLock(taskID)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		m.Lock()
		defer m.Unlock()
		if err := fn(ctx); err != nil {
			slog.Warn("ci auto-fix", "task_id", taskID, "err", err)
		}
	}()
}

func (s *Service) taskLock(taskID int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[int64]*sync.Mutex{}
	}
	m := s.locks[taskID]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[taskID] = m
	}
	return m
}

// over are loop states that end it until Jungle Monkey pushes again.
func over(state string) bool {
	switch state {
	case store.AutoFixHeld, store.AutoFixNeedsYou, store.AutoFixCancelled, store.AutoFixGreen:
		return true
	}
	return false
}

// Evaluate looks at the checks of sha, the head of a task's branch, after a
// CI result: when they are all done it stops the loop as green, or starts
// the next fix attempt, or hands the task back. failure tells whether a
// failure triggered it (only then is a foreign head a human push).
func (s *Service) Evaluate(ctx context.Context, taskID int64, sha string, failure bool) error {
	t, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	state := deref(t.AutoFixState)
	if t.PRNumber == nil || t.LastPushedSHA == nil {
		return nil // not a PR Jungle Monkey opened
	}
	if sha == "" || sha != *t.LastPushedSHA {
		if failure {
			s.Cancel(ctx, taskID, "a commit was pushed to the branch by someone else")
		}
		return nil
	}
	if over(state) || t.Status != tasks.PROpen {
		return nil // stopped, or a run is active (its push restarts CI)
	}
	project, err := s.Store.GetProject(ctx, t.ProjectID)
	if err != nil {
		return err
	}
	settings, err := s.Store.AutoFixSettings(ctx, project)
	if err != nil || !settings.Enabled {
		return err
	}
	repo, err := s.Store.GetRepository(ctx, t.RepositoryID)
	if err != nil {
		return err
	}
	checks, err := s.GitHub.CheckRuns(ctx, repo.FullName, repo.InstallationID, sha)
	if err != nil {
		return err
	}
	pending, failed, green := autofix.Summarize(settings, checks)
	switch {
	case pending:
		return nil // the last suite to finish decides
	case green:
		if state == store.AutoFixWaiting {
			s.green(ctx, t)
		}
		return nil
	case len(failed) == 0:
		return nil // only checks outside ci_autofix.checks failed
	}
	if ok, err := s.Store.ClaimAutoFixSHA(ctx, taskID, sha); err != nil || !ok {
		return err
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].Name < failed[j].Name })
	logs := s.logs(ctx, repo, failed)
	text, sig := autofix.Combine(logs)

	attempts := t.AutoFixAttempts
	if state == "" {
		attempts = 0 // a new loop
	}
	outcome, reason := autofix.Decide(settings, autofix.State{
		OpenedBySystem: true, HeadSHA: sha, LastPushedSHA: sha,
		Attempts: attempts, LastSignature: deref(t.AutoFixSignature),
	}, failed[0].Name, sig)
	switch outcome {
	case autofix.Fix:
		return s.start(ctx, t, project, settings, attempts+1, failed, sig, text)
	case autofix.NeedsYou:
		s.needsYou(ctx, t, reason, failed, sig)
	}
	return nil
}

// logs fetches and trims each failed check's log. GitHub Actions jobs have
// downloadable logs; other apps' checks only their output text.
func (s *Service) logs(ctx context.Context, repo *store.Repository, failed []autofix.Check) []autofix.FailureLog {
	out := make([]autofix.FailureLog, 0, len(failed))
	for _, c := range failed {
		l := autofix.FailureLog{Check: c}
		raw := c.Output
		if c.App == "github-actions" {
			log, steps, err := s.GitHub.JobLog(ctx, repo.FullName, repo.InstallationID, c.ID)
			l.FailedSteps = steps
			if err != nil {
				slog.Warn("ci auto-fix: job log", "repo", repo.FullName, "check", c.Name, "err", err)
				raw = strings.TrimSpace(raw + "\n(job log unavailable: " + err.Error() + ")")
			} else {
				raw = log
			}
		}
		l.Log = autofix.TrimLog(raw, autofix.TailLines)
		out = append(out, l)
	}
	return out
}

func checkList(cs []autofix.Check) []map[string]any {
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, map[string]any{"name": c.Name, "url": c.URL, "conclusion": c.Conclusion})
	}
	return out
}

func checkNames(cs []autofix.Check) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

// start queues fix attempt n with the trimmed logs attached.
func (s *Service) start(ctx context.Context, t *store.Task, p *store.Project, settings autofix.Settings, n int,
	failed []autofix.Check, sig, logText string) error {
	name := fmt.Sprintf("ci-logs-attempt-%d.txt", n)
	var up *store.Upload
	if s.Files != nil {
		f, err := attachments.Process(name, []byte(logText))
		if err != nil {
			return err
		}
		release := s.Files.Hold()
		defer release()
		up = &store.Upload{
			Items: []store.NewAttachment{{Name: f.Name, Mime: f.Mime, Size: int64(len(f.Data))}},
			Place: s.Files.Place([]*attachments.Image{f}),
		}
	}
	taskBudget := s.DefaultBudgetUSD
	if p.MaxBudgetUSD != nil {
		taskBudget = *p.MaxBudgetUSD
	}
	nt, ev, err := s.Store.StartAutoFix(ctx, store.AutoFixStart{
		TaskID: t.ID, Attempt: n, MaxAttempts: settings.Attempts(), Signature: sig,
		BudgetUSD: settings.Budget(taskBudget),
		Input:     autofix.Instruction(n, settings.Attempts(), checkNames(failed), sig, name),
		Payload: map[string]any{
			"phase": "started", "attempt": n, "max_attempts": settings.Attempts(),
			"checks": checkList(failed), "signature": sig, "head_sha": deref(t.LastPushedSHA),
		},
		Upload: up,
	})
	if errors.Is(err, store.ErrConflict) {
		return nil // a run started meanwhile
	}
	if err != nil {
		return err
	}
	s.Hub.PublishTask(nt)
	s.Hub.PublishEvent(ev)
	if s.Attempt != nil {
		s.Attempt(t.ProjectID)
	}
	if s.Wake != nil {
		s.Wake()
	}
	return nil
}

func (s *Service) needsYou(ctx context.Context, t *store.Task, reason string, failed []autofix.Check, sig string) {
	nt, err := s.Store.SetAutoFixState(ctx, t.ID, append([]string{""}, store.AutoFixActive...), store.AutoFixNeedsYou)
	if err != nil {
		return
	}
	s.Hub.PublishTask(nt)
	s.event(ctx, t.ID, map[string]any{
		"phase": "needs_you", "reason": reason, "attempt": nt.AutoFixAttempts,
		"max_attempts": deref(nt.AutoFixMaxAttempts), "checks": checkList(failed), "signature": sig,
	})
	s.outcome(t.ProjectID, OutcomeNeedsYou)
}

func (s *Service) green(ctx context.Context, t *store.Task) {
	nt, err := s.Store.SetAutoFixState(ctx, t.ID, []string{store.AutoFixWaiting}, store.AutoFixGreen)
	if err != nil {
		return
	}
	s.Hub.PublishTask(nt)
	s.event(ctx, t.ID, map[string]any{"phase": "green", "fixes": nt.AutoFixAttempts})
	s.outcome(t.ProjectID, OutcomeGreen)
}

// Cancel ends an active loop (a human push, a manual follow-up, the PR
// closed). A fix run that is already running finishes and pushes as usual.
func (s *Service) Cancel(ctx context.Context, taskID int64, reason string) {
	nt, err := s.Store.SetAutoFixState(ctx, taskID, store.AutoFixActive, store.AutoFixCancelled)
	if err != nil {
		return
	}
	// A pending diff-guard question goes with it.
	_ = s.Store.CloseOpenQuestions(ctx, taskID, "")
	s.Hub.PublishTask(nt)
	s.event(ctx, taskID, map[string]any{"phase": "cancelled", "reason": reason})
	s.outcome(nt.ProjectID, OutcomeCancelled)
}

// Resolve applies the owner's answer to the diff-guard question.
func (s *Service) Resolve(ctx context.Context, taskID int64, push bool) error {
	if s.Holder == nil {
		return fmt.Errorf("%w: pushing is not configured", tasks.ErrConflict)
	}
	return s.Holder.ResolveHeld(ctx, taskID, push)
}

func (s *Service) event(ctx context.Context, taskID int64, payload map[string]any) {
	ev, err := s.Store.AddBackendEvent(ctx, taskID, nil, "autofix", payload)
	if err != nil {
		slog.Error("ci auto-fix event", "task_id", taskID, "err", err)
		return
	}
	s.Hub.PublishEvent(ev)
}

func (s *Service) outcome(project, outcome string) {
	if s.Outcome != nil {
		s.Outcome(project, outcome)
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
