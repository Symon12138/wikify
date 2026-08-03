package evidence

import (
	"strings"
	"testing"

	"github.com/Symon12138/wikify/internal/scan"
)

func deepBody() string {
	return "# Page\n\n" +
		"## 目录\n1. [A](#a)\n\n" +
		"## A\n" + strings.Repeat("内容深入分析。", 40) + "\n\n" +
		"```mermaid\nsequenceDiagram\n  A->>B: call\n## not a heading inside fence\n```\n\n" +
		"**图表来源**\n- [A.java:1-10](file://app/a/A.java#L1-L10)\n\n" +
		"**章节来源**\n- [A.java:1-80](file://app/a/A.java#L1-L80)\n\n" +
		"## B\n| k | v |\n|---|---|\n| x | y |\n\n" +
		"Sources: [B.java](file://app/b/B.java#L5-L20)\n\n" +
		"**章节来源**\n- [B.java:1-40](file://app/b/B.java#L1-L40)\n\n" +
		"## C\nMore prose grounded in code.\n\n" +
		"Sources: [C.java](file://app/c/C.java#L2-L9)\n"
}

func TestDepthScoreCounts(t *testing.T) {
	r := DepthScore(deepBody())
	if r.Sections != 4 { // 目录, A, B, C — fence-internal "##" excluded
		t.Fatalf("sections=%d want 4", r.Sections)
	}
	if r.DistinctCitePaths != 3 { // a/A.java, b/B.java, c/C.java (dupes collapse)
		t.Fatalf("distinct cite paths=%d want 3", r.DistinctCitePaths)
	}
	if r.LineAnchoredCites < 4 {
		t.Fatalf("line anchored cites=%d want >=4", r.LineAnchoredCites)
	}
	if r.Mermaids != 1 {
		t.Fatalf("mermaids=%d want 1", r.Mermaids)
	}
	if r.Tables != 1 {
		t.Fatalf("tables=%d want 1", r.Tables)
	}
	if r.SectionSourceBlocks != 2 {
		t.Fatalf("section source blocks=%d want 2", r.SectionSourceBlocks)
	}
	if r.ProseRunes < 200 {
		t.Fatalf("prose runes=%d want >=200", r.ProseRunes)
	}
}

func TestDepthReportShallowRule(t *testing.T) {
	cases := []struct {
		name    string
		r       DepthReport
		shallow bool
	}{
		{"deep", DepthReport{Sections: 4, DistinctCitePaths: 3, SectionSourceBlocks: 1}, false},
		{"few sections", DepthReport{Sections: 3, DistinctCitePaths: 5, SectionSourceBlocks: 2}, true},
		{"few cite paths", DepthReport{Sections: 6, DistinctCitePaths: 2, SectionSourceBlocks: 2}, true},
		{"no section sources", DepthReport{Sections: 6, DistinctCitePaths: 5, SectionSourceBlocks: 0}, true},
		{"all zero", DepthReport{}, true},
	}
	for _, c := range cases {
		if got := c.r.Shallow(); got != c.shallow {
			t.Fatalf("%s: Shallow()=%v want %v", c.name, got, c.shallow)
		}
	}
}

func TestDepthScoreShallowBody(t *testing.T) {
	shallow := "# T\n\n## 目录\n1. x\n\n## Only\nshort\n"
	r := DepthScore(shallow)
	if !r.Shallow() {
		t.Fatalf("expected shallow: %+v", r)
	}
	if DepthScore(deepBody()).Shallow() {
		t.Fatalf("deep body must not be shallow: %s", DepthScore(deepBody()).Summary())
	}
}

func TestDepthReportSummaryFlagsFailingAxes(t *testing.T) {
	s := DepthReport{Sections: 2, DistinctCitePaths: 5, SectionSourceBlocks: 0}.Summary()
	if !strings.Contains(s, "sections=2 (<4)") {
		t.Fatalf("summary should flag sections: %s", s)
	}
	if strings.Contains(s, "distinct cite paths=5 (<") {
		t.Fatalf("summary should not flag passing axis: %s", s)
	}
	if !strings.Contains(s, "section-source blocks=0 (<1)") {
		t.Fatalf("summary should flag section sources: %s", s)
	}
}

func TestThinSections(t *testing.T) {
	body := "# T\n\n## 目录\n1. x\n\n" +
		"## Rich\n" + strings.Repeat("深入的分析内容。", 40) + "\n**章节来源**\n- [A](file://a/A.java#L1)\n\n" +
		"## Thin\nshort prose\n**章节来源**\n- [A](file://a/A.java#L1)\n\n" +
		"## NoSource\n" + strings.Repeat("长文但没有来源。", 40) + "\n"
	out := ThinSections(body)
	joined := strings.Join(out, "|")
	if strings.Contains(joined, "目录") {
		t.Fatalf("TOC must be skipped: %v", out)
	}
	if strings.Contains(joined, "Rich") {
		t.Fatalf("rich sourced section must not be thin: %v", out)
	}
	if !strings.Contains(joined, "Thin") {
		t.Fatalf("short section should be thin: %v", out)
	}
	if !strings.Contains(joined, "NoSource") {
		t.Fatalf("unsourced section should be thin: %v", out)
	}
}

func TestFindInvalidCitePaths(t *testing.T) {
	files := []scan.FileInfo{
		{RelativePath: "app/service/OrderService.java"},
		{RelativePath: "app/controller/OrderController.java"},
		{RelativePath: "web/js/util.js"},
		{RelativePath: "server/js/util.js"}, // ambiguous basename with the one above
	}
	body := "Sources: [X](file://app/service/OrderService.java#L1-L10)\n" + // valid
		"Sources: [Y](file://app/svc/OrderService.java#L3)\n" + // wrong dir, unique basename
		"Sources: [Z](file://lib/util.js#L2)\n" + // ambiguous basename
		"Sources: [W](file://app/NoSuchFile.java#L9)\n" // no basename match

	out := FindInvalidCitePaths(body, files)
	if len(out) != 3 {
		t.Fatalf("want 3 issues, got %d: %v", len(out), out)
	}
	joined := strings.Join(out, "\n")
	if strings.Contains(joined, "app/service/OrderService.java (") ||
		strings.Contains(joined, "repo: app/service/OrderService.java") {
		t.Fatalf("valid path flagged: %v", out)
	}
	if !strings.Contains(joined, "app/svc/OrderService.java (did you mean app/service/OrderService.java?)") {
		t.Fatalf("missing unique-basename suggestion: %v", out)
	}
	if strings.Contains(joined, "lib/util.js (did you mean") {
		t.Fatalf("ambiguous basename must not get a suggestion: %v", out)
	}
	if !strings.Contains(joined, "cited path does not exist in repo: lib/util.js") {
		t.Fatalf("ambiguous miss should still be reported: %v", out)
	}
	if !strings.Contains(joined, "cited path does not exist in repo: app/NoSuchFile.java") ||
		strings.Contains(joined, "NoSuchFile.java (did you mean") {
		t.Fatalf("no-match miss should be reported without suggestion: %v", out)
	}
}

func TestFindInvalidCitePathsSkipsWithoutFiles(t *testing.T) {
	body := "Sources: [X](file://ghost/Missing.java#L1)"
	if out := FindInvalidCitePaths(body, nil); out != nil {
		t.Fatalf("nil files must disable detection, got %v", out)
	}
	if out := FindInvalidCitePaths("", []scan.FileInfo{{RelativePath: "a.go"}}); out != nil {
		t.Fatalf("empty body should yield nil, got %v", out)
	}
}

func TestFindInvalidCitePathsCap(t *testing.T) {
	files := []scan.FileInfo{{RelativePath: "real.go"}}
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("file://missing")
		sb.WriteByte(byte('a' + i))
		sb.WriteString(".go\n")
	}
	out := FindInvalidCitePaths(sb.String(), files)
	if len(out) != maxInvalidCiteIssues {
		t.Fatalf("cap %d, got %d", maxInvalidCiteIssues, len(out))
	}
}
