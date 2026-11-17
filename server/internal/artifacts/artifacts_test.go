package artifacts

import (
	"strings"
	"testing"
)

func TestCollect(t *testing.T) {
	files := []File{
		{Path: "manifest.json", Content: `{"artifacts":[{"file":"er.mmd","title":"Schema","kind":"mermaid","confidence":"exact","sources":["db.sql"]},{"file":"gone.md"},{"file":"../etc/passwd"}]}`},
		{Path: "er.mmd", Content: "erDiagram"},
		{Path: "report.md", Content: "# Report"},
		{Path: "plan.json", Content: `{"summary":"x"}`},
		{Path: "notes.bin", Content: "x"},
		{Path: "big.md", Content: strings.Repeat("a", MaxFileBytes+1)},
	}
	got, warn := Collect(files)
	paths := []string{}
	for _, a := range got {
		paths = append(paths, a.Path+":"+a.Kind)
	}
	if strings.Join(paths, ",") != "er.mmd:mermaid,report.md:markdown,plan.json:json" {
		t.Fatalf("artifacts %v", paths)
	}
	if got[0].Title != "Schema" || got[0].Confidence != "exact" || got[0].Sources[0] != "db.sql" || len(got[0].SHA256) != 64 {
		t.Errorf("manifest metadata lost: %+v", got[0])
	}
	if got[1].Title != "report.md" || got[1].Confidence != "derived" {
		t.Errorf("unlisted file: %+v", got[1])
	}
	if len(warn) != 2 || !strings.Contains(warn[0], "gone.md") || !strings.Contains(warn[1], "big.md") {
		t.Errorf("warnings %v", warn)
	}
	if _, warn := Collect([]File{{Path: "manifest.json", Content: "{"}}); len(warn) != 1 {
		t.Errorf("bad manifest: %v", warn)
	}
	for _, p := range []string{"../x", "/etc/x", ".fleetpocket/out/../../x"} {
		if Clean(p) != "" {
			t.Errorf("Clean(%q) = %q", p, Clean(p))
		}
	}
	if Clean(".fleetpocket/out/a/b.md") != "a/b.md" {
		t.Error("prefix not stripped")
	}
}
