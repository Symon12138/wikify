package wikiplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureAndRead(t *testing.T) {
	dir := t.TempDir()
	p, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	plan, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.Version != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.Wiki == nil {
		t.Fatal("expected wiki section in default plan")
	}
	// second Ensure is no-op
	p2, err := Ensure(dir)
	if err != nil || p2 != p {
		t.Fatalf("ensure again: %v %s", err, p2)
	}
	if Path(dir) != filepath.Join(dir, ".wikify", "wiki_plan.yaml") {
		t.Fatal(Path(dir))
	}
}

func TestReadWikiDocuments(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".wikify")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `version: 1
wiki:
  documents:
    - title: 概述
      goal: 项目概述
`
	if err := os.WriteFile(filepath.Join(root, "wiki_plan.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	docs := plan.Documents()
	if len(docs) != 1 || docs[0].Title != "概述" {
		t.Fatalf("unexpected documents: %+v", docs)
	}
}

func TestGuidanceText(t *testing.T) {
	p := &Plan{
		Wiki: &Section{
			Template: "architecture",
			Notes:    []Note{{Text: "Focus on modules", Author: "wikify"}},
		},
		Scope: &struct {
			Include []string `yaml:"include,omitempty" json:"include,omitempty"`
			Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
		}{Include: []string{"src/**"}, Exclude: []string{"vendor/**"}},
	}
	g := p.GuidanceText()
	for _, part := range []string{"architecture", "Focus on modules", "src/**", "vendor/**"} {
		if !strings.Contains(g, part) {
			t.Fatalf("guidance missing %q: %q", part, g)
		}
	}
	if (*Plan)(nil).GuidanceText() != "" {
		t.Fatal("nil plan should be empty")
	}
}


func TestHasScopeAndAccessors(t *testing.T) {
	if (*Plan)(nil).HasScope() {
		t.Fatal("nil plan")
	}
	p := &Plan{Scope: &struct {
		Include []string `yaml:"include,omitempty" json:"include,omitempty"`
		Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	}{Include: []string{"src/**"}, Exclude: []string{"vendor/**"}}}
	if !p.HasScope() {
		t.Fatal("expected scope")
	}
	if len(p.ScopeInclude()) != 1 || p.ScopeInclude()[0] != "src/**" {
		t.Fatalf("include=%v", p.ScopeInclude())
	}
	if len(p.ScopeExclude()) != 1 {
		t.Fatalf("exclude=%v", p.ScopeExclude())
	}
}
