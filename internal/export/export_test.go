package export

import (
	"fmt"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Symon12138/wikify/internal/models"
	"github.com/Symon12138/wikify/internal/scan"
)

func TestEnsurePageFormatHasCiteAndTOC(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Files: []scan.FileInfo{
			{RelativePath: "src/main.go", Lines: 20},
		},
	}
	// Substantial multi-heading body → cite + TOC.
	body := "## 目的\n\n说明目的与范围，保证页面足够充实以便通过实质内容判定。\n\n## 细节\n\n补充实现说明与依赖关系。\n"
	out := EnsurePageFormat("项目概述", body, m, []string{"src/main.go"}, true)
	if !strings.Contains(out, "<cite>") {
		t.Fatal("missing <cite>")
	}
	if !strings.Contains(out, "## 目录") {
		t.Fatal("missing TOC")
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "# 项目概述") {
		t.Fatal("missing title")
	}
	if !strings.Contains(out, "file://src/main.go") {
		t.Fatal("missing file cite")
	}
}

func TestEnsurePageFormatThinStubForEmpty(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Files: []scan.FileInfo{
			{RelativePath: "src/main.go", Lines: 20},
		},
	}
	out := EnsurePageFormat("空页", "", m, []string{"src/main.go"}, true)
	if !IsStubPage(out) {
		t.Fatalf("expected stub marker, got:\n%s", out)
	}
	if strings.Contains(out, "## 范围与前提") || strings.Contains(out, "## 系统结构") {
		t.Fatal("thin stub must not expand full handbook skeleton")
	}
	if !strings.Contains(out, "<cite>") {
		t.Fatal("stub should still cite sources when available")
	}
	if !strings.Contains(out, "待补充") {
		t.Fatal("expected 待补充 status")
	}
	outEN := EnsurePageFormat("Empty", "too short", m, []string{"src/main.go"}, false)
	if !IsStubPage(outEN) {
		t.Fatalf("EN stub missing marker:\n%s", outEN)
	}
}

func TestEnsurePageFormatInjectsTOCForSubstantial(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Files: []scan.FileInfo{
			{RelativePath: "src/main.go", Lines: 20},
		},
	}
	body := "## 目的与范围\n\n说明范围。\n\n## 架构概览\n\n说明架构。\n\nSources: [main.go](src/main.go#L1-L10)\n"
	out := EnsurePageFormat("分层架构设计", body, m, []string{"src/main.go"}, true)
	if !strings.Contains(out, "## 目录") {
		t.Fatalf("expected injected TOC, got:\n%s", out[:min(400, len(out))])
	}
	if !strings.Contains(out, "目的与范围") || !strings.Contains(out, "架构概览") {
		t.Fatal("TOC should list H2 headings")
	}
	if !strings.Contains(out, "<cite>") {
		t.Fatal("expected cite block for substantial page")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestNormalizeCitations(t *testing.T) {
	in := "Sources: [main.go](src/main.go#L10-L20) and [x](file://a.go#L1-L2)"
	out := normalizeCitations(in)
	if !strings.Contains(out, "[main.go](file://src/main.go#L10-L20)") {
		t.Fatalf("expected file:// rewrite, got: %s", out)
	}
	if !strings.Contains(out, "[x](file://a.go#L1-L2)") {
		t.Fatalf("expected keep file://, got: %s", out)
	}
	// http links untouched
	web := normalizeCitations("[doc](https://example.com/a#L1)")
	if web != "[doc](https://example.com/a#L1)" {
		t.Fatalf("http rewritten: %s", web)
	}
}

func TestExportLayout(t *testing.T) {
	dir := t.TempDir()
	m := &scan.Model{
		Name:        "demo",
		Language:    "zh",
		GeneratedAt: "2026-01-01T00:00:00Z",
		GitCommit:   "abc123",
		Summary:     "demo summary",
		Files: []scan.FileInfo{
			{RelativePath: "main.go", Lines: 10},
		},
	}
	wiki := &models.Wiki{
		Pages: []models.WikiPage{
			{
				Title: "项目概述", Slug: "1-overview", Section: "项目概述",
				ContentPath: "项目概述.md", DescriptionSlug: "project-overview",
				DependentFiles: []string{"main.go"}, Goal: "概述",
			},
			{
				Title: "整体架构设计", Slug: "2-arch", Section: "系统架构设计", Parent: "系统架构设计",
				ContentPath: "系统架构设计/整体架构设计.md", DescriptionSlug: "overall-architecture",
			},
			{
				Title: "系统架构设计", Slug: "3-arch-root", Section: "系统架构设计",
				ContentPath: "系统架构设计/系统架构设计.md", DescriptionSlug: "system-architecture",
			},
		},
	}
	contents := map[string]string{
		"1-overview":  "# 项目概述\n\nhello",
		"2-arch":      "# 整体架构设计\n\narch",
		"3-arch-root": "# 系统架构设计\n\nroot",
	}
	if err := Export(dir, m, wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(dir, ".wikify", "meta", "wiki-metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"knowledge_relations", "wiki_catalogs", "wiki_items", "wiki_overview", "wiki_readme", "wiki_repo"} {
		if _, ok := meta[k]; !ok {
			t.Errorf("missing key %s", k)
		}
	}
	contentPath := filepath.Join(dir, ".wikify", "content", "项目概述.md")
	if _, err := os.Stat(contentPath); err != nil {
		t.Fatal(err)
	}
	// Offline section knowledge cards
	if _, err := os.Stat(filepath.Join(dir, ".wikify", "knowledge", "zh", "_index.json")); err != nil {
		t.Fatal("missing knowledge index:", err)
	}
	// browse-index for browser viewing
	idxPath := filepath.Join(dir, ".wikify", "meta", "browse-index.json")
	if _, err := os.Stat(idxPath); err != nil {
		t.Fatal("missing browse-index.json:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".wikify", "meta", "wiki.json")); err != nil {
		t.Fatal("missing wiki.json:", err)
	}
	// PARENT_CHILD should exist for parent link
	rels, _ := meta["knowledge_relations"].([]any)
	if len(rels) == 0 {
		t.Log("relations:", rels)
	}
}

func TestEnsurePageFormatPreservesSubstantialLLM(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Files: []scan.FileInfo{
			{RelativePath: "src/main.go", Lines: 20},
		},
	}
	in := `# 项目概述

本页说明系统目标与边界。

## 架构要点

核心模块负责调度与持久化。

Sources: [main.go](src/main.go#L1-L10)
`
	out := EnsurePageFormat("项目概述", in, m, []string{"src/main.go"}, true)
	if !strings.Contains(out, "## 架构要点") {
		t.Fatalf("LLM heading lost: %s", out)
	}
	if strings.Contains(out, "## 范围与前提") {
		t.Fatal("should not inject handbook skeleton over substantial content")
	}
	if !strings.Contains(out, "file://src/main.go") {
		t.Fatal("missing normalized cite")
	}
	if !strings.Contains(out, "<cite>") {
		t.Fatal("expected pre-bound <cite> prepended")
	}
	// body prose kept
	if !strings.Contains(out, "核心模块负责调度与持久化") {
		t.Fatal("prose lost")
	}
}

func TestEnsurePageFormatStubUsesSkeleton(t *testing.T) {
	// Historical name kept; empty pages now use thin stub, not full handbook.
	m := &scan.Model{Name: "demo", Files: []scan.FileInfo{{RelativePath: "a.go", Lines: 5}}}
	out := EnsurePageFormat("空页", "短", m, []string{"a.go"}, true)
	if !IsStubPage(out) {
		t.Fatalf("expected thin stub marker: %s", out)
	}
	if !strings.Contains(out, "## 概述") {
		t.Fatalf("stub should have overview: %s", out)
	}
	if strings.Contains(out, "## 范围与前提") {
		t.Fatal("thin stub must not expand full handbook skeleton")
	}
	if !strings.Contains(out, "待补充") {
		t.Fatal("expected 待补充 status")
	}
}

func TestExportForcesTrackAndMetaExtend(t *testing.T) {
	dir := t.TempDir()
	m := &scan.Model{
		Name: "demo", Language: "zh", GeneratedAt: "2026-01-01T00:00:00Z",
		Files: []scan.FileInfo{{RelativePath: "svc/PayService.java", Lines: 40}},
	}
	// Deliberately omit Track — Export must materialise it.
	wiki := &models.Wiki{
		Pages: []models.WikiPage{
			{
				Title: "项目概述", Slug: "1-overview", Section: "项目概述",
				ContentPath: "项目概述.md", DescriptionSlug: "project-overview",
				DependentFiles: []string{"svc/PayService.java"},
			},
			{
				Title: "支付流程", Slug: "2-pay", Section: "核心业务能力",
				ContentPath: "核心业务能力/支付流程.md", DescriptionSlug: "payment-flow",
				DependentFiles: []string{"svc/PayService.java"},
			},
			{
				Title: "分层架构", Slug: "3-arch", Section: "系统架构设计",
				ContentPath: "系统架构设计/分层架构.md", DescriptionSlug: "layered-arch",
			},
		},
	}
	contents := map[string]string{
		"1-overview": "# 项目概述\n\nhello world for overview page with enough text to be kept.",
		"2-pay":      "# 支付流程\n\n## 目的\n\n说明支付。\n\n## 流程\n\n步骤说明。\n",
		"3-arch":     "# 分层架构\n\n## 总览\n\n架构说明。\n\n## 组件\n\n组件列表。\n",
	}
	if err := Export(dir, m, wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	// wiki.json must contain non-empty track for every page
	raw, err := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved models.Wiki
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	for _, p := range saved.Pages {
		if p.Track != models.TrackFoundation && p.Track != models.TrackBusiness && p.Track != models.TrackTechnical {
			t.Errorf("page %q missing valid track, got %q", p.Title, p.Track)
		}
	}
	// in-memory wiki also mutated
	for _, p := range wiki.Pages {
		if p.Track == "" {
			t.Errorf("in-memory page %q track still empty", p.Title)
		}
	}
	// browse-index carries track
	idxRaw, err := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "browse-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var idx BrowseIndex
	if err := json.Unmarshal(idxRaw, &idx); err != nil {
		t.Fatal(err)
	}
	if len(idx.Pages) < 3 {
		t.Fatalf("browse pages=%d", len(idx.Pages))
	}
	for _, p := range idx.Pages {
		if p.Track == "" {
			t.Errorf("browse page %q missing track", p.Title)
		}
	}
	// metadata extend has track + dependent_files; extend_info.tracks present
	metaRaw, err := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki-metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatal(err)
	}
	items, _ := meta["wiki_items"].([]any)
	if len(items) == 0 {
		t.Fatal("no wiki_items")
	}
	foundTrackInExtend := false
	for _, it := range items {
		m, _ := it.(map[string]any)
		extStr, _ := m["extend"].(string)
		if strings.Contains(extStr, `"track"`) {
			foundTrackInExtend = true
		}
		if title, _ := m["title"].(string); title == "支付流程" {
			if rc, _ := m["reference_count"].(float64); rc < 1 {
				t.Errorf("payment page reference_count=%v", rc)
			}
			if !strings.Contains(extStr, "PayService") {
				t.Errorf("extend missing dependent_files: %s", extStr)
			}
		}
	}
	if !foundTrackInExtend {
		t.Fatal("wiki_items.extend missing track")
	}
	repo, _ := meta["wiki_repo"].(map[string]any)
	extInfo, _ := repo["extend_info"].(map[string]any)
	tracks, _ := extInfo["tracks"].(map[string]any)
	if len(tracks) == 0 {
		t.Fatalf("extend_info.tracks empty: %#v", extInfo)
	}
}

func TestEnsurePageFormatInjectsTOCAfterCite(t *testing.T) {
	// ADS-like: substantial body already has <cite> but no ## 目录 H2.
	m := &scan.Model{
		Name:  "demo",
		Files: []scan.FileInfo{{RelativePath: "a/B.java", Lines: 30}},
	}
	body := `<cite>
**参考文献**
- [B.java](file://a/B.java#L1-L30)
</cite>

## 目的与范围

本页描述范围。

## 核心流程

流程说明足够长以通过 substantial 判定。

## 依赖关系

依赖若干模块。
`
	out := EnsurePageFormat("业务能力页", body, m, []string{"a/B.java"}, true)
	if !hasTOCHeading(out) {
		t.Fatalf("expected ## 目录 after cite inject, got head:\n%s", out[:min(500, len(out))])
	}
	// TOC should sit after </cite>
	citeEnd := strings.Index(out, "</cite>")
	tocAt := strings.Index(out, "## 目录")
	if citeEnd < 0 || tocAt < citeEnd {
		t.Fatalf("TOC should follow cite; citeEnd=%d tocAt=%d", citeEnd, tocAt)
	}
	if !strings.Contains(out, "目的与范围") || !strings.Contains(out, "核心流程") {
		t.Fatal("TOC entries missing")
	}
}

func TestEnsureRelatedNavAndMermaidFill(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Modules: []scan.ModuleSummary{
			{Name: "api", Path: "api"},
			{Name: "app", Path: "app"},
			{Name: "web", Path: "web"},
		},
		Files: []scan.FileInfo{{RelativePath: "a.go", Lines: 10}},
	}
	// Substantial page with cite+TOC but no mermaid.
	body := `<cite>
**参考文献**
- [a.go](file://a.go#L1-L10)
</cite>

## 目录
1. [目的](#目的)
2. [流程](#流程)

## 目的

说明足够长以通过 substantial 判定的正文内容。

## 流程

步骤说明若干。
`
	out := EnsurePageFormat("支付能力", body, m, []string{"a.go"}, true)
	if len(extractMermaid(out)) < 1 {
		t.Fatalf("expected mermaid fill, got:\n%s", out[:min(600, len(out))])
	}
	if !strings.Contains(out, "## 结构示意") {
		t.Fatal("missing 结构示意 heading")
	}

	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "支付能力", Slug: "1", Section: "核心业务能力", Track: models.TrackBusiness, ContentPath: "核心业务能力/支付能力.md"},
		{Title: "退款能力", Slug: "2", Section: "核心业务能力", Track: models.TrackBusiness, ContentPath: "核心业务能力/退款能力.md"},
		{Title: "支付接口", Slug: "3", Section: "接口文档", Track: models.TrackTechnical, ContentPath: "接口文档/支付接口.md"},
	}}
	nav := EnsureRelatedNav(out, wiki, wiki.Pages[0], true)
	if !strings.Contains(nav, "## 相关页面") {
		t.Fatal("missing related section")
	}
	if !strings.Contains(nav, "退款能力") {
		t.Fatal("missing sibling link")
	}
	// Idempotent
	nav2 := EnsureRelatedNav(nav, wiki, wiki.Pages[0], true)
	if strings.Count(nav2, "## 相关页面") != 1 {
		t.Fatalf("related section not idempotent: count=%d", strings.Count(nav2, "## 相关页面"))
	}
}

func TestExportWritesKnowledgeCards(t *testing.T) {
	dir := t.TempDir()
	m := &scan.Model{Name: "demo", Language: "zh", Files: []scan.FileInfo{{RelativePath: "a.go", Lines: 5}}}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Slug: "1", Section: "项目概述", ContentPath: "项目概述.md", Track: models.TrackFoundation},
		{Title: "支付", Slug: "2", Section: "核心业务能力", ContentPath: "核心业务能力/支付.md", Track: models.TrackBusiness},
		{Title: "退款", Slug: "3", Section: "核心业务能力", ContentPath: "核心业务能力/退款.md", Track: models.TrackBusiness},
	}}
	contents := map[string]string{
		"1": "# 项目概述\n\n## A\n\nx\n\n## B\n\ny\n",
		"2": "# 支付\n\n## A\n\nx\n\n## B\n\ny\n",
		"3": "# 退款\n\n## A\n\nx\n\n## B\n\ny\n",
	}
	if err := Export(dir, m, wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(dir, ".wikify", "knowledge", "zh", "_index.json")
	raw, err := os.ReadFile(idx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "核心业务能力") {
		t.Fatalf("index missing section: %s", raw[:min(200, len(raw))])
	}
	ov := filepath.Join(dir, ".wikify", "knowledge", "zh", "核心业务能力", "overview.md")
	b, err := os.ReadFile(ov)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "支付") || !strings.Contains(string(b), "退款") {
		t.Fatalf("overview missing pages: %s", b)
	}
	// content pages should have related nav
	pay, err := os.ReadFile(filepath.Join(dir, ".wikify", "content", "核心业务能力", "支付.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pay), "## 相关页面") {
		t.Fatal("exported page missing related nav")
	}
}

func TestBuildMetadataParentChildWithVirtualSection(t *testing.T) {
	// Section/parent names that are NOT real page titles must still produce PARENT_CHILD.
	m := &scan.Model{Name: "demo", Language: "zh", GeneratedAt: "2026-01-01T00:00:00Z"}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Slug: "1", Section: "项目概述", ContentPath: "项目概述.md", DescriptionSlug: "project-overview"},
		{Title: "客户基本信息管理", Slug: "2", Section: "客户管理", Parent: "客户管理", ContentPath: "客户管理/客户基本信息管理.md"},
		{Title: "客户合同管理", Slug: "3", Section: "客户管理", Parent: "客户管理", ContentPath: "客户管理/客户合同管理.md"},
		{Title: "分层架构", Slug: "4", Section: "系统架构设计", Parent: "系统架构设计", ContentPath: "系统架构设计/分层架构.md"},
	}}
	contents := map[string]string{
		"1": "# 项目概述\n\n## A\n\nx\n\n## B\n\ny\n",
		"2": "# 客户基本信息管理\n\n## A\n\nx\n\n## B\n\ny\n",
		"3": "# 客户合同管理\n\n## A\n\nx\n\n## B\n\ny\n",
		"4": "# 分层架构\n\n## A\n\nx\n\n## B\n\ny\n",
	}
	meta := buildMetadata(m, wiki, contents, "zh")
	raw, err := json.Marshal(meta["knowledge_relations"])
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) < 3 {
		t.Fatalf("expected PARENT_CHILD relations for section containers, got %d: %s", len(list), raw)
	}
	pc := 0
	for _, r := range list {
		rt, _ := r["relationship_type"].(string)
		if rt != "PARENT_CHILD" && rt != "RELATED_TO" {
			t.Fatalf("bad type: %#v", r)
		}
		if r["source_type"] != "WIKI_ITEM" || r["target_type"] != "WIKI_ITEM" {
			t.Fatalf("bad endpoints: %#v", r)
		}
		if rt == "PARENT_CHILD" {
			pc++
		}
	}
	if pc < 2 {
		t.Fatalf("expected PARENT_CHILD relations, got %d of %d: %s", pc, len(list), raw)
	}
	// Virtual container should appear in wiki_items
	itemsRaw, _ := json.Marshal(meta["wiki_items"])
	itemsStr := string(itemsRaw)
	if !strings.Contains(itemsStr, "客户管理") {
		t.Fatalf("expected virtual container 客户管理 in wiki_items: %s", itemsStr[:min(400, len(itemsStr))])
	}
	// Readable description slugs for CJK (not empty / not bare topic- only without role)
	var items []map[string]any
	_ = json.Unmarshal(itemsRaw, &items)
	for _, it := range items {
		desc, _ := it["description"].(string)
		if desc == "" {
			t.Fatalf("empty description on %#v", it["title"])
		}
		if strings.HasPrefix(desc, "topic-") {
			t.Fatalf("legacy opaque topic- slug still used for %q: %s", it["title"], desc)
		}
	}
}

func TestExportRebindsDependentFiles(t *testing.T) {
	dir := t.TempDir()
	// Seed a tiny fake source tree so scan can inventory files.
	src := filepath.Join(dir, "app", "src", "main", "java", "com", "demo")
	if err := os.MkdirAll(filepath.Join(src, "service"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "controller"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "app", "pom.xml"), []byte("<project/>"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "service", "OrderService.java"), []byte("class OrderService {}"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "service", "OrderServiceImpl.java"), []byte("class OrderServiceImpl {}"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "controller", "OrderControl.java"), []byte("class OrderControl {}"), 0o644)

	m := &scan.Model{
		Name: "demo", Language: "zh", GeneratedAt: "2026-01-01T00:00:00Z",
		Files: []scan.FileInfo{
			{RelativePath: "app/pom.xml", Lines: 10},
			{RelativePath: "app/src/main/java/com/demo/service/OrderService.java", Lines: 40},
			{RelativePath: "app/src/main/java/com/demo/service/OrderServiceImpl.java", Lines: 60},
			{RelativePath: "app/src/main/java/com/demo/controller/OrderControl.java", Lines: 50},
		},
	}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{
			Title: "订单受理管理", Slug: "1-order", Section: "订单模块",
			ContentPath: "订单模块/订单受理管理.md", DescriptionSlug: "order",
			// Deliberately bad binding from an older generate.
			DependentFiles: []string{"app/pom.xml"},
		},
	}}
	contents := map[string]string{
		"1-order": "# 订单受理管理\n\n## 目的\n\n说明订单。\n\n## 流程\n\n步骤。\n",
	}
	if err := Export(dir, m, wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	// wiki.json should prefer service over pom
	raw, err := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved models.Wiki
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	var order *models.WikiPage
	for i := range saved.Pages {
		if saved.Pages[i].Title == "订单受理管理" {
			order = &saved.Pages[i]
			break
		}
	}
	if order == nil {
		t.Fatalf("order page missing, pages=%d", len(saved.Pages))
	}
	deps := strings.Join(order.DependentFiles, "\n")
	if strings.Contains(deps, "pom.xml") {
		t.Fatalf("pom still bound after rebind: %v", order.DependentFiles)
	}
	if !strings.Contains(deps, "OrderService") {
		t.Fatalf("expected OrderService evidence, got %v", order.DependentFiles)
	}
}

func TestPolishReExportsTracksAndTOC(t *testing.T) {
	dir := t.TempDir()
	// Seed a minimal .wikify as if an older binary wrote it (no tracks, no TOC).
	contentDir := filepath.Join(dir, ".wikify", "content")
	metaDir := filepath.Join(dir, ".wikify", "meta")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wiki := models.Wiki{
		Pages: []models.WikiPage{
			{
				Title: "项目概述", Slug: "1-overview", Section: "项目概述",
				ContentPath: "项目概述.md", DescriptionSlug: "project-overview",
			},
			{
				Title: "订单受理", Slug: "2-order", Section: "核心业务能力",
				ContentPath: "核心业务能力/订单受理.md", DescriptionSlug: "order-accept",
				DependentFiles: []string{"OrderService.java"},
			},
		},
	}
	// Write wiki.json WITHOUT track fields
	wb, _ := json.MarshalIndent(wiki, "", "  ")
	if err := os.WriteFile(filepath.Join(metaDir, "wiki.json"), wb, 0o644); err != nil {
		t.Fatal(err)
	}
	page1 := "# 项目概述\n\n这是一段足够长的概述文字，用于判定 substantial 页面并触发格式化。\n\n## 背景\n\n背景说明。\n\n## 目标\n\n目标说明。\n"
	page2 := "# 订单受理\n\n## 流程入口\n\n入口说明。\n\n## 校验规则\n\n规则说明。\n\n## 落库\n\n落库说明。\n"
	if err := os.WriteFile(filepath.Join(contentDir, "项目概述.md"), []byte(page1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(contentDir, "核心业务能力"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "核心业务能力", "订单受理.md"), []byte(page2), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, ".wikify", "lang"), []byte("zh\n"), 0o644)

	if err := Polish(dir, ExportOptions{}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(metaDir, "wiki.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved models.Wiki
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	for _, p := range saved.Pages {
		if p.Track == "" {
			t.Errorf("polish left track empty on %q", p.Title)
		}
	}
	// content should gain ## 目录
	body, err := os.ReadFile(filepath.Join(contentDir, "核心业务能力", "订单受理.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasTOCHeading(string(body)) {
		t.Fatalf("polish should inject TOC, got:\n%s", string(body)[:min(400, len(body))])
	}
	if _, err := os.Stat(filepath.Join(metaDir, "browse-index.json")); err != nil {
		t.Fatal("polish missing browse-index")
	}
	if _, err := os.Stat(filepath.Join(metaDir, "wiki-metadata.json")); err != nil {
		t.Fatal("polish missing wiki-metadata")
	}
}


func TestMermaidFillToThree(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Modules: []scan.ModuleSummary{
			{Name: "api", Path: "api"},
			{Name: "app", Path: "app"},
		},
		Files: []scan.FileInfo{{RelativePath: "a.go", Lines: 10}},
	}
	// Substantial page with cite+TOC but no mermaid → fill 1–2 diagrams (not 5).
	body := `<cite>
**参考文献**
- [a.go](file://a.go#L1-L10)
</cite>

## 目录
1. [目的](#目的)
2. [流程](#流程)

## 目的

说明足够长以通过 substantial 判定的正文内容，保证页面充实。

## 流程

步骤说明若干，用于验证 mermaid 补齐逻辑。
`
	out := EnsurePageFormat("支付能力", body, m, []string{"a.go"}, true)
	n := len(extractMermaid(out))
	if n < 1 || n > 2 {
		t.Fatalf("expected 1–2 mermaid diagrams for empty page, got %d:\n%s", n, out[:min(800, len(out))])
	}
	// Page with 2 existing mermaid must NOT be topped up to 5.
	two := body + "\n```mermaid\nflowchart TB\n  A-->B\n```\n\n```mermaid\nflowchart LR\n  C-->D\n```\n"
	out2 := EnsurePageFormat("支付能力", two, m, []string{"a.go"}, true)
	n2 := len(extractMermaid(out2))
	if n2 < 2 {
		t.Fatalf("expected keep >=2 existing diagrams, got %d", n2)
	}
	if n2 > 4 {
		t.Fatalf("must not pad to 5+ diagrams, got %d", n2)
	}
	// Stub pages get zero mermaid filler.
	stubBody := "> **状态:** 待补充\n\n本页在生成阶段内容不足或失败，已写入占位稿。"
	out3 := EnsurePageFormat("安全与访问控制", stubBody, m, []string{"a.go"}, true)
	if !IsStubPage(out3) {
		t.Fatalf("expected stub marker, got:\n%s", out3[:min(400, len(out3))])
	}
	if n3 := len(extractMermaid(out3)); n3 != 0 {
		t.Fatalf("stub must have 0 mermaid, got %d", n3)
	}
}

func TestBuildMetadataRelatedTo(t *testing.T) {
	m := &scan.Model{Name: "demo", Language: "zh", GeneratedAt: "2026-01-01T00:00:00Z"}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Slug: "1", Section: "项目概述", ContentPath: "项目概述.md", Track: models.TrackFoundation},
		{Title: "订单受理", Slug: "2", Section: "订单模块", ContentPath: "订单模块/订单受理.md", Track: models.TrackBusiness},
		{Title: "订单查询", Slug: "3", Section: "订单模块", ContentPath: "订单模块/订单查询.md", Track: models.TrackBusiness},
		{Title: "订单接口", Slug: "4", Section: "接口文档", ContentPath: "接口文档/订单接口.md", Track: models.TrackTechnical},
	}}
	contents := map[string]string{
		"1": "# 项目概述\n\n## A\n\nx\n\n## B\n\ny\n",
		"2": "# 订单受理\n\n## A\n\nx\n\n## B\n\ny\n",
		"3": "# 订单查询\n\n## A\n\nx\n\n## B\n\ny\n",
		"4": "# 订单接口\n\n## A\n\nx\n\n## B\n\ny\n",
	}
	meta := buildMetadata(m, wiki, contents, "zh")
	raw, _ := json.Marshal(meta["knowledge_relations"])
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	var parentChild, related int
	for _, r := range list {
		switch r["relationship_type"] {
		case "PARENT_CHILD":
			parentChild++
		case "RELATED_TO":
			related++
		}
	}
	if parentChild < 1 {
		t.Fatalf("expected PARENT_CHILD, got list=%s", raw)
	}
	if related < 1 {
		t.Fatalf("expected RELATED_TO (same-section or cross-rail), got parent=%d related=%d list=%s", parentChild, related, raw)
	}
}

func TestExportWritesPerPageKnowledgeCards(t *testing.T) {
	dir := t.TempDir()
	m := &scan.Model{Name: "demo", Language: "zh", Files: []scan.FileInfo{{RelativePath: "a.go", Lines: 5}}}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Slug: "1", Section: "项目概述", ContentPath: "项目概述.md", Track: models.TrackFoundation, Goal: "概述目标"},
		{Title: "支付", Slug: "2", Section: "核心业务能力", ContentPath: "核心业务能力/支付.md", Track: models.TrackBusiness, Goal: "支付能力", DependentFiles: []string{"a.go"}},
	}}
	contents := map[string]string{
		"1": "# 项目概述\n\n## A\n\n项目说明正文。\n\n## B\n\n更多细节。\n",
		"2": "# 支付\n\n## A\n\n支付流程说明。\n\n## B\n\n规则细节。\n",
	}
	if err := Export(dir, m, wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	// Per-page card should exist
	card := filepath.Join(dir, ".wikify", "knowledge", "zh", "核心业务能力", "支付.md")
	b, err := os.ReadFile(card)
	if err != nil {
		t.Fatalf("missing per-page card: %v", err)
	}
	if !strings.Contains(string(b), "支付") {
		t.Fatalf("card missing title: %s", b)
	}
	if !strings.Contains(string(b), "关键源文件") && !strings.Contains(string(b), "a.go") {
		t.Fatalf("card missing deps: %s", b)
	}
	idxRaw, err := os.ReadFile(filepath.Join(dir, ".wikify", "knowledge", "zh", "_index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idxRaw), "card_count") {
		t.Fatalf("index missing card_count: %s", idxRaw)
	}
}

func TestExportDoesNotMergeEngineeringSeeds(t *testing.T) {
	// Regression: Export must not invent eng pages after generation.
	// Seeds belong in catalog (runner.buildCatalog → MergeEngineeringSeeds).
	dir := t.TempDir()
	m := &scan.Model{
		Name: "demo", Language: "zh", GeneratedAt: "2026-01-01T00:00:00Z",
		Modules: []scan.ModuleSummary{{Name: "app", Path: "app"}, {Name: "api", Path: "api"}},
		Files: []scan.FileInfo{
			{RelativePath: "app/src/main/java/com/demo/controller/OrderControl.java", Lines: 40},
			{RelativePath: "app/src/main/java/com/demo/security/AuthFilter.java", Lines: 30},
			{RelativePath: "app/src/test/java/T.java", Lines: 10},
			{RelativePath: "Dockerfile", Lines: 5},
			{RelativePath: "app/pom.xml", Lines: 10},
			{RelativePath: "api/pom.xml", Lines: 10},
			{RelativePath: "app/src/main/resources/application.yml", Lines: 10},
		},
	}
	for i := 0; i < 40; i++ {
		m.Files = append(m.Files, scan.FileInfo{RelativePath: "app/misc/F" + string(rune('a'+i%26)) + ".java", Lines: 5})
	}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Slug: "1", Section: "项目概述", ContentPath: "项目概述.md", Track: models.TrackFoundation},
		{Title: "订单受理", Slug: "2", Section: "订单模块", ContentPath: "订单模块/订单受理.md", Track: models.TrackBusiness},
	}}
	contents := map[string]string{
		"1": "# 项目概述\n\n## A\n\nx\n\n## B\n\ny\n",
		"2": "# 订单受理\n\n## A\n\nx\n\n## B\n\ny\n",
	}
	if err := Export(dir, m, wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
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
	// NAV-2/NAV-3 synthesized nav pages (arch overview / section index) are
	// allowed; anything else beyond the 2 input pages is an invented seed.
	var organic []models.WikiPage
	for _, p := range saved.Pages {
		if !isGeneratedNavPage(p) {
			organic = append(organic, p)
		}
	}
	if len(organic) != 2 {
		titles := make([]string, 0, len(organic))
		for _, p := range organic {
			titles = append(titles, p.Title)
		}
		t.Fatalf("export must not invent eng pages, got %d titles=%v", len(organic), titles)
	}
	for _, p := range saved.Pages {
		for _, bad := range []string{"API接口文档", "安全与访问控制", "部署与运维", "测试与质量", "常见问题"} {
			if p.Title == bad {
				t.Fatalf("export invented engineering page %q", p.Title)
			}
		}
	}
}
func TestRewriteNumericSlugLinks(t *testing.T) {
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "客户基本信息", Slug: "50-50", ContentPath: "客户管理模块/客户基本信息.md", Section: "客户管理模块"},
		{Title: "安全与访问控制", Slug: "144-144", ContentPath: "安全与访问控制/安全与访问控制.md"},
	}}
	body := "见 [客户基本信息](50-50) 与 [安全](144-144)。未知 [x](99-99) 保留。"
	got, _ := rewriteWikiLinks(body, wiki)
	if strings.Contains(got, "](50-50)") || strings.Contains(got, "](144-144)") {
		t.Fatalf("numeric slugs not rewritten: %s", got)
	}
	if !strings.Contains(got, "](客户管理模块/客户基本信息.md)") {
		t.Fatalf("expected ContentPath rewrite: %s", got)
	}
	if !strings.Contains(got, "](安全与访问控制/安全与访问控制.md)") {
		t.Fatalf("expected second rewrite: %s", got)
	}
	if !strings.Contains(got, "](99-99)") {
		t.Fatalf("unknown slug must be preserved: %s", got)
	}
}
func TestStubInventoryRoleMatched(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Files: []scan.FileInfo{
			{RelativePath: "web/UserController.java", Lines: 40},
			{RelativePath: "security/AuthFilter.java", Lines: 30},
			{RelativePath: "config/application.yml", Lines: 20},
			{RelativePath: "deploy/Dockerfile", Lines: 15},
			{RelativePath: "svc/misc/Unrelated.java", Lines: 10},
			{RelativePath: "pom.xml", Lines: 5},
		},
	}
	// Empty body → thin stub with inventory.
	out := EnsurePageFormat("安全与访问控制", "", m, nil, true)
	if !IsStubPage(out) {
		t.Fatalf("expected stub:\n%s", out[:min(400, len(out))])
	}
	if !strings.Contains(out, "仓库信号清单") {
		t.Fatalf("expected inventory section:\n%s", out[:min(800, len(out))])
	}
	if !strings.Contains(out, "security/AuthFilter.java") {
		t.Fatalf("expected security path in inventory/cites:\n%s", out[:min(1200, len(out))])
	}
	// API title should prefer controller.
	outAPI := EnsurePageFormat("API接口文档", "", m, nil, true)
	if !strings.Contains(outAPI, "UserController.java") {
		t.Fatalf("expected API controller path:\n%s", outAPI[:min(1200, len(outAPI))])
	}
	// Deploy title
	outDep := EnsurePageFormat("部署与运维", "", m, nil, true)
	if !strings.Contains(outDep, "Dockerfile") {
		t.Fatalf("expected Dockerfile for deploy stub:\n%s", outDep[:min(1200, len(outDep))])
	}
	// No mermaid on stubs
	if strings.Contains(out, "```mermaid") {
		t.Fatal("stub must not inject mermaid")
	}
}

func TestInventoryPathsForTitleNoProductLeak(t *testing.T) {
	m := &scan.Model{Files: []scan.FileInfo{
		{RelativePath: "security/LoginService.java", Lines: 10},
		{RelativePath: "web/OrderController.java", Lines: 10},
	}}
	paths := inventoryPathsForTitle(m, "安全与访问控制", 8)
	if len(paths) == 0 {
		t.Fatal("expected security paths")
	}
	// Must not invent product paths that are not in scan model.
	for _, p := range paths {
		if strings.Contains(p, "现金池") || strings.Contains(strings.ToLower(p), "cashpool") {
			t.Fatalf("product path leak: %s", p)
		}
	}
}

func TestExportDoesNotInventEngineeringPages(t *testing.T) {
	dir := t.TempDir()
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Slug: "1", Section: "项目概述", ContentPath: "项目概述.md", Track: models.TrackFoundation},
	}}
	body := "# 项目概述\n\n## 目的\n\n" + strings.Repeat("仓库定位与读者路径说明。", 30) + "\n\n## 范围\n\n更多。\n"
	contents := map[string]string{"1": body}
	m := &scan.Model{
		Name: "demo", Language: "zh",
		Files: []scan.FileInfo{
			{RelativePath: "src/main/java/com/demo/controller/OrderControl.java", Lines: 40},
			{RelativePath: "src/main/java/com/demo/security/AuthFilter.java", Lines: 20},
			{RelativePath: "deploy/Dockerfile", Lines: 10},
			{RelativePath: "src/test/java/T.java", Lines: 10},
		},
	}
	if err := Export(dir, m, wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got models.Wiki
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Pages) != 1 {
		titles := make([]string, 0, len(got.Pages))
		for _, p := range got.Pages {
			titles = append(titles, p.Title)
		}
		t.Fatalf("export invented pages: %d titles=%v", len(got.Pages), titles)
	}
}

func TestWikiItemsCarryDependentFiles(t *testing.T) {
	dir := t.TempDir()
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{
			Title: "订单受理", Slug: "1", Section: "订单", ContentPath: "订单/订单受理.md",
			Track: models.TrackBusiness,
			DependentFiles: []string{
				"src/main/java/com/demo/controller/OrderControl.java",
				"src/main/java/com/demo/service/OrderService.java",
			},
		},
	}}
	body := "# 订单受理\n\n## 目的\n\n" + strings.Repeat("订单受理流程说明。", 40) + "\n\n## 细节\n\n更多。\n"
	m := &scan.Model{
		Name: "demo", Language: "zh",
		Files: []scan.FileInfo{
			{RelativePath: "src/main/java/com/demo/controller/OrderControl.java", Lines: 40},
			{RelativePath: "src/main/java/com/demo/service/OrderService.java", Lines: 30},
		},
	}
	if err := Export(dir, m, wiki, map[string]string{"1": body}, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki-metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	items, _ := meta["wiki_items"].([]any)
	if len(items) == 0 {
		t.Fatal("no wiki_items")
	}
	found := false
	for _, rawIt := range items {
		it, _ := rawIt.(map[string]any)
		if it["title"] != "订单受理" {
			continue
		}
		found = true
		top := it["dependent_files"]
		if top == nil || top == "" || top == "[]" {
			t.Fatalf("top-level dependent_files empty: %#v", top)
		}
		var paths []string
		switch v := top.(type) {
		case string:
			if err := json.Unmarshal([]byte(v), &paths); err != nil {
				t.Fatal(err)
			}
		case []any:
			for _, x := range v {
				paths = append(paths, fmt.Sprint(x))
			}
		}
		if len(paths) == 0 {
			t.Fatalf("parsed deps empty from %#v", top)
		}
		extStr, _ := it["extend"].(string)
		var ext map[string]any
		_ = json.Unmarshal([]byte(extStr), &ext)
		extDeps, _ := ext["dependent_files"].([]any)
		if len(extDeps) == 0 {
			t.Fatalf("extend.dependent_files empty: %s", extStr)
		}
	}
	if !found {
		t.Fatal("订单受理 item missing")
	}
}

func TestDefaultMermaidUsesModuleDeps(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Modules: []scan.ModuleSummary{
			{Name: "api", Path: "api"},
			{Name: "app", Path: "app"},
		},
		ModuleDeps: []scan.ModuleDep{
			{From: "api", To: "app", Weight: 3},
		},
	}
	out := defaultMermaid(m, "架构", 2, 0)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "api") || !strings.Contains(joined, "app") {
		t.Fatalf("expected real module deps in mermaid, got:\n%s", joined)
	}
}


func TestDefaultMermaidFocusImportEdges(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		ImportEdges: []scan.ImportEdge{
			{From: "web/OrderController.java", To: "svc/OrderService.java", Kind: "import"},
			{From: "svc/OrderService.java", To: "repo/OrderRepo.java", Kind: "import"},
			{From: "web/OrderController.java", To: "web/Base.java", Kind: "same_package"},
		},
	}
	out := defaultMermaid(m, "订单接口", 1, 0, "web/OrderController.java", "svc/OrderService.java")
	if len(out) == 0 {
		t.Fatal("no diagrams")
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "OrderController") || !strings.Contains(joined, "OrderService") {
		t.Fatalf("expected file-level deps, got:\n%s", joined)
	}
	// same_package should not dominate (Base may or may not appear)
	if !strings.Contains(joined, "-->") {
		t.Fatalf("expected edges in diagram:\n%s", joined)
	}
}

func TestForceInjectCodeDependencyMermaid(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		ImportEdges: []scan.ImportEdge{
			{From: "web/OrderController.java", To: "svc/OrderService.java", Kind: "import"},
			{From: "svc/OrderService.java", To: "dao/OrderDao.java", Kind: "import"},
		},
		Files: []scan.FileInfo{
			{RelativePath: "web/OrderController.java", Lines: 40},
			{RelativePath: "svc/OrderService.java", Lines: 50},
			{RelativePath: "dao/OrderDao.java", Lines: 30},
		},
	}
	body := "<cite>\n**参考文献**\n- [OrderController.java](file://web/OrderController.java#L1-L40)\n</cite>\n\n## 目录\n1. [目的](#目的)\n2. [流程](#流程)\n\n## 目的\n\n说明足够长以通过 substantial 判定的正文内容，保证页面充实，并验证代码依赖图强制注入。\n\n## 流程\n\n步骤说明若干，用于验证 mermaid 在已有两图时仍会注入代码依赖示意。\n\n```mermaid\nflowchart TB\n  A-->B\n```\n\n```mermaid\nflowchart LR\n  C-->D\n```\n\n"
	refs := []string{"web/OrderController.java", "svc/OrderService.java", "dao/OrderDao.java"}
	out := EnsurePageFormat("订单服务", body, m, refs, true)
	if !strings.Contains(out, "代码依赖示意") {
		t.Fatalf("expected 代码依赖示意 section, got:\n%s", out[:min(1200, len(out))])
	}
	if !strings.Contains(out, "OrderController") || !strings.Contains(out, "OrderService") {
		t.Fatalf("expected file-level nodes in force-injected diagram:\n%s", out)
	}
	if !strings.Contains(out, "classDef focus") {
		t.Fatalf("expected focus classDef fingerprint:\n%s", out)
	}
	n := len(extractMermaid(out))
	if n < 3 {
		t.Fatalf("expected >=3 mermaid after force inject, got %d", n)
	}
	out2 := EnsurePageFormat("订单服务", out, m, refs, true)
	if c := strings.Count(out2, "## 代码依赖示意"); c != 1 {
		t.Fatalf("expected single 代码依赖示意 after re-polish, got %d", c)
	}
}

func TestRoleChainMermaidFallback(t *testing.T) {
	g := roleChainMermaid([]string{
		"web/PayController.java",
		"svc/PayService.java",
		"dao/PayDao.java",
		"entity/PayPO.java",
		"static/help.html",
	})
	if g == "" {
		t.Fatal("expected role chain diagram")
	}
	if !strings.Contains(g, "PayController") || !strings.Contains(g, "PayService") {
		t.Fatalf("missing role nodes: %s", g)
	}
	if !strings.Contains(g, "-->") {
		t.Fatalf("expected chain edges: %s", g)
	}
	if roleChainMermaid([]string{"svc/OnlyService.java"}) != "" {
		t.Fatal("single role must not draw chain")
	}
	m := &scan.Model{
		ImportEdges: []scan.ImportEdge{
			{From: "web/PayController.java", To: "svc/PayService.java", Kind: "import"},
		},
	}
	g2 := codeDependencyDiagram(m, "支付", []string{"web/PayController.java", "svc/PayService.java", "dao/PayDao.java"})
	if !strings.Contains(g2, "classDef focus") {
		t.Fatalf("expected import-edge focus graph, got:\n%s", g2)
	}
	g3 := codeDependencyDiagram(&scan.Model{}, "支付", []string{"web/PayController.java", "svc/PayService.java"})
	if g3 == "" || strings.Contains(g3, "classDef focus") {
		t.Fatalf("expected role-chain fallback, got:\n%s", g3)
	}
}

func TestEnsurePageFormatInjectsRoleChainWithoutGraph(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Files: []scan.FileInfo{
			{RelativePath: "web/FooController.java", Lines: 20},
			{RelativePath: "svc/FooService.java", Lines: 30},
		},
	}
	body := "<cite>\n**参考文献**\n- [FooController.java](file://web/FooController.java#L1-L20)\n</cite>\n\n## 目录\n1. [目的](#目的)\n\n## 目的\n\n说明足够长以通过 substantial 判定的正文内容，验证无 import 边时角色链兜底。\n\n## 细节\n\n继续补充正文使页面充实。\n\n```mermaid\nflowchart TB\n  A-->B\n```\n\n```mermaid\nflowchart LR\n  C-->D\n```\n\n"
	out := EnsurePageFormat("Foo 能力", body, m, []string{"web/FooController.java", "svc/FooService.java"}, true)
	if !strings.Contains(out, "代码依赖示意") {
		t.Fatalf("expected role-chain section:\n%s", out[:min(1000, len(out))])
	}
	if !strings.Contains(out, "FooController") || !strings.Contains(out, "FooService") {
		t.Fatalf("expected role nodes:\n%s", out)
	}
}


func TestExtractCitePathsAndMergeFocus(t *testing.T) {
	body := "<cite>\n- [A](file://web/OrderController.java#L1-L40)\n- [B](file://svc/OrderService.java#L1-L20)\n</cite>\nSee also file://dao/OrderDao.java#L5-L10 and [x](file://readme.html).\n"
	cites := extractCitePaths(body)
	joined := strings.Join(cites, ",")
	if !strings.Contains(joined, "OrderController") || !strings.Contains(joined, "OrderService") {
		t.Fatalf("expected code cites, got %v", cites)
	}
	if strings.Contains(joined, "readme.html") {
		t.Fatalf("readme.html must be skipped: %v", cites)
	}
	out := mergeFocusPaths([]string{"svc/OrderService.java"}, cites, 4)
	if out[0] != "svc/OrderService.java" {
		t.Fatalf("primary first: %v", out)
	}
	if len(out) < 2 {
		t.Fatalf("expected merge of cites: %v", out)
	}
}

func TestMergeFocusPathsPrefersRoleCodeOverConfigNoise(t *testing.T) {
	// ADS-shaped weak deps: 8 config/log/editor paths would fill the entire budget
	// before cites under naive primary-first merge.
	weak := []string{
		"web/src/main/resources/log4j2.xml",
		"web/WebContent/WEB-INF/classes/log4j2.xml",
		"web/WebContent/js/help/ueditor/dialogs/charts/chart.config.js",
		"web/WebContent/js/help/ueditor/dialogs/template/config.js",
		"web/WebContent/js/ueditor/dialogs/charts/chart.config.js",
		"web/WebContent/js/ueditor/dialogs/template/config.js",
		"web/WebContent/WEB-INF/classes/mapper/base/LogRecord.xml",
		"web/WebContent/WEB-INF/classes/config/mybatis-config.xml",
	}
	cites := []string{
		"app/po/iir/FirstWeight.java",
		"app/controller/iir/FirstWeightController.java",
		"app/service/iir/FirstWeightService.java",
		"web/src/main/resources/mapper/iir/FirstWeightMapper.xml",
	}
	out := mergeFocusPaths(weak, cites, 8)
	joined := strings.Join(out, ",")
	if strings.Contains(joined, "log4j") || strings.Contains(joined, "ueditor") || strings.Contains(joined, "mybatis-config") {
		t.Fatalf("noise must not occupy focus budget: %v", out)
	}
	if !strings.Contains(joined, "FirstWeightController") || !strings.Contains(joined, "FirstWeightService") {
		t.Fatalf("role code must win: %v", out)
	}
	// FirstWeight.java under /po/ is entity via package-layout role.
	if !strings.Contains(joined, "FirstWeight.java") {
		t.Fatalf("entity via /po/ must be kept: %v", out)
	}
}

func TestPathRoleHintLitePackageLayout(t *testing.T) {
	if pathRoleHintLite("app/po/iir/FirstWeight.java") != "ent" {
		t.Fatalf("expected ent for /po/, got %q", pathRoleHintLite("app/po/iir/FirstWeight.java"))
	}
	if pathRoleHintLite("app/controller/iir/FirstWeightController.java") != "ctrl" {
		t.Fatalf("expected ctrl")
	}
	if pathRoleHintLite("app/service/iir/FirstWeightService.java") != "svc" {
		t.Fatalf("expected svc")
	}
}

func TestEnrichDependentFilesFromBody(t *testing.T) {
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{
			Title: "一级权重管理",
			Slug:  "w1",
			DependentFiles: []string{
				"web/src/main/resources/log4j2.xml",
				"web/WebContent/WEB-INF/classes/log4j2.xml",
				"web/WebContent/js/help/ueditor/dialogs/charts/chart.config.js",
				"web/WebContent/js/help/ueditor/dialogs/template/config.js",
				"web/WebContent/js/ueditor/dialogs/charts/chart.config.js",
				"web/WebContent/js/ueditor/dialogs/template/config.js",
				"web/WebContent/WEB-INF/classes/mapper/base/LogRecord.xml",
				"web/WebContent/WEB-INF/classes/config/mybatis-config.xml",
			},
		},
	}}
	body := "# 一级权重管理\n\n说明足够长以通过 substantial 判定的正文，并验证 cite 回填 dependent_files。\n\n更多说明用于充实页面内容。\n\n- [FirstWeightController.java](file://app/controller/iir/FirstWeightController.java#L1-L40)\n- [FirstWeightService.java](file://app/service/iir/FirstWeightService.java#L1-L50)\n- [FirstWeight.java](file://app/po/iir/FirstWeight.java#L1-L30)\n- [FirstWeightMapper.xml](file://web/src/main/resources/mapper/iir/FirstWeightMapper.xml#L1-L80)\n"
	contents := map[string]string{"w1": body}
	enrichDependentFilesFromBody(wiki, contents)
	deps := wiki.Pages[0].DependentFiles
	joined := strings.Join(deps, ",")
	if !strings.Contains(joined, "FirstWeightController") || !strings.Contains(joined, "FirstWeightService") {
		t.Fatalf("expected cite enrichment, got %v", deps)
	}
	if strings.Contains(joined, "log4j") || strings.Contains(joined, "ueditor") {
		t.Fatalf("noise deps must be dropped after enrich: %v", deps)
	}
	// Top slots should be role code, not residual config.
	top := joined
	if len(deps) > 3 {
		top = strings.Join(deps[:3], ",")
	}
	if !strings.Contains(top, "FirstWeight") {
		t.Fatalf("top deps should be FirstWeight* code: %v", deps)
	}
}

func TestPathTopicTokensCamelCase(t *testing.T) {
	toks := pathTopicTokens("app/controller/iir/FirstWeightController.java")
	joined := strings.Join(toks, ",")
	if !strings.Contains(joined, "first") || !strings.Contains(joined, "weight") {
		t.Fatalf("expected first/weight from CamelCase, got %v", toks)
	}
	if strings.Contains(joined, "controller") {
		t.Fatalf("role token controller must be stripped: %v", toks)
	}
}

func TestCitesOutrankOffTopicMultiRoleDeps(t *testing.T) {
	// Multi-role Log*/Bank* deps look "strong" but share no identifier family with
	// FirstWeight* cites — product-agnostic family drift detection must replace them.
	deps := []string{
		"web/app/base/view/LogRecord/LogRecordController.js",
		"web/app/base/view/LogRecord/SynsDataLogController.js",
		"web/app/base/model/BankStaffModel.js",
		"web/app/base/model/BankStaffUnUserModel.js",
		"web/app/base/view/logerror/LogErrorControllerV2.js",
		"web/src/main/resources/mapper/know/PointCatalogueMapper.xml",
	}
	cites := []string{
		"app/po/iir/FirstWeight.java",
		"app/controller/iir/FirstWeightController.java",
		"app/service/iir/FirstWeightService.java",
		"app/service/iir/impl/FirstWeightServiceImpl.java",
		"web/src/main/resources/mapper/iir/FirstWeightMapper.xml",
		"app/po/iir/SecondWeight.java",
	}
	if !citesOutrankDeps("一级权重管理与二级权重配置", "内部评级权重", deps, cites) {
		t.Fatal("expected cites to outrank off-topic multi-role deps")
	}
	// Same family on both sides → do not force replace.
	aligned := []string{
		"app/controller/iir/FirstWeightController.java",
		"app/service/iir/FirstWeightService.java",
		"app/po/iir/FirstWeight.java",
	}
	if citesOutrankDeps("一级权重", "", aligned, cites) {
		t.Fatal("aligned deps must not be treated as drift")
	}
}

func TestEnrichReplacesOffTopicMultiRoleDeps(t *testing.T) {
	wiki := &models.Wiki{Pages: []models.WikiPage{{
		Title: "一级权重管理与二级权重配置",
		Slug:  "w1",
		Goal:  "内部评级权重维护",
		DependentFiles: []string{
			"web/app/base/view/LogRecord/LogRecordController.js",
			"web/app/base/view/LogRecord/SynsDataLogController.js",
			"web/app/base/model/BankStaffModel.js",
			"web/app/base/model/BankStaffUnUserModel.js",
			"web/app/base/view/logerror/LogErrorControllerV2.js",
			"web/app/base/view/logsql/LogSqlControllerV2.js",
			"web/src/main/resources/mapper/know/PointCatalogueMapper.xml",
			"web/app/base/view/logerror/LogErrorViewV2.js",
		},
	}}}
	body := "# 一级权重\n\n说明足够长以通过 substantial 判定的正文内容，并验证偏题 multi-role deps 被 cite 族覆盖。\n\n更多说明用于充实页面内容。\n\n" +
		"- [FirstWeightController.java](file://app/controller/iir/FirstWeightController.java#L1-L40)\n" +
		"- [FirstWeightService.java](file://app/service/iir/FirstWeightService.java#L1-L50)\n" +
		"- [FirstWeight.java](file://app/po/iir/FirstWeight.java#L1-L30)\n" +
		"- [SecondWeight.java](file://app/po/iir/SecondWeight.java#L1-L30)\n" +
		"- [FirstWeightMapper.xml](file://web/src/main/resources/mapper/iir/FirstWeightMapper.xml#L1-L80)\n"
	enrichDependentFilesFromBody(wiki, map[string]string{"w1": body})
	deps := wiki.Pages[0].DependentFiles
	joined := strings.Join(deps, ",")
	if !strings.Contains(joined, "FirstWeightController") || !strings.Contains(joined, "FirstWeightService") {
		t.Fatalf("expected FirstWeight* after drift replace: %v", deps)
	}
	if strings.Contains(joined, "LogRecord") || strings.Contains(joined, "BankStaff") {
		t.Fatalf("off-topic Log/Bank deps must be replaced: %v", deps)
	}
	// Family tokens should dominate top slots.
	if len(deps) == 0 || !strings.Contains(deps[0], "FirstWeight") && !strings.Contains(deps[0], "SecondWeight") {
		t.Fatalf("top dep should be weight family: %v", deps)
	}
}

func TestRoleChainRecognizesContAndModel(t *testing.T) {
	g := roleChainMermaid([]string{
		"web/app/base/controller/FrameCont.js",
		"web/app/base/model/UserModel.js",
		"web/app/base/store/UserStore.js",
	})
	if g == "" {
		t.Fatal("expected ExtJS Cont/Model/Store role chain")
	}
	if !strings.Contains(g, "FrameCont") || !strings.Contains(g, "UserModel") {
		t.Fatalf("missing nodes: %s", g)
	}
}

func TestEnsurePageFormatUsesBodyCitesWhenDepsWeak(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Files: []scan.FileInfo{
			{RelativePath: "app/controller/iir/FirstWeightController.java", Lines: 40},
			{RelativePath: "app/service/iir/FirstWeightService.java", Lines: 50},
			{RelativePath: "app/po/iir/FirstWeight.java", Lines: 30},
		},
	}
	body := "<cite>\n**参考文献**\n- [FirstWeightController.java](file://app/controller/iir/FirstWeightController.java#L1-L40)\n- [FirstWeightService.java](file://app/service/iir/FirstWeightService.java#L1-L50)\n- [FirstWeight.java](file://app/po/iir/FirstWeight.java#L1-L30)\n</cite>\n\n## 目录\n1. [目的](#目的)\n\n## 目的\n\n说明足够长以通过 substantial 判定的正文内容，验证弱 deps 时用正文 cite 注入代码依赖示意。\n\n## 细节\n\n继续补充正文使页面充实。\n\n```mermaid\nflowchart TB\n  A-->B\n```\n\n```mermaid\nflowchart LR\n  C-->D\n```\n\n"
	// ADS-shaped: 8 weak config deps would crowd out cites under old merge.
	weak := []string{
		"web/src/main/resources/log4j2.xml",
		"web/WebContent/WEB-INF/classes/log4j2.xml",
		"web/WebContent/js/ueditor/dialogs/charts/chart.config.js",
		"web/WebContent/js/ueditor/dialogs/template/config.js",
		"web/WebContent/WEB-INF/classes/mapper/base/LogRecord.xml",
		"web/WebContent/WEB-INF/classes/config/mybatis-config.xml",
	}
	refs := mergeFocusPaths(weak, extractCitePaths(body), 12)
	if strings.Contains(strings.Join(refs, ","), "log4j") {
		t.Fatalf("merge must demote log4j: %v", refs)
	}
	out := EnsurePageFormat("一级权重", body, m, refs, true)
	if !strings.Contains(out, "代码依赖示意") {
		t.Fatalf("expected code-dep from body cites:\n%s", out[:min(1200, len(out))])
	}
	if !strings.Contains(out, "FirstWeightController") || !strings.Contains(out, "FirstWeightService") {
		t.Fatalf("expected role nodes from cites:\n%s", out)
	}
}

func TestExportEnrichAndInjectFromWeakDepsBodyCites(t *testing.T) {
	// End-to-end: Export with weak rebind-like deps + rich body cites must
	// rewrite dependent_files and inject 代码依赖示意.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "app", "controller", "iir", "FirstWeightController.java"),
		"package iir;\npublic class FirstWeightController {}\n")
	mustWriteFile(t, filepath.Join(dir, "app", "service", "iir", "FirstWeightService.java"),
		"package iir;\npublic class FirstWeightService {}\n")
	mustWriteFile(t, filepath.Join(dir, "app", "po", "iir", "FirstWeight.java"),
		"package iir;\npublic class FirstWeight {}\n")
	mustWriteFile(t, filepath.Join(dir, "web", "src", "main", "resources", "log4j2.xml"), "<Configuration/>\n")

	wiki := &models.Wiki{Pages: []models.WikiPage{{
		Title: "一级权重管理", Slug: "w1", Section: "内部评级", Track: models.TrackBusiness,
		DependentFiles: []string{
			"web/src/main/resources/log4j2.xml",
			"web/WebContent/js/ueditor/dialogs/charts/chart.config.js",
			"web/WebContent/WEB-INF/classes/config/mybatis-config.xml",
		},
		ContentPath: "内部评级/一级权重管理.md",
	}}}
	body := "# 一级权重管理\n\n说明足够长以通过 substantial 判定的正文内容，并验证弱 deps 时用正文 cite 注入代码依赖示意。\n\n## 细节\n\n继续补充正文使页面充实。\n\n<cite>\n- [FirstWeightController.java](file://app/controller/iir/FirstWeightController.java#L1-L40)\n- [FirstWeightService.java](file://app/service/iir/FirstWeightService.java#L1-L50)\n- [FirstWeight.java](file://app/po/iir/FirstWeight.java#L1-L30)\n</cite>\n\n## 目录\n1. [细节](#细节)\n\n```mermaid\nflowchart TB\n  A-->B\n```\n\n```mermaid\nflowchart LR\n  C-->D\n```\n"
	m := &scan.Model{
		Root: dir, Name: "demo", Language: "zh",
		Files: []scan.FileInfo{
			{RelativePath: "app/controller/iir/FirstWeightController.java", Lines: 10},
			{RelativePath: "app/service/iir/FirstWeightService.java", Lines: 10},
			{RelativePath: "app/po/iir/FirstWeight.java", Lines: 10},
			{RelativePath: "web/src/main/resources/log4j2.xml", Lines: 5},
		},
	}
	if err := Export(dir, m, wiki, map[string]string{"w1": body}, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	// dependent_files rewritten in wiki.json
	raw, err := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "FirstWeightController") {
		t.Fatalf("wiki.json deps not enriched:\n%s", raw)
	}
	if strings.Contains(string(raw), "log4j2.xml") && !strings.Contains(string(raw), "FirstWeightService") {
		t.Fatalf("log4j still monopolising deps:\n%s", raw)
	}
	// page content has code dependency diagram
	md, err := os.ReadFile(filepath.Join(dir, ".wikify", "content", "内部评级", "一级权重管理.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(md)
	if !strings.Contains(text, "代码依赖示意") {
		t.Fatalf("expected 代码依赖示意 inject:\n%s", text[:min(1500, len(text))])
	}
	if !strings.Contains(text, "FirstWeightController") || !strings.Contains(text, "FirstWeightService") {
		t.Fatalf("expected role nodes:\n%s", text)
	}
}

func TestExportWritesGraphJSON(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "web", "OrderController.java"), "package web;\npublic class OrderController {}\n")
	mustWriteFile(t, filepath.Join(dir, "svc", "OrderService.java"), "package svc;\npublic class OrderService {}\n")
	wiki := &models.Wiki{Pages: []models.WikiPage{{
		Title: "订单服务", Slug: "order", Section: "业务", Track: models.TrackBusiness,
		DependentFiles: []string{"web/OrderController.java", "svc/OrderService.java"},
		ContentPath:    "业务/订单服务.md",
	}}}
	body := "# 订单服务\n\n说明足够长以通过 substantial 判定的正文内容，并写入 graph。\n\n## 细节\n\n继续补充。\n"
	m := &scan.Model{
		Root: dir, Name: "demo", Language: "zh",
		Files: []scan.FileInfo{
			{RelativePath: "web/OrderController.java", Lines: 10},
			{RelativePath: "svc/OrderService.java", Lines: 10},
		},
		ImportEdges: []scan.ImportEdge{{From: "web/OrderController.java", To: "svc/OrderService.java", Kind: "import"}},
		EntryPoints: []scan.EntryPoint{{Path: "web/OrderController.java", Kind: "api"}},
	}
	if err := Export(dir, m, wiki, map[string]string{"order": body}, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	gp := filepath.Join(dir, ".wikify", "graph.json")
	raw, err := os.ReadFile(gp)
	if err != nil {
		t.Fatalf("graph.json not written: %v", err)
	}
	if !strings.Contains(string(raw), "OrderController") {
		t.Fatalf("graph.json missing edge content: %s", raw)
	}
}


func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
