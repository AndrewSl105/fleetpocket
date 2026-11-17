// MCP servers for each agent, from the run command's `mcp` block. Every
// server is an HTTP (Streamable HTTP) endpoint of Jungle Monkey's gateway,
// authenticated with the run's gateway token; credentials for the real
// servers stay on the server. Option names verified against the installed
// SDKs (docs/DECISIONS.md, Agent).
import type { McpConfig } from "./protocol.ts";

// The environment variable Codex reads the token from (bearer_token_env_var).
// It contains "TOKEN", so Codex's shell_environment_policy keeps it from the
// commands it runs.
export const CODEX_MCP_TOKEN_VAR = "FLEETPOCKET_MCP_TOKEN";

type HttpServer = { type: "http"; url: string; headers: Record<string, string> };

function httpServers(mcp: McpConfig): Record<string, HttpServer> {
  const out: Record<string, HttpServer> = {};
  for (const s of mcp.servers) out[s.name] = { type: "http", url: s.url, headers: { Authorization: `Bearer ${mcp.token}` } };
  return out;
}

// Claude Agent SDK: options.mcpServers (McpHttpServerConfig) and the
// server-level allowedTools rule `mcp__<name>`, so its tools need no approval.
export function claudeMcp(mcp: McpConfig | undefined): { mcpServers: Record<string, HttpServer>; allowedTools: string[] } | null {
  if (!mcp || mcp.servers.length === 0) return null;
  return { mcpServers: httpServers(mcp), allowedTools: mcp.servers.map((s) => `mcp__${s.name}`) };
}

// Codex: `mcp_servers.<name>.url` and `bearer_token_env_var` config keys
// (Streamable HTTP), passed as --config overrides; the token goes in the
// CLI's environment, never in argv.
export function codexMcp(mcp: McpConfig | undefined): { config: Record<string, { url: string; bearer_token_env_var: string }>; env: Record<string, string> } | null {
  if (!mcp || mcp.servers.length === 0) return null;
  const config: Record<string, { url: string; bearer_token_env_var: string }> = {};
  for (const s of mcp.servers) config[s.name] = { url: s.url, bearer_token_env_var: CODEX_MCP_TOKEN_VAR };
  return { config, env: { [CODEX_MCP_TOKEN_VAR]: mcp.token } };
}

// Cursor SDK: AgentOptions.mcpServers ({ type: "http", url, headers }).
export function cursorMcp(mcp: McpConfig | undefined): Record<string, HttpServer> | null {
  if (!mcp || mcp.servers.length === 0) return null;
  return httpServers(mcp);
}
