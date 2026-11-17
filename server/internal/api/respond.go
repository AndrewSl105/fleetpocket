package api

import (
	"encoding/json"
	"errors"
	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/nodes"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/andrewsl105/fleetpocket/server/internal/localrepos"
	"github.com/andrewsl105/fleetpocket/server/internal/pipelinerun"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		// Files lists the conflicting files of a refused merge (merge_conflict).
		Files []string `json:"files,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var b errorBody
	b.Error.Code, b.Error.Message = code, msg
	writeJSON(w, status, b)
}

// writeErr maps service errors to HTTP responses.
func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *workspace.MergeConflictError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, store.ErrConflict), errors.Is(err, pipelinerun.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, pipelinerun.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, tasks.ErrInvalid), errors.Is(err, projects.ErrInvalid), errors.Is(err, nodes.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, tasks.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, tasks.ErrNotRunning):
		writeError(w, http.StatusConflict, "not_running", err.Error())
	case errors.Is(err, tasks.ErrNotMergeable):
		writeError(w, http.StatusConflict, "not_mergeable", err.Error())
	case errors.Is(err, errGitHubDisabled), errors.Is(err, tasks.ErrGitHubDisabled):
		writeError(w, http.StatusServiceUnavailable, "github_disabled", err.Error())
	case errors.Is(err, hosts.ErrOffline):
		writeError(w, http.StatusServiceUnavailable, "host_offline", "the computer this task runs on is offline")
	case errors.Is(err, tasks.ErrNoGitHubRemote):
		writeError(w, http.StatusBadRequest, "no_github_remote", err.Error())
	case errors.Is(err, errLocalModeOnly):
		writeError(w, http.StatusBadRequest, "local_mode_only", err.Error())
	case errors.Is(err, localrepos.ErrInvalid), errors.Is(err, hosts.ErrLocalRepoOnNode):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, localrepos.ErrOutsideHome):
		writeError(w, http.StatusForbidden, "outside_home", err.Error())
	case errors.As(err, &conflict):
		var b errorBody
		b.Error.Code, b.Error.Message, b.Error.Files = "merge_conflict", conflict.Error(), conflict.Files
		writeJSON(w, http.StatusConflict, b)
	case errors.Is(err, workspace.ErrDirty):
		writeError(w, http.StatusConflict, "dirty", err.Error())
	case errors.Is(err, workspace.ErrDetached):
		writeError(w, http.StatusConflict, "detached", err.Error())
	case errors.Is(err, workspace.ErrCurrentBranch):
		writeError(w, http.StatusConflict, "current_branch", err.Error())
	case errors.Is(err, workspace.ErrBranchGone):
		writeError(w, http.StatusConflict, "branch_gone", err.Error())
	case errors.Is(err, workspace.ErrGitRefused):
		writeError(w, http.StatusConflict, "git_refused", err.Error())
	case errors.Is(err, workspace.ErrRepoMissing):
		writeError(w, http.StatusConflict, "repo_missing", err.Error())
	default:
		slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

const maxBody = 1 << 20

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// limitParam reads ?limit= (default 50, max 200).
func limitParam(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return 50
	}
	return min(n, 200)
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid", name+" must be a positive integer")
		return 0, false
	}
	return id, true
}
