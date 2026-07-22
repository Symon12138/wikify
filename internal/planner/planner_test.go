package planner

import (
	"strings"
	"testing"

	"github.com/symon/wikify/internal/models"
	"github.com/symon/wikify/internal/scan"
)

func TestBuildDefaultTreeZH(t *testing.T) {
	m := &scan.Model{
		Name:     "demo",
		Language: "zh",
		Files: []scan.FileInfo{
			{RelativePath: "src/main/java/com/demo/OrderController.java", Lines: 50},
			{RelativePath: "src/main/java/com/demo/entity/Order.java", Lines: 30},
			{RelativePath: "src/main/java/com/demo/service/OrderService.java", Lines: 40},
		},
		Modules: []scan.ModuleSummary{
			{Name: "demo", Path: "src"},
		},
	}
	wiki := Build(m, nil, Options{MaxPages: 80})
	if wiki == nil || len(wiki.Pages) < 5 {
		t.Fatalf("expected multi-page seed, got %d", len(wiki.Pages))
	}
	hasOverview := false
	for _, p := range wiki.Pages {
		if p.Title == "项目概述" {
			hasOverview = true
		}
		if p.ContentPath == "" {
			t.Errorf("page %q missing ContentPath", p.Title)
		}
		if p.DescriptionSlug == "" {
			t.Errorf("page %q missing DescriptionSlug", p.Title)
		}
	}
	if !hasOverview {
		t.Error("missing 项目概述")
	}
	xml := FormatSeedXML(wiki)
	if !strings.Contains(xml, "<section>") || !strings.Contains(xml, "<topic") {
		t.Fatalf("bad seed xml: %s", xml[:min(200, len(xml))])
	}
}

func TestBusinessDomainsPreferCustomerOverControl(t *testing.T) {
	// Simulate GRC-ish layout with many controllers under cust/kri packages.
	var files []scan.FileInfo
	for _, name := range []string{
		"CustBasicControl", "CustEntityControl", "CustFollowControl",
		"CustFinMainControl", "CustContractControl", "CustBasicService",
	} {
		files = append(files, scan.FileInfo{
			RelativePath: "com.sunyard.common.app/src/main/java/com/sunyard/common/app/controller/cust/" + name + ".java",
			Lines:        80,
		})
	}
	for _, name := range []string{"KriElementValueController", "KriQuotaValueController", "KriElementService"} {
		files = append(files, scan.FileInfo{
			RelativePath: "com.sunyard.common.app/src/main/java/com/sunyard/common/app/controller/kri/" + name + ".java",
			Lines:        60,
		})
	}
	// Flood of unrelated controllers that should be aggregated/capped, not 1:1 pages.
	for i := 0; i < 30; i++ {
		files = append(files, scan.FileInfo{
			RelativePath: "com.sunyard.common.app/src/main/java/com/sunyard/common/app/controller/misc/MiscThing" + strings.Repeat("X", i%5) + "Control.java",
			Lines:        40,
		})
	}
	m := &scan.Model{Name: "grc", Language: "zh", Files: files}
	wiki := Build(m, nil, Options{MaxPages: 120, InventoryRatio: 0.25})
	if wiki == nil {
		t.Fatal("nil wiki")
	}

	var biz, inv, classlike int
	for _, p := range wiki.Pages {
		switch p.Section {
		case "客户管理模块", "指标监控模块", "内部审计模块", "内部控制模块", "知识管理模块", "风险评估模块", "工作流管理":
			biz++
		case "接口文档", "API接口文档", "数据库设计", "数据模型设计":
			if p.Title != p.Section && p.Title != "接口设计原则" {
				inv++
			}
		}
		if strings.Contains(p.Title, "Control") || strings.HasSuffix(p.Title, "Control接口") {
			classlike++
		}
	}
	if biz < 2 {
		t.Fatalf("expected business domain pages, biz=%d total=%d titles=%v", biz, len(wiki.Pages), titlesOf(wiki))
	}
	if float64(inv) > float64(len(wiki.Pages))*0.35 {
		t.Fatalf("inventory too heavy: inv=%d total=%d", inv, len(wiki.Pages))
	}
	if classlike > 0 {
		t.Fatalf("class-like titles still present: %v", titlesOf(wiki))
	}
	joined := strings.Join(titlesOf(wiki), "\n")
	if !strings.Contains(joined, "客户") {
		t.Fatalf("expected 客户* business titles, got:\n%s", joined)
	}
}

func titlesOf(wiki *models.Wiki) []string {
	var out []string
	for _, p := range wiki.Pages {
		out = append(out, p.Title)
	}
	return out
}

func TestDescriptionSlug(t *testing.T) {
	if DescriptionSlug("项目概述") != "project-overview" {
		t.Fatalf("got %s", DescriptionSlug("项目概述"))
	}
	if DescriptionSlug("Getting Started") != "getting-started" {
		t.Fatalf("got %s", DescriptionSlug("Getting Started"))
	}
	s := DescriptionSlug("纯中文无映射标题")
	if s == "" || !strings.HasPrefix(s, "topic-") {
		t.Fatalf("expected hash fallback, got %q", s)
	}
}

func TestDualRailTracksAndBudgets(t *testing.T) {
	var files []scan.FileInfo
	for _, name := range []string{"CustBasicControl", "CustEntityControl", "CustFollowControl"} {
		files = append(files, scan.FileInfo{
			RelativePath: "app/src/main/java/com/demo/controller/cust/" + name + ".java",
			Lines:        80,
		})
	}
	for _, name := range []string{"KriQuotaControl", "KriElementService"} {
		files = append(files, scan.FileInfo{
			RelativePath: "app/src/main/java/com/demo/controller/kri/" + name + ".java",
			Lines:        50,
		})
	}
	for i := 0; i < 20; i++ {
		files = append(files, scan.FileInfo{
			RelativePath: "app/src/main/java/com/demo/controller/misc/Misc" + strings.Repeat("A", i%3+1) + "Control.java",
			Lines:        40,
		})
	}
	m := &scan.Model{Name: "demo", Language: "zh", Files: files}
	wiki := Build(m, nil, Options{MaxPages: 80, InventoryRatio: 0.20})
	if wiki == nil || len(wiki.Pages) == 0 {
		t.Fatal("empty wiki")
	}
	var found, biz, tech int
	for _, p := range wiki.Pages {
		if p.Track == "" {
			t.Errorf("page %q missing Track", p.Title)
		}
		switch p.Track {
		case models.TrackFoundation:
			found++
		case models.TrackBusiness:
			biz++
		case models.TrackTechnical:
			tech++
		}
	}
	if found == 0 {
		t.Error("expected foundation pages")
	}
	if biz == 0 {
		t.Error("expected business pages")
	}
	if tech == 0 {
		t.Error("expected technical pages")
	}
	// Business should not be dwarfed by technical inventory under dual-rail budgets.
	if biz < 2 {
		t.Fatalf("business rail too thin: foundation=%d business=%d technical=%d", found, biz, tech)
	}
}

func TestApplyHierarchyPathsSetsTrack(t *testing.T) {
	w := &models.Wiki{Pages: []models.WikiPage{
		{Title: "客户基本信息", Section: "客户管理模块"},
		{Title: "项目概述", Section: "项目概述"},
		{Title: "接口总览", Section: "接口文档"},
	}}
	ApplyHierarchyPaths(w)
	if w.Pages[0].Track != models.TrackBusiness {
		t.Fatalf("biz track=%q", w.Pages[0].Track)
	}
	if w.Pages[1].Track != models.TrackFoundation {
		t.Fatalf("found track=%q", w.Pages[1].Track)
	}
	if w.Pages[2].Track != models.TrackTechnical {
		t.Fatalf("tech track=%q", w.Pages[2].Track)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestEnsureTracksAndRebalance(t *testing.T) {
	// Simulated free LLM catalog: no tracks, almost no business pages.
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Section: "项目概述", Slug: "1"},
		{Title: "某接口索引", Section: "接口文档", Slug: "2"},
		{Title: "某表结构", Section: "数据库设计", Slug: "3"},
		{Title: "整体架构", Section: "系统架构设计", Slug: "4"},
	}}
	seed := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Section: "项目概述", Track: models.TrackFoundation, Slug: "s1"},
		{Title: "客户基本信息管理", Section: "客户管理模块", Track: models.TrackBusiness, Slug: "s2"},
		{Title: "KRI指标监控", Section: "指标监控模块", Track: models.TrackBusiness, Slug: "s3"},
		{Title: "客户跟进管理", Section: "客户管理模块", Track: models.TrackBusiness, Slug: "s4"},
		{Title: "审计因子管理", Section: "内部审计模块", Track: models.TrackBusiness, Slug: "s5"},
		{Title: "ICE评价", Section: "内部控制模块", Track: models.TrackBusiness, Slug: "s6"},
		{Title: "杂接口", Section: "接口文档", Track: models.TrackTechnical, Slug: "s7"},
	}}
	RebalanceDualRail(wiki, seed, 80)
	var biz, found, empty int
	for _, p := range wiki.Pages {
		if p.Track == "" {
			empty++
		}
		switch p.Track {
		case models.TrackBusiness:
			biz++
		case models.TrackFoundation:
			found++
		}
	}
	if empty > 0 {
		t.Fatalf("empty tracks remain: %d", empty)
	}
	if found < 1 {
		t.Fatalf("expected foundation pages after rebalance, found=%d", found)
	}
	if biz < 4 {
		t.Fatalf("expected soft-merged business pages, biz=%d total=%d", biz, len(wiki.Pages))
	}
	// Original technical pages still present
	titles := titlesOf(wiki)
	joined := strings.Join(titles, ",")
	if !strings.Contains(joined, "某接口索引") {
		t.Fatalf("LLM technical page dropped: %v", titles)
	}
}

func TestFromAllowlistInfersTrack(t *testing.T) {
	// Build via allowlist path: plan documents
	// Use Planned conversion through Build with plan is heavy; unit-test InferTrack wiring via empty track pages + EnsureTracks.
	w := &models.Wiki{Pages: []models.WikiPage{
		{Title: "整体架构设计", Section: "系统架构设计"},
		{Title: "客户基本信息", Section: "客户管理模块"},
		{Title: "快速开始", Section: "快速开始"},
	}}
	EnsureTracks(w)
	if w.Pages[0].Track != models.TrackTechnical {
		t.Fatalf("arch track=%s", w.Pages[0].Track)
	}
	if w.Pages[1].Track != models.TrackBusiness {
		t.Fatalf("biz track=%s", w.Pages[1].Track)
	}
	if w.Pages[2].Track != models.TrackFoundation {
		t.Fatalf("found track=%s", w.Pages[2].Track)
	}
}
