package planner

import (
	"strings"
	"testing"

	"github.com/Symon12138/wikify/internal/models"
	"github.com/Symon12138/wikify/internal/scan"
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

func TestBusinessDomainsFromPathSegments(t *testing.T) {
	// Path tokens drive section names — no fixed product domain dictionary.
	var files []scan.FileInfo
	for _, name := range []string{
		"CustBasicControl", "CustEntityControl", "CustFollowControl",
		"CustFinMainControl", "CustContractControl", "CustBasicService",
	} {
		files = append(files, scan.FileInfo{
			RelativePath: "com.demo.app/src/main/java/com/demo/app/controller/cust/" + name + ".java",
			Lines:        80,
		})
	}
	for _, name := range []string{"KriElementValueController", "KriQuotaValueController", "KriElementService"} {
		files = append(files, scan.FileInfo{
			RelativePath: "com.demo.app/src/main/java/com/demo/app/controller/kri/" + name + ".java",
			Lines:        60,
		})
	}
	// Flood of unrelated controllers that should be aggregated/capped, not 1:1 pages.
	for i := 0; i < 30; i++ {
		files = append(files, scan.FileInfo{
			RelativePath: "com.demo.app/src/main/java/com/demo/app/controller/misc/MiscThing" + strings.Repeat("X", i%5) + "Control.java",
			Lines:        40,
		})
	}
	m := &scan.Model{Name: "demo", Language: "zh", Files: files}
	wiki := Build(m, nil, Options{MaxPages: 120, InventoryRatio: 0.25})
	if wiki == nil {
		t.Fatal("nil wiki")
	}

	joined := strings.Join(titlesOf(wiki), "\n")
	var inv, classlike, bizPages int
	for _, p := range wiki.Pages {
		if p.Track == models.TrackBusiness {
			bizPages++
		}
		if p.Section == "接口文档" || p.Section == "API接口文档" || p.Section == "数据库设计" || p.Section == "数据模型设计" {
			if p.Title != p.Section && p.Title != "接口设计原则" {
				inv++
			}
		}
		if strings.Contains(p.Title, "Control") || strings.HasSuffix(p.Title, "Control接口") {
			classlike++
		}
	}
	if bizPages < 2 {
		t.Fatalf("expected business pages from path segments, biz=%d total=%d titles=\n%s", bizPages, len(wiki.Pages), joined)
	}
	if float64(inv) > float64(len(wiki.Pages))*0.35 {
		t.Fatalf("inventory too heavy: inv=%d total=%d", inv, len(wiki.Pages))
	}
	if classlike > 0 {
		t.Fatalf("class-like titles still present: %v", titlesOf(wiki))
	}
	// Path tokens should surface (cust/kri humanized), not hard-coded product syllabus.
	low := strings.ToLower(joined)
	if !strings.Contains(low, "cust") && !strings.Contains(joined, "Cust") &&
		!strings.Contains(low, "kri") && !strings.Contains(joined, "Kri") {
		t.Fatalf("expected path-segment business titles from cust/kri packages, got:\n%s", joined)
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
	if s == "" || strings.HasPrefix(s, "topic-") {
		t.Fatalf("expected readable role+hash slug, got %q", s)
	}
	if !strings.HasPrefix(s, "page-") && !strings.Contains(s, "-") {
		t.Fatalf("expected role-prefix form, got %q", s)
	}
	mgmt := DescriptionSlug("客户基本信息管理")
	if !strings.HasPrefix(mgmt, "mgmt-") || mgmt == "mgmt" || mgmt == "mgmt-capability" {
		t.Fatalf("expected unique mgmt-<hash> for 管理 title, got %q", mgmt)
	}
	// Pure role composition must not be accepted as final slug (collision risk).
	weak := DescriptionSlug("业务能力管理")
	if weak == "mgmt-capability" || weak == "capability-mgmt" || isBareRoleSlug(weak) {
		t.Fatalf("weak role-only slug still accepted: %q", weak)
	}
	api := DescriptionSlug("订单接口")
	if !strings.HasPrefix(api, "api") && api != "api" {
		// "订单接口" → suffix strip may leave ascii-ish "api" or "api-..."
		if !strings.Contains(api, "api") {
			t.Fatalf("expected api-related slug, got %q", api)
		}
	}
}

func TestEngineeringSeedsPresent(t *testing.T) {
	// Multi-module Java-ish tree should seed API/security/ops/FAQ style engineering topics.
	var files []scan.FileInfo
	for _, p := range []string{
		"app/src/main/java/com/demo/controller/order/OrderControl.java",
		"app/src/main/java/com/demo/service/order/OrderService.java",
		"app/src/main/java/com/demo/entity/order/Order.java",
		"app/src/main/java/com/demo/security/AuthFilter.java",
		"app/src/main/resources/application.yml",
		"app/src/test/java/com/demo/OrderTest.java",
		"deploy/Dockerfile",
		"app/pom.xml",
		"api/pom.xml",
	} {
		files = append(files, scan.FileInfo{RelativePath: p, Lines: 40})
	}
	// pad file count for FAQ/performance thresholds
	for i := 0; i < 50; i++ {
		files = append(files, scan.FileInfo{
			RelativePath: "app/src/main/java/com/demo/misc/Misc" + strings.Repeat("X", i%3+1) + ".java",
			Lines:        20,
		})
	}
	m := &scan.Model{
		Name: "demo", Language: "zh", Files: files,
		Modules: []scan.ModuleSummary{{Name: "app", Path: "app"}, {Name: "api", Path: "api"}},
	}
	wiki := Build(m, nil, Options{MaxPages: 120})
	joined := strings.Join(titlesOf(wiki), "\n")
	wantAny := []string{"API接口文档", "安全与访问控制", "部署与运维", "测试与质量", "常见问题", "编码规范", "性能与扩展"}
	hit := 0
	for _, w := range wantAny {
		if strings.Contains(joined, w) {
			hit++
		}
	}
	if hit < 4 {
		t.Fatalf("expected generic engineering seeds (>=4 of %v), hit=%d titles=\n%s", wantAny, hit, joined)
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
		{Title: "订单受理", Section: "订单模块"},
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
		{Title: "订单受理管理", Section: "订单模块", Track: models.TrackBusiness, Slug: "s2"},
		{Title: "库存查询", Section: "库存模块", Track: models.TrackBusiness, Slug: "s3"},
		{Title: "退款流程", Section: "订单模块", Track: models.TrackBusiness, Slug: "s4"},
		{Title: "结算对账", Section: "结算模块", Track: models.TrackBusiness, Slug: "s5"},
		{Title: "会员积分", Section: "会员模块", Track: models.TrackBusiness, Slug: "s6"},
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
	// Technical seed pages must NOT be force-merged by rebalance.
	if strings.Contains(joined, "杂接口") {
		t.Fatalf("technical seed should not soft-merge: %v", titles)
	}
}

func TestSignalDrivenDefaultTree(t *testing.T) {
	// Auth + config + API signals present; fixed product syllabus pages must not appear.
	m := &scan.Model{
		Name: "demo", Language: "zh",
		Files: []scan.FileInfo{
			{RelativePath: "src/main/java/com/demo/SecurityFilter.java", Lines: 40},
			{RelativePath: "src/main/java/com/demo/auth/LoginService.java", Lines: 30},
			{RelativePath: "src/main/java/com/demo/OrderController.java", Lines: 50},
			{RelativePath: "src/main/resources/application.yml", Lines: 20},
			{RelativePath: "src/test/java/com/demo/OrderTest.java", Lines: 25},
		},
	}
	wiki := Build(m, nil, Options{MaxPages: 120})
	joined := strings.Join(titlesOf(wiki), "\n")

	// Must have universal foundation.
	for _, want := range []string{"项目概述", "快速开始"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing foundation %q; titles:\n%s", want, joined)
		}
	}
	// Signal-gated optional sections.
	if !strings.Contains(joined, "安全与访问控制") && !strings.Contains(joined, "Security") {
		t.Errorf("auth signal should open security index; titles:\n%s", joined)
	}
	if !strings.Contains(joined, "配置管理") && !strings.Contains(joined, "Configuration") {
		t.Errorf("config signal should open config index; titles:\n%s", joined)
	}
	if !strings.Contains(joined, "测试与质量") && !strings.Contains(joined, "Testing") {
		t.Errorf("test signal should open testing index; titles:\n%s", joined)
	}

	// Hard-coded product/engineering syllabus must be gone.
	for _, bad := range []string{"安全设计", "测试策略", "常见问题解答", "环境部署", "认证与会话", "客户管理模块", "指标监控模块"} {
		if strings.Contains(joined, bad) {
			t.Errorf("project-specific template %q must not appear; titles:\n%s", bad, joined)
		}
	}
}

func TestRebalanceOnlyFoundationBusiness(t *testing.T) {
	// Free LLM catalog: thin business; seed has both business and technical.
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Section: "项目概述", Slug: "1"},
		{Title: "订单管理", Section: "订单模块", Slug: "2"},
		{Title: "整体架构", Section: "架构设计", Slug: "4"},
	}}
	seed := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Section: "项目概述", Track: models.TrackFoundation},
		{Title: "订单管理", Section: "订单模块", Track: models.TrackBusiness},
		{Title: "库存查询", Section: "库存模块", Track: models.TrackBusiness},
		{Title: "退款流程", Section: "订单模块", Track: models.TrackBusiness},
		{Title: "结算对账", Section: "结算模块", Track: models.TrackBusiness},
		{Title: "会员积分", Section: "会员模块", Track: models.TrackBusiness},
		{Title: "安全与访问控制", Section: "安全与访问控制", Track: models.TrackTechnical, ContentPath: "安全与访问控制.md"},
		{Title: "部署与运维", Section: "部署与运维", Track: models.TrackTechnical, ContentPath: "部署与运维.md"},
		{Title: "测试与质量", Section: "测试与质量", Track: models.TrackTechnical, ContentPath: "测试与质量.md"},
		{Title: "故障排查", Section: "故障排查", Track: models.TrackTechnical, ContentPath: "故障排查.md"},
		{Title: "杂接口", Section: "接口文档", Track: models.TrackTechnical},
	}}
	RebalanceDualRail(wiki, seed, 80)
	joined := strings.Join(titlesOf(wiki), ",")
	// Business soft-merge should happen.
	biz := 0
	for _, p := range wiki.Pages {
		if p.Track == models.TrackBusiness {
			biz++
		}
	}
	if biz < 4 {
		t.Fatalf("expected business soft-merge, biz=%d titles=%v", biz, titlesOf(wiki))
	}
	// Technical seed pages must not be injected by rebalance.
	for _, bad := range []string{"安全与访问控制", "部署与运维", "测试与质量", "故障排查", "杂接口"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("technical seed should not soft-merge via rebalance: %v", titlesOf(wiki))
		}
	}
	// Original pages kept
	if !strings.Contains(joined, "订单管理") || !strings.Contains(joined, "整体架构") {
		t.Fatalf("original pages dropped: %v", titlesOf(wiki))
	}
}

func TestFromAllowlistInfersTrack(t *testing.T) {
	w := &models.Wiki{Pages: []models.WikiPage{
		{Title: "整体架构设计", Section: "系统架构设计"},
		{Title: "订单受理", Section: "订单模块"},
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

func TestNoHardcodedProductDomains(t *testing.T) {
	// Ensure source-level product dictionary is gone (compile-time structural check via Build output).
	m := &scan.Model{
		Name: "emptyish", Language: "zh",
		Files: []scan.FileInfo{
			{RelativePath: "README.md", Lines: 10},
			{RelativePath: "main.go", Lines: 20},
		},
	}
	wiki := Build(m, nil, Options{MaxPages: 40})
	joined := strings.Join(titlesOf(wiki), "\n")
	for _, bad := range []string{
		"客户管理模块", "指标监控模块", "内部审计模块", "内部控制模块",
		"知识管理模块", "风险评估模块", "工作流管理", "现金池",
	} {
		if strings.Contains(joined, bad) {
			t.Errorf("hardcoded product domain %q leaked into default tree:\n%s", bad, joined)
		}
	}
}


func TestRailBudgetsSum(t *testing.T) {
	for _, n := range []int{10, 30, 40, 80, 120, 200} {
		f, b, tech := railBudgets(n)
		if f+b+tech != n {
			t.Fatalf("railBudgets(%d)=%d+%d+%d sum=%d", n, f, b, tech, f+b+tech)
		}
		if f < 1 || b < 1 || tech < 1 {
			t.Fatalf("railBudgets(%d) zero rail: f=%d b=%d t=%d", n, f, b, tech)
		}
	}
}

func TestTrimWikiByRailPreservesMix(t *testing.T) {
	// 40 foundation-ish would be wrong; build mixed wiki over max.
	w := &models.Wiki{}
	for i := 0; i < 5; i++ {
		w.Pages = append(w.Pages, models.WikiPage{Title: "F" + string(rune('a'+i)), Section: "项目概述", Track: models.TrackFoundation, Slug: "f" + string(rune('0'+i))})
	}
	for i := 0; i < 20; i++ {
		w.Pages = append(w.Pages, models.WikiPage{Title: "B" + string(rune('a'+i%26)) + string(rune('0'+i/26)), Section: "订单模块", Track: models.TrackBusiness, Slug: "b" + string(rune('0'+i%10)) + string(rune('a'+i/10))})
	}
	for i := 0; i < 30; i++ {
		w.Pages = append(w.Pages, models.WikiPage{Title: "T" + string(rune('a'+i%26)) + string(rune('0'+i/26)), Section: "接口文档", Track: models.TrackTechnical, Slug: "t" + string(rune('0'+i%10)) + string(rune('a'+i/10))})
	}
	TrimWikiByRail(w, 20)
	if len(w.Pages) != 20 {
		t.Fatalf("len=%d want 20", len(w.Pages))
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
	// All three rails should survive a mixed trim.
	if f == 0 || b == 0 || tech == 0 {
		t.Fatalf("rail wiped: f=%d b=%d t=%d pages=%v", f, b, tech, titlesOf(w))
	}
}

func TestDeployNotImpliedByModules(t *testing.T) {
	model := &scan.Model{
		Language: "zh",
		Modules:  []scan.ModuleSummary{{Name: "a", Path: "a"}, {Name: "b", Path: "b"}},
		Files: []scan.FileInfo{
			{RelativePath: "a/Service.java"},
			{RelativePath: "b/Service.java"},
		},
	}
	sig := detectRepoSignals(model)
	if sig.hasDeploy {
		t.Fatal("multi-module alone must not imply deploy")
	}
	if !sig.multiModule {
		t.Fatal("expected multiModule signal")
	}
}

func TestBuildRespectsSmallMaxPages(t *testing.T) {
	model := &scan.Model{
		Language: "zh",
		Name:     "demo",
		Files: []scan.FileInfo{
			{RelativePath: "src/main/java/com/demo/order/OrderController.java"},
			{RelativePath: "src/main/java/com/demo/order/OrderService.java"},
			{RelativePath: "src/main/java/com/demo/order/OrderEntity.java"},
			{RelativePath: "src/main/java/com/demo/billing/BillController.java"},
			{RelativePath: "src/main/resources/application.yml"},
			{RelativePath: "Dockerfile"},
		},
		Modules: []scan.ModuleSummary{{Name: "demo", Path: "demo"}},
	}
	// Force API/data signals via names above.
	w := Build(model, nil, Options{MaxPages: 15})
	if len(w.Pages) > 15 {
		t.Fatalf("pages=%d exceeds MaxPages=15", len(w.Pages))
	}
	if len(w.Pages) == 0 {
		t.Fatal("empty wiki")
	}
}


func TestMergeEngineeringSeeds(t *testing.T) {
	// Thin LLM catalog missing engineering indexes; scan has API/auth/test/deploy signals.
	var files []scan.FileInfo
	for _, p := range []string{
		"app/src/main/java/com/demo/controller/OrderControl.java",
		"app/src/main/java/com/demo/service/OrderService.java",
		"app/src/main/java/com/demo/security/AuthFilter.java",
		"app/src/main/resources/application.yml",
		"app/src/test/java/com/demo/OrderTest.java",
		"deploy/Dockerfile",
		"app/pom.xml",
		"api/pom.xml",
	} {
		files = append(files, scan.FileInfo{RelativePath: p, Lines: 40})
	}
	for i := 0; i < 40; i++ {
		files = append(files, scan.FileInfo{
			RelativePath: "app/src/main/java/com/demo/misc/Misc" + strings.Repeat("X", i%3+1) + ".java",
			Lines:        20,
		})
	}
	m := &scan.Model{
		Name: "demo", Language: "zh", Files: files,
		Modules: []scan.ModuleSummary{{Name: "app", Path: "app"}, {Name: "api", Path: "api"}},
	}
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Section: "项目概述", Track: models.TrackFoundation, Slug: "1"},
		{Title: "订单受理", Section: "订单模块", Track: models.TrackBusiness, ContentPath: "订单模块/订单受理.md", Slug: "2"},
		{Title: "整体架构", Section: "系统架构设计", Track: models.TrackTechnical, Slug: "3"},
	}}
	n := MergeEngineeringSeeds(wiki, m, 120)
	if n < 4 {
		t.Fatalf("expected engineering soft-merge (>=4), got n=%d titles=%v", n, titlesOf(wiki))
	}
	joined := strings.Join(titlesOf(wiki), "\n")
	// At least some signal-gated indexes should appear.
	hits := 0
	for _, w := range []string{"API接口文档", "安全与访问控制", "部署与运维", "测试与质量", "常见问题", "配置管理", "故障排查", "性能与扩展"} {
		if strings.Contains(joined, w) {
			hits++
		}
	}
	if hits < 3 {
		t.Fatalf("expected generic engineering titles, hits=%d titles=\n%s", hits, joined)
	}
	// Idempotent: second merge should add 0 (or very few if trim reshuffled).
	n2 := MergeEngineeringSeeds(wiki, m, 120)
	if n2 > 2 {
		t.Fatalf("second merge should be near-noop, n2=%d", n2)
	}
	// Must not inject product domains.
	for _, bad := range []string{"客户管理模块", "现金池", "指标监控模块"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("product domain leaked: %s", bad)
		}
	}
}

func TestMergeEngineeringSeedsIntoSparseCatalog(t *testing.T) {
	// Free LLM catalog that only planned business pages — eng indexes must still appear.
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Slug: "1", Section: "项目概述", Track: models.TrackFoundation},
		{Title: "订单受理", Slug: "2", Section: "订单模块", Track: models.TrackBusiness},
	}}
	var files []scan.FileInfo
	for _, path := range []string{
		"app/src/main/java/com/demo/controller/order/OrderControl.java",
		"app/src/main/java/com/demo/service/order/OrderService.java",
		"app/src/main/java/com/demo/security/AuthFilter.java",
		"app/src/main/resources/application.yml",
		"app/src/test/java/com/demo/OrderTest.java",
		"deploy/Dockerfile",
		"app/pom.xml",
		"api/pom.xml",
	} {
		files = append(files, scan.FileInfo{RelativePath: path, Lines: 40})
	}
	for i := 0; i < 50; i++ {
		files = append(files, scan.FileInfo{
			RelativePath: "app/src/main/java/com/demo/misc/Misc" + strings.Repeat("X", i%3+1) + ".java",
			Lines:        20,
		})
	}
	m := &scan.Model{
		Name: "demo", Language: "zh", Files: files,
		Modules: []scan.ModuleSummary{{Name: "app", Path: "app"}, {Name: "api", Path: "api"}},
	}
	before := len(wiki.Pages)
	n := MergeEngineeringSeeds(wiki, m, 120)
	if n == 0 {
		t.Fatal("expected engineering seeds to be merged into sparse catalog")
	}
	if len(wiki.Pages) <= before {
		t.Fatalf("pages did not grow: before=%d after=%d n=%d", before, len(wiki.Pages), n)
	}
	joined := strings.Join(titlesOf(wiki), "\n")
	hit := 0
	for _, w := range []string{"API接口文档", "安全与访问控制", "部署与运维", "测试与质量"} {
		if strings.Contains(joined, w) {
			hit++
		}
	}
	if hit < 2 {
		t.Fatalf("expected eng topics after merge, hit=%d titles=\n%s", hit, joined)
	}
	n2 := MergeEngineeringSeeds(wiki, m, 120)
	if n2 != 0 {
		t.Fatalf("second merge should add 0, got %d", n2)
	}
}
