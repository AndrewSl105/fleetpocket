package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Pipeline is a pipelines row with its stages and the cost of its tasks.
type Pipeline struct {
	ID             int64            `json:"id"`
	ProjectID      string           `json:"project_id"`
	RepositoryID   int64            `json:"repository_id"`
	Template       string           `json:"template"`
	Title          string           `json:"title"`
	Prompt         string           `json:"prompt"`
	Status         string           `json:"status"`
	StatusReason   *string          `json:"status_reason"`
	BudgetUSD      *float64         `json:"budget_usd"`
	MaxParallel    int              `json:"max_parallel"`
	BaseBranch     string           `json:"base_branch"`
	PipelineBranch *string          `json:"pipeline_branch"`
	Plan           json.RawMessage  `json:"plan"`
	PlanAttempts   int              `json:"plan_attempts"`
	CreatedBy      *int64           `json:"created_by"`
	CreatedAt      time.Time        `json:"created_at"`
	FinishedAt     *time.Time       `json:"finished_at"`
	CostUSD        float64          `json:"cost_usd"`
	Stages         []*PipelineStage `json:"stages"`
}

// PipelineStage is one stage; TaskID is set once its task exists.
type PipelineStage struct {
	ID           int64    `json:"id"`
	PipelineID   int64    `json:"pipeline_id"`
	Key          string   `json:"key"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	DependsOn    []string `json:"depends_on"`
	Provider     string   `json:"provider"`
	Model        *string  `json:"model"`
	Output       string   `json:"output"`
	Skills       []string `json:"skills"`
	Files        []string `json:"files"`
	Prompt       string   `json:"prompt"`
	DoneWhen     string   `json:"done_when"`
	Node         *string  `json:"node"`
	TaskID       *int64   `json:"task_id"`
	Status       string   `json:"status"`
	StatusReason *string  `json:"status_reason"`
	Position     int      `json:"position"`
}

const pipelineCols = `id, project_id, repository_id, template, title, prompt, status, status_reason, budget_usd::float8,
	max_parallel, base_branch, pipeline_branch, plan, plan_attempts, created_by, created_at, finished_at,
	coalesce((SELECT sum(cost_usd_total) FROM tasks WHERE tasks.pipeline_id = pipelines.id), 0)::float8`

func scanPipeline(row interface{ Scan(...any) error }) (*Pipeline, error) {
	var p Pipeline
	err := row.Scan(&p.ID, &p.ProjectID, &p.RepositoryID, &p.Template, &p.Title, &p.Prompt, &p.Status, &p.StatusReason,
		&p.BudgetUSD, &p.MaxParallel, &p.BaseBranch, &p.PipelineBranch, &p.Plan, &p.PlanAttempts, &p.CreatedBy,
		&p.CreatedAt, &p.FinishedAt, &p.CostUSD)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &p, err
}

const stageCols = `id, pipeline_id, key, kind, title, depends_on, provider, model, output, skills, files, prompt, done_when,
	node, task_id, status, status_reason, position`

func scanStage(row interface{ Scan(...any) error }) (*PipelineStage, error) {
	var s PipelineStage
	err := row.Scan(&s.ID, &s.PipelineID, &s.Key, &s.Kind, &s.Title, &s.DependsOn, &s.Provider, &s.Model, &s.Output,
		&s.Skills, &s.Files, &s.Prompt, &s.DoneWhen, &s.Node, &s.TaskID, &s.Status, &s.StatusReason, &s.Position)
	return &s, err
}

func insertStages(ctx context.Context, tx pgx.Tx, pipelineID int64, stages []PipelineStage) error {
	for _, st := range stages {
		if _, err := tx.Exec(ctx, `INSERT INTO pipeline_stages (pipeline_id, key, kind, title, depends_on, provider, model, output,
				skills, files, prompt, done_when, node, status, position)
			VALUES ($1, $2, $3, $4, coalesce($5::text[], '{}'), $6, $7, $8, coalesce($9::text[], '{}'), coalesce($10::text[], '{}'),
				$11, $12, $13, coalesce(nullif($14, ''), 'waiting'), $15)`,
			pipelineID, st.Key, st.Kind, st.Title, st.DependsOn, st.Provider, st.Model, st.Output, st.Skills, st.Files,
			st.Prompt, st.DoneWhen, st.Node, st.Status, st.Position); err != nil {
			return err
		}
	}
	return nil
}

// CreatePipeline inserts a running pipeline and its stages.
func (s *Store) CreatePipeline(ctx context.Context, p Pipeline, stages []PipelineStage) (*Pipeline, error) {
	var id int64
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO pipelines (project_id, repository_id, template, title, prompt, budget_usd,
				max_parallel, base_branch, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			p.ProjectID, p.RepositoryID, p.Template, p.Title, p.Prompt, p.BudgetUSD, p.MaxParallel, p.BaseBranch, p.CreatedBy).Scan(&id); err != nil {
			return err
		}
		return insertStages(ctx, tx, id, stages)
	})
	if isFKViolation(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetPipeline(ctx, id)
}

// GetPipeline returns a pipeline with its stages in position order.
func (s *Store) GetPipeline(ctx context.Context, id int64) (*Pipeline, error) {
	p, err := scanPipeline(s.Pool.QueryRow(ctx, `SELECT `+pipelineCols+` FROM pipelines WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+stageCols+` FROM pipeline_stages WHERE pipeline_id = $1 ORDER BY position, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	p.Stages = []*PipelineStage{}
	for rows.Next() {
		st, err := scanStage(rows)
		if err != nil {
			return nil, err
		}
		p.Stages = append(p.Stages, st)
	}
	return p, rows.Err()
}

// ListPipelines returns pipelines newest first (project "" = all), without stages.
func (s *Store) ListPipelines(ctx context.Context, project string, limit int) ([]*Pipeline, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+pipelineCols+` FROM pipelines WHERE ($1 = '' OR project_id = $1)
		ORDER BY id DESC LIMIT $2`, project, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Pipeline{}
	for rows.Next() {
		p, err := scanPipeline(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// OpenPipelineIDs are the pipelines the engine still drives.
func (s *Store) OpenPipelineIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM pipelines WHERE status IN ('running', 'needs_you', 'awaiting_approval') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// SetPipelineStatus sets the status (finished_at for terminal ones).
func (s *Store) SetPipelineStatus(ctx context.Context, id int64, status string, reason *string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE pipelines SET status = $2, status_reason = $3,
		finished_at = CASE WHEN $2 IN ('completed', 'failed', 'cancelled') THEN coalesce(finished_at, now()) ELSE NULL END
		WHERE id = $1`, id, status, reason)
	return err
}

func (s *Store) SetPipelineBranch(ctx context.Context, id int64, branch string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE pipelines SET pipeline_branch = $2 WHERE id = $1`, id, branch)
	return err
}

// SetPipelinePlan stores the plan (nil clears it) and the design attempts.
func (s *Store) SetPipelinePlan(ctx context.Context, id int64, plan json.RawMessage, attempts int) error {
	_, err := s.Pool.Exec(ctx, `UPDATE pipelines SET plan = $2, plan_attempts = $3 WHERE id = $1`, id, plan, attempts)
	return err
}

func (s *Store) SetPipelineBudget(ctx context.Context, id int64, budget float64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE pipelines SET budget_usd = $2 WHERE id = $1`, id, budget)
	return err
}

// AddPipelineStages appends stages (fan-out after approval).
func (s *Store) AddPipelineStages(ctx context.Context, pipelineID int64, stages []PipelineStage) error {
	return s.Tx(ctx, func(tx pgx.Tx) error { return insertStages(ctx, tx, pipelineID, stages) })
}

// SetStageStatus updates a stage; taskID nil keeps the current task.
func (s *Store) SetStageStatus(ctx context.Context, stageID int64, status string, reason *string, taskID *int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE pipeline_stages SET status = $2, status_reason = $3, task_id = coalesce($4, task_id)
		WHERE id = $1`, stageID, status, reason, taskID)
	return err
}

// StageFiles returns a stage's owned globs.
func (s *Store) StageFiles(ctx context.Context, stageID int64) ([]string, error) {
	var files []string
	err := s.Pool.QueryRow(ctx, `SELECT files FROM pipeline_stages WHERE id = $1`, stageID).Scan(&files)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return files, err
}

// PipelineTaskIDs are the pipeline's tasks that have not ended.
func (s *Store) PipelineOpenTaskIDs(ctx context.Context, pipelineID int64) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM tasks WHERE pipeline_id = $1
		AND status NOT IN ('completed', 'failed', 'cancelled', 'pr_open', 'branch_ready')`, pipelineID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
