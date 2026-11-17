package api

import (
	"net/http"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// GET /api/v1/settings/checkpoints: whether runners take checkpoints (default off).
func (s *Server) getCheckpointSettings(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.CheckpointSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// PUT /api/v1/settings/checkpoints replaces the setting.
func (s *Server) putCheckpointSettings(w http.ResponseWriter, r *http.Request) {
	var in store.CheckpointSettings
	if !decode(w, r, &in) {
		return
	}
	if err := s.Store.SetCheckpointSettings(r.Context(), in); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

// GET /api/v1/tasks/{id}/checkpoints
func (s *Server) listCheckpoints(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	list, err := s.Tasks.Checkpoints(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// GET /api/v1/tasks/{id}/checkpoints/{checkpointId}: what a restore discards.
func (s *Server) previewCheckpoint(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	cid, ok := pathID(w, r, "checkpointId")
	if !ok {
		return
	}
	p, err := s.Tasks.PreviewCheckpoint(r.Context(), id, cid)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// POST /api/v1/tasks/{id}/checkpoints/{checkpointId}/restore
func (s *Server) restoreCheckpoint(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	cid, ok := pathID(w, r, "checkpointId")
	if !ok {
		return
	}
	var in struct {
		Instruction string `json:"instruction"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, err := s.Tasks.Restore(r.Context(), id, cid, in.Instruction, "web")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
