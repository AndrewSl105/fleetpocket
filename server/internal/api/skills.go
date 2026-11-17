package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andrewsl105/fleetpocket/server/internal/skills"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// maxSkillUpload bounds an imported SKILL.md or zip.
const maxSkillUpload = 2 << 20

type skillInput struct {
	Slug             string            `json:"slug"`
	Body             string            `json:"body"`
	Files            []store.SkillFile `json:"files"`
	EnabledByDefault bool              `json:"enabled_by_default"`
}

// validateSkill runs skills.Validate (paths, limits, frontmatter, secrets)
// and returns the store row to write.
func (s *Server) validateSkill(in skillInput) (store.Skill, error) {
	files := make([]skills.File, 0, len(in.Files))
	for _, f := range in.Files {
		files = append(files, skills.File{Path: f.Path, Content: f.Content})
	}
	name, desc, err := skills.Validate(skills.Skill{Slug: in.Slug, Body: in.Body, Files: files}, s.SkillScanner)
	if err != nil {
		return store.Skill{}, err
	}
	out := []store.SkillFile{}
	for _, f := range in.Files {
		if f.Path != skills.SkillFileName {
			out = append(out, f)
		}
	}
	return store.Skill{Slug: in.Slug, Name: name, Description: desc, Body: in.Body, Files: out, EnabledByDefault: in.EnabledByDefault}, nil
}

func writeSkillErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, skills.ErrInvalid) {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	writeErr(w, r, err)
}

func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListSkills(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	vis := make([]*store.Skill, 0, len(list))
	for _, sk := range list {
		if s.skillVisible(r, sk) {
			vis = append(vis, sk)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": vis})
}

func (s *Server) getSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	sk, err := s.Store.GetSkill(r.Context(), id)
	if err == nil && !s.skillVisible(r, sk) {
		err = store.ErrNotFound
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sk)
}

// skillVisible: a personal skill is its owner's (and admins') only.
func (s *Server) skillVisible(r *http.Request, sk *store.Skill) bool {
	a := actorOf(r)
	return !a.Team || !sk.Personal || a.admin() || sk.OwnerUserID != nil && *sk.OwnerUserID == a.UserID
}

// skillEditable: admins edit any skill, members their personal ones.
func (s *Server) skillEditable(w http.ResponseWriter, r *http.Request, id int64) bool {
	a := actorOf(r)
	if !a.Team || a.admin() {
		return true
	}
	sk, err := s.Store.GetSkill(r.Context(), id)
	if err != nil {
		return true // the handler answers 404
	}
	if sk.Personal && sk.OwnerUserID != nil && *sk.OwnerUserID == a.UserID {
		return true
	}
	forbid(w, "members can change their personal skills only")
	return false
}

func (s *Server) createSkill(w http.ResponseWriter, r *http.Request) {
	var in skillInput
	if !decode(w, r, &in) {
		return
	}
	s.insertSkill(w, r, in)
}

func (s *Server) insertSkill(w http.ResponseWriter, r *http.Request, in skillInput) {
	sk, err := s.validateSkill(in)
	if err != nil {
		writeSkillErr(w, r, err)
		return
	}
	if a := actorOf(r); a.Team {
		// Admins' skills are team skills; members' are personal.
		sk.OwnerUserID, sk.Personal = a.uid(), !a.admin()
	}
	out, err := s.Store.CreateSkill(r.Context(), sk)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) updateSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok || !s.skillEditable(w, r, id) {
		return
	}
	var in skillInput
	if !decode(w, r, &in) {
		return
	}
	sk, err := s.validateSkill(in)
	if err != nil {
		writeSkillErr(w, r, err)
		return
	}
	out, err := s.Store.UpdateSkill(r.Context(), id, sk)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok || !s.skillEditable(w, r, id) {
		return
	}
	if err := s.Store.DeleteSkill(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// duplicateSkill copies a skill under the first free "<slug>-copy[-N]" slug.
func (s *Server) duplicateSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	src, err := s.Store.GetSkill(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	for n := 1; n <= 20; n++ {
		slug := src.Slug + "-copy"
		if n > 1 {
			slug = fmt.Sprintf("%s-copy-%d", src.Slug, n)
		}
		if len(slug) > 64 {
			break
		}
		cp := *src
		cp.Slug, cp.EnabledByDefault = slug, false
		out, err := s.Store.CreateSkill(r.Context(), cp)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		if err != nil {
			writeErr(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
		return
	}
	writeError(w, http.StatusConflict, "conflict", "no free slug for the copy")
}

// importSkill takes multipart field "file": a SKILL.md or a .zip of a skill
// folder; optional field "slug".
func (s *Server) importSkill(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSkillUpload+64<<10)
	if err := r.ParseMultipartForm(maxSkillUpload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "expected multipart form with a file (at most 2 MB)")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "file is required")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSkillUpload+1))
	if err != nil || len(data) > maxSkillUpload {
		writeError(w, http.StatusBadRequest, "invalid", "file is too large")
		return
	}
	slug := r.FormValue("slug")
	var sk skills.Skill
	if strings.HasSuffix(strings.ToLower(hdr.Filename), ".zip") {
		sk, err = skills.ImportZip(data, slug)
		if err != nil {
			writeSkillErr(w, r, err)
			return
		}
	} else {
		sk = skills.Skill{Slug: slug, Body: string(data)}
		if sk.Slug == "" {
			name, _, _ := skills.Frontmatter(sk.Body)
			sk.Slug = skills.Slugify(name)
		}
	}
	in := skillInput{Slug: sk.Slug, Body: sk.Body}
	for _, f := range sk.Files {
		in.Files = append(in.Files, store.SkillFile{Path: f.Path, Content: f.Content})
	}
	s.insertSkill(w, r, in)
}

func (s *Server) exportSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	sk, err := s.Store.GetSkill(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	in := skills.Skill{Slug: sk.Slug, Body: sk.Body}
	for _, f := range sk.Files {
		in.Files = append(in.Files, skills.File{Path: f.Path, Content: f.Content})
	}
	zip, err := skills.Export(in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, sk.Slug))
	_, _ = w.Write(zip)
}

// splitList splits a comma-separated multipart field.
func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
