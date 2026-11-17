package skills

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Gitleaks scans skill files with the same gitleaks binary the publisher uses
// (`gitleaks dir` on a temporary copy). Findings are "file:line rule" only.
func Gitleaks(binary string) SecretsScanner {
	return func(files []File) ([]string, error) {
		bin, err := exec.LookPath(binary)
		if err != nil {
			return nil, fmt.Errorf("%s not installed", binary)
		}
		dir, err := os.MkdirTemp("", "fp-skill-scan-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		for _, f := range files {
			p := filepath.Join(dir, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(p, []byte(f.Content), 0o600); err != nil {
				return nil, err
			}
		}
		cmd := exec.Command(bin, "dir", "--no-banner", "--redact", "--exit-code", "3",
			"--report-format", "json", "--report-path", "-", dir)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err = cmd.Run()
		var exitErr *exec.ExitError
		leaks := errors.As(err, &exitErr) && exitErr.ExitCode() == 3
		if err != nil && !leaks {
			return nil, fmt.Errorf("gitleaks: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
		var report []struct {
			File      string `json:"File"`
			StartLine int    `json:"StartLine"`
			RuleID    string `json:"RuleID"`
		}
		if s := bytes.TrimSpace(stdout.Bytes()); len(s) > 0 {
			if err := json.Unmarshal(s, &report); err != nil {
				return nil, fmt.Errorf("gitleaks report: %w", err)
			}
		}
		out := make([]string, 0, len(report))
		for _, f := range report {
			rel, _ := filepath.Rel(dir, f.File)
			out = append(out, fmt.Sprintf("%s:%d %s", filepath.ToSlash(rel), f.StartLine, f.RuleID))
		}
		return out, nil
	}
}
