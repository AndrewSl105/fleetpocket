package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/attachments"
	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

// ---- auth ----

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.Limiter.Blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts, try again later")
		return
	}
	var in struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
		// Email signs in a team member (team mode). Without it the owner
		// credentials (env or setup wizard) sign in as user #1: the
		// break-glass admin login.
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	if s.Team.Enabled(ctx) {
		var u *store.User
		var err error
		if in.Email != "" {
			u, err = s.Team.Login(ctx, in.Email, in.Password, in.TOTP)
			if err != nil {
				// The owner may type their email with the owner credentials.
				if owner, oerr := s.Store.OwnerUser(ctx); oerr == nil && owner.Email == store.NormalizeEmail(in.Email) &&
					s.Auth.CheckLogin(in.Password, in.TOTP) == nil {
					u, err = owner, nil
				}
			}
		} else if err = s.Auth.CheckLogin(in.Password, in.TOTP); err == nil {
			u, err = s.Store.OwnerUser(ctx)
			if err == nil {
				_ = s.Team.Store.Audit(ctx, &u.ID, "auth.break_glass", "user", strconv.FormatInt(u.ID, 10), nil)
			}
		}
		if err != nil || u == nil {
			s.Limiter.Fail(ip)
			writeError(w, http.StatusUnauthorized, "bad_credentials", "invalid email, password or code")
			return
		}
		_, _ = s.Store.UpdateUser(ctx, u.ID, store.UserFields{Touch: true})
		if !s.startUserSession(w, r, u, "web") {
			return
		}
		writeJSON(w, http.StatusOK, s.sessionInfoFor(ctx, "web", u))
		return
	}
	if err := s.Auth.CheckLogin(in.Password, in.TOTP); err != nil {
		s.Limiter.Fail(ip)
		writeError(w, http.StatusUnauthorized, "bad_credentials", "invalid password or code")
		return
	}
	if _, err := s.Auth.StartSession(ctx, w, "web"); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sessionInfo(ctx, "web"))
}

func (s *Server) loginTelegram(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.Limiter.Blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts, try again later")
		return
	}
	var in struct {
		InitData string `json:"init_data"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !s.Cfg.TelegramEnabled() {
		writeError(w, http.StatusServiceUnavailable, "telegram_disabled", "Telegram is not configured")
		return
	}
	u, err := auth.VerifyInitData(in.InitData, s.Cfg.TelegramBotToken, time.Hour, s.now())
	// Team mode: any linked, active user; otherwise the one allowed id.
	var member *store.User
	if err == nil && s.Team.Enabled(r.Context()) {
		member, err = s.Team.TelegramUser(r.Context(), u.ID)
	} else if err == nil && u.ID != s.Cfg.TelegramAllowedUserID {
		err = auth.ErrBadCredentials
	}
	if err != nil {
		s.Limiter.Fail(ip)
		writeError(w, http.StatusUnauthorized, "bad_credentials", "invalid Telegram init data")
		return
	}
	token, err := s.Auth.StartSession(r.Context(), w, "telegram")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	info := s.sessionInfo(r.Context(), "telegram")
	if member != nil {
		id, _, _ := cutDot(token)
		if err := s.Store.SetSessionUser(r.Context(), id, member.ID); err != nil {
			writeErr(w, r, err)
			return
		}
		info = s.sessionInfoFor(r.Context(), "telegram", member)
	}
	// The cookie works where the Mini App is the top-level page (Telegram's
	// native apps); Telegram Web embeds it cross-site, so the client keeps
	// this token in memory and sends it as a bearer token instead.
	info["token"] = token
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.EndSession(r.Context(), w, r); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// wsTicket issues a single-use WebSocket ticket for the current session.
func (s *Server) wsTicket(w http.ResponseWriter, r *http.Request) {
	id, _ := r.Context().Value(sessionIDKey).(string)
	v, err := s.Tickets.Issue(id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": v, "expires_in": int(s.Tickets.TTL.Seconds())})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	kind, _ := r.Context().Value(sessionKindKey).(string)
	if a := actorOf(r); a.Team {
		writeJSON(w, http.StatusOK, s.sessionInfoFor(r.Context(), kind, a.User))
		return
	}
	writeJSON(w, http.StatusOK, s.sessionInfo(r.Context(), kind))
}

// sessionInfoFor is SessionInfo in team mode: every agent can be used (the
// person's key decides), plus the signed-in user.
func (s *Server) sessionInfoFor(ctx context.Context, kind string, u *store.User) map[string]any {
	info := s.sessionInfo(ctx, kind)
	info["agents"] = config.Agents
	info["user"] = meView{User: u, Initials: store.Initials(u.Name), Telegram: u.TelegramUserID != nil}
	return info
}

// sessionInfo is the SessionInfo body: the session kind, the agents tasks
// may use, the mode and whether the Ideas board is on.
func (s *Server) sessionInfo(ctx context.Context, kind string) map[string]any {
	def := s.Cfg.DefaultAgent
	if def == "" {
		def = "claude"
	}
	mode := s.Cfg.Mode
	if mode == "" {
		mode = config.ModeServer
	}
	return map[string]any{"ok": true, "kind": kind, "agents": s.Cfg.EnabledAgents(), "default_agent": def, "mode": mode,
		"ideas_enabled": s.Ideas != nil && s.Ideas.Enabled(ctx), "team_enabled": s.Team.Enabled(ctx)}
}

// ---- projects ----

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	limit := limitParam(r)
	archived := r.URL.Query().Get("archived") == "true"
	items, err := s.Store.ListProjects(r.Context(), archived, r.URL.Query().Get("cursor"), limit+1)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := page[*store.Project]{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		out.NextCursor = items[limit-1].ID
	}
	if out.Items == nil {
		out.Items = []*store.Project{}
	}
	if err := s.withMonthCost(r.Context(), out.Items...); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in projects.CreateInput
	if !decode(w, r, &in) {
		return
	}
	p, err := s.Projects.Create(r.Context(), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetProject(r.Context(), r.PathValue("id"))
	if err == nil {
		err = s.withMonthCost(r.Context(), p)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	var in store.ProjectFields
	if !decode(w, r, &in) {
		return
	}
	p, err := s.Projects.Update(r.Context(), r.PathValue("id"), in)
	if err == nil {
		err = s.withMonthCost(r.Context(), p)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if in.Budget != nil && s.Tasks != nil && s.Tasks.Executor != nil {
		s.Tasks.Executor.Wake() // a raised cap lets waiting tasks start
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) archiveProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.Projects.Archive(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) addRepository(w http.ResponseWriter, r *http.Request) {
	var in projects.RepoInput
	if !decode(w, r, &in) {
		return
	}
	if in.Source == "local" {
		s.addRepositoryLocal(w, r, in)
		return
	}
	repo, err := s.Projects.AddRepository(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, repo)
}

func (s *Server) updateRepository(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "repoId")
	if !ok {
		return
	}
	var in projects.RepoUpdateInput
	if !decode(w, r, &in) {
		return
	}
	repo, err := s.Projects.UpdateRepository(r.Context(), r.PathValue("id"), id, in)
	if err != nil {
		if errors.Is(err, store.ErrRepoInUse) {
			writeError(w, http.StatusConflict, "in_use", err.Error())
			return
		}
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (s *Server) deleteRepository(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "repoId")
	if !ok {
		return
	}
	if err := s.Store.DeleteRepository(r.Context(), r.PathValue("id"), id); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "in_use", "repository is used by tasks")
			return
		}
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) githubRepositories(w http.ResponseWriter, r *http.Request) {
	if s.GitHub == nil {
		writeErr(w, r, errGitHubDisabled)
		return
	}
	repos, err := s.GitHub.ListRepositories(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if repos == nil {
		repos = []GitHubRepo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": repos})
}

// ---- tasks ----

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	limit := limitParam(r)
	f := store.TaskFilter{ProjectID: qs.Get("project"), Limit: limit + 1}
	if st := qs.Get("status"); st != "" {
		f.Statuses = strings.Split(st, ",")
	}
	// Teams: ?created_by=me or a user id (the Mine | person filters).
	switch v := qs.Get("created_by"); v {
	case "":
	case "me":
		f.CreatedBy = actorOf(r).UserID
		if f.CreatedBy == 0 {
			f.CreatedBy = -1 // single-owner mode: nobody is "me"
		}
	default:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "created_by must be me or a user id")
			return
		}
		f.CreatedBy = n
	}
	if c := qs.Get("cursor"); c != "" {
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "bad cursor")
			return
		}
		f.Cursor = n
	}
	items, err := s.Tasks.List(r.Context(), f)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := page[*store.Task]{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		out.NextCursor = strconv.FormatInt(items[limit-1].ID, 10)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var in tasks.CreateInput
	if isMultipart(r) {
		f, imgs, ok := readMultipart(w, r, "project_id", "repository_id", "title", "prompt", "agent", "node", "output", "skills_add", "skills_remove", "loop", "start_from", "idea_id")
		if !ok {
			return
		}
		repoID, err := formInt64(f, "repository_id")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		ideaID, err := formInt64(f, "idea_id")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		in = tasks.CreateInput{ProjectID: f["project_id"], RepositoryID: repoID, Title: f["title"], Prompt: f["prompt"], Agent: f["agent"], Node: f["node"], Images: imgs,
			Output: f["output"], SkillsAdd: splitList(f["skills_add"]), SkillsRemove: splitList(f["skills_remove"]), StartFrom: f["start_from"], IdeaID: ideaID}
		if f["loop"] != "" {
			if err := json.Unmarshal([]byte(f["loop"]), &in.Loop); err != nil {
				writeError(w, http.StatusBadRequest, "invalid", "loop: "+err.Error())
				return
			}
		}
	} else if !decode(w, r, &in) {
		return
	}
	in.Source = "web"
	in.CreatedBy = actorOf(r).uid()
	// A task started from an idea: check the idea first, so a bad idea_id
	// creates no task; then link it, which moves the card to In progress.
	if in.IdeaID != 0 {
		if s.ideasOff(w) {
			return
		}
		if err := s.Ideas.CheckLink(r.Context(), in.IdeaID); err != nil {
			ideasErr(w, r, err)
			return
		}
	}
	v, err := s.Tasks.Create(r.Context(), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if in.IdeaID != 0 {
		if err := s.Ideas.LinkTask(r.Context(), in.IdeaID, v.Task); err != nil {
			slog.WarnContext(r.Context(), "link idea", "idea_id", in.IdeaID, "task_id", v.ID, "err", err)
		}
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	v, err := s.Tasks.Get(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) taskEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.Store.GetTask(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	qs := r.URL.Query()
	after := qs.Get("after_id")
	if after == "" {
		after = qs.Get("cursor")
	}
	limit := limitParam(r)
	var items []*store.Event
	var err error
	if qs.Has("before_id") {
		if after != "" {
			writeError(w, http.StatusBadRequest, "invalid", "before_id cannot be combined with after_id or cursor")
			return
		}
		beforeID, perr := strconv.ParseInt(qs.Get("before_id"), 10, 64)
		if perr != nil || beforeID < 0 {
			writeError(w, http.StatusBadRequest, "invalid", "bad before_id")
			return
		}
		items, err = s.Store.ListEventsBefore(r.Context(), id, beforeID, limit+1)
	} else {
		afterID, _ := strconv.ParseInt(after, 10, 64)
		items, err = s.Store.ListEvents(r.Context(), id, afterID, limit+1)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := page[*store.Event]{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		out.NextCursor = strconv.FormatInt(items[limit-1].ID, 10)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) taskRuns(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.Store.GetTask(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	var afterID int64
	if c := r.URL.Query().Get("cursor"); c != "" {
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "bad cursor")
			return
		}
		afterID = n
	}
	limit := limitParam(r)
	items, err := s.Store.ListRuns(r.Context(), id, afterID, limit+1)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := page[*store.Run]{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		out.NextCursor = strconv.FormatInt(items[limit-1].ID, 10)
	}
	writeJSON(w, http.StatusOK, out)
}

const maxDiff = 1 << 20

func (s *Server) taskDiff(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	t, err := s.Store.GetTask(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	repo, err := s.Store.GetRepository(r.Context(), t.RepositoryID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if s.Diff == nil || t.Branch == nil {
		writeError(w, http.StatusConflict, "no_workspace", "task has no workspace yet")
		return
	}
	diff, truncated, err := s.Diff(r.Context(), t, repo, maxDiff)
	if errors.Is(err, hosts.ErrOffline) {
		writeErr(w, r, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, "no_workspace", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diff": diff, "truncated": truncated, "base": repo.BaseBranch, "branch": *t.Branch})
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		QuestionID string `json:"question_id"`
		Text       string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, err := s.Tasks.Answer(r.Context(), id, in.QuestionID, in.Text, "web")
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "already_answered", "question was already answered")
			return
		}
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) message(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	var imgs []*attachments.Image
	if isMultipart(r) {
		f, files, ok := readMultipart(w, r, "text")
		if !ok {
			return
		}
		in.Text, imgs = f["text"], files
	} else if !decode(w, r, &in) {
		return
	}
	v, err := s.Tasks.Message(r.Context(), id, in.Text, imgs)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) control(fn func(context.Context, int64) (*tasks.View, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		v, err := fn(r.Context(), id)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.GetStats(r.Context(), s.now())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
