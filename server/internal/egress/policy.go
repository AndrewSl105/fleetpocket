// Package egress is the allowlist egress proxy of projects in allowlist mode
// (docs/SPEC-server.md "Egress allowlist"): the runner's task network is
// internal, and its only way out is this proxy, a sidecar running
// `fleetpocket egress-proxy`, which allows CONNECT and plain HTTP to listed
// hosts and reports the rest to the server.
package egress

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Preset is a group of hosts the owner can tick.
type Preset struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Hosts []string `json:"hosts"`
}

// PresetAgent expands to the API hosts of the task's agent.
const PresetAgent = "agent"

// AgentHosts are the hosts each agent's SDK talks to.
var AgentHosts = map[string][]string{
	"claude": {"api.anthropic.com", "*.anthropic.com", "claude.ai", "*.claude.ai"},
	"codex":  {"api.openai.com", "*.openai.com", "chatgpt.com", "*.chatgpt.com"},
	"cursor": {"cursor.com", "*.cursor.com", "*.cursor.sh"},
}

// Presets lists every preset; "agent" shows the hosts of all agents.
func Presets() []Preset {
	var agent []string
	for _, a := range []string{"claude", "codex", "cursor"} {
		agent = append(agent, AgentHosts[a]...)
	}
	return []Preset{
		{PresetAgent, "The task's agent API", agent},
		{"npm", "npm", []string{"registry.npmjs.org", "*.npmjs.org", "registry.yarnpkg.com", "*.yarnpkg.com"}},
		{"pypi", "PyPI", []string{"pypi.org", "files.pythonhosted.org", "*.pythonhosted.org"}},
		{"go", "Go module proxy", []string{"proxy.golang.org", "sum.golang.org", "storage.googleapis.com"}},
		{"crates", "crates.io", []string{"crates.io", "*.crates.io"}},
		{"dockerhub", "Docker Hub", []string{"registry-1.docker.io", "auth.docker.io", "production.cloudflare.docker.com", "*.docker.io"}},
		{"github", "GitHub (dependency downloads)", []string{"github.com", "codeload.github.com", "api.github.com", "*.githubusercontent.com"}},
	}
}

// ValidPreset reports whether id names a preset.
func ValidPreset(id string) bool {
	return slices.ContainsFunc(Presets(), func(p Preset) bool { return p.ID == id })
}

// Expand returns the hosts of presets (for agent) plus extra hosts.
func Expand(presets []string, hosts []string, agent string) []string {
	var out []string
	for _, id := range presets {
		if id == PresetAgent {
			out = append(out, AgentHosts[agent]...)
			continue
		}
		for _, p := range Presets() {
			if p.ID == id {
				out = append(out, p.Hosts...)
			}
		}
	}
	out = append(out, hosts...)
	slices.Sort(out)
	return slices.Compact(out)
}

var hostRE = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(:[0-9]{1,5})?$`)

// NormalizeHost validates an allowlist entry: a hostname, "*.domain" (its
// subdomains, not the domain itself), an IP, optionally with ":port"
// (without a port, 80 and 443 are allowed).
func NormalizeHost(h string) (string, error) {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimSuffix(h, ".")
	if h == "" || len(h) > 260 {
		return "", fmt.Errorf("empty or too long host")
	}
	if host, port, err := net.SplitHostPort(h); err == nil {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid port in %q", h)
		}
		if net.ParseIP(host) != nil {
			return h, nil
		}
	} else if net.ParseIP(strings.Trim(h, "[]")) != nil {
		return h, nil
	}
	if !hostRE.MatchString(h) {
		return "", fmt.Errorf("invalid host %q (a hostname, *.domain, or host:port)", h)
	}
	return h, nil
}

// Allowlist decides which destinations the proxy lets through.
type Allowlist struct {
	entries []entry
}

type entry struct {
	host     string // without "*." for wildcards
	wildcard bool
	port     string // "" = 80 and 443
}

// NewAllowlist parses entries (invalid ones are skipped).
func NewAllowlist(hosts []string) *Allowlist {
	a := &Allowlist{}
	for _, h := range hosts {
		h, err := NormalizeHost(h)
		if err != nil {
			continue
		}
		e := entry{host: h}
		if host, port, err := net.SplitHostPort(h); err == nil {
			e.host, e.port = host, port
		}
		if strings.HasPrefix(e.host, "*.") {
			e.host, e.wildcard = e.host[2:], true
		}
		a.entries = append(a.entries, e)
	}
	return a
}

// Allows reports whether host:port may be reached. host is a name or IP as
// the client sent it; an IP is allowed only when listed as that IP.
func (a *Allowlist) Allows(host, port string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	for _, e := range a.entries {
		if e.port == "" {
			if port != "443" && port != "80" {
				continue
			}
		} else if e.port != port {
			continue
		}
		switch {
		case e.wildcard:
			if strings.HasSuffix(host, "."+e.host) && net.ParseIP(host) == nil {
				return true
			}
		case strings.Trim(e.host, "[]") == host:
			return true
		}
	}
	return false
}
