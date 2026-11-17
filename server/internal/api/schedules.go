package api

import (
	"net/http"
	"strconv"

	"github.com/andrewsl105/fleetpocket/server/internal/schedules"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// GET /api/v1/schedules
func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.Schedules.List(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) getSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	sc, err := s.Schedules.Get(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	var in schedules.Input
	if !decode(w, r, &in) {
		return
	}
	sc, err := s.Schedules.Create(r.Context(), in)
	if err == nil {
		sc, err = s.authorSchedule(r, sc.ID)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sc)
}

func (s *Server) updateSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in schedules.Input
	if !decode(w, r, &in) {
		return
	}
	sc, err := s.Schedules.Update(r.Context(), id, in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.Schedules.Delete(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/schedules/{id}/enable and /disable
func (s *Server) setScheduleEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		sc, err := s.Schedules.SetEnabled(r.Context(), id, enabled)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, sc)
	}
}

// POST /api/v1/schedules/{id}/run fires the schedule now.
func (s *Server) runSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	f, err := s.Schedules.RunNow(r.Context(), id, "web")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) duplicateSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	sc, err := s.Schedules.Duplicate(r.Context(), id)
	if err == nil {
		sc, err = s.authorSchedule(r, sc.ID)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sc)
}

// GET /api/v1/schedules/{id}/firings: the history, newest first.
func (s *Server) scheduleFirings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	list, err := s.Schedules.History(r.Context(), id, limit)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// authorSchedule records the actor as the schedule's creator (team mode).
func (s *Server) authorSchedule(r *http.Request, id int64) (*store.Schedule, error) {
	if uid := actorOf(r).uid(); uid != nil {
		if err := s.Store.SetAuthor(r.Context(), "schedules", id, *uid); err != nil {
			return nil, err
		}
	}
	return s.Schedules.Get(r.Context(), id)
}
