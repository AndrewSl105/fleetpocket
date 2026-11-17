// Package localrepos finds, browses and adds local repositories: git work
// trees on the owner's disk (MODE=local only, docs/SPEC-server.md
// "Repository sources").
package localrepos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// Scan limits (SPEC-server "Repository sources").
const (
	MaxDepth       = 3
	MaxSuggestions = 200
	ScanTimeout    = 2 * time.Second
	CacheTTL       = 60 * time.Second
)

// DefaultRoots are the folders, relative to the home directory, where
// people keep their code.
var DefaultRoots = []string{"code", "Projects", "projects", "dev", "src", "repos", filepath.Join("Documents", "GitHub"), "work"}

var (
	// ErrOutsideHome: the folder browser only shows the home directory.
	ErrOutsideHome = errors.New("only folders inside your home directory can be browsed")
	// ErrInvalid wraps validation failures (400).
	ErrInvalid = errors.New("invalid")
)

// Suggestion is a git work tree found by the scan.
type Suggestion struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Branch     string    `json:"branch"`
	Dirty      bool      `json:"dirty"`
	LastCommit time.Time `json:"last_commit"`
	// Added: the repository is already a local repository of some project.
	Added bool `json:"added"`
}

// Entry is one folder in the browser.
type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsGit bool   `json:"is_git"`
}

// Listing is a folder and its subfolders.
type Listing struct {
	Path    string  `json:"path"`
	Parent  *string `json:"parent"` // nil at the home directory
	Home    string  `json:"home"`
	IsGit   bool    `json:"is_git"`
	Entries []Entry `json:"entries"`
}

// Service implements the /local endpoints.
type Service struct {
	Store *store.Store
	// Home is the owner's home directory; Start the directory the CLI was
	// started in (scanned too; "" for none).
	Home  string
	Start string
	// Roots override DefaultRoots (tests).
	Roots []string
	// DataDir is the Jungle Monkey data directory, which cannot be added.
	DataDir string
	// CreateProject makes the project for a new repository (projects.Service).
	CreateProject func(ctx context.Context, id, name string) (*store.Project, error)
	Now           func() time.Time

	mu      sync.Mutex
	cached  []Suggestion
	cacheAt time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Suggestions returns git work trees under the usual code folders and the
// start directory, newest commit first: at most MaxSuggestions, found within
// ScanTimeout, cached for CacheTTL.
func (s *Service) Suggestions(ctx context.Context) ([]Suggestion, error) {
	s.mu.Lock()
	if s.cached != nil && s.now().Sub(s.cacheAt) < CacheTTL {
		out := append([]Suggestion(nil), s.cached...)
		s.mu.Unlock()
		return s.markAdded(ctx, out), nil
	}
	s.mu.Unlock()

	scanCtx, cancel := context.WithTimeout(ctx, ScanTimeout)
	defer cancel()
	paths := s.scan(scanCtx)
	out := make([]Suggestion, 0, len(paths))
	for _, p := range paths {
		// Branch and dirty state also count against the time budget.
		if scanCtx.Err() != nil {
			break
		}
		sg := Suggestion{Name: filepath.Base(p), Path: p}
		if st, err := workspace.StatusLocal(scanCtx, p); err == nil {
			sg.Branch, sg.Dirty, sg.LastCommit = st.Branch, st.Dirty, st.LastCommit
		}
		out = append(out, sg)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastCommit.After(out[j].LastCommit) })
	s.mu.Lock()
	s.cached, s.cacheAt = out, s.now()
	s.mu.Unlock()
	return s.markAdded(ctx, append([]Suggestion(nil), out...)), nil
}

func (s *Service) markAdded(ctx context.Context, list []Suggestion) []Suggestion {
	if s.Store == nil {
		return list
	}
	repos, err := s.Store.ListLocalRepositories(ctx)
	if err != nil {
		return list
	}
	added := map[string]bool{}
	for _, r := range repos {
		added[r.Path] = true
	}
	for i := range list {
		list[i].Added = added[list[i].Path]
	}
	return list
}

// scan walks the roots breadth-first to MaxDepth. A folder with a .git entry
// is a work tree and is not descended into. Hidden folders, node_modules and
// symlinks that lead back into a visited folder (loops) are skipped.
func (s *Service) scan(ctx context.Context) []string {
	roots := s.Roots
	if roots == nil {
		roots = DefaultRoots
	}
	type item struct {
		dir   string
		depth int
	}
	var queue []item
	for _, r := range roots {
		queue = append(queue, item{filepath.Join(s.Home, r), 0})
	}
	if s.Start != "" {
		queue = append(queue, item{s.Start, 0})
	}
	visited := map[string]bool{}
	found := map[string]bool{}
	var out []string
	for len(queue) > 0 && len(out) < MaxSuggestions && ctx.Err() == nil {
		it := queue[0]
		queue = queue[1:]
		real, err := filepath.EvalSymlinks(it.dir)
		if err != nil || visited[real] {
			continue
		}
		visited[real] = true
		if isWorkTree(real) {
			if !found[real] {
				found[real] = true
				out = append(out, real)
			}
			continue
		}
		if it.depth >= MaxDepth {
			continue
		}
		entries, err := os.ReadDir(real)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				continue
			}
			if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
				continue
			}
			queue = append(queue, item{filepath.Join(real, name), it.depth + 1})
		}
	}
	return out
}

// isWorkTree: dir has a .git folder (or a .git file: a linked worktree or
// submodule) with a HEAD.
func isWorkTree(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	if !st.IsDir() {
		return true
	}
	_, err = os.Stat(filepath.Join(dir, ".git", "HEAD"))
	return err == nil
}

// Browse lists the subfolders of path (the home directory when empty). The
// resolved path must stay inside the home directory: ".." and symlinks that
// lead out of it are refused.
func (s *Service) Browse(path string) (*Listing, error) {
	home, err := filepath.EvalSymlinks(s.Home)
	if err != nil {
		return nil, err
	}
	if path == "" {
		path = home
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(home, path)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s does not exist", store.ErrNotFound, path)
		}
		return nil, err
	}
	if !inside(real, home) {
		return nil, ErrOutsideHome
	}
	st, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a folder", ErrInvalid, path)
	}
	l := &Listing{Path: real, Home: home, IsGit: isWorkTree(real), Entries: []Entry{}}
	if real != home {
		parent := filepath.Dir(real)
		l.Parent = &parent
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		p := filepath.Join(real, name)
		// Follow symlinked folders only when they stay inside home.
		target, err := filepath.EvalSymlinks(p)
		if err != nil || !inside(target, home) {
			continue
		}
		if ts, err := os.Stat(target); err != nil || !ts.IsDir() {
			continue
		}
		l.Entries = append(l.Entries, Entry{Name: name, Path: p, IsGit: isWorkTree(target)})
	}
	sort.Slice(l.Entries, func(i, j int) bool { return strings.ToLower(l.Entries[i].Name) < strings.ToLower(l.Entries[j].Name) })
	return l, nil
}

func inside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Added is the result of Add.
type Added struct {
	Project    *store.Project    `json:"project"`
	Repository *store.Repository `json:"repository"`
	// Created: the repository was added now (false: it already was).
	Created bool `json:"created"`
}

// Add makes path a local repository. It is idempotent: a folder that is
// already a repository returns that one. Without projectID a new project
// named after the folder is created; with it the repository joins that
// project.
func (s *Service) Add(ctx context.Context, path, projectID string) (*Added, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: path is required", ErrInvalid)
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: path must be absolute", ErrInvalid)
	}
	info, err := workspace.InspectLocal(ctx, path, s.DataDir)
	if err != nil {
		if errors.Is(err, workspace.ErrNotRepo) || errors.Is(err, workspace.ErrRepoMissing) {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		return nil, err
	}
	if r, err := s.Store.FindLocalRepository(ctx, info.Path); err == nil {
		if projectID == "" || projectID == r.ProjectID {
			p, err := s.Store.GetProject(ctx, r.ProjectID)
			if err != nil {
				return nil, err
			}
			return &Added{Project: p, Repository: r}, nil
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	base := info.DefaultBranch
	if base == "" {
		base = "main"
	}
	if projectID == "" {
		p, err := s.newProject(ctx, info.Name)
		if err != nil {
			return nil, err
		}
		projectID = p.ID
	}
	repo, err := s.Store.AddRepository(ctx, store.Repository{
		ProjectID: projectID, FullName: info.Name, BaseBranch: base, Source: store.SourceLocal,
		Path: info.Path, RemoteURL: info.RemoteURL,
	})
	if errors.Is(err, store.ErrConflict) {
		return nil, fmt.Errorf("%w: the project already has a repository named %q", store.ErrConflict, info.Name)
	}
	if err != nil {
		return nil, err
	}
	p, err := s.Store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return &Added{Project: p, Repository: repo, Created: true}, nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// ProjectID turns a folder name into a project id: a lowercase slug of at
// most 40 characters ("repo" when nothing is left).
func ProjectID(name string) string {
	id := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(workspace.Slug(name)), "-"), "-")
	if len(id) > 36 {
		id = strings.Trim(id[:36], "-")
	}
	if id == "" {
		id = "repo"
	}
	return id
}

// newProject creates a project for a folder, suffixing -2, -3… when the id
// is taken.
func (s *Service) newProject(ctx context.Context, name string) (*store.Project, error) {
	id := ProjectID(name)
	for n := 1; n < 100; n++ {
		try := id
		if n > 1 {
			try = fmt.Sprintf("%s-%d", id, n)
		}
		p, err := s.CreateProject(ctx, try, name)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		return p, err
	}
	return nil, fmt.Errorf("%w: no free project id for %q", store.ErrConflict, name)
}

// Locate points a local repository at its new folder after it was moved.
func (s *Service) Locate(ctx context.Context, repoID int64, path string) (*store.Repository, error) {
	r, err := s.Store.GetRepository(ctx, repoID)
	if err != nil {
		return nil, err
	}
	if !r.Local() {
		return nil, fmt.Errorf("%w: only local repositories have a folder", ErrInvalid)
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: path must be absolute", ErrInvalid)
	}
	info, err := workspace.InspectLocal(ctx, path, s.DataDir)
	if err != nil {
		if errors.Is(err, workspace.ErrNotRepo) || errors.Is(err, workspace.ErrRepoMissing) {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		return nil, err
	}
	return s.Store.UpdateRepository(ctx, r.ProjectID, r.ID, store.RepoUpdate{Path: &info.Path})
}

// Status is a local repository's folder state for the project page.
type Status struct {
	RepositoryID int64  `json:"repository_id"`
	Path         string `json:"path"`
	Missing      bool   `json:"missing"`
	Branch       string `json:"branch"`
	Detached     bool   `json:"detached"`
	Dirty        bool   `json:"dirty"`
}

// RepoStatus reads the folder's branch and dirty state.
func (s *Service) RepoStatus(ctx context.Context, repoID int64) (*Status, error) {
	r, err := s.Store.GetRepository(ctx, repoID)
	if err != nil {
		return nil, err
	}
	if !r.Local() {
		return nil, fmt.Errorf("%w: not a local repository", ErrInvalid)
	}
	st, err := workspace.StatusLocal(ctx, r.Path)
	if err != nil {
		return nil, err
	}
	return &Status{RepositoryID: r.ID, Path: r.Path, Missing: !st.Exists, Branch: st.Branch, Detached: st.Detached, Dirty: st.Dirty}, nil
}
