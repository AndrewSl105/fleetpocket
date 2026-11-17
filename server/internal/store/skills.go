package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// SettingSkillsSeeded marks that the built-in skills were written once;
// deleting a seeded skill therefore sticks.
const SettingSkillsSeeded = "skills_seeded"

type SkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Skill struct {
	ID               int64       `json:"id"`
	Slug             string      `json:"slug"`
	Name             string      `json:"name"`
	Description      string      `json:"description"`
	Body             string      `json:"body"`
	Files            []SkillFile `json:"files"`
	EnabledByDefault bool        `json:"enabled_by_default"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
	// Projects that enable the skill (ids).
	Projects []string `json:"projects"`
}

const skillCols = `id, slug, name, description, body, files, enabled_by_default, created_at, updated_at,
	coalesce((SELECT array_agg(project_id ORDER BY project_id) FROM project_skills WHERE skill_id = skills.id), '{}')`

func scanSkill(row interface{ Scan(...any) error }) (*Skill, error) {
	var sk Skill
	var files []byte
	if err := row.Scan(&sk.ID, &sk.Slug, &sk.Name, &sk.Description, &sk.Body, &files, &sk.EnabledByDefault,
		&sk.CreatedAt, &sk.UpdatedAt, &sk.Projects); err != nil {
		return nil, err
	}
	sk.Files = []SkillFile{}
	if err := json.Unmarshal(files, &sk.Files); err != nil {
		return nil, err
	}
	return &sk, nil
}

func (s *Store) ListSkills(ctx context.Context) ([]*Skill, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+skillCols+` FROM skills ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Skill{}
	for rows.Next() {
		sk, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

func (s *Store) GetSkill(ctx context.Context, id int64) (*Skill, error) {
	sk, err := scanSkill(s.Pool.QueryRow(ctx, `SELECT `+skillCols+` FROM skills WHERE id = $1`, id))
	return sk, notFound(err)
}

// SkillsBySlug returns the named skills that exist, in the order given.
func (s *Store) SkillsBySlug(ctx context.Context, slugs []string) ([]*Skill, error) {
	if len(slugs) == 0 {
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+skillCols+` FROM skills WHERE slug = ANY($1::text[])
		ORDER BY array_position($1::text[], slug)`, slugs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Skill
	for rows.Next() {
		sk, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// CreateSkill inserts a skill; ErrConflict when the slug is taken.
func (s *Store) CreateSkill(ctx context.Context, sk Skill) (*Skill, error) {
	files, _ := json.Marshal(nonNilFiles(sk.Files))
	out, err := scanSkill(s.Pool.QueryRow(ctx, `INSERT INTO skills (slug, name, description, body, files, enabled_by_default)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+skillCols,
		sk.Slug, sk.Name, sk.Description, sk.Body, files, sk.EnabledByDefault))
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	return out, err
}

// UpdateSkill replaces a skill's editable fields.
func (s *Store) UpdateSkill(ctx context.Context, id int64, sk Skill) (*Skill, error) {
	files, _ := json.Marshal(nonNilFiles(sk.Files))
	out, err := scanSkill(s.Pool.QueryRow(ctx, `UPDATE skills SET slug = $2, name = $3, description = $4, body = $5,
		files = $6, enabled_by_default = $7, updated_at = now() WHERE id = $1 RETURNING `+skillCols,
		id, sk.Slug, sk.Name, sk.Description, sk.Body, files, sk.EnabledByDefault))
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	return out, notFound(err)
}

func (s *Store) DeleteSkill(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM skills WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SeedSkills inserts the given skills once per installation (first run only).
func (s *Store) SeedSkills(ctx context.Context, seed []Skill) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, 'true') ON CONFLICT DO NOTHING`, SettingSkillsSeeded)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		for _, sk := range seed {
			files, _ := json.Marshal(nonNilFiles(sk.Files))
			if _, err := tx.Exec(ctx, `INSERT INTO skills (slug, name, description, body, files)
				VALUES ($1, $2, $3, $4, $5) ON CONFLICT (slug) DO NOTHING`,
				sk.Slug, sk.Name, sk.Description, sk.Body, files); err != nil {
				return err
			}
		}
		return nil
	})
}

// DefaultSkillSlugs returns the skills a project's tasks get by default:
// the project's enabled skills plus those enabled by default everywhere.
func (s *Store) DefaultSkillSlugs(ctx context.Context, projectID string) ([]string, error) {
	var out []string
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(array_agg(slug ORDER BY slug), '{}') FROM skills
		WHERE enabled_by_default OR id IN (SELECT skill_id FROM project_skills WHERE project_id = $1)`, projectID).Scan(&out)
	return out, err
}

// SetTaskSkills records the resolved skills a run used.
func (s *Store) SetTaskSkills(ctx context.Context, taskID int64, slugs []string) error {
	if slugs == nil {
		slugs = []string{}
	}
	_, err := s.Pool.Exec(ctx, `UPDATE tasks SET skills = $2 WHERE id = $1`, taskID, slugs)
	return err
}

func nonNilFiles(f []SkillFile) []SkillFile {
	if f == nil {
		return []SkillFile{}
	}
	return f
}
