package hosts

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// Team mode: a run's resolved key replaces the host's env keys entirely,
// reaches the container only as the provider env, and never goes to a node.
func TestRunnerEnvTeamKeys(t *testing.T) {
	serverKeys := func(agent string) map[string]string {
		return map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "server-oauth-SECRET"}
	}
	l := &Local{AgentEnv: serverKeys, Workspace: &workspace.Manager{Dir: t.TempDir()}, Docker: &runnermgr.Manager{}}
	personal := "sk-ant-personal-SECRET-1234"

	// Single-owner mode (nil AgentEnv): the host's env keys, unchanged.
	env := l.RunnerEnv(StartSpec{Agent: "claude", Env: map[string]string{"FOO": "1"}})
	if env["CLAUDE_CODE_OAUTH_TOKEN"] != "server-oauth-SECRET" || env["FOO"] != "1" || len(env) != 2 {
		t.Fatalf("single-owner env: %v", env)
	}

	// Team mode: only the personal key; the server's OAuth token is gone.
	spec := StartSpec{TaskID: 7, RunID: 9, Project: "p", Image: "img", Agent: "claude",
		Env: map[string]string{"FOO": "1"}, AgentEnv: map[string]string{"ANTHROPIC_API_KEY": personal}}
	env = l.RunnerEnv(spec)
	if env["ANTHROPIC_API_KEY"] != personal || env["CLAUDE_CODE_OAUTH_TOKEN"] != "" || len(env) != 2 {
		t.Fatalf("team env: %v", env)
	}

	// The container spec carries it only as an environment variable.
	cfg, hc, err := l.Docker.Spec(runnermgr.RunnerSpec{TaskID: 7, RunID: 9, Project: "p", Image: "img",
		Workspace: l.Workspace.Path(7), Env: env})
	if err != nil {
		t.Fatal(err)
	}
	inEnv := 0
	for _, e := range cfg.Env {
		if strings.Contains(e, personal) {
			if e != "ANTHROPIC_API_KEY="+personal {
				t.Fatalf("key in an unexpected env entry: %q", e)
			}
			inEnv++
		}
		if strings.Contains(e, "server-oauth-SECRET") {
			t.Fatalf("server key leaked into a team run: %q", e)
		}
	}
	if inEnv != 1 {
		t.Fatalf("key not in env exactly once: %v", cfg.Env)
	}
	rest := fmt.Sprintf("%v %v %v %v %v", cfg.Cmd, cfg.Entrypoint, cfg.Labels, hc.Binds, hc.Mounts)
	if strings.Contains(rest, personal) {
		t.Fatalf("key outside the env: %s", rest)
	}

	// The spec sent to a node never carries the resolved key.
	b, _ := json.Marshal(spec)
	if strings.Contains(string(b), personal) || strings.Contains(string(b), "agent_env") {
		t.Fatalf("StartSpec JSON carries the key: %s", b)
	}
}
