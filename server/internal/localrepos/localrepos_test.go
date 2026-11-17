package localrepos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func realHome(t *testing.T) string {
	t.Helper()
	h, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func paths(list []Suggestion) map[string]bool {
	m := map[string]bool{}
	for _, s := range list {
		m[s.Path] = true
	}
	return m
}

func TestSuggestionsScan(t *testing.T) {
	home := realHome(t)
	initRepo(t, filepath.Join(home, "code", "app"))                     // depth 1
	initRepo(t, filepath.Join(home, "code", "team", "svc", "api"))      // depth 3
	initRepo(t, filepath.Join(home, "code", "a", "b", "c", "too-deep")) // depth 4
	initRepo(t, filepath.Join(home, "code", "app", "nested"))           // inside a repo: not descended
	initRepo(t, filepath.Join(home, "code", "node_modules", "dep"))     // skipped
	initRepo(t, filepath.Join(home, "code", ".cache", "hidden"))        // skipped
	initRepo(t, filepath.Join(home, "Documents", "GitHub", "site"))     // default root
	initRepo(t, filepath.Join(home, "Downloads", "not-a-root"))         // not scanned
	start := filepath.Join(home, "elsewhere", "cli")                    // the CLI's start dir
	initRepo(t, start)
	// A symlink loop and a symlink to a repo already found.
	if err := os.Symlink(filepath.Join(home, "code"), filepath.Join(home, "code", "team", "loop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "code", "app"), filepath.Join(home, "code", "app-link")); err != nil {
		t.Fatal(err)
	}

	s := &Service{Home: home, Start: start}
	list, err := s.Suggestions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := paths(list)
	want := []string{
		filepath.Join(home, "code", "app"), filepath.Join(home, "code", "team", "svc", "api"),
		filepath.Join(home, "Documents", "GitHub", "site"), start,
	}
	for _, p := range want {
		if !got[p] {
			t.Errorf("missing %s", p)
		}
	}
	if len(list) != len(want) {
		t.Errorf("got %d suggestions, want %d: %v", len(list), len(want), got)
	}
	for _, sg := range list {
		if sg.Branch != "main" || sg.Dirty || sg.LastCommit.IsZero() || sg.Name != filepath.Base(sg.Path) {
			t.Errorf("%+v", sg)
		}
	}
}

func TestSuggestionsCapAndCache(t *testing.T) {
	home := realHome(t)
	for i := 0; i < MaxSuggestions+5; i++ {
		// Only a .git/HEAD: cheap, and enough for the scan.
		dir := filepath.Join(home, "repos", fmt.Sprintf("r%03d", i), ".git")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	s := &Service{Home: home, Roots: []string{"repos"}, Now: func() time.Time { return now }}
	if got := s.scan(context.Background()); len(got) != MaxSuggestions {
		t.Fatalf("scan found %d, want the cap %d", len(got), MaxSuggestions)
	}

	// A context that is already over finds nothing (the 2 s budget).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := s.scan(ctx); len(got) != 0 {
		t.Fatalf("scan after the deadline found %d", len(got))
	}

	// Cached for CacheTTL.
	small := realHome(t)
	initRepo(t, filepath.Join(small, "code", "one"))
	s = &Service{Home: small, Now: func() time.Time { return now }}
	first, _ := s.Suggestions(context.Background())
	initRepo(t, filepath.Join(small, "code", "two"))
	cached, _ := s.Suggestions(context.Background())
	if len(first) != 1 || len(cached) != 1 {
		t.Fatalf("cache: %d then %d", len(first), len(cached))
	}
	now = now.Add(CacheTTL + time.Second)
	fresh, _ := s.Suggestions(context.Background())
	if len(fresh) != 2 {
		t.Fatalf("after the TTL: %d", len(fresh))
	}
}

func TestBrowseStaysInHome(t *testing.T) {
	home := realHome(t)
	initRepo(t, filepath.Join(home, "code", "app"))
	if err := os.MkdirAll(filepath.Join(home, "code", "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := realHome(t)
	if err := os.Symlink(outside, filepath.Join(home, "code", "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "code", "app"), filepath.Join(home, "app-link")); err != nil {
		t.Fatal(err)
	}
	s := &Service{Home: home}

	l, err := s.Browse("")
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != home || l.Parent != nil || len(l.Entries) != 2 || l.Entries[0].Name != "app-link" || !l.Entries[0].IsGit || l.Entries[1].Name != "code" {
		t.Fatalf("home: %+v", l)
	}
	l, err = s.Browse(filepath.Join(home, "code"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range l.Entries {
		names[e.Name] = e.IsGit
	}
	if len(names) != 2 || !names["app"] || names["notes"] {
		t.Fatalf("code: %+v (the escape symlink must be hidden)", l.Entries)
	}
	if l.Parent == nil || *l.Parent != home {
		t.Fatalf("parent %v", l.Parent)
	}

	for _, p := range []string{
		filepath.Join(home, ".."),
		filepath.Join(home, "code", "..", ".."),
		filepath.Join(home, "code", "escape"),
		outside,
		"/",
	} {
		if _, err := s.Browse(p); !errors.Is(err, ErrOutsideHome) {
			t.Errorf("Browse(%s) = %v, want ErrOutsideHome", p, err)
		}
	}
	if _, err := s.Browse(filepath.Join(home, "nope")); err == nil {
		t.Error("missing folder")
	}
}

func TestProjectID(t *testing.T) {
	for in, want := range map[string]string{
		"my app":        "my-app",
		"Jungle_Monkey": "jungle-monkey",
		"Café":          "cafe",
		"Додати":        "repo",
		"a-very-long-folder-name-that-keeps-going-and-going": "a-very-long-folder-name-that-keeps",
	} {
		if got := ProjectID(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
