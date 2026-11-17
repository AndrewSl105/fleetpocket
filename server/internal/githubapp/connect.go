package githubapp

// Connecting GitHub in local mode (docs/SPEC-server.md "GitHub in local
// mode"): the OAuth device flow of the Jungle Monkey OAuth App, the token of
// the GitHub CLI (`gh auth token`, only when the owner asks for it) or a
// pasted fine-grained personal access token. The token is stored in the OS
// keychain (0600 file fallback), read by this process only, never sent to a
// runner.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/andrewsl105/fleetpocket/server/internal/api"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DeviceScope is what the device flow asks for: `repo` is the narrowest
// classic scope that can push to a private repository, open PRs and read
// check runs (public_repo covers public repositories only).
const DeviceScope = "repo"

// TokenName is the secrets entry of the token (secrets.GitHubToken).
const TokenName = "GITHUB_TOKEN"

// TokenStore is the keychain (internal/secrets).
type TokenStore interface {
	Get(name string) string
	Set(name, value string) (fallback bool, err error)
	Delete(name string) error
}

// Connector owns the local-mode GitHub token.
type Connector struct {
	// ClientID is the OAuth App for the device flow ("" disables it).
	ClientID string
	// BaseURL is https://github.com (tests override it).
	BaseURL string
	Client  *UserClient
	Store   TokenStore
	// GHToken runs `gh auth token`; nil uses the gh binary on PATH.
	GHToken func(ctx context.Context) (string, error)
	// OnChange is called after the token changed (connect, disconnect).
	OnChange func()
	Now      func() time.Time

	mu       sync.Mutex
	flow     *deviceFlow
	identity *Identity
	idToken  string
	// token caches the keychain entry (loaded once; save/Disconnect update it).
	token  string
	loaded bool
}

type deviceFlow struct {
	deviceCode string
	code       *DeviceCode
	expires    time.Time
	interval   time.Duration
	next       time.Time
}

// DeviceCode is what the owner needs to authorize the device.
type DeviceCode = api.DeviceCode

// ConnStatus is GET /local/github.
type ConnStatus = api.GitHubConnStatus

func (c *Connector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Connector) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return "https://github.com"
}

// Token is the stored token ("" when not connected).
func (c *Connector) Token(context.Context) (string, error) { return c.stored(), nil }

func (c *Connector) stored() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		c.token, c.loaded = c.Store.Get(TokenName), true
	}
	return c.token
}

// Connected reports whether a token is stored.
func (c *Connector) Connected() bool { return c.stored() != "" }

// Status checks the stored token (cached per token).
func (c *Connector) Status(ctx context.Context) ConnStatus {
	st := ConnStatus{Scopes: []string{}, DeviceFlow: c.ClientID != "", GHCLI: ghInstalled()}
	tok := c.stored()
	if tok == "" {
		return st
	}
	c.mu.Lock()
	id, cachedFor := c.identity, c.idToken
	c.mu.Unlock()
	if id == nil || cachedFor != tok {
		var err error
		if id, err = c.Client.Whoami(ctx, tok); err != nil {
			st.Error = err.Error()
			return st
		}
		c.mu.Lock()
		c.identity, c.idToken = id, tok
		c.mu.Unlock()
	}
	st.Connected, st.Login, st.Scopes = true, id.Login, id.Scopes
	return st
}

func ghInstalled() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// post sends a form to github.com and decodes the JSON answer.
func (c *Connector) post(ctx context.Context, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	return json.Unmarshal(body, out)
}

// StartDevice begins the device flow: the owner opens VerificationURI and
// enters UserCode.
func (c *Connector) StartDevice(ctx context.Context) (*DeviceCode, error) {
	if c.ClientID == "" {
		return nil, errors.New("this build has no GitHub OAuth App client id (set JM_GITHUB_CLIENT_ID); use the GitHub CLI or a token instead")
	}
	var out struct {
		DeviceCode       string `json:"device_code"`
		UserCode         string `json:"user_code"`
		VerificationURI  string `json:"verification_uri"`
		ExpiresIn        int    `json:"expires_in"`
		Interval         int    `json:"interval"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := c.post(ctx, "/login/device/code", url.Values{"client_id": {c.ClientID}, "scope": {DeviceScope}}, &out); err != nil {
		return nil, err
	}
	if out.Error != "" || out.DeviceCode == "" {
		return nil, fmt.Errorf("GitHub refused the device flow: %s %s", out.Error, out.ErrorDescription)
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	code := &DeviceCode{UserCode: out.UserCode, VerificationURI: out.VerificationURI, ExpiresIn: out.ExpiresIn, Interval: out.Interval}
	c.mu.Lock()
	c.flow = &deviceFlow{
		deviceCode: out.DeviceCode, code: code,
		expires:  c.now().Add(time.Duration(out.ExpiresIn) * time.Second),
		interval: time.Duration(out.Interval) * time.Second,
	}
	c.mu.Unlock()
	return code, nil
}

// Device flow poll results.
const (
	DevicePending   = "pending"
	DeviceConnected = "connected"
	DeviceExpired   = "expired"
	DeviceDenied    = "denied"
	DeviceNone      = "none" // no flow started
)

// PollDevice asks GitHub once whether the owner authorized the device,
// respecting the interval (slow_down adds 5 s). On success the token is
// stored.
func (c *Connector) PollDevice(ctx context.Context) (string, error) {
	c.mu.Lock()
	f := c.flow
	if f == nil {
		c.mu.Unlock()
		return DeviceNone, nil
	}
	now := c.now()
	if now.After(f.expires) {
		c.flow = nil
		c.mu.Unlock()
		return DeviceExpired, nil
	}
	if now.Before(f.next) {
		c.mu.Unlock()
		return DevicePending, nil
	}
	f.next = now.Add(f.interval)
	c.mu.Unlock()

	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Interval    int    `json:"interval"`
	}
	err := c.post(ctx, "/login/oauth/access_token", url.Values{
		"client_id": {c.ClientID}, "device_code": {f.deviceCode},
		"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
	}, &out)
	if err != nil {
		return "", err
	}
	switch out.Error {
	case "":
		if out.AccessToken == "" {
			return "", errors.New("GitHub returned no token")
		}
		if err := c.save(ctx, out.AccessToken); err != nil {
			return "", err
		}
		c.mu.Lock()
		c.flow = nil
		c.mu.Unlock()
		return DeviceConnected, nil
	case "authorization_pending":
		return DevicePending, nil
	case "slow_down":
		c.mu.Lock()
		if out.Interval > 0 {
			f.interval = time.Duration(out.Interval) * time.Second
		} else {
			f.interval += 5 * time.Second
		}
		f.next = c.now().Add(f.interval)
		c.mu.Unlock()
		return DevicePending, nil
	case "expired_token", "token_expired":
		c.clearFlow()
		return DeviceExpired, nil
	case "access_denied":
		c.clearFlow()
		return DeviceDenied, nil
	default:
		c.clearFlow()
		return "", fmt.Errorf("GitHub device flow: %s", out.Error)
	}
}

func (c *Connector) clearFlow() {
	c.mu.Lock()
	c.flow = nil
	c.mu.Unlock()
}

// ImportGHToken stores the GitHub CLI's token (`gh auth token`). The web
// app asks the owner before calling it.
func (c *Connector) ImportGHToken(ctx context.Context) error {
	get := c.GHToken
	if get == nil {
		get = func(ctx context.Context) (string, error) {
			out, err := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com").Output()
			if err != nil {
				return "", fmt.Errorf("`gh auth token` failed (is the GitHub CLI logged in? run `gh auth login`): %w", err)
			}
			return strings.TrimSpace(string(out)), nil
		}
	}
	tok, err := get(ctx)
	if err != nil {
		return err
	}
	if tok == "" {
		return errors.New("the GitHub CLI has no token; run `gh auth login`")
	}
	return c.save(ctx, tok)
}

// SetToken stores a pasted token after checking it with GitHub.
func (c *Connector) SetToken(ctx context.Context, tok string) error {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return errors.New("token is empty")
	}
	return c.save(ctx, tok)
}

func (c *Connector) save(ctx context.Context, tok string) error {
	id, err := c.Client.Whoami(ctx, tok)
	if err != nil {
		return err
	}
	if _, err := c.Store.Set(TokenName, tok); err != nil {
		return fmt.Errorf("store the token: %w", err)
	}
	c.mu.Lock()
	c.identity, c.idToken = id, tok
	c.token, c.loaded = tok, true
	c.mu.Unlock()
	if c.OnChange != nil {
		c.OnChange()
	}
	return nil
}

// Disconnect forgets the token.
func (c *Connector) Disconnect() error {
	if err := c.Store.Delete(TokenName); err != nil {
		return err
	}
	c.mu.Lock()
	c.identity, c.idToken, c.flow = nil, "", nil
	c.token, c.loaded = "", true
	c.mu.Unlock()
	if c.OnChange != nil {
		c.OnChange()
	}
	return nil
}

var _ api.LocalGitHub = (*Connector)(nil)
