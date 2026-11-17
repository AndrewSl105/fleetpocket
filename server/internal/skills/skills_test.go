package skills

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const body = "---\nname: Demo\ndescription: Does demo things\n---\nBody.\n"

func TestValidatePaths(t *testing.T) {
	for _, p := range []string{"/abs", "../x", "a/../b", `a\b`, ".git/config", "a/.git/x", "", "a//b"} {
		_, _, err := Validate(Skill{Slug: "demo", Body: body, Files: []File{{Path: p, Content: "x"}}}, nil)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", p, err)
		}
	}
	name, desc, err := Validate(Skill{Slug: "demo", Body: body, Files: []File{{Path: "ref/a.md", Content: "x"}}}, nil)
	if err != nil || name != "Demo" || desc != "Does demo things" {
		t.Fatal(name, desc, err)
	}
}

func TestValidateLimitsAndFrontmatter(t *testing.T) {
	cases := map[string]Skill{
		"slug":        {Slug: "Bad_Slug", Body: body},
		"frontmatter": {Slug: "demo", Body: "---\nname: x\n---\n"},
		"no fm":       {Slug: "demo", Body: "just text"},
		"size":        {Slug: "demo", Body: body, Files: []File{{Path: "big.md", Content: strings.Repeat("a", MaxTotalBytes)}}},
		"binary":      {Slug: "demo", Body: body, Files: []File{{Path: "b.bin", Content: "\x00\x01"}}},
	}
	many := Skill{Slug: "demo", Body: body}
	for i := 0; i < MaxFiles; i++ {
		many.Files = append(many.Files, File{Path: "f" + strings.Repeat("x", i) + ".md", Content: "x"})
	}
	cases["count"] = many
	for name, s := range cases {
		if _, _, err := Validate(s, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestValidateSecrets(t *testing.T) {
	scan := func(files []File) ([]string, error) {
		for _, f := range files {
			if strings.Contains(f.Content, "ghp_") {
				return []string{f.Path + ":1 github-pat"}, nil
			}
		}
		return nil, nil
	}
	_, _, err := Validate(Skill{Slug: "demo", Body: body, Files: []File{{Path: "k.md", Content: "ghp_x"}}}, scan)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "k.md:1") {
		t.Fatal(err)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	in := Skill{Slug: "demo", Body: body, Files: []File{{Path: "a.md", Content: "A"}, {Path: "ref/b.md", Content: "B"}}}
	zip, err := Export(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ImportZip(zip, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("%+v != %+v", in, out)
	}
	if _, err := ImportZip([]byte("nope"), ""); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestResolve(t *testing.T) {
	got := Resolve([]string{"a", "b"}, []string{"c", "a"}, []string{"b"})
	if !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatal(got)
	}
	if Resolve(nil, nil, nil) != nil {
		t.Fatal("empty")
	}
}

func TestSeedSkillsAreValid(t *testing.T) {
	seed := Seed()
	var slugs []string
	for _, s := range seed {
		slugs = append(slugs, s.Slug)
		if _, _, err := Validate(s, nil); err != nil {
			t.Errorf("%s: %v", s.Slug, err)
		}
		if !strings.Contains(s.Body, "read-only") || !strings.Contains(s.Body, "40 nodes") {
			t.Errorf("%s misses the common rules", s.Slug)
		}
	}
	if !reflect.DeepEqual(slugs, []string{"db-schema", "infra-map", "sequence-flow", "system-design-c4"}) {
		t.Fatal(slugs)
	}
}
