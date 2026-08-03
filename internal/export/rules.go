// Business Rules Digest (RD): deterministic, LLM-free aggregation page.
//
// At export time this file scans the bodies of business-track pages for
// "rule-shaped" statements — sentences or list items that carry a source cite
// anchor ([label](file://path#L1-L10)) AND hit a generic rule keyword
// (validation / limit / timeout / default / must / forbidden / unique / …).
// Hits are denoised (headings, table separator rows, code and mermaid fences,
// machine <cite> blocks excluded; whitespace-normalized dedupe; 240-rune safe
// truncation), capped (10 per source page, 200 total) and grouped by the
// source page's Section into a single "业务规则清单" / "Business Rules
// Digest" page. Fewer than 5 total hits → no page is generated.
//
// The digest follows the synthesized-nav-page contract from nav.go: a
// DescriptionSlug marker (rulesDigestMarker) makes removeGeneratedNavPages
// strip any previous instance at the start of every Export, and the write
// loop emits the deterministic body verbatim — repeated exports are
// byte-identical and never accumulate duplicates.
//
// All keyword lists are generic rule vocabulary — no product/project domain
// terms.
package export

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Symon12138/wikify/internal/models"
)

const (
	// rulesDigestMarker is the DescriptionSlug prefix identifying the digest
	// page as export-synthesized (see isGeneratedNavPage / nav.go markers).
	rulesDigestMarker = "rules-digest"

	rulesPerPageMax = 10  // max rule statements kept per source page
	rulesTotalMax   = 200 // max rule statements in the whole digest
	rulesMinTotal   = 5   // below this the digest page is not generated
	ruleMaxRunes    = 240 // statements longer than this are truncated/dropped
)

// zhRuleTerms are generic Chinese rule-signal words. A candidate sentence must
// contain at least one (plus a cite anchor) to count as a rule statement.
// Chinese terms are matched in both language modes — a Chinese rule sentence
// inside an English-labelled repo is still a rule.
var zhRuleTerms = []string{
	"校验", "限制", "上限", "下限", "超时", "默认", "必须", "禁止", "不允许",
	"不得", "至少", "最多", "不能", "失效", "过期", "唯一", "必填", "超过",
	"小于", "大于", "阈值",
}

// reEnRuleTerms matches generic English rule-signal words (word-bounded,
// case-insensitive, light plural/verb inflections). Active when the export
// language is not zh.
var reEnRuleTerms = regexp.MustCompile(`(?i)\b(must|shall|required|forbidden|limits?|timeouts?|defaults?|at (?:most|least)|exceeds?|exceeded|unique|expires?|expired|expiry|thresholds?)\b`)

var (
	// reRuleListItem matches unordered/ordered markdown list markers.
	reRuleListItem = regexp.MustCompile(`^\s*(?:[-*+]|\d{1,3}[.)、])\s+`)
	// reRuleTableSep matches table separator rows / horizontal rules when the
	// line additionally contains "-" (| --- | :--- |, ---).
	reRuleTableSep = regexp.MustCompile(`^[\s:|\-]+$`)
	reRuleSpace    = regexp.MustCompile(`\s+`)
)

// hitsRuleTerm reports whether s contains a generic rule keyword. zh terms are
// always checked; the English word list only when the export language isn't zh.
func hitsRuleTerm(s string, zh bool) bool {
	for _, t := range zhRuleTerms {
		if strings.Contains(s, t) {
			return true
		}
	}
	if !zh {
		return reEnRuleTerms.MatchString(s)
	}
	return false
}

// splitRuleSentences splits a paragraph line into sentence candidates on
// Chinese/ASCII sentence enders, without splitting inside parentheses so that
// markdown link targets ("(file://a/b.go#L1-L2)") stay intact. ASCII enders
// split only before whitespace / end of line, keeping "v1.2"-style tokens.
func splitRuleSentences(line string) []string {
	var out []string
	var cur []rune
	depth := 0
	runes := []rune(line)
	for i, r := range runes {
		cur = append(cur, r)
		switch r {
		case '(', '（':
			depth++
		case ')', '）':
			if depth > 0 {
				depth--
			}
		}
		if depth > 0 {
			continue
		}
		switch r {
		case '。', '！', '？', '；':
			out = append(out, string(cur))
			cur = cur[:0]
		case '.', '!', '?', ';':
			if i+1 >= len(runes) || runes[i+1] == ' ' || runes[i+1] == '\t' {
				out = append(out, string(cur))
				cur = cur[:0]
			}
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

// truncateRuleText caps s at maxRunes. It never cuts through a markdown link:
// when the cut lands inside one, it backs off to before the link's opening
// "[". If the surviving prefix no longer carries a complete link-form cite
// ([x](file://…)), the statement is dropped (returns "") rather than emitting
// a rule whose evidence anchor was truncated away.
func truncateRuleText(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	head := string(r[:maxRunes])
	if i := strings.LastIndex(head, "["); i >= 0 && !strings.Contains(head[i:], ")") {
		head = head[:i]
	}
	head = strings.TrimRight(head, " \t")
	if !reCiteLink.MatchString(head) {
		return ""
	}
	return head + "…"
}

// extractRuleStatements scans one page body and returns its rule statements
// in document order: whitespace-normalized, truncated, locally deduped.
// Denoising: code/mermaid fences, machine <cite> regions, headings, table
// separator rows and blank lines are skipped; blockquote markers unwrapped.
// A candidate qualifies only with BOTH a file:// cite and a rule keyword.
func extractRuleStatements(body string, zh bool) []string {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	inFence := false
	inCite := false
	for _, raw := range strings.Split(body, "\n") {
		t := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.Contains(t, "<cite>") {
			inCite = true
		}
		if inCite {
			if strings.Contains(t, "</cite>") {
				inCite = false
			}
			continue
		}
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		for strings.HasPrefix(t, ">") { // unwrap blockquotes
			t = strings.TrimSpace(strings.TrimPrefix(t, ">"))
		}
		if t == "" || (strings.Contains(t, "-") && reRuleTableSep.MatchString(t)) {
			continue
		}
		var cands []string
		if m := reRuleListItem.FindString(t); m != "" {
			cands = []string{t[len(m):]} // list items count as one statement
		} else {
			cands = splitRuleSentences(t)
		}
		for _, c := range cands {
			c = strings.TrimSpace(reRuleSpace.ReplaceAllString(c, " "))
			if c == "" || !strings.Contains(c, "file://") || !hitsRuleTerm(c, zh) {
				continue
			}
			c = truncateRuleText(c, ruleMaxRunes)
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// ruleEntry is one aggregated rule statement with its provenance.
type ruleEntry struct {
	text      string // original sentence (normalized), cite anchor intact
	pageTitle string
	link      string // content-root-relative link to the source page
}

// ruleGroup collects the entries of one source Section (business domain).
type ruleGroup struct {
	section string
	entries []ruleEntry
}

// rulePageTrack returns the page's materialized track, inferring it when the
// stored value is invalid (defensive for direct unit-level callers; Export
// already ran EnsureTracks).
func rulePageTrack(p models.WikiPage) string {
	switch p.Track {
	case models.TrackFoundation, models.TrackBusiness, models.TrackTechnical:
		return p.Track
	}
	return models.InferTrack(p)
}

// ensureBusinessRulesDigestPage synthesizes the RD digest page from the
// current business-track page bodies. Assumes removeGeneratedNavPages already
// stripped any previous digest instance (remove-then-rebuild idempotency, as
// for NAV-2/NAV-3). Skipped when an LLM-authored page already uses the digest
// title, or when fewer than rulesMinTotal statements are found. The page is
// inserted right after the last business-track page so it closes the business
// rail; Section == Title makes it an independent top-level section.
func ensureBusinessRulesDigestPage(wiki *models.Wiki, contents map[string]string, zh bool) {
	if wiki == nil || contents == nil || len(wiki.Pages) == 0 {
		return
	}
	title := "业务规则清单"
	if !zh {
		title = "Business Rules Digest"
	}
	slugExists := map[string]bool{}
	for _, p := range wiki.Pages {
		if p.Title == title {
			return // respect an existing (LLM-authored) page of the same name
		}
		slugExists[p.Slug] = true
	}

	var groups []*ruleGroup
	bySection := map[string]*ruleGroup{}
	seen := map[string]bool{} // global whitespace-normalized dedupe
	total := 0
	for _, p := range wiki.Pages {
		if total >= rulesTotalMax {
			break
		}
		if isGeneratedNavPage(p) || rulePageTrack(p) != models.TrackBusiness {
			continue
		}
		body := contents[p.Slug]
		if strings.TrimSpace(body) == "" {
			continue
		}
		// Normalize [x](path#L1-L2) cites to file:// form first, so run 1
		// (pre-format bodies) and run 2 (formatted bodies) extract the same
		// statements even when cite-path validation was skipped (no model
		// inventory). normalizeCitations is idempotent.
		perPage := 0
		for _, s := range extractRuleStatements(normalizeCitations(body), zh) {
			if perPage >= rulesPerPageMax || total >= rulesTotalMax {
				break
			}
			if seen[s] {
				continue
			}
			seen[s] = true
			sec := strings.TrimSpace(p.Section)
			if sec == "" {
				sec = p.Title
			}
			g := bySection[sec]
			if g == nil {
				g = &ruleGroup{section: sec}
				bySection[sec] = g
				groups = append(groups, g)
			}
			g.entries = append(g.entries, ruleEntry{text: s, pageTitle: p.Title, link: linkTarget(p)})
			perPage++
			total++
		}
	}
	if total < rulesMinTotal {
		return
	}

	hash := uuidFromKey(rulesDigestMarker)[:8]
	slug := "rules-digest"
	for slugExists[slug] {
		slug += "-x"
	}
	goal := "跨业务域聚合的规则式陈述清单（导出时离线抽取，带源码引用）"
	if !zh {
		goal = "Aggregated rule statements across business domains (offline extraction at export time, with source citations)"
	}
	page := models.WikiPage{
		Title:           title,
		Slug:            slug,
		Level:           "Intermediate",
		Section:         title,
		Goal:            goal,
		ContentPath:     title + ".md",
		DescriptionSlug: rulesDigestMarker + "-" + hash,
		Track:           models.TrackBusiness,
	}
	contents[slug] = rulesDigestBody(title, groups, total, zh)

	last := -1
	for i, p := range wiki.Pages {
		if rulePageTrack(p) == models.TrackBusiness {
			last = i
		}
	}
	if last < 0 {
		last = len(wiki.Pages) - 1
	}
	out := make([]models.WikiPage, 0, len(wiki.Pages)+1)
	out = append(out, wiki.Pages[:last+1]...)
	out = append(out, page)
	out = append(out, wiki.Pages[last+1:]...)
	wiki.Pages = out
}

// rulesDigestBody renders the deterministic digest markdown: intro line, TOC,
// purpose/scope/audience section, then one H2 per business domain with each
// rule kept verbatim (original cite anchor intact) plus a source-page link.
func rulesDigestBody(title string, groups []*ruleGroup, total int, zh bool) string {
	purpose, toc := "目的与范围", "目录"
	if !zh {
		purpose, toc = "Purpose and Scope", "Table of Contents"
	}
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	if zh {
		b.WriteString(fmt.Sprintf("本页由导出流程从业务轨页面离线聚合生成（不调用大模型），共收录 %d 条规则式陈述，按业务域分组。\n\n", total))
	} else {
		b.WriteString(fmt.Sprintf("This page is aggregated offline at export time from business-track pages (no LLM involved). It collects %d rule statements grouped by business domain.\n\n", total))
	}
	b.WriteString("## " + toc + "\n\n")
	b.WriteString(fmt.Sprintf("1. [%s](#%s)\n", purpose, anchor(purpose)))
	for i, g := range groups {
		b.WriteString(fmt.Sprintf("%d. [%s](#%s)\n", i+2, g.section, anchor(g.section)))
	}
	b.WriteString("\n## " + purpose + "\n\n")
	if zh {
		b.WriteString("- **目的**：集中查阅散落在各业务域页面中的规则式陈述（校验、限额、超时、状态与权限约束等），支持规则审阅与变更影响评估。\n")
		b.WriteString(fmt.Sprintf("- **范围**：业务轨页面正文中带源码引用的规则句，共 %d 条；每条保留原句、源码引用与来源页面链接。\n", total))
		b.WriteString("- **读者**：业务负责人与开发人员；建议先按业务域浏览规则，再经源码引用回到实现核对细节。\n\n")
	} else {
		b.WriteString("- **Purpose**: a single place to review rule statements (validation, limits, timeouts, state and permission constraints) scattered across business-domain pages.\n")
		b.WriteString(fmt.Sprintf("- **Scope**: %d rule statements with source citations extracted from business-track page bodies; each keeps the original sentence, its citation and a link to the source page.\n", total))
		b.WriteString("- **Audience**: business owners and developers; browse by domain first, then follow citations into the implementation.\n\n")
	}
	for _, g := range groups {
		b.WriteString("## " + g.section + "\n\n")
		for _, e := range g.entries {
			if zh {
				b.WriteString(fmt.Sprintf("- %s — 来源：[%s](%s)\n", e.text, e.pageTitle, e.link))
			} else {
				b.WriteString(fmt.Sprintf("- %s — Source: [%s](%s)\n", e.text, e.pageTitle, e.link))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}
