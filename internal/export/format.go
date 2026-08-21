package export

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var SupportedFormats = []string{"docusaurus", "mkdocs", "notion", "confluence"}

var reConfluenceLink = regexp.MustCompile("\\[([^\\]]+)\\]\\(([^)]+)\\)")

type formatPage struct {
	Title       string
	Slug        string
	Section     string
	ContentPath string
}
func ExportToFormat(workDir, format, outDir string, opts ExportOptions) error {
	if format == "" {
		return fmt.Errorf("format required: one of %s", strings.Join(SupportedFormats, ", "))
	}
	format = strings.ToLower(strings.TrimSpace(format))
	root := filepath.Join(workDir, ".wikify")
	wikiPath := filepath.Join(root, "meta", "wiki.json")
	data, err := os.ReadFile(wikiPath)
	if err != nil {
		return fmt.Errorf("read %s: %w (run generate first)", wikiPath, err)
	}
	var raw struct {
		Pages []struct {
			Title string `json:"title"` 
			Slug string `json:"slug"` 
			Section string `json:"section"` 
			ContentPath string `json:"content_path"` 
		} `json:"pages"` 
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse wiki.json: %w", err)
	}
	if len(raw.Pages) == 0 {
		return fmt.Errorf("wiki.json has no pages")
	}
	contentDir := filepath.Join(root, "content")
	contents := map[string]string{}
	for _, p := range raw.Pages {
		rel := p.ContentPath
		if rel == "" {
			rel = p.Title + ".md"
		}
		rel = strings.TrimPrefix(filepath.ToSlash(rel), "content/")
		b, err := os.ReadFile(filepath.Join(contentDir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		contents[p.Slug] = string(b)
	}
	if len(contents) == 0 {
		return fmt.Errorf("no content markdown found under %s", contentDir)
	}
	var pages []formatPage
	for _, p := range raw.Pages {
		if _, ok := contents[p.Slug]; !ok {
			continue
		}
		pages = append(pages, formatPage{p.Title, p.Slug, p.Section, p.ContentPath})
	}
	if outDir == "" {
		outDir = filepath.Join(workDir, ".wikify", "export", format)
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("create outDir: %w", err)
	}
	switch format {
	case "docusaurus":
		return exportDocusaurus(pages, contents, outDir)
	case "mkdocs":
		return exportMkDocs(pages, contents, outDir)
	case "notion":
		return exportNotion(pages, contents, outDir)
	case "confluence":
		return exportConfluence(pages, contents, outDir)
	default:
		return fmt.Errorf("unsupported format %q: choose one of %s", format, strings.Join(SupportedFormats, ", "))
	}
}
func exportDocusaurus(pages []formatPage, contents map[string]string, outDir string) error {
	docsDir := filepath.Join(outDir, "docs")
	for _, p := range pages {
		body := contents[p.Slug]
		if !strings.HasPrefix(strings.TrimSpace(body), "---") {
			fm := fmt.Sprintf("---\nid: %s\ntitle: %q\nsidebar_label: %q\n---\n\n", p.Slug, p.Title, p.Title)
			body = fm + body
		}
		rel := p.ContentPath
		if rel == "" {
			rel = p.Title + ".md"
		}
		rel = strings.TrimPrefix(filepath.ToSlash(rel), "content/")
		dest := filepath.Join(docsDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(body), 0644); err != nil {
			return err
		}
	}
	sections := map[string][]formatPage{}
	for _, p := range pages {
		sec := p.Section
		if sec == "" {
			sec = "general"
		}
		sections[sec] = append(sections[sec], p)
	}
	for sec := range sections {
		catPath := filepath.Join(docsDir, sec, "_category_.json")
		if _, err := os.Stat(catPath); err == nil {
			continue
		}
		cat := fmt.Sprintf("{\n  \"label\": %q,\n  \"position\": 1,\n  \"link\": {\"type\": \"generated-index\"}\n}\n", sec)
		_ = os.WriteFile(catPath, []byte(cat), 0644)
	}
	readme := "# Docusaurus export\n\nCopy docs/ into your Docusaurus docs/ folder.\n"
	_ = os.WriteFile(filepath.Join(outDir, "README.md"), []byte(readme), 0644)
	return nil
}

func exportMkDocs(pages []formatPage, contents map[string]string, outDir string) error {
	docsDir := filepath.Join(outDir, "docs")
	for _, p := range pages {
		rel := p.ContentPath
		if rel == "" {
			rel = p.Title + ".md"
		}
		rel = strings.TrimPrefix(filepath.ToSlash(rel), "content/")
		if err := os.MkdirAll(filepath.Dir(filepath.Join(docsDir, filepath.FromSlash(rel))), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(docsDir, filepath.FromSlash(rel)), []byte(contents[p.Slug]), 0644); err != nil {
			return err
		}
	}
	sections := map[string][]formatPage{}
	var order []string
	for _, p := range pages {
		sec := p.Section
		if sec == "" {
			sec = "General"
		}
		if _, ok := sections[sec]; !ok {
			order = append(order, sec)
		}
		sections[sec] = append(sections[sec], p)
	}
	sort.Strings(order)
	var b strings.Builder
	b.WriteString("site_name: Wiki\n")
	b.WriteString("theme:\n  name: material\n")
	b.WriteString("nav:\n")
	for _, sec := range order {
		b.WriteString(fmt.Sprintf("  - %s:\n", sec))
		for _, p := range sections[sec] {
			rel := p.ContentPath
			if rel == "" {
				rel = p.Title + ".md"
			}
			rel = strings.TrimPrefix(filepath.ToSlash(rel), "content/")
			b.WriteString(fmt.Sprintf("    - %s: %s\n", p.Title, rel))
		}
	}
	return os.WriteFile(filepath.Join(outDir, "mkdocs.yml"), []byte(b.String()), 0644)
}

func exportNotion(pages []formatPage, contents map[string]string, outDir string) error {
	docsDir := filepath.Join(outDir, "notion")
	for _, p := range pages {
		rel := p.ContentPath
		if rel == "" {
			rel = p.Title + ".md"
		}
		rel = strings.TrimPrefix(filepath.ToSlash(rel), "content/")
	dest := filepath.Join(docsDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		body := contents[p.Slug]
		if !strings.HasPrefix(strings.TrimSpace(body), "---") {
			body = fmt.Sprintf("---\ntitle: %q\n---\n\n", p.Title) + body
		}
		if err := os.WriteFile(dest, []byte(body), 0644); err != nil {
			return err
		}
	}
	readme := "# Notion export\n\n1. In Notion, use Import -> Markdown, select the notion/ folder.\n"
	return os.WriteFile(filepath.Join(outDir, "README.md"), []byte(readme), 0644)
}

func exportConfluence(pages []formatPage, contents map[string]string, outDir string) error {
	docsDir := filepath.Join(outDir, "confluence")
	for _, p := range pages {
		rel := p.ContentPath
		if rel == "" {
			rel = p.Title + ".md"
		}
		rel = strings.TrimPrefix(filepath.ToSlash(rel), "content/")
	rel = strings.TrimSuffix(rel, ".md") + ".wiki"
	dest := filepath.Join(docsDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		wiki := mdToConfluenceWiki(contents[p.Slug])
		if err := os.WriteFile(dest, []byte(wiki), 0644); err != nil {
			return err
		}
	}
	readme := "# Confluence export\n\nEach .wiki file uses Wiki Markup. In Confluence: Create -> Wiki Markup.\n"
	return os.WriteFile(filepath.Join(outDir, "README.md"), []byte(readme), 0644)
}

func mdToConfluenceWiki(md string) string {
	lines := strings.Split(md, "\n")
	for i, ln := range lines {
		trim := strings.TrimSpace(ln)
		if strings.HasPrefix(trim, "```") {
			if strings.HasPrefix(trim, "```mermaid") {
				lines[i] = "{code:language=text}"
			} else if trim == "```" {
				lines[i] = "{code}"
			} else {
				lang := strings.TrimPrefix(trim, "```")
				if lang == "" { lang = "text" }
				lines[i] = "{code:language=" + lang + "}"
			}
			continue
		}
		if strings.HasPrefix(trim, "# ") {
			lines[i] = "h1. " + strings.TrimSpace(strings.TrimPrefix(trim, "# "))
		} else if strings.HasPrefix(trim, "## ") {
			lines[i] = "h2. " + strings.TrimSpace(strings.TrimPrefix(trim, "## "))
		} else if strings.HasPrefix(trim, "### ") {
			lines[i] = "h3. " + strings.TrimSpace(strings.TrimPrefix(trim, "### "))
		} else {
			lines[i] = reConfluenceLink.ReplaceAllString(ln, "[$1|$2]")
		}
	}
	return strings.Join(lines, "\n")
}