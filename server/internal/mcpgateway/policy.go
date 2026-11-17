// Package mcpgateway is the MCP gateway: the one endpoint through which a
// runner reaches the MCP servers of its project. The runner authenticates
// with a per-run token; the gateway injects the server's credentials (which
// the runner never sees), enforces its tool list, read-only flag and call
// limit, forwards to the upstream (a remote URL, a stdio sidecar on the
// server's Docker, or the bundled read-only Postgres server) and records
// every call as a timeline event. See docs/SPEC-server.md "MCP gateway".
package mcpgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// Tool modes of a server.
const (
	ToolsAll   = "all"
	ToolsAllow = "allow"
	ToolsDeny  = "deny"
)

// Stdio runtimes.
const (
	RuntimeNPM   = "npm"
	RuntimePip   = "pip"
	RuntimeImage = "image"
)

// SecretDatabaseURL is the secret key of a postgres server's connection URL.
const SecretDatabaseURL = "DATABASE_URL"

// SecretBearer is a remote server's secret sent as "Authorization: Bearer <v>".
const SecretBearer = "bearer"

var (
	nameRE      = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	headerRE    = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	envRE       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	packageRE   = regexp.MustCompile(`^[A-Za-z0-9@/._:+=\[\],<>~!-]{1,200}$`)
	errInvalid  = errors.New("invalid MCP server")
	reservedEnv = map[string]bool{"PATH": true, "HOME": true, "HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true}
)

// Invalid wraps a validation message (the API answers 400).
func Invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, a...))
}

// IsInvalid reports whether err is a validation error.
func IsInvalid(err error) bool { return errors.Is(err, errInvalid) }

// Validate checks a server's settings (secrets are checked by key only).
func Validate(m *store.MCPServer, secretKeys []string) error {
	if !nameRE.MatchString(m.Name) {
		return Invalid("name must be 1-32 lowercase letters, digits, - or _, starting with a letter")
	}
	switch m.ToolsMode {
	case ToolsAll, ToolsAllow, ToolsDeny:
	default:
		return Invalid(`tools_mode must be "all", "allow" or "deny"`)
	}
	for _, t := range m.Tools {
		if _, err := path.Match(t, ""); err != nil || t == "" {
			return Invalid("tool pattern %q", t)
		}
	}
	if m.MaxCalls != nil && *m.MaxCalls < 1 {
		return Invalid("max_calls must be at least 1 (or null for no limit)")
	}
	switch m.Kind {
	case store.MCPRemote:
		tr := deref(m.Transport)
		if tr != "http" && tr != "sse" {
			return Invalid(`transport must be "http" or "sse"`)
		}
		u, err := url.Parse(deref(m.URL))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			return Invalid("url must be an http(s) URL without credentials (put them in secrets)")
		}
		for _, k := range secretKeys {
			if k != SecretBearer && !headerRE.MatchString(k) {
				return Invalid("secret %q: remote server secrets are HTTP header names (or %q)", k, SecretBearer)
			}
		}
	case store.MCPStdio:
		switch deref(m.Runtime) {
		case RuntimeNPM, RuntimePip, RuntimeImage:
		default:
			return Invalid(`runtime must be "npm", "pip" or "image"`)
		}
		if !packageRE.MatchString(deref(m.Package)) {
			return Invalid("package must be a package name or image reference")
		}
		if c := deref(m.Command); c != "" && !packageRE.MatchString(c) {
			return Invalid("command")
		}
		for _, k := range secretKeys {
			if !envRE.MatchString(k) || reservedEnv[strings.ToUpper(k)] {
				return Invalid("secret %q: stdio server secrets are environment variable names", k)
			}
		}
	case store.MCPPostgres:
		for _, k := range secretKeys {
			if k != SecretDatabaseURL {
				return Invalid("a postgres server has one secret, %s", SecretDatabaseURL)
			}
		}
	default:
		return Invalid(`kind must be "remote", "stdio" or "postgres"`)
	}
	return nil
}

// ToolAllowed applies the server's tool list and read-only flag. The
// bundled Postgres server enforces read-only itself, so all its tools pass.
func ToolAllowed(m *store.MCPServer, t *mcp.Tool) bool {
	if !nameAllowed(m, t.Name) {
		return false
	}
	if m.ReadOnly && m.Kind != store.MCPPostgres {
		// A read-only server exposes only the tools it marks read-only.
		return t.Annotations != nil && t.Annotations.ReadOnlyHint
	}
	return true
}

func nameAllowed(m *store.MCPServer, name string) bool {
	match := false
	for _, p := range m.Tools {
		if ok, _ := path.Match(p, name); ok {
			match = true
			break
		}
	}
	switch m.ToolsMode {
	case ToolsAllow:
		return match
	case ToolsDeny:
		return !match
	}
	return true
}

// OpenSecrets decrypts a server's credentials.
func OpenSecrets(box *secretbox.Box, m *store.MCPServer) (map[string]string, error) {
	out := map[string]string{}
	if len(m.Secrets) == 0 {
		return out, nil
	}
	b, err := box.Open(m.Secrets, AAD(m.ID))
	if err != nil {
		return nil, err
	}
	return out, json.Unmarshal(b, &out)
}

// SealSecrets encrypts a server's credentials and returns them with hints.
func SealSecrets(box *secretbox.Box, id int64, secrets map[string]string) ([]byte, map[string]string, error) {
	hints := map[string]string{}
	if len(secrets) == 0 {
		return nil, hints, nil
	}
	b, _ := json.Marshal(secrets)
	sealed, err := box.Seal(b, AAD(id))
	if err != nil {
		return nil, nil, err
	}
	for k, v := range secrets {
		hints[k] = secretbox.Hint(v)
	}
	return sealed, hints, nil
}

// AAD binds a sealed value to its row.
func AAD(id int64) string { return fmt.Sprintf("mcp_server:%d", id) }

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
