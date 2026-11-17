package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/andrewsl105/fleetpocket/server/internal/attachments"
	"github.com/andrewsl105/fleetpocket/server/internal/ideas"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// ideasErr maps the board's own errors, then falls back to writeErr.
func ideasErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ideas.ErrDisabled):
		writeError(w, http.StatusConflict, "ideas_disabled", err.Error())
	case errors.Is(err, ideas.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	default:
		writeErr(w, r, err)
	}
}

func (s *Server) ideasOff(w http.ResponseWriter) bool {
	if s.Ideas == nil {
		writeError(w, http.StatusConflict, "ideas_disabled", ideas.ErrDisabled.Error())
		return true
	}
	return false
}

// GET /api/v1/settings/ideas: whether the board is on (default off) and its columns.
func (s *Server) getIdeaSettings(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.IdeaSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// PUT /api/v1/settings/ideas replaces the setting (columns: the whole list in order).
func (s *Server) putIdeaSettings(w http.ResponseWriter, r *http.Request) {
	if s.ideasOff(w) {
		return
	}
	var in store.IdeaSettings
	if !decode(w, r, &in) {
		return
	}
	c, err := s.Ideas.SetSettings(r.Context(), in)
	if err != nil {
		ideasErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// GET /api/v1/ideas?archived=&project=&tag=&q=
func (s *Server) listIdeas(w http.ResponseWriter, r *http.Request) {
	if s.ideasOff(w) {
		return
	}
	qs := r.URL.Query()
	list, err := s.Ideas.List(r.Context(), store.IdeaFilter{Archived: qs.Get("archived") == "true", ProjectID: qs.Get("project"),
		Tag: qs.Get("tag"), Query: qs.Get("q")})
	if err != nil {
		ideasErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func splitLines(v string) []string {
	var out []string
	for _, l := range strings.Split(v, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// POST /api/v1/ideas: JSON, or multipart with files[] (tags comma-separated,
// links one per line).
func (s *Server) createIdea(w http.ResponseWriter, r *http.Request) {
	if s.ideasOff(w) {
		return
	}
	var in ideas.Input
	var imgs []*attachments.Image
	if isMultipart(r) {
		f, files, ok := readMultipart(w, r, "title", "body", "project_id", "column", "tags", "links")
		if !ok {
			return
		}
		in = ideas.Input{Title: f["title"], Body: f["body"], Column: f["column"], Tags: splitList(f["tags"]), Links: splitLines(f["links"])}
		if v, ok := f["project_id"]; ok && v != "" {
			in.ProjectID = &v
		}
		imgs = files
	} else if !decode(w, r, &in) {
		return
	}
	in.Source = "web"
	idea, err := s.Ideas.Create(r.Context(), in, imgs)
	if err == nil {
		idea, err = s.authorIdea(r, idea)
	}
	if err != nil {
		ideasErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, idea)
}

// readVoice reads a multipart voice upload: the recording in `audio` (its
// part Content-Type is the recorder's type) and the text fields named.
func readVoice(w http.ResponseWriter, r *http.Request, fields ...string) (map[string]string, ideas.Voice, bool) {
	allowed := map[string]bool{"duration_ms": true}
	for _, f := range fields {
		allowed[f] = true
	}
	var v ideas.Voice
	values := map[string]string{}
	fail := func(msg string) (map[string]string, ideas.Voice, bool) {
		writeError(w, http.StatusBadRequest, "invalid", msg)
		return nil, v, false
	}
	if !isMultipart(r) {
		return fail("send the recording as multipart/form-data with an audio part")
	}
	r.Body = http.MaxBytesReader(w, r.Body, attachments.MaxFileBytes+maxBody)
	mr, err := r.MultipartReader()
	if err != nil {
		return fail("multipart body: " + err.Error())
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail("multipart body: " + err.Error())
		}
		name := part.FormName()
		switch {
		case name == "audio":
			if v.Data != nil {
				return fail("one recording per request")
			}
			data, err := io.ReadAll(io.LimitReader(part, attachments.MaxFileBytes+1))
			if err != nil {
				return fail("multipart body: " + err.Error())
			}
			if len(data) > attachments.MaxFileBytes {
				return fail(fmt.Sprintf("the recording is larger than %d MB", attachments.MaxFileBytes>>20))
			}
			v.Data, v.Mime, v.Name = data, part.Header.Get("Content-Type"), part.FileName()
		case allowed[name]:
			b, err := io.ReadAll(io.LimitReader(part, maxBody+1))
			if err != nil || len(b) > maxBody {
				return fail("field " + name + " is too long")
			}
			values[name] = string(b)
		default:
			return fail(fmt.Sprintf("unknown field %q", name))
		}
	}
	if v.Data == nil {
		return fail("the audio part is missing")
	}
	if d := values["duration_ms"]; d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return fail("duration_ms must be a non-negative integer")
		}
		v.DurationMS = n
	}
	return values, v, true
}

// POST /api/v1/ideas/voice: a hold-to-talk recording becomes a new idea.
func (s *Server) createVoiceIdea(w http.ResponseWriter, r *http.Request) {
	if s.ideasOff(w) {
		return
	}
	f, v, ok := readVoice(w, r, "project_id", "column")
	if !ok {
		return
	}
	in := ideas.Input{Column: f["column"], Source: "web"}
	if p := f["project_id"]; p != "" {
		in.ProjectID = &p
	}
	idea, err := s.Ideas.CreateVoice(r.Context(), in, v)
	if err == nil {
		idea, err = s.authorIdea(r, idea)
	}
	if err != nil {
		ideasErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, idea)
}

func (s *Server) ideaHandler(fn func(w http.ResponseWriter, r *http.Request, id int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.ideasOff(w) {
			return
		}
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		fn(w, r, id)
	}
}

func (s *Server) respondIdea(w http.ResponseWriter, r *http.Request, idea *store.Idea, err error) {
	if err != nil {
		ideasErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, idea)
}

func (s *Server) getIdea(w http.ResponseWriter, r *http.Request, id int64) {
	idea, err := s.Ideas.Get(r.Context(), id)
	s.respondIdea(w, r, idea, err)
}

func (s *Server) updateIdea(w http.ResponseWriter, r *http.Request, id int64) {
	var p ideas.Patch
	if !decode(w, r, &p) {
		return
	}
	idea, err := s.Ideas.Update(r.Context(), id, p)
	s.respondIdea(w, r, idea, err)
}

func (s *Server) deleteIdea(w http.ResponseWriter, r *http.Request, id int64) {
	if err := s.Ideas.Delete(r.Context(), id); err != nil {
		ideasErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) moveIdea(w http.ResponseWriter, r *http.Request, id int64) {
	var in struct {
		Column  string `json:"column"`
		AfterID *int64 `json:"after_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	idea, err := s.Ideas.Move(r.Context(), id, in.Column, in.AfterID)
	s.respondIdea(w, r, idea, err)
}

func (s *Server) archiveIdea(archived bool) func(w http.ResponseWriter, r *http.Request, id int64) {
	return func(w http.ResponseWriter, r *http.Request, id int64) {
		idea, err := s.Ideas.SetArchived(r.Context(), id, archived)
		s.respondIdea(w, r, idea, err)
	}
}

func (s *Server) addIdeaFiles(w http.ResponseWriter, r *http.Request, id int64) {
	if !isMultipart(r) {
		writeError(w, http.StatusBadRequest, "invalid", "send files as multipart/form-data in files[]")
		return
	}
	_, imgs, ok := readMultipart(w, r)
	if !ok {
		return
	}
	if len(imgs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid", "no files")
		return
	}
	idea, err := s.Ideas.AddFiles(r.Context(), id, imgs)
	s.respondIdea(w, r, idea, err)
}

func (s *Server) addIdeaVoice(w http.ResponseWriter, r *http.Request, id int64) {
	_, v, ok := readVoice(w, r)
	if !ok {
		return
	}
	idea, err := s.Ideas.AddVoice(r.Context(), id, v)
	s.respondIdea(w, r, idea, err)
}

func (s *Server) addIdeaLink(w http.ResponseWriter, r *http.Request, id int64) {
	var in struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &in) {
		return
	}
	idea, err := s.Ideas.AddLink(r.Context(), id, in.URL)
	s.respondIdea(w, r, idea, err)
}

func (s *Server) deleteIdeaAttachment(w http.ResponseWriter, r *http.Request, id int64) {
	aid, ok := pathID(w, r, "attachmentId")
	if !ok {
		return
	}
	idea, err := s.Ideas.RemoveAttachment(r.Context(), id, aid)
	s.respondIdea(w, r, idea, err)
}

// GET /api/v1/ideas/{id}/attachments/{attachmentId}(?thumb=1): images and
// voice notes inline with their type, other files as downloads.
func (s *Server) ideaAttachment(w http.ResponseWriter, r *http.Request, id int64) {
	aid, ok := pathID(w, r, "attachmentId")
	if !ok {
		return
	}
	if s.Files == nil {
		writeError(w, http.StatusNotFound, "not_found", "attachments are not configured")
		return
	}
	if _, err := s.Ideas.Get(r.Context(), id); err != nil {
		ideasErr(w, r, err)
		return
	}
	a, err := s.Store.GetIdeaAttachment(r.Context(), id, aid)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	path, ctype, name, disposition := s.Files.IdeaPath(a), a.Mime, a.Name, "inline"
	_, audio := attachments.AudioType(a.Mime)
	switch {
	case r.URL.Query().Get("thumb") == "1":
		if !a.HasThumb {
			writeError(w, http.StatusNotFound, "not_found", "the attachment has no thumbnail")
			return
		}
		path, ctype, name = s.Files.IdeaThumbPath(a), "image/jpeg", "thumb-"+a.Name
	case a.Kind == store.IdeaLink:
		writeError(w, http.StatusNotFound, "not_found", "a link has no file")
		return
	case a.Kind == store.IdeaVoice && audio, attachments.IsImage(a.Mime):
	default:
		ctype, disposition = "application/octet-stream", "attachment"
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusGone, "gone", "the attachment was removed")
		return
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Content-Disposition", disposition+"; filename*=UTF-8''"+url.PathEscape(name))
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// authorIdea records the actor as the idea's creator (team mode).
func (s *Server) authorIdea(r *http.Request, idea *store.Idea) (*store.Idea, error) {
	uid := actorOf(r).uid()
	if uid == nil {
		return idea, nil
	}
	if err := s.Store.SetAuthor(r.Context(), "ideas", idea.ID, *uid); err != nil {
		return nil, err
	}
	idea.CreatedByID = uid
	if u := actorOf(r).User; u != nil {
		idea.CreatedBy = u.Ref()
	}
	return idea, nil
}
