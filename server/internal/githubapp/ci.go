package githubapp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v92/github"

	"github.com/andrewsl105/fleetpocket/server/internal/autofix"
)

// maxLogBytes bounds a downloaded job log; only its tail is kept.
const maxLogBytes = 8 << 20

// CheckRuns lists the latest check runs of a commit (CI auto-fix). It uses a
// repository-scoped installation token on the server; runners never see it.
func (a *App) CheckRuns(ctx context.Context, fullName string, installationID int64, sha string) ([]autofix.Check, error) {
	c, owner, name, err := a.repoClient(ctx, fullName, installationID)
	if err != nil {
		return nil, err
	}
	filter := "latest"
	opt := &github.ListCheckRunsOptions{Filter: &filter, ListOptions: github.ListOptions{PerPage: 100}}
	var out []autofix.Check
	for {
		res, resp, err := c.Checks.ListCheckRunsForRef(ctx, owner, name, sha, opt)
		if err != nil {
			return nil, a.fail("list_check_runs", fmt.Errorf("list check runs: %w", err))
		}
		for _, cr := range res.CheckRuns {
			ch := autofix.Check{
				ID: cr.GetID(), Name: cr.GetName(), Status: cr.GetStatus(), Conclusion: cr.GetConclusion(),
				URL: cr.GetHTMLURL(), App: cr.GetApp().GetSlug(),
			}
			if o := cr.GetOutput(); o != nil {
				ch.Output = strings.TrimSpace(strings.Join([]string{o.GetTitle(), o.GetSummary(), o.GetText()}, "\n"))
			}
			out = append(out, ch)
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}

// JobLog downloads the log of a GitHub Actions job (whose id is the id of its
// check run) and names its failed steps. The download URL GitHub redirects to
// is pre-signed, so it is fetched without the token.
func (a *App) JobLog(ctx context.Context, fullName string, installationID int64, jobID int64) (string, []string, error) {
	c, owner, name, err := a.repoClient(ctx, fullName, installationID)
	if err != nil {
		return "", nil, err
	}
	var failed []string
	if job, _, err := c.Actions.GetWorkflowJobByID(ctx, owner, name, jobID); err == nil {
		for _, s := range job.Steps {
			if s.GetConclusion() == "failure" {
				failed = append(failed, s.GetName())
			}
		}
	}
	u, _, err := c.Actions.GetWorkflowJobLogs(ctx, owner, name, jobID, 0)
	if err != nil {
		return "", failed, a.fail("job_logs", fmt.Errorf("job %d logs: %w", jobID, err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", failed, err
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", failed, a.fail("job_logs", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", failed, a.fail("job_logs", fmt.Errorf("job %d logs: %s", jobID, resp.Status))
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", failed, err
	}
	if len(b) > maxLogBytes {
		b = b[len(b)-maxLogBytes:]
	}
	return string(b), failed, nil
}
