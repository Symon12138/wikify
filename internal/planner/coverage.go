// Coverage-gap detection: diff the scanned code inventory against the union of
// evidence already bound to catalog pages, cluster uncovered files by path
// segment, and top the catalog up with technical pages for real blind spots.
//
// Generic structural heuristics only — no product-domain knowledge.
package planner

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/Symon12138/wikify/internal/evidence"
	"github.com/Symon12138/wikify/internal/models"
	"github.com/Symon12138/wikify/internal/scan"
)

// Gap is a cluster of code files no catalog page references.
type Gap struct {
	Segment string   // packageDomain cluster key
	Files   []string // uncovered files (repo-relative, slash-separated)
	Count   int      // len(Files)
}

// normEvidencePath canonicalizes a repo-relative path for coverage matching.
func normEvidencePath(p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	return strings.TrimPrefix(p, "./")
}

// CoverageGaps reports code files not referenced by any page's DependentFiles,
// clustered by packageDomain. A cluster only becomes a Gap when NONE of its
// files are covered (zero-overlap rule): partially covered clusters are assumed
// to be represented by an existing page whose evidence list was truncated.
// Clusters smaller than minCluster (default 4 when <=0) are dropped as noise.
// Returns gaps (sorted by Count desc, then Segment asc), plus covered/totalCode
// counts over the IsCodeFile && !IsNoisePath inventory.
func CoverageGaps(model *scan.Model, wiki *models.Wiki, minCluster int) (gaps []Gap, covered, totalCode int) {
	if model == nil {
		return nil, 0, 0
	}
	if minCluster <= 0 {
		minCluster = 4
	}
	coveredSet := map[string]bool{}
	if wiki != nil {
		for _, p := range wiki.Pages {
			for _, dep := range p.DependentFiles {
				if n := normEvidencePath(dep); n != "" {
					coveredSet[n] = true
				}
			}
		}
	}

	type bucket struct {
		files    []string
		coveredN int
	}
	buckets := map[string]*bucket{}
	vd := evidence.NewVendorDetector(model, nil)
	for _, f := range model.Files {
		if !scan.IsCodeFile(f.RelativePath) || scan.IsNoisePath(f.RelativePath) {
			continue
		}
		// Committed third-party library files are not coverage debt: they must
		// neither count as uncovered code nor form gap clusters that would be
		// topped up into vendor scaffold pages.
		if vd.IsVendor(f.RelativePath) {
			continue
		}
		totalCode++
		rel := normEvidencePath(f.RelativePath)
		isCov := coveredSet[rel]
		if isCov {
			covered++
		}
		seg := packageDomain(f.RelativePath)
		if seg == "" {
			continue
		}
		b := buckets[seg]
		if b == nil {
			b = &bucket{}
			buckets[seg] = b
		}
		if isCov {
			b.coveredN++
		} else {
			b.files = append(b.files, rel)
		}
	}

	for seg, b := range buckets {
		// Zero-overlap rule: any covered file in the cluster kills the gap.
		if b.coveredN > 0 || len(b.files) < minCluster {
			continue
		}
		files := append([]string(nil), b.files...)
		sort.Strings(files)
		gaps = append(gaps, Gap{Segment: seg, Files: files, Count: len(files)})
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].Count != gaps[j].Count {
			return gaps[i].Count > gaps[j].Count
		}
		return gaps[i].Segment < gaps[j].Segment
	})
	return gaps, covered, totalCode
}

// TopUpFromGaps appends up to maxAdd technical-track pages covering the given
// gaps under the modules taxonomy section. Titles come from the gap segment via
// composeChineseTitle (zh) / humanize (en) and are deduped with normalizeTitle
// against existing Titles, Sections and ContentPath segments. Each new page is
// pre-bound with the gap's largest files (by line count, cap 8) so downstream
// evidence binding keeps them. Returns the number of pages added.
func TopUpFromGaps(wiki *models.Wiki, model *scan.Model, gaps []Gap, maxAdd int, zh bool) int {
	if wiki == nil || model == nil || len(gaps) == 0 || maxAdd <= 0 {
		return 0
	}
	tax := chooseTaxonomy(model, zh)
	mod := tax.modules

	seen := map[string]bool{}
	note := func(s string) {
		if n := normalizeTitle(s); n != "" {
			seen[n] = true
		}
	}
	for _, p := range wiki.Pages {
		note(p.Title)
		note(p.Section)
		for _, segp := range strings.Split(filepath.ToSlash(p.ContentPath), "/") {
			note(strings.TrimSuffix(segp, ".md"))
		}
	}

	lines := map[string]int{}
	for _, f := range model.Files {
		lines[normEvidencePath(f.RelativePath)] = f.Lines
	}

	added := 0
	for _, g := range gaps {
		if added >= maxAdd {
			break
		}
		title := humanize(g.Segment)
		if zh {
			title = composeChineseTitle(g.Segment, "module")
		}
		if isLowQuality(title) {
			title = humanize(g.Segment)
		}
		if isLowQuality(title) || seen[normalizeTitle(title)] {
			continue
		}
		deps := append([]string(nil), g.Files...)
		sort.SliceStable(deps, func(i, j int) bool {
			li, lj := lines[deps[i]], lines[deps[j]]
			if li != lj {
				return li > lj
			}
			return deps[i] < deps[j]
		})
		if len(deps) > 8 {
			deps = deps[:8]
		}
		safe := safeSegment(title)
		page := models.WikiPage{
			Title:   title,
			Slug:    models.MakeSlug(len(wiki.Pages)+1, title),
			Level:   "Advanced",
			Section: mod,
			Group:   mod,
			Parent:  mod,
			Goal: goal(zh,
				"补齐目录盲区：基于 "+g.Segment+" 路径下的代码说明该模块的职责与实现。",
				"Cover catalog blind spot: responsibilities and implementation under "+g.Segment+"."),
			DependentFiles:  deps,
			ContentPath:     mod + "/" + safe + "/" + safe + ".md",
			DescriptionSlug: DescriptionSlug(title),
			Track:           models.TrackTechnical,
		}
		wiki.Pages = append(wiki.Pages, page)
		note(title)
		added++
	}
	if added > 0 {
		EnsureTracks(wiki)
	}
	return added
}
