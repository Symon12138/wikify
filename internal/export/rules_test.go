package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JSHurt/wikify/internal/models"
	"github.com/JSHurt/wikify/internal/scan"
)

// ── extraction ────────────────────────────────────────────────────────────────

func TestExtractRuleStatementsZh(t *testing.T) {
	body := strings.Join([]string{
		"# 订单受理",
		"",
		"## 规则",
		"",
		"- 订单金额必须大于 0，见 [check](file://src/order/OrderService.java#L10-L20)。",
		"- 订单流程见 [flow](file://src/order/OrderService.java#L1-L5)。", // cite 无关键词 → 拒
		"密码长度至少 8 位。", // 关键词无 cite → 拒
		"",
		"账户余额不足时不允许支付，参见 [pay](file://src/pay/PayService.java#L30-L40)。其余流程见相关文档。",
		"",
		"| 字段 | 校验 |",
		"| --- | --- |",
		"| 金额 | 必须为正 [amt](file://src/order/OrderService.java#L22-L24) |",
		"",
		"```mermaid",
		"graph TD",
		"  A[超时关闭 file://src/order/Timeout.java] --> B[结束]",
		"```",
		"",
		"<cite>",
		"**参考文献**",
		"- 引用文件必须唯一 [ref](file://src/order/OrderService.java#L1-L80)",
		"</cite>",
		"",
		"- 订单金额必须大于  0，见   [check](file://src/order/OrderService.java#L10-L20)。", // 空白差异 → 去重
	}, "\n")
	got := extractRuleStatements(body, true)
	if len(got) != 3 {
		t.Fatalf("statements=%d want 3:\n%s", len(got), strings.Join(got, "\n"))
	}
	if !strings.Contains(got[0], "必须大于 0") || !strings.Contains(got[0], "file://src/order/OrderService.java#L10-L20") {
		t.Fatalf("got[0]=%q", got[0])
	}
	// 句子切分：只保留规则句，后续叙述句被切掉。
	if !strings.Contains(got[1], "不允许支付") || strings.Contains(got[1], "其余流程") {
		t.Fatalf("sentence split failed: %q", got[1])
	}
	// 表格数据行保留、分隔行排除。
	if !strings.Contains(got[2], "必须为正") {
		t.Fatalf("table data row missing: %q", got[2])
	}
	for _, s := range got {
		if strings.Contains(s, "Timeout.java") || strings.Contains(s, "引用文件必须唯一") {
			t.Fatalf("fence/<cite> content leaked: %q", s)
		}
	}
}

func TestExtractRuleStatementsEnglish(t *testing.T) {
	body := strings.Join([]string{
		"# Orders",
		"",
		"- The order amount must be positive, see [check](file://src/order/OrderService.java#L10-L20).",
		"- Payment flow overview, see [pay](file://src/pay/PayService.java#L1-L9).", // no keyword
		"",
		"Sessions expire after 30 minutes [session](file://src/auth/SessionManager.java#L5-L15).",
	}, "\n")
	got := extractRuleStatements(body, false)
	if len(got) != 2 {
		t.Fatalf("en statements=%d want 2:\n%s", len(got), strings.Join(got, "\n"))
	}
	if !strings.Contains(got[0], "must be positive") || !strings.Contains(got[1], "expire after 30 minutes") {
		t.Fatalf("wrong statements: %q / %q", got[0], got[1])
	}
	// mustard 不应命中 must（词边界）。
	none := extractRuleStatements("- Add mustard sauce, see [c](file://src/food/Sauce.java#L1-L2).", false)
	if len(none) != 0 {
		t.Fatalf("word boundary violated: %v", none)
	}
	// zh 模式下英文关键词不生效。
	zhMode := extractRuleStatements("- The order amount must be positive [check](file://src/order/OrderService.java#L10-L20).", true)
	if len(zhMode) != 0 {
		t.Fatalf("en keywords active in zh mode: %v", zhMode)
	}
}

func TestTruncateRuleText(t *testing.T) {
	// cite 在截断点之前 → 截断保留完整 cite,加省略号。
	keep := "必须校验：[c](file://src/a/Check.java#L1-L2)" + strings.Repeat("说", 300)
	got := truncateRuleText(keep, ruleMaxRunes)
	if got == "" || !strings.HasSuffix(got, "…") || !strings.Contains(got, "(file://src/a/Check.java#L1-L2)") {
		t.Fatalf("keep-case: %q", got)
	}
	if n := len([]rune(got)); n > ruleMaxRunes+1 {
		t.Fatalf("truncated len=%d > %d", n, ruleMaxRunes+1)
	}
	// cite 跨越截断点 → 回退后无完整 cite → 丢弃。
	drop := strings.Repeat("规", 235) + "必须校验 [c](file://src/a/Check.java#L1-L2)。"
	if got := truncateRuleText(drop, ruleMaxRunes); got != "" {
		t.Fatalf("drop-case survived: %q", got)
	}
	// 不超长 → 原样返回。
	short := "必须校验 [c](file://src/a/Check.java#L1-L2)。"
	if got := truncateRuleText(short, ruleMaxRunes); got != short {
		t.Fatalf("short changed: %q", got)
	}
	// 经 extractRuleStatements 的端到端截断/丢弃。
	stmts := extractRuleStatements("- "+keep+"\n- "+drop+"\n", true)
	if len(stmts) != 1 || !strings.HasSuffix(stmts[0], "…") {
		t.Fatalf("extraction truncation: %v", stmts)
	}
}

// ── grouping / caps / threshold (unit level) ──────────────────────────────────

func rulesUnitWiki() (*models.Wiki, map[string]string) {
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Slug: "ov", Section: "项目概述", ContentPath: "项目概述.md",
			Track: models.TrackFoundation},
		{Title: "订单受理", Slug: "o1", Section: "订单模块", ContentPath: "订单模块/订单受理.md",
			Track: models.TrackBusiness},
		{Title: "订单结算", Slug: "o2", Section: "订单模块", ContentPath: "订单模块/订单结算.md",
			Track: models.TrackBusiness},
		{Title: "账户管理", Slug: "a1", Section: "账户模块", ContentPath: "账户模块/账户管理.md",
			Track: models.TrackBusiness},
		{Title: "部署架构", Slug: "t1", Section: "部署", ContentPath: "部署/部署架构.md",
			Track: models.TrackTechnical},
	}}
	contents := map[string]string{
		"ov": "# 项目概述\n\n构建产物必须签名 [b](file://build/Sign.java#L1-L2)。\n", // foundation → 不采集
		"o1": strings.Join([]string{
			"# 订单受理",
			"",
			"## 受理规则",
			"",
			"- 订单金额必须大于 0 [c1](file://app/src/OrderService.java#L10-L12)。",
			"- 单笔订单最多包含 50 个商品 [c2](file://app/src/OrderService.java#L20-L22)。",
			"- 订单超时 30 分钟后自动关闭 [c3](file://app/src/OrderService.java#L30-L32)。",
			"",
		}, "\n"),
		"o2": strings.Join([]string{
			"# 订单结算",
			"",
			"## 结算规则",
			"",
			"- 结算单金额不得为负 [c4](file://app/src/OrderService.java#L40-L42)。",
			"- 订单金额必须大于 0 [c1](file://app/src/OrderService.java#L10-L12)。", // 跨页重复 → 去重
			"",
		}, "\n"),
		"a1": strings.Join([]string{
			"# 账户管理",
			"",
			"## 账户规则",
			"",
			"- 账户名必须唯一 [c5](file://app/src/AccountService.java#L5-L8)。",
			"- 密码长度至少 8 位且必填 [c6](file://app/src/AccountService.java#L15-L18)。",
			"",
		}, "\n"),
		"t1": "# 部署架构\n\n实例数上限为 8 [d](file://deploy/Scale.java#L1-L4)。\n", // technical → 不采集
	}
	return wiki, contents
}

func TestRulesDigestGroupingAndSourceFilter(t *testing.T) {
	wiki, contents := rulesUnitWiki()
	ensureBusinessRulesDigestPage(wiki, contents, true)

	var digest *models.WikiPage
	idx := -1
	for i := range wiki.Pages {
		if strings.HasPrefix(wiki.Pages[i].DescriptionSlug, rulesDigestMarker+"-") {
			digest = &wiki.Pages[i]
			idx = i
		}
	}
	if digest == nil {
		t.Fatal("digest page not synthesized")
	}
	if digest.Title != "业务规则清单" || digest.Section != digest.Title ||
		digest.Track != models.TrackBusiness || digest.ContentPath != "业务规则清单.md" {
		t.Fatalf("digest page=%+v", digest)
	}
	// 插在最后一个 business 页(a1, 原下标3)之后、technical 页之前。
	if idx != 4 || wiki.Pages[5].Slug != "t1" {
		t.Fatalf("digest position=%d (pages=%d)", idx, len(wiki.Pages))
	}
	body := contents[digest.Slug]
	// 7 条候选跨页去重 1 条后剩 6 条：o1 3 条 + o2 1 条 + a1 2 条。
	if n := strings.Count(body, "来源："); n != 6 {
		t.Fatalf("rule lines=%d want 6:\n%s", n, body)
	}
	// 按来源页 Section 分组，顺序=首次出现顺序。
	i1, i2 := strings.Index(body, "## 订单模块"), strings.Index(body, "## 账户模块")
	if i1 < 0 || i2 < 0 || i1 > i2 {
		t.Fatalf("group headings wrong (i1=%d i2=%d):\n%s", i1, i2, body)
	}
	// foundation/technical 轨来源被排除。
	if strings.Contains(body, "Sign.java") || strings.Contains(body, "Scale.java") {
		t.Fatalf("non-business source leaked:\n%s", body)
	}
	// 每条带原句 cite + 来源页链接。
	if !strings.Contains(body, "- 订单金额必须大于 0 [c1](file://app/src/OrderService.java#L10-L12)。 — 来源：[订单受理](订单模块/订单受理.md)") {
		t.Fatalf("entry format wrong:\n%s", body)
	}
	// 跨页重复只保留一次。
	if n := strings.Count(body, "#L10-L12"); n != 1 {
		t.Fatalf("cross-page dup kept %d times:\n%s", n, body)
	}
	// 结构：开篇说明 + 目录 + 目的与范围,并通过 pagelint 硬检查。
	for _, want := range []string{"# 业务规则清单", "## 目录", "## 目的与范围", "**读者**"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q:\n%s", want, body)
		}
	}
	if _, hard, _ := LintPageBody(body); len(hard) != 0 {
		t.Fatalf("pagelint hard issues: %v", hard)
	}
}

func TestRulesDigestPerPageAndTotalCaps(t *testing.T) {
	// 每源页上限 10。
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "订单受理", Slug: "o1", Section: "订单模块", ContentPath: "订单模块/订单受理.md",
			Track: models.TrackBusiness},
	}}
	var b strings.Builder
	b.WriteString("# 订单受理\n\n")
	for i := 1; i <= 12; i++ {
		b.WriteString(rulesFixtureRule("o1", i))
	}
	contents := map[string]string{"o1": b.String()}
	ensureBusinessRulesDigestPage(wiki, contents, true)
	body := digestBodyOf(t, wiki, contents)
	if n := strings.Count(body, "来源："); n != rulesPerPageMax {
		t.Fatalf("per-page cap: rules=%d want %d", n, rulesPerPageMax)
	}

	// 总量上限 200(21 页 × 10 条 = 210 候选 → 第 21 页被截断为 0 条)。
	var pages []models.WikiPage
	contents = map[string]string{}
	for p := 1; p <= 21; p++ {
		slug := rulesFixtureSlug(p)
		pages = append(pages, models.WikiPage{
			Title: "页" + slug, Slug: slug, Section: "域" + slug,
			ContentPath: "域" + slug + "/页" + slug + ".md", Track: models.TrackBusiness,
		})
		var pb strings.Builder
		pb.WriteString("# 页" + slug + "\n\n")
		for i := 1; i <= 10; i++ {
			pb.WriteString(rulesFixtureRule(slug, i))
		}
		contents[slug] = pb.String()
	}
	wiki = &models.Wiki{Pages: pages}
	ensureBusinessRulesDigestPage(wiki, contents, true)
	body = digestBodyOf(t, wiki, contents)
	if n := strings.Count(body, "来源："); n != rulesTotalMax {
		t.Fatalf("total cap: rules=%d want %d", n, rulesTotalMax)
	}
	if strings.Contains(body, "## 域"+rulesFixtureSlug(21)) {
		t.Fatalf("21st page group present despite total cap:\n%s", body[:400])
	}
}

func rulesFixtureSlug(p int) string { return "p" + strings.Repeat("x", p%3) + itoa(p) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// rulesFixtureRule renders one unique generic rule list item.
func rulesFixtureRule(key string, i int) string {
	n := itoa(i)
	return "- 字段 " + key + "-" + n + " 必须小于 " + n + "00 [c](file://app/src/" + key + "/Rule" + n + ".java#L" + n + "-L" + n + ")。\n"
}

func digestBodyOf(t *testing.T, wiki *models.Wiki, contents map[string]string) string {
	t.Helper()
	for _, p := range wiki.Pages {
		if strings.HasPrefix(p.DescriptionSlug, rulesDigestMarker+"-") {
			return contents[p.Slug]
		}
	}
	t.Fatal("digest page not found")
	return ""
}

func TestRulesDigestBelowThresholdNotGenerated(t *testing.T) {
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "订单受理", Slug: "o1", Section: "订单模块", ContentPath: "订单模块/订单受理.md",
			Track: models.TrackBusiness},
	}}
	var b strings.Builder
	b.WriteString("# 订单受理\n\n")
	for i := 1; i <= rulesMinTotal-1; i++ { // 4 条 < 5
		b.WriteString(rulesFixtureRule("o1", i))
	}
	contents := map[string]string{"o1": b.String()}
	before := len(wiki.Pages)
	ensureBusinessRulesDigestPage(wiki, contents, true)
	if len(wiki.Pages) != before || len(contents) != 1 {
		t.Fatalf("digest generated below threshold: pages=%d contents=%d", len(wiki.Pages), len(contents))
	}
}

func TestRulesDigestRespectsAuthoredPage(t *testing.T) {
	wiki, contents := rulesUnitWiki()
	wiki.Pages = append(wiki.Pages, models.WikiPage{
		Title: "业务规则清单", Slug: "authored", Section: "业务规则清单",
		ContentPath: "业务规则清单.md", Track: models.TrackBusiness,
	})
	contents["authored"] = "# 业务规则清单\n\n## 概述\n\n作者撰写的规则清单。\n"
	ensureBusinessRulesDigestPage(wiki, contents, true)
	for _, p := range wiki.Pages {
		if strings.HasPrefix(p.DescriptionSlug, rulesDigestMarker+"-") {
			t.Fatalf("digest synthesized despite authored page: %+v", p)
		}
	}
	if !strings.Contains(contents["authored"], "作者撰写") {
		t.Fatal("authored body overwritten")
	}
}

// ── full Export pipeline ──────────────────────────────────────────────────────

func rulesExportModel() *scan.Model {
	return &scan.Model{
		Name: "demo", Language: "zh", GeneratedAt: "2026-01-01T00:00:00Z",
		Files: []scan.FileInfo{
			{RelativePath: "app/src/OrderService.java", Lines: 80},
			{RelativePath: "app/src/AccountService.java", Lines: 60},
		},
	}
}

func rulesExportWiki() (*models.Wiki, map[string]string) {
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "订单受理", Slug: "o1", Section: "订单模块", ContentPath: "订单模块/订单受理.md",
			Track: models.TrackBusiness, DependentFiles: []string{"app/src/OrderService.java"}},
		{Title: "账户管理", Slug: "a1", Section: "账户模块", ContentPath: "账户模块/账户管理.md",
			Track: models.TrackBusiness, DependentFiles: []string{"app/src/AccountService.java"}},
	}}
	contents := map[string]string{
		"o1": strings.Join([]string{
			"# 订单受理",
			"",
			"## 受理规则",
			"",
			"- 订单金额必须大于 0 [c1](file://app/src/OrderService.java#L10-L12)。",
			"- 单笔订单最多包含 50 个商品 [c2](file://app/src/OrderService.java#L20-L22)。",
			"- 订单超时 30 分钟后自动关闭 [c3](file://app/src/OrderService.java#L30-L32)。",
			"",
			"## 流程说明",
			"",
			"受理入口先做参数检查，再进入状态机流转，最后落库并发出事件通知。",
			"",
		}, "\n"),
		"a1": strings.Join([]string{
			"# 账户管理",
			"",
			"## 账户规则",
			"",
			"- 账户名必须唯一 [c4](file://app/src/AccountService.java#L5-L8)。",
			"- 密码长度至少 8 位且必填 [c5](file://app/src/AccountService.java#L15-L18)。",
			"- 连续 5 次登录失败后账户锁定，默认 15 分钟 [c6](file://app/src/AccountService.java#L25-L28)。",
			"",
			"## 生命周期",
			"",
			"账户从开立、激活、冻结到销户的各状态变化都会记录审计日志供追溯。",
			"",
		}, "\n"),
	}
	return wiki, contents
}

func TestExportRulesDigestWiredIntoArtifacts(t *testing.T) {
	dir := t.TempDir()
	wiki, contents := rulesExportWiki()
	if err := Export(dir, rulesExportModel(), wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	// 内容文件落盘且保留 cite/来源链接/目录。
	raw, err := os.ReadFile(filepath.Join(dir, ".wikify", "content", "业务规则清单.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"# 业务规则清单", "## 目录", "## 目的与范围",
		"file://app/src/OrderService.java#L10-L12",
		"来源：[订单受理](订单模块/订单受理.md)",
		"来源：[账户管理](账户模块/账户管理.md)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("digest file missing %q:\n%s", want, body)
		}
	}
	if _, hard, _ := LintPageBody(body); len(hard) != 0 {
		t.Fatalf("exported digest hard lint issues: %v", hard)
	}
	// meta/wiki.json:业务轨 + marker 保留。
	wraw, _ := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki.json"))
	var saved models.Wiki
	if err := json.Unmarshal(wraw, &saved); err != nil {
		t.Fatal(err)
	}
	var dp *models.WikiPage
	for i := range saved.Pages {
		if strings.HasPrefix(saved.Pages[i].DescriptionSlug, rulesDigestMarker+"-") {
			dp = &saved.Pages[i]
		}
	}
	if dp == nil || dp.Title != "业务规则清单" || dp.Track != models.TrackBusiness {
		t.Fatalf("wiki.json digest=%+v", dp)
	}
	// browse-index / wiki-metadata / search-index 均收录。
	for _, name := range []string{"browse-index.json", "wiki-metadata.json", "search-index.json"} {
		b, rerr := os.ReadFile(filepath.Join(dir, ".wikify", "meta", name))
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		if !strings.Contains(string(b), "业务规则清单") {
			t.Fatalf("%s missing digest entry", name)
		}
	}
}

func TestExportRulesDigestIdempotent(t *testing.T) {
	dir := t.TempDir()
	wiki, contents := rulesExportWiki()
	if err := Export(dir, rulesExportModel(), wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, ".wikify", "content", "业务规则清单.md"))
	if err != nil {
		t.Fatal(err)
	}
	// 第二次 Export 接收被第一次改写过的 wiki/contents(polish 路径)。
	if err := Export(dir, rulesExportModel(), wiki, contents, ExportOptions{Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(dir, ".wikify", "content", "业务规则清单.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("digest not byte-identical across exports:\n--1--\n%s\n--2--\n%s", first, second)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".wikify", "meta", "wiki.json"))
	var saved models.Wiki
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range saved.Pages {
		if strings.HasPrefix(p.DescriptionSlug, rulesDigestMarker+"-") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("digest pages accumulated: %d", n)
	}
}

func TestExportRulesDigestEnglish(t *testing.T) {
	dir := t.TempDir()
	model := rulesExportModel()
	model.Language = "en"
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "Order Intake", Slug: "o1", Section: "Orders", ContentPath: "Orders/Order Intake.md",
			Track: models.TrackBusiness, DependentFiles: []string{"app/src/OrderService.java"}},
	}}
	contents := map[string]string{
		"o1": strings.Join([]string{
			"# Order Intake",
			"",
			"## Rules",
			"",
			"- The order amount must be positive [c1](file://app/src/OrderService.java#L10-L12).",
			"- An order holds at most 50 items [c2](file://app/src/OrderService.java#L20-L22).",
			"- Orders expire after 30 minutes [c3](file://app/src/OrderService.java#L30-L32).",
			"- The account name is unique per tenant [c4](file://app/src/AccountService.java#L5-L8).",
			"- Passwords require at least 8 characters [c5](file://app/src/AccountService.java#L15-L18).",
			"",
			"## Flow",
			"",
			"Intake validates the payload, then walks the state machine and persists.",
			"",
		}, "\n"),
	}
	if err := Export(dir, model, wiki, contents, ExportOptions{Lang: "en"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".wikify", "content", "Business Rules Digest.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"# Business Rules Digest", "## Table of Contents", "## Purpose and Scope",
		"Source: [Order Intake](Orders/Order Intake.md)", "must be positive",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("en digest missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "来源：") {
		t.Fatal("zh label leaked into en digest")
	}
}
