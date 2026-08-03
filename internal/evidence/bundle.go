package evidence

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Symon12138/wikify/internal/scan"
)

// Prompt-size caps for the bundle blocks rendered by PromptSection.
// Every block is omitted entirely when empty.
const (
	maxOutlineSymbolsPerFile = 5  // symbols rendered per Primary file outline
	maxRouteRows             = 15 // detected HTTP route rows
	maxConfigKeyRows         = 20 // config keys total across Primary files
	maxSectionPlanFiles      = 4  // files per suggested section topic
	maxReachChainHints       = 3  // entry-point call-path hints
)

// PageEvidenceBundle is the structured evidence package injected into the
// write-page prompt. It is product-agnostic: only title/goal tokens, path
// stems, import graph, and role heuristics — no domain lexicon.
type PageEvidenceBundle struct {
	// Primary is the pre-bound dependent_files list (already ranked).
	Primary []string `json:"primary,omitempty"`
	// Neighbors are 1-hop import graph neighbors of Primary (capped).
	Neighbors []string `json:"neighbors,omitempty"`
	// Entries are entry points that share path tokens with the page or Primary.
	Entries []string `json:"entries,omitempty"`
	// Hints are short, human-readable role/signature lines for the model.
	Hints []string `json:"hints,omitempty"`
	// Outlines maps Primary file path (ToSlash) → top-level declarations with
	// exact line numbers, capped per file (prefer func/method over types).
	Outlines map[string][]scan.Symbol `json:"outlines,omitempty"`
	// Routes are detected HTTP endpoints owned by Primary/Neighbor files.
	Routes []scan.Route `json:"routes,omitempty"`
	// ConfigKeys maps Primary config file path → top-level keys (capped total).
	ConfigKeys map[string][]string `json:"config_keys,omitempty"`
	// SectionPlan suggests generic H2 archetypes mapped to evidence subsets,
	// built with the same role classifier as the signature hints.
	SectionPlan []SectionEvidence `json:"section_plan,omitempty"`
}

// SectionEvidence maps one suggested generic section topic to the files that
// should ground it. Topics are archetype names (bilingual), never domain terms.
type SectionEvidence struct {
	Topic string   `json:"topic"`
	Files []string `json:"files,omitempty"`
}

// BuildPageEvidenceBundle assembles a prompt-ready evidence pack for one page.
// deps may be empty — then PickDependentFiles is used when model is non-nil.
// The optional trailing argument is the page track ("business" | "technical" |
// "foundation", see models.Track*) used to pick section-plan archetypes;
// omitted or unknown values fall back to the technical archetype set.
func BuildPageEvidenceBundle(model *scan.Model, title, goal string, deps []string, limit int, trackOpt ...string) PageEvidenceBundle {
	if limit <= 0 {
		limit = 8
	}
	track := ""
	if len(trackOpt) > 0 {
		track = strings.ToLower(strings.TrimSpace(trackOpt[0]))
	}
	b := PageEvidenceBundle{}
	if model == nil {
		b.Primary = unique(deps)
		if len(b.Primary) > limit {
			b.Primary = b.Primary[:limit]
		}
		return b
	}

	primary := unique(deps)
	if len(primary) == 0 {
		primary = PickDependentFiles(model, title, goal, limit)
	}
	if len(primary) > limit {
		primary = primary[:limit]
	}
	b.Primary = primary

	// 1-hop import neighbors of primary seeds.
	vd := NewVendorDetector(model, nil)
	vendorTopic := isVendorTopic(title + " " + goal)
	seen := map[string]bool{}
	for _, p := range primary {
		seen[filepath.ToSlash(p)] = true
	}
	var neighbors []string
	for _, seed := range primary {
		for _, nb := range model.ImportNeighbors(seed, 6) {
			p := filepath.ToSlash(nb)
			if seen[p] || scan.IsNoisePath(p) || isUniversalNoisePath(p) || (vd.IsVendor(p) && !vendorTopic) {
				continue
			}
			seen[p] = true
			neighbors = append(neighbors, p)
			if len(neighbors) >= limit {
				break
			}
		}
		if len(neighbors) >= limit {
			break
		}
	}
	b.Neighbors = neighbors

	// Entry points that share tokens with title/goal or primary paths.
	text := title + " " + goal + " " + strings.Join(primary, " ")
	tokens := tokenize(text)
	var entries []string
	var entryPaths []string
	for _, ep := range model.EntryPoints {
		p := filepath.ToSlash(ep.Path)
		if seen[p] || scan.IsNoisePath(p) || isUniversalNoisePath(p) {
			continue
		}
		if !pathSharesToken(p, tokens) && !pathSharesToken(p, tokenize(strings.Join(primary, " "))) {
			// Still allow entry if same package segment as a primary seed.
			if !sharesPackageSeg(p, primary) {
				continue
			}
		}
		label := p
		if ep.Kind != "" {
			label = fmt.Sprintf("%s (%s)", p, ep.Kind)
			if ep.Symbol != "" {
				label = fmt.Sprintf("%s (%s:%s)", p, ep.Kind, ep.Symbol)
			}
		}
		entries = append(entries, label)
		entryPaths = append(entryPaths, p)
		seen[p] = true
		if len(entries) >= 4 {
			break
		}
	}
	b.Entries = entries

	// Symbol outlines for Primary files (depth-3-symbol-outlines consumer):
	// exact declaration names + line numbers so the model can cite #L anchors
	// without a tool round-trip per file. Capped per file, callables first.
	if len(model.Symbols) > 0 {
		outlines := map[string][]scan.Symbol{}
		for _, p := range primary {
			sp := filepath.ToSlash(p)
			if syms := model.Symbols[sp]; len(syms) > 0 {
				outlines[sp] = topSymbols(syms, maxOutlineSymbolsPerFile)
			}
		}
		if len(outlines) > 0 {
			b.Outlines = outlines
		}
	}

	// Detected HTTP routes owned by Primary/Neighbor files (scan-routes consumer).
	if len(model.Routes) > 0 {
		member := map[string]bool{}
		for _, p := range primary {
			member[filepath.ToSlash(p)] = true
		}
		for _, n := range neighbors {
			member[filepath.ToSlash(n)] = true
		}
		var rts []scan.Route
		for _, rt := range model.Routes {
			if !member[filepath.ToSlash(rt.File)] {
				continue
			}
			rts = append(rts, rt)
			if len(rts) >= maxRouteRows {
				break
			}
		}
		if len(rts) > 0 {
			b.Routes = rts
		}
	}

	// Config keys of Primary config files (scan-config-keys consumer).
	if len(model.ConfigKeys) > 0 {
		total := 0
		cfg := map[string][]string{}
		for _, p := range primary {
			if total >= maxConfigKeyRows {
				break
			}
			sp := filepath.ToSlash(p)
			keys := model.ConfigKeys[sp]
			if len(keys) == 0 {
				continue
			}
			if total+len(keys) > maxConfigKeyRows {
				keys = keys[:maxConfigKeyRows-total]
			}
			cfg[sp] = keys
			total += len(keys)
		}
		if len(cfg) > 0 {
			b.ConfigKeys = cfg
		}
	}

	// Role / signature heuristics from basenames, enriched with declared
	// symbol names when the scan extracted them.
	b.Hints = buildSignatureHints(append(append([]string{}, primary...), neighbors...), model.Symbols)

	// Entry-to-focus reach chains (scan-reach-chains, bundle part): shortest
	// import-level path from an entry point into the Primary set, so the page
	// narrates the same call path its diagrams should draw. Only the first
	// maxReachChainHints entry points are probed (BFS is bounded anyway).
	if len(model.EntryPoints) > 0 && len(primary) > 0 {
		targets := map[string]bool{}
		for _, p := range primary {
			targets[filepath.ToSlash(p)] = true
		}
		seenChain := map[string]bool{}
		for i, ep := range model.EntryPoints {
			if i >= maxReachChainHints {
				break
			}
			chain := model.ReachChain(ep.Path, targets, 4)
			if len(chain) < 2 {
				continue
			}
			parts := make([]string, 0, len(chain))
			for _, c := range chain {
				parts = append(parts, filepath.Base(filepath.ToSlash(c)))
			}
			line := "call-path (import-level): " + strings.Join(parts, " → ")
			if seenChain[line] {
				continue
			}
			seenChain[line] = true
			b.Hints = append(b.Hints, line)
		}
	}

	// Per-section evidence routing (depth-1): generic archetypes → role-bucket
	// subsets. Empty buckets are omitted entirely.
	b.SectionPlan = buildSectionPlan(track, primary, neighbors, entryPaths)
	return b
}

// PromptSection renders the bundle as a user-prompt appendix.
// Empty bundles return "".
func (b PageEvidenceBundle) PromptSection() string {
	if len(b.Primary) == 0 && len(b.Neighbors) == 0 && len(b.Entries) == 0 && len(b.Hints) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n## Pre-bound Source Files\n")
	sb.WriteString("Read these first and cite with file://…#L ranges. Prefer Primary over Neighbors.\n")
	sb.WriteString("Do **not** invent modules, endpoints, or fields absent from these files.\n")
	sb.WriteString("Include **at least one sequenceDiagram** (or flowchart of the main call path) grounded in Primary/Neighbors when the page is a capability or API topic.\n")
	sb.WriteString("Under each Mermaid fence add **图表来源** with real file://#L cites from this list.\n\n")

	if len(b.Primary) > 0 {
		sb.WriteString("**Primary sources:**\n")
		for _, f := range b.Primary {
			sb.WriteString("- ")
			sb.WriteString(f)
			sb.WriteString("\n")
		}
	}
	if len(b.Outlines) > 0 {
		sb.WriteString("\n**Primary file outlines (symbol @ line — cite with #L):**\n")
		for _, f := range b.Primary {
			syms := b.Outlines[filepath.ToSlash(f)]
			if len(syms) == 0 {
				continue
			}
			parts := make([]string, 0, len(syms))
			for _, s := range syms {
				parts = append(parts, fmt.Sprintf("%s @ L%d", s.Name, s.Line))
			}
			sb.WriteString("- ")
			sb.WriteString(filepath.Base(filepath.ToSlash(f)))
			sb.WriteString(": ")
			sb.WriteString(strings.Join(parts, ", "))
			sb.WriteString("\n")
		}
	}
	if len(b.Neighbors) > 0 {
		sb.WriteString("\n**Import neighbors (1-hop, optional):**\n")
		for _, f := range b.Neighbors {
			sb.WriteString("- ")
			sb.WriteString(f)
			sb.WriteString("\n")
		}
	}
	if len(b.Entries) > 0 {
		sb.WriteString("\n**Related entry points:**\n")
		for _, f := range b.Entries {
			sb.WriteString("- ")
			sb.WriteString(f)
			sb.WriteString("\n")
		}
	}
	if len(b.Routes) > 0 {
		sb.WriteString("\n**Detected HTTP routes (ground endpoint tables in these):**\n")
		for _, rt := range b.Routes {
			sb.WriteString("- ")
			sb.WriteString(rt.Method)
			sb.WriteString(" ")
			sb.WriteString(rt.Path)
			sb.WriteString(" — ")
			sb.WriteString(rt.File)
			sb.WriteString("\n")
		}
	}
	if len(b.ConfigKeys) > 0 {
		sb.WriteString("\n**Config keys detected (top-level, non-exhaustive):**\n")
		for _, f := range b.Primary {
			keys := b.ConfigKeys[filepath.ToSlash(f)]
			if len(keys) == 0 {
				continue
			}
			sb.WriteString("- ")
			sb.WriteString(filepath.ToSlash(f))
			sb.WriteString(": ")
			sb.WriteString(strings.Join(keys, ", "))
			sb.WriteString("\n")
		}
	}
	if len(b.SectionPlan) > 0 {
		sb.WriteString("\n**Per-section evidence routing (suggested):**\n")
		sb.WriteString("Suggested H2 topics with the files to read and cite for each; adapt topic wording to the page language.\n")
		for _, se := range b.SectionPlan {
			sb.WriteString("- ")
			sb.WriteString(se.Topic)
			sb.WriteString(" → ")
			sb.WriteString(strings.Join(se.Files, ", "))
			sb.WriteString("\n")
		}
	}
	if len(b.Hints) > 0 {
		sb.WriteString("\n**Role / signature hints:**\n")
		for _, h := range b.Hints {
			sb.WriteString("- ")
			sb.WriteString(h)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// SoftVerifyPageBody returns soft quality issues for a generated page body.
// Empty slice means no obvious structural gaps. Does not hard-fail generation.
func SoftVerifyPageBody(body string) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return []string{"empty body"}
	}
	var issues []string
	lower := strings.ToLower(body)
	if !strings.Contains(body, "file://") && !strings.Contains(body, "<cite") {
		issues = append(issues, "missing file:// or <cite> citations")
	}
	if !strings.Contains(body, "## 目录") && !strings.Contains(lower, "table of contents") {
		issues = append(issues, "missing TOC (## 目录)")
	}
	mermaidN := strings.Count(lower, "```mermaid")
	if mermaidN < 2 {
		issues = append(issues, fmt.Sprintf("mermaid count %d < 2", mermaidN))
	}
	if mermaidN > 0 && !strings.Contains(lower, "sequencediagram") && !strings.Contains(lower, "sequence") {
		// Soft: capability pages benefit from sequence; not a hard requirement for pure reference.
		if strings.Count(body, "\n## ") >= 3 {
			issues = append(issues, "no sequenceDiagram detected (recommended for process pages)")
		}
	}
	if mermaidN > 0 && !strings.Contains(body, "图表来源") && !strings.Contains(lower, "figure sources") && !strings.Contains(lower, "figure source") {
		issues = append(issues, "mermaid present but missing 图表来源 / Figure sources")
	}
	return issues
}

// roleOfPath classifies a path into a generic architectural role. It is the
// single classifier shared by buildSignatureHints and buildSectionPlan —
// structural patterns only, no product vocabulary.
func roleOfPath(p string) (key, label string) {
	lower := strings.ToLower(filepath.ToSlash(p))
	switch {
	// "resource($|[^s])" hits JAX-RS resource classes without swallowing the
	// ubiquitous src/main/resources/ directory.
	case matchAny(lower, `controller|control|/api/|handler|servlet|resource($|[^s])`):
		return "ctrl", "HTTP/API entry"
	case matchAny(lower, `/service/|serviceimpl|service\.java$|/svc/`):
		return "svc", "service / business logic"
	case matchAny(lower, `/dao/|/mapper/|repository|/repo/`):
		return "dao", "persistence"
	case matchAny(lower, `/po/|/entity/|/domain/|/model/|dto|vo\.java$`):
		return "ent", "data model"
	case matchAny(lower, `config|application\.(yml|yaml|properties)`):
		return "config", "configuration"
	case matchAny(lower, `filter|interceptor|security|auth`):
		return "security", "security / cross-cutting"
	case matchAny(lower, `_test\.|test/|spec\.|/tests/`):
		return "test", "test"
	}
	return "", ""
}

// buildSignatureHints renders one role line per recognized path. Files whose
// symbols were extracted list up to maxOutlineSymbolsPerFile real declaration
// names; only files without symbols keep the "heuristic" disclaimer.
func buildSignatureHints(paths []string, symbols map[string][]scan.Symbol) []string {
	var hints []string
	seen := map[string]bool{}
	for _, p := range paths {
		p = filepath.ToSlash(p)
		base := filepath.Base(p)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		_, role := roleOfPath(p)
		if role == "" {
			continue
		}
		key := role + "|" + stem
		if seen[key] {
			continue
		}
		seen[key] = true
		if syms := topSymbols(symbols[p], maxOutlineSymbolsPerFile); len(syms) > 0 {
			names := make([]string, 0, len(syms))
			for _, s := range syms {
				names = append(names, s.Name)
			}
			hints = append(hints, fmt.Sprintf("%s — %s (%s) (`%s`)", stem, role, strings.Join(names, ", "), p))
		} else {
			hints = append(hints, fmt.Sprintf("%s — %s (`%s`) — heuristic, verify in source", stem, role, p))
		}
		if len(hints) >= 10 {
			break
		}
	}
	return hints
}

// topSymbols selects up to max symbols for prompt rendering. When trimming is
// required, callable declarations (func/method) are preferred over types so
// the model gets citable behavior anchors first. Order is otherwise stable.
func topSymbols(syms []scan.Symbol, max int) []scan.Symbol {
	if len(syms) == 0 || max <= 0 {
		return nil
	}
	if len(syms) <= max {
		return syms
	}
	out := make([]scan.Symbol, 0, max)
	for _, s := range syms {
		if s.Kind == "func" || s.Kind == "method" {
			out = append(out, s)
			if len(out) >= max {
				return out
			}
		}
	}
	for _, s := range syms {
		if s.Kind == "func" || s.Kind == "method" {
			continue
		}
		out = append(out, s)
		if len(out) >= max {
			return out
		}
	}
	return out
}

// buildSectionPlan maps generic section archetypes to evidence subsets using
// the same role classifier as buildSignatureHints — no new heuristics, no
// domain vocabulary. Empty buckets are omitted entirely. Topics are bilingual
// archetype names (zh / en) matching the bundle's existing mixed-register style.
func buildSectionPlan(track string, primary, neighbors, entryPaths []string) []SectionEvidence {
	roles := map[string][]string{}
	seen := map[string]bool{}
	classify := func(paths []string) {
		for _, p := range paths {
			sp := filepath.ToSlash(p)
			if seen[sp] {
				continue
			}
			seen[sp] = true
			key, _ := roleOfPath(sp)
			if key == "" || key == "test" {
				continue
			}
			roles[key] = append(roles[key], sp)
		}
	}
	classify(primary)
	classify(neighbors)

	merge := func(limit int, lists ...[]string) []string {
		var out []string
		dup := map[string]bool{}
		for _, l := range lists {
			for _, p := range l {
				sp := filepath.ToSlash(p)
				if sp == "" || dup[sp] {
					continue
				}
				dup[sp] = true
				out = append(out, sp)
				if len(out) >= limit {
					return out
				}
			}
		}
		return out
	}

	var plan []SectionEvidence
	add := func(topic string, files []string) {
		if len(files) == 0 {
			return
		}
		plan = append(plan, SectionEvidence{Topic: topic, Files: files})
	}
	if track == "business" { // models.TrackBusiness
		add("主流程 / Main process", merge(maxSectionPlanFiles, roles["ctrl"], roles["svc"]))
		add("业务规则 / Business rules", merge(maxSectionPlanFiles, roles["svc"]))
		add("数据对象 / Data objects", merge(maxSectionPlanFiles, roles["ent"]))
		add("实现落点 / Implementation anchors", merge(len(primary), primary))
	} else { // technical / foundation → technical archetypes
		add("入口与API面 / Entry & API surface", merge(maxSectionPlanFiles, roles["ctrl"], entryPaths))
		add("核心逻辑与主流程 / Core logic & main flow", merge(maxSectionPlanFiles, roles["svc"]))
		add("数据模型 / Data model", merge(maxSectionPlanFiles, roles["ent"], roles["dao"]))
		add("配置 / Configuration", merge(maxSectionPlanFiles, roles["config"]))
		add("横切关注点 / Cross-cutting concerns", merge(maxSectionPlanFiles, roles["security"]))
	}
	return plan
}

func pathSharesToken(path string, tokens []string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	camel := strings.ToLower(strings.Join(splitIdent(stem), " "))
	for _, tok := range tokens {
		t := strings.ToLower(tok)
		if len(t) < 2 {
			continue
		}
		if strings.Contains(lower, t) || strings.Contains(stem, t) || strings.Contains(camel, t) {
			return true
		}
	}
	return false
}

func sharesPackageSeg(path string, seeds []string) bool {
	seg := packageSeg(path)
	if seg == "" {
		return false
	}
	for _, s := range seeds {
		if packageSeg(s) == seg {
			return true
		}
	}
	return false
}
