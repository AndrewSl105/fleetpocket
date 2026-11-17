package mcpgateway

import (
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func TestToolPolicy(t *testing.T) {
	ro := &mcp.Tool{Name: "list_issues", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}
	rw := &mcp.Tool{Name: "delete_issue"}
	m := &store.MCPServer{Kind: store.MCPRemote, ToolsMode: ToolsDeny, Tools: []string{"delete_*"}}
	if !ToolAllowed(m, ro) || ToolAllowed(m, rw) {
		t.Fatal("deny list")
	}
	m.ToolsMode, m.Tools = ToolsAllow, []string{"list_*"}
	if !ToolAllowed(m, ro) || ToolAllowed(m, rw) {
		t.Fatal("allow list")
	}
	m.ToolsMode, m.ReadOnly = ToolsAll, true
	if !ToolAllowed(m, ro) || ToolAllowed(m, rw) {
		t.Fatal("read-only hides tools not marked read-only")
	}
	m.Kind = store.MCPPostgres
	if !ToolAllowed(m, rw) {
		t.Fatal("postgres enforces read-only itself")
	}
}

func TestStdioCommand(t *testing.T) {
	c, _ := StdioCommand(StdioSpec{Runtime: RuntimeNPM, Package: "@linear/mcp", Args: []string{"--x"}, Env: map[string]string{"K": "v"}}, "agent:1")
	if c.Image != "agent:1" || !slices.Equal(c.Entrypoint, []string{"npx", "-y", "@linear/mcp"}) || c.Cmd[0] != "--x" || c.Env["K"] != "v" {
		t.Fatalf("npm: %+v", c)
	}
	c, _ = StdioCommand(StdioSpec{Runtime: RuntimePip, Package: "mcp-server-x"}, "agent:1")
	if c.Env["JM_MCP_COMMAND"] != "mcp-server-x" || c.Entrypoint[0] != "sh" {
		t.Fatalf("pip: %+v", c)
	}
	c, _ = StdioCommand(StdioSpec{Runtime: RuntimeImage, Package: "ghcr.io/x/mcp:1", Args: []string{"stdio"}}, "agent:1")
	if c.Image != "ghcr.io/x/mcp:1" || c.Entrypoint != nil || c.Cmd[0] != "stdio" {
		t.Fatalf("image: %+v", c)
	}
	if _, err := StdioCommand(StdioSpec{Runtime: "cargo"}, "a"); err == nil {
		t.Fatal("unknown runtime")
	}
}

func TestValidate(t *testing.T) {
	u, tr := "https://x.test/mcp", "http"
	ok := &store.MCPServer{Name: "linear", Kind: store.MCPRemote, URL: &u, Transport: &tr, ToolsMode: ToolsAll}
	if err := Validate(ok, []string{"bearer", "X-Api-Key"}); err != nil {
		t.Fatal(err)
	}
	if err := Validate(ok, []string{"bad header"}); err == nil {
		t.Fatal("bad header name")
	}
	rt, pkg := "npm", "@x/y"
	st := &store.MCPServer{Name: "s", Kind: store.MCPStdio, Runtime: &rt, Package: &pkg, ToolsMode: ToolsAll}
	if err := Validate(st, []string{"PATH"}); err == nil {
		t.Fatal("PATH as a secret")
	}
	if err := Validate(&store.MCPServer{Name: "db", Kind: store.MCPPostgres, ToolsMode: ToolsAll}, []string{"OTHER"}); err == nil {
		t.Fatal("postgres secret other than DATABASE_URL")
	}
}
