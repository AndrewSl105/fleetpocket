package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/andrewsl105/fleetpocket/server/internal/localrepos"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
)

// errLocalModeOnly: local repositories exist only in local mode
// (`npx jungle-monkey`); the self-hosted server refuses them.
var errLocalModeOnly = errors.New("local repositories are only available in local mode (npx jungle-monkey)")

// localRepos guards the /local endpoints: 400 local_mode_only in server mode.
func (s *Server) localRepos(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.LocalRepos == nil {
			writeErr(w, r, errLocalModeOnly)
			return
		}
		next(w, r)
	}
}

// sessionOrControl accepts the browser session, or the CLI's control token
// (`jungle-monkey add`, and the repository the CLI was started in).
func (s *Server) sessionOrControl(next http.HandlerFunc) http.HandlerFunc {
	withSession := s.requireSession(next)
	return func(w http.ResponseWriter, r *http.Request) {
		if got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && s.Local != nil && s.Local.ControlToken != "" &&
			subtle.ConstantTimeCompare([]byte(got), []byte(s.Local.ControlToken)) == 1 {
			next(w, r)
			return
		}
		withSession(w, r)
	}
}

type suggestionsResponse struct {
	Items []localrepos.Suggestion `json:"items"`
	// Start is the repository the CLI was started in, when it is one.
	Start *string `json:"start"`
}

func (s *Server) localSuggestions(w http.ResponseWriter, r *http.Request) {
	items, err := s.LocalRepos.Suggestions(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := suggestionsResponse{Items: items}
	if s.LocalRepos.Start != "" {
		for _, it := range items {
			if it.Path == s.LocalRepos.Start {
				p := it.Path
				out.Start = &p
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) localBrowse(w http.ResponseWriter, r *http.Request) {
	l, err := s.LocalRepos.Browse(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

type addLocalInput struct {
	Path      string `json:"path"`
	ProjectID string `json:"project_id"`
}

func (s *Server) addLocalRepo(w http.ResponseWriter, r *http.Request) {
	var in addLocalInput
	if !decode(w, r, &in) {
		return
	}
	s.writeAdded(w, r, in)
}

func (s *Server) writeAdded(w http.ResponseWriter, r *http.Request, in addLocalInput) {
	added, err := s.LocalRepos.Add(r.Context(), in.Path, in.ProjectID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	status := http.StatusOK
	if added.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, added)
}

func (s *Server) localRepoStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	st, err := s.LocalRepos.RepoStatus(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) locateLocalRepo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &in) {
		return
	}
	repo, err := s.LocalRepos.Locate(r.Context(), id, in.Path)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if s.ResumeDeliveries != nil {
		s.ResumeDeliveries(r.Context(), repo.ID)
	}
	writeJSON(w, http.StatusOK, repo)
}

// addRepositoryLocal handles POST /projects/{id}/repositories with
// source "local".
func (s *Server) addRepositoryLocal(w http.ResponseWriter, r *http.Request, in projects.RepoInput) {
	if s.LocalRepos == nil {
		writeErr(w, r, errLocalModeOnly)
		return
	}
	added, err := s.LocalRepos.Add(r.Context(), in.Path, r.PathValue("id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	status := http.StatusOK
	if added.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, added.Repository)
}
