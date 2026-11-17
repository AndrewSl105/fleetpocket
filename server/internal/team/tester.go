package team

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// TestResult is what the "Test" button shows. The key itself never
// appears in it.
type TestResult struct {
	// Status is ok, invalid, not_testable or error.
	Status  string `json:"status"`
	Message string `json:"message"`
}

// KeyTester runs a provider's cheapest authenticated request.
type KeyTester interface {
	Test(ctx context.Context, provider string, k *PersonalKey) TestResult
}

// HTTPTester calls the providers' documented "list models" / "key info"
// endpoints (no tokens are spent):
//   - Anthropic API key: GET {Anthropic}/v1/models (x-api-key, anthropic-version)
//   - OpenAI API key:    GET {OpenAI}/v1/models (Bearer)
//   - Cursor API key:    GET {Cursor}/v0/me (Bearer; Cursor's "API key info")
//
// A Claude subscription token (CLAUDE_CODE_OAUTH_TOKEN) has no documented
// check endpoint, so it is reported as not testable.
type HTTPTester struct {
	Client                    *http.Client
	Anthropic, OpenAI, Cursor string
}

// DefaultTester talks to the real providers.
var DefaultTester KeyTester = &HTTPTester{
	Anthropic: "https://api.anthropic.com", OpenAI: "https://api.openai.com", Cursor: "https://api.cursor.com",
}

func (t *HTTPTester) Test(ctx context.Context, provider string, k *PersonalKey) TestResult {
	var req *http.Request
	var err error
	switch {
	case provider == "claude" && k.Kind == "oauth_token":
		return TestResult{Status: "not_testable", Message: "Claude subscription tokens cannot be checked without running the agent; the next task will tell."}
	case provider == "claude":
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, t.Anthropic+"/v1/models?limit=1", nil)
		if err == nil {
			req.Header.Set("x-api-key", k.Value)
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	case provider == "codex":
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, t.OpenAI+"/v1/models", nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+k.Value)
		}
	case provider == "cursor":
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, t.Cursor+"/v0/me", nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+k.Value)
		}
	default:
		return TestResult{Status: "error", Message: "unknown provider"}
	}
	if err != nil {
		return TestResult{Status: "error", Message: "could not build the request"}
	}
	c := t.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return TestResult{Status: "error", Message: "the provider could not be reached"}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return TestResult{Status: "ok", Message: "The key works."}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return TestResult{Status: "invalid", Message: "The provider rejected this key."}
	default:
		return TestResult{Status: "error", Message: fmt.Sprintf("The provider answered HTTP %d.", resp.StatusCode)}
	}
}
