package scheduler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/egress"
	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/mcpgateway"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// EventIntegrations lists what a run gets: its MCP servers and egress mode.
const EventIntegrations = "integrations"

// errNoIsolation: an allowlist task on a host that cannot enforce it.
var errNoIsolation = errors.New("this project's egress allowlist can only be enforced on the server's own Docker; the task runs on a node. Move the project to the server or switch egress to open")

// integrations is what execute adds for MCP servers and the egress allowlist.
type integrations struct {
	allowlist bool
	token     string
	mcp       *runnermgr.MCPConfig
	env       map[string]string
	hosts     []string
	servers   []string
}

// prepareIntegrations sets up the task network (internal in allowlist
// mode), the run's gateway token, the egress proxy and the MCP config sent
// to the runner. With no MCP servers and egress open it only creates the
// network, exactly as before. Errors fail the run (never silently open).
func (s *Scheduler) prepareIntegrations(ctx context.Context, h hosts.Host, t *store.Task, run *store.Run, p *store.Project) (*integrations, error) {
	in := &integrations{env: map[string]string{}}
	eg, err := s.Store.ProjectEgress(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("egress: %w", err)
	}
	in.allowlist = eg.Mode == store.EgressAllowlist

	var servers []*store.MCPServer
	if s.Gateway != nil {
		servers, err = s.Store.EffectiveMCPServers(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("mcp servers: %w", err)
		}
		if len(servers) > 0 && !s.Gateway.Enabled() {
			s.addEvent(ctx, t.ID, &run.ID, EventIntegrations, map[string]any{"servers": []string{}, "egress": eg.Mode,
				"warning": "MCP servers skipped: SECRETS_KEY is not set"})
			servers = nil
		}
	}
	base := s.Cfg.GatewayBaseURL()
	if len(servers) > 0 && base == "" {
		s.addEvent(ctx, t.ID, &run.ID, EventIntegrations, map[string]any{"servers": []string{}, "egress": eg.Mode,
			"warning": "MCP servers skipped: set PUBLIC_BASE_URL or MCP_GATEWAY_URL so runners can reach the gateway"})
		servers = nil
	}

	var iso hosts.Isolator
	if in.allowlist {
		var ok bool
		if iso, ok = h.(hosts.Isolator); !ok {
			return nil, errNoIsolation
		}
		if err := iso.EnsureInternalNetwork(ctx, t.ID, t.ProjectID); err != nil {
			return nil, fmt.Errorf("network: %w", err)
		}
	} else if err := h.EnsureNetwork(ctx, t.ID, t.ProjectID); err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}
	if !in.allowlist && len(servers) == 0 {
		return in, nil
	}

	if s.Gateway != nil && s.Gateway.Store != nil {
		ids := make([]int64, 0, len(servers))
		for _, m := range servers {
			ids = append(ids, m.ID)
		}
		// Valid for the longest the run can last; revoked when it ends.
		ttl := s.Cfg.DefaultMaxDuration
		if p.MaxDurationSec != nil {
			ttl = time.Duration(*p.MaxDurationSec) * time.Second
		}
		in.token, err = s.Gateway.Issue(ctx, run.ID, ids, ttl+15*time.Minute)
		if err != nil {
			return nil, fmt.Errorf("gateway token: %w", err)
		}
	}
	gwHost, gwPort := hostPort(base)
	if gwHost == "host.docker.internal" && s.Cfg.Local() {
		in.hosts = []string{"host.docker.internal:host-gateway"}
	}
	if len(servers) > 0 {
		in.mcp = &runnermgr.MCPConfig{Token: in.token}
		for _, m := range servers {
			in.mcp.Servers = append(in.mcp.Servers, runnermgr.MCPServerRef{
				Name: m.Name, URL: base + mcpgateway.PathPrefix + "mcp/" + m.Name,
			})
			in.servers = append(in.servers, m.Name)
		}
	}

	if in.allowlist {
		allow := egress.Expand(eg.Presets, eg.Hosts, t.Agent)
		penv := map[string]string{egress.EnvAllow: strings.Join(allow, ",")}
		if gwHost != "" {
			// The gateway is always reachable, and may be a private address.
			penv[egress.EnvAllow] += "," + net.JoinHostPort(gwHost, gwPort)
			penv[egress.EnvAllowPrivate] = gwHost
			if in.token != "" {
				penv[egress.EnvReportURL] = base + mcpgateway.PathPrefix + "egress/blocked"
				penv[egress.EnvReportToken] = in.token
			}
		}
		proxy, err := iso.EnsureEgressProxy(ctx, runnermgr.EgressSpec{
			TaskID: t.ID, Project: t.ProjectID, Image: s.Cfg.EgressProxyImage, Binary: s.EgressBinary,
			Binds: s.EgressBinds, Env: penv, ExtraHosts: in.hosts,
		})
		if err != nil {
			return nil, err
		}
		noProxy := "localhost,127.0.0.1,::1,postgres,redis"
		for k, v := range map[string]string{"HTTP_PROXY": proxy, "HTTPS_PROXY": proxy, "NO_PROXY": noProxy} {
			in.env[k], in.env[strings.ToLower(k)] = v, v
		}
		// Node's fetch (Codex/Cursor helpers, npm scripts) honours the proxy env with it.
		in.env["NODE_USE_ENV_PROXY"] = "1"
	}
	payload := map[string]any{"servers": nonNilStrings(in.servers), "egress": eg.Mode}
	s.addEvent(ctx, t.ID, &run.ID, EventIntegrations, payload)
	return in, nil
}

// endIntegrations revokes the run's token and stops its stdio MCP sidecars.
func (s *Scheduler) endIntegrations(ctx context.Context, runID int64) {
	if s.Gateway != nil && s.Gateway.Store != nil {
		s.Gateway.EndRun(ctx, runID)
	}
}

func hostPort(base string) (string, string) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", ""
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return strings.ToLower(u.Hostname()), port
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
