// Page-level structural lint (QL-1) and citation path validation (QL-2).
// Both are deterministic, product-agnostic and offline: pure string surgery
// plus (for cite clamping only) cached line recounts under model.Root.
package export

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/Symon12138/wikify/internal/scan"
)

// reMermaidHeader is the allowlist of mermaid diagram types: the first
// meaningful line of every ```mermaid fence must start with one of these.
var reMermaidHeader = regexp.MustCompile(`^(graph|flowchart|sequenceDiagram|classDiagram|stateDiagram(-v2)?|erDiagram|journey|pie|gantt)\b`)

// LintPageBody applies safe structural fixes to a page body and reports what
// it found. Fixes never delete author content:
//   - odd number of ``` fences        → append one closing fence at EOF
//   - ```mermaid with unknown header  → demote to a plain ``` code block
//   - duplicate H2 titles             → rename later occurrences "title (2)"
//   - near-empty H2 sections          → reported only (soft), no rewrite
//
// hard lists structural breaks still present AFTER the fixes (the fixer is
// total for the classes above, so hard is a safety net for pathological
// inputs); the runner treats a non-empty hard list like a thin body and
// retries. soft lists everything that was auto-fixed or merely flagged.
func LintPageBody(body string) (fixed string, hard []string, soft []string) {
	if strings.TrimSpace(body) == "" {
		return body, nil, nil
	}
	fixed = body

	// (a) Unbalanced code fences → close at EOF so later fence-aware passes
	// (heading scans, mermaid checks, TOC build) see a consistent document.
	if countFenceLines(fixed)%2 == 1 {
		if !strings.HasSuffix(fixed, "\n") {
			fixed += "\n"
		}
		fixed += "```"
		soft = append(soft, "auto-closed unbalanced code fence")
	}

	// (b) Mermaid blocks whose first meaningful line is not a known diagram
	// type render as an error box in most viewers — demote to plain code.
	var demoted []string
	fixed, demoted = demoteInvalidMermaid(fixed)
	soft = append(soft, demoted...)

	// (c)+(d) H2 section checks (outside code fences).
	var renamed, empties []string
	fixed, renamed, empties = lintH2Sections(fixed)
	soft = append(soft, renamed...)
	soft = append(soft, empties...)

	// Re-verify: anything still broken after the fixes is a hard issue.
	if countFenceLines(fixed)%2 == 1 {
		hard = append(hard, "unbalanced code fence (could not auto-close)")
	}
	if _, remain := demoteInvalidMermaid(fixed); len(remain) > 0 {
		hard = append(hard, "invalid mermaid block (could not demote)")
	}
	return fixed, hard, soft
}

// countFenceLines counts lines that open or close a ``` code fence.
func countFenceLines(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(strings.TrimRight(ln, "\r")), "```") {
			n++
		}
	}
	return n
}

// demoteInvalidMermaid rewrites ```mermaid fences whose first meaningful line
// (skipping blank lines and %% directives/comments) does not match
// reMermaidHeader into plain ``` fences. Content is preserved verbatim.
func demoteInvalidMermaid(body string) (string, []string) {
	lines := strings.Split(body, "\n")
	var issues []string
	inFence := false
	fenceStart := -1
	isMermaid := false
	for i, ln := range lines {
		t := strings.TrimSpace(strings.TrimRight(ln, "\r"))
		if !strings.HasPrefix(t, "```") {
			continue
		}
		if !inFence {
			inFence = true
			fenceStart = i
			info := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "```")))
			isMermaid = info == "mermaid"
			continue
		}
		if isMermaid {
			first := ""
			for j := fenceStart + 1; j < i; j++ {
				s := strings.TrimSpace(lines[j])
				// %%{init}%% directives and %% comments are legal leaders.
				if s == "" || strings.HasPrefix(s, "%%") {
					continue
				}
				first = s
				break
			}
			if !reMermaidHeader.MatchString(first) {
				k := strings.Index(lines[fenceStart], "```")
				lines[fenceStart] = lines[fenceStart][:k] + "```"
				if first == "" {
					issues = append(issues, "demoted empty mermaid block to plain code fence")
				} else {
					issues = append(issues, fmt.Sprintf("demoted invalid mermaid block (starts %q)", truncateRunes(first, 40)))
				}
			}
		}
		inFence = false
		isMermaid = false
	}
	return strings.Join(lines, "\n"), issues
}

// lintH2Sections renames duplicate H2 titles ("title (2)") and flags H2
// sections with fewer than 10 non-space runes before the next heading / EOF.
func lintH2Sections(body string) (string, []string, []string) {
	lines := strings.Split(body, "\n")
	inFence := make([]bool, len(lines))
	fence := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(strings.TrimRight(ln, "\r")), "```") {
			inFence[i] = true // fence marker lines never carry headings
			fence = !fence
			continue
		}
		inFence[i] = fence
	}
	reHeading := regexp.MustCompile(`^#{1,6}\s+`)

	// (d) duplicate H2 titles → deterministic rename of later occurrences.
	var renamed []string
	taken := map[string]bool{}
	for i, ln := range lines {
		if inFence[i] || !strings.HasPrefix(ln, "## ") {
			continue
		}
		title := strings.TrimSpace(strings.TrimPrefix(strings.TrimRight(ln, "\r"), "## "))
		if title == "" {
			continue
		}
		if !taken[title] {
			taken[title] = true
			continue
		}
		n := 2
		cand := fmt.Sprintf("%s (%d)", title, n)
		for taken[cand] {
			n++
			cand = fmt.Sprintf("%s (%d)", title, n)
		}
		lines[i] = "## " + cand
		taken[cand] = true
		renamed = append(renamed, fmt.Sprintf("renamed duplicate H2 %q to %q", title, cand))
	}

	// (c) near-empty H2 sections (soft only — enrichment is the LLM's job).
	var empties []string
	for i, ln := range lines {
		if inFence[i] || !strings.HasPrefix(ln, "## ") {
			continue
		}
		title := strings.TrimSpace(strings.TrimPrefix(strings.TrimRight(ln, "\r"), "## "))
		runes := 0
		for j := i + 1; j < len(lines) && runes < 10; j++ {
			if !inFence[j] && reHeading.MatchString(lines[j]) {
				break
			}
			for _, r := range lines[j] {
				if !unicode.IsSpace(r) {
					runes++
				}
			}
		}
		if runes < 10 {
			empties = append(empties, fmt.Sprintf("empty section: %q", title))
		}
	}
	return strings.Join(lines, "\n"), renamed, empties
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ── QL-2: citation path validation ────────────────────────────────────────────

// scanLineCap mirrors scan.countLines(path, 2000): a FileInfo.Lines at or
// above this value may be capped, so clamping recounts the file exactly.
const scanLineCap = 2000

const (
	citeExact = iota
	citeCorrected
	citeUnresolved
)

var (
	reCiteLink = regexp.MustCompile(`\[([^\]]*)\]\(file://([^)#\s]+)(#[^)\s]*)?\)`)
	reBareCite = regexp.MustCompile(`file://([^#\s"'()\[\]<>]+)(#[^\s"'()\[\]<>]*)?`)
	reCiteFrag = regexp.MustCompile(`^#[Ll](\d+)(?:-[Ll]?(\d+))?`)
)

// citeLineCounter resolves real line counts for anchor clamping. Counts come
// from the scan model; values at the countLines cap are recounted exactly via
// one os.ReadFile under root, cached per Export call.
type citeLineCounter struct {
	root  string
	lines map[string]int
	cache map[string]int
}

func (c *citeLineCounter) count(p string) int {
	if n, ok := c.cache[p]; ok {
		return n
	}
	n := c.lines[p]
	if n >= scanLineCap && c.root != "" {
		if data, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(p))); err == nil {
			if len(data) == 0 {
				n = 0
			} else {
				// Mirror scan.countLines semantics (1 + '\n' count), uncapped.
				n = 1
				for _, b := range data {
					if b == '\n' {
						n++
					}
				}
			}
		}
	}
	c.cache[p] = n
	return n
}

// ValidateCitePaths checks every file:// citation in body against the scanned
// repo inventory and heals what it safely can:
//   - exact path        → kept; #Lstart-Lend clamped to the real line count
//   - unique basename   → path rewritten to the real file
//   - unique suffix     → (≥2 trailing segments) path rewritten
//   - unresolvable      → link dropped, label kept as plain `code` text
//
// Validation is skipped (body returned untouched, all counts zero) when the
// model is nil or has no files — offline fixtures must not lose links.
// Idempotent: corrected paths resolve exactly on the next run, dropped links
// no longer contain file:// and are not reprocessed.
func ValidateCitePaths(body string, model *scan.Model) (string, int, int, int, int) {
	if body == "" || model == nil || len(model.Files) == 0 {
		return body, 0, 0, 0, 0
	}
	// Unify relative path#L links into file:// form first so they are
	// validated too. normalizeCitations is idempotent — EnsurePageFormat
	// re-runs it later as a no-op.
	body = normalizeCitations(body)

	exists := map[string]int{} // rel path → scan-reported line count
	byBase := map[string][]string{}
	var all []string
	for _, f := range model.Files {
		rel := strings.TrimPrefix(filepath.ToSlash(f.RelativePath), "./")
		if rel == "" {
			continue
		}
		if _, ok := exists[rel]; ok {
			continue
		}
		exists[rel] = f.Lines
		byBase[path.Base(rel)] = append(byBase[path.Base(rel)], rel)
		all = append(all, rel)
	}
	counter := &citeLineCounter{root: model.Root, lines: exists, cache: map[string]int{}}

	resolve := func(p string) (string, int) {
		if _, ok := exists[p]; ok {
			return p, citeExact
		}
		if cands := byBase[path.Base(p)]; len(cands) == 1 {
			return cands[0], citeCorrected
		}
		// Unique longest-suffix match with at least 2 trailing segments.
		segs := strings.Split(p, "/")
		for l := len(segs); l >= 2; l-- {
			suffix := strings.Join(segs[len(segs)-l:], "/")
			hit, n := "", 0
			for _, cand := range all {
				if cand == suffix || strings.HasSuffix(cand, "/"+suffix) {
					hit = cand
					n++
					if n > 1 {
						break
					}
				}
			}
			if n == 1 {
				return hit, citeCorrected
			}
		}
		return "", citeUnresolved
	}

	clampFrag := func(p, frag string) string {
		if frag == "" {
			return ""
		}
		m := reCiteFrag.FindStringSubmatch(frag)
		if m == nil {
			return frag // unknown anchor form — leave untouched
		}
		total := counter.count(p)
		if total <= 0 {
			return frag
		}
		start, _ := strconv.Atoi(m[1])
		if start < 1 {
			start = 1
		}
		if start > total {
			start = total
		}
		if m[2] == "" {
			return fmt.Sprintf("#L%d", start)
		}
		end, _ := strconv.Atoi(m[2])
		if end > total {
			end = total
		}
		if end < start {
			end = start
		}
		return fmt.Sprintf("#L%d-L%d", start, end)
	}

	var total, valid, corrected, dropped int

	// Pass 1 — markdown links [label](file://path#L…).
	out := reCiteLink.ReplaceAllStringFunc(body, func(m string) string {
		sub := reCiteLink.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		label, rawPath, frag := sub[1], sub[2], sub[3]
		p := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(rawPath)), "./")
		total++
		resolved, kind := resolve(p)
		switch kind {
		case citeExact:
			valid++
		case citeCorrected:
			corrected++
		default:
			dropped++
			lbl := strings.Trim(strings.TrimSpace(label), "`")
			if lbl == "" {
				lbl = path.Base(p)
			}
			return "`" + lbl + "`"
		}
		return fmt.Sprintf("[%s](file://%s%s)", label, resolved, clampFrag(resolved, frag))
	})

	// Pass 2 — bare file:// cites outside markdown link targets. Replace
	// right-to-left so byte offsets stay valid.
	locs := reBareCite.FindAllStringSubmatchIndex(out, -1)
	for i := len(locs) - 1; i >= 0; i-- {
		loc := locs[i]
		start := loc[0]
		if start >= 2 && out[start-2:start] == "](" {
			continue // link target — already handled in pass 1
		}
		rawPath := out[loc[2]:loc[3]]
		frag := ""
		end := loc[3]
		if loc[4] >= 0 {
			frag = out[loc[4]:loc[5]]
			end = loc[5]
		}
		// Trim trailing punctuation that regularly leaks into the char class
		// (`code` spans, bold markers, sentence stops).
		trim := func(s string) (string, int) {
			cut := 0
			for len(s) > 0 {
				switch s[len(s)-1] {
				case '`', '*', '.', ',', ';', ':':
					s = s[:len(s)-1]
					cut++
				default:
					return s, cut
				}
			}
			return s, cut
		}
		if frag != "" {
			var cut int
			frag, cut = trim(frag)
			end -= cut
		} else {
			var cut int
			rawPath, cut = trim(rawPath)
			end -= cut
		}
		p := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(rawPath)), "./")
		if p == "" {
			continue
		}
		total++
		resolved, kind := resolve(p)
		var repl string
		switch kind {
		case citeExact:
			valid++
			repl = "file://" + p + clampFrag(p, frag)
		case citeCorrected:
			corrected++
			repl = "file://" + resolved + clampFrag(resolved, frag)
		default:
			dropped++
			if start > 0 && out[start-1] == '`' {
				repl = p // already inside backticks — just drop the scheme
			} else {
				repl = "`" + p + "`"
			}
		}
		out = out[:start] + repl + out[end:]
	}
	return out, total, valid, corrected, dropped
}
