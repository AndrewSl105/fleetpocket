// Package artifacts turns the files a run left in .fleetpocket/out/ into
// task artifacts (docs/SPEC-server.md "Outputs"). Pure: the host reads the
// files (hosts.Host.CollectOutputs), this package validates them against
// manifest.json, the store saves them.
package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// Dir is the output directory, relative to the workspace.
const Dir = ".fleetpocket/out"

// Manifest is the file the agent writes next to its artifacts.
const Manifest = "manifest.json"

// Limits: per file and per run.
const (
	MaxFileBytes = 1 << 20
	MaxFiles     = 50
)

// Kinds a task_artifacts row may have.
var kinds = map[string]bool{"mermaid": true, "dbml": true, "d2": true, "plantuml": true, "svg": true, "markdown": true, "sql": true, "json": true}

// File is one file read from the output directory (Path relative to it).
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Artifact is one collected artifact.
type Artifact struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Confidence string   `json:"confidence"`
	Sources    []string `json:"sources"`
	Content    string   `json:"content"`
	SHA256     string   `json:"sha256"`
}

type manifest struct {
	Artifacts []struct {
		File       string   `json:"file"`
		Title      string   `json:"title"`
		Summary    string   `json:"summary"`
		Kind       string   `json:"kind"`
		Confidence string   `json:"confidence"`
		Sources    []string `json:"sources"`
	} `json:"artifacts"`
}

// KindOf infers a kind from the file extension ("" when unknown).
func KindOf(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown":
		return "markdown"
	case ".mmd", ".mermaid":
		return "mermaid"
	case ".json":
		return "json"
	case ".sql":
		return "sql"
	case ".dbml":
		return "dbml"
	case ".d2":
		return "d2"
	case ".puml", ".plantuml":
		return "plantuml"
	case ".svg":
		return "svg"
	}
	return ""
}

// Clean returns p as a safe relative path inside the output directory, or
// "" when it escapes it.
func Clean(p string) string {
	c := strings.TrimPrefix(strings.TrimSpace(p), "./")
	c = path.Clean(strings.TrimPrefix(c, Dir+"/"))
	if c == "." || c == "" || strings.HasPrefix(c, "/") || c == ".." || strings.HasPrefix(c, "../") {
		return ""
	}
	return c
}

// Collect builds the artifacts from the output files. Files listed in the
// manifest come first with its metadata; other files with a known kind are
// added with their name as title. Problems (bad manifest, unknown kinds, a
// listed file missing, size limits) are returned as warnings, never fatal.
func Collect(files []File) ([]Artifact, []string) {
	byPath := map[string]File{}
	for _, f := range files {
		if c := Clean(f.Path); c != "" {
			byPath[c] = f
		}
	}
	var out []Artifact
	var warn []string
	seen := map[string]bool{Manifest: true}
	add := func(p, kind, title, summary, confidence string, sources []string) {
		f, ok := byPath[p]
		switch {
		case !ok:
			warn = append(warn, fmt.Sprintf("%s is listed in the manifest but missing", p))
			return
		case len(f.Content) > MaxFileBytes:
			warn = append(warn, fmt.Sprintf("%s is larger than %d bytes; skipped", p, MaxFileBytes))
			return
		case len(out) >= MaxFiles:
			warn = append(warn, fmt.Sprintf("more than %d artifacts; %s skipped", MaxFiles, p))
			return
		}
		if kind == "" {
			kind = KindOf(p)
		}
		if !kinds[kind] {
			warn = append(warn, fmt.Sprintf("%s: unknown kind %q; skipped", p, kind))
			return
		}
		if confidence != "exact" {
			confidence = "derived"
		}
		if strings.TrimSpace(title) == "" {
			title = path.Base(p)
		}
		if sources == nil {
			sources = []string{}
		}
		sum := sha256.Sum256([]byte(f.Content))
		out = append(out, Artifact{Path: p, Kind: kind, Title: title, Summary: summary, Confidence: confidence, Sources: sources,
			Content: f.Content, SHA256: hex.EncodeToString(sum[:])})
	}
	if mf, ok := byPath[Manifest]; ok {
		var m manifest
		if err := json.Unmarshal([]byte(mf.Content), &m); err != nil {
			warn = append(warn, "manifest.json is not valid JSON: "+err.Error())
		}
		for _, a := range m.Artifacts {
			p := Clean(a.File)
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			add(p, a.Kind, a.Title, a.Summary, a.Confidence, a.Sources)
		}
	}
	for _, f := range files {
		p := Clean(f.Path)
		if p == "" || seen[p] || KindOf(p) == "" {
			continue
		}
		seen[p] = true
		add(p, "", "", "", "derived", nil)
	}
	return out, warn
}
