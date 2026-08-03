package agent

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strings"

	openai "github.com/sashabaranov/go-openai"
	"github.com/JSHurt/wikify/internal/evidence"
	"github.com/JSHurt/wikify/internal/models"
	"github.com/JSHurt/wikify/internal/prompts"
	"github.com/JSHurt/wikify/internal/scan"
	"github.com/JSHurt/wikify/internal/tools"
)

var blogPattern = regexp.MustCompile(`(?s)<blog>(.*?)</blog>`)

// RunPage generates markdown documentation for a single wiki page.
// Returns the extracted blog content (without <blog> tags).
//
// Optional trailing args (backward compatible with existing call sites):
//   - *scan.Model as the first optional arg enables graph-aware PageEvidenceBundle.
func RunPage(
	ctx context.Context,
	client *openai.Client,
	model string,
	workDir string,
	language string,
	page *models.WikiPage,
	wiki *models.Wiki,
	verbose bool,
	onToolCall func(name, args, result string),
	onStatus func(status string),
	extra ...any,
) (string, error) {
	var repoModel *scan.Model
	for _, e := range extra {
		if m, ok := e.(*scan.Model); ok && m != nil {
			repoModel = m
		}
	}

	osName := "linux"
	if runtime.GOOS == "windows" {
		osName = "windows"
	} else if runtime.GOOS == "darwin" {
		osName = "macos"
	}

	ts := tools.New(workDir)
	structure := ts.Handle["get_dir_structure"](map[string]any{"dir_path": ".", "max_depth": float64(2)})
	catalog := wiki.FormatCatalog(page.Slug)

	systemPrompt := fmt.Sprintf(prompts.PageSystem, workDir, osName)
	userPrompt := fmt.Sprintf(prompts.PageUser,
		workDir, osName,
		page.Title, page.Level, language,
		structure, catalog,
		page.Title, page.Title, page.Title,
	)

	track := page.Track
	if track == "" {
		track = models.InferTrack(*page)
	}

	// Structured evidence bundle (Primary + import neighbors + entry hints +
	// symbol outlines / routes / config keys / per-section routing).
	// Built outside the prompt block so Pass B repair and Pass C enrich reuse it.
	bundle := evidence.BuildPageEvidenceBundle(repoModel, page.Title, page.Goal, page.DependentFiles, 8, track)
	if len(bundle.Primary) == 0 && len(page.DependentFiles) > 0 {
		bundle.Primary = append([]string{}, page.DependentFiles...)
	}
	if len(bundle.Primary) > 0 {
		page.DependentFiles = append([]string{}, bundle.Primary...)
	}

	// Inject track guidance, cross-rail links, and evidence binding.
	{
		var b strings.Builder
		b.WriteString("\n\n## Documentation track\n")
		b.WriteString("This page belongs to the **")
		b.WriteString(track)
		b.WriteString("** rail.\n")
		switch track {
		case models.TrackBusiness:
			b.WriteString(`Write primarily for domain / product / BA readers:
- Open with capability purpose, actors, and business rules.
- Document the main process with Mermaid sequenceDiagram or flowchart; keep technical depth as anchors, not the spine.
- End with section "实现落点" (or "Implementation anchors"): key packages/classes and links to related technical wiki pages.
- Every material claim must cite a pre-bound Primary/Neighbor file with file://…#L.
`)
		case models.TrackFoundation:
			b.WriteString(`Write primarily for first-time readers:
- State purpose, audience, and system snapshot.
- Provide two reading paths with catalog links: 业务能力路径 and 技术参考路径.
`)
		default:
			b.WriteString(`Write primarily for engineers:
- Open with "所属业务" (or "Owning capabilities") linking related business wiki pages when present.
- Focus on architecture, contracts, data shapes, configuration, and operability.
- Prefer sequenceDiagram for request/call paths when Primary sources include controller/service/handler.
`)
		}
		if page.Goal != "" {
			b.WriteString("\n**Documentation objective:** ")
			b.WriteString(page.Goal)
			b.WriteString("\n")
		}
		if wiki != nil {
			if related := wiki.RelatedCrossTrack(*page, 6); len(related) > 0 {
				b.WriteString("\n**Cross-rail related pages (must link when relevant):**\n")
				for _, r := range related {
					b.WriteString("- [")
					b.WriteString(r.Title)
					b.WriteString("](")
					b.WriteString(relatedLinkTarget(r))
					b.WriteString(") — track=")
					b.WriteString(r.Track)
					if r.Section != "" {
						b.WriteString(", section=")
						b.WriteString(r.Section)
					}
					b.WriteString("\n")
				}
			}
		}

		if sec := bundle.PromptSection(); sec != "" {
			b.WriteString(sec)
		} else if len(page.DependentFiles) > 0 {
			b.WriteString("\n## Pre-bound Source Files\n")
			b.WriteString("Read these first and cite with file://…#L ranges. Additional files only when necessary.\n\n**Primary sources:**\n")
			for _, f := range page.DependentFiles {
				b.WriteString("- ")
				b.WriteString(f)
				b.WriteString("\n")
			}
		}

		b.WriteString("\n## Hard quality constraints\n")
		b.WriteString("- Cite only paths that appear in Pre-bound sources or that you actually opened with tools.\n")
		b.WriteString("- Do not invent APIs, tables, or modules. If evidence is thin, state the gap explicitly.\n")
		b.WriteString("- For capability/API pages: include at least one sequenceDiagram (or flowchart of the main path) with 图表来源.\n")
		b.WriteString("- Under every Mermaid fence: **图表来源** with real file://#L cites.\n")
		b.WriteString("- Required chrome: H1, optional <cite>, ## 目录, ≥2 Mermaid when the topic is non-trivial.\n")
		b.WriteString("\n## Writing procedure (single response)\n")
		b.WriteString("1. Outline H2 sections (for TOC).\n")
		b.WriteString("2. Ground claims in Primary/Neighbor files (tool-read if needed).\n")
		b.WriteString("3. Write full formal page with cite, TOC, ≥2 Mermaid (prefer sequence+structure).\n")
		b.WriteString("4. Self-check: every Mermaid has 图表来源; no invented paths.\n")

		userPrompt += b.String()
	}

	// Multi-pass write: Pass A = full draft; Pass B = repair only when soft-verify
	// finds structural gaps (missing cite/TOC/mermaid/sequence). Pass B is a
	// single cheap rewrite without tools, using the draft + issue list.
	if onStatus != nil {
		onStatus("[pass-a: draft]")
	}
	raw, err := Run(ctx, Config{
		Client:       client,
		Model:        model,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		Tools:        ts.Tools,
		Handlers:     ts.Handle,
		Verbose:      verbose,
		OnToolCall:   onToolCall,
		OnStatus:     onStatus,
	})
	if err != nil {
		return "", err
	}
	body := extractBlog(raw)
	issues := evidence.SoftVerifyPageBody(body)
	// QL-5: fold deterministic invalid-cite findings into the same issue list
	// so the repair prompt sees the concrete bad→good path pairs under
	// "## Structural issues to fix". Skipped without a repo model.
	if repoModel != nil {
		issues = append(issues, evidence.FindInvalidCitePaths(body, repoModel.Files)...)
	}
	if len(issues) == 0 || shouldSkipRepairPass(page, body, issues) {
		// Pass B skipped. Pass C (depth enrichment) runs only when soft-verify
		// was completely clean but the page still scores shallow — see
		// maybeRunEnrichPass for the exact gating and accept rules.
		if len(issues) == 0 {
			return maybeRunEnrichPass(ctx, client, model, workDir, language, page, body, bundle, verbose, onToolCall, onStatus), nil
		}
		return body, nil
	}
	if onStatus != nil {
		onStatus("[pass-b: repair]")
	}
	repaired, rerr := runPageRepairPass(ctx, client, model, workDir, language, page, body, issues, bundle, verbose, onToolCall, onStatus)
	if rerr != nil {
		// Keep Pass A body on repair failure — never discard a substantial draft.
		if verbose {
			fmt.Printf("  \033[33m⚠ pass-b repair skipped: %v\033[0m\n", rerr)
		}
		return body, nil
	}
	if strings.TrimSpace(repaired) == "" {
		return body, nil
	}
	// Prefer repair only when it improves soft-verify (or keeps body substantial).
	newIssues := evidence.SoftVerifyPageBody(repaired)
	if len(newIssues) <= len(issues) && len([]rune(repaired)) >= len([]rune(body))*3/4 {
		return repaired, nil
	}
	return body, nil
}

// invalidCitePrefix marks the deterministic QL-5 findings appended by
// evidence.FindInvalidCitePaths.
const invalidCitePrefix = "cited path does not exist in repo:"

// shouldSkipRepairPass avoids a second LLM call for stubs / foundation overviews
// that intentionally stay short, or when the only issue is a soft sequence hint
// on non-capability pages.
//
// QL-5 hard rule: two or more nonexistent cite paths always warrant the repair
// turn (never skipped, even for thin bodies) — the bad→good pairs are already
// in the issue list. A lone invalid cite is treated as soft: the export-layer
// validator heals single typos more cheaply than an extra LLM call.
func shouldSkipRepairPass(page *models.WikiPage, body string, issues []string) bool {
	if page == nil || len(issues) == 0 {
		return true
	}
	invalidCites := 0
	for _, iss := range issues {
		if strings.HasPrefix(iss, invalidCitePrefix) {
			invalidCites++
		}
	}
	if invalidCites >= 2 {
		return false
	}
	// Thin bodies: let runner retry whole page instead of polish-repair.
	if len([]rune(body)) < 400 {
		return true
	}
	hard := 0
	for _, iss := range issues {
		if strings.HasPrefix(iss, invalidCitePrefix) {
			// Below the ≥2 threshold — soft, left to the export validator.
			continue
		}
		if strings.Contains(iss, "sequenceDiagram") {
			// sequence is soft for foundation pages
			track := page.Track
			if track == "" {
				track = models.InferTrack(*page)
			}
			if track == models.TrackFoundation {
				continue
			}
		}
		hard++
	}
	return hard == 0
}

func runPageRepairPass(
	ctx context.Context,
	client *openai.Client,
	model string,
	workDir string,
	language string,
	page *models.WikiPage,
	draft string,
	issues []string,
	bundle evidence.PageEvidenceBundle,
	verbose bool,
	onToolCall func(name, args, result string),
	onStatus func(status string),
) (string, error) {
	// Cap draft size so repair stays cheap.
	draftForPrompt := draft
	if runes := []rune(draftForPrompt); len(runes) > 12000 {
		draftForPrompt = string(runes[:12000]) + "\n\n…[truncated for repair pass]…"
	}
	var issueLines strings.Builder
	for _, iss := range issues {
		issueLines.WriteString("- ")
		issueLines.WriteString(iss)
		issueLines.WriteString("\n")
	}
	primary := strings.Join(bundle.Primary, "\n- ")
	if primary != "" {
		primary = "- " + primary
	}
	sys := `You are a technical documentation editor. Repair an existing wiki page draft.
Rules:
- Keep factual content; do not invent modules, APIs, or file paths.
- Output ONLY the repaired full markdown inside <blog>…</blog>.
- Preserve language of the draft.
- Fix every listed structural issue.
- Citations must use file://relative/path#Lstart-Lend and only paths from the allowed list or already present in the draft.
- Mermaid: keep real node names from sources; add sequenceDiagram when required; under each fence add **图表来源**.`
	user := fmt.Sprintf(`## Page
Title: %s
Language: %s
WorkDir: %s

## Structural issues to fix
%s
## Allowed primary source paths
%s

## Draft to repair
%s

Return the full repaired page in <blog>…</blog>.`,
		page.Title, language, workDir, issueLines.String(), primary, draftForPrompt)

	raw, err := Run(ctx, Config{
		Client:       client,
		Model:        model,
		SystemPrompt: sys,
		UserPrompt:   user,
		// No tools on repair pass — edit only.
		Verbose:    verbose,
		OnToolCall: onToolCall,
		OnStatus:   onStatus,
	})
	if err != nil {
		return "", err
	}
	return extractBlog(raw), nil
}

// maybeRunEnrichPass runs the Pass C depth enrichment and returns the body to
// publish (enriched or original). Gating — ALL of:
//  1. Soft-verify was clean, i.e. Pass B was skipped with zero issues
//     (enforced by the caller, which only calls this when len(issues) == 0);
//  2. evidence.DepthScore(draft).Shallow() — too few H2s, too few distinct
//     cited paths, or no 章节来源 blocks;
//  3. len(bundle.Primary) >= 3 — enough evidence exists to deepen the page.
//
// Accept rule (mirrors Pass B): the enriched body is kept only when
//  a. DepthScore strictly improves on at least one previously failing axis;
//  b. rune length does not shrink below 3/4 of the draft;
//  c. soft-verify stays clean (the draft was clean — enrichment must not
//     introduce structural regressions).
func maybeRunEnrichPass(
	ctx context.Context,
	client *openai.Client,
	model string,
	workDir string,
	language string,
	page *models.WikiPage,
	body string,
	bundle evidence.PageEvidenceBundle,
	verbose bool,
	onToolCall func(name, args, result string),
	onStatus func(status string),
) string {
	report := evidence.DepthScore(body)
	if !report.Shallow() || len(bundle.Primary) < 3 {
		return body
	}
	if onStatus != nil {
		onStatus("[pass-c: enrich]")
	}
	enriched, err := runPageEnrichPass(ctx, client, model, workDir, language, page, body, report, bundle, verbose, onToolCall, onStatus)
	if err != nil {
		// Keep the Pass A body on enrich failure — same posture as Pass B.
		if verbose {
			fmt.Printf("  \033[33m⚠ pass-c enrich skipped: %v\033[0m\n", err)
		}
		return body
	}
	if strings.TrimSpace(enriched) == "" {
		return body
	}
	newReport := evidence.DepthScore(enriched)
	if !depthImproved(report, newReport) ||
		len([]rune(enriched)) < len([]rune(body))*3/4 ||
		len(evidence.SoftVerifyPageBody(enriched)) > 0 {
		if verbose {
			fmt.Printf("  \033[33m⚠ pass-c enrich rejected (no depth improvement): before[%s] after[%s]\033[0m\n",
				report.Summary(), newReport.Summary())
		}
		return body
	}
	if verbose {
		fmt.Printf("  \033[32m✓ pass-c enrich accepted: before[%s] after[%s]\033[0m\n",
			report.Summary(), newReport.Summary())
	}
	return enriched
}

// depthImproved reports whether next strictly improves prev on at least one
// axis that made prev shallow (the Shallow() axes: Sections, DistinctCitePaths,
// SectionSourceBlocks).
func depthImproved(prev, next evidence.DepthReport) bool {
	if prev.Sections < evidence.ShallowMinSections && next.Sections > prev.Sections {
		return true
	}
	if prev.DistinctCitePaths < evidence.ShallowMinDistinctCitePaths && next.DistinctCitePaths > prev.DistinctCitePaths {
		return true
	}
	if prev.SectionSourceBlocks < evidence.ShallowMinSectionSources && next.SectionSourceBlocks > prev.SectionSourceBlocks {
		return true
	}
	return false
}

// runPageEnrichPass is the Pass C single tool-less rewrite: expand thin H2
// sections against Primary sources/outlines. Same machinery as the repair
// pass (capped draft, no tools, <blog> extraction) with the prompts.PageEnrich
// template.
func runPageEnrichPass(
	ctx context.Context,
	client *openai.Client,
	model string,
	workDir string,
	language string,
	page *models.WikiPage,
	draft string,
	report evidence.DepthReport,
	bundle evidence.PageEvidenceBundle,
	verbose bool,
	onToolCall func(name, args, result string),
	onStatus func(status string),
) (string, error) {
	draftForPrompt := draft
	if runes := []rune(draftForPrompt); len(runes) > 12000 {
		draftForPrompt = string(runes[:12000]) + "\n\n…[truncated for enrich pass]…"
	}
	var thin strings.Builder
	for _, t := range evidence.ThinSections(draft) {
		thin.WriteString("- ")
		thin.WriteString(t)
		thin.WriteString("\n")
	}
	if thin.Len() == 0 {
		thin.WriteString("- (no single section flagged — deepen the weakest H2 sections)\n")
	}
	var ev strings.Builder
	for _, p := range bundle.Primary {
		ev.WriteString("- ")
		ev.WriteString(p)
		ev.WriteString("\n")
		if syms := bundle.Outlines[p]; len(syms) > 0 {
			parts := make([]string, 0, len(syms))
			for _, s := range syms {
				parts = append(parts, fmt.Sprintf("%s @ L%d", s.Name, s.Line))
			}
			ev.WriteString("  - outline: ")
			ev.WriteString(strings.Join(parts, ", "))
			ev.WriteString("\n")
		}
	}
	sys := `You are a technical documentation editor. Deepen an existing wiki page draft that passed structural checks but remains shallow.
Rules:
- Keep factual content; do not invent modules, APIs, or file paths.
- Output ONLY the enriched full markdown inside <blog>…</blog>.
- Preserve the language of the draft.
- Citations must use file://relative/path#Lstart-Lend and only paths from the listed sources or already present in the draft.
- Mermaid: keep existing diagrams; under each fence keep or add **图表来源**.`
	user := fmt.Sprintf(prompts.PageEnrich,
		page.Title, language, workDir,
		report.Summary(), thin.String(), ev.String(), draftForPrompt)

	raw, err := Run(ctx, Config{
		Client:       client,
		Model:        model,
		SystemPrompt: sys,
		UserPrompt:   user,
		// No tools on enrich pass — edit only, same as repair.
		Verbose:    verbose,
		OnToolCall: onToolCall,
		OnStatus:   onStatus,
	})
	if err != nil {
		return "", err
	}
	return extractBlog(raw), nil
}

func extractBlog(text string) string {
	if m := blogPattern.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(text)
}

// relatedLinkTarget prefers ContentPath so LLM prompts do not emit bare "50-50" slugs.
func relatedLinkTarget(p models.WikiPage) string {
	if cp := strings.TrimSpace(p.ContentPath); cp != "" {
		cp = strings.TrimPrefix(cp, "content/")
		return strings.ReplaceAll(cp, "\\", "/")
	}
	if t := strings.TrimSpace(p.Title); t != "" {
		return t + ".md"
	}
	if p.Slug != "" {
		return p.Slug
	}
	return "page.md"
}
