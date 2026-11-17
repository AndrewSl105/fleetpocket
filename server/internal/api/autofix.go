package api

import (
	"net/http"

	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// GET /api/v1/settings/ci-autofix: the global CI auto-fix default. Only these
// keys are exposed; the settings table also holds secrets.
func (s *Server) getAutoFixDefaults(w http.ResponseWriter, r *http.Request) {
	o, err := s.Store.AutoFixDefaults(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// PUT /api/v1/settings/ci-autofix replaces the global default.
func (s *Server) putAutoFixDefaults(w http.ResponseWriter, r *http.Request) {
	var in store.AutoFixOverrides
	if !decode(w, r, &in) {
		return
	}
	if err := projects.ValidateAutoFix(in); err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.Store.SetAutoFixDefaults(r.Context(), in); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}
