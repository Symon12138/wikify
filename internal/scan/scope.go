package scan

import (
	"path/filepath"
	"strings"
)

// InScope reports whether rel (slash-separated, repo-relative) is allowed by
// include/exclude patterns from wiki_plan.yaml scope.
//
// Rules:
//   - empty include + empty exclude → allow all
//   - exclude wins when both match
//   - non-empty include → path must match at least one include
//
// Pattern syntax (minimal, portable):
//   - "src" or "src/"          → path == src or under src/
//   - "src/**"                 → same as prefix
//   - "**/Foo.java"            → any depth basename/suffix match
//   - "a/*/b"                  → filepath.Match style
func InScope(rel string, include, exclude []string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || rel == "." {
		return false
	}
	for _, pat := range exclude {
		if MatchPath(pat, rel) {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for _, pat := range include {
		if MatchPath(pat, rel) {
			return true
		}
	}
	return false
}

// MatchPath matches one scope pattern against a repo-relative path.
func MatchPath(pattern, path string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	path = filepath.ToSlash(strings.TrimSpace(path))
	if pattern == "" || path == "" {
		return false
	}
	// Strip leading ./
	pattern = strings.TrimPrefix(pattern, "./")
	path = strings.TrimPrefix(path, "./")

	// "**/bar", "**/pkg/**", "**/Foo.java" — handle before trailing /**.
	if strings.HasPrefix(pattern, "**/") {
		rest := strings.TrimPrefix(pattern, "**/")
		if strings.HasSuffix(rest, "/**") {
			mid := strings.TrimSuffix(rest, "/**")
			return path == mid ||
				strings.HasPrefix(path, mid+"/") ||
				strings.HasSuffix(path, "/"+mid) ||
				strings.Contains(path, "/"+mid+"/")
		}
		// suffix / basename
		if path == rest || strings.HasSuffix(path, "/"+rest) {
			return true
		}
		// rest may itself be a glob
		if strings.ContainsAny(rest, "*?[") {
			if ok, _ := filepath.Match(rest, path); ok {
				return true
			}
			if ok, _ := filepath.Match(rest, filepath.Base(path)); ok {
				return true
			}
			parts := strings.Split(path, "/")
			for i := range parts {
				sub := strings.Join(parts[i:], "/")
				if ok, _ := filepath.Match(rest, sub); ok {
					return true
				}
			}
		}
		return false
	}

	// "foo/**" or "foo/" → directory prefix
	if strings.HasSuffix(pattern, "/**") {
		pref := strings.TrimSuffix(pattern, "/**")
		return path == pref || strings.HasPrefix(path, pref+"/")
	}
	if strings.HasSuffix(pattern, "/") {
		pref := strings.TrimSuffix(pattern, "/")
		return path == pref || strings.HasPrefix(path, pref+"/")
	}

	// Full-path glob
	if strings.ContainsAny(pattern, "*?[") {
		if ok, _ := filepath.Match(pattern, path); ok {
			return true
		}
		if ok, _ := filepath.Match(pattern, filepath.Base(path)); ok {
			return true
		}
		// Try matching pattern against each suffix of the path (a/*/c vs deep/a/x/c).
		parts := strings.Split(path, "/")
		for i := range parts {
			sub := strings.Join(parts[i:], "/")
			if ok, _ := filepath.Match(pattern, sub); ok {
				return true
			}
		}
		return false
	}

	// Literal: exact or directory prefix
	return path == pattern || strings.HasPrefix(path, pattern+"/")
}

// ApplyScope filters Files by include/exclude and rebuilds Modules/Languages/Summary.
// No-op when both lists are empty or model is nil.
func (m *Model) ApplyScope(include, exclude []string) {
	if m == nil {
		return
	}
	include = cleanPatterns(include)
	exclude = cleanPatterns(exclude)
	if len(include) == 0 && len(exclude) == 0 {
		return
	}
	kept := make([]FileInfo, 0, len(m.Files))
	langs := map[string]int{}
	for _, f := range m.Files {
		if !InScope(f.RelativePath, include, exclude) {
			continue
		}
		kept = append(kept, f)
		if f.Ext != "" {
			langs[f.Ext]++
		}
	}
	m.Files = kept
	m.Languages = langs
	m.ManifestRoots = deriveManifestRoots(kept)
	m.Modules = deriveModules(kept, m.ManifestRoots)
	m.Summary = buildSummary(m.Name, kept, langs)
}

func cleanPatterns(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
