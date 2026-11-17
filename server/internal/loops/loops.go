// Package loops holds the decisions of "loop until…" tasks
// (docs/SPEC-server.md "Loops"): whether another iteration runs after a
// check, what its prompt says, and how check output is trimmed. It has no
// I/O; the scheduler applies the result.
package loops

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/andrewsl105/fleetpocket/server/internal/autofix"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

const (
	DefaultMaxIterations = 3
	MaxMaxIterations     = 10
	// MaxIdle iterations in a row without file changes end the loop.
	MaxIdle = 2
	// MaxOutputBytes bounds the trimmed check output kept in the timeline
	// and given to the next iteration.
	MaxOutputBytes = 48 * 1024
	MaxCommandLen  = 2000
	MaxTimeoutSec  = 3600
)

// Normalize validates a loop config and fills its defaults. nil stays nil.
func Normalize(c *store.LoopConfig) (*store.LoopConfig, error) {
	if c == nil {
		return nil, nil
	}
	out := *c
	out.CheckCommand = strings.TrimSpace(out.CheckCommand)
	switch {
	case out.CheckCommand == "":
		return nil, fmt.Errorf("loop.check_command is required")
	case len(out.CheckCommand) > MaxCommandLen:
		return nil, fmt.Errorf("loop.check_command is longer than %d characters", MaxCommandLen)
	case strings.ContainsAny(out.CheckCommand, "\x00"):
		return nil, fmt.Errorf("loop.check_command contains a NUL byte")
	}
	if out.MaxIterations == 0 {
		out.MaxIterations = DefaultMaxIterations
	}
	if out.MaxIterations < 1 || out.MaxIterations > MaxMaxIterations {
		return nil, fmt.Errorf("loop.max_iterations must be 1..%d", MaxMaxIterations)
	}
	if out.BudgetUSD != nil && *out.BudgetUSD <= 0 {
		out.BudgetUSD = nil
	}
	if out.TimeoutSec < 0 || out.TimeoutSec > MaxTimeoutSec {
		return nil, fmt.Errorf("loop.timeout_sec must be 1..%d", MaxTimeoutSec)
	}
	return &out, nil
}

// Check is the result of one check run.
type Check struct {
	Command  string
	ExitCode int
	Output   string
	TimedOut bool
}

func (c Check) Passed() bool { return c.ExitCode == 0 && !c.TimedOut }

// State is what the scheduler knows when an iteration's run ended.
type State struct {
	Iteration     int // the iteration that just ended (1-based)
	MaxIterations int
	Idle          int  // iterations in a row without file changes before this one
	Changed       bool // this iteration committed file changes
	Check         *Check
	// LimitHit: the run stopped at a turn, budget or duration limit.
	LimitHit  bool
	Limit     string
	SpentUSD  float64 // the loop's cost so far, this iteration included
	BudgetUSD float64 // 0: no loop budget
}

// Decision says whether another iteration runs; if not, the final loop
// state and why.
type Decision struct {
	Next   bool
	State  string // store.Loop* when !Next
	Reason string
	Idle   int // for the next iteration
}

// Decide applies the stop conditions: the check passed, a run limit (or
// the loop budget) was hit, no check result, two iterations in a row
// without file changes, max_iterations, the loop budget.
func Decide(st State) Decision {
	overBudget := st.BudgetUSD > 0 && st.SpentUSD >= st.BudgetUSD
	idle := st.Idle + 1
	if st.Changed {
		idle = 0
	}
	switch {
	case st.LimitHit && overBudget:
		return Decision{State: store.LoopBudget, Reason: fmt.Sprintf("loop budget $%.2f used up", st.BudgetUSD)}
	case st.LimitHit:
		return Decision{State: store.LoopStopped, Reason: "the run stopped at a limit: " + st.Limit}
	case st.Check == nil:
		return Decision{State: store.LoopStopped, Reason: "the runner reported no check result (update the runner image)"}
	case st.Check.Passed():
		return Decision{State: store.LoopPassed, Reason: fmt.Sprintf("check passed in iteration %d/%d", st.Iteration, st.MaxIterations)}
	case idle >= MaxIdle:
		return Decision{State: store.LoopNoChanges, Reason: fmt.Sprintf("%d iterations in a row changed no files", idle)}
	case st.Iteration >= st.MaxIterations:
		return Decision{State: store.LoopMaxIterations, Reason: fmt.Sprintf("check still failing after %d iterations", st.Iteration)}
	case overBudget:
		return Decision{State: store.LoopBudget, Reason: fmt.Sprintf("loop budget $%.2f used up", st.BudgetUSD)}
	}
	return Decision{Next: true, Idle: idle, Reason: fmt.Sprintf("check failed (%s); iteration %d/%d", exitText(*st.Check), st.Iteration+1, st.MaxIterations)}
}

func exitText(c Check) string {
	if c.TimedOut {
		return "timed out"
	}
	return fmt.Sprintf("exit code %d", c.ExitCode)
}

// TrimOutput trims check output like CI logs (internal/autofix): ANSI codes
// removed, the last lines plus earlier error lines; then capped in size,
// keeping the end.
func TrimOutput(out string) string {
	t := autofix.TrimLog(out, autofix.TailLines)
	if len(t) <= MaxOutputBytes {
		return t
	}
	cut := t[len(t)-MaxOutputBytes:]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[1:]
	}
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i < len(cut)-1 {
		cut = cut[i+1:]
	}
	return "…\n" + cut
}

// Input is the prompt of the next iteration: the failed check and its
// (trimmed) output.
func Input(next, maxIterations int, c Check) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Loop iteration %d of %d. After your last changes the check `%s` failed (%s).\n", next, maxIterations, c.Command, exitText(c))
	b.WriteString("Fix what it reports, then finish with a short summary; Jungle Monkey runs the check again when you finish.\n\n")
	b.WriteString("Check output (trimmed):\n```\n")
	b.WriteString(strings.TrimRight(c.Output, "\n"))
	b.WriteString("\n```\n")
	return b.String()
}

// SystemPrompt is the loop's note in the agent's system prompt.
func SystemPrompt(c *store.LoopConfig) string {
	return fmt.Sprintf("This task loops until a check passes: when you finish, Jungle Monkey runs `%s` in /workspace; if it fails you get its output in the next iteration (at most %d). Make the check pass; do not weaken or skip it.\n",
		c.CheckCommand, c.MaxIterations)
}
