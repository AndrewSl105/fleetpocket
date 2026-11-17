package api_test

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestCIAutoFixSettings(t *testing.T) {
	c, _, secret := setup(t)
	otp, _ := totp.GenerateCode(secret, time.Now())
	if code, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "pw-for-tests", "totp": otp}); code != 200 {
		t.Fatal("login")
	}
	// Off by default: nothing stored, every field inherits the built-in default.
	if code, body := c.do("GET", "/api/v1/settings/ci-autofix", nil); code != 200 || body["enabled"] != nil {
		t.Fatalf("default: %d %v", code, body)
	}
	if code, _ := c.do("PUT", "/api/v1/settings/ci-autofix", map[string]any{"enabled": true, "max_attempts": 6}); code != 400 {
		t.Fatalf("max_attempts 6 accepted: %d", code)
	}
	if code, body := c.do("PUT", "/api/v1/settings/ci-autofix", map[string]any{"enabled": true, "max_attempts": 3, "checks": []string{"test"}}); code != 200 || body["max_attempts"] != float64(3) {
		t.Fatalf("put: %d %v", code, body)
	}
	if _, body := c.do("GET", "/api/v1/settings/ci-autofix", nil); body["enabled"] != true || body["allow_ci_changes"] != nil {
		t.Fatalf("get: %v", body)
	}
	// Projects carry their own overrides.
	c.do("POST", "/api/v1/projects", map[string]any{"id": "demo", "name": "Demo"})
	code, p := c.do("PATCH", "/api/v1/projects/demo", map[string]any{"ci_autofix": map[string]any{"enabled": false}})
	if code != 200 || p["ci_autofix"].(map[string]any)["enabled"] != false {
		t.Fatalf("project: %d %v", code, p)
	}
}
