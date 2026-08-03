package planner

import (
	"strings"
	"testing"

	"github.com/JSHurt/wikify/internal/models"
	"github.com/JSHurt/wikify/internal/scan"
)

// devTaskModel builds a scan model from repo-relative paths only; the cookbook
// seed must be decidable from structure alone.
func devTaskModel(lang string, rels ...string) *scan.Model {
	m := &scan.Model{Name: "demo", Language: lang}
	for _, rel := range rels {
		m.Files = append(m.Files, scan.FileInfo{RelativePath: rel, Lines: 40})
	}
	return m
}

func devTaskPageByTitle(w *models.Wiki, title string) *models.WikiPage {
	for i := range w.Pages {
		if w.Pages[i].Title == title {
			return &w.Pages[i]
		}
	}
	return nil
}

// devTaskLayeredRepo is a generic layered repo: entry points, business logic,
// persistence and data shapes, plus enough unrelated files to clear the size
// gate. No product vocabulary.
var devTaskLayeredRepo = []string{
	"src/main/java/com/demo/controller/OrderController.java",
	"src/main/java/com/demo/service/OrderService.java",
	"src/main/java/com/demo/dao/OrderDao.java",
	"src/main/java/com/demo/entity/Order.java",
	"src/main/java/com/demo/controller/ItemController.java",
	"src/main/java/com/demo/service/ItemService.java",
	"src/main/java/com/demo/util/StringUtils.java",
	"src/main/java/com/demo/util/DateUtils.java",
	"src/main/java/com/demo/util/JsonUtils.java",
	"src/main/resources/application.yml",
	"src/test/java/com/demo/OrderTest.java",
	"README.md",
	"pom.xml",
}

func TestDevTaskSeedPlannedWhenLayersEvidenced(t *testing.T) {
	m := devTaskModel("zh", devTaskLayeredRepo...)
	w := Build(m, nil, Options{MaxPages: 120})

	p := devTaskPageByTitle(w, "典型开发任务")
	if p == nil {
		t.Fatalf("典型开发任务 seed missing; titles = %v", titlesOf(w))
	}
	if p.Section != "开发指南" || p.Parent != "开发指南" {
		t.Fatalf("section/parent = %q/%q, want 开发指南/开发指南", p.Section, p.Parent)
	}
	if p.ContentPath != "开发指南/典型开发任务.md" {
		t.Fatalf("content path = %q, want 开发指南/典型开发任务.md", p.ContentPath)
	}
	if p.Level != "Intermediate" {
		t.Fatalf("level = %q, want Intermediate", p.Level)
	}
	if len(p.DependentFiles) == 0 {
		t.Fatalf("典型开发任务 has no pre-bound deps")
	}
	if len(p.DependentFiles) > 8 {
		t.Fatalf("deps = %d, want <= 8: %v", len(p.DependentFiles), p.DependentFiles)
	}
	joined := strings.Join(p.DependentFiles, "|")
	for _, want := range []string{"OrderController.java", "OrderService.java", "OrderDao.java", "Order.java"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("deps = %v, want %s", p.DependentFiles, want)
		}
	}
}

func TestDevTaskSeedEnglishTitleAndPath(t *testing.T) {
	rels := append([]string{}, devTaskLayeredRepo...)
	m := devTaskModel("en", rels...)
	w := Build(m, nil, Options{MaxPages: 120})

	p := devTaskPageByTitle(w, "Typical development tasks")
	if p == nil {
		t.Fatalf("english cookbook seed missing; titles = %v", titlesOf(w))
	}
	if p.Section != "Developer Guide" || p.Parent != "Developer Guide" {
		t.Fatalf("section/parent = %q/%q, want Developer Guide", p.Section, p.Parent)
	}
	if devTaskPageByTitle(w, "典型开发任务") != nil {
		t.Fatalf("english plan should not carry the zh title")
	}
}

func TestDevTaskSeedNotDuplicated(t *testing.T) {
	m := devTaskModel("zh", devTaskLayeredRepo...)
	w := Build(m, nil, Options{MaxPages: 120})

	n := 0
	for _, p := range w.Pages {
		if p.Title == "典型开发任务" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("典型开发任务 appears %d times, want 1", n)
	}
}

func TestDevTaskSeedGates(t *testing.T) {
	// Drop the API layer / the data layer / the size gate in turn; each removal
	// alone must suppress the cookbook seed.
	noAPI := []string{
		"src/main/java/com/demo/service/OrderService.java",
		"src/main/java/com/demo/dao/OrderDao.java",
		"src/main/java/com/demo/entity/Order.java",
		"src/main/java/com/demo/util/StringUtils.java",
		"src/main/java/com/demo/util/DateUtils.java",
		"src/main/java/com/demo/util/JsonUtils.java",
		"src/main/java/com/demo/util/FileUtils.java",
		"src/main/java/com/demo/util/MathUtils.java",
		"src/main/java/com/demo/util/ListUtils.java",
		"src/main/java/com/demo/util/MapUtils.java",
		// NB: keep this out of src/main/resources/ — the API signal regex is
		// substring-based, so "resources" would match "resource" and flip hasAPI.
		"config/application.yml",
		"README.md",
		"pom.xml",
	}
	noData := []string{
		"src/main/java/com/demo/controller/OrderController.java",
		"src/main/java/com/demo/service/OrderService.java",
		"src/main/java/com/demo/util/StringUtils.java",
		"src/main/java/com/demo/util/DateUtils.java",
		"src/main/java/com/demo/util/JsonUtils.java",
		"src/main/java/com/demo/util/FileUtils.java",
		"src/main/java/com/demo/util/MathUtils.java",
		"src/main/java/com/demo/util/ListUtils.java",
		"src/main/java/com/demo/util/MapUtils.java",
		"src/main/java/com/demo/util/SetUtils.java",
		"config/application.yml",
		"README.md",
		"pom.xml",
	}
	tooSmall := []string{
		"src/main/java/com/demo/controller/OrderController.java",
		"src/main/java/com/demo/service/OrderService.java",
		"src/main/java/com/demo/dao/OrderDao.java",
		"src/main/java/com/demo/entity/Order.java",
		"README.md",
	}
	cases := map[string][]string{
		"no api layer":  noAPI,
		"no data layer": noData,
		"too few files": tooSmall,
	}
	for name, rels := range cases {
		w := Build(devTaskModel("zh", rels...), nil, Options{MaxPages: 120})
		if p := devTaskPageByTitle(w, "典型开发任务"); p != nil {
			t.Fatalf("%s: cookbook seed should be suppressed; titles = %v", name, titlesOf(w))
		}
	}
}

func TestDevTaskRoleOf(t *testing.T) {
	cases := []struct {
		rel  string
		want string
	}{
		{"src/main/java/com/demo/OrderController.java", "api"},
		{"internal/handler/order.go", "api"},
		{"app/routes/index.ts", "api"},
		{"src/main/java/com/demo/OrderServiceImpl.java", "service"},
		{"src/biz/order.go", "service"},
		{"src/main/java/com/demo/OrderMapper.java", "data"},
		{"internal/repository/order.go", "data"},
		{"src/main/java/com/demo/Order.java", ""},
		{"src/main/java/com/demo/OrderEntity.java", "entity"},
		{"src/models/order.ts", "entity"},
		{"src/main/java/com/demo/util/StringUtils.java", ""},
		{"README.md", ""},
	}
	for _, c := range cases {
		if got := devTaskRoleOf(c.rel); got != c.want {
			t.Fatalf("devTaskRoleOf(%q) = %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestDevTaskStemCollapsesLayerWords(t *testing.T) {
	cases := []struct {
		rel  string
		want string
	}{
		{"a/OrderController.java", "order"},
		{"b/OrderService.java", "order"},
		{"c/OrderServiceImpl.java", "order"},
		{"d/OrderDao.java", "order"},
		{"e/order_repository.go", "order"},
		{"f/UserProfileMapper.java", "userprofile"},
		{"g/Dao.java", ""},
	}
	for _, c := range cases {
		if got := devTaskStem(c.rel); got != c.want {
			t.Fatalf("devTaskStem(%q) = %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestDevTaskRoleChainPrefersSameStem(t *testing.T) {
	m := devTaskModel("zh",
		"src/main/java/com/demo/dao/ItemDao.java",
		"src/main/java/com/demo/controller/OrderController.java",
		"src/main/java/com/demo/service/OrderService.java",
		"src/main/java/com/demo/dao/OrderDao.java",
		"src/main/java/com/demo/entity/Order.java",
		"src/main/java/com/demo/controller/ItemController.java",
	)
	chain := devTaskRoleChain(m)
	if len(chain) < 4 {
		t.Fatalf("chain = %v, want at least 4 files", chain)
	}
	// api -> service -> data -> entity, all from the same "order" stem first.
	want := []string{
		"src/main/java/com/demo/controller/OrderController.java",
		"src/main/java/com/demo/service/OrderService.java",
		"src/main/java/com/demo/dao/OrderDao.java",
		"src/main/java/com/demo/entity/Order.java",
	}
	for i, w := range want {
		if chain[i] != w {
			t.Fatalf("chain[%d] = %q, want %q (chain = %v)", i, chain[i], w, chain)
		}
	}
	// Deterministic across repeated calls on the same model.
	again := devTaskRoleChain(m)
	if strings.Join(again, "|") != strings.Join(chain, "|") {
		t.Fatalf("chain not deterministic: %v vs %v", chain, again)
	}
}

func TestDevTaskRoleChainSkipsVendorAndNonCode(t *testing.T) {
	// Vendor drops and non-code files must not be able to evidence a layer.
	m := devTaskModel("js",
		"static/js/lib/jquery.min.js",
		"static/js/vendor/vue-router.js",
		"node_modules/express/lib/router/index.js",
		"docs/OrderController.md",
		"conf/OrderMapper.xml",
		"src/service/order.js",
	)
	if chain := devTaskRoleChain(m); chain != nil {
		t.Fatalf("chain = %v, want nil (only one real role evidenced)", chain)
	}
}

func TestDevTaskRoleChainNilWhenSingleRole(t *testing.T) {
	m := devTaskModel("zh",
		"src/main/java/com/demo/controller/OrderController.java",
		"src/main/java/com/demo/controller/ItemController.java",
		"src/main/java/com/demo/util/StringUtils.java",
	)
	if chain := devTaskRoleChain(m); chain != nil {
		t.Fatalf("chain = %v, want nil (api only)", chain)
	}
	if chain := devTaskRoleChain(nil); chain != nil {
		t.Fatalf("chain(nil model) = %v, want nil", chain)
	}
}

func TestDevTaskSeedDescriptionSlugSymmetry(t *testing.T) {
	// WikiPage.Slug is ordinal (MakeSlug(index, title)); the stable, language
	// independent handle is DescriptionSlug, which titleSlugs maps for both
	// the zh and en cookbook titles.
	zh := devTaskPageByTitle(Build(devTaskModel("zh", devTaskLayeredRepo...), nil, Options{MaxPages: 120}), "典型开发任务")
	if zh == nil {
		t.Fatal("zh cookbook page missing")
	}
	if zh.DescriptionSlug != "typical-development-tasks" {
		t.Fatalf("zh DescriptionSlug = %q, want typical-development-tasks", zh.DescriptionSlug)
	}
	en := devTaskPageByTitle(Build(devTaskModel("java", devTaskLayeredRepo...), nil, Options{MaxPages: 120}), "Typical development tasks")
	if en == nil {
		t.Fatal("en cookbook page missing")
	}
	if en.DescriptionSlug != zh.DescriptionSlug {
		t.Fatalf("en DescriptionSlug = %q, want %q", en.DescriptionSlug, zh.DescriptionSlug)
	}
}
