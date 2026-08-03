package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ImportEdge is a directed dependency between two repository-relative source files.
type ImportEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind,omitempty"` // import | same_package
}

// EntryPoint is a likely program/API entry surface.
type EntryPoint struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"` // main | api | handler | cli | app
	Symbol string `json:"symbol,omitempty"`
}

// ModuleDep aggregates import edges between module path segments.
type ModuleDep struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Weight int    `json:"weight"`
}

// GraphFile is the optional external graph interchange format
// (.wikify/graph.json or --graph-file). Nodes are free-form; only edges
// with both ends mappable to scanned files are applied. Symbols/Routes/
// ConfigKeys/Entities are optional so old graph files keep loading unchanged.
type GraphFile struct {
	ImportEdges []ImportEdge        `json:"import_edges,omitempty"`
	Edges       []ImportEdge        `json:"edges,omitempty"` // alias
	EntryPoints []EntryPoint        `json:"entry_points,omitempty"`
	ModuleDeps  []ModuleDep         `json:"module_deps,omitempty"`
	Symbols     map[string][]Symbol `json:"symbols,omitempty"`
	Routes      []Route             `json:"routes,omitempty"`
	ConfigKeys  map[string][]string `json:"config_keys,omitempty"`
	Entities    []Entity            `json:"entities,omitempty"`
}

var (
	reGoImportLine   = regexp.MustCompile(`^\s*"([^"]+)"\s*$`)
	reGoImportSingle = regexp.MustCompile(`(?m)^\s*import\s+(?:\w+\s+)?"([^"]+)"`)
	reJavaImport     = regexp.MustCompile(`(?m)^\s*import\s+(?:static\s+)?([a-zA-Z_][\w]*(?:\.[a-zA-Z_][\w]*)+)(?:\.\*)?\s*;`)
	reJSFrom         = regexp.MustCompile(`(?m)(?:import|export)\s+(?:type\s+)?(?:[^'"\n]+from\s+)?['"]([^'"]+)['"]`)
	reJSRequire      = regexp.MustCompile(`(?m)require\s*\(\s*['"]([^'"]+)['"]\s*\)`)
	rePyFrom         = regexp.MustCompile(`(?m)^\s*(?:from\s+([.\w]+)\s+import|import\s+([.\w]+))`)
	reCInclude       = regexp.MustCompile(`(?m)^\s*#include\s+"([^"]+)"`)
	reCsUsing        = regexp.MustCompile(`(?m)^\s*using\s+(?:static\s+)?([A-Za-z_][\w.]*)\s*;`)
	reRustUse        = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?use\s+crate::([\w:]+)`)
	reRustMod        = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?mod\s+(\w+)\s*;`)
	rePhpUse         = regexp.MustCompile(`(?m)^\s*use\s+([\w\\]+)\s*;`)
	rePhpRequire     = regexp.MustCompile(`(?m)(?:require|include)(?:_once)?\s*\(?\s*(?:__DIR__\s*\.\s*)?['"]([^'"]+)['"]`)
	reRestAnno       = regexp.MustCompile(`(?m)@(?:RestController|Controller|RequestMapping|RestHandler)\b`)
	reGoFuncMain     = regexp.MustCompile(`(?m)^func\s+main\s*\(`)
	reGoPackageMain  = regexp.MustCompile(`(?m)^package\s+main\b`)
	reSpringBootApp  = regexp.MustCompile(`(?m)@SpringBootApplication\b`)
)

const (
	maxGraphFileBytes = 256_000
	maxImportEdges    = 8000
	maxEntryPoints    = 200
	maxModuleDeps     = 120
)

// EnrichGraph fills ImportEdges, EntryPoints, ModuleDeps and adjacency indexes.
// Safe to call multiple times; replaces previous graph fields.
func EnrichGraph(model *Model) {
	if model == nil {
		return
	}
	root := model.Root
	filesByPath := map[string]FileInfo{}
	var codeFiles []FileInfo
	for _, f := range model.Files {
		rel := filepath.ToSlash(f.RelativePath)
		filesByPath[rel] = f
		if IsCodeFile(rel) && !IsNoisePath(rel) {
			codeFiles = append(codeFiles, f)
		}
	}

	// Index: basename stem (Class.java → Class) and package-ish directory keys.
	stemIndex := map[string][]string{}          // lower stem → paths
	dirIndex := map[string][]string{}           // parent dir → paths
	pkgSuffixIndex := map[string][]string{}     // java-like a/b/c → paths under .../a/b/c/
	for _, f := range codeFiles {
		rel := filepath.ToSlash(f.RelativePath)
		base := filepath.Base(rel)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if stem != "" {
			k := strings.ToLower(stem)
			stemIndex[k] = append(stemIndex[k], rel)
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir != "." && dir != "" {
			dirIndex[dir] = append(dirIndex[dir], rel)
		}
		// package suffix from path after java/kotlin/scala or full dir
		pkg := packageKeyFromPath(rel)
		if pkg != "" {
			pkgSuffixIndex[pkg] = append(pkgSuffixIndex[pkg], rel)
			// also register short suffixes (last 2–3 segments)
			parts := strings.Split(pkg, "/")
			for n := 2; n <= 3 && n <= len(parts); n++ {
				suf := strings.Join(parts[len(parts)-n:], "/")
				pkgSuffixIndex[suf] = append(pkgSuffixIndex[suf], rel)
			}
		}
	}

	// Top-level directory segments (for Python absolute-import resolution).
	topDirs := map[string]bool{}
	for _, f := range model.Files {
		rel := filepath.ToSlash(f.RelativePath)
		if i := strings.Index(rel, "/"); i > 0 {
			topDirs[rel[:i]] = true
		}
	}

	var edges []ImportEdge
	seenEdge := map[string]bool{}
	addEdge := func(from, to, kind string) {
		from = filepath.ToSlash(from)
		to = filepath.ToSlash(to)
		if from == "" || to == "" || from == to {
			return
		}
		if _, ok := filesByPath[from]; !ok {
			return
		}
		if _, ok := filesByPath[to]; !ok {
			// allow only repo files
			return
		}
		key := from + "\x00" + to + "\x00" + kind
		if seenEdge[key] {
			return
		}
		seenEdge[key] = true
		edges = append(edges, ImportEdge{From: from, To: to, Kind: kind})
	}

	// Same-directory "package" edges (cheap structural signal).
	for dir, paths := range dirIndex {
		if dir == "." || len(paths) < 2 || len(paths) > 40 {
			continue
		}
		// Cap pairwise fan-out: link each file to up to 3 siblings.
		sort.Strings(paths)
		for i, a := range paths {
			n := 0
			for j, b := range paths {
				if i == j {
					continue
				}
				addEdge(a, b, "same_package")
				n++
				if n >= 3 {
					break
				}
			}
		}
	}

	// Parse imports per code file (bounded). The same read also feeds symbol,
	// route and entity extraction so no extra I/O is spent on them.
	symbolsByFile := map[string][]Symbol{}
	var routes []Route
	var entities []Entity
	for _, f := range codeFiles {
		if len(edges) >= maxImportEdges {
			break
		}
		rel := filepath.ToSlash(f.RelativePath)
		if f.Size > maxGraphFileBytes {
			continue
		}
		abs := rel
		if root != "" {
			abs = filepath.Join(root, filepath.FromSlash(rel))
		}
		data, err := os.ReadFile(abs)
		if err != nil || len(data) == 0 {
			continue
		}
		if len(data) > maxGraphFileBytes {
			data = data[:maxGraphFileBytes]
		}
		text := string(data)
		ext := strings.ToLower(filepath.Ext(rel))
		dir := filepath.ToSlash(filepath.Dir(rel))

		if syms := extractSymbols(ext, text); len(syms) > 0 {
			symbolsByFile[rel] = syms
		}
		if len(routes) < maxRoutesTotal {
			for _, r := range extractRoutes(ext, rel, text) {
				routes = append(routes, r)
				if len(routes) >= maxRoutesTotal {
					break
				}
			}
		}
		if len(entities) < maxEntitiesTotal {
			for _, e := range extractEntities(ext, rel, text) {
				entities = append(entities, e)
				if len(entities) >= maxEntitiesTotal {
					break
				}
			}
		}

		switch ext {
		case ".go":
			for _, imp := range parseGoImports(text) {
				for _, target := range resolveGoImport(imp, rel, pkgSuffixIndex, dirIndex) {
					addEdge(rel, target, "import")
				}
			}
		case ".java", ".kt", ".scala", ".groovy":
			for _, imp := range reJavaImport.FindAllStringSubmatch(text, 40) {
				if len(imp) < 2 {
					continue
				}
				for _, target := range resolveJavaImport(imp[1], stemIndex, pkgSuffixIndex) {
					addEdge(rel, target, "import")
				}
			}
		case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue", ".svelte":
			var specs []string
			for _, m := range reJSFrom.FindAllStringSubmatch(text, 40) {
				if len(m) > 1 {
					specs = append(specs, m[1])
				}
			}
			for _, m := range reJSRequire.FindAllStringSubmatch(text, 20) {
				if len(m) > 1 {
					specs = append(specs, m[1])
				}
			}
			for _, spec := range specs {
				for _, target := range resolveRelativeImport(spec, dir, filesByPath) {
					addEdge(rel, target, "import")
				}
			}
		case ".py":
			for _, m := range rePyFrom.FindAllStringSubmatch(text, 40) {
				mod := m[1]
				if mod == "" {
					mod = m[2]
				}
				if mod == "" {
					continue
				}
				if strings.HasPrefix(mod, ".") {
					for _, target := range resolvePythonRelative(mod, dir, filesByPath) {
						addEdge(rel, target, "import")
					}
					continue
				}
				// Absolute import — resolved only when the first segment maps
				// to a top-level repo directory (precision guard).
				for _, target := range resolvePythonAbsolute(mod, topDirs, filesByPath) {
					addEdge(rel, target, "import")
				}
			}
		case ".c", ".cc", ".cpp", ".h", ".hpp":
			for _, m := range reCInclude.FindAllStringSubmatch(text, 40) {
				for _, target := range resolveCInclude(m[1], dir, filesByPath) {
					addEdge(rel, target, "import")
				}
			}
		case ".cs":
			// C# namespaces resolve like java package suffixes.
			for _, m := range reCsUsing.FindAllStringSubmatch(text, 40) {
				for _, target := range resolveJavaImport(m[1], stemIndex, pkgSuffixIndex) {
					addEdge(rel, target, "import")
				}
			}
		case ".rs":
			for _, m := range reRustUse.FindAllStringSubmatch(text, 40) {
				for _, target := range resolveRustUse(m[1], rel, filesByPath) {
					addEdge(rel, target, "import")
				}
			}
			for _, m := range reRustMod.FindAllStringSubmatch(text, 40) {
				for _, target := range resolveRustMod(m[1], rel, filesByPath) {
					addEdge(rel, target, "import")
				}
			}
		case ".php":
			// `use NS\Class;` — reuse the java resolver via dot-separated form.
			for _, m := range rePhpUse.FindAllStringSubmatch(text, 40) {
				imp := strings.ReplaceAll(strings.Trim(m[1], `\`), `\`, ".")
				for _, target := range resolveJavaImport(imp, stemIndex, pkgSuffixIndex) {
					addEdge(rel, target, "import")
				}
			}
			for _, m := range rePhpRequire.FindAllStringSubmatch(text, 40) {
				for _, target := range resolvePhpRequire(m[1], dir, filesByPath) {
					addEdge(rel, target, "import")
				}
			}
		}
	}

	if len(edges) > maxImportEdges {
		edges = edges[:maxImportEdges]
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})

	// Entry points
	var entries []EntryPoint
	seenEntry := map[string]bool{}
	addEntry := func(path, kind, symbol string) {
		path = filepath.ToSlash(path)
		if path == "" || seenEntry[path] {
			return
		}
		if _, ok := filesByPath[path]; !ok {
			return
		}
		seenEntry[path] = true
		// Prefer a real declared symbol (main func / primary class) over the
		// filename stem when extraction found one.
		if syms := symbolsByFile[path]; len(syms) > 0 {
			symbol = entrySymbol(syms, symbol)
		}
		entries = append(entries, EntryPoint{Path: path, Kind: kind, Symbol: symbol})
	}

	for _, f := range codeFiles {
		if len(entries) >= maxEntryPoints {
			break
		}
		rel := filepath.ToSlash(f.RelativePath)
		lower := strings.ToLower(rel)
		base := filepath.Base(rel)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		ext := strings.ToLower(filepath.Ext(rel))

		// Path-based API surfaces (no file read).
		if IsAPISourceFile(rel) {
			addEntry(rel, "api", stem)
		}
		if matchEntryName(stem, base) {
			kind := "handler"
			if strings.Contains(strings.ToLower(stem), "main") {
				kind = "main"
			}
			addEntry(rel, kind, stem)
		}

		// Content-based for small files.
		if f.Size == 0 || f.Size > maxGraphFileBytes {
			continue
		}
		abs := rel
		if root != "" {
			abs = filepath.Join(root, filepath.FromSlash(rel))
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		if len(data) > maxGraphFileBytes {
			data = data[:maxGraphFileBytes]
		}
		text := string(data)
		switch ext {
		case ".go":
			if reGoPackageMain.MatchString(text) && reGoFuncMain.MatchString(text) {
				addEntry(rel, "main", "main")
			}
		case ".java", ".kt":
			if reSpringBootApp.MatchString(text) {
				addEntry(rel, "app", stem)
			}
			if reRestAnno.MatchString(text) {
				addEntry(rel, "api", stem)
			}
			if strings.EqualFold(stem, "Application") || strings.HasSuffix(stem, "Application") {
				addEntry(rel, "app", stem)
			}
		case ".py":
			if base == "main.py" || base == "__main__.py" || strings.Contains(text, "if __name__") {
				addEntry(rel, "main", stem)
			}
		case ".ts", ".js":
			if base == "main.ts" || base == "main.js" || base == "index.ts" || base == "index.js" ||
				base == "app.ts" || base == "app.js" || base == "server.ts" || base == "server.js" {
				addEntry(rel, "app", stem)
			}
		}
		_ = lower
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Path < entries[j].Path
	})
	if len(entries) > maxEntryPoints {
		entries = entries[:maxEntryPoints]
	}

	// Module dependency aggregation
	weight := map[string]int{}
	for _, e := range edges {
		if e.Kind != "import" {
			continue
		}
		fm := modulePathOf(e.From)
		tm := modulePathOf(e.To)
		if fm == "" || tm == "" || fm == tm {
			continue
		}
		key := fm + "\x00" + tm
		weight[key]++
	}
	var mods []ModuleDep
	for k, w := range weight {
		parts := strings.SplitN(k, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		mods = append(mods, ModuleDep{From: parts[0], To: parts[1], Weight: w})
	}
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].Weight != mods[j].Weight {
			return mods[i].Weight > mods[j].Weight
		}
		if mods[i].From != mods[j].From {
			return mods[i].From < mods[j].From
		}
		return mods[i].To < mods[j].To
	})
	if len(mods) > maxModuleDeps {
		mods = mods[:maxModuleDeps]
	}

	model.ImportEdges = edges
	model.EntryPoints = entries
	model.ModuleDeps = mods
	if len(symbolsByFile) > 0 {
		model.Symbols = symbolsByFile
	} else {
		model.Symbols = nil
	}
	model.Routes = routes
	model.Entities = entities
	enrichConfigKeys(model)
	model.rebuildAdjacency()
}

func (m *Model) rebuildAdjacency() {
	if m == nil {
		return
	}
	out := map[string][]string{}
	in := map[string][]string{}
	seenOut := map[string]map[string]bool{}
	seenIn := map[string]map[string]bool{}
	add := func(store map[string][]string, seen map[string]map[string]bool, from, to string) {
		if seen[from] == nil {
			seen[from] = map[string]bool{}
		}
		if seen[from][to] {
			return
		}
		seen[from][to] = true
		store[from] = append(store[from], to)
	}
	for _, e := range m.ImportEdges {
		// Prefer true imports; still include same_package as weaker neighbors.
		add(out, seenOut, e.From, e.To)
		add(in, seenIn, e.To, e.From)
	}
	m.importOut = out
	m.importIn = in
}

// ImportNeighbors returns files that import-related to path (out + in), capped.
// True import edges are returned before same_package / structural siblings so
// evidence and mermaid prefer semantic dependencies over directory co-location.
func (m *Model) ImportNeighbors(path string, max int) []string {
	if m == nil || path == "" || max <= 0 {
		return nil
	}
	if m.importOut == nil && m.importIn == nil && len(m.ImportEdges) > 0 {
		m.rebuildAdjacency()
	}
	path = filepath.ToSlash(path)
	seen := map[string]bool{path: true}
	var primary, secondary []string
	push := func(dst *[]string, p string) {
		p = filepath.ToSlash(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		*dst = append(*dst, p)
	}
	// Walk edges with kind preference first (more accurate than adjacency alone).
	for _, e := range m.ImportEdges {
		var other string
		if e.From == path {
			other = e.To
		} else if e.To == path {
			other = e.From
		} else {
			continue
		}
		if e.Kind == "same_package" {
			push(&secondary, other)
		} else {
			push(&primary, other)
		}
	}
	// Fall back to adjacency caches for anything missed (e.g. overlay without kinds).
	for _, p := range m.importOut[path] {
		push(&primary, p)
	}
	for _, p := range m.importIn[path] {
		push(&primary, p)
	}
	out := make([]string, 0, max)
	for _, p := range primary {
		out = append(out, p)
		if len(out) >= max {
			return out
		}
	}
	for _, p := range secondary {
		out = append(out, p)
		if len(out) >= max {
			return out
		}
	}
	return out
}

// ReachChain returns the shortest chain of files (ToSlash relative paths,
// endpoints included) from `from` to any path in targets, walking only
// kind=import out-edges. Depth is capped at 4 hops and the search visits at
// most ~200 nodes; nil when no target is reachable within those bounds.
func (m *Model) ReachChain(from string, targets map[string]bool, maxDepth int) []string {
	const maxVisited = 200
	if m == nil || from == "" || len(targets) == 0 {
		return nil
	}
	from = filepath.ToSlash(from)
	if targets[from] {
		return []string{from}
	}
	if maxDepth <= 0 || maxDepth > 4 {
		maxDepth = 4
	}
	// Import-only adjacency (the cached importOut also carries same_package
	// edges, which are co-location — not dependency — signals).
	out := map[string][]string{}
	for _, e := range m.ImportEdges {
		if e.Kind != "import" {
			continue
		}
		out[e.From] = append(out[e.From], e.To)
	}
	parent := map[string]string{from: ""}
	frontier := []string{from}
	visited := 1
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, node := range frontier {
			for _, to := range out[node] {
				if _, seen := parent[to]; seen {
					continue
				}
				parent[to] = node
				if targets[to] {
					var chain []string
					for p := to; p != ""; p = parent[p] {
						chain = append(chain, p)
					}
					for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
						chain[i], chain[j] = chain[j], chain[i]
					}
					return chain
				}
				visited++
				if visited >= maxVisited {
					return nil
				}
				next = append(next, to)
			}
		}
		frontier = next
	}
	return nil
}

// SameModuleFiles returns other code files sharing the module path of path.
func (m *Model) SameModuleFiles(path string, max int) []string {
	if m == nil || path == "" || max <= 0 {
		return nil
	}
	path = filepath.ToSlash(path)
	mod := modulePathOf(path)
	if mod == "" {
		return nil
	}
	var out []string
	for _, f := range m.Files {
		rel := filepath.ToSlash(f.RelativePath)
		if rel == path || !IsCodeFile(rel) || IsNoisePath(rel) {
			continue
		}
		if modulePathOf(rel) == mod {
			out = append(out, rel)
			if len(out) >= max {
				break
			}
		}
	}
	return out
}

// ApplyGraphFile merges an external graph snapshot into the model.
// Only edges whose endpoints exist in the scanned file list are kept.
func ApplyGraphFile(model *Model, g *GraphFile) {
	if model == nil || g == nil {
		return
	}
	files := map[string]bool{}
	for _, f := range model.Files {
		files[filepath.ToSlash(f.RelativePath)] = true
	}
	var edges []ImportEdge
	src := g.ImportEdges
	if len(src) == 0 {
		src = g.Edges
	}
	seen := map[string]bool{}
	for _, e := range append(append([]ImportEdge{}, model.ImportEdges...), src...) {
		from := filepath.ToSlash(e.From)
		to := filepath.ToSlash(e.To)
		if !files[from] || !files[to] || from == to {
			continue
		}
		key := from + "\x00" + to
		if seen[key] {
			continue
		}
		seen[key] = true
		if e.Kind == "" {
			e.Kind = "import"
		}
		edges = append(edges, ImportEdge{From: from, To: to, Kind: e.Kind})
		if len(edges) >= maxImportEdges {
			break
		}
	}
	model.ImportEdges = edges

	if len(g.EntryPoints) > 0 {
		seenE := map[string]bool{}
		var ents []EntryPoint
		for _, e := range append(append([]EntryPoint{}, model.EntryPoints...), g.EntryPoints...) {
			p := filepath.ToSlash(e.Path)
			if !files[p] || seenE[p] {
				continue
			}
			seenE[p] = true
			if e.Kind == "" {
				e.Kind = "handler"
			}
			ents = append(ents, EntryPoint{Path: p, Kind: e.Kind, Symbol: e.Symbol})
			if len(ents) >= maxEntryPoints {
				break
			}
		}
		model.EntryPoints = ents
	}
	// Symbols/Routes/ConfigKeys: fresh extraction wins; the overlay only fills
	// files the current scan did not cover. Unknown paths are dropped.
	if len(g.Symbols) > 0 {
		if model.Symbols == nil {
			model.Symbols = map[string][]Symbol{}
		}
		for p, syms := range g.Symbols {
			p = filepath.ToSlash(p)
			if !files[p] || len(model.Symbols[p]) > 0 || len(syms) == 0 {
				continue
			}
			if len(syms) > maxSymbolsPerFile {
				syms = syms[:maxSymbolsPerFile]
			}
			model.Symbols[p] = syms
		}
		if len(model.Symbols) == 0 {
			model.Symbols = nil
		}
	}
	if len(g.Routes) > 0 {
		seenR := map[string]bool{}
		var routes []Route
		for _, r := range append(append([]Route{}, model.Routes...), g.Routes...) {
			f := filepath.ToSlash(r.File)
			if !files[f] || strings.TrimSpace(r.Path) == "" {
				continue
			}
			key := r.Method + "\x00" + r.Path + "\x00" + f
			if seenR[key] {
				continue
			}
			seenR[key] = true
			r.File = f
			routes = append(routes, r)
			if len(routes) >= maxRoutesTotal {
				break
			}
		}
		model.Routes = routes
	}
	if len(g.Entities) > 0 {
		// Same policy as Symbols: fresh extraction wins per file; the overlay
		// only fills files the current scan produced no entities for.
		have := map[string]bool{}
		for _, e := range model.Entities {
			have[filepath.ToSlash(e.File)] = true
		}
		for _, e := range g.Entities {
			p := filepath.ToSlash(e.File)
			if !files[p] || have[p] || strings.TrimSpace(e.Name) == "" {
				continue
			}
			if len(model.Entities) >= maxEntitiesTotal {
				break
			}
			e.File = p
			if len(e.Fields) > maxEntityFields {
				e.Fields = e.Fields[:maxEntityFields]
			}
			model.Entities = append(model.Entities, e)
		}
	}
	if len(g.ConfigKeys) > 0 {
		if model.ConfigKeys == nil {
			model.ConfigKeys = map[string][]string{}
		}
		for p, ks := range g.ConfigKeys {
			p = filepath.ToSlash(p)
			if !files[p] || len(model.ConfigKeys[p]) > 0 || len(ks) == 0 {
				continue
			}
			if len(ks) > maxConfigKeysPerFile {
				ks = ks[:maxConfigKeysPerFile]
			}
			model.ConfigKeys[p] = ks
		}
		if len(model.ConfigKeys) == 0 {
			model.ConfigKeys = nil
		}
	}
	if len(g.ModuleDeps) > 0 {
		model.ModuleDeps = g.ModuleDeps
		if len(model.ModuleDeps) > maxModuleDeps {
			model.ModuleDeps = model.ModuleDeps[:maxModuleDeps]
		}
	} else {
		// re-aggregate from merged edges
		weight := map[string]int{}
		for _, e := range model.ImportEdges {
			if e.Kind == "same_package" {
				continue
			}
			fm, tm := modulePathOf(e.From), modulePathOf(e.To)
			if fm == "" || tm == "" || fm == tm {
				continue
			}
			weight[fm+"\x00"+tm]++
		}
		var mods []ModuleDep
		for k, w := range weight {
			parts := strings.SplitN(k, "\x00", 2)
			mods = append(mods, ModuleDep{From: parts[0], To: parts[1], Weight: w})
		}
		sort.Slice(mods, func(i, j int) bool {
			if mods[i].Weight != mods[j].Weight {
				return mods[i].Weight > mods[j].Weight
			}
			return mods[i].From+"/"+mods[i].To < mods[j].From+"/"+mods[j].To
		})
		if len(mods) > maxModuleDeps {
			mods = mods[:maxModuleDeps]
		}
		model.ModuleDeps = mods
	}
	model.rebuildAdjacency()
}

// LoadGraphFile reads a JSON graph file if present.
func LoadGraphFile(path string) (*GraphFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g GraphFile
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// WriteGraphFile persists the model's graph snapshot for later polish/export
// reloads (Scan auto-loads workDir/.wikify/graph.json when present).
// No-op when model is nil or the graph is empty.
func WriteGraphFile(path string, model *Model) error {
	if model == nil || path == "" {
		return nil
	}
	if len(model.ImportEdges) == 0 && len(model.EntryPoints) == 0 && len(model.ModuleDeps) == 0 &&
		len(model.Symbols) == 0 && len(model.Routes) == 0 && len(model.ConfigKeys) == 0 &&
		len(model.Entities) == 0 {
		return nil
	}
	g := GraphFile{
		ImportEdges: model.ImportEdges,
		EntryPoints: model.EntryPoints,
		ModuleDeps:  model.ModuleDeps,
		Symbols:     model.Symbols,
		Routes:      model.Routes,
		ConfigKeys:  model.ConfigKeys,
		Entities:    model.Entities,
	}
	if len(g.ImportEdges) > maxImportEdges {
		g.ImportEdges = g.ImportEdges[:maxImportEdges]
	}
	if len(g.EntryPoints) > maxEntryPoints {
		g.EntryPoints = g.EntryPoints[:maxEntryPoints]
	}
	if len(g.ModuleDeps) > maxModuleDeps {
		g.ModuleDeps = g.ModuleDeps[:maxModuleDeps]
	}
	if len(g.Routes) > maxRoutesTotal {
		g.Routes = g.Routes[:maxRoutesTotal]
	}
	if len(g.Entities) > maxEntitiesTotal {
		g.Entities = g.Entities[:maxEntitiesTotal]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// DefaultGraphPath returns workDir/.wikify/graph.json.
func DefaultGraphPath(workDir string) string {
	return filepath.Join(workDir, ".wikify", "graph.json")
}

func parseGoImports(text string) []string {
	var out []string
	// block import (
	lines := strings.Split(text, "\n")
	inBlock := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if !inBlock {
			if strings.HasPrefix(trim, "import ") {
				if strings.Contains(trim, "(") {
					inBlock = true
					continue
				}
				if m := reGoImportSingle.FindStringSubmatch(line); len(m) > 1 {
					out = append(out, m[1])
				}
			}
			continue
		}
		if strings.HasPrefix(trim, ")") {
			inBlock = false
			continue
		}
		if m := reGoImportLine.FindStringSubmatch(trim); len(m) > 1 {
			out = append(out, m[1])
		} else if i := strings.Index(trim, "\""); i >= 0 {
			rest := trim[i+1:]
			if j := strings.Index(rest, "\""); j >= 0 {
				out = append(out, rest[:j])
			}
		}
	}
	return uniqueStrings(out)
}

func resolveGoImport(imp, from string, pkgSuffixIndex, dirIndex map[string][]string) []string {
	imp = strings.TrimSpace(imp)
	if imp == "" {
		return nil
	}
	// Skip stdlib single-segment imports (fmt, os, …).
	if !strings.Contains(imp, ".") && !strings.Contains(imp, "/") {
		return nil
	}
	// Prefer last 2–4 path segments as package key.
	parts := strings.Split(imp, "/")
	var candidates []string
	for n := min(4, len(parts)); n >= 2; n-- {
		key := strings.Join(parts[len(parts)-n:], "/")
		if paths := pkgSuffixIndex[key]; len(paths) > 0 {
			candidates = append(candidates, paths...)
			break
		}
		if paths := dirIndex[key]; len(paths) > 0 {
			candidates = append(candidates, paths...)
			break
		}
	}
	// Single segment package under same top module
	if len(candidates) == 0 && len(parts) >= 1 {
		last := parts[len(parts)-1]
		for dir, paths := range dirIndex {
			if strings.HasSuffix(dir, "/"+last) || dir == last {
				candidates = append(candidates, paths...)
			}
		}
	}
	return filterNotSelf(from, uniqueStrings(candidates), 6)
}

func resolveJavaImport(imp string, stemIndex, pkgSuffixIndex map[string][]string) []string {
	imp = strings.TrimSpace(imp)
	if imp == "" {
		return nil
	}
	parts := strings.Split(imp, ".")
	if len(parts) == 0 {
		return nil
	}
	last := parts[len(parts)-1]
	// Type import: last segment capitalized
	if last != "" && last[0] >= 'A' && last[0] <= 'Z' {
		if paths := stemIndex[strings.ToLower(last)]; len(paths) > 0 {
			return filterNotSelf("", paths, 4)
		}
	}
	// Package import → directory
	pkg := strings.ToLower(strings.Join(parts, "/"))
	if paths := pkgSuffixIndex[pkg]; len(paths) > 0 {
		return paths[:min(6, len(paths))]
	}
	if len(parts) >= 2 {
		suf := strings.ToLower(strings.Join(parts[len(parts)-2:], "/"))
		if paths := pkgSuffixIndex[suf]; len(paths) > 0 {
			return paths[:min(6, len(paths))]
		}
	}
	return nil
}

func resolveRelativeImport(spec, fromDir string, files map[string]FileInfo) []string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}
	// Only relative / alias-less local paths
	if !(strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/")) {
		// bare alias @/ — skip (project-specific)
		if strings.HasPrefix(spec, "@") {
			return nil
		}
		// non-relative package — skip
		if !strings.HasPrefix(spec, ".") {
			return nil
		}
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.Join(fromDir, spec)))
	cleaned = strings.TrimPrefix(cleaned, "/")
	var out []string
	try := func(p string) {
		p = filepath.ToSlash(p)
		if _, ok := files[p]; ok {
			out = append(out, p)
		}
	}
	try(cleaned)
	exts := []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue", ".json"}
	for _, e := range exts {
		try(cleaned + e)
	}
	for _, e := range exts {
		try(cleaned + "/index" + e)
	}
	return uniqueStrings(out)
}

func resolvePythonRelative(mod, fromDir string, files map[string]FileInfo) []string {
	// from ..foo.bar import baz  /  from . import x
	dots := 0
	for dots < len(mod) && mod[dots] == '.' {
		dots++
	}
	rest := mod[dots:]
	dir := fromDir
	for i := 0; i < dots-1; i++ {
		dir = filepath.ToSlash(filepath.Dir(dir))
		if dir == "." {
			break
		}
	}
	if rest == "" {
		// from . import something — link package __init__ if any
		try := filepath.ToSlash(filepath.Join(dir, "__init__.py"))
		if _, ok := files[try]; ok {
			return []string{try}
		}
		return nil
	}
	rel := filepath.ToSlash(filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(rest, ".", "/"))))
	var out []string
	for _, p := range []string{rel + ".py", rel + "/__init__.py"} {
		if _, ok := files[p]; ok {
			out = append(out, p)
		}
	}
	return out
}

// resolvePythonAbsolute maps `from a.b import x` / `import a.b` to a/b.py or
// a/b/__init__.py when segment `a` is a top-level repo directory (or a.py
// exists at the root for single-segment modules).
func resolvePythonAbsolute(mod string, topDirs map[string]bool, files map[string]FileInfo) []string {
	parts := strings.Split(strings.TrimSpace(mod), ".")
	if len(parts) == 0 || parts[0] == "" {
		return nil
	}
	if !topDirs[parts[0]] {
		if len(parts) == 1 {
			if p := parts[0] + ".py"; hasFile(files, p) {
				return []string{p}
			}
		}
		return nil
	}
	rel := strings.Join(parts, "/")
	var out []string
	for _, p := range []string{rel + ".py", rel + "/__init__.py"} {
		if hasFile(files, p) {
			out = append(out, p)
		}
	}
	return out
}

// resolveCInclude resolves quoted #include "…" against the including file's
// directory and the repo root, trying .h/.hpp candidates for bare names.
func resolveCInclude(spec, fromDir string, files map[string]FileInfo) []string {
	spec = strings.TrimSpace(filepath.ToSlash(spec))
	if spec == "" {
		return nil
	}
	var out []string
	try := func(p string) {
		p = filepath.ToSlash(filepath.Clean(p))
		p = strings.TrimPrefix(p, "./")
		if hasFile(files, p) {
			out = append(out, p)
		}
	}
	for _, base := range []string{filepath.ToSlash(filepath.Join(fromDir, spec)), spec} {
		try(base)
		if filepath.Ext(base) == "" {
			try(base + ".h")
			try(base + ".hpp")
		}
	}
	return uniqueStrings(out)
}

// resolveRustUse resolves `use crate::a::b::Item` against the crate source
// root (nearest ancestor "src" dir, else the file's own dir), dropping
// trailing item segments until a module file matches.
func resolveRustUse(usePath, fromRel string, files map[string]FileInfo) []string {
	var segs []string
	for _, s := range strings.Split(usePath, "::") {
		s = strings.TrimSpace(s)
		if s == "" || s == "*" {
			continue
		}
		segs = append(segs, s)
	}
	if len(segs) == 0 {
		return nil
	}
	dir := filepath.ToSlash(filepath.Dir(fromRel))
	root := dir
	parts := strings.Split(dir, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == "src" {
			root = strings.Join(parts[:i+1], "/")
			break
		}
	}
	for n := len(segs); n >= 1; n-- {
		rel := strings.Join(segs[:n], "/")
		var out []string
		for _, cand := range []string{rel + ".rs", rel + "/mod.rs"} {
			p := filepath.ToSlash(filepath.Join(root, cand))
			p = strings.TrimPrefix(p, "./")
			if hasFile(files, p) {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return uniqueStrings(out)
		}
	}
	return nil
}

// resolveRustMod resolves `mod name;` to <dir>/name.rs, <dir>/name/mod.rs, or
// (2018 edition submodules) <dir>/<stem>/name.rs for non-root module files.
func resolveRustMod(name, fromRel string, files map[string]FileInfo) []string {
	dir := filepath.ToSlash(filepath.Dir(fromRel))
	cands := []string{name + ".rs", name + "/mod.rs"}
	stem := strings.TrimSuffix(filepath.Base(fromRel), ".rs")
	if stem != "main" && stem != "lib" && stem != "mod" {
		cands = append(cands, stem+"/"+name+".rs")
	}
	for _, cand := range cands {
		p := filepath.ToSlash(filepath.Join(dir, cand))
		p = strings.TrimPrefix(p, "./")
		if hasFile(files, p) {
			return []string{p}
		}
	}
	return nil
}

// resolvePhpRequire resolves require/include specs relative to the file's dir
// (also handling `__DIR__ . '/x.php'` captures that begin with "/").
func resolvePhpRequire(spec, fromDir string, files map[string]FileInfo) []string {
	spec = strings.TrimSpace(filepath.ToSlash(spec))
	if spec == "" || strings.HasPrefix(spec, "http") {
		return nil
	}
	var out []string
	try := func(base string) {
		p := filepath.ToSlash(filepath.Clean(base))
		p = strings.TrimPrefix(p, "./")
		if hasFile(files, p) {
			out = append(out, p)
		}
		if filepath.Ext(p) == "" && hasFile(files, p+".php") {
			out = append(out, p+".php")
		}
	}
	try(filepath.ToSlash(filepath.Join(fromDir, spec)))
	try(strings.TrimPrefix(spec, "/"))
	return uniqueStrings(out)
}

func hasFile(files map[string]FileInfo, p string) bool {
	_, ok := files[filepath.ToSlash(p)]
	return ok
}

func packageKeyFromPath(rel string) string {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		if p == "java" || p == "kotlin" || p == "scala" {
			if i+1 < len(parts) {
				rest := parts[i+1 : len(parts)-1] // drop filename
				if len(rest) == 0 {
					return ""
				}
				return strings.ToLower(strings.Join(rest, "/"))
			}
		}
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." {
		return ""
	}
	return strings.ToLower(dir)
}

func matchEntryName(stem, base string) bool {
	s := strings.ToLower(stem)
	switch {
	case s == "main" || s == "app" || s == "server" || s == "application":
		return true
	case strings.HasSuffix(s, "controller") || strings.HasSuffix(s, "resource") ||
		strings.HasSuffix(s, "handler") || strings.HasSuffix(s, "endpoint") ||
		strings.HasSuffix(s, "servlet") || strings.HasSuffix(s, "restcontroller"):
		return true
	case base == "main.go" || base == "main.py" || base == "Main.java":
		return true
	}
	return false
}

func filterNotSelf(from string, paths []string, max int) []string {
	var out []string
	for _, p := range paths {
		if p == from {
			continue
		}
		out = append(out, p)
		if len(out) >= max {
			break
		}
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
