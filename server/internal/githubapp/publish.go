package githubapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/andrewsl105/fleetpocket/server/internal/costs"
	"github.com/andrewsl105/fleetpocket/server/internal/scheduler"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// Publisher implements scheduler.Publisher: validate, push, open or update the PR.
// The workspace operations run on the task's host (PublishInput.Host).
type Publisher struct {
	// App is the GitHub client: the GitHub App (server mode) or the owner's
	// token (local mode, UserClient).
	App Client
	// Connected reports whether GitHub can be used right now; nil means
	// always (server mode). Local mode: a token is stored.
	Connected func() bool
	// RequireScanner fails the publish when the host cannot run the secrets
	// scan (SECRETS_SCAN=gitleaks).
	RequireScanner bool
	// TaskURL links back to the task in the web app.
	TaskURL func(taskID int64) string
}

// Enabled implements scheduler.Enabler.
func (p *Publisher) Enabled() bool { return p.Connected == nil || p.Connected() }

func (p *Publisher) Publish(ctx context.Context, in scheduler.PublishInput) (*scheduler.PublishResult, error) {
	t, repo, h := in.Task, in.Repo, in.Host
	if err := h.Validate(ctx, t.ID, repo.BaseBranch, p.RequireScanner); err != nil {
		if errors.Is(err, workspace.ErrInvalidDiff) {
			return nil, fmt.Errorf("%w: %v", scheduler.ErrRejected, err)
		}
		return nil, err
	}
	wsRepo := scheduler.WorkspaceRepo(repo, t)
	forcedFrom := ""
	if in.Force {
		// A restore discarded commits that were already pushed.
		old, err := h.ForcePush(ctx, t.ID, wsRepo, in.Branch)
		if errors.Is(err, workspace.ErrNotAgentBranch) {
			return nil, fmt.Errorf("%w: a restore rewrote branch %s, which Jungle Monkey did not create, so it is not force-pushed", scheduler.ErrRejected, in.Branch)
		}
		if err != nil {
			return nil, fmt.Errorf("force push (with lease): %w", err)
		}
		forcedFrom = old
	} else if err := h.Push(ctx, t.ID, wsRepo, in.Branch); err != nil {
		return nil, fmt.Errorf("push: %w", err)
	}
	if in.NoPR {
		return &scheduler.PublishResult{ForcedFrom: forcedFrom}, nil
	}
	stat, _ := h.DiffStat(ctx, t.ID, repo.BaseBranch)

	number := 0
	if t.PRNumber != nil {
		number = *t.PRNumber
	} else if pr, err := p.App.FindOpenPR(ctx, repo.FullName, repo.InstallationID, in.Branch); err != nil {
		return nil, err
	} else if pr != nil {
		number = pr.Number // created before a restart
	}
	if number == 0 {
		pr, err := p.App.CreatePR(ctx, repo.FullName, repo.InstallationID, in.Branch, repo.BaseBranch, t.Title, p.body(in, stat))
		if err != nil {
			return nil, err
		}
		return &scheduler.PublishResult{PRNumber: pr.Number, PRURL: pr.URL, Created: true, ForcedFrom: forcedFrom}, nil
	}
	if err := p.App.Comment(ctx, repo.FullName, repo.InstallationID, number, p.comment(in, stat, forcedFrom)); err != nil {
		return nil, err
	}
	url := ""
	if t.PRURL != nil {
		url = *t.PRURL
	} else {
		url = fmt.Sprintf("https://github.com/%s/pull/%d", repo.FullName, number)
	}
	return &scheduler.PublishResult{PRNumber: number, PRURL: url, ForcedFrom: forcedFrom}, nil
}

func (p *Publisher) body(in scheduler.PublishInput, stat string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Task\n\n%s\n\n", quote(in.Task.Prompt))
	if in.RequestedBy != "" {
		fmt.Fprintf(&b, "Requested by %s\n\n", in.RequestedBy)
	}
	if in.Summary != "" {
		fmt.Fprintf(&b, "### Summary\n\n%s\n\n", in.Summary)
	}
	if in.Limit != "" {
		fmt.Fprintf(&b, "> [!WARNING]\n> The run stopped at a limit: %s. The changes may be incomplete.\n\n", in.Limit)
	}
	if stat = strings.TrimSpace(stat); stat != "" {
		fmt.Fprintf(&b, "### Files changed\n\n```\n%s\n```\n\n", stat)
	}
	fmt.Fprintf(&b, "Cost: %s · Turns: %d", costs.Format(in.CostUSD, in.Task.CostEstimated, in.Task.CostUnknown), in.Turns)
	if p.TaskURL != nil {
		if u := p.TaskURL(in.Task.ID); u != "" {
			fmt.Fprintf(&b, " · [Task #%d](%s)", in.Task.ID, u)
		}
	}
	b.WriteString("\n\n<sub>Opened by Jungle Monkey.</sub>\n")
	return b.String()
}

func (p *Publisher) comment(in scheduler.PublishInput, stat, forcedFrom string) string {
	var b strings.Builder
	b.WriteString("### New run pushed\n\n")
	if forcedFrom != "" {
		fmt.Fprintf(&b, "> [!NOTE]\n> The owner restored this task to an earlier checkpoint, so Jungle Monkey rewrote `%s` with `--force-with-lease` (it was at %s). The commits after the checkpoint are no longer on this branch.\n\n", in.Branch, shortSHA(forcedFrom))
	}
	if in.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n", in.Summary)
	}
	if in.Limit != "" {
		fmt.Fprintf(&b, "Stopped at a limit: %s.\n\n", in.Limit)
	}
	if stat = strings.TrimSpace(stat); stat != "" {
		fmt.Fprintf(&b, "<details><summary>Files changed (whole PR)</summary>\n\n```\n%s\n```\n</details>\n\n", stat)
	}
	fmt.Fprintf(&b, "Task cost so far: %s", costs.Format(in.CostUSD, in.Task.CostEstimated, in.Task.CostUnknown))
	return b.String()
}

func quote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
