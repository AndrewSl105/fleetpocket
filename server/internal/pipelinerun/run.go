// Package pipelinerun drives pipelines (docs/SPEC-server.md "Pipelines"):
// it applies pipelines.Advance to the stored stages, creates each stage's
// ordinary task, maps task outcomes back to stages, validates the design's
// plan and implements the owner's actions. Every pass re-reads the database,
// so it is idempotent and safe after a restart.
package pipelinerun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/attachments"
	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/pipelines"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

// Tasks is the part of tasks.Service the driver uses.
type Tasks interface {
	Create(ctx context.Context, in tasks.CreateInput) (*tasks.View, error)
	Message(ctx context.Context, taskID int64, text string, imgs []*attachments.Image) (*tasks.View, error)
	Cancel(ctx context.Context, taskID int64) (*tasks.View, error)
	EnabledAgents() []string
}

// Notify reports pipeline moments (Telegram); nil = none.
type Notify func(p *store.Pipeline, kind, text string)

type Driver struct {
	Store *store.Store
	Tasks Tasks
	// CheapModel returns a provider's cheap model ("" = the provider default).
	CheapModel func(provider string) string
	// FreeSlots is the server's number of free task slots.
	FreeSlots func() int
	Notify    Notify

	mu sync.Mutex // one pass at a time
}

var (
	ErrInvalid  = errors.New("invalid input")
	ErrConflict = errors.New("conflict")
)

// maxInline caps a dependency artifact copied into a stage's prompt.
const maxInline = 64 << 10

// Run reconciles every open pipeline every interval until ctx ends; Kick
// (on task state changes) runs a pass sooner.
func (d *Driver) Run(ctx context.Context, interval time.Duration, kick <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		d.ReconcileAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-kick:
		}
	}
}

func (d *Driver) ReconcileAll(ctx context.Context) {
	ids, err := d.Store.OpenPipelineIDs(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("pipelines", "err", err)
		}
		return
	}
	for _, id := range ids {
		if err := d.Reconcile(ctx, id); err != nil {
			slog.Error("pipeline reconcile", "pipeline_id", id, "err", err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// Reconcile runs one pass over a pipeline.
func (d *Driver) Reconcile(ctx context.Context, id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, err := d.Store.GetPipeline(ctx, id)
	if err != nil {
		return err
	}
	tpl, _ := pipelines.TemplateByKey(p.Template)
	for _, st := range p.Stages {
		if st.Status != pipelines.StageRunning || st.TaskID == nil {
			continue
		}
		if err := d.settle(ctx, p, tpl, st); err != nil {
			return err
		}
	}
	if p, err = d.Store.GetPipeline(ctx, id); err != nil {
		return err
	}
	budget := 0.0
	if p.BudgetUSD != nil {
		budget = *p.BudgetUSD
	}
	free := 1 << 20
	if d.FreeSlots != nil {
		free = d.FreeSlots()
	}
	dec := pipelines.Advance(p.Status, engineStages(p), p.MaxParallel, budget, p.CostUSD, free)
	byKey := map[string]*store.PipelineStage{}
	for _, st := range p.Stages {
		byKey[st.Key] = st
	}
	for _, k := range dec.Ready {
		if err := d.Store.SetStageStatus(ctx, byKey[k].ID, pipelines.StageReady, nil, nil); err != nil {
			return err
		}
	}
	for _, k := range dec.Start {
		if err := d.start(ctx, p, tpl, byKey[k]); err != nil {
			reason := err.Error()
			_ = d.Store.SetStageStatus(ctx, byKey[k].ID, pipelines.StageFailed, &reason, nil)
			return d.setStatus(ctx, p, pipelines.PipelineNeedsYou, fmt.Sprintf("stage %q could not start: %s", k, reason))
		}
	}
	if dec.Status != p.Status || (dec.Reason != "" && deref(p.StatusReason) != dec.Reason) {
		return d.setStatus(ctx, p, dec.Status, dec.Reason)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (d *Driver) setStatus(ctx context.Context, p *store.Pipeline, status, reason string) error {
	var r *string
	if reason != "" {
		r = &reason
	}
	if err := d.Store.SetPipelineStatus(ctx, p.ID, status, r); err != nil {
		return err
	}
	if d.Notify != nil && status != p.Status {
		switch status {
		case pipelines.PipelineNeedsYou:
			d.Notify(p, "needs_you", reason)
		case pipelines.PipelineCompleted:
			d.Notify(p, "done", "")
		case pipelines.PipelineAwaitingApproval:
			d.Notify(p, "plan_ready", "")
		}
	}
	return nil
}

func engineStages(p *store.Pipeline) []pipelines.Stage {
	out := make([]pipelines.Stage, 0, len(p.Stages))
	for _, st := range p.Stages {
		out = append(out, pipelines.Stage{Key: st.Key, Kind: st.Kind, Title: st.Title, DependsOn: st.DependsOn, Status: st.Status,
			Provider: st.Provider, Output: st.Output, Files: st.Files, Prompt: st.Prompt, DoneWhen: st.DoneWhen,
			TaskID: st.TaskID, Position: st.Position})
	}
	return out
}

// settle maps a running stage's task to done/failed; a design stage's plan
// is validated first (one automatic follow-up with the errors).
func (d *Driver) settle(ctx context.Context, p *store.Pipeline, tpl pipelines.Template, st *store.PipelineStage) error {
	t, err := d.Store.GetTask(ctx, *st.TaskID)
	if err != nil {
		return err
	}
	arts, err := d.Store.Artifacts(ctx, t.ID)
	if err != nil {
		return err
	}
	out := pipelines.StageOutcome(st.Output, t.Status, len(arts))
	switch out {
	case "":
		return nil
	case pipelines.StageFailed:
		reason := fmt.Sprintf("task #%d ended %s", t.ID, t.Status)
		if t.StatusReason != nil && *t.StatusReason != "" {
			reason += ": " + *t.StatusReason
		}
		if d.Notify != nil {
			d.Notify(p, "stage_failed", fmt.Sprintf("%s: %s", st.Title, reason))
		}
		return d.Store.SetStageStatus(ctx, st.ID, pipelines.StageFailed, &reason, nil)
	}
	if st.Kind == pipelines.KindDesign && tpl.FanOut {
		return d.checkPlan(ctx, p, st, t, arts)
	}
	return d.Store.SetStageStatus(ctx, st.ID, pipelines.StageDone, nil, nil)
}

func (d *Driver) checkPlan(ctx context.Context, p *store.Pipeline, st *store.PipelineStage, t *store.Task, arts []*store.TaskArtifact) error {
	var errs []string
	var raw json.RawMessage
	i := slices.IndexFunc(arts, func(a *store.TaskArtifact) bool { return a.Path == "plan.json" })
	if i < 0 {
		errs = []string{"plan.json was not written to .fleetpocket/out/"}
	} else if plan, err := pipelines.ParsePlan([]byte(arts[i].Content)); err != nil {
		errs = []string{err.Error()}
	} else {
		errs = pipelines.Validate(plan, d.Tasks.EnabledAgents())
		raw = json.RawMessage(arts[i].Content)
	}
	if len(errs) == 0 {
		if err := d.Store.SetPipelinePlan(ctx, p.ID, raw, p.PlanAttempts+1); err != nil {
			return err
		}
		if err := d.Store.SetStageStatus(ctx, st.ID, pipelines.StageDone, nil, nil); err != nil {
			return err
		}
		return d.setStatus(ctx, p, pipelines.PipelineAwaitingApproval, "")
	}
	msg := "The plan is invalid:\n- " + strings.Join(errs, "\n- ")
	if p.PlanAttempts == 0 {
		if err := d.Store.SetPipelinePlan(ctx, p.ID, nil, 1); err != nil {
			return err
		}
		_, err := d.Tasks.Message(ctx, t.ID, msg+"\nFix .fleetpocket/out/plan.json so it follows the contract.", nil)
		return err
	}
	return d.Store.SetStageStatus(ctx, st.ID, pipelines.StageFailed, &msg, nil)
}

// start creates a ready stage's task.
func (d *Driver) start(ctx context.Context, p *store.Pipeline, tpl pipelines.Template, st *store.PipelineStage) error {
	prompt, err := d.prompt(ctx, p, tpl, st)
	if err != nil {
		return err
	}
	model := deref(st.Model)
	if def := stageDef(tpl, st.Key); model == "" && def != nil && def.Tier == pipelines.TierCheap && d.CheapModel != nil {
		model = d.CheapModel(st.Provider)
	}
	if model == "" && st.Kind == pipelines.KindReview && d.CheapModel != nil {
		model = d.CheapModel(st.Provider)
	}
	output := st.Output
	push := ""
	if output == pipelines.OutputBranch {
		push = pipelines.PushBranch(pipelines.Stage{Key: st.Key, Kind: st.Kind, Output: st.Output}, p.ID)
	}
	if st.Kind == pipelines.KindReview || st.Kind == pipelines.KindDesign || st.Kind == pipelines.KindScout || st.Kind == pipelines.KindReport {
		if output == pipelines.OutputPR && st.Kind == pipelines.KindReview {
			output = "pr_and_artifacts" // review.md plus the single PR
		}
	}
	v, err := d.Tasks.Create(ctx, tasks.CreateInput{
		ProjectID: p.ProjectID, RepositoryID: p.RepositoryID,
		Title:  fmt.Sprintf("%s · %s", p.Title, st.Title),
		Prompt: prompt, Agent: st.Provider, Node: deref(st.Node), Output: output, SkillsAdd: st.Skills, Source: "pipeline",
		PipelineID: &p.ID, StageID: &st.ID, BaseRef: pipelines.BaseRef(st.Kind, p.ID, p.BaseBranch, tplFanOut(p)),
		PushBranch: push, Model: model,
	})
	if err != nil {
		return err
	}
	if push != "" && st.Kind == pipelines.KindIntegrate {
		_ = d.Store.SetPipelineBranch(ctx, p.ID, push)
	}
	return d.Store.SetStageStatus(ctx, st.ID, pipelines.StageRunning, nil, &v.ID)
}

func tplFanOut(p *store.Pipeline) bool {
	t, _ := pipelines.TemplateByKey(p.Template)
	return t.FanOut
}

func stageDef(tpl pipelines.Template, key string) *pipelines.StageDef {
	for i := range tpl.Stages {
		if tpl.Stages[i].Key == key {
			return &tpl.Stages[i]
		}
	}
	return nil
}

// prompt: instructions + request + dependency summaries, then the
// dependencies' artifacts inline as <stage-key>/<file>.
func (d *Driver) prompt(ctx context.Context, p *store.Pipeline, tpl pipelines.Template, st *store.PipelineStage) (string, error) {
	instr := ""
	if def := stageDef(tpl, st.Key); def != nil {
		instr = def.Instructions
	}
	var plan *pipelines.Plan
	if len(p.Plan) > 0 {
		plan, _ = pipelines.ParsePlan(p.Plan)
	}
	switch st.Kind {
	case pipelines.KindBuild:
		if tpl.FanOut {
			instr = pipelines.BuildInstructions(pipelines.Stage{Key: st.Key, Files: st.Files, DoneWhen: st.DoneWhen}) + "\n\n" + st.Prompt
		}
	case pipelines.KindIntegrate:
		order := []string{}
		if plan != nil {
			order = pipelines.TopoOrder(plan)
		}
		instr = pipelines.IntegrateInstructions(p.ID, order, "")
	case pipelines.KindReview:
		instr = pipelines.ReviewInstructions
		if plan != nil {
			instr += "\n\nInclude this in your final summary (it becomes the PR description):\n\n" + pipelines.PRBody(plan, "", nil)
		}
	}
	byKey := map[string]*store.PipelineStage{}
	for _, s := range p.Stages {
		byKey[s.Key] = s
	}
	var deps []pipelines.Dep
	var files strings.Builder
	seen := map[string]bool{}
	var collect func(keys []string) error
	collect = func(keys []string) error {
		for _, k := range keys {
			dep := byKey[k]
			if dep == nil || seen[k] {
				continue
			}
			seen[k] = true
			if dep.TaskID != nil {
				summary := ""
				if t, err := d.Store.GetTask(ctx, *dep.TaskID); err == nil && t.LastActivity != nil {
					summary = *t.LastActivity
				}
				deps = append(deps, pipelines.Dep{Key: dep.Key, Title: dep.Title, Summary: summary})
				arts, err := d.Store.Artifacts(ctx, *dep.TaskID)
				if err != nil {
					return err
				}
				for _, a := range arts {
					c := a.Content
					if len(c) > maxInline {
						c = c[:maxInline] + "\n…(truncated)"
					}
					fmt.Fprintf(&files, "\n## Attachment %s/%s\n\n````\n%s\n````\n", dep.Key, a.Path, c)
				}
			}
			// Builds also see what their dependencies saw (scout report, plan).
			if err := collect(dep.DependsOn); err != nil {
				return err
			}
		}
		return nil
	}
	if err := collect(st.DependsOn); err != nil {
		return "", err
	}
	return pipelines.StagePrompt(instr, p.Prompt, deps) + files.String(), nil
}

// ---- Owner actions ----

func (d *Driver) load(ctx context.Context, id int64) (*store.Pipeline, error) {
	return d.Store.GetPipeline(ctx, id)
}

// Approve fans the approved plan out into build, integrate and review stages.
func (d *Driver) Approve(ctx context.Context, id int64) error {
	p, err := d.load(ctx, id)
	if err != nil {
		return err
	}
	if p.Status != pipelines.PipelineAwaitingApproval || len(p.Plan) == 0 {
		return fmt.Errorf("%w: the pipeline is not waiting for approval", ErrConflict)
	}
	plan, err := pipelines.ParsePlan(p.Plan)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if errs := pipelines.Validate(plan, d.Tasks.EnabledAgents()); len(errs) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(errs, "; "))
	}
	lead := "claude"
	if len(p.Stages) > 0 {
		lead = p.Stages[len(p.Stages)-1].Provider
	}
	cheap := ""
	if d.CheapModel != nil {
		cheap = d.CheapModel(lead)
	}
	var rows []store.PipelineStage
	for _, s := range pipelines.FanOut(plan, len(p.Stages), lead, cheap) {
		row := store.PipelineStage{Key: s.Key, Kind: s.Kind, Title: s.Title, DependsOn: s.DependsOn, Provider: s.Provider,
			Output: s.Output, Files: s.Files, Prompt: s.Prompt, DoneWhen: s.DoneWhen, Status: pipelines.StageWaiting, Position: s.Position}
		if s.Model != "" {
			row.Model = ptr(s.Model)
		}
		rows = append(rows, row)
	}
	if err := d.Store.AddPipelineStages(ctx, id, rows); err != nil {
		return err
	}
	if err := d.Store.SetPipelineStatus(ctx, id, pipelines.PipelineRunning, nil); err != nil {
		return err
	}
	return d.Reconcile(ctx, id)
}

// EditPlan replaces the plan while it waits for approval (re-validated).
func (d *Driver) EditPlan(ctx context.Context, id int64, raw json.RawMessage) ([]string, error) {
	p, err := d.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Status != pipelines.PipelineAwaitingApproval {
		return nil, fmt.Errorf("%w: the plan can only be edited while it waits for approval", ErrConflict)
	}
	plan, err := pipelines.ParsePlan(raw)
	if err != nil {
		return []string{err.Error()}, nil
	}
	if errs := pipelines.Validate(plan, d.Tasks.EnabledAgents()); len(errs) > 0 {
		return errs, nil
	}
	b, _ := json.Marshal(plan)
	return nil, d.Store.SetPipelinePlan(ctx, id, b, p.PlanAttempts)
}

// Redesign sends the owner's note to the design stage as a follow-up.
func (d *Driver) Redesign(ctx context.Context, id int64, note string) error {
	p, err := d.load(ctx, id)
	if err != nil {
		return err
	}
	if p.Status != pipelines.PipelineAwaitingApproval {
		return fmt.Errorf("%w: the pipeline is not waiting for approval", ErrConflict)
	}
	i := slices.IndexFunc(p.Stages, func(s *store.PipelineStage) bool { return s.Kind == pipelines.KindDesign })
	if i < 0 || p.Stages[i].TaskID == nil {
		return fmt.Errorf("%w: no design stage", ErrConflict)
	}
	if _, err := d.Tasks.Message(ctx, *p.Stages[i].TaskID, "Redesign the plan: "+note, nil); err != nil {
		return err
	}
	if err := d.Store.SetPipelinePlan(ctx, id, nil, 0); err != nil {
		return err
	}
	if err := d.Store.SetStageStatus(ctx, p.Stages[i].ID, pipelines.StageRunning, nil, nil); err != nil {
		return err
	}
	return d.Store.SetPipelineStatus(ctx, id, pipelines.PipelineRunning, nil)
}

func (d *Driver) stage(p *store.Pipeline, stageID int64) (*store.PipelineStage, error) {
	for _, s := range p.Stages {
		if s.ID == stageID {
			return s, nil
		}
	}
	return nil, store.ErrNotFound
}

// Retry re-runs a failed stage: a follow-up on its task (with the note), or
// a new task when it never started.
func (d *Driver) Retry(ctx context.Context, id, stageID int64, note string) error {
	p, err := d.load(ctx, id)
	if err != nil {
		return err
	}
	st, err := d.stage(p, stageID)
	if err != nil {
		return err
	}
	if st.Status != pipelines.StageFailed {
		return fmt.Errorf("%w: only a failed stage can be retried", ErrConflict)
	}
	if st.TaskID != nil {
		text := "Retry this stage."
		if strings.TrimSpace(note) != "" {
			text += " " + note
		}
		if _, err := d.Tasks.Message(ctx, *st.TaskID, text, nil); err != nil {
			return err
		}
		if err := d.Store.SetStageStatus(ctx, st.ID, pipelines.StageRunning, nil, nil); err != nil {
			return err
		}
	} else if err := d.Store.SetStageStatus(ctx, st.ID, pipelines.StageWaiting, nil, nil); err != nil {
		return err
	}
	if err := d.Store.SetPipelineStatus(ctx, id, pipelines.PipelineRunning, nil); err != nil {
		return err
	}
	return d.Reconcile(ctx, id)
}

// Skip marks a failed or waiting stage skipped; its dependants may start.
func (d *Driver) Skip(ctx context.Context, id, stageID int64) error {
	p, err := d.load(ctx, id)
	if err != nil {
		return err
	}
	st, err := d.stage(p, stageID)
	if err != nil {
		return err
	}
	if st.Status == pipelines.StageRunning || st.Status == pipelines.StageDone {
		return fmt.Errorf("%w: a %s stage cannot be skipped", ErrConflict, st.Status)
	}
	if err := d.Store.SetStageStatus(ctx, st.ID, pipelines.StageSkipped, nil, nil); err != nil {
		return err
	}
	if err := d.Store.SetPipelineStatus(ctx, id, pipelines.PipelineRunning, nil); err != nil {
		return err
	}
	return d.Reconcile(ctx, id)
}

// RaiseBudget sets a higher budget and resumes a pipeline stopped by it.
func (d *Driver) RaiseBudget(ctx context.Context, id int64, budget float64) error {
	p, err := d.load(ctx, id)
	if err != nil {
		return err
	}
	if budget <= p.CostUSD {
		return fmt.Errorf("%w: the budget must exceed what was spent ($%.2f)", ErrInvalid, p.CostUSD)
	}
	if err := d.Store.SetPipelineBudget(ctx, id, budget); err != nil {
		return err
	}
	if p.Status == pipelines.PipelineNeedsYou {
		if err := d.Store.SetPipelineStatus(ctx, id, pipelines.PipelineRunning, nil); err != nil {
			return err
		}
	}
	return d.Reconcile(ctx, id)
}

// Cancel cancels the pipeline, its running tasks and its open stages.
func (d *Driver) Cancel(ctx context.Context, id int64) error {
	p, err := d.load(ctx, id)
	if err != nil {
		return err
	}
	if p.Status == pipelines.PipelineCompleted || p.Status == pipelines.PipelineCancelled {
		return fmt.Errorf("%w: the pipeline already ended", ErrConflict)
	}
	if err := d.Store.SetPipelineStatus(ctx, id, pipelines.PipelineCancelled, nil); err != nil {
		return err
	}
	ids, err := d.Store.PipelineOpenTaskIDs(ctx, id)
	if err != nil {
		return err
	}
	for _, tid := range ids {
		if _, err := d.Tasks.Cancel(ctx, tid); err != nil && !errors.Is(err, store.ErrConflict) {
			slog.Warn("pipeline cancel task", "task_id", tid, "err", err)
		}
	}
	for _, st := range p.Stages {
		switch st.Status {
		case pipelines.StageWaiting, pipelines.StageReady, pipelines.StageRunning:
			if err := d.Store.SetStageStatus(ctx, st.ID, pipelines.StageCancelled, nil, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// OwnershipCheck is the scheduler's BeforePush: a build stage's diff may only
// touch its owned globs; otherwise nothing is pushed and the stage fails
// (the pipeline goes needs_you with the list).
func (d *Driver) OwnershipCheck(ctx context.Context, h hosts.Host, t *store.Task) error {
	if t.StageID == nil {
		return nil
	}
	globs, err := d.Store.StageFiles(ctx, *t.StageID)
	if err != nil || len(globs) == 0 {
		return nil
	}
	base := deref(t.BaseRef)
	diff, truncated, err := h.Diff(ctx, t.ID, base, 8<<20)
	if err != nil {
		return err
	}
	if truncated {
		return fmt.Errorf("the diff is too large to check file ownership")
	}
	if outside := pipelines.OutsideOwnership(globs, DiffFiles(diff)); len(outside) > 0 {
		return fmt.Errorf("files outside this subtask's ownership (%s): %s", strings.Join(globs, ", "), strings.Join(outside, ", "))
	}
	return nil
}

// DiffFiles lists the paths a unified git diff touches (both sides of a rename).
func DiffFiles(diff string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "diff --git a/") {
			continue
		}
		rest := strings.TrimPrefix(line, "diff --git a/")
		i := strings.Index(rest, " b/")
		if i < 0 {
			continue
		}
		for _, f := range []string{rest[:i], rest[i+3:]} {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}
