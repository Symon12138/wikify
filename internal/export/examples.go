package export

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var testFilePattern = regexp.MustCompile("(?i)(_test\\.go$|Test\\.java$|Tests\\.java$|Test\\.kt$|\\.test\\.(ts|js)$|\\.spec\\.(ts|js)$)")

func extractTestSnippets(workDir string, dependentFiles []string) []string {
	if len(dependentFiles) == 0 {
		return nil
	}
	bases := map[string]bool{}
	for _, f := range dependentFiles {
		base := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		for _, suf := range []string{"Controller", "Service", "Dao", "Repository", "Entity"} {
			if strings.HasSuffix(base, suf) && len(base) > len(suf) {
				base = base[:len(base)-len(suf)]
				break
			}
		}
		if base != "" {
			bases[strings.ToLower(base)] = true
		}
	}
	if len(bases) == 0 {
		return nil
	}
	var snippets []string
	filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == ".wikify" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if !testFilePattern.MatchString(path) {
			return nil
		}
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
		matched := false
		for b := range bases {
			if strings.Contains(base, b) || strings.Contains(b, strings.TrimSuffix(base, "test")) {
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 200*1024 {
			return nil
		}
		snippet := extractFirstTestFunc(string(data))
		if snippet != "" {
			rel, _ := filepath.Rel(workDir, path)
			snippets = append(snippets, fmt.Sprintf("**%s**\n\n```\n%s\n```", filepath.ToSlash(rel), snippet))
		}
		if len(snippets) >= 2 {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(snippets)
	if len(snippets) > 2 {
		snippets = snippets[:2]
	}
	return snippets
}

func extractFirstTestFunc(content string) string {
	reGo := regexp.MustCompile("(?m)^func (Test\\w+)\\([^)]*\\)\\s*\\{")
	if loc := reGo.FindStringIndex(content); loc != nil {
		return truncateSnippet(extractBlock(content, loc[1]-1), 30)
	}
	reJava := regexp.MustCompile("(?m)(@Test[^\\n]*\\n)?\\s*(public\\s+)?(void|fun)\\s+test\\w+\\s*\\([^)]*\\)\\s*\\{")
	if loc := reJava.FindStringIndex(content); loc != nil {
		return truncateSnippet(extractBlock(content, loc[1]-1), 30)
	}
	return ""
}

func extractBlock(s string, openIdx int) string {
	depth := 0
	start := -1
	for i := openIdx; i < len(s); i++ {
		if s[i] == '{' {
			if start == -1 {
				start = i
			}
			depth++
		} else if s[i] == '}' {
			depth--
			if depth == 0 && start >= 0 {
				return strings.TrimSpace(s[start : i+1])
			}
		}
	}
	return ""
}

func truncateSnippet(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		s = strings.Join(lines, "\n") + "\n// ... truncated"
	}
	if len(s) > 3000 {
		s = s[:3000] + "\n// ... truncated"
	}
	return s
}
