package setup

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v92/github"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// DefaultPEMPath is where the server container sees
// /srv/fleetpocket/secrets/github-app.pem (docs/DEPLOY.md).
const DefaultPEMPath = "/secrets/github-app.pem"

// Manifest is a GitHub App manifest
// (docs.github.com: "Registering a GitHub App from a manifest").
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	HookAttributes     HookAttributes    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	SetupURL           string            `json:"setup_url"`
	Description        string            `json:"description"`
	Public             bool              `json:"public"`
	DefaultEvents      []string          `json:"default_events"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}

type HookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// NewManifest describes the App Jungle Monkey needs: the permissions and
// events of docs/DEPLOY.md section 5, used by internal/githubapp.
func NewManifest(publicBaseURL string) Manifest {
	base := strings.TrimRight(publicBaseURL, "/")
	return Manifest{
		Name:           "fleetpocket-" + strings.ToLower(randomString(6)),
		URL:            base,
		HookAttributes: HookAttributes{URL: base + "/webhooks/github", Active: true},
		RedirectURL:    base + "/api/v1/setup/github/callback",
		SetupURL:       base + "/setup?step=repo",
		Description:    "Jungle Monkey: coding agents that open pull requests.",
		Public:         false,
		DefaultEvents:  []string{"pull_request", "check_suite", "workflow_run"},
		DefaultPermissions: map[string]string{
			"contents":      "write",
			"pull_requests": "write",
			"metadata":      "read",
			"checks":        "read",
			"actions":       "read",
		},
	}
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}

// GitHub organization names: alphanumerics and single hyphens, max 39.
var orgName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9]|-[A-Za-z0-9]){0,38}$`)

var ErrBadOrganization = errors.New("invalid organization name")

// ManifestFormURL is where the browser POSTs the manifest: the personal
// account, or an organization when org is set.
func ManifestFormURL(org, state string) (string, error) {
	u := "https://github.com/settings/apps/new"
	if org != "" {
		if !orgName.MatchString(org) {
			return "", ErrBadOrganization
		}
		u = "https://github.com/organizations/" + org + "/settings/apps/new"
	}
	return u + "?state=" + url.QueryEscape(state), nil
}

// CreatedApp is what Jungle Monkey keeps from the manifest conversion.
type CreatedApp struct {
	ID            int64
	Slug          string
	WebhookSecret string
	PEM           string
	HTMLURL       string
}

// InstallURL is the App's "install on repositories" page.
func (a *CreatedApp) InstallURL() string {
	return strings.TrimRight(a.HTMLURL, "/") + "/installations/new"
}

// ConvertManifest exchanges the redirect's code for the new App's
// credentials (POST /app-manifests/{code}/conversions, no auth needed).
// apiURL overrides api.github.com in tests.
func ConvertManifest(ctx context.Context, apiURL, code string) (*CreatedApp, error) {
	opts := []github.ClientOptionsFunc{github.WithTimeout(30 * time.Second)}
	if apiURL != "" {
		base := strings.TrimRight(apiURL, "/") + "/"
		opts = append(opts, github.WithURLs(&base, &base))
	}
	c, err := github.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	cfg, _, err := c.Apps.CompleteAppManifest(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("github manifest conversion: %w", err)
	}
	a := &CreatedApp{
		ID: cfg.GetID(), Slug: cfg.GetSlug(), WebhookSecret: cfg.GetWebhookSecret(),
		PEM: cfg.GetPEM(), HTMLURL: cfg.GetHTMLURL(),
	}
	if a.ID == 0 || a.Slug == "" || a.WebhookSecret == "" || a.PEM == "" || a.HTMLURL == "" {
		return nil, errors.New("github manifest conversion: incomplete response")
	}
	return a, nil
}

// WritePEM writes the App's private key with mode 0600. It writes in place:
// in the deploy compose file the key is a single-file bind mount, which
// cannot be replaced by a rename.
func WritePEM(path, pem string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteString(pem); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Settings returns the settings rows that record a created App.
func (a *CreatedApp) Settings() map[string]string {
	return map[string]string{
		store.SettingGitHubAppID:         strconv.FormatInt(a.ID, 10),
		store.SettingGitHubAppSlug:       a.Slug,
		store.SettingGitHubWebhookSecret: a.WebhookSecret,
	}
}

// ResolveGitHub picks the App ID and webhook secret: environment values win,
// each on its own, over the wizard's settings rows.
func ResolveGitHub(envAppID int64, envSecret string, settings map[string]string) (int64, string) {
	id, secret := envAppID, envSecret
	if id == 0 {
		id, _ = strconv.ParseInt(settings[store.SettingGitHubAppID], 10, 64)
	}
	if secret == "" {
		secret = settings[store.SettingGitHubWebhookSecret]
	}
	return id, secret
}
