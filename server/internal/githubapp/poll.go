package githubapp

// Local mode has no public URL for webhooks, so the PRs of pr_open tasks are
// polled instead and fed into the same code paths as POST /webhooks/github
// (Webhook.closePR and Webhook.ciTask, which call OnCIFailed for CI
// auto-fix). Conditional requests (ETag / If-None-Match) keep unchanged
// answers free: a 304 does not count against the rate limit.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

// Poll intervals: the normal pace, and the most a run of errors backs off to.
const (
	PollInterval   = 60 * time.Second
	MaxPollBackoff = 15 * time.Minute
)

// Poller checks the PRs of pr_open tasks.
type Poller struct {
	Store  *store.Store
	Hook   *Webhook
	Token  func(ctx context.Context) (string, error)
	APIURL string // "" = https://api.github.com
	// Interval overrides PollInterval (tests).
	Interval time.Duration

	mu    sync.Mutex
	etags map[string]string
	cache map[string][]byte
	// seen is the last CI state fed per task ("<sha>:<state>"), so a state
	// is reported once.
	seen map[int64]string
}

// Run polls until ctx ends, backing off (doubling, up to MaxPollBackoff)
// while GitHub fails.
func (p *Poller) Run(ctx context.Context) {
	base := p.Interval
	if base == 0 {
		base = PollInterval
	}
	wait := base
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err := p.Poll(ctx); err != nil {
			wait = min(wait*2, MaxPollBackoff)
			slog.Warn("GitHub poll failed", "err", err, "retry_in", wait)
			continue
		}
		wait = base
	}
}

// Poll checks every pr_open task once.
func (p *Poller) Poll(ctx context.Context) error {
	tok, err := p.Token(ctx)
	if err != nil || tok == "" {
		return nil // not connected: nothing to poll
	}
	list, err := p.Store.TasksByStatus(ctx, tasks.PROpen)
	if err != nil {
		return err
	}
	var firstErr error
	for _, t := range list {
		if t.PRNumber == nil {
			continue
		}
		if err := p.pollTask(ctx, tok, t); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (p *Poller) pollTask(ctx context.Context, tok string, t *store.Task) error {
	repo, err := p.Store.GetRepository(ctx, t.RepositoryID)
	if err != nil {
		return err
	}
	full := repo.GitHubName()
	if full == "" {
		return nil
	}
	var pr struct {
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := p.get(ctx, tok, fmt.Sprintf("/repos/%s/pulls/%d", full, *t.PRNumber), &pr); err != nil {
		return err
	}
	if pr.State == "closed" {
		return p.Hook.closePR(ctx, t, *t.PRNumber, pr.Merged)
	}
	if pr.Head.SHA == "" {
		return nil
	}
	var runs struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
		} `json:"check_runs"`
	}
	if err := p.get(ctx, tok, fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", full, pr.Head.SHA), &runs); err != nil {
		return err
	}
	if len(runs.CheckRuns) == 0 {
		return nil // no CI on this repository
	}
	// One state per commit: pending while any run is unfinished, failure if
	// any finished run failed, else success.
	action, status, conclusion, name, url := "completed", "completed", "success", "", ""
	for _, r := range runs.CheckRuns {
		if r.Status != "completed" {
			action, status, conclusion, name, url = "in_progress", r.Status, "", r.Name, r.HTMLURL
			break
		}
		if CIStatus("completed", "completed", r.Conclusion) == "failure" && conclusion != "failure" {
			conclusion, name, url = r.Conclusion, r.Name, r.HTMLURL
		}
	}
	state := pr.Head.SHA + ":" + CIStatus(action, status, conclusion)
	p.mu.Lock()
	if p.seen == nil {
		p.seen = map[int64]string{}
	}
	same := p.seen[t.ID] == state
	p.seen[t.ID] = state
	p.mu.Unlock()
	if same {
		return nil
	}
	return p.Hook.ciTask(ctx, t, pr.Head.SHA, action, status, conclusion, name, url)
}

// get is a conditional GET: a 304 reuses the cached body.
func (p *Poller) get(ctx context.Context, tok, path string, out any) error {
	base := strings.TrimRight(p.APIURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	p.mu.Lock()
	if p.etags == nil {
		p.etags, p.cache = map[string]string{}, map[string][]byte{}
	}
	if et := p.etags[path]; et != "" {
		req.Header.Set("If-None-Match", et)
	}
	p.mu.Unlock()
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body []byte
	switch resp.StatusCode {
	case http.StatusNotModified:
		p.mu.Lock()
		body = p.cache[path]
		p.mu.Unlock()
	case http.StatusOK:
		if body, err = io.ReadAll(io.LimitReader(resp.Body, 8<<20)); err != nil {
			return err
		}
		p.mu.Lock()
		if et := resp.Header.Get("ETag"); et != "" {
			p.etags[path], p.cache[path] = et, body
		}
		p.mu.Unlock()
	default:
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			if s, _ := strconv.Atoi(resp.Header.Get("Retry-After")); s > 0 {
				return fmt.Errorf("GitHub rate limit: retry after %ds", s)
			}
		}
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return json.Unmarshal(body, out)
}
