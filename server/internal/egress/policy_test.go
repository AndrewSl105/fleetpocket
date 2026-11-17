package egress

import (
	"slices"
	"testing"
)

func TestAllowlist(t *testing.T) {
	a := NewAllowlist([]string{"registry.npmjs.org", "*.example.org", "api.test:8443", "10.0.0.5", "bad host!"})
	for _, c := range []struct {
		host, port string
		want       bool
	}{
		{"registry.npmjs.org", "443", true},
		{"REGISTRY.npmjs.org.", "80", true},
		{"registry.npmjs.org", "22", false},
		{"evil-registry.npmjs.org", "443", false},
		{"a.example.org", "443", true},
		{"a.b.example.org", "443", true},
		{"example.org", "443", false}, // a wildcard is subdomains only
		{"notexample.org", "443", false},
		{"api.test", "8443", true},
		{"api.test", "443", false},
		{"10.0.0.5", "443", true},
		{"10.0.0.6", "443", false},
		{"1.2.3.4", "443", false},
	} {
		if got := a.Allows(c.host, c.port); got != c.want {
			t.Errorf("Allows(%s, %s) = %v, want %v", c.host, c.port, got, c.want)
		}
	}
}

func TestNormalizeAndExpand(t *testing.T) {
	for _, ok := range []string{"Example.COM", "*.npmjs.org", "host:8080", "1.2.3.4"} {
		if _, err := NormalizeHost(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "http://x.com", "x.com/path", "*", "a b", "host:99999"} {
		if _, err := NormalizeHost(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	got := Expand([]string{"agent", "npm"}, []string{"x.dev"}, "codex")
	if !slices.Contains(got, "api.openai.com") || slices.Contains(got, "api.anthropic.com") || !slices.Contains(got, "registry.npmjs.org") || !slices.Contains(got, "x.dev") {
		t.Fatalf("expand: %v", got)
	}
}
