// Package planner builds a multi-level document tree seed from repository inventory.
package planner

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/JSHurt/wikify/internal/evidence"
	"github.com/JSHurt/wikify/internal/models"
	"github.com/JSHurt/wikify/internal/scan"
	"github.com/JSHurt/wikify/internal/wikiplan"
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
	// DependentFiles pre-binds evidence paths (repo-relative, slash-separated).
	// bindEvidence skips pages that already carry deps, so these survive.
	DependentFiles []string
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

	// Dual-rail budgets: fractions of MaxPages; floors never sum above MaxPages.
	// Computed before seeding so section/child caps can scale with the budget.
	foundBudget, bizBudget, techBudget := railBudgets(opts.MaxPages)

	// Vendor detector shared by all candidate builders: committed third-party
	// library trees (crypto-js, swiper, jquery plugins …) must not become page
	// candidates. scope.include patterns that explicitly name a library exempt
	// their paths (see evidence.NewVendorDetector).
	vd := evidence.NewVendorDetector(model, plan.ScopeInclude())

	foundation, technicalBase := buildDefaultTreeSplit(model, zh, techBudget)
	business := buildBusinessDomains(model, zh, bizBudget, vd)

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
	inv := buildInventory(model, zh, invCap, vd)
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
	// Per-rail trim if somehow over MaxPages (never prefix-chop mixed rails).
	if len(items) > opts.MaxPages {
		items = trimPlannedByRail(items, opts.MaxPages)
	}
	for i := range items {
		if items[i].Track == "" {
			items[i].Track = trackForCategory(items[i].Category, items[i].Section)
		}
	}
	return toWiki(items)
}

// railBudgets returns foundation/business/technical page caps that always sum to maxPages.
// Target mix ≈ 10% / 50% / 40%, with tiny floors that scale down for small maxPages.
func railBudgets(maxPages int) (found, biz, tech int) {
	if maxPages <= 0 {
		maxPages = 120
	}
	biz = int(float64(maxPages) * 0.50)
	tech = int(float64(maxPages) * 0.40)
	found = maxPages - biz - tech
	// Soft floors only when MaxPages is large enough to absorb them.
	if maxPages >= 40 {
		if biz < 8 {
			biz = 8
		}
		if tech < 10 {
			tech = 10
		}
		if found < 2 {
			found = 2
		}
	} else {
		// Small catalogs: keep at least 1 foundation when possible.
		if found < 1 {
			found = 1
		}
		if biz < 1 {
			biz = 1
		}
		if tech < 1 {
			tech = 1
		}
	}
	// Re-normalize so sum == maxPages (prefer shrinking tech, then biz).
	sum := found + biz + tech
	for sum > maxPages {
		if tech > 1 {
			tech--
		} else if biz > 1 {
			biz--
		} else if found > 1 {
			found--
		} else {
			break
		}
		sum = found + biz + tech
	}
	for sum < maxPages {
		// Give remainder to business first (capability coverage).
		biz++
		sum++
	}
	return found, biz, tech
}

// SuggestMaxPages scales the default catalog budget with repository substance:
// 12 + codeFiles/8 + 2*modules, clamped to [12, 200], where codeFiles counts
// files that are code and not on noise paths. A nil/empty model (scan failed or
// nothing survived scope) keeps the legacy 120 fallback.
func SuggestMaxPages(model *scan.Model) int {
	if model == nil || len(model.Files) == 0 {
		return 120
	}
	code := 0
	for _, f := range model.Files {
		if scan.IsCodeFile(f.RelativePath) && !scan.IsNoisePath(f.RelativePath) {
			code++
		}
	}
	n := 12 + code/8 + 2*len(model.Modules)
	if n < 12 {
		n = 12
	}
	if n > 200 {
		n = 200
	}
	return n
}

// trimPlannedByRail trims Planned lists keeping dual-rail proportions.
func trimPlannedByRail(items []Planned, maxPages int) []Planned {
	if maxPages <= 0 || len(items) <= maxPages {
		return items
	}
	foundB, bizB, techB := railBudgets(maxPages)
	var f, b, t []Planned
	for _, p := range items {
		tr := p.Track
		if tr == "" {
			tr = trackForCategory(p.Category, p.Section)
		}
		switch tr {
		case models.TrackFoundation:
			f = append(f, p)
		case models.TrackBusiness:
			b = append(b, p)
		default:
			t = append(t, p)
		}
	}
	if len(f) > foundB {
		f = f[:foundB]
	}
	if len(b) > bizB {
		b = b[:bizB]
	}
	if len(t) > techB {
		t = t[:techB]
	}
	out := append(append(f, b...), t...)
	if len(out) > maxPages {
		out = out[:maxPages]
	}
	return out
}

// TrimWikiByRail trims a free LLM catalog to maxPages while preserving dual-rail mix.
// Prefer dropping technical inventory tail over foundation/business pages.
func TrimWikiByRail(wiki *models.Wiki, maxPages int) {
	if wiki == nil || maxPages <= 0 || len(wiki.Pages) <= maxPages {
		return
	}
	EnsureTracks(wiki)
	foundB, bizB, techB := railBudgets(maxPages)
	var f, b, t []models.WikiPage
	for _, p := range wiki.Pages {
		switch p.Track {
		case models.TrackFoundation:
			f = append(f, p)
		case models.TrackBusiness:
			b = append(b, p)
		default:
			t = append(t, p)
		}
	}
	if len(f) > foundB {
		f = f[:foundB]
	}
	if len(b) > bizB {
		b = b[:bizB]
	}
	if len(t) > techB {
		t = t[:techB]
	}
	// If under max after caps, fill from remaining technical then business then foundation.
	out := append(append(f, b...), t...)
	if len(out) < maxPages {
		// Rebuild leftovers by original order
		seen := map[string]bool{}
		for _, p := range out {
			seen[p.Slug+p.Title] = true
		}
		for _, p := range wiki.Pages {
			if len(out) >= maxPages {
				break
			}
			key := p.Slug + p.Title
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, p)
		}
	}
	if len(out) > maxPages {
		out = out[:maxPages]
	}
	// Re-slug if needed for stability after reorder
	for i := range out {
		if out[i].Slug == "" {
			out[i].Slug = models.MakeSlug(i+1, out[i].Title)
		}
	}
	wiki.Pages = out
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
// Also re-runs InferTrack for already-valid tracks so foundation promotion
// (入门/总览/阅读路径 mislabeled as business) applies on polish/export.
func EnsureTracks(wiki *models.Wiki) {
	if wiki == nil {
		return
	}
	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		p.Track = models.InferTrack(*p)
	}
}

// RebalanceDualRail ensures Track coverage and soft-merges foundation/business
// pages from seed only when those rails are truly thin.
// Does not inject fixed engineering templates — structure comes from seed/scan.
// Does not rewrite LLM titles; over-cap uses rail-aware trim (not prefix chop).
// For signal-gated engineering indexes (API/security/ops/test/FAQ), use MergeEngineeringSeeds.
func RebalanceDualRail(wiki, seed *models.Wiki, maxPages int) {
	if wiki == nil {
		return
	}
	EnsureTracks(wiki)
	if seed == nil || len(seed.Pages) == 0 {
		if maxPages > 0 {
			TrimWikiByRail(wiki, maxPages)
		}
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
	total := len(wiki.Pages)
	bizN := count(models.TrackBusiness)
	foundN := count(models.TrackFoundation)

	// Seed must actually offer business pages (real path clusters), else accept thin biz rail.
	seedBiz := 0
	for _, p := range seed.Pages {
		if p.Track == models.TrackBusiness {
			seedBiz++
		}
	}

	// Soft floors: repair empty foundation or thin business.
	// Absolute count + share both matter — a 1/3 biz ratio still needs top-up
	// when the free catalog only listed one capability page.
	// Avoid polluting healthy catalogs with low-quality path-humanize titles.
	wantFound := 1
	wantBiz := 0
	if seedBiz > 0 {
		// Absolute soft target scales with maxPages (and seed supply).
		absTarget := 3
		if maxPages >= 80 {
			absTarget = 6
		} else if maxPages > 0 && maxPages < 40 {
			absTarget = 2
		}
		absTarget = minInt(absTarget, seedBiz)

		ratioThin := total > 0 && float64(bizN)/float64(total) < 0.20
		countThin := bizN < absTarget
		if total == 0 || bizN == 0 || ratioThin || countThin {
			wantBiz = absTarget
			// When only slightly thin, top up modestly rather than jump to full target.
			if bizN > 0 && wantBiz > bizN+4 {
				wantBiz = bizN + 4
			}
			wantBiz = minInt(wantBiz, seedBiz)
		}
	}

	need := map[string]int{}
	if foundN < wantFound {
		need[models.TrackFoundation] = wantFound - foundN
	}
	if wantBiz > 0 && bizN < wantBiz {
		need[models.TrackBusiness] = wantBiz - bizN
	}
	if len(need) == 0 {
		if maxPages > 0 {
			TrimWikiByRail(wiki, maxPages)
		}
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
		// Only soft-merge foundation/business rails; technical pages stay LLM/scan-owned.
		if p.Track == models.TrackTechnical {
			continue
		}
		// Skip low-quality path mirrors when soft-merging into an existing catalog.
		if isLowQuality(p.Title) {
			continue
		}
		add = append(add, p)
		seen[key] = true
		need[p.Track]--
	}
	if len(add) > 0 {
		wiki.Pages = append(wiki.Pages, add...)
	}
	if maxPages > 0 {
		TrimWikiByRail(wiki, maxPages)
	}
	for i := range wiki.Pages {
		if wiki.Pages[i].Slug == "" {
			wiki.Pages[i].Slug = models.MakeSlug(i+1, wiki.Pages[i].Title)
		}
	}
	EnsureTracks(wiki)
}


// MergeEngineeringSeeds soft-merges signal-gated generic engineering index pages
// (API / security / deploy / test / FAQ / performance / coding standards / config)
// into an existing catalog when those topics are missing.
//
// Driven only by scan signals — no product domain names. Safe for polish on
// older LLM catalogs that omitted engineering coverage. Prefers section index
// pages; inventory class mirrors are never injected. Returns count added.
func MergeEngineeringSeeds(wiki *models.Wiki, model *scan.Model, maxPages int) int {
	if wiki == nil || model == nil {
		return 0
	}
	EnsureTracks(wiki)
	zh := model.Language != "en"
	seed := buildDefaultTree(model, zh, 0)
	seenTitle := map[string]bool{}
	seenSectionEng := map[string]bool{}
	for _, p := range wiki.Pages {
		key := strings.ToLower(strings.TrimSpace(p.Title))
		seenTitle[key] = true
		if isEngineeringSection(p.Section) || isEngineeringSection(p.Title) {
			seenSectionEng[strings.ToLower(strings.TrimSpace(p.Section))] = true
			seenSectionEng[strings.ToLower(strings.TrimSpace(p.Title))] = true
		}
	}

	var add []models.WikiPage
	for _, it := range seed {
		if !isEngineeringCategory(it.Category) {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(it.Title))
		if seenTitle[key] {
			continue
		}
		secKey := strings.ToLower(strings.TrimSpace(it.Section))
		isIndex := it.Title == it.Section || it.Parent == ""
		if !isIndex {
			// Children only after parent index is present or just added.
			parentKey := strings.ToLower(strings.TrimSpace(it.Parent))
			if parentKey == "" {
				parentKey = secKey
			}
			if !seenTitle[parentKey] && !seenSectionEng[secKey] {
				continue
			}
		} else if seenSectionEng[secKey] {
			// Index: skip if any page already sits in this engineering section.
			continue
		}
		if isLowQuality(it.Title) {
			continue
		}
		track := it.Track
		if track == "" {
			track = trackForCategory(it.Category, it.Section)
		}
		page := models.WikiPage{
			Title:           it.Title,
			Slug:            models.MakeSlug(len(wiki.Pages)+len(add)+1, it.Title),
			Level:           it.Level,
			Section:         it.Section,
			Group:           it.Group,
			Parent:          it.Parent,
			Goal:            it.Goal,
			DependentFiles:  it.DependentFiles,
			ContentPath:     it.ContentPath,
			DescriptionSlug: DescriptionSlug(it.Title),
			Track:           track,
		}
		if page.Level == "" {
			page.Level = "Intermediate"
		}
		add = append(add, page)
		seenTitle[key] = true
		seenSectionEng[secKey] = true
		if len(add) >= 24 {
			break
		}
	}
	if len(add) == 0 {
		return 0
	}
	wiki.Pages = append(wiki.Pages, add...)
	if maxPages > 0 {
		TrimWikiByRail(wiki, maxPages)
	}
	for i := range wiki.Pages {
		if wiki.Pages[i].Slug == "" {
			wiki.Pages[i].Slug = models.MakeSlug(i+1, wiki.Pages[i].Title)
		}
	}
	EnsureTracks(wiki)
	return len(add)
}

// isEngineeringCategory marks signal-gated technical seed categories.
func isEngineeringCategory(cat string) bool {
	switch cat {
	case "api", "data", "config", "security", "ops", "guide", "architecture":
		return true
	}
	return false
}

// isEngineeringSection detects generic engineering section labels (CN + EN).
func isEngineeringSection(s string) bool {
	if s == "" {
		return false
	}
	keys := []string{
		"API接口", "接口文档", "API Documentation", "API",
		"数据模型", "数据库", "Data Model", "Database",
		"配置管理", "Configuration",
		"安全与访问", "Security",
		"部署与运维", "部署", "运维", "Deploy", "Operations", "Ops",
		"测试与质量", "Testing",
		"性能与扩展", "Performance",
		"故障排查", "Troubleshooting",
		"常见问题", "FAQ",
		"开发指南", "Developer Guide",
		"编码规范", "Coding",
		"系统架构", "Architecture",
	}
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}


func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

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
			sec = d.Title
		}
		// Multi-level path: section/title when parent present.
		cpath := safeSegment(d.Title) + ".md"
		if d.Parent != "" && d.Parent != d.Title {
			cpath = safeSegment(d.Parent) + "/" + safeSegment(d.Title) + ".md"
		}
		items = append(items, Planned{
			Title:       d.Title,
			Parent:      d.Parent,
			Section:     sec,
			Goal:        d.Goal,
			Level:       "Intermediate",
			Track:       models.InferTrack(models.WikiPage{Title: d.Title, Section: sec, Goal: d.Goal}),
			ContentPath: cpath,
		})
		if max > 0 && len(items) >= max {
			break
		}
	}
	wiki := toWiki(items)
	ApplyHierarchyPaths(wiki)
	EnsureTracks(wiki)
	return wiki
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
			DependentFiles:  it.DependentFiles,
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
	// Stable, language-only labels — no product codenames or domain word lists.
	if !zh {
		return taxonomy{
			modules: "Core Modules", architecture: "System Architecture", api: "API Documentation",
			data: "Data Model Design", ops: "Deployment and Operations", troubleshooting: "Troubleshooting",
		}
	}
	return taxonomy{
		modules: "核心模块详解", architecture: "系统架构设计", api: "API接口文档",
		data: "数据模型设计", ops: "部署与运维", troubleshooting: "故障排查",
	}
}

// buildDefaultTree seeds foundation + technical skeleton pages from scan signals.
// techBudget (0 = unknown) lets the module-children cap scale with the technical
// rail budget; all other seeding is budget-independent.
func buildDefaultTree(model *scan.Model, zh bool, techBudget int) []Planned {
	tax := chooseTaxonomy(model, zh)
	sig := detectRepoSignals(model)
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

	// Universal foundation — every repo needs orientation + first run.
	if zh {
		push(Planned{Title: "项目概述", Section: "项目概述", Category: "overview", Goal: "描述项目定位、核心价值、模块边界与目标用户。", ContentPath: "项目概述.md", Level: "Beginner"})
		push(Planned{Title: "快速开始", Section: "快速开始", Category: "getting-started", Goal: "说明环境准备、安装构建与最小可运行步骤。", ContentPath: "快速开始.md", Level: "Beginner"})
	} else {
		push(Planned{Title: "Project Overview", Section: "Project Overview", Category: "overview", Goal: "Describe project positioning and value.", ContentPath: "project-overview.md", Level: "Beginner"})
		push(Planned{Title: "Getting Started", Section: "Getting Started", Category: "getting-started", Goal: "Explain setup and minimal run steps.", ContentPath: "getting-started.md", Level: "Beginner"})
	}

	// Architecture — always one index; children only when structure signals justify depth.
	arch := tax.architecture
	push(Planned{Title: arch, Section: arch, Category: "architecture", Goal: goal(zh, "给出系统整体架构、分层关系与关键路径。", "Overall architecture and layering."), ContentPath: arch + "/" + arch + ".md", Level: "Intermediate"})
	// Expand architecture kids only for multi-module or layered layouts.
	if sig.multiModule || sig.hasAPI || sig.hasData {
		archKidsZH := []string{"整体架构设计", "模块划分原则"}
		archKidsEN := []string{"Overall Architecture", "Module Boundaries"}
		if sig.hasData {
			archKidsZH = append(archKidsZH, "数据流架构")
			archKidsEN = append(archKidsEN, "Data Flow Architecture")
		}
		if sig.hasAPI {
			archKidsZH = append(archKidsZH, "组件交互关系")
			archKidsEN = append(archKidsEN, "Component Interactions")
		}
		if sig.hasDeploy {
			archKidsZH = append(archKidsZH, "部署架构")
			archKidsEN = append(archKidsEN, "Deployment Architecture")
		}
		kids := archKidsZH
		if !zh {
			kids = archKidsEN
		}
		for _, k := range kids {
			push(Planned{Title: k, Section: arch, Group: arch, Parent: arch, Category: "architecture", Goal: k, ContentPath: arch + "/" + k + ".md", Level: "Intermediate"})
		}
	}
	// Multi-root repos: one architecture child per manifest root (structural, not product-derived).
	if roots := multiManifestRoots(model); len(roots) >= 2 {
		for i, r := range roots {
			if i >= 8 {
				break
			}
			rb := pathBase(r)
			title := humanize(rb)
			if isLowQuality(title) {
				title = rb
			}
			safe := safeSegment(title)
			push(Planned{
				Title: title, Section: arch, Group: arch, Parent: arch, Category: "architecture",
				Goal:        goal(zh, "说明子模块 "+r+" 在整体架构中的角色、边界与依赖。", "Architecture role, boundary and dependencies of module root "+r+"."),
				ContentPath: arch + "/" + safe + ".md", Level: "Intermediate",
			})
		}
	}

	// Modules index only when scan found real modules (not fixed product modules).
	mod := tax.modules
	if model != nil && len(model.Modules) > 0 {
		// Cap module children at 8 by default; when the technical rail budget is
		// generous (>=24 pages) allow up to min(12, module count) — breadth scaling.
		modCap := 8
		if techBudget >= 24 && len(model.Modules) > modCap {
			modCap = minInt(12, len(model.Modules))
		}
		push(Planned{Title: mod, Section: mod, Category: "modules", Goal: goal(zh, "索引仓库模块及其职责。", "Index repository modules."), ContentPath: mod + "/" + mod + ".md", Level: "Intermediate"})
		for i, m := range model.Modules {
			if i >= modCap {
				break
			}
			// Module directories named after well-known libraries are committed
			// third-party trees, not project modules.
			if evidence.IsKnownVendorLib(m.Name) || evidence.IsKnownVendorLib(pathBase(m.Path)) {
				continue
			}
			title := humanize(m.Name)
			if zh {
				title = composeChineseTitle(m.Name, "module")
			}
			if isLowQuality(title) {
				title = m.Name
			}
			if match(title, `^(模块|Common|App|Api|Web|Core|Module)(模块)?$`) {
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

	// Developer guide — one index always; children only for non-trivial repos.
	fileN := 0
	if model != nil {
		fileN = len(model.Files)
	}
	if zh {
		push(Planned{Title: "开发指南", Section: "开发指南", Category: "guide", Goal: "开发规范、目录与技术栈。", ContentPath: "开发指南/开发指南.md", Level: "Beginner"})
		if fileN >= 12 || sig.multiModule {
			push(Planned{Title: "目录结构", Section: "开发指南", Group: "开发指南", Parent: "开发指南", Goal: "说明仓库目录布局。", ContentPath: "开发指南/目录结构.md", Level: "Beginner"})
			push(Planned{Title: "技术栈与依赖", Section: "开发指南", Group: "开发指南", Parent: "开发指南", Goal: "列出主要依赖与运行时。", ContentPath: "开发指南/技术栈与依赖.md", Level: "Beginner"})
		}
	} else {
		push(Planned{Title: "Developer Guide", Section: "Developer Guide", Category: "guide", Goal: "Coding conventions and layout.", ContentPath: "developer-guide/developer-guide.md", Level: "Beginner"})
		if fileN >= 12 || sig.multiModule {
			push(Planned{Title: "Directory layout", Section: "Developer Guide", Group: "Developer Guide", Parent: "Developer Guide", Goal: "Repository layout.", ContentPath: "developer-guide/directory-layout.md", Level: "Beginner"})
			push(Planned{Title: "Tech stack and dependencies", Section: "Developer Guide", Group: "Developer Guide", Parent: "Developer Guide", Goal: "Dependencies and runtime.", ContentPath: "developer-guide/tech-stack.md", Level: "Beginner"})
		}
	}

	// Signal-driven technical indexes — generic engineering topics only (no product names).
	// Prefer section index pages + a few scan-justified children so Qoder-style
	// engineering coverage (API/security/deploy/test/FAQ) is present without hardcoding domains.
	if sig.hasAPI {
		push(Planned{Title: tax.api, Section: tax.api, Category: "api", Goal: goal(zh, "汇总对外接口能力与调用约定（按真实包/路径聚合）。", "API surface overview aggregated from the codebase."), ContentPath: tax.api + "/" + tax.api + ".md", Level: "Intermediate"})
		if zh {
			push(Planned{Title: "接口设计约定", Section: tax.api, Group: tax.api, Parent: tax.api, Category: "api", Goal: "接口分层、错误码与版本约定（以代码为准）。", ContentPath: tax.api + "/接口设计约定.md", Level: "Intermediate"})
			if sig.hasAuth {
				push(Planned{Title: "接口鉴权与限流", Section: tax.api, Group: tax.api, Parent: tax.api, Category: "api", Goal: "鉴权、会话与限流相关入口。", ContentPath: tax.api + "/接口鉴权与限流.md", Level: "Advanced"})
			}
		} else {
			push(Planned{Title: "API design conventions", Section: tax.api, Group: tax.api, Parent: tax.api, Category: "api", Goal: "Layering, errors and versioning as found in code.", ContentPath: tax.api + "/api-design-conventions.md", Level: "Intermediate"})
			if sig.hasAuth {
				push(Planned{Title: "API auth and rate limits", Section: tax.api, Group: tax.api, Parent: tax.api, Category: "api", Goal: "Auth, session and rate-limit entry points.", ContentPath: tax.api + "/api-auth-and-rate-limits.md", Level: "Advanced"})
			}
		}
	}
	if sig.hasData {
		push(Planned{Title: tax.data, Section: tax.data, Category: "data", Goal: goal(zh, "数据模型与持久化总览（按领域包聚合）。", "Data models overview aggregated from the codebase."), ContentPath: tax.data + "/" + tax.data + ".md", Level: "Intermediate"})
		if zh {
			push(Planned{Title: "持久化与访问层", Section: tax.data, Group: tax.data, Parent: tax.data, Category: "data", Goal: "DAO/Mapper/Repository 访问路径。", ContentPath: tax.data + "/持久化与访问层.md", Level: "Intermediate"})
		} else {
			push(Planned{Title: "Persistence and access layer", Section: tax.data, Group: tax.data, Parent: tax.data, Category: "data", Goal: "DAO/mapper/repository access paths.", ContentPath: tax.data + "/persistence-and-access-layer.md", Level: "Intermediate"})
		}
	}
	if sig.hasConfig {
		cfgDeps := capDeps(sig.ConfigFiles, 8)
		if zh {
			push(Planned{Title: "配置管理", Section: "配置管理", Category: "config", Goal: "配置项与环境差异（以仓库实际配置为准）。", ContentPath: "配置管理/配置管理.md", Level: "Intermediate", DependentFiles: cfgDeps})
			push(Planned{Title: "环境与配置差异", Section: "配置管理", Group: "配置管理", Parent: "配置管理", Category: "config", Goal: "多环境配置与覆盖关系。", ContentPath: "配置管理/环境与配置差异.md", Level: "Intermediate", DependentFiles: cfgDeps})
		} else {
			push(Planned{Title: "Configuration", Section: "Configuration", Category: "config", Goal: "Configuration and environment differences as found in the repo.", ContentPath: "configuration/configuration.md", Level: "Intermediate", DependentFiles: cfgDeps})
			push(Planned{Title: "Environment differences", Section: "Configuration", Group: "Configuration", Parent: "Configuration", Category: "config", Goal: "Multi-env config overlays.", ContentPath: "configuration/environment-differences.md", Level: "Intermediate", DependentFiles: cfgDeps})
		}
	}
	if sig.hasAuth {
		if zh {
			push(Planned{Title: "安全与访问控制", Section: "安全与访问控制", Category: "security", Goal: "仓库中实际存在的认证/授权/访问控制机制。", ContentPath: "安全与访问控制/安全与访问控制.md", Level: "Advanced"})
			push(Planned{Title: "认证与授权机制", Section: "安全与访问控制", Group: "安全与访问控制", Parent: "安全与访问控制", Category: "security", Goal: "登录、令牌与权限校验入口。", ContentPath: "安全与访问控制/认证与授权机制.md", Level: "Advanced"})
		} else {
			push(Planned{Title: "Security and access control", Section: "Security and access control", Category: "security", Goal: "Auth and access control mechanisms present in the repo.", ContentPath: "security-and-access-control/security-and-access-control.md", Level: "Advanced"})
			push(Planned{Title: "Authentication and authorization", Section: "Security and access control", Group: "Security and access control", Parent: "Security and access control", Category: "security", Goal: "Login, token and permission entry points.", ContentPath: "security-and-access-control/authentication-and-authorization.md", Level: "Advanced"})
		}
	}
	if sig.hasDeploy || sig.hasOps {
		opsIdxDeps := capDeps(append(append([]string{}, sig.DeployFiles...), sig.OpsFiles...), 8)
		deployDeps := capDeps(sig.DeployFiles, 8)
		opsDeps := capDeps(sig.OpsFiles, 8)
		push(Planned{Title: tax.ops, Section: tax.ops, Category: "ops", Goal: goal(zh, "部署与运维要点（以仓库构建/脚本为准）。", "Deploy and ops based on repo build/scripts."), ContentPath: tax.ops + "/" + tax.ops + ".md", Level: "Intermediate", DependentFiles: opsIdxDeps})
		if zh {
			if sig.hasDeploy {
				push(Planned{Title: "构建与发布", Section: tax.ops, Group: tax.ops, Parent: tax.ops, Category: "ops", Goal: "构建脚本、镜像与发布入口。", ContentPath: tax.ops + "/构建与发布.md", Level: "Intermediate", DependentFiles: deployDeps})
			}
			if sig.hasOps {
				push(Planned{Title: "监控与任务调度", Section: tax.ops, Group: tax.ops, Parent: tax.ops, Category: "ops", Goal: "监控、定时任务与运维脚本。", ContentPath: tax.ops + "/监控与任务调度.md", Level: "Intermediate", DependentFiles: opsDeps})
			}
		} else {
			if sig.hasDeploy {
				push(Planned{Title: "Build and release", Section: tax.ops, Group: tax.ops, Parent: tax.ops, Category: "ops", Goal: "Build scripts, images and release entry points.", ContentPath: tax.ops + "/build-and-release.md", Level: "Intermediate", DependentFiles: deployDeps})
			}
			if sig.hasOps {
				push(Planned{Title: "Monitoring and scheduling", Section: tax.ops, Group: tax.ops, Parent: tax.ops, Category: "ops", Goal: "Monitoring, jobs and ops scripts.", ContentPath: tax.ops + "/monitoring-and-scheduling.md", Level: "Intermediate", DependentFiles: opsDeps})
			}
		}
	}
	if sig.hasTests {
		testDeps := capDeps(sig.TestFiles, 8)
		if zh {
			push(Planned{Title: "测试与质量", Section: "测试与质量", Category: "guide", Goal: "仓库中的测试布局与质量门禁入口。", ContentPath: "测试与质量/测试与质量.md", Level: "Intermediate", DependentFiles: testDeps})
			push(Planned{Title: "测试布局与运行", Section: "测试与质量", Group: "测试与质量", Parent: "测试与质量", Category: "guide", Goal: "单元/集成测试目录与运行方式。", ContentPath: "测试与质量/测试布局与运行.md", Level: "Intermediate", DependentFiles: testDeps})
		} else {
			push(Planned{Title: "Testing and quality", Section: "Testing and quality", Category: "guide", Goal: "Test layout and quality gates found in the repo.", ContentPath: "testing-and-quality/testing-and-quality.md", Level: "Intermediate", DependentFiles: testDeps})
			push(Planned{Title: "Test layout and how to run", Section: "Testing and quality", Group: "Testing and quality", Parent: "Testing and quality", Category: "guide", Goal: "Unit/integration test layout and run commands.", ContentPath: "testing-and-quality/test-layout-and-how-to-run.md", Level: "Intermediate", DependentFiles: testDeps})
		}
	}
	// Performance seed when code volume / multi-module justifies a dedicated page.
	if fileN >= 80 || sig.multiModule {
		if zh {
			push(Planned{Title: "性能与扩展", Section: "性能与扩展", Category: "ops", Goal: "性能关注点、缓存与扩展边界（以代码路径为准）。", ContentPath: "性能与扩展.md", Level: "Advanced"})
		} else {
			push(Planned{Title: "Performance and scalability", Section: "Performance and scalability", Category: "ops", Goal: "Perf hotspots, caching and scaling boundaries from the codebase.", ContentPath: "performance-and-scalability.md", Level: "Advanced"})
		}
	}
	// Coding standards as child of developer guide for non-trivial repos.
	if fileN >= 12 || sig.multiModule {
		if zh {
			push(Planned{Title: "编码规范", Section: "开发指南", Group: "开发指南", Parent: "开发指南", Category: "guide", Goal: "命名、分层与代码组织约定。", ContentPath: "开发指南/编码规范.md", Level: "Beginner"})
		} else {
			push(Planned{Title: "Coding standards", Section: "Developer Guide", Group: "Developer Guide", Parent: "Developer Guide", Category: "guide", Goal: "Naming, layering and code organisation.", ContentPath: "developer-guide/coding-standards.md", Level: "Beginner"})
		}
	}
	// Typical development tasks — a cookbook page for end-to-end code changes.
	// Gated on both an API surface and a data layer, and on being able to
	// evidence a generic role chain from real files (no product vocabulary).
	if sig.hasAPI && sig.hasData && (fileN >= 12 || sig.multiModule) {
		if chain := devTaskRoleChain(model); len(chain) >= 2 {
			if zh {
				push(Planned{Title: "典型开发任务", Section: "开发指南", Group: "开发指南", Parent: "开发指南", Category: "guide", Goal: "常见改动的端到端路径：涉及文件、修改顺序与验证方式。", ContentPath: "开发指南/典型开发任务.md", Level: "Intermediate", DependentFiles: capDeps(chain, 8)})
			} else {
				push(Planned{Title: "Typical development tasks", Section: "Developer Guide", Group: "Developer Guide", Parent: "Developer Guide", Category: "guide", Goal: "End-to-end change paths: files touched, edit order and verification.", ContentPath: "developer-guide/typical-development-tasks.md", Level: "Intermediate", DependentFiles: capDeps(chain, 8)})
			}
		}
	}
	// Troubleshooting when ops/deploy/tests signals exist — avoid empty shells.
	if sig.hasOps || sig.hasDeploy || sig.hasTests {
		push(Planned{Title: tax.troubleshooting, Section: tax.troubleshooting, Category: "ops", Goal: goal(zh, "常见故障与排查入口。", "Troubleshooting entry points."), ContentPath: tax.troubleshooting + ".md", Level: "Intermediate"})
	}
	// FAQ for medium+ repos (generic questions, not product-specific).
	if fileN >= 40 || sig.multiModule {
		if zh {
			push(Planned{Title: "常见问题", Section: "常见问题", Category: "guide", Goal: "环境、构建与运行中的高频问题入口。", ContentPath: "常见问题.md", Level: "Beginner"})
		} else {
			push(Planned{Title: "FAQ", Section: "FAQ", Category: "guide", Goal: "Common setup, build and run questions.", ContentPath: "faq.md", Level: "Beginner"})
		}
	}

	return out
}

// repoSignals are boolean capabilities inferred from scan inventory only.
// They gate optional technical sections — never hard-code product domain pages.
// The *Files slices carry representative evidence paths (sorted, capped) so
// engineering seed pages can pre-bind DependentFiles.
type repoSignals struct {
	hasAPI, hasData, hasConfig, hasAuth, hasDeploy, hasOps, hasTests, multiModule bool

	DeployFiles []string
	OpsFiles    []string
	TestFiles   []string
	ConfigFiles []string
}

// signalFileCap bounds each collected evidence list in repoSignals.
const signalFileCap = 12

func detectRepoSignals(model *scan.Model) repoSignals {
	var s repoSignals
	if model == nil {
		return s
	}
	vd := evidence.NewVendorDetector(model, nil)
	apiN, dataN, cfgN, authN, deployN, opsN, testN := 0, 0, 0, 0, 0, 0, 0
	for _, f := range model.Files {
		orig := filepath.ToSlash(f.RelativePath)
		rel := strings.ToLower(orig)
		base := strings.ToLower(filepath.Base(rel))
		// Committed library files must not fake capabilities (vue-router.js
		// matching `router` would flip hasAPI on a pure front-end drop).
		if vd.IsVendor(orig) {
			continue
		}
		if scan.IsAPISourceFile(f.RelativePath) || match(rel, `(controller|resource|handler|router|endpoint|action|servlet|restcontroller)`) ||
			match(base, `(controller|control|resource|handler|endpoint)\.(java|kt|ts|js|go|cs|py)$`) {
			apiN++
		}
		if match(rel, `(entity|domain|model|dto|vo|/po/|mapper|dao|repository|schema|migration|\.sql$|\.bpmn$)`) {
			dataN++
		}
		if match(base, `(application|config|settings|bootstrap|application-\w+)\.(yml|yaml|properties|toml|json)$`) ||
			match(rel, `(^|/)\.env|(^|/)(config|conf|resources)(/|$)`) ||
			match(base, `\.(properties|yml|yaml)$`) && match(rel, `(config|conf|resources|spring)`) {
			cfgN++
			s.ConfigFiles = append(s.ConfigFiles, orig)
		}
		if match(rel, `(auth|security|oauth|jwt|shiro|passport|permission|rbac|login|session|interceptor|filter)`) ||
			match(base, `(security|auth|login|permission).*\.(java|kt|ts|js|go|cs|py)$`) {
			authN++
		}
		// Deploy from real deploy artifacts + common CI/build scripts.
		if match(base, `(dockerfile|docker-compose|compose\.ya?ml|chart\.yaml|values\.yaml|jenkinsfile|makefile|\.gitlab-ci\.yml|\.travis\.yml)`) ||
			match(rel, `(^|/)(deploy|deployment|k8s|helm|terraform|ansible|ci|cd|\.github/workflows|\.gitlab|\.circleci)(/|$)`) ||
			match(base, `(pom\.xml|build\.gradle|package\.json|go\.mod)$`) && len(model.Modules) >= 2 {
			deployN++
			s.DeployFiles = append(s.DeployFiles, orig)
		}
		if match(rel, `(ops|monitor|metric|prometheus|grafana|quartz|schedule|cron|job|actuator|health)`) {
			opsN++
			s.OpsFiles = append(s.OpsFiles, orig)
		}
		if match(rel, `(^|/)(test|tests|__tests__|spec)/`) || match(base, `_test\.(go|py|ts|js|java)$|\.spec\.(ts|js)$|test\.(ts|js|py)$|tests?\.java$`) {
			testN++
			s.TestFiles = append(s.TestFiles, orig)
		}
	}
	for _, lst := range []*[]string{&s.DeployFiles, &s.OpsFiles, &s.TestFiles, &s.ConfigFiles} {
		sort.Strings(*lst)
		if len(*lst) > signalFileCap {
			*lst = (*lst)[:signalFileCap]
		}
	}
	// Thresholds: one strong hit is enough for API/data; config/auth/ops need a bit more signal noise control.
	s.hasAPI = apiN >= 1
	s.hasData = dataN >= 1
	s.hasConfig = cfgN >= 1
	s.hasAuth = authN >= 1
	s.hasDeploy = deployN >= 1
	s.hasOps = opsN >= 1
	s.hasTests = testN >= 1
	// Multi-module: manifest roots are the authoritative structural signal;
	// fall back to derived Modules only when no manifest roots were scanned.
	if len(model.ManifestRoots) >= 2 {
		s.multiModule = true
	} else if len(model.ManifestRoots) == 0 && len(model.Modules) >= 2 {
		s.multiModule = true
	}
	// Multi-module Java/Maven trees almost always have deploy/build story even without Dockerfile.
	if s.multiModule && (apiN > 0 || dataN > 0) {
		s.hasDeploy = true
	}
	return s
}

// EngineeringEvidence groups representative engineering file paths by category
// ("deploy" / "ops" / "test" / "config") from scan signals. Callers may use it
// to pre-bind evidence for engineering pages; categories without hits are omitted.
func EngineeringEvidence(model *scan.Model) map[string][]string {
	sig := detectRepoSignals(model)
	out := map[string][]string{}
	if len(sig.DeployFiles) > 0 {
		out["deploy"] = sig.DeployFiles
	}
	if len(sig.OpsFiles) > 0 {
		out["ops"] = sig.OpsFiles
	}
	if len(sig.TestFiles) > 0 {
		out["test"] = sig.TestFiles
	}
	if len(sig.ConfigFiles) > 0 {
		out["config"] = sig.ConfigFiles
	}
	return out
}

// capDeps returns a defensive copy of paths capped at n (nil when empty).
func capDeps(paths []string, n int) []string {
	if len(paths) == 0 || n <= 0 {
		return nil
	}
	if len(paths) > n {
		paths = paths[:n]
	}
	out := make([]string, len(paths))
	copy(out, paths)
	return out
}

// devTaskRoleKinds lists generic layering roles in natural edit order. It is a
// structural convention (entry point -> business logic -> persistence -> data
// shape), not a product vocabulary.
var devTaskRoleKinds = []string{"api", "service", "data", "entity"}

// devTaskCodeExt limits the role chain to source files.
var devTaskCodeExt = map[string]bool{
	".java": true, ".kt": true, ".go": true, ".ts": true, ".tsx": true,
	".js": true, ".jsx": true, ".vue": true, ".py": true, ".rb": true,
	".php": true, ".cs": true, ".scala": true, ".groovy": true,
}

// devTaskRoleNoise are role/layer words dropped when deriving a file stem.
var devTaskRoleNoise = map[string]bool{
	"controller": true, "controllers": true, "resource": true, "handler": true,
	"router": true, "routes": true, "endpoint": true, "servlet": true,
	"service": true, "services": true, "serviceimpl": true, "impl": true,
	"usecase": true, "manager": true, "biz": true, "application": true,
	"dao": true, "mapper": true, "repository": true, "repo": true,
	"entity": true, "dto": true, "vo": true, "po": true, "bo": true,
	"model": true, "domain": true, "api": true, "rest": true, "web": true,
	"base": true, "abstract": true, "default": true,
}

// devTaskRoleOf classifies a repo-relative path into one generic layer role,
// or "" when the path carries no layering signal. Basename conventions win over
// directory conventions because they are the stronger signal.
func devTaskRoleOf(rel string) string {
	base := strings.ToLower(pathBase(rel))
	switch {
	case match(base, `(controller|resource|handler|router|endpoint|servlet)`):
		return "api"
	case match(base, `(service|usecase|manager|biz)`):
		return "service"
	case match(base, `(dao|mapper|repository)`):
		return "data"
	case match(base, `(entity|dto|vo|po|bo|model|domain)`):
		return "entity"
	}
	low := strings.ToLower(rel)
	switch {
	case match(low, `(^|/)(controller|controllers|handler|handlers|router|routes|api|rest|endpoint|endpoints)/`):
		return "api"
	case match(low, `(^|/)(service|services|usecase|usecases|biz|application)/`):
		return "service"
	case match(low, `(^|/)(dao|mapper|mappers|repository|repositories|store|persistence)/`):
		return "data"
	case match(low, `(^|/)(entity|entities|model|models|domain|dto|vo|po|pojo)/`):
		return "entity"
	}
	return ""
}

// devTaskStem reduces a file name to its concept tokens by dropping layer words,
// so FooController / FooService / FooDao collapse to the same stem "foo".
func devTaskStem(rel string) string {
	base := pathBase(rel)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var keep []string
	for _, t := range splitIdent(base) {
		lt := strings.ToLower(t)
		if lt == "" || isDigits(lt) || devTaskRoleNoise[lt] {
			continue
		}
		keep = append(keep, lt)
	}
	return strings.Join(keep, "")
}

// devTaskRoleChain picks real evidence files illustrating an end-to-end change
// path across generic layers. It prefers a chain sharing one concept stem
// (FooController -> FooService -> FooDao -> Foo), and falls back to one
// representative file per role. Returns nil when fewer than two roles are
// evidenced, so the cookbook page is never planned as an empty shell.
func devTaskRoleChain(model *scan.Model) []string {
	if model == nil || len(model.Files) == 0 {
		return nil
	}
	vd := evidence.NewVendorDetector(model, nil)
	byRole := map[string][]string{}
	byStem := map[string]map[string]string{}
	for _, f := range model.Files {
		orig := filepath.ToSlash(f.RelativePath)
		// IsVendor assumes noise paths were already dropped during scan, so
		// re-apply IsNoisePath here: a stray node_modules/**/router.js must not
		// be able to evidence an api layer.
		if orig == "" || scan.IsNoisePath(orig) || vd.IsVendor(orig) {
			continue
		}
		if !devTaskCodeExt[strings.ToLower(filepath.Ext(orig))] {
			continue
		}
		role := devTaskRoleOf(orig)
		if role == "" {
			continue
		}
		byRole[role] = append(byRole[role], orig)
		if stem := devTaskStem(orig); stem != "" {
			if byStem[stem] == nil {
				byStem[stem] = map[string]string{}
			}
			if _, seen := byStem[stem][role]; !seen {
				byStem[stem][role] = orig
			}
		}
	}
	for _, paths := range byRole {
		sort.Strings(paths)
	}
	// Best same-stem chain: most roles covered, ties broken by stem name for
	// deterministic output.
	bestStem, bestCount := "", 0
	stems := make([]string, 0, len(byStem))
	for s := range byStem {
		stems = append(stems, s)
	}
	sort.Strings(stems)
	for _, s := range stems {
		if n := len(byStem[s]); n > bestCount {
			bestStem, bestCount = s, n
		}
	}
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	if bestCount >= 2 {
		for _, role := range devTaskRoleKinds {
			add(byStem[bestStem][role])
		}
	}
	// Top up with one representative per role still missing from the chain.
	roles := 0
	for _, role := range devTaskRoleKinds {
		if len(byRole[role]) == 0 {
			continue
		}
		roles++
		if bestCount >= 2 && byStem[bestStem][role] != "" {
			continue
		}
		add(byRole[role][0])
	}
	if roles < 2 {
		return nil
	}
	return out
}

// multiManifestRoots returns the non-"." manifest roots when the repo is truly
// multi-root (>=2 of them); otherwise nil.
func multiManifestRoots(model *scan.Model) []string {
	if model == nil {
		return nil
	}
	var roots []string
	for _, r := range model.ManifestRoots {
		r = strings.TrimSpace(filepath.ToSlash(r))
		if r == "" || r == "." {
			continue
		}
		roots = append(roots, r)
	}
	if len(roots) < 2 {
		return nil
	}
	return roots
}

// pathBase returns the last slash-separated segment of a repo-relative path.
func pathBase(p string) string {
	p = strings.TrimSuffix(filepath.ToSlash(p), "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// owningRootBase returns the basename of the deepest manifest root containing
// rel, or "" when rel is under none of the given roots.
func owningRootBase(rel string, roots []string) string {
	rel = filepath.ToSlash(rel)
	best := ""
	for _, r := range roots {
		if rel == r || strings.HasPrefix(rel, r+"/") {
			if len(r) > len(best) {
				best = r
			}
		}
	}
	if best == "" {
		return ""
	}
	return pathBase(best)
}

// buildDefaultTreeSplit returns foundation pages and technical skeleton separately
// so dual-rail budgets can be applied independently.
func buildDefaultTreeSplit(model *scan.Model, zh bool, techBudget int) (foundation, technical []Planned) {
	all := buildDefaultTree(model, zh, techBudget)
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

// buildBusinessDomains clusters code by package/path segments discovered in the
// scan — no hard-coded product domains. Titles are humanized from real path tokens.
// bizBudget scales the section cap: clamp(bizBudget/4, 12, 24).
// Multi-root repos prefix cluster keys with the owning manifest-root basename so
// same-named packages in different roots stay separate.
func buildBusinessDomains(model *scan.Model, zh bool, bizBudget int, vd *evidence.VendorDetector) []Planned {
	if model == nil {
		return nil
	}
	if vd == nil {
		vd = evidence.NewVendorDetector(model, nil)
	}
	roots := multiManifestRoots(model)
	// Group code files by packageDomain (last meaningful path segment).
	bySeg := map[string][]fileBag{}
	for _, f := range model.Files {
		if !scan.IsCodeFile(f.RelativePath) || scan.IsNoisePath(f.RelativePath) {
			continue
		}
		// Committed third-party library files must never seed business domains
		// (they produce filename-mirror pages like「Encutf16管理」).
		if vd.IsVendor(f.RelativePath) {
			continue
		}
		// Prefer application code; skip pure tests for domain clustering.
		rel := strings.ToLower(filepath.ToSlash(f.RelativePath))
		if match(rel, `(^|/)(test|tests|__tests__|spec)/`) {
			continue
		}
		seg := packageDomain(f.RelativePath)
		if seg == "" {
			continue
		}
		// Skip ultra-generic shells
		if match(strings.ToLower(seg), `^(common|util|utils|core|base|shared|internal|pkg|lib|main|app|api|web|config|constant|constants|exception|error|errors)$`) {
			continue
		}
		// Multi-root repos: qualify the cluster key by owning manifest root so
		// e.g. services/billing/order and services/orders/order do not merge.
		if len(roots) >= 2 {
			if rb := owningRootBase(f.RelativePath, roots); rb != "" && !strings.EqualFold(rb, seg) {
				seg = rb + "/" + seg
			}
		}
		stem := strings.TrimSuffix(filepath.Base(f.RelativePath), filepath.Ext(f.RelativePath))
		bySeg[seg] = append(bySeg[seg], fileBag{f, stem})
	}

	type sk struct {
		k string
		n int
	}
	var order []sk
	for k, files := range bySeg {
		if len(files) < 3 {
			continue
		}
		order = append(order, sk{k, len(files)})
	}
	sort.Slice(order, func(a, b int) bool {
		if order[a].n != order[b].n {
			return order[a].n > order[b].n
		}
		return order[a].k < order[b].k
	})
	// Cap sections so dual-rail budget stays healthy; scale with the business budget.
	sectionCap := bizBudget / 4
	if sectionCap < 12 {
		sectionCap = 12
	}
	if sectionCap > 24 {
		sectionCap = 24
	}
	if len(order) > sectionCap {
		order = order[:sectionCap]
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

	for _, o := range order {
		seg := o.k
		files := bySeg[seg]
		// Section title from path token only — project-agnostic. Root-qualified
		// keys ("root/domain") flow through humanize/composeChineseTitle as tokens.
		titleStem := strings.ReplaceAll(seg, "/", " ")
		secCore := humanize(titleStem)
		if zh {
			secCore = composeChineseTitle(titleStem, "module")
		}
		if isLowQuality(secCore) {
			secCore = humanize(titleStem)
		}
		sec := secCore
		if zh && !strings.HasSuffix(sec, "模块") && !strings.HasSuffix(sec, "服务") && !strings.HasSuffix(sec, "能力") {
			sec = sec + "模块"
		}
		overview := sec
		if zh {
			overview = secCore + "概述"
		} else {
			overview = secCore + " Overview"
		}

		add(Planned{
			Title: overview, Section: sec, Category: "business",
			Goal: goal(zh,
				"从仓库路径与代码说明「"+sec+"」的职责边界、主要对象与协作关系。",
				"Explain the responsibilities and collaborators of "+sec+" as found in the codebase."),
			ContentPath: sec + "/" + safeSegment(overview) + ".md",
			Level:       "Intermediate",
		})

		// Child pages from dominant stems under this segment.
		// Child limit scales with cluster size: 3 + size/6, clamped to [3, 8].
		childLimit := 3 + len(files)/6
		if childLimit < 3 {
			childLimit = 3
		}
		if childLimit > 8 {
			childLimit = 8
		}
		stems := dominantStemsFrom(files, childLimit)
		for _, st := range stems {
			title := humanize(st)
			if zh {
				title = composeChineseTitle(st, "capability")
			}
			if title == overview || title == sec || isLowQuality(title) {
				continue
			}
			// Avoid near-duplicate of section name
			if strings.EqualFold(strings.TrimSpace(title), strings.TrimSpace(secCore)) {
				continue
			}
			safe := safeSegment(title)
			add(Planned{
				Title: title, Section: sec, Group: sec, Parent: overview, Category: "business",
				Goal: goal(zh,
					"说明「"+title+"」在仓库中的实现落点、关键流程与依赖。",
					"Explain how "+title+" is implemented and which code it touches."),
				ContentPath: sec + "/" + safe + ".md",
				Level:       "Advanced",
			})
		}
	}
	return out
}

func dominantStemsFrom(files []fileBag, limit int) []string {
	counts := map[string]int{}
	for _, f := range files {
		clean := regexp.MustCompile(`(?i)(Controller|Control|Comp|ServiceImpl|Service|Dao|Mapper|Model|Entity|Po|Dto|Vo|Impl|Handler|Resource|Action)$`).ReplaceAllString(f.stem, "")
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
		// Drop pure technical noise keys
		if match(strings.ToLower(key), `^(base|abstract|common|util|utils|config|constant|exception)$`) {
			continue
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
func buildInventory(model *scan.Model, zh bool, budget int, vd *evidence.VendorDetector) []Planned {
	if model == nil || budget <= 0 {
		return nil
	}
	if vd == nil {
		vd = evidence.NewVendorDetector(model, nil)
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
		if scan.IsNoisePath(f.RelativePath) || vd.IsVendor(f.RelativePath) {
			continue
		}
		seg := packageDomain(f.RelativePath)
		if seg == "" {
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
		if !scan.IsCodeFile(f.RelativePath) || scan.IsNoisePath(f.RelativePath) || vd.IsVendor(f.RelativePath) {
			continue
		}
		stem := strings.TrimSuffix(filepath.Base(f.RelativePath), filepath.Ext(f.RelativePath))
		if match(stem, `^(Base|Abstract|Common|Entity|Model|Dto|Vo|DataEntity)$`) {
			continue
		}
		seg := packageDomain(f.RelativePath)
		if seg == "" {
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
// Pure-CJK titles fall back to a readable role prefix + short hash (not opaque topic-only).
func DescriptionSlug(title string) string {
	if s, ok := titleSlugs[title]; ok {
		return s
	}
	// Strip common Chinese role suffixes to help ascii fallback
	t := title
	for _, pair := range [][2]string{
		{"模块", "-module"}, {"服务", "-service"}, {"接口", "-api"},
		{"设计", "-design"}, {"文档", "-docs"}, {"管理", "-mgmt"},
		{"能力", "-capability"}, {"流程", "-flow"}, {"架构", "-arch"},
		{"配置", "-config"}, {"部署", "-deploy"}, {"运维", "-ops"},
		{"安全", "-security"}, {"测试", "-test"}, {"数据", "-data"},
		{"模型", "-model"}, {"总览", "-overview"}, {"概述", "-overview"},
		{"指南", "-guide"}, {"策略", "-strategy"}, {"规范", "-spec"},
	} {
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
	// Accept ascii slug only when it has enough identity (not just a bare role suffix).
	if s != "" && hasASCIILetter(s) && !isBareRoleSlug(s) {
		return s
	}
	// Readable hybrid: engineering role prefix + short hash (stable, no product lexicon).
	return slugRolePrefix(title) + "-" + strings.ToLower(itohex(stableHash(title)))
}

func hasASCIILetter(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			return true
		}
	}
	return false
}

// isBareRoleSlug is true when ascii residue is only generic role token(s)
// (e.g. "mgmt", "api", "mgmt-capability", "model-model-mgmt") from CJK suffix strip.
func isBareRoleSlug(s string) bool {
	role := map[string]bool{
		"mgmt": true, "api": true, "module": true, "service": true, "design": true,
		"docs": true, "capability": true, "flow": true, "arch": true, "config": true,
		"deploy": true, "ops": true, "security": true, "test": true, "data": true,
		"model": true, "overview": true, "guide": true, "strategy": true, "spec": true,
		"svc": true, "mod": true, "cfg": true, "sec": true, "page": true,
	}
	parts := strings.Split(s, "-")
	if len(parts) == 0 {
		return true
	}
	for _, p := range parts {
		if p == "" {
			continue
		}
		if !role[p] {
			return false
		}
	}
	return true
}

func stableHash(title string) int {
	h := 0
	for _, r := range title {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return h
}

// slugRolePrefix picks a short English role label from generic title cues only.
func slugRolePrefix(title string) string {
	switch {
	case strings.Contains(title, "架构"):
		return "arch"
	case strings.Contains(title, "接口") || strings.Contains(title, "API"):
		return "api"
	case strings.Contains(title, "数据") || strings.Contains(title, "模型") || strings.Contains(title, "实体"):
		return "data"
	case strings.Contains(title, "部署") || strings.Contains(title, "运维"):
		return "ops"
	case strings.Contains(title, "安全") || strings.Contains(title, "权限") || strings.Contains(title, "认证"):
		return "sec"
	case strings.Contains(title, "配置"):
		return "cfg"
	case strings.Contains(title, "测试"):
		return "test"
	case strings.Contains(title, "故障") || strings.Contains(title, "排查"):
		return "troubleshoot"
	case strings.Contains(title, "流程") || strings.Contains(title, "工作流"):
		return "flow"
	case strings.Contains(title, "服务"):
		return "svc"
	case strings.Contains(title, "模块"):
		return "mod"
	case strings.Contains(title, "管理"):
		return "mgmt"
	case strings.Contains(title, "指南") || strings.Contains(title, "概述") || strings.Contains(title, "总览"):
		return "guide"
	default:
		return "page"
	}
}

var titleSlugs = map[string]string{
	"项目概述": "project-overview", "快速开始": "getting-started",
	"系统架构设计": "system-architecture", "整体架构设计": "overall-architecture",
	"模块划分原则": "module-boundaries", "数据流架构": "data-flow-architecture",
	"组件交互关系": "component-interactions", "部署架构": "deployment-architecture",
	"核心模块详解": "core-modules", "开发指南": "developer-guide",
	"目录结构": "directory-structure", "技术栈与依赖": "tech-stack-and-dependencies",
	"API接口文档": "api-documentation", "配置管理": "configuration-management",
	"安全与访问控制": "security-and-access-control",
	"部署与运维": "deployment-and-operations",
	"故障排查": "troubleshooting", "数据模型设计": "data-model-design",
	"数据库设计": "database-design", "接口文档": "api-docs", "架构设计": "architecture-design",
	"核心业务模块": "core-business-modules", "测试与质量": "testing-and-quality",
	"性能与扩展": "performance-and-scalability",
	"常见问题": "faq", "附录": "appendix",
	"编码规范": "coding-standards", "典型开发任务": "typical-development-tasks",
	"Project Overview": "project-overview",
	"Getting Started":  "getting-started", "System Architecture": "system-architecture",
	"Core Modules": "core-modules", "API Documentation": "api-documentation",
	"Data Model Design": "data-model-design", "Deployment and Operations": "deployment-and-operations",
	"Troubleshooting": "troubleshooting", "Security Design": "security-design",
	"Developer Guide": "developer-guide", "Testing and quality": "testing-and-quality",
	"Security and access control": "security-and-access-control",
	"Configuration": "configuration", "FAQ": "faq", "Appendix": "appendix",
	"Performance and scalability": "performance-and-scalability",
	"Coding standards": "coding-standards",
	"Typical development tasks": "typical-development-tasks",
}

func composeChineseTitle(stem, kind string) string {
	cleaned := regexp.MustCompile(`(?i)(Controller|Control|Comp|Resource|Endpoint|Handler|Servlet|ServiceImpl|Service|Action|Req|Resp|Request|Response|Bean|Util|Helper|Manager|Impl|Dao|Mapper|Model|Entity|Po|Dto|Vo)$`).ReplaceAllString(stem, "")
	tokens := splitIdent(cleaned)
	noise := map[string]bool{
		"service": true, "svc": true, "controller": true, "control": true, "comp": true,
		"handler": true, "resource": true, "action": true, "impl": true, "util": true,
		"helper": true, "manager": true, "common": true, "base": true, "abstract": true,
		"sample": true, "test": true, "api": true, "web": true, "rest": true,
		"g": true, "dao": true, "mapper": true, "com": true, "org": true,
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
		for _, t := range tokens {
			filtered = append(filtered, strings.ToLower(t))
		}
	}
	// Generic composition only: join humanized tokens + kind suffix. No product lexicon.
	core := humanize(strings.Join(filtered, " "))
	if core == "" {
		core = humanize(stem)
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
