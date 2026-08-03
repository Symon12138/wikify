package planner

import (
	"fmt"
	"strings"

	"github.com/Symon12138/wikify/internal/models"
)

// ApplyHierarchyPaths sets Parent / ContentPath / DescriptionSlug from the
// catalog hierarchy the model produced (section → optional group → title).
// Does not consult any seed tree — model structure is authoritative.
//
// Path layout under content/:
//   - section topic (title == section):  <title>.md  or <section>/<title>.md when nested
//   - section + topic:                   <section>/<title>.md
//   - section + group + topic:           <section>/<group>/<title>.md
func ApplyHierarchyPaths(wiki *models.Wiki) {
	if wiki == nil {
		return
	}
	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		sec := strings.TrimSpace(p.Section)
		grp := strings.TrimSpace(p.Group)
		title := strings.TrimSpace(p.Title)
		if title == "" {
			title = fmt.Sprintf("page-%d", i+1)
			p.Title = title
		}

		// Parent: nearest container in the tree.
		switch {
		case grp != "" && grp != title:
			p.Parent = grp
		case sec != "" && sec != title:
			p.Parent = sec
		default:
			// Root-like page (overview often equals section name).
			if p.Parent == sec || p.Parent == title {
				p.Parent = ""
			}
		}

		// Content path from hierarchy (always rebuild so model tree wins).
		var segs []string
		if sec != "" && sec != title {
			segs = append(segs, safeSegment(sec))
		}
		if grp != "" && grp != title && grp != sec {
			segs = append(segs, safeSegment(grp))
		}
		segs = append(segs, safeSegment(title)+".md")
		p.ContentPath = strings.Join(segs, "/")

		if p.DescriptionSlug == "" {
			p.DescriptionSlug = DescriptionSlug(title)
		}
		if p.Track == "" {
			p.Track = models.InferTrack(*p)
		}
		if p.Goal == "" {
			// Light goal for evidence binding; not a planning constraint.
			if sec != "" && sec != title {
				p.Goal = sec + " / " + title
			} else {
				p.Goal = title
			}
		}
	}
}

// FormatInventoryHint is a short non-binding reference for the catalog agent
// (module names / sample paths). Empty when model is nil.
// Business-domain titles are listed first to bias free planning away from
// raw API/entity dumps.
func FormatInventoryHint(wiki *models.Wiki, maxLines int) string {
	if wiki == nil || len(wiki.Pages) == 0 {
		return "(no inventory hint)"
	}
	if maxLines <= 0 {
		maxLines = 40
	}
	var b strings.Builder
	b.WriteString("Sample topics from automated scan (optional). Prefer business capability titles; treat API/entity items as indexes only:\n")
	n := 0
	// Pass 1: business / architecture style sections first
	curSec := ""
	writePage := func(p models.WikiPage) {
		if p.Section != curSec {
			curSec = p.Section
			b.WriteString("- section: ")
			b.WriteString(p.Section)
			b.WriteByte('\n')
		}
		b.WriteString("  - ")
		b.WriteString(p.Title)
		if p.Group != "" {
			b.WriteString(" [")
			b.WriteString(p.Group)
			b.WriteByte(']')
		}
		b.WriteByte('\n')
		n++
	}
	isBiz := func(sec string) bool {
		s := strings.ToLower(sec)
		if strings.Contains(sec, "接口") || strings.Contains(sec, "API") || strings.Contains(sec, "数据库") || strings.Contains(sec, "Data") {
			return false
		}
		return strings.Contains(sec, "模块") || strings.Contains(sec, "架构") || strings.Contains(sec, "管理") ||
			strings.Contains(s, "module") || strings.Contains(s, "architecture") ||
			sec == "项目概述" || sec == "快速开始" || sec == "开发指南"
	}
	for _, p := range wiki.Pages {
		if n >= maxLines {
			break
		}
		if isBiz(p.Section) {
			writePage(p)
		}
	}
	for _, p := range wiki.Pages {
		if n >= maxLines {
			b.WriteString("  …\n")
			break
		}
		if !isBiz(p.Section) {
			writePage(p)
		}
	}
	return b.String()
}
