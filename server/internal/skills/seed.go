package skills

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed seed
var seedFS embed.FS

// Seed returns the built-in diagram skills, written once on first run
// (deletable, not enabled by default).
func Seed() []Skill {
	dirs, _ := fs.ReadDir(seedFS, "seed")
	var out []Skill
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		s := Skill{Slug: d.Name()}
		_ = fs.WalkDir(seedFS, "seed/"+d.Name(), func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			b, err := seedFS.ReadFile(p)
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(p, "seed/"+d.Name()+"/")
			if rel == SkillFileName {
				s.Body = string(b)
			} else {
				s.Files = append(s.Files, File{Path: rel, Content: string(b)})
			}
			return nil
		})
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}
