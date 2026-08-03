package evidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JSHurt/wikify/internal/scan"
)

// TitleBridge is a generated, product-agnostic map from path/title tokens to
// repository paths. It is written under .wikify/meta/title_bridge.json during
// export and reloaded on later generate/polish so Chinese titles can reuse
// previously successful English path stems without hard-coding domain lexicons.
type TitleBridge struct {
	Version    int                    `json:"version"`
	// TokenPaths: lowercased CamelCase/path token → relative paths (capped).
	TokenPaths map[string][]string    `json:"token_paths,omitempty"`
	// PageHints: prior page bindings (title → paths / path tokens).
	PageHints  []TitleBridgePageHint  `json:"page_hints,omitempty"`
}

// TitleBridgePageHint records one page's successful evidence binding.
type TitleBridgePageHint struct {
	Title      string   `json:"title"`
	Tokens     []string `json:"tokens,omitempty"`
	PathTokens []string `json:"path_tokens,omitempty"`
	Paths      []string `json:"paths,omitempty"`
}

const (
	titleBridgeVersion   = 1
	maxTokenPathsPerKey  = 12
	maxBridgeTokens      = 4000
	maxPageHints         = 400
)

// DefaultTitleBridgePath returns workDir/.wikify/meta/title_bridge.json.
func DefaultTitleBridgePath(workDir string) string {
	return filepath.Join(workDir, ".wikify", "meta", "title_bridge.json")
}

// BuildTitleBridge mines path tokens from the scan model and optional prior
// page bindings. No product-domain dictionary is used.
func BuildTitleBridge(model *scan.Model, pageTitles []string, pageDeps map[string][]string) *TitleBridge {
	b := &TitleBridge{
		Version:    titleBridgeVersion,
		TokenPaths: map[string][]string{},
	}
	if model != nil {
		vd := NewVendorDetector(model, nil)
		for _, f := range model.Files {
			rel := filepath.ToSlash(f.RelativePath)
			if scan.IsNoisePath(rel) || isUniversalNoisePath(rel) || vd.IsVendor(rel) {
				continue
			}
			if !scan.IsCodeFile(rel) && !matchAny(strings.ToLower(rel), `\.(ya?ml|xml|properties|sql|bpmn)$`) {
				continue
			}
			base := filepath.Base(rel)
			stem := strings.TrimSuffix(base, filepath.Ext(base))
			tokens := append(splitIdent(stem), packageSegTokens(rel)...)
			for _, tok := range tokens {
				t := strings.ToLower(strings.TrimSpace(tok))
				if len(t) < 3 || isStopToken(t) {
					continue
				}
				addTokenPath(b.TokenPaths, t, rel)
			}
			if len(b.TokenPaths) >= maxBridgeTokens {
				break
			}
		}
	}
	// Page hints from current wiki bindings.
	for _, title := range pageTitles {
		deps := pageDeps[title]
		if len(deps) == 0 {
			continue
		}
		toks := tokenize(title)
		var pathToks []string
		seenPT := map[string]bool{}
		for _, d := range deps {
			base := filepath.Base(filepath.ToSlash(d))
			stem := strings.TrimSuffix(base, filepath.Ext(base))
			for _, pt := range splitIdent(stem) {
				pl := strings.ToLower(pt)
				if len(pl) < 3 || isStopToken(pl) || seenPT[pl] {
					continue
				}
				seenPT[pl] = true
				pathToks = append(pathToks, pl)
			}
		}
		if len(pathToks) == 0 && len(deps) == 0 {
			continue
		}
		b.PageHints = append(b.PageHints, TitleBridgePageHint{
			Title:      title,
			Tokens:     toks,
			PathTokens: pathToks,
			Paths:      unique(deps),
		})
		if len(b.PageHints) >= maxPageHints {
			break
		}
	}
	return b
}

// LoadTitleBridge reads a bridge file; returns nil when missing/invalid.
func LoadTitleBridge(path string) *TitleBridge {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var b TitleBridge
	if err := json.Unmarshal(data, &b); err != nil {
		return nil
	}
	if b.TokenPaths == nil {
		b.TokenPaths = map[string][]string{}
	}
	return &b
}

// SaveTitleBridge writes the bridge JSON (creates parent dirs).
func SaveTitleBridge(path string, b *TitleBridge) error {
	if b == nil || path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b.Version = titleBridgeVersion
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// MergeTitleBridge overlays page hints / token paths from other into base.
func MergeTitleBridge(base, other *TitleBridge) *TitleBridge {
	if base == nil {
		return other
	}
	if other == nil {
		return base
	}
	if base.TokenPaths == nil {
		base.TokenPaths = map[string][]string{}
	}
	for k, paths := range other.TokenPaths {
		for _, p := range paths {
			addTokenPath(base.TokenPaths, k, p)
		}
	}
	// Prefer newer page hints by title.
	byTitle := map[string]TitleBridgePageHint{}
	for _, h := range base.PageHints {
		byTitle[h.Title] = h
	}
	for _, h := range other.PageHints {
		byTitle[h.Title] = h
	}
	base.PageHints = base.PageHints[:0]
	for _, h := range byTitle {
		base.PageHints = append(base.PageHints, h)
		if len(base.PageHints) >= maxPageHints {
			break
		}
	}
	return base
}

// ExtraSynonymsFromBridge returns additional path-oriented synonym tokens for
// scoring, derived only from prior bindings and path token inventory.
func ExtraSynonymsFromBridge(b *TitleBridge, title, goal string) []string {
	if b == nil {
		return nil
	}
	text := title + " " + goal
	tokens := tokenize(text)
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] || isStopToken(s) {
			return
		}
		seen[s] = true
		out = append(out, s)
	}

	// 1) Exact / fuzzy page-hint reuse.
	titleLower := strings.ToLower(strings.TrimSpace(title))
	for _, h := range b.PageHints {
		if strings.EqualFold(h.Title, title) || strings.ToLower(h.Title) == titleLower {
			for _, pt := range h.PathTokens {
				add(pt)
			}
			for _, p := range h.Paths {
				base := filepath.Base(filepath.ToSlash(p))
				stem := strings.TrimSuffix(base, filepath.Ext(base))
				for _, pt := range splitIdent(stem) {
					add(pt)
				}
			}
			continue
		}
		// Soft: share ≥2 title tokens with a prior hint.
		// Chinese titles often arrive as unsegmented Han strings; also count
		// multi-rune hint tokens that appear as substrings of the title text.
		overlap := 0
		hset := map[string]bool{}
		for _, t := range h.Tokens {
			hset[strings.ToLower(t)] = true
		}
		titleRaw := strings.ToLower(strings.TrimSpace(title))
		for _, t := range tokens {
			if hset[strings.ToLower(t)] {
				overlap++
			}
		}
		for _, ht := range h.Tokens {
			hl := strings.ToLower(ht)
			if len([]rune(hl)) >= 2 && strings.Contains(titleRaw, hl) {
				// substring hit (Chinese compound titles)
				if !hset["_hit_"+hl] {
					hset["_hit_"+hl] = true
					// only bump if not already counted via tokenize equality
					already := false
					for _, t := range tokens {
						if strings.EqualFold(t, ht) {
							already = true
							break
						}
					}
					if !already {
						overlap++
					}
				}
			}
		}
		if overlap >= 2 {
			for _, pt := range h.PathTokens {
				add(pt)
			}
		}
	}

	// 2) Direct token inventory: title English tokens that appear in TokenPaths.
	for _, t := range tokens {
		tl := strings.ToLower(t)
		if paths := b.TokenPaths[tl]; len(paths) > 0 {
			add(tl)
			// also add sibling camel parts from first few paths
			for i, p := range paths {
				if i >= 3 {
					break
				}
				base := filepath.Base(filepath.ToSlash(p))
				stem := strings.TrimSuffix(base, filepath.Ext(base))
				for _, pt := range splitIdent(stem) {
					add(pt)
				}
			}
		}
	}
	return out
}

// PathsForTitleHints returns previously bound paths for an exact title match.
func PathsForTitleHints(b *TitleBridge, title string) []string {
	if b == nil || title == "" {
		return nil
	}
	for _, h := range b.PageHints {
		if strings.EqualFold(h.Title, title) {
			return unique(h.Paths)
		}
	}
	return nil
}

func addTokenPath(m map[string][]string, token, path string) {
	path = filepath.ToSlash(path)
	list := m[token]
	for _, p := range list {
		if p == path {
			return
		}
	}
	if len(list) >= maxTokenPathsPerKey {
		return
	}
	m[token] = append(list, path)
}

func packageSegTokens(rel string) []string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	noise := map[string]bool{
		"src": true, "main": true, "java": true, "resources": true, "com": true,
		"org": true, "net": true, "cn": true, "impl": true, "controller": true,
		"service": true, "dao": true, "mapper": true, "web": true, "app": true,
		"test": true, "tests": true, "kotlin": true, "scala": true,
	}
	var out []string
	for _, p := range parts {
		pl := strings.ToLower(p)
		if noise[pl] || len(pl) < 3 {
			continue
		}
		// skip file basename (handled via splitIdent)
		if strings.Contains(p, ".") {
			continue
		}
		out = append(out, p)
	}
	return out
}

func isStopToken(t string) bool {
	switch t {
	case "java", "xml", "json", "html", "css", "the", "and", "for", "with",
		"src", "main", "test", "app", "com", "org", "impl", "base", "util",
		"utils", "common", "data", "info", "type", "class", "object":
		return true
	}
	return false
}

// sortedTokenKeys is a test helper surface (stable iteration).
func sortedTokenKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
