package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Symon12138/wikify/internal/models"
	"github.com/Symon12138/wikify/internal/planner"
	"github.com/Symon12138/wikify/internal/scan"
	"github.com/Symon12138/wikify/internal/wikiplan"
)

func TestBindEvidenceFillsEmptyDeps(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Files: []scan.FileInfo{
			{RelativePath: "src/order/OrderService.java", Lines: 40},
			{RelativePath: "src/order/OrderController.java", Lines: 30},
			{RelativePath: "src/misc/Util.java", Lines: 10},
		},
	}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "订单管理", Goal: "order service", Slug: "1", DependentFiles: nil},
		{Title: "已有证据", Slug: "2", DependentFiles: []string{"src/misc/Util.java"}},
	}}
	bindEvidence(wiki, m)
	if len(wiki.Pages[0].DependentFiles) == 0 {
		t.Fatal("expected dependent files for empty page")
	}
	// Existing deps preserved (not overwritten).
	if len(wiki.Pages[1].DependentFiles) != 1 || wiki.Pages[1].DependentFiles[0] != "src/misc/Util.java" {
		t.Fatalf("existing deps changed: %v", wiki.Pages[1].DependentFiles)
	}
	if wiki.Pages[0].DescriptionSlug == "" {
		t.Fatal("expected description slug fill")
	}
}

func TestBuildCatalogAppliesScope(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src", "main", "A.java"), "class A {}")
	mustWrite(t, filepath.Join(dir, "src", "test", "T.java"), "class T {}")
	mustWrite(t, filepath.Join(dir, "vendor", "v.go"), "package v")

	planDir := filepath.Join(dir, ".wikify")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `version: 1
wiki:
  template: architecture
  notes: []
  documents: []
scope:
  include:
    - src/**
  exclude:
    - src/test/**
`
	if err := os.WriteFile(filepath.Join(planDir, "wiki_plan.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	repoModel, err := scan.Scan(dir, "zh", scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := wikiplan.Read(dir)
	if err != nil || plan == nil {
		t.Fatalf("plan: %v %+v", err, plan)
	}
	if !plan.HasScope() {
		t.Fatal("expected scope")
	}
	repoModel.ApplyScope(plan.ScopeInclude(), plan.ScopeExclude())
	for _, f := range repoModel.Files {
		p := f.RelativePath
		if strings.Contains(p, "test") || strings.Contains(p, "vendor") {
			t.Fatalf("out-of-scope file kept: %s", p)
		}
	}
	if len(repoModel.Files) == 0 {
		t.Fatal("all files filtered")
	}

	wiki := planner.Build(repoModel, plan, planner.Options{MaxPages: 20})
	if wiki == nil || len(wiki.Pages) == 0 {
		t.Fatal("empty wiki from scoped model")
	}
	planner.TrimWikiByRail(wiki, 10)
	if len(wiki.Pages) > 10 {
		t.Fatalf("trim failed: %d", len(wiki.Pages))
	}
}

func TestTrimWikiByRailKeepsRails(t *testing.T) {
	w := &models.Wiki{}
	for i := 0; i < 5; i++ {
		w.Pages = append(w.Pages, models.WikiPage{
			Title: "F" + string(rune('a'+i)), Section: "项目概述", Track: models.TrackFoundation,Slug: "f" + string(rune('a'+i)),
		})
	}
	for i := 0; i < 12; i++ {
		w.Pages = append(w.Pages, models.WikiPage{
			Title: "B" + string(rune('a'+i%26)), Section: "订单模块", Track: models.TrackBusiness,Slug: "b" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
		})
	}
	for i := 0; i < 20; i++ {
		w.Pages = append(w.Pages, models.WikiPage{
			Title: "T" + string(rune('a'+i%26)), Section: "接口文档", Track: models.TrackTechnical,Slug: "t" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
		})
	}
	planner.TrimWikiByRail(w, 15)
	if len(w.Pages) != 15 {
		t.Fatalf("len=%d want 15", len(w.Pages))
	}
	var f, b, tech int
	for _, p := range w.Pages {
		switch p.Track {
		case models.TrackFoundation:
			f++
		case models.TrackBusiness:
			b++
		default:
			tech++
		}
	}
	if f == 0 || b == 0 || tech == 0 {
		t.Fatalf("rail wiped: f=%d b=%d t=%d", f, b, tech)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}


func TestSeedDraftsFromPublished(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".wikify")
	content := filepath.Join(root, "content")
	meta := filepath.Join(root, "meta")
	if err := os.MkdirAll(filepath.Join(content, "sec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	// substantial body
	sub := "# 实质页\n\n## 目的\n\n" + strings.Repeat("说明业务能力与实现路径。", 40) + "\n\n## 细节\n\n更多内容。\n"
	if err := os.WriteFile(filepath.Join(content, "sec", "实质页.md"), []byte(sub), 0o644); err != nil {
		t.Fatal(err)
	}
	// stub body
	stub := "# 空壳\n\n> **状态:** 待补充\n\n已写入占位稿。\n"
	if err := os.WriteFile(filepath.Join(content, "sec", "空壳.md"), []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "实质页", Slug: "1", Section: "sec", ContentPath: "sec/实质页.md", Track: models.TrackBusiness},
		{Title: "空壳", Slug: "2", Section: "sec", ContentPath: "sec/空壳.md", Track: models.TrackTechnical},
	}}
	b, err := json.Marshal(wiki)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta, "wiki.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(root, "drafts")
	kept, stubs, shallow, err := seedDraftsFromPublished(dir, draft, false)
	if err != nil {
		t.Fatal(err)
	}
	if kept != 1 || stubs != 1 || shallow != 0 {
		t.Fatalf("kept=%d stubs=%d shallow=%d want 1,1,0", kept, stubs, shallow)
	}
	if _, err := os.Stat(filepath.Join(draft, "1.md")); err != nil {
		t.Fatal("substantial draft missing")
	}
	if _, err := os.Stat(filepath.Join(draft, "2.md")); err == nil {
		t.Fatal("stub should not be pre-seeded as done")
	}
	if _, err := os.Stat(filepath.Join(draft, "wiki.json")); err != nil {
		t.Fatal("draft wiki.json missing")
	}
}

func TestSeedDraftsFromPublishedEnrichShallow(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".wikify")
	content := filepath.Join(root, "content")
	meta := filepath.Join(root, "meta")
	if err := os.MkdirAll(filepath.Join(content, "sec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	// Deep page: ≥4 H2, ≥3 distinct cite paths, ≥1 章节来源 block.
	deep := "# 深页\n\n" +
		"## 概述\n\n业务能力说明 [a](file://src/a.go#L1-L10)\n\n**章节来源**\n- [a](file://src/a.go#L1-L10)\n\n" +
		"## 结构\n\n结构说明 [b](file://src/b.go#L1-L10)\n\n" +
		"## 实现\n\n实现说明 [c](file://src/c.go#L1-L10)\n\n" +
		"## 小结\n\n" + strings.Repeat("总结与要点。", 20) + "\n"
	if err := os.WriteFile(filepath.Join(content, "sec", "深页.md"), []byte(deep), 0o644); err != nil {
		t.Fatal(err)
	}
	// Shallow-but-substantial page: 2 H2 (passes isSubstantialBody), 1 cite,
	// no section-source block → DepthScore .Shallow().
	shallowBody := "# 浅页\n\n## 目的\n\n" + strings.Repeat("说明业务能力与实现路径。", 40) +
		"\n\n## 细节\n\n更多内容 [a](file://src/a.go#L1-L5)。\n"
	if err := os.WriteFile(filepath.Join(content, "sec", "浅页.md"), []byte(shallowBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stub page.
	stubBody := "# 空壳\n\n> **状态:** 待补充\n\n已写入占位稿。\n"
	if err := os.WriteFile(filepath.Join(content, "sec", "空壳.md"), []byte(stubBody), 0o644); err != nil {
		t.Fatal(err)
	}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "深页", Slug: "1", Section: "sec", ContentPath: "sec/深页.md", Track: models.TrackBusiness},
		{Title: "浅页", Slug: "2", Section: "sec", ContentPath: "sec/浅页.md", Track: models.TrackBusiness},
		{Title: "空壳", Slug: "3", Section: "sec", ContentPath: "sec/空壳.md", Track: models.TrackTechnical},
	}}
	b, err := json.Marshal(wiki)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta, "wiki.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(root, "drafts")

	// enrichShallow off: shallow page is kept like any substantial page.
	kept, stubs, shallow, err := seedDraftsFromPublished(dir, draft, false)
	if err != nil {
		t.Fatal(err)
	}
	if kept != 2 || stubs != 1 || shallow != 0 {
		t.Fatalf("no-enrich: kept=%d stubs=%d shallow=%d want 2,1,0", kept, stubs, shallow)
	}

	// enrichShallow on: shallow page is left missing for regeneration.
	kept, stubs, shallow, err = seedDraftsFromPublished(dir, draft, true)
	if err != nil {
		t.Fatal(err)
	}
	if kept != 1 || stubs != 1 || shallow != 1 {
		t.Fatalf("enrich: kept=%d stubs=%d shallow=%d want 1,1,1", kept, stubs, shallow)
	}
	if _, err := os.Stat(filepath.Join(draft, "1.md")); err != nil {
		t.Fatal("deep page draft missing")
	}
	if _, err := os.Stat(filepath.Join(draft, "2.md")); err == nil {
		t.Fatal("shallow page must not be pre-seeded when enrich-shallow is set")
	}
	if _, err := os.Stat(filepath.Join(draft, "3.md")); err == nil {
		t.Fatal("stub must not be pre-seeded")
	}
}

func TestMissingPagesTreatsStubAsMissing(t *testing.T) {
	dir := t.TempDir()
	sub := "# 实质页\n\n## 目的\n\n" + strings.Repeat("说明业务能力与实现路径。", 40) + "\n\n## 细节\n\n更多内容。\n"
	stub := "# 空壳\n\n> **状态:** 待补充\n\n已写入占位稿。\n"
	if err := os.WriteFile(filepath.Join(dir, "1.md"), []byte(sub), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2.md"), []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "实质页", Slug: "1"},
		{Title: "空壳", Slug: "2"},
		{Title: "缺失", Slug: "3"},
	}}
	out := missingPages(dir, wiki)
	if len(out) != 2 {
		t.Fatalf("want 2 missing (stub+absent), got %d %#v", len(out), out)
	}
	slugs := map[string]bool{}
	for _, p := range out {
		slugs[p.Slug] = true
	}
	if !slugs["2"] || !slugs["3"] || slugs["1"] {
		t.Fatalf("unexpected missing set: %v", slugs)
	}
	if countDonePages(dir, wiki) != 1 {
		t.Fatalf("countDone want 1 got %d", countDonePages(dir, wiki))
	}
}

func TestIsSubstantialBodyRejectsThin(t *testing.T) {
	if isSubstantialBody("") {
		t.Fatal("empty should not be substantial")
	}
	if isSubstantialBody("# t\n\nshort") {
		t.Fatal("short body should not pass")
	}
	body := "# t\n\n## a\n\n" + strings.Repeat("内容", 200) + "\n\n## b\n\n更多。\n"
	if !isSubstantialBody(body) {
		t.Fatal("multi-heading long body should pass")
	}
	stub := "# t\n\n> **状态:** 待补充\n\n已写入占位稿。\n"
	if isSubstantialBody(stub) {
		t.Fatal("stub marker must fail substantial check")
	}
}

func coverageTopUpFixture() (*models.Wiki, *scan.Model) {
	m := &scan.Model{
		Name:     "demo",
		Language: "en",
		Files: []scan.FileInfo{
			{RelativePath: "order/OrderService.java", Lines: 100},
			{RelativePath: "order/OrderController.java", Lines: 90},
			{RelativePath: "order/OrderRepo.java", Lines: 80},
			{RelativePath: "order/OrderHelper.java", Lines: 70},
			{RelativePath: "billing/InvoiceService.java", Lines: 200},
			{RelativePath: "billing/InvoiceCalc.java", Lines: 150},
			{RelativePath: "billing/InvoiceRepo.java", Lines: 120},
			{RelativePath: "billing/InvoiceJob.java", Lines: 110},
		},
	}
	wiki := &models.Wiki{Pages: []models.WikiPage{{
		Title: "Order Flow", Slug: "1", Section: "Order", Track: models.TrackBusiness,
		ContentPath: "order/order-flow.md",
		DependentFiles: []string{
			"order/OrderService.java", "order/OrderController.java",
			"order/OrderRepo.java", "order/OrderHelper.java",
		},
	}}}
	return wiki, m
}

func TestApplyCoverageTopUpAddsGapPages(t *testing.T) {
	wiki, m := coverageTopUpFixture()
	applyCoverageTopUp(wiki, m, Config{MaxPages: 50})
	if len(wiki.Pages) != 2 {
		t.Fatalf("pages = %d, want 2 (billing gap page appended)", len(wiki.Pages))
	}
	p := wiki.Pages[1]
	if p.Title != "Billing" || p.Track != models.TrackTechnical {
		t.Fatalf("top-up page = %+v", p)
	}
	if len(p.DependentFiles) == 0 || p.DependentFiles[0] != "billing/InvoiceService.java" {
		t.Fatalf("top-up deps = %v, want pre-bound billing files (largest first)", p.DependentFiles)
	}
	// Existing page's evidence must be untouched (bindEvidence skips pre-bound).
	if len(wiki.Pages[0].DependentFiles) != 4 {
		t.Fatalf("existing deps were rewritten: %v", wiki.Pages[0].DependentFiles)
	}
}

func TestApplyCoverageTopUpRespectsMaxPages(t *testing.T) {
	wiki, m := coverageTopUpFixture()
	applyCoverageTopUp(wiki, m, Config{MaxPages: 1})
	if len(wiki.Pages) != 1 {
		t.Fatalf("pages = %d, want 1 (no budget headroom)", len(wiki.Pages))
	}
}
