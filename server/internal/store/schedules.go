package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Concurrency policies of a schedule.
const (
	ConcurrencySkip  = "skip"
	ConcurrencyQueue = "queue"
)

// Firing triggers and outcomes (schedule_firings).
const (
	TriggerCron   = "cron"
	TriggerManual = "manual"

	FiringStarted = "started"
	FiringQueued  = "queued"
	FiringSkipped = "skipped"
	FiringMissed  = "missed"
	FiringFailed  = "failed"
)

// ScheduleBusyStatuses are the task states in which a schedule's previous
// task counts as "still running" (it has not reached a PR or an end).
var ScheduleBusyStatuses = []string{"queued", "preparing", "running", "waiting_input", "paused", "pushing"}

// Schedule is a recurring task (docs/SPEC-server.md "Schedules").
type Schedule struct {
	ID               int64       `json:"id"`
	Name             string      `json:"name"`
	ProjectID        string      `json:"project_id"`
	RepositoryID     *int64      `json:"repository_id"`
	Prompt           string      `json:"prompt"`
	Agent            *string     `json:"agent"`
	Skills           []string    `json:"skills"`
	MCPServers       []string    `json:"mcp_servers"`
	Cron             string      `json:"cron"`
	Timezone         string      `json:"timezone"`
	RunBudgetUSD     *float64    `json:"run_budget_usd"`
	MonthlyBudgetUSD *float64    `json:"monthly_budget_usd"`
	Concurrency      string      `json:"concurrency"`
	PROnlyIfChanges  bool        `json:"pr_only_if_changes"`
	EndsAt           *time.Time  `json:"ends_at"`
	Loop             *LoopConfig `json:"loop"`
	Enabled          bool        `json:"enabled"`
	NextRunAt        *time.Time  `json:"next_run_at"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`

	// Read-only: month-to-date cost of its tasks' runs, whether a task of
	// it is still running, and its latest firing.
	MonthSpendUSD float64         `json:"month_spend_usd"`
	Busy          bool            `json:"busy"`
	LastFiring    *ScheduleFiring `json:"last_firing"`
}

// ScheduleFiring is one row of a schedule's history, with its task.
type ScheduleFiring struct {
	ID           int64     `json:"id"`
	ScheduleID   int64     `json:"schedule_id"`
	ScheduledFor time.Time `json:"scheduled_for"`
	Trigger      string    `json:"trigger"`
	Outcome      string    `json:"outcome"`
	Reason       *string   `json:"reason"`
	TaskID       *int64    `json:"task_id"`
	CreatedAt    time.Time `json:"created_at"`
	// The task's status, cost and PR (nil without a task).
	TaskStatus  *string  `json:"task_status"`
	TaskCostUSD *float64 `json:"task_cost_usd"`
	TaskPRURL   *string  `json:"task_pr_url"`
	LoopState   *string  `json:"loop_state"`
}

const scheduleCols = `s.id, s.name, s.project_id, s.repository_id, s.prompt, s.agent, s.skills, s.mcp_servers, s.cron, s.timezone,
	s.run_budget_usd::float8, s.monthly_budget_usd::float8, s.concurrency, s.pr_only_if_changes, s.ends_at, s.loop, s.enabled,
	s.next_run_at, s.created_at, s.updated_at,
	coalesce((SELECT sum(r.cost_usd) FROM runs r JOIN tasks t ON t.id = r.task_id WHERE t.schedule_id = s.id AND r.started_at >= $1), 0)::float8,
	EXISTS (SELECT 1 FROM tasks t WHERE t.schedule_id = s.id AND t.status = ANY($2))`

func scanSchedule(row interface{ Scan(...any) error }) (*Schedule, error) {
	var sc Schedule
	var loop []byte
	if err := row.Scan(&sc.ID, &sc.Name, &sc.ProjectID, &sc.RepositoryID, &sc.Prompt, &sc.Agent, &sc.Skills, &sc.MCPServers,
		&sc.Cron, &sc.Timezone, &sc.RunBudgetUSD, &sc.MonthlyBudgetUSD, &sc.Concurrency, &sc.PROnlyIfChanges, &sc.EndsAt, &loop,
		&sc.Enabled, &sc.NextRunAt, &sc.CreatedAt, &sc.UpdatedAt, &sc.MonthSpendUSD, &sc.Busy); err != nil {
		return nil, err
	}
	if sc.Skills == nil {
		sc.Skills = []string{}
	}
	if len(loop) > 0 {
		if err := json.Unmarshal(loop, &sc.Loop); err != nil {
			return nil, err
		}
	}
	return &sc, nil
}

// ListSchedules returns every schedule, by name, with its month-to-date
// spend (runs started since monthStart) and latest firing.
func (s *Store) ListSchedules(ctx context.Context, monthStart time.Time) ([]*Schedule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+scheduleCols+` FROM schedules s ORDER BY lower(s.name), s.id`, monthStart, ScheduleBusyStatuses)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*Schedule, error) { return scanSchedule(r) })
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []*Schedule{}
	}
	last, err := s.lastFirings(ctx)
	if err != nil {
		return nil, err
	}
	for _, sc := range out {
		sc.LastFiring = last[sc.ID]
	}
	return out, nil
}

func (s *Store) GetSchedule(ctx context.Context, id int64, monthStart time.Time) (*Schedule, error) {
	sc, err := scanSchedule(s.Pool.QueryRow(ctx, `SELECT `+scheduleCols+` FROM schedules s WHERE s.id = $3`, monthStart, ScheduleBusyStatuses, id))
	if err != nil {
		return nil, notFound(err)
	}
	fs, err := s.ScheduleFirings(ctx, id, 1)
	if err == nil && len(fs) > 0 {
		sc.LastFiring = fs[0]
	}
	return sc, err
}

// ScheduleFields are the writable fields of a schedule.
type ScheduleFields struct {
	Name             string
	ProjectID        string
	RepositoryID     *int64
	Prompt           string
	Agent            *string
	Skills           []string
	MCPServers       []string
	Cron             string
	Timezone         string
	RunBudgetUSD     *float64
	MonthlyBudgetUSD *float64
	Concurrency      string
	PROnlyIfChanges  bool
	EndsAt           *time.Time
	Loop             *LoopConfig
	Enabled          bool
	NextRunAt        *time.Time
}

func (f ScheduleFields) args() []any {
	skills := f.Skills
	if skills == nil {
		skills = []string{}
	}
	return []any{f.Name, f.ProjectID, f.RepositoryID, f.Prompt, f.Agent, skills, f.MCPServers, f.Cron, f.Timezone,
		f.RunBudgetUSD, f.MonthlyBudgetUSD, f.Concurrency, f.PROnlyIfChanges, f.EndsAt, loopJSON(f.Loop), f.Enabled, f.NextRunAt}
}

// CreateSchedule inserts a schedule. ErrNotFound when the project or
// repository does not exist.
func (s *Store) CreateSchedule(ctx context.Context, f ScheduleFields) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `INSERT INTO schedules (name, project_id, repository_id, prompt, agent, skills, mcp_servers, cron, timezone,
			run_budget_usd, monthly_budget_usd, concurrency, pr_only_if_changes, ends_at, loop, enabled, next_run_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) RETURNING id`, f.args()...).Scan(&id)
	if isFKViolation(err) {
		return 0, ErrNotFound
	}
	return id, err
}

// UpdateSchedule replaces a schedule's fields.
func (s *Store) UpdateSchedule(ctx context.Context, id int64, f ScheduleFields) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE schedules SET name = $1, project_id = $2, repository_id = $3, prompt = $4, agent = $5,
			skills = $6, mcp_servers = $7, cron = $8, timezone = $9, run_budget_usd = $10, monthly_budget_usd = $11,
			concurrency = $12, pr_only_if_changes = $13, ends_at = $14, loop = $15, enabled = $16, next_run_at = $17, updated_at = now()
		WHERE id = $18`, append(f.args(), id)...)
	if isFKViolation(err) {
		return ErrNotFound
	}
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetScheduleState enables or disables a schedule and sets its next firing.
func (s *Store) SetScheduleState(ctx context.Context, id int64, enabled bool, next *time.Time) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE schedules SET enabled = $2, next_run_at = $3, updated_at = now() WHERE id = $1`, id, enabled, next)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// AdvanceSchedule moves next_run_at from `from` to `next` (nil: no more
// firings). False when another caller advanced it first.
func (s *Store) AdvanceSchedule(ctx context.Context, id int64, from time.Time, next *time.Time) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE schedules SET next_run_at = $3 WHERE id = $1 AND enabled AND next_run_at = $2`, id, from, next)
	return err == nil && tag.RowsAffected() == 1, err
}

func (s *Store) DeleteSchedule(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM schedules WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DueSchedules are the enabled schedules whose next firing is at or before now.
func (s *Store) DueSchedules(ctx context.Context, now, monthStart time.Time) ([]*Schedule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+scheduleCols+` FROM schedules s WHERE s.enabled AND s.next_run_at <= $3 ORDER BY s.next_run_at, s.id`,
		monthStart, ScheduleBusyStatuses, now)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*Schedule, error) { return scanSchedule(r) })
	return out, err
}

// ScheduleBusy reports whether a task of the schedule is still running.
func (s *Store) ScheduleBusy(ctx context.Context, id int64) (bool, error) {
	var busy bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tasks WHERE schedule_id = $1 AND status = ANY($2))`, id, ScheduleBusyStatuses).Scan(&busy)
	return busy, err
}

const firingCols = `f.id, f.schedule_id, f.scheduled_for, f.trigger, f.outcome, f.reason, f.task_id, f.created_at,
	t.status, t.cost_usd_total::float8, t.pr_url, t.loop_state`

func scanFiring(row interface{ Scan(...any) error }) (*ScheduleFiring, error) {
	var f ScheduleFiring
	err := row.Scan(&f.ID, &f.ScheduleID, &f.ScheduledFor, &f.Trigger, &f.Outcome, &f.Reason, &f.TaskID, &f.CreatedAt,
		&f.TaskStatus, &f.TaskCostUSD, &f.TaskPRURL, &f.LoopState)
	return &f, err
}

// AddFiring records a firing.
func (s *Store) AddFiring(ctx context.Context, f ScheduleFiring) (*ScheduleFiring, error) {
	var id int64
	if err := s.Pool.QueryRow(ctx, `INSERT INTO schedule_firings (schedule_id, scheduled_for, trigger, outcome, reason, task_id)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, f.ScheduleID, f.ScheduledFor, f.Trigger, f.Outcome, f.Reason, f.TaskID).Scan(&id); err != nil {
		return nil, err
	}
	return s.getFiring(ctx, id)
}

func (s *Store) getFiring(ctx context.Context, id int64) (*ScheduleFiring, error) {
	f, err := scanFiring(s.Pool.QueryRow(ctx, `SELECT `+firingCols+` FROM schedule_firings f LEFT JOIN tasks t ON t.id = f.task_id WHERE f.id = $1`, id))
	return f, notFound(err)
}

// FiringStartedTask turns a queued firing into a started one.
func (s *Store) FiringStartedTask(ctx context.Context, firingID, taskID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE schedule_firings SET outcome = 'started', task_id = $2 WHERE id = $1`, firingID, taskID)
	return err
}

// FiringFailed marks a queued firing failed.
func (s *Store) FiringFailed(ctx context.Context, firingID int64, reason string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE schedule_firings SET outcome = 'failed', reason = $2 WHERE id = $1`, firingID, reason)
	return err
}

// QueuedFirings are firings waiting for their schedule's previous task, oldest first.
func (s *Store) QueuedFirings(ctx context.Context) ([]*ScheduleFiring, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+firingCols+` FROM schedule_firings f LEFT JOIN tasks t ON t.id = f.task_id
		WHERE f.outcome = 'queued' ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (*ScheduleFiring, error) { return scanFiring(r) })
}

// HasQueuedFiring reports whether a firing of the schedule already waits.
func (s *Store) HasQueuedFiring(ctx context.Context, scheduleID int64) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schedule_firings WHERE schedule_id = $1 AND outcome = 'queued')`, scheduleID).Scan(&ok)
	return ok, err
}

// ScheduleFirings is a schedule's history, newest first.
func (s *Store) ScheduleFirings(ctx context.Context, scheduleID int64, limit int) ([]*ScheduleFiring, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+firingCols+` FROM schedule_firings f LEFT JOIN tasks t ON t.id = f.task_id
		WHERE f.schedule_id = $1 ORDER BY f.id DESC LIMIT $2`, scheduleID, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*ScheduleFiring, error) { return scanFiring(r) })
	if out == nil {
		out = []*ScheduleFiring{}
	}
	return out, err
}

func (s *Store) lastFirings(ctx context.Context) (map[int64]*ScheduleFiring, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT ON (f.schedule_id) `+firingCols+` FROM schedule_firings f
		LEFT JOIN tasks t ON t.id = f.task_id ORDER BY f.schedule_id, f.id DESC`)
	if err != nil {
		return nil, err
	}
	fs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*ScheduleFiring, error) { return scanFiring(r) })
	out := map[int64]*ScheduleFiring{}
	for _, f := range fs {
		out[f.ScheduleID] = f
	}
	return out, err
}
