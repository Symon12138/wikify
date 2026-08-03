// Package runner orchestrates the two-phase documentation pipeline.
package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	openai "github.com/sashabaranov/go-openai"
	"github.com/JSHurt/wikify/internal/agent"
	"github.com/JSHurt/wikify/internal/evidence"
	wikiout "github.com/JSHurt/wikify/internal/export"
	"github.com/JSHurt/wikify/internal/models"
	"github.com/JSHurt/wikify/internal/planner"
	"github.com/JSHurt/wikify/internal/scan"
	"github.com/JSHurt/wikify/internal/tui"
	"github.com/JSHurt/wikify/internal/wikiplan"
)

// Config holds all settings for a single documentation run.
type Config struct {
	APIKey  string
	BaseURL string
	Model   string

	WorkDir  string
	Language string
	Workers  int

	OutputDir string

	Draft      string // "resume" | "clear" | "cancel" | "" (auto-detect)
	SkipFailed bool
	AutoYes    bool // -y flag: skip interactive prompts

	// OnlyStubs seeds drafts from published .wikify content and regenerates
	// only pages that still look like honest stubs (待补充 / Status: stub).
	// Skips catalog re-planning so substantial pages are preserved.
	OnlyStubs bool

	// EnrichShallow additionally regenerates substantial pages whose depth
	// profile is shallow (evidence.DepthScore: few H2 sections, few distinct
	// cited files, or no section-source blocks). Works alone or combined with
	// OnlyStubs; both reuse the published wiki.json and skip catalog planning.
	EnrichShallow bool

	VerboseCatalog bool
	VerbosePages   bool
	MaxRetries     int

	// Multi-level generation controls (single default path).
	MaxPages int    // catalog size cap; <=0 auto-scales with repo size after scan
	LangDir  string // zh | en for final content (default from scan)

	// GraphFile optional external code-graph JSON overlay for scan.
	GraphFile string
}

// draftsDir returns the path to the in-progress drafts directory.
func draftsDir(workDir string) string {
	return filepath.Join(workDir, ".wikify", "drafts")
}

// finalWikiRoot is the single deliverable directory.
func finalWikiRoot(workDir string) string {
	return filepath.Join(workDir, ".wikify")
}

// hasDraft returns true when wiki.json exists in drafts.
func hasDraft(workDir string) bool {
	_, err := os.Stat(filepath.Join(draftsDir(workDir), "wiki.json"))
	return err == nil
}

// Run executes the full documentation generation pipeline.
func Run(cfg Config) error {
	if cfg.APIKey == "" {
		return fmt.Errorf("API key not configured, run: wikify config")
	}
	// MaxPages <= 0 is resolved in buildCatalog once the repo has been scanned
	// (planner.SuggestMaxPages scales the budget with repo size). Paths that
	// never scan (resume/only-stubs) do not consume MaxPages; planner/agent keep
	// their own 120 safety nets.

	oaiCfg := openai.DefaultConfig(cfg.APIKey)
	oaiCfg.BaseURL = cfg.BaseURL
	// Long wiki turns (streamed) need generous overall timeout; keep TLS/dial tight.
	// Cloudflare 524 is a gateway limit (~100s idle) — streaming keeps the pipe warm.
	oaiCfg.HTTPClient = newLLMHTTPClient()
	client := openai.NewClientWithConfig(oaiCfg)

	outPath := cfg.OutputDir
	if outPath == "" {
		outPath = draftsDir(cfg.WorkDir)
	}

	// ── Only-stubs / enrich-shallow: seed drafts from published content ──
	if cfg.OnlyStubs || cfg.EnrichShallow {
		if cfg.Draft == "cancel" {
			fmt.Println("Generation cancelled.")
			return nil
		}
		if err := os.MkdirAll(outPath, 0o755); err != nil {
			return fmt.Errorf("cannot create output dir: %w", err)
		}
		kept, stubs, shallow, err := seedDraftsFromPublished(cfg.WorkDir, outPath, cfg.EnrichShallow)
		if err != nil {
			return err
		}
		mode := "Only-stubs"
		if cfg.EnrichShallow && !cfg.OnlyStubs {
			mode = "Enrich-shallow"
		}
		if stubs+shallow == 0 {
			fmt.Printf("%s: nothing to regenerate under .wikify/content (kept %d substantial, 0 stubs, 0 shallow).\n", mode, kept)
			return nil
		}
		fmt.Printf("%s: kept %d substantial page(s), will regenerate %d stub(s), %d shallow\n", mode, kept, stubs, shallow)
		action := "resume"
		useTUI := !cfg.VerboseCatalog && !cfg.VerbosePages
		if useTUI {
			return tuiRun(client, cfg, action, outPath)
		}
		return plainRun(client, cfg, action, outPath)
	}

	// ── Draft state detection & interactive prompt ──────────────────────
	action := cfg.Draft
	if hasDraft(cfg.WorkDir) && action == "" {
		if cfg.AutoYes {
			action = "resume"
			fmt.Println("Draft found, resuming (-y)")
		} else {
			var err error
			action, err = promptDraftAction(cfg.WorkDir)
			if err != nil {
				return err
			}
		}
	}

	switch action {
	case "cancel":
		fmt.Println("Generation cancelled.")
		return nil
	case "clear":
		fmt.Printf("Clearing draft: %s\n", outPath)
		if err := clearDir(outPath); err != nil {
			return err
		}
		action = ""
	}

	if err := os.MkdirAll(outPath, 0o755); err != nil {
		return fmt.Errorf("cannot create output dir: %w", err)
	}

	// Choose TUI or plain output
	useTUI := !cfg.VerboseCatalog && !cfg.VerbosePages
	if useTUI {
		return tuiRun(client, cfg, action, outPath)
	}
	return plainRun(client, cfg, action, outPath)
}

// buildCatalog: model freely plans the catalog; inventory is optional hint only.
// Paths/Parent come from the model hierarchy (section/group/title), not a fixed seed.
func buildCatalog(ctx context.Context, client *openai.Client, cfg Config, verboseFn func(string, string, string), onStatus func(string)) (*models.Wiki, *scan.Model, error) {
	repoModel, err := scan.Scan(cfg.WorkDir, cfg.Language, scan.Options{GraphFile: cfg.GraphFile})
	if err != nil {
		// non-fatal: continue without inventory
		repoModel = &scan.Model{Root: cfg.WorkDir, Name: filepath.Base(cfg.WorkDir), Language: "zh"}
	}
	_, _ = wikiplan.Ensure(cfg.WorkDir)
	plan, _ := wikiplan.Read(cfg.WorkDir)
	// Apply wiki_plan scope to inventory so planner/evidence only see allowed paths.
	if plan != nil && plan.HasScope() {
		repoModel.ApplyScope(plan.ScopeInclude(), plan.ScopeExclude())
	}
	// Resolve the page budget from the scoped inventory when not set explicitly.
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = planner.SuggestMaxPages(repoModel)
		fmt.Printf("  catalog: max-pages auto-scaled to %d (repo size)\n", cfg.MaxPages)
	}

	if onStatus != nil {
		onStatus("planning document tree…")
	}

	// Explicit wiki_plan documents allowlist still wins (user-authored constraint).
	if plan != nil && len(plan.Documents()) > 0 {
		wiki := planner.Build(repoModel, plan, planner.Options{MaxPages: cfg.MaxPages})
		planner.ApplyHierarchyPaths(wiki)
		planner.EnsureTracks(wiki)
		if n := planner.MergeEngineeringSeeds(wiki, repoModel, cfg.MaxPages); n > 0 {
			fmt.Printf("  catalog: +%d engineering seed page(s) (scan-gated)\n", n)
		}
		bindEvidence(wiki, repoModel)
		applyCoverageTopUp(wiki, repoModel, cfg)
		return wiki, repoModel, nil
	}

	// Optional inventory for the agent (non-binding). Also used as LLM-failure fallback
	// and dual-rail soft-merge seed after free catalog planning.
	inventory := planner.Build(repoModel, plan, planner.Options{MaxPages: cfg.MaxPages})
	hint := planner.FormatInventoryHint(inventory, 40)
	planGuidance := ""
	if plan != nil {
		planGuidance = plan.GuidanceText()
	}

	wiki, err := agent.RunCatalog(ctx, client, cfg.Model, cfg.WorkDir, cfg.Language, false, verboseFn, onStatus, hint, planGuidance, cfg.MaxPages)
	usedLLM := err == nil && wiki != nil && len(wiki.Pages) > 0
	if !usedLLM {
		// Last resort only: use inventory tree when the model produces nothing usable.
		wiki = inventory
	}
	// Model hierarchy -> Parent / ContentPath (no seed field merge).
	planner.ApplyHierarchyPaths(wiki)
	// Free LLM catalogs often omit track; fill tracks and soft-merge thin rails from seed.
	// Soft cap is rail-aware (not prefix-chop) so foundation/business aren't dropped first.
	if usedLLM {
		planner.RebalanceDualRail(wiki, inventory, cfg.MaxPages)
	} else {
		planner.EnsureTracks(wiki)
		if cfg.MaxPages > 0 {
			planner.TrimWikiByRail(wiki, cfg.MaxPages)
		}
	}
	// CRITICAL: merge signal-gated engineering indexes HERE (before page gen),
	// not in Export. Export-time merge created pages with no draft -> thin stubs
	// and forced a second --only-stubs pass. Catalog-time merge means one generate
	// covers API/security/deploy/test/FAQ pages when scan signals justify them.
	if n := planner.MergeEngineeringSeeds(wiki, repoModel, cfg.MaxPages); n > 0 {
		fmt.Printf("  catalog: +%d engineering seed page(s) (scan-gated)\n", n)
	}
	bindEvidence(wiki, repoModel)
	applyCoverageTopUp(wiki, repoModel, cfg)
	return wiki, repoModel, nil
}

// applyCoverageTopUp closes catalog blind spots after evidence binding: detect
// code clusters no page references (zero-overlap rule, min cluster 4) and, when
// budget allows, append up to 6 pre-bound technical pages for the biggest gaps.
// New pages carry DependentFiles already, so re-running bindEvidence only fills
// pages that are still empty. Always prints one coverage summary line.
func applyCoverageTopUp(wiki *models.Wiki, repoModel *scan.Model, cfg Config) {
	gaps, covered, totalCode := planner.CoverageGaps(repoModel, wiki, 4)
	added := 0
	if wiki != nil && len(gaps) > 0 && (cfg.MaxPages <= 0 || len(wiki.Pages) < cfg.MaxPages) {
		maxAdd := 6
		if cfg.MaxPages > 0 && cfg.MaxPages-len(wiki.Pages) < maxAdd {
			maxAdd = cfg.MaxPages - len(wiki.Pages)
		}
		zh := repoModel != nil && repoModel.Language == "zh"
		added = planner.TopUpFromGaps(wiki, repoModel, gaps, maxAdd, zh)
		if added > 0 {
			if cfg.MaxPages > 0 {
				planner.TrimWikiByRail(wiki, cfg.MaxPages)
			}
			bindEvidence(wiki, repoModel)
		}
	}
	fmt.Printf("  coverage: %d/%d code files bound; %d gap cluster(s); +%d page(s)\n", covered, totalCode, len(gaps), added)
}

func bindEvidence(wiki *models.Wiki, repoModel *scan.Model) {
	if wiki == nil {
		return
	}
	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		if len(p.DependentFiles) == 0 {
			p.DependentFiles = evidence.PickDependentFiles(repoModel, p.Title, p.Goal, 8)
		}
		if p.DescriptionSlug == "" {
			p.DescriptionSlug = planner.DescriptionSlug(p.Title)
		}
	}
}

// seedDraftsFromPublished rebuilds drafts/ from published .wikify so generate can
// resume: substantial pages are copied as done, stub pages are left missing.
// When enrichShallow is set, substantial pages whose DepthScore is shallow
// (few sections / distinct cites / section sources) are also left missing so
// resume regenerates them with fresh evidence.
// Returns (keptSubstantial, stubsToRegen, shallowToRegen, error).
func seedDraftsFromPublished(workDir, draftDir string, enrichShallow bool) (kept, stubs, shallow int, err error) {
	root := filepath.Join(workDir, ".wikify")
	wikiPath := filepath.Join(root, "meta", "wiki.json")
	data, err := os.ReadFile(wikiPath)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("only-stubs: read %s: %w (run generate once or polish first)", wikiPath, err)
	}
	var wiki models.Wiki
	if err := json.Unmarshal(data, &wiki); err != nil {
		return 0, 0, 0, fmt.Errorf("only-stubs: parse wiki.json: %w", err)
	}
	if len(wiki.Pages) == 0 {
		return 0, 0, 0, fmt.Errorf("only-stubs: wiki.json has no pages")
	}
	planner.EnsureTracks(&wiki)

	contentDir := filepath.Join(root, "content")
	// Fresh draft tree so missingPages only sees intentionally omitted stubs.
	if err := clearDir(draftDir); err != nil {
		return 0, 0, 0, err
	}
	if err := os.MkdirAll(draftDir, 0o755); err != nil {
		return 0, 0, 0, err
	}

	// Refresh evidence for stub pages so regenerate cites role-matched paths.
	repoModel, _ := scan.Scan(workDir, "", scan.Options{})

	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		rel := p.ContentPath
		if rel == "" {
			rel = p.Title + ".md"
		}
		rel = strings.TrimPrefix(filepath.ToSlash(rel), "content/")
		raw, readErr := os.ReadFile(filepath.Join(contentDir, filepath.FromSlash(rel)))
		body := ""
		if readErr == nil {
			body = string(raw)
		}
		isStub := readErr != nil || wikiout.IsStubPage(body) || !isSubstantialBody(body)
		isShallow := !isStub && enrichShallow && evidence.DepthScore(body).Shallow()
		if isStub || isShallow {
			// Leave draft missing → resume will regenerate this page.
			// Clear stale deps so bindEvidence / RunPage get fresh role-matched files.
			if repoModel != nil && len(repoModel.Files) > 0 {
				if deps := evidence.PickDependentFiles(repoModel, p.Title, p.Goal, 8); len(deps) > 0 {
					p.DependentFiles = deps
				}
			}
			if isStub {
				stubs++
			} else {
				shallow++
			}
			continue
		}
		if err := savePage(p.Slug, body, draftDir); err != nil {
			return kept, stubs, shallow, err
		}
		kept++
	}
	if err := saveWiki(&wiki, draftDir); err != nil {
		return kept, stubs, shallow, err
	}
	return kept, stubs, shallow, nil
}

// isSubstantialBody mirrors export's spirit without importing unexported helpers:
// multi-heading or long plain text counts as done work worth keeping.
func isSubstantialBody(body string) bool {
	if wikiout.IsStubPage(body) {
		return false
	}
	if strings.Count(body, "\n## ") >= 2 {
		return true
	}
	// strip rough length: drop cite/mermaid fences for a cheaper check
	plain := body
	if i := strings.Index(plain, "```"); i >= 0 {
		// keep length heuristic on raw when diagrams present
	}
	runes := []rune(strings.TrimSpace(plain))
	return len(runes) >= 600
}

// publishFinal writes the single deliverable under .wikify/{content,meta}/ from draft pages.
func publishFinal(cfg Config, repoModel *scan.Model, wiki *models.Wiki, draftDir string) error {
	if wiki == nil {
		return fmt.Errorf("nil wiki")
	}
	planner.EnsureTracks(wiki)
	src := draftDir
	if src == "" {
		src = draftsDir(cfg.WorkDir)
	}
	contents := map[string]string{}
	for _, p := range wiki.Pages {
		data, err := os.ReadFile(filepath.Join(src, p.Slug+".md"))
		if err != nil {
			continue
		}
		contents[p.Slug] = string(data)
	}
	lang := cfg.LangDir
	if lang == "" {
		if repoModel != nil {
			lang = repoModel.Language
		}
		if lang == "" {
			lang = "zh"
		}
	}
	if err := wikiout.Export(cfg.WorkDir, repoModel, wiki, contents, wikiout.ExportOptions{Lang: lang, GraphFile: cfg.GraphFile}); err != nil {
		return err
	}
	// Clear drafts after successful publish
	_ = clearDir(src)
	// QL-4: console digest of the quality report Export just wrote.
	if rep, qerr := wikiout.LoadQualityReport(cfg.WorkDir); qerr == nil {
		for _, ln := range rep.Summary() {
			fmt.Println("  " + ln)
		}
	}
	fmt.Printf("  Wiki → %s\n", finalWikiRoot(cfg.WorkDir))
	return nil
}

// ── TUI run ────────────────────────────────────────────────────────────────────

func tuiRun(client *openai.Client, cfg Config, action, outPath string) error {
	ctx := context.Background()

	// Channels for in-session retry / skip communication from TUI → runner.
	retryCh := make(chan string, max(64, numWorkers(cfg.Workers)*4))
	skipCh := make(chan struct{}, 1)

	model := tui.NewWithChannels(retryCh, skipCh)
	prog := tea.NewProgram(model, tea.WithAltScreen())

	var runErr error
	var repoModel *scan.Model

	go func() {
		// ── Phase 1: Catalog ──────────────────────────────────────────────
		prog.Send(tui.CatalogStartMsg{})

		var wiki *models.Wiki

		if action == "resume" {
			loaded, err := loadDraftWiki(outPath)
			if err != nil {
				runErr = fmt.Errorf("cannot load draft wiki.json: %w", err)
				prog.Quit()
				return
			}
			wiki = loaded
			// re-scan for export / evidence on resume (best-effort)
			repoModel, _ = scan.Scan(cfg.WorkDir, cfg.Language, scan.Options{GraphFile: cfg.GraphFile})
			// Older drafts may lack dependent_files; re-bind empty ones.
			bindEvidence(wiki, repoModel)
			_ = saveWiki(wiki, outPath)
			total := len(wiki.Pages)
			done := countDonePages(outPath, wiki)
			prog.Send(tui.CatalogStatusMsg{Status: fmt.Sprintf("[resumed: %d/%d pages done]", done, total)})
			prog.Send(tui.CatalogDoneMsg{Pages: total, Sections: 0})
		} else {
			onStatus := func(s string) { prog.Send(tui.CatalogStatusMsg{Status: s}) }
			var err error
			wiki, repoModel, err = buildCatalog(ctx, client, cfg, nil, onStatus)
			if err != nil {
				runErr = err
				prog.Quit()
				return
			}
			if err2 := saveWiki(wiki, outPath); err2 != nil {
				runErr = err2
				prog.Quit()
				return
			}
			sections := map[string]bool{}
			for _, p := range wiki.Pages {
				sections[p.Section] = true
			}
			prog.Send(tui.CatalogDoneMsg{Pages: len(wiki.Pages), Sections: len(sections)})
		}

		// ── Phase 2: Pages ────────────────────────────────────────────────
		pages := wiki.Pages
		if action == "resume" {
			pages = missingPages(outPath, wiki)
		}

		rows := make([]tui.PageRow, len(pages))
		for i, p := range pages {
			rows[i] = tui.PageRow{Idx: i + 1, Title: p.Title, Slug: p.Slug}
		}
		prog.Send(tui.PagesInitMsg{Pages: rows})

		// Map slug → WikiPage for lookup during retries.
		pageBySlug := make(map[string]models.WikiPage, len(wiki.Pages))
		for _, p := range wiki.Pages {
			pageBySlug[p.Slug] = p
		}

		// Run initial batch of pages.
		failedSet := make(map[string]bool)
		var failedMu sync.Mutex
		runPagesTracked(ctx, client, cfg, pages, wiki, outPath, prog, &failedSet, &failedMu, repoModel)

		// ── Phase 3: Retry loop / Publish ─────────────────────────────────
		failedMu.Lock()
		hasFailed := len(failedSet) > 0
		failedMu.Unlock()

		finishOK := func() {
			if err := publishFinal(cfg, repoModel, wiki, outPath); err != nil {
				runErr = err
				prog.Quit()
				return
			}
			prog.Send(tui.GenerationDoneMsg{VersionID: "current", TotalPages: len(wiki.Pages)})
		}

		if !hasFailed {
			finishOK()
			return
		}

		// -y / --skip-failed: do not block on interactive retry; publish what we have
		// (missing drafts become honest thin stubs in Export). Completes one generate
		// without hanging the TUI when some pages fail after retries.
		if cfg.AutoYes || cfg.SkipFailed {
			failedMu.Lock()
			nFail := len(failedSet)
			failedMu.Unlock()
			fmt.Printf("\n\033[33m⚠ %d page(s) failed after retries — publishing partial wiki\033[0m\n", nFail)
			finishOK()
			return
		}

		// Has failures — stay in retry loop until user skips or all retried.
		var activeRetries int32
		retryAllDone := make(chan struct{}, 1)

		checkAllDone := func() {
			failedMu.Lock()
			f := len(failedSet)
			failedMu.Unlock()
			if f == 0 && atomic.LoadInt32(&activeRetries) == 0 {
				select {
				case retryAllDone <- struct{}{}:
				default:
				}
			}
		}

		for {
			select {
			case slug := <-retryCh:
				// User pressed r on a specific failed page — retry it.
				failedMu.Lock()
				delete(failedSet, slug)
				failedMu.Unlock()

				atomic.AddInt32(&activeRetries, 1)
				go func(s string) {
					defer func() {
						atomic.AddInt32(&activeRetries, -1)
						checkAllDone()
					}()

					prog.Send(tui.PageStartMsg{Slug: s})
					p := pageBySlug[s]
					onStatus := func(status string) {
						prog.Send(tui.PageStatusMsg{Slug: s, Status: status})
					}
					content, err := agent.RunPage(ctx, client, cfg.Model, cfg.WorkDir, cfg.Language,
						&p, wiki, false, nil, onStatus, repoModel)
					if err != nil {
						failedMu.Lock()
						failedSet[s] = true
						failedMu.Unlock()
						prog.Send(tui.PageFailedMsg{Slug: s, Err: err.Error()})
						return
					}
					if !isSubstantialBody(content) {
						failedMu.Lock()
						failedSet[s] = true
						failedMu.Unlock()
						prog.Send(tui.PageFailedMsg{Slug: s, Err: fmt.Sprintf("page body too thin for %q", p.Title)})
						return
					}
					// QL-1 gate: safe fixes applied; remaining hard issues fail the retry.
					linted, hardIssues, _ := wikiout.LintPageBody(content)
					if len(hardIssues) > 0 {
						failedMu.Lock()
						failedSet[s] = true
						failedMu.Unlock()
						prog.Send(tui.PageFailedMsg{Slug: s, Err: fmt.Sprintf("page lint hard issues for %q: %s", p.Title, strings.Join(hardIssues, "; "))})
						return
					}
					content = linted
					if err2 := savePage(s, content, outPath); err2 != nil {
						failedMu.Lock()
						failedSet[s] = true
						failedMu.Unlock()
						prog.Send(tui.PageFailedMsg{Slug: s, Err: err2.Error()})
						return
					}
					// Persist Primary deps chosen during RunPage evidence bundling.
					if len(p.DependentFiles) > 0 {
						pageBySlug[s] = p
						for i := range wiki.Pages {
							if wiki.Pages[i].Slug == s {
								wiki.Pages[i].DependentFiles = append([]string{}, p.DependentFiles...)
								break
							}
						}
						_ = saveWiki(wiki, outPath)
					}
					prog.Send(tui.PageDoneMsg{Slug: s})
				}(slug)

			case <-skipCh:
				// User pressed s — skip remaining failures and publish.
				finishOK()
				return

			case <-retryAllDone:
				// All failures have been retried and succeeded.
				finishOK()
				return
			}
		}
	}()

	if _, err := prog.Run(); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	fmt.Printf("\n\033[32m✓ Wiki generation complete\033[0m\n")
	return nil
}

// runPagesTracked runs pages in parallel, recording failures into failedSet.
func runPagesTracked(ctx context.Context, client *openai.Client, cfg Config, pages []models.WikiPage, wiki *models.Wiki, outPath string, prog *tea.Program, failedSet *map[string]bool, failedMu *sync.Mutex, repoModel *scan.Model) {
	w := numWorkers(cfg.Workers)
	sem := make(chan struct{}, w)
	var wg sync.WaitGroup
	var wikiMu sync.Mutex

	for _, page := range pages {
		p := page
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			prog.Send(tui.PageStartMsg{Slug: p.Slug})
			onStatus := func(status string) {
				prog.Send(tui.PageStatusMsg{Slug: p.Slug, Status: status})
			}

			maxAttempts := cfg.MaxRetries + 1
			var lastErr error
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				if attempt > 1 {
					prog.Send(tui.PageRetryingMsg{Slug: p.Slug})
					if lastErr != nil {
						time.Sleep(pageRetryDelay(attempt-1, lastErr))
					}
				}
				content, err := agent.RunPage(ctx, client, cfg.Model, cfg.WorkDir, cfg.Language,
					&p, wiki, false, nil, onStatus, repoModel)
				if err != nil {
					lastErr = err
					continue
				}
				if !isSubstantialBody(content) {
					lastErr = fmt.Errorf("page body too thin for %q (%d runes)", p.Title, len([]rune(content)))
					continue
				}
				// QL-1 gate: safe fixes applied; remaining hard issues → retry.
				linted, hardIssues, _ := wikiout.LintPageBody(content)
				if len(hardIssues) > 0 {
					lastErr = fmt.Errorf("page lint hard issues for %q: %s", p.Title, strings.Join(hardIssues, "; "))
					continue
				}
				content = linted
				if err2 := savePage(p.Slug, content, outPath); err2 != nil {
					lastErr = err2
					continue
				}
				if len(p.DependentFiles) > 0 {
					wikiMu.Lock()
					for i := range wiki.Pages {
						if wiki.Pages[i].Slug == p.Slug {
							wiki.Pages[i].DependentFiles = append([]string{}, p.DependentFiles...)
							break
						}
					}
					_ = saveWiki(wiki, outPath)
					wikiMu.Unlock()
				}
				prog.Send(tui.PageDoneMsg{Slug: p.Slug})
				return
			}
			failedMu.Lock()
			(*failedSet)[p.Slug] = true
			failedMu.Unlock()
			errMsg := "unknown error"
			if lastErr != nil {
				errMsg = lastErr.Error()
			}
			prog.Send(tui.PageFailedMsg{Slug: p.Slug, Err: errMsg})
		}()
	}
	wg.Wait()
}

// ── Plain run (--quiet / --verbose-pages) ──────────────────────────────────────

func plainRun(client *openai.Client, cfg Config, action, outPath string) error {
	ctx := context.Background()

	var wiki *models.Wiki
	var repoModel *scan.Model

	if action == "resume" {
		loaded, err := loadDraftWiki(outPath)
		if err != nil {
			return fmt.Errorf("cannot load draft wiki.json, try --draft clear: %w", err)
		}
		wiki = loaded
		repoModel, _ = scan.Scan(cfg.WorkDir, cfg.Language, scan.Options{GraphFile: cfg.GraphFile})
		// Older drafts may lack dependent_files; re-bind empty ones.
		bindEvidence(wiki, repoModel)
		_ = saveWiki(wiki, outPath)
		total := len(wiki.Pages)
		done := countDonePages(outPath, wiki)
		fmt.Printf("Resuming draft: %d/%d pages done, %d remaining\n", done, total, total-done)
	} else {
		var verboseFn func(string, string, string)
		if cfg.VerboseCatalog {
			verboseFn = tui.PlainLogFunc()
		}
		fmt.Printf("\033[34mPhase 1 — Generate Catalog\033[0m\n")
		var err error
		wiki, repoModel, err = buildCatalog(ctx, client, cfg, verboseFn, nil)
		if err != nil {
			return err
		}
		if err := saveWiki(wiki, outPath); err != nil {
			return err
		}
		sections := map[string]bool{}
		for _, p := range wiki.Pages {
			sections[p.Section] = true
		}
		fmt.Printf("\033[32m  [done] — %d pages across %d sections\033[0m\n",
			len(wiki.Pages), len(sections))
	}

	fmt.Printf("\n\033[34mPhase 2 — Generate Pages (%d total)\033[0m\n", len(wiki.Pages))

	pages := wiki.Pages
	if action == "resume" {
		pages = missingPages(outPath, wiki)
	}

	w := numWorkers(cfg.Workers)
	fmt.Printf("\033[2m%d pages, %d workers\033[0m\n", len(pages), w)

	var verbosePageFn func(string, string, string)
	if cfg.VerbosePages {
		verbosePageFn = tui.PlainLogFunc()
	}

	var failed int64
	if w <= 1 {
		failed = runSerialPlain(ctx, client, cfg, pages, wiki, outPath, verbosePageFn, repoModel)
	} else {
		failed = runParallelPlain(ctx, client, cfg, pages, wiki, outPath, verbosePageFn, repoModel)
	}

	done := countDonePages(outPath, wiki)
	missing := len(wiki.Pages) - done
	if missing > 0 && !cfg.SkipFailed && !cfg.AutoYes {
		fmt.Printf("\n\033[33m⚠ %d page(s) missing or failed — not publishing (use --skip-failed or -y to publish partial, or resume later)\033[0m\n", missing)
		fmt.Printf("  Drafts kept at: \033[36m%s\033[0m\n", outPath)
		return nil
	}
	if (failed > 0 || missing > 0) && (cfg.SkipFailed || cfg.AutoYes) {
		fmt.Printf("\n\033[33m⚠ publishing with %d missing and %d failed page(s) (honest stubs for gaps)\033[0m\n", missing, failed)
	}
	if err := publishFinal(cfg, repoModel, wiki, outPath); err != nil {
		fmt.Printf("\033[33m⚠ publish failed: %v\033[0m\n", err)
		return err
	}

	fmt.Printf("\n\033[32m✓ Wiki generation complete! %d pages published\033[0m\n", done)
	fmt.Printf("  Output: \033[36m%s\033[0m\n", finalWikiRoot(cfg.WorkDir))
	fmt.Printf("  Run wikify browse to view your docs\n")
	return nil
}

// ── page runners ───────────────────────────────────────────────────────────────

func runSerialPlain(ctx context.Context, client *openai.Client, cfg Config, pages []models.WikiPage, wiki *models.Wiki, outPath string, onToolCall func(string, string, string), repoModel *scan.Model) int64 {
	var failed int64
	total := len(pages)
	for i, page := range pages {
		p := page
		fmt.Printf("[%d/%d] %s (%s)\n", i+1, total, p.Title, p.Slug)
		if !generateWithRetryPlain(ctx, client, cfg, wiki, &p, outPath, onToolCall, repoModel) {
			failed++
		} else if len(p.DependentFiles) > 0 {
			for j := range wiki.Pages {
				if wiki.Pages[j].Slug == p.Slug {
					wiki.Pages[j].DependentFiles = append([]string{}, p.DependentFiles...)
					break
				}
			}
			_ = saveWiki(wiki, outPath)
		}
	}
	return failed
}

func runParallelPlain(ctx context.Context, client *openai.Client, cfg Config, pages []models.WikiPage, wiki *models.Wiki, outPath string, onToolCall func(string, string, string), repoModel *scan.Model) int64 {
	sem := make(chan struct{}, numWorkers(cfg.Workers))
	var wg sync.WaitGroup
	var failed int64
	var wikiMu sync.Mutex
	for _, page := range pages {
		p := page
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if !generateWithRetryPlain(ctx, client, cfg, wiki, &p, outPath, onToolCall, repoModel) {
				atomic.AddInt64(&failed, 1)
				return
			}
			if len(p.DependentFiles) > 0 {
				wikiMu.Lock()
				for j := range wiki.Pages {
					if wiki.Pages[j].Slug == p.Slug {
						wiki.Pages[j].DependentFiles = append([]string{}, p.DependentFiles...)
						break
					}
				}
				_ = saveWiki(wiki, outPath)
				wikiMu.Unlock()
			}
		}()
	}
	wg.Wait()
	return failed
}

// generateWithRetryPlain returns true on success.
func generateWithRetryPlain(ctx context.Context, client *openai.Client, cfg Config, wiki *models.Wiki, page *models.WikiPage, outPath string, onToolCall func(string, string, string), repoModel *scan.Model) bool {
	maxAttempts := cfg.MaxRetries + 1
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		content, err := agent.RunPage(ctx, client, cfg.Model, cfg.WorkDir, cfg.Language, page, wiki, cfg.VerbosePages, onToolCall, nil, repoModel)
		if err != nil {
			if attempt < maxAttempts {
				fmt.Printf("  \033[33m⚠ auto-retry %d/%d: %v\033[0m\n", attempt, cfg.MaxRetries, err)
				time.Sleep(pageRetryDelay(attempt, err))
				continue
			}
			if cfg.SkipFailed {
				fmt.Printf("  \033[33m⚠ skipped: %s (%v)\033[0m\n", page.Slug, err)
			} else {
				fmt.Printf("  \033[31m✗ failed: %v\033[0m\n", err)
			}
			return false
		}
		// Reject thin / empty blog bodies so we retry instead of publishing stubs for
		// "successful" agent turns that only emitted a title or a few lines.
		if !isSubstantialBody(content) {
			err = fmt.Errorf("page body too thin for %q (%d runes)", page.Title, len([]rune(content)))
			if attempt < maxAttempts {
				fmt.Printf("  \033[33m⚠ auto-retry %d/%d: %v\033[0m\n", attempt, cfg.MaxRetries, err)
				time.Sleep(pageRetryDelay(attempt, err))
				continue
			}
			if cfg.SkipFailed {
				fmt.Printf("  \033[33m⚠ skipped thin body: %s\033[0m\n", page.Slug)
			} else {
				fmt.Printf("  \033[31m✗ thin body: %v\033[0m\n", err)
			}
			return false
		}
		// QL-1 gate: apply safe structural fixes; hard issues remaining after
		// auto-fix (unclosable fences etc.) are treated like a thin body.
		linted, hardIssues, _ := wikiout.LintPageBody(content)
		if len(hardIssues) > 0 {
			err = fmt.Errorf("page lint hard issues for %q: %s", page.Title, strings.Join(hardIssues, "; "))
			if attempt < maxAttempts {
				fmt.Printf("  \033[33m⚠ auto-retry %d/%d: %v\033[0m\n", attempt, cfg.MaxRetries, err)
				time.Sleep(pageRetryDelay(attempt, err))
				continue
			}
			if cfg.SkipFailed {
				fmt.Printf("  \033[33m⚠ skipped lint-broken body: %s\033[0m\n", page.Slug)
			} else {
				fmt.Printf("  \033[31m✗ lint: %v\033[0m\n", err)
			}
			return false
		}
		content = linted
		if err2 := savePage(page.Slug, content, outPath); err2 != nil {
			fmt.Printf("  \033[31m✗ save failed: %v\033[0m\n", err2)
			return false
		}
		// Soft structural hints only (never hard-fail after a substantial body).
		if issues := evidence.SoftVerifyPageBody(content); len(issues) > 0 && cfg.VerbosePages {
			fmt.Printf("  \033[2msoft-verify: %s\033[0m\n", strings.Join(issues, "; "))
		}
		fmt.Printf("  \033[32m✓\033[0m %s.md\n", page.Slug)
		return true
	}
	return false
}

func promptDraftAction(workDir string) (string, error) {
	dDir := draftsDir(workDir)
	data, _ := os.ReadFile(filepath.Join(dDir, "wiki.json"))
	var w struct {
		Pages []struct {
			Slug string `json:"slug"`
		} `json:"pages"`
	}
	_ = json.Unmarshal(data, &w)
	total := len(w.Pages)
	done := 0
	for _, p := range w.Pages {
		if _, e := os.Stat(filepath.Join(dDir, p.Slug+".md")); e == nil {
			done++
		}
	}

	partial := done < total
	if partial {
		fmt.Printf("Unfinished generation task found (Completed: %d/%d)\n", done, total)
	} else {
		fmt.Printf("Documentation already exists (%d pages).\n", total)
	}
	fmt.Println("  [r] Resume")
	fmt.Println("  [c] Clear and restart")
	fmt.Println("  [x] Cancel")
	fmt.Print("Select [r/c/x]: ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	switch strings.TrimSpace(strings.ToLower(scanner.Text())) {
	case "r", "resume", "":
		return "resume", nil
	case "c", "clear":
		return "clear", nil
	default:
		return "cancel", nil
	}
}

// ── helpers ────────────────────────────────────────────────────────────────────

func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
	return nil
}

func loadDraftWiki(outPath string) (*models.Wiki, error) {
	data, err := os.ReadFile(filepath.Join(outPath, "wiki.json"))
	if err != nil {
		return nil, err
	}
	var wiki models.Wiki
	if err := json.Unmarshal(data, &wiki); err != nil {
		return nil, err
	}
	if len(wiki.Pages) == 0 {
		return nil, fmt.Errorf("wiki 中没有页面")
	}
	// Older drafts may omit track; materialise before write/export.
	planner.EnsureTracks(&wiki)
	return &wiki, nil
}

func missingPages(outPath string, wiki *models.Wiki) []models.WikiPage {
	var out []models.WikiPage
	skipped := 0
	for _, p := range wiki.Pages {
		path := filepath.Join(outPath, p.Slug+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			out = append(out, p)
			continue
		}
		body := string(data)
		// Treat honest stubs / non-substantial drafts as still missing so resume
		// and mid-run recovery do not skip pages that only got a placeholder.
		if wikiout.IsStubPage(body) || !isSubstantialBody(body) {
			out = append(out, p)
			continue
		}
		skipped++
	}
	if skipped > 0 {
		fmt.Printf("\033[2mResuming: skipping %d already-generated pages, %d remaining\033[0m\n", skipped, len(out))
	}
	return out
}

func countDonePages(outPath string, wiki *models.Wiki) int {
	n := 0
	for _, p := range wiki.Pages {
		data, err := os.ReadFile(filepath.Join(outPath, p.Slug+".md"))
		if err != nil {
			continue
		}
		body := string(data)
		if wikiout.IsStubPage(body) || !isSubstantialBody(body) {
			continue
		}
		n++
	}
	return n
}

var workersWarnOnce sync.Once

func numWorkers(n int) int {
	if n < 1 {
		return 1
	}
	// Soft advice: high concurrency through Cloudflare relays is the main
	// source of HTTP 524 storms. Values above 4 still work, but warn once.
	if n > 4 {
		workersWarnOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "\033[33m⚠ workers=%d is high for reverse-proxy LLM gateways; consider 1–2 if you see HTTP 524\033[0m\n", n)
		})
	}
	return n
}

func saveWiki(wiki *models.Wiki, outPath string) error {
	planner.EnsureTracks(wiki)
	b, err := json.MarshalIndent(wiki, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outPath, "wiki.json"), b, 0o644)
}

func savePage(slug, content, outPath string) error {
	return os.WriteFile(filepath.Join(outPath, slug+".md"), []byte(content), 0o644)
}


// newLLMHTTPClient tunes transport for long LLM calls through reverse proxies.
func newLLMHTTPClient() *http.Client {
	return &http.Client{
		// Overall per-request budget. Streaming completions can legitimately run
		// several minutes for a full page draft; CF 524 cares about idle time,
		// not total duration, once SSE chunks start flowing.
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   20 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 3 * time.Minute, // wait for first byte / headers
		},
	}
}

// pageRetryDelay spaces page-level retries so a storm of 524s does not
// immediately re-hammer the same gateway.
func pageRetryDelay(attempt int, err error) time.Duration {
	base := 3 * time.Second
	if agent.IsTransientAPIError(err) {
		// 3s, 6s, 12s, 24s … cap 45s; jittered so parallel workers that all
		// hit the same gateway outage do not retry in lockstep waves.
		if attempt < 1 {
			attempt = 1
		}
		d := base * time.Duration(1<<uint(attempt-1))
		if d > 45*time.Second {
			d = 45 * time.Second
		}
		return agent.JitterDelay(d)
	}
	// thin body / logic errors: short pause only
	return 1 * time.Second
}
