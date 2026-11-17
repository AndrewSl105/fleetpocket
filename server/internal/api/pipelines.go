package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/andrewsl105/fleetpocket/server/internal/pipelines"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

var errPipelinesOff = errors.New("pipelines are not available")

// GET /api/v1/pipeline-templates
func (s *Server) listPipelineTemplates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": pipelines.Templates(), "plan_schema": json.RawMessage(pipelines.PlanSchema)})
}

// GET /api/v1/pipelines?project=
func (s *Server) listPipelines(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListPipelines(r.Context(), r.URL.Query().Get("project"), 100)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// GET /api/v1/pipelines/{id}
func (s *Server) getPipeline(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := s.Store.GetPipeline(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type stageOverride struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Node     string `json:"node"`
}

type createPipelineIn struct {
	Template     string                   `json:"template"`
	ProjectID    string                   `json:"project_id"`
	RepositoryID int64                    `json:"repository_id"`
	Title        string                   `json:"title"`
	Prompt       string                   `json:"prompt"`
	BudgetUSD    *float64                 `json:"budget_usd"`
	MaxParallel  int                      `json:"max_parallel"`
	Stages       map[string]stageOverride `json:"stages"`
}

// POST /api/v1/pipelines
func (s *Server) createPipeline(w http.ResponseWriter, r *http.Request) {
	if s.Pipelines == nil {
		writeErr(w, r, errPipelinesOff)
		return
	}
	var in createPipelineIn
	if !decode(w, r, &in) {
		return
	}
	bad := func(msg string) { writeError(w, http.StatusBadRequest, "invalid", msg) }
	tpl, ok := pipelines.TemplateByKey(in.Template)
	if !ok {
		bad(fmt.Sprintf("unknown template %q", in.Template))
		return
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Prompt == "" {
		bad("prompt is required")
		return
	}
	if in.BudgetUSD != nil && *in.BudgetUSD <= 0 {
		bad("budget_usd must be positive")
		return
	}
	if in.MaxParallel == 0 {
		in.MaxParallel = pipelines.DefaultMaxParallel
	}
	if in.MaxParallel < 1 || in.MaxParallel > 10 {
		bad("max_parallel must be 1 to 10")
		return
	}
	p, err := s.Store.GetProject(r.Context(), in.ProjectID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var repo *store.Repository
	for i := range p.Repositories {
		rp := &p.Repositories[i]
		if (in.RepositoryID == 0 && rp.IsDefault) || rp.ID == in.RepositoryID {
			repo = rp
		}
	}
	if repo == nil {
		bad("the project has no such repository")
		return
	}
	enabled := s.Tasks.EnabledAgents()
	lead := tasks.ProjectAgent(p, s.Cfg.DefaultAgent)
	var stages []store.PipelineStage
	for i, d := range tpl.Stages {
		o := in.Stages[d.Key]
		provider := lead
		if o.Provider != "" {
			provider = o.Provider
		}
		if !slices.Contains(enabled, provider) {
			bad(fmt.Sprintf("stage %s: provider %q is not available", d.Key, provider))
			return
		}
		st := store.PipelineStage{Key: d.Key, Kind: d.Kind, Title: d.Title, DependsOn: d.DependsOn, Provider: provider,
			Output: d.Output, Skills: d.Skills, Status: pipelines.StageWaiting, Position: i}
		if o.Model != "" {
			st.Model = &o.Model
		}
		if o.Node != "" {
			st.Node = &o.Node
		}
		stages = append(stages, st)
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = tasks.TitleFromPrompt(in.Prompt)
	}
	out, err := s.Store.CreatePipeline(r.Context(), store.Pipeline{CreatedBy: actorOf(r).uid(), ProjectID: p.ID, RepositoryID: repo.ID, Template: tpl.Key,
		Title: title, Prompt: in.Prompt, BudgetUSD: in.BudgetUSD, MaxParallel: in.MaxParallel, BaseBranch: repo.BaseBranch}, stages)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.Pipelines.Reconcile(r.Context(), out.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	if out, err = s.Store.GetPipeline(r.Context(), out.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

type pipelineActionIn struct {
	StageID   int64           `json:"stage_id"`
	Note      string          `json:"note"`
	BudgetUSD float64         `json:"budget_usd"`
	Plan      json.RawMessage `json:"plan"`
}

// POST /api/v1/pipelines/{id}/{action}: approve, plan, redesign, retry,
// skip, cancel, budget.
func (s *Server) pipelineAction(w http.ResponseWriter, r *http.Request) {
	if s.Pipelines == nil {
		writeErr(w, r, errPipelinesOff)
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in pipelineActionIn
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	ctx, d := r.Context(), s.Pipelines
	var err error
	switch r.PathValue("action") {
	case "approve":
		err = d.Approve(ctx, id)
	case "plan":
		var errs []string
		if errs, err = d.EditPlan(ctx, id, in.Plan); err == nil && len(errs) > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid_plan",
				"message": strings.Join(errs, "; ")}, "errors": errs})
			return
		}
	case "redesign":
		err = d.Redesign(ctx, id, in.Note)
	case "retry":
		err = d.Retry(ctx, id, in.StageID, in.Note)
	case "skip":
		err = d.Skip(ctx, id, in.StageID)
	case "cancel":
		err = d.Cancel(ctx, id)
	case "budget":
		err = d.RaiseBudget(ctx, id, in.BudgetUSD)
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown action "+strconv.Quote(r.PathValue("action")))
		return
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	p, err := s.Store.GetPipeline(ctx, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
