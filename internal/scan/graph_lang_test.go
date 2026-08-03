package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func hasImportEdge(m *Model, from, to string) bool {
	for _, e := range m.ImportEdges {
		if e.Kind == "import" && e.From == from && e.To == to {
			return true
		}
	}
	return false
}

func TestEnrichGraphCInclude(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src/main.c"),
		"#include \"util.h\"\n#include \"core/engine\"\nint main() { return 0; }\n")
	mustWrite(t, filepath.Join(dir, "src/util.h"), "int helper(void);\n")
	mustWrite(t, filepath.Join(dir, "src/core/engine.hpp"), "struct Engine {};\n")

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasImportEdge(m, "src/main.c", "src/util.h") {
		t.Fatalf("c include edge missing: %+v", m.ImportEdges)
	}
	if !hasImportEdge(m, "src/main.c", "src/core/engine.hpp") {
		t.Fatalf("c include hpp fallback edge missing: %+v", m.ImportEdges)
	}
}

func TestEnrichGraphCSharpUsing(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "App/Models/Order.cs"),
		"namespace App.Models\n{\n    public class Order {}\n}\n")
	mustWrite(t, filepath.Join(dir, "App/Services/OrderService.cs"),
		"using System;\nusing App.Models;\nnamespace App.Services\n{\n    public class OrderService {}\n}\n")

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasImportEdge(m, "App/Services/OrderService.cs", "App/Models/Order.cs") {
		t.Fatalf("csharp using edge missing: %+v", m.ImportEdges)
	}
}

func TestEnrichGraphRustUseAndMod(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src/main.rs"),
		"mod util;\nmod net;\n\nuse crate::util::helper;\n\nfn main() {}\n")
	mustWrite(t, filepath.Join(dir, "src/util.rs"), "pub fn helper() {}\n")
	mustWrite(t, filepath.Join(dir, "src/net/mod.rs"), "pub fn connect() {}\n")

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasImportEdge(m, "src/main.rs", "src/util.rs") {
		t.Fatalf("rust mod/use edge to util.rs missing: %+v", m.ImportEdges)
	}
	if !hasImportEdge(m, "src/main.rs", "src/net/mod.rs") {
		t.Fatalf("rust mod edge to net/mod.rs missing: %+v", m.ImportEdges)
	}
}

func TestEnrichGraphPhpUseAndRequire(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "app/Model/OrderModel.php"),
		`<?php
namespace App\Model;
class OrderModel {}
`)
	mustWrite(t, filepath.Join(dir, "app/Controller/OrderController.php"),
		`<?php
namespace App\Controller;
use App\Model\OrderModel;
require_once __DIR__ . '/../helpers.php';
class OrderController {}
`)
	mustWrite(t, filepath.Join(dir, "app/helpers.php"), "<?php\nfunction fmt() {}\n")

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasImportEdge(m, "app/Controller/OrderController.php", "app/Model/OrderModel.php") {
		t.Fatalf("php use edge missing: %+v", m.ImportEdges)
	}
	if !hasImportEdge(m, "app/Controller/OrderController.php", "app/helpers.php") {
		t.Fatalf("php require edge missing: %+v", m.ImportEdges)
	}
}

func TestEnrichGraphSvelteRelativeImport(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "web/src/App.svelte"),
		"<script>\nimport { helper } from './lib/helper.js';\n</script>\n<main>{helper}</main>\n")
	mustWrite(t, filepath.Join(dir, "web/src/lib/helper.js"), "export const helper = 1;\n")

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasImportEdge(m, "web/src/App.svelte", "web/src/lib/helper.js") {
		t.Fatalf("svelte relative import edge missing: %+v", m.ImportEdges)
	}
}

func TestEnrichGraphPythonAbsoluteImport(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "run.py"),
		"import os\nfrom app.utils import helper\nimport app\n\nhelper()\n")
	mustWrite(t, filepath.Join(dir, "app/__init__.py"), "# package\n")
	mustWrite(t, filepath.Join(dir, "app/utils.py"), "def helper():\n    pass\n")

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasImportEdge(m, "run.py", "app/utils.py") {
		t.Fatalf("python absolute from-import edge missing: %+v", m.ImportEdges)
	}
	if !hasImportEdge(m, "run.py", "app/__init__.py") {
		t.Fatalf("python absolute package import edge missing: %+v", m.ImportEdges)
	}
	for _, e := range m.ImportEdges {
		if e.From == "run.py" && strings.Contains(e.To, "os") {
			t.Fatalf("stdlib import must not resolve: %+v", e)
		}
	}
}

func TestReachChain(t *testing.T) {
	m := &Model{ImportEdges: []ImportEdge{
		{From: "a.go", To: "b.go", Kind: "import"},
		{From: "b.go", To: "c.go", Kind: "import"},
		{From: "c.go", To: "d.go", Kind: "import"},
		{From: "a.go", To: "x.go", Kind: "same_package"},
		{From: "x.go", To: "c.go", Kind: "import"},
	}}

	if got := m.ReachChain("a.go", map[string]bool{"c.go": true}, 4); !reflect.DeepEqual(got, []string{"a.go", "b.go", "c.go"}) {
		t.Fatalf("hit chain = %v", got)
	}
	if got := m.ReachChain("a.go", map[string]bool{"zz.go": true}, 4); got != nil {
		t.Fatalf("miss should be nil, got %v", got)
	}
	// a→b→c→d is 3 hops: blocked at maxDepth 2, found at 3.
	if got := m.ReachChain("a.go", map[string]bool{"d.go": true}, 2); got != nil {
		t.Fatalf("depth cap 2 should block, got %v", got)
	}
	if got := m.ReachChain("a.go", map[string]bool{"d.go": true}, 3); !reflect.DeepEqual(got, []string{"a.go", "b.go", "c.go", "d.go"}) {
		t.Fatalf("depth 3 chain = %v", got)
	}
	// maxDepth <= 0 falls back to the default cap of 4.
	if got := m.ReachChain("a.go", map[string]bool{"d.go": true}, 0); len(got) != 4 {
		t.Fatalf("default depth chain = %v", got)
	}
	// from is itself a target.
	if got := m.ReachChain("a.go", map[string]bool{"a.go": true}, 4); !reflect.DeepEqual(got, []string{"a.go"}) {
		t.Fatalf("self target = %v", got)
	}
	// same_package edges are not walked: x.go only linked via same_package.
	if got := m.ReachChain("a.go", map[string]bool{"x.go": true}, 4); got != nil {
		t.Fatalf("same_package must be excluded, got %v", got)
	}
}

func TestReachChainVisitedCap(t *testing.T) {
	var edges []ImportEdge
	for i := 0; i < 220; i++ {
		edges = append(edges, ImportEdge{From: "a.go", To: fmt.Sprintf("n%03d.go", i), Kind: "import"})
	}
	edges = append(edges,
		ImportEdge{From: "a.go", To: "b.go", Kind: "import"},
		ImportEdge{From: "b.go", To: "t.go", Kind: "import"},
	)
	m := &Model{ImportEdges: edges}
	if got := m.ReachChain("a.go", map[string]bool{"t.go": true}, 4); got != nil {
		t.Fatalf("visited cap should abort search, got %v", got)
	}
}

func TestWriteGraphFilePersistsNewFields(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.go"),
		"package main\n\nimport \"net/http\"\n\nfunc main() {\n\thttp.HandleFunc(\"/healthz\", nil)\n}\n")
	mustWrite(t, filepath.Join(dir, "application.yml"), "server:\n  port: 8080\n")

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Symbols["main.go"]) == 0 {
		t.Fatalf("scan should extract symbols: %+v", m.Symbols)
	}
	if len(m.Routes) == 0 {
		t.Fatalf("scan should extract routes: %+v", m.Routes)
	}
	if len(m.ConfigKeys["application.yml"]) == 0 {
		t.Fatalf("scan should extract config keys: %+v", m.ConfigKeys)
	}
	path := DefaultGraphPath(dir)
	if err := WriteGraphFile(path, m); err != nil {
		t.Fatal(err)
	}
	g, err := LoadGraphFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Symbols["main.go"]) == 0 || g.Symbols["main.go"][0].Name != "main" {
		t.Fatalf("symbols not persisted: %+v", g.Symbols)
	}
	routeHit := false
	for _, r := range g.Routes {
		if r.Path == "/healthz" && r.File == "main.go" {
			routeHit = true
		}
	}
	if !routeHit {
		t.Fatalf("routes not persisted: %+v", g.Routes)
	}
	if len(g.ConfigKeys["application.yml"]) == 0 {
		t.Fatalf("config keys not persisted: %+v", g.ConfigKeys)
	}
}

func TestApplyGraphFileOverlayNewFields(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "package a\n")
	mustWrite(t, filepath.Join(dir, "application.yml"), "# no keys here\n")
	_ = os.MkdirAll(filepath.Join(dir, ".wikify"), 0o755)
	mustWrite(t, filepath.Join(dir, ".wikify", "graph.json"), `{
  "symbols": {
    "a.go": [{"name":"Helper","kind":"func","line":3}],
    "missing.go": [{"name":"X","kind":"func","line":1}]
  },
  "routes": [
    {"method":"GET","path":"/api/x","file":"a.go","hint":"go-router"},
    {"method":"GET","path":"/api/y","file":"missing.go"}
  ],
  "config_keys": {"application.yml": ["server"], "missing.yml": ["x"]}
}`)

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	syms := m.Symbols["a.go"]
	if len(syms) != 1 || syms[0].Name != "Helper" || syms[0].Line != 3 {
		t.Fatalf("overlay symbols not applied: %+v", m.Symbols)
	}
	if _, ok := m.Symbols["missing.go"]; ok {
		t.Fatalf("unknown symbol path must be dropped: %+v", m.Symbols)
	}
	if len(m.Routes) != 1 || m.Routes[0].Path != "/api/x" || m.Routes[0].File != "a.go" {
		t.Fatalf("overlay routes not filtered/applied: %+v", m.Routes)
	}
	if got := m.ConfigKeys["application.yml"]; len(got) != 1 || got[0] != "server" {
		t.Fatalf("overlay config keys not applied: %+v", m.ConfigKeys)
	}
	if _, ok := m.ConfigKeys["missing.yml"]; ok {
		t.Fatalf("unknown config path must be dropped: %+v", m.ConfigKeys)
	}
}

func TestLoadGraphFileBackwardCompat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "graph.json")
	mustWrite(t, path, `{
  "import_edges": [{"from":"a.go","to":"b.go","kind":"import"}],
  "entry_points": [{"path":"a.go","kind":"main"}]
}`)
	g, err := LoadGraphFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.ImportEdges) != 1 || len(g.EntryPoints) != 1 {
		t.Fatalf("old schema fields lost: %+v", g)
	}
	if g.Symbols != nil || g.Routes != nil || g.ConfigKeys != nil {
		t.Fatalf("new fields should stay empty for old files: %+v", g)
	}
}
