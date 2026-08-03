package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Symon12138/wikify/internal/models"
	"github.com/Symon12138/wikify/internal/scan"
)

// ── QL-1: LintPageBody ────────────────────────────────────────────────────────

func TestLintPageBodyFenceAutoClose(t *testing.T) {
	body := "# t\n\nintro text\n\n```go\nfunc main() {}\n"
	fixed, hard, soft := LintPageBody(body)
	if countFenceLines(fixed)%2 != 0 {
		t.Fatalf("fences still unbalanced:\n%s", fixed)
	}
	if !strings.Contains(fixed, "func main() {}") {
		t.Fatal("content lost during fence close")
	}
	if len(hard) != 0 {
		t.Fatalf("unexpected hard issues: %v", hard)
	}
	if len(soft) == 0 {
		t.Fatal("expected soft issue for auto-closed fence")
	}
	// Idempotent.
	fixed2, hard2, _ := LintPageBody(fixed)
	if fixed2 != fixed {
		t.Fatalf("not idempotent:\n--1--\n%s\n--2--\n%s", fixed, fixed2)
	}
	if len(hard2) != 0 {
		t.Fatalf("hard issues on second run: %v", hard2)
	}
}

func TestLintPageBodyMermaidDemotion(t *testing.T) {
	body := "# t\n\n```mermaid\nthis is prose, not a diagram\n```\n"
	fixed, hard, soft := LintPageBody(body)
	if strings.Contains(fixed, "```mermaid") {
		t.Fatalf("invalid mermaid fence not demoted:\n%s", fixed)
	}
	if !strings.Contains(fixed, "this is prose, not a diagram") {
		t.Fatal("mermaid demotion deleted content")
	}
	if len(hard) != 0 {
		t.Fatalf("unexpected hard issues: %v", hard)
	}
	if len(soft) == 0 {
		t.Fatal("expected soft issue for demoted mermaid")
	}
}

func TestLintPageBodyMermaidAllowlistSurvives(t *testing.T) {
	heads := []string{
		"graph TD", "flowchart LR", "sequenceDiagram", "classDiagram",
		"stateDiagram", "stateDiagram-v2", "erDiagram", "journey", "pie", "gantt",
	}
	for _, h := range heads {
		body := "# t\n\n```mermaid\n" + h + "\n  A --> B\n```\n"
		fixed, hard, _ := LintPageBody(body)
		if !strings.Contains(fixed, "```mermaid") {
			t.Errorf("valid mermaid %q was demoted:\n%s", h, fixed)
		}
		if len(hard) != 0 {
			t.Errorf("hard issues for valid mermaid %q: %v", h, hard)
		}
	}
	// %%{init}%% directives / %% comments are legal leading lines.
	body := "# t\n\n```mermaid\n%%{init: {'theme':'dark'}}%%\n%% comment\nflowchart TD\n  A --> B\n```\n"
	fixed, _, _ := LintPageBody(body)
	if !strings.Contains(fixed, "```mermaid") {
		t.Fatalf("mermaid with %%%% directive leader was demoted:\n%s", fixed)
	}
}

func TestLintPageBodyEmptyAndDuplicateH2(t *testing.T) {
	body := "# t\n\n## 概述\n\n这里有一些足够长的内容说明。\n\n## 空节\n\n## 概述\n\n第二段概述内容也足够长。\n"
	fixed, hard, soft := LintPageBody(body)
	if !strings.Contains(fixed, "## 概述 (2)") {
		t.Fatalf("duplicate H2 not renamed:\n%s", fixed)
	}
	if strings.Count(fixed, "## 概述\n") != 1 {
		t.Fatalf("first H2 occurrence should stay untouched:\n%s", fixed)
	}
	foundEmpty := false
	for _, s := range soft {
		if strings.Contains(s, "空节") {
			foundEmpty = true
		}
	}
	if !foundEmpty {
		t.Fatalf("empty section not flagged, soft=%v", soft)
	}
	if len(hard) != 0 {
		t.Fatalf("unexpected hard issues: %v", hard)
	}
	// Idempotent: renamed titles are unique now.
	fixed2, _, _ := LintPageBody(fixed)
	if fixed2 != fixed {
		t.Fatalf("not idempotent:\n--1--\n%s\n--2--\n%s", fixed, fixed2)
	}
}

func TestLintPageBodyHeadingsInsideFencesIgnored(t *testing.T) {
	body := "# t\n\n## 真实\n\n有内容的一节说明文字。\n\n```text\n## 假标题\n## 假标题\n```\n"
	fixed, hard, _ := LintPageBody(body)
	if strings.Contains(fixed, "假标题 (2)") {
		t.Fatalf("heading inside fence was renamed:\n%s", fixed)
	}
	if len(hard) != 0 {
		t.Fatalf("unexpected hard issues: %v", hard)
	}
}

// ── QL-2: ValidateCitePaths ───────────────────────────────────────────────────

func citeTestModel() *scan.Model {
	return &scan.Model{
		Name: "demo",
		Files: []scan.FileInfo{
			{RelativePath: "src/service/UserService.java", Lines: 120},
			{RelativePath: "src/util/Helper.java", Lines: 50},
			{RelativePath: "src/other/Helper.java", Lines: 60},
			{RelativePath: "web/app/config/settings.py", Lines: 80},
		},
	}
}

func TestValidateCitePaths(t *testing.T) {
	m := citeTestModel()
	body := strings.Join([]string{
		"详见 [UserService](file://src/service/UserService.java#L10-L20)。",
		"范围 [Clamp](file://src/service/UserService.java#L100-L400)。",
		"配置 [Settings](file://foo/settings.py#L1-L5)。",
		"工具 [Helper](file://wrong/other/Helper.java#L1-L2)。",
		"幽灵 [Ghost](file://no/such/File.java#L1-L3)。",
	}, "\n\n")
	got, total, valid, corrected, dropped := ValidateCitePaths(body, m)
	if total != 5 || valid != 2 || corrected != 2 || dropped != 1 {
		t.Fatalf("counts total=%d valid=%d corrected=%d dropped=%d want 5,2,2,1", total, valid, corrected, dropped)
	}
	for _, want := range []string{
		"[UserService](file://src/service/UserService.java#L10-L20)",
		"[Clamp](file://src/service/UserService.java#L100-L120)",
		"[Settings](file://web/app/config/settings.py#L1-L5)",
		"[Helper](file://src/other/Helper.java#L1-L2)",
		"`Ghost`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "no/such/File.java") {
		t.Fatalf("dropped path still present:\n%s", got)
	}
	// Idempotent: healed body must survive a second pass byte-identically.
	got2, total2, valid2, corrected2, dropped2 := ValidateCitePaths(got, m)
	if got2 != got {
		t.Fatalf("not idempotent:\n--1--\n%s\n--2--\n%s", got, got2)
	}
	if total2 != 4 || valid2 != 4 || corrected2 != 0 || dropped2 != 0 {
		t.Fatalf("second-pass counts total=%d valid=%d corrected=%d dropped=%d want 4,4,0,0", total2, valid2, corrected2, dropped2)
	}
}

func TestValidateCitePathsNilModelSkips(t *testing.T) {
	body := "详见 [Ghost](file://no/such/File.java#L1-L3)。"
	got, total, valid, corrected, dropped := ValidateCitePaths(body, nil)
	if got != body || total != 0 || valid != 0 || corrected != 0 || dropped != 0 {
		t.Fatalf("nil model must skip: got=%q counts=%d,%d,%d,%d", got, total, valid, corrected, dropped)
	}
	got, total, _, _, _ = ValidateCitePaths(body, &scan.Model{})
	if got != body || total != 0 {
		t.Fatalf("empty-file model must skip: got=%q total=%d", got, total)
	}
}

func TestValidateCitePathsBareCites(t *testing.T) {
	m := citeTestModel()
	body := "详见 file://src/util/Helper.java#L1-L99 内容，以及 file://no/such/File.java#L1-L3 说明。"
	got, total, valid, corrected, dropped := ValidateCitePaths(body, m)
	if total != 2 || valid != 1 || corrected != 0 || dropped != 1 {
		t.Fatalf("counts total=%d valid=%d corrected=%d dropped=%d want 2,1,0,1", total, valid, corrected, dropped)
	}
	if !strings.Contains(got, "file://src/util/Helper.java#L1-L50") {
		t.Fatalf("bare cite not clamped:\n%s", got)
	}
	if !strings.Contains(got, "`no/such/File.java`") {
		t.Fatalf("unresolved bare cite should keep path as code text:\n%s", got)
	}
	if strings.Contains(got, "file://no/such") {
		t.Fatalf("unresolved bare cite still a link:\n%s", got)
	}
	got2, _, _, _, _ := ValidateCitePaths(got, m)
	if got2 != got {
		t.Fatalf("bare cite pass not idempotent:\n--1--\n%s\n--2--\n%s", got, got2)
	}
}

func TestValidateCitePathsRecountsCappedLines(t *testing.T) {
	tmp := t.TempDir()
	// 2500 lines by scan.countLines semantics (1 + '\n' count).
	content := strings.Repeat("x\n", 2499) + "x"
	if err := os.WriteFile(filepath.Join(tmp, "big.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &scan.Model{
		Root:  tmp,
		Files: []scan.FileInfo{{RelativePath: "big.go", Lines: 2000}}, // capped scan value
	}
	got, total, valid, _, _ := ValidateCitePaths("[Big](file://big.go#L1-L3000)", m)
	if total != 1 || valid != 1 {
		t.Fatalf("counts total=%d valid=%d want 1,1", total, valid)
	}
	if !strings.Contains(got, "#L1-L2500") {
		t.Fatalf("expected recounted clamp to L2500, got:\n%s", got)
	}
}

// ── QL-3: rewriteWikiLinks ────────────────────────────────────────────────────

func TestRewriteWikiLinks(t *testing.T) {
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "客户基本信息", Slug: "50-50", Section: "客户管理模块", ContentPath: "客户管理模块/客户基本信息.md"},
		{Title: "Auth Overview", Slug: "auth-overview", Section: "Security", ContentPath: "Security/AuthOverview.md"},
		{Title: "Dup", Slug: "d1", Section: "A", ContentPath: "A/Dup.md"},
		{Title: "Dup", Slug: "d2", Section: "B", ContentPath: "B/Dup.md"},
	}}
	cases := []struct {
		name   string
		body   string
		want   string
		broken int
	}{
		{"exact content path kept", "[a](客户管理模块/客户基本信息.md)", "[a](客户管理模块/客户基本信息.md)", 0},
		{"title md rewritten", "[a](客户基本信息.md)", "[a](客户管理模块/客户基本信息.md)", 0},
		{"slug md rewritten", "[a](auth-overview.md)", "[a](Security/AuthOverview.md)", 0},
		{"normalized title rewritten", "[a](Auth_Overview.md)", "[a](Security/AuthOverview.md)", 0},
		{"fragment preserved", "[a](客户基本信息.md#sec)", "[a](客户管理模块/客户基本信息.md#sec)", 0},
		{"ambiguous kept broken", "[d](Dup.md)", "[d](Dup.md)", 1},
		{"missing counts broken", "[m](Missing.md)", "[m](Missing.md)", 1},
		{"numeric slug rewritten", "[x](50-50)", "[x](客户管理模块/客户基本信息.md)", 0},
		{"unknown numeric broken", "[x](99-99)", "[x](99-99)", 1},
		{"http untouched", "[h](http://example.com/a.md)", "[h](http://example.com/a.md)", 0},
		{"file scheme untouched", "[f](file://src/a.md)", "[f](file://src/a.md)", 0},
		{"anchor untouched", "[t](#目录)", "[t](#目录)", 0},
		{"non md non numeric untouched", "[i](img/logo.png)", "[i](img/logo.png)", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, broken := rewriteWikiLinks(tc.body, wiki)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if broken != tc.broken {
				t.Fatalf("broken=%d want %d", broken, tc.broken)
			}
			// Idempotent.
			got2, _ := rewriteWikiLinks(got, wiki)
			if got2 != got {
				t.Fatalf("not idempotent: %q -> %q", got, got2)
			}
		})
	}
}

// ── QL-4: quality report ──────────────────────────────────────────────────────

func TestExportWritesQualityReport(t *testing.T) {
	dir := t.TempDir()
	m := &scan.Model{
		Name: "demo", Language: "zh", GeneratedAt: "2026-01-01T00:00:00Z",
		Files: []scan.FileInfo{
			{RelativePath: "src/a.go", Lines: 30},
			{RelativePath: "src/b.go", Lines: 20},
		},
	}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{
			Title: "支付流程", Slug: "1-pay", Section: "核心业务能力",
			ContentPath: "核心业务能力/支付流程.md", DependentFiles: []string{"src/a.go"},
		},
		{
			Title: "空壳", Slug: "2-stub", Section: "核心业务能力",
			ContentPath: "核心业务能力/空壳.md",
		},
	}}
	payBody := "# 支付流程\n\n" +
		"## 目的\n\n说明支付流程 [a](file://src/a.go#L1-L10) 与相关模块。另见 [坏](file://nope/xx.go#L1-L2)。参考 [缺页](Missing.md)。\n\n" +
		"## 流程\n\n```mermaid\nflowchart TD\n  A --> B\n```\n\n步骤说明文字足够长以避免被判为空节。\n"
	contents := map[string]string{
		"1-pay":  payBody,
		"2-stub": "# 空壳\n\n> **状态:** 待补充\n\n已写入占位稿。\n",
	}
	if err := Export(dir, m, wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	rep, err := LoadQualityReport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Version != 1 {
		t.Fatalf("version=%d want 1", rep.Version)
	}
	// NAV-2 synthesizes a section index for 核心业务能力 (2 children) → 3 pages.
	if rep.Pages != 3 || len(rep.PageRecords) != 3 {
		t.Fatalf("pages=%d records=%d want 3,3", rep.Pages, len(rep.PageRecords))
	}
	if len(rep.StubList) != 1 || rep.StubList[0] != "2-stub" {
		t.Fatalf("stub_list=%v want [2-stub] (nav pages must never be stub-flagged)", rep.StubList)
	}
	if rep.SubstantialPct != 66.7 {
		t.Fatalf("substantial_pct=%v want 66.7", rep.SubstantialPct)
	}
	var pay *QualityPage
	for i := range rep.PageRecords {
		if rep.PageRecords[i].Slug == "1-pay" {
			pay = &rep.PageRecords[i]
		}
	}
	if pay == nil {
		t.Fatal("missing 1-pay record")
	}
	if pay.CitesTotal < 2 || pay.CitesValid < 1 || pay.CitesDropped < 1 {
		t.Fatalf("cite counters total=%d valid=%d dropped=%d", pay.CitesTotal, pay.CitesValid, pay.CitesDropped)
	}
	if pay.BrokenWikiLinks < 1 {
		t.Fatalf("broken_wiki_links=%d want >=1", pay.BrokenWikiLinks)
	}
	if pay.H2Count < 2 || pay.MermaidTotal < 1 || pay.Runes <= 0 {
		t.Fatalf("shape h2=%d mermaid=%d runes=%d", pay.H2Count, pay.MermaidTotal, pay.Runes)
	}
	if rep.DiagramCount < 1 {
		t.Fatalf("diagram_count=%d want >=1", rep.DiagramCount)
	}
	if len(rep.TrackDistribution) == 0 {
		t.Fatal("track_distribution empty")
	}
	// src/a.go is validly cited (body cite); dependency rebinding / injected
	// cite blocks may legitimately reference src/b.go too → at least 50%.
	if rep.RepoCoveragePct < 50.0 {
		t.Fatalf("repo_coverage_pct=%v want >=50", rep.RepoCoveragePct)
	}
	// Dropped cite must not survive in the exported page.
	out, err := os.ReadFile(filepath.Join(dir, ".wikify", "content", "核心业务能力", "支付流程.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "file://nope/xx.go") {
		t.Fatalf("dropped cite leaked into exported page:\n%s", out)
	}
	// Summary digest renders six lines.
	if lines := rep.Summary(); len(lines) != 6 {
		t.Fatalf("summary lines=%d want 6: %v", len(lines), lines)
	}
}

// Export run twice over the same tree must produce byte-identical content
// (QL-1/QL-2/QL-3 all idempotent inside the full pipeline).
func TestExportTwiceIdempotentContent(t *testing.T) {
	dir := t.TempDir()
	m := &scan.Model{
		Name: "demo", Language: "zh", GeneratedAt: "2026-01-01T00:00:00Z",
		Files: []scan.FileInfo{
			{RelativePath: "src/a.go", Lines: 30},
			{RelativePath: "src/b.go", Lines: 20},
		},
	}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{
			Title: "支付流程", Slug: "1-pay", Section: "核心业务能力",
			ContentPath: "核心业务能力/支付流程.md", DependentFiles: []string{"src/a.go"},
		},
	}}
	contents := map[string]string{
		"1-pay": "# 支付流程\n\n" +
			"## 目的\n\n说明支付流程 [a](file://src/a.go#L1-L10) 与相关模块。另见 [坏](file://nope/xx.go#L1-L2)。\n\n" +
			"## 流程\n\n```mermaid\nflowchart TD\n  A --> B\n```\n\n步骤说明文字足够长以避免被判为空节。\n",
	}
	if err := Export(dir, m, wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(dir, ".wikify", "content", "核心业务能力", "支付流程.md")
	first, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	// Second export re-consumes the mutated wiki + formatted contents (the
	// polish path). Content must not drift.
	if err := Export(dir, m, wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("export not idempotent:\n--1--\n%s\n--2--\n%s", first, second)
	}
}
