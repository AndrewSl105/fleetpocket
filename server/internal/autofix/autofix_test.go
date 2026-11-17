package autofix

import (
	"fmt"
	"strings"
	"testing"
)

func on() Settings { return Settings{Enabled: true} }

func ours(attempts int) State {
	return State{OpenedBySystem: true, HeadSHA: "abc", LastPushedSHA: "abc", Attempts: attempts}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		s    Settings
		st   State
		sig  string
		want Outcome
	}{
		{"off", Settings{}, ours(0), "x", Skip},
		{"first attempt", on(), ours(0), "x", Fix},
		{"human push", on(), State{OpenedBySystem: true, HeadSHA: "def", LastPushedSHA: "abc"}, "x", Skip},
		{"not our PR", on(), State{HeadSHA: "abc", LastPushedSHA: "abc"}, "x", Skip},
		{"run active", on(), func() State { s := ours(0); s.RunActive = true; return s }(), "x", Skip},
		{"cancelled by follow-up", on(), func() State { s := ours(1); s.Cancelled = true; return s }(), "x", Skip},
		{"attempt cap", on(), ours(2), "y", NeedsYou},
		{"same signature twice", on(), func() State { s := ours(1); s.LastSignature = "x"; return s }(), "x", NeedsYou},
		{"new signature", on(), func() State { s := ours(1); s.LastSignature = "x"; return s }(), "y", Fix},
		{"budget", on(), func() State { s := ours(1); s.SpentUSD, s.LoopBudgetUSD = 2, 2; return s }(), "y", NeedsYou},
		{"check not listed", Settings{Enabled: true, Checks: []string{"lint"}}, ours(0), "x", Skip},
	}
	for _, c := range cases {
		got, why := Decide(c.s, c.st, "test", c.sig)
		if got != c.want {
			t.Errorf("%s: got %v (%s), want %v", c.name, got, why, c.want)
		}
	}
}

func TestSettingsClamp(t *testing.T) {
	if (Settings{}).Attempts() != 2 || (Settings{MaxAttempts: 9}).Attempts() != 5 || (Settings{MaxAttempts: 3}).Attempts() != 3 {
		t.Fatal("attempt clamp")
	}
	if (Settings{}).Budget(4) != 2 || (Settings{BudgetUSD: 1}).Budget(4) != 1 {
		t.Fatal("budget default")
	}
}

func TestTrimLog(t *testing.T) {
	var b strings.Builder
	b.WriteString("\x1b[31merror: early failure\x1b[0m\n")
	for i := range 1000 {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	b.WriteString("--- FAIL: TestX")
	got := TrimLog(b.String(), 400)
	lines := strings.Split(got, "\n")
	if lines[0] != "error: early failure" || lines[1] != "…" {
		t.Fatalf("head: %q", lines[:2])
	}
	if strings.Contains(got, "\x1b") || strings.Contains(got, "line 500\n") {
		t.Fatal("ANSI or middle lines kept")
	}
	if len(lines) != 402 || lines[len(lines)-1] != "--- FAIL: TestX" {
		t.Fatalf("tail: %d lines, last %q", len(lines), lines[len(lines)-1])
	}
}

func TestSignatureStable(t *testing.T) {
	a := Signature("test", "2026-11-17T10:00:00.1Z --- FAIL: TestX (0.12s)\nfoo_test.go:42: error: want 1")
	b := Signature("test", "2026-11-18T09:00:00.9Z \x1b[31m--- FAIL: TestX (3.40s)\x1b[0m\nfoo_test.go:57: error: want 1")
	if a != b {
		t.Fatal("same failure, different signature")
	}
	if a == Signature("lint", "--- FAIL: TestX (0.12s)\nfoo_test.go:42: error: want 1") {
		t.Fatal("check name not part of signature")
	}
	if a == Signature("test", "--- FAIL: TestY") {
		t.Fatal("different failure, same signature")
	}
}

func TestGuardDiff(t *testing.T) {
	if r := GuardDiff([]FileChange{{Path: "server/x.go", Patch: "+return nil"}}); len(r) != 0 {
		t.Fatalf("clean diff flagged: %v", r)
	}
	bad := [][]FileChange{
		{{Path: ".github/workflows/ci.yml"}},
		{{Path: ".golangci.yml"}},
		{{Path: "x_test.go", Deleted: true}},
		{{Path: "x_test.go", Patch: "+\tt.Skip(\"flaky\")"}},
		{{Path: "a.test.ts", Patch: "+it.skip('x', () => {})"}},
		{{Path: "vitest.config.ts", Patch: "+  coverage: { lines: 10 },"}},
	}
	for _, d := range bad {
		if len(GuardDiff(d)) == 0 {
			t.Errorf("not flagged: %+v", d)
		}
	}
}

func TestParseDiffFeedsGuard(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
index 1..2 100644
--- a/main.go
+++ b/main.go
@@ -1 +1 @@
-x
+y
diff --git a/old_test.go b/old_test.go
deleted file mode 100644
--- a/old_test.go
+++ /dev/null
@@ -1 +0,0 @@
-package x
diff --git a/.github/workflows/ci.yml b/.github/workflows/ci.yml
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -1 +1 @@
+on: push
diff --git a/web/a.test.ts b/web/a.test.ts
--- a/web/a.test.ts
+++ b/web/a.test.ts
@@ -1 +1 @@
+it.skip('x', () => {})
`
	files := ParseDiff(diff)
	if len(files) != 4 || files[0].Path != "main.go" || !files[1].Deleted || files[1].Path != "old_test.go" {
		t.Fatalf("%+v", files)
	}
	if got := GuardDiff(files[:1]); len(got) != 0 {
		t.Fatalf("plain change flagged: %v", got)
	}
	got := strings.Join(GuardDiff(files), "|")
	for _, want := range []string{"deletes test file: old_test.go", "changes CI configuration: .github/workflows/ci.yml", "skips a test in web/a.test.ts"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}
