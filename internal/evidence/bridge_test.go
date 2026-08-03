package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JSHurt/wikify/internal/scan"
)

func TestBuildAndLoadTitleBridge(t *testing.T) {
	m := &scan.Model{
		Root: t.TempDir(),
		Files: []scan.FileInfo{
			{RelativePath: "app/controller/OrderController.java", Lines: 80},
			{RelativePath: "app/service/OrderService.java", Lines: 120},
			{RelativePath: "app/po/Order.java", Lines: 40},
			{RelativePath: "pom.xml", Lines: 20},
		},
	}
	deps := map[string][]string{
		"订单受理": {"app/controller/OrderController.java", "app/service/OrderService.java"},
	}
	b := BuildTitleBridge(m, []string{"订单受理"}, deps)
	if b == nil || len(b.TokenPaths) == 0 {
		t.Fatal("expected token paths from scan")
	}
	// path tokens should include order / controller / service stems
	joined := strings.Join(sortedTokenKeys(b.TokenPaths), ",")
	if !strings.Contains(joined, "order") {
		t.Fatalf("expected order token in bridge keys: %s", joined)
	}
	if len(b.PageHints) != 1 {
		t.Fatalf("page hints=%d", len(b.PageHints))
	}
	// no pom noise as sole key path for order
	for _, p := range b.TokenPaths["order"] {
		if strings.HasSuffix(p, "pom.xml") {
			t.Fatalf("pom should not be order token path: %v", b.TokenPaths["order"])
		}
	}

	path := filepath.Join(t.TempDir(), "meta", "title_bridge.json")
	if err := SaveTitleBridge(path, b); err != nil {
		t.Fatal(err)
	}
	loaded := LoadTitleBridge(path)
	if loaded == nil || len(loaded.PageHints) != 1 {
		t.Fatalf("load failed: %+v", loaded)
	}
	extra := ExtraSynonymsFromBridge(loaded, "订单受理", "说明订单流程")
	if len(extra) == 0 {
		t.Fatal("expected extra synonyms from exact page hint")
	}
	// exact title prior paths
	prior := PathsForTitleHints(loaded, "订单受理")
	if len(prior) == 0 {
		t.Fatal("expected prior paths")
	}
}

func TestExtraSynonymsSoftOverlap(t *testing.T) {
	b := &TitleBridge{
		Version: 1,
		PageHints: []TitleBridgePageHint{
			{
				Title:      "订单受理管理",
				Tokens:     []string{"订单", "受理", "管理"},
				PathTokens: []string{"order", "service"},
				Paths:      []string{"app/service/OrderService.java"},
			},
		},
		TokenPaths: map[string][]string{
			"order": {"app/service/OrderService.java"},
		},
	}
	// title shares 订单+受理
	extra := ExtraSynonymsFromBridge(b, "订单受理查询", "查询")
	hit := false
	for _, e := range extra {
		if e == "order" || e == "service" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected soft overlap synonyms, got %v", extra)
	}
}

func TestPickDependentFilesUsesTitleBridge(t *testing.T) {
	root := t.TempDir()
	// Bridge maps Chinese title to Order* paths via path tokens.
	br := &TitleBridge{
		Version: 1,
		PageHints: []TitleBridgePageHint{
			{
				Title:      "一级权重配置",
				Tokens:     []string{"一级", "权重", "配置"},
				PathTokens: []string{"first", "weight"},
				Paths: []string{
					"app/controller/FirstWeightController.java",
					"app/service/FirstWeightService.java",
				},
			},
		},
		TokenPaths: map[string][]string{
			"first":  {"app/controller/FirstWeightController.java"},
			"weight": {"app/service/FirstWeightService.java"},
		},
	}
	if err := SaveTitleBridge(DefaultTitleBridgePath(root), br); err != nil {
		t.Fatal(err)
	}
	// Ensure parent meta dir exists for DefaultTitleBridgePath
	_ = os.MkdirAll(filepath.Join(root, ".wikify", "meta"), 0o755)
	// re-save after mkdir
	if err := SaveTitleBridge(DefaultTitleBridgePath(root), br); err != nil {
		t.Fatal(err)
	}

	m := &scan.Model{
		Root: root,
		Files: []scan.FileInfo{
			{RelativePath: "app/controller/FirstWeightController.java", Lines: 100},
			{RelativePath: "app/service/FirstWeightService.java", Lines: 120},
			{RelativePath: "app/service/LogRecordService.java", Lines: 80},
			{RelativePath: "app/controller/BankStaffController.java", Lines: 90},
			{RelativePath: "pom.xml", Lines: 40},
		},
	}
	// Title without English tokens — bridge should inject first/weight.
	out := PickDependentFiles(m, "一级权重配置", "说明权重规则", 6)
	if len(out) == 0 {
		t.Fatal("empty deps")
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "FirstWeight") {
		t.Fatalf("expected FirstWeight* via title bridge, got %v", out)
	}
	if strings.Contains(joined, "pom.xml") {
		t.Fatalf("pom noise: %v", out)
	}
}

func TestMergeTitleBridge(t *testing.T) {
	a := &TitleBridge{TokenPaths: map[string][]string{"order": {"a/Order.java"}}}
	b := &TitleBridge{
		TokenPaths: map[string][]string{"order": {"b/OrderService.java"}, "pay": {"c/Pay.java"}},
		PageHints:  []TitleBridgePageHint{{Title: "订单", Paths: []string{"b/OrderService.java"}}},
	}
	m := MergeTitleBridge(a, b)
	if len(m.TokenPaths["order"]) < 2 {
		t.Fatalf("merge paths: %v", m.TokenPaths["order"])
	}
	if len(m.PageHints) != 1 {
		t.Fatalf("hints=%d", len(m.PageHints))
	}
}
