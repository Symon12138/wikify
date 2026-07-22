// Package planner builds a multi-level document tree seed from repository inventory.
package planner

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/symon/wikify/internal/models"
	"github.com/symon/wikify/internal/scan"
	"github.com/symon/wikify/internal/wikiplan"
)

// Planned is one planned wiki page before LLM generation.
type Planned struct {
	Title       string
	Parent      string
	Section     string
	Group       string
	Goal        string
	Category    string
	Level       string
	Track       string // foundation | business | technical
	ContentPath string // relative under content/, e.g. 系统架构设计/整体架构设计.md
}

// Options controls planning size.
type Options struct {
	MaxPages int
	// PreferDeterministic keeps planner seed as the primary catalog input.
	PreferDeterministic bool
	// InventoryRatio caps code-inventory pages as a fraction of MaxPages (default 0.20).
	// Business-domain pages from path clustering are NOT counted against this cap.
	InventoryRatio float64
}

// Build constructs a dual-rail wiki catalog from scan model + optional allowlist.
// Rails: foundation (onboarding) + business (capabilities) + technical (arch/API/data/ops).
// Budget default (~MaxPages): foundation ~10%, business ~50%, technical ~40% (inventory inside technical).
func Build(model *scan.Model, plan *wikiplan.Plan, opts Options) *models.Wiki {
	if opts.MaxPages <= 0 {
		opts.MaxPages = 120
	}
	if opts.InventoryRatio <= 0 {
		opts.InventoryRatio = 0.20
	}
	zh := model != nil && model.Language == "zh"

	// Allowlist from wiki_plan.wiki.documents takes priority when non-empty.
	if docs := plan.Documents(); len(docs) > 0 {
		return fromAllowlist(docs, opts.MaxPages)
	}

	foundation, technicalBase := buildDefaultTreeSplit(model, zh)
	business := buildBusinessDomains(model, zh)

	// Assign tracks explicitly (idempotent).
	for i := range foundation {
		foundation[i].Track = models.TrackFoundation
	}
	for i := range business {
		business[i].Track = models.TrackBusiness
	}
	for i := range technicalBase {
		technicalBase[i].Track = models.TrackTechnical
	}

	// Dual-rail budgets
	bizBudget := int(float64(opts.MaxPages) * 0.50)
	if bizBudget < 12 {
		bizBudget = 12
	}
	techBudget := int(float64(opts.MaxPages) * 0.40)
	if techBudget < 16 {
		techBudget = 16
	}
	foundBudget := opts.MaxPages - bizBudget - techBudget
	if foundBudget < 4 {
		foundBudget = 4
	}

	if len(foundation) > foundBudget {
		foundation = foundation[:foundBudget]
	}
	if len(business) > bizBudget {
		business = business[:bizBudget]
	}

	// Inventory lives on the technical rail and shares techBudget with skeleton tech pages.
	techRemain := techBudget - len(technicalBase)
	if techRemain < 0 {
		techRemain = 0
	}
	invCap := int(float64(opts.MaxPages) * opts.InventoryRatio)
	if invCap > techRemain {
		invCap = techRemain
	}
	if invCap < 0 {
		invCap = 0
	}
	inv := buildInventory(model, zh, invCap)
	for i := range inv {
		inv[i].Track = models.TrackTechnical
	}
	technical := mergeUnique(technicalBase, inv)
	if len(technical) > techBudget {
		technical = technical[:techBudget]
	}

	// Order: foundation → business → technical (browse-friendly dual rail).
	items := mergeUnique(foundation, business)
	items = mergeUnique(items, technical)
	if len(items) > opts.MaxPages {
		items = items[:opts.MaxPages]
	}
	for i := range items {
		if items[i].Track == "" {
			items[i].Track = trackForCategory(items[i].Category, items[i].Section)
		}
	}
	return toWiki(items)
}

func trackForCategory(cat, section string) string {
	switch cat {
	case "overview", "getting-started":
		return models.TrackFoundation
	case "business":
		return models.TrackBusiness
	case "architecture", "api", "data", "guide", "config", "security", "ops", "modules":
		return models.TrackTechnical
	}
	// Align with models.InferTrack for free LLM catalogs / hierarchy path fill-in.
	return models.InferTrack(models.WikiPage{Section: section})
}

// EnsureTracks fills empty/invalid Track on every page via models.InferTrack.
func EnsureTracks(wiki *models.Wiki) {
	if wiki == nil {
		return
	}
	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		tr := p.Track
		if tr != models.TrackFoundation && tr != models.TrackBusiness && tr != models.TrackTechnical {
			p.Track = models.InferTrack(*p)
		}
	}
}

// RebalanceDualRail ensures Track coverage and soft-merges foundation/business
// pages from seed when the free LLM catalog is thin on those rails.
// Does not rewrite LLM titles; respects maxPages (0 = no cap beyond merge size).
func RebalanceDualRail(wiki, seed *models.Wiki, maxPages int) {
	if wiki == nil {
		return
	}
	EnsureTracks(wiki)
	if seed == nil || len(seed.Pages) == 0 {
		return
	}
	EnsureTracks(seed)

	count := func(tr string) int {
		n := 0
		for _, p := range wiki.Pages {
			if p.Track == tr {
				n++
			}
		}
		return n
	}
	bizN := count(models.TrackBusiness)
	foundN := count(models.TrackFoundation)

	// Soft floors: keep dual-rail readable after free planning.
	wantBiz := 4
	wantFound := 1
	if maxPages > 0 {
		// Scale floors lightly with budget.
		if maxPages >= 80 && wantBiz < 8 {
			wantBiz = 8
		}
	}

	need := map[string]int{}
	if bizN < wantBiz {
		need[models.TrackBusiness] = wantBiz - bizN
	}
	if foundN < wantFound {
		need[models.TrackFoundation] = wantFound - foundN
	}
	if len(need) == 0 {
		return
	}

	seen := map[string]bool{}
	for _, p := range wiki.Pages {
		key := strings.ToLower(strings.TrimSpace(p.Title))
		seen[key] = true
		if p.ContentPath != "" {
			seen["path:"+p.ContentPath] = true
		}
	}

	var add []models.WikiPage
	for _, p := range seed.Pages {
		want, ok := need[p.Track]
		if !ok || want <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(p.Title))
		if seen[key] || (p.ContentPath != "" && seen["path:"+p.ContentPath]) {
			continue
		}
		// Skip inventory-style seed pages on technical rail even if mis-tagged.
		if p.Track == models.TrackTechnical {
			continue
		}
		add = append(add, p)
		seen[key] = true
		need[p.Track]--
	}
	if len(add) == 0 {
		return
	}
	wiki.Pages = append(wiki.Pages, add...)
	if maxPages > 0 && len(wiki.Pages) > maxPages {
		// Prefer keeping original LLM pages first; trim from end of soft-adds.
		// If already over before adds, leave earlier soft cap to caller.
		wiki.Pages = wiki.Pages[:maxPages]
	}
	// Re-number slugs for uniqueness after merge.
	for i := range wiki.Pages {
		if wiki.Pages[i].Slug == "" {
			wiki.Pages[i].Slug = models.MakeSlug(i+1, wiki.Pages[i].Title)
		}
	}
	EnsureTracks(wiki)
}

// FormatSeedXML renders planned pages as catalog XML seed for the catalog agent.
func FormatSeedXML(wiki *models.Wiki) string {
	if wiki == nil || len(wiki.Pages) == 0 {
		return ""
	}
	var b strings.Builder
	curSec := ""
	curGroup := ""
	openGroup := false
	for _, p := range wiki.Pages {
		if p.Section != curSec {
			if openGroup {
				b.WriteString("</group>\n")
				openGroup = false
			}
			if curSec != "" {
				b.WriteString("</section>\n")
			}
			curSec = p.Section
			curGroup = ""
			b.WriteString("<section>\n")
			b.WriteString(p.Section)
			b.WriteString("\n")
		}
		if p.Group != "" {
			if p.Group != curGroup {
				if openGroup {
					b.WriteString("</group>\n")
				}
				curGroup = p.Group
				b.WriteString("<group>\n")
				b.WriteString(p.Group)
				b.WriteString("\n")
				openGroup = true
			}
		} else if openGroup {
			b.WriteString("</group>\n")
			openGroup = false
			curGroup = ""
		}
		level := p.Level
		if level == "" {
			level = "Beginner"
		}
		b.WriteString(`<topic level="`)
		b.WriteString(level)
		b.WriteString(`">`)
		b.WriteString(p.Title)
		b.WriteString("</topic>\n")
	}
	if openGroup {
		b.WriteString("</group>\n")
	}
	if curSec != "" {
		b.WriteString("</section>\n")
	}
	return b.String()
}

func fromAllowlist(docs []wikiplan.Document, max int) *models.Wiki {
	items := make([]Planned, 0, len(docs))
	for _, d := range docs {
		sec := d.Parent
		if sec == "" {
			sec = "Get Started"
		}
		items = append(items, Planned{
			Title:       d.Title,
			Parent:      d.Parent,
			Section:     sec,
			Goal:        d.Goal,
			Level:       "Intermediate",
			Track:       models.InferTrack(models.WikiPage{Title: d.Title, Section: sec, Goal: d.Goal}),
			ContentPath: safeSegment(d.Title) + ".md",
		})
		if len(items) >= max {
			break
		}
	}
	return toWiki(items)
}

func toWiki(items []Planned) *models.Wiki {
	wiki := &models.Wiki{}
	for i, it := range items {
		level := it.Level
		if level == "" {
			level = "Intermediate"
		}
		track := it.Track
		if track == "" {
			track = trackForCategory(it.Category, it.Section)
		}
		page := models.WikiPage{
			Title:           it.Title,
			Slug:            models.MakeSlug(i+1, it.Title),
			Level:           level,
			Section:         it.Section,
			Group:           it.Group,
			Parent:          it.Parent,
			Goal:            it.Goal,
			ContentPath:     it.ContentPath,
			DescriptionSlug: DescriptionSlug(it.Title),
			Track:           track,
		}
		wiki.Pages = append(wiki.Pages, page)
	}
	return wiki
}

type taxonomy struct {
	modules, architecture, api, data, ops, troubleshooting string
}

func chooseTaxonomy(model *scan.Model, zh bool) taxonomy {
	joined := ""
	if model != nil {
		for _, f := range model.Files {
			joined += f.RelativePath + "\n"
		}
	}
	businessHeavy := match(joined, `credit|loan|bill|pay|order|invoice|scf|finance|account|cash|risk|cust|kri|ice|audit|grc|授信|贷款|票据|支付|账款|风控|客户|审计`)
	dbHeavy := match(joined, `sql|mapper|dao|entity|schema|migration|mybatis|hibernate|jpa`)
	domainHeavy := match(joined, `entity|domain|dto|model|vo`)
	javaWeb := match(joined, `servlet|spring|controller|web\.xml|dispatcher-servlet`)
	multiModule := match(joined, `dfap|bsp|microservice|ncb-ilp`) || (model != nil && len(model.Modules) >= 6)
	monolithBusiness := zh && businessHeavy && javaWeb && !multiModule

	if !zh {
		return taxonomy{
			modules: "Core Modules", architecture: "System Architecture", api: "API Documentation",
			data: "Data Model Design", ops: "Deployment and Operations", troubleshooting: "Troubleshooting",
		}
	}
	t := taxonomy{
		modules: "核心模块详解", architecture: "系统架构设计", api: "API接口文档",
		data: "数据模型设计", ops: "部署与运维", troubleshooting: "故障排查",
	}
	if monolithBusiness {
		t.modules = "核心业务模块"
		t.architecture = "架构设计"
		t.api = "接口文档"
		t.data = "数据库设计"
		t.troubleshooting = "故障排查与维护"
	} else if javaWeb && !multiModule {
		t.architecture = "架构设计"
		t.api = "接口文档"
	}
	if (dbHeavy && !domainHeavy) || monolithBusiness {
		t.data = "数据库设计"
	}
	return t
}

func buildDefaultTree(model *scan.Model, zh bool) []Planned {
	tax := chooseTaxonomy(model, zh)
	var out []Planned
	push := func(p Planned) {
		if p.Title == "" {
			return
		}
		for _, e := range out {
			if e.Title == p.Title {
				return
			}
		}
		if p.Level == "" {
			p.Level = "Beginner"
		}
		out = append(out, p)
	}

	if zh {
		push(Planned{Title: "项目概述", Section: "项目概述", Category: "overview", Goal: "描述项目定位、核心价值、模块边界与目标用户。", ContentPath: "项目概述.md", Level: "Beginner"})
		push(Planned{Title: "快速开始", Section: "快速开始", Category: "getting-started", Goal: "说明环境准备、安装构建与最小可运行步骤。", ContentPath: "快速开始.md", Level: "Beginner"})
	} else {
		push(Planned{Title: "Project Overview", Section: "Project Overview", Category: "overview", Goal: "Describe project positioning and value.", ContentPath: "project-overview.md", Level: "Beginner"})
		push(Planned{Title: "Getting Started", Section: "Getting Started", Category: "getting-started", Goal: "Explain setup and minimal run steps.", ContentPath: "getting-started.md", Level: "Beginner"})
	}

	// Architecture section + children
	arch := tax.architecture
	push(Planned{Title: arch, Section: arch, Category: "architecture", Goal: goal(zh, "给出系统整体架构、分层关系与关键路径。", "Overall architecture and layering."), ContentPath: arch + "/" + arch + ".md", Level: "Intermediate"})
	archKids := []string{"整体架构设计", "模块划分原则", "数据流架构", "组件交互关系", "分层架构设计", "部署架构"}
	if !zh {
		archKids = []string{"Overall Architecture", "Module Boundaries", "Data Flow Architecture", "Component Interactions", "Layered Architecture", "Deployment Architecture"}
	}
	for _, k := range archKids {
		push(Planned{Title: k, Section: arch, Group: arch, Parent: arch, Category: "architecture", Goal: k, ContentPath: arch + "/" + k + ".md", Level: "Intermediate"})
	}

	// Modules (index only — domain pages come from buildBusinessDomains)
	mod := tax.modules
	push(Planned{Title: mod, Section: mod, Category: "modules", Goal: goal(zh, "索引核心业务/技术模块及其职责。", "Index core modules."), ContentPath: mod + "/" + mod + ".md", Level: "Intermediate"})
	if model != nil {
		// Prefer humanized module pages only for top-level multi-module layouts (few pages).
		for i, m := range model.Modules {
			if i >= 8 {
				break
			}
			title := composeChineseTitle(m.Name, "module")
			if !zh {
				title = humanize(m.Name)
			}
			if isLowQuality(title) {
				title = m.Name
			}
			// Skip trivial single-word technical shells that mirror package noise.
			if match(title, `^(模块|Common|App|Api|Web|Core)(模块)?$`) {
				continue
			}
			safe := safeSegment(title)
			push(Planned{
				Title: title, Section: mod, Group: mod, Parent: mod, Category: "modules",
				Goal:        goal(zh, "说明模块 "+m.Path+" 的职责与依赖。", "Explain module "+m.Path),
				ContentPath: mod + "/" + safe + "/" + safe + ".md", Level: "Advanced",
			})
		}
	}

	// Dev guide / API index / data index / security / ops / troubleshooting
	if zh {
		push(Planned{Title: "开发指南", Section: "开发指南", Category: "guide", Goal: "开发规范、目录与技术栈。", ContentPath: "开发指南/开发指南.md", Level: "Beginner"})
		push(Planned{Title: "目录结构", Section: "开发指南", Group: "开发指南", Parent: "开发指南", Goal: "说明仓库目录布局。", ContentPath: "开发指南/目录结构.md", Level: "Beginner"})
		push(Planned{Title: "技术栈与依赖", Section: "开发指南", Group: "开发指南", Parent: "开发指南", Goal: "列出主要依赖与运行时。", ContentPath: "开发指南/技术栈与依赖.md", Level: "Beginner"})
		// API / data stay as index pages; detailed inventory is budget-capped elsewhere.
		push(Planned{Title: tax.api, Section: tax.api, Category: "api", Goal: "汇总对外接口能力与调用约定（按业务主题索引，避免逐类罗列）。", ContentPath: tax.api + "/" + tax.api + ".md", Level: "Intermediate"})
		push(Planned{Title: "接口设计原则", Section: tax.api, Group: tax.api, Parent: tax.api, Category: "api", Goal: "统一说明鉴权、分页、错误码与版本策略。", ContentPath: tax.api + "/接口设计原则.md", Level: "Intermediate"})
		push(Planned{Title: tax.data, Section: tax.data, Category: "data", Goal: "数据模型与持久化总览（按领域聚合，避免逐表罗列）。", ContentPath: tax.data + "/" + tax.data + ".md", Level: "Intermediate"})
		push(Planned{Title: "配置管理", Section: "配置管理", Category: "config", Goal: "配置项与环境差异。", ContentPath: "配置管理.md", Level: "Intermediate"})
		push(Planned{Title: "安全设计", Section: "安全设计", Category: "security", Goal: "认证授权与安全边界。", ContentPath: "安全设计.md", Level: "Advanced"})
		push(Planned{Title: tax.ops, Section: tax.ops, Category: "ops", Goal: "部署与运维要点。", ContentPath: tax.ops + ".md", Level: "Intermediate"})
		push(Planned{Title: tax.troubleshooting, Section: tax.troubleshooting, Category: "ops", Goal: "常见故障与排查。", ContentPath: tax.troubleshooting + ".md", Level: "Intermediate"})
	} else {
		push(Planned{Title: "Developer Guide", Section: "Developer Guide", Category: "guide", Goal: "Coding conventions and layout.", ContentPath: "developer-guide/developer-guide.md", Level: "Beginner"})
		push(Planned{Title: tax.api, Section: tax.api, Category: "api", Goal: "API surface overview (business-indexed).", ContentPath: "api-documentation/api-documentation.md", Level: "Intermediate"})
		push(Planned{Title: tax.data, Section: tax.data, Category: "data", Goal: "Data models overview.", ContentPath: "data-model-design/data-model-design.md", Level: "Intermediate"})
		push(Planned{Title: "Security Design", Section: "Security Design", Category: "security", Goal: "Auth and security.", ContentPath: "security-design.md", Level: "Advanced"})
		push(Planned{Title: tax.ops, Section: tax.ops, Category: "ops", Goal: "Deploy and ops.", ContentPath: "deployment-and-operations.md", Level: "Intermediate"})
		push(Planned{Title: tax.troubleshooting, Section: tax.troubleshooting, Category: "ops", Goal: "Troubleshooting.", ContentPath: "troubleshooting.md", Level: "Intermediate"})
	}
	return out
}

// buildDefaultTreeSplit returns foundation pages and technical skeleton separately
// so dual-rail budgets can be applied independently.
func buildDefaultTreeSplit(model *scan.Model, zh bool) (foundation, technical []Planned) {
	all := buildDefaultTree(model, zh)
	for _, p := range all {
		switch p.Category {
		case "overview", "getting-started":
			p.Track = models.TrackFoundation
			foundation = append(foundation, p)
		default:
			p.Track = models.TrackTechnical
			technical = append(technical, p)
		}
	}
	return foundation, technical
}

// fileBag is a scanned file tagged with its class/file stem.
type fileBag struct {
	f    scan.FileInfo
	stem string
}

// domainDef maps package/path tokens to a business section and capability titles.
type domainDef struct {
	keys       []string // path tokens (case-insensitive)
	sectionZH  string
	sectionEN  string
	overviewZH string
	overviewEN string
	// capability stems → human titles (matched against class stems under the domain)
	caps map[string]string
}

var knownDomains = []domainDef{
	{
		keys:      []string{"cust", "customer", "client", "crm"},
		sectionZH: "客户管理模块", sectionEN: "Customer Management",
		overviewZH: "客户管理模块", overviewEN: "Customer Management Overview",
		caps: map[string]string{
			"basic": "客户基本信息管理", "entity": "客户实体与导入功能", "fin": "客户财务数据管理",
			"follow": "客户跟进与限额管理", "contract": "客户合同与担保管理", "import": "客户实体与导入功能",
			"debt": "客户财务数据管理", "benefit": "客户财务数据管理", "cash": "客户财务数据管理",
		},
	},
	{
		keys:      []string{"kri", "kpi", "quota", "indicator", "metric"},
		sectionZH: "指标监控模块", sectionEN: "Indicator Monitoring",
		overviewZH: "KRI与指标监控", overviewEN: "KRI and Indicator Monitoring",
		caps: map[string]string{
			"element": "KRI要素填报", "quota": "指标值管理", "value": "指标值管理",
			"flow": "指标审批工作流", "his": "指标历史数据",
		},
	},
	{
		keys:      []string{"ice", "icebase", "control", "internalcontrol"},
		sectionZH: "内部控制模块", sectionEN: "Internal Control",
		overviewZH: "ICE模型概述", overviewEN: "ICE Model Overview",
		caps: map[string]string{
			"base": "ICE基础配置", "eval": "ICE评价与缺陷", "defect": "ICE评价与缺陷",
			"matrix": "ICE矩阵与映射", "test": "ICE测试执行",
		},
	},
	{
		keys:      []string{"audit", "iir", "internalaudit"},
		sectionZH: "内部审计模块", sectionEN: "Internal Audit",
		overviewZH: "内部审计模块", overviewEN: "Internal Audit Overview",
		caps: map[string]string{
			"factor": "审计因子管理", "plan": "审计计划管理", "project": "审计项目管理",
			"find": "审计发现与整改", "report": "审计报告", "syn": "审计因子同步",
		},
	},
	{
		keys:      []string{"know", "knowledge", "doc", "document"},
		sectionZH: "知识管理模块", sectionEN: "Knowledge Management",
		overviewZH: "知识管理模块", overviewEN: "Knowledge Management Overview",
		caps: map[string]string{
			"doc": "文档管理系统", "file": "文档管理系统", "catalog": "知识目录与分类",
			"share": "知识共享与权限",
		},
	},
	{
		keys:      []string{"risk", "rcsa", "assess"},
		sectionZH: "风险评估模块", sectionEN: "Risk Assessment",
		overviewZH: "风险评估模块", overviewEN: "Risk Assessment Overview",
		caps: map[string]string{
			"assess": "风险评估流程", "matrix": "风险矩阵", "loss": "损失数据管理",
		},
	},
	{
		keys:      []string{"flow", "workflow", "bpm", "activiti", "camunda"},
		sectionZH: "工作流管理", sectionEN: "Workflow Management",
		overviewZH: "工作流管理", overviewEN: "Workflow Management Overview",
		caps: map[string]string{
			"task": "流程任务处理", "def": "流程定义与配置", "his": "流程历史",
		},
	},
	{
		keys:      []string{"auth", "security", "login", "permission", "role"},
		sectionZH: "安全架构设计", sectionEN: "Security Architecture",
		overviewZH: "安全架构设计", overviewEN: "Security Architecture Overview",
		caps: map[string]string{
			"login": "认证与登录", "role": "角色与权限", "filter": "安全过滤链",
		},
	},
	{
		keys:      []string{"ops", "monitor", "quartz", "schedule", "job"},
		sectionZH: "运营支持模块", sectionEN: "Operations Support",
		overviewZH: "运营支持模块", overviewEN: "Operations Support Overview",
		caps: map[string]string{
			"job": "定时任务与批处理", "log": "日志与审计轨迹", "monitor": "运行监控",
		},
	},
}

// buildBusinessDomains clusters code under known business path tokens into
// Qoder-style multi-level sections (e.g. 客户管理模块/客户基本信息管理).
func buildBusinessDomains(model *scan.Model, zh bool) []Planned {
	if model == nil {
		return nil
	}
	// Collect files per domain key.
	byDomain := map[int][]fileBag{}
	for _, f := range model.Files {
		if !scan.IsCodeFile(f.RelativePath) || scan.IsNoisePath(f.RelativePath) {
			continue
		}
		lower := strings.ToLower(filepath.ToSlash(f.RelativePath))
		stem := strings.TrimSuffix(filepath.Base(f.RelativePath), filepath.Ext(f.RelativePath))
		for di, d := range knownDomains {
			for _, k := range d.keys {
				// Match path segment or package folder, not bare substring in "controller".
				if match(lower, `/`+regexp.QuoteMeta(k)+`(/|$)`) ||
					match(lower, `\.`+regexp.QuoteMeta(k)+`\.`) ||
					match(strings.ToLower(stem), `^`+regexp.QuoteMeta(k)) {
					byDomain[di] = append(byDomain[di], fileBag{f, stem})
					goto nextFile
				}
			}
		}
	nextFile:
	}

	var out []Planned
	seenTitle := map[string]bool{}
	add := func(p Planned) {
		if p.Title == "" || seenTitle[p.Title] || isLowQuality(p.Title) {
			return
		}
		seenTitle[p.Title] = true
		out = append(out, p)
	}

	// Sort domain indices by file count desc for stable priority.
	type dk struct {
		i, n int
	}
	var order []dk
	for i, files := range byDomain {
		if len(files) < 3 {
			continue
		}
		order = append(order, dk{i, len(files)})
	}
	sort.Slice(order, func(a, b int) bool { return order[a].n > order[b].n })
	if len(order) > 10 {
		order = order[:10]
	}

	for _, o := range order {
		d := knownDomains[o.i]
		files := byDomain[o.i]
		sec := d.sectionZH
		overview := d.overviewZH
		if !zh {
			sec = d.sectionEN
			overview = d.overviewEN
		}
		// Section overview page
		add(Planned{
			Title: overview, Section: sec, Category: "business",
			Goal: goal(zh,
				"从业务视角说明「"+sec+"」的能力边界、核心对象与主要流程，并索引子主题。",
				"Business overview of "+sec+" capabilities and main flows."),
			ContentPath: sec + "/" + safeSegment(overview) + ".md",
			Level:       "Intermediate",
		})

		// Capability pages: match stems against cap keywords; also group by stem prefix.
		capHits := map[string]int{} // title → hit count
		for _, fb := range files {
			stemL := strings.ToLower(fb.stem)
			// strip technical suffixes before matching
			clean := regexp.MustCompile(`(?i)(controller|control|comp|serviceimpl|service|dao|mapper|model|entity|po|dto|vo|impl)$`).ReplaceAllString(stemL, "")
			for key, title := range d.caps {
				if strings.Contains(clean, key) || strings.Contains(stemL, key) {
					if !zh {
						title = humanize(key) + " Capability"
					}
					capHits[title]++
				}
			}
		}
		// Rank capabilities by hits
		type ch struct {
			title string
			n     int
		}
		var caps []ch
		for t, n := range capHits {
			if n >= 1 {
				caps = append(caps, ch{t, n})
			}
		}
		sort.Slice(caps, func(i, j int) bool { return caps[i].n > caps[j].n })
		if len(caps) > 6 {
			caps = caps[:6]
		}
		for _, c := range caps {
			if c.title == overview {
				continue
			}
			safe := safeSegment(c.title)
			add(Planned{
				Title: c.title, Section: sec, Group: sec, Parent: overview, Category: "business",
				Goal: goal(zh,
					"说明「"+c.title+"」的业务规则、主要接口与数据对象，并给出调用与排查要点。",
					"Explain "+c.title+" rules, APIs and data objects."),
				ContentPath: sec + "/" + safe + ".md",
				Level:       "Advanced",
			})
		}

		// If no cap matched, synthesise 1–2 pages from dominant class stems.
		if len(caps) == 0 {
			stems := dominantStemsFrom(files, 3)
			for _, st := range stems {
				title := composeChineseTitle(st, "capability")
				if !zh {
					title = humanize(st)
				}
				if title == overview || isLowQuality(title) {
					continue
				}
				safe := safeSegment(title)
				add(Planned{
					Title: title, Section: sec, Group: sec, Parent: overview, Category: "business",
					Goal:        goal(zh, "说明「"+title+"」相关业务能力。", "Explain "+title),
					ContentPath: sec + "/" + safe + ".md",
					Level:       "Advanced",
				})
			}
		}
	}
	return out
}

func dominantStemsFrom(files []fileBag, limit int) []string {
	counts := map[string]int{}
	for _, f := range files {
		// Group by leading camel tokens (CustBasicControl → CustBasic)
		clean := regexp.MustCompile(`(?i)(Controller|Control|Comp|ServiceImpl|Service|Dao|Mapper|Model|Entity|Po|Dto|Vo|Impl)$`).ReplaceAllString(f.stem, "")
		if clean == "" || len(clean) < 3 {
			continue
		}
		parts := splitIdent(clean)
		key := clean
		if len(parts) >= 2 {
			key = parts[0] + parts[1]
		} else if len(parts) == 1 {
			key = parts[0]
		}
		counts[key]++
	}
	type kv struct {
		k string
		n int
	}
	var list []kv
	for k, n := range counts {
		if n >= 1 {
			list = append(list, kv{k, n})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	var out []string
	for _, e := range list {
		out = append(out, e.k)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// buildInventory adds a small number of API/entity reference pages (hard budget).
// Prefer aggregated themes over one-page-per-class.
func buildInventory(model *scan.Model, zh bool, budget int) []Planned {
	if model == nil || budget <= 0 {
		return nil
	}
	tax := chooseTaxonomy(model, zh)
	var out []Planned
	seen := map[string]bool{}
	add := func(p Planned) {
		if p.Title == "" || seen[p.Title] || isLowQuality(p.Title) {
			return
		}
		if len(out) >= budget {
			return
		}
		seen[p.Title] = true
		out = append(out, p)
	}

	// Aggregate API files by package segment (not per controller).
	segAPI := map[string][]scan.FileInfo{}
	for _, f := range model.Files {
		if !(scan.IsAPISourceFile(f.RelativePath) || match(f.RelativePath, `(Action|Controller|Resource|Handler)\.`)) {
			continue
		}
		if scan.IsNoisePath(f.RelativePath) {
			continue
		}
		seg := packageDomain(f.RelativePath)
		if seg == "" {
			continue
		}
		// Skip domains already covered as business sections (avoid duplicate noise).
		if domainCovered(seg) {
			continue
		}
		segAPI[seg] = append(segAPI[seg], f)
	}
	type sk struct {
		k string
		n int
	}
	var segs []sk
	for k, v := range segAPI {
		if len(v) >= 2 {
			segs = append(segs, sk{k, len(v)})
		}
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].n > segs[j].n })
	// At most half of inventory budget for API aggregates.
	apiCap := budget / 2
	if apiCap < 2 {
		apiCap = 2
	}
	for i, s := range segs {
		if i >= apiCap {
			break
		}
		title := composeChineseTitle(s.k, "api")
		if !zh {
			title = humanize(s.k) + " APIs"
		}
		// Prefer "…能力接口" style over raw class mirrors.
		safe := safeSegment(title)
		add(Planned{
			Title: title, Section: tax.api, Group: tax.api, Parent: tax.api, Category: "api",
			Goal: goal(zh,
				"按业务聚合说明 "+s.k+" 相关接口族的职责、路径约定与调用链（勿逐方法堆砌）。",
				"Aggregated APIs around "+s.k),
			ContentPath: tax.api + "/" + safe + ".md", Level: "Advanced",
		})
	}

	// Entity aggregates by package segment (remaining budget).
	segEnt := map[string][]scan.FileInfo{}
	for _, f := range model.Files {
		if !match(f.RelativePath, `entity|domain|model|dto|vo|po/`) && !match(f.RelativePath, `/po/|/entity/|/model/`) {
			continue
		}
		if !scan.IsCodeFile(f.RelativePath) || scan.IsNoisePath(f.RelativePath) {
			continue
		}
		stem := strings.TrimSuffix(filepath.Base(f.RelativePath), filepath.Ext(f.RelativePath))
		if match(stem, `^(Base|Abstract|Common|Entity|Model|Dto|Vo|DataEntity)$`) {
			continue
		}
		seg := packageDomain(f.RelativePath)
		if seg == "" || domainCovered(seg) {
			continue
		}
		segEnt[seg] = append(segEnt[seg], f)
	}
	segs = segs[:0]
	for k, v := range segEnt {
		if len(v) >= 2 {
			segs = append(segs, sk{k, len(v)})
		}
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].n > segs[j].n })
	for _, s := range segs {
		title := composeChineseTitle(s.k, "entity")
		if !zh {
			title = humanize(s.k) + " Data Model"
		}
		safe := safeSegment(title)
		add(Planned{
			Title: title, Section: tax.data, Group: tax.data, Parent: tax.data, Category: "data",
			Goal: goal(zh,
				"说明 "+s.k+" 领域的主要数据对象、关系与持久化要点。",
				"Data objects around "+s.k),
			ContentPath: tax.data + "/" + safe + ".md", Level: "Advanced",
		})
	}
	return out
}

func packageDomain(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	// Prefer last meaningful package segment before filename.
	noise := map[string]bool{
		"src": true, "main": true, "java": true, "resources": true, "com": true, "org": true,
		"net": true, "cn": true, "impl": true, "util": true, "utils": true, "common": true,
		"web": true, "controller": true, "service": true, "dao": true, "mapper": true,
		"po": true, "entity": true, "model": true, "dto": true, "vo": true, "api": true,
		"app": true, "test": true,
	}
	for i := len(parts) - 2; i >= 0; i-- {
		p := parts[i]
		pl := strings.ToLower(p)
		if noise[pl] || len(pl) < 2 {
			continue
		}
		// Skip pure version-like segments
		if match(pl, `^v?\d+$`) {
			continue
		}
		return p
	}
	// Fallback: class stem stripped
	base := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	base = regexp.MustCompile(`(?i)(Controller|Control|Service|Dao|Mapper|Model|Entity)$`).ReplaceAllString(base, "")
	if len(base) < 2 {
		return ""
	}
	return base
}

func domainCovered(seg string) bool {
	sl := strings.ToLower(seg)
	for _, d := range knownDomains {
		for _, k := range d.keys {
			if sl == k || strings.HasPrefix(sl, k) || strings.Contains(sl, k) {
				return true
			}
		}
	}
	return false
}

func mergeUnique(a, b []Planned) []Planned {
	seen := map[string]bool{}
	var out []Planned
	for _, items := range [][]Planned{a, b} {
		for _, p := range items {
			if seen[p.Title] {
				continue
			}
			seen[p.Title] = true
			out = append(out, p)
		}
	}
	return out
}

// DescriptionSlug returns kebab-case English slug for metadata description.
func DescriptionSlug(title string) string {
	if s, ok := titleSlugs[title]; ok {
		return s
	}
	// Strip common Chinese suffixes to help ascii fallback
	t := title
	for _, pair := range [][2]string{{"模块", "-module"}, {"服务", "-service"}, {"接口", "-api"}, {"设计", "-design"}, {"文档", "-docs"}, {"管理", "-mgmt"}} {
		t = strings.ReplaceAll(t, pair[0], pair[1])
	}
	var b strings.Builder
	for _, r := range strings.ToLower(t) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == '-' || r == '_' || r == ' ' {
			b.WriteByte('-')
		}
	}
	s := regexp.MustCompile(`-+`).ReplaceAllString(b.String(), "-")
	s = strings.Trim(s, "-")
	if s != "" {
		return s
	}
	// hash fallback
	h := 0
	for _, r := range title {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return "topic-" + strings.ToLower(itohex(h))
}

var titleSlugs = map[string]string{
	"项目概述": "project-overview", "快速开始": "getting-started",
	"系统架构设计": "system-architecture", "整体架构设计": "overall-architecture",
	"模块划分原则": "module-boundaries", "数据流架构": "data-flow-architecture",
	"核心模块详解": "core-modules", "开发指南": "developer-guide",
	"目录结构": "directory-structure", "技术栈与依赖": "tech-stack-and-dependencies",
	"API接口文档": "api-documentation", "配置管理": "configuration-management",
	"安全设计": "security-design", "部署与运维": "deployment-and-operations",
	"故障排查": "troubleshooting", "数据模型设计": "data-model-design",
	"数据库设计": "database-design", "接口文档": "api-docs", "架构设计": "architecture-design",
	"核心业务模块": "core-business-modules", "接口设计原则": "api-design-principles",
	"客户管理模块": "customer-management", "客户基本信息管理": "customer-basic-info",
	"客户实体与导入功能": "customer-entity-import", "客户财务数据管理": "customer-finance",
	"客户跟进与限额管理": "customer-follow-limit", "客户合同与担保管理": "customer-contract",
	"指标监控模块": "indicator-monitoring", "KRI与指标监控": "kri-monitoring",
	"内部审计模块": "internal-audit", "内部控制模块": "internal-control",
	"ICE模型概述": "ice-model-overview", "知识管理模块": "knowledge-management",
	"风险评估模块": "risk-assessment", "工作流管理": "workflow-management",
	"安全架构设计": "security-architecture", "运营支持模块": "operations-support",
	"Project Overview": "project-overview",
	"Getting Started":  "getting-started", "System Architecture": "system-architecture",
	"Core Modules": "core-modules", "API Documentation": "api-documentation",
	"Data Model Design": "data-model-design", "Deployment and Operations": "deployment-and-operations",
	"Troubleshooting": "troubleshooting", "Security Design": "security-design",
	"Developer Guide": "developer-guide",
}

func composeChineseTitle(stem, kind string) string {
	cleaned := regexp.MustCompile(`(?i)(Controller|Control|Comp|Resource|Endpoint|Handler|Servlet|ServiceImpl|Service|Action|Req|Resp|Request|Response|Bean|Util|Helper|Manager|Impl|Dao|Mapper|Model|Entity|Po|Dto|Vo)$`).ReplaceAllString(stem, "")
	tokens := splitIdent(cleaned)
	noise := map[string]bool{
		"service": true, "svc": true, "controller": true, "control": true, "comp": true,
		"handler": true, "resource": true, "action": true, "impl": true, "util": true,
		"helper": true, "manager": true, "common": true, "base": true, "abstract": true,
		"sample": true, "test": true, "ncb": true, "api": true, "web": true, "rest": true,
		"g": true, "dao": true, "mapper": true, "sunyard": true, "com": true,
	}
	var filtered []string
	for _, t := range tokens {
		tl := strings.ToLower(t)
		if noise[tl] || isDigits(tl) {
			continue
		}
		filtered = append(filtered, tl)
	}
	if len(filtered) == 0 {
		filtered = tokens
	}
	phrases := []struct {
		match []string
		title string
	}{
		{[]string{"cash", "pool"}, "现金池"},
		{[]string{"vir", "acc"}, "虚拟账户"},
		{[]string{"virtual", "account"}, "虚拟账户"},
		{[]string{"credit", "apply"}, "信用申请"},
		{[]string{"loan", "apply"}, "贷款申请"},
		{[]string{"loan", "detail"}, "贷款明细"},
		{[]string{"disb", "detail"}, "放款明细"},
		{[]string{"repay", "plan"}, "还款计划"},
		{[]string{"repay", "detail"}, "还款明细"},
		{[]string{"black", "list"}, "黑名单"},
		{[]string{"data", "source"}, "数据源"},
		{[]string{"work", "flow"}, "工作流"},
		{[]string{"cust", "info"}, "客户信息"},
		{[]string{"cust", "basic"}, "客户基本信息"},
		{[]string{"cust", "entity"}, "客户实体"},
		{[]string{"cust", "follow"}, "客户跟进"},
		{[]string{"customer", "info"}, "客户信息"},
		{[]string{"kri", "element"}, "KRI要素"},
		{[]string{"kri", "quota"}, "KRI指标"},
		{[]string{"audit", "factor"}, "审计因子"},
		{[]string{"ice", "base"}, "ICE基础"},
	}
	for _, ph := range phrases {
		ok := true
		for _, m := range ph.match {
			found := false
			for _, t := range filtered {
				if strings.Contains(t, m) || strings.Contains(m, t) {
					found = true
					break
				}
			}
			if !found {
				ok = false
				break
			}
		}
		if ok {
			return finalizeTitle(ph.title, kind)
		}
	}
	dict := map[string]string{
		"pay": "支付", "payment": "支付", "credit": "信贷", "loan": "贷款", "disb": "放款",
		"repay": "还款", "bill": "票据", "risk": "风控", "cust": "客户", "customer": "客户",
		"contract": "合同", "settle": "结算", "auth": "认证", "login": "登录", "user": "用户",
		"order": "订单", "account": "账户", "cash": "现金", "pool": "池", "esb": "ESB",
		"batch": "批处理", "schedule": "调度", "config": "配置", "security": "安全",
		"api": "接口", "service": "服务", "module": "模块", "channel": "渠道",
		"kri": "KRI", "ice": "ICE", "audit": "审计", "know": "知识", "knowledge": "知识",
		"quota": "指标", "element": "要素", "factor": "因子", "workflow": "工作流",
		"basic": "基础", "entity": "实体", "follow": "跟进", "fin": "财务",
		"import": "导入", "export": "导出", "iir": "内部审计", "grc": "GRC",
	}
	var parts []string
	for _, t := range filtered {
		if zh, ok := dict[t]; ok {
			parts = append(parts, zh)
		} else if isAscii(t) {
			parts = append(parts, humanize(t))
		} else {
			parts = append(parts, t)
		}
	}
	core := strings.Join(parts, "")
	if core == "" {
		core = stem
	}
	return finalizeTitle(core, kind)
}

func finalizeTitle(core, kind string) string {
	if core == "" {
		switch kind {
		case "api":
			return "接口文档"
		case "module":
			return "核心模块"
		case "capability":
			return "业务能力"
		default:
			return "主题"
		}
	}
	switch kind {
	case "api":
		// Prefer "…接口族" / "…接口" without leaking Control/Comp suffixes.
		if strings.HasSuffix(core, "接口") || strings.Contains(core, "API") {
			return core
		}
		return core + "接口"
	case "module":
		if !strings.HasSuffix(core, "模块") {
			return core + "模块"
		}
	case "capability":
		if strings.HasSuffix(core, "管理") || strings.HasSuffix(core, "功能") ||
			strings.HasSuffix(core, "模块") || strings.HasSuffix(core, "流程") {
			return core
		}
		return core + "管理"
	case "entity":
		if strings.HasSuffix(core, "模型") || strings.HasSuffix(core, "实体") || strings.HasSuffix(core, "表") {
			return core
		}
		return core + "数据模型"
	}
	return core
}

func humanize(s string) string {
	parts := splitIdent(s)
	for i, p := range parts {
		if p == "" {
			continue
		}
		r := []rune(p)
		r[0] = unicode.ToUpper(r[0])
		parts[i] = string(r)
	}
	return strings.Join(parts, " ")
}

func splitIdent(stem string) []string {
	stem = regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(stem, `${1} ${2}`)
	stem = regexp.MustCompile(`[_\-\.]+`).ReplaceAllString(stem, " ")
	return strings.Fields(stem)
}

func safeSegment(title string) string {
	title = strings.TrimSpace(title)
	title = strings.ReplaceAll(title, "/", "-")
	title = strings.ReplaceAll(title, "\\", "-")
	title = strings.ReplaceAll(title, ":", "-")
	if title == "" {
		return "page"
	}
	return title
}

func isLowQuality(title string) bool {
	if title == "" || len([]rune(title)) < 2 {
		return true
	}
	if match(title, `^(接口|服务|处理|控制|资源|模块|实体类|API|管理|数据模型)$`) {
		return true
	}
	if match(title, `^(处理|服务|控制|资源)(服务|接口|模块)?$`) {
		return true
	}
	// Reject residual technical class mirrors: FooControl接口 / BarComp接口
	if match(title, `(?i)(Control|Controller|Comp|ServiceImpl)(接口|模块)?$`) {
		return true
	}
	return false
}

func match(s, pattern string) bool {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

func goal(zh bool, a, b string) string {
	if zh {
		return a
	}
	return b
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func isAscii(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func itohex(n int) string {
	const hex = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 && i > 0 {
		i--
		b[i] = hex[n%16]
		n /= 16
	}
	return string(b[i:])
}
