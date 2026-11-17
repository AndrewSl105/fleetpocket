// Package team is team mode for self-hosted installs (docs/SPEC-server.md
// "Teams"): users and roles, invites, personal agent keys, Telegram links
// and the audit log. Off by default; a nil *Service or one whose team row is
// missing is single-owner mode, unchanged.
package team

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

var (
	// ErrLocalMode: team endpoints in local mode (npx jungle-monkey).
	ErrLocalMode = errors.New("teams are not available in local mode")
	// ErrOff: a team endpoint while team mode is off.
	ErrOff     = errors.New("team mode is off")
	ErrInvalid = errors.New("invalid input")
	// ErrLastAdmin protects the last active admin.
	ErrLastAdmin = errors.New("the last active admin cannot be demoted, disabled or removed")
	// ErrNoSecretsKey: team mode stores TOTP secrets and keys with secretbox.
	ErrNoSecretsKey = errors.New("team mode needs SECRETS_KEY (rerun deploy/install.sh or set SECRETS_KEY)")
	ErrBadCode      = errors.New("invalid code")
	ErrInviteGone   = errors.New("this invite link is invalid, used, revoked or expired")
)

const (
	InviteTTL     = 7 * 24 * time.Hour
	LinkCodeTTL   = 10 * time.Minute
	enrollmentTTL = 15 * time.Minute
	minPassword   = 12
)

// Owner gives the single owner's credentials (auth.Manager).
type Owner interface {
	Owner() (hash, totpSecret string)
	SetOwner(hash, totpSecret string)
}

type Service struct {
	Store *store.Store
	Box   *secretbox.Box
	Cfg   *config.Config
	Owner Owner
	Now   func() time.Time

	mu      sync.Mutex
	cached  *store.TeamSettings
	pending map[string]enrollment
}

type enrollment struct {
	secret  string
	invite  []byte
	expires time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Local reports local mode, where team mode is never available.
func (s *Service) Local() bool { return s != nil && s.Cfg != nil && s.Cfg.Local() }

// Settings returns the team row (cached; this process writes every change).
func (s *Service) Settings(ctx context.Context) store.TeamSettings {
	if s == nil || s.Local() {
		return store.TeamSettings{}
	}
	s.mu.Lock()
	if s.cached != nil {
		c := *s.cached
		s.mu.Unlock()
		return c
	}
	s.mu.Unlock()
	t, err := s.Store.TeamSettings(ctx)
	if err != nil {
		return store.TeamSettings{}
	}
	s.mu.Lock()
	s.cached = &t
	s.mu.Unlock()
	return t
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}

// Enabled reports whether team mode is on. Nil-safe.
func (s *Service) Enabled(ctx context.Context) bool { return s.Settings(ctx).Enabled }

func (s *Service) check(ctx context.Context) error {
	if s.Local() {
		return ErrLocalMode
	}
	if !s.Enabled(ctx) {
		return ErrOff
	}
	return nil
}

func (s *Service) audit(ctx context.Context, actor *int64, action, tt, tid string, meta map[string]any) {
	_ = s.Store.Audit(ctx, actor, action, tt, tid, meta)
}

// Audit records an entry (exported for other parts: MCP, egress, budgets).
func (s *Service) Audit(ctx context.Context, actor *int64, action, targetType, targetID string, meta map[string]any) {
	if s == nil || !s.Enabled(ctx) {
		return
	}
	s.audit(ctx, actor, action, targetType, targetID, meta)
}

func totpAAD(id int64) string { return "user_totp:" + strconv.FormatInt(id, 10) }
func credAAD(id int64, provider string) string {
	return "user_credential:" + strconv.FormatInt(id, 10) + ":" + provider
}

func (s *Service) sealTOTP(secret string) func(id int64) ([]byte, error) {
	return func(id int64) ([]byte, error) { return s.Box.Seal([]byte(secret), totpAAD(id)) }
}

// TOTPSecret opens a user's TOTP secret.
func (s *Service) TOTPSecret(u *store.User) (string, error) {
	if len(u.TOTPSecret) == 0 {
		return "", ErrBadCode
	}
	b, err := s.Box.Open(u.TOTPSecret, totpAAD(u.ID))
	return string(b), err
}

func validEmail(e string) bool {
	a, err := mail.ParseAddress(e)
	return err == nil && a.Address == e && strings.Contains(e, "@")
}

// ---- team on / off ----

// Enable turns team mode on: the owner becomes user #1 (admin) with their
// existing password hash and TOTP secret; sessions and authorship move to them.
func (s *Service) Enable(ctx context.Context, email, name string) (*store.User, error) {
	if s.Local() {
		return nil, ErrLocalMode
	}
	if !s.Box.Enabled() {
		return nil, ErrNoSecretsKey
	}
	email = store.NormalizeEmail(email)
	if !validEmail(email) || strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: a valid email and a name are required", ErrInvalid)
	}
	hash, secret := s.Owner.Owner()
	if hash == "" || secret == "" {
		return nil, fmt.Errorf("%w: the owner login is not set up", ErrInvalid)
	}
	u, err := s.Store.EnableTeam(ctx, store.NewUser{Email: email, Name: name, Role: store.RoleAdmin, PasswordHash: hash, IsOwner: true}, s.sealTOTP(secret))
	s.invalidate()
	if err != nil {
		return nil, err
	}
	s.audit(ctx, &u.ID, "team.enable", "user", strconv.FormatInt(u.ID, 10), nil)
	// The owner's Telegram link (TELEGRAM_ALLOWED_USER_ID) becomes theirs.
	if s.Cfg != nil && s.Cfg.TelegramAllowedUserID != 0 {
		tg := s.Cfg.TelegramAllowedUserID
		if nu, err := s.Store.UpdateUser(ctx, u.ID, store.UserFields{TelegramUserID: &tg}); err == nil {
			u = nu
		}
	}
	return u, nil
}

// Disable turns team mode off; allowed only with one active user left, whose
// credentials become the single owner's.
func (s *Service) Disable(ctx context.Context, actor int64) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	users, _, err := s.Store.CountActive(ctx)
	if err != nil {
		return err
	}
	if users > 1 {
		return fmt.Errorf("%w: disable or remove the other users first (team mode turns off with one active user)", store.ErrConflict)
	}
	u, err := s.Store.GetUser(ctx, actor)
	if err != nil {
		return err
	}
	secret, err := s.TOTPSecret(u)
	if err != nil {
		return err
	}
	// Single-owner mode logs in with this user's credentials from now on.
	oh, os := s.Owner.Owner()
	if oh != u.PasswordHash || os != secret {
		if err := s.Store.UpsertSetting(ctx, store.SettingOwnerPasswordHash, u.PasswordHash); err != nil {
			return err
		}
		if err := s.Store.UpsertSetting(ctx, store.SettingOwnerTOTPSecret, secret); err != nil {
			return err
		}
		s.Owner.SetOwner(u.PasswordHash, secret)
	}
	s.audit(ctx, &actor, "team.disable", "", "", nil)
	err = s.Store.DisableTeam(ctx)
	s.invalidate()
	return err
}

// SetFallback toggles "Allow the server's shared keys as a fallback".
func (s *Service) SetFallback(ctx context.Context, actor int64, on bool) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	t := s.Settings(ctx)
	t.SharedKeyFallback = on
	err := s.Store.SetTeamSettings(ctx, t)
	s.invalidate()
	if err == nil {
		s.audit(ctx, &actor, "team.fallback", "", "", map[string]any{"enabled": on})
	}
	return err
}

// ---- login ----

// Login checks an email + password + TOTP code against the users table.
func (s *Service) Login(ctx context.Context, email, password, code string) (*store.User, error) {
	u, err := s.Store.UserByEmail(ctx, email)
	if err != nil {
		// Same work as a real check, so timing does not reveal the email.
		_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(password))
		return nil, auth.ErrBadCredentials
	}
	pwErr := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password))
	secret, sErr := s.TOTPSecret(u)
	codeOK := sErr == nil && auth.ValidTOTP(code, secret, s.now())
	if pwErr != nil || !codeOK || u.DisabledAt != nil {
		return nil, auth.ErrBadCredentials
	}
	_, _ = s.Store.UpdateUser(ctx, u.ID, store.UserFields{Touch: true})
	return u, nil
}

var (
	dummyOnce sync.Once
	dummy     []byte
)

func dummyHash() []byte {
	dummyOnce.Do(func() { dummy, _ = bcrypt.GenerateFromPassword([]byte("not a password"), 12) })
	return dummy
}

// ---- invites and signup ----

func hashToken(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateInvite returns the invite and its token (shown once, stored hashed).
func (s *Service) CreateInvite(ctx context.Context, actor int64, role string, email *string, ttl time.Duration) (*store.Invite, string, error) {
	if err := s.check(ctx); err != nil {
		return nil, "", err
	}
	if !store.ValidRole(role) {
		return nil, "", fmt.Errorf("%w: role must be admin, member or viewer", ErrInvalid)
	}
	if email != nil {
		e := store.NormalizeEmail(*email)
		if e == "" {
			email = nil
		} else if !validEmail(e) {
			return nil, "", fmt.Errorf("%w: invalid email", ErrInvalid)
		} else {
			email = &e
		}
	}
	if ttl <= 0 {
		ttl = InviteTTL
	}
	if ttl > 90*24*time.Hour {
		return nil, "", fmt.Errorf("%w: an invite expires within 90 days", ErrInvalid)
	}
	tok, err := randomToken()
	if err != nil {
		return nil, "", err
	}
	inv, err := s.Store.CreateInvite(ctx, hashToken(tok), role, email, &actor, s.now().Add(ttl))
	if err != nil {
		return nil, "", err
	}
	s.audit(ctx, &actor, "invite.create", "invite", strconv.FormatInt(inv.ID, 10), map[string]any{"role": role, "email": email})
	return inv, tok, nil
}

func (s *Service) RevokeInvite(ctx context.Context, actor, id int64) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := s.Store.RevokeInvite(ctx, id); err != nil {
		return err
	}
	s.audit(ctx, &actor, "invite.revoke", "invite", strconv.FormatInt(id, 10), nil)
	return nil
}

// SignupStart validates an invite and returns a TOTP secret to enrol plus
// an enrollment id for SignupFinish. Nothing is stored yet.
type Enrollment struct {
	ID         string  `json:"enrollment_id"`
	Role       string  `json:"role"`
	Email      *string `json:"email"`
	TOTPSecret string  `json:"totp_secret"`
	OTPAuthURI string  `json:"otpauth_uri"`
}

func (s *Service) SignupStart(ctx context.Context, token string) (*Enrollment, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	inv, err := s.Store.InviteByToken(ctx, hashToken(token))
	if err != nil {
		return nil, ErrInviteGone
	}
	account := "team"
	if inv.Email != nil {
		account = *inv.Email
	}
	secret, uri, err := auth.NewTOTP(account)
	if err != nil {
		return nil, err
	}
	id, err := randomToken()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.pending == nil {
		s.pending = map[string]enrollment{}
	}
	now := s.now()
	for k, e := range s.pending {
		if now.After(e.expires) {
			delete(s.pending, k)
		}
	}
	s.pending[id] = enrollment{secret: secret, invite: hashToken(token), expires: now.Add(enrollmentTTL)}
	s.mu.Unlock()
	return &Enrollment{ID: id, Role: inv.Role, Email: inv.Email, TOTPSecret: secret, OTPAuthURI: uri}, nil
}

type SignupInput struct {
	Token        string `json:"token"`
	EnrollmentID string `json:"enrollment_id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	TOTP         string `json:"totp"`
}

// SignupFinish creates the account once the TOTP code proves enrolment;
// the invite is used up in the same transaction.
func (s *Service) SignupFinish(ctx context.Context, in SignupInput) (*store.User, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	in.Email = store.NormalizeEmail(in.Email)
	if !validEmail(in.Email) || strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("%w: a valid email and a name are required", ErrInvalid)
	}
	if len([]rune(in.Password)) < minPassword {
		return nil, fmt.Errorf("%w: the password needs at least %d characters", ErrInvalid, minPassword)
	}
	s.mu.Lock()
	e, ok := s.pending[in.EnrollmentID]
	s.mu.Unlock()
	if !ok || s.now().After(e.expires) || string(e.invite) != string(hashToken(in.Token)) {
		return nil, fmt.Errorf("%w: the enrolment expired; open the invite link again", ErrInvalid)
	}
	if !auth.ValidTOTP(in.TOTP, e.secret, s.now()) {
		return nil, ErrBadCode
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	u, inv, err := s.Store.RedeemInvite(ctx, hashToken(in.Token), store.NewUser{Email: in.Email, Name: in.Name, PasswordHash: hash})
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrInviteGone
	}
	if err != nil {
		return nil, err
	}
	sealed, err := s.Box.Seal([]byte(e.secret), totpAAD(u.ID))
	if err != nil {
		return nil, err
	}
	if u, err = s.Store.UpdateUser(ctx, u.ID, store.UserFields{TOTPSecret: sealed, Touch: true}); err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.pending, in.EnrollmentID)
	s.mu.Unlock()
	s.audit(ctx, &u.ID, "user.signup", "user", strconv.FormatInt(u.ID, 10), map[string]any{"invite_id": inv.ID, "role": u.Role})
	return u, nil
}

// ---- users ----

// lastAdminGuard refuses a change that leaves no active admin.
func (s *Service) lastAdminGuard(ctx context.Context, u *store.User) error {
	if u.Role != store.RoleAdmin || !u.Active() {
		return nil
	}
	_, admins, err := s.Store.CountActive(ctx)
	if err != nil {
		return err
	}
	if admins <= 1 {
		return fmt.Errorf("%w: %v", store.ErrConflict, ErrLastAdmin)
	}
	return nil
}

func (s *Service) SetRole(ctx context.Context, actor, id int64, role string) (*store.User, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if !store.ValidRole(role) {
		return nil, fmt.Errorf("%w: role must be admin, member or viewer", ErrInvalid)
	}
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if u.Role == role {
		return u, nil
	}
	if err := s.lastAdminGuard(ctx, u); err != nil {
		return nil, err
	}
	nu, err := s.Store.UpdateUser(ctx, id, store.UserFields{Role: &role})
	if err == nil {
		s.audit(ctx, &actor, "user.role", "user", strconv.FormatInt(id, 10), map[string]any{"from": u.Role, "to": role})
	}
	return nu, err
}

// SetBudget sets a user's monthly budget (nil clears).
func (s *Service) SetBudget(ctx context.Context, actor, id int64, usd *float64) (*store.User, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	v := -1.0
	if usd != nil {
		if *usd < 0 {
			return nil, fmt.Errorf("%w: budget must be positive", ErrInvalid)
		}
		v = *usd
	}
	u, err := s.Store.UpdateUser(ctx, id, store.UserFields{MonthlyBudgetUSD: &v})
	if err == nil {
		s.audit(ctx, &actor, "budget.user", "user", strconv.FormatInt(id, 10), map[string]any{"monthly_usd": usd})
	}
	return u, err
}

// DisableResult lists what disabling a user left for the caller: the
// waiting tasks to cancel, the live ones (finish or cancel, as the admin
// chose) and the computers to disconnect.
type DisableResult struct {
	Cancelled []int64  `json:"cancelled_tasks"`
	Live      []int64  `json:"live_tasks"`
	Nodes     []string `json:"-"`
}

// DisableUser applies the side effects (store.DisableUser); the caller
// cancels or lets finish the live tasks per the admin's choice.
func (s *Service) DisableUser(ctx context.Context, actor, id int64, liveAction string) (*DisableResult, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.lastAdminGuard(ctx, u); err != nil {
		return nil, err
	}
	waiting, live, nodes, err := s.Store.DisableUser(ctx, id)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, &actor, "user.disable", "user", strconv.FormatInt(id, 10),
		map[string]any{"cancelled_tasks": waiting, "live_tasks": live, "live_action": liveAction})
	return &DisableResult{Cancelled: waiting, Live: live, Nodes: nodes}, nil
}

func (s *Service) EnableUser(ctx context.Context, actor, id int64) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := s.Store.EnableUser(ctx, id); err != nil {
		return err
	}
	s.audit(ctx, &actor, "user.enable", "user", strconv.FormatInt(id, 10), nil)
	return nil
}

// RemoveUser deletes a user who is already disabled (or never active).
func (s *Service) RemoveUser(ctx context.Context, actor, id int64) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if err := s.lastAdminGuard(ctx, u); err != nil {
		return err
	}
	if u.DisabledAt == nil && u.Active() {
		return fmt.Errorf("%w: disable the user before removing them", store.ErrConflict)
	}
	if err := s.Store.DeleteUser(ctx, id); err != nil {
		return err
	}
	s.audit(ctx, &actor, "user.remove", "user", strconv.FormatInt(id, 10), map[string]any{"email": u.Email})
	return nil
}

// ProfileInput is what a user changes about themselves.
type ProfileInput struct {
	Name            *string `json:"name"`
	Email           *string `json:"email"`
	GitHubLogin     *string `json:"github_login"`
	CommitEmail     *string `json:"commit_email"`
	TaskScope       *string `json:"task_scope"`
	CurrentPassword *string `json:"current_password"`
	NewPassword     *string `json:"new_password"`
}

func (s *Service) UpdateProfile(ctx context.Context, id int64, in ProfileInput, session string) (*store.User, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	f := store.UserFields{Name: in.Name, GitHubLogin: in.GitHubLogin, CommitEmail: in.CommitEmail}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if in.Email != nil {
		e := store.NormalizeEmail(*in.Email)
		if !validEmail(e) {
			return nil, fmt.Errorf("%w: invalid email", ErrInvalid)
		}
		f.Email = &e
	}
	if in.CommitEmail != nil && *in.CommitEmail != "" && !validEmail(strings.TrimSpace(*in.CommitEmail)) {
		return nil, fmt.Errorf("%w: invalid commit email", ErrInvalid)
	}
	if in.TaskScope != nil {
		if *in.TaskScope != "mine" && *in.TaskScope != "everyone" {
			return nil, fmt.Errorf("%w: task_scope must be mine or everyone", ErrInvalid)
		}
		f.TaskScope = in.TaskScope
	}
	if in.NewPassword != nil {
		if in.CurrentPassword == nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(*in.CurrentPassword)) != nil {
			return nil, fmt.Errorf("%w: the current password is wrong", ErrInvalid)
		}
		if len([]rune(*in.NewPassword)) < minPassword {
			return nil, fmt.Errorf("%w: the password needs at least %d characters", ErrInvalid, minPassword)
		}
		h, err := auth.HashPassword(*in.NewPassword)
		if err != nil {
			return nil, err
		}
		f.PasswordHash = &h
	}
	nu, err := s.Store.UpdateUser(ctx, id, f)
	if err == nil && in.NewPassword != nil {
		_ = s.Store.DeleteUserSessions(ctx, id, session)
		s.audit(ctx, &id, "user.password", "user", strconv.FormatInt(id, 10), nil)
	}
	return nu, err
}

// TOTPResetStart returns a new secret to enrol; TOTPResetFinish stores it
// once a code from it is confirmed (with the current password).
func (s *Service) TOTPResetStart(ctx context.Context, id int64) (*Enrollment, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	secret, uri, err := auth.NewTOTP(u.Email)
	if err != nil {
		return nil, err
	}
	eid, err := randomToken()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.pending == nil {
		s.pending = map[string]enrollment{}
	}
	s.pending[eid] = enrollment{secret: secret, invite: []byte("user:" + strconv.FormatInt(id, 10)), expires: s.now().Add(enrollmentTTL)}
	s.mu.Unlock()
	return &Enrollment{ID: eid, Role: u.Role, Email: &u.Email, TOTPSecret: secret, OTPAuthURI: uri}, nil
}

func (s *Service) TOTPResetFinish(ctx context.Context, id int64, enrollmentID, password, code string) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	e, ok := s.pending[enrollmentID]
	s.mu.Unlock()
	if !ok || s.now().After(e.expires) || string(e.invite) != "user:"+strconv.FormatInt(id, 10) {
		return fmt.Errorf("%w: the enrolment expired; start again", ErrInvalid)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return fmt.Errorf("%w: the current password is wrong", ErrInvalid)
	}
	if !auth.ValidTOTP(code, e.secret, s.now()) {
		return ErrBadCode
	}
	sealed, err := s.Box.Seal([]byte(e.secret), totpAAD(id))
	if err != nil {
		return err
	}
	if _, err := s.Store.UpdateUser(ctx, id, store.UserFields{TOTPSecret: sealed}); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.pending, enrollmentID)
	s.mu.Unlock()
	s.audit(ctx, &id, "user.totp_reset", "user", strconv.FormatInt(id, 10), nil)
	return nil
}

// ---- personal keys ----

// PutKey stores (or replaces) a personal key; the value is write-only.
func (s *Service) PutKey(ctx context.Context, userID int64, provider, kind, value, label string) (*store.Credential, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	value = strings.TrimSpace(value)
	if !validKind(provider, kind) {
		return nil, fmt.Errorf("%w: provider must be claude (oauth_token or api_key), codex or cursor (api_key)", ErrInvalid)
	}
	if value == "" || len(value) > 4096 || strings.ContainsAny(value, " \n\r\t") {
		return nil, fmt.Errorf("%w: the key looks wrong (empty, too long or with spaces)", ErrInvalid)
	}
	sealed, err := s.Box.Seal([]byte(value), credAAD(userID, provider))
	if err != nil {
		return nil, err
	}
	c, err := s.Store.PutCredential(ctx, store.Credential{UserID: userID, Provider: provider, Kind: kind, Sealed: sealed,
		Hint: secretbox.Hint(value), Label: strings.TrimSpace(label)})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, &userID, "key.add", "credential", provider, map[string]any{"kind": kind})
	return c, nil
}

func (s *Service) DeleteKey(ctx context.Context, userID int64, provider string) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := s.Store.DeleteCredential(ctx, userID, provider); err != nil {
		return err
	}
	s.audit(ctx, &userID, "key.remove", "credential", provider, nil)
	return nil
}

// personalKey opens a user's key for a provider (nil: none).
func (s *Service) personalKey(ctx context.Context, userID int64, provider string) (*PersonalKey, error) {
	c, err := s.Store.GetCredential(ctx, userID, provider)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := s.Box.Open(c.Sealed, credAAD(userID, provider))
	if err != nil {
		return nil, err
	}
	return &PersonalKey{Kind: c.Kind, Value: string(v)}, nil
}

// KeyFor tests a stored key (the "Test" button) via tester.
func (s *Service) KeyFor(ctx context.Context, userID int64, provider string) (*PersonalKey, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	k, err := s.personalKey(ctx, userID, provider)
	if err == nil && k == nil {
		err = store.ErrNotFound
	}
	return k, err
}

// AgentEnv resolves the credentials a run of task t gets (Resolve): nil
// for the host's default, a replacement map, or ErrNeedsKey. Nil-safe.
func (s *Service) AgentEnv(ctx context.Context, t *store.Task) (map[string]string, error) {
	if s == nil {
		return nil, nil
	}
	st := s.Settings(ctx)
	in := ResolveInput{TeamOn: st.Enabled, Fallback: st.SharedKeyFallback, OnNode: t.Node != nil, Agent: t.Agent}
	if in.TeamOn && !in.OnNode {
		if t.CreatedByID != nil {
			k, err := s.personalKey(ctx, *t.CreatedByID, t.Agent)
			if err != nil {
				return nil, err
			}
			in.Personal = k
		}
		if s.Cfg != nil {
			in.EnvKeys = s.Cfg.AgentEnv(t.Agent)
			in.Fake = s.Cfg.RunnerFake
		}
	}
	env, src, err := Resolve(in)
	if err == nil && src == SourcePersonal {
		_ = s.Store.TouchCredential(ctx, *t.CreatedByID, t.Agent)
	}
	return env, err
}

// ---- Telegram links ----

// LinkCode issues a one-time /link code for the user (10 minutes).
func (s *Service) LinkCode(ctx context.Context, userID int64) (string, time.Time, error) {
	if err := s.check(ctx); err != nil {
		return "", time.Time{}, err
	}
	exp := s.now().Add(LinkCodeTTL)
	for range 5 {
		n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
		if err != nil {
			return "", time.Time{}, err
		}
		code := fmt.Sprintf("%06d", n.Int64())
		err = s.Store.CreateLinkCode(ctx, code, userID, exp)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		return code, exp, err
	}
	return "", time.Time{}, errors.New("could not allocate a link code")
}

// Link redeems a code sent to the bot by Telegram user tgID.
func (s *Service) Link(ctx context.Context, code string, tgID int64) (*store.User, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	u, err := s.Store.RedeemLinkCode(ctx, strings.TrimSpace(code), tgID)
	if err != nil {
		return nil, ErrBadCode
	}
	s.audit(ctx, &u.ID, "telegram.link", "user", strconv.FormatInt(u.ID, 10), nil)
	return u, nil
}

func (s *Service) Unlink(ctx context.Context, userID int64) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	zero := int64(0)
	if _, err := s.Store.UpdateUser(ctx, userID, store.UserFields{TelegramUserID: &zero}); err != nil {
		return err
	}
	s.audit(ctx, &userID, "telegram.unlink", "user", strconv.FormatInt(userID, 10), nil)
	return nil
}

// TelegramUser returns the active user a Telegram account is linked to.
func (s *Service) TelegramUser(ctx context.Context, tgID int64) (*store.User, error) {
	u, err := s.Store.UserByTelegram(ctx, tgID)
	if err != nil {
		return nil, err
	}
	if !u.Active() {
		return nil, store.ErrNotFound
	}
	return u, nil
}

// ---- roles ----

// CanWrite: create tasks, schedules, ideas (admins and members).
func CanWrite(role string) bool { return role == store.RoleAdmin || role == store.RoleMember }

// CanActOn: act on a task created by owner (admins: any; members: own).
func CanActOn(role string, actor int64, owner *int64) bool {
	if role == store.RoleAdmin {
		return true
	}
	return role == store.RoleMember && owner != nil && *owner == actor
}

// NodeAllowed: in team mode a member's (or viewer's) task may run only on
// their own computers and on computers an admin shared; admins anywhere.
// Nil-safe; single-owner mode allows everything.
func (s *Service) NodeAllowed(ctx context.Context, n *store.Node, by *int64) error {
	if s == nil || !s.Enabled(ctx) || by == nil || n == nil {
		return nil
	}
	u, err := s.Store.GetUser(ctx, *by)
	if err != nil {
		return err
	}
	if u.Role == store.RoleAdmin || n.Shared || n.OwnerUserID != nil && *n.OwnerUserID == u.ID {
		return nil
	}
	return fmt.Errorf("%w: computer %s belongs to someone else and is not shared with the team", tasks.ErrForbidden, n.Name)
}
