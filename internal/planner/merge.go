package planner

import (
	"strings"
	"unicode"

	"github.com/Symon12138/wikify/internal/models"
)

// MergePlannerFields copies Goal/Parent/ContentPath/DescriptionSlug/Group from the
// deterministic seed onto LLM-refined pages. Matching order:
//  1. exact title
//  2. normalized title (whitespace/punctuation collapsed)
//  3. section-scoped fuzzy (containment or high token overlap)
//  4. global fuzzy fallback
// Unmatched LLM pages still get ContentPath reconstructed from Parent/Section/Title.
func MergePlannerFields(llm, seed *models.Wiki) *models.Wiki {
	if llm == nil {
		return seed
	}
	if seed == nil || len(seed.Pages) == 0 {
		for i := range llm.Pages {
			ensureContentPath(&llm.Pages[i])
		}
		return llm
	}

	byExact := map[string]models.WikiPage{}
	byNorm := map[string]models.WikiPage{}
	for _, p := range seed.Pages {
		byExact[p.Title] = p
		n := normalizeTitle(p.Title)
		if n != "" {
			// First seed wins on collision; exact path still preferred later.
			if _, ok := byNorm[n]; !ok {
				byNorm[n] = p
			}
		}
	}

	used := map[string]bool{} // seed slug or title key already claimed
	for i := range llm.Pages {
		lp := &llm.Pages[i]
		if s, ok := byExact[lp.Title]; ok {
			applySeedFields(lp, s)
			used[seedKey(s)] = true
			ensureContentPath(lp)
			continue
		}
		if s, ok := byNorm[normalizeTitle(lp.Title)]; ok && !used[seedKey(s)] {
			applySeedFields(lp, s)
			used[seedKey(s)] = true
			ensureContentPath(lp)
			continue
		}
		if s, ok := bestFuzzySeed(*lp, seed.Pages, used, true); ok {
			applySeedFields(lp, s)
			used[seedKey(s)] = true
			ensureContentPath(lp)
			continue
		}
		if s, ok := bestFuzzySeed(*lp, seed.Pages, used, false); ok {
			applySeedFields(lp, s)
			used[seedKey(s)] = true
		}
		ensureContentPath(lp)
	}
	return llm
}

func seedKey(p models.WikiPage) string {
	if p.Slug != "" {
		return "slug:" + p.Slug
	}
	return "title:" + p.Title
}

func applySeedFields(dst *models.WikiPage, src models.WikiPage) {
	if dst.Parent == "" {
		dst.Parent = src.Parent
	}
	if dst.Goal == "" {
		dst.Goal = src.Goal
	}
	if dst.ContentPath == "" {
		dst.ContentPath = src.ContentPath
	}
	if dst.DescriptionSlug == "" {
		dst.DescriptionSlug = src.DescriptionSlug
	}
	if dst.Group == "" {
		dst.Group = src.Group
	}
	// Prefer seed section only when LLM left it empty (rare).
	if dst.Section == "" {
		dst.Section = src.Section
	}
	if len(dst.DependentFiles) == 0 && len(src.DependentFiles) > 0 {
		dst.DependentFiles = append([]string(nil), src.DependentFiles...)
	}
}

func ensureContentPath(p *models.WikiPage) {
	if p.ContentPath != "" {
		return
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = "page"
	}
	parent := strings.TrimSpace(p.Parent)
	if parent == "" {
		parent = strings.TrimSpace(p.Section)
	}
	if parent != "" && parent != title {
		p.ContentPath = parent + "/" + title + ".md"
		return
	}
	p.ContentPath = title + ".md"
}

func bestFuzzySeed(lp models.WikiPage, seeds []models.WikiPage, used map[string]bool, sameSectionOnly bool) (models.WikiPage, bool) {
	lt := normalizeTitle(lp.Title)
	if lt == "" {
		return models.WikiPage{}, false
	}
	ltTokens := titleTokens(lt)
	bestScore := 0.0
	var best models.WikiPage
	found := false
	for _, s := range seeds {
		if used[seedKey(s)] {
			continue
		}
		if sameSectionOnly {
			if lp.Section == "" || s.Section == "" || lp.Section != s.Section {
				// Also allow match when LLM section equals seed parent/group labels.
				if lp.Section != s.Parent && lp.Section != s.Group {
					continue
				}
			}
		}
		st := normalizeTitle(s.Title)
		if st == "" {
			continue
		}
		score := titleSimilarity(lt, st, ltTokens, titleTokens(st))
		// Require a reasonably strong match to avoid wrong path/goal transfer.
		if score < 0.55 {
			continue
		}
		if !found || score > bestScore {
			bestScore = score
			best = s
			found = true
		}
	}
	return best, found
}

func normalizeTitle(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		// Treat other runes as separators.
		if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func titleTokens(norm string) []string {
	if norm == "" {
		return nil
	}
	// For CJK-heavy titles without spaces, also emit overlapping bigrams.
	parts := strings.Fields(norm)
	var out []string
	for _, p := range parts {
		out = append(out, p)
		runes := []rune(p)
		if len(runes) >= 4 {
			for i := 0; i+1 < len(runes); i++ {
				out = append(out, string(runes[i:i+2]))
			}
		}
	}
	return out
}

func titleSimilarity(a, b string, ta, tb []string) float64 {
	if a == b {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	// Containment bonus (LLM often lengthens/shortens seed titles).
	if strings.Contains(a, b) || strings.Contains(b, a) {
		// length ratio softens weak containments like "a" in "architecture"
		la, lb := float64(len([]rune(a))), float64(len([]rune(b)))
		ratio := la / lb
		if ratio > 1 {
			ratio = lb / la
		}
		if ratio >= 0.4 {
			return 0.75 + 0.25*ratio
		}
	}
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	setA := map[string]struct{}{}
	for _, t := range ta {
		setA[t] = struct{}{}
	}
	inter := 0
	setB := map[string]struct{}{}
	for _, t := range tb {
		setB[t] = struct{}{}
		if _, ok := setA[t]; ok {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
