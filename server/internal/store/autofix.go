package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/andrewsl105/fleetpocket/server/internal/autofix"
)

// SettingCIAutoFix is the settings key of the global CI auto-fix default
// (JSON AutoFixOverrides; missing: off).
const SettingCIAutoFix = "ci_autofix"

// Auto-fix loop states (tasks.autofix_state; NULL: no loop).
const (
	AutoFixFixing    = "fixing"    // an attempt run is queued or running
	AutoFixHeld      = "held"      // the fix diff needs the owner's approval
	AutoFixApproved  = "approved"  // the owner said "push anyway"; pushing
	AutoFixWaiting   = "waiting"   // the fix is pushed; waiting for CI
	AutoFixGreen     = "green"     // CI passed after a fix
	AutoFixNeedsYou  = "needs_you" // stopped; the owner has to look
	AutoFixCancelled = "cancelled" // a human push, a manual follow-up or PR close
)

// AutoFixActive are the states of a loop that is still going.
var AutoFixActive = []string{AutoFixFixing, AutoFixHeld, AutoFixApproved, AutoFixWaiting}

// AutoFixOverrides are CI auto-fix settings that override the level below
// (project over global, global over the built-in defaults). Nil fields inherit.
type AutoFixOverrides struct {
	Enabled        *bool     `json:"enabled"`
	MaxAttempts    *int      `json:"max_attempts"`
	Checks         *[]string `json:"checks"`
	BudgetUSD      *float64  `json:"budget_usd"`
	AllowCIChanges *bool     `json:"allow_ci_changes"`
}

// IsZero reports whether nothing is overridden.
func (o AutoFixOverrides) IsZero() bool {
	return o.Enabled == nil && o.MaxAttempts == nil && o.Checks == nil && o.BudgetUSD == nil && o.AllowCIChanges == nil
}

// Apply returns s with o's set fields.
func (o AutoFixOverrides) Apply(s autofix.Settings) autofix.Settings {
	if o.Enabled != nil {
		s.Enabled = *o.Enabled
	}
	if o.MaxAttempts != nil {
		s.MaxAttempts = *o.MaxAttempts
	}
	if o.Checks != nil {
		s.Checks = nil
		for _, c := range *o.Checks {
			if c = strings.TrimSpace(c); c != "" {
				s.Checks = append(s.Checks, c)
			}
		}
	}
	if o.BudgetUSD != nil {
		s.BudgetUSD = *o.BudgetUSD
	}
	if o.AllowCIChanges != nil {
		s.AllowCIChanges = *o.AllowCIChanges
	}
	return s
}

// AutoFixDefaults is the global CI auto-fix default.
func (s *Store) AutoFixDefaults(ctx context.Context) (AutoFixOverrides, error) {
	var o AutoFixOverrides
	var v string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, SettingCIAutoFix).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	return o, json.Unmarshal([]byte(v), &o)
}

// SetAutoFixDefaults replaces the global CI auto-fix default.
func (s *Store) SetAutoFixDefaults(ctx context.Context, o AutoFixOverrides) error {
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, SettingCIAutoFix, string(b))
	return err
}

// AutoFixSettings are a project's effective CI auto-fix settings.
func (s *Store) AutoFixSettings(ctx context.Context, p *Project) (autofix.Settings, error) {
	g, err := s.AutoFixDefaults(ctx)
	if err != nil {
		return autofix.Settings{}, err
	}
	return p.CIAutoFix.Apply(g.Apply(autofix.Settings{})), nil
}

// LastCommittedSHA is the sha of the task's newest runner commit ("" if none).
func (s *Store) LastCommittedSHA(ctx context.Context, taskID int64) (string, error) {
	var sha string
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(payload->>'sha', '') FROM events
		WHERE task_id = $1 AND type = 'committed' ORDER BY id DESC LIMIT 1`, taskID).Scan(&sha)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return sha, err
}

// SetPushedSHA records the commit Jungle Monkey is pushing to the task branch.
func (s *Store) SetPushedSHA(ctx context.Context, taskID int64, sha string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE tasks SET last_pushed_sha = nullif($2, '') WHERE id = $1`, taskID, sha)
	return err
}

// ClaimAutoFixSHA marks a failing head commit handled; false when it already was.
func (s *Store) ClaimAutoFixSHA(ctx context.Context, taskID int64, sha string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE tasks SET autofix_handled_sha = $2
		WHERE id = $1 AND autofix_handled_sha IS DISTINCT FROM $2`, taskID, sha)
	return tag.RowsAffected() == 1, err
}

// SetAutoFixState moves the loop to state if it is in one of from (empty:
// any, "" matches no loop). ErrConflict otherwise.
func (s *Store) SetAutoFixState(ctx context.Context, taskID int64, from []string, state string) (*Task, error) {
	t, err := scanTask(s.Pool.QueryRow(ctx, `UPDATE tasks SET autofix_state = nullif($2, ''), updated_at = now()
		WHERE id = $1 AND (coalesce(cardinality($3::text[]), 0) = 0 OR coalesce(autofix_state, '') = ANY($3))
		RETURNING `+taskCols, taskID, state, from))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, gerr := s.GetTask(ctx, taskID); gerr != nil {
			return nil, gerr
		}
		return nil, ErrConflict
	}
	return t, err
}

// ResetAutoFix forgets the loop (a new push that auto-fix did not make).
func (s *Store) ResetAutoFix(ctx context.Context, taskID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE tasks SET autofix_state = NULL, autofix_attempts = NULL, autofix_max_attempts = NULL,
		autofix_signature = NULL WHERE id = $1`, taskID)
	return err
}

// AutoFixStart describes one auto-fix attempt.
type AutoFixStart struct {
	TaskID      int64
	Attempt     int
	MaxAttempts int
	Signature   string
	BudgetUSD   float64
	// Input is the run's prompt; Payload the `autofix` event (phase
	// "started"), which carries Upload (the trimmed logs) as attachments.
	Input   string
	Payload map[string]any
	Upload  *Upload
}

// StartAutoFix queues the follow-up run of an attempt on a pr_open task with
// its own budget, records the `autofix` event with the log attachment linked
// to that run, and moves the loop to fixing, in one transaction. ErrConflict
// when the task is no longer pr_open.
func (s *Store) StartAutoFix(ctx context.Context, a AutoFixStart) (*Task, *Event, error) {
	var out *Task
	var ev *Event
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		t, runID, err := requeueTx(ctx, tx, a.TaskID, []string{"pr_open"}, "followup", a.Input)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET autofix_attempt = $2, max_budget_usd = nullif($3::numeric, 0) WHERE id = $1`,
			runID, a.Attempt, a.BudgetUSD); err != nil {
			return err
		}
		if a.Payload == nil {
			a.Payload = map[string]any{}
		}
		a.Payload["run_id"] = runID
		if ev, err = insertEventWithUploadTx(ctx, tx, a.TaskID, &runID, "autofix", a.Payload, a.Upload, false); err != nil {
			return err
		}
		events := t.StatusEvents
		out, err = scanTask(tx.QueryRow(ctx, `UPDATE tasks SET autofix_state = 'fixing', autofix_attempts = $2,
			autofix_max_attempts = $3, autofix_signature = nullif($4, '') WHERE id = $1 RETURNING `+taskCols,
			a.TaskID, a.Attempt, a.MaxAttempts, a.Signature))
		if out != nil {
			out.StatusEvents = events
		}
		return err
	})
	return out, ev, err
}
