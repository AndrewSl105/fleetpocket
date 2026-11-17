package pipelines

import (
	"path"
	"strings"
)

// File ownership globs are slash paths relative to the repository root;
// "**" matches any number of segments, other segments use path.Match.

func cleanGlob(g string) string {
	return strings.Trim(path.Clean("/"+strings.TrimSpace(g)), "/")
}

// MatchGlob reports whether file matches the ownership glob g.
func MatchGlob(g, file string) bool {
	return matchSegs(strings.Split(cleanGlob(g), "/"), strings.Split(cleanGlob(file), "/"))
}

func matchSegs(g, f []string) bool {
	for len(g) > 0 {
		if g[0] == "**" {
			if len(g) == 1 {
				return true
			}
			for i := 0; i <= len(f); i++ {
				if matchSegs(g[1:], f[i:]) {
					return true
				}
			}
			return false
		}
		if len(f) == 0 {
			return false
		}
		if ok, err := path.Match(g[0], f[0]); err != nil || !ok {
			return false
		}
		g, f = g[1:], f[1:]
	}
	return len(f) == 0
}

// literalPrefix is the glob's leading segments without wildcards.
func literalPrefix(g string) []string {
	var out []string
	for _, s := range strings.Split(cleanGlob(g), "/") {
		if strings.ContainsAny(s, "*?[") {
			break
		}
		out = append(out, s)
	}
	return out
}

func hasMeta(g string) bool { return strings.ContainsAny(g, "*?[") }

// GlobsOverlap reports whether two ownership globs may match a common file.
// Conservative: two globs overlap when one's literal prefix is a segment
// prefix of the other's (so "web/**" and "web/src/a.ts" overlap, while
// "server/**" and "web/**" do not). Two literal paths overlap only when
// equal or one is a directory of the other.
func GlobsOverlap(a, b string) bool {
	if !hasMeta(a) && !hasMeta(b) {
		ca, cb := cleanGlob(a), cleanGlob(b)
		return ca == cb || strings.HasPrefix(ca, cb+"/") || strings.HasPrefix(cb, ca+"/")
	}
	if !hasMeta(a) {
		return literalVsGlob(a, b)
	}
	if !hasMeta(b) {
		return literalVsGlob(b, a)
	}
	return prefixRelated(literalPrefix(a), literalPrefix(b))
}

// literalVsGlob: the glob matches the literal path, or the literal path is a
// directory the glob's literal prefix lies under.
func literalVsGlob(lit, g string) bool {
	if MatchGlob(g, lit) {
		return true
	}
	l, p := strings.Split(cleanGlob(lit), "/"), literalPrefix(g)
	return len(l) <= len(p) && prefixRelated(l, p)
}

func prefixRelated(a, b []string) bool {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// OutsideOwnership returns the files no glob owns (the post-run check).
func OutsideOwnership(globs, files []string) []string {
	var out []string
	for _, f := range files {
		owned := false
		for _, g := range globs {
			if MatchGlob(g, f) {
				owned = true
				break
			}
		}
		if !owned {
			out = append(out, f)
		}
	}
	return out
}
