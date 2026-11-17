package githubapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v92/github"

	"github.com/andrewsl105/fleetpocket/server/internal/api"
	"github.com/andrewsl105/fleetpocket/server/internal/autofix"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

// Client is what the publisher, the projects picker, merges and CI auto-fix
// need from GitHub. *App (server mode, a GitHub App with installation
// tokens) and *UserClient (local mode, the owner's own token) implement it.
// installationID is ignored by UserClient.
type Client interface {
	Token(ctx context.Context, fullName string, installationID int64) (string, error)
	ListRepositories(ctx context.Context) ([]api.GitHubRepo, error)
	FindOpenPR(ctx context.Context, fullName string, installationID int64, branch string) (*PR, error)
	CreatePR(ctx context.Context, fullName string, installationID int64, head, base, title, body string) (*PR, error)
	Comment(ctx context.Context, fullName string, installationID int64, number int, body string) error
	MergePR(ctx context.Context, fullName string, installationID int64, number int, branch, title string) error
	CheckRuns(ctx context.Context, fullName string, installationID int64, sha string) ([]autofix.Check, error)
	JobLog(ctx context.Context, fullName string, installationID int64, jobID int64) (string, []string, error)
}

var (
	_ Client = (*App)(nil)
	_ Client = (*UserClient)(nil)
)

// ErrNotConnected: local mode without a GitHub token. It wraps
// tasks.ErrGitHubDisabled, so the API answers 503 github_disabled.
var ErrNotConnected = fmt.Errorf("%w: connect GitHub in Settings to push and open PRs", tasks.ErrGitHubDisabled)

// UserClient talks to GitHub with the owner's own token (local mode: OAuth
// device flow, `gh auth token` or a fine-grained PAT). The token lives in the
// OS keychain, is read per call by this process and never reaches a runner.
type UserClient struct {
	*App
}

// NewUserClient returns a client whose token comes from token (which returns
// "" when GitHub is not connected). apiURL overrides api.github.com (tests).
func NewUserClient(token func(ctx context.Context) (string, error), apiURL string) *UserClient {
	a := &App{apiURL: strings.TrimRight(apiURL, "/")}
	a.userToken = func(ctx context.Context) (string, error) {
		tok, err := token(ctx)
		if err != nil {
			return "", err
		}
		if tok == "" {
			return "", ErrNotConnected
		}
		return tok, nil
	}
	return &UserClient{App: a}
}

// Connected reports whether a token is stored.
func (u *UserClient) Connected(ctx context.Context) bool {
	_, err := u.userToken(ctx)
	return err == nil
}

// Identity is who a token belongs to and what it may do.
type Identity struct {
	Login string `json:"login"`
	// Scopes are the classic OAuth scopes (X-OAuth-Scopes); empty for a
	// fine-grained token, whose permissions GitHub does not list.
	Scopes []string `json:"scopes"`
}

// Whoami checks token against GET /user.
func (u *UserClient) Whoami(ctx context.Context, token string) (*Identity, error) {
	c, err := u.newClient(github.WithAuthToken(token))
	if err != nil {
		return nil, err
	}
	user, resp, err := c.Users.Get(ctx, "")
	if err != nil {
		var ge *github.ErrorResponse
		if errors.As(err, &ge) && ge.Response != nil && ge.Response.StatusCode == http.StatusUnauthorized {
			return nil, errors.New("GitHub rejected the token (401)")
		}
		return nil, err
	}
	id := &Identity{Login: user.GetLogin(), Scopes: []string{}}
	for _, s := range strings.Split(resp.Header.Get("X-OAuth-Scopes"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			id.Scopes = append(id.Scopes, s)
		}
	}
	return id, nil
}

// listUserRepositories lists the repositories the owner's token can push to.
func (a *App) listUserRepositories(ctx context.Context) ([]api.GitHubRepo, error) {
	tok, err := a.userToken(ctx)
	if err != nil {
		return nil, err
	}
	c, err := a.newClient(github.WithAuthToken(tok))
	if err != nil {
		return nil, err
	}
	var out []api.GitHubRepo
	opt := &github.RepositoryListByAuthenticatedUserOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		repos, resp, err := c.Repositories.ListByAuthenticatedUser(ctx, opt)
		if err != nil {
			return nil, a.fail("list_repos", err)
		}
		for _, r := range repos {
			if !r.GetPermissions().GetPush() {
				continue
			}
			out = append(out, api.GitHubRepo{FullName: r.GetFullName(), DefaultBranch: r.GetDefaultBranch(), Private: r.GetPrivate()})
		}
		if resp.NextPage == 0 || len(out) >= 1000 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}
