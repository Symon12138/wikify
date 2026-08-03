// Navigation & discoverability synthesis (NAV-1..NAV-5).
//
// This file adds deterministic, LLM-free navigation artifacts on top of the
// generated pages:
//   - NAV-1: shared-evidence related links (inverted file→pages index)
//   - NAV-2: per-section index pages (content/<Section>/<Section>.md)
//   - NAV-3: repository architecture overview registered first in page order
//   - NAV-4: meta/search-index.json (static full-text search seed)
//   - NAV-5: meta/file-page-index.json (file → citing pages reverse index)
//
// All heuristics reuse existing generic role/structure classifiers
// (pathRoleHintLite, isNoiseDepPath, extractCitePaths) — no product-domain
// terms. Synthesized pages carry a DescriptionSlug marker so re-export
// removes and regenerates them (idempotent, never accumulating duplicates).
package export

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JSHurt/wikify/internal/models"
	"github.com/JSHurt/wikify/internal/scan"
)

// Marker prefixes stored in WikiPage.DescriptionSlug for synthesized pages
// (rulesDigestMarker lives in rules.go). All survive buildMetadata's slug
// recomputation: they are not "topic-" prefixed and the appended hash token
// keeps them out of isBareRoleSlug.
const (
	sectionIndexMarker = "section-index"
	archOverviewMarker = "arch-overview"
)

// isGeneratedNavPage reports pages synthesized by export (section index pages,
// the architecture overview, and the business-rules digest — see rules.go).
// Recognition is by DescriptionSlug marker so polish reloads of meta/wiki.json
// identify them without extra fields.
func isGeneratedNavPage(p models.WikiPage) bool {
	return strings.HasPrefix(p.DescriptionSlug, sectionIndexMarker) ||
		strings.HasPrefix(p.DescriptionSlug, archOverviewMarker) ||
		strings.HasPrefix(p.DescriptionSlug, rulesDigestMarker)
}

// removeGeneratedNavPages strips previously synthesized nav pages (and their
// in-memory bodies) so every Export regenerates them from current structure.
// This is the idempotency mechanism for NAV-2/NAV-3: remove → re-synthesize
// deterministically → identical output on repeated exports.
func removeGeneratedNavPages(wiki *models.Wiki, contents map[string]string) {
	if wiki == nil || len(wiki.Pages) == 0 {
		return
	}
	out := make([]models.WikiPage, 0, len(wiki.Pages))
	for _, p := range wiki.Pages {
		if isGeneratedNavPage(p) {
			if contents != nil {
				delete(contents, p.Slug)
			}
			continue
		}
		out = append(out, p)
	}
	wiki.Pages = out
}

// ── NAV-1: shared-evidence related links ──────────────────────────────────────

// sharedEvidenceRef is one ranked shared-evidence neighbour of a page.
type sharedEvidenceRef struct {
	Page   models.WikiPage
	Shared int    // number of shared evidence files
	File   string // representative shared file (role-bearing preferred)
}

// reEvidenceCiteBlock matches the machine-shaped <cite> reference block that
// EnsurePageFormat injects (buildCiteBlock: a bold 参考文献/References header
// followed by file links). Plain LLM-authored <cite> lists carry no such
// header and are kept as genuine evidence.
var reEvidenceCiteBlock = regexp.MustCompile(`(?s)<cite>\s*\*\*(参考文献|References)\*\*.*?</cite>`)

// evidenceBody strips machine-injected regions before cite extraction so the
// evidence set is stable across polish runs: EnsurePageFormat injects a
// <cite> reference block padded from the model inventory, and the write loop
// appends a generated related-pages nav. Extracting cites from either would
// change shared-file counts on every re-export (non-idempotent feedback).
func evidenceBody(body string) string {
	if body == "" {
		return body
	}
	body = reEvidenceCiteBlock.ReplaceAllString(body, "")
	body = stripSectionByTitle(body, "相关页面")
	body = stripSectionByTitle(body, "Related pages")
	return body
}

// pageEvidenceFiles returns the sorted evidence-file set of a page:
// DependentFiles ∪ inline body cite paths (reference blocks and generated nav
// excluded — see evidenceBody), normalized (ToSlash, "./"-stripped) and
// noise-filtered via isNoiseDepPath. Shared by NAV-1 and NAV-5.
func pageEvidenceFiles(p models.WikiPage, body string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		path = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(path)), "./")
		if path == "" || seen[path] || isNoiseDepPath(path) {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	for _, d := range p.DependentFiles {
		add(d)
	}
	for _, c := range extractCitePaths(evidenceBody(body)) {
		add(c)
	}
	sort.Strings(out)
	return out
}

// buildSharedEvidence computes, per page slug, up to limit pages that share
// evidence files. Pair rule: two pages relate when they share >=2 files, or
// >=1 file carrying an architectural role signal (pathRoleHintLite != "").
// Order: shared-count desc, then title asc (slug asc as final tiebreak) —
// fully deterministic. Synthesized nav pages never participate.
func buildSharedEvidence(wiki *models.Wiki, contents map[string]string, limit int) map[string][]sharedEvidenceRef {
	res := map[string][]sharedEvidenceRef{}
	if wiki == nil || len(wiki.Pages) < 2 {
		return res
	}
	if limit <= 0 {
		limit = 5
	}
	type entry struct {
		page  models.WikiPage
		files map[string]bool
	}
	var entries []entry
	for _, p := range wiki.Pages {
		if isGeneratedNavPage(p) {
			continue
		}
		files := pageEvidenceFiles(p, contents[p.Slug])
		if len(files) == 0 {
			continue
		}
		set := make(map[string]bool, len(files))
		for _, f := range files {
			set[f] = true
		}
		entries = append(entries, entry{page: p, files: set})
	}
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			a, b := entries[i], entries[j]
			small, large := a.files, b.files
			if len(large) < len(small) {
				small, large = large, small
			}
			var shared []string
			for f := range small {
				if large[f] {
					shared = append(shared, f)
				}
			}
			if len(shared) == 0 {
				continue
			}
			sort.Strings(shared)
			rep := ""
			for _, f := range shared {
				if pathRoleHintLite(f) != "" {
					rep = f
					break
				}
			}
			if len(shared) < 2 && rep == "" {
				continue // single shared file without role signal — too weak
			}
			if rep == "" {
				rep = shared[0]
			}
			res[a.page.Slug] = append(res[a.page.Slug], sharedEvidenceRef{Page: b.page, Shared: len(shared), File: rep})
			res[b.page.Slug] = append(res[b.page.Slug], sharedEvidenceRef{Page: a.page, Shared: len(shared), File: rep})
		}
	}
	for slug, refs := range res {
		sort.SliceStable(refs, func(x, y int) bool {
			if refs[x].Shared != refs[y].Shared {
				return refs[x].Shared > refs[y].Shared
			}
			if refs[x].Page.Title != refs[y].Page.Title {
				return refs[x].Page.Title < refs[y].Page.Title
			}
			return refs[x].Page.Slug < refs[y].Page.Slug
		})
		if len(refs) > limit {
			refs = refs[:limit]
		}
		res[slug] = refs
	}
	return res
}

// ── NAV-2: section index pages ────────────────────────────────────────────────

// ensureSectionIndexPages synthesizes content/<Section>/<Section>.md index
// pages for sections with >=2 child pages and no existing page of the same
// name (LLM-authored section roots are respected). Assumes previously
// generated nav pages were already removed (removeGeneratedNavPages).
func ensureSectionIndexPages(wiki *models.Wiki, contents map[string]string, zh bool) {
	if wiki == nil || len(wiki.Pages) == 0 || contents == nil {
		return
	}
	titleExists := map[string]bool{}
	slugExists := map[string]bool{}
	pathExists := map[string]bool{}
	for _, p := range wiki.Pages {
		titleExists[p.Title] = true
		slugExists[p.Slug] = true
		if p.ContentPath != "" {
			pathExists[filepath.ToSlash(strings.TrimPrefix(p.ContentPath, "content/"))] = true
		}
	}
	var secOrder []string
	children := map[string][]models.WikiPage{}
	for _, p := range wiki.Pages {
		sec := strings.TrimSpace(p.Section)
		if sec == "" || sec == p.Title || isGeneratedNavPage(p) {
			continue
		}
		if _, ok := children[sec]; !ok {
			secOrder = append(secOrder, sec)
		}
		children[sec] = append(children[sec], p)
	}
	newBySec := map[string]models.WikiPage{}
	for _, sec := range secOrder {
		kids := children[sec]
		indexPath := sec + "/" + sec + ".md"
		if len(kids) < 2 || titleExists[sec] || pathExists[filepath.ToSlash(indexPath)] {
			continue
		}
		hash := uuidFromKey("section-index:" + sec)[:8]
		slug := "sec-" + hash
		for slugExists[slug] {
			slug += "x"
		}
		slugExists[slug] = true
		page := models.WikiPage{
			Title:           sec,
			Slug:            slug,
			Level:           "Beginner",
			Section:         sec,
			Goal:            navGoalText(sec, zh),
			ContentPath:     indexPath,
			DescriptionSlug: sectionIndexMarker + "-" + hash,
			Track:           dominantTrack(kids),
		}
		contents[slug] = sectionIndexBody(sec, kids, contents, zh)
		newBySec[sec] = page
	}
	if len(newBySec) == 0 {
		return
	}
	// Insert each index right before the first page of its section so it
	// surfaces at the top of the section's browse category.
	out := make([]models.WikiPage, 0, len(wiki.Pages)+len(newBySec))
	inserted := map[string]bool{}
	for _, p := range wiki.Pages {
		sec := strings.TrimSpace(p.Section)
		if ip, ok := newBySec[sec]; ok && !inserted[sec] {
			out = append(out, ip)
			inserted[sec] = true
		}
		out = append(out, p)
	}
	wiki.Pages = out
}

func navGoalText(sec string, zh bool) string {
	if zh {
		return "章节导航索引：" + sec
	}
	return "Section index: " + sec
}

// dominantTrack picks the most common child track, ties broken in rail order
// (foundation → business → technical). Deterministic.
func dominantTrack(pages []models.WikiPage) string {
	counts := map[string]int{}
	for _, p := range pages {
		if p.Track != "" {
			counts[p.Track]++
		}
	}
	best := ""
	bestN := 0
	for _, tr := range []string{models.TrackFoundation, models.TrackBusiness, models.TrackTechnical} {
		if counts[tr] > bestN {
			best, bestN = tr, counts[tr]
		}
	}
	return best
}

// sectionIndexBody renders the deterministic section index markdown.
// contents maps page slug → rendered markdown body; used to extract real
// first-paragraph summaries instead of the path-style Goal string.
func sectionIndexBody(sec string, kids []models.WikiPage, contents map[string]string, zh bool) string {
	titles := make([]string, 0, len(kids))
	for _, k := range kids {
		titles = append(titles, k.Title)
	}
	var b strings.Builder
	b.WriteString("# " + sec + "\n\n")
	if zh {
		b.WriteString(fmt.Sprintf("本页是「%s」章节的导航索引，共 %d 个子页面：%s。\n\n", sec, len(kids), joinNavTitles(titles, zh)))
		b.WriteString("## 子页面\n\n")
	} else {
		b.WriteString(fmt.Sprintf("This page is the navigation index for the \"%s\" section, covering %d pages: %s.\n\n", sec, len(kids), joinNavTitles(titles, zh)))
		b.WriteString("## Pages\n\n")
	}
	for _, k := range kids {
		line := fmt.Sprintf("- [%s](%s)", k.Title, linkTarget(k))
		// Prefer real first-paragraph from rendered body; fall back to Goal.
		desc := pageFirstParagraph(contents[k.Slug], 80)
		if desc == "" {
			desc = navFirstLine(k.Goal, 60)
		}
		if desc != "" {
			line += " — " + desc
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// joinNavTitles renders up to 6 titles as a one-line summary.
func joinNavTitles(titles []string, zh bool) string {
	const max = 6
	over := len(titles) > max
	if over {
		titles = titles[:max]
	}
	sep := ", "
	if zh {
		sep = "、"
	}
	s := strings.Join(titles, sep)
	if over {
		if zh {
			s += " 等"
		} else {
			s += ", etc."
		}
	}
	return s
}

// navFirstLine returns the first line of s trimmed and capped at maxRunes.
func navFirstLine(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	r := []rune(s)
	if maxRunes > 0 && len(r) > maxRunes {
		s = string(r[:maxRunes]) + "…"
	}
	return s
}

// pageFirstParagraph extracts the first meaningful prose paragraph from a
// rendered markdown page body and caps it at maxRunes. It skips:
//   - headings (# / ##…)
//   - fenced code blocks (```mermaid, ~~~ …)
//   - <cite>…</cite> reference blocks (single-line or multi-line)
//   - blank lines, list items, tables, blockquotes, horizontal rules
//
// The function is idempotent with respect to EnsurePageFormat enrichment:
// a page body that has been through cite injection, TOC, and mermaid injection
// produces the same first paragraph as the original LLM body.
func pageFirstParagraph(body string, maxRunes int) string {
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	inFence := false
	inCite := false
	var paraLines []string

	for _, raw := range lines {
		l := strings.TrimSpace(raw)

		// Fenced code / mermaid block toggle.
		if strings.HasPrefix(l, "```") || strings.HasPrefix(l, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		// <cite> block: single-line (<cite>…</cite>) or multi-line open/close.
		if strings.HasPrefix(l, "<cite") {
			if !strings.Contains(l, "</cite>") {
				inCite = true
			}
			continue
		}
		if strings.HasPrefix(l, "</cite>") {
			inCite = false
			continue
		}
		if inCite {
			continue
		}

		// Skip headings, list items, tables, blockquotes, hr.
		if l == "" {
			if len(paraLines) > 0 {
				break // blank line ends first paragraph
			}
			continue
		}
		if strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, ">") {
			continue
		}
		if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") ||
			strings.HasPrefix(l, "+ ") || (len(l) > 2 && l[0] >= '0' && l[0] <= '9' && l[1] == '.') {
			continue
		}
		if strings.HasPrefix(l, "|") {
			continue // table row
		}
		if l == "---" || l == "***" || l == "___" {
			continue // hr / frontmatter divider
		}

		paraLines = append(paraLines, l)
	}

	if len(paraLines) == 0 {
		return ""
	}
	text := strings.Join(paraLines, " ")
	r := []rune(text)
	if maxRunes > 0 && len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return text
}

// ── NAV-3: repository architecture overview ───────────────────────────────────

// ensureArchOverviewPage synthesizes the repo architecture overview page and
// registers it FIRST in wiki page order, so browse '/' (redirect to
// wiki.Pages[0].Slug) lands on it. Skipped when the model has fewer than 2
// modules or a page with the same title already exists — the current '/'
// behaviour is preserved in both cases.
func ensureArchOverviewPage(model *scan.Model, wiki *models.Wiki, contents map[string]string, zh bool) {
	if wiki == nil || contents == nil || model == nil || len(model.Modules) < 2 {
		return
	}
	diagram := repoArchitectureMermaid(model)
	if diagram == "" {
		return
	}
	title := "架构总览"
	if !zh {
		title = "Architecture Overview"
	}
	slugExists := map[string]bool{}
	for _, p := range wiki.Pages {
		if p.Title == title {
			return // respect an existing (LLM-authored) page of the same name
		}
		slugExists[p.Slug] = true
	}
	hash := uuidFromKey(archOverviewMarker)[:8]
	slug := "arch-overview"
	for slugExists[slug] {
		slug += "-x"
	}
	goal := "仓库模块结构与依赖关系总览"
	if !zh {
		goal = "Repository module structure and dependency overview"
	}
	page := models.WikiPage{
		Title:           title,
		Slug:            slug,
		Level:           "Beginner",
		Section:         title,
		Goal:            goal,
		ContentPath:     title + ".md",
		DescriptionSlug: archOverviewMarker + "-" + hash,
		Track:           models.TrackFoundation,
	}
	contents[slug] = archOverviewBody(model, title, diagram, zh)
	wiki.Pages = append([]models.WikiPage{page}, wiki.Pages...)
}

// archOverviewBody renders the deterministic architecture overview markdown:
// intro line, module dependency diagram, and a module table
// (name, file count, key entrypoints).
func archOverviewBody(model *scan.Model, title, diagram string, zh bool) string {
	name := strings.TrimSpace(model.Name)
	if name == "" {
		name = "repo"
	}
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	if zh {
		b.WriteString(fmt.Sprintf("仓库「%s」共 %d 个模块、%d 个文件。下图基于 import 关系汇总模块级依赖。\n\n", name, len(model.Modules), len(model.Files)))
		b.WriteString("## 模块依赖图\n\n")
	} else {
		b.WriteString(fmt.Sprintf("Repository \"%s\" has %d modules and %d files. The diagram below aggregates module-level dependencies from import edges.\n\n", name, len(model.Modules), len(model.Files)))
		b.WriteString("## Module dependency diagram\n\n")
	}
	b.WriteString(diagram + "\n\n")
	if zh {
		b.WriteString("## 模块清单\n\n")
		b.WriteString("| 模块 | 文件数 | 关键入口 |\n| --- | --- | --- |\n")
	} else {
		b.WriteString("## Modules\n\n")
		b.WriteString("| Module | Files | Key entrypoints |\n| --- | --- | --- |\n")
	}
	const maxRows = 30
	for i, m := range model.Modules {
		if i >= maxRows {
			break
		}
		label := strings.TrimSpace(m.Name)
		if label == "" {
			label = strings.TrimSpace(m.Path)
		}
		label = strings.ReplaceAll(label, "|", "/")
		entries := moduleEntrypoints(model, m, 2)
		ep := "—"
		if len(entries) > 0 {
			sep := ", "
			if zh {
				sep = "、"
			}
			ep = strings.Join(entries, sep)
		}
		b.WriteString(fmt.Sprintf("| `%s` | %d | %s |\n", label, len(m.Files), ep))
	}
	b.WriteString("\n")
	return b.String()
}

// moduleEntrypoints returns up to limit entrypoint basenames under the module.
func moduleEntrypoints(model *scan.Model, m scan.ModuleSummary, limit int) []string {
	prefix := filepath.ToSlash(strings.TrimSpace(m.Path))
	if prefix == "" {
		return nil
	}
	var out []string
	for _, ep := range model.EntryPoints {
		p := filepath.ToSlash(ep.Path)
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			out = append(out, filepath.Base(p))
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// ── NAV-4: static search index ────────────────────────────────────────────────

// SearchIndexEntry is one record in meta/search-index.json.
type SearchIndexEntry struct {
	Title   string `json:"title"`
	Slug    string `json:"slug"`
	Section string `json:"section,omitempty"`
	Path    string `json:"path"`
	Excerpt string `json:"excerpt,omitempty"`
}

var (
	reNavFence = regexp.MustCompile("(?s)```.*?```")
	reNavCite  = regexp.MustCompile(`(?s)<cite>.*?</cite>`)
	reNavLink  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	reNavFile  = regexp.MustCompile(`(?i)file://[^\s)"']+`)
	reNavTag   = regexp.MustCompile(`<[^>\n]+>`)
	reNavSpace = regexp.MustCompile(`\s+`)
)

// plainTextExcerpt strips mermaid/code fences, cite blocks, links, and
// markdown syntax down to plain text capped at maxRunes (rune-safe).
func plainTextExcerpt(body string, maxRunes int) string {
	if body == "" {
		return ""
	}
	s := reNavFence.ReplaceAllString(body, " ")
	s = reNavCite.ReplaceAllString(s, " ")
	s = reNavLink.ReplaceAllString(s, "$1")
	s = reNavFile.ReplaceAllString(s, " ")
	s = reNavTag.ReplaceAllString(s, " ")
	s = strings.NewReplacer("#", " ", "*", " ", "`", " ", ">", " ", "|", " ").Replace(s)
	s = strings.TrimSpace(reNavSpace.ReplaceAllString(s, " "))
	r := []rune(s)
	if maxRunes > 0 && len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return s
}

// writeSearchIndex writes meta/search-index.json for static hosting / browse.
func writeSearchIndex(metaDir string, wiki *models.Wiki, contents map[string]string) error {
	if wiki == nil {
		return nil
	}
	entries := make([]SearchIndexEntry, 0, len(wiki.Pages))
	for _, p := range wiki.Pages {
		entries = append(entries, SearchIndexEntry{
			Title:   p.Title,
			Slug:    p.Slug,
			Section: p.Section,
			Path:    linkTarget(p),
			Excerpt: plainTextExcerpt(contents[p.Slug], 240),
		})
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(metaDir, "search-index.json"), b, 0o644)
}

// ── NAV-5: file → citing-pages reverse index ──────────────────────────────────

// FilePageRef is one citing page in meta/file-page-index.json.
type FilePageRef struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
	Path  string `json:"path"`
}

// writeFilePageIndex writes meta/file-page-index.json mapping every evidence
// file (DependentFiles ∪ body cite paths, noise-filtered) to the pages that
// reference it. Consumed by browse source-chip popovers; harmless when absent.
func writeFilePageIndex(metaDir string, wiki *models.Wiki, contents map[string]string) error {
	if wiki == nil {
		return nil
	}
	idx := map[string][]FilePageRef{}
	for _, p := range wiki.Pages {
		if isGeneratedNavPage(p) {
			continue
		}
		for _, f := range pageEvidenceFiles(p, contents[p.Slug]) {
			idx[f] = append(idx[f], FilePageRef{Title: p.Title, Slug: p.Slug, Path: linkTarget(p)})
		}
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(metaDir, "file-page-index.json"), b, 0o644)
}
