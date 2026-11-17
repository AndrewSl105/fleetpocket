package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// Loop states (tasks.loop_state). Every state but LoopRunning is final and
// says why the loop ended.
const (
	LoopRunning       = "running"
	LoopPassed        = "passed"
	LoopMaxIterations = "max_iterations"
	LoopBudget        = "budget"
	LoopNoChanges     = "no_changes"
	LoopStopped       = "stopped"
)

// LoopConfig is a task's "loop until…" option (docs/SPEC-server.md "Loops").
type LoopConfig struct {
	CheckCommand  string `json:"check_command"`
	MaxIterations int    `json:"max_iterations"`
	// BudgetUSD caps the cost of all iterations; nil: only the per-run budget.
	BudgetUSD *float64 `json:"budget_usd"`
	// TimeoutSec of the check command; 0: the runner's default (600).
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

func loopJSON(l *LoopConfig) []byte {
	if l == nil {
		return nil
	}
	b, _ := json.Marshal(l)
	return b
}

// SetRunLoopIteration records the loop iteration a run belongs to.
func (s *Store) SetRunLoopIteration(ctx context.Context, runID int64, n int) error {
	_, err := s.Pool.Exec(ctx, `UPDATE runs SET loop_iteration = $2 WHERE id = $1`, runID, n)
	return err
}

// LoopNext is the next iteration of a running loop.
type LoopNext struct {
	TaskID    int64
	From      []string // states the task may be in (the iteration that just ended)
	Iteration int
	Idle      int      // iterations in a row without file changes
	Input     string   // the prompt: the check output
	BudgetUSD *float64 // the run's budget (what is left of the loop's); nil: the usual
	Payload   map[string]any
}

// StartLoopIteration queues the next iteration's run (kind "loop"), moves
// the task to queued and records a `loop` event (phase "iteration"), all
// in one transaction. ErrConflict when the task left `From` or its loop is
// no longer running.
func (s *Store) StartLoopIteration(ctx context.Context, n LoopNext) (*Task, *Event, error) {
	var out *Task
	var ev *Event
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var running bool
		if err := tx.QueryRow(ctx, `SELECT loop_state = 'running' FROM tasks WHERE id = $1 FOR UPDATE`, n.TaskID).Scan(&running); err != nil {
			return notFound(err)
		}
		if !running {
			return ErrConflict
		}
		t, runID, err := requeueTx(ctx, tx, n.TaskID, n.From, "loop", n.Input)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET loop_iteration = $2, max_budget_usd = $3 WHERE id = $1`, runID, n.Iteration, n.BudgetUSD); err != nil {
			return err
		}
		if n.Payload == nil {
			n.Payload = map[string]any{}
		}
		n.Payload["phase"], n.Payload["iteration"], n.Payload["run_id"] = "iteration", n.Iteration, runID
		if ev, err = insertBackendEvent(ctx, tx, n.TaskID, &runID, "loop", n.Payload); err != nil {
			return err
		}
		events := t.StatusEvents
		out, err = scanTask(tx.QueryRow(ctx, `UPDATE tasks SET loop_iteration = $2, loop_idle = $3 WHERE id = $1 RETURNING `+taskCols,
			n.TaskID, n.Iteration, n.Idle))
		if out != nil {
			out.StatusEvents = events
		}
		return err
	})
	return out, ev, err
}

// EndLoop sets the final loop state of a running loop with a `loop` event
// (phase "stopped"). ErrConflict when the loop is not running.
func (s *Store) EndLoop(ctx context.Context, taskID int64, state string, payload map[string]any) (*Task, *Event, error) {
	var out *Task
	var ev *Event
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanTask(tx.QueryRow(ctx, `UPDATE tasks SET loop_state = $2, updated_at = now()
			WHERE id = $1 AND loop_state = 'running' RETURNING `+taskCols, taskID, state))
		if err == pgx.ErrNoRows {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if payload == nil {
			payload = map[string]any{}
		}
		payload["phase"], payload["state"] = "stopped", state
		ev, err = insertBackendEvent(ctx, tx, taskID, nil, "loop", payload)
		return err
	})
	return out, ev, err
}

// RestartLoop starts a task's loop over (a retry): iteration 1, running.
func (s *Store) RestartLoop(ctx context.Context, taskID int64) (*Task, error) {
	t, err := scanTask(s.Pool.QueryRow(ctx, `UPDATE tasks SET loop_state = 'running', loop_iteration = 1, loop_idle = 0
		WHERE id = $1 AND loop IS NOT NULL RETURNING `+taskCols, taskID))
	return t, notFound(err)
}

// LoopSpent is what the task's current loop cost so far: its loop runs
// since the latest initial or retry run.
func (s *Store) LoopSpent(ctx context.Context, taskID int64) (float64, error) {
	var v float64
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(sum(cost_usd), 0)::float8 FROM runs
		WHERE task_id = $1 AND loop_iteration IS NOT NULL
		AND id >= coalesce((SELECT max(id) FROM runs WHERE task_id = $1 AND kind IN ('initial', 'retry')), 0)`, taskID).Scan(&v)
	return v, err
}

// RunCheck is the stored `check` event of a run, if any.
func (s *Store) RunCheck(ctx context.Context, runID int64) (*Event, error) {
	var e Event
	err := s.Pool.QueryRow(ctx, `SELECT id, task_id, run_id, seq, type, payload, created_at FROM events
		WHERE run_id = $1 AND type = 'check' ORDER BY id DESC LIMIT 1`, runID).
		Scan(&e.ID, &e.TaskID, &e.RunID, &e.Seq, &e.Type, &e.Payload, &e.CreatedAt)
	return &e, notFound(err)
}
