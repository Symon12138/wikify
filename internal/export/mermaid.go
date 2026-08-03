package export

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JSHurt/wikify/internal/scan"
)

// defaultMermaid returns up to need diagram blocks for title.
// already offsets which templates we skip so top-ups do not duplicate the first N
// that a previous pass may have written.
// When focusPaths are non-empty (typically page DependentFiles), the first diagram
// is a file-level dependency subgraph (import edges or role-chain fallback).
func defaultMermaid(model *scan.Model, title string, need, already int, focusPaths ...string) []string {
	name := "System"
	var mods []string
	if model != nil {
		if model.Name != "" {
			name = model.Name
		}
		for i, m := range model.Modules {
			if i >= 6 {
				break
			}
			label := m.Name
			if label == "" {
				label = m.Path
			}
			if label != "" {
				mods = append(mods, escapeMermaid(label))
			}
		}
	}
	repo := escapeMermaid(name)
	topic := escapeMermaid(title)
	if repo == "" {
		repo = "System"
	}

	// Pool of diverse diagram types. already offsets which templates we skip so
	// top-ups do not duplicate the first N that a previous pass may have written.
	var pool []string

	// 0 — page-local dependency graph (import edges, else role chain).
	if g := codeDependencyDiagram(model, title, focusPaths); g != "" {
		pool = append(pool, g)
	}

	// Data-grounded diagram types come right after the dependency graph and
	// before the generic templates, so pages with need > 1 naturally pick up
	// diverse diagram kinds (erDiagram / classDiagram / sequenceDiagram) built
	// from names actually extracted from the code.
	if g := entityERMermaid(model, focusPaths); g != "" {
		pool = append(pool, g)
	}
	if g := classDiagramMermaid(model, focusPaths); g != "" {
		pool = append(pool, g)
	}
	if g := routeSequenceMermaid(model, focusPaths); g != "" {
		pool = append(pool, g)
	}

	// 1 flowchart TB — topic in repo context
	var g1 strings.Builder
	g1.WriteString("```mermaid\nflowchart TB\n")
	g1.WriteString(fmt.Sprintf("  Repo[\"%s\"] --> Topic[\"%s\"]\n", repo, topic))
	if len(mods) == 0 {
		g1.WriteString("  Topic --> Impl[Implementation]\n")
		g1.WriteString("  Topic --> Data[Data]\n")
	} else {
		for i, m := range mods {
			id := fmt.Sprintf("M%d", i+1)
			g1.WriteString(fmt.Sprintf("  Topic --> %s[\"%s\"]\n", id, m))
		}
	}
	g1.WriteString("```")
	pool = append(pool, g1.String())

	// 2 flowchart LR — module mesh or layered (prefer real ModuleDeps)
	var g2 strings.Builder
	g2.WriteString("```mermaid\nflowchart LR\n")
	if model != nil && len(model.ModuleDeps) > 0 {
		// Real inter-module import edges from the code graph.
		idOf := map[string]string{}
		next := 1
		idFor := func(path string) string {
			label := path
			if i := strings.LastIndex(path, "/"); i >= 0 {
				label = path[i+1:]
			}
			if label == "" {
				label = path
			}
			if id, ok := idOf[path]; ok {
				return id
			}
			id := fmt.Sprintf("N%d", next)
			next++
			idOf[path] = id
			g2.WriteString(fmt.Sprintf("  %s[\"%s\"]\n", id, escapeMermaid(label)))
			return id
		}
		limit := 8
		if limit > len(model.ModuleDeps) {
			limit = len(model.ModuleDeps)
		}
		for i := 0; i < limit; i++ {
			d := model.ModuleDeps[i]
			a, b := idFor(d.From), idFor(d.To)
			g2.WriteString(fmt.Sprintf("  %s --> %s\n", a, b))
		}
	} else if len(mods) >= 2 {
		g2.WriteString("  subgraph Modules\n")
		for i, m := range mods {
			id := fmt.Sprintf("N%d", i+1)
			g2.WriteString(fmt.Sprintf("    %s[\"%s\"]\n", id, m))
		}
		g2.WriteString("  end\n")
		for i := 0; i < len(mods)-1 && i < 4; i++ {
			g2.WriteString(fmt.Sprintf("  N%d --> N%d\n", i+1, i+2))
		}
	} else {
		g2.WriteString("  UI[Presentation] --> App[Application]\n")
		g2.WriteString(fmt.Sprintf("  App --> Domain[\"%s\"]\n", topic))
		g2.WriteString("  Domain --> Infra[Infrastructure]\n")
	}
	g2.WriteString("```")
	pool = append(pool, g2.String())

	// 3 sequenceDiagram — typical request path through the topic
	var g3 strings.Builder
	g3.WriteString("```mermaid\nsequenceDiagram\n")
	g3.WriteString("  participant C as Client\n")
	g3.WriteString("  participant A as API/Entry\n")
	g3.WriteString(fmt.Sprintf("  participant S as %s\n", topic))
	g3.WriteString("  participant D as Data/Store\n")
	g3.WriteString("  C->>A: Request\n")
	g3.WriteString("  A->>S: Delegate\n")
	g3.WriteString("  S->>D: Load/Persist\n")
	g3.WriteString("  D-->>S: Result\n")
	g3.WriteString("  S-->>A: Response model\n")
	g3.WriteString("  A-->>C: Reply\n")
	g3.WriteString("```")
	pool = append(pool, g3.String())

	// 4 classDiagram — conceptual components around the topic
	var g4 strings.Builder
	g4.WriteString("```mermaid\nclassDiagram\n")
	g4.WriteString(fmt.Sprintf("  class Topic[\"%s\"]\n", topic))
	g4.WriteString("  class Entry\n")
	g4.WriteString("  class Service\n")
	g4.WriteString("  class Store\n")
	g4.WriteString("  Entry --> Service : invoke\n")
	g4.WriteString("  Service --> Topic : domain\n")
	g4.WriteString("  Service --> Store : persist\n")
	g4.WriteString("```")
	pool = append(pool, g4.String())

	// 5 stateDiagram — lifecycle of the capability
	var g5 strings.Builder
	g5.WriteString("```mermaid\nstateDiagram-v2\n")
	g5.WriteString("  [*] --> Idle\n")
	g5.WriteString(fmt.Sprintf("  Idle --> Active : enter %s\n", topic))
	g5.WriteString("  Active --> Processing : work\n")
	g5.WriteString("  Processing --> Active : continue\n")
	g5.WriteString("  Processing --> Done : complete\n")
	g5.WriteString("  Done --> [*]\n")
	g5.WriteString("```")
	pool = append(pool, g5.String())

	// 6 mindmap-like flowchart of concerns
	var g6 strings.Builder
	g6.WriteString("```mermaid\nflowchart TD\n")
	g6.WriteString(fmt.Sprintf("  T[\"%s\"] --> R[Rules]\n", topic))
	g6.WriteString("  T --> I[Interfaces]\n")
	g6.WriteString("  T --> P[Persistence]\n")
	g6.WriteString("  T --> O[Ops/Config]\n")
	g6.WriteString("```")
	pool = append(pool, g6.String())

	if already < 0 {
		already = 0
	}
	if already >= len(pool) {
		already = 0
	}
	if need <= 0 {
		need = 2
	}
	var out []string
	for i := 0; len(out) < need && i < len(pool); i++ {
		idx := (already + i) % len(pool)
		out = append(out, pool[idx])
	}
	return out
}

// codeDependencyDiagram returns the best page-local dependency diagram:
//  1. import-edge subgraph when the scan graph connects focus files
//  2. role-chain (Controller→Service→Dao→PO) inferred from focus basenames
//
// Returns "" when neither is useful.
func codeDependencyDiagram(model *scan.Model, title string, focus []string) string {
	if g := focusDependencyMermaid(model, title, focus); g != "" {
		return g
	}
	return roleChainMermaid(focus)
}

// focusDependencyMermaid builds a compact flowchart of import edges among
// page focus files and their 1-hop neighbors. Returns "" when the graph has
// nothing useful for this page.
func focusDependencyMermaid(model *scan.Model, title string, focus []string) string {
	if model == nil || len(model.ImportEdges) == 0 {
		return ""
	}
	focusSet := map[string]bool{}
	var seeds []string
	for _, p := range focus {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || focusSet[p] {
			continue
		}
		lower := strings.ToLower(p)
		// Skip non-code noise as seeds.
		if strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".md") ||
			strings.HasSuffix(lower, "pom.xml") || strings.HasSuffix(lower, "package.json") ||
			strings.HasSuffix(lower, ".css") || strings.HasSuffix(lower, ".scss") {
			continue
		}
		focusSet[p] = true
		seeds = append(seeds, p)
		if len(seeds) >= 8 {
			break
		}
	}
	// When no refs, try entry points whose basenames loosely match title tokens.
	if len(seeds) == 0 && len(model.EntryPoints) > 0 {
		titleLower := strings.ToLower(title)
		tok := firstAlphaToken(titleLower)
		for _, ep := range model.EntryPoints {
			base := strings.ToLower(filepath.Base(ep.Path))
			stem := strings.TrimSuffix(base, filepath.Ext(base))
			if stem != "" && (strings.Contains(titleLower, stem) || (tok != "" && strings.Contains(stem, tok))) {
				p := filepath.ToSlash(ep.Path)
				if !focusSet[p] {
					focusSet[p] = true
					seeds = append(seeds, p)
				}
			}
			if len(seeds) >= 4 {
				break
			}
		}
	}
	if len(seeds) == 0 {
		return ""
	}

	// Node set = focus files only; no 1-hop expansion to avoid showing
	// unrelated files that happen to share an import edge with a focus file.
	nodeSet := map[string]bool{}
	for _, s := range seeds {
		nodeSet[s] = true
	}

	// Collect edges between selected nodes (prefer kind=import).
	type edge struct{ from, to string }
	var edges []edge
	seenE := map[string]bool{}
	for _, e := range model.ImportEdges {
		if e.Kind == "same_package" {
			continue
		}
		from, to := filepath.ToSlash(e.From), filepath.ToSlash(e.To)
		if !nodeSet[from] || !nodeSet[to] {
			continue
		}
		key := from + "\x00" + to
		if seenE[key] {
			continue
		}
		seenE[key] = true
		edges = append(edges, edge{from, to})
		if len(edges) >= 14 {
			break
		}
	}
	if len(edges) == 0 {
		// Fall back: star from first seed to neighbors even without mutual edges.
		s0 := seeds[0]
		nbs := model.ImportNeighbors(s0, 3)
		if len(nbs) == 0 {
			return ""
		}
		for _, nb := range nbs {
			edges = append(edges, edge{s0, filepath.ToSlash(nb)})
		}
	}

	idOf := map[string]string{}
	next := 1
	var b strings.Builder
	b.WriteString("```mermaid\nflowchart LR\n")
	idFor := func(path string) string {
		if id, ok := idOf[path]; ok {
			return id
		}
		label := filepath.Base(path)
		if label == "" {
			label = path
		}
		id := fmt.Sprintf("F%d", next)
		next++
		idOf[path] = id
		// Highlight seed focus nodes.
		if focusSet[path] {
			b.WriteString(fmt.Sprintf("  %s[\"%s\"]:::focus\n", id, escapeMermaid(label)))
		} else {
			b.WriteString(fmt.Sprintf("  %s[\"%s\"]\n", id, escapeMermaid(label)))
		}
		return id
	}
	for _, e := range edges {
		a, c := idFor(e.from), idFor(e.to)
		b.WriteString(fmt.Sprintf("  %s --> %s\n", a, c))
	}
	b.WriteString("  classDef focus stroke-width:2px\n")
	b.WriteString("```")
	return b.String()
}

// roleChainMermaid builds Controller → Service → Dao/Mapper → Entity/PO chain
// from focus basenames when import edges are unavailable (common for Java
// scan without full package resolution). Pure heuristic; never invents files.
func roleChainMermaid(focus []string) string {
	type node struct {
		id, label, role string
	}
	var nodes []node
	seen := map[string]bool{}
	roleOf := func(path string) string {
		lower := strings.ToLower(filepath.ToSlash(path))
		base := filepath.Base(lower)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		switch {
		// Java *Controller/*Control + ExtJS *Cont.js / *Controller.js
		case strings.Contains(stem, "controller") || strings.HasSuffix(stem, "control") ||
			strings.HasSuffix(stem, "cont") ||
			strings.Contains(stem, "handler") || strings.Contains(stem, "resource") ||
			strings.Contains(stem, "servlet") || strings.HasSuffix(stem, "action"):
			return "ctrl"
		case strings.Contains(stem, "service") || strings.HasSuffix(stem, "svc") ||
			strings.HasSuffix(stem, "manager") || strings.HasSuffix(stem, "mgr"):
			return "svc"
		case strings.Contains(stem, "dao") || strings.Contains(stem, "mapper") ||
			strings.Contains(stem, "repository") || strings.HasSuffix(stem, "repo"):
			return "dao"
		// Entity/PO + ExtJS *Model.js / *Store.js
		case strings.Contains(stem, "entity") || strings.HasSuffix(stem, "po") ||
			strings.HasSuffix(stem, "pojo") || strings.Contains(stem, "model") ||
			strings.Contains(stem, "dto") || strings.Contains(stem, "vo") ||
			strings.HasSuffix(stem, "store"):
			return "ent"
		}
		// Package-layout fallback (FirstWeight.java under /po/iir/ is still entity).
		switch {
		case strings.Contains(lower, "/controller/") || strings.Contains(lower, "/controllers/") ||
			strings.Contains(lower, "/handler/") || strings.Contains(lower, "/servlet/"):
			return "ctrl"
		case strings.Contains(lower, "/service/") || strings.Contains(lower, "/services/") ||
			strings.Contains(lower, "/serviceimpl/") || strings.Contains(lower, "/svc/"):
			return "svc"
		case strings.Contains(lower, "/dao/") || strings.Contains(lower, "/mapper/") ||
			strings.Contains(lower, "/repository/") || strings.Contains(lower, "/repositories/") ||
			strings.Contains(lower, "/repo/"):
			return "dao"
		case strings.Contains(lower, "/po/") || strings.Contains(lower, "/entity/") ||
			strings.Contains(lower, "/entities/") || strings.Contains(lower, "/domain/") ||
			strings.Contains(lower, "/model/") || strings.Contains(lower, "/models/") ||
			strings.Contains(lower, "/dto/") || strings.Contains(lower, "/vo/"):
			return "ent"
		default:
			return ""
		}
	}
	for _, p := range focus {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		role := roleOf(p)
		if role == "" {
			continue
		}
		lower := strings.ToLower(p)
		if strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".md") ||
			strings.HasSuffix(lower, "pom.xml") {
			continue
		}
		seen[p] = true
		label := filepath.Base(p)
		id := fmt.Sprintf("R%d", len(nodes)+1)
		nodes = append(nodes, node{id: id, label: escapeMermaid(label), role: role})
		if len(nodes) >= 10 {
			break
		}
	}
	// Need at least two distinct roles to draw a chain.
	roles := map[string]int{}
	for _, n := range nodes {
		roles[n.role]++
	}
	if len(roles) < 2 {
		return ""
	}
	order := []string{"ctrl", "svc", "dao", "ent"}
	byRole := map[string][]node{}
	for _, n := range nodes {
		byRole[n.role] = append(byRole[n.role], n)
	}
	var b strings.Builder
	b.WriteString("```mermaid\nflowchart LR\n")
	for _, r := range order {
		for _, n := range byRole[r] {
			b.WriteString(fmt.Sprintf("  %s[\"%s\"]\n", n.id, n.label))
		}
	}
	// Chain primary representatives across roles.
	var primaries []node
	for _, r := range order {
		if list := byRole[r]; len(list) > 0 {
			primaries = append(primaries, list[0])
		}
	}
	for i := 0; i < len(primaries)-1; i++ {
		b.WriteString(fmt.Sprintf("  %s --> %s\n", primaries[i].id, primaries[i+1].id))
	}
	// Peer extras of same role → next primary (or link to last primary).
	for _, r := range order {
		list := byRole[r]
		if len(list) <= 1 {
			continue
		}
		var nextID string
		for i, p := range primaries {
			if p.role == r && i+1 < len(primaries) {
				nextID = primaries[i+1].id
				break
			}
		}
		for _, extra := range list[1:] {
			if nextID != "" {
				b.WriteString(fmt.Sprintf("  %s --> %s\n", extra.id, nextID))
			} else if len(primaries) > 0 {
				b.WriteString(fmt.Sprintf("  %s -.-> %s\n", primaries[len(primaries)-1].id, extra.id))
			}
		}
	}
	b.WriteString("```")
	return b.String()
}

// entityERMermaid builds an erDiagram from Kind="entity" declarations whose
// source file appears in the page focus paths. Table and column labels are the
// names extracted from code (table annotations / struct tags / field names) —
// no domain guessing. Relations are drawn only when one entity carries a
// `<other>Id` / `<other>_id` field; otherwise tables stand side by side.
// Returns "" when no focus file contributes an entity.
func entityERMermaid(model *scan.Model, focus []string) string {
	if model == nil || len(model.Entities) == 0 {
		return ""
	}
	focusSet := mermaidFocusSet(focus)
	if len(focusSet) == 0 {
		return ""
	}
	const maxTables = 6
	const maxColumns = 12

	type table struct {
		entity scan.Entity
		id     string
		attrs  []string
	}
	var tables []table
	seen := map[string]bool{}
	for _, e := range model.Entities {
		if e.Kind != "entity" || len(e.Fields) == 0 || !focusSet[filepath.ToSlash(e.File)] {
			continue
		}
		label := e.Table
		if label == "" {
			label = e.Name
		}
		id := mermaidIdent(label)
		if id == "" || seen[id] {
			continue
		}
		var attrs []string
		for _, f := range e.Fields {
			typ := erTypeToken(f.Type)
			col := f.Column
			if col == "" {
				col = f.Name
			}
			name := mermaidIdent(col)
			if typ == "" || name == "" {
				continue
			}
			line := "    " + typ + " " + name
			if f.Key {
				line += " PK"
			}
			attrs = append(attrs, line)
			if len(attrs) >= maxColumns {
				break
			}
		}
		if len(attrs) == 0 {
			continue
		}
		seen[id] = true
		tables = append(tables, table{entity: e, id: id, attrs: attrs})
		if len(tables) >= maxTables {
			break
		}
	}
	if len(tables) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("```mermaid\nerDiagram\n")
	for _, t := range tables {
		b.WriteString("  " + t.id + " {\n")
		for _, a := range t.attrs {
			b.WriteString(a + "\n")
		}
		b.WriteString("  }\n")
	}
	for i, ta := range tables {
		for j, tc := range tables {
			if i == j {
				continue
			}
			ref := erReferenceField(ta.entity, tc.entity)
			if ref == "" {
				continue
			}
			// tc is referenced by ta (ta holds the foreign-key field).
			b.WriteString(fmt.Sprintf("  %s ||--o{ %s : %s\n", tc.id, ta.id, mermaidIdent(ref)))
		}
	}
	b.WriteString("```")
	return b.String()
}

// erReferenceField returns the field of a that looks like a foreign key to c
// (name `<c>Id` / `<c>_id`, or column `<c_table>_id`), else "".
func erReferenceField(a, c scan.Entity) string {
	target := strings.ToLower(c.Name)
	targetSnake := lowerSnake(c.Name)
	tableBase := strings.ToLower(c.Table)
	for _, f := range a.Fields {
		n := strings.ToLower(f.Name)
		col := strings.ToLower(f.Column)
		switch {
		case n == target+"id" || n == targetSnake+"_id",
			col == targetSnake+"_id" || (col != "" && col == target+"id"),
			tableBase != "" && (col == tableBase+"_id" || n == tableBase+"id"):
			if f.Column != "" {
				return f.Column
			}
			return f.Name
		}
	}
	return ""
}

// classDiagramMermaid builds a classDiagram from Kind class/interface
// declarations hit by the page focus paths, merging Entities fields with
// public method names from model.Symbols. Method-to-class attribution from
// Symbols is unknown for multi-declaration files, so methods are attached
// only when the file declares exactly one class-like entity; otherwise the
// class lists fields only. Returns "" when no focus file contributes a class.
func classDiagramMermaid(model *scan.Model, focus []string) string {
	if model == nil || len(model.Entities) == 0 {
		return ""
	}
	focusSet := mermaidFocusSet(focus)
	if len(focusSet) == 0 {
		return ""
	}
	const maxClasses = 6
	const maxFields = 10
	const maxMethods = 8

	declsPerFile := map[string]int{}
	for _, e := range model.Entities {
		declsPerFile[filepath.ToSlash(e.File)]++
	}
	methodsFor := func(file string) []string {
		if model.Symbols == nil || declsPerFile[file] != 1 {
			return nil
		}
		var out []string
		seen := map[string]bool{}
		for _, s := range model.Symbols[file] {
			if s.Kind != "method" || s.Name == "" || seen[s.Name] {
				continue
			}
			seen[s.Name] = true
			out = append(out, s.Name)
			if len(out) >= maxMethods {
				break
			}
		}
		return out
	}

	var picked []scan.Entity
	methodsOf := map[string][]string{}
	seen := map[string]bool{}
	for _, e := range model.Entities {
		if e.Kind != "class" && e.Kind != "interface" {
			continue
		}
		file := filepath.ToSlash(e.File)
		if !focusSet[file] {
			continue
		}
		id := mermaidIdent(e.Name)
		if id == "" || seen[id] {
			continue
		}
		methods := methodsFor(file)
		if len(e.Fields) == 0 && len(methods) == 0 {
			continue
		}
		seen[id] = true
		methodsOf[id] = methods
		picked = append(picked, e)
		if len(picked) >= maxClasses {
			break
		}
	}
	if len(picked) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("```mermaid\nclassDiagram\n")
	for _, e := range picked {
		id := mermaidIdent(e.Name)
		b.WriteString("  class " + id + " {\n")
		if e.Kind == "interface" {
			b.WriteString("    <<interface>>\n")
		}
		n := 0
		for _, f := range e.Fields {
			name := mermaidIdent(f.Name)
			if name == "" {
				continue
			}
			typ := classTypeToken(f.Type)
			if typ != "" {
				b.WriteString("    " + typ + " " + name + "\n")
			} else {
				b.WriteString("    " + name + "\n")
			}
			n++
			if n >= maxFields {
				break
			}
		}
		for _, m := range methodsOf[id] {
			b.WriteString("    +" + mermaidIdent(m) + "()\n")
		}
		b.WriteString("  }\n")
	}
	// Field-type associations between picked classes (real declared types only).
	seenRel := map[string]bool{}
	for _, a := range picked {
		aID := mermaidIdent(a.Name)
		for _, c := range picked {
			if a.Name == c.Name {
				continue
			}
			cID := mermaidIdent(c.Name)
			key := aID + "->" + cID
			if seenRel[key] {
				continue
			}
			for _, f := range a.Fields {
				if typeMentions(f.Type, c.Name) {
					seenRel[key] = true
					b.WriteString(fmt.Sprintf("  %s --> %s : %s\n", aID, cID, mermaidIdent(f.Name)))
					break
				}
			}
		}
	}
	b.WriteString("```")
	return b.String()
}

// routeSequenceMermaid draws a sequenceDiagram when a focus file registers
// HTTP routes and imports a service-like collaborator. Route methods/paths
// and file names are real extracted data; arrow labels stay generic
// (delegate/result) so no invented domain call is drawn. Returns "" when no
// clean controller→service import pair exists.
func routeSequenceMermaid(model *scan.Model, focus []string) string {
	if model == nil || len(model.Routes) == 0 || len(model.ImportEdges) == 0 {
		return ""
	}
	focusSet := mermaidFocusSet(focus)
	if len(focusSet) == 0 {
		return ""
	}
	routesByFile := map[string][]scan.Route{}
	for _, r := range model.Routes {
		f := filepath.ToSlash(r.File)
		if focusSet[f] {
			routesByFile[f] = append(routesByFile[f], r)
		}
	}
	if len(routesByFile) == 0 {
		return ""
	}
	// Pick the focus file (in focus order) with the most routes.
	ctrl := ""
	for _, p := range focus {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if len(routesByFile[p]) > len(routesByFile[ctrl]) {
			ctrl = p
		}
	}
	if ctrl == "" {
		return ""
	}
	// Service-like import target of the controller file.
	var candidates []string
	for _, e := range model.ImportEdges {
		if e.Kind == "same_package" || filepath.ToSlash(e.From) != ctrl {
			continue
		}
		to := filepath.ToSlash(e.To)
		base := strings.ToLower(filepath.Base(to))
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if strings.Contains(stem, "service") || strings.HasSuffix(stem, "svc") {
			candidates = append(candidates, to)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Strings(candidates)
	svc := candidates[0]

	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			switch r {
			case ';', '#', '\n', '\r':
				return -1
			}
			return r
		}, s)
	}
	var b strings.Builder
	b.WriteString("```mermaid\nsequenceDiagram\n")
	b.WriteString("  participant Client\n")
	b.WriteString(fmt.Sprintf("  participant Ctrl as %s\n", clean(filepath.Base(ctrl))))
	b.WriteString(fmt.Sprintf("  participant Svc as %s\n", clean(filepath.Base(svc))))
	n := 0
	for _, r := range routesByFile[ctrl] {
		b.WriteString(fmt.Sprintf("  Client->>Ctrl: %s %s\n", clean(r.Method), clean(r.Path)))
		n++
		if n >= 3 {
			break
		}
	}
	b.WriteString("  Ctrl->>Svc: delegate\n")
	b.WriteString("  Svc-->>Ctrl: result\n")
	b.WriteString("  Ctrl-->>Client: response\n")
	b.WriteString("```")
	return b.String()
}

// mermaidFocusSet normalizes focus paths into a ToSlash lookup set.
func mermaidFocusSet(focus []string) map[string]bool {
	set := map[string]bool{}
	for _, p := range focus {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p != "" {
			set[p] = true
		}
	}
	return set
}

// mermaidIdent keeps letters, digits and underscores, replacing every other
// rune with '_'. A leading digit is prefixed with '_'; empty input stays "".
func mermaidIdent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	out := sb.String()
	if out[0] >= '0' && out[0] <= '9' {
		out = "_" + out
	}
	return out
}

// erTypeToken compacts a source type into one erDiagram-safe token: generic
// parameters are cut, array/pointer markers dropped, and remaining characters
// restricted to identifier runes (erDiagram column types must not contain
// spaces or angle brackets).
func erTypeToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "<("); i >= 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, "[]", "")
	s = strings.TrimLeft(s, "*&")
	if i := strings.LastIndex(s, "."); i >= 0 {
		s = s[i+1:]
	}
	return mermaidIdent(s)
}

// classTypeToken renders a source type for classDiagram members: generics use
// mermaid tilde syntax (List<Long> → List~Long~), spaces/array brackets are
// removed, and any other non-identifier character is dropped.
func classTypeToken(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "[]", "")
	s = strings.ReplaceAll(s, "<", "~")
	s = strings.ReplaceAll(s, ">", "~")
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '_' || r == '~' || r == ',' || r == '.' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// typeMentions reports whether declared type expression typ contains name as
// a whole identifier word (List<Order> mentions Order; OrderItem does not
// mention Order).
func typeMentions(typ, name string) bool {
	if typ == "" || name == "" {
		return false
	}
	isWord := func(r byte) bool {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
	}
	for i := 0; i+len(name) <= len(typ); i++ {
		if typ[i:i+len(name)] != name {
			continue
		}
		beforeOK := i == 0 || !isWord(typ[i-1])
		afterOK := i+len(name) == len(typ) || !isWord(typ[i+len(name)])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

// lowerSnake converts CamelCase to lower snake_case (UserRole → user_role).
func lowerSnake(s string) string {
	var sb strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				sb.WriteRune('_')
			}
			sb.WriteRune(r - 'A' + 'a')
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func firstAlphaToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Prefer an ASCII identifier-ish token if present.
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-')
	}) {
		if len(part) >= 3 {
			return strings.ToLower(part)
		}
	}
	return ""
}

// repoArchitectureMermaid renders the repository-level architecture diagram
// (NAV-3): a `graph TD` over scan Modules with aggregated module-level
// dependency edges. Edges prefer scan's precomputed ModuleDeps; otherwise
// ImportEdges (kind "import") are aggregated by longest-prefix module
// matching. Self-edges dropped, duplicates merged, capped at 30 edges
// (weight desc, then endpoints asc — deterministic). When module paths span
// multiple top-level roots, nodes are grouped into per-root subgraphs.
// Returns "" when the model has fewer than 2 modules.
func repoArchitectureMermaid(model *scan.Model) string {
	if model == nil || len(model.Modules) < 2 {
		return ""
	}
	const maxNodes = 30
	const maxEdges = 30

	type node struct {
		path  string // normalized module path
		label string
		root  string // first path segment (subgraph group)
	}
	nodes := make([]node, 0, len(model.Modules))
	idxOf := map[string]int{}
	for _, m := range model.Modules {
		if len(nodes) >= maxNodes {
			break
		}
		p := strings.Trim(filepath.ToSlash(strings.TrimSpace(m.Path)), "/")
		if p == "" || p == "." {
			p = strings.TrimSpace(m.Name)
		}
		if p == "" {
			continue
		}
		if _, dup := idxOf[p]; dup {
			continue
		}
		label := strings.TrimSpace(m.Name)
		if label == "" {
			label = p
			if i := strings.LastIndex(p, "/"); i >= 0 {
				label = p[i+1:]
			}
		}
		root := p
		if i := strings.Index(p, "/"); i >= 0 {
			root = p[:i]
		}
		idxOf[p] = len(nodes)
		nodes = append(nodes, node{path: p, label: label, root: root})
	}
	if len(nodes) < 2 {
		return ""
	}

	// moduleIdx maps a repo file path to the longest-prefix module.
	prefixes := make([]string, 0, len(nodes))
	for _, n := range nodes {
		prefixes = append(prefixes, n.path)
	}
	sort.Slice(prefixes, func(a, b int) bool { return len(prefixes[a]) > len(prefixes[b]) })
	moduleIdx := func(file string) int {
		f := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(file)), "./")
		if f == "" {
			return -1
		}
		for _, p := range prefixes {
			if f == p || strings.HasPrefix(f, p+"/") {
				return idxOf[p]
			}
		}
		return -1
	}

	// Aggregate module-level edge weights.
	type edgeKey struct{ from, to int }
	weight := map[edgeKey]int{}
	if len(model.ModuleDeps) > 0 {
		for _, d := range model.ModuleDeps {
			a, b := moduleIdx(d.From), moduleIdx(d.To)
			if a < 0 || b < 0 || a == b {
				continue
			}
			w := d.Weight
			if w <= 0 {
				w = 1
			}
			weight[edgeKey{a, b}] += w
		}
	}
	if len(weight) == 0 {
		for _, e := range model.ImportEdges {
			if e.Kind == "same_package" {
				continue
			}
			a, b := moduleIdx(e.From), moduleIdx(e.To)
			if a < 0 || b < 0 || a == b {
				continue
			}
			weight[edgeKey{a, b}]++
		}
	}
	type edge struct {
		from, to, w int
	}
	edges := make([]edge, 0, len(weight))
	for k, w := range weight {
		edges = append(edges, edge{from: k.from, to: k.to, w: w})
	}
	sort.Slice(edges, func(a, b int) bool {
		if edges[a].w != edges[b].w {
			return edges[a].w > edges[b].w
		}
		if nodes[edges[a].from].path != nodes[edges[b].from].path {
			return nodes[edges[a].from].path < nodes[edges[b].from].path
		}
		return nodes[edges[a].to].path < nodes[edges[b].to].path
	})
	if len(edges) > maxEdges {
		edges = edges[:maxEdges]
	}

	// Group nodes by top-level root, preserving first-appearance order.
	var rootOrder []string
	byRoot := map[string][]int{}
	for i, n := range nodes {
		if _, ok := byRoot[n.root]; !ok {
			rootOrder = append(rootOrder, n.root)
		}
		byRoot[n.root] = append(byRoot[n.root], i)
	}

	var b strings.Builder
	b.WriteString("```mermaid\ngraph TD\n")
	if len(rootOrder) > 1 {
		for gi, root := range rootOrder {
			b.WriteString(fmt.Sprintf("  subgraph G%d[\"%s\"]\n", gi, escapeMermaid(root)))
			for _, i := range byRoot[root] {
				b.WriteString(fmt.Sprintf("    M%d[\"%s\"]\n", i, escapeMermaid(nodes[i].label)))
			}
			b.WriteString("  end\n")
		}
	} else {
		for i, n := range nodes {
			b.WriteString(fmt.Sprintf("  M%d[\"%s\"]\n", i, escapeMermaid(n.label)))
		}
	}
	for _, e := range edges {
		b.WriteString(fmt.Sprintf("  M%d --> M%d\n", e.from, e.to))
	}
	b.WriteString("```")
	return b.String()
}
