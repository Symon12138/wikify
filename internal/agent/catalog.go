package agent

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	openai "github.com/sashabaranov/go-openai"
	"github.com/JSHurt/wikify/internal/models"
	"github.com/JSHurt/wikify/internal/prompts"
	"github.com/JSHurt/wikify/internal/tools"
)

// RunCatalog explores the repository and returns a structured Wiki catalog.
// The model plans the tree freely. inventoryHint is optional non-binding context;
// planGuidance comes from wiki_plan.yaml (template/notes/scope).
func RunCatalog(
	ctx context.Context,
	client *openai.Client,
	model string,
	workDir string,
	language string,
	verbose bool,
	onToolCall func(name, args, result string),
	onStatus func(status string),
	inventoryHint string,
	planGuidance string,
	maxPages int,
) (*models.Wiki, error) {
	osName := "linux"
	if runtime.GOOS == "windows" {
		osName = "windows"
	} else if runtime.GOOS == "darwin" {
		osName = "macos"
	}

	ts := tools.New(workDir)

	// Get top-level structure for the user prompt
	structure := ts.Handle["get_dir_structure"](map[string]any{"dir_path": ".", "max_depth": float64(2)})

	if maxPages <= 0 {
		maxPages = 120
	}
	systemPrompt := fmt.Sprintf(prompts.CatalogSystem, workDir, osName, maxPages)
	if strings.TrimSpace(inventoryHint) == "" {
		inventoryHint = "(no inventory hint — plan solely from repository inspection)"
	}
	if strings.TrimSpace(planGuidance) == "" {
		planGuidance = "(none — plan from repository structure and analysis)"
	}
	userPrompt := fmt.Sprintf(prompts.CatalogUser, workDir, osName, language, maxPages, structure, planGuidance, inventoryHint)

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
		return nil, fmt.Errorf("catalog agent failed: %w", err)
	}

	if !strings.Contains(raw, "<section>") {
		preview := raw
		if len(preview) > 500 {
			preview = preview[:500]
		}
		return nil, fmt.Errorf("catalog agent: no <section> found in LLM output.\n\n%s", preview)
	}

	wiki := models.ParseCatalogXML(raw)
	if len(wiki.Pages) == 0 {
		return nil, fmt.Errorf("parsed catalog has no pages. Raw output:\n%s", raw[:min(500, len(raw))])
	}

	return wiki, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
