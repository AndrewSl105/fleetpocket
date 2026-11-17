package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Node is a computer that runs tasks next to the server.
type Node struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	MaxConcurrent int        `json:"max_concurrent"`
	Agents        []string   `json:"agents"`
	Platform      string     `json:"platform"`
	Version       string     `json:"version"`
	LastSeenAt    *time.Time `json:"last_seen_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	TokenHash     []byte     `json:"-"`
	// Teams: the node's owner and whether an admin shared it with the team.
	OwnerUserID *int64   `json:"-"`
	Shared      bool     `json:"shared"`
	CreatedBy   *UserRef `json:"created_by"`
}

// NodeView is a node as clients see it.
type NodeView struct {
	*Node
	Online      bool `json:"online"`
	ActiveTasks int  `json:"active_tasks"`
}

const nodeCols = `id, name, max_concurrent, agents, platform, version, last_seen_at, created_at, updated_at, token_hash,
	owner_user_id, shared, (SELECT u.name FROM users u WHERE u.id = nodes.owner_user_id)`

func scanNode(row interface{ Scan(...any) error }) (*Node, error) {
	var n Node
	var owner *string
	if err := row.Scan(&n.ID, &n.Name, &n.MaxConcurrent, &n.Agents, &n.Platform, &n.Version, &n.LastSeenAt,
		&n.CreatedAt, &n.UpdatedAt, &n.TokenHash, &n.OwnerUserID, &n.Shared, &owner); err != nil {
		return nil, err
	}
	if n.OwnerUserID != nil && owner != nil {
		n.CreatedBy = &UserRef{ID: *n.OwnerUserID, Name: *owner, Initials: Initials(*owner)}
	}
	if n.Agents == nil {
		n.Agents = []string{}
	}
	return &n, nil
}

func (s *Store) ListNodes(ctx context.Context) ([]*Node, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+nodeCols+` FROM nodes ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) GetNode(ctx context.Context, id string) (*Node, error) {
	n, err := scanNode(s.Pool.QueryRow(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id = $1`, id))
	return n, notFound(err)
}

func (s *Store) CreateNode(ctx context.Context, id, name string, tokenHash []byte) (*Node, error) {
	n, err := scanNode(s.Pool.QueryRow(ctx, `INSERT INTO nodes (id, name, token_hash) VALUES ($1, $2, $3) RETURNING `+nodeCols,
		id, name, tokenHash))
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	return n, err
}

// NodeFields are the owner-editable fields; nil leaves a field unchanged.
type NodeFields struct {
	Name          *string `json:"name"`
	MaxConcurrent *int    `json:"max_concurrent"`
}

func (s *Store) UpdateNode(ctx context.Context, id string, f NodeFields) (*Node, error) {
	sets := []string{"updated_at = now()"}
	args := []any{id}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if f.Name != nil {
		add("name", *f.Name)
	}
	if f.MaxConcurrent != nil {
		add("max_concurrent", *f.MaxConcurrent)
	}
	n, err := scanNode(s.Pool.QueryRow(ctx, `UPDATE nodes SET `+strings.Join(sets, ", ")+` WHERE id = $1 RETURNING `+nodeCols, args...))
	return n, notFound(err)
}

func (s *Store) SetNodeToken(ctx context.Context, id string, tokenHash []byte) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE nodes SET token_hash = $2, updated_at = now() WHERE id = $1`, id, tokenHash)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// NodeSeen records a node's hello (or its disconnect when agents is nil).
func (s *Store) NodeSeen(ctx context.Context, id string, agents []string, platform, version string) (*Node, error) {
	if agents == nil {
		n, err := scanNode(s.Pool.QueryRow(ctx, `UPDATE nodes SET last_seen_at = now() WHERE id = $1 RETURNING `+nodeCols, id))
		return n, notFound(err)
	}
	n, err := scanNode(s.Pool.QueryRow(ctx, `UPDATE nodes SET last_seen_at = now(), agents = $2, platform = $3, version = $4
		WHERE id = $1 RETURNING `+nodeCols, id, agents, platform, version))
	return n, notFound(err)
}

// SetNodeReport stores agents and platform a computer reported while pairing
// (before its first connection; last_seen_at stays empty).
func (s *Store) SetNodeReport(ctx context.Context, id string, agents []string, platform string) (*Node, error) {
	n, err := scanNode(s.Pool.QueryRow(ctx, `UPDATE nodes SET agents = $2, platform = $3 WHERE id = $1 RETURNING `+nodeCols, id, agents, platform))
	return n, notFound(err)
}

// DeleteNode removes a node unless it has live tasks (ErrConflict).
func (s *Store) DeleteNode(ctx context.Context, id string) error {
	var live int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE node = $1
		AND status IN ('queued', 'preparing', 'running', 'waiting_input', 'pushing', 'paused')`, id).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return fmt.Errorf("%w: %d live tasks run on this computer", ErrConflict, live)
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ActiveTasksByNode counts tasks in preparing, running, waiting_input or
// pushing per host ("" is the server).
func (s *Store) ActiveTasksByNode(ctx context.Context) (map[string]int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT coalesce(node, ''), count(*) FROM tasks
		WHERE status IN ('preparing', 'running', 'waiting_input', 'pushing') GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var host string
		var n int
		if err := rows.Scan(&host, &n); err != nil {
			return nil, err
		}
		out[host] = n
	}
	return out, rows.Err()
}

// SetNodeOwner sets a node's owner (teams: "Add computer" by a user).
func (s *Store) SetNodeOwner(ctx context.Context, id string, userID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE nodes SET owner_user_id = $2 WHERE id = $1`, id, userID)
	return err
}

// SetNodeShared marks a node shared with the team (admins).
func (s *Store) SetNodeShared(ctx context.Context, id string, shared bool) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE nodes SET shared = $2, updated_at = now() WHERE id = $1`, id, shared)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
