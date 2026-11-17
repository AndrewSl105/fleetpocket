package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andrewsl105/fleetpocket/server/internal/artifacts"
)

// TaskArtifact is one version of a collected artifact.
type TaskArtifact struct {
	ID         int64     `json:"id"`
	TaskID     int64     `json:"task_id"`
	RunID      *int64    `json:"run_id"`
	Path       string    `json:"path"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary"`
	Confidence string    `json:"confidence"`
	Sources    []string  `json:"sources"`
	Content    string    `json:"content"`
	SHA256     string    `json:"sha256"`
	Version    int       `json:"version"`
	CreatedAt  time.Time `json:"created_at"`
}

// SaveArtifacts stores a run's artifacts. A path gets a new version only
// when its content changed since the latest one. It returns how many rows
// were added.
func (s *Store) SaveArtifacts(ctx context.Context, taskID int64, runID *int64, list []artifacts.Artifact) (int, error) {
	added := 0
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		for _, a := range list {
			var latest string
			var version int
			err := tx.QueryRow(ctx, `SELECT sha256, version FROM task_artifacts WHERE task_id = $1 AND path = $2
				ORDER BY version DESC LIMIT 1`, taskID, a.Path).Scan(&latest, &version)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil && latest == a.SHA256 {
				continue
			}
			sources, _ := json.Marshal(a.Sources)
			if _, err := tx.Exec(ctx, `INSERT INTO task_artifacts (task_id, run_id, path, kind, title, summary, confidence, sources, content, sha256, version)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				taskID, runID, a.Path, a.Kind, a.Title, a.Summary, a.Confidence, sources, a.Content, a.SHA256, version+1); err != nil {
				return err
			}
			added++
		}
		return nil
	})
	return added, err
}

// Artifacts returns the latest version of each of the task's artifacts, by path.
func (s *Store) Artifacts(ctx context.Context, taskID int64) ([]*TaskArtifact, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT ON (path) id, task_id, run_id, path, kind, title, summary, confidence, sources,
		content, sha256, version, created_at FROM task_artifacts WHERE task_id = $1 ORDER BY path, version DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TaskArtifact{}
	for rows.Next() {
		var a TaskArtifact
		var sources []byte
		if err := rows.Scan(&a.ID, &a.TaskID, &a.RunID, &a.Path, &a.Kind, &a.Title, &a.Summary, &a.Confidence, &sources,
			&a.Content, &a.SHA256, &a.Version, &a.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(sources, &a.Sources)
		if a.Sources == nil {
			a.Sources = []string{}
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}
