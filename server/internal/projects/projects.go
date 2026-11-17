// Package projects is the project and repository registry.
package projects

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/andrewsl105/fleetpocket/server/internal/autofix"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

var ErrInvalid = errors.New("invalid input")

var (
	slugRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	repoRe      = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	envKeyRe    = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	memoryRe    = regexp.MustCompile(`^[0-9]+[kmg]?$`)
	branchRe    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/-]{0,199}$`)
	Services    = map[string]bool{"postgres": true, "redis": true}
	reservedEnv = map[string]bool{
		"CLAUDE_CODE_OAUTH_TOKEN": true, "ANTHROPIC_API_KEY": true, "OPENAI_API_KEY": true, "CODEX_API_KEY": true,
		"CODEX_HOME": true, "CURSOR_API_KEY": true, "DATABASE_URL": true, "REDIS_URL": true,
		"FLEETPOCKET_RUNNER_FAKE": true, "FLEETPOCKET_WORKSPACE": true, "HOME": true, "PATH": true,
	}
)

type Service struct{ Store *store.Store }

type CreateInput struct {
	ID string `json:"id"`
	store.ProjectFields
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*store.Project, error) {
	if !slugRe.MatchString(in.ID) {
		return nil, fmt.Errorf("%w: id must be a lowercase slug (a-z, 0-9, -; max 40)", ErrInvalid)
	}
	if err := validate(in.ProjectFields); err != nil {
		return nil, err
	}
	if err := s.checkNode(ctx, in.Node); err != nil {
		return nil, err
	}
	return s.Store.CreateProject(ctx, in.ID, in.ProjectFields)
}

func (s *Service) Update(ctx context.Context, id string, f store.ProjectFields) (*store.Project, error) {
	if err := validate(f); err != nil {
		return nil, err
	}
	if err := s.checkNode(ctx, f.Node); err != nil {
		return nil, err
	}
	return s.Store.UpdateProject(ctx, id, f)
}

// checkNode accepts nil, "", "server" and registered node ids.
func (s *Service) checkNode(ctx context.Context, node *string) error {
	if node == nil || *node == "" || *node == "server" {
		return nil
	}
	if _, err := s.Store.GetNode(ctx, *node); errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: no computer %q", ErrInvalid, *node)
	} else if err != nil {
		return err
	}
	return nil
}

func (s *Service) Archive(ctx context.Context, id string) (*store.Project, error) {
	t := true
	return s.Store.UpdateProject(ctx, id, store.ProjectFields{Archived: &t})
}

func validate(f store.ProjectFields) error {
	if f.Name != nil && strings.TrimSpace(*f.Name) == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalid)
	}
	if f.Agent != nil && *f.Agent != "" && !config.ValidAgent(*f.Agent) {
		return fmt.Errorf("%w: agent must be one of %s", ErrInvalid, strings.Join(config.Agents, ", "))
	}
	if f.Services != nil {
		for _, svc := range *f.Services {
			if !Services[svc] {
				return fmt.Errorf("%w: unsupported service %q (postgres, redis)", ErrInvalid, svc)
			}
		}
	}
	if f.Env != nil {
		for k := range *f.Env {
			if !envKeyRe.MatchString(k) {
				return fmt.Errorf("%w: env key %q must match [A-Z_][A-Z0-9_]*", ErrInvalid, k)
			}
			if reservedEnv[k] {
				return fmt.Errorf("%w: env key %q is reserved", ErrInvalid, k)
			}
		}
	}
	if f.RunnerMemory != nil && *f.RunnerMemory != "" && !memoryRe.MatchString(*f.RunnerMemory) {
		return fmt.Errorf("%w: runner_memory must look like 3g or 512m", ErrInvalid)
	}
	if f.MaxTurns != nil && *f.MaxTurns < 0 || f.MaxBudgetUSD != nil && *f.MaxBudgetUSD < 0 ||
		f.MaxDurationSec != nil && *f.MaxDurationSec < 0 || f.RunnerCPUs != nil && *f.RunnerCPUs < 0 {
		return fmt.Errorf("%w: limits must not be negative", ErrInvalid)
	}
	if f.CIAutoFix != nil {
		if err := ValidateAutoFix(*f.CIAutoFix); err != nil {
			return err
		}
	}
	if f.Budget != nil && f.Budget.MonthlyUSD != nil && (*f.Budget.MonthlyUSD < 0 || *f.Budget.MonthlyUSD > MaxMonthlyBudgetUSD) {
		return fmt.Errorf("%w: budget.monthly_usd must be 0 to %d", ErrInvalid, MaxMonthlyBudgetUSD)
	}
	return nil
}

// MaxMonthlyBudgetUSD bounds monthly caps (projects.budget, the global one).
const MaxMonthlyBudgetUSD = 1_000_000

// ValidateAutoFix checks CI auto-fix overrides (project or global default).
func ValidateAutoFix(o store.AutoFixOverrides) error {
	if o.MaxAttempts != nil && (*o.MaxAttempts < 1 || *o.MaxAttempts > autofix.MaxMaxAttempts) {
		return fmt.Errorf("%w: ci_autofix.max_attempts must be 1 to %d", ErrInvalid, autofix.MaxMaxAttempts)
	}
	if o.BudgetUSD != nil && *o.BudgetUSD < 0 {
		return fmt.Errorf("%w: ci_autofix.budget_usd must not be negative", ErrInvalid)
	}
	if o.Checks != nil && len(*o.Checks) > 50 {
		return fmt.Errorf("%w: ci_autofix.checks lists at most 50 check names", ErrInvalid)
	}
	return nil
}

type RepoInput struct {
	FullName       string `json:"full_name"`
	BaseBranch     string `json:"base_branch"`
	InstallationID int64  `json:"installation_id"`
	IsDefault      bool   `json:"is_default"`
}

func (s *Service) AddRepository(ctx context.Context, projectID string, in RepoInput) (*store.Repository, error) {
	if !repoRe.MatchString(in.FullName) {
		return nil, fmt.Errorf("%w: full_name must be owner/repo", ErrInvalid)
	}
	if in.InstallationID == 0 {
		return nil, fmt.Errorf("%w: installation_id is required (see GET /api/v1/github/repositories)", ErrInvalid)
	}
	if in.BaseBranch == "" {
		in.BaseBranch = "dev"
	}
	if err := validBranch(in.BaseBranch); err != nil {
		return nil, err
	}
	return s.Store.AddRepository(ctx, store.Repository{
		ProjectID: projectID, FullName: in.FullName, BaseBranch: in.BaseBranch,
		InstallationID: in.InstallationID, IsDefault: in.IsDefault,
	})
}

type RepoUpdateInput struct {
	BaseBranch *string `json:"base_branch"`
	IsDefault  *bool   `json:"is_default"`
}

func (s *Service) UpdateRepository(ctx context.Context, projectID string, id int64, in RepoUpdateInput) (*store.Repository, error) {
	if in.BaseBranch != nil {
		if err := validBranch(*in.BaseBranch); err != nil {
			return nil, err
		}
	}
	return s.Store.UpdateRepository(ctx, projectID, id, store.RepoUpdate{BaseBranch: in.BaseBranch, IsDefault: in.IsDefault})
}

// validBranch accepts plain git branch names. The name reaches git argv
// (clone --branch), so it must never start with "-".
func validBranch(b string) error {
	if !branchRe.MatchString(b) || strings.Contains(b, "..") || strings.Contains(b, "//") ||
		strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".") || strings.HasSuffix(b, ".lock") {
		return fmt.Errorf("%w: base_branch is not a valid branch name", ErrInvalid)
	}
	return nil
}
