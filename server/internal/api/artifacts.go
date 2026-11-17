package api

import "net/http"

// GET /api/v1/tasks/{id}/artifacts: the latest version of each artifact.
func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.Store.GetTask(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	list, err := s.Store.Artifacts(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}
