package evidence

import (
	"strings"
	"testing"

	"github.com/JSHurt/wikify/internal/scan"
)

func TestPickDependentFilesHitsController(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "src/main/java/com/demo/PaymentController.java", Lines: 80},
			{RelativePath: "src/main/java/com/demo/PaymentService.java", Lines: 120},
			{RelativePath: "README.md", Lines: 40},
			{RelativePath: "docs/unrelated.txt", Lines: 10},
		},
	}
	out := PickDependentFiles(m, "支付接口", "说明支付相关 API", 5)
	if len(out) == 0 {
		t.Fatal("expected non-empty dependent files")
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "Payment") {
		t.Fatalf("expected Payment* path, got %v", out)
	}
}

func TestPickDependentFilesLimit(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "a/FooController.java", Lines: 10},
			{RelativePath: "b/BarService.java", Lines: 10},
			{RelativePath: "c/BazEntity.java", Lines: 10},
			{RelativePath: "d/Qux.java", Lines: 10},
		},
	}
	out := PickDependentFiles(m, "Foo", "bar", 2)
	if len(out) > 2 {
		t.Fatalf("limit 2, got %d: %v", len(out), out)
	}
}

func TestPickDependentFilesAvoidsUniversalFallback(t *testing.T) {
	// Token-driven discrimination: titles supply domain words, paths supply stems.
	// No product-domain hardcoding required — "客户"→cust synonym + CustBasic path.
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "com.demo.api/pom.xml", Lines: 40},
			{RelativePath: "com.demo.app/pom.xml", Lines: 40},
			{RelativePath: "com.demo.app/src/main/java/com/demo/app/constant/cust/CustConst.java", Lines: 80},
			{RelativePath: "com.demo.app/src/main/java/com/demo/app/controller/cust/CustBasicControl.java", Lines: 200},
			{RelativePath: "com.demo.app/src/main/java/com/demo/app/service/cust/CustBasicService.java", Lines: 150},
			{RelativePath: "com.demo.app/src/main/java/com/demo/app/po/cust/CustBasic.java", Lines: 90},
			{RelativePath: "com.demo.app/src/main/java/com/demo/app/controller/order/OrderControl.java", Lines: 120},
		},
	}
	cust := PickDependentFiles(m, "客户基本信息管理", "说明客户基本信息管理的业务规则与接口", 8)
	if len(cust) == 0 {
		t.Fatal("expected customer deps")
	}
	joined := strings.Join(cust, "\n")
	if strings.Contains(joined, "pom.xml") {
		t.Fatalf("business page should not prefer pom.xml: %v", cust)
	}
	if !strings.Contains(joined, "CustBasic") {
		t.Fatalf("expected CustBasic* evidence, got %v", cust)
	}

	order := PickDependentFiles(m, "订单受理", "说明订单受理流程", 8)
	same := 0
	set := map[string]bool{}
	for _, p := range cust {
		set[p] = true
	}
	for _, p := range order {
		if set[p] {
			same++
		}
	}
	if same == len(cust) && len(cust) > 0 && len(order) > 0 {
		t.Fatalf("customer and order deps are identical (no discrimination):\ncust=%v\norder=%v", cust, order)
	}
}

func TestPickDependentFilesPrefersServiceOverPom(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "com.demo.app/pom.xml", Lines: 40},
			{RelativePath: "com.demo.api/pom.xml", Lines: 40},
			{RelativePath: "com.demo.app/src/main/java/com/demo/app/controller/order/OrderControl.java", Lines: 120},
			{RelativePath: "com.demo.app/src/main/java/com/demo/app/service/order/OrderService.java", Lines: 200},
			{RelativePath: "com.demo.app/src/main/java/com/demo/app/service/order/OrderServiceImpl.java", Lines: 260},
			{RelativePath: "com.demo.app/src/main/java/com/demo/app/po/order/Order.java", Lines: 90},
		},
	}
	out := PickDependentFiles(m, "订单受理管理", "说明订单受理业务规则与服务编排", 6)
	if len(out) == 0 {
		t.Fatal("empty deps")
	}
	joined := strings.Join(out, "\n")
	if strings.Contains(joined, "pom.xml") {
		t.Fatalf("business page must not bind pom.xml: %v", out)
	}
	if !strings.Contains(joined, "OrderService") {
		t.Fatalf("expected OrderService* evidence, got %v", out)
	}
	// ServiceImpl should rank at least as high as controller for business titles.
	svcIdx, ctlIdx := -1, -1
	for i, p := range out {
		if strings.Contains(p, "ServiceImpl") || strings.Contains(p, "OrderService.java") {
			if svcIdx < 0 {
				svcIdx = i
			}
		}
		if strings.Contains(p, "OrderControl") {
			ctlIdx = i
		}
	}
	if svcIdx < 0 {
		t.Fatalf("service missing: %v", out)
	}
	if ctlIdx >= 0 && svcIdx > ctlIdx {
		t.Fatalf("service should rank above controller for business page: %v", out)
	}
}

func TestNoProductLexiconInEvidence(t *testing.T) {
	// Structural guard: product-specific path boosts must stay out of matching.
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "app/src/main/java/com/demo/controller/order/OrderService.java", Lines: 50},
			{RelativePath: "app/src/main/java/com/demo/icebase/IceBase.java", Lines: 40},
			{RelativePath: "app/src/main/java/com/demo/cashpool/CashPool.java", Lines: 40},
		},
	}
	// Title without product words should not specially prefer icebase/cashpool.
	out := PickDependentFiles(m, "订单服务", "说明订单服务实现", 5)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "Order") {
		t.Fatalf("expected Order* evidence, got %v", out)
	}
}

func TestPickDependentFilesGenericArchitecture(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "app/pom.xml", Lines: 20},
			{RelativePath: "app/src/main/java/com/demo/DemoApplication.java", Lines: 40},
			{RelativePath: "app/src/main/java/com/demo/controller/OrderControl.java", Lines: 80},
			{RelativePath: "app/src/main/java/com/demo/service/OrderService.java", Lines: 100},
			{RelativePath: "app/src/main/resources/application.yml", Lines: 30},
		},
	}
	out := PickDependentFiles(m, "模块划分原则", "说明多模块工程结构与组件交互", 6)
	if len(out) == 0 {
		t.Fatal("architecture page must bind some evidence")
	}
}

func TestPickDependentFilesGenericFrontend(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "web/src/main/webapp/app/view/Main.js", Lines: 80},
			{RelativePath: "web/src/main/webapp/resources/css/theme.css", Lines: 40},
			{RelativePath: "app/src/main/java/com/demo/service/OrderService.java", Lines: 100},
			{RelativePath: "app/pom.xml", Lines: 20},
		},
	}
	out := PickDependentFiles(m, "主题风格与静态资源组织", "前端界面架构 / 静态资源", 6)
	if len(out) == 0 {
		t.Fatal("frontend page must bind evidence")
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "webapp") && !strings.Contains(joined, ".js") && !strings.Contains(joined, ".css") {
		t.Fatalf("expected frontend paths, got %v", out)
	}
}

func TestPickDependentFilesGenericFAQAndBuild(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "app/src/main/java/com/demo/controller/ErrorControl.java", Lines: 50},
			{RelativePath: "app/src/main/resources/log4j2.xml", Lines: 20},
			{RelativePath: "app/src/main/resources/application.yml", Lines: 30},
			{RelativePath: "app/pom.xml", Lines: 20},
			{RelativePath: "Dockerfile", Lines: 10},
			{RelativePath: "README.md", Lines: 40},
		},
	}
	faq := PickDependentFiles(m, "常见问题", "环境、构建与运行中的高频问题入口", 6)
	if len(faq) == 0 {
		t.Fatal("FAQ page must bind evidence")
	}
	build := PickDependentFiles(m, "构建与发布", "构建脚本、镜像与发布入口", 6)
	if len(build) == 0 {
		t.Fatal("build page must bind evidence")
	}
}

func TestPickDependentFilesBusinessStillPrefersService(t *testing.T) {
	// Regression: generic role expansion must not make business pages pom-heavy.
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "app/pom.xml", Lines: 40},
			{RelativePath: "app/src/main/java/com/demo/controller/order/OrderControl.java", Lines: 120},
			{RelativePath: "app/src/main/java/com/demo/service/order/OrderService.java", Lines: 200},
			{RelativePath: "app/src/main/java/com/demo/service/order/OrderServiceImpl.java", Lines: 260},
		},
	}
	out := PickDependentFiles(m, "订单受理管理", "说明订单受理业务规则与服务编排", 6)
	joined := strings.Join(out, "\n")
	if strings.Contains(joined, "pom.xml") {
		t.Fatalf("business page must not bind pom: %v", out)
	}
	if !strings.Contains(joined, "OrderService") {
		t.Fatalf("expected service evidence: %v", out)
	}
}

func TestPickDependentFilesRESTControllerIndex(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "app/pom.xml", Lines: 20},
			{RelativePath: "app/src/main/java/com/demo/controller/OrderControl.java", Lines: 80},
			{RelativePath: "app/src/main/java/com/demo/controller/UserController.java", Lines: 90},
			{RelativePath: "app/src/main/java/com/demo/service/OrderService.java", Lines: 100},
		},
	}
	out := PickDependentFiles(m, "核心REST与控制器索引", "REST API 与 Controller 入口索引", 6)
	if len(out) == 0 {
		t.Fatal("REST/controller index must bind evidence")
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "Controller") && !strings.Contains(joined, "Control") {
		t.Fatalf("expected controller paths, got %v", out)
	}
}

func TestPickDependentFilesDraftAttachment(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "app/src/main/java/com/demo/service/OrderService.java", Lines: 100},
			{RelativePath: "web/src/main/webapp/js/help/ueditor/ueditor.config.js", Lines: 40},
			{RelativePath: "web/src/main/webapp/app/base/view/help/HelpDocController.js", Lines: 60},
			{RelativePath: "app/src/main/java/com/demo/controller/AttachmentController.java", Lines: 80},
		},
	}
	out := PickDependentFiles(m, "草稿、编辑记录与附件", "草稿箱、编辑历史与附件上传", 6)
	if len(out) == 0 {
		t.Fatal("draft/attachment page must bind evidence")
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "ueditor") && !strings.Contains(joined, "Help") && !strings.Contains(joined, "Attachment") {
		t.Fatalf("expected draft/help/attachment paths, got %v", out)
	}
}

func TestPickDependentFilesGraphNeighbors(t *testing.T) {
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
	}
	// rebuild adjacency used by ImportNeighbors
	// EnrichGraph not required — ImportNeighbors rebuilds from edges
	out := PickDependentFiles(m, "订单接口", "说明订单 API", 6)
	if len(out) == 0 {
		t.Fatal("empty")
	}
	joined := strings.Join(out, "\n")
	// Should surface service via graph even when title is API-focused
	if !strings.Contains(joined, "OrderService") {
		t.Fatalf("expected graph-boosted OrderService in %v", out)
	}
	if strings.Contains(joined, "Unrelated") {
		t.Fatalf("should not pull unrelated: %v", out)
	}
}

func TestPickDependentFilesDemotesConstantsAndHTML(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "com/demo/service/OrderService.java", Lines: 80},
			{RelativePath: "com/demo/service/OrderServiceImpl.java", Lines: 120},
			{RelativePath: "com/demo/dao/OrderDao.java", Lines: 40},
			{RelativePath: "com/demo/web/OrderController.java", Lines: 50},
			{RelativePath: "com/demo/common/Constants.java", Lines: 200},
			{RelativePath: "com/demo/const/AppConst.java", Lines: 90},
			{RelativePath: "webapp/help/order.html", Lines: 30},
			{RelativePath: "static/css/app.css", Lines: 10},
			{RelativePath: "pom.xml", Lines: 80},
		},
	}
	deps := PickDependentFiles(m, "订单管理服务", "说明订单业务能力与处理流程", 6)
	if len(deps) == 0 {
		t.Fatal("expected deps")
	}
	joined := strings.Join(deps, ",")
	if !strings.Contains(joined, "OrderService") && !strings.Contains(joined, "OrderServiceImpl") {
		t.Fatalf("expected service in deps: %v", deps)
	}
	// Hard noise must never outrank real source in top-4.
	top4 := deps
	if len(top4) > 4 {
		top4 = top4[:4]
	}
	topJoined := strings.Join(top4, ",")
	for _, bad := range []string{"Constants.java", "AppConst.java", "app.css", "pom.xml"} {
		if strings.Contains(topJoined, bad) {
			t.Fatalf("noise %s must not be in top-4 deps: %v", bad, deps)
		}
	}
	// HTML with "order" token may still score, but must not beat service/dao/controller.
	if len(deps) >= 3 {
		first3 := strings.Join(deps[:3], ",")
		if strings.Contains(first3, "order.html") {
			t.Fatalf("html noise must not occupy top-3: %v", deps)
		}
	}
}


func TestPickDependentFilesOverviewDemotesConstHTML(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "web/res/img/button/readme.html", Lines: 5},
			{RelativePath: "app/constant/cust/CustConst.java", Lines: 200},
			{RelativePath: "app/constant/know/KnowConst.java", Lines: 180},
			{RelativePath: "app/controller/order/OrderControl.java", Lines: 120},
			{RelativePath: "app/service/order/OrderService.java", Lines: 200},
			{RelativePath: "app/DemoApplication.java", Lines: 40},
			{RelativePath: "app/pom.xml", Lines: 40},
		},
	}
	// Generic overview titles historically monopolised Const + readme.html.
	out := PickDependentFiles(m, "项目概述与业务定位", "说明平台定位与推荐阅读路径", 6)
	if len(out) == 0 {
		t.Fatal("expected some deps")
	}
	top := out
	if len(top) > 3 {
		top = top[:3]
	}
	topJ := strings.Join(top, ",")
	for _, bad := range []string{"readme.html", "CustConst", "KnowConst"} {
		if strings.Contains(topJ, bad) {
			t.Fatalf("noise %s must not top-3 overview deps: %v", bad, out)
		}
	}
}

func TestPickDependentFilesSymbolNameBoost(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "internal/jobs/runner_a.go", Lines: 120},
			{RelativePath: "internal/jobs/runner_b.go", Lines: 120},
		},
		Symbols: map[string][]scan.Symbol{
			"internal/jobs/runner_a.go": {
				{Name: "Reconcile", Kind: "func", Line: 10},
			},
		},
	}
	out := PickDependentFiles(m, "Reconcile 对账", "对账入口", 5)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "runner_a.go") {
		t.Fatalf("symbol-matching file should be selected: %v", out)
	}
	if strings.Contains(joined, "runner_b.go") {
		t.Fatalf("non-matching sibling must not ride along: %v", out)
	}
}

func TestPickDependentFilesRoutePathBoost(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "web/handlers/h1.go", Lines: 90},
			{RelativePath: "web/handlers/h2.go", Lines: 90},
		},
		Routes: []scan.Route{
			{Method: "GET", Path: "/billing/export", File: "web/handlers/h1.go", Hint: "go-router"},
			{Method: "GET", Path: "/misc/ping", File: "web/handlers/h2.go", Hint: "go-router"},
		},
	}
	out := PickDependentFiles(m, "billing 导出", "账单导出流程", 5)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "h1.go") {
		t.Fatalf("route-owning file should be selected: %v", out)
	}
	if strings.Contains(joined, "h2.go") {
		t.Fatalf("unrelated route file must not ride along: %v", out)
	}
}

func TestPickDependentFilesConfigKeyBoost(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "config/settings.yml", Lines: 40},
			{RelativePath: "config/other.yml", Lines: 40},
		},
		ConfigKeys: map[string][]string{
			"config/settings.yml": {"redis", "kafka"},
			"config/other.yml":    {"logging"},
		},
	}
	out := PickDependentFiles(m, "redis 配置", "redis 连接配置说明", 5)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "settings.yml") {
		t.Fatalf("key-matching config should be selected: %v", out)
	}
	idxA := strings.Index(joined, "settings.yml")
	idxB := strings.Index(joined, "other.yml")
	if idxB >= 0 && idxB < idxA {
		t.Fatalf("key match must outrank keyless config: %v", out)
	}
}
