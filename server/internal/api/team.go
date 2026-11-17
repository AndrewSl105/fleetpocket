package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/team"
)

// Actor is who makes a request. In single-owner mode it is the owner with
// every right (UserID 0, Team false).
type Actor struct {
	Team   bool
	UserID int64
	Role   string
	User   *store.User
}

const actorKey ctxKey = 3

func actorOf(r *http.Request) Actor {
	if a, ok := r.Context().Value(actorKey).(Actor); ok {
		return a
	}
	return Actor{Role: store.RoleAdmin}
}

// uid is the actor's user id for authorship (nil in single-owner mode).
func (a Actor) uid() *int64 {
	if !a.Team {
		return nil
	}
	id := a.UserID
	return &id
}

func (a Actor) admin() bool { return a.Role == store.RoleAdmin }

// Policy is the role a route needs (docs/SPEC-server.md "Roles").
type Policy int

const (
	// PolicyRead: any signed-in user (viewers too).
	PolicyRead Policy = iota
	// PolicySelf: any signed-in user acting on their own account.
	PolicySelf
	// PolicyWrite: admins and members (create tasks, schedules, ideas).
	PolicyWrite
	// PolicyOwnTask: admins on any task, members on their own ({id} is the task).
	PolicyOwnTask
	// PolicyOwnSchedule / PolicyOwnIdea: the same for schedules and ideas.
	PolicyOwnSchedule
	PolicyOwnIdea
	// PolicyAdmin: admins only.
	PolicyAdmin
)

// Route is one registered endpoint and its policy (the role-matrix test
// iterates them).
type Route struct {
	Pattern string
	Policy  Policy
}

// Routes lists the session routes with their policies (after Handler).
func (s *Server) Routes() []Route { return s.routes }

// withActor resolves the session's user in team mode. A session without a
// user (or a disabled one) is refused once team mode is on.
func (s *Server) withActor(r *http.Request) (*http.Request, bool) {
	if !s.Team.Enabled(r.Context()) {
		return r, true
	}
	sid, _ := r.Context().Value(sessionIDKey).(string)
	uid, err := s.Store.SessionUser(r.Context(), sid)
	if err != nil || uid == nil {
		return r, false
	}
	u, err := s.Store.GetUser(r.Context(), *uid)
	if err != nil || u.DisabledAt != nil {
		return r, false
	}
	a := Actor{Team: true, UserID: u.ID, Role: u.Role, User: u}
	return r.WithContext(context.WithValue(r.Context(), actorKey, a)), true
}

// policy wraps a session route with its role check.
func (s *Server) policy(p Policy, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r, ok := s.withActor(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "login required")
			return
		}
		a := actorOf(r)
		if !a.Team {
			next(w, r)
			return
		}
		switch p {
		case PolicyRead, PolicySelf:
		case PolicyWrite:
			if !team.CanWrite(a.Role) {
				forbid(w, "viewers cannot change anything")
				return
			}
		case PolicyAdmin:
			if !a.admin() {
				forbid(w, "admins only")
				return
			}
		case PolicyOwnTask, PolicyOwnSchedule, PolicyOwnIdea:
			if !s.ownerCheck(w, r, p, a) {
				return
			}
		}
		next(w, r)
	}
}

func forbid(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusForbidden, "forbidden", msg)
}

// ownerCheck lets admins act on anything (audit-logged when it is someone
// else's) and members on what they created.
func (s *Server) ownerCheck(w http.ResponseWriter, r *http.Request, p Policy, a Actor) bool {
	if !team.CanWrite(a.Role) {
		forbid(w, "viewers cannot change anything")
		return false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return true // the handler answers the bad id
	}
	var owner *int64
	kind := "task"
	switch p {
	case PolicyOwnTask:
		t, err := s.Store.GetTask(r.Context(), id)
		if err != nil {
			return true // 404 from the handler
		}
		owner = t.CreatedByID
	case PolicyOwnSchedule:
		kind = "schedule"
		owner, err = s.Store.AuthorOf(r.Context(), "schedules", id)
	case PolicyOwnIdea:
		kind = "idea"
		owner, err = s.Store.AuthorOf(r.Context(), "ideas", id)
	}
	if errors.Is(err, store.ErrNotFound) {
		return true
	}
	if !team.CanActOn(a.Role, a.UserID, owner) {
		forbid(w, "only its creator or an admin can do this")
		return false
	}
	if a.admin() && (owner == nil || *owner != a.UserID) && r.Method != http.MethodGet {
		s.Team.Audit(r.Context(), a.uid(), kind+".admin_action", kind, strconv.FormatInt(id, 10),
			map[string]any{"method": r.Method, "path": r.URL.Path, "owner": owner})
	}
	return true
}

// teamOn answers team endpoints when team mode is unavailable.
func (s *Server) teamErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, team.ErrLocalMode):
		writeError(w, http.StatusBadRequest, "not_available_in_local_mode", "teams are not available in local mode")
	case errors.Is(err, team.ErrOff):
		writeError(w, http.StatusConflict, "team_off", err.Error())
	case errors.Is(err, team.ErrNoSecretsKey):
		writeError(w, http.StatusConflict, "secrets_key_missing", err.Error())
	case errors.Is(err, team.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, team.ErrBadCode):
		writeError(w, http.StatusBadRequest, "bad_code", "invalid or expired code")
	case errors.Is(err, team.ErrInviteGone):
		writeError(w, http.StatusGone, "invite_gone", err.Error())
	default:
		writeErr(w, r, err)
	}
}

// teamGuard refuses team endpoints in local mode (and without a service).
func (s *Server) teamGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Cfg != nil && s.Cfg.Local() || s.Team == nil {
			writeError(w, http.StatusBadRequest, "not_available_in_local_mode", "teams are not available in local mode")
			return
		}
		next(w, r)
	}
}

// ---- /team ----

func (s *Server) teamStatus(w http.ResponseWriter, r *http.Request) {
	st := s.Team.Settings(r.Context())
	out := map[string]any{"enabled": st.Enabled, "shared_key_fallback": st.SharedKeyFallback,
		"secrets_key": s.Team.Box.Enabled(), "server_keys": s.Cfg.EnabledAgents()}
	if st.Enabled {
		users, admins, err := s.Store.CountActive(r.Context())
		if err != nil {
			writeErr(w, r, err)
			return
		}
		out["active_users"], out["active_admins"] = users, admins
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) teamEnable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	if s.Team.Enabled(r.Context()) {
		writeError(w, http.StatusConflict, "conflict", "team mode is already on")
		return
	}
	u, err := s.Team.Enable(r.Context(), in.Email, in.Name)
	if err != nil {
		s.teamErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "user": u})
}

func (s *Server) teamDisable(w http.ResponseWriter, r *http.Request) {
	if err := s.Team.Disable(r.Context(), actorOf(r).UserID); err != nil {
		s.teamErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

func (s *Server) teamSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SharedKeyFallback *bool `json:"shared_key_fallback"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.SharedKeyFallback != nil {
		if err := s.Team.SetFallback(r.Context(), actorOf(r).UserID, *in.SharedKeyFallback); err != nil {
			s.teamErr(w, r, err)
			return
		}
		if s.Tasks != nil && s.Tasks.Executor != nil {
			s.Tasks.Executor.Wake()
		}
	}
	s.teamStatus(w, r)
}

// ---- /users ----

// userView is a user as admins see them in the list.
type userView struct {
	*store.User
	Initials string `json:"initials"`
	Active   bool   `json:"active"`
	Telegram bool   `json:"telegram_linked"`
}

func viewUser(u *store.User) userView {
	return userView{User: u, Initials: store.Initials(u.Name), Active: u.Active(), Telegram: u.TelegramUserID != nil}
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	if !s.Team.Enabled(r.Context()) {
		s.teamErr(w, r, team.ErrOff)
		return
	}
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := make([]userView, 0, len(users))
	for _, u := range users {
		v := viewUser(u)
		if !actorOf(r).admin() {
			// Members and viewers see names for the person filter only.
			v.User = &store.User{ID: u.ID, Name: u.Name, Role: u.Role, CreatedAt: u.CreatedAt, DisabledAt: u.DisabledAt}
			v.Telegram = false
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Role             *string  `json:"role"`
		MonthlyBudgetUSD *float64 `json:"monthly_budget_usd"`
		ClearBudget      bool     `json:"clear_budget"`
		Disabled         *bool    `json:"disabled"`
		// LiveTasks is what happens to a disabled user's running tasks:
		// "finish" (default) or "cancel".
		LiveTasks string `json:"live_tasks"`
	}
	if !decode(w, r, &in) {
		return
	}
	actor := actorOf(r).UserID
	ctx := r.Context()
	if u, err := s.Store.GetUser(ctx, id); err == nil && u.IsOwner && (in.Role != nil && *in.Role != store.RoleAdmin || in.Disabled != nil && *in.Disabled) {
		writeError(w, http.StatusConflict, "conflict", "the owner (user #1) is the break-glass admin and cannot be demoted or disabled")
		return
	}
	var result any
	if in.Role != nil {
		if _, err := s.Team.SetRole(ctx, actor, id, *in.Role); err != nil {
			s.teamErr(w, r, err)
			return
		}
	}
	if in.MonthlyBudgetUSD != nil || in.ClearBudget {
		if _, err := s.Team.SetBudget(ctx, actor, id, in.MonthlyBudgetUSD); err != nil {
			s.teamErr(w, r, err)
			return
		}
		if s.Tasks != nil && s.Tasks.Executor != nil {
			s.Tasks.Executor.Wake()
		}
	}
	if in.Disabled != nil {
		if *in.Disabled {
			if in.LiveTasks != "" && in.LiveTasks != "finish" && in.LiveTasks != "cancel" {
				writeError(w, http.StatusBadRequest, "invalid", "live_tasks must be finish or cancel")
				return
			}
			res, err := s.Team.DisableUser(ctx, actor, id, in.LiveTasks)
			if err != nil {
				s.teamErr(w, r, err)
				return
			}
			// Through the task service: status events, runner cleanup, pushes.
			for _, tid := range res.Cancelled {
				_, _ = s.Tasks.Cancel(ctx, tid)
			}
			if in.LiveTasks == "cancel" {
				for _, tid := range res.Live {
					_, _ = s.Tasks.Cancel(ctx, tid)
				}
			}
			// Their computers' tokens are void; drop connections still open.
			if s.Nodes != nil && s.Nodes.Registry != nil {
				for _, id := range res.Nodes {
					s.Nodes.Registry.Kick(id)
				}
			}
			result = res
		} else if err := s.Team.EnableUser(ctx, actor, id); err != nil {
			s.teamErr(w, r, err)
			return
		}
	}
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(u), "disable": result})
}

func (s *Server) removeUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if u, err := s.Store.GetUser(r.Context(), id); err == nil && u.IsOwner {
		writeError(w, http.StatusConflict, "conflict", "the owner (user #1) is the break-glass admin and cannot be removed")
		return
	}
	if err := s.Team.RemoveUser(r.Context(), actorOf(r).UserID, id); err != nil {
		s.teamErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- /invites ----

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role           string  `json:"role"`
		Email          *string `json:"email"`
		ExpiresInHours int     `json:"expires_in_hours"`
	}
	if !decode(w, r, &in) {
		return
	}
	inv, tok, err := s.Team.CreateInvite(r.Context(), actorOf(r).UserID, in.Role, in.Email, time.Duration(in.ExpiresInHours)*time.Hour)
	if err != nil {
		s.teamErr(w, r, err)
		return
	}
	base := s.Cfg.WebAppURL
	if base == "" {
		base = s.publicURL(r)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"invite": inv, "token": tok, "url": base + "/signup?token=" + tok})
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request) {
	if !s.Team.Enabled(r.Context()) {
		s.teamErr(w, r, team.ErrOff)
		return
	}
	items, err := s.Store.ListInvites(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.Team.RevokeInvite(r.Context(), actorOf(r).UserID, id); err != nil {
		s.teamErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- /signup (no session) ----

func (s *Server) signupStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	ip := clientIP(r)
	if s.Limiter.Blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts, try again later")
		return
	}
	e, err := s.Team.SignupStart(r.Context(), in.Token)
	if err != nil {
		if errors.Is(err, team.ErrInviteGone) {
			s.Limiter.Fail(ip)
		}
		s.teamErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	var in team.SignupInput
	if !decode(w, r, &in) {
		return
	}
	ip := clientIP(r)
	if s.Limiter.Blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts, try again later")
		return
	}
	u, err := s.Team.SignupFinish(r.Context(), in)
	if err != nil {
		if errors.Is(err, team.ErrBadCode) || errors.Is(err, team.ErrInviteGone) {
			s.Limiter.Fail(ip)
		}
		s.teamErr(w, r, err)
		return
	}
	if !s.startUserSession(w, r, u, "web") {
		return
	}
	writeJSON(w, http.StatusCreated, s.sessionInfoFor(r.Context(), "web", u))
}

// startUserSession starts a session that belongs to u.
func (s *Server) startUserSession(w http.ResponseWriter, r *http.Request, u *store.User, kind string) bool {
	tok, err := s.Auth.StartSession(r.Context(), w, kind)
	if err != nil {
		writeErr(w, r, err)
		return false
	}
	id, _, _ := cutDot(tok)
	if err := s.Store.SetSessionUser(r.Context(), id, u.ID); err != nil {
		writeErr(w, r, err)
		return false
	}
	return true
}

func cutDot(tok string) (string, string, bool) {
	for i := 0; i < len(tok); i++ {
		if tok[i] == '.' {
			return tok[:i], tok[i+1:], true
		}
	}
	return tok, "", false
}

// ---- /me (team) ----

type meView struct {
	*store.User
	Initials string `json:"initials"`
	Telegram bool   `json:"telegram_linked"`
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	var in team.ProfileInput
	if !decode(w, r, &in) {
		return
	}
	sid, _ := r.Context().Value(sessionIDKey).(string)
	u, err := s.Team.UpdateProfile(r.Context(), actorOf(r).UserID, in, sid)
	if err != nil {
		s.teamErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, meView{User: u, Initials: store.Initials(u.Name), Telegram: u.TelegramUserID != nil})
}

func (s *Server) totpResetStart(w http.ResponseWriter, r *http.Request) {
	e, err := s.Team.TOTPResetStart(r.Context(), actorOf(r).UserID)
	if err != nil {
		s.teamErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) totpResetFinish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EnrollmentID string `json:"enrollment_id"`
		Password     string `json:"password"`
		TOTP         string `json:"totp"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.Team.TOTPResetFinish(r.Context(), actorOf(r).UserID, in.EnrollmentID, in.Password, in.TOTP); err != nil {
		s.teamErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- /me/credentials ----

func (s *Server) listCredentials(w http.ResponseWriter, r *http.Request) {
	if !s.Team.Enabled(r.Context()) {
		s.teamErr(w, r, team.ErrOff)
		return
	}
	items, err := s.Store.ListCredentials(r.Context(), actorOf(r).UserID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "shared_key_fallback": s.Team.Settings(r.Context()).SharedKeyFallback})
}

func (s *Server) putCredential(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
		Label string `json:"label"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := s.Team.PutKey(r.Context(), actorOf(r).UserID, r.PathValue("provider"), in.Kind, in.Value, in.Label)
	if err != nil {
		s.teamErr(w, r, err)
		return
	}
	if s.Tasks != nil && s.Tasks.Executor != nil {
		s.Tasks.Executor.Wake()
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteCredential(w http.ResponseWriter, r *http.Request) {
	if err := s.Team.DeleteKey(r.Context(), actorOf(r).UserID, r.PathValue("provider")); err != nil {
		s.teamErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) testCredential(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	k, err := s.Team.KeyFor(r.Context(), actorOf(r).UserID, provider)
	if err != nil {
		s.teamErr(w, r, err)
		return
	}
	tester := s.KeyTester
	if tester == nil {
		tester = team.DefaultTester
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res := tester.Test(ctx, provider, k)
	writeJSON(w, http.StatusOK, res)
}

// ---- /me/telegram ----

func (s *Server) telegramLink(w http.ResponseWriter, r *http.Request) {
	code, exp, err := s.Team.LinkCode(r.Context(), actorOf(r).UserID)
	if err != nil {
		s.teamErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "command": "/link " + code, "expires_at": exp,
		"bot_enabled": s.Cfg.TelegramEnabled()})
}

func (s *Server) telegramUnlink(w http.ResponseWriter, r *http.Request) {
	if err := s.Team.Unlink(r.Context(), actorOf(r).UserID); err != nil {
		s.teamErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- /audit ----

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	if !s.Team.Enabled(r.Context()) {
		s.teamErr(w, r, team.ErrOff)
		return
	}
	f := store.AuditFilter{Limit: limitParam(r), Action: r.URL.Query().Get("action")}
	if v := r.URL.Query().Get("actor"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "actor must be a user id")
			return
		}
		f.Actor = &id
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		f.Before, _ = strconv.ParseInt(v, 10, 64)
	}
	items, err := s.Store.ListAudit(r.Context(), store.AuditFilter{Actor: f.Actor, Action: f.Action, Before: f.Before, Limit: f.Limit + 1})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	next := ""
	if len(items) > f.Limit {
		items = items[:f.Limit]
		next = strconv.FormatInt(items[f.Limit-1].ID, 10)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// ---- nodes and skills in team mode ----

// ownNode: admins manage any computer, members their own.
func (s *Server) ownNode(w http.ResponseWriter, r *http.Request) bool {
	a := actorOf(r)
	if !a.Team || a.admin() {
		return true
	}
	n, err := s.Store.GetNode(r.Context(), r.PathValue("id"))
	if err != nil {
		return true
	}
	if n.OwnerUserID != nil && *n.OwnerUserID == a.UserID {
		return true
	}
	forbid(w, "only its owner or an admin can change this computer")
	return false
}

func (s *Server) shareNode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Shared bool `json:"shared"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if err := s.Store.SetNodeShared(r.Context(), id, in.Shared); err != nil {
		writeErr(w, r, err)
		return
	}
	s.Team.Audit(r.Context(), actorOf(r).uid(), "node.share", "node", id, map[string]any{"shared": in.Shared})
	n, err := s.Nodes.Get(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// promoteSkill makes a member's personal skill a team skill (admins).
func (s *Server) promoteSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.Store.SetSkillPersonal(r.Context(), id, false); err != nil {
		writeErr(w, r, err)
		return
	}
	s.Team.Audit(r.Context(), actorOf(r).uid(), "skill.promote", "skill", strconv.FormatInt(id, 10), nil)
	sk, err := s.Store.GetSkill(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sk)
}

// auditedRoutes are admin changes the audit log records (team mode), by
// route; the entry names the path's target and never a request body.
var auditedRoutes = map[string]string{
	"POST /api/v1/mcp-servers":                "mcp.create",
	"PUT /api/v1/mcp-servers/{id}":            "mcp.update",
	"DELETE /api/v1/mcp-servers/{id}":         "mcp.delete",
	"PUT /api/v1/projects/{id}/integrations":  "integrations.update",
	"POST /api/v1/projects/{id}/egress/allow": "egress.allow",
	"PUT /api/v1/settings/costs":              "budget.global",
	"PATCH /api/v1/projects/{id}":             "project.update",
	"POST /api/v1/tasks/{id}/budget-override": "budget.override",
}

// audited records action after a successful response.
func (s *Server) audited(action string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)
		if rec.status < 300 {
			s.Team.Audit(r.Context(), actorOf(r).uid(), action, "", r.PathValue("id"), map[string]any{"path": r.URL.Path})
		}
	}
}
