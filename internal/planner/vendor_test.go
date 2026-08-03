package planner

import (
	"regexp"
	"testing"

	"github.com/Symon12138/wikify/internal/evidence"
	"github.com/Symon12138/wikify/internal/scan"
)

var reVendorTitle = regexp.MustCompile(`(?i)crypto|swiper|encutf16|enc.?utf|rabbit|nopadding|jquery|datatables`)

// frameModel mimics the failure repo: committed crypto-js / swiper trees next
// to real business Java code.
func frameModel() *scan.Model {
	return &scan.Model{
		Language: "zh",
		Files: []scan.FileInfo{
			{RelativePath: "webapp/js/crypto-js/enc-utf16.js", Lines: 120},
			{RelativePath: "webapp/js/crypto-js/enc-base64.js", Lines: 100},
			{RelativePath: "webapp/js/crypto-js/pad-nopadding.js", Lines: 40},
			{RelativePath: "webapp/js/crypto-js/rabbit-legacy.js", Lines: 200},
			{RelativePath: "webapp/js/crypto-js/aes.js", Lines: 400},
			{RelativePath: "webapp/js/swiper/swiper.esm.js", Lines: 800},
			{RelativePath: "webapp/js/swiper/swiper.umd.js", Lines: 900},
			{RelativePath: "webapp/js/swiper/swiper-bundle.js", Lines: 950},
			{RelativePath: "src/main/java/com/demo/cust/CustBasicService.java", Lines: 220},
			{RelativePath: "src/main/java/com/demo/cust/CustBasicControl.java", Lines: 150},
			{RelativePath: "src/main/java/com/demo/cust/CustInfo.java", Lines: 90},
		},
	}
}

// Regression for the「Encutf16管理」bug: vendor library clusters must neither
// become business sections nor child capability pages, while real business
// clusters keep their pages.
func TestBuildBusinessDomainsSkipsVendorClusters(t *testing.T) {
	items := buildBusinessDomains(frameModel(), true, 60, nil)
	sawCust := false
	for _, it := range items {
		if reVendorTitle.MatchString(it.Title) || reVendorTitle.MatchString(it.Section) {
			t.Fatalf("vendor cluster leaked into business pages: %q (section %q)", it.Title, it.Section)
		}
		if regexp.MustCompile(`(?i)cust`).MatchString(it.Section) {
			sawCust = true
		}
	}
	if !sawCust {
		t.Fatalf("business cust cluster missing, items=%v", titlesOfPlanned(items))
	}
}

// Full Build must be vendor-free end to end (business + inventory + tree +
// signals all guarded).
func TestBuildSkipsVendorLibraryClusters(t *testing.T) {
	wiki := Build(frameModel(), nil, Options{MaxPages: 80})
	if wiki == nil || len(wiki.Pages) == 0 {
		t.Fatal("empty wiki")
	}
	for _, p := range wiki.Pages {
		if reVendorTitle.MatchString(p.Title) || reVendorTitle.MatchString(p.Section) {
			t.Fatalf("vendor page leaked into catalog: %q (section %q)", p.Title, p.Section)
		}
	}
}

// scope.include explicitly naming the library re-admits its cluster.
func TestBuildBusinessDomainsScopeIncludeOverride(t *testing.T) {
	m := frameModel()
	vd := evidence.NewVendorDetector(m, []string{"webapp/js/crypto-js/**"})
	items := buildBusinessDomains(m, true, 60, vd)
	found := false
	for _, it := range items {
		if regexp.MustCompile(`(?i)crypto`).MatchString(it.Title + " " + it.Section) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("explicitly included crypto-js cluster should produce pages, items=%v", titlesOfPlanned(items))
	}
	// The non-included swiper tree stays out.
	for _, it := range items {
		if regexp.MustCompile(`(?i)swiper`).MatchString(it.Title + " " + it.Section) {
			t.Fatalf("swiper must remain excluded: %q", it.Title)
		}
	}
}

// Inventory aggregates must not build API pages from vendor handler-style
// filenames.
func TestBuildInventorySkipsVendor(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "webapp/static/libs/datatables/dataTables.rowHandler.js", Lines: 300},
			{RelativePath: "webapp/static/libs/datatables/dataTables.colHandler.js", Lines: 280},
			{RelativePath: "src/main/java/com/demo/order/OrderController.java", Lines: 150},
			{RelativePath: "src/main/java/com/demo/order/OrderQueryController.java", Lines: 130},
		},
	}
	items := buildInventory(m, false, 10, nil)
	sawOrder := false
	for _, it := range items {
		if regexp.MustCompile(`(?i)datatables|data tables`).MatchString(it.Title) {
			t.Fatalf("vendor datatables inventory page leaked: %q", it.Title)
		}
		if regexp.MustCompile(`(?i)order`).MatchString(it.Title) {
			sawOrder = true
		}
	}
	if !sawOrder {
		t.Fatalf("business order API aggregate missing, items=%v", titlesOfPlanned(items))
	}
}

// vue-router.js alone must not flip hasAPI; a real controller must.
func TestDetectRepoSignalsIgnoresVendorRouter(t *testing.T) {
	vendorOnly := &scan.Model{Files: []scan.FileInfo{
		{RelativePath: "webapp/static/vue-router/vue-router.js", Lines: 900},
		{RelativePath: "README.md", Lines: 20},
	}}
	if detectRepoSignals(vendorOnly).hasAPI {
		t.Fatal("vendor vue-router.js must not enable hasAPI")
	}
	real := &scan.Model{Files: []scan.FileInfo{
		{RelativePath: "src/main/java/com/demo/controller/OrderController.java", Lines: 150},
	}}
	if !detectRepoSignals(real).hasAPI {
		t.Fatal("real controller should enable hasAPI")
	}
}

// Coverage gap top-up must not treat committed library trees as blind spots.
func TestCoverageGapsSkipVendor(t *testing.T) {
	m := frameModel()
	gaps, _, total := CoverageGaps(m, nil, 3)
	for _, g := range gaps {
		if reVendorTitle.MatchString(g.Segment) {
			t.Fatalf("vendor cluster reported as coverage gap: %q", g.Segment)
		}
	}
	// Only the 3 cust java files count as code inventory (8 vendor js skipped).
	if total != 3 {
		t.Fatalf("totalCode should exclude vendor files, got %d (gaps=%v)", total, gaps)
	}
}

func titlesOfPlanned(items []Planned) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Title)
	}
	return out
}
