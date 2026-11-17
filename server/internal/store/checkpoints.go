package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// SettingCheckpoints is the settings row of CheckpointSettings (missing: off).
const SettingCheckpoints = "checkpoints"

// CheckpointSettings are the global checkpoint settings.
type CheckpointSettings struct {
	// Enabled makes runners take checkpoints (default false: today's behaviour).
	Enabled bool `json:"enabled"`
}

// CheckpointSettings returns the global checkpoint settings.
func (s *Store) CheckpointSettings(ctx context.Context) (CheckpointSettings, error) {
	var c CheckpointSettings
	var v string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, SettingCheckpoints).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal([]byte(v), &c)
}

// SetCheckpointSettings replaces the global checkpoint settings.
func (s *Store) SetCheckpointSettings(ctx context.Context, c CheckpointSettings) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, SettingCheckpoints, string(b))
	return err
}

// Checkpoint is one runner checkpoint: the commit RefSHA at
// refs/jm/checkpoints/<RunID>/<N> in the task workspace.
type Checkpoint struct {
	ID           int64     `json:"id"`
	TaskID       int64     `json:"task_id"`
	RunID        int64     `json:"run_id"`
	EventID      *int64    `json:"event_id"`
	N            int       `json:"n"`
	RefSHA       string    `json:"ref_sha"`
	Turn         int       `json:"turn"`
	SessionRef   string    `json:"-"`
	FilesChanged int       `json:"files_changed"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"created_at"`
}

const checkpointCols = `id, task_id, run_id, event_id, n, ref_sha, turn, session_ref, files_changed, reason, created_at`

func scanCheckpoint(row interface{ Scan(...any) error }) (*Checkpoint, error) {
	var c Checkpoint
	err := row.Scan(&c.ID, &c.TaskID, &c.RunID, &c.EventID, &c.N, &c.RefSHA, &c.Turn, &c.SessionRef, &c.FilesChanged, &c.Reason, &c.CreatedAt)
	return &c, err
}

// AddCheckpoint records a checkpoint; a replayed one (same run and n) is
// returned as stored.
func (s *Store) AddCheckpoint(ctx context.Context, c Checkpoint) (*Checkpoint, error) {
	if c.Reason == "" {
		c.Reason = "turn"
	}
	out, err := scanCheckpoint(s.Pool.QueryRow(ctx, `INSERT INTO checkpoints (task_id, run_id, event_id, n, ref_sha, turn, session_ref, files_changed, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (run_id, n) DO UPDATE SET event_id = coalesce(checkpoints.event_id, EXCLUDED.event_id)
		RETURNING `+checkpointCols, c.TaskID, c.RunID, c.EventID, c.N, c.RefSHA, c.Turn, c.SessionRef, c.FilesChanged, c.Reason))
	return out, err
}

// ListCheckpoints returns a task's checkpoints, oldest first.
func (s *Store) ListCheckpoints(ctx context.Context, taskID int64) ([]*Checkpoint, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+checkpointCols+` FROM checkpoints WHERE task_id = $1 ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Checkpoint{}
	for rows.Next() {
		c, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCheckpoint returns one checkpoint of a task.
func (s *Store) GetCheckpoint(ctx context.Context, taskID, id int64) (*Checkpoint, error) {
	c, err := scanCheckpoint(s.Pool.QueryRow(ctx, `SELECT `+checkpointCols+` FROM checkpoints WHERE task_id = $1 AND id = $2`, taskID, id))
	return c, notFound(err)
}

// HasRestore reports whether a task ever ran a restore: its branch may then
// be rewritten (force-with-lease) by the next push.
func (s *Store) HasRestore(ctx context.Context, taskID int64) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE task_id = $1 AND kind = 'restore')`, taskID).Scan(&ok)
	return ok, err
}

// RequeueRestore queues a `restore` run from checkpoint cp with input as its
// prompt, and records a checkpoint_restored event with payload, atomically.
func (s *Store) RequeueRestore(ctx context.Context, taskID int64, from []string, cp *Checkpoint, input string, payload map[string]any) (*Task, *Event, error) {
	var out *Task
	var ev *Event
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		var runID int64
		if out, runID, err = requeueTx(ctx, tx, taskID, from, "restore", input); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET restore_checkpoint_id = $2 WHERE id = $1`, runID, cp.ID); err != nil {
			return err
		}
		payload["restore_run_id"] = runID
		ev, err = insertBackendEvent(ctx, tx, taskID, &runID, "checkpoint_restored", payload)
		return err
	})
	return out, ev, err
}

// EventsThrough returns a task's events with id <= lastID, oldest first, of
// the given types (the timeline a restore summary is built from).
func (s *Store) EventsThrough(ctx context.Context, taskID, lastID int64, types []string) ([]*Event, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, task_id, run_id, seq, type, payload, created_at FROM events
		WHERE task_id = $1 AND id <= $2 AND type = ANY($3) ORDER BY id`, taskID, lastID, types)
	if err != nil {
		return nil, err
	}
	return collectEvents(rows)
}

// EventsAfter counts a task's events after id by type (what a restore discards).
func (s *Store) EventsAfter(ctx context.Context, taskID, afterID int64) (map[string]int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT type, count(*) FROM events WHERE task_id = $1 AND id > $2 GROUP BY type`, taskID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, rows.Err()
}
