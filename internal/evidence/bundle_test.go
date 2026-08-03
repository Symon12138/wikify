package evidence

import (
	"strings"
	"testing"

	"github.com/Symon12138/wikify/internal/scan"
)

func TestBuildPageEvidenceBundleGraph(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "app/controller/OrderController.java", Lines: 80},
			{RelativePath: "app/service/OrderService.java", Lines: 120},
			{RelativePath: "app/service/OrderServiceImpl.java", Lines: 200},
			{RelativePath: "app/po/Order.java", Lines: 40},
			{RelativePath: "other/Unrelated.java", Lines: 50},
		},
		ImportEdges: []scan.ImportEdge{
			{From: "app/controller/OrderController.java", To: "app/service/OrderService.java", Kind: "import"},
			{From: "app/service/OrderServiceImpl.java", To: "app/service/OrderService.java", Kind: "import"},
			{From: "app/service/OrderServiceImpl.java", To: "app/po/Order.java", Kind: "import"},
		},
		EntryPoints: []scan.EntryPoint{
			{Path: "app/controller/OrderController.java", Kind: "api", Symbol: "OrderController"},
		},
	}
	deps := []string{"app/controller/OrderController.java", "app/service/OrderService.java"}
	b := BuildPageEvidenceBundle(m, "订单接口", "说明订单 API", deps, 8)
	if len(b.Primary) == 0 {
		t.Fatal("primary empty")
	}
	joinedP := strings.Join(b.Primary, "\n")
	if !strings.Contains(joinedP, "OrderController") {
		t.Fatalf("primary missing controller: %v", b.Primary)
	}
	sec := b.PromptSection()
	if sec == "" {
		t.Fatal("prompt section empty")
	}
	if !strings.Contains(sec, "Primary sources") {
		t.Fatalf("prompt missing primary block: %s", sec)
	}
	if !strings.Contains(sec, "sequenceDiagram") {
		t.Fatalf("prompt should require sequenceDiagram: %s", sec)
	}
	if !strings.Contains(sec, "OrderController") {
		t.Fatalf("prompt missing primary path: %s", sec)
	}
	for _, n := range b.Neighbors {
		if strings.Contains(n, "Unrelated") {
			t.Fatalf("unrelated neighbor: %v", b.Neighbors)
		}
	}
}

func TestBuildPageEvidenceBundleOutlinesRoutesConfig(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "app/controller/OrderController.java", Lines: 80},
			{RelativePath: "app/service/OrderServiceImpl.java", Lines: 200},
			{RelativePath: "src/main/resources/application.yml", Lines: 30},
		},
		Symbols: map[string][]scan.Symbol{
			"app/service/OrderServiceImpl.java": {
				{Name: "OrderServiceImpl", Kind: "class", Line: 20},
				{Name: "createOrder", Kind: "method", Line: 42},
				{Name: "cancelOrder", Kind: "method", Line: 87},
				{Name: "listByUser", Kind: "method", Line: 120},
				{Name: "toDTO", Kind: "method", Line: 150},
				{Name: "validate", Kind: "method", Line: 170},
				{Name: "OrderView", Kind: "type", Line: 190},
			},
		},
		Routes: []scan.Route{
			{Method: "GET", Path: "/api/orders", File: "app/controller/OrderController.java", Hint: "spring"},
			{Method: "POST", Path: "/api/orders", File: "app/controller/OrderController.java", Hint: "spring"},
			{Method: "GET", Path: "/api/other", File: "elsewhere/Other.java", Hint: "spring"},
		},
		ConfigKeys: map[string][]string{
			"src/main/resources/application.yml": {"server", "spring", "logging"},
		},
	}
	deps := []string{
		"app/controller/OrderController.java",
		"app/service/OrderServiceImpl.java",
		"src/main/resources/application.yml",
	}
	b := BuildPageEvidenceBundle(m, "订单服务", "订单主流程", deps, 8, "technical")

	// Outlines: capped at maxOutlineSymbolsPerFile, callables preferred.
	syms := b.Outlines["app/service/OrderServiceImpl.java"]
	if len(syms) != maxOutlineSymbolsPerFile {
		t.Fatalf("outline cap: got %d symbols: %v", len(syms), syms)
	}
	for _, s := range syms {
		if s.Kind != "method" && s.Kind != "func" {
			t.Fatalf("trim must prefer callables, got kind %q: %v", s.Kind, syms)
		}
	}

	sec := b.PromptSection()
	if !strings.Contains(sec, "Primary file outlines") {
		t.Fatalf("missing outlines block:\n%s", sec)
	}
	if !strings.Contains(sec, "createOrder @ L42") {
		t.Fatalf("outline must carry name @ line:\n%s", sec)
	}
	// Routes: only Primary/Neighbor-owned routes render.
	if !strings.Contains(sec, "Detected HTTP routes") ||
		!strings.Contains(sec, "GET /api/orders — app/controller/OrderController.java") {
		t.Fatalf("missing route rows:\n%s", sec)
	}
	if strings.Contains(sec, "/api/other") {
		t.Fatalf("foreign route leaked:\n%s", sec)
	}
	// Config keys.
	if !strings.Contains(sec, "Config keys detected") ||
		!strings.Contains(sec, "src/main/resources/application.yml: server, spring, logging") {
		t.Fatalf("missing config keys block:\n%s", sec)
	}
	// Signature hints: real symbol names, no "heuristic" tag for symbolized file.
	joinedHints := strings.Join(b.Hints, "\n")
	if !strings.Contains(joinedHints, "createOrder") {
		t.Fatalf("hints must list real symbols: %v", b.Hints)
	}
	for _, h := range b.Hints {
		if strings.Contains(h, "OrderServiceImpl") && strings.Contains(h, "heuristic") {
			t.Fatalf("symbolized file must drop heuristic tag: %s", h)
		}
	}
}

func TestBuildPageEvidenceBundleBlocksOmittedWhenEmpty(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{{RelativePath: "pkg/thing.go", Lines: 50}},
	}
	b := BuildPageEvidenceBundle(m, "Thing", "about thing", []string{"pkg/thing.go"}, 8)
	sec := b.PromptSection()
	for _, block := range []string{
		"Primary file outlines", "Detected HTTP routes",
		"Config keys detected", "Per-section evidence routing",
	} {
		if strings.Contains(sec, block) {
			t.Fatalf("block %q must be omitted when empty:\n%s", block, sec)
		}
	}
}

func TestBuildPageEvidenceBundleReachChainHint(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "cmd/main.go", Lines: 30},
			{RelativePath: "internal/api/server.go", Lines: 90},
			{RelativePath: "internal/core/engine.go", Lines: 150},
		},
		ImportEdges: []scan.ImportEdge{
			{From: "cmd/main.go", To: "internal/api/server.go", Kind: "import"},
			{From: "internal/api/server.go", To: "internal/core/engine.go", Kind: "import"},
		},
		EntryPoints: []scan.EntryPoint{
			{Path: "cmd/main.go", Kind: "main", Symbol: "main"},
		},
	}
	b := BuildPageEvidenceBundle(m, "engine", "core engine flow", []string{"internal/core/engine.go"}, 8)
	joined := strings.Join(b.Hints, "\n")
	if !strings.Contains(joined, "call-path (import-level): main.go → server.go → engine.go") {
		t.Fatalf("missing reach-chain hint: %v", b.Hints)
	}
}

func TestBuildSectionPlanTechnicalAndBusiness(t *testing.T) {
	primary := []string{
		"app/controller/OrderController.java",
		"app/service/OrderServiceImpl.java",
		"app/po/Order.java",
		"src/main/resources/application.yml",
	}
	tech := buildSectionPlan("technical", primary, nil, nil)
	topics := func(plan []SectionEvidence) string {
		var ts []string
		for _, se := range plan {
			ts = append(ts, se.Topic)
		}
		return strings.Join(ts, "|")
	}
	tt := topics(tech)
	if !strings.Contains(tt, "Entry & API surface") ||
		!strings.Contains(tt, "Core logic & main flow") ||
		!strings.Contains(tt, "Data model") ||
		!strings.Contains(tt, "Configuration") {
		t.Fatalf("technical topics wrong: %s", tt)
	}
	if strings.Contains(tt, "Cross-cutting") {
		t.Fatalf("empty security bucket must be omitted: %s", tt)
	}
	for _, se := range tech {
		if strings.Contains(se.Topic, "Entry") && !strings.Contains(strings.Join(se.Files, ","), "OrderController") {
			t.Fatalf("entry bucket should hold controller: %v", se.Files)
		}
		if strings.Contains(se.Topic, "Configuration") && !strings.Contains(strings.Join(se.Files, ","), "application.yml") {
			t.Fatalf("config bucket should hold yml: %v", se.Files)
		}
	}

	biz := buildSectionPlan("business", primary, nil, nil)
	bt := topics(biz)
	if !strings.Contains(bt, "主流程") || !strings.Contains(bt, "业务规则") ||
		!strings.Contains(bt, "数据对象") || !strings.Contains(bt, "实现落点") {
		t.Fatalf("business topics wrong: %s", bt)
	}
	for _, se := range biz {
		if strings.Contains(se.Topic, "实现落点") && len(se.Files) != len(primary) {
			t.Fatalf("anchors should list all primary: %v", se.Files)
		}
	}

	if plan := buildSectionPlan("technical", []string{"random/notes.txt"}, nil, nil); len(plan) != 0 {
		t.Fatalf("no-role primary must produce empty plan: %v", plan)
	}
}

func TestBuildPageEvidenceBundleNilModel(t *testing.T) {
	deps := []string{"a/Foo.java", "b/Bar.java"}
	b := BuildPageEvidenceBundle(nil, "Foo", "bar", deps, 8)
	if len(b.Primary) != 2 {
		t.Fatalf("primary=%v", b.Primary)
	}
	if b.PromptSection() == "" {
		t.Fatal("expected prompt section from primary-only")
	}
}

func TestSoftVerifyPageBody(t *testing.T) {
	if issues := SoftVerifyPageBody(""); len(issues) == 0 {
		t.Fatal("empty body should issue")
	}
	good := "# Title\n\n" +
		"<cite>\n- [A.java](file://a/A.java#L1-L10)\n</cite>\n\n" +
		"## 目录\n1. [引言](#引言)\n2. [流程](#流程)\n\n" +
		"## 引言\nHello.\n\n" +
		"```mermaid\nsequenceDiagram\n  A->>B: call\n```\n\n" +
		"**图表来源**\n- [A.java:1-10](file://a/A.java#L1-L10)\n\n" +
		"## 流程\n" +
		"```mermaid\nflowchart TD\n  A --> B\n```\n\n" +
		"**图表来源**\n- [A.java:1-10](file://a/A.java#L1-L10)\n"
	issues := SoftVerifyPageBody(good)
	for _, iss := range issues {
		if strings.Contains(iss, "missing") || strings.Contains(iss, "empty") || strings.Contains(iss, "mermaid count") {
			t.Fatalf("unexpected hard issue on good body: %v", issues)
		}
	}
	thin := SoftVerifyPageBody("# Only title\n\nshort")
	if len(thin) == 0 {
		t.Fatal("thin body should have issues")
	}
}
