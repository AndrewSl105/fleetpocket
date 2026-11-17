package pipelines

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// PlanSchema is the JSON Schema of plan.json, served to clients and quoted in
// the design prompt. The server validates plans with Validate (the schema's
// rules plus the cross-field ones a schema cannot express).
//
//go:embed plan.schema.json
var PlanSchema []byte

// Plan is the design stage's plan.json.
type Plan struct {
	Summary   string     `json:"summary"`
	Contracts []Contract `json:"contracts"`
	Subtasks  []Subtask  `json:"subtasks"`
	Risks     []string   `json:"risks"`
}

type Contract struct {
	Path        string `json:"path"`
	Description string `json:"description"`
}

type Subtask struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Prompt    string   `json:"prompt"`
	Provider  string   `json:"provider"`
	Files     []string `json:"files"`
	DependsOn []string `json:"depends_on"`
	DoneWhen  string   `json:"done_when"`
}

// ContractsKey is the subtask that owns the contracts.
const ContractsKey = "contracts"

const maxSubtasks = 6

var keyRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ParsePlan decodes plan.json strictly (unknown fields are an error).
func ParsePlan(b []byte) (*Plan, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var p Plan
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("plan.json is not valid JSON for the plan contract: %v", err)
	}
	return &p, nil
}

// Validate returns every rule the plan breaks (empty: valid). providers are
// the agents available on this server.
func Validate(p *Plan, providers []string) []string {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if strings.TrimSpace(p.Summary) == "" {
		add("summary is empty")
	}
	n := len(p.Subtasks)
	if n < 1 || n > maxSubtasks {
		add("plan has %d subtasks; it needs 1 to %d", n, maxSubtasks)
	}
	keys := map[string]int{}
	for i, s := range p.Subtasks {
		switch {
		case !keyRE.MatchString(s.Key):
			add("subtask %d: key %q must match %s", i+1, s.Key, keyRE)
		case slices.Contains(reserved, s.Key):
			add("subtask key %q is reserved for a pipeline stage", s.Key)
		case keys[s.Key] > 0:
			add("subtask key %q is used twice", s.Key)
		}
		keys[s.Key] = i + 1
		if strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Prompt) == "" {
			add("subtask %q needs a title and a prompt", s.Key)
		}
		if strings.TrimSpace(s.DoneWhen) == "" {
			add("subtask %q needs done_when", s.Key)
		}
		if !slices.Contains(providers, s.Provider) {
			add("subtask %q: provider %q is not available on this server (available: %s)", s.Key, s.Provider, strings.Join(providers, ", "))
		}
		if len(s.Files) == 0 {
			add("subtask %q owns no files", s.Key)
		}
		for _, d := range s.DependsOn {
			if d == s.Key {
				add("subtask %q depends on itself", s.Key)
			} else if _, ok := keys[d]; !ok && !hasKey(p, d) {
				add("subtask %q depends on unknown subtask %q", s.Key, d)
			}
		}
	}
	if len(errs) > 0 {
		return errs
	}
	if cyc := cycle(p); cyc != "" {
		return append(errs, "depends_on has a cycle: "+cyc)
	}
	anc := ancestors(p)
	for i, a := range p.Subtasks {
		for _, b := range p.Subtasks[i+1:] {
			if anc[a.Key][b.Key] || anc[b.Key][a.Key] {
				continue // ordered: never run at the same time
			}
			for _, ga := range a.Files {
				for _, gb := range b.Files {
					if GlobsOverlap(ga, gb) {
						add("subtasks %q and %q can run at the same time but both own %q / %q", a.Key, b.Key, ga, gb)
					}
				}
			}
		}
	}
	if len(p.Contracts) > 0 {
		if p.Subtasks[0].Key != ContractsKey {
			add("the plan has contracts, so its first subtask must be %q", ContractsKey)
		} else {
			c := p.Subtasks[0]
			for _, ct := range p.Contracts {
				if len(OutsideOwnership(c.Files, []string{ct.Path})) > 0 {
					add("contract %q is not owned by subtask %q", ct.Path, ContractsKey)
				}
			}
			for _, s := range p.Subtasks[1:] {
				if !anc[s.Key][ContractsKey] {
					add("subtask %q must depend on %q", s.Key, ContractsKey)
				}
			}
		}
	}
	return errs
}

func hasKey(p *Plan, k string) bool {
	return slices.ContainsFunc(p.Subtasks, func(s Subtask) bool { return s.Key == k })
}

// cycle returns one dependency cycle as "a → b → a", or "".
func cycle(p *Plan) string {
	deps := map[string][]string{}
	for _, s := range p.Subtasks {
		deps[s.Key] = s.DependsOn
	}
	state := map[string]int{} // 1 visiting, 2 done
	var stack []string
	var found string
	var visit func(k string) bool
	visit = func(k string) bool {
		state[k] = 1
		stack = append(stack, k)
		for _, d := range deps[k] {
			if state[d] == 1 {
				i := slices.Index(stack, d)
				found = strings.Join(append(slices.Clone(stack[i:]), d), " → ")
				return true
			}
			if state[d] == 0 && visit(d) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		state[k] = 2
		return false
	}
	for _, s := range p.Subtasks {
		if state[s.Key] == 0 && visit(s.Key) {
			return found
		}
	}
	return ""
}

// ancestors maps each subtask to every subtask it depends on, transitively.
// Call only on an acyclic plan.
func ancestors(p *Plan) map[string]map[string]bool {
	deps := map[string][]string{}
	for _, s := range p.Subtasks {
		deps[s.Key] = s.DependsOn
	}
	out := map[string]map[string]bool{}
	var walk func(k string) map[string]bool
	walk = func(k string) map[string]bool {
		if m, ok := out[k]; ok {
			return m
		}
		m := map[string]bool{}
		out[k] = m
		for _, d := range deps[k] {
			m[d] = true
			for a := range walk(d) {
				m[a] = true
			}
		}
		return m
	}
	for _, s := range p.Subtasks {
		walk(s.Key)
	}
	return out
}

// TopoOrder returns the subtask keys in dependency order (stable by plan
// order): the integrate stage merges branches in this order.
func TopoOrder(p *Plan) []string {
	done := map[string]bool{}
	var out []string
	for len(out) < len(p.Subtasks) {
		progressed := false
		for _, s := range p.Subtasks {
			if done[s.Key] {
				continue
			}
			ready := true
			for _, d := range s.DependsOn {
				if !done[d] {
					ready = false
				}
			}
			if ready {
				done[s.Key] = true
				out = append(out, s.Key)
				progressed = true
			}
		}
		if !progressed {
			break // a cycle; Validate rejects it
		}
	}
	return out
}
