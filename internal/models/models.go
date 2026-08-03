package models

import (
	"fmt"
	"regexp"
	"strings"
)

// WikiPage represents a single documentation page.
type WikiPage struct {
	Title   string `json:"title"`
	Slug    string `json:"slug"`
	Level   string `json:"level"` // Beginner / Intermediate / Advanced
	Section string `json:"section"`
	Group   string `json:"group,omitempty"` // optional

	// Extra generation fields (parent/goal/evidence/path).
	Parent          string   `json:"parent,omitempty"`
	Goal            string   `json:"goal,omitempty"`
	DependentFiles  []string `json:"dependent_files,omitempty"`
	ContentPath     string   `json:"content_path,omitempty"` // path under content/
	DescriptionSlug string   `json:"description_slug,omitempty"`
	// Track selects dual-rail documentation style:
	// foundation (onboarding), business (capabilities/process), technical (arch/API/data/ops).
	Track string `json:"track,omitempty"`
}

// Wiki is the full catalog of documentation pages.
type Wiki struct {
	Pages []WikiPage `json:"pages"`
}

// Track constants for dual-rail documentation.
const (
	TrackFoundation = "foundation" // overview, quick start, reading paths
	TrackBusiness   = "business"   // business capabilities & processes
	TrackTechnical  = "technical"  // architecture, API, data, ops, security
)

// FormatCatalog renders the catalog as markdown navigation, marking currentSlug.
// Pages are grouped by track (入门 / 业务 / 技术) then by section for dual-rail reading.
func (w *Wiki) FormatCatalog(currentSlug string) string {
	if w == nil || len(w.Pages) == 0 {
		return ""
	}
	order := []string{TrackFoundation, TrackBusiness, TrackTechnical, ""}
	labels := map[string]string{
		TrackFoundation: "入门与总览 (Foundation)",
		TrackBusiness:   "业务能力 (Business)",
		TrackTechnical:  "技术参考 (Technical)",
		"":              "其他",
	}
	// Detect English-ish titles for label language (heuristic).
	en := false
	for _, p := range w.Pages {
		if p.Title == "Project Overview" || p.Title == "Getting Started" {
			en = true
			break
		}
	}
	if en {
		labels = map[string]string{
			TrackFoundation: "Foundation",
			TrackBusiness:   "Business capabilities",
			TrackTechnical:  "Technical reference",
			"":              "Other",
		}
	}

	byTrack := map[string][]WikiPage{}
	for _, p := range w.Pages {
		tr := p.Track
		if tr != TrackFoundation && tr != TrackBusiness && tr != TrackTechnical {
			tr = InferTrack(p)
		}
		byTrack[tr] = append(byTrack[tr], p)
	}

	var lines []string
	for _, tr := range order {
		pages := byTrack[tr]
		if len(pages) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("### %s", labels[tr]))
		curSection := ""
		curGroup := ""
		for _, p := range pages {
			if p.Section != curSection {
				curSection = p.Section
				curGroup = ""
				lines = append(lines, fmt.Sprintf("- **%s**", p.Section))
			}
			if p.Group != "" && p.Group != curGroup {
				curGroup = p.Group
				lines = append(lines, fmt.Sprintf("  - *%s*", p.Group))
			}
			indent := "  "
			if p.Group != "" {
				indent = "    "
			}
			marker := ""
			if p.Slug == currentSlug {
				marker = " [You are currently here]"
			}
			// Prefer ContentPath / Title.md so LLM catalog context and nav links
			// resolve on disk. Bare counter slugs (e.g. "50-50") break browsers.
			lines = append(lines, fmt.Sprintf("%s- [%s](%s)%s", indent, p.Title, catalogLinkTarget(p), marker))
		}
		lines = append(lines, "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// catalogLinkTarget is a stable, resolvable relative path for catalog navigation.
// ContentPath (under content/) wins; otherwise Title.md. Slug is never used alone
// because CJK titles degrade to "N-N" via MakeSlug and are not real file names.
func catalogLinkTarget(p WikiPage) string {
	if cp := strings.TrimSpace(p.ContentPath); cp != "" {
		cp = strings.TrimPrefix(cp, "content/")
		cp = strings.TrimPrefix(cp, "/content/")
		return strings.ReplaceAll(cp, "\\", "/")
	}
	if t := strings.TrimSpace(p.Title); t != "" {
		return t + ".md"
	}
	if p.Slug != "" {
		return p.Slug
	}
	return "page.md"
}

// RelatedCrossTrack returns pages on the opposite rail that share title/section tokens.
func (w *Wiki) RelatedCrossTrack(page WikiPage, limit int) []WikiPage {
	if w == nil || limit <= 0 {
		return nil
	}
	src := page.Track
	if src == "" {
		src = InferTrack(page)
	}
	want := TrackTechnical
	if src == TrackTechnical {
		want = TrackBusiness
	} else if src == TrackFoundation {
		// Foundation links lightly to both; prefer business then technical.
		want = TrackBusiness
	}
	tokens := tokeniseTitle(page.Title + " " + page.Section)
	type scored struct {
		p WikiPage
		n int
	}
	var list []scored
	for _, p := range w.Pages {
		if p.Slug == page.Slug || p.Title == page.Title {
			continue
		}
		tr := p.Track
		if tr == "" {
			tr = InferTrack(p)
		}
		// foundation may collect from either rail
		if src != TrackFoundation && tr != want {
			// also allow foundation pages as soft links
			if tr != TrackFoundation {
				continue
			}
		}
		n := overlapTokens(tokens, tokeniseTitle(p.Title+" "+p.Section+" "+p.Goal))
		if n <= 0 {
			continue
		}
		list = append(list, scored{p, n})
	}
	// sort by score desc
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].n > list[i].n {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	var out []WikiPage
	for _, s := range list {
		out = append(out, s.p)
		if len(out) >= limit {
			break
		}
	}
	// If business page has no technical match, link technical index pages (CN+EN).
	if len(out) == 0 && src == TrackBusiness {
		for _, p := range w.Pages {
			tr := p.Track
			if tr == "" {
				tr = InferTrack(p)
			}
			if tr != TrackTechnical {
				continue
			}
			if isTechnicalIndexPage(p) {
				out = append(out, p)
				if len(out) >= limit {
					break
				}
			}
		}
	}
	// Symmetric: technical page with no business hit → foundation overview if present.
	if len(out) == 0 && src == TrackTechnical {
		for _, p := range w.Pages {
			tr := p.Track
			if tr == "" {
				tr = InferTrack(p)
			}
			if tr == TrackFoundation {
				out = append(out, p)
				if len(out) >= limit {
					break
				}
			}
		}
	}
	return out
}

// isTechnicalIndexPage detects architecture/API/data/ops index pages in CN or EN.
func isTechnicalIndexPage(p WikiPage) bool {
	// Section-level index: title equals section, or section/title carries tech labels.
	if p.Title != "" && p.Title == p.Section {
		return true
	}
	hay := p.Section + " " + p.Title
	keys := []string{
		"接口", "API", "数据", "架构", "部署", "运维", "安全", "配置", "故障",
		"Architecture", "Deploy", "Deployment", "Security", "Database", "Data Model",
		"Configuration", "Troubleshooting", "Operations", "Ops", "Developer",
		"Core Modules", "System Architecture", "API Documentation",
	}
	for _, k := range keys {
		if strings.Contains(hay, k) {
			return true
		}
	}
	return isTechnicalSection(p.Section)
}

// InferTrack assigns foundation | business | technical from section/title when Track is empty or invalid.
// Exported so planner/runner share one rule set after free LLM catalogs.
//
// Policy:
//   - Empty/invalid Track → full classification.
//   - Already-valid Track is kept, except strong foundation cues promote business→foundation
//     (LLM free catalogs often mislabel 入门/总览/阅读路径 as business).
//   - Never demotes technical → foundation solely on soft "总览" cues when the topic is eng-heavy.
func InferTrack(p WikiPage) string {
	sec := strings.TrimSpace(p.Section)
	title := strings.TrimSpace(p.Title)
	goal := strings.TrimSpace(p.Goal)

	// Capability leaves under orientation sections (e.g. 项目概述/客户关系管理能力概览)
	// must stay on the business rail — never inherit foundation from the container alone.
	if isCapabilityLeafTitle(title) {
		if isTechnicalSection(sec) || isTechnicalSection(title) {
			return TrackTechnical
		}
		return TrackBusiness
	}

	// Strong foundation cues promote any mislabeled rail (LLM free catalogs stamp
	// 入门/阅读路径 as business).
	if isFoundationCue(sec, title, goal) {
		return TrackFoundation
	}

	// Stale foundation labels: only keep when the leaf is still orientation.
	// (Previous polishes / free catalogs sometimes painted eng/business leaves foundation.)
	if p.Track == TrackFoundation {
		// fall through to reclassify
	} else if p.Track == TrackBusiness || p.Track == TrackTechnical {
		// Preserve business/technical when no foundation cue applies.
		return p.Track
	}

	if isTechnicalSection(sec) || isTechnicalSection(title) {
		return TrackTechnical
	}
	if isBusinessSection(sec) {
		return TrackBusiness
	}
	// Title cues for capability pages (CN 管理, EN Management/Process).
	if strings.Contains(title, "管理") && !strings.Contains(title, "配置管理") {
		return TrackBusiness
	}
	if matchFold(title, "management") || matchFold(title, "capability") || matchFold(title, "process") {
		return TrackBusiness
	}
	// Domain "…业务总览" under a module section → business.
	if strings.Contains(title, "业务总览") || strings.Contains(title, "业务概述") {
		return TrackBusiness
	}
	return TrackTechnical
}

// isCapabilityLeafTitle detects domain capability pages that often sit under a
// foundation container section (项目概述 / 快速开始) but are not onboarding.
// Generic engineering vocabulary only — no product-domain lexicon.
func isCapabilityLeafTitle(title string) bool {
	t := strings.TrimSpace(title)
	if t == "" {
		return false
	}
	// Repo-level maps / positioning stay foundation.
	if strings.Contains(t, "全景") || strings.Contains(t, "定位") || strings.Contains(t, "简介") ||
		strings.Contains(t, "阅读路径") || strings.Contains(t, "推荐阅读") ||
		strings.Contains(t, "适用场景") || strings.Contains(t, "导读") ||
		strings.Contains(t, "快速开始") || strings.Contains(t, "快速上手") ||
		strings.Contains(t, "入门") || matchFold(t, "getting started") ||
		matchFold(t, "introduction") || matchFold(t, "onboarding") {
		return false
	}
	// "…能力概览/概述" with a domain stem (客户关系管理能力概览).
	if strings.Contains(t, "能力概览") || strings.Contains(t, "能力概述") ||
		strings.Contains(t, "Capability Overview") || strings.Contains(t, "capability overview") {
		return true
	}
	// Capability / process leaves without orientation words.
	if strings.Contains(t, "业务域") {
		return true
	}
	return false
}

// isFoundationCue reports onboarding / orientation topics (dual-rail foundation).
// Generic engineering words only — no product-domain lexicon.
func isFoundationCue(sec, title, goal string) bool {
	if isCapabilityLeafTitle(title) {
		return false
	}
	// Title itself is an orientation page.
	if isFoundationSection(title) || isOrientationTitle(title) {
		return true
	}
	// Goal-only orientation (rare).
	if isOrientationTitle(goal) {
		return true
	}
	// Foundation container section: inherit foundation for leaves that are not
	// clearly technical/eng indexes. Capability leaves already returned false.
	// This keeps short synthetic titles under 项目概述 on the foundation rail
	// (planner fixtures) while still letting "部署/接口/架构…" leaves escape.
	if isFoundationSection(sec) {
		if isTechnicalSection(title) || isStrongTechnicalTopic(title) {
			return false
		}
		if strings.Contains(title, "业务总览") || strings.Contains(title, "业务概述") {
			return false
		}
		return true
	}
	blob := sec + " " + title + " " + goal
	if strings.TrimSpace(blob) == "" {
		return false
	}
	// Strong onboarding phrases on title/goal (not bare section container words alone).
	strong := []string{
		"快速开始", "快速上手", "入门", "阅读路径", "推荐阅读",
		"适用场景", "平台定位", "导读", "读者路径", "仓库定位",
		"Getting Started", "Get Started", "Project Overview", "Introduction",
		"Onboarding", "Reading Path", "Read Path", "Who This Is For",
	}
	tg := title + " " + goal
	for _, k := range strong {
		if strings.Contains(tg, k) {
			return true
		}
	}
	// Soft: bare 总览 / Overview — only for pure onboarding blobs.
	// Capability clusters like "Billing Overview" / "订单概述" stay business.
	if isSoftOverviewOnboarding(blob) {
		return true
	}
	return false
}

// isOrientationTitle is true for leaf titles that belong on the foundation rail.
func isOrientationTitle(title string) bool {
	t := strings.TrimSpace(title)
	if t == "" {
		return false
	}
	if isFoundationSection(t) {
		return true
	}
	keys := []string{
		"定位", "适用场景", "阅读路径", "推荐阅读", "导读", "读者路径",
		"简介", "术语表", "版本说明", "交付形态", "运行前提", "环境准备",
		"源码获取", "本地构建", "本地运行", "首次登录",
		"可运行配置", "快速开始", "快速上手", "入门",
		"全景", "业务边界", "能力地图", "能力全景",
		"Getting Started", "Introduction", "Onboarding", "Reading Path",
		"Who This Is For", "Prerequisites", "Quick Start", "Capability Map",
	}
	for _, k := range keys {
		if strings.Contains(t, k) || matchFold(t, k) {
			return true
		}
	}
	// "应用启动" is orientation only without framework/tech stems (ExtJS/app.js…).
	if strings.Contains(t, "应用启动") && !isStrongTechnicalTopic(t) &&
		!strings.Contains(t, "Ext") && !strings.Contains(t, "app.js") &&
		!matchFold(t, "spring") {
		return true
	}
	// "开发模式" alone is onboarding; with resource/build tech stays technical via other rails.
	if strings.Contains(t, "开发模式") && !isStrongTechnicalTopic(t) {
		return true
	}
	// Pure overview labels at title level.
	if isSoftOverviewOnboarding(t) {
		return true
	}
	return false
}

// isSoftOverviewOnboarding is true for repo-level orientation overviews only.
// "Foo Overview" / "Foo概述" with a non-empty domain prefix → capability cluster.
func isSoftOverviewOnboarding(blob string) bool {
	blob = strings.TrimSpace(blob)
	if blob == "" {
		return false
	}
	if isStrongTechnicalTopic(blob) {
		return false
	}
	// Domain capability / business stems mean this is not pure repo orientation.
	// e.g. "知识管理平台业务总览" contains 平台+总览 but is a capability page.
	if strings.Contains(blob, "业务") || strings.Contains(blob, "能力") ||
		strings.Contains(blob, "模块") || matchFold(blob, "billing") ||
		matchFold(blob, "order") || matchFold(blob, "payment") {
		// Exception: pure "业务定位/业务边界" orientation phrases.
		if !(strings.Contains(blob, "业务定位") || strings.Contains(blob, "业务边界") ||
			strings.Contains(blob, "业务域全景")) {
			return false
		}
	}
	// Pure overview labels.
	pure := []string{"总览", "概述", "Overview", "overview"}
	for _, p := range pure {
		if strings.EqualFold(blob, p) {
			return true
		}
	}
	// "项目总览" / "平台总览" / "仓库 Overview" etc. without a capability stem.
	// Require the orientation noun to dominate (starts with / is near 项目|平台|仓库).
	if strings.HasPrefix(blob, "项目") || strings.HasPrefix(blob, "平台") || strings.HasPrefix(blob, "仓库") ||
		matchFold(blob, "project overview") || matchFold(blob, "platform overview") || matchFold(blob, "repo overview") {
		if strings.Contains(blob, "总览") || strings.Contains(blob, "概述") || matchFold(blob, "overview") {
			return true
		}
	}
	// Short phrases like "项目总览" / "平台总览" as whole-ish tokens.
	for _, pref := range []string{"项目总览", "平台总览", "仓库总览", "项目概述", "平台概述", "仓库概述",
		"入门总览", "系统总览", "整体总览", "全局总览", "整体概述", "全局概述"} {
		if strings.Contains(blob, pref) {
			return true
		}
	}
	return false
}

func isFoundationSection(s string) bool {
	if s == "" {
		return false
	}
	switch s {
	case "项目概述", "快速开始", "平台入门与总览", "入门与总览",
		"Project Overview", "Getting Started", "Get Started", "Overview", "Introduction":
		return true
	}
	// Section names that are clearly onboarding containers.
	if strings.Contains(s, "入门") || strings.Contains(s, "快速开始") || strings.Contains(s, "快速上手") {
		return true
	}
	if strings.Contains(s, "阅读路径") || strings.Contains(s, "推荐阅读") {
		return true
	}
	if matchFold(s, "getting started") || matchFold(s, "project overview") || matchFold(s, "onboarding") {
		return true
	}
	// Soft section-level overview: only pure onboarding containers.
	if isSoftOverviewOnboarding(s) {
		return true
	}
	return false
}

// isStrongTechnicalTopic is used to keep eng indexes on the technical rail
// even when the title contains soft foundation words like 总览/Overview.
func isStrongTechnicalTopic(s string) bool {
	if s == "" {
		return false
	}
	keys := []string{
		"接口", "API", "安全", "部署", "运维", "测试", "数据访问", "数据库", "缓存",
		"日志", "工作流", "ETL", "MyBatis", "Spring", "Controller", "Service",
		"Mapper", "Filter", "Interceptor", "Docker", "K8s", "CI", "CD",
		"Architecture", "Deploy", "Security", "Database", "Workflow",
	}
	for _, k := range keys {
		if strings.Contains(s, k) || matchFold(s, k) {
			return true
		}
	}
	return false
}

func isTechnicalSection(s string) bool {
	if s == "" {
		return false
	}
	// Core framework / engineering labels stay technical even if they contain 模块.
	if strings.Contains(s, "核心业务") || strings.Contains(s, "核心模块") ||
		strings.Contains(s, "Core Module") || strings.Contains(s, "Core Modules") {
		return true
	}
	keys := []string{
		"接口", "API", "数据", "架构", "部署", "运维", "安全", "开发", "故障", "配置",
		"测试与质量", "技术栈", "目录结构",
		"Architecture", "Deploy", "Security", "Developer", "Database", "Data Model",
		"Configuration", "Troubleshooting", "Testing", "Operations", "Ops",
	}
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// isBusinessSection identifies capability-module sections (not core-framework dumps).
// Works for CN (…模块), EN (…Management), and path-derived short ASCII tokens (Billing, Cust).
// Free-form Chinese orientation sections (入门/总览/… without 模块) must NOT fall here —
// they are foundation via isFoundationCue / isFoundationSection.
func isBusinessSection(sec string) bool {
	if sec == "" || isFoundationSection(sec) || isTechnicalSection(sec) {
		return false
	}
	if isFoundationCue(sec, "", "") {
		return false
	}
	if strings.Contains(sec, "模块") {
		// 核心模块 stays technical (handled above via isTechnicalSection).
		return true
	}
	if strings.Contains(sec, "Management") || strings.Contains(sec, "Monitoring") ||
		strings.Contains(sec, "Workflow") || strings.Contains(sec, "Audit") ||
		strings.Contains(sec, "Risk") || strings.Contains(sec, "Capability") ||
		strings.Contains(sec, "Domain") || strings.Contains(sec, "Business") {
		return true
	}
	// Path-clustering style: "Foo Overview" / "Foo概述" — capability cluster, not repo onboarding.
	if strings.HasSuffix(sec, " Overview") || strings.HasSuffix(sec, "概述") {
		// Pure onboarding overviews already returned false via isFoundationCue.
		return true
	}
	// Short package-like section names: ASCII path tokens only (Cust, Billing).
	// Chinese multi-character phrases without spaces used to false-positive as business
	// (e.g. 平台入门与总览) — require mostly-ASCII for this heuristic.
	runes := []rune(sec)
	if len(runes) >= 2 && len(runes) <= 40 && !strings.Contains(sec, "/") && mostlyASCII(sec) {
		if !strings.Contains(sec, " ") || isTitleCasePhrase(sec) {
			return true
		}
	}
	return false
}

func mostlyASCII(s string) bool {
	if s == "" {
		return false
	}
	ascii, total := 0, 0
	for _, r := range s {
		if r == ' ' || r == '-' || r == '_' {
			continue
		}
		total++
		if r <= 127 {
			ascii++
		}
	}
	return total > 0 && ascii*100/total >= 70
}

func matchFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

func isTitleCasePhrase(s string) bool {
	parts := strings.Fields(s)
	if len(parts) == 0 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		r := []rune(p)
		if r[0] >= 'a' && r[0] <= 'z' {
			return false
		}
	}
	return true
}

func tokeniseTitle(s string) []string {
	s = strings.ToLower(s)
	// split Han runs and ascii words
	var out []string
	var cur strings.Builder
	flush := func() {
		t := cur.String()
		cur.Reset()
		if len([]rune(t)) >= 2 {
			out = append(out, t)
		}
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
			continue
		}
		if r >= 0x4e00 && r <= 0x9fff {
			flush()
			// bigrams later; keep single CJK as soft tokens of length>=2 via window
			cur.WriteRune(r)
			// don't flush every char — accumulate
			continue
		}
		flush()
	}
	flush()
	// also emit 2-gram for pure CJK title
	runes := []rune(strings.ReplaceAll(s, " ", ""))
	var han []rune
	for _, r := range runes {
		if r >= 0x4e00 && r <= 0x9fff {
			han = append(han, r)
		}
	}
	for i := 0; i+1 < len(han); i++ {
		out = append(out, string(han[i:i+2]))
	}
	return out
}

func overlapTokens(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, t := range b {
		set[t] = true
	}
	n := 0
	seen := map[string]bool{}
	for _, t := range a {
		if set[t] && !seen[t] {
			seen[t] = true
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Slug generation
// ---------------------------------------------------------------------------

var (
	reNonAlnum      = regexp.MustCompile(`[^a-z0-9]+`)
	reLeadTrailDash = regexp.MustCompile(`^-+|-+$`)
)

// slugify converts a title to a URL-safe ASCII slug.
// Non-ASCII characters (e.g. Chinese) are replaced, resulting in "" which
// triggers the numeric fallback in MakeSlug — matching Python's behavior
// with allow_unicode=False.
func slugify(title string) string {
	result := strings.ToLower(title)
	result = reNonAlnum.ReplaceAllString(result, "-")
	result = reLeadTrailDash.ReplaceAllString(result, "")
	return result
}

// MakeSlug creates a numbered slug like "1-overview" or "2-2" (for non-ASCII titles).
func MakeSlug(counter int, title string) string {
	part := slugify(title)
	if part == "" {
		part = fmt.Sprintf("%d", counter)
	}
	return fmt.Sprintf("%d-%s", counter, part)
}

// ---------------------------------------------------------------------------
// Catalog XML parser
// ---------------------------------------------------------------------------

var (
	reSectionBlock = regexp.MustCompile(`(?s)<section>(.*?)</section>`)
	reTopicFull    = regexp.MustCompile(`(?s)<topic[^>]*level=["']?([^"'>\s]+)["']?[^>]*>(.*?)</topic>`)
	reTopicLenient = regexp.MustCompile(`(?s)<topic[^>]*>(.*?)</topic>`)
	reLevelAttr    = regexp.MustCompile(`level=["']?([^"'>\s]+)`)
)

// ParseCatalogXML parses the <section>/<topic>/<group> XML produced by the catalog agent.
func ParseCatalogXML(xmlText string) *Wiki {
	wiki := &Wiki{}
	counter := 0

	for _, secMatch := range reSectionBlock.FindAllStringSubmatch(xmlText, -1) {
		secContent := secMatch[1]

		// Section name: text before first <topic or <group
		firstTagPos := len(secContent)
		for _, tag := range []string{"<topic", "<group"} {
			if p := strings.Index(secContent, tag); p != -1 && p < firstTagPos {
				firstTagPos = p
			}
		}
		sectionName := strings.TrimSpace(secContent[:firstTagPos])
		if sectionName == "" {
			continue
		}

		remaining := secContent[firstTagPos:]
		pos := 0
		for pos < len(remaining) {
			gStart := strings.Index(remaining[pos:], "<group>")
			tStart := strings.Index(remaining[pos:], "<topic")
			if gStart != -1 {
				gStart += pos
			}
			if tStart != -1 {
				tStart += pos
			}

			if gStart == -1 && tStart == -1 {
				break
			}

			if gStart != -1 && (tStart == -1 || gStart < tStart) {
				// Group block
				gEnd := strings.Index(remaining[gStart:], "</group>")
				if gEnd == -1 {
					break
				}
				gEnd += gStart
				groupContent := remaining[gStart+7 : gEnd]
				// Group name: text before first <topic
				grpName := ""
				if grpTopicPos := strings.Index(groupContent, "<topic"); grpTopicPos != -1 {
					grpName = strings.TrimSpace(groupContent[:grpTopicPos])
				}
				// Topics inside group
				for _, tm := range reTopicFull.FindAllStringSubmatch(groupContent, -1) {
					counter++
					level := strings.TrimSpace(tm[1])
					title := strings.TrimSpace(tm[2])
					if title == "" {
						continue
					}
					wiki.Pages = append(wiki.Pages, WikiPage{
						Title:   title,
						Slug:    MakeSlug(counter, title),
						Level:   level,
						Section: sectionName,
						Group:   grpName,
					})
				}
				pos = gEnd + 8
			} else {
				// Standalone topic
				tEnd := strings.Index(remaining[tStart:], "</topic>")
				if tEnd == -1 {
					break
				}
				tEnd += tStart + len("</topic>")
				topicStr := remaining[tStart:tEnd]

				var level, title string
				if m := reTopicFull.FindStringSubmatch(topicStr); m != nil {
					level = strings.TrimSpace(m[1])
					title = strings.TrimSpace(m[2])
				} else if m2 := reTopicLenient.FindStringSubmatch(topicStr); m2 != nil {
					title = strings.TrimSpace(m2[1])
					if lm := reLevelAttr.FindStringSubmatch(topicStr); lm != nil {
						level = strings.TrimSpace(lm[1])
					} else {
						level = "Beginner"
					}
				}
				if title != "" {
					counter++
					wiki.Pages = append(wiki.Pages, WikiPage{
						Title:   title,
						Slug:    MakeSlug(counter, title),
						Level:   level,
						Section: sectionName,
					})
				}
				pos = tEnd
			}
		}
	}
	return wiki
}
