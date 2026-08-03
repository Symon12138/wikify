package browse

import (
	"fmt"
	"html"
	"path/filepath"
	"regexp"
	"strings"
)

// Markdown / raw patterns for source citations used in generated pages.
//   [label](file://path#L1-L40)
//   file://path#L1-L40
//   <cite>...</cite> blocks (HTML; goldmark leaves inner MD unparsed)
var (
	reMDFileLink = regexp.MustCompile(`\[([^\]]*)\]\(file://([^)\s#]+)(?:#(L\d+(?:-L\d+)?))?\)`)
	reBareFile   = regexp.MustCompile(`(?i)\bfile://([^\s)\]<"']+)`)
	reCiteBlock  = regexp.MustCompile(`(?is)<cite>\s*(.*?)\s*</cite>`)
	reLineRange  = regexp.MustCompile(`(?i)^L(\d+)(?:-L(\d+))?$`)
)

// preprocessMarkdown rewrites citations into HTML that renders cleanly in browse.
// Must run before goldmark so <cite> contents are not left as raw markdown text.
func preprocessMarkdown(raw []byte) []byte {
	s := string(raw)
	// 1) Expand <cite> blocks into structured reference panels first.
	s = reCiteBlock.ReplaceAllStringFunc(s, func(block string) string {
		m := reCiteBlock.FindStringSubmatch(block)
		if m == nil {
			return block
		}
		inner := strings.TrimSpace(m[1])
		// Collect markdown file links inside cite.
		var chips []string
		seen := map[string]bool{}
		for _, sm := range reMDFileLink.FindAllStringSubmatch(inner, -1) {
			label, path, frag := sm[1], sm[2], sm[3]
			key := path + "#" + frag
			if seen[key] {
				continue
			}
			seen[key] = true
			chips = append(chips, renderSourceChip(label, path, frag))
		}
		// Also bare file:// inside cite (no markdown wrapper).
		for _, sm := range reBareFile.FindAllStringSubmatch(inner, -1) {
			full := sm[1]
			path, frag := splitFileFrag(full)
			key := path + "#" + frag
			if seen[key] {
				continue
			}
			// Skip if this was already part of a markdown link matched above
			// (bare regex also matches inside ](file://...) — filter by checking original link form).
			if strings.Contains(inner, "](file://"+full+")") || strings.Contains(inner, "](file://"+path) {
				continue
			}
			seen[key] = true
			chips = append(chips, renderSourceChip(filepath.Base(path), path, frag))
		}

		title := "源码引用"
		// Prefer **title** or first bold line as panel title.
		if bm := regexp.MustCompile(`(?m)^\*\*([^*]+)\*\*`).FindStringSubmatch(inner); bm != nil {
			title = strings.TrimSpace(bm[1])
		} else if strings.Contains(inner, "References") {
			title = "References"
		} else if strings.Contains(inner, "参考文献") {
			title = "参考文献"
		}

		if len(chips) == 0 {
			// Fall back: strip file:// noise and keep plain text cleaned.
			clean := reMDFileLink.ReplaceAllStringFunc(inner, func(link string) string {
				sm := reMDFileLink.FindStringSubmatch(link)
				if sm == nil {
					return link
				}
				return sm[1]
			})
			clean = reBareFile.ReplaceAllString(clean, "")
			clean = strings.TrimSpace(clean)
			return fmt.Sprintf(
				`<div class="src-panel"><div class="src-panel-title">%s</div><div class="src-panel-body">%s</div></div>`,
				html.EscapeString(title),
				html.EscapeString(clean),
			)
		}
		return fmt.Sprintf(
			`<div class="src-panel"><div class="src-panel-title">%s</div><div class="src-chips">%s</div></div>`,
			html.EscapeString(title),
			strings.Join(chips, ""),
		)
	})

	// 2) Remaining markdown file links in body → chips.
	s = reMDFileLink.ReplaceAllStringFunc(s, func(link string) string {
		sm := reMDFileLink.FindStringSubmatch(link)
		if sm == nil {
			return link
		}
		return renderSourceChip(sm[1], sm[2], sm[3])
	})

	// 3) Bare file:// leftovers (rare) → chips.
	s = reBareFile.ReplaceAllStringFunc(s, func(m string) string {
		sm := reBareFile.FindStringSubmatch(m)
		if sm == nil {
			return m
		}
		path, frag := splitFileFrag(sm[1])
		return renderSourceChip(filepath.Base(path), path, frag)
	})

	return []byte(s)
}

func splitFileFrag(full string) (path, frag string) {
	// full is everything after file://
	if i := strings.Index(full, "#"); i >= 0 {
		return full[:i], strings.TrimPrefix(full[i+1:], "")
	}
	return full, ""
}

// renderSourceChip builds a compact, readable citation chip.
// Shows basename (+ line range); full repo-relative path is in title tooltip.
func renderSourceChip(label, path, frag string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	path = filepath.ToSlash(path)

	base := strings.TrimSpace(label)
	if base == "" || base == path || strings.Contains(base, "/") {
		base = filepath.Base(path)
	}
	// Drop common noise prefixes from label.
	base = strings.TrimSuffix(base, "/")

	lineText := formatLineFrag(frag)
	full := path
	if lineText != "" {
		full = path + " · " + lineText
	}

	// Icon-like monospaced chip; title holds full path for hover.
	inner := html.EscapeString(base)
	if lineText != "" {
		inner += `<span class="src-lines">` + html.EscapeString(lineText) + `</span>`
	}
	// data-path feeds the NAV-5 "other pages citing this file" popover.
	return fmt.Sprintf(
		`<span class="src-chip" title="%s" data-path="%s"><span class="src-icon">◇</span><span class="src-name">%s</span></span>`,
		html.EscapeString(full),
		html.EscapeString(path),
		inner,
	)
}

func formatLineFrag(frag string) string {
	frag = strings.TrimSpace(frag)
	if frag == "" {
		return ""
	}
	// Accept L12-L40, L12, 12-40, etc.
	frag = strings.TrimPrefix(frag, "#")
	m := reLineRange.FindStringSubmatch(frag)
	if m == nil {
		// try without L prefix
		if m2 := regexp.MustCompile(`^(\d+)(?:-(\d+))?$`).FindStringSubmatch(frag); m2 != nil {
			if m2[2] != "" {
				return "L" + m2[1] + "–" + m2[2]
			}
			return "L" + m2[1]
		}
		return frag
	}
	if m[2] != "" {
		return "L" + m[1] + "–" + m[2]
	}
	return "L" + m[1]
}
