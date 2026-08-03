package evidence

import (
	"strings"
	"testing"

	"github.com/Symon12138/wikify/internal/scan"
)

// Committed library trees (crypto-js style) must be vendor even without any
// naming-pattern or asset-dir help: the known-lib directory segment alone is a
// strong signal, and business JS in the same model stays clean.
func TestVendorLikenessKnownLibDir(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "webapp/js/crypto-js/enc-utf16.js", Lines: 100},
			{RelativePath: "webapp/js/crypto-js/pad-nopadding.js", Lines: 40},
			{RelativePath: "webapp/js/crypto-js/rabbit-legacy.js", Lines: 180},
			{RelativePath: "webapp/js/crypto-js/aes.js", Lines: 300},
			{RelativePath: "src/order/order-list.js", Lines: 220},
			{RelativePath: "src/api/http.js", Lines: 90},
		},
		ImportEdges: []scan.ImportEdge{
			{From: "src/order/order-list.js", To: "src/api/http.js", Kind: "import"},
		},
	}
	d := NewVendorDetector(m, nil)
	for _, rel := range []string{
		"webapp/js/crypto-js/enc-utf16.js",
		"webapp/js/crypto-js/pad-nopadding.js",
		"webapp/js/crypto-js/rabbit-legacy.js",
	} {
		if !d.IsVendor(rel) {
			t.Errorf("%s: expected vendor, likeness=%v", rel, d.Likeness(rel))
		}
	}
	// One-shot convenience form agrees.
	if VendorLikeness("webapp/js/crypto-js/aes.js", m) < VendorThreshold {
		t.Errorf("VendorLikeness(aes.js) below threshold: %v", VendorLikeness("webapp/js/crypto-js/aes.js", m))
	}
	// Import-linked business JS stays clean.
	for _, rel := range []string{"src/order/order-list.js", "src/api/http.js"} {
		if d.IsVendor(rel) {
			t.Errorf("%s: business js must not be vendor, likeness=%v", rel, d.Likeness(rel))
		}
	}
}

func TestVendorLikenessDistAndVersionedNames(t *testing.T) {
	m := &scan.Model{Files: []scan.FileInfo{
		{RelativePath: "static/js/swiper.esm.js"},
		{RelativePath: "static/js/foo.bundle.js"},
		{RelativePath: "webapp/script/jquery-3.6.0.js"},
		{RelativePath: "src/app/main.js"},
		{RelativePath: "vue.config.js"},
	}}
	d := NewVendorDetector(m, nil)
	for _, rel := range []string{
		"static/js/swiper.esm.js",       // known lib stem + dist name + asset dir
		"static/js/foo.bundle.js",       // dist name + asset dir (no known lib needed)
		"webapp/script/jquery-3.6.0.js", // versioned known lib, no asset dir needed
	} {
		if !d.IsVendor(rel) {
			t.Errorf("%s: expected vendor, likeness=%v", rel, d.Likeness(rel))
		}
	}
	// Ordinary business file under src: zero vendor evidence.
	if got := d.Likeness("src/app/main.js"); got != 0 {
		t.Errorf("src/app/main.js: expected likeness 0, got %v", got)
	}
	// Framework-named project config must never be flagged.
	if got := d.Likeness("vue.config.js"); got >= VendorThreshold {
		t.Errorf("vue.config.js: must not be vendor, likeness=%v", got)
	}
}

// A directory full of import-isolated same-ext files is the shape of a dropped
// library: with the asset-dir hint it crosses the threshold, without it (e.g.
// standalone scripts under tools/) it stays below. When the model has no import
// graph at all the isolation signal is disabled entirely.
func TestVendorLikenessIsolatedCluster(t *testing.T) {
	files := []scan.FileInfo{
		{RelativePath: "src/a.js"}, {RelativePath: "src/b.js"},
	}
	for _, dir := range []string{"static/pages", "tools/batch"} {
		for _, n := range []string{"one", "two", "three", "four", "five", "six"} {
			files = append(files, scan.FileInfo{RelativePath: dir + "/" + n + ".js"})
		}
	}
	m := &scan.Model{
		Files: files,
		ImportEdges: []scan.ImportEdge{
			{From: "src/a.js", To: "src/b.js", Kind: "import"},
		},
	}
	d := NewVendorDetector(m, nil)
	if !d.IsVendor("static/pages/one.js") {
		t.Errorf("isolated cluster under static/: expected vendor, likeness=%v", d.Likeness("static/pages/one.js"))
	}
	if d.IsVendor("tools/batch/one.js") {
		t.Errorf("isolated cluster outside asset dirs must stay below threshold, likeness=%v", d.Likeness("tools/batch/one.js"))
	}
	// No import graph → no isolation signal → static cluster only gets the
	// weak asset-dir hint.
	noGraph := NewVendorDetector(&scan.Model{Files: files}, nil)
	if noGraph.IsVendor("static/pages/one.js") {
		t.Errorf("without import edges the cluster signal must be off, likeness=%v", noGraph.Likeness("static/pages/one.js"))
	}
}

// Business JS keeps its head even under static/ as long as it participates in
// the import graph.
func TestVendorLikenessBusinessJsKept(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "static/js/app/checkout.js"},
			{RelativePath: "src/api/http.js"},
		},
		ImportEdges: []scan.ImportEdge{
			{From: "static/js/app/checkout.js", To: "src/api/http.js", Kind: "import"},
		},
	}
	d := NewVendorDetector(m, nil)
	if d.IsVendor("static/js/app/checkout.js") {
		t.Errorf("linked business js under static/ must not be vendor, likeness=%v", d.Likeness("static/js/app/checkout.js"))
	}
}

// Application code living in a framework-named directory (src/react/…) is
// rescued by the outside-linkage discount.
func TestVendorLikenessLinkedLibNameDirKept(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "src/react/App.jsx"},
			{RelativePath: "src/api/client.js"},
		},
		ImportEdges: []scan.ImportEdge{
			{From: "src/react/App.jsx", To: "src/api/client.js", Kind: "import"},
		},
	}
	d := NewVendorDetector(m, nil)
	if d.IsVendor("src/react/App.jsx") {
		t.Errorf("import-linked app code in lib-named dir must stay below threshold, likeness=%v", d.Likeness("src/react/App.jsx"))
	}
}

// scope.include patterns that explicitly name a library exempt its paths;
// broad includes do not.
func TestVendorDetectorScopeIncludeExempt(t *testing.T) {
	m := &scan.Model{Files: []scan.FileInfo{
		{RelativePath: "webapp/js/crypto-js/aes.js"},
	}}
	explicit := NewVendorDetector(m, []string{"webapp/js/crypto-js/**"})
	if got := explicit.Likeness("webapp/js/crypto-js/aes.js"); got != 0 {
		t.Errorf("explicit lib include must exempt, likeness=%v", got)
	}
	for _, pat := range []string{"webapp/**", "static/**", "**"} {
		broad := NewVendorDetector(m, []string{pat})
		if !broad.IsVendor("webapp/js/crypto-js/aes.js") {
			t.Errorf("broad include %q must not exempt, likeness=%v", pat, broad.Likeness("webapp/js/crypto-js/aes.js"))
		}
	}
}

// Business pages must not bind committed library files as evidence.
func TestPickDependentFilesDemotesVendor(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "webapp/static/libs/jquery/jquery.pay.js", Lines: 400},
			{RelativePath: "src/main/java/com/demo/pay/PayController.java", Lines: 120},
			{RelativePath: "src/main/java/com/demo/pay/PayService.java", Lines: 200},
			{RelativePath: "src/main/java/com/demo/pay/PayServiceImpl.java", Lines: 260},
		},
	}
	out := PickDependentFiles(m, "支付管理", "说明支付业务规则与服务编排", 3)
	if len(out) == 0 {
		t.Fatal("expected deps")
	}
	joined := strings.Join(out, "\n")
	if strings.Contains(joined, "jquery") {
		t.Fatalf("vendor jquery plugin must not crowd business evidence: %v", out)
	}
	if !strings.Contains(joined, "PayService") {
		t.Fatalf("expected PayService* evidence, got %v", out)
	}
}

// Pages explicitly about front-end components may still cite library files
// (soft demotion instead of exclusion).
func TestPickDependentFilesVendorTopicStillCitable(t *testing.T) {
	m := &scan.Model{
		Files: []scan.FileInfo{
			{RelativePath: "webapp/static/js/crypto-js/aes.js", Lines: 300},
			{RelativePath: "webapp/static/js/crypto-js/enc-utf16.js", Lines: 80},
			{RelativePath: "src/main/java/com/demo/order/OrderService.java", Lines: 200},
		},
	}
	out := PickDependentFiles(m, "前端加密组件", "说明 crypto-js 加密组件的集成方式", 5)
	if !strings.Contains(strings.Join(out, "\n"), "crypto-js/") {
		t.Fatalf("vendor-topic page should still cite crypto-js files, got %v", out)
	}
}
