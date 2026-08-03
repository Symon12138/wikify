package scan

import "testing"

func TestMatchPath(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"src", "src/main/A.java", true},
		{"src/", "src/main/A.java", true},
		{"src/**", "src/main/A.java", true},
		{"src/**", "lib/A.java", false},
		{"**/OrderController.java", "a/b/OrderController.java", true},
		{"**/OrderController.java", "OrderController.java", true},
		{"**/test/**", "src/test/java/X.java", true},
		{"**/test/**", "src/main/X.java", false},
		{"*.java", "Foo.java", true},
		{"*.java", "dir/Foo.java", true},
		{"vendor", "vendor/x.go", true},
		{"vendor", "src/vendor/x.go", false},
		{"", "a.go", false},
	}
	for _, c := range cases {
		got := MatchPath(c.pat, c.path)
		if got != c.want {
			t.Errorf("MatchPath(%q,%q)=%v want %v", c.pat, c.path, got, c.want)
		}
	}
}

func TestInScopeExcludeWins(t *testing.T) {
	include := []string{"src/**"}
	exclude := []string{"src/test/**"}
	if !InScope("src/main/A.java", include, exclude) {
		t.Fatal("main should be included")
	}
	if InScope("src/test/A.java", include, exclude) {
		t.Fatal("test should be excluded")
	}
	if InScope("lib/A.java", include, exclude) {
		t.Fatal("outside include should be false")
	}
}

func TestInScopeEmptyAllowsAll(t *testing.T) {
	if !InScope("anything/x.go", nil, nil) {
		t.Fatal("empty scope should allow")
	}
	if InScope("vendor/x.go", nil, []string{"vendor/**"}) {
		t.Fatal("exclude-only should filter")
	}
}

func TestApplyScopeRebuilds(t *testing.T) {
	m := &Model{
		Name: "demo",
		Files: []FileInfo{
			{RelativePath: "src/A.java", Ext: ".java", Lines: 10},
			{RelativePath: "src/test/T.java", Ext: ".java", Lines: 5},
			{RelativePath: "vendor/v.go", Ext: ".go", Lines: 3},
			{RelativePath: "src/B.java", Ext: ".java", Lines: 8},
		},
		Languages: map[string]int{".java": 3, ".go": 1},
	}
	m.ApplyScope([]string{"src/**"}, []string{"src/test/**"})
	if len(m.Files) != 2 {
		t.Fatalf("files=%d want 2: %+v", len(m.Files), m.Files)
	}
	if m.Languages[".java"] != 2 {
		t.Fatalf("langs=%v", m.Languages)
	}
	if m.Languages[".go"] != 0 {
		t.Fatalf("go should be gone: %v", m.Languages)
	}
	// no-op when empty
	n := len(m.Files)
	m.ApplyScope(nil, nil)
	if len(m.Files) != n {
		t.Fatal("empty scope mutated model")
	}
}
