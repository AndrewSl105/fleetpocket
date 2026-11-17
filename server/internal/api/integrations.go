package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/andrewsl105/fleetpocket/server/internal/egress"
	"github.com/andrewsl105/fleetpocket/server/internal/mcpgateway"
	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// MCP servers and the egress allowlist (docs/API.md, "Integrations").
// Secrets are write-only: responses carry secret_hints (last characters).

func (s *Server) gatewayOn(w http.ResponseWriter) bool {
	if !s.Gateway.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "secrets_disabled", secretbox.ErrDisabled.Error())
		return false
	}
	return true
}

func (s *Server) writeMCPErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case mcpgateway.IsInvalid(err):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, secretbox.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, "secrets_disabled", err.Error())
	case errors.Is(err, secretbox.ErrOpen):
		writeError(w, http.StatusConflict, "secrets_unreadable", "the stored secrets cannot be decrypted with the current SECRETS_KEY; enter them again")
	default:
		writeErr(w, r, err)
	}
}

// GET /api/v1/mcp-servers?project=<id>: the project's servers, or the
// global ones without ?project.
func (s *Server) listMCPServers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListMCPServers(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) createMCPServer(w http.ResponseWriter, r *http.Request) {
	if !s.gatewayOn(w) {
		return
	}
	var in mcpgateway.Input
	if !decode(w, r, &in) {
		return
	}
	if in.ProjectID != nil {
		if _, err := s.Store.GetProject(r.Context(), *in.ProjectID); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	m, err := s.Gateway.Create(r.Context(), in)
	if err != nil {
		s.writeMCPErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func mcpID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, store.ErrNotFound
	}
	return id, nil
}

func (s *Server) getMCPServer(w http.ResponseWriter, r *http.Request) {
	id, err := mcpID(r)
	if err == nil {
		var m *store.MCPServer
		if m, err = s.Store.GetMCPServer(r.Context(), id); err == nil {
			writeJSON(w, http.StatusOK, m)
			return
		}
	}
	writeErr(w, r, err)
}

// PUT /api/v1/mcp-servers/{id} replaces the settings; secrets merge.
func (s *Server) updateMCPServer(w http.ResponseWriter, r *http.Request) {
	if !s.gatewayOn(w) {
		return
	}
	id, err := mcpID(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in mcpgateway.Input
	if !decode(w, r, &in) {
		return
	}
	m, err := s.Gateway.Update(r.Context(), id, in)
	if err != nil {
		s.writeMCPErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) deleteMCPServer(w http.ResponseWriter, r *http.Request) {
	id, err := mcpID(r)
	if err == nil {
		err = s.Store.DeleteMCPServer(r.Context(), id)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/mcp-servers/{id}/test connects and lists the tools.
func (s *Server) testMCPServer(w http.ResponseWriter, r *http.Request) {
	if !s.gatewayOn(w) {
		return
	}
	id, err := mcpID(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	res, err := s.Gateway.Test(r.Context(), id)
	if err != nil {
		s.writeMCPErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// AgentSupport says how an agent gets MCP servers and the proxy.
type AgentSupport struct {
	Agent string `json:"agent"`
	MCP   bool   `json:"mcp"`
	// Proxy: "yes" (the agent's SDK honours HTTPS_PROXY) or "unverified".
	Proxy string `json:"proxy"`
	Note  string `json:"note"`
}

var agentSupport = []AgentSupport{
	{"claude", true, "yes", "Agent SDK mcpServers (HTTP)"},
	{"codex", true, "yes", "Codex config mcp_servers (Streamable HTTP, bearer token from the environment)"},
	{"cursor", true, "unverified", "Cursor SDK mcpServers (HTTP); proxy through NODE_USE_ENV_PROXY, not verified"},
}

type integrationsView struct {
	SecretsEnabled  bool               `json:"secrets_enabled"`
	Egress          store.Egress       `json:"egress"`
	EgressSupported bool               `json:"egress_supported"`
	Presets         []egress.Preset    `json:"presets"`
	Servers         []*store.MCPServer `json:"servers"`
	GlobalServers   []*store.MCPServer `json:"global_servers"`
	GlobalServerIDs []int64            `json:"global_server_ids"`
	Active          []*store.MCPServer `json:"active"`
	Agents          []AgentSupport     `json:"agents"`
	GatewayURL      string             `json:"gateway_url"`
}

// GET /api/v1/projects/{id}/integrations
func (s *Server) getIntegrations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := s.Store.GetProject(ctx, r.PathValue("id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	v := integrationsView{SecretsEnabled: s.Gateway.Enabled(), Presets: egress.Presets(), Agents: agentSupport,
		EgressSupported: p.Node == nil, GatewayURL: s.Cfg.GatewayBaseURL()}
	if v.Egress, err = s.Store.ProjectEgress(ctx, p.ID); err == nil {
		if v.Servers, err = s.Store.ListMCPServers(ctx, p.ID); err == nil {
			if v.GlobalServers, err = s.Store.ListMCPServers(ctx, ""); err == nil {
				if v.GlobalServerIDs, err = s.Store.ProjectMCPGlobals(ctx, p.ID); err == nil {
					v.Active, err = s.Store.EffectiveMCPServers(ctx, p.ID)
				}
			}
		}
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type integrationsInput struct {
	Egress          *store.Egress `json:"egress"`
	GlobalServerIDs *[]int64      `json:"global_server_ids"`
}

// PUT /api/v1/projects/{id}/integrations sets the egress policy and the
// global servers the project uses (fields not sent are kept).
func (s *Server) putIntegrations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if _, err := s.Store.GetProject(ctx, id); err != nil {
		writeErr(w, r, err)
		return
	}
	var in integrationsInput
	if !decode(w, r, &in) {
		return
	}
	if in.Egress != nil {
		e, msg := normalizeEgress(*in.Egress)
		if msg != "" {
			writeError(w, http.StatusBadRequest, "invalid", msg)
			return
		}
		if err := s.Store.SetProjectEgress(ctx, id, e); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	if in.GlobalServerIDs != nil {
		if err := s.Store.SetProjectMCPGlobals(ctx, id, *in.GlobalServerIDs); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	s.getIntegrations(w, r)
}

func normalizeEgress(e store.Egress) (store.Egress, string) {
	if e.Mode == "" {
		e.Mode = store.EgressOpen
	}
	if e.Mode != store.EgressOpen && e.Mode != store.EgressAllowlist {
		return e, `egress.mode must be "open" or "allowlist"`
	}
	out := store.Egress{Mode: e.Mode, Presets: []string{}, Hosts: []string{}}
	for _, p := range e.Presets {
		if !egress.ValidPreset(p) {
			return e, "unknown preset " + strconv.Quote(p)
		}
		out.Presets = append(out.Presets, p)
	}
	seen := map[string]bool{}
	for _, h := range e.Hosts {
		n, err := egress.NormalizeHost(h)
		if err != nil {
			return e, err.Error()
		}
		if !seen[n] {
			seen[n] = true
			out.Hosts = append(out.Hosts, n)
		}
	}
	if len(out.Hosts) > 200 {
		return e, "at most 200 hosts"
	}
	return out, ""
}

// POST /api/v1/projects/{id}/egress/allow {host}: "Allow for this project"
// on a blocked request; takes effect on the next run.
func (s *Server) allowEgress(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host string `json:"host"`
	}
	if !decode(w, r, &in) {
		return
	}
	host, err := egress.NormalizeHost(in.Host)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	e, err := s.Store.AddEgressHost(r.Context(), r.PathValue("id"), host)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}
