package pipelines

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Pipeline statuses.
const (
	PipelineRunning          = "running"
	PipelineAwaitingApproval = "awaiting_approval"
	PipelineNeedsYou         = "needs_you"
	PipelineCompleted        = "completed"
	PipelineFailed           = "failed"
	PipelineCancelled        = "cancelled"
)

// Stage statuses.
const (
	StageWaiting   = "waiting"
	StageReady     = "ready"
	StageRunning   = "running"
	StageDone      = "done"
	StageFailed    = "failed"
	StageSkipped   = "skipped"
	StageCancelled = "cancelled"
)

// DefaultMaxParallel is a pipeline's default number of running stages.
const DefaultMaxParallel = 3

// Stage is the engine's view of a pipeline_stages row.
type Stage struct {
	Key       string
	Kind      string
	Title     string
	DependsOn []string
	Status    string
	Provider  string
	Model     string
	Output    string
	Skills    []string
	// Files are the owned globs of a build stage (empty: no ownership check).
	Files    []string
	Prompt   string
	DoneWhen string
	Node     *string
	TaskID   *int64
	Position int
}

// satisfied: a dependency that lets dependants start.
func satisfied(status string) bool { return status == StageDone || status == StageSkipped }

// Decision is what one engine pass asks the caller to do.
type Decision struct {
	// Ready are stages that just became ready (waiting → ready).
	Ready []string
	// Start are ready stages to start now (create their tasks), in order.
	Start []string
	// Status is the pipeline's status after this pass.
	Status string
	// Reason explains needs_you (a failed stage, the budget).
	Reason string
}

// Advance applies the scheduling rules to a pipeline's stages. It is pure
// and idempotent: the scheduler calls it on every loop tick and every task
// state change, after a restart too, and acts on the result. freeSlots is the
// server's number of free task slots (MAX_CONCURRENT_TASKS − running tasks).
func Advance(status string, stages []Stage, maxParallel int, budgetUSD, spentUSD float64, freeSlots int) Decision {
	d := Decision{Status: status}
	if status == PipelineCancelled || status == PipelineCompleted || status == PipelineFailed || status == PipelineAwaitingApproval {
		return d
	}
	by := map[string]*Stage{}
	for i := range stages {
		by[stages[i].Key] = &stages[i]
	}
	for _, s := range stages {
		if s.Status == StageFailed {
			d.Status, d.Reason = PipelineNeedsYou, fmt.Sprintf("stage %q failed", s.Key)
			return d
		}
	}
	running, open := 0, 0
	for i := range stages {
		s := &stages[i]
		if s.Status == StageWaiting {
			ok := true
			for _, dep := range s.DependsOn {
				if ds, found := by[dep]; !found || !satisfied(ds.Status) {
					ok = false
				}
			}
			if ok {
				s.Status = StageReady
				d.Ready = append(d.Ready, s.Key)
			}
		}
		switch s.Status {
		case StageRunning:
			running++
			open++
		case StageWaiting, StageReady:
			open++
		}
	}
	if open == 0 {
		d.Status = PipelineCompleted
		return d
	}
	ready := make([]*Stage, 0)
	for i := range stages {
		if stages[i].Status == StageReady {
			ready = append(ready, &stages[i])
		}
	}
	sort.SliceStable(ready, func(i, j int) bool { return ready[i].Position < ready[j].Position })
	if len(ready) > 0 && budgetUSD > 0 && spentUSD >= budgetUSD {
		if running == 0 {
			d.Status, d.Reason = PipelineNeedsYou, fmt.Sprintf("budget used up ($%.2f of $%.2f)", spentUSD, budgetUSD)
		}
		return d
	}
	if maxParallel <= 0 {
		maxParallel = DefaultMaxParallel
	}
	slots := min(maxParallel-running, freeSlots)
	for _, s := range ready {
		if slots <= 0 {
			break
		}
		d.Start = append(d.Start, s.Key)
		slots--
	}
	d.Status = PipelineRunning
	return d
}

// StageOutcome maps a stage task's state to the stage status: done when the
// task ended successfully for its output, failed when it ended otherwise,
// "" while it is still going. artifacts is the task's artifact count.
func StageOutcome(output, taskStatus string, artifacts int) string {
	switch taskStatus {
	case "failed", "cancelled":
		return StageFailed
	}
	switch output {
	case OutputArtifacts:
		if taskStatus == "completed" {
			if artifacts > 0 {
				return StageDone
			}
			return StageFailed
		}
	case OutputBranch:
		if taskStatus == "branch_ready" {
			return StageDone
		}
		if taskStatus == "completed" || taskStatus == "pr_open" {
			return StageFailed
		}
	default: // pr
		if taskStatus == "pr_open" || taskStatus == "branch_ready" {
			return StageDone
		}
		if taskStatus == "completed" {
			return StageFailed // finished without a PR (no changes)
		}
	}
	return ""
}

// ---- Branches ----

// PipelineBranch is created from the base branch when the pipeline starts.
func PipelineBranch(id int64) string { return fmt.Sprintf("jm/pipe-%d", id) }

// StageBranch is where a build stage pushes.
func StageBranch(id int64, key string) string { return fmt.Sprintf("jm/pipe-%d/%s", id, key) }

// BaseRef is the branch a stage's task starts from and PushBranch where it
// pushes ("" for stages that push nothing or open the final PR).
func BaseRef(kind string, id int64, baseBranch string, fanOut bool) string {
	if !fanOut || kind == KindScout || kind == KindDesign {
		return baseBranch
	}
	return PipelineBranch(id)
}

func PushBranch(st Stage, id int64) string {
	switch st.Kind {
	case KindBuild:
		if st.Output == OutputBranch {
			return StageBranch(id, st.Key)
		}
	case KindIntegrate:
		return PipelineBranch(id)
	}
	return ""
}

// ---- Fan-out ----

// Reserved stage keys a subtask may not use.
var reserved = []string{"scout", "design", "approval", "integrate", "review", "report", "implement"}

// FanOut turns an approved plan into stages appended after the existing
// ones (from position next): one build per subtask (output branch, owned
// files, deps mapped), then integrate (all builds) and review (integrate,
// opens the PR). cheap is the review's model.
func FanOut(p *Plan, next int, lead string, cheap string) []Stage {
	var out []Stage
	var builds []string
	for _, s := range p.Subtasks {
		out = append(out, Stage{
			Key: s.Key, Kind: KindBuild, Title: s.Title, DependsOn: withDesign(s.DependsOn),
			Status: StageWaiting, Provider: s.Provider, Output: OutputBranch, Files: slices.Clone(s.Files),
			Prompt: s.Prompt, DoneWhen: s.DoneWhen, Position: next,
		})
		builds = append(builds, s.Key)
		next++
	}
	out = append(out,
		Stage{Key: "integrate", Kind: KindIntegrate, Title: "Integrate", DependsOn: builds, Status: StageWaiting,
			Provider: lead, Output: OutputBranch, Position: next},
		Stage{Key: "review", Kind: KindReview, Title: "Review & PR", DependsOn: []string{"integrate"}, Status: StageWaiting,
			Provider: lead, Model: cheap, Output: OutputPR, Position: next + 1},
	)
	return out
}

// withDesign makes a root build depend on the design stage, so the graph
// stays connected.
func withDesign(deps []string) []string {
	if len(deps) == 0 {
		return []string{"design"}
	}
	return slices.Clone(deps)
}

// ---- Prompts ----

// Dep is a finished dependency as the next stage sees it.
type Dep struct {
	Key     string
	Title   string
	Summary string
}

// StagePrompt builds a stage task's prompt: the stage's instructions, the
// pipeline's request, and the summaries of its dependencies.
func StagePrompt(instructions, request string, deps []Dep) string {
	var b strings.Builder
	if instructions != "" {
		b.WriteString(instructions)
		b.WriteString("\n\n")
	}
	b.WriteString("## Request\n\n")
	b.WriteString(strings.TrimSpace(request))
	b.WriteString("\n")
	for _, d := range deps {
		fmt.Fprintf(&b, "\n## From stage %q (%s)\n\n%s\n", d.Key, d.Title, strings.TrimSpace(d.Summary))
	}
	return b.String()
}

// BuildInstructions open a build stage's prompt.
func BuildInstructions(s Stage) string {
	return fmt.Sprintf(`You are build subtask %q of an approved plan (plan.json, the contracts and scout/report.md are attached).
Only change files matching: %s. Writes elsewhere are refused, and the server rejects the branch if the diff touches other files.
Done when: %s`, s.Key, strings.Join(s.Files, ", "), s.DoneWhen)
}

// IntegrateInstructions: merge every build branch in dependency order.
func IntegrateInstructions(id int64, order []string, check string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the integrate stage. You are on %s. Run `git fetch origin` if the branches below are not present locally, then merge them in this order, resolving conflicts:\n", PipelineBranch(id))
	for _, k := range order {
		fmt.Fprintf(&b, "- %s\n", StageBranch(id, k))
	}
	if check != "" {
		fmt.Fprintf(&b, "\nThen run the full check `%s` and fix what breaks.", check)
	} else {
		b.WriteString("\nThen ask the test-runner subagent (or detect the project's test and lint commands) to run the full check, and fix what breaks.")
	}
	return b.String()
}

// ReviewInstructions: review the integrated diff against the plan.
const ReviewInstructions = `You are the review stage. Use the reviewer subagent and the team's skills if present.
Check the diff against plan.json (each subtask's done_when) and the request. Fix small issues yourself.
Write .fleetpocket/out/review.md with your notes (list it in .fleetpocket/out/manifest.json). The pull request is opened for you.`

// PRBody is the single PR's body: the plan summary, a checklist per
// subtask, the review notes and the diagrams.
func PRBody(p *Plan, review string, diagrams []string) string {
	var b strings.Builder
	b.WriteString("## Plan\n\n")
	b.WriteString(strings.TrimSpace(p.Summary))
	b.WriteString("\n\n## Subtasks\n\n")
	for _, s := range p.Subtasks {
		fmt.Fprintf(&b, "- [x] **%s** (%s): %s\n", s.Title, s.Provider, s.DoneWhen)
	}
	if strings.TrimSpace(review) != "" {
		b.WriteString("\n## Review\n\n")
		b.WriteString(strings.TrimSpace(review))
		b.WriteString("\n")
	}
	for _, d := range diagrams {
		b.WriteString("\n```mermaid\n")
		b.WriteString(strings.TrimSpace(d))
		b.WriteString("\n```\n")
	}
	return b.String()
}
