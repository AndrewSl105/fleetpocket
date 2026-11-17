package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Team mode (docs/SPEC-server.md "Teams"). The `team` settings row exists
// only while team mode is on.
const SettingTeam = "team"

// Roles.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
	RoleViewer = "viewer"
)

func ValidRole(r string) bool { return r == RoleAdmin || r == RoleMember || r == RoleViewer }

// TeamSettings is the `team` row.
type TeamSettings struct {
	Enabled bool `json:"enabled"`
	// SharedKeyFallback lets a task whose creator has no personal key use
	// the server's env key (default off).
	SharedKeyFallback bool `json:"shared_key_fallback"`
}

// TeamSettings returns the team row, or the zero value when team mode is off.
func (s *Store) TeamSettings(ctx context.Context) (TeamSettings, error) {
	var v string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, SettingTeam).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return TeamSettings{}, nil
	}
	if err != nil {
		return TeamSettings{}, err
	}
	var t TeamSettings
	if err := json.Unmarshal([]byte(v), &t); err != nil {
		return TeamSettings{}, err
	}
	return t, nil
}

func setTeam(ctx context.Context, db q, t TeamSettings) error {
	b, _ := json.Marshal(t)
	_, err := db.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, SettingTeam, string(b))
	return err
}

// SetTeamSettings replaces the team row (team mode must be on).
func (s *Store) SetTeamSettings(ctx context.Context, t TeamSettings) error {
	return setTeam(ctx, s.Pool, t)
}

// UpsertSetting writes one settings row.
func (s *Store) UpsertSetting(ctx context.Context, key, value string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}

type User struct {
	ID               int64      `json:"id"`
	Email            string     `json:"email"`
	Name             string     `json:"name"`
	Role             string     `json:"role"`
	PasswordHash     string     `json:"-"`
	TOTPSecret       []byte     `json:"-"`
	IsOwner          bool       `json:"is_owner"`
	GitHubLogin      *string    `json:"github_login"`
	CommitEmail      *string    `json:"commit_email"`
	TelegramUserID   *int64     `json:"telegram_user_id"`
	MonthlyBudgetUSD *float64   `json:"monthly_budget_usd"`
	TaskScope        *string    `json:"task_scope"`
	DisabledAt       *time.Time `json:"disabled_at"`
	CreatedAt        time.Time  `json:"created_at"`
	LastLoginAt      *time.Time `json:"last_login_at"`
}

// Active: not disabled and TOTP enrolled.
func (u *User) Active() bool { return u.DisabledAt == nil && len(u.TOTPSecret) > 0 }

// Initials are the first letters of the first two words of the name.
func Initials(name string) string {
	var out []rune
	for _, w := range strings.Fields(name) {
		r := []rune(w)
		out = append(out, []rune(strings.ToUpper(string(r[0])))...)
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// UserRef is what task, idea, schedule and node responses carry.
type UserRef struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Initials string `json:"initials"`
}

func (u *User) Ref() *UserRef {
	if u == nil {
		return nil
	}
	return &UserRef{ID: u.ID, Name: u.Name, Initials: Initials(u.Name)}
}

const userCols = `id, email, name, role, password_hash, totp_secret, is_owner, github_login, commit_email,
	telegram_user_id, monthly_budget_usd::float8, task_scope, disabled_at, created_at, last_login_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &u.TOTPSecret, &u.IsOwner, &u.GitHubLogin,
		&u.CommitEmail, &u.TelegramUserID, &u.MonthlyBudgetUSD, &u.TaskScope, &u.DisabledAt, &u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

func NormalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// NewUser is a row to insert; TOTPSecret is set by the caller once sealed
// (it needs the id, so CreateUser seals through the callback).
type NewUser struct {
	Email, Name, Role, PasswordHash string
	IsOwner                         bool
}

// CreateUser inserts a user; seal (may be nil) returns the sealed TOTP
// secret for the new id and runs inside the same transaction.
func (s *Store) CreateUser(ctx context.Context, n NewUser, seal func(id int64) ([]byte, error)) (*User, error) {
	var out *User
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		u, err := createUser(ctx, tx, n, seal)
		out = u
		return err
	})
	return out, err
}

func createUser(ctx context.Context, db q, n NewUser, seal func(id int64) ([]byte, error)) (*User, error) {
	var id int64
	err := db.QueryRow(ctx, `INSERT INTO users (email, name, role, password_hash, is_owner) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		NormalizeEmail(n.Email), strings.TrimSpace(n.Name), n.Role, n.PasswordHash, n.IsOwner).Scan(&id)
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a user with this email already exists", ErrConflict)
	}
	if err != nil {
		return nil, err
	}
	if seal != nil {
		sealed, err := seal(id)
		if err != nil {
			return nil, err
		}
		if _, err := db.Exec(ctx, `UPDATE users SET totp_secret = $2 WHERE id = $1`, id, sealed); err != nil {
			return nil, err
		}
	}
	return scanUser(db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (s *Store) GetUser(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = $1`, NormalizeEmail(email)))
}

func (s *Store) OwnerUser(ctx context.Context) (*User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE is_owner ORDER BY id LIMIT 1`))
}

func (s *Store) UserByTelegram(ctx context.Context, tgID int64) (*User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE telegram_user_id = $1`, tgID))
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserRefs returns the refs of the given ids (missing ids are skipped).
func (s *Store) UserRefs(ctx context.Context, ids []int64) (map[int64]*UserRef, error) {
	out := map[int64]*UserRef{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, name FROM users WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = &UserRef{ID: id, Name: name, Initials: Initials(name)}
	}
	return out, rows.Err()
}

// CountActive counts active users (enrolled, not disabled), and of them admins.
func (s *Store) CountActive(ctx context.Context) (users, admins int, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE role = 'admin') FROM users
		WHERE disabled_at IS NULL AND totp_secret IS NOT NULL`).Scan(&users, &admins)
	return
}

// UserFields are the changeable profile and admin fields; nil keeps a value.
type UserFields struct {
	Name             *string
	Email            *string
	Role             *string
	GitHubLogin      *string // "" clears
	CommitEmail      *string // "" clears
	PasswordHash     *string
	TOTPSecret       []byte
	MonthlyBudgetUSD *float64 // < 0 clears
	TaskScope        *string
	TelegramUserID   *int64 // 0 clears
	Touch            bool   // last_login_at = now()
}

func (s *Store) UpdateUser(ctx context.Context, id int64, f UserFields) (*User, error) {
	sets := []string{}
	args := []any{id}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if f.Name != nil {
		add("name", strings.TrimSpace(*f.Name))
	}
	if f.Email != nil {
		add("email", NormalizeEmail(*f.Email))
	}
	if f.Role != nil {
		add("role", *f.Role)
	}
	if f.GitHubLogin != nil {
		add("github_login", nilIfEmpty(strings.TrimPrefix(strings.TrimSpace(*f.GitHubLogin), "@")))
	}
	if f.CommitEmail != nil {
		add("commit_email", nilIfEmpty(strings.TrimSpace(*f.CommitEmail)))
	}
	if f.PasswordHash != nil {
		add("password_hash", *f.PasswordHash)
	}
	if f.TOTPSecret != nil {
		add("totp_secret", f.TOTPSecret)
	}
	if f.MonthlyBudgetUSD != nil {
		if *f.MonthlyBudgetUSD < 0 {
			add("monthly_budget_usd", nil)
		} else {
			add("monthly_budget_usd", *f.MonthlyBudgetUSD)
		}
	}
	if f.TaskScope != nil {
		add("task_scope", *f.TaskScope)
	}
	if f.TelegramUserID != nil {
		if *f.TelegramUserID == 0 {
			add("telegram_user_id", nil)
		} else {
			add("telegram_user_id", *f.TelegramUserID)
		}
	}
	if f.Touch {
		sets = append(sets, "last_login_at = now()")
	}
	if len(sets) == 0 {
		return s.GetUser(ctx, id)
	}
	u, err := scanUser(s.Pool.QueryRow(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = $1 RETURNING `+userCols, args...))
	if err != nil && isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: email or Telegram account already used by another user", ErrConflict)
	}
	return u, err
}

// DisableUser marks the user disabled and applies the side effects that
// belong in one transaction: sessions revoked, owned computers' tokens
// invalidated (and unshared), personal keys and the Telegram link deleted,
// schedules paused. It returns the user's waiting tasks (queued, needs_key,
// paused: the caller cancels them through the task service, so the timeline
// and Telegram see it), their live tasks and their computers (to disconnect).
func (s *Store) DisableUser(ctx context.Context, id int64) (waiting, live []int64, nodes []string, err error) {
	err = s.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET disabled_at = now(), telegram_user_id = NULL WHERE id = $1 AND disabled_at IS NULL`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		for _, stmt := range []string{
			`DELETE FROM auth_sessions WHERE user_id = $1`,
			// A random hash no token can match: the computer must be paired again.
			`UPDATE nodes SET token_hash = gen_random_uuid()::text::bytea, shared = false WHERE owner_user_id = $1`,
			`DELETE FROM user_credentials WHERE user_id = $1`,
			`DELETE FROM telegram_link_codes WHERE user_id = $1`,
			`UPDATE schedules SET enabled = false WHERE created_by = $1`,
		} {
			if _, err := tx.Exec(ctx, stmt, id); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `SELECT id, status FROM tasks WHERE created_by = $1
			AND status IN ('queued', 'needs_key', 'paused', 'preparing', 'running', 'waiting_input', 'pushing') ORDER BY id`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var tid int64
			var st string
			if err := rows.Scan(&tid, &st); err != nil {
				rows.Close()
				return err
			}
			if st == "queued" || st == "needs_key" || st == "paused" {
				waiting = append(waiting, tid)
			} else {
				live = append(live, tid)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		nrows, err := tx.Query(ctx, `SELECT id FROM nodes WHERE owner_user_id = $1 ORDER BY id`, id)
		if err != nil {
			return err
		}
		defer nrows.Close()
		for nrows.Next() {
			var nid string
			if err := nrows.Scan(&nid); err != nil {
				return err
			}
			nodes = append(nodes, nid)
		}
		return nrows.Err()
	})
	return waiting, live, nodes, err
}

// EnableUser clears disabled_at.
func (s *Store) EnableUser(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE users SET disabled_at = NULL WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteUser removes a user; history keeps a NULL author.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// EnableTeam creates user #1 from the owner, attaches the existing
// sessions and authorship to them and writes the team row, all at once.
func (s *Store) EnableTeam(ctx context.Context, n NewUser, seal func(id int64) ([]byte, error)) (*User, error) {
	var out *User
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM settings WHERE key = $1)`, SettingTeam).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("%w: team mode is already on", ErrConflict)
		}
		// A previous team (turned off) may have left users behind.
		var owner *User
		u, err := scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = $1`, NormalizeEmail(n.Email)))
		switch {
		case err == nil:
			if _, err := tx.Exec(ctx, `UPDATE users SET is_owner = (id = $1)`, u.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET name = $2, role = 'admin', password_hash = $3, disabled_at = NULL WHERE id = $1`,
				u.ID, strings.TrimSpace(n.Name), n.PasswordHash); err != nil {
				return err
			}
			if seal != nil {
				sealed, err := seal(u.ID)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE users SET totp_secret = $2 WHERE id = $1`, u.ID, sealed); err != nil {
					return err
				}
			}
			owner = u
		case errors.Is(err, ErrNotFound):
			if _, err := tx.Exec(ctx, `UPDATE users SET is_owner = false`); err != nil {
				return err
			}
			n.Role, n.IsOwner = RoleAdmin, true
			owner, err = createUser(ctx, tx, n, seal)
			if err != nil {
				return err
			}
		default:
			return err
		}
		id := owner.ID
		for _, stmt := range []string{
			`UPDATE auth_sessions SET user_id = $1 WHERE user_id IS NULL`,
			`UPDATE tasks SET created_by = $1 WHERE created_by IS NULL`,
			`UPDATE runs SET user_id = $1 WHERE user_id IS NULL`,
			`UPDATE schedules SET created_by = $1 WHERE created_by IS NULL`,
			`UPDATE ideas SET created_by = $1 WHERE created_by IS NULL`,
			`UPDATE mcp_servers SET created_by = $1 WHERE created_by IS NULL`,
			`UPDATE skills SET owner_user_id = $1 WHERE owner_user_id IS NULL`,
			`UPDATE nodes SET owner_user_id = $1 WHERE owner_user_id IS NULL`,
		} {
			if _, err := tx.Exec(ctx, stmt, id); err != nil {
				return err
			}
		}
		if err := setTeam(ctx, tx, TeamSettings{Enabled: true}); err != nil {
			return err
		}
		out, err = scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
		return err
	})
	return out, err
}

// DisableTeam removes the team row. Users, keys and authorship stay so the
// team can be turned on again; single-owner mode ignores them.
func (s *Store) DisableTeam(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM settings WHERE key = $1`, SettingTeam)
	return err
}

// ---- sessions ----

// SetSessionUser attaches a session to a user.
func (s *Store) SetSessionUser(ctx context.Context, sessionID string, userID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE auth_sessions SET user_id = $2 WHERE id = $1`, sessionID, userID)
	return err
}

// SessionUser returns the user id of a session (nil: none).
func (s *Store) SessionUser(ctx context.Context, sessionID string) (*int64, error) {
	var id *int64
	err := s.Pool.QueryRow(ctx, `SELECT user_id FROM auth_sessions WHERE id = $1`, sessionID).Scan(&id)
	return id, notFound(err)
}

// DeleteUserSessions revokes every session of a user except keep.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64, keep string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM auth_sessions WHERE user_id = $1 AND id <> $2`, userID, keep)
	return err
}

// ---- invites ----

type Invite struct {
	ID        int64      `json:"id"`
	Role      string     `json:"role"`
	Email     *string    `json:"email"`
	CreatedBy *int64     `json:"created_by"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at"`
	UsedBy    *int64     `json:"used_by"`
	RevokedAt *time.Time `json:"revoked_at"`
	CreatedAt time.Time  `json:"created_at"`
}

const inviteCols = `id, role, email, created_by, expires_at, used_at, used_by, revoked_at, created_at`

func scanInvite(row interface{ Scan(...any) error }) (*Invite, error) {
	var i Invite
	err := row.Scan(&i.ID, &i.Role, &i.Email, &i.CreatedBy, &i.ExpiresAt, &i.UsedAt, &i.UsedBy, &i.RevokedAt, &i.CreatedAt)
	return &i, notFound(err)
}

func (s *Store) CreateInvite(ctx context.Context, tokenHash []byte, role string, email *string, by *int64, expires time.Time) (*Invite, error) {
	var e any
	if email != nil && *email != "" {
		e = NormalizeEmail(*email)
	}
	return scanInvite(s.Pool.QueryRow(ctx, `INSERT INTO invites (token_hash, role, email, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+inviteCols, tokenHash, role, e, by, expires))
}

func (s *Store) ListInvites(ctx context.Context) ([]*Invite, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+inviteCols+` FROM invites ORDER BY id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Invite{}
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// InviteByToken returns a usable invite (not used, revoked or expired).
func (s *Store) InviteByToken(ctx context.Context, tokenHash []byte) (*Invite, error) {
	return scanInvite(s.Pool.QueryRow(ctx, `SELECT `+inviteCols+` FROM invites
		WHERE token_hash = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > now()`, tokenHash))
}

func (s *Store) RevokeInvite(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE invites SET revoked_at = now() WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// RedeemInvite marks a usable invite used and creates the (not yet active)
// user in one transaction. A second redeem of the same token gets ErrNotFound.
func (s *Store) RedeemInvite(ctx context.Context, tokenHash []byte, n NewUser) (*User, *Invite, error) {
	var u *User
	var inv *Invite
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		inv, err = scanInvite(tx.QueryRow(ctx, `SELECT `+inviteCols+` FROM invites
			WHERE token_hash = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > now() FOR UPDATE`, tokenHash))
		if err != nil {
			return err
		}
		if inv.Email != nil && *inv.Email != NormalizeEmail(n.Email) {
			return fmt.Errorf("%w: this invite is for another email address", ErrConflict)
		}
		n.Role = inv.Role
		u, err = createUser(ctx, tx, n, nil)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE invites SET used_at = now(), used_by = $2 WHERE id = $1`, inv.ID, u.ID)
		return err
	})
	return u, inv, err
}

// ---- audit ----

type AuditEntry struct {
	ID          int64          `json:"id"`
	At          time.Time      `json:"at"`
	ActorUserID *int64         `json:"actor_user_id"`
	Action      string         `json:"action"`
	TargetType  string         `json:"target_type"`
	TargetID    string         `json:"target_id"`
	Meta        map[string]any `json:"meta"`
}

func (s *Store) Audit(ctx context.Context, actor *int64, action, targetType, targetID string, meta map[string]any) error {
	if meta == nil {
		meta = map[string]any{}
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO audit_log (actor_user_id, action, target_type, target_id, meta) VALUES ($1, $2, $3, $4, $5)`,
		actor, action, targetType, targetID, b)
	return err
}

type AuditFilter struct {
	Actor  *int64
	Action string
	Before int64 // cursor: entries with id < Before
	Limit  int
}

func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]*AuditEntry, error) {
	where := []string{"true"}
	args := []any{}
	if f.Actor != nil {
		args = append(args, *f.Actor)
		where = append(where, fmt.Sprintf("actor_user_id = $%d", len(args)))
	}
	if f.Action != "" {
		args = append(args, f.Action)
		where = append(where, fmt.Sprintf("action = $%d", len(args)))
	}
	if f.Before > 0 {
		args = append(args, f.Before)
		where = append(where, fmt.Sprintf("id < $%d", len(args)))
	}
	args = append(args, f.Limit)
	rows, err := s.Pool.Query(ctx, `SELECT id, at, actor_user_id, action, target_type, target_id, meta FROM audit_log
		WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(` ORDER BY id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		var meta []byte
		if err := rows.Scan(&a.ID, &a.At, &a.ActorUserID, &a.Action, &a.TargetType, &a.TargetID, &meta); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(meta, &a.Meta)
		out = append(out, &a)
	}
	return out, rows.Err()
}

// ---- personal agent keys ----

type Credential struct {
	UserID     int64      `json:"-"`
	Provider   string     `json:"provider"`
	Kind       string     `json:"kind"`
	Sealed     []byte     `json:"-"`
	Hint       string     `json:"last4"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

const credCols = `user_id, provider, kind, value_encrypted, hint, label, created_at, last_used_at`

func scanCred(row interface{ Scan(...any) error }) (*Credential, error) {
	var c Credential
	err := row.Scan(&c.UserID, &c.Provider, &c.Kind, &c.Sealed, &c.Hint, &c.Label, &c.CreatedAt, &c.LastUsedAt)
	return &c, notFound(err)
}

func (s *Store) PutCredential(ctx context.Context, c Credential) (*Credential, error) {
	return scanCred(s.Pool.QueryRow(ctx, `INSERT INTO user_credentials (user_id, provider, kind, value_encrypted, hint, label)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, provider) DO UPDATE SET kind = EXCLUDED.kind, value_encrypted = EXCLUDED.value_encrypted,
			hint = EXCLUDED.hint, label = EXCLUDED.label, created_at = now(), last_used_at = NULL
		RETURNING `+credCols, c.UserID, c.Provider, c.Kind, c.Sealed, c.Hint, c.Label))
}

func (s *Store) GetCredential(ctx context.Context, userID int64, provider string) (*Credential, error) {
	return scanCred(s.Pool.QueryRow(ctx, `SELECT `+credCols+` FROM user_credentials WHERE user_id = $1 AND provider = $2`, userID, provider))
}

func (s *Store) ListCredentials(ctx context.Context, userID int64) ([]*Credential, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+credCols+` FROM user_credentials WHERE user_id = $1 ORDER BY provider`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Credential{}
	for rows.Next() {
		c, err := scanCred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCredential(ctx context.Context, userID int64, provider string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM user_credentials WHERE user_id = $1 AND provider = $2`, userID, provider)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) TouchCredential(ctx context.Context, userID int64, provider string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE user_credentials SET last_used_at = now() WHERE user_id = $1 AND provider = $2`, userID, provider)
	return err
}

// ---- Telegram link codes ----

func (s *Store) CreateLinkCode(ctx context.Context, code string, userID int64, expires time.Time) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM telegram_link_codes WHERE user_id = $1 OR expires_at < now()`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO telegram_link_codes (code, user_id, expires_at) VALUES ($1, $2, $3)`, code, userID, expires)
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	})
}

// RedeemLinkCode links tgID to the code's user (single use) and returns them.
func (s *Store) RedeemLinkCode(ctx context.Context, code string, tgID int64) (*User, error) {
	var u *User
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var uid int64
		err := tx.QueryRow(ctx, `DELETE FROM telegram_link_codes WHERE code = $1 AND expires_at > now() RETURNING user_id`, code).Scan(&uid)
		if err != nil {
			return notFound(err)
		}
		// One Telegram account links to one user: move it.
		if _, err := tx.Exec(ctx, `UPDATE users SET telegram_user_id = NULL WHERE telegram_user_id = $1 AND id <> $2`, tgID, uid); err != nil {
			return err
		}
		u, err = scanUser(tx.QueryRow(ctx, `UPDATE users SET telegram_user_id = $2 WHERE id = $1 AND disabled_at IS NULL RETURNING `+userCols, uid, tgID))
		return err
	})
	return u, err
}

// authorCols are the tables with a created_by column (AuthorOf, SetAuthor).
var authorCols = map[string]string{"schedules": "created_by", "ideas": "created_by", "mcp_servers": "created_by", "skills": "owner_user_id"}

// AuthorOf returns who created a row (nil: nobody, e.g. single-owner mode).
func (s *Store) AuthorOf(ctx context.Context, table string, id int64) (*int64, error) {
	col, ok := authorCols[table]
	if !ok {
		return nil, fmt.Errorf("no author column on %s", table)
	}
	var by *int64
	err := s.Pool.QueryRow(ctx, `SELECT `+col+` FROM `+table+` WHERE id = $1`, id).Scan(&by)
	return by, notFound(err)
}

// SetAuthor records who created a row.
func (s *Store) SetAuthor(ctx context.Context, table string, id, userID int64) error {
	col, ok := authorCols[table]
	if !ok {
		return fmt.Errorf("no author column on %s", table)
	}
	_, err := s.Pool.Exec(ctx, `UPDATE `+table+` SET `+col+` = $2 WHERE id = $1`, id, userID)
	return err
}

// AuthorRefs returns the creator refs of rows of a table, by row id.
func (s *Store) AuthorRefs(ctx context.Context, table string, ids []int64) (map[int64]*UserRef, error) {
	col, ok := authorCols[table]
	out := map[int64]*UserRef{}
	if !ok {
		return nil, fmt.Errorf("no author column on %s", table)
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.id, u.id, u.name FROM `+table+` t JOIN users u ON u.id = t.`+col+` WHERE t.id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, uid int64
		var name string
		if err := rows.Scan(&id, &uid, &name); err != nil {
			return nil, err
		}
		out[id] = &UserRef{ID: uid, Name: name, Initials: Initials(name)}
	}
	return out, rows.Err()
}
