// Package browse serves generated wiki docs via a built-in Go HTTP server.
// No Node.js or npm required.
//
// Supports:
//   - .wikify/drafts — in-progress flat {slug}.md
//   - .wikify/content + .wikify/meta — final multi-level wiki (single deliverable)
package browse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
)

// unicodeHeadingIDs is a goldmark parser.IDs implementation that preserves
// CJK and non-ASCII characters in heading anchors. The default goldmark
// AutoHeadingID strips all non-ASCII bytes, turning Chinese headings into
// generic "heading-N" IDs that never match [text](#text) anchors in LLM TOCs.
type unicodeHeadingIDs struct {
	seen map[string]int
}

func newUnicodeHeadingIDs() parser.IDs {
	return &unicodeHeadingIDs{seen: make(map[string]int)}
}

func (ids *unicodeHeadingIDs) Generate(value []byte, kind ast.NodeKind) []byte {
	s := strings.ToLower(string(value))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ' || r == '\t' || r == '_':
			b.WriteRune('-')
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-':
			b.WriteRune(r)
		case r > 127: // keep non-ASCII (CJK, accented, etc.) — valid in HTML5 id
			b.WriteRune(r)
		// otherwise: skip ASCII punctuation/symbols
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = "heading"
	}
	if _, exists := ids.seen[id]; exists {
		ids.seen[id]++
		id = fmt.Sprintf("%s-%d", id, ids.seen[id])
	}
	ids.seen[id] = 0
	return []byte(id)
}

func (ids *unicodeHeadingIDs) Put(value []byte) {
	s := string(value)
	if _, ok := ids.seen[s]; !ok {
		ids.seen[s] = 0
	}
}

// ── data types ─────────────────────────────────────────────────────────────────

type pageInfo struct {
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Section     string `json:"section,omitempty"`
	ContentPath string `json:"content_path,omitempty"` // under content/ when serving export layout
	Track       string `json:"track,omitempty"`        // foundation | business | technical
	Index       int    `json:"-"`                     // 1-based global menu order
	CatIndex    int    `json:"-"`                     // 1-based index within category
}

type wikiData struct {
	Pages []pageInfo `json:"pages"`
}

type navCategory struct {
	Label string
	Index int // 1-based category order
	Items []pageInfo
	Track string // dominant track for badge, optional
}

type pageData struct {
	ProjectName string
	Title       string
	Section     string
	ActiveSlug  string
	Categories  []navCategory
	Content     template.HTML
	StaticLinks bool
	SourceLabel string
}

// docSource describes where markdown bodies and the catalog come from.
type docSource struct {
	// catalogDir holds wiki.json (wikify) or browse-index.json parent meta dir.
	catalogDir string
	// bodyDir is where page markdown lives: wikify version dir, or exported content dir.
	bodyDir string
	// exported true when bodyDir is .wikify/.../content
	exported bool
	isDraft  bool
	label    string // shown in UI badge
}

// ── public entry point ─────────────────────────────────────────────────────────

// Run starts the documentation server (or builds a static site if build=true).
// siteDir is intentionally unused (kept for CLI compat).
func Run(workDir, _ string, port int, openBrowser, build bool) error {
	src, err := resolveSource(workDir)
	if err != nil {
		return err
	}

	wiki, err := readWiki(src)
	if err != nil {
		return err
	}

	if src.isDraft {
		fmt.Println("⚠️  正在预览草稿（尚未提交的生成内容）")
	}
	if src.exported {
		fmt.Printf("📂 来源: exported content (%s)\n", src.bodyDir)
	} else {
		fmt.Printf("📂 来源: %s\n", src.label)
	}

	projectName := filepath.Base(workDir)
	cats := buildCategories(wiki.Pages)

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Table, extension.Strikethrough, extension.TaskList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(ghtml.WithUnsafe()),
	)

	tmpl, err := template.New("page").Parse(htmlTmpl)
	if err != nil {
		return fmt.Errorf("解析 HTML 模板失败: %w", err)
	}

	if build {
		return buildStatic(workDir, src, projectName, cats, wiki.Pages, md, tmpl)
	}

	return serve(src, projectName, cats, wiki, md, tmpl, port, openBrowser)
}

// resolveSource finds the best docs source to serve.
// Priority:
//  1. In-progress .wikify/drafts (so resume preview stays correct)
//  2. Final .wikify/{content,meta} single deliverable
//  3. Legacy .wikify/wiki/{drafts,versions} (read-only, older layouts)
func resolveSource(workDir string) (*docSource, error) {
	root := filepath.Join(workDir, ".wikify")
	drafts := filepath.Join(root, "drafts")
	hasDraft := fileExists(filepath.Join(drafts, "wiki.json"))

	// Mid-generation: prefer drafts only when page bodies exist.
	// Catalog-only drafts (wiki.json without *.md) must not shadow published content.
	if hasDraft && dirHasMarkdown(drafts) {
		return &docSource{
			catalogDir: drafts,
			bodyDir:    drafts,
			exported:   false,
			isDraft:    true,
			label:      "wikify drafts",
		}, nil
	}

	if q := findExportSource(workDir); q != nil {
		return q, nil
	}
	// Fallback: drafts catalog with no bodies (shell preview only).
	if hasDraft {
		return &docSource{
			catalogDir: drafts,
			bodyDir:    drafts,
			exported:   false,
			isDraft:    true,
			label:      "wikify drafts",
		}, nil
	}

	// Legacy fallback: .wikify/wiki/drafts or versions from pre-unify layouts.
	legacyWiki := filepath.Join(root, "wiki")
	legacyDrafts := filepath.Join(legacyWiki, "drafts")
	if fileExists(filepath.Join(legacyDrafts, "wiki.json")) {
		return &docSource{
			catalogDir: legacyDrafts,
			bodyDir:    legacyDrafts,
			exported:   false,
			isDraft:    true,
			label:      "legacy drafts",
		}, nil
	}
	if v := readCurrentVersion(legacyWiki, filepath.Join(legacyWiki, "versions")); v != "" {
		return &docSource{
			catalogDir: v,
			bodyDir:    v,
			exported:   false,
			isDraft:    false,
			label:      "legacy version",
		}, nil
	}
	if latest := latestVersionDir(filepath.Join(legacyWiki, "versions")); latest != "" {
		return &docSource{
			catalogDir: latest,
			bodyDir:    latest,
			exported:   false,
			isDraft:    false,
			label:      "legacy version",
		}, nil
	}

	return nil, fmt.Errorf("找不到文档，请先运行 wikify generate（会写出 .wikify/content）")
}

func readCurrentVersion(wikiDir, versionsDir string) string {
	data, err := os.ReadFile(filepath.Join(wikiDir, "current"))
	if err != nil {
		return ""
	}
	versionID := strings.TrimSpace(string(data))
	if versionID == "" {
		return ""
	}
	vDir := filepath.Join(versionsDir, versionID)
	if fileExists(filepath.Join(vDir, "wiki.json")) {
		return vDir
	}
	return ""
}

func latestVersionDir(versionsDir string) string {
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return ""
	}
	var latest string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		vDir := filepath.Join(versionsDir, entry.Name())
		if fileExists(filepath.Join(vDir, "wiki.json")) {
			latest = vDir
		}
	}
	return latest
}

func findExportSource(workDir string) *docSource {
	root := filepath.Join(workDir, ".wikify")
	contentDir := filepath.Join(root, "content")
	metaDir := filepath.Join(root, "meta")
	indexPath := filepath.Join(metaDir, "browse-index.json")
	wikiJSON := filepath.Join(metaDir, "wiki.json")
	if !dirHasMarkdown(contentDir) {
		return nil
	}
	if !fileExists(indexPath) && !fileExists(wikiJSON) {
		// content alone is enough; index rebuilt from files
	}
	return &docSource{
		catalogDir: metaDir,
		bodyDir:    contentDir,
		exported:   true,
		isDraft:    false,
		label:      "wikify",
	}
}

// dirHasMarkdown reports whether dir (recursively) contains at least one non-empty .md file.
func dirHasMarkdown(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() == 0 {
			return nil
		}
		found = true
		return filepath.SkipAll
	})
	return found
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ── HTTP server ────────────────────────────────────────────────────────────────

func serve(src *docSource, projectName string, cats []navCategory, wiki *wikiData, md goldmark.Markdown, tmpl *template.Template, port int, openBrowser bool) error {
	mux := http.NewServeMux()

	// Local static assets (embedded Mermaid) — no CDN required.
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(mustSubStatic()))))

	// NAV-4: lazy in-memory full-text search over served page bodies.
	idx := newSearchIndex(src, wiki)
	mux.HandleFunc("/api/search", idx.handleSearch)
	// NAV-5: file→citing-pages reverse index (export artifact; "{}" when absent).
	mux.HandleFunc("/api/file-pages", handleFilePages(src))

	// JSON fragment API for SPA-style client navigation (no full page reload).
	mux.HandleFunc("/api/page/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/page/"), "/")
		slug = strings.TrimSuffix(slug, ".html")
		slug = strings.TrimSuffix(slug, ".md")
		if slug == "" {
			http.NotFound(w, r)
			return
		}
		if resolved := resolveSlug(slug, wiki, cats); resolved != "" {
			slug = resolved
		}
		writePageJSON(w, slug, src, wiki, cats, md)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimPrefix(r.URL.Path, "/")
		if slug == "" {
			if len(wiki.Pages) > 0 {
				http.Redirect(w, r, "/"+wiki.Pages[0].Slug, http.StatusFound)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		slug = strings.TrimSuffix(slug, ".html")
		slug = strings.TrimSuffix(slug, ".md")
		if resolved := resolveSlug(slug, wiki, cats); resolved != "" {
			slug = resolved
		}
		renderPage(w, slug, src, projectName, cats, wiki, md, tmpl, false)
	})

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("🚀 文档服务已启动 → \033[36mhttp://localhost%s\033[0m\n", addr)
	fmt.Printf("   共 %d 个页面 | 来源 %s | 按 Ctrl+C 停止\n", len(wiki.Pages), src.label)

	if openBrowser {
		go openURL(fmt.Sprintf("http://localhost%s", addr))
	}

	return http.ListenAndServe(addr, mux)
}

func mustSubStatic() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Should never happen with go:embed static/*
		return staticFS
	}
	return sub
}

func renderPage(w http.ResponseWriter, slug string, src *docSource, projectName string, cats []navCategory, wiki *wikiData, md goldmark.Markdown, tmpl *template.Template, static bool) {
	title, section, content := loadPageHTMLOpts(slug, src, wiki, cats, md, static)

	data := pageData{
		ProjectName: projectName,
		Title:       title,
		Section:     section,
		ActiveSlug:  slug,
		Categories:  cats,
		Content:     content,
		StaticLinks: static,
		SourceLabel: src.label,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

type pageAPIResponse struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Section string `json:"section"`
	Content string `json:"content"`
}

func loadPageHTML(slug string, src *docSource, wiki *wikiData, cats []navCategory, md goldmark.Markdown) (title, section string, content template.HTML) {
	return loadPageHTMLOpts(slug, src, wiki, cats, md, false)
}

func loadPageHTMLOpts(slug string, src *docSource, wiki *wikiData, cats []navCategory, md goldmark.Markdown, static bool) (title, section string, content template.HTML) {
	title, section, contentPath := resolvePage(slug, wiki, cats)
	raw, err := readPageBody(src, slug, contentPath)
	if err == nil {
		raw = rewriteWikiInternalLinks(raw, wiki, static)
		raw = preprocessMarkdown(raw)
		var buf bytes.Buffer
		if err2 := md.Convert(raw, &buf, parser.WithContext(parser.NewContext(parser.WithIDs(newUnicodeHeadingIDs())))); err2 == nil {
			content = template.HTML(buf.String())
		}
	}
	return title, section, content
}

func writePageJSON(w http.ResponseWriter, slug string, src *docSource, wiki *wikiData, cats []navCategory, md goldmark.Markdown) {
	// Validate / normalize slug (content_path and titles also accepted).
	if resolved := resolveSlug(slug, wiki, cats); resolved != "" {
		slug = resolved
	} else {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	title, section, content := loadPageHTML(slug, src, wiki, cats, md)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(pageAPIResponse{
		Slug:    slug,
		Title:   title,
		Section: section,
		Content: string(content),
	})
}

func readPageBody(src *docSource, slug, contentPath string) ([]byte, error) {
	// Try candidates in order. Exported trees store files under content_path
	// (often "Section/Title.md"); drafts use flat "{slug}.md".
	var candidates []string
	add := func(rel string) {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			return
		}
		rel = filepath.ToSlash(rel)
		rel = strings.TrimPrefix(rel, "content/")
		for _, c := range candidates {
			if c == rel {
				return
			}
		}
		candidates = append(candidates, rel)
	}
	if !src.exported && slug != "" {
		// Drafts prefer flat slug.md first.
		add(slug + ".md")
	}
	add(contentPath)
	if slug != "" {
		add(slug + ".md")
	}
	if contentPath != "" {
		base := filepath.Base(filepath.FromSlash(contentPath))
		if base != "" && base != "." && base != string(filepath.Separator) {
			add(base)
		}
	}
	for _, rel := range candidates {
		p := filepath.Join(src.bodyDir, filepath.FromSlash(rel))
		if data, err := os.ReadFile(p); err == nil {
			return data, nil
		}
	}
	// Last resort: walk content tree for basename match.
	if src.exported && contentPath != "" {
		want := strings.ToLower(filepath.Base(filepath.FromSlash(contentPath)))
		var found []byte
		_ = filepath.WalkDir(src.bodyDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || found != nil {
				return nil
			}
			if strings.ToLower(d.Name()) == want {
				if data, err2 := os.ReadFile(path); err2 == nil {
					found = data
					return filepath.SkipAll
				}
			}
			return nil
		})
		if found != nil {
			return found, nil
		}
	}
	if src.exported {
		return nil, fmt.Errorf("page not found in wikify content: %s", slug)
	}
	return nil, fmt.Errorf("page not found in drafts: %s", slug)
}

// ── static build ───────────────────────────────────────────────────────────────

func buildStatic(workDir string, src *docSource, projectName string, cats []navCategory, pages []pageInfo, md goldmark.Markdown, tmpl *template.Template) error {
	buildDir := filepath.Join(workDir, ".wikify", "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return err
	}
	// Copy embedded assets so static sites keep offline Mermaid.
	if err := writeStaticAssets(filepath.Join(buildDir, "static")); err != nil {
		return err
	}

	fmt.Printf("🔨 构建静态站点 → %s\n", buildDir)
	wiki := &wikiData{Pages: pages}
	for i, p := range pages {
		title, section, content := loadPageHTMLOpts(p.Slug, src, wiki, cats, md, true)
		data := pageData{
			ProjectName: projectName,
			Title:       title,
			Section:     section,
			ActiveSlug:  p.Slug,
			Categories:  cats,
			Content:     content,
			StaticLinks: true,
			SourceLabel: src.label,
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			fmt.Printf("  ⚠ 跳过 %s: %v\n", p.Slug, err)
			continue
		}
		dest := filepath.Join(buildDir, p.Slug+".html")
		_ = os.WriteFile(dest, buf.Bytes(), 0o644)
		if i == 0 {
			_ = os.WriteFile(filepath.Join(buildDir, "index.html"), buf.Bytes(), 0o644)
		}
		fmt.Printf("  ✓ %s.html\n", p.Slug)
	}
	fmt.Printf("\n✅ 完成，共 %d 个页面\n输出: %s\n", len(pages), buildDir)
	return nil
}

// ── helpers ────────────────────────────────────────────────────────────────────

func readWiki(src *docSource) (*wikiData, error) {
	// 1) wikify wiki.json
	if !src.exported {
		data, err := os.ReadFile(filepath.Join(src.catalogDir, "wiki.json"))
		if err != nil {
			return nil, fmt.Errorf("找不到 wiki.json：%w", err)
		}
		var wiki wikiData
		if err := json.Unmarshal(data, &wiki); err != nil {
			return nil, fmt.Errorf("解析 wiki.json 失败：%w", err)
		}
		if len(wiki.Pages) == 0 {
			return nil, fmt.Errorf("wiki 中没有页面")
		}
		return &wiki, nil
	}

	// 2) meta/wiki.json (full catalog)
	if data, err := os.ReadFile(filepath.Join(src.catalogDir, "wiki.json")); err == nil {
		var wiki wikiData
		if err := json.Unmarshal(data, &wiki); err != nil {
			return nil, fmt.Errorf("解析 wiki.json 失败：%w", err)
		}
		if len(wiki.Pages) == 0 {
			return nil, fmt.Errorf("wiki 中没有页面")
		}
		return &wiki, nil
	}

	// 3) browse-index.json
	indexPath := filepath.Join(src.catalogDir, "browse-index.json")
	if data, err := os.ReadFile(indexPath); err == nil {
		var idx struct {
			Pages []pageInfo `json:"pages"`
		}
		if err := json.Unmarshal(data, &idx); err != nil {
			return nil, fmt.Errorf("解析 browse-index.json 失败：%w", err)
		}
		if len(idx.Pages) == 0 {
			return nil, fmt.Errorf("browse-index 中没有页面")
		}
		return &wikiData{Pages: idx.Pages}, nil
	}

	// 4) fallback: scan content dir for *.md
	var pages []pageInfo
	_ = filepath.WalkDir(src.bodyDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		rel, err2 := filepath.Rel(src.bodyDir, path)
		if err2 != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		title := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
		sec := filepath.Dir(rel)
		if sec == "." {
			sec = "文档"
		}
		slug := strings.TrimSuffix(rel, ".md")
		slug = strings.ReplaceAll(slug, "/", "-")
		pages = append(pages, pageInfo{
			Title: title, Slug: slug, Section: sec, ContentPath: rel,
		})
		return nil
	})
	if len(pages) == 0 {
		return nil, fmt.Errorf("导出内容目录下没有 markdown 页面")
	}
	return &wikiData{Pages: pages}, nil
}

func buildCategories(pages []pageInfo) []navCategory {
	// Preserve wiki page order for global numbering, but sort categories so
	// foundation sections surface first, then business, then technical.
	type bucket struct {
		label string
		items []pageInfo
		track string
		order int // first appearance
	}
	catMap := map[string]*bucket{}
	order := []string{}
	for i, p := range pages {
		sec := p.Section
		if sec == "" {
			sec = "文档"
		}
		if _, ok := catMap[sec]; !ok {
			order = append(order, sec)
			catMap[sec] = &bucket{label: sec, track: p.Track, order: i}
		}
		b := catMap[sec]
		// Dominant track = first non-empty track in section.
		if b.track == "" && p.Track != "" {
			b.track = p.Track
		}
		b.items = append(b.items, p)
	}
	// Stable sort categories by track priority then first appearance.
	trackRank := func(t string) int {
		switch t {
		case "foundation":
			return 0
		case "business":
			return 1
		case "technical":
			return 2
		default:
			return 3
		}
	}
	type ranked struct {
		key  string
		rank int
		ord  int
	}
	rankedList := make([]ranked, 0, len(order))
	for _, s := range order {
		b := catMap[s]
		rankedList = append(rankedList, ranked{key: s, rank: trackRank(b.track), ord: b.order})
	}
	for i := 0; i < len(rankedList); i++ {
		for j := i + 1; j < len(rankedList); j++ {
			if rankedList[j].rank < rankedList[i].rank ||
				(rankedList[j].rank == rankedList[i].rank && rankedList[j].ord < rankedList[i].ord) {
				rankedList[i], rankedList[j] = rankedList[j], rankedList[i]
			}
		}
	}
	out := make([]navCategory, 0, len(rankedList))
	global := 0
	for ci, r := range rankedList {
		b := catMap[r.key]
		items := make([]pageInfo, 0, len(b.items))
		for li, p := range b.items {
			global++
			p.Index = global
			p.CatIndex = li + 1
			items = append(items, p)
		}
		out = append(out, navCategory{
			Label: b.label,
			Index: ci + 1,
			Items: items,
			Track: b.track,
		})
	}
	return out
}

func resolvePage(slug string, wiki *wikiData, cats []navCategory) (title, section, contentPath string) {
	if s := resolveSlug(slug, wiki, cats); s != "" {
		slug = s
	}
	if wiki != nil {
		for _, p := range wiki.Pages {
			if p.Slug == slug {
				return p.Title, p.Section, p.ContentPath
			}
		}
	}
	for _, c := range cats {
		for _, p := range c.Items {
			if p.Slug == slug {
				return p.Title, c.Label, p.ContentPath
			}
		}
	}
	return slug, "", ""
}

// resolveSlug maps alternate identifiers (content_path, Title.md, Title) to the catalog slug.
func resolveSlug(raw string, wiki *wikiData, cats []navCategory) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimPrefix(raw, "/")
	raw = strings.TrimSuffix(raw, ".html")
	raw = strings.TrimSuffix(raw, ".md")
	raw = filepath.ToSlash(raw)
	raw = strings.TrimPrefix(raw, "content/")
	low := strings.ToLower(raw)
	base := strings.ToLower(filepath.Base(raw))

	match := func(p pageInfo) string {
		if p.Slug == raw || strings.ToLower(p.Slug) == low {
			return p.Slug
		}
		if p.Title != "" && (p.Title == raw || strings.ToLower(p.Title) == low || strings.ToLower(p.Title) == base) {
			return p.Slug
		}
		if p.ContentPath != "" {
			cp := filepath.ToSlash(strings.TrimPrefix(p.ContentPath, "content/"))
			cpLow := strings.ToLower(cp)
			cpBase := strings.ToLower(strings.TrimSuffix(filepath.Base(cp), ".md"))
			if cpLow == low || cpLow == low+".md" || cpBase == low || cpBase == base {
				return p.Slug
			}
		}
		return ""
	}
	if wiki != nil {
		for _, p := range wiki.Pages {
			if s := match(p); s != "" {
				return s
			}
		}
	}
	for _, c := range cats {
		for _, p := range c.Items {
			if s := match(p); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// rewriteWikiInternalLinks maps markdown links that point at content_path / Title.md /
// bare catalog slugs onto browse routes (/slug or slug.html). Leaves file://, http(s),
// anchors, and unknown targets untouched.
func rewriteWikiInternalLinks(raw []byte, wiki *wikiData, static bool) []byte {
	if wiki == nil || len(wiki.Pages) == 0 || len(raw) == 0 {
		return raw
	}
	byPath := map[string]string{}
	bySlug := map[string]string{}
	byTitle := map[string]string{}
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "./")
		s = strings.TrimPrefix(s, "/")
		s = filepath.ToSlash(s)
		s = strings.TrimPrefix(s, "content/")
		return strings.ToLower(s)
	}
	for _, p := range wiki.Pages {
		if p.Slug == "" {
			continue
		}
		bySlug[strings.ToLower(p.Slug)] = p.Slug
		if p.Title != "" {
			byTitle[strings.ToLower(p.Title)] = p.Slug
			byPath[norm(p.Title+".md")] = p.Slug
		}
		if p.ContentPath != "" {
			cp := norm(p.ContentPath)
			byPath[cp] = p.Slug
			byPath[norm(filepath.Base(filepath.FromSlash(p.ContentPath)))] = p.Slug
		}
	}
	re := regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
	s := string(raw)
	var b strings.Builder
	last := 0
	for _, loc := range re.FindAllStringSubmatchIndex(s, -1) {
		fullStart, fullEnd := loc[0], loc[1]
		// skip images ![
		if fullStart > 0 && s[fullStart-1] == '!' {
			continue
		}
		label := s[loc[2]:loc[3]]
		target := strings.TrimSpace(s[loc[4]:loc[5]])
		if strings.HasPrefix(target, "file://") || strings.HasPrefix(target, "http://") ||
			strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") ||
			strings.HasPrefix(target, "#") || strings.HasPrefix(target, "data:") {
			continue
		}
		if i := strings.IndexAny(target, " \"'\t"); i >= 0 {
			target = target[:i]
		}
		target = strings.Trim(target, "<>")
		key := norm(target)
		keyNoMD := strings.TrimSuffix(key, ".md")
		slug := ""
		if v, ok := byPath[key]; ok {
			slug = v
		} else if v, ok := byPath[keyNoMD+".md"]; ok {
			slug = v
		} else if v, ok := bySlug[keyNoMD]; ok {
			slug = v
		} else if v, ok := bySlug[key]; ok {
			slug = v
		} else if v, ok := byTitle[keyNoMD]; ok {
			slug = v
		}
		if slug == "" {
			continue
		}
		b.WriteString(s[last:fullStart])
		if static {
			b.WriteString("[" + label + "](" + slug + ".html)")
		} else {
			b.WriteString("[" + label + "](/" + slug + ")")
		}
		last = fullEnd
	}
	b.WriteString(s[last:])
	return []byte(b.String())
}

func openURL(url string) {
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("cmd", "/c", "start", url).Start()
	case "darwin":
		_ = exec.Command("open", url).Start()
	default:
		_ = exec.Command("xdg-open", url).Start()
	}
}

// ── HTML template ──────────────────────────────────────────────────────────────

const htmlTmpl = `<!DOCTYPE html>
<html lang="zh-CN" data-theme="dark">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="generator" content="Symon Wikify">
<title>{{.Title}} · {{.ProjectName}} — Symon</title>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
:root{
  --sb-w:312px;
  --font:"Segoe UI Variable","Segoe UI",system-ui,-apple-system,"PingFang SC","Noto Sans SC","Microsoft YaHei",sans-serif;
  --mono:"JetBrains Mono","Fira Code","Cascadia Code",Consolas,monospace;
  --g:linear-gradient(125deg,#c084fc 0%,#818cf8 40%,#22d3ee 75%,#f472b6 100%);
}
html[data-theme="dark"]{
  --bg0:#03050c;--bg1:#0a0e1c;
  --paper:rgba(12,16,30,.92);--paper-solid:#0e1424;
  --border:rgba(168,85,247,.22);--border-hi:rgba(34,211,238,.45);
  --text:#f4f7ff;--text-dim:#c5cee3;--heading:#fff;--muted:#8b95b0;
  --link:#67e8f9;--link-h:#a5f3fc;
  --accent:#c084fc;--accent2:#22d3ee;--glow:rgba(192,132,252,.55);
  --sb:linear-gradient(180deg,#050816 0%,#0a1024 55%,#0c1530 100%);
  --sb-t:#c8d0e6;--sb-th:#ffffff;--sb-on:#f3e8ff;
  --sb-onbg:linear-gradient(90deg,rgba(192,132,252,.28),rgba(34,211,238,.08));
  --code-bg:rgba(192,132,252,.14);--code-fg:#f9a8d4;
  --pre:#060a14;--pre-b:rgba(192,132,252,.28);
  --chip-bg:rgba(34,211,238,.1);--chip-b:rgba(34,211,238,.4);--chip-t:#67e8f9;--chip-l:#22d3ee;
  --panel:linear-gradient(145deg,rgba(88,28,135,.28),rgba(8,47,73,.35));
  --quote-bg:rgba(192,132,252,.12);--quote-b:#c084fc;
  --th:rgba(192,132,252,.1);--tr:rgba(148,163,184,.035);
  --top:rgba(3,5,12,.78);
  --shadow:0 0 0 1px rgba(192,132,252,.12),0 24px 60px rgba(0,0,0,.55),0 0 100px rgba(168,85,247,.08);
  --scroll:rgba(192,132,252,.35);
  --neon:0 0 20px rgba(192,132,252,.35),0 0 40px rgba(34,211,238,.12);
}
html[data-theme="light"]{
  --bg0:#eef0ff;--bg1:#f8f7ff;
  --paper:rgba(255,255,255,.9);--paper-solid:#fff;
  --border:rgba(124,58,237,.14);--border-hi:rgba(124,58,237,.35);
  --text:#0f172a;--text-dim:#334155;--heading:#020617;--muted:#475569;
  --link:#6d28d9;--link-h:#5b21b6;
  --accent:#7c3aed;--accent2:#0891b2;--glow:rgba(124,58,237,.22);
  --sb:linear-gradient(180deg,#0f172a 0%,#1e1b4b 100%);
  --sb-t:#94a3b8;--sb-th:#f8fafc;--sb-on:#e9d5ff;
  --sb-onbg:linear-gradient(90deg,rgba(192,132,252,.3),rgba(34,211,238,.1));
  --code-bg:#f3e8ff;--code-fg:#a21caf;
  --pre:#0f172a;--pre-b:rgba(15,23,42,.2);
  --chip-bg:#ede9fe;--chip-b:#c4b5fd;--chip-t:#5b21b6;--chip-l:#7c3aed;
  --panel:linear-gradient(145deg,#faf5ff,#eef2ff);
  --quote-bg:#f5f3ff;--quote-b:#8b5cf6;
  --th:#f5f3ff;--tr:#fafbff;
  --top:rgba(255,255,255,.85);
  --shadow:0 1px 2px rgba(15,23,42,.05),0 20px 50px rgba(124,58,237,.1);
  --scroll:#c4b5fd;
  --neon:0 8px 30px rgba(124,58,237,.12);
}
html,body{height:100%}
body{
  font-family:var(--font);color:var(--text);display:flex;overflow:hidden;height:100vh;
  -webkit-font-smoothing:antialiased;background:var(--bg0);position:relative;
}
#fx-canvas{position:fixed;inset:0;z-index:0;pointer-events:none;opacity:.28}
.cursor-glow{
  position:fixed;width:420px;height:420px;margin:-210px 0 0 -210px;border-radius:50%;
  pointer-events:none;z-index:1;opacity:0;mix-blend-mode:screen;
  background:radial-gradient(circle,rgba(192,132,252,.18) 0%,rgba(34,211,238,.08) 35%,transparent 70%);
  transition:opacity .25s;will-change:transform
}
html[data-theme="light"] .cursor-glow{mix-blend-mode:multiply}
body:hover .cursor-glow{opacity:1}
.bg-noise{
  position:fixed;inset:0;z-index:1;pointer-events:none;opacity:.02;
  background-image:url("data:image/svg+xml,%3Csvg viewBox='0 0 200 200' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='.85' numOctaves='4' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)'/%3E%3C/svg%3E");
}
.progress{
  position:fixed;top:0;left:0;height:3px;width:100%;z-index:200;
  background:var(--g);background-size:220% 100%;
  box-shadow:0 0 18px var(--glow),0 0 4px #22d3ee;
  transform:scaleX(0);transform-origin:0 50%;
}
.theme-flash{position:fixed;inset:0;z-index:300;pointer-events:none;opacity:0}
.sb,.main{position:relative;z-index:2}
.sb{
  width:var(--sb-w);min-width:var(--sb-w);background:var(--sb);
  display:flex;flex-direction:column;height:100vh;
  border-right:1px solid rgba(255,255,255,.07);
  box-shadow:16px 0 60px rgba(0,0,0,.35);
}
.sb::before{
  content:"";position:absolute;inset:0;pointer-events:none;
  background:
    radial-gradient(500px 260px at 10% -10%, rgba(192,132,252,.28), transparent 65%),
    radial-gradient(360px 220px at 100% 100%, rgba(34,211,238,.14), transparent 60%);
}
.sb-head{padding:1.15rem 1.1rem 1rem;border-bottom:1px solid rgba(255,255,255,.07);flex-shrink:0;position:relative}
.brand-row{display:flex;align-items:center;gap:.8rem}
.brand-mark{
  width:44px;height:44px;border-radius:14px;flex-shrink:0;
  background:var(--g);background-size:200% 200%;
  display:grid;place-items:center;position:relative;
  box-shadow:var(--neon),0 0 0 1px rgba(255,255,255,.2);
}
.brand-mark::after{
  content:"";position:absolute;inset:-2px;border-radius:16px;
  background:linear-gradient(135deg,rgba(192,132,252,.5),rgba(34,211,238,.35));
  z-index:-1;opacity:.45;filter:blur(4px)
}
.brand-mark svg{width:24px;height:24px;fill:#0b1020}
.brand-copy{min-width:0}
.brand-name{
  font-size:1.15rem;font-weight:900;letter-spacing:.12em;line-height:1.1;
  background:var(--g);background-size:200% auto;
  -webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent;
}
.brand-tag{margin-top:3px;font-size:.6rem;font-weight:800;letter-spacing:.18em;text-transform:uppercase;color:#7c8db8}
.project-card{
  margin-top:1rem;padding:.75rem .85rem .75rem 1rem;border-radius:14px;
  background:rgba(255,255,255,.04);border:1px solid rgba(255,255,255,.08);
  position:relative;overflow:hidden
}
.project-card::before{
  content:"";position:absolute;left:0;top:0;bottom:0;width:3px;background:var(--g)
}
.project-name{font-size:.92rem;font-weight:780;color:var(--sb-th);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.project-meta{display:flex;align-items:center;gap:6px;margin-top:5px;font-size:.62rem;font-weight:800;letter-spacing:.08em;text-transform:uppercase;color:#9aa8c7}
.project-meta .dot{width:7px;height:7px;border-radius:50%;background:#34d399;box-shadow:0 0 10px #34d399;animation:pulse 1.8s ease-out infinite}
@keyframes pulse{0%{box-shadow:0 0 0 0 rgba(52,211,153,.6)}70%{box-shadow:0 0 0 10px transparent}100%{box-shadow:0 0 0 0 transparent}}
.sb-search{padding:.9rem 1rem .45rem;flex-shrink:0;position:relative}
.sb-search::before{content:"⌕";position:absolute;left:1.55rem;top:50%;transform:translateY(-30%);color:#9aa8c4;z-index:2;font-size:1rem}
.sb-search input{
  width:100%;background:rgba(255,255,255,.05);border:1px solid rgba(255,255,255,.1);
  color:var(--sb-th);border-radius:12px;padding:.62rem .85rem .62rem 2.15rem;font-size:.82rem;outline:none;
  transition:border-color .15s,box-shadow .15s,background .15s
}
.sb-search input::placeholder{color:#9aa8c4}
.sb-search input:focus{
  border-color:rgba(192,132,252,.6);background:rgba(192,132,252,.1);
  box-shadow:0 0 0 3px rgba(192,132,252,.18),0 0 30px rgba(34,211,238,.1)
}
.sb-nav{padding:.15rem 0 1rem;flex:1;overflow-y:auto;position:relative}
.sb-nav::-webkit-scrollbar{width:4px}
.sb-nav::-webkit-scrollbar-thumb{background:rgba(255,255,255,.12);border-radius:4px}
/* collapsible categories */
.cat-toggle{
  width:100%;display:flex;align-items:center;gap:.45rem;
  padding:.55rem .85rem;margin:.35rem .55rem 2px;border:0;background:transparent;
  color:#d4dced;font:inherit;font-size:.72rem;font-weight:850;letter-spacing:.08em;
  text-transform:uppercase;cursor:pointer;border-radius:10px;text-align:left;
  transition:background .15s,color .15s
}
.cat-toggle:hover{background:rgba(255,255,255,.08);color:#fff}
.cat-toggle .chev{
  width:14px;height:14px;flex-shrink:0;display:grid;place-items:center;
  transition:transform .2s ease;color:#c4b5fd;font-size:.65rem
}
.cat-toggle .cat-name{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.cat-toggle .cat-count{
  font-size:.62rem;font-weight:800;letter-spacing:0;text-transform:none;
  color:#e2e8f0;background:rgba(255,255,255,.12);border-radius:999px;
  padding:.1rem .42rem;min-width:1.4rem;text-align:center
}
.cat.open .cat-toggle .chev{transform:rotate(90deg)}
.cat.open .cat-toggle{color:#fff}
.cat-items{display:none;padding-bottom:.15rem}
.cat.open .cat-items{display:block}
.cat-label{display:none}
.nav-a{
  display:flex;align-items:center;gap:.5rem;padding:.46rem .7rem .46rem .65rem;margin:2px .55rem;
  color:var(--sb-t);text-decoration:none;font-size:.84rem;font-weight:520;border-radius:11px;
  transition:background .15s,color .15s,box-shadow .15s;
  white-space:nowrap;overflow:hidden;text-overflow:ellipsis;line-height:1.45;position:relative
}
.nav-num{
  flex-shrink:0;min-width:1.7rem;height:1.35rem;padding:0 .28rem;
  display:inline-flex;align-items:center;justify-content:center;
  font-family:var(--mono);font-size:.68rem;font-weight:800;letter-spacing:.02em;
  color:#9aa8c7;background:rgba(255,255,255,.06);border:1px solid rgba(255,255,255,.08);
  border-radius:7px;transition:all .15s
}
.nav-title{min-width:0;overflow:hidden;text-overflow:ellipsis}
.nav-a:hover{color:var(--sb-th);background:rgba(255,255,255,.05)}
.nav-a:hover .nav-num{color:#e9d5ff;border-color:rgba(192,132,252,.45);box-shadow:0 0 12px rgba(192,132,252,.25)}
.nav-a.on{
  color:var(--sb-on);background:var(--sb-onbg);font-weight:700;
  box-shadow:inset 0 0 0 1px rgba(233,213,255,.2),0 0 24px rgba(192,132,252,.12)
}
.nav-a.on .nav-num{
  color:#0b1020;background:var(--g);border-color:transparent;
  box-shadow:0 0 14px rgba(192,132,252,.55)
}
.nav-a.on::after{
  content:"";position:absolute;right:10px;width:5px;height:5px;border-radius:50%;
  background:#22d3ee;box-shadow:0 0 10px #22d3ee
}
.cat-idx{
  flex-shrink:0;font-family:var(--mono);font-size:.62rem;font-weight:900;
  color:#a5b4fc;opacity:.9;min-width:1.1rem
}
.cat-track{
  flex-shrink:0;font-size:.55rem;font-weight:800;letter-spacing:.04em;text-transform:uppercase;
  color:#94a3b8;background:rgba(255,255,255,.06);border-radius:999px;padding:.08rem .35rem
}
.cat-track.t-foundation{color:#67e8f9;background:rgba(34,211,238,.12)}
.cat-track.t-business{color:#f9a8d4;background:rgba(244,114,182,.12)}
.cat-track.t-technical{color:#c4b5fd;background:rgba(192,132,252,.12)}
.sb-foot{padding:.85rem 1rem 1rem;border-top:1px solid rgba(255,255,255,.07);flex-shrink:0;display:flex;flex-direction:column;gap:.55rem;position:relative}
.foot-actions{display:flex;gap:.45rem}
.icon-btn{
  flex:1;display:inline-flex;align-items:center;justify-content:center;gap:.4rem;
  background:rgba(255,255,255,.05);border:1px solid rgba(255,255,255,.1);color:#dbe3f5;
  border-radius:11px;padding:.5rem .5rem;font-size:.74rem;font-weight:700;cursor:pointer;
  transition:background .15s,border-color .15s,box-shadow .15s;font-family:inherit
}
.icon-btn:hover{background:rgba(192,132,252,.18);border-color:rgba(233,213,255,.4);box-shadow:0 0 22px rgba(192,132,252,.2)}
.theme-ico{display:inline-block;line-height:1;transform-origin:center center;will-change:transform}
.powered{display:flex;align-items:center;justify-content:center;gap:.4rem;font-size:.68rem;font-weight:700;color:#6b7a96}
.powered .symon{background:var(--g);background-size:200% auto;-webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent;font-weight:900;letter-spacing:.06em}
.powered .bolt{color:#c084fc}
.main{flex:1;min-width:0;display:flex;flex-direction:column;overflow:hidden}
.topbar{
  background:var(--top);
  border-bottom:1px solid var(--border);padding:.75rem 1.5rem;
  display:flex;align-items:center;gap:.6rem;font-size:.8rem;color:var(--muted);flex-shrink:0
}
.topbar-brand{display:inline-flex;align-items:center;gap:.4rem;margin-right:.4rem;padding-right:.75rem;border-right:1px solid var(--border);flex-shrink:0}
.topbar-brand .mini-mark{width:20px;height:20px;border-radius:7px;background:var(--g);display:grid;place-items:center;box-shadow:0 0 12px rgba(192,132,252,.4)}
.topbar-brand .mini-mark svg{width:12px;height:12px;fill:#0b1020}
.topbar-brand .mini-name{font-size:.72rem;font-weight:900;letter-spacing:.12em;background:var(--g);-webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent}
.topbar-path{display:flex;align-items:center;gap:.4rem;min-width:0;flex:1;overflow:hidden}
.topbar-sep{opacity:.4}
.topbar-cur{color:var(--heading);font-weight:700;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.topbar-actions{display:flex;gap:.4rem;margin-left:auto}
.pill{
  background:transparent;border:1px solid var(--border);color:var(--text-dim);
  border-radius:999px;padding:.3rem .8rem;font-size:.72rem;font-weight:700;cursor:pointer;font-family:inherit;
  transition:border-color .15s,box-shadow .15s,color .15s
}
.pill:hover{border-color:var(--accent);color:var(--heading);box-shadow:0 0 0 3px var(--glow)}
.topsearch{position:relative;margin-right:.5rem}
.topsearch input{
  background:transparent;border:1px solid var(--border);color:var(--text-dim);
  border-radius:999px;padding:.32rem .9rem;font-size:.74rem;outline:none;width:170px;
  font-family:inherit;transition:border-color .15s,box-shadow .15s,width .2s
}
.topsearch input:focus{border-color:var(--accent);box-shadow:0 0 0 3px var(--glow);width:230px;color:var(--heading)}
.search-pop{
  position:absolute;top:calc(100% + 8px);right:0;width:340px;max-height:60vh;overflow-y:auto;z-index:60;
  background:var(--paper-solid);border:1px solid var(--border);border-radius:14px;box-shadow:var(--shadow);
  display:none;padding:.4rem
}
.search-pop.open{display:block}
.search-item{display:block;text-decoration:none;padding:.55rem .65rem;border-radius:10px;cursor:pointer}
.search-item:hover{background:var(--glow)}
.search-item .si-title{font-size:.8rem;font-weight:800;color:var(--heading)}
.search-item .si-sec{font-size:.66rem;color:var(--muted);margin-left:.4rem;font-weight:600}
.search-item .si-snip{font-size:.7rem;color:var(--text-dim);margin-top:.2rem;line-height:1.5;word-break:break-all}
.search-empty{padding:.7rem .65rem;font-size:.72rem;color:var(--muted)}
.src-chip[data-path]{cursor:pointer}
.src-pop{
  position:fixed;z-index:70;width:300px;max-height:40vh;overflow-y:auto;
  background:var(--paper-solid);border:1px solid var(--border);border-radius:14px;box-shadow:var(--shadow);padding:.45rem
}
.src-pop .sp-head{font-size:.64rem;font-weight:900;letter-spacing:.1em;text-transform:uppercase;color:var(--muted);padding:.3rem .55rem .4rem;font-family:var(--mono);word-break:break-all}
.src-pop a{display:block;text-decoration:none;padding:.4rem .55rem;border-radius:9px;font-size:.76rem;font-weight:700;color:var(--heading)}
.src-pop a:hover{background:var(--glow)}
.src-pop .sp-none{padding:.4rem .55rem;font-size:.7rem;color:var(--muted)}
.scroll{flex:1;overflow-y:auto;scroll-behavior:smooth}
.scroll::-webkit-scrollbar{width:8px}
.scroll::-webkit-scrollbar-thumb{background:var(--scroll);border-radius:8px;border:2px solid transparent;background-clip:padding-box}
.page-wrap{max-width:960px;padding:1.9rem 1.75rem 3.4rem;margin:0 auto}
.paper{
  background:var(--paper);
  border:1px solid var(--border);border-radius:22px;padding:2.3rem 2.55rem 2.8rem;
  box-shadow:var(--shadow);position:relative;overflow:hidden
}
.paper::before{content:"";position:absolute;top:0;left:0;right:0;height:3px;background:var(--g);background-size:200% 100%}
.paper::after{
  content:"";position:absolute;top:-30%;right:-12%;width:320px;height:320px;pointer-events:none;
  background:radial-gradient(circle,rgba(192,132,252,.16),transparent 65%)
}
.content{position:relative;z-index:1}
.content > :first-child{margin-top:0}
.content h1{
  font-size:clamp(1.8rem,2.6vw,2.25rem);font-weight:900;color:var(--heading);
  margin:0 0 1.25rem;letter-spacing:-.04em;line-height:1.15;
  background:linear-gradient(120deg,var(--heading) 10%,var(--accent) 50%,var(--accent2) 100%);
  -webkit-background-clip:text;background-clip:text
}
html[data-theme="dark"] .content h1{-webkit-text-fill-color:transparent}
.content h2{
  font-size:1.32rem;font-weight:820;color:var(--heading);
  margin:2.25rem 0 .8rem;padding-bottom:.55rem;border-bottom:1px solid var(--border);position:relative
}
.content h2::after{content:"";position:absolute;left:0;bottom:-1px;width:64px;height:2px;background:var(--g);border-radius:2px;box-shadow:0 0 12px var(--glow)}
.content h3{font-size:1.1rem;font-weight:750;color:var(--heading);margin:1.7rem 0 .5rem}
.content h4,.content h5{font-weight:680;color:var(--heading);margin:1.25rem 0 .35rem}
.content p{line-height:1.92;margin-bottom:1rem;color:var(--text)}
.content ul,.content ol{padding-left:1.35rem;margin-bottom:1rem}
.content li{line-height:1.85;margin-bottom:.3rem;color:var(--text)}
.content li::marker{color:var(--accent)}
.content a{color:var(--link);text-decoration:none;border-bottom:1px solid transparent;transition:border-color .12s,color .12s,text-shadow .12s}
.content a:hover{color:var(--link-h);border-bottom-color:currentColor;text-shadow:0 0 12px rgba(34,211,238,.35)}
.content strong{color:var(--heading);font-weight:750}
.content blockquote{border-left:3px solid var(--quote-b);padding:.85rem 1.2rem;margin:1.2rem 0;background:var(--quote-bg);border-radius:0 14px 14px 0}
.content hr{border:none;height:1px;margin:2.3rem 0;background:linear-gradient(90deg,transparent,var(--border-hi),transparent)}
.content img{max-width:100%;border-radius:14px;margin:.6rem 0;box-shadow:var(--shadow)}
.content code{background:var(--code-bg);padding:.12em .42em;border-radius:6px;font-family:var(--mono);font-size:.86em;color:var(--code-fg)}
.content pre{
  background:var(--pre);color:#e2e8f0;padding:1.2rem 1.35rem;border-radius:16px;
  overflow-x:auto;margin:1rem 0 1.4rem;font-size:.84rem;line-height:1.7;
  border:1px solid var(--pre-b);box-shadow:inset 0 1px 0 rgba(255,255,255,.04),0 12px 32px rgba(0,0,0,.3)
}
.content pre code{background:none;padding:0;color:inherit;font-size:inherit}
.content table{width:100%;border-collapse:separate;border-spacing:0;margin:1rem 0 1.4rem;font-size:.9rem;border:1px solid var(--border);border-radius:14px;overflow:hidden;background:var(--paper-solid)}
.content th{background:var(--th);font-weight:750;text-align:left;padding:.7rem 1rem;border-bottom:1px solid var(--border);color:var(--heading)}
.content td{padding:.55rem 1rem;border-bottom:1px solid var(--border);color:var(--text)}
.content tr:last-child td{border-bottom:none}
.content tr:nth-child(even) td{background:var(--tr)}
.src-panel{
  background:var(--panel);border:1px solid var(--border);border-radius:18px;
  padding:1.1rem 1.2rem 1.2rem;margin:0 0 1.75rem;
  box-shadow:inset 0 1px 0 rgba(255,255,255,.06),0 0 40px rgba(192,132,252,.06)
}
.src-panel-title{font-size:.66rem;font-weight:900;letter-spacing:.14em;text-transform:uppercase;color:var(--muted);margin-bottom:.8rem;display:flex;align-items:center;gap:.45rem}
.src-panel-title::before{content:"";width:10px;height:10px;border-radius:3px;background:var(--g);box-shadow:0 0 14px var(--glow)}
.src-chips{display:flex;flex-wrap:wrap;gap:.5rem}
.src-chip{
  display:inline-flex;align-items:center;gap:.35rem;max-width:100%;
  background:var(--chip-bg);border:1px solid var(--chip-b);color:var(--chip-t);
  border-radius:999px;padding:.32rem .85rem .32rem .55rem;font-size:.76rem;line-height:1.35;
  font-family:var(--mono);cursor:default;transition:transform .15s,box-shadow .15s,border-color .15s;position:relative;overflow:hidden
}
.src-chip:hover{transform:translateY(-2px);border-color:var(--chip-l);box-shadow:0 0 0 3px rgba(34,211,238,.14),0 8px 22px rgba(34,211,238,.16)}
.src-icon{color:var(--chip-l);font-size:.72rem}
.src-name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:15rem}
.src-lines{margin-left:.2rem;padding-left:.4rem;border-left:1px solid color-mix(in srgb,var(--chip-l) 40%,transparent);color:var(--chip-l);font-size:.7rem;font-weight:800;white-space:nowrap}
p .src-chip,li .src-chip{vertical-align:middle;margin:0 2px}
.content cite{display:none}
.mermaid-wrap{background:var(--paper-solid);border:1px solid var(--border);border-radius:16px;padding:1.3rem;margin:1.15rem 0;text-align:center;overflow-x:auto;box-shadow:var(--shadow)}
.pager{display:grid;grid-template-columns:1fr 1fr;gap:.85rem;margin-top:1.9rem}
.pager a{
  display:flex;flex-direction:column;gap:.28rem;padding:1.05rem 1.15rem;text-decoration:none;
  background:var(--paper);border:1px solid var(--border);border-radius:16px;
  transition:border-color .15s,box-shadow .15s,transform .15s;min-width:0;position:relative;overflow:hidden
}
.pager a:hover{border-color:var(--border-hi);transform:translateY(-3px);box-shadow:var(--neon)}
.pager .lbl{font-size:.64rem;font-weight:900;letter-spacing:.12em;text-transform:uppercase;color:var(--muted)}
.pager .ttl{font-size:.93rem;font-weight:720;color:var(--heading);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.pager .next{text-align:right}
.pager a.disabled{opacity:.3;pointer-events:none}
.page-foot{margin-top:1.55rem;text-align:center;font-size:.72rem;color:var(--muted);letter-spacing:.04em}
.page-foot .symon{background:var(--g);background-size:200% auto;-webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent;font-weight:900}
.empty{display:flex;flex-direction:column;align-items:center;justify-content:center;min-height:42vh;text-align:center;color:var(--muted);padding:2rem}
.empty-icon{width:76px;height:76px;border-radius:22px;margin-bottom:1.15rem;background:var(--g);display:grid;place-items:center;box-shadow:var(--neon)}
.empty-icon svg{width:36px;height:36px;fill:#0b1020}
.empty h2{font-size:1.2rem;color:var(--heading);margin-bottom:.5rem}
.empty code{background:var(--code-bg);padding:.15em .45em;border-radius:6px;font-size:.875em}
.fx-ripple{position:absolute;width:12px;height:12px;margin:-6px 0 0 -6px;border-radius:50%;background:radial-gradient(circle,rgba(192,132,252,.6),rgba(34,211,238,.2) 55%,transparent 70%);pointer-events:none;z-index:5}
.fx-reveal{opacity:0;transform:translateY(24px)}
/* Pure CSS motion — lightweight page entrance / SPA transitions */
@keyframes wikify-rise {
  from { opacity:0; transform:translateY(18px); }
  to   { opacity:1; transform:none; }
}
@keyframes wikify-slide-in {
  from { opacity:0; transform:translateX(-18px); }
  to   { opacity:1; transform:none; }
}
@keyframes wikify-pop {
  from { opacity:0; transform:scale(.86); }
  to   { opacity:1; transform:none; }
}
@keyframes wikify-content-in {
  from { opacity:0; transform:translateY(22px); filter:blur(4px); }
  to   { opacity:1; transform:none; filter:none; }
}
@keyframes wikify-content-out {
  from { opacity:1; transform:translateY(0); filter:none; }
  to   { opacity:0; transform:translateY(-12px); filter:blur(3px); }
}
@keyframes wikify-glow {
  0%,100% { box-shadow:0 0 0 rgba(192,132,252,0); }
  50% { box-shadow:0 0 36px rgba(192,132,252,.28); }
}
body.fx-pending .sb{animation:wikify-slide-in .55s cubic-bezier(.2,.8,.2,1) both}
body.fx-pending .brand-mark{animation:wikify-pop .5s cubic-bezier(.2,.9,.2,1.2) .06s both}
body.fx-pending .brand-name,
body.fx-pending .brand-tag{animation:wikify-rise .4s ease .12s both}
body.fx-pending .project-card{animation:wikify-rise .42s ease .18s both}
body.fx-pending .sb-search{animation:wikify-rise .38s ease .24s both}
body.fx-pending .cat{animation:wikify-slide-in .4s ease both}
body.fx-pending .cat:nth-child(1){animation-delay:.28s}
body.fx-pending .cat:nth-child(2){animation-delay:.32s}
body.fx-pending .cat:nth-child(3){animation-delay:.36s}
body.fx-pending .cat:nth-child(4){animation-delay:.4s}
body.fx-pending .cat:nth-child(5){animation-delay:.44s}
body.fx-pending .cat:nth-child(n+6){animation-delay:.48s}
body.fx-pending .topbar{animation:wikify-rise .4s ease .1s both}
body.fx-pending .paper{animation:wikify-rise .55s cubic-bezier(.2,.8,.2,1) .16s both}
body.fx-pending .content h1{animation:wikify-rise .45s ease .28s both}
body.fx-pending .src-panel{animation:wikify-rise .4s ease .32s both}
body.fx-pending .pager a{animation:wikify-rise .35s ease .38s both}
body.fx-pending .page-foot{animation:wikify-rise .3s ease .48s both}
body.fx-pending .nav-a.on{animation:wikify-glow .9s ease .55s 1 both}
.content.page-out{animation:wikify-content-out .18s ease forwards}
.content.page-in{animation:wikify-content-in .42s cubic-bezier(.2,.8,.2,1) both}
.paper.page-flash{animation:wikify-glow .5s ease 1}
.nav-a.nav-flash{animation:wikify-glow .55s ease 1}
@media (prefers-reduced-motion: reduce){
  .project-meta .dot{animation:none!important}
  #fx-canvas,.cursor-glow{display:none}
}
@media (max-width:900px){
  .sb{display:none}
  .page-wrap{padding:1rem}
  .paper{padding:1.35rem 1.15rem 1.8rem;border-radius:16px}
  .pager{grid-template-columns:1fr}
  #fx-canvas,.cursor-glow{display:none}
}
</style>
</head>
<body class="fx-pending">
<div class="progress" id="progress"></div>
<div class="theme-flash" id="theme-flash" aria-hidden="true"></div>
<aside class="sb">
  <div class="sb-head">
    <div class="brand-row">
      <div class="brand-mark" title="Symon">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 2L3 7v5c0 5.25 3.6 10.15 9 11.35C17.4 22.15 21 17.25 21 12V7l-9-5zm0 3.2l6 3.33v3.47c0 3.9-2.54 7.55-6 8.55-3.46-1-6-4.65-6-8.55V8.53l6-3.33z"/></svg>
      </div>
      <div class="brand-copy">
        <div class="brand-name">SYMON</div>
        <div class="brand-tag">Wikify · Docs</div>
      </div>
    </div>
    <div class="project-card">
      <div class="project-name">{{.ProjectName}}</div>
      <div class="project-meta"><span class="dot"></span><span>{{if .SourceLabel}}{{.SourceLabel}}{{else}}Wiki Live{{end}}</span></div>
    </div>
  </div>
  <div class="sb-search"><input id="nav-filter" type="search" placeholder="搜索页面  /" autocomplete="off"></div>
  <nav class="sb-nav" id="sb-nav">
    {{- range .Categories}}
    <div class="cat" data-cat data-label="{{.Label}}" data-track="{{.Track}}">
      <button type="button" class="cat-toggle" aria-expanded="false">
        <span class="chev">&#9654;</span>
        <span class="cat-idx">{{printf "%02d" .Index}}</span>
        <span class="cat-name">{{.Label}}</span>
        {{- if .Track}}<span class="cat-track t-{{.Track}}">{{.Track}}</span>{{end}}
        <span class="cat-count">{{len .Items}}</span>
      </button>
      <div class="cat-items">
      {{- range .Items}}
      {{- $slug := .Slug}}
      <a href="{{if $.StaticLinks}}{{$slug}}.html{{else}}/{{$slug}}{{end}}"
         class="nav-a{{if eq $.ActiveSlug $slug}} on{{end}}"
         title="{{printf "%02d · %s" .Index .Title}}" data-title="{{.Title}}" data-index="{{.Index}}">
        <span class="nav-num">{{printf "%02d" .Index}}</span>
        <span class="nav-title">{{.Title}}</span>
      </a>
      {{- end}}
      </div>
    </div>
    {{- end}}
  </nav>
  <div class="sb-foot">
    <div class="foot-actions">
      <button class="icon-btn" id="theme-btn" type="button" title="切换主题"><span class="theme-ico" id="theme-ico">◐</span> 主题</button>
      <button class="icon-btn" id="top-btn" type="button" title="回到顶部">↑ 顶部</button>
    </div>
    <div class="powered"><span class="bolt">⚡</span> Powered by <span class="symon">Symon</span></div>
  </div>
</aside>
<div class="main">
  <div class="topbar">
    <div class="topbar-brand" title="Symon Wikify">
      <span class="mini-mark"><svg viewBox="0 0 24 24"><path d="M12 2L3 7v5c0 5.25 3.6 10.15 9 11.35C17.4 22.15 21 17.25 21 12V7l-9-5z"/></svg></span>
      <span class="mini-name">SYMON</span>
    </div>
    <div class="topbar-path">
      <span>{{.ProjectName}}</span>
      {{- if .Section}}<span class="topbar-sep">/</span><span>{{.Section}}</span>{{end}}
      <span class="topbar-sep">/</span>
      <span class="topbar-cur">{{.Title}}</span>
    </div>
    <div class="topbar-actions">
      <div class="topsearch" id="topsearch">
        <input id="site-search" type="search" placeholder="全文搜索…" autocomplete="off">
        <div class="search-pop" id="search-pop"></div>
      </div>
      <button class="pill" id="theme-btn-2" type="button"><span class="theme-ico" id="theme-ico-2">◐</span> 主题</button>
    </div>
  </div>
  <div class="scroll" id="scroll">
    <div class="page-wrap">
      <article class="paper">
        <div class="content">
          {{- if .Content}}
            {{.Content}}
          {{- else}}
          <div class="empty">
            <div class="empty-icon"><svg viewBox="0 0 24 24"><path d="M12 2L3 7v5c0 5.25 3.6 10.15 9 11.35C17.4 22.15 21 17.25 21 12V7l-9-5z"/></svg></div>
            <h2>页面尚未生成</h2>
            <p>请先运行 <code>wikify generate</code> 生成该页面内容。</p>
          </div>
          {{- end}}
        </div>
      </article>
      <nav class="pager" id="pager">
        <a class="prev disabled" id="prev-link" href="#"><span class="lbl">← 上一篇</span><span class="ttl" id="prev-title">—</span></a>
        <a class="next disabled" id="next-link" href="#"><span class="lbl">下一篇 →</span><span class="ttl" id="next-title">—</span></a>
      </nav>
      <div class="page-foot">Crafted with <span class="symon">Symon</span> Wikify</div>
    </div>
  </div>
</div>
<script>window.WIKIFY_STATIC={{if .StaticLinks}}"static"{{else}}"/static"{{end}};</script>
<script>
(function(){
  var root=document.documentElement;
  var body=document.body;
  var staticBase = (typeof window.WIKIFY_STATIC === 'string' && window.WIKIFY_STATIC) ? window.WIKIFY_STATIC : '/static';
  var isStatic = {{if .StaticLinks}}true{{else}}false{{end}};
  var projectName = {{printf "%q" .ProjectName}};
  var saved=localStorage.getItem('wikify-theme');
  if(saved==='light'||saved==='dark') root.setAttribute('data-theme',saved);
  var mermaidReady = false;
  var mermaidLoading = null;
  var navigating = false;


  function toggleTheme(){
    var next=root.getAttribute('data-theme')==='dark'?'light':'dark';
    root.setAttribute('data-theme',next);
    localStorage.setItem('wikify-theme',next);
    document.querySelectorAll('.theme-ico').forEach(function(ico){
      ico.style.transition='transform .35s ease';
      ico.style.transform='rotate(360deg)';
      setTimeout(function(){ ico.style.transition=''; ico.style.transform=''; }, 360);
    });
    var flash=document.getElementById('theme-flash');
    if(flash){
      flash.style.transition='opacity .12s ease';
      flash.style.background = next==='light' ? 'rgba(255,255,255,.55)' : 'rgba(3,5,12,.55)';
      flash.style.opacity = '.35';
      setTimeout(function(){
        flash.style.opacity = '0';
        setTimeout(function(){ flash.style.transition=''; flash.style.background=''; }, 140);
      }, 120);
    }
  }
  var tb=document.getElementById('theme-btn');
  var tb2=document.getElementById('theme-btn-2');
  if(tb) tb.addEventListener('click',toggleTheme);
  if(tb2) tb2.addEventListener('click',toggleTheme);

  function ensureMermaid(){
    if(mermaidReady || (typeof mermaid !== 'undefined')){
      mermaidReady = true;
      return Promise.resolve();
    }
    if(mermaidLoading) return mermaidLoading;
    mermaidLoading = new Promise(function(resolve, reject){
      var s=document.createElement('script');
      s.src=staticBase+'/mermaid.min.js';
      s.onload=function(){
        try{
          mermaid.initialize({
            startOnLoad:false,
            theme:root.getAttribute('data-theme')!=='light'?'dark':'neutral',
            securityLevel:'loose'
          });
          mermaidReady = true;
          resolve();
        }catch(e){ reject(e); }
      };
      s.onerror=function(){ mermaidLoading=null; reject(new Error('mermaid load failed')); };
      document.head.appendChild(s);
    });
    return mermaidLoading;
  }

  function processMermaid(rootEl){
    if(!rootEl) return;
    rootEl.querySelectorAll('pre > code.language-mermaid').forEach(function(el){
      var wrap=document.createElement('div');wrap.className='mermaid-wrap';
      var div=document.createElement('div');div.className='mermaid';div.textContent=el.textContent;
      wrap.appendChild(div);el.parentElement.replaceWith(wrap);
    });
    var nodes = rootEl.querySelectorAll('.mermaid');
    if(!nodes.length) return;
    ensureMermaid().then(function(){
      if(typeof mermaid === 'undefined') return;
      // re-theme if needed
      try{
        mermaid.initialize({
          startOnLoad:false,
          theme:root.getAttribute('data-theme')!=='light'?'dark':'neutral',
          securityLevel:'loose'
        });
      }catch(e){}
      if(mermaid.run){
        mermaid.run({nodes: Array.prototype.slice.call(nodes)}).catch(function(){});
      } else if(mermaid.init){
        mermaid.init(undefined, nodes);
      }
    }).catch(function(){});
  }

  function setCatOpen(cat, open){
    if(!cat) return;
    cat.classList.toggle('open', !!open);
    var b=cat.querySelector('.cat-toggle');
    if(b) b.setAttribute('aria-expanded', open ? 'true' : 'false');
  }
  // Accordion: at most one category expanded (plus temporary opens during search).
  function openOnlyCat(cat){
    document.querySelectorAll('.cat[data-cat]').forEach(function(c){
      setCatOpen(c, c === cat);
    });
  }
  document.querySelectorAll('.cat-toggle').forEach(function(btn){
    btn.addEventListener('click', function(){
      var cat=btn.closest('.cat');
      if(!cat) return;
      var open=!cat.classList.contains('open');
      if(open) openOnlyCat(cat);
      else setCatOpen(cat, false);
    });
  });

  function getLinks(){ return Array.from(document.querySelectorAll('.nav-a')); }
  function currentIndex(){
    var links=getLinks();
    return links.findIndex(function(a){return a.classList.contains('on');});
  }
  function linkSlug(a){
    if(!a) return '';
    try{
      var u = new URL(a.href, location.href);
      if(u.origin !== location.origin) return '';
      var path = u.pathname.replace(/^\//,'');
      if(path.endsWith('.html')) path = path.slice(0,-5);
      if(path.endsWith('.md')) path = path.slice(0,-3);
      return decodeURIComponent(path);
    }catch(e){
      return (a.getAttribute('href')||'').replace(/^\//,'').replace(/\.html$/,'').replace(/\.md$/,'').split('#')[0];
    }
  }
  // Returns the URL fragment (including leading #) from a link, or ''.
  function linkHash(a){
    if(!a) return '';
    try{
      return new URL(a.href, location.href).hash || '';
    }catch(e){
      var href=a.getAttribute('href')||'';
      var i=href.indexOf('#');
      return i>=0 ? href.slice(i) : '';
    }
  }

  function setActiveNav(slug){
    var activeCat = null;
    getLinks().forEach(function(a){
      var on = linkSlug(a) === slug;
      a.classList.toggle('on', on);
      if(on) activeCat = a.closest('.cat');
    });
    // Always keep only the current page's category open (default collapsed).
    openOnlyCat(activeCat);
  }

  function updatePager(){
    var links=getLinks();
    var cur=currentIndex();
    var pl=document.getElementById('prev-link');
    var nl=document.getElementById('next-link');
    function linkLabel(a){
      if(!a) return '—';
      var n=a.getAttribute('data-index');
      var t=a.getAttribute('data-title')||a.textContent||'';
      return n ? (String(n).padStart(2,'0')+' · '+t) : t;
    }
    if(pl){
      if(cur>0){
        pl.href=links[cur-1].href; pl.classList.remove('disabled');
        document.getElementById('prev-title').textContent=linkLabel(links[cur-1]);
      } else {
        pl.href='#'; pl.classList.add('disabled');
        document.getElementById('prev-title').textContent='—';
      }
    }
    if(nl){
      if(cur>=0 && cur<links.length-1){
        nl.href=links[cur+1].href; nl.classList.remove('disabled');
        document.getElementById('next-title').textContent=linkLabel(links[cur+1]);
      } else {
        nl.href='#'; nl.classList.add('disabled');
        document.getElementById('next-title').textContent='—';
      }
    }
  }

  function updateChrome(title, section){
    document.title = (title||'Wiki') + ' · ' + (projectName||'wikify');
    var path = document.querySelector('.topbar-path');
    if(path){
      var html = '<span>'+escapeHtml(projectName||'')+'</span>';
      if(section){
        html += '<span class="topbar-sep">/</span><span>'+escapeHtml(section)+'</span>';
      }
      html += '<span class="topbar-sep">/</span><span class="topbar-cur">'+escapeHtml(title||'')+'</span>';
      path.innerHTML = html;
    }
  }
  function escapeHtml(s){
    return String(s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
  }

  var sc=document.getElementById('scroll');
  var bar=document.getElementById('progress');
  if(bar){
    bar.style.transformOrigin = '0 50%';
    bar.style.transition = 'transform .12s ease-out';
  }
  function updateProgress(){
    if(!sc||!bar) return;
    var max=sc.scrollHeight-sc.clientHeight;
    var v = max>0 ? (sc.scrollTop/max) : 0;
    bar.style.transform = 'scaleX('+v+')';
  }
  if(sc){
    var ticking=false;
    sc.addEventListener('scroll',function(){
      if(!ticking){ ticking=true; requestAnimationFrame(function(){ updateProgress(); ticking=false; }); }
    },{passive:true});
    updateProgress();
  }
  var topBtn=document.getElementById('top-btn');
  if(topBtn&&sc) topBtn.addEventListener('click',function(){
    sc.scrollTo({top:0,behavior:'smooth'});
  });

  function animateContentIn(el){
    if(!el) return;
    el.classList.remove('page-out');
    // force reflow so page-in restarts every navigation
    void el.offsetWidth;
    el.classList.add('page-in');
    var paper = document.querySelector('.paper');
    if(paper){
      paper.classList.remove('page-flash');
      void paper.offsetWidth;
      paper.classList.add('page-flash');
    }
  }

  function applyPage(data, slug, push, fragment){
    var contentEl = document.querySelector('.content');
    if(!contentEl) return;
    var doSwap = function(){
      contentEl.classList.remove('page-out');
      contentEl.innerHTML = data.content || '<div class="empty"><h2>页面尚未生成</h2></div>';
      processMermaid(contentEl);
      setActiveNav(slug);
      updateChrome(data.title, data.section);
      updatePager();
      if(sc) sc.scrollTop = 0;
      if(fragment){
        var fid = decodeURIComponent(fragment.replace(/^#/,''));
        setTimeout(function(){
          var el = document.getElementById(fid);
          if(el) el.scrollIntoView({behavior:'smooth',block:'start'});
        }, 80);
      }
      updateProgress();
      animateContentIn(contentEl);
      var active = document.querySelector('.nav-a.on');
      if(active){
        active.scrollIntoView({block:'nearest'});
        active.classList.remove('nav-flash');
        void active.offsetWidth;
        active.classList.add('nav-flash');
      }
      if(push){
        try{ history.pushState({slug:slug,fragment:fragment||''}, data.title||'', '/'+slug+(fragment||'')); }catch(e){}
      } else {
        try{ history.replaceState({slug:slug,fragment:fragment||''}, data.title||'', '/'+slug+(fragment||'')); }catch(e){}
      }
      navigating = false;
    };
    // CSS out → swap → CSS in
    contentEl.classList.remove('page-in');
    contentEl.classList.add('page-out');
    setTimeout(doSwap, 160);
  }

  function navigateTo(slug, push, fragment){
    if(!slug || navigating) return;
    if(isStatic){
      // static export has no /api/page — full navigation
      location.href = slug + '.html' + (fragment||'');
      return;
    }
    var cur = document.querySelector('.nav-a.on');
    if(cur && linkSlug(cur) === slug && push !== false){
      // same page — just scroll to fragment
      if(fragment && sc){
        var fid = decodeURIComponent(fragment.replace(/^#/,''));
        var el = document.getElementById(fid);
        if(el) el.scrollIntoView({behavior:'smooth',block:'start'});
      }
      return;
    }
    navigating = true;
    fetch('/api/page/'+encodeURIComponent(slug), {headers:{'Accept':'application/json'}})
      .then(function(r){
        if(!r.ok) throw new Error('status '+r.status);
        return r.json();
      })
      .then(function(data){ applyPage(data, slug, push !== false, fragment||''); })
      .catch(function(){
        navigating = false;
        location.href = '/'+slug+(fragment||'');
      });
  }

  // Intercept sidebar + pager + in-content wiki links (live mode only)
  if(!isStatic){
    document.addEventListener('click', function(e){
      var a = e.target.closest && e.target.closest('a.nav-a, #prev-link, #next-link, .content a');
      if(!a) return;
      if(a.classList.contains('disabled')) { e.preventDefault(); return; }
      if(e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return;
      var href = a.getAttribute('href') || '';
      if(!href || /^(https?:|mailto:|file:|javascript:)/i.test(href)) return;
      // pure in-page anchor (#section) — scroll inside #scroll container
      if(href.charAt(0)==='#'){
        e.preventDefault();
        var fid = decodeURIComponent(href.slice(1));
        var el = document.getElementById(fid);
        if(el && sc) el.scrollIntoView({behavior:'smooth',block:'start'});
        try{ history.pushState(null,'',location.pathname+href); }catch(ex){}
        return;
      }
      var slug = linkSlug(a);
      if(!slug || slug === '#') return;
      if(slug.indexOf('static/')===0) return;
      e.preventDefault();
      navigateTo(slug, true, linkHash(a));
    });
    window.addEventListener('popstate', function(ev){
      var slug = (ev.state && ev.state.slug) || linkSlug({getAttribute:function(){return location.pathname;}}) || location.pathname.replace(/^\//,'');
      var fragment = (ev.state && ev.state.fragment) || location.hash || '';
      if(slug) navigateTo(slug, false, fragment);
    });
  }

  document.addEventListener('keydown',function(e){
    if(e.target.tagName==='INPUT'||e.target.tagName==='TEXTAREA')return;
    var links=getLinks();
    var cur=currentIndex();
    if(e.key==='ArrowRight'&&cur<links.length-1){
      e.preventDefault();
      if(isStatic) location.href=links[cur+1].href;
      else navigateTo(linkSlug(links[cur+1]), true);
    }
    if(e.key==='ArrowLeft'&&cur>0){
      e.preventDefault();
      if(isStatic) location.href=links[cur-1].href;
      else navigateTo(linkSlug(links[cur-1]), true);
    }
    if(e.key==='/'&&!e.ctrlKey&&!e.metaKey){
      e.preventDefault();
      var f=document.getElementById('nav-filter');
      if(f) f.focus();
    }
  });

  var filter=document.getElementById('nav-filter');
  if(filter){
    filter.addEventListener('input',function(){
      var q=(filter.value||'').trim().toLowerCase();
      document.querySelectorAll('[data-cat]').forEach(function(cat){
        var any=false;
        cat.querySelectorAll('.nav-a').forEach(function(a){
          var title=(a.getAttribute('data-title')||'');
          var num=(a.getAttribute('data-index')||'');
          var hit=!q||title.toLowerCase().indexOf(q)>=0||num.indexOf(q)>=0||
            (a.textContent||'').toLowerCase().indexOf(q)>=0;
          a.style.display=hit?'':'none';
          if(hit) any=true;
        });
        cat.style.display=any?'':'none';
        if(q && any) setCatOpen(cat, true);
        if(!q){
          // leave collapsed; re-apply current-only after filter cleared
        }
      });
      if(!q){
        var on = document.querySelector('.nav-a.on');
        openOnlyCat(on ? on.closest('.cat') : null);
      }
    });
  }

  // Default: all collapsed except the category that owns the active page.
  var activeLink=document.querySelector('.nav-a.on');
  openOnlyCat(activeLink ? activeLink.closest('.cat') : null);
  processMermaid(document.querySelector('.content'));
  updatePager();
  if(activeLink) activeLink.scrollIntoView({block:'nearest'});
  try{
    var initSlug = linkSlug(activeLink) || location.pathname.replace(/^\//,'');
    history.replaceState({slug:initSlug}, document.title, location.pathname);
  }catch(e){}

  function escHtml(s){
    return String(s==null?'':s).replace(/[&<>"']/g,function(c){
      return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];
    });
  }

  // ── NAV-4: full-text search over /api/search (live mode only) ──
  var searchWrap=document.getElementById('topsearch');
  var searchInput=document.getElementById('site-search');
  var searchPop=document.getElementById('search-pop');
  function closeSearch(){ if(searchPop){ searchPop.classList.remove('open'); searchPop.innerHTML=''; } }
  if(isStatic && searchWrap){ searchWrap.style.display='none'; }
  if(!isStatic && searchInput && searchPop){
    var searchTimer=null, searchSeq=0;
    var runSearch=function(){
      var q=(searchInput.value||'').trim();
      if(!q){ closeSearch(); return; }
      var seq=++searchSeq;
      fetch('/api/search?q='+encodeURIComponent(q),{headers:{'Accept':'application/json'}})
        .then(function(r){ if(!r.ok) throw new Error('status '+r.status); return r.json(); })
        .then(function(items){
          if(seq!==searchSeq) return;
          if(!items || !items.length){
            searchPop.innerHTML='<div class="search-empty">无结果</div>';
            searchPop.classList.add('open');
            return;
          }
          searchPop.innerHTML=items.map(function(it){
            return '<a class="search-item" href="/'+encodeURIComponent(it.slug)+'" data-slug="'+escHtml(it.slug)+'">'
              +'<span class="si-title">'+escHtml(it.title)+'</span>'
              +(it.section?'<span class="si-sec">'+escHtml(it.section)+'</span>':'')
              +(it.snippet?'<div class="si-snip">'+escHtml(it.snippet)+'</div>':'')
              +'</a>';
          }).join('');
          searchPop.classList.add('open');
        })
        .catch(function(){ closeSearch(); });
    };
    searchInput.addEventListener('input',function(){
      clearTimeout(searchTimer);
      searchTimer=setTimeout(runSearch,160);
    });
    searchInput.addEventListener('keydown',function(e){
      if(e.key==='Escape'){ closeSearch(); searchInput.blur(); }
      if(e.key==='Enter'){
        var first=searchPop.querySelector('.search-item');
        if(first){
          e.preventDefault();
          var slug=first.getAttribute('data-slug');
          closeSearch(); searchInput.value='';
          navigateTo(slug, true);
        }
      }
    });
    searchPop.addEventListener('click',function(e){
      var a=e.target.closest && e.target.closest('.search-item');
      if(!a) return;
      e.preventDefault(); e.stopPropagation();
      var slug=a.getAttribute('data-slug');
      closeSearch(); searchInput.value='';
      navigateTo(slug, true);
    });
    document.addEventListener('click',function(e){
      if(searchWrap && !searchWrap.contains(e.target)) closeSearch();
    });
  }

  // ── NAV-5: source-chip popover — other pages citing the same file.
  // Fetches /api/file-pages once; empty map (old wikis) keeps chips inert. ──
  var filePages=null, filePagesLoading=false, filePagesQueue=[];
  var srcPop=null;
  function closeSrcPop(){
    if(srcPop && srcPop.parentNode) srcPop.parentNode.removeChild(srcPop);
    srcPop=null;
  }
  function loadFilePages(cb){
    if(filePages){ cb(filePages); return; }
    filePagesQueue.push(cb);
    if(filePagesLoading) return;
    filePagesLoading=true;
    fetch('/api/file-pages',{headers:{'Accept':'application/json'}})
      .then(function(r){ if(!r.ok) throw new Error('status '+r.status); return r.json(); })
      .then(function(map){ filePages=(map && typeof map==='object')?map:{}; })
      .catch(function(){ filePages={}; })
      .then(function(){
        var q=filePagesQueue; filePagesQueue=[];
        q.forEach(function(fn){ try{ fn(filePages); }catch(err){} });
      });
  }
  function showSrcPop(chip, path){
    loadFilePages(function(map){
      var refs=(map && map[path])||[];
      closeSrcPop();
      if(!refs.length) return; // no index data → graceful no-op
      var cur=location.pathname.replace(/^\//,'');
      var others=refs.filter(function(rf){ return rf.slug!==cur; });
      srcPop=document.createElement('div');
      srcPop.className='src-pop';
      var inner='<div class="sp-head">'+escHtml(path)+'</div>';
      if(others.length){
        inner+=others.map(function(rf){
          return '<a href="/'+encodeURIComponent(rf.slug)+'" data-slug="'+escHtml(rf.slug)+'">'+escHtml(rf.title)+'</a>';
        }).join('');
      }else{
        inner+='<div class="sp-none">没有其他页面引用此文件</div>';
      }
      srcPop.innerHTML=inner;
      document.body.appendChild(srcPop);
      var r=chip.getBoundingClientRect();
      var left=Math.max(8, Math.min(r.left, window.innerWidth-320));
      var top=r.bottom+8;
      if(top+srcPop.offsetHeight>window.innerHeight-8){
        top=Math.max(8, r.top-srcPop.offsetHeight-8);
      }
      srcPop.style.top=top+'px';
      srcPop.style.left=left+'px';
      srcPop.addEventListener('click',function(e){
        var a=e.target.closest && e.target.closest('a[data-slug]');
        if(!a) return;
        e.preventDefault(); e.stopPropagation();
        var slug=a.getAttribute('data-slug');
        closeSrcPop();
        navigateTo(slug, true);
      });
    });
  }
  if(!isStatic){
    document.addEventListener('click',function(e){
      var chip=e.target.closest && e.target.closest('.src-chip[data-path]');
      if(chip){
        e.preventDefault();
        showSrcPop(chip, chip.getAttribute('data-path')||'');
        return;
      }
      if(srcPop && !srcPop.contains(e.target)) closeSrcPop();
    });
  }

  // Entrance is pure CSS on body.fx-pending; drop the flag after animations finish.
  setTimeout(function(){ body.classList.remove('fx-pending'); }, 900);

})();
</script>
</body>
</html>`
