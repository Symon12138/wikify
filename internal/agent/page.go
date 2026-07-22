package agent

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strings"

	openai "github.com/sashabaranov/go-openai"
	"github.com/symon/wikify/internal/models"
	"github.com/symon/wikify/internal/prompts"
	"github.com/symon/wikify/internal/tools"
)

var blogPattern = regexp.MustCompile(`(?s)<blog>(.*?)</blog>`)

// RunPage generates markdown documentation for a single wiki page.
// Returns the extracted blog content (without <blog> tags).
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
) (string, error) {
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

	// Inject track guidance, cross-rail links, and evidence binding.
	{
		var b strings.Builder
		track := page.Track
		if track == "" {
			track = models.InferTrack(*page)
		}
		b.WriteString("\n\n## Documentation track\n")
		b.WriteString("This page belongs to the **")
		b.WriteString(track)
		b.WriteString("** rail.\n")
		switch track {
		case models.TrackBusiness:
			b.WriteString(`Write primarily for domain / product / BA readers:
- Open with capability purpose, actors, and business rules.
- Document the main process with Mermaid; keep technical depth as anchors, not the spine.
- End with section "实现落点" (or "Implementation anchors"): key packages/classes and links to related technical wiki pages.
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
					b.WriteString(r.Slug)
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
		if len(page.DependentFiles) > 0 {
			b.WriteString("\n## Pre-bound Source Files\n")
			b.WriteString("Read these first and cite with file://…#L ranges. Additional files only when necessary.\n\n**Primary sources:**\n")
			for _, f := range page.DependentFiles {
				b.WriteString("- ")
				b.WriteString(f)
				b.WriteString("\n")
			}
		}
		userPrompt += b.String()
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

	return extractBlog(raw), nil
}

func extractBlog(text string) string {
	if m := blogPattern.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(text)
}
