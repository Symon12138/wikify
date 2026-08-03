package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanBasic(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "pkg", "util.go"), []byte("package pkg\n"), 0o644)

	m, err := Scan(dir, "zh", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Name == "" || len(m.Files) < 2 {
		t.Fatalf("unexpected model: %+v", m)
	}
	if !IsCodeFile("main.go") {
		t.Fatal("IsCodeFile")
	}
	if !IsAPISourceFile("web/UserController.java") {
		t.Fatal("IsAPISourceFile")
	}
	if !IsNoisePath("node_modules/x/index.js") {
		t.Fatal("IsNoisePath")
	}
}

func TestDeriveManifestRoots(t *testing.T) {
	files := []FileInfo{
		{RelativePath: "go.mod"},
		{RelativePath: "main.go"},
		{RelativePath: "services/auth/go.mod"},
		{RelativePath: "services/auth/main.go"},
		{RelativePath: "services/auth/inner/go.mod"}, // nested under services/auth → deduped
		{RelativePath: "services/auth/inner/lib.go"},
		{RelativePath: "services/billing/go.mod"},
		{RelativePath: "services/billing/main.go"},
		{RelativePath: "docs/package.json"}, // no code underneath → dropped
		{RelativePath: "docs/readme.md"},
		{RelativePath: "node_modules/x/package.json"}, // noise → ignored
		{RelativePath: "node_modules/x/index.js"},
	}
	got := deriveManifestRoots(files)
	want := []string{".", "services/auth", "services/billing"}
	if len(got) != len(want) {
		t.Fatalf("roots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roots = %v, want %v", got, want)
		}
	}
}

func TestDeriveModulesNearestManifestRoot(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("backend/server/go.mod", "module example.com/server\n")
	write("backend/server/cmd/main.go", "package main\nfunc main() {}\n")
	write("backend/server/pkg/util.go", "package pkg\n")
	write("frontend/webapp/package.json", "{\"name\":\"webapp\"}\n")
	write("frontend/webapp/src/index.ts", "export const a = 1;\n")
	write("frontend/webapp/src/util.ts", "export const b = 2;\n")

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	wantRoots := []string{"backend/server", "frontend/webapp"}
	if len(m.ManifestRoots) != 2 || m.ManifestRoots[0] != wantRoots[0] || m.ManifestRoots[1] != wantRoots[1] {
		t.Fatalf("ManifestRoots = %v, want %v", m.ManifestRoots, wantRoots)
	}
	// With >= 2 manifest roots, modules group by nearest root — not by the
	// first path segment ("backend"/"frontend").
	paths := map[string]bool{}
	for _, mod := range m.Modules {
		paths[mod.Path] = true
	}
	if !paths["backend/server"] || !paths["frontend/webapp"] {
		t.Fatalf("modules not grouped by manifest root: %+v", m.Modules)
	}
	if paths["backend"] || paths["frontend"] {
		t.Fatalf("first-segment fallback should not fire: %+v", m.Modules)
	}
}

func TestScanIncludesCIDirs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".github", "workflows", "ci.yml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("name: CI\non: push\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range m.Files {
		if f.RelativePath == ".github/workflows/ci.yml" {
			found = true
		}
	}
	if !found {
		t.Fatalf("workflow file missing from inventory: %+v", m.Files)
	}
	if IsCodeFile(".github/workflows/ci.yml") {
		t.Fatal("workflow yml must not count as code")
	}
	// Allowlisted CI dirs are scanned; other dot-dirs stay skipped.
	for _, name := range []string{".github", ".gitlab", ".circleci"} {
		if shouldSkipDir(name) {
			t.Fatalf("%s should be scanned", name)
		}
	}
	for _, name := range []string{".git", ".idea", ".cache"} {
		if !shouldSkipDir(name) {
			t.Fatalf("%s should be skipped", name)
		}
	}
}
