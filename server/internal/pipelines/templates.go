// Package pipelines runs a pipeline: a graph of stages where every stage is
// an ordinary task (docs/SPEC-server.md "Pipelines"). This file holds the
// templates; plan.go the design stage's plan contract; engine.go the pure
// scheduling rules the scheduler loop applies.
package pipelines

import "slices"

// Stage kinds.
const (
	KindScout     = "scout"
	KindDesign    = "design"
	KindBuild     = "build"
	KindIntegrate = "integrate"
	KindReview    = "review"
	KindReport    = "report"
)

// Stage outputs: the task outputs plus "branch" (push without a PR).
const (
	OutputPR        = "pr"
	OutputArtifacts = "artifacts"
	OutputBranch    = "branch"
)

// Model tiers: a stage runs on the provider's cheap model (CHEAP_MODEL_*) or
// the task's normal (strong) model.
const (
	TierCheap  = "cheap"
	TierStrong = "strong"
)

// StageDef is one stage of a template.
type StageDef struct {
	Key       string   `json:"key"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	DependsOn []string `json:"depends_on"`
	Tier      string   `json:"tier"`
	Output    string   `json:"output"`
	// Skills are added when the server has them (missing slugs are ignored).
	Skills []string `json:"skills,omitempty"`
	// Instructions open the stage's prompt.
	Instructions string `json:"-"`
	// Inputs are dependency artifacts handed over as attachments
	// (<stage-key>/<file>).
	Inputs []string `json:"inputs,omitempty"`
}

// Template is a versioned pipeline shape. FanOut: after the design stage's
// plan is approved, build stages are inserted per subtask (fanout.go)
// followed by integrate and review.
type Template struct {
	Key         string     `json:"key"`
	Version     int        `json:"version"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Stages      []StageDef `json:"stages"`
	// Approval: the pipeline waits for the owner after the design stage.
	Approval bool `json:"approval"`
	FanOut   bool `json:"fan_out"`
}

const scoutInstructions = `You are the scout stage of a pipeline. Do not change the repository.
Explore it and write .fleetpocket/out/report.md with these sections: Structure, Entry points, Relevant files (for the request below), Risks, How to test.
Also write .fleetpocket/out/manifest.json listing the files you produced, per the artifact contract.`

const designInstructions = `You are the design stage of a pipeline. Do not change the repository.
Read the scout report in the attachments, then write .fleetpocket/out/plan.json following this contract exactly:
{"summary": "...", "contracts": [{"path": "...", "description": "..."}],
 "subtasks": [{"key": "api", "title": "...", "prompt": "...", "provider": "claude|codex|cursor",
   "files": ["server/internal/foo/**"], "depends_on": [], "done_when": "..."}],
 "risks": ["..."]}
Rules: 1-6 subtasks; unique keys (lowercase, digits, dashes); no dependency cycles; subtasks that can run at the same time must not own overlapping files;
if contracts is non-empty, the first subtask has key "contracts", owns those paths, and every other subtask depends on it.
Add diagrams (Mermaid) where they help and list every file in .fleetpocket/out/manifest.json.`

const auditDesignInstructions = `You are the design stage of an audit pipeline. Do not change the repository.
Read the scout report in the attachments and document the system: database schema, components (C4) and infrastructure, as Mermaid diagrams under .fleetpocket/out/, listed in .fleetpocket/out/manifest.json.`

const reportInstructions = `You are the report stage of an audit pipeline. Do not change the repository.
From the attachments write .fleetpocket/out/audit.md: findings, risks, and suggested tasks. Write each suggested task as a level-3 heading "### Task: <title>" followed by its prompt.
List it in .fleetpocket/out/manifest.json.`

const implementInstructions = `Implement the request below. The scout's report is attached (scout/report.md): use it to find your way, then verify against the code.`

var templates = []Template{
	{
		Key: "scout-implement", Version: 1, Title: "Scout → Implement",
		Description: "A cheap scout maps the code, then a strong model implements and opens a PR.",
		Stages: []StageDef{
			{Key: "scout", Kind: KindScout, Title: "Scout", Tier: TierCheap, Output: OutputArtifacts, Instructions: scoutInstructions},
			{Key: "implement", Kind: KindBuild, Title: "Implement", DependsOn: []string{"scout"}, Tier: TierStrong, Output: OutputPR,
				Instructions: implementInstructions, Inputs: []string{"scout/report.md"}},
		},
	},
	{
		Key: "design-parallel", Version: 1, Title: "Design → parallel build",
		Description: "Scout, design a plan you approve, build its subtasks in parallel, integrate, review and open one PR.",
		Approval:    true, FanOut: true,
		Stages: []StageDef{
			{Key: "scout", Kind: KindScout, Title: "Scout", Tier: TierCheap, Output: OutputArtifacts, Instructions: scoutInstructions},
			{Key: "design", Kind: KindDesign, Title: "Design", DependsOn: []string{"scout"}, Tier: TierStrong, Output: OutputArtifacts,
				Skills: []string{"db-schema", "system-design-c4"}, Instructions: designInstructions, Inputs: []string{"scout/report.md"}},
		},
	},
	{
		Key: "audit", Version: 1, Title: "Audit",
		Description: "Scout, document the system with diagrams, and report findings and suggested tasks. No branch, no PR.",
		Stages: []StageDef{
			{Key: "scout", Kind: KindScout, Title: "Scout", Tier: TierCheap, Output: OutputArtifacts, Instructions: scoutInstructions},
			{Key: "design", Kind: KindDesign, Title: "Diagrams", DependsOn: []string{"scout"}, Tier: TierStrong, Output: OutputArtifacts,
				Skills: []string{"db-schema", "system-design-c4", "infra-map"}, Instructions: auditDesignInstructions, Inputs: []string{"scout/report.md"}},
			{Key: "report", Kind: KindReport, Title: "Report", DependsOn: []string{"design"}, Tier: TierStrong, Output: OutputArtifacts,
				Instructions: reportInstructions, Inputs: []string{"scout/report.md"}},
		},
	},
}

// Templates lists the templates (a copy).
func Templates() []Template { return slices.Clone(templates) }

// TemplateByKey returns a template.
func TemplateByKey(key string) (Template, bool) {
	for _, t := range templates {
		if t.Key == key {
			return t, true
		}
	}
	return Template{}, false
}
