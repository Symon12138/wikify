package evidence

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JSHurt/wikify/internal/scan"
)

// Shallow-page thresholds. Named vars (not consts) so they stay tunable by
// callers and tests without an API change.
var (
	// ShallowMinSections is the minimum H2 count for a page to count as deep.
	ShallowMinSections = 4
	// ShallowMinDistinctCitePaths is the minimum number of distinct cited
	// file:// paths for a page to count as deep.
	ShallowMinDistinctCitePaths = 3
	// ShallowMinSectionSources is the minimum number of 章节来源 / Section
	// sources blocks for a page to count as deep.
	ShallowMinSectionSources = 1
	// ThinSectionMinRunes is the prose floor below which an H2 section is
	// reported by ThinSections.
	ThinSectionMinRunes = 200
	// maxInvalidCiteIssues bounds the issue list fed into the repair prompt.
	maxInvalidCiteIssues = 8
)

// DepthReport is a deterministic, purely string-counted depth profile of a
// generated page body. No LLM, no repo access — fully offline-testable.
type DepthReport struct {
	Sections            int // H2 headings (`## ` at line start, outside fences)
	DistinctCitePaths   int // distinct file:// paths (fragment stripped)
	LineAnchoredCites   int // cites carrying a #L<digit> anchor
	Mermaids            int // ```mermaid fences
	Tables              int // markdown tables (one per |---| separator row)
	SectionSourceBlocks int // 章节来源 / Section sources blocks
	ProseRunes          int // rune count outside code fences
}

// Shallow reports whether the page needs a depth-enrichment pass:
// too few sections, too few distinct cited files, or no section-source blocks.
func (r DepthReport) Shallow() bool {
	return r.Sections < ShallowMinSections ||
		r.DistinctCitePaths < ShallowMinDistinctCitePaths ||
		r.SectionSourceBlocks < ShallowMinSectionSources
}

// Summary renders the report as one compact log/prompt line, flagging the
// axes that fall below the shallow thresholds.
func (r DepthReport) Summary() string {
	flag := func(v, min int) string {
		if v < min {
			return fmt.Sprintf("%d (<%d)", v, min)
		}
		return fmt.Sprintf("%d", v)
	}
	return fmt.Sprintf("sections=%s, distinct cite paths=%s, section-source blocks=%s, line-anchored cites=%d, mermaid=%d, tables=%d, prose runes=%d",
		flag(r.Sections, ShallowMinSections),
		flag(r.DistinctCitePaths, ShallowMinDistinctCitePaths),
		flag(r.SectionSourceBlocks, ShallowMinSectionSources),
		r.LineAnchoredCites, r.Mermaids, r.Tables, r.ProseRunes)
}

var (
	reCitePath      = regexp.MustCompile(`file://([^#\s"'()\[\]<>]+)`)
	reLineAnchor    = regexp.MustCompile(`#L\d`)
	reTableSepChars = regexp.MustCompile(`^[\s|:\-]+$`)
)

// DepthScore counts structural depth signals in a page body. Pure string
// counting; code-fence content is excluded from Sections/Tables/ProseRunes.
func DepthScore(body string) DepthReport {
	var r DepthReport
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inFence && strings.HasPrefix(strings.ToLower(trimmed), "```mermaid") {
				r.Mermaids++
			}
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			r.Sections++
		}
		if strings.Contains(line, "|") && strings.Contains(line, "---") && reTableSepChars.MatchString(trimmed) {
			r.Tables++
		}
		if strings.Contains(line, "章节来源") || strings.Contains(strings.ToLower(line), "section sources") {
			r.SectionSourceBlocks++
		}
		r.ProseRunes += len([]rune(trimmed))
	}
	seen := map[string]bool{}
	for _, m := range reCitePath.FindAllStringSubmatch(body, -1) {
		p := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(m[1])), "./")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
	}
	r.DistinctCitePaths = len(seen)
	r.LineAnchoredCites = len(reLineAnchor.FindAllString(body, -1))
	return r
}

// ThinSections returns the titles of H2 sections that need enrichment: prose
// below ThinSectionMinRunes or no 章节来源 / Section sources block. The TOC
// section (目录 / Table of Contents) is skipped — it is short by design.
// Capped at 6 titles so the enrich prompt stays bounded.
func ThinSections(body string) []string {
	type sec struct {
		title     string
		runes     int
		hasSource bool
	}
	var secs []sec
	inFence := false
	cur := -1
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			secs = append(secs, sec{title: strings.TrimSpace(strings.TrimPrefix(line, "## "))})
			cur = len(secs) - 1
			continue
		}
		if cur < 0 {
			continue
		}
		if strings.Contains(line, "章节来源") || strings.Contains(strings.ToLower(line), "section sources") {
			secs[cur].hasSource = true
		}
		secs[cur].runes += len([]rune(trimmed))
	}
	var out []string
	for _, s := range secs {
		lower := strings.ToLower(s.title)
		if strings.Contains(s.title, "目录") || strings.Contains(lower, "table of contents") {
			continue
		}
		if s.runes < ThinSectionMinRunes || !s.hasSource {
			out = append(out, s.title)
			if len(out) >= 6 {
				break
			}
		}
	}
	return out
}

// FindInvalidCitePaths returns repair-prompt issues for cited file:// paths
// that do not exist in the scanned file list, with a nearest-match suggestion
// when the basename resolves to exactly one real file. Detection is skipped
// entirely when files is empty (offline fixtures / no scan model).
func FindInvalidCitePaths(body string, files []scan.FileInfo) []string {
	if len(files) == 0 || strings.TrimSpace(body) == "" {
		return nil
	}
	exact := map[string]bool{}
	byBase := map[string][]string{}
	for _, f := range files {
		rel := filepath.ToSlash(f.RelativePath)
		if rel == "" {
			continue
		}
		exact[rel] = true
		base := filepath.Base(rel)
		byBase[base] = append(byBase[base], rel)
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range reCitePath.FindAllStringSubmatch(body, -1) {
		p := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(m[1])), "./")
		p = strings.Trim(p, "`*")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if exact[p] {
			continue
		}
		if cands := byBase[filepath.Base(p)]; len(cands) == 1 {
			out = append(out, fmt.Sprintf("cited path does not exist in repo: %s (did you mean %s?)", p, cands[0]))
		} else {
			out = append(out, fmt.Sprintf("cited path does not exist in repo: %s", p))
		}
		if len(out) >= maxInvalidCiteIssues {
			break
		}
	}
	return out
}
