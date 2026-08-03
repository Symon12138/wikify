// Package wikiplan loads/saves .wikify/wiki_plan.yaml.
package wikiplan

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Document is a planned wiki page entry.
type Document struct {
	Title  string `yaml:"title" json:"title"`
	Goal   string `yaml:"goal" json:"goal"`
	Parent string `yaml:"parent,omitempty" json:"parent,omitempty"`
	Hints  string `yaml:"hints,omitempty" json:"hints,omitempty"`
}

// Note is a free-form planning note.
type Note struct {
	Text   string `yaml:"text" json:"text"`
	Author string `yaml:"author,omitempty" json:"author,omitempty"`
}

// Section holds wiki planning notes and optional document allowlist.
type Section struct {
	Template  string     `yaml:"template,omitempty" json:"template,omitempty"`
	Notes     []Note     `yaml:"notes,omitempty" json:"notes,omitempty"`
	Documents []Document `yaml:"documents,omitempty" json:"documents,omitempty"`
}

// Plan is the root of wiki_plan.yaml.
type Plan struct {
	Version int `yaml:"version" json:"version"`
	// Wiki is the planning block (yaml key: wiki).
	Wiki  *Section `yaml:"wiki,omitempty" json:"wiki,omitempty"`
	Scope *struct {
		Include []string `yaml:"include,omitempty" json:"include,omitempty"`
		Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	} `yaml:"scope,omitempty" json:"scope,omitempty"`
}

// Documents returns the allowlist from wiki. Safe when p is nil.
func (p *Plan) Documents() []Document {
	if p == nil || p.Wiki == nil {
		return nil
	}
	return p.Wiki.Documents
}

// ScopeInclude returns scope.include patterns (may be nil).
func (p *Plan) ScopeInclude() []string {
	if p == nil || p.Scope == nil {
		return nil
	}
	return p.Scope.Include
}

// ScopeExclude returns scope.exclude patterns (may be nil).
func (p *Plan) ScopeExclude() []string {
	if p == nil || p.Scope == nil {
		return nil
	}
	return p.Scope.Exclude
}

// HasScope reports whether include or exclude is non-empty.
func (p *Plan) HasScope() bool {
	if p == nil || p.Scope == nil {
		return false
	}
	return len(p.Scope.Include) > 0 || len(p.Scope.Exclude) > 0
}

// Path returns .wikify/wiki_plan.yaml under workDir.
func Path(workDir string) string {
	return filepath.Join(workDir, ".wikify", "wiki_plan.yaml")
}

// Ensure writes a default plan if missing; returns the path.
func Ensure(workDir string) (string, error) {
	p := Path(workDir)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(DefaultYAML()), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// Read loads wiki_plan.yaml if present.
func Read(workDir string) (*Plan, error) {
	p := Path(workDir)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var plan Plan
	if err := yaml.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("invalid wiki_plan.yaml: %w", err)
	}
	if plan.Version == 0 {
		plan.Version = 1
	}
	return &plan, nil
}

// DefaultYAML is a minimal architecture template.
func DefaultYAML() string {
	return "version: 1\n" +
		"wiki:\n" +
		"  template: architecture\n" +
		"  notes:\n" +
		"    - text: Documentation should focus on architecture, module relationships, and implementation details.\n" +
		"      author: wikify\n" +
		"  documents: []\n" +
		"scope:\n" +
		"  include: []\n" +
		"  exclude: []\n"
}
