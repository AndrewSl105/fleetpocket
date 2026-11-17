package team

import (
	"errors"
	"fmt"
)

// ErrNeedsKey: in team mode the task creator has no key for the agent and
// the shared-key fallback is off (or the server has no key either).
var ErrNeedsKey = errors.New("needs_key")

// Providers are the agents a personal key can be stored for.
var Providers = []string{"claude", "codex", "cursor"}

// Kinds valid per provider.
func validKind(provider, kind string) bool {
	switch provider {
	case "claude":
		return kind == "oauth_token" || kind == "api_key"
	case "codex", "cursor":
		return kind == "api_key"
	}
	return false
}

// EnvVar is the provider environment variable a personal key becomes,
// exactly the one the env key of the same kind uses (config.AgentEnv).
func EnvVar(provider, kind string) string {
	switch {
	case provider == "claude" && kind == "oauth_token":
		return "CLAUDE_CODE_OAUTH_TOKEN"
	case provider == "claude":
		return "ANTHROPIC_API_KEY"
	case provider == "codex":
		return "OPENAI_API_KEY"
	case provider == "cursor":
		return "CURSOR_API_KEY"
	}
	return ""
}

// ProviderLabel names a provider for people ("Add your Claude key").
func ProviderLabel(p string) string {
	switch p {
	case "codex":
		return "Codex (OpenAI)"
	case "cursor":
		return "Cursor"
	}
	return "Claude"
}

// Key sources a run's credentials come from.
const (
	SourceEnv      = "env"      // single-owner mode: the server's env keys, unchanged
	SourceNode     = "node"     // node task: the computer's own logins
	SourcePersonal = "personal" // team mode: the creator's key
	SourceFallback = "fallback" // team mode: the server's env key (fallback on)
)

// PersonalKey is a decrypted personal key.
type PersonalKey struct {
	Kind  string
	Value string
}

// ResolveInput is everything key resolution looks at.
type ResolveInput struct {
	TeamOn   bool
	Fallback bool
	// OnNode: the task runs on a computer, which uses its own logins.
	OnNode bool
	Agent  string
	// Personal is the creator's key for Agent (nil: none, or no creator).
	Personal *PersonalKey
	// EnvKeys is config.AgentEnv(Agent): the server's shared key.
	EnvKeys map[string]string
	// Fake: the fake runner needs no credentials.
	Fake bool
}

// Resolve decides which credentials a run gets (docs/SPEC-server.md
// "Personal agent keys"). A nil env with a nil error means "the host's
// default" (env keys on the server, the node's own logins on a node): the
// pre-teams behaviour. A non-nil env replaces the host's default entirely.
func Resolve(in ResolveInput) (map[string]string, string, error) {
	if in.OnNode {
		return nil, SourceNode, nil
	}
	if !in.TeamOn {
		return nil, SourceEnv, nil
	}
	if in.Personal != nil && in.Personal.Value != "" {
		return map[string]string{EnvVar(in.Agent, in.Personal.Kind): in.Personal.Value}, SourcePersonal, nil
	}
	if in.Fallback && (len(in.EnvKeys) > 0 || in.Fake) {
		env := map[string]string{}
		for k, v := range in.EnvKeys {
			env[k] = v
		}
		return env, SourceFallback, nil
	}
	return nil, "", ErrNeedsKey
}

// NeedsKeyReason is the status reason of a needs_key task.
func NeedsKeyReason(agent string) string {
	return fmt.Sprintf("Add your %s key in My keys", ProviderLabel(agent))
}
