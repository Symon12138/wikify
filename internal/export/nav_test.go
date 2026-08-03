package export

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/JSHurt/wikify/internal/models"
	"github.com/JSHurt/wikify/internal/scan"
)

// ── NAV-1 ─────────────────────────────────────────────────────────────────────

func TestBuildSharedEvidencePairRule(t *testing.T) {
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "甲", Slug: "a", DependentFiles: []string{
			"src/service/OrderService.java", "src/util/Helper.java", "src/misc/Notes.java"}},
		{Title: "乙", Slug: "b"}, // evidence via body cite only
		{Title: "丙", Slug: "c", DependentFiles: []string{"src/util/Helper.java"}},
		{Title: "丁", Slug: "d", DependentFiles: []string{"src/util/Helper.java", "src/misc/Notes.java"}},
	}}
	contents := map[string]string{
		"b": "# 乙\n\n见 [svc](file://src/service/OrderService.java#L1-L20)。\n",
	}
	shared := buildSharedEvidence(wiki, contents, 5)

	refs := shared["a"]
	if len(refs) != 2 {
		t.Fatalf("a refs=%d want 2 (%+v)", len(refs), refs)
	}
	// Order: shared-count desc → 丁 (2 files) before 乙 (1 role file).
	if refs[0].Page.Slug != "d" || refs[0].Shared != 2 {
		t.Fatalf("a first ref=%+v want d/2", refs[0])
	}
	if refs[1].Page.Slug != "b" || refs[1].Shared != 1 {
		t.Fatalf("a second ref=%+v want b/1", refs[1])
	}
	// Role-bearing representative file for the single-file pair.
	if refs[1].File != "src/service/OrderService.java" {
		t.Fatalf("a↔b representative=%q", refs[1].File)
	}
	// c shares only 1 role-less file (Helper.java) with a and d → no relation.
	if len(shared["c"]) != 0 {
		t.Fatalf("c must have no shared-evidence refs, got %+v", shared["c"])
	}
	// Symmetry: b sees a.
	found := false
	for _, r := range shared["b"] {
		if r.Page.Slug == "a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("b refs miss a: %+v", shared["b"])
	}
}

func TestBuildSharedEvidenceCapAndOrder(t *testing.T) {
	pages := []models.WikiPage{{Title: "X", Slug: "x",
		DependentFiles: []string{"src/a/F1.java", "src/a/F2.java"}}}
	for i := 1; i <= 7; i++ {
		pages = append(pages, models.WikiPage{
			Title: fmt.Sprintf("P%d", i), Slug: fmt.Sprintf("p%d", i),
			DependentFiles: []string{"src/a/F1.java", "src/a/F2.java"},
		})
	}
	shared := buildSharedEvidence(&models.Wiki{Pages: pages}, nil, 5)
	refs := shared["x"]
	if len(refs) != 5 {
		t.Fatalf("cap violated: %d refs", len(refs))
	}
	// Equal counts → title asc: P1..P5.
	for i, r := range refs {
		want := fmt.Sprintf("P%d", i+1)
		if r.Page.Title != want {
			t.Fatalf("refs[%d]=%q want %q", i, r.Page.Title, want)
		}
	}
}

func TestEnsureRelatedNavSharedGroupAndDedupe(t *testing.T) {
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "主页", Slug: "p", Section: "S1", ContentPath: "S1/主页.md", Track: models.TrackBusiness},
		{Title: "邻居", Slug: "q", Section: "S1", ContentPath: "S1/邻居.md", Track: models.TrackBusiness},
		{Title: "远页", Slug: "r", Section: "S2", ContentPath: "S2/远页.md", Track: models.TrackTechnical},
	}}
	shared := []sharedEvidenceRef{
		{Page: wiki.Pages[1], Shared: 2, File: "src/service/A.java"}, // sibling — must dedupe
		{Page: wiki.Pages[2], Shared: 1, File: "src/service/A.java"},
	}
	out := ensureRelatedNavShared("# 主页\n\n正文。\n", wiki, wiki.Pages[0], true, shared)
	if !strings.Contains(out, "**共享证据**") {
		t.Fatalf("missing 共享证据 group:\n%s", out)
	}
	if !strings.Contains(out, "[远页](S2/远页.md)") {
		t.Fatalf("missing shared link to 远页:\n%s", out)
	}
	// 邻居 already sits in 同章节 — never repeated under 共享证据.
	if n := strings.Count(out, "[邻居]("); n != 1 {
		t.Fatalf("邻居 linked %d times, want 1:\n%s", n, out)
	}
	// English labels.
	outEn := ensureRelatedNavShared("# Main\n\nBody.\n", wiki, wiki.Pages[0], false, shared)
	if !strings.Contains(outEn, "**Shared evidence**") {
		t.Fatalf("missing Shared evidence group:\n%s", outEn)
	}
	// Idempotent: re-running strips and rebuilds without duplication.
	again := ensureRelatedNavShared(out, wiki, wiki.Pages[0], true, shared)
	if again != out {
		t.Fatalf("ensureRelatedNavShared not idempotent:\n--1--\n%s\n--2--\n%s", out, again)
	}
}

func TestMetadataSharedEvidenceRelations(t *testing.T) {
	model := &scan.Model{Name: "demo", Language: "zh", GeneratedAt: "2026-01-01T00:00:00Z"}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "甲", Slug: "a", Section: "SA", Track: models.TrackBusiness,
			DependentFiles: []string{"src/service/OrderService.java"}},
		{Title: "乙", Slug: "b", Section: "SB", Track: models.TrackTechnical,
			DependentFiles: []string{"src/service/OrderService.java"}},
	}}
	meta := buildMetadata(model, wiki, map[string]string{}, "zh")
	raw, _ := json.Marshal(meta["knowledge_relations"])
	if !strings.Contains(string(raw), "Shared evidence: src/service/OrderService.java") {
		t.Fatalf("missing shared-evidence RELATED_TO relation: %s", raw)
	}
	// Dedupe: exactly one relation carries the marker (undirected pair).
	if n := strings.Count(string(raw), "Shared evidence: "); n != 1 {
		t.Fatalf("shared-evidence relations=%d want 1: %s", n, raw)
	}
}

// ── NAV-3 (diagram unit) ──────────────────────────────────────────────────────

func TestRepoArchitectureMermaidSkipAndSubgraphs(t *testing.T) {
	if g := repoArchitectureMermaid(nil); g != "" {
		t.Fatalf("nil model → %q", g)
	}
	one := &scan.Model{Modules: []scan.ModuleSummary{{Name: "solo", Path: "solo"}}}
	if g := repoArchitectureMermaid(one); g != "" {
		t.Fatalf("<2 modules must skip, got %q", g)
	}

	m := &scan.Model{
		Modules: []scan.ModuleSummary{
			{Name: "core", Path: "app/core"},
			{Name: "web", Path: "app/web"},
			{Name: "util", Path: "lib/util"},
		},
		ModuleDeps: []scan.ModuleDep{
			{From: "app/web", To: "app/core", Weight: 3},
			{From: "app/core", To: "lib/util", Weight: 1},
		},
	}
	g := repoArchitectureMermaid(m)
	if !strings.HasPrefix(g, "```mermaid\ngraph TD\n") {
		t.Fatalf("not a graph TD block:\n%s", g)
	}
	// Multi-root (app + lib) → per-root subgraphs.
	if strings.Count(g, "subgraph ") != 2 {
		t.Fatalf("want 2 subgraphs:\n%s", g)
	}
	if !strings.Contains(g, `["app"]`) || !strings.Contains(g, `["lib"]`) {
		t.Fatalf("missing root labels:\n%s", g)
	}
	if strings.Count(g, "-->") != 2 {
		t.Fatalf("want 2 edges:\n%s", g)
	}
	// Weight-desc ordering: web→core (w3) precedes core→util (w1).
	if i, j := strings.Index(g, "M1 --> M0"), strings.Index(g, "M0 --> M2"); i < 0 || j < 0 || i > j {
		t.Fatalf("edge order wrong (i=%d j=%d):\n%s", i, j, g)
	}
}

func TestRepoArchitectureMermaidEdgeCapAndImportFallback(t *testing.T) {
	// 7 modules, all 42 directed pairs → capped at 30 edges.
	var m scan.Model
	for i := 0; i < 7; i++ {
		m.Modules = append(m.Modules, scan.ModuleSummary{
			Name: fmt.Sprintf("m%d", i), Path: fmt.Sprintf("m%d", i)})
	}
	for i := 0; i < 7; i++ {
		for j := 0; j < 7; j++ {
			if i != j {
				m.ModuleDeps = append(m.ModuleDeps, scan.ModuleDep{
					From: fmt.Sprintf("m%d", i), To: fmt.Sprintf("m%d", j), Weight: 1})
			}
		}
	}
	g := repoArchitectureMermaid(&m)
	if n := strings.Count(g, "-->"); n != 30 {
		t.Fatalf("edge cap: got %d edges, want 30", n)
	}

	// No ModuleDeps → aggregate ImportEdges; same_package ignored; dedup.
	m2 := &scan.Model{
		Modules: []scan.ModuleSummary{{Name: "a", Path: "a"}, {Name: "b", Path: "b"}},
		ImportEdges: []scan.ImportEdge{
			{From: "a/x.go", To: "b/y.go", Kind: "import"},
			{From: "a/z.go", To: "b/y.go", Kind: "import"},
			{From: "a/x.go", To: "a/z.go", Kind: "same_package"},
		},
	}
	g2 := repoArchitectureMermaid(m2)
	if n := strings.Count(g2, "-->"); n != 1 {
		t.Fatalf("import fallback edges=%d want 1:\n%s", n, g2)
	}
	if !strings.Contains(g2, "M0 --> M1") {
		t.Fatalf("missing aggregated a→b edge:\n%s", g2)
	}
}

// ── NAV-2/NAV-3/NAV-4/NAV-5 through the full Export pipeline ─────────────────

func navTestModel() *scan.Model {
	return &scan.Model{
		Name: "demo", Language: "zh", GeneratedAt: "2026-01-01T00:00:00Z",
		Modules: []scan.ModuleSummary{
			{Name: "app", Path: "app", Files: []scan.FileInfo{{RelativePath: "app/src/OrderService.java"}}},
			{Name: "api", Path: "api", Files: []scan.FileInfo{{RelativePath: "api/src/Gate.java"}}},
		},
		ModuleDeps: []scan.ModuleDep{{From: "api", To: "app", Weight: 2}},
		Files: []scan.FileInfo{
			{RelativePath: "app/src/OrderService.java", Lines: 80},
			{RelativePath: "api/src/Gate.java", Lines: 40},
		},
		EntryPoints: []scan.EntryPoint{{Path: "api/src/Gate.java", Kind: "api", Symbol: "Gate"}},
	}
}

func navTestWiki() (*models.Wiki, map[string]string) {
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Slug: "ov", Section: "项目概述", ContentPath: "项目概述.md",
			Track: models.TrackFoundation},
		{Title: "订单受理", Slug: "o1", Section: "订单模块", ContentPath: "订单模块/订单受理.md",
			Track: models.TrackBusiness, DependentFiles: []string{"app/src/OrderService.java"}},
		{Title: "订单结算", Slug: "o2", Section: "订单模块", ContentPath: "订单模块/订单结算.md",
			Track: models.TrackBusiness, DependentFiles: []string{"app/src/OrderService.java"}},
	}}
	contents := map[string]string{
		"ov": "# 项目概述\n\n## 定位\n\n演示项目，覆盖订单受理与结算流程说明。\n\n## 范围\n\n本仓库为演示仓库。\n",
		"o1": "# 订单受理\n\n## 流程\n\n受理逻辑见 [svc](file://app/src/OrderService.java#L1-L40)。\n\n## 说明\n\n受理入口校验参数。\n",
		"o2": "# 订单结算\n\n## 流程\n\n结算逻辑见 [svc](file://app/src/OrderService.java#L41-L80)。\n\n## 说明\n\n结算按日汇总。\n",
	}
	return wiki, contents
}

func TestExportSynthesizesArchOverviewFirstAndSectionIndex(t *testing.T) {
	dir := t.TempDir()
	wiki, contents := navTestWiki()
	if err := Export(dir, navTestModel(), wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved models.Wiki
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	// NAV-3: arch overview registered FIRST → browse '/' (Pages[0]) lands on it.
	if len(saved.Pages) == 0 || saved.Pages[0].Title != "架构总览" {
		t.Fatalf("Pages[0]=%+v want 架构总览 first", saved.Pages[0])
	}
	if !strings.HasPrefix(saved.Pages[0].DescriptionSlug, archOverviewMarker+"-") {
		t.Fatalf("arch marker slug=%q", saved.Pages[0].DescriptionSlug)
	}
	arch, err := os.ReadFile(filepath.Join(dir, ".wikify", "content", "架构总览.md"))
	if err != nil {
		t.Fatal(err)
	}
	as := string(arch)
	for _, want := range []string{"graph TD", "```mermaid", "| 模块 | 文件数 | 关键入口 |", "| `app` | 1 |", "Gate.java"} {
		if !strings.Contains(as, want) {
			t.Fatalf("arch page missing %q:\n%s", want, as)
		}
	}
	// NAV-2: 订单模块 has 2 children and no root page → index synthesized.
	var secIdx *models.WikiPage
	for i := range saved.Pages {
		if strings.HasPrefix(saved.Pages[i].DescriptionSlug, sectionIndexMarker+"-") {
			secIdx = &saved.Pages[i]
		}
	}
	if secIdx == nil || secIdx.Title != "订单模块" || secIdx.ContentPath != "订单模块/订单模块.md" {
		t.Fatalf("section index page=%+v", secIdx)
	}
	sec, err := os.ReadFile(filepath.Join(dir, ".wikify", "content", "订单模块", "订单模块.md"))
	if err != nil {
		t.Fatal(err)
	}
	ss := string(sec)
	if !strings.Contains(ss, "[订单受理](订单模块/订单受理.md)") || !strings.Contains(ss, "[订单结算](订单模块/订单结算.md)") {
		t.Fatalf("section index missing child links:\n%s", ss)
	}
	// 项目概述 (section == title, 1 child) gets no index; no eng pages invented.
	for _, p := range saved.Pages {
		if p.Title == "项目概述" && strings.HasPrefix(p.DescriptionSlug, sectionIndexMarker) {
			t.Fatalf("unexpected index for 项目概述: %+v", p)
		}
	}
}

func TestExportArchOverviewSkippedUnderTwoModules(t *testing.T) {
	dir := t.TempDir()
	wiki, contents := navTestWiki()
	m := navTestModel()
	m.Modules = m.Modules[:1] // 1 module → keep old '/' behaviour
	m.ModuleDeps = nil
	if err := Export(dir, m, wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki.json"))
	var saved models.Wiki
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Pages[0].Title != "项目概述" {
		t.Fatalf("Pages[0]=%q want 项目概述 (no arch page)", saved.Pages[0].Title)
	}
	if _, err := os.Stat(filepath.Join(dir, ".wikify", "content", "架构总览.md")); err == nil {
		t.Fatal("arch page must not exist for <2 modules")
	}
}

func TestExportSectionIndexRespectsExistingRootPage(t *testing.T) {
	dir := t.TempDir()
	wiki, contents := navTestWiki()
	// LLM already authored a section root page 订单模块/订单模块.md.
	wiki.Pages = append(wiki.Pages, models.WikiPage{
		Title: "订单模块", Slug: "root", Section: "订单模块",
		ContentPath: "订单模块/订单模块.md", Track: models.TrackBusiness,
	})
	contents["root"] = "# 订单模块\n\n## 概览\n\n作者撰写的模块总览。\n\n## 边界\n\n订单域。\n"
	if err := Export(dir, navTestModel(), wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki.json"))
	var saved models.Wiki
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	for _, p := range saved.Pages {
		if strings.HasPrefix(p.DescriptionSlug, sectionIndexMarker+"-") {
			t.Fatalf("index synthesized despite existing root page: %+v", p)
		}
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".wikify", "content", "订单模块", "订单模块.md"))
	if !strings.Contains(string(body), "作者撰写的模块总览") {
		t.Fatalf("LLM root page overwritten:\n%s", body)
	}
}

// hashTreeAndIndexes fingerprints all content bytes + the NAV meta artifacts.
func hashTreeAndIndexes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	contentDir := filepath.Join(dir, ".wikify", "content")
	err := filepath.WalkDir(contentDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(contentDir, path)
		out["content/"+filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"search-index.json", "file-page-index.json", "wiki.json", "browse-index.json"} {
		b, rerr := os.ReadFile(filepath.Join(dir, ".wikify", "meta", name))
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		out["meta/"+name] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	return out
}

func TestExportNavTwiceIdempotent(t *testing.T) {
	dir := t.TempDir()
	wiki, contents := navTestWiki()
	if err := Export(dir, navTestModel(), wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	first := hashTreeAndIndexes(t, dir)
	// Second run receives the wiki/contents mutated by the first (polish path).
	if err := Export(dir, navTestModel(), wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	second := hashTreeAndIndexes(t, dir)
	if len(first) != len(second) {
		t.Fatalf("file sets differ: %d vs %d", len(first), len(second))
	}
	var keys []string
	for k := range first {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if first[k] != second[k] {
			t.Fatalf("non-idempotent artifact %s", k)
		}
	}
	// No accumulated nav pages after the second run.
	raw, _ := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki.json"))
	var saved models.Wiki
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	arch, secIdx := 0, 0
	for _, p := range saved.Pages {
		if strings.HasPrefix(p.DescriptionSlug, archOverviewMarker+"-") {
			arch++
		}
		if strings.HasPrefix(p.DescriptionSlug, sectionIndexMarker+"-") {
			secIdx++
		}
	}
	if arch != 1 || secIdx != 1 {
		t.Fatalf("nav pages accumulated: arch=%d secIdx=%d", arch, secIdx)
	}
}

func TestExportWritesSearchAndFilePageIndexes(t *testing.T) {
	dir := t.TempDir()
	wiki, contents := navTestWiki()
	if err := Export(dir, navTestModel(), wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	// NAV-4: meta/search-index.json — one entry per page (incl. synthesized).
	sb, err := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "search-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []SearchIndexEntry
	if err := json.Unmarshal(sb, &entries); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki.json"))
	var saved models.Wiki
	_ = json.Unmarshal(raw, &saved)
	if len(entries) != len(saved.Pages) {
		t.Fatalf("search entries=%d pages=%d", len(entries), len(saved.Pages))
	}
	byTitle := map[string]SearchIndexEntry{}
	for _, e := range entries {
		if e.Title == "" || e.Path == "" || !utf8.ValidString(e.Excerpt) {
			t.Fatalf("bad entry %+v", e)
		}
		byTitle[e.Title] = e
	}
	if e := byTitle["订单受理"]; e.Path != "订单模块/订单受理.md" || !strings.Contains(e.Excerpt, "受理") {
		t.Fatalf("订单受理 entry=%+v", e)
	}
	if strings.Contains(byTitle["订单受理"].Excerpt, "```") {
		t.Fatal("excerpt leaked code fence")
	}

	// NAV-5: meta/file-page-index.json — evidence file → citing pages.
	fb, err := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "file-page-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var idx map[string][]FilePageRef
	if err := json.Unmarshal(fb, &idx); err != nil {
		t.Fatal(err)
	}
	// rebindDependentFiles may legitimately bind further pages (the fixture
	// model has only two files), so assert membership, not an exact count.
	refs := idx["app/src/OrderService.java"]
	got := map[string]bool{}
	for _, r := range refs {
		if r.Slug == "" || r.Path == "" {
			t.Fatalf("bad ref %+v", r)
		}
		got[r.Title] = true
	}
	if !got["订单受理"] || !got["订单结算"] {
		t.Fatalf("OrderService citing pages=%+v want both order pages", refs)
	}
}
