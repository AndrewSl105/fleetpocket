package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andrewsl105/fleetpocket/server/internal/costs"
)

// SettingCosts is the settings key of the global cost settings (JSON
// CostSettings; missing: no monthly cap, the default price table).
const SettingCosts = "costs"

// BudgetWaitReason is the status reason of a queued task held by a monthly
// budget (docs/API.md, "Budgets").
const BudgetWaitReason = "waiting: budget"

// CostSettings are the global cost settings. A nil PriceTable is the
// built-in costs.DefaultPrices.
type CostSettings struct {
	MonthlyBudgetUSD *float64          `json:"monthly_budget_usd"`
	PriceTable       *costs.PriceTable `json:"price_table"`
}

// Prices is the effective price table.
func (c CostSettings) Prices() costs.PriceTable {
	if c.PriceTable != nil {
		return *c.PriceTable
	}
	return costs.DefaultPrices
}

// GlobalCap is the global monthly cap (0: none).
func (c CostSettings) GlobalCap() float64 {
	if c.MonthlyBudgetUSD == nil {
		return 0
	}
	return *c.MonthlyBudgetUSD
}

// ProjectBudget is a project's monthly cap (projects.budget).
type ProjectBudget struct {
	MonthlyUSD *float64 `json:"monthly_usd"`
}

// Cap is the monthly cap (0: none).
func (b *ProjectBudget) Cap() float64 {
	if b == nil || b.MonthlyUSD == nil {
		return 0
	}
	return *b.MonthlyUSD
}

// CostSettings returns the global cost settings.
func (s *Store) CostSettings(ctx context.Context) (CostSettings, error) {
	var c CostSettings
	var v string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, SettingCosts).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal([]byte(v), &c)
}

// SetCostSettings replaces the global cost settings.
func (s *Store) SetCostSettings(ctx context.Context, c CostSettings) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, SettingCosts, string(b))
	return err
}

// ---- per-run usage ----

// SetRunAgent records the run's agent, model and auth (from `started`).
func (s *Store) SetRunAgent(ctx context.Context, runID int64, agent, model, auth string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE runs SET agent = nullif($2, ''), model = nullif($3, ''), auth = nullif($4, '') WHERE id = $1`,
		runID, agent, model, auth)
	return err
}

// RunUsage is what a run has used so far (its own share, not the session's).
type RunUsage struct {
	CostUSD float64
	Source  string // costs.Source*
	Tokens  costs.Tokens
	// Turns is nil when the event carries no turn count (`done`).
	Turns *int
	// TokensKnown is false when the event carries no tokens (`done`).
	TokensKnown bool
}

// SetRunUsage records a run's usage and recomputes the task's totals
// (cost, the auto-fix share, the estimated and API-equivalent flags,
// turns). Returns the updated task.
func (s *Store) SetRunUsage(ctx context.Context, runID int64, u RunUsage) (*Task, error) {
	var out *Task
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var taskID int64
		err := tx.QueryRow(ctx, `UPDATE runs SET cost_usd = $2, cost_source = $3,
				turns = coalesce($4, turns),
				input_tokens = CASE WHEN $5 THEN $6 ELSE input_tokens END,
				output_tokens = CASE WHEN $5 THEN $7 ELSE output_tokens END,
				cache_read_tokens = CASE WHEN $5 THEN $8 ELSE cache_read_tokens END,
				cache_write_tokens = CASE WHEN $5 THEN $9 ELSE cache_write_tokens END
			WHERE id = $1 RETURNING task_id`,
			runID, u.CostUSD, u.Source, u.Turns, u.TokensKnown, u.Tokens.Input, u.Tokens.Output, u.Tokens.CacheRead, u.Tokens.CacheWrite).Scan(&taskID)
		if err != nil {
			return notFound(err)
		}
		out, err = recomputeTaskCostTx(ctx, tx, taskID)
		return err
	})
	return out, err
}

func recomputeTaskCostTx(ctx context.Context, tx q, taskID int64) (*Task, error) {
	return scanTask(tx.QueryRow(ctx, `UPDATE tasks SET cost_usd_total = a.total, autofix_cost_usd = nullif(a.fix, 0),
			cost_estimated = nullif(a.est, false), cost_api_equivalent = nullif(a.sub, false), cost_unknown = nullif(a.unk, false),
			turns = a.nturns, updated_at = now()
		FROM (SELECT coalesce(sum(cost_usd), 0) AS total,
				coalesce(sum(cost_usd) FILTER (WHERE autofix_attempt IS NOT NULL), 0) AS fix,
				coalesce(bool_or(cost_source = 'estimated'), false) AS est,
				coalesce(bool_or(cost_source = 'unknown'), false) AS unk,
				coalesce(bool_or(cost_source = 'reported' AND auth = 'subscription'), false) AS sub,
				coalesce(sum(turns), 0) AS nturns
			FROM runs WHERE task_id = $1) a
		WHERE id = $1 RETURNING `+taskCols, taskID))
}

// SessionBaseline is what earlier runs of the same Claude session already
// reported: runs since the last fresh (initial/retry/restore) run. Claude reports
// cost and tokens cumulatively per session, so a resumed run's own usage
// is its reported total minus this baseline (docs/PROTOCOL.md).
func (s *Store) SessionBaseline(ctx context.Context, taskID, runID int64) (float64, costs.Tokens, error) {
	var c float64
	var t costs.Tokens
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(sum(cost_usd), 0)::float8, coalesce(sum(input_tokens), 0)::bigint,
			coalesce(sum(output_tokens), 0)::bigint, coalesce(sum(cache_read_tokens), 0)::bigint, coalesce(sum(cache_write_tokens), 0)::bigint
		FROM runs
		WHERE task_id = $1 AND id < $2 AND started_at IS NOT NULL
		AND id >= coalesce((SELECT max(id) FROM runs WHERE task_id = $1 AND id < $2 AND kind IN ('initial', 'retry', 'restore')), 0)`,
		taskID, runID).Scan(&c, &t.Input, &t.Output, &t.CacheRead, &t.CacheWrite)
	return c, t, err
}

// ---- budgets ----

// BudgetGate is what ClaimNext needs to hold tasks over a monthly cap.
type BudgetGate struct {
	MonthStart time.Time
	GlobalUSD  float64 // 0: no global cap
}

// budgetCTE defines spent (month-to-date cost per project),
// blocked_projects, blocked_schedules and global_blocked; m and g are the parameter numbers of
// the month start and the global cap.
func budgetCTE(m, g int) string {
	return fmt.Sprintf(`spent AS (
		SELECT t.project_id, sum(r.cost_usd) AS usd FROM runs r JOIN tasks t ON t.id = r.task_id
		WHERE r.started_at >= $%[1]d GROUP BY 1
	), blocked_projects AS (
		SELECT p.id FROM projects p JOIN spent s ON s.project_id = p.id
		WHERE (p.budget->>'monthly_usd')::numeric > 0 AND s.usd >= (p.budget->>'monthly_usd')::numeric
	), blocked_schedules AS (
		SELECT sc.id FROM schedules sc
		WHERE sc.monthly_budget_usd > 0 AND sc.monthly_budget_usd <= (SELECT coalesce(sum(r.cost_usd), 0) FROM runs r
			JOIN tasks t ON t.id = r.task_id WHERE t.schedule_id = sc.id AND r.started_at >= $%[1]d)
	), global_blocked AS (
		SELECT $%[2]d::numeric > 0 AND coalesce((SELECT sum(usd) FROM spent), 0) >= $%[2]d::numeric AS b
	)`, m, g)
}

// BudgetWaitChange is a task whose budget wait changed; Event is the
// `budget` event of a task that started waiting (nil when it stopped).
type BudgetWaitChange struct {
	Task  *Task
	Event *Event
}

// MarkBudgetWaits sets budget_wait (and the status reason "waiting:
// budget") on queued tasks a monthly cap holds, with a `budget` event
// (phase "waiting"), and clears it from tasks no longer held. Returns the
// tasks that changed.
func (s *Store) MarkBudgetWaits(ctx context.Context, gate BudgetGate, spentFor func(scope string, t *Task) (spent, limit float64)) ([]BudgetWaitChange, error) {
	var out []BudgetWaitChange
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH `+budgetCTE(1, 2)+`, want AS (
				SELECT t.id, CASE
					WHEN t.status <> 'queued' OR t.budget_override_at >= $1 THEN NULL
					WHEN t.project_id IN (SELECT id FROM blocked_projects) THEN 'project'
					WHEN t.schedule_id IN (SELECT id FROM blocked_schedules) THEN 'schedule'
					WHEN (SELECT b FROM global_blocked) THEN 'global'
				END AS scope
				FROM tasks t WHERE t.status = 'queued' OR t.budget_wait IS NOT NULL
			)
			UPDATE tasks SET budget_wait = w.scope,
				status_reason = CASE WHEN w.scope IS NOT NULL THEN $3
					WHEN tasks.status_reason = $3 THEN NULL ELSE tasks.status_reason END,
				last_activity = CASE WHEN w.scope IS NOT NULL THEN $3 ELSE tasks.last_activity END,
				last_activity_at = CASE WHEN w.scope IS NOT NULL THEN now() ELSE tasks.last_activity_at END
			FROM want w WHERE tasks.id = w.id AND tasks.budget_wait IS DISTINCT FROM w.scope
			RETURNING `+prefixed("tasks.", taskCols), gate.MonthStart, gate.GlobalUSD, BudgetWaitReason)
		if err != nil {
			return err
		}
		ts, err := collectTasks(rows)
		if err != nil {
			return err
		}
		for _, t := range ts {
			c := BudgetWaitChange{Task: t}
			if t.BudgetWait != nil {
				payload := map[string]any{"phase": "waiting", "scope": *t.BudgetWait}
				if spentFor != nil {
					spent, limit := spentFor(*t.BudgetWait, t)
					payload["spent_usd"], payload["limit_usd"] = round2(spent), limit
				}
				if *t.BudgetWait == costs.ScopeProject {
					payload["project_id"] = t.ProjectID
				}
				if *t.BudgetWait == costs.ScopeSchedule && t.ScheduleID != nil {
					payload["schedule_id"] = *t.ScheduleID
				}
				if c.Event, err = insertBackendEvent(ctx, tx, t.ID, nil, "budget", payload); err != nil {
					return err
				}
			}
			out = append(out, c)
		}
		return nil
	})
	return out, err
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// MonthSpend is the cost of runs started since monthStart: in total and
// per project.
func (s *Store) MonthSpend(ctx context.Context, monthStart time.Time) (float64, map[string]float64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT t.project_id, sum(r.cost_usd)::float8 FROM runs r JOIN tasks t ON t.id = r.task_id
		WHERE r.started_at >= $1 GROUP BY 1`, monthStart)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	total, by := 0.0, map[string]float64{}
	for rows.Next() {
		var p string
		var v float64
		if err := rows.Scan(&p, &v); err != nil {
			return 0, nil, err
		}
		by[p] = v
		total += v
	}
	return total, by, rows.Err()
}

// MonthUnknown tells which projects (and whether any) have runs started
// since monthStart whose cost is unknown (tokens but no price), so a
// month-to-date sum can say it is incomplete.
func (s *Store) MonthUnknown(ctx context.Context, monthStart time.Time) (bool, map[string]bool, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT t.project_id FROM runs r JOIN tasks t ON t.id = r.task_id
		WHERE r.started_at >= $1 AND r.cost_source = 'unknown'`, monthStart)
	if err != nil {
		return false, nil, err
	}
	defer rows.Close()
	by := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return false, nil, err
		}
		by[p] = true
	}
	return len(by) > 0, by, rows.Err()
}

// AddBudgetAlert records that a scope reached level this month; false when
// it already had (the notification was sent).
func (s *Store) AddBudgetAlert(ctx context.Context, kind, id string, month time.Time, level int) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `INSERT INTO budget_alerts (scope_kind, scope_id, month, level) VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`, kind, id, month.Format("2006-01-02"), level)
	return err == nil && tag.RowsAffected() == 1, err
}

// OverrideBudget lets a queued task start despite the monthly caps for the
// rest of the month, with a `budget` event (phase "override") naming who.
// ErrConflict when the task is not queued.
func (s *Store) OverrideBudget(ctx context.Context, taskID int64, via string) (*Task, *Event, error) {
	var out *Task
	var ev *Event
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var scope *string
		err := tx.QueryRow(ctx, `SELECT budget_wait FROM tasks WHERE id = $1 AND status = 'queued' FOR UPDATE`, taskID).Scan(&scope)
		if errors.Is(err, pgx.ErrNoRows) {
			if _, gerr := scanTask(tx.QueryRow(ctx, `SELECT `+taskCols+` FROM tasks WHERE id = $1`, taskID)); gerr != nil {
				return notFound(gerr)
			}
			return ErrConflict
		}
		if err != nil {
			return err
		}
		out, err = scanTask(tx.QueryRow(ctx, `UPDATE tasks SET budget_override_at = now(), budget_wait = NULL,
				status_reason = CASE WHEN status_reason = $2 THEN NULL ELSE status_reason END, updated_at = now()
			WHERE id = $1 RETURNING `+taskCols, taskID, BudgetWaitReason))
		if err != nil {
			return err
		}
		payload := map[string]any{"phase": "override", "via": via}
		if scope != nil {
			payload["scope"] = *scope
		}
		ev, err = insertBackendEvent(ctx, tx, taskID, nil, "budget", payload)
		return err
	})
	return out, ev, err
}

// RunCosts are the started runs in [from, to) for the cost report.
func (s *Store) RunCosts(ctx context.Context, from, to time.Time) ([]costs.RunCost, error) {
	rows, err := s.Pool.Query(ctx, `SELECT r.started_at, t.project_id, coalesce(r.agent, t.agent), r.cost_usd::float8,
			coalesce(r.cost_source, ''), coalesce(r.auth, ''), r.autofix_attempt IS NOT NULL,
			coalesce(r.input_tokens, 0), coalesce(r.output_tokens, 0)
		FROM runs r JOIN tasks t ON t.id = r.task_id
		WHERE r.started_at >= $1 AND r.started_at < $2 ORDER BY r.started_at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []costs.RunCost
	for rows.Next() {
		var r costs.RunCost
		if err := rows.Scan(&r.StartedAt, &r.ProjectID, &r.Agent, &r.CostUSD, &r.Source, &r.Auth, &r.AutoFix, &r.InputTokens, &r.OutputTokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
