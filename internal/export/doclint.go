package export

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Symon12138/wikify/internal/pathsafe"
)

var reWikiLink = regexp.MustCompile("\\[[^\\]]+\\]\\(([^)]+\\.md[^)]*)\\)")

type DocLintIssue struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func LintWiki(workDir string) ([]DocLintIssue, error) {
	root := filepath.Join(workDir, ".wikify")
	contentDir := filepath.Join(root, "content")
	metaPath := filepath.Join(root, "meta", "wiki.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w (run generate first)", metaPath, err)
	}
	type wikiFile struct {
		Pages []struct {
			Title       string `json:"title"`
			Slug        string `json:"slug"`
			ContentPath string `json:"content_path"`
		} `json:"pages"`
	}
	var wf wikiFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parse wiki.json: %w", err)
	}
	var issues []DocLintIssue
	for _, p := range wf.Pages {
		rel := p.ContentPath
		if rel == "" {
			rel = p.Title + ".md"
		}
		safe, serr := pathsafe.Rel(strings.TrimPrefix(filepath.ToSlash(rel), "content/"))
		if serr != nil {
			issues = append(issues, DocLintIssue{Slug: p.Slug, Title: p.Title, Kind: "unsafe-path", Message: fmt.Sprintf("content_path escapes content dir: %q", p.ContentPath)})
			continue
		}
		rel = safe
		body, err := os.ReadFile(filepath.Join(contentDir, filepath.FromSlash(rel)))
		if err != nil {
			issues = append(issues, DocLintIssue{Slug: p.Slug, Title: p.Title, Kind: "missing-file", Message: fmt.Sprintf("content file missing: %s", rel)})
			continue
		}
		s := string(body)
		if len([]rune(strings.TrimSpace(s))) < 200 {
			issues = append(issues, DocLintIssue{Slug: p.Slug, Title: p.Title, Kind: "thin-body", Message: "body too short (<200 runes)"})
		}
		if _, hard, _ := LintPageBody(s); len(hard) > 0 {
			for _, h := range hard {
				issues = append(issues, DocLintIssue{Slug: p.Slug, Title: p.Title, Kind: "lint-hard", Message: h})
			}
		}
		for _, m := range reWikiLink.FindAllStringSubmatch(s, -1) {
			link := m[1]
			if idx := strings.Index(link, "#"); idx >= 0 {
				link = link[:idx]
			}
			if idx := strings.Index(link, "?"); idx >= 0 {
				link = link[:idx]
			}
			if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") || strings.HasPrefix(link, "//") {
				continue
			}
			dir := filepath.Dir(filepath.Join(contentDir, filepath.FromSlash(rel)))
			target := filepath.Join(dir, filepath.FromSlash(link))
			if _, err := os.Stat(target); err != nil {
				issues = append(issues, DocLintIssue{Slug: p.Slug, Title: p.Title, Kind: "broken-wiki-link", Message: fmt.Sprintf("broken link %q -> %s", m[0], link)})
			}
		}
	}
	return issues, nil
}