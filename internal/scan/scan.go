// Package scan provides a lightweight repository inventory used by the
// planner and evidence binder.
package scan

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	ignore "github.com/sabhiram/go-gitignore"
)

// FileInfo is a scanned source file.
type FileInfo struct {
	RelativePath string
	Lines        int
	Size         int64
	Ext          string
}

// ModuleSummary groups files under a top-level module path.
type ModuleSummary struct {
	Name  string
	Path  string
	Files []FileInfo
}

// Model is the repository inventory used by planner/evidence/export.
type Model struct {
	Root        string
	Name        string
	Language    string // "zh" | "en" documentation language
	GeneratedAt string
	Files       []FileInfo
	Modules     []ModuleSummary
	Languages   map[string]int // ext -> count
	GitCommit   string
	GitBranch   string
	Summary     string

	// Lightweight code graph (filled by EnrichGraph). Optional fields stay empty
	// when graph enrichment is skipped or finds nothing.
	ImportEdges []ImportEdge
	EntryPoints []EntryPoint
	ModuleDeps  []ModuleDep

	// Symbols maps ToSlash relative path → top-level declarations extracted
	// during EnrichGraph's read loop (capped per file).
	Symbols map[string][]Symbol
	// Entities are data-bearing type declarations (ORM-annotated entities and
	// plain classes/interfaces with fields) extracted during EnrichGraph's
	// read loop (capped per file and per repo).
	Entities []Entity
	// Routes are HTTP endpoint registrations detected by generic framework
	// patterns (capped globally).
	Routes []Route
	// ConfigKeys maps configuration file path → top-level key names (capped
	// per file). Filled by EnrichGraph for common config formats.
	ConfigKeys map[string][]string
	// ManifestRoots are directories containing a build manifest (go.mod,
	// package.json, pom.xml, …) with at least one code file underneath.
	// "." denotes the repository root. Sorted; nested roots deduped.
	ManifestRoots []string

	// Adjacency caches (not serialized); rebuilt from ImportEdges.
	importOut map[string][]string
	importIn  map[string][]string
}

// Options controls scanning.
type Options struct {
	MaxFiles int
	MaxDepth int
	// GraphFile is an optional absolute/relative path to an external graph JSON
	// overlay (same schema as .wikify/graph.json). When empty, Scan still loads
	// workDir/.wikify/graph.json if present.
	GraphFile string
}

var reCode = regexp.MustCompile(`(?i)\.(java|kt|ts|tsx|js|jsx|py|go|rs|cs|php|scala|groovy|c|cc|cpp|h|hpp|vue|svelte)$`)

// Scan walks workDir and builds a lightweight model.
func Scan(workDir string, docLang string, opts Options) (*Model, error) {
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 8000
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 12
	}
	if docLang == "" {
		docLang = "zh"
	}
	switch strings.ToLower(docLang) {
	case "chinese", "zh", "zh-cn", "zh-tw":
		docLang = "zh"
	case "english", "en", "en-us", "en-gb":
		docLang = "en"
	}

	ig := loadIgnore(workDir)
	var files []FileInfo
	langs := map[string]int{}

	err := filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err2 := filepath.Rel(workDir, path)
		if err2 != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		depth := strings.Count(rel, "/")
		if d.IsDir() {
			base := d.Name()
			if shouldSkipDir(base) || ignored(ig, rel+"/") || depth >= opts.MaxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if ignored(ig, rel) || isNoiseFile(rel) {
			return nil
		}
		info, err3 := d.Info()
		if err3 != nil {
			return nil
		}
		if info.Size() > 400_000 {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(rel))
		lines := countLines(path, 2000)
		files = append(files, FileInfo{
			RelativePath: rel,
			Lines:        lines,
			Size:         info.Size(),
			Ext:          ext,
		})
		if ext != "" {
			langs[ext]++
		}
		if len(files) >= opts.MaxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool { return files[i].RelativePath < files[j].RelativePath })
	name := filepath.Base(workDir)
	roots := deriveManifestRoots(files)
	model := &Model{
		Root:          workDir,
		Name:          name,
		Language:      docLang,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Files:         files,
		Languages:     langs,
		GitCommit:     readGitHead(workDir),
		GitBranch:     readGitBranch(workDir),
		Summary:       buildSummary(name, files, langs),
		ManifestRoots: roots,
		Modules:       deriveModules(files, roots),
	}
	EnrichGraph(model)
	// Optional external graph overlay. Explicit GraphFile wins; else default path.
	graphPath := opts.GraphFile
	if graphPath == "" {
		graphPath = filepath.Join(workDir, ".wikify", "graph.json")
	} else if !filepath.IsAbs(graphPath) {
		cand := filepath.Join(workDir, graphPath)
		if _, err := os.Stat(cand); err == nil {
			graphPath = cand
		}
	}
	if g, err := LoadGraphFile(graphPath); err == nil && g != nil {
		ApplyGraphFile(model, g)
	}
	return model, nil
}

// IsCodeFile reports whether path looks like source code.
func IsCodeFile(path string) bool {
	return reCode.MatchString(path)
}

// IsAPISourceFile reports controller/api-like paths.
func IsAPISourceFile(path string) bool {
	if !IsCodeFile(path) || IsNoisePath(path) {
		return false
	}
	lower := strings.ToLower(path)
	if regexp.MustCompile(`(^|/)(api|controller|controllers|route|routes|endpoint|endpoints|handler|handlers|servlet|resource|rest|graphql)(/|$)`).MatchString(lower) {
		return true
	}
	base := filepath.Base(path)
	return regexp.MustCompile(`(?i)(Controller|Resource|Endpoint|Handler|Servlet|RestApi|ApiService)\.`).MatchString(base)
}

// IsNoisePath reports build/vendor noise paths.
func IsNoisePath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	return regexp.MustCompile(`(^|/)(\.|node_modules|target|build|dist|out|bin|obj|vendor|coverage|tmp|temp)(/|$)`).MatchString(lower)
}

func loadIgnore(root string) *ignore.GitIgnore {
	for _, name := range []string{".gitignore", ".wikifyignore"} {
		p := filepath.Join(root, name)
		if ig, err := ignore.CompileIgnoreFile(p); err == nil && ig != nil {
			return ig
		}
	}
	return nil
}

func ignored(ig *ignore.GitIgnore, rel string) bool {
	if ig == nil {
		return false
	}
	return ig.MatchesPath(rel)
}

func shouldSkipDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".svn", ".hg", "node_modules", "vendor", "target", "build", "dist", "out", "bin", "obj",
		".idea", ".vscode", ".wikify", "coverage", "__pycache__", ".next", ".turbo":
		return true
	}
	// CI directories are allowlisted so workflow/pipeline files enter the
	// inventory (their yml stays IsCodeFile == false, so clustering is safe).
	switch strings.ToLower(name) {
	case ".github", ".gitlab", ".circleci":
		return false
	}
	return strings.HasPrefix(name, ".")
}

func isNoiseFile(rel string) bool {
	lower := strings.ToLower(rel)
	for _, suf := range []string{".class", ".jar", ".png", ".jpg", ".gif", ".svg", ".map", ".min.js", ".woff", ".woff2", ".pdf", ".zip"} {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	return false
}

func countLines(path string, max int) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	if len(data) == 0 {
		return 0
	}
	n := 1
	for _, b := range data {
		if b == '\n' {
			n++
			if n >= max {
				return n
			}
		}
	}
	return n
}

// deriveManifestRoots collects directories holding a build manifest and at
// least one code file underneath. Nested roots are deduped keeping the
// shallowest non-"." ancestor; the result is sorted for determinism.
func deriveManifestRoots(files []FileInfo) []string {
	isManifest := func(base string) bool {
		switch strings.ToLower(base) {
		case "go.mod", "package.json", "pom.xml", "build.gradle", "build.gradle.kts",
			"cargo.toml", "pyproject.toml":
			return true
		}
		return strings.HasSuffix(strings.ToLower(base), ".csproj")
	}
	rootSet := map[string]bool{}
	var codePaths []string
	for _, f := range files {
		rel := filepath.ToSlash(f.RelativePath)
		if IsNoisePath(rel) {
			continue
		}
		if IsCodeFile(rel) {
			codePaths = append(codePaths, rel)
		}
		if isManifest(filepath.Base(rel)) {
			rootSet[filepath.ToSlash(filepath.Dir(rel))] = true
		}
	}
	if len(rootSet) == 0 {
		return nil
	}
	hasCodeUnder := func(dir string) bool {
		for _, p := range codePaths {
			if dir == "." || p == dir || strings.HasPrefix(p, dir+"/") {
				return true
			}
		}
		return false
	}
	var roots []string
	for dir := range rootSet {
		if hasCodeUnder(dir) {
			roots = append(roots, dir)
		}
	}
	// Shallow-first order so dedup keeps the shallowest ancestor. "." never
	// swallows real sub-roots (a root manifest is common in monorepos).
	sort.Slice(roots, func(i, j int) bool {
		di, dj := strings.Count(roots[i], "/"), strings.Count(roots[j], "/")
		if di != dj {
			return di < dj
		}
		return roots[i] < roots[j]
	})
	var kept []string
	for _, r := range roots {
		nested := false
		for _, k := range kept {
			if k != "." && strings.HasPrefix(r, k+"/") {
				nested = true
				break
			}
		}
		if !nested {
			kept = append(kept, r)
		}
	}
	if len(kept) > 64 {
		kept = kept[:64]
	}
	sort.Strings(kept)
	return kept
}

// nearestManifestRoot returns the deepest manifest root containing rel, or ""
// when rel is under none of the non-"." roots.
func nearestManifestRoot(rel string, roots []string) string {
	rel = filepath.ToSlash(rel)
	best := ""
	for _, r := range roots {
		if r == "" || r == "." {
			continue
		}
		if rel == r || strings.HasPrefix(rel, r+"/") {
			if len(r) > len(best) {
				best = r
			}
		}
	}
	return best
}

func deriveModules(files []FileInfo, manifestRoots []string) []ModuleSummary {
	// True multi-root repos group by nearest manifest root; otherwise fall back
	// to the first-segment heuristic.
	var roots []string
	for _, r := range manifestRoots {
		if r != "" && r != "." {
			roots = append(roots, r)
		}
	}
	useRoots := len(roots) >= 2
	byPath := map[string][]FileInfo{}
	for _, f := range files {
		if !IsCodeFile(f.RelativePath) {
			continue
		}
		mod := ""
		if useRoots {
			mod = nearestManifestRoot(f.RelativePath, roots)
		}
		if mod == "" {
			mod = modulePathOf(f.RelativePath)
		}
		if mod == "" {
			continue
		}
		byPath[mod] = append(byPath[mod], f)
	}
	type kv struct {
		k string
		v []FileInfo
	}
	var list []kv
	for k, v := range byPath {
		if len(v) < 2 {
			continue
		}
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if len(list[i].v) != len(list[j].v) {
			return len(list[i].v) > len(list[j].v)
		}
		return list[i].k < list[j].k
	})
	if len(list) > 32 {
		list = list[:32]
	}
	out := make([]ModuleSummary, 0, len(list))
	for _, item := range list {
		out = append(out, ModuleSummary{Name: filepath.Base(item.k), Path: item.k, Files: item.v})
	}
	return out
}

func modulePathOf(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) >= 2 && (parts[0] == "packages" || parts[0] == "apps" || parts[0] == "libs" || parts[0] == "services") {
		return parts[0] + "/" + parts[1]
	}
	if len(parts) >= 1 {
		first := parts[0]
		if first != "src" && first != "test" && first != "docs" {
			return first
		}
	}
	for i, p := range parts {
		if p == "java" || p == "kotlin" || p == "scala" {
			if i+1 < len(parts) {
				rest := parts[i+1:]
				if len(rest) >= 3 {
					return strings.Join(rest[:3], "/")
				}
				return strings.Join(rest, "/")
			}
		}
	}
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}

func buildSummary(name string, files []FileInfo, langs map[string]int) string {
	var top []string
	type pair struct {
		k string
		n int
	}
	var ps []pair
	for k, n := range langs {
		ps = append(ps, pair{k, n})
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].n > ps[j].n })
	for i, p := range ps {
		if i >= 5 {
			break
		}
		top = append(top, p.k)
	}
	return name + " — scanned " + strconv.Itoa(len(files)) + " files; languages: " + strings.Join(top, ", ")
}

func readGitHead(root string) string {
	// .git/HEAD may be a ref; try packed-ish simple read of short SHA via .git/HEAD + ref
	data, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(data))
	if strings.HasPrefix(s, "ref:") {
		ref := strings.TrimSpace(strings.TrimPrefix(s, "ref:"))
		b, err2 := os.ReadFile(filepath.Join(root, ".git", ref))
		if err2 == nil {
			sha := strings.TrimSpace(string(b))
			if len(sha) > 12 {
				return sha[:12]
			}
			return sha
		}
		return ""
	}
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func readGitBranch(root string) string {
	data, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return "main"
	}
	s := strings.TrimSpace(string(data))
	if strings.HasPrefix(s, "ref:") {
		ref := strings.TrimSpace(strings.TrimPrefix(s, "ref:"))
		return filepath.Base(ref)
	}
	return "HEAD"
}
