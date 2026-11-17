package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// added is POST /api/v1/local/repos.
type added struct {
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Repository struct {
		ID         int64  `json:"id"`
		Path       string `json:"path"`
		BaseBranch string `json:"base_branch"`
	} `json:"repository"`
	Created bool `json:"created"`
}

// workTree returns the top level of the git work tree dir is in, or "".
func workTree(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	top := strings.TrimSpace(string(out))
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	return top
}

// addRepo adds path as a local repository of the running instance.
func addRepo(c *control, path string) (*added, error) {
	var out added
	if err := c.send(http.MethodPost, "/api/v1/local/repos", map[string]string{"path": path}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func printAdded(a *added) {
	verb := "Added"
	if !a.Created {
		verb = "Already added:"
	}
	fmt.Fprintf(os.Stderr, "  %s %s %s\n", green(verb), bold(a.Repository.Path),
		dim(fmt.Sprintf("(project %s, base branch %s)", a.Project.ID, a.Repository.BaseBranch)))
}

// runAdd is `jungle-monkey add [path]`: add a folder (default: the current
// one) as a local repository.
func runAdd(args []string) error {
	_, rest, err := parseFlags("add", args)
	if err != nil {
		return err
	}
	dir := "."
	if len(rest) > 0 {
		dir = rest[0]
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	top := workTree(abs)
	if top == "" {
		return fmt.Errorf("%s is not inside a git repository", abs)
	}
	c, err := running()
	if err != nil {
		return err
	}
	a, err := addRepo(c, top)
	if err != nil {
		return err
	}
	printAdded(a)
	fmt.Fprintf(os.Stderr, "  Open it: %s/projects/%s\n", c.base, a.Project.ID)
	return nil
}
