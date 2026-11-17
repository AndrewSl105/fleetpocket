package pipelines

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

var all = []string{"claude", "codex", "cursor"}

func TestTemplates(t *testing.T) {
	keys := map[string]bool{}
	for _, tp := range Templates() {
		if keys[tp.Key] || tp.Version < 1 || len(tp.Stages) == 0 {
			t.Fatalf("bad template %+v", tp)
		}
		keys[tp.Key] = true
		seen := map[string]bool{}
		for _, s := range tp.Stages {
			for _, d := range s.DependsOn {
				if !seen[d] {
					t.Errorf("%s/%s depends on later or unknown %q", tp.Key, s.Key, d)
				}
			}
			seen[s.Key] = true
			if s.Instructions == "" {
				t.Errorf("%s/%s has no instructions", tp.Key, s.Key)
			}
		}
	}
	for _, k := range []string{"scout-implement", "design-parallel", "audit"} {
		if !keys[k] {
			t.Errorf("missing template %s", k)
		}
	}
	si, _ := TemplateByKey("scout-implement")
	if si.Stages[0].Tier != TierCheap || si.Stages[0].Output != OutputArtifacts || si.Stages[1].Output != OutputPR {
		t.Errorf("scout-implement stages: %+v", si.Stages)
	}
	if !slices.Contains(si.Stages[1].Inputs, "scout/report.md") {
		t.Error("implement does not get scout/report.md")
	}
	au, _ := TemplateByKey("audit")
	for _, s := range au.Stages {
		if s.Output != OutputArtifacts {
			t.Errorf("audit stage %s pushes code", s.Key)
		}
	}
	if dp, _ := TemplateByKey("design-parallel"); !dp.Approval || !dp.FanOut {
		t.Error("design-parallel must wait for approval and fan out")
	}
	var schema map[string]any
	if err := json.Unmarshal(PlanSchema, &schema); err != nil {
		t.Fatal(err)
	}
}

func TestGlobs(t *testing.T) {
	for _, c := range []struct {
		g, f string
		ok   bool
	}{
		{"server/internal/foo/**", "server/internal/foo/a.go", true},
		{"server/internal/foo/**", "server/internal/foo/x/y.go", true},
		{"server/internal/foo/**", "server/internal/bar/a.go", false},
		{"web/src/*.ts", "web/src/a.ts", true},
		{"web/src/*.ts", "web/src/x/a.ts", false},
		{"**/*.md", "docs/a.md", true},
		{"docs/API.md", "docs/API.md", true},
	} {
		if got := MatchGlob(c.g, c.f); got != c.ok {
			t.Errorf("MatchGlob(%q, %q) = %v", c.g, c.f, got)
		}
	}
	for _, c := range []struct {
		a, b string
		ok   bool
	}{
		{"server/**", "web/**", false},
		{"web/**", "web/src/a.ts", true},
		{"web/src/**", "web/src/api/**", true},
		{"docs/API.md", "docs/openapi.yaml", false},
		{"docs", "docs/API.md", true},
		{"a.ts", "*.go", false},
		{"**/*.md", "server/**", true},
	} {
		if got := GlobsOverlap(c.a, c.b); got != c.ok {
			t.Errorf("GlobsOverlap(%q, %q) = %v", c.a, c.b, got)
		}
	}
	if out := OutsideOwnership([]string{"server/**"}, []string{"server/a.go", "web/b.ts"}); !slices.Equal(out, []string{"web/b.ts"}) {
		t.Errorf("OutsideOwnership = %v", out)
	}
}

func sub(key string, files []string, deps ...string) Subtask {
	return Subtask{Key: key, Title: key, Prompt: "do " + key, Provider: "claude", Files: files, DependsOn: deps, DoneWhen: "tests pass"}
}

func valid() *Plan {
	return &Plan{
		Summary:   "Add export",
		Contracts: []Contract{{Path: "docs/openapi.yaml", Description: "API"}},
		Subtasks: []Subtask{
			sub("contracts", []string{"docs/openapi.yaml"}),
			sub("api", []string{"server/**"}, "contracts"),
			sub("web", []string{"web/**"}, "contracts"),
		},
	}
}

func hasErr(errs []string, part string) bool {
	return slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, part) })
}

func TestValidate(t *testing.T) {
	if errs := Validate(valid(), all); len(errs) != 0 {
		t.Fatalf("valid plan: %v", errs)
	}
	cases := map[string]func(p *Plan){
		"needs 1 to 6":  func(p *Plan) { p.Subtasks = nil },
		"used twice":    func(p *Plan) { p.Subtasks[2].Key = "api" },
		"not available": func(p *Plan) { p.Subtasks[1].Provider = "gpt" },
		"cycle": func(p *Plan) {
			p.Subtasks[1].DependsOn = []string{"contracts", "web"}
			p.Subtasks[2].DependsOn = []string{"contracts", "api"}
		},
		"unknown subtask": func(p *Plan) { p.Subtasks[1].DependsOn = []string{"nope"} },
		"same time":       func(p *Plan) { p.Subtasks[2].Files = []string{"server/internal/**"} },
		"must be \"contracts\"": func(p *Plan) {
			p.Subtasks[0].Key = "schema"
			p.Subtasks[1].DependsOn = []string{"schema"}
			p.Subtasks[2].DependsOn = []string{"schema"}
		},
		"must depend on": func(p *Plan) { p.Subtasks[2].DependsOn = nil },
		"is not owned":   func(p *Plan) { p.Contracts = append(p.Contracts, Contract{Path: "agent/src/protocol.ts"}) },
		"reserved":       func(p *Plan) { p.Subtasks[1].Key = "review"; p.Subtasks[1].DependsOn = []string{"contracts"} },
		"done_when":      func(p *Plan) { p.Subtasks[1].DoneWhen = "" },
	}
	for want, mut := range cases {
		p := valid()
		mut(p)
		if errs := Validate(p, all); !hasErr(errs, want) {
			t.Errorf("%s: errors %v", want, errs)
		}
	}
	// Ordered subtasks may own the same files.
	p := valid()
	p.Subtasks[2] = sub("web", []string{"server/**"}, "api")
	if errs := Validate(p, all); len(errs) != 0 {
		t.Errorf("ordered overlap rejected: %v", errs)
	}
	// Seven subtasks.
	p = &Plan{Summary: "s"}
	for _, k := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"} {
		p.Subtasks = append(p.Subtasks, sub(k, []string{k + "/**"}))
	}
	if !hasErr(Validate(p, all), "needs 1 to 6") {
		t.Error("7 subtasks accepted")
	}
	if _, err := ParsePlan([]byte(`{"summary":"x","subtasks":[],"extra":1}`)); err == nil {
		t.Error("unknown field accepted")
	}
	if order := TopoOrder(valid()); !slices.Equal(order, []string{"contracts", "api", "web"}) {
		t.Errorf("TopoOrder = %v", order)
	}
}

func stage(key, status string, pos int, deps ...string) Stage {
	return Stage{Key: key, Status: status, DependsOn: deps, Position: pos, Output: OutputArtifacts}
}

func TestAdvanceOrderAndParallel(t *testing.T) {
	st := []Stage{
		stage("scout", StageDone, 0),
		stage("a", StageWaiting, 1, "scout"), stage("b", StageWaiting, 2, "scout"),
		stage("c", StageWaiting, 3, "scout"), stage("d", StageWaiting, 4, "scout"),
		stage("int", StageWaiting, 5, "a", "b", "c", "d"),
	}
	d := Advance(PipelineRunning, st, 3, 10, 1, 10)
	if !slices.Equal(d.Ready, []string{"a", "b", "c", "d"}) || !slices.Equal(d.Start, []string{"a", "b", "c"}) {
		t.Fatalf("decision %+v", d)
	}
	// Global slots cap max_parallel.
	if d := Advance(PipelineRunning, st, 3, 10, 1, 1); len(d.Start) != 1 {
		t.Errorf("free slots ignored: %+v", d)
	}
	// Idempotent: running stages count, nothing restarts.
	st[1].Status, st[2].Status, st[3].Status, st[4].Status = StageRunning, StageRunning, StageRunning, StageReady
	if d := Advance(PipelineRunning, st, 3, 10, 1, 10); len(d.Start) != 0 {
		t.Errorf("over max_parallel: %+v", d)
	}
	// Skipped satisfies dependants.
	st[1].Status, st[2].Status, st[3].Status, st[4].Status = StageDone, StageSkipped, StageDone, StageDone
	if d := Advance(PipelineRunning, st, 3, 10, 1, 10); !slices.Equal(d.Start, []string{"int"}) {
		t.Errorf("integrate not started: %+v", d)
	}
	st[5].Status = StageDone
	if d := Advance(PipelineRunning, st, 3, 10, 1, 10); d.Status != PipelineCompleted {
		t.Errorf("not completed: %+v", d)
	}
}

func TestAdvanceFailureBudgetCancel(t *testing.T) {
	st := []Stage{stage("scout", StageFailed, 0), stage("x", StageWaiting, 1, "scout")}
	if d := Advance(PipelineRunning, st, 3, 0, 0, 5); d.Status != PipelineNeedsYou || len(d.Start) != 0 {
		t.Errorf("failure: %+v", d)
	}
	st = []Stage{stage("scout", StageDone, 0), stage("x", StageWaiting, 1, "scout")}
	if d := Advance(PipelineRunning, st, 3, 2, 2, 5); d.Status != PipelineNeedsYou || len(d.Start) != 0 || !strings.Contains(d.Reason, "budget") {
		t.Errorf("budget: %+v", d)
	}
	// Raised budget: starts again.
	if d := Advance(PipelineNeedsYou, st, 3, 4, 2, 5); d.Status != PipelineRunning || len(d.Start) != 1 {
		t.Errorf("raised budget: %+v", d)
	}
	for _, s := range []string{PipelineCancelled, PipelineAwaitingApproval, PipelineCompleted} {
		if d := Advance(s, st, 3, 0, 0, 5); len(d.Start) != 0 || d.Status != s {
			t.Errorf("%s: %+v", s, d)
		}
	}
}

func TestStageOutcome(t *testing.T) {
	for _, c := range []struct {
		output, status string
		n              int
		want           string
	}{
		{OutputArtifacts, "completed", 1, StageDone},
		{OutputArtifacts, "completed", 0, StageFailed},
		{OutputArtifacts, "running", 0, ""},
		{OutputBranch, "branch_ready", 0, StageDone},
		{OutputBranch, "completed", 0, StageFailed},
		{OutputPR, "pr_open", 0, StageDone},
		{OutputPR, "branch_ready", 0, StageDone},
		{OutputPR, "completed", 0, StageFailed},
		{OutputPR, "failed", 0, StageFailed},
		{OutputPR, "waiting_input", 0, ""},
	} {
		if got := StageOutcome(c.output, c.status, c.n); got != c.want {
			t.Errorf("%s/%s/%d = %q", c.output, c.status, c.n, got)
		}
	}
}

func TestFanOutAndBranches(t *testing.T) {
	st := FanOut(valid(), 2, "claude", "haiku")
	keys := []string{}
	for _, s := range st {
		keys = append(keys, s.Key)
	}
	if !slices.Equal(keys, []string{"contracts", "api", "web", "integrate", "review"}) {
		t.Fatalf("keys %v", keys)
	}
	if !slices.Equal(st[0].DependsOn, []string{"design"}) || !slices.Equal(st[1].DependsOn, []string{"contracts"}) {
		t.Errorf("deps %v %v", st[0].DependsOn, st[1].DependsOn)
	}
	if !slices.Equal(st[3].DependsOn, []string{"contracts", "api", "web"}) || st[4].Output != OutputPR || st[4].Model != "haiku" {
		t.Errorf("integrate/review %+v %+v", st[3], st[4])
	}
	if st[1].Output != OutputBranch || PushBranch(st[1], 7) != "jm/pipe-7/api" || PushBranch(st[3], 7) != "jm/pipe-7" || PushBranch(st[4], 7) != "" {
		t.Error("branches")
	}
	if BaseRef(KindBuild, 7, "main", true) != "jm/pipe-7" || BaseRef(KindScout, 7, "main", true) != "main" || BaseRef(KindBuild, 7, "main", false) != "main" {
		t.Error("base refs")
	}
	if p := IntegrateInstructions(7, TopoOrder(valid()), "make test"); !strings.Contains(p, "jm/pipe-7/contracts\n- jm/pipe-7/api") || !strings.Contains(p, "make test") {
		t.Errorf("integrate prompt %s", p)
	}
	body := PRBody(valid(), "LGTM", []string{"graph TD; a-->b"})
	for _, w := range []string{"Add export", "- [x] **api**", "LGTM", "```mermaid"} {
		if !strings.Contains(body, w) {
			t.Errorf("PR body lacks %q", w)
		}
	}
	pr := StagePrompt("Do it.", "Add CSV export", []Dep{{Key: "scout", Title: "Scout", Summary: "Found router."}})
	if !strings.Contains(pr, "Add CSV export") || !strings.Contains(pr, "Found router.") {
		t.Errorf("prompt %s", pr)
	}
}
