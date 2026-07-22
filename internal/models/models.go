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
			lines = append(lines, fmt.Sprintf("%s- [%s](%s)%s", indent, p.Title, p.Slug, marker))
		}
		lines = append(lines, "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
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
	// If business page has no technical match, link API/data index pages.
	if len(out) == 0 && src == TrackBusiness {
		for _, p := range w.Pages {
			tr := p.Track
			if tr == "" {
				tr = InferTrack(p)
			}
			if tr == TrackTechnical && (p.Title == p.Section || strings.Contains(p.Section, "接口") || strings.Contains(p.Section, "API") || strings.Contains(p.Section, "数据") || strings.Contains(p.Section, "架构")) {
				out = append(out, p)
				if len(out) >= limit {
					break
				}
			}
		}
	}
	return out
}

// InferTrack assigns foundation | business | technical from section/title when Track is empty or invalid.
// Exported so planner/runner share one rule set after free LLM catalogs.
func InferTrack(p WikiPage) string {
	if p.Track == TrackFoundation || p.Track == TrackBusiness || p.Track == TrackTechnical {
		return p.Track
	}
	sec := p.Section
	title := p.Title
	switch {
	case sec == "项目概述" || sec == "快速开始" || sec == "Project Overview" || sec == "Getting Started" ||
		sec == "Get Started" || sec == "Overview":
		return TrackFoundation
	case isBusinessSection(sec):
		// 客户管理模块 / 指标监控模块 / Customer Management …
		return TrackBusiness
	case strings.Contains(sec, "接口") || strings.Contains(sec, "API") || strings.Contains(sec, "数据") || strings.Contains(sec, "架构") ||
		strings.Contains(sec, "部署") || strings.Contains(sec, "运维") || strings.Contains(sec, "安全") || strings.Contains(sec, "开发") ||
		strings.Contains(sec, "故障") || strings.Contains(sec, "配置") ||
		strings.Contains(sec, "Architecture") || strings.Contains(sec, "Deploy") || strings.Contains(sec, "Security") || strings.Contains(sec, "Developer") ||
		strings.Contains(sec, "Database") || strings.Contains(sec, "Data Model"):
		return TrackTechnical
	case strings.Contains(title, "管理") && !strings.Contains(title, "配置管理"):
		return TrackBusiness
	default:
		return TrackTechnical
	}
}

// isBusinessSection identifies capability-module sections (not core-framework dumps).
func isBusinessSection(sec string) bool {
	if sec == "" {
		return false
	}
	// Core framework sections stay technical even if they contain 模块.
	if strings.Contains(sec, "核心业务") || strings.Contains(sec, "核心模块") || strings.Contains(sec, "Core Module") {
		return false
	}
	if strings.Contains(sec, "模块") {
		return true
	}
	if strings.Contains(sec, "Management") || strings.Contains(sec, "Monitoring") ||
		strings.Contains(sec, "Workflow") || strings.Contains(sec, "Audit") || strings.Contains(sec, "Risk") {
		return true
	}
	return false
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
