package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/go-github/v92/github"

	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

// CIFailure describes a failed check for the OnCIFailed hook.
type CIFailure struct {
	Task *store.Task
	Name string
	URL  string
	// HeadSHA is the commit the checks ran on.
	HeadSHA string
}

// CIUpdate is a CI status change that is not a failure (pending, success),
// for the OnCI hook.
type CIUpdate struct {
	Task    *store.Task
	Status  string // pending | success
	HeadSHA string
}

// Webhook receives GitHub events at POST /webhooks/github.
type Webhook struct {
	Secret []byte
	Store  *store.Store
	Hub    *events.Hub
	Tasks  *tasks.Service
	// Cleanup removes a task's containers once its PR is merged or closed.
	Cleanup func(ctx context.Context, taskID int64)

	// OnCIFailed is called once per failed check suite or workflow run of a
	// task's branch (CI auto-fix, internal/cifix), after the result is
	// recorded. It must not block: the delivery times out after 10 s.
	OnCIFailed func(ctx context.Context, f CIFailure)
	// OnCI is called for the other CI status changes (new checks started,
	// a suite passed).
	OnCI func(ctx context.Context, u CIUpdate)
	// OnPRClosed is called when a task's PR is closed or merged.
	OnPRClosed func(ctx context.Context, taskID int64)
}

func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	payload, err := github.ValidatePayload(r, h.Secret)
	if err != nil {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	typ := github.WebHookType(r)
	ev, err := github.ParseWebHook(typ, payload)
	if err != nil {
		// Unknown or unparsable event types are acknowledged and ignored.
		w.WriteHeader(http.StatusOK)
		return
	}
	ctx := r.Context()
	switch e := ev.(type) {
	case *github.PullRequestEvent:
		err = h.pullRequest(ctx, e)
	case *github.CheckSuiteEvent:
		cs := e.GetCheckSuite()
		err = h.ci(ctx, e.GetRepo().GetFullName(), cs.GetHeadBranch(), cs.GetHeadSHA(), e.GetAction(), cs.GetStatus(), cs.GetConclusion(),
			cs.GetApp().GetName(), "")
	case *github.WorkflowRunEvent:
		wr := e.GetWorkflowRun()
		err = h.ci(ctx, e.GetRepo().GetFullName(), wr.GetHeadBranch(), wr.GetHeadSHA(), e.GetAction(), wr.GetStatus(), wr.GetConclusion(),
			wr.GetName(), wr.GetHTMLURL())
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.Error("webhook", "type", typ, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Webhook) pullRequest(ctx context.Context, e *github.PullRequestEvent) error {
	if e.GetAction() != "closed" {
		return nil
	}
	pr := e.GetPullRequest()
	t, err := h.Store.TaskByPR(ctx, e.GetRepo().GetFullName(), pr.GetNumber())
	if err != nil {
		return err
	}
	return h.closePR(ctx, t, pr.GetNumber(), pr.GetMerged())
}

// closePR records that task t's PR was closed or merged and completes the
// task (webhook, or the local-mode poller).
func (h *Webhook) closePR(ctx context.Context, t *store.Task, number int, merged bool) error {
	// A merge from Fleetpocket (tasks.Service.Merge) has already recorded it.
	if !h.closedRecorded(ctx, t.ID, number) {
		h.event(ctx, t.ID, "pr_closed", map[string]any{"number": number, "merged": merged})
	}
	if h.OnPRClosed != nil {
		h.OnPRClosed(ctx, t.ID)
	}
	if tasks.IsTerminal(t.Status) {
		return nil
	}
	reason := "PR closed without merging"
	if merged {
		reason = "PR merged"
	}
	if h.Cleanup != nil {
		h.Cleanup(ctx, t.ID)
	}
	if _, err := h.Tasks.SetStatus(ctx, t.ID, tasks.Completed, reason); err != nil && !errors.Is(err, store.ErrConflict) {
		return err
	}
	return nil
}

// closedRecorded reports whether the task's latest PR event is already a
// pr_closed for number.
func (h *Webhook) closedRecorded(ctx context.Context, taskID int64, number int) bool {
	evs, err := h.Store.LastEvents(ctx, taskID, []string{"pr_opened", "pr_updated", "pr_closed"}, 1)
	if err != nil || len(evs) == 0 || evs[0].Type != "pr_closed" {
		return false
	}
	var p struct {
		Number int `json:"number"`
	}
	return json.Unmarshal(evs[0].Payload, &p) == nil && p.Number == number
}

// CIStatus maps GitHub's action/status/conclusion to pending|success|failure.
// Returns "" for events that do not change the status.
func CIStatus(action, status, conclusion string) string {
	switch action {
	case "requested", "rerequested", "in_progress":
		return "pending"
	case "completed":
	default:
		return ""
	}
	if status != "" && status != "completed" {
		return "pending"
	}
	switch conclusion {
	case "success", "neutral", "skipped":
		return "success"
	case "failure", "timed_out", "cancelled", "action_required", "startup_failure", "stale":
		return "failure"
	}
	return ""
}

func (h *Webhook) ci(ctx context.Context, repo, branch, sha, action, status, conclusion, name, url string) error {
	ci := CIStatus(action, status, conclusion)
	if ci == "" || branch == "" {
		return nil
	}
	t, err := h.Store.TaskByBranch(ctx, repo, branch)
	if err != nil {
		return err
	}
	return h.ciTask(ctx, t, sha, action, status, conclusion, name, url)
}

// ciTask records a CI status change of task t's branch and calls the
// auto-fix hooks (webhook, or the local-mode poller).
func (h *Webhook) ciTask(ctx context.Context, t *store.Task, sha, action, status, conclusion, name, url string) error {
	ci := CIStatus(action, status, conclusion)
	if ci == "" {
		return nil
	}
	// A failure sticks until the next push starts new checks (pending).
	if ci == "success" && t.CIStatus != nil && *t.CIStatus == "failure" {
		ci = "failure"
	} else if err := h.Store.SetCIStatus(ctx, t.ID, ci); err != nil {
		return err
	}
	if ci != "pending" || action == "requested" {
		h.event(ctx, t.ID, "ci", map[string]any{"status": CIStatus(action, status, conclusion), "name": name, "url": url})
	}
	if nt, err := h.Store.GetTask(ctx, t.ID); err == nil {
		h.Hub.PublishTask(nt)
	}
	switch raw := CIStatus(action, status, conclusion); {
	case raw == "failure" && h.OnCIFailed != nil:
		h.OnCIFailed(ctx, CIFailure{Task: t, Name: name, URL: url, HeadSHA: sha})
	case raw != "failure" && h.OnCI != nil:
		h.OnCI(ctx, CIUpdate{Task: t, Status: raw, HeadSHA: sha})
	}
	return nil
}

func (h *Webhook) event(ctx context.Context, taskID int64, typ string, payload any) {
	ev, err := h.Store.AddBackendEvent(ctx, taskID, nil, typ, payload)
	if err != nil {
		slog.Error("webhook event", "task_id", taskID, "err", err)
		return
	}
	h.Hub.PublishEvent(ev)
}
