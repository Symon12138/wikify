package planner

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Symon12138/wikify/internal/models"
	"github.com/Symon12138/wikify/internal/scan"
)

// coverageFixture: one fully covered cluster (order) + one uncovered (billing).
func coverageFixture() (*scan.Model, *models.Wiki) {
	m := &scan.Model{
		Name:     "demo",
		Language: "en",
		Files: []scan.FileInfo{
			{RelativePath: "order/OrderService.java", Lines: 100},
			{RelativePath: "order/OrderController.java", Lines: 90},
			{RelativePath: "order/OrderRepo.java", Lines: 80},
			{RelativePath: "order/OrderHelper.java", Lines: 70},
			{RelativePath: "billing/InvoiceService.java", Lines: 200},
			{RelativePath: "billing/InvoiceCalc.java", Lines: 150},
			{RelativePath: "billing/InvoiceRepo.java", Lines: 120},
			{RelativePath: "billing/InvoiceJob.java", Lines: 110},
			{RelativePath: "billing/InvoiceMap.java", Lines: 60},
			{RelativePath: "node_modules/pkg/x.js", Lines: 500}, // noise: ignored
			{RelativePath: "README.md", Lines: 40},              // non-code: ignored
		},
	}
	w := &models.Wiki{Pages: []models.WikiPage{{
		Title: "Order Flow", Section: "Order", ContentPath: "order/order-flow.md",
		// Mixed separators + ./ prefix exercise path normalization.
		DependentFiles: []string{
			"order/OrderService.java",
			"order\\OrderController.java",
			"./order/OrderRepo.java",
			"order/OrderHelper.java",
		},
	}}}
	return m, w
}

func TestCoverageGapsDetectsUncoveredCluster(t *testing.T) {
	m, w := coverageFixture()
	gaps, covered, total := CoverageGaps(m, w, 4)
	if total != 9 {
		t.Fatalf("totalCode = %d, want 9 (noise + non-code excluded)", total)
	}
	if covered != 4 {
		t.Fatalf("covered = %d, want 4", covered)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %+v, want exactly 1 (billing)", gaps)
	}
	g := gaps[0]
	if g.Segment != "billing" || g.Count != 5 || len(g.Files) != 5 {
		t.Fatalf("gap = %+v, want billing cluster of 5", g)
	}
	for _, f := range g.Files {
		if !strings.HasPrefix(f, "billing/") {
			t.Fatalf("gap file %q not under billing/", f)
		}
	}
}

func TestCoverageGapsZeroOverlapRule(t *testing.T) {
	m, w := coverageFixture()
	// One billing file already covered -> the whole billing cluster is assumed
	// represented (evidence lists are truncated at 8) and must NOT be a gap.
	w.Pages[0].DependentFiles = append(w.Pages[0].DependentFiles, "billing/InvoiceCalc.java")
	gaps, covered, total := CoverageGaps(m, w, 4)
	if total != 9 || covered != 5 {
		t.Fatalf("covered/total = %d/%d, want 5/9", covered, total)
	}
	if len(gaps) != 0 {
		t.Fatalf("gaps = %+v, want none (zero-overlap rule)", gaps)
	}
}

func TestCoverageGapsMinClusterThreshold(t *testing.T) {
	m := &scan.Model{
		Name: "demo", Language: "en",
		Files: []scan.FileInfo{
			{RelativePath: "billing/InvoiceService.java", Lines: 10},
			{RelativePath: "billing/InvoiceCalc.java", Lines: 10},
			{RelativePath: "billing/InvoiceRepo.java", Lines: 10},
		},
	}
	w := &models.Wiki{}
	if gaps, _, _ := CoverageGaps(m, w, 4); len(gaps) != 0 {
		t.Fatalf("3-file cluster passed minCluster=4: %+v", gaps)
	}
	if gaps, _, _ := CoverageGaps(m, w, 3); len(gaps) != 1 {
		t.Fatalf("3-file cluster should pass minCluster=3")
	}
}

func TestTopUpFromGapsAddsPreboundPages(t *testing.T) {
	m, w := coverageFixture()
	gaps, _, _ := CoverageGaps(m, w, 4)
	added := TopUpFromGaps(w, m, gaps, 6, false)
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	p := w.Pages[len(w.Pages)-1]
	if p.Title != "Billing" {
		t.Fatalf("title = %q, want Billing", p.Title)
	}
	if p.Section != "Core Modules" || p.Track != models.TrackTechnical || p.Level != "Advanced" {
		t.Fatalf("page meta = section %q track %q level %q", p.Section, p.Track, p.Level)
	}
	if len(p.DependentFiles) != 5 {
		t.Fatalf("deps = %v, want all 5 billing files", p.DependentFiles)
	}
	// Pre-bound deps sorted by line count desc.
	if p.DependentFiles[0] != "billing/InvoiceService.java" {
		t.Fatalf("deps[0] = %q, want largest file first", p.DependentFiles[0])
	}
	if p.Slug == "" || p.DescriptionSlug == "" {
		t.Fatalf("slug/descriptionSlug missing: %+v", p)
	}
}

func TestTopUpFromGapsCapsDepsAtEight(t *testing.T) {
	var files []scan.FileInfo
	for i := 0; i < 10; i++ {
		files = append(files, scan.FileInfo{RelativePath: fmt.Sprintf("shipping/Ship%02dService.java", i), Lines: 10 + i})
	}
	m := &scan.Model{Name: "demo", Language: "en", Files: files}
	w := &models.Wiki{}
	gaps, _, _ := CoverageGaps(m, w, 4)
	if TopUpFromGaps(w, m, gaps, 6, false) != 1 {
		t.Fatalf("expected 1 page added")
	}
	if got := len(w.Pages[0].DependentFiles); got != 8 {
		t.Fatalf("deps capped at 8, got %d", got)
	}
}

func TestTopUpFromGapsTitleDedupeAndMaxAdd(t *testing.T) {
	m, w := coverageFixture()
	gaps, _, _ := CoverageGaps(m, w, 4)

	// Dedupe against existing Title.
	wt := &models.Wiki{Pages: append([]models.WikiPage{{Title: "Billing", Section: "Money"}}, w.Pages...)}
	if n := TopUpFromGaps(wt, m, gaps, 6, false); n != 0 {
		t.Fatalf("title dedupe failed: added %d", n)
	}
	// Dedupe against ContentPath segment.
	wp := &models.Wiki{Pages: append([]models.WikiPage{{Title: "Money Stuff", ContentPath: "billing/index.md"}}, w.Pages...)}
	if n := TopUpFromGaps(wp, m, gaps, 6, false); n != 0 {
		t.Fatalf("content-path dedupe failed: added %d", n)
	}

	// maxAdd caps additions across gaps.
	var files []scan.FileInfo
	for _, dom := range []string{"billing", "shipping", "catalog"} {
		for i := 0; i < 4; i++ {
			files = append(files, scan.FileInfo{RelativePath: fmt.Sprintf("%s/File%dService.java", dom, i), Lines: 10})
		}
	}
	m2 := &scan.Model{Name: "demo", Language: "en", Files: files}
	w2 := &models.Wiki{}
	gaps2, _, _ := CoverageGaps(m2, w2, 4)
	if len(gaps2) != 3 {
		t.Fatalf("want 3 gaps, got %+v", gaps2)
	}
	if n := TopUpFromGaps(w2, m2, gaps2, 2, false); n != 2 {
		t.Fatalf("maxAdd=2 but added %d", n)
	}
}

func TestTopUpFromGapsChineseTitles(t *testing.T) {
	m, w := coverageFixture()
	m.Language = "zh"
	gaps, _, _ := CoverageGaps(m, w, 4)
	if TopUpFromGaps(w, m, gaps, 6, true) != 1 {
		t.Fatalf("expected 1 page added")
	}
	p := w.Pages[len(w.Pages)-1]
	if !strings.HasSuffix(p.Title, "模块") {
		t.Fatalf("zh title = %q, want …模块", p.Title)
	}
	if p.Section != "核心模块详解" {
		t.Fatalf("zh section = %q, want 核心模块详解", p.Section)
	}
}

func TestSuggestMaxPagesFormula(t *testing.T) {
	// Small repo: 20 code files, 1 module -> 12 + 20/8 + 2 = 16.
	var files []scan.FileInfo
	for i := 0; i < 20; i++ {
		files = append(files, scan.FileInfo{RelativePath: fmt.Sprintf("pkg/file%02d.go", i)})
	}
	small := &scan.Model{Name: "s", Files: files, Modules: []scan.ModuleSummary{{Name: "pkg", Path: "pkg"}}}
	if got := SuggestMaxPages(small); got != 16 {
		t.Fatalf("small = %d, want 16", got)
	}
	if got := SuggestMaxPages(small); got < 12 || got > 25 {
		t.Fatalf("small = %d, want within [12,25]", got)
	}

	// Large synthetic repo caps at 200.
	var big []scan.FileInfo
	for i := 0; i < 3000; i++ {
		big = append(big, scan.FileInfo{RelativePath: fmt.Sprintf("p%d/f%d.go", i/50, i)})
	}
	var mods []scan.ModuleSummary
	for i := 0; i < 20; i++ {
		mods = append(mods, scan.ModuleSummary{Name: fmt.Sprintf("m%d", i)})
	}
	large := &scan.Model{Name: "l", Files: big, Modules: mods}
	if got := SuggestMaxPages(large); got != 200 {
		t.Fatalf("large = %d, want 200 cap", got)
	}

	// No model / empty inventory keeps the legacy 120 fallback.
	if got := SuggestMaxPages(nil); got != 120 {
		t.Fatalf("nil model = %d, want 120", got)
	}
	if got := SuggestMaxPages(&scan.Model{Name: "e"}); got != 120 {
		t.Fatalf("empty model = %d, want 120", got)
	}
}

func TestMultiModuleSignalFromManifestRoots(t *testing.T) {
	// >=2 manifest roots -> multiModule even without derived Modules.
	m1 := &scan.Model{Name: "a", ManifestRoots: []string{"svc-a", "svc-b"}}
	if !detectRepoSignals(m1).multiModule {
		t.Fatalf("2 manifest roots should imply multiModule")
	}
	// A single (root) manifest is authoritative: Modules>=2 no longer forces it.
	m2 := &scan.Model{Name: "b", ManifestRoots: []string{"."},
		Modules: []scan.ModuleSummary{{Name: "x"}, {Name: "y"}}}
	if detectRepoSignals(m2).multiModule {
		t.Fatalf("single manifest root must override Modules>=2 fallback")
	}
	// No manifest info at all -> legacy Modules>=2 fallback still applies.
	m3 := &scan.Model{Name: "c",
		Modules: []scan.ModuleSummary{{Name: "x"}, {Name: "y"}}}
	if !detectRepoSignals(m3).multiModule {
		t.Fatalf("empty ManifestRoots should fall back to Modules>=2")
	}
}

func multiRootFixture(roots []string) *scan.Model {
	return &scan.Model{
		Name: "demo", Language: "en",
		ManifestRoots: roots,
		Modules: []scan.ModuleSummary{
			{Name: "svc-a", Path: "svc-a"}, {Name: "svc-b", Path: "svc-b"},
		},
		Files: []scan.FileInfo{
			{RelativePath: "svc-a/pom.xml", Lines: 30},
			{RelativePath: "svc-b/pom.xml", Lines: 30},
			{RelativePath: "svc-a/src/order/OrderCreateService.java", Lines: 100},
			{RelativePath: "svc-a/src/order/OrderCancelService.java", Lines: 90},
			{RelativePath: "svc-a/src/order/OrderQueryService.java", Lines: 80},
			{RelativePath: "svc-b/src/order/OrderSyncService.java", Lines: 100},
			{RelativePath: "svc-b/src/order/OrderAuditService.java", Lines: 90},
			{RelativePath: "svc-b/src/order/OrderReplayService.java", Lines: 80},
		},
	}
}

func TestMultiRootBusinessClustersStaySeparate(t *testing.T) {
	m := multiRootFixture([]string{"svc-a", "svc-b"})
	items := buildBusinessDomains(m, false, 60, nil)
	sections := map[string]bool{}
	for _, it := range items {
		if it.Parent == "" { // overview pages define sections
			sections[it.Section] = true
		}
	}
	if !sections["Svc A Order"] || !sections["Svc B Order"] {
		t.Fatalf("multi-root clusters merged; sections = %v", sections)
	}

	// Single root: same files cluster into one plain "Order" section.
	s := multiRootFixture([]string{"."})
	items = buildBusinessDomains(s, false, 60, nil)
	overviews := 0
	for _, it := range items {
		if it.Parent == "" {
			overviews++
			if it.Section != "Order" {
				t.Fatalf("single-root section = %q, want Order", it.Section)
			}
		}
	}
	if overviews != 1 {
		t.Fatalf("single-root overviews = %d, want 1", overviews)
	}
}

func TestMultiRootArchitectureChildren(t *testing.T) {
	m := multiRootFixture([]string{"svc-a", "svc-b"})
	tree := buildDefaultTree(m, false, 0)
	found := map[string]bool{}
	for _, p := range tree {
		if p.Section == "System Architecture" && p.Parent == "System Architecture" {
			found[p.Title] = true
		}
	}
	if !found["Svc A"] || !found["Svc B"] {
		t.Fatalf("per-root architecture children missing; arch kids = %v", found)
	}

	// Single-root repos get no per-root children in the architecture section
	// (the same names may still appear as Core Modules children — that's fine).
	s := multiRootFixture([]string{"."})
	for _, p := range buildDefaultTree(s, false, 0) {
		if p.Section == "System Architecture" && (p.Title == "Svc A" || p.Title == "Svc B") {
			t.Fatalf("single-root repo should not emit per-root arch child %q", p.Title)
		}
	}
}

func TestEngineeringEvidenceAndPreboundSeeds(t *testing.T) {
	m := &scan.Model{
		Name: "demo", Language: "en",
		Files: []scan.FileInfo{
			{RelativePath: ".github/workflows/ci.yml", Lines: 40},
			{RelativePath: "Dockerfile", Lines: 20},
			{RelativePath: "app/Main.java", Lines: 60},
		},
	}
	sig := detectRepoSignals(m)
	if !sig.hasDeploy {
		t.Fatalf("ci.yml + Dockerfile should set hasDeploy")
	}
	joined := strings.Join(sig.DeployFiles, "|")
	if !strings.Contains(joined, ".github/workflows/ci.yml") || !strings.Contains(joined, "Dockerfile") {
		t.Fatalf("DeployFiles = %v", sig.DeployFiles)
	}
	ev := EngineeringEvidence(m)
	if len(ev["deploy"]) != 2 {
		t.Fatalf("EngineeringEvidence deploy = %v", ev["deploy"])
	}

	// Seed pages carry the evidence pre-bound, all the way through Build/toWiki.
	w := Build(m, nil, Options{MaxPages: 60})
	var buildRelease *models.WikiPage
	for i := range w.Pages {
		if w.Pages[i].Title == "Build and release" {
			buildRelease = &w.Pages[i]
		}
	}
	if buildRelease == nil {
		t.Fatalf("Build and release seed missing; titles = %v", titlesOf(w))
	}
	if len(buildRelease.DependentFiles) == 0 {
		t.Fatalf("Build and release page has no pre-bound deps")
	}
	if !strings.Contains(strings.Join(buildRelease.DependentFiles, "|"), "Dockerfile") {
		t.Fatalf("deps = %v, want Dockerfile", buildRelease.DependentFiles)
	}

	// MergeEngineeringSeeds path (polish) also stamps deps.
	wiki := &models.Wiki{Pages: []models.WikiPage{{Title: "Project Overview", Section: "Project Overview", Track: models.TrackFoundation}}}
	if n := MergeEngineeringSeeds(wiki, m, 60); n == 0 {
		t.Fatalf("expected engineering seeds merged")
	}
	for _, p := range wiki.Pages {
		if p.Title == "Deployment and Operations" && len(p.DependentFiles) == 0 {
			t.Fatalf("merged ops index missing pre-bound deps")
		}
	}
}

func TestBudgetScaledSectionCap(t *testing.T) {
	// 16 domains x 3 files: small biz budget keeps the legacy 12-section cap,
	// a big budget admits all 16 (cap = clamp(bizBudget/4, 12, 24)).
	var files []scan.FileInfo
	for i := 0; i < 16; i++ {
		dom := fmt.Sprintf("domain%c%c", 'a'+i/4, 'a'+i%4)
		for j := 0; j < 3; j++ {
			files = append(files, scan.FileInfo{RelativePath: fmt.Sprintf("%s/File%dService.java", dom, j), Lines: 20})
		}
	}
	m := &scan.Model{Name: "demo", Language: "en", Files: files}

	countOverviews := func(items []Planned) int {
		n := 0
		for _, it := range items {
			if it.Parent == "" {
				n++
			}
		}
		return n
	}
	if got := countOverviews(buildBusinessDomains(m, false, 40, nil)); got != 12 {
		t.Fatalf("bizBudget=40 sections = %d, want 12 (floor cap)", got)
	}
	if got := countOverviews(buildBusinessDomains(m, false, 96, nil)); got != 16 {
		t.Fatalf("bizBudget=96 sections = %d, want all 16", got)
	}
}

func TestClusterChildLimitScalesWithSize(t *testing.T) {
	// 40-file cluster: child limit = clamp(3 + 40/6, 3, 8) = 8 (was fixed 5).
	var files []scan.FileInfo
	for i := 0; i < 40; i++ {
		files = append(files, scan.FileInfo{
			RelativePath: fmt.Sprintf("bigdomain/Stem%c%cService.java", 'A'+i/5, 'a'+i%5), Lines: 30})
	}
	m := &scan.Model{Name: "demo", Language: "en", Files: files}
	items := buildBusinessDomains(m, false, 60, nil)
	children := 0
	for _, it := range items {
		if it.Section == "Bigdomain" && it.Parent != "" {
			children++
		}
	}
	if children != 8 {
		t.Fatalf("children = %d, want 8 for a 40-file cluster", children)
	}

	// Small cluster (6 files) stays at the 3-child floor.
	var small []scan.FileInfo
	for i := 0; i < 6; i++ {
		small = append(small, scan.FileInfo{
			RelativePath: fmt.Sprintf("tinydomain/Part%c%cService.java", 'A'+i, 'a'+i), Lines: 30})
	}
	sm := &scan.Model{Name: "demo", Language: "en", Files: small}
	children = 0
	for _, it := range buildBusinessDomains(sm, false, 60, nil) {
		if it.Section == "Tinydomain" && it.Parent != "" {
			children++
		}
	}
	if children > 4 {
		t.Fatalf("small-cluster children = %d, want <= 4 (3 + 6/6)", children)
	}
}

func TestModuleChildrenCapScalesWithTechBudget(t *testing.T) {
	var mods []scan.ModuleSummary
	for i := 0; i < 12; i++ {
		mods = append(mods, scan.ModuleSummary{Name: fmt.Sprintf("billing%02d", i), Path: fmt.Sprintf("billing%02d", i)})
	}
	m := &scan.Model{
		Name: "demo", Language: "en", Modules: mods,
		Files: []scan.FileInfo{{RelativePath: "billing00/Main.java", Lines: 10}},
	}
	countModuleKids := func(tree []Planned) int {
		n := 0
		for _, p := range tree {
			if p.Section == "Core Modules" && p.Parent == "Core Modules" {
				n++
			}
		}
		return n
	}
	if got := countModuleKids(buildDefaultTree(m, false, 0)); got != 8 {
		t.Fatalf("default module cap = %d, want 8", got)
	}
	if got := countModuleKids(buildDefaultTree(m, false, 48)); got != 12 {
		t.Fatalf("techBudget=48 module cap = %d, want 12", got)
	}
}
