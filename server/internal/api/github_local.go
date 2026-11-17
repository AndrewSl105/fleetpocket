package api

import (
	"context"
	"net/http"
)

// DeviceCode is what the owner enters at verification_uri (GitHub device flow).
type DeviceCode struct {
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// GitHubConnStatus is GET /local/github.
type GitHubConnStatus struct {
	Connected bool     `json:"connected"`
	Login     string   `json:"login"`
	Scopes    []string `json:"scopes"`
	// Error is set when a stored token no longer works.
	Error string `json:"error"`
	// DeviceFlow: an OAuth App client id is configured.
	DeviceFlow bool `json:"device_flow"`
	// GHCLI: the GitHub CLI is installed (its token can be imported).
	GHCLI bool `json:"gh_cli"`
}

// LocalGitHub connects GitHub with the owner's own token in local mode
// (githubapp.Connector).
type LocalGitHub interface {
	Status(ctx context.Context) GitHubConnStatus
	StartDevice(ctx context.Context) (*DeviceCode, error)
	PollDevice(ctx context.Context) (string, error)
	ImportGHToken(ctx context.Context) error
	SetToken(ctx context.Context, token string) error
	Disconnect() error
}

func (s *Server) localGitHub(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.LocalGitHub == nil {
			writeErr(w, r, errLocalModeOnly)
			return
		}
		next(w, r)
	}
}

func (s *Server) githubStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.LocalGitHub.Status(r.Context()))
}

func (s *Server) githubDeviceStart(w http.ResponseWriter, r *http.Request) {
	code, err := s.LocalGitHub.StartDevice(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "github", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, code)
}

func (s *Server) githubDevicePoll(w http.ResponseWriter, r *http.Request) {
	st, err := s.LocalGitHub.PollDevice(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "github", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "github": s.LocalGitHub.Status(r.Context())})
}

func (s *Server) githubImportGH(w http.ResponseWriter, r *http.Request) {
	if err := s.LocalGitHub.ImportGHToken(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.LocalGitHub.Status(r.Context()))
}

func (s *Server) githubSetToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.LocalGitHub.SetToken(r.Context(), in.Token); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.LocalGitHub.Status(r.Context()))
}

func (s *Server) githubDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.LocalGitHub.Disconnect(); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.LocalGitHub.Status(r.Context()))
}
