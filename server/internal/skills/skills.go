// Package skills validates, packs and resolves Skill library entries: SKILL.md
// folders the scheduler writes into a task's workspace through the runner.
package skills

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Limits (SPEC-server, "Skill library").
const (
	MaxFiles      = 50
	MaxTotalBytes = 1 << 20
	SkillFileName = "SKILL.md"
)

var SlugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)

// ErrInvalid wraps every validation rejection.
var ErrInvalid = errors.New("invalid skill")

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Skill is the editable part of a skill. Body is the whole SKILL.md
// (frontmatter included); Files are the extra files next to it.
type Skill struct {
	Slug  string `json:"slug"`
	Body  string `json:"body"`
	Files []File `json:"files"`
}

// SecretsScanner returns findings ("file:line rule") for a set of files.
type SecretsScanner func(files []File) ([]string, error)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Validate checks slug, paths, sizes, text content and frontmatter, then runs
// the secrets scan (nil skips it). It returns the frontmatter name and description.
func Validate(s Skill, scan SecretsScanner) (name, description string, err error) {
	if !SlugRe.MatchString(s.Slug) {
		return "", "", invalid("slug must match %s", SlugRe)
	}
	all := AllFiles(s)
	if len(all) > MaxFiles {
		return "", "", invalid("at most %d files", MaxFiles)
	}
	total := 0
	seen := map[string]bool{}
	for _, f := range all {
		if err := CheckPath(f.Path); err != nil {
			return "", "", err
		}
		if seen[f.Path] {
			return "", "", invalid("duplicate file %s", f.Path)
		}
		seen[f.Path] = true
		if !utf8.ValidString(f.Content) || strings.ContainsRune(f.Content, 0) {
			return "", "", invalid("%s is not a text file", f.Path)
		}
		total += len(f.Content)
	}
	if total > MaxTotalBytes {
		return "", "", invalid("files exceed %d bytes in total", MaxTotalBytes)
	}
	name, description, _ = Frontmatter(s.Body)
	if name == "" || description == "" {
		return "", "", invalid("SKILL.md frontmatter needs name and description")
	}
	if scan != nil {
		findings, err := scan(all)
		if err != nil {
			return "", "", fmt.Errorf("secrets scan: %w", err)
		}
		if len(findings) > 0 {
			return "", "", invalid("possible secrets: %s", strings.Join(findings, ", "))
		}
	}
	return name, description, nil
}

// CheckPath rejects absolute paths, "..", backslashes and anything under .git.
func CheckPath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return invalid("bad path %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." || seg == "" || seg == ".git" {
			return invalid("bad path %q", p)
		}
	}
	return nil
}

// AllFiles returns SKILL.md followed by the extra files (SKILL.md in Files is ignored).
func AllFiles(s Skill) []File {
	out := []File{{Path: SkillFileName, Content: s.Body}}
	for _, f := range s.Files {
		if f.Path != SkillFileName {
			out = append(out, f)
		}
	}
	return out
}

var fmRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?(.*)\z`)

// Frontmatter reads name and description from SKILL.md's YAML frontmatter
// (single-line scalars, optionally quoted) and returns the markdown body.
func Frontmatter(md string) (name, description, body string) {
	m := fmRe.FindStringSubmatch(md)
	if m == nil {
		return "", "", md
	}
	for _, line := range strings.Split(m[1], "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), ":")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "name":
			name = v
		case "description":
			description = v
		}
	}
	return name, description, m[2]
}

// Export packs a skill as a zip of its folder: <slug>/SKILL.md, <slug>/….
func Export(s Skill) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range AllFiles(s) {
		w, err := zw.Create(s.Slug + "/" + f.Path)
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(w, f.Content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ImportZip reads a zip of one skill folder (SKILL.md at the root or in a
// single top-level folder, whose name becomes the slug unless slug is set).
func ImportZip(data []byte, slug string) (Skill, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Skill{}, invalid("not a zip file")
	}
	type entry struct{ name, content string }
	var entries []entry
	total := 0
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		if zf.Mode()&0o170000 == 0o120000 {
			return Skill{}, invalid("symlinks are not allowed (%s)", zf.Name)
		}
		if len(entries) >= MaxFiles+1 {
			return Skill{}, invalid("at most %d files", MaxFiles)
		}
		rc, err := zf.Open()
		if err != nil {
			return Skill{}, invalid("zip: %v", err)
		}
		b, err := io.ReadAll(io.LimitReader(rc, int64(MaxTotalBytes-total)+1))
		_ = rc.Close()
		if err != nil {
			return Skill{}, invalid("zip: %v", err)
		}
		total += len(b)
		if total > MaxTotalBytes {
			return Skill{}, invalid("files exceed %d bytes in total", MaxTotalBytes)
		}
		entries = append(entries, entry{zf.Name, string(b)})
	}
	// Strip one common top-level folder.
	prefix := ""
	for _, e := range entries {
		if e.name == SkillFileName {
			prefix = ""
			break
		}
		if dir, rest, ok := strings.Cut(e.name, "/"); ok && rest == SkillFileName {
			prefix = dir + "/"
		}
	}
	s := Skill{Slug: slug}
	if s.Slug == "" && prefix != "" {
		s.Slug = strings.TrimSuffix(prefix, "/")
	}
	found := false
	for _, e := range entries {
		if !strings.HasPrefix(e.name, prefix) {
			return Skill{}, invalid("files outside the skill folder (%s)", e.name)
		}
		p := strings.TrimPrefix(e.name, prefix)
		if p == SkillFileName {
			s.Body, found = e.content, true
			continue
		}
		if err := CheckPath(p); err != nil {
			return Skill{}, err
		}
		s.Files = append(s.Files, File{Path: p, Content: e.content})
	}
	if !found {
		return Skill{}, invalid("the zip has no SKILL.md")
	}
	if s.Slug == "" {
		name, _, _ := Frontmatter(s.Body)
		s.Slug = Slugify(name)
	}
	sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Path < s.Files[j].Path })
	return s, nil
}

// Slugify turns a skill name into a slug candidate.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 64 {
		s = strings.TrimRight(s[:64], "-")
	}
	return s
}

// Resolve returns project defaults + task additions − task removals,
// deduplicated and in a stable order.
func Resolve(projectDefaults, add, remove []string) []string {
	drop := map[string]bool{}
	for _, s := range remove {
		drop[s] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{projectDefaults, add} {
		for _, s := range list {
			if !drop[s] && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}
