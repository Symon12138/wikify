// Quality report (QL-4): deterministic per-page + aggregate quality profile
// written to .wikify/meta/quality-report.json on every Export. No LLM, no
// product-domain heuristics — pure counting over final page bodies.
package export

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JSHurt/wikify/internal/evidence"
	"github.com/JSHurt/wikify/internal/models"
	"github.com/JSHurt/wikify/internal/scan"
)

// pageQuality carries per-page QL-2/QL-3 counters threaded from Export's
// validation passes into the quality report.
type pageQuality struct {
	citesTotal, citesValid, citesCorrected, citesDropped int
	brokenWikiLinks                                      int
}

// QualityPage is one page record in meta/quality-report.json.
type QualityPage struct {
	Slug            string `json:"slug"`
	Title           string `json:"title"`
	Track           string `json:"track"`
	Stub            bool   `json:"stub"`
	Runes           int    `json:"runes"`
	H2Count         int    `json:"h2_count"`
	MermaidTotal    int    `json:"mermaid_total"`
	MermaidInjected int    `json:"mermaid_injected"`
	CitesTotal      int    `json:"cites_total"`
	CitesValid      int    `json:"cites_valid"`
	CitesCorrected  int    `json:"cites_corrected"`
	CitesDropped    int    `json:"cites_dropped"`
	BrokenWikiLinks int    `json:"broken_wiki_links"`
	DepthShallow    bool   `json:"depth_shallow"`
}

// QualityReport is the aggregate profile written to meta/quality-report.json.
// CiteValidityPct counts corrected cites as valid (they resolve after the
// fix); ShallowPages counts non-stub pages only (stubs are tracked by
// StubList and handled by --only-stubs, shallow pages by --enrich-shallow).
type QualityReport struct {
	Version           int            `json:"version"`
	Pages             int            `json:"pages"`
	SubstantialPct    float64        `json:"substantial_pct"`
	StubList          []string       `json:"stub_list"`
	CitesTotal        int            `json:"cites_total"`
	CitesValid        int            `json:"cites_valid"`
	CitesCorrected    int            `json:"cites_corrected"`
	CitesDropped      int            `json:"cites_dropped"`
	CiteValidityPct   float64        `json:"cite_validity_pct"`
	DiagramCount      int            `json:"diagram_count"`
	BrokenWikiLinks   int            `json:"broken_wiki_links"`
	ShallowPages      int            `json:"shallow_pages"`
	RepoCoveragePct   float64        `json:"repo_coverage_pct"`
	TrackDistribution map[string]int `json:"track_distribution"`
	PageRecords       []QualityPage  `json:"page_records"`
}

// injectedDiagramSections are the export-owned section headings under which
// ensureMermaidDiagrams places generated (non-LLM) diagrams.
var injectedDiagramSections = []string{
	"代码依赖示意", "Code dependency",
	"结构示意", "Structure diagram",
	"补充示意", "Additional diagrams",
}

var reQualityCitePath = regexp.MustCompile(`file://([^#\s"'()\[\]<>]+)`)

// writeQualityReport builds and writes meta/quality-report.json from the
// final (already formatted) page bodies. stats carries the QL-2 cite counters
// and QL-3 broken-link counters collected during Export.
func writeQualityReport(metaDir string, wiki *models.Wiki, contents map[string]string, model *scan.Model, stats map[string]pageQuality) error {
	if wiki == nil {
		return nil
	}
	rep := QualityReport{
		Version:           1,
		Pages:             len(wiki.Pages),
		StubList:          []string{},
		TrackDistribution: map[string]int{},
		PageRecords:       make([]QualityPage, 0, len(wiki.Pages)),
	}

	// Repo inventory for coverage: cited paths must exist and be code files.
	inRepo := map[string]bool{}
	codeTotal := 0
	if model != nil {
		for _, f := range model.Files {
			rel := strings.TrimPrefix(filepath.ToSlash(f.RelativePath), "./")
			if rel == "" || inRepo[rel] {
				continue
			}
			inRepo[rel] = true
			if scan.IsCodeFile(rel) && !scan.IsNoisePath(rel) {
				codeTotal++
			}
		}
	}
	citedCode := map[string]bool{}

	for i := range wiki.Pages {
		p := wiki.Pages[i]
		body := contents[p.Slug]
		st := stats[p.Slug]
		dr := evidence.DepthScore(body)
		// Synthesized nav pages (section index / arch overview) are short by
		// design — never stub- or shallow-flag them (would trigger LLM redo).
		gen := isGeneratedNavPage(p)
		stub := IsStubPage(body) && !gen
		rec := QualityPage{
			Slug:            p.Slug,
			Title:           p.Title,
			Track:           p.Track,
			Stub:            stub,
			Runes:           len([]rune(body)),
			H2Count:         dr.Sections,
			MermaidTotal:    dr.Mermaids,
			MermaidInjected: countInjectedMermaid(body),
			CitesTotal:      st.citesTotal,
			CitesValid:      st.citesValid,
			CitesCorrected:  st.citesCorrected,
			CitesDropped:    st.citesDropped,
			BrokenWikiLinks: st.brokenWikiLinks,
			DepthShallow:    !gen && !stub && dr.Shallow(),
		}
		rep.PageRecords = append(rep.PageRecords, rec)

		if stub {
			rep.StubList = append(rep.StubList, p.Slug)
		}
		if rec.DepthShallow {
			rep.ShallowPages++
		}
		rep.CitesTotal += rec.CitesTotal
		rep.CitesValid += rec.CitesValid
		rep.CitesCorrected += rec.CitesCorrected
		rep.CitesDropped += rec.CitesDropped
		rep.DiagramCount += rec.MermaidTotal
		rep.BrokenWikiLinks += rec.BrokenWikiLinks
		rep.TrackDistribution[p.Track]++

		for _, m := range reQualityCitePath.FindAllStringSubmatch(body, -1) {
			cp := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(m[1])), "./")
			cp = strings.Trim(cp, "`*")
			if cp != "" && inRepo[cp] && scan.IsCodeFile(cp) && !scan.IsNoisePath(cp) {
				citedCode[cp] = true
			}
		}
	}

	if rep.Pages > 0 {
		rep.SubstantialPct = round1(float64(rep.Pages-len(rep.StubList)) / float64(rep.Pages) * 100)
	}
	if rep.CitesTotal > 0 {
		rep.CiteValidityPct = round1(float64(rep.CitesValid+rep.CitesCorrected) / float64(rep.CitesTotal) * 100)
	}
	if codeTotal > 0 {
		rep.RepoCoveragePct = round1(float64(len(citedCode)) / float64(codeTotal) * 100)
	}

	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(metaDir, "quality-report.json"), b, 0o644)
}

// countInjectedMermaid counts mermaid fences under export-owned diagram
// sections (code dependency / structure / additional). LLM-authored diagrams
// live under content sections and are not counted here.
func countInjectedMermaid(body string) int {
	n := 0
	for _, t := range injectedDiagramSections {
		if sec := sectionByTitle(body, t); sec != "" {
			n += len(extractMermaid(sec))
		}
	}
	return n
}

// sectionByTitle returns the content of "## <title>" up to the next H2 / EOF.
func sectionByTitle(s, title string) string {
	re := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(title) + `\s*$`)
	loc := re.FindStringIndex(s)
	if loc == nil {
		return ""
	}
	rest := s[loc[1]:]
	if next := regexp.MustCompile(`(?m)^##\s+`).FindStringIndex(rest); next != nil {
		return rest[:next[0]]
	}
	return rest
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

// LoadQualityReport reads meta/quality-report.json written by Export.
func LoadQualityReport(workDir string) (*QualityReport, error) {
	data, err := os.ReadFile(filepath.Join(workDir, ".wikify", "meta", "quality-report.json"))
	if err != nil {
		return nil, err
	}
	var r QualityReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Summary renders the ~6-line console digest printed after generate/polish.
func (r *QualityReport) Summary() []string {
	if r == nil {
		return nil
	}
	shallowHint := ""
	if r.ShallowPages > 0 {
		shallowHint = " — try: wikify generate --enrich-shallow"
	}
	return []string{
		fmt.Sprintf("quality: %d page(s), %.1f%% substantial (%d stub)", r.Pages, r.SubstantialPct, len(r.StubList)),
		fmt.Sprintf("quality: cites %d/%d valid (%.1f%%) — %d corrected, %d dropped", r.CitesValid+r.CitesCorrected, r.CitesTotal, r.CiteValidityPct, r.CitesCorrected, r.CitesDropped),
		fmt.Sprintf("quality: %d mermaid diagram(s), %d broken wiki link(s)", r.DiagramCount, r.BrokenWikiLinks),
		fmt.Sprintf("quality: repo coverage %.1f%% of code files cited", r.RepoCoveragePct),
		fmt.Sprintf("quality: %d shallow page(s)%s", r.ShallowPages, shallowHint),
		fmt.Sprintf("quality: tracks %s", formatTrackDistribution(r.TrackDistribution)),
	}
}

// formatTrackDistribution renders track counts in stable rail order.
func formatTrackDistribution(dist map[string]int) string {
	if len(dist) == 0 {
		return "(none)"
	}
	order := []string{models.TrackFoundation, models.TrackBusiness, models.TrackTechnical}
	seen := map[string]bool{}
	var parts []string
	for _, k := range order {
		if n, ok := dist[k]; ok {
			parts = append(parts, fmt.Sprintf("%s %d", k, n))
			seen[k] = true
		}
	}
	var rest []string
	for k := range dist {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		parts = append(parts, fmt.Sprintf("%s %d", k, dist[k]))
	}
	return strings.Join(parts, " / ")
}
