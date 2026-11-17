// Package autofix holds the decision logic of the CI auto-fix loop: when a
// failed check may start a fix run, how job logs are trimmed for the agent,
// how a failure is fingerprinted, and which diffs must not be pushed without
// the owner. It has no I/O so the webhook path and local-mode CI polling can
// share it.
package autofix

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Defaults and limits for the per-project settings.
const (
	DefaultMaxAttempts = 2
	MaxMaxAttempts     = 5
	TailLines          = 400
)

// Settings are the per-project auto-fix settings (with a global default).
// The zero value is "off", i.e. today's behaviour.
type Settings struct {
	Enabled bool
	// MaxAttempts is clamped to 1..MaxMaxAttempts; 0 means DefaultMaxAttempts.
	MaxAttempts int
	// Checks limits auto-fix to these check names; empty means all checks.
	Checks []string
	// BudgetUSD per attempt; 0 means the project's task budget / 2.
	BudgetUSD      float64
	AllowCIChanges bool
}

// Attempts returns the effective attempt cap.
func (s Settings) Attempts() int {
	switch {
	case s.MaxAttempts <= 0:
		return DefaultMaxAttempts
	case s.MaxAttempts > MaxMaxAttempts:
		return MaxMaxAttempts
	}
	return s.MaxAttempts
}

// Budget returns the effective per-attempt budget.
func (s Settings) Budget(taskBudgetUSD float64) float64 {
	if s.BudgetUSD > 0 {
		return s.BudgetUSD
	}
	return taskBudgetUSD / 2
}

// Covers reports whether a failing check is in scope.
func (s Settings) Covers(check string) bool {
	if len(s.Checks) == 0 {
		return true
	}
	for _, c := range s.Checks {
		if strings.EqualFold(c, check) {
			return true
		}
	}
	return false
}

// State is what the server knows about the task when a check fails.
type State struct {
	OpenedBySystem bool
	HeadSHA        string // SHA the failing check ran on
	LastPushedSHA  string // last commit the system pushed to the branch
	RunActive      bool
	Attempts       int    // auto-fix attempts already made in this loop
	LastSignature  string // signature that triggered the previous attempt
	Cancelled      bool   // human push, manual follow-up or PR closed
	SpentUSD       float64
	LoopBudgetUSD  float64 // 0 = no loop-level cap
}

// Outcome of Decide.
type Outcome int

const (
	Skip     Outcome = iota // do nothing (not ours, off, stale, busy)
	Fix                     // start a fix attempt
	NeedsYou                // stop the loop and ask the owner
)

// Decide applies the guards of the auto-fix flow to one failure.
func Decide(s Settings, st State, check, signature string) (Outcome, string) {
	switch {
	case !s.Enabled:
		return Skip, "auto-fix is off"
	case !st.OpenedBySystem:
		return Skip, "PR was not opened by the system"
	case st.Cancelled:
		return Skip, "auto-fix cancelled"
	case !s.Covers(check):
		return Skip, "check not in auto-fix list"
	case st.HeadSHA == "" || st.HeadSHA != st.LastPushedSHA:
		return Skip, "failing commit is not the latest system push"
	case st.RunActive:
		return Skip, "a run is active on the task"
	case st.Attempts >= s.Attempts():
		return NeedsYou, fmt.Sprintf("CI still failing after %d fix attempts", st.Attempts)
	case st.Attempts > 0 && signature != "" && signature == st.LastSignature:
		return NeedsYou, "the same failure repeated after a fix"
	case st.LoopBudgetUSD > 0 && st.SpentUSD >= st.LoopBudgetUSD:
		return NeedsYou, "auto-fix budget reached"
	}
	return Fix, fmt.Sprintf("auto-fix attempt %d/%d", st.Attempts+1, s.Attempts())
}

var (
	ansiRE  = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07`)
	errorRE = regexp.MustCompile(`(?i)(\berror\b|\bfail(ed|ure)?\b|\bpanic\b|exception|assert|--- FAIL|✕|✗)`)
	// Timestamps and hex/number noise removed before hashing so the same
	// failure on another run yields the same signature.
	tsRE    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T[\d:.]+Z\s*`)
	noiseRE = regexp.MustCompile(`0x[0-9a-fA-F]+|\b[0-9a-f]{7,40}\b|\d+(\.\d+)?(ms|s)\b|:\d+(:\d+)?\b`)
)

// StripANSI removes terminal escape sequences.
func StripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// TrimLog strips ANSI codes and keeps the last tail lines plus any earlier
// line that matches an error pattern, in original order. Skipped stretches
// are marked with "…".
func TrimLog(raw string, tail int) string {
	if tail <= 0 {
		tail = TailLines
	}
	lines := strings.Split(strings.ReplaceAll(StripANSI(raw), "\r\n", "\n"), "\n")
	start := max(len(lines)-tail, 0)
	var b strings.Builder
	gap := false
	for i, l := range lines {
		if i >= start || errorRE.MatchString(l) {
			if gap {
				b.WriteString("…\n")
				gap = false
			}
			b.WriteString(l)
			if i < len(lines)-1 {
				b.WriteByte('\n')
			}
			continue
		}
		gap = true
	}
	return b.String()
}

// Signature fingerprints a failure: check name plus the first three
// normalised error lines of its log, hashed.
func Signature(check, log string) string {
	var picked []string
	for _, l := range strings.Split(StripANSI(log), "\n") {
		l = strings.TrimSpace(tsRE.ReplaceAllString(strings.TrimSpace(l), ""))
		if l == "" || !errorRE.MatchString(l) {
			continue
		}
		picked = append(picked, strings.ToLower(noiseRE.ReplaceAllString(l, "#")))
		if len(picked) == 3 {
			break
		}
	}
	sum := sha256.Sum256([]byte(strings.ToLower(check) + "\n" + strings.Join(picked, "\n")))
	return hex.EncodeToString(sum[:])[:16]
}

// FileChange is one file of the fix diff.
type FileChange struct {
	Path    string
	Deleted bool
	Patch   string // unified diff hunk text; may be empty
}

var (
	ciFiles = map[string]bool{
		".gitlab-ci.yml": true, ".travis.yml": true, "azure-pipelines.yml": true,
		"Jenkinsfile": true, ".golangci.yml": true, ".golangci.yaml": true,
		"codecov.yml": true, ".codecov.yml": true, "bitbucket-pipelines.yml": true,
	}
	testFileRE = regexp.MustCompile(`(_test\.go|\.(test|spec)\.[cm]?[jt]sx?|(^|/)test_[^/]+\.py|_test\.py)$`)
	skipRE     = regexp.MustCompile(`^\+.*(\bt\.Skip(Now|f)?\(|\b(it|describe|test)\.(skip|todo)\(|\bx(it|describe)\(|@pytest\.mark\.skip|@unittest\.skip)`)
	coverageRE = regexp.MustCompile(`(?i)^\+.*(coverage|threshold|fail_under|--cov-fail-under)`)
)

// GuardDiff returns the reasons a fix diff needs the owner's approval
// (CI configuration, deleted or skipped tests, coverage thresholds).
// An empty result means the diff may be pushed.
func GuardDiff(files []FileChange) []string {
	var out []string
	for _, f := range files {
		p := strings.TrimPrefix(f.Path, "./")
		switch {
		case strings.HasPrefix(p, ".github/workflows/") || strings.HasPrefix(p, ".circleci/") || ciFiles[path.Base(p)]:
			out = append(out, "changes CI configuration: "+p)
		case testFileRE.MatchString(p) && f.Deleted:
			out = append(out, "deletes test file: "+p)
		}
		for _, l := range strings.Split(f.Patch, "\n") {
			if skipRE.MatchString(l) {
				out = append(out, "skips a test in "+p)
				break
			}
			if coverageRE.MatchString(l) {
				out = append(out, "changes a coverage threshold in "+p)
				break
			}
		}
	}
	return out
}

// Instruction is the text appended to the follow-up run.
func Instruction(n, maxAttempts int, checks []string, signature, logName string) string {
	return fmt.Sprintf("CI auto-fix attempt %d/%d. Failing checks: %s (signature %s). "+
		"The trimmed job logs are attached as %s. Fix the cause and keep the change minimal. "+
		"Do not skip, delete or weaken tests, and do not edit CI configuration.",
		n, maxAttempts, strings.Join(checks, ", "), signature, logName)
}
