// Package export writes the final wiki layout under .wikify/.
package export

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Symon12138/wikify/internal/evidence"
	"github.com/Symon12138/wikify/internal/models"
	"github.com/Symon12138/wikify/internal/scan"
)

// ExportOptions controls Wiki export layout.
type ExportOptions struct {
	Lang string // zh | en
	// GraphFile optional external graph JSON path forwarded to scan.Options.
	GraphFile string
}

// Export writes the final wiki under .wikify/{content,meta}/ (single deliverable).
func Export(workDir string, model *scan.Model, wiki *models.Wiki, pageContents map[string]string, opts ExportOptions) error {
	if wiki == nil {
		return fmt.Errorf("nil wiki")
	}
	// Always materialise tracks before any JSON / browse / metadata write.
	// Free LLM catalogs and older draft wiki.json often omit track entirely.
	EnsureTracks(wiki)
	// Do NOT MergeEngineeringSeeds here. Export runs after page generation; adding
	// catalog pages without draft bodies produced honest thin stubs and forced a
	// second --only-stubs pass. Engineering seeds are merged in runner.buildCatalog
	// (before LLM write) so a single generate covers them. Polish only re-formats
	// pages that already exist in wiki.json.
	// Re-bind dependent_files when scan inventory is available (polish + generate).
	// Older generates over-bound pom.xml; evidence scoring now prefers service layers.
	rebindDependentFiles(model, wiki)
	// QL-2: validate + heal file:// cite paths BEFORE dependent-file enrichment
	// so hallucinated paths cannot leak into DependentFiles / diagrams / the
	// title bridge. Counters feed meta/quality-report.json (QL-4). Skipped
	// internally when the model has no file inventory.
	qstats := map[string]pageQuality{}
	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		body := pageContents[p.Slug]
		if body == "" {
			continue
		}
		fixedBody, total, valid, corrected, dropped := ValidateCitePaths(body, model)
		pageContents[p.Slug] = fixedBody
		qstats[p.Slug] = pageQuality{citesTotal: total, citesValid: valid, citesCorrected: corrected, citesDropped: dropped}
	}
	// When binding is weak (config/xml/noise only), merge file:// cites already in the body
	// so code-dependency diagrams can still form a role chain from real page evidence.
	enrichDependentFilesFromBody(wiki, pageContents)
	// Ensure every page has a ContentPath before write.
	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		if p.ContentPath == "" {
			if p.Section != "" && p.Section != p.Title {
				p.ContentPath = p.Section + "/" + p.Title + ".md"
			} else {
				p.ContentPath = p.Title + ".md"
			}
		}
	}

	lang := opts.Lang
	if lang == "" {
		if model != nil && model.Language == "en" {
			lang = "en"
		} else {
			lang = "zh"
		}
	}

	// NAV-2/NAV-3: drop previously synthesized nav pages (polish reloads them
	// from meta/wiki.json) and regenerate them deterministically from current
	// structure. Remove-then-rebuild keeps repeated exports byte-identical.
	removeGeneratedNavPages(wiki, pageContents)
	// RD: offline business-rules digest aggregated from business-track page
	// bodies (rule-shaped statements with cite anchors). Runs after cite
	// healing above and before section indexes so its own section can never
	// spawn an index page. Regenerated each export via the shared marker
	// removal above.
	ensureBusinessRulesDigestPage(wiki, pageContents, lang == "zh")
	ensureSectionIndexPages(wiki, pageContents, lang == "zh")
	ensureArchOverviewPage(model, wiki, pageContents, lang == "zh")
	// NAV-1: shared-evidence neighbours from the file→pages inverted index
	// (DependentFiles ∪ body cites), consumed by the related-nav section below.
	shared := buildSharedEvidence(wiki, pageContents, 5)

	root := filepath.Join(workDir, ".wikify")
	contentDir := filepath.Join(root, "content")
	metaDir := filepath.Join(root, "meta")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		return err
	}

	// Write pages
	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		body := pageContents[p.Slug]
		if body == "" {
			body = "# " + p.Title + "\n\n"
		}
		var formatted string
		if isGeneratedNavPage(*p) {
			// Synthesized nav pages (section index / arch overview) keep their
			// deterministic bodies — no TOC/diagram/related-nav injection.
			formatted = body
		} else {
			// Focus = bound deps ∪ body cites (deduped). Prefer deps first for ranking.
			refs := mergeFocusPaths(p.DependentFiles, extractCitePaths(body), 12)
			formatted = EnsurePageFormat(p.Title, body, model, refs, lang == "zh")
			formatted = ensureRelatedNavShared(formatted, wiki, *p, lang == "zh", shared[p.Slug])
		}
		// QL-3: heal intra-wiki links (bare counter slugs, title/slug-addressed
		// .md targets); unresolved .md links stay as-is and count as broken.
		var brokenLinks int
		formatted, brokenLinks = rewriteWikiLinks(formatted, wiki)
		st := qstats[p.Slug]
		st.brokenWikiLinks = brokenLinks
		qstats[p.Slug] = st
		rel := p.ContentPath
		if rel == "" {
			rel = p.Title + ".md"
		}
		rel = filepath.ToSlash(rel)
		rel = strings.TrimPrefix(rel, "content/")
		out := filepath.Join(contentDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, []byte(formatted), 0o644); err != nil {
			return err
		}
		// Keep in-memory contents in sync for knowledge export stubs.
		pageContents[p.Slug] = formatted
	}

	// Metadata
	meta := buildMetadata(model, wiki, pageContents, lang)
	metaPath := filepath.Join(metaDir, "wiki-metadata.json")
	b, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(metaPath, b, 0o644); err != nil {
		return err
	}

	// Lightweight nav index for wikify browse.
	nav := buildBrowseIndex(wiki, lang)
	navPath := filepath.Join(metaDir, "browse-index.json")
	nb, _ := json.MarshalIndent(nav, "", "  ")
	if err := os.WriteFile(navPath, nb, 0o644); err != nil {
		return err
	}
	// Full catalog (slugs, dependent_files, content_path) for browse/resume tooling.
	wb, err := json.MarshalIndent(wiki, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(metaDir, "wiki.json"), wb, 0o644); err != nil {
		return err
	}
	// NAV-4: static full-text search seed consumed by browse /api/search and
	// static hosting. NAV-5: file→citing-pages reverse index for source chips.
	if err := writeSearchIndex(metaDir, wiki, pageContents); err != nil {
		return err
	}
	if err := writeFilePageIndex(metaDir, wiki, pageContents); err != nil {
		return err
	}
	// Persist scan graph so subsequent polish can reload edges without re-deriving
	// everything (Scan also auto-loads this path as an overlay).
	if model != nil {
		_ = scan.WriteGraphFile(scan.DefaultGraphPath(workDir), model)
	}
	// Generated title↔path bridge (product-agnostic) for next generate/evidence.
	writeTitleBridge(workDir, model, wiki)
	// Section-level knowledge cards (offline, no LLM) — navigation companion to content/.
	if err := writeKnowledgeCards(root, wiki, pageContents, lang); err != nil {
		return err
	}
	// QL-4: deterministic quality profile — read back by runner/polish summary.
	if err := writeQualityReport(metaDir, wiki, pageContents, model, qstats); err != nil {
		return err
	}
	// Language marker for browse source resolution.
	_ = os.WriteFile(filepath.Join(root, "lang"), []byte(lang+"\n"), 0o644)
	return nil
}

// Polish re-exports an existing .wikify tree without calling any LLM.
// It reloads meta/wiki.json + content/**, fills tracks, re-runs EnsurePageFormat
// (TOC/cite normalisation), and rewrites meta/browse indexes.
// Use after upgrading wikify when a full generate is too expensive.
func Polish(workDir string, opts ExportOptions) error {
	root := filepath.Join(workDir, ".wikify")
	wikiPath := filepath.Join(root, "meta", "wiki.json")
	data, err := os.ReadFile(wikiPath)
	if err != nil {
		return fmt.Errorf("read %s: %w (run generate first)", wikiPath, err)
	}
	var wiki models.Wiki
	if err := json.Unmarshal(data, &wiki); err != nil {
		return fmt.Errorf("parse wiki.json: %w", err)
	}
	if len(wiki.Pages) == 0 {
		return fmt.Errorf("wiki.json has no pages")
	}
	EnsureTracks(&wiki)

	contentDir := filepath.Join(root, "content")
	contents := map[string]string{}
	for _, p := range wiki.Pages {
		rel := p.ContentPath
		if rel == "" {
			rel = p.Title + ".md"
		}
		rel = strings.TrimPrefix(filepath.ToSlash(rel), "content/")
		raw, err := os.ReadFile(filepath.Join(contentDir, filepath.FromSlash(rel)))
		if err != nil {
			// try flat draft-style slug file under meta sibling drafts if present
			continue
		}
		contents[p.Slug] = string(raw)
	}
	if len(contents) == 0 {
		return fmt.Errorf("no content markdown found under %s", contentDir)
	}

	lang := opts.Lang
	if lang == "" {
		if b, err := os.ReadFile(filepath.Join(root, "lang")); err == nil {
			lang = strings.TrimSpace(string(b))
		}
	}
	if lang == "" {
		lang = "zh"
	}
	opts.Lang = lang

	// Best-effort rescan for cite line numbers; polish still works if scan fails.
	model, _ := scan.Scan(workDir, lang, scan.Options{GraphFile: opts.GraphFile})
	if model == nil {
		model = &scan.Model{Root: workDir, Name: filepath.Base(workDir), Language: lang}
	}
	if err := Export(workDir, model, &wiki, contents, opts); err != nil {
		return err
	}
	// QL-4: console digest of the quality report just written by Export.
	if rep, err := LoadQualityReport(workDir); err == nil {
		for _, ln := range rep.Summary() {
			fmt.Println(ln)
		}
	}
	return nil
}

// BrowseIndex is the page list used by wikify browse for exported content.
type BrowseIndex struct {
	Lang  string            `json:"lang"`
	Pages []BrowseIndexPage `json:"pages"`
}

// BrowseIndexPage is one navigable page under content/.
type BrowseIndexPage struct {
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Section     string `json:"section,omitempty"`
	Track       string `json:"track,omitempty"` // foundation | business | technical
	ContentPath string `json:"content_path"`    // relative under content/
}

// EnsureTracks fills empty/invalid Track on every page via models.InferTrack.
// Called at export boundary so meta/browse always carry dual-rail labels even when
// catalog drafts were produced by older binaries.
func EnsureTracks(wiki *models.Wiki) {
	if wiki == nil {
		return
	}
	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		// Always re-infer: InferTrack preserves valid rails except foundation promotion.
		p.Track = models.InferTrack(*p)
	}
}


// extractCitePaths collects repository-relative paths from file:// links in body.
func extractCitePaths(body string) []string {
	if body == "" {
		return nil
	}
	re := regexp.MustCompile(`(?i)file://([^\s\)#"']+)`)
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if len(m) < 2 {
			continue
		}
		p := filepath.ToSlash(strings.TrimSpace(m[1]))
		// Drop line anchors if any leaked.
		if i := strings.IndexByte(p, '#'); i >= 0 {
			p = p[:i]
		}
		p = strings.TrimPrefix(p, "./")
		if p == "" || seen[p] {
			continue
		}
		// Skip pure noise seeds for focus (html/md + universal config/log/editor).
		if isNoiseDepPath(p) {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) >= 24 {
			break
		}
	}
	return out
}

// focusRank ranks a path for merge: lower is better.
// 0 role-bearing code, 1 other code, 2 useful non-code (mapper xml…), 3 noise/config.
func focusRank(path string) int {
	if isNoiseDepPath(path) {
		return 3
	}
	if pathRoleHintLite(path) != "" {
		return 0
	}
	if isCodeishPath(path) {
		return 1
	}
	lower := strings.ToLower(filepath.ToSlash(path))
	// Mapper / mybatis SQL bindings are useful for role chains even without stem role.
	if strings.HasSuffix(lower, ".xml") && (strings.Contains(lower, "mapper") || strings.Contains(lower, "/dao/")) {
		return 1
	}
	return 2
}

// mergeFocusPaths merges primary then secondary, but ranks by architectural signal
// so weak config/log/editor deps cannot crowd out role-bearing code cites.
// Within the same rank, paths that share a mined topic family (recurring non-role
// identifier tokens across the candidate set) outrank orphans — still product-agnostic.
// Then primary order is preserved ahead of secondary.
func mergeFocusPaths(primary, secondary []string, limit int) []string {
	if limit <= 0 {
		limit = 12
	}
	type cand struct {
		path   string
		rank   int
		order  int
		pri    int // 0 primary, 1 secondary
		family int // 1 if hits mined family, else 0
	}
	seen := map[string]bool{}
	var list []cand
	var allPaths []string
	add := func(paths []string, pri int) {
		for _, p := range paths {
			p = filepath.ToSlash(strings.TrimSpace(p))
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			allPaths = append(allPaths, p)
			list = append(list, cand{path: p, rank: focusRank(p), order: len(list), pri: pri})
		}
	}
	add(primary, 0)
	add(secondary, 1)
	family := familyTokensFromPaths(allPaths)
	for i := range list {
		if pathHitsFamily(list[i].path, family) {
			list[i].family = 1
		}
	}
	// Stable quality sort: rank → family hit → primary-first → original order.
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			a, b := list[i], list[j]
			better := b.rank < a.rank ||
				(b.rank == a.rank && b.family > a.family) ||
				(b.rank == a.rank && b.family == a.family && b.pri < a.pri) ||
				(b.rank == a.rank && b.family == a.family && b.pri == a.pri && b.order < a.order)
			if better {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	out := make([]string, 0, limit)
	for _, c := range list {
		// Drop pure noise unless nothing better fills the budget.
		if c.rank >= 3 && len(out) > 0 {
			continue
		}
		out = append(out, c.path)
		if len(out) >= limit {
			break
		}
	}
	// If everything was noise, still return a few so callers are not empty.
	if len(out) == 0 {
		for _, c := range list {
			out = append(out, c.path)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// pathRoleHintLite returns a coarse architectural role for a path.
// Basename first, then package path segments (/controller/, /po/, …).
func pathRoleHintLite(path string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	switch {
	case strings.Contains(stem, "controller") || strings.HasSuffix(stem, "control") ||
		strings.HasSuffix(stem, "cont") || // ExtJS *Cont.js
		strings.Contains(stem, "handler") || strings.Contains(stem, "resource") ||
		strings.Contains(stem, "servlet") || strings.HasSuffix(stem, "action"):
		return "ctrl"
	case strings.Contains(stem, "service") || strings.HasSuffix(stem, "svc") ||
		strings.HasSuffix(stem, "manager") || strings.HasSuffix(stem, "mgr"):
		return "svc"
	case strings.Contains(stem, "dao") || strings.Contains(stem, "mapper") ||
		strings.Contains(stem, "repository") || strings.HasSuffix(stem, "repo"):
		return "dao"
	case strings.Contains(stem, "entity") || strings.HasSuffix(stem, "po") ||
		strings.HasSuffix(stem, "pojo") || strings.Contains(stem, "model") ||
		strings.Contains(stem, "dto") || strings.Contains(stem, "vo") ||
		strings.HasSuffix(stem, "store"): // ExtJS store as entity-ish
		return "ent"
	}
	// Package-layout fallback: FirstWeight.java under /po/iir/ is still entity.
	switch {
	case strings.Contains(lower, "/controller/") || strings.Contains(lower, "/controllers/") ||
		strings.Contains(lower, "/handler/") || strings.Contains(lower, "/servlet/"):
		return "ctrl"
	case strings.Contains(lower, "/service/") || strings.Contains(lower, "/services/") ||
		strings.Contains(lower, "/serviceimpl/") || strings.Contains(lower, "/svc/"):
		return "svc"
	case strings.Contains(lower, "/dao/") || strings.Contains(lower, "/mapper/") ||
		strings.Contains(lower, "/repository/") || strings.Contains(lower, "/repositories/") ||
		strings.Contains(lower, "/repo/"):
		return "dao"
	case strings.Contains(lower, "/po/") || strings.Contains(lower, "/entity/") ||
		strings.Contains(lower, "/entities/") || strings.Contains(lower, "/domain/") ||
		strings.Contains(lower, "/model/") || strings.Contains(lower, "/models/") ||
		strings.Contains(lower, "/dto/") || strings.Contains(lower, "/vo/"):
		return "ent"
	default:
		return ""
	}
}

func isCodeishPath(path string) bool {
	lower := strings.ToLower(path)
	return regexp.MustCompile(`(?i)\.(java|kt|ts|tsx|js|jsx|py|go|cs|sql|bpmn)$`).MatchString(lower)
}

// isNoiseDepPath reports paths that should not monopolise dependent_files /
// code-dependency focus seeds on capability pages (build manifests, static
// markup, shared constants, logging/editor scaffolding, exploded WEB-INF copies).
func isNoiseDepPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	if strings.HasSuffix(lower, "pom.xml") || strings.HasSuffix(lower, "package.json") ||
		strings.HasSuffix(lower, "go.mod") || strings.HasSuffix(lower, "build.gradle") {
		return true
	}
	if strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm") ||
		strings.HasSuffix(lower, ".css") || strings.HasSuffix(lower, ".scss") ||
		strings.HasSuffix(lower, ".md") {
		return true
	}
	if strings.Contains(base, "const") && strings.HasSuffix(base, ".java") {
		return true
	}
	if strings.Contains(lower, "/constant/") || strings.Contains(lower, "/constants/") {
		return true
	}
	// Logging scaffolding is rarely page evidence outside log/audit topics.
	if strings.Contains(base, "log4j") || strings.Contains(base, "logback") {
		return true
	}
	// Exploded WEB-INF/classes copies are low-signal duplicates of sources.
	if strings.Contains(lower, "/web-inf/classes/") {
		return true
	}
	// Rich-text editor scaffolding + chart template configs.
	if strings.Contains(lower, "/ueditor/") {
		return true
	}
	if base == "chart.config.js" || (strings.Contains(lower, "/dialogs/") && strings.HasSuffix(base, "config.js")) {
		return true
	}
	if base == "mybatis-config.xml" || strings.HasSuffix(lower, "/mybatis-config.xml") {
		return true
	}
	return false
}

// depsSignal summarises architectural usefulness of a path list.
type depsSignal struct {
	roles map[string]bool
	code  int
}

func analyseDepsSignal(deps []string) depsSignal {
	s := depsSignal{roles: map[string]bool{}}
	for _, d := range deps {
		if isNoiseDepPath(d) {
			continue
		}
		if isCodeishPath(d) || pathRoleHintLite(d) != "" {
			s.code++
			if r := pathRoleHintLite(d); r != "" {
				s.roles[r] = true
			}
		}
	}
	return s
}

// depsNeedCiteEnrich reports whether bound deps lack multi-role code signal.
func depsNeedCiteEnrich(deps []string) bool {
	s := analyseDepsSignal(deps)
	if len(s.roles) >= 2 {
		return false
	}
	if s.code >= 3 {
		return false
	}
	return true
}

// roleStopTokens are architectural role words stripped when mining topic families.
// Generic only — no product/domain lexicon.
var roleStopTokens = map[string]bool{
	"controller": true, "control": true, "cont": true, "action": true,
	"handler": true, "resource": true, "servlet": true, "endpoint": true,
	"service": true, "svc": true, "manager": true, "mgr": true, "impl": true,
	"dao": true, "mapper": true, "repository": true, "repo": true,
	"entity": true, "model": true, "dto": true, "vo": true, "po": true, "pojo": true,
	"store": true, "view": true, "form": true, "panel": true, "window": true,
	"page": true, "index": true, "list": true, "detail": true, "edit": true,
	"base": true, "abstract": true, "common": true, "util": true, "utils": true,
	"helper": true, "config": true, "constant": true, "constants": true, "const": true,
	"test": true, "tests": true, "mock": true, "stub": true, "main": true,
	"application": true, "app": true, "web": true, "api": true, "rest": true,
	"java": true, "js": true, "ts": true, "tsx": true, "jsx": true, "xml": true,
}

// pathTopicTokens extracts non-role identifier tokens from a path basename +
// package segments (CamelCase / snake split). Product-agnostic.
func pathTopicTokens(path string) []string {
	slash := filepath.ToSlash(path)
	base := filepath.Base(slash)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	// CamelCase split BEFORE lowercasing so FirstWeightController → first, weight, controller.
	stem = regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(stem, `${1} ${2}`)
	stem = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`).ReplaceAllString(stem, `${1} ${2}`)
	stem = regexp.MustCompile(`[_\-\.]+`).ReplaceAllString(stem, " ")
	parts := strings.Fields(strings.ToLower(stem))
	// Also keep meaningful path segments (package folders).
	for _, seg := range strings.Split(slash, "/") {
		seg = strings.TrimSpace(seg)
		if strings.ContainsAny(seg, ".") {
			continue
		}
		// Camel-split directory names too (rare but cheap).
		segSplit := regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(seg, `${1} ${2}`)
		for _, sp := range strings.Fields(strings.ToLower(segSplit)) {
			if len(sp) < 3 || roleStopTokens[sp] {
				continue
			}
			if regexp.MustCompile(`^\d+$`).MatchString(sp) {
				continue
			}
			parts = append(parts, sp)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if len(p) < 3 || roleStopTokens[p] || seen[p] {
			continue
		}
		// Drop very generic short technical tokens.
		if p == "src" || p == "main" || p == "resources" || p == "classes" ||
			p == "content" || p == "static" || p == "public" || p == "private" {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// familyTokensFromPaths finds identifier tokens that recur across role-bearing
// code paths — the page's "topic family" mined from evidence, not a lexicon.
func familyTokensFromPaths(paths []string) map[string]int {
	freq := map[string]int{}
	// Count a token at most once per path.
	for _, p := range paths {
		if isNoiseDepPath(p) {
			continue
		}
		if pathRoleHintLite(p) == "" && !isCodeishPath(p) {
			continue
		}
		local := map[string]bool{}
		for _, t := range pathTopicTokens(p) {
			if local[t] {
				continue
			}
			local[t] = true
			freq[t]++
		}
	}
	// Prefer tokens that appear in ≥2 paths; if none, keep top singles with role paths.
	out := map[string]int{}
	for t, n := range freq {
		if n >= 2 {
			out[t] = n
		}
	}
	if len(out) == 0 {
		// Fallback: highest-frequency non-role tokens (even if n==1) when we have
		// multi-role cites that share no literal stem (rare). Keep top 3.
		type kv struct {
			t string
			n int
		}
		var list []kv
		for t, n := range freq {
			list = append(list, kv{t, n})
		}
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				if list[j].n > list[i].n || (list[j].n == list[i].n && list[j].t < list[i].t) {
					list[i], list[j] = list[j], list[i]
				}
			}
		}
		for i := 0; i < len(list) && i < 3; i++ {
			out[list[i].t] = list[i].n
		}
	}
	return out
}

// pathHitsFamily reports whether path carries any family token.
func pathHitsFamily(path string, family map[string]int) bool {
	if len(family) == 0 {
		return false
	}
	for _, t := range pathTopicTokens(path) {
		if family[t] > 0 {
			return true
		}
	}
	return false
}

// setFamilyHits counts paths that hit the family; also returns role count among hits.
func setFamilyHits(paths []string, family map[string]int) (hits int, roles int) {
	roleSet := map[string]bool{}
	for _, p := range paths {
		if !pathHitsFamily(p, family) {
			continue
		}
		hits++
		if r := pathRoleHintLite(p); r != "" {
			roleSet[r] = true
		}
	}
	return hits, len(roleSet)
}

// titleTokenHits scores how many title/goal tokens (ASCII or CJK len≥2) appear
// in the path set. No domain lexicon — pure substring / stem containment.
func titleTokenHits(title, goal string, paths []string) int {
	text := strings.TrimSpace(title + " " + goal)
	if text == "" || len(paths) == 0 {
		return 0
	}
	// Split CJK runs and ASCII idents.
	raw := regexp.MustCompile(`[A-Za-z][A-Za-z0-9]+|[\p{Han}]{2,}`).FindAllString(text, -1)
	var tokens []string
	seen := map[string]bool{}
	for _, t := range raw {
		tl := strings.ToLower(t)
		// skip ultra-generic Chinese page words
		if tl == "管理" || tl == "模块" || tl == "系统" || tl == "功能" ||
			tl == "说明" || tl == "概述" || tl == "详情" || tl == "服务" ||
			tl == "接口" || tl == "配置" || tl == "the" || tl == "and" ||
			tl == "for" || tl == "with" {
			continue
		}
		if len([]rune(tl)) < 2 || seen[tl] {
			continue
		}
		seen[tl] = true
		tokens = append(tokens, tl)
		if len(tokens) >= 16 {
			break
		}
	}
	if len(tokens) == 0 {
		return 0
	}
	hits := 0
	for _, p := range paths {
		pl := strings.ToLower(filepath.ToSlash(p))
		base := strings.ToLower(filepath.Base(pl))
		for _, t := range tokens {
			if strings.Contains(pl, t) || strings.Contains(base, t) {
				hits++
				break
			}
		}
	}
	return hits
}

// citesOutrankDeps reports that body cites form a coherent multi-role family
// that bound deps do not share — classic rebind drift onto universal Log/Base JS.
// Product-agnostic: family is mined from cite basenames, not a domain dictionary.
func citesOutrankDeps(title, goal string, deps, cites []string) bool {
	var roleCites []string
	citeRoles := map[string]bool{}
	for _, c := range cites {
		if isNoiseDepPath(c) {
			continue
		}
		if r := pathRoleHintLite(c); r != "" {
			roleCites = append(roleCites, c)
			citeRoles[r] = true
		} else if isCodeishPath(c) {
			roleCites = append(roleCites, c)
		}
	}
	if len(citeRoles) < 2 || len(roleCites) < 2 {
		return false
	}
	family := familyTokensFromPaths(roleCites)
	if len(family) == 0 {
		return false
	}
	citeHits, citeHitRoles := setFamilyHits(roleCites, family)
	if citeHits < 2 || citeHitRoles < 2 {
		// Family must cover a real multi-role chain in cites.
		return false
	}
	depHits, depHitRoles := setFamilyHits(deps, family)
	// Deps already share the cite family with meaningful coverage → not drift.
	// (e.g. FirstWeightController/Service/PO deps vs FirstWeight* cites)
	if depHits >= 2 && depHitRoles >= 1 {
		return false
	}
	// Strong case: cites share a family that deps completely miss.
	if depHits == 0 {
		return true
	}
	// Soft case: deps barely touch the family (single accidental hit) while cites
	// form a full multi-role family chain.
	if depHits == 1 && citeHits >= 3 && citeHitRoles >= 2 {
		return true
	}
	// Title explicitly matches cites better than deps (English / mixed titles).
	// Only when deps are not family-aligned (already handled above).
	thC := titleTokenHits(title, goal, roleCites)
	thD := titleTokenHits(title, goal, deps)
	if thC >= 2 && thC > thD+1 && depHits < 2 {
		return true
	}
	return false
}

// rankCitePaths orders body cites: family-matching role code first, then other
// role/code, then rest. Family may be nil (then plain role/code ranking).
func rankCitePaths(cites []string, family map[string]int) []string {
	var famRole, famCode, role, code, rest []string
	for _, c := range cites {
		if isNoiseDepPath(c) {
			continue
		}
		hit := pathHitsFamily(c, family)
		r := pathRoleHintLite(c)
		switch {
		case hit && r != "":
			famRole = append(famRole, c)
		case hit && isCodeishPath(c):
			famCode = append(famCode, c)
		case r != "":
			role = append(role, c)
		case isCodeishPath(c):
			code = append(code, c)
		default:
			rest = append(rest, c)
		}
	}
	out := make([]string, 0, len(cites))
	out = append(out, famRole...)
	out = append(out, famCode...)
	out = append(out, role...)
	out = append(out, code...)
	out = append(out, rest...)
	return out
}

// enrichDependentFilesFromBody upgrades weak or off-topic DependentFiles using
// page body cites. Triggers when:
//  1. bound deps lack multi-role code signal (config/noise-heavy), or
//  2. body cites form a multi-role topic family that deps do not share (rebind drift).
//
// No product-domain lexicon: topic family is mined from cite identifier tokens.
func enrichDependentFilesFromBody(wiki *models.Wiki, contents map[string]string) {
	if wiki == nil || contents == nil {
		return
	}
	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		body := contents[p.Slug]
		// Inline prose cites only: the <cite> reference block is machine-injected
		// on export (padded from the model inventory) and re-harvesting it on a
		// polish pass would feed inventory files back into DependentFiles
		// (feedback loop, non-idempotent re-export).
		cites := extractCitePaths(evidenceBody(body))
		if len(cites) == 0 {
			continue
		}
		weak := depsNeedCiteEnrich(p.DependentFiles)
		drift := citesOutrankDeps(p.Title, p.Goal, p.DependentFiles, cites)
		if !weak && !drift {
			continue
		}
		family := familyTokensFromPaths(cites)
		ranked := rankCitePaths(cites, family)
		if len(ranked) == 0 {
			continue
		}
		if drift {
			// Off-topic multi-role deps: replace with cite family, do not keep drift seeds.
			p.DependentFiles = mergeFocusPaths(ranked, nil, 8)
			continue
		}
		// Weak deps: cites primary, non-noise kept deps as filler only.
		kept := make([]string, 0, 8)
		for _, d := range p.DependentFiles {
			if !isNoiseDepPath(d) {
				kept = append(kept, d)
			}
		}
		p.DependentFiles = mergeFocusPaths(ranked, kept, 8)
	}
}

// rebindDependentFiles refreshes evidence paths from the current scan model.
// Skips when model has no files (offline fixture without scan). Always rewrites
// so polish can fix pom-heavy bindings from older generates.
// PickDependentFiles also loads .wikify/meta/title_bridge.json when present.
func rebindDependentFiles(model *scan.Model, wiki *models.Wiki) {
	if model == nil || wiki == nil || len(model.Files) == 0 {
		return
	}
	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		deps := evidence.PickDependentFiles(model, p.Title, p.Goal, 8)
		if len(deps) > 0 {
			p.DependentFiles = deps
		}
	}
}

// writeTitleBridge persists a generated title/path token bridge under meta/.
// Merges with any existing bridge so polish/generate accumulate path stems.
func writeTitleBridge(workDir string, model *scan.Model, wiki *models.Wiki) {
	if workDir == "" || wiki == nil {
		return
	}
	path := evidence.DefaultTitleBridgePath(workDir)
	var titles []string
	deps := map[string][]string{}
	for _, p := range wiki.Pages {
		titles = append(titles, p.Title)
		if len(p.DependentFiles) > 0 {
			deps[p.Title] = append([]string{}, p.DependentFiles...)
		}
	}
	fresh := evidence.BuildTitleBridge(model, titles, deps)
	if prev := evidence.LoadTitleBridge(path); prev != nil {
		fresh = evidence.MergeTitleBridge(prev, fresh)
	}
	_ = evidence.SaveTitleBridge(path, fresh)
}

func buildBrowseIndex(wiki *models.Wiki, lang string) BrowseIndex {
	idx := BrowseIndex{Lang: lang}
	if wiki == nil {
		return idx
	}
	for _, p := range wiki.Pages {
		tr := p.Track
		if tr != models.TrackFoundation && tr != models.TrackBusiness && tr != models.TrackTechnical {
			tr = models.InferTrack(p)
		}
		cp := p.ContentPath
		if cp == "" {
			cp = p.Title + ".md"
		}
		cp = strings.TrimPrefix(filepath.ToSlash(cp), "content/")
		idx.Pages = append(idx.Pages, BrowseIndexPage{
			Title: p.Title, Slug: p.Slug, Section: p.Section, Track: tr, ContentPath: cp,
		})
	}
	return idx
}

// EnsurePageFormat normalizes wiki page markdown without destroying LLM structure.
// Citation form is unified: [label](file://path#Lstart-Lend) and optional <cite> list.
//
// Behaviour:
//  1. Always normalize citations and ensure a leading # title.
//  2. If the body already has <cite> + TOC, keep structure; still fill missing Mermaid.
//  3. If the body is substantial (headings / length / sources), keep the LLM
//     outline: prepend <cite> when missing; inject ## 目录 when missing; fill Mermaid.
//  4. Only empty/stub pages fall back to a formal handbook skeleton.
// EnsurePageFormat normalizes wiki page markdown without destroying LLM structure.
// Policy (honest quality metrics):
//   - marked stubs / non-substantial bodies → thin stub, NO mermaid filler
//   - substantial pages with ≥2 mermaid → no generic top-up, but still inject
//     a page-local code-dependency diagram when import graph / role chain exists
//   - substantial pages with 0–1 mermaid → fill up to 2 diagrams max
//   - re-polish strips prior "补充示意" / "代码依赖示意" / known template filler
func EnsurePageFormat(title, content string, model *scan.Model, references []string, zh bool) string {
	body := stripLeadingTitle(content)
	body = normalizeCitations(body)
	// QL-1: safe structural fixes (fence auto-close, invalid-mermaid demotion,
	// duplicate-H2 rename). Hard/soft issue lists are consumed by the runner
	// gate; export just self-heals deterministically.
	body, _, _ = LintPageBody(body)
	body = strings.TrimSpace(body)

	refs := referenceDetails(model, references, 16)
	// Prefer role-matched inventory for thin/stub pages so cites are not random first files.
	if IsStubPage(body) || !isSubstantialPage(body) {
		refs = preferRoleMatchedRefs(model, title, refs, 16)
	}
	citeTitle := "参考文献"
	if !zh {
		citeTitle = "References"
	}
	citeBlock := buildCiteBlock(citeTitle, refs)

	// Honest stub first — even if a previous pass already injected cite/TOC/mermaid.
	if IsStubPage(body) || !isSubstantialPage(body) {
		return strings.TrimRight(buildThinStub(title, body, model, refs, zh), "\n") + "\n"
	}

	// Drop previous export filler so re-polish does not accumulate template diagrams.
	body = stripSectionByTitle(body, "补充示意")
	body = stripSectionByTitle(body, "Additional diagrams")
	body = stripSectionByTitle(body, "代码依赖示意")
	body = stripSectionByTitle(body, "Code dependency")
	body = stripGeneratedStructureSection(body)
	body = stripFillerMermaid(body)

	hasCite := strings.Contains(body, "<cite>")
	hasTOC := hasTOCHeading(body)

	// Already structured by the model (or a previous pass).
	if hasCite && hasTOC {
		out := body
		if !strings.HasPrefix(strings.TrimSpace(out), "#") {
			out = "# " + title + "\n\n" + out
		}
		out = ensureMermaidDiagrams(out, title, model, zh, references...)
		return strings.TrimRight(out, "\n") + "\n"
	}

	// Substantial LLM content: preserve author structure; fill cite + TOC + mermaid.
	if !hasTOC {
		if toc := buildTOCFromBody(body, zh); toc != "" {
			if strings.Contains(body, "</cite>") {
				body = insertAfterCite(body, toc)
			} else {
				body = toc + "\n\n" + body
			}
			hasTOC = true
		}
	}
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
	if !hasCite && citeBlock != "" {
		b.WriteString(citeBlock)
		b.WriteString("\n\n")
		if !hasTOC {
			if toc := buildTOCFromBody(body, zh); toc != "" {
				b.WriteString(toc)
				b.WriteString("\n\n")
				hasTOC = true
			}
		}
	}
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return ensureMermaidDiagrams(b.String(), title, model, zh, references...)
}

// EnsureRelatedNav appends a "相关页面" section with same-section siblings and
// cross-rail links. Idempotent: replaces a previously generated block.
func EnsureRelatedNav(body string, wiki *models.Wiki, page models.WikiPage, zh bool) string {
	return ensureRelatedNavShared(body, wiki, page, zh, nil)
}

// ensureRelatedNavShared additionally renders the NAV-1 共享证据 / "Shared
// evidence" group from precomputed shared-evidence neighbours
// (buildSharedEvidence). Links already present in the sibling or cross-rail
// groups are never repeated there.
func ensureRelatedNavShared(body string, wiki *models.Wiki, page models.WikiPage, zh bool, shared []sharedEvidenceRef) string {
	if wiki == nil || len(wiki.Pages) == 0 {
		return body
	}
	body = stripSectionByTitle(body, "相关页面")
	body = stripSectionByTitle(body, "Related pages")
	body = strings.TrimRight(body, "\n") + "\n"

	heading := "相关页面"
	sibLabel := "同章节"
	crossLabel := "跨轨推荐"
	sharedLabel := "共享证据"
	if !zh {
		heading = "Related pages"
		sibLabel = "Same section"
		crossLabel = "Cross-rail"
		sharedLabel = "Shared evidence"
	}

	var siblings []models.WikiPage
	for _, p := range wiki.Pages {
		if p.Slug == page.Slug || p.Title == page.Title {
			continue
		}
		if page.Section != "" && p.Section == page.Section {
			siblings = append(siblings, p)
		}
		if len(siblings) >= 8 {
			break
		}
	}
	cross := wiki.RelatedCrossTrack(page, 6)

	// NAV-1 dedupe: never repeat a page already linked above.
	used := map[string]bool{page.Slug: true}
	for _, p := range siblings {
		used[p.Slug] = true
	}
	for _, p := range cross {
		used[p.Slug] = true
	}
	var sharedOut []sharedEvidenceRef
	for _, r := range shared {
		if !used[r.Page.Slug] {
			used[r.Page.Slug] = true
			sharedOut = append(sharedOut, r)
		}
	}

	if len(siblings) == 0 && len(cross) == 0 && len(sharedOut) == 0 {
		return body
	}

	var b strings.Builder
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n\n") {
		b.WriteString("\n")
	}
	b.WriteString("## ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	if len(siblings) > 0 {
		b.WriteString("**")
		b.WriteString(sibLabel)
		b.WriteString("**\n\n")
		for _, p := range siblings {
			b.WriteString(fmt.Sprintf("- [%s](%s)\n", p.Title, linkTarget(p)))
		}
		b.WriteString("\n")
	}
	if len(cross) > 0 {
		b.WriteString("**")
		b.WriteString(crossLabel)
		b.WriteString("**\n\n")
		for _, p := range cross {
			b.WriteString(fmt.Sprintf("- [%s](%s) · %s\n", p.Title, linkTarget(p), p.Section))
		}
		b.WriteString("\n")
	}
	if len(sharedOut) > 0 {
		b.WriteString("**")
		b.WriteString(sharedLabel)
		b.WriteString("**\n\n")
		for _, r := range sharedOut {
			note := filepath.Base(r.File)
			if r.Shared > 1 {
				note = fmt.Sprintf("%s ×%d", note, r.Shared)
			}
			b.WriteString(fmt.Sprintf("- [%s](%s) · %s\n", r.Page.Title, linkTarget(r.Page), note))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// rewriteWikiLinks heals intra-wiki markdown links (QL-3), generalising the
// old numeric-slug rewrite. Resolution keys per page: exact link target
// (kept as-is), Title+".md", Slug, and normalized title (lowercase,
// spaces/underscores stripped). Only UNIQUE hits are rewritten to
// linkTarget(p); unresolved non-http/non-file:// .md links (and unknown
// counter slugs) are left untouched and counted as broken for the quality
// report. Idempotent: rewritten targets hit the exact map on the next run.
func rewriteWikiLinks(body string, wiki *models.Wiki) (string, int) {
	if wiki == nil || body == "" || len(wiki.Pages) == 0 {
		return body, 0
	}
	exact := map[string]bool{}
	keys := map[string][]int{} // resolution key → page indices (unique hits only)
	add := func(k string, i int) {
		if k == "" || k == ".md" {
			return
		}
		keys[k] = append(keys[k], i)
	}
	for i := range wiki.Pages {
		p := wiki.Pages[i]
		exact[linkTarget(p)] = true
		if p.Title != "" {
			add(p.Title+".md", i)
			add(normalizeWikiLinkKey(p.Title), i)
		}
		add(p.Slug, i)
	}
	gather := func(ks ...string) []int {
		seen := map[int]bool{}
		var out []int
		for _, k := range ks {
			for _, i := range keys[k] {
				if !seen[i] {
					seen[i] = true
					out = append(out, i)
				}
			}
		}
		return out
	}

	reLink := regexp.MustCompile(`\]\(([^)\s]+)\)`)
	reNumericSlug := regexp.MustCompile(`^\d+-\d+$`)
	broken := 0
	out := reLink.ReplaceAllStringFunc(body, func(m string) string {
		target := m[2 : len(m)-1]
		if target == "" || strings.HasPrefix(target, "#") || strings.Contains(target, "://") {
			return m // anchors, http(s), file:// — not intra-wiki page links
		}
		base, frag, _ := strings.Cut(target, "#")
		if base == "" {
			return m
		}
		fragSuffix := ""
		if frag != "" {
			fragSuffix = "#" + frag
		}
		// (e) bare counter slugs ("](50-50)") — legacy LLM catalog copy-paste.
		if reNumericSlug.MatchString(base) {
			if idxs := gather(base); len(idxs) == 1 {
				return "](" + linkTarget(wiki.Pages[idxs[0]]) + fragSuffix + ")"
			}
			broken++
			return m
		}
		if !strings.HasSuffix(strings.ToLower(base), ".md") {
			return m // images, source files, etc. — out of scope
		}
		clean := strings.TrimPrefix(filepath.ToSlash(base), "./")
		if exact[clean] {
			return m // already the canonical target
		}
		bn := path.Base(clean)
		idxs := gather(
			clean,
			bn,
			strings.TrimSuffix(bn, ".md"),
			normalizeWikiLinkKey(strings.TrimSuffix(bn, ".md")),
		)
		if len(idxs) == 1 {
			return "](" + linkTarget(wiki.Pages[idxs[0]]) + fragSuffix + ")"
		}
		broken++
		return m
	})
	return out, broken
}

// normalizeWikiLinkKey lowercases and strips spaces/underscores so lightly
// mangled titles ("User_Management.md", "user management.md") still resolve.
func normalizeWikiLinkKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

func linkTarget(p models.WikiPage) string {
	if p.ContentPath != "" {
		return filepath.ToSlash(strings.TrimPrefix(p.ContentPath, "content/"))
	}
	// Prefer title-based path over bare counter slugs ("50-50") which are not files.
	if p.Title != "" {
		return p.Title + ".md"
	}
	if p.Slug != "" {
		return p.Slug
	}
	return "page.md"
}

// ensureMermaidDiagrams keeps existing LLM diagrams and:
//  1. Always injects a page-local code-dependency diagram (import edges or
//     role-chain) under "## 代码依赖示意" when one can be built and is not already
//     present — even if the page already has ≥2 LLM mermaids.
//  2. When the page has fewer than minKeep diagrams, appends at most
//     (minKeep - existing) generic diagrams from defaultMermaid.
//
// minKeep is 2. Generic classDiagram/stateDiagram filler is never added past
// that floor. Callers must not invoke this on thin stubs.
func ensureMermaidDiagrams(body, title string, model *scan.Model, zh bool, focusPaths ...string) string {
	const minKeep = 2
	const maxFill = 2

	// Force-inject code dependency diagram when graph / role chain is available.
	// Idempotent: EnsurePageFormat strips prior "代码依赖示意" section first.
	if dep := codeDependencyDiagram(model, title, focusPaths); dep != "" && !hasCodeDependencyDiagram(body) {
		depHeading := "代码依赖示意"
		if !zh {
			depHeading = "Code dependency"
		}
		block := "\n## " + depHeading + "\n\n" + dep + "\n"
		// Prefer before related-pages nav; else after TOC/cite; else append.
		inserted := false
		for _, h := range []string{"相关页面", "Related pages"} {
			re := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(h) + `\s*$`)
			if loc := re.FindStringIndex(body); loc != nil {
				body = body[:loc[0]] + strings.TrimSpace(block) + "\n\n" + body[loc[0]:]
				inserted = true
				break
			}
		}
		if !inserted {
			if strings.Contains(body, "</cite>") {
				if hasTOCHeading(body) {
					body = insertAfterTOC(body, block)
				} else {
					body = insertAfterCite(body, strings.TrimSpace(block))
				}
			} else {
				body = strings.TrimRight(body, "\n") + "\n" + block
			}
		}
	}

	existing := extractMermaid(body)
	if len(existing) >= minKeep {
		return body
	}
	need := minKeep - len(existing)
	if need > maxFill {
		need = maxFill
	}
	if need <= 0 {
		return body
	}
	// Skip pool index 0 (code-dep) when we already force-injected one, so fill
	// uses the generic templates rather than duplicating the same graph.
	already := len(existing)
	if hasCodeDependencyDiagram(body) {
		already = max(already, 1)
	}
	diagrams := defaultMermaid(model, title, need, already, focusPaths...)
	// Drop any diagram that duplicates an already-present code-dep block.
	if hasCodeDependencyDiagram(body) {
		filtered := diagrams[:0]
		for _, d := range diagrams {
			if isCodeDependencyBlock(d) {
				continue
			}
			filtered = append(filtered, d)
		}
		diagrams = filtered
	}
	if len(diagrams) == 0 {
		return body
	}
	heading := "结构示意"
	if !zh {
		heading = "Structure diagram"
	}
	if len(existing) == 0 {
		block := "\n## " + heading + "\n\n" + strings.Join(diagrams, "\n\n") + "\n"
		if strings.Contains(body, "</cite>") {
			if hasTOCHeading(body) {
				return insertAfterTOC(body, block)
			}
			return insertAfterCite(body, strings.TrimSpace(block))
		}
		return strings.TrimRight(body, "\n") + "\n" + block
	}
	// Already has one diagram: append shortfall once.
	extraHeading := "补充示意"
	if !zh {
		extraHeading = "Additional diagrams"
	}
	block := "\n## " + extraHeading + "\n\n" + strings.Join(diagrams, "\n\n") + "\n"
	for _, h := range []string{"相关页面", "Related pages"} {
		re := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(h) + `\s*$`)
		if loc := re.FindStringIndex(body); loc != nil {
			return body[:loc[0]] + strings.TrimSpace(block) + "\n\n" + body[loc[0]:]
		}
	}
	return strings.TrimRight(body, "\n") + "\n" + block
}

func insertAfterTOC(body, block string) string {
	re := regexp.MustCompile(`(?m)^##\s+(目录|Table of Contents)\s*$`)
	loc := re.FindStringIndex(body)
	if loc == nil {
		return strings.TrimRight(body, "\n") + "\n" + block
	}
	rest := body[loc[1]:]
	next := regexp.MustCompile(`(?m)^##\s+`).FindStringIndex(rest)
	if next == nil {
		return body + "\n" + block
	}
	at := loc[1] + next[0]
	return body[:at] + strings.TrimSpace(block) + "\n\n" + body[at:]
}

// hasTOCHeading reports whether body already has a dedicated TOC H2.
func hasTOCHeading(body string) bool {
	re := regexp.MustCompile(`(?m)^##\s+(目录|Table of Contents)\s*$`)
	return re.MatchString(body)
}

// buildTOCFromBody extracts ## headings and builds a numbered TOC block.
func buildTOCFromBody(body string, zh bool) string {
	re := regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)
	matches := re.FindAllStringSubmatch(body, -1)
	var items []string
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		// Skip existing TOC headings
		if name == "目录" || name == "Table of Contents" {
			continue
		}
		items = append(items, name)
	}
	if len(items) < 2 {
		return ""
	}
	tocTitle := "目录"
	if !zh {
		tocTitle = "Table of Contents"
	}
	var lines []string
	lines = append(lines, "## "+tocTitle)
	for i, name := range items {
		lines = append(lines, fmt.Sprintf("%d. [%s](#%s)", i+1, name, anchor(name)))
	}
	return strings.Join(lines, "\n")
}

func insertAfterCite(body, toc string) string {
	// Find </cite> and insert TOC after it.
	idx := strings.Index(body, "</cite>")
	if idx < 0 {
		return toc + "\n\n" + body
	}
	end := idx + len("</cite>")
	return strings.TrimSpace(body[:end]) + "\n\n" + toc + "\n\n" + strings.TrimSpace(body[end:])
}

func isSubstantialPage(body string) bool {
	s := strings.TrimSpace(body)
	if s == "" {
		return false
	}
	if strings.Contains(s, "## ") || strings.Contains(s, "### ") {
		return true
	}
	if strings.Contains(s, "Sources:") || strings.Contains(s, "file://") {
		return true
	}
	if strings.Contains(s, "```mermaid") || strings.Contains(s, "```") {
		return true
	}
	// ~one short paragraph of real prose is enough to avoid skeleton overwrite.
	return len([]rune(s)) >= 120
}

func buildCiteBlock(citeTitle string, refs []refDetail) string {
	if len(refs) == 0 {
		return ""
	}
	var citeLines []string
	for _, r := range refs {
		label := filepath.Base(r.path)
		end := r.lines
		if end > 40 || end <= 0 {
			end = 40
		}
		citeLines = append(citeLines, fmt.Sprintf("- [%s](file://%s#L1-L%d)", label, r.path, end))
	}
	return "<cite>\n**" + citeTitle + "**\n" + strings.Join(citeLines, "\n") + "\n</cite>"
}

// buildThinStub produces an honest incomplete page for empty/failed generation.
// Prefer this over a full handbook skeleton so polish/export do not look "complete".
func buildThinStub(title, body string, model *scan.Model, refs []refDetail, zh bool) string {
	status := "待补充"
	overview := "概述"
	note := "本页在生成阶段内容不足或失败，已写入占位稿。请重新生成该页，或根据下方引用源文件手工补充。"
	next := "下一步"
	nextBody := "- 阅读 **参考文献** 中的源文件\n- 重新运行 `wikify generate` 覆盖本页，或在草稿中补写后再 `wikify polish`"
	citeTitle := "参考文献"
	if !zh {
		status = "stub"
		overview = "Overview"
		note = "This page has insufficient content (generation shortfall or failure). It is an honest placeholder — regenerate the page or fill it from the cited sources."
		next = "Next steps"
		nextBody = "- Read the source files under **References**\n- Re-run `wikify generate` for this page, or edit the draft and run `wikify polish`"
		citeTitle = "References"
	}

	prose := strings.TrimSpace(stripCiteAndTOC(stripMermaid(body)))
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString("> **")
	if zh {
		b.WriteString("状态")
	} else {
		b.WriteString("Status")
	}
	b.WriteString(":** ")
	b.WriteString(status)
	b.WriteString("\n\n")

	if cite := buildCiteBlock(citeTitle, refs); cite != "" {
		b.WriteString(cite)
		b.WriteString("\n\n")
	}

	// Minimal TOC so stub pages still satisfy format completeness checks.
	tocTitle := "目录"
	if !zh {
		tocTitle = "Table of Contents"
	}
	b.WriteString("## ")
	b.WriteString(tocTitle)
	b.WriteString("\n\n")
	b.WriteString("1. [")
	b.WriteString(overview)
	b.WriteString("](#")
	b.WriteString(anchor(overview))
	b.WriteString(")\n")
	b.WriteString("2. [")
	b.WriteString(next)
	b.WriteString("](#")
	b.WriteString(anchor(next))
	b.WriteString(")\n\n")

	b.WriteString("## ")
	b.WriteString(overview)
	b.WriteString("\n\n")
	if prose != "" {
		b.WriteString(prose)
		b.WriteString("\n\n")
	}
	b.WriteString(note)
	b.WriteString("\n\n")

	b.WriteString("## ")
	b.WriteString(next)
	b.WriteString("\n\n")
	b.WriteString(nextBody)
	b.WriteString("\n")
	// Scan-driven inventory: real paths only — no fabricated product narrative.
	if inv := buildInventorySection(title, model, refs, zh); inv != "" {
		b.WriteString(inv)
		b.WriteString("\n")
	}
	return b.String()
}

// IsStubPage reports whether body looks like a thin generation stub (for polish/metrics).
func IsStubPage(body string) bool {
	if body == "" {
		return true
	}
	// Blockquote or inline status markers from buildThinStub / honest placeholders.
	if strings.Contains(body, "**状态:** 待补充") || strings.Contains(body, "**Status:** stub") {
		return true
	}
	if strings.Contains(body, "已写入占位稿") || strings.Contains(body, "honest placeholder") {
		return true
	}
	// Legacy: very short pages with almost no structure.
	plain := strings.TrimSpace(stripCiteAndTOC(stripMermaid(body)))
	return len([]rune(plain)) < 80 && !strings.Contains(body, "## ")
}

// buildHandbookSkeleton is retained for tests/back-compat; empty pages use buildThinStub.
func buildHandbookSkeleton(title, body string, model *scan.Model, refs []refDetail, zh bool) string {
	sections := []string{"概述", "范围与前提", "系统结构", "核心设计", "实现要点", "依赖与集成", "运行与运维", "故障排查", "小结", "附录"}
	citeTitle, tocTitle := "参考文献", "目录"
	chartLabel, sourceLabel := "图表出处", "章节出处"
	if !zh {
		sections = []string{"Overview", "Scope and Prerequisites", "System Structure", "Core Design", "Implementation Notes", "Dependencies and Integration", "Operations", "Troubleshooting", "Summary", "Appendix"}
		citeTitle, tocTitle = "References", "Table of Contents"
		chartLabel, sourceLabel = "Figure sources", "Section sources"
	}

	var citeLines []string
	for _, r := range refs {
		label := filepath.Base(r.path)
		end := r.lines
		if end > 40 || end <= 0 {
			end = 40
		}
		citeLines = append(citeLines, fmt.Sprintf("- [%s](file://%s#L1-L%d)", label, r.path, end))
	}
	var toc []string
	for i, name := range sections {
		toc = append(toc, fmt.Sprintf("%d. [%s](#%s)", i+1, name, anchor(name)))
	}
	var sourceBlock []string
	for i, r := range refs {
		if i >= 10 {
			break
		}
		end := r.lines
		if end > 40 || end <= 0 {
			end = 40
		}
		sourceBlock = append(sourceBlock, fmt.Sprintf("- [%s:1-%d](file://%s#L1-L%d)", r.path, end, r.path, end))
	}

	var focus []string
	for _, r := range refs {
		if r.path != "" {
			focus = append(focus, r.path)
		}
	}
	mermaids := extractMermaid(body)
	if len(mermaids) == 0 {
		mermaids = defaultMermaid(model, title, 5, 0, focus...)
	} else if len(mermaids) < 5 {
		mermaids = append(mermaids, defaultMermaid(model, title, 5-len(mermaids), len(mermaids), focus...)...)
	}
	prose := stripCiteAndTOC(stripMermaid(body))
	sectionBodies := map[string]string{}
	for _, name := range sections {
		sectionBodies[name] = ""
	}
	overviewKey, designKey, implKey := "概述", "核心设计", "实现要点"
	if !zh {
		overviewKey, designKey, implKey = "Overview", "Core Design", "Implementation Notes"
	}
	if prose != "" {
		sectionBodies[overviewKey] = prose
		if len(mermaids) > 0 {
			sectionBodies[designKey] = mermaids[0]
		}
		if len(mermaids) > 1 {
			sectionBodies[implKey] = mermaids[1]
		}
	}
	for _, name := range sections {
		if strings.TrimSpace(sectionBodies[name]) == "" {
			sectionBodies[name] = fallbackSection(name, title, model, zh)
		}
	}

	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
	if len(citeLines) > 0 {
		b.WriteString("<cite>\n**")
		b.WriteString(citeTitle)
		b.WriteString("**\n")
		b.WriteString(strings.Join(citeLines, "\n"))
		b.WriteString("\n</cite>\n\n")
	}
	b.WriteString("## ")
	b.WriteString(tocTitle)
	b.WriteString("\n")
	b.WriteString(strings.Join(toc, "\n"))
	b.WriteString("\n\n")
	for _, name := range sections {
		b.WriteString("## ")
		b.WriteString(name)
		b.WriteString("\n\n")
		b.WriteString(sectionBodies[name])
		b.WriteString("\n\n")
	}
	if len(sourceBlock) > 0 {
		b.WriteString("**")
		b.WriteString(chartLabel)
		b.WriteString("**\n")
		b.WriteString(strings.Join(sourceBlock, "\n"))
		b.WriteString("\n\n**")
		b.WriteString(sourceLabel)
		b.WriteString("**\n")
		b.WriteString(strings.Join(sourceBlock, "\n"))
		b.WriteString("\n")
	}
	return b.String()
}

type refDetail struct {
	path  string
	lines int
}


// preferRoleMatchedRefs reorders/augments refs using scan inventory paths that match
// generic engineering roles inferred from the page title (api/security/test/deploy/...).
// No product-domain dictionary — path role stems only.
func preferRoleMatchedRefs(model *scan.Model, title string, refs []refDetail, limit int) []refDetail {
	if model == nil || limit <= 0 {
		return refs
	}
	rolePaths := inventoryPathsForTitle(model, title, limit*2)
	if len(rolePaths) == 0 {
		return refs
	}
	lineMap := map[string]int{}
	for _, f := range model.Files {
		lineMap[filepath.ToSlash(f.RelativePath)] = f.Lines
	}
	seen := map[string]bool{}
	var out []refDetail
	for _, pth := range rolePaths {
		pth = filepath.ToSlash(pth)
		if seen[pth] {
			continue
		}
		seen[pth] = true
		out = append(out, refDetail{path: pth, lines: lineMap[pth]})
		if len(out) >= limit {
			return out
		}
	}
	for _, r := range refs {
		if seen[r.path] {
			continue
		}
		seen[r.path] = true
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// buildInventorySection emits a markdown section listing real scan-matched paths
// for stub pages so offline polish still provides actionable navigation into the repo.
func buildInventorySection(title string, model *scan.Model, refs []refDetail, zh bool) string {
	paths := inventoryPathsForTitle(model, title, 12)
	if len(paths) == 0 {
		for _, r := range refs {
			if r.path != "" {
				paths = append(paths, r.path)
			}
			if len(paths) >= 8 {
				break
			}
		}
	}
	if len(paths) == 0 {
		return ""
	}
	heading := "仓库信号清单"
	colPath, colRole := "路径", "角色启发"
	if !zh {
		heading = "Repository signal inventory"
		colPath, colRole = "Path", "Role hint"
	}
	var b strings.Builder
	b.WriteString("## ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	b.WriteString("| ")
	b.WriteString(colPath)
	b.WriteString(" | ")
	b.WriteString(colRole)
	b.WriteString(" |\n| --- | --- |\n")
	for i, pth := range paths {
		if i >= 12 {
			break
		}
		b.WriteString("| `")
		b.WriteString(pth)
		b.WriteString("` | ")
		b.WriteString(pathRoleHint(pth, zh))
		b.WriteString(" |\n")
	}
	b.WriteString("\n")
	return b.String()
}

// inventoryPathsForTitle picks scan files whose path roles match generic cues in title.
func inventoryPathsForTitle(model *scan.Model, title string, limit int) []string {
	if model == nil || limit <= 0 {
		return nil
	}
	t := strings.ToLower(title)
	type scored struct {
		path  string
		score int
	}
	var list []scored
	for _, f := range model.Files {
		rel := filepath.ToSlash(f.RelativePath)
		if scan.IsNoisePath(rel) {
			continue
		}
		lower := strings.ToLower(rel)
		base := strings.ToLower(filepath.Base(rel))
		score := 0
		if containsAny(title, "接口", "API", "Api", "api", "Controller") || strings.Contains(t, "api") {
			if scan.IsAPISourceFile(rel) {
				score += 8
			}
			if matchPath(lower, `controller|handler|resource|router|endpoint|rest`) {
				score += 4
			}
		}
		if containsAny(title, "安全", "认证", "授权", "权限", "Security", "Auth") || strings.Contains(t, "secur") || strings.Contains(t, "auth") {
			if matchPath(lower, `auth|security|oauth|jwt|shiro|permission|rbac|login|session|filter|interceptor`) {
				score += 8
			}
		}
		if containsAny(title, "部署", "运维", "Docker", "K8s", "Deploy", "Ops") || strings.Contains(t, "deploy") || strings.Contains(t, "ops") {
			if matchPath(lower, `dockerfile|docker-compose|deploy|k8s|helm|terraform|ansible|\.github/workflows|jenkinsfile`) ||
				matchPath(base, `dockerfile|docker-compose|makefile|jenkinsfile`) {
				score += 8
			}
			if matchPath(base, `pom\.xml|build\.gradle|package\.json|go\.mod`) {
				score += 2
			}
		}
		if containsAny(title, "测试", "质量", "Test", "QA") || strings.Contains(t, "test") {
			if matchPath(lower, `(^|/)(test|tests|__tests__|spec)/`) || matchPath(base, `_test\.|tests?\.(java|go|py|ts|js)$|\.spec\.`) {
				score += 8
			}
		}
		if containsAny(title, "配置", "Config", "环境") || strings.Contains(t, "config") {
			if matchPath(base, `application|config|settings|bootstrap|\.env|properties|yml|yaml`) ||
				matchPath(lower, `(^|/)(config|conf|resources)(/|$)`) {
				score += 7
			}
		}
		if containsAny(title, "数据", "模型", "持久", "Entity", "DAO", "Mapper") || strings.Contains(t, "data") || strings.Contains(t, "model") {
			if matchPath(lower, `entity|domain|model|dto|mapper|dao|repository|schema|migration|\.sql$`) {
				score += 7
			}
		}
		if containsAny(title, "架构", "Architecture", "模块", "Module", "组件") {
			if matchPath(base, `pom\.xml|build\.gradle|go\.mod|package\.json|readme`) {
				score += 3
			}
			if len(filepath.Dir(rel)) > 1 && scan.IsCodeFile(rel) {
				score += 1
			}
		}
		if containsAny(title, "常见问题", "故障", "排查", "FAQ", "Troubleshoot") {
			if matchPath(lower, `actuator|health|logback|logging|error|exception|monitor`) {
				score += 6
			}
			if matchPath(base, `application|config|\.env`) {
				score += 3
			}
		}
		if score <= 0 {
			continue
		}
		if scan.IsCodeFile(rel) {
			score += 1
		}
		list = append(list, scored{rel, score})
	}
	if len(list) == 0 {
		return nil
	}
	// Sort by score desc, then path (stable simple sort).
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].score > list[i].score || (list[j].score == list[i].score && list[j].path < list[i].path) {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	seenSeg := map[string]int{}
	var out []string
	for _, s := range list {
		seg := s.path
		if i := strings.Index(seg, "/"); i >= 0 {
			seg = seg[:i]
		}
		if seenSeg[seg] >= 3 {
			continue
		}
		seenSeg[seg]++
		out = append(out, s.path)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func matchPath(lower, pattern string) bool {
	re := regexp.MustCompile(`(?i)` + pattern)
	return re.MatchString(lower)
}

func pathRoleHint(path string, zh bool) string {
	lower := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	switch {
	case scan.IsAPISourceFile(path) || matchPath(lower, `controller|handler|resource|router`):
		if zh {
			return "接口/控制层"
		}
		return "api/controller"
	case matchPath(lower, `auth|security|permission|oauth|jwt|shiro|rbac`):
		if zh {
			return "安全/认证"
		}
		return "security/auth"
	case matchPath(lower, `dockerfile|docker-compose|deploy|k8s|helm|terraform|\.github/workflows`):
		if zh {
			return "部署/交付"
		}
		return "deploy"
	case matchPath(lower, `(^|/)(test|tests|__tests__|spec)/`) || matchPath(base, `_test\.|tests?\.(java|go|py)`):
		if zh {
			return "测试"
		}
		return "test"
	case matchPath(base, `application|config|settings|\.env|properties|yml|yaml`) || matchPath(lower, `(^|/)(config|conf|resources)(/|$)`):
		if zh {
			return "配置"
		}
		return "config"
	case matchPath(lower, `entity|mapper|dao|repository|\.sql$|migration`):
		if zh {
			return "数据/持久化"
		}
		return "data"
	case matchPath(lower, `service|serviceimpl`):
		if zh {
			return "服务层"
		}
		return "service"
	default:
		if zh {
			return "源码"
		}
		return "source"
	}
}

func referenceDetails(model *scan.Model, references []string, limit int) []refDetail {
	lineMap := map[string]int{}
	if model != nil {
		for _, f := range model.Files {
			lineMap[filepath.ToSlash(f.RelativePath)] = f.Lines
		}
	}
	var out []refDetail
	seen := map[string]bool{}
	for _, r := range references {
		p := filepath.ToSlash(r)
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, refDetail{path: p, lines: lineMap[p]})
		if len(out) >= limit {
			return out
		}
	}
	if model != nil {
		for _, f := range model.Files {
			p := filepath.ToSlash(f.RelativePath)
			if seen[p] || !scan.IsCodeFile(p) {
				continue
			}
			seen[p] = true
			out = append(out, refDetail{path: p, lines: f.Lines})
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func buildMetadata(model *scan.Model, wiki *models.Wiki, contents map[string]string, lang string) map[string]any {
	name := "repo"
	now := ""
	commit := ""
	summary := ""
	if model != nil {
		name = model.Name
		now = model.GeneratedAt
		commit = model.GitCommit
		summary = model.Summary
	}
	if commit == "" {
		commit = "0000000000000000000000000000000000000000"
	}
	repoID := uuidFromKey("repo:" + name)

	type cat struct {
		ID             string `json:"id"`
		RepoID         string `json:"repo_id"`
		Name           string `json:"name"`
		Description    string `json:"description"`
		Prompt         string `json:"prompt"`
		ProgressStatus string `json:"progress_status"`
		DependentFiles string `json:"dependent_files"`
		GmtCreate      string `json:"gmt_create"`
		GmtModified    string `json:"gmt_modified"`
		RawData        string `json:"raw_data"`
	}
	type item struct {
		CatalogID      string `json:"catalog_id"`
		Title          string `json:"title"`
		Description    string `json:"description"`
		Extend         string `json:"extend"`
		DependentFiles string `json:"dependent_files"`
		ProgressStatus string `json:"progress_status"`
		RepoID         string `json:"repo_id"`
		ReferenceCount int    `json:"reference_count"`
		ID             string `json:"id"`
		GmtCreate      string `json:"gmt_create"`
		GmtModified    string `json:"gmt_modified"`
	}
	type rel struct {
		ID               int    `json:"id"`
		SourceID         string `json:"source_id"`
		TargetID         string `json:"target_id"`
		SourceType       string `json:"source_type"`
		TargetType       string `json:"target_type"`
		RelationshipType string `json:"relationship_type"`
		Extra            string `json:"extra"`
		GmtCreate        string `json:"gmt_create"`
		GmtModified      string `json:"gmt_modified"`
	}

	var catalogs []cat
	var items []item
	titleToWikiID := map[string]string{}
	var structure []string
	trackCounts := map[string]int{}

	for _, p := range wiki.Pages {
		cid := uuidFromKey("catalog:" + p.Slug)
		wid := uuidFromKey("wiki:" + p.Slug)
		titleToWikiID[p.Title] = wid
		// Always recompute for polish/upgrades: old drafts may store topic-<hash> slugs.
		slug := descriptionSlug(p.Title)
		if p.DescriptionSlug != "" && !strings.HasPrefix(p.DescriptionSlug, "topic-") && !isBareRoleSlug(p.DescriptionSlug) {
			// Keep a prior non-opaque slug (e.g. explicit seed mapping already on the page).
			slug = p.DescriptionSlug
		}
		p.DescriptionSlug = slug
		deps := encodeDependentFiles(p.DependentFiles)
		prompt := p.Goal
		if prompt == "" {
			prompt = p.Title
		}
		tr := p.Track
		if tr != models.TrackFoundation && tr != models.TrackBusiness && tr != models.TrackTechnical {
			tr = models.InferTrack(p)
		}
		trackCounts[tr]++
		extend := mustJSON(map[string]any{
			"track":           tr,
			"section":         p.Section,
			"dependent_files": p.DependentFiles,
		})
		status := "completed"
		if body, ok := contents[p.Slug]; ok && IsStubPage(body) {
			status = "stub"
		}
		catalogs = append(catalogs, cat{
			ID: cid, RepoID: repoID, Name: p.Title, Description: slug, Prompt: prompt,
			ProgressStatus: status, DependentFiles: deps, GmtCreate: now, GmtModified: now, RawData: "",
		})
		items = append(items, item{
			CatalogID: cid, Title: p.Title, Description: slug, Extend: extend, DependentFiles: deps,
			ProgressStatus: status, RepoID: repoID, ReferenceCount: len(p.DependentFiles),
			ID: wid, GmtCreate: now, GmtModified: now,
		})
		cp := p.ContentPath
		if cp == "" {
			cp = p.Title + ".md"
		}
		structure = append(structure, "content/"+filepath.ToSlash(cp))
	}

	// Materialise container parents (section/group titles that are not real pages).
	// LLM catalogs often set Parent=section while never emitting a page titled as the section;
	// without synthetic container items, knowledge_relations stays empty (unlike Qoder samples).
	containers := collectMissingParents(wiki, titleToWikiID)
	for _, name := range containers {
		if titleToWikiID[name] != "" {
			continue
		}
		cid := uuidFromKey("catalog:container:" + name)
		wid := uuidFromKey("wiki:container:" + name)
		titleToWikiID[name] = wid
		slug := descriptionSlug(name)
		tr := models.InferTrack(models.WikiPage{Title: name, Section: name})
		trackCounts[tr]++
		extend := mustJSON(map[string]any{
			"track":     tr,
			"section":   name,
			"container": true,
			"virtual":   true,
		})
		catalogs = append(catalogs, cat{
			ID: cid, RepoID: repoID, Name: name, Description: slug,
			Prompt: name, ProgressStatus: "completed", DependentFiles: "[]",
			GmtCreate: now, GmtModified: now, RawData: "",
		})
		items = append(items, item{
			CatalogID: cid, Title: name, Description: slug, Extend: extend, DependentFiles: "[]",
			ProgressStatus: "completed", RepoID: repoID, ReferenceCount: 0,
			ID: wid, GmtCreate: now, GmtModified: now,
		})
	}

	var relations []rel
	rid := 1
	seenRel := map[string]bool{}
	addRel := func(parentTitle, childTitle string) {
		parentID := titleToWikiID[parentTitle]
		childID := titleToWikiID[childTitle]
		if parentID == "" || childID == "" || parentID == childID {
			return
		}
		key := parentID + "->" + childID
		if seenRel[key] {
			return
		}
		seenRel[key] = true
		relations = append(relations, rel{
			ID: rid, SourceID: parentID, TargetID: childID,
			SourceType: "WIKI_ITEM", TargetType: "WIKI_ITEM", RelationshipType: "PARENT_CHILD",
			Extra:     fmt.Sprintf("Wiki parent-child relationship: %s -> %s", parentTitle, childTitle),
			GmtCreate: now, GmtModified: now,
		})
		rid++
	}
	for _, p := range wiki.Pages {
		// Explicit Parent field (group or section).
		if p.Parent != "" && p.Parent != p.Title {
			addRel(p.Parent, p.Title)
		}
		// Section container → page (covers free catalogs that leave Parent empty).
		if p.Section != "" && p.Section != p.Title && p.Section != p.Parent {
			addRel(p.Section, p.Title)
		}
		// Group container → page when group differs from section/parent.
		if p.Group != "" && p.Group != p.Title && p.Group != p.Parent && p.Group != p.Section {
			addRel(p.Group, p.Title)
		}
	}

	// RELATED_TO: same-section siblings + cross-rail token overlap (Qoder-style graph).
	addRelated := func(aTitle, bTitle, why string) {
		aID := titleToWikiID[aTitle]
		bID := titleToWikiID[bTitle]
		if aID == "" || bID == "" || aID == bID {
			return
		}
		// Undirected pair key — store once in canonical order.
		key := aID + "<->" + bID
		keyRev := bID + "<->" + aID
		if seenRel[key] || seenRel[keyRev] || seenRel[aID+"->"+bID] || seenRel[bID+"->"+aID] {
			return
		}
		seenRel[key] = true
		relations = append(relations, rel{
			ID: rid, SourceID: aID, TargetID: bID,
			SourceType: "WIKI_ITEM", TargetType: "WIKI_ITEM", RelationshipType: "RELATED_TO",
			Extra:     why,
			GmtCreate: now, GmtModified: now,
		})
		rid++
	}
	// Same-section siblings (cap per page).
	bySection := map[string][]models.WikiPage{}
	for _, p := range wiki.Pages {
		sec := p.Section
		if sec == "" {
			sec = p.Title
		}
		bySection[sec] = append(bySection[sec], p)
	}
	for _, pages := range bySection {
		limit := len(pages)
		if limit > 6 {
			limit = 6
		}
		for i := 0; i < limit; i++ {
			for j := i + 1; j < limit; j++ {
				addRelated(pages[i].Title, pages[j].Title,
					fmt.Sprintf("Same section: %s", pages[i].Section))
			}
		}
	}
	// Cross-rail RELATED_TO from token overlap (reuse catalog logic).
	for _, p := range wiki.Pages {
		cross := wiki.RelatedCrossTrack(p, 4)
		for _, c := range cross {
			addRelated(p.Title, c.Title,
				fmt.Sprintf("Cross-rail related: %s <-> %s", p.Title, c.Title))
		}
	}
	// NAV-1: shared-evidence RELATED_TO pairs from the file→pages inverted
	// index (same rule as the 共享证据 nav group; addRelated dedupes overlaps
	// with sibling/cross-rail pairs above).
	sharedEv := buildSharedEvidence(wiki, contents, 5)
	for _, p := range wiki.Pages {
		for _, r := range sharedEv[p.Slug] {
			addRelated(p.Title, r.Page.Title,
				fmt.Sprintf("Shared evidence: %s", r.File))
		}
	}

	overview := fmt.Sprintf("# %s\n\n%s\n\nPages: %d\n", name, summary, len(wiki.Pages))
	return map[string]any{
		"knowledge_relations": relations,
		"wiki_catalogs":       catalogs,
		"wiki_items":          items,
		"wiki_overview": map[string]any{
			"content": overview, "gmt_create": now, "gmt_modified": now,
			"id": uuidFromKey("overview:" + repoID), "repo_id": repoID,
		},
		"wiki_readme": map[string]any{
			"content": summary, "gmt_create": now, "gmt_modified": now,
			"id": uuidFromKey("readme:" + repoID), "repo_id": repoID,
		},
		"wiki_repo": map[string]any{
			"id": repoID, "name": name,
			"progress_status": "completed", "wiki_present_status": "COMPLETED",
			"optimized_catalog":          buildCatalogText(model),
			"current_document_structure": mustJSON(structure),
			"catalogue_think_content":    "",
			"recovery_checkpoint":        "wiki_generation_completed",
			"last_commit_id":             commit,
			"last_commit_update":         now,
			"gmt_create":                 now,
			"gmt_modified":               now,
			"extend_info": map[string]any{
				"language": lang, "document_count": len(wiki.Pages),
				"module_count": moduleCount(model), "generator": "wikify",
				"planning": "structure+llm",
				"tracks":   trackCounts,
			},
		},
	}
}

func buildCatalogText(model *scan.Model) string {
	if model == nil {
		return ""
	}
	var lines []string
	for i, f := range model.Files {
		if i >= 200 {
			break
		}
		lines = append(lines, f.RelativePath)
	}
	return strings.Join(lines, "\n")
}

func moduleCount(model *scan.Model) int {
	if model == nil {
		return 0
	}
	return len(model.Modules)
}

func uuidFromKey(key string) string {
	sum := sha1.Sum([]byte("wikify:" + key))
	h := hex.EncodeToString(sum[:])
	// UUID-ish layout
	return fmt.Sprintf("%s-%s-5%s-%s%s-%s",
		h[0:8], h[8:12], h[13:16],
		fmt.Sprintf("%x", (sum[8]&0x3f)|0x80), h[18:20],
		h[20:32],
	)
}

// encodeDependentFiles serialises paths as a JSON array string for metadata
// (Qoder samples often store dependent_files as a stringified JSON list).
func encodeDependentFiles(paths []string) string {
	if len(paths) == 0 {
		return "[]"
	}
	clean := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		clean = append(clean, p)
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// collectMissingParents returns section/group/parent titles that appear as
// containers but are not themselves wiki page titles.
func collectMissingParents(wiki *models.Wiki, titleToWikiID map[string]string) []string {
	if wiki == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || titleToWikiID[name] != "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, p := range wiki.Pages {
		if p.Parent != "" && p.Parent != p.Title {
			add(p.Parent)
		}
		if p.Section != "" && p.Section != p.Title {
			add(p.Section)
		}
		if p.Group != "" && p.Group != p.Title {
			add(p.Group)
		}
	}
	return out
}

func descriptionSlug(title string) string {
	// Prefer planner.DescriptionSlug semantics without importing planner
	// (would create an import cycle via runner → planner → export).
	if s, ok := exportTitleSlugs[title]; ok {
		return s
	}
	t := title
	for _, pair := range [][2]string{
		{"模块", "-module"}, {"服务", "-service"}, {"接口", "-api"},
		{"设计", "-design"}, {"文档", "-docs"}, {"管理", "-mgmt"},
		{"能力", "-capability"}, {"流程", "-flow"}, {"架构", "-arch"},
		{"配置", "-config"}, {"部署", "-deploy"}, {"运维", "-ops"},
		{"安全", "-security"}, {"测试", "-test"}, {"数据", "-data"},
		{"模型", "-model"}, {"总览", "-overview"}, {"概述", "-overview"},
		{"指南", "-guide"}, {"策略", "-strategy"}, {"规范", "-spec"},
	} {
		t = strings.ReplaceAll(t, pair[0], pair[1])
	}
	var b strings.Builder
	for _, r := range strings.ToLower(t) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == ' ' || r == '_' || r == '-' {
			b.WriteByte('-')
		}
	}
	s := regexp.MustCompile(`-+`).ReplaceAllString(b.String(), "-")
	s = strings.Trim(s, "-")
	if s != "" && hasASCIILetter(s) && !isBareRoleSlug(s) {
		return s
	}
	// Readable hybrid for pure-CJK titles: section-ish prefix + short hash.
	prefix := readableSlugPrefix(title)
	return prefix + "-" + uuidFromKey(title)[:8]
}

func hasASCIILetter(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			return true
		}
	}
	return false
}

// isBareRoleSlug reports slugs that are only generic role tokens (single or
// composed), e.g. "mgmt", "capability", "mgmt-capability", "model-model-mgmt".
// Such slugs collide across unrelated CJK titles after suffix stripping.
func isBareRoleSlug(s string) bool {
	role := map[string]bool{
		"mgmt": true, "api": true, "module": true, "service": true, "design": true,
		"docs": true, "capability": true, "flow": true, "arch": true, "config": true,
		"deploy": true, "ops": true, "security": true, "test": true, "data": true,
		"model": true, "overview": true, "guide": true, "strategy": true, "spec": true,
		"svc": true, "mod": true, "cfg": true, "sec": true, "page": true,
	}
	parts := strings.Split(s, "-")
	if len(parts) == 0 {
		return true
	}
	for _, p := range parts {
		if p == "" {
			continue
		}
		if !role[p] {
			return false
		}
	}
	return true
}

// readableSlugPrefix picks a short English role label from generic title cues.
// Product/domain words are never hard-coded — only engineering role stems.
func readableSlugPrefix(title string) string {
	switch {
	case strings.Contains(title, "架构"):
		return "arch"
	case strings.Contains(title, "接口") || strings.Contains(title, "API"):
		return "api"
	case strings.Contains(title, "数据") || strings.Contains(title, "模型") || strings.Contains(title, "实体"):
		return "data"
	case strings.Contains(title, "部署") || strings.Contains(title, "运维"):
		return "ops"
	case strings.Contains(title, "安全") || strings.Contains(title, "权限") || strings.Contains(title, "认证"):
		return "sec"
	case strings.Contains(title, "配置"):
		return "cfg"
	case strings.Contains(title, "测试"):
		return "test"
	case strings.Contains(title, "故障") || strings.Contains(title, "排查"):
		return "troubleshoot"
	case strings.Contains(title, "流程") || strings.Contains(title, "工作流"):
		return "flow"
	case strings.Contains(title, "服务"):
		return "svc"
	case strings.Contains(title, "模块"):
		return "mod"
	case strings.Contains(title, "管理"):
		return "mgmt"
	case strings.Contains(title, "指南") || strings.Contains(title, "概述") || strings.Contains(title, "总览"):
		return "guide"
	default:
		return "page"
	}
}

var exportTitleSlugs = map[string]string{
	"项目概述": "project-overview", "快速开始": "getting-started",
	"系统架构设计": "system-architecture", "整体架构设计": "overall-architecture",
	"模块划分原则": "module-boundaries", "数据流架构": "data-flow-architecture",
	"组件交互关系": "component-interactions", "部署架构": "deployment-architecture",
	"核心模块详解": "core-modules", "开发指南": "developer-guide",
	"目录结构": "directory-structure", "技术栈与依赖": "tech-stack-and-dependencies",
	"API接口文档": "api-documentation", "配置管理": "configuration-management",
	"安全与访问控制": "security-and-access-control",
	"部署与运维": "deployment-and-operations",
	"故障排查": "troubleshooting", "数据模型设计": "data-model-design",
	"数据库设计": "database-design", "接口文档": "api-docs", "架构设计": "architecture-design",
	"核心业务模块": "core-business-modules", "测试与质量": "testing-and-quality",
	"性能与扩展": "performance-and-scalability",
	"常见问题": "faq", "附录": "appendix",
	"编码规范": "coding-standards", "协作与支撑": "collaboration-and-support",
	"后端应用架构": "backend-application-architecture",
	"技术栈与工程基础": "tech-stack-and-engineering-foundation",
	"Project Overview": "project-overview",
	"Getting Started":  "getting-started", "System Architecture": "system-architecture",
	"Core Modules": "core-modules", "API Documentation": "api-documentation",
	"Data Model Design": "data-model-design", "Deployment and Operations": "deployment-and-operations",
	"Troubleshooting": "troubleshooting", "Security Design": "security-design",
	"Developer Guide": "developer-guide", "Testing and quality": "testing-and-quality",
	"Security and access control": "security-and-access-control",
	"Configuration": "configuration", "FAQ": "faq", "Appendix": "appendix",
	"Performance and scalability": "performance-and-scalability",
	"Coding standards": "coding-standards",
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func stripLeadingTitle(s string) string {
	s = strings.TrimSpace(s)
	// Only strip a single H1 line ("# title"), never "## …" section headings.
	if strings.HasPrefix(s, "# ") || strings.HasPrefix(s, "#\t") {
		if i := strings.Index(s, "\n"); i >= 0 {
			return strings.TrimSpace(s[i+1:])
		}
		return ""
	}
	// Bare "#title" without space is still an H1 in common markdown; "##" must stay.
	if strings.HasPrefix(s, "#") && !strings.HasPrefix(s, "##") {
		if i := strings.Index(s, "\n"); i >= 0 {
			return strings.TrimSpace(s[i+1:])
		}
		return ""
	}
	return s
}

// normalizeCitations rewrites markdown source links to the unified file://…#L form.
// Accepts: [label](path#L1-L10), [label](path#L1), or already file:// (left as-is).
func normalizeCitations(s string) string {
	// Match [label](...#Lstart) or [label](...#Lstart-Lend) / #Lstart-Lend
	re := regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	return re.ReplaceAllStringFunc(s, func(m string) string {
		sub := re.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		label, target := sub[1], sub[2]
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			return m
		}
		// Already unified
		if strings.HasPrefix(target, "file://") {
			return m
		}
		// Expect path#L… fragment
		path, frag, ok := strings.Cut(target, "#")
		if !ok || path == "" {
			return m
		}
		// L123 or L123-L456 or L123-456
		frag = strings.TrimSpace(frag)
		if !strings.HasPrefix(frag, "L") && !strings.HasPrefix(frag, "l") {
			return m
		}
		frag = "L" + strings.TrimPrefix(strings.TrimPrefix(frag, "L"), "l")
		// Normalize L1-10 → L1-L10
		if i := strings.Index(frag[1:], "-"); i >= 0 {
			// frag like L123-456 or L123-L456
			left := frag[:1+i]
			right := frag[1+i+1:]
			right = strings.TrimPrefix(right, "L")
			right = strings.TrimPrefix(right, "l")
			frag = left + "-L" + right
		}
		return fmt.Sprintf("[%s](file://%s#%s)", label, path, frag)
	})
}

func stripMermaid(s string) string {
	return regexp.MustCompile("(?s)```mermaid.*?```").ReplaceAllString(s, "")
}


// stripFillerMermaid removes known export-template diagrams that pad pages
// (class Topic / stateDiagram enter <title> / generic Repo-->Topic flowcharts
// written by older defaultMermaid). Real LLM diagrams are kept.
// Note: code-dependency diagrams live under their own section heading and are
// removed via stripSectionByTitle("代码依赖示意"), not here.
func stripFillerMermaid(s string) string {
	re := regexp.MustCompile("(?s)```mermaid\\s*(.*?)```")
	return re.ReplaceAllStringFunc(s, func(block string) string {
		inner := strings.TrimSpace(block)
		// Detect template fingerprints from defaultMermaid pool.
		if strings.Contains(inner, "class Topic") {
			return ""
		}
		if strings.Contains(inner, "stateDiagram") && strings.Contains(inner, "enter ") {
			return ""
		}
		if strings.Contains(inner, "Repo[") && strings.Contains(inner, "Topic[") {
			return ""
		}
		if strings.Contains(inner, "participant S as ") && strings.Contains(inner, "C->>A: Request") {
			return ""
		}
		if strings.Contains(inner, "T --> R[Rules]") && strings.Contains(inner, "T --> I[Interfaces]") {
			return ""
		}
		if strings.Contains(inner, "UI[Presentation] --> App[Application]") {
			return ""
		}
		return block
	})
}

// stripGeneratedStructureSection removes a "结构示意" / "Structure diagram"
// section on re-polish, but only when its content is nothing but mermaid
// fences — exactly the shape ensureMermaidDiagrams writes. Leaving it in
// place is not idempotent: stripFillerMermaid removes template diagrams from
// inside it and the shortfall would then be re-added under a separate
// "补充示意" section on the next pass. Authored sections carrying prose under
// the same heading are preserved.
func stripGeneratedStructureSection(s string) string {
	for _, title := range []string{"结构示意", "Structure diagram"} {
		re := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(title) + `\s*$`)
		loc := re.FindStringIndex(s)
		if loc == nil {
			continue
		}
		rest := s[loc[1]:]
		section := rest
		next := regexp.MustCompile(`(?m)^##\s+`).FindStringIndex(rest)
		if next != nil {
			section = rest[:next[0]]
		}
		if strings.TrimSpace(stripMermaid(section)) != "" {
			continue // authored prose under the heading — keep as-is
		}
		if next == nil {
			s = strings.TrimSpace(s[:loc[0]])
		} else {
			s = strings.TrimSpace(s[:loc[0]] + rest[next[0]:])
		}
	}
	return s
}

// hasCodeDependencyDiagram reports whether body already contains a code-dep
// section or a focus-graph fingerprint (classDef focus / role-chain R# ids).
func hasCodeDependencyDiagram(body string) bool {
	if regexp.MustCompile(`(?m)^##\s+(代码依赖示意|Code dependency)\s*$`).MatchString(body) {
		return true
	}
	// Focus-graph fingerprint from focusDependencyMermaid.
	if strings.Contains(body, "classDef focus") {
		return true
	}
	return false
}

// isCodeDependencyBlock reports whether a mermaid fence is our export-built
// code dependency diagram (import focus or role chain).
func isCodeDependencyBlock(block string) bool {
	if strings.Contains(block, "classDef focus") {
		return true
	}
	// Role-chain uses R1/R2 node ids + flowchart LR only — weak signal; rely on
	// section heading for role-chain uniqueness. Here only match focus graphs.
	return false
}

func stripCiteAndTOC(s string) string {
	s = regexp.MustCompile(`(?s)<cite>.*?</cite>`).ReplaceAllString(s, "")
	// Go RE2 has no lookahead; strip TOC section until next ## heading.
	s = stripSectionByTitle(s, "目录")
	s = stripSectionByTitle(s, "Table of Contents")
	return strings.TrimSpace(s)
}

func stripSectionByTitle(s, title string) string {
	// Match "## title" then consume lines until next "## " at line start (or EOF).
	re := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(title) + `\s*$`)
	loc := re.FindStringIndex(s)
	if loc == nil {
		return s
	}
	rest := s[loc[1]:]
	next := regexp.MustCompile(`(?m)^##\s+`).FindStringIndex(rest)
	if next == nil {
		return strings.TrimSpace(s[:loc[0]])
	}
	return strings.TrimSpace(s[:loc[0]] + rest[next[0]:])
}

func extractMermaid(s string) []string {
	re := regexp.MustCompile("(?s)```mermaid\\s*(.*?)```")
	m := re.FindAllStringSubmatch(s, -1)
	var out []string
	for _, g := range m {
		out = append(out, "```mermaid\n"+strings.TrimSpace(g[1])+"\n```")
	}
	return out
}

// writeKnowledgeCards exports a knowledge tree under .wikify/knowledge/<lang>/:
//   - section overview.md + _module.yaml (navigation)
//   - per-page topic cards (<safeTitle>.md) with goal, deps, track, excerpt
// Offline companion to content/; no LLM required.
func writeKnowledgeCards(root string, wiki *models.Wiki, contents map[string]string, lang string) error {
	if wiki == nil || len(wiki.Pages) == 0 {
		return nil
	}
	if lang == "" {
		lang = "zh"
	}
	base := filepath.Join(root, "knowledge", lang)
	// Fresh tree each export.
	_ = os.RemoveAll(base)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}

	type secInfo struct {
		title string
		pages []models.WikiPage
	}
	order := []string{}
	bySec := map[string]*secInfo{}
	for _, p := range wiki.Pages {
		sec := p.Section
		if sec == "" {
			sec = p.Title
		}
		if bySec[sec] == nil {
			bySec[sec] = &secInfo{title: sec}
			order = append(order, sec)
		}
		bySec[sec].pages = append(bySec[sec].pages, p)
	}

	type indexMod struct {
		DirName string   `json:"dir_name"`
		Title   string   `json:"title"`
		Track   string   `json:"track,omitempty"`
		Pages   []string `json:"pages"`
		Cards   []string `json:"cards,omitempty"`
		Count   int      `json:"count"`
	}
	var modules []indexMod
	cardCount := 0
	for _, sec := range order {
		info := bySec[sec]
		dirName := safeKnowledgeDir(sec)
		dir := filepath.Join(base, dirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		track := ""
		if len(info.pages) > 0 {
			track = info.pages[0].Track
			if track == "" {
				track = models.InferTrack(info.pages[0])
			}
		}
		var pagePaths []string
		var cardPaths []string
		var md strings.Builder
		md.WriteString("# ")
		md.WriteString(sec)
		md.WriteString("\n\n")
		if lang == "zh" {
			md.WriteString("本节知识卡由 wikify 从 wiki 目录离线汇总，便于按业务/技术章节浏览。\n\n")
			md.WriteString("## 页面索引\n\n")
		} else {
			md.WriteString("Offline knowledge card aggregated from the wiki catalog.\n\n")
			md.WriteString("## Pages\n\n")
		}
		for _, p := range info.pages {
			cp := p.ContentPath
			if cp == "" {
				cp = p.Title + ".md"
			}
			cp = filepath.ToSlash(strings.TrimPrefix(cp, "content/"))
			pagePaths = append(pagePaths, cp)
			cardName := safeKnowledgeDir(p.Title) + ".md"
			cardPaths = append(cardPaths, dirName+"/"+cardName)
			// relative link from knowledge/<lang>/<dir>/overview.md → content/
			md.WriteString(fmt.Sprintf("- [%s](../../../content/%s)", p.Title, cp))
			if p.Track != "" {
				md.WriteString(" `")
				md.WriteString(p.Track)
				md.WriteString("`")
			}
			md.WriteString(fmt.Sprintf(" · [卡](./%s)", cardName))
			md.WriteString("\n")

			// Per-page topic card.
			var card strings.Builder
			card.WriteString("# ")
			card.WriteString(p.Title)
			card.WriteString("\n\n")
			if lang == "zh" {
				card.WriteString("| 字段 | 值 |\n| --- | --- |\n")
				card.WriteString(fmt.Sprintf("| 章节 | %s |\n", p.Section))
				tr := p.Track
				if tr == "" {
					tr = models.InferTrack(p)
				}
				card.WriteString(fmt.Sprintf("| 轨道 | %s |\n", tr))
				if p.Level != "" {
					card.WriteString(fmt.Sprintf("| 难度 | %s |\n", p.Level))
				}
				if p.Parent != "" {
					card.WriteString(fmt.Sprintf("| 父主题 | %s |\n", p.Parent))
				}
				card.WriteString(fmt.Sprintf("| 内容 | [打开正文](../../../content/%s) |\n\n", cp))
				if p.Goal != "" {
					card.WriteString("## 目标\n\n")
					card.WriteString(p.Goal)
					card.WriteString("\n\n")
				}
				if len(p.DependentFiles) > 0 {
					card.WriteString("## 关键源文件\n\n")
					for _, f := range p.DependentFiles {
						card.WriteString(fmt.Sprintf("- `%s`\n", f))
					}
					card.WriteString("\n")
				}
				card.WriteString("## 摘要\n\n")
			} else {
				card.WriteString("| Field | Value |\n| --- | --- |\n")
				card.WriteString(fmt.Sprintf("| Section | %s |\n", p.Section))
				tr := p.Track
				if tr == "" {
					tr = models.InferTrack(p)
				}
				card.WriteString(fmt.Sprintf("| Track | %s |\n", tr))
				if p.Level != "" {
					card.WriteString(fmt.Sprintf("| Level | %s |\n", p.Level))
				}
				if p.Parent != "" {
					card.WriteString(fmt.Sprintf("| Parent | %s |\n", p.Parent))
				}
				card.WriteString(fmt.Sprintf("| Content | [Open](../../../content/%s) |\n\n", cp))
				if p.Goal != "" {
					card.WriteString("## Goal\n\n")
					card.WriteString(p.Goal)
					card.WriteString("\n\n")
				}
				if len(p.DependentFiles) > 0 {
					card.WriteString("## Dependent files\n\n")
					for _, f := range p.DependentFiles {
						card.WriteString(fmt.Sprintf("- `%s`\n", f))
					}
					card.WriteString("\n")
				}
				card.WriteString("## Summary\n\n")
			}
			// Excerpt from body (strip cite/toc/mermaid noise).
			body := contents[p.Slug]
			excerpt := knowledgeExcerpt(body, 400)
			if excerpt == "" {
				if lang == "zh" {
					excerpt = "（正文摘要暂缺，请查看 content 目录下完整页面。）"
				} else {
					excerpt = "(No excerpt yet — open the full page under content/.)"
				}
			}
			card.WriteString(excerpt)
			card.WriteString("\n")
			if err := os.WriteFile(filepath.Join(dir, cardName), []byte(card.String()), 0o644); err != nil {
				return err
			}
			cardCount++
		}
		// Related sections (other sections sharing track)
		md.WriteString("\n")
		if lang == "zh" {
			md.WriteString("## 相邻章节\n\n")
		} else {
			md.WriteString("## Related sections\n\n")
		}
		nRel := 0
		for _, other := range order {
			if other == sec {
				continue
			}
			oi := bySec[other]
			otr := ""
			if len(oi.pages) > 0 {
				otr = oi.pages[0].Track
				if otr == "" {
					otr = models.InferTrack(oi.pages[0])
				}
			}
			if track != "" && otr != "" && otr != track {
				continue
			}
			md.WriteString(fmt.Sprintf("- [%s](../%s/overview.md)\n", other, safeKnowledgeDir(other)))
			nRel++
			if nRel >= 8 {
				break
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "overview.md"), []byte(md.String()), 0o644); err != nil {
			return err
		}
		modYAML := fmt.Sprintf("schema_version: 1\nmodule_path: %s\ntitle: %s\ntrack: %s\npage_count: %d\ncard_count: %d\n",
			dirName, sec, track, len(info.pages), len(info.pages))
		if err := os.WriteFile(filepath.Join(dir, "_module.yaml"), []byte(modYAML), 0o644); err != nil {
			return err
		}
		modules = append(modules, indexMod{
			DirName: dirName, Title: sec, Track: track, Pages: pagePaths, Cards: cardPaths, Count: len(info.pages),
		})
	}

	idx := map[string]any{
		"schema_version": 1,
		"locale":         lang,
		"generator":      "wikify",
		"modules":        modules,
		"document_count": len(wiki.Pages),
		"section_count":  len(modules),
		"card_count":     cardCount,
	}
	ib, _ := json.MarshalIndent(idx, "", "  ")
	return os.WriteFile(filepath.Join(base, "_index.json"), ib, 0o644)
}

// knowledgeExcerpt pulls a short plain-text summary from page markdown.
func knowledgeExcerpt(body string, maxRunes int) string {
	if body == "" {
		return ""
	}
	s := stripCiteAndTOC(stripMermaid(body))
	// Drop leading H1
	s = stripLeadingTitle(s)
	// Collapse fenced code
	reCode := regexp.MustCompile("(?s)```.*?```")
	s = reCode.ReplaceAllString(s, "")
	// Drop markdown headings markers for readability
	lines := strings.Split(s, "\n")
	var kept []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			if len(kept) > 0 && kept[len(kept)-1] != "" {
				kept = append(kept, "")
			}
			continue
		}
		if strings.HasPrefix(t, "#") {
			// keep heading text only
			t = strings.TrimSpace(strings.TrimLeft(t, "#"))
			if t == "" {
				continue
			}
		}
		if strings.HasPrefix(t, ">") || strings.HasPrefix(t, "|") {
			continue
		}
		kept = append(kept, t)
		if len([]rune(strings.Join(kept, " "))) >= maxRunes {
			break
		}
	}
	out := strings.TrimSpace(strings.Join(kept, "\n"))
	runes := []rune(out)
	if maxRunes > 0 && len(runes) > maxRunes {
		out = string(runes[:maxRunes]) + "…"
	}
	return out
}


func safeKnowledgeDir(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "section"
	}
	// Keep CJK and alnum; replace path-hostile chars.
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t':
			return '-'
		}
		return r
	}, name)
	return name
}

func escapeMermaid(s string) string {
	s = strings.ReplaceAll(s, `"`, "'")
	s = strings.ReplaceAll(s, "[", "(")
	s = strings.ReplaceAll(s, "]", ")")
	return s
}

func fallbackSection(name, title string, model *scan.Model, zh bool) string {
	repo := ""
	if model != nil {
		repo = model.Name
	}
	if zh {
		if repo == "" {
			repo = "本仓库"
		}
		switch name {
		case "概述":
			return fmt.Sprintf("本章说明「%s」在 %s 中的定位、职责与阅读价值，并为后续章节提供统一术语与上下文。", title, repo)
		case "范围与前提":
			return fmt.Sprintf("本章界定「%s」的讨论边界、适用读者及阅读前所需的背景知识或运行环境。", title)
		case "系统结构":
			return fmt.Sprintf("本章描述与「%s」相关的模块划分、目录组织及主要控制流，作为后续设计说明的基础。", title)
		case "核心设计":
			return fmt.Sprintf("本章归纳「%s」的关键设计决策、不变量与协作关系，并辅以结构示意。", title)
		case "实现要点":
			return fmt.Sprintf("本章结合源码说明「%s」的关键实现路径、扩展点与值得注意的边界条件。", title)
		case "依赖与集成":
			return fmt.Sprintf("本章梳理「%s」对外依赖、被依赖关系及与相邻子系统的集成方式。", title)
		case "运行与运维":
			return fmt.Sprintf("本章说明「%s」相关的配置项、运行方式与运维关注点（如有）。", title)
		case "故障排查":
			return fmt.Sprintf("本章列出与「%s」相关的常见故障现象、可能原因及排查步骤。", title)
		case "小结":
			return fmt.Sprintf("本章回顾「%s」的要点，并给出延伸阅读与后续章节的指引。", title)
		case "附录":
			return fmt.Sprintf("附录汇集「%s」相关的补充材料，例如术语表、配置样例或索引。", title)
		default:
			return fmt.Sprintf("本章围绕「%s」主题中的「%s」进行说明，论述以仓库源码与配置为准。", title, name)
		}
	}
	if repo == "" {
		repo = "this repository"
	}
	switch name {
	case "Overview":
		return fmt.Sprintf("This chapter states the role, responsibilities, and reading value of \"%s\" within %s, and establishes shared terminology for subsequent sections.", title, repo)
	case "Scope and Prerequisites":
		return fmt.Sprintf("This chapter defines the scope of \"%s\", the intended audience, and any background knowledge or runtime prerequisites.", title)
	case "System Structure":
		return fmt.Sprintf("This chapter describes module boundaries, directory layout, and primary control flow related to \"%s\".", title)
	case "Core Design":
		return fmt.Sprintf("This chapter summarises the principal design decisions, invariants, and collaboration patterns for \"%s\".", title)
	case "Implementation Notes":
		return fmt.Sprintf("This chapter walks through the key implementation paths, extension points, and boundary conditions for \"%s\", grounded in source code.", title)
	case "Dependencies and Integration":
		return fmt.Sprintf("This chapter catalogues external dependencies, reverse dependencies, and integration points associated with \"%s\".", title)
	case "Operations":
		return fmt.Sprintf("This chapter covers configuration, runtime behaviour, and operational considerations for \"%s\" where applicable.", title)
	case "Troubleshooting":
		return fmt.Sprintf("This chapter lists common failure modes, likely causes, and diagnostic steps related to \"%s\".", title)
	case "Summary":
		return fmt.Sprintf("This chapter recaps the essential points of \"%s\" and points to related documentation.", title)
	case "Appendix":
		return fmt.Sprintf("The appendix collects supplementary material for \"%s\", such as glossaries, sample configuration, or indexes.", title)
	default:
		return fmt.Sprintf("This chapter addresses \"%s\" under the topic \"%s\", with claims grounded in repository sources.", name, title)
	}
}

func anchor(name string) string {
	// Simple github-like anchor
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	return regexp.MustCompile(`[^\p{L}\p{N}-]`).ReplaceAllString(s, "")
}
