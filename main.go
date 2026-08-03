// wikify — turn any codebase into a structured wiki
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/JSHurt/wikify/internal/browse"
	"github.com/JSHurt/wikify/internal/config"
	"github.com/JSHurt/wikify/internal/export"
	"github.com/JSHurt/wikify/internal/runner"
)

var appVersion = "0.1.0"

func main() {
	root := &cobra.Command{
		Use:          "wikify",
		Short:        "Turn any codebase into a beautiful wiki",
		Long:         "wikify — generate structured wiki documentation from your local codebase using an AI agent.\nSee https://github.com/JSHurt/wikify for docs.",
		SilenceUsage: true,
	}
	root.AddCommand(
		newGenerateCmd(),
		newPolishCmd(),
		newConfigCmd(),
		newBrowseCmd(),
		newVersionCmd(),
	)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── generate ──────────────────────────────────────────────────────────────────

func newGenerateCmd() *cobra.Command {
	var (
		dir            string
		draft          string
		skipFailed     bool
		yes            bool
		onlyStubs      bool
		enrichShallow  bool
		lang           string
		workers        int
		verboseCatalog bool
		verbosePages   bool
		retries        int
		maxPages       int
		exportLang     string
		graphFile      string
	)

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate wiki documentation for the current workspace",
		Long: `Generate wiki documentation for the current workspace.

Full run plans a catalog then writes every page with the LLM.

Use --only-stubs after polish when most pages are already substantial: reuses
.wikify/meta/wiki.json + content/**, keeps non-stub pages, regenerates only
pages marked 待补充 / Status: stub. Does not re-plan the catalog.

Use --enrich-shallow (alone or with --only-stubs) to also regenerate pages
whose depth profile is shallow: few H2 sections, few distinct cited files, or
no section-source blocks. Also reuses the published wiki without re-planning.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cfg.APIKey == "" {
				return fmt.Errorf("API key not configured — run: wikify config")
			}

			if cmd.Flags().Changed("lang") {
				cfg.Language = lang
			}
			if cmd.Flags().Changed("workers") {
				cfg.Workers = workers
			}
			// Prefer config max_retries when --retries flag is not set.
			maxRetries := retries
			if !cmd.Flags().Changed("retries") && cfg.Retries > 0 {
				maxRetries = cfg.Retries
			}

			targetDir := dir
			if targetDir == "" {
				targetDir, _ = os.Getwd()
			}
			absDir, err := filepath.Abs(targetDir)
			if err != nil {
				return err
			}

			return runner.Run(runner.Config{
				APIKey:         cfg.APIKey,
				BaseURL:        cfg.BaseURL,
				Model:          cfg.Model,
				WorkDir:        absDir,
				Language:       cfg.Language,
				Workers:        cfg.Workers,
				Draft:          draft,
				SkipFailed:     skipFailed,
				AutoYes:        yes,
				OnlyStubs:      onlyStubs,
				EnrichShallow:  enrichShallow,
				VerboseCatalog: verboseCatalog,
				VerbosePages:   verbosePages,
				MaxRetries:     maxRetries,
				MaxPages:       maxPages,
				LangDir:        exportLang,
				GraphFile:      graphFile,
			})
		},
	}

	f := cmd.Flags()
	f.StringVar(&draft, "draft", "", "Action for existing draft: resume, clear, or cancel")
	f.BoolVar(&skipFailed, "skip-failed", false, "Automatically skip failed pages and publish remaining wiki")
	f.BoolVarP(&yes, "yes", "y", false, "Skip all confirmations and generate immediately")
	f.BoolVar(&onlyStubs, "only-stubs", false, "Regenerate only stub pages from published .wikify (skip catalog, keep substantial pages)")
	f.BoolVar(&enrichShallow, "enrich-shallow", false, "Also regenerate shallow pages (few sections/cites/sources) from published .wikify")
	f.BoolVar(&verbosePages, "verbose-pages", false, "Show page agent tool calls (disables TUI)")
	f.BoolVar(&verboseCatalog, "verbose-catalog", false, "Show catalog agent tool calls (disables TUI)")
	f.IntVar(&retries, "retries", 1, "Max retries per page on failure")
	f.StringVar(&dir, "dir", "", "Target directory (default: current working directory)")
	f.StringVar(&lang, "lang", "", "Override documentation language (e.g. Chinese, English)")
	f.IntVar(&workers, "workers", 0, "Override concurrent worker count")
	f.IntVar(&maxPages, "max-pages", 0, "Max wiki pages (default: auto-scaled to repo size)")
	f.StringVar(&exportLang, "export-lang", "", "Content language label zh|en (default from scan)")
	f.StringVar(&graphFile, "graph-file", "", "Optional external code-graph JSON (import edges overlay)")

	return cmd
}

// ── polish (offline re-export) ────────────────────────────────────────────────

func newPolishCmd() *cobra.Command {
	var (
		dir        string
		exportLang string
		graphFile  string
	)
	cmd := &cobra.Command{
		Use:   "polish",
		Short: "Re-export existing .wikify without LLM (tracks, TOC, metadata)",
		Long: `Polish rewrites .wikify/{content,meta} from the current wiki.json and markdown.

No catalog/page agents are run. Use this after upgrading wikify to fill tracks,
inject missing ## 目录, and refresh browse/metadata — without a full generate.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			targetDir := dir
			if targetDir == "" {
				targetDir, _ = os.Getwd()
			}
			absDir, err := filepath.Abs(targetDir)
			if err != nil {
				return err
			}
			if err := export.Polish(absDir, export.ExportOptions{Lang: exportLang, GraphFile: graphFile}); err != nil {
				return err
			}
			fmt.Println("✓ Polished →", filepath.Join(absDir, ".wikify"))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "Target directory that contains .wikify (default: cwd)")
	cmd.Flags().StringVar(&exportLang, "export-lang", "", "Content language label zh|en (default from .wikify/lang)")
	cmd.Flags().StringVar(&graphFile, "graph-file", "", "Optional external code-graph JSON (import edges overlay)")
	return cmd
}

// ── config ────────────────────────────────────────────────────────────────────

func newConfigCmd() *cobra.Command {
	var (
		apiKey   string
		baseURL  string
		model    string
		language string
		workers  int
		retries  int
	)

	cmd := &cobra.Command{
		Use:   "config",
		Short: "View or modify wikify configuration",
		Long: `View or modify settings stored in ~/.wikify/config.yaml.

Interactive (default when no flags and a TTY is available):
  wikify config
    up/down     select field
    left/right  cycle language/workers/retries; model cycles remote list
    Enter       edit text (API key, base URL, model free input)
    r           refresh model list from Base URL (/models)
    s           save
    q           quit without saving

Non-interactive flags (script-friendly):
  wikify config --api-key sk-xxx --base-url https://... --model deepseek-chat
  wikify config --lang zh --workers 3 --retries 2`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw := config.LoadRaw()

			changed := false
			if cmd.Flags().Changed("api-key") {
				raw.LLM.APIKey = apiKey
				changed = true
			}
			if cmd.Flags().Changed("base-url") {
				raw.LLM.BaseURL = baseURL
				changed = true
			}
			if cmd.Flags().Changed("model") {
				raw.LLM.Model = model
				changed = true
			}
			if cmd.Flags().Changed("lang") {
				raw.Language = language
				raw.DocLanguage = language
				changed = true
			}
			if cmd.Flags().Changed("workers") {
				raw.Concurrency.MaxConcurrent = workers
				changed = true
			}
			if cmd.Flags().Changed("retries") {
				raw.Concurrency.MaxRetries = retries
				changed = true
			}

			if changed {
				if err := config.Save(raw); err != nil {
					return fmt.Errorf("failed to save config: %w", err)
				}
				fmt.Println("✓ Config saved →", config.ConfigPath())
				return nil
			}

			// No flags: prefer interactive TUI when a terminal is available.
			saved, err := config.Interactive()
			if err != nil {
				return err
			}
			if saved {
				return nil
			}
			// Non-TTY or user quit without saving → show current config.
			printConfig(raw)
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&apiKey, "api-key", "", "Set API key (llm.api_key)")
	f.StringVar(&baseURL, "base-url", "", "Set API base URL (llm.base_url, default: https://api.deepseek.com/v1)")
	f.StringVar(&model, "model", "", "Set model name (llm.model, default: deepseek-chat)")
	f.StringVar(&language, "lang", "", "Set language code (language / doc_language, e.g. zh / en)")
	f.IntVar(&workers, "workers", 0, "Set max concurrency (concurrency.max_concurrent, default: 1)")
	f.IntVar(&retries, "retries", 0, "Set max retries (concurrency.max_retries, default: 1)")

	cmd.AddCommand(newConfigCheckCmd())

	return cmd
}

// newConfigCheckCmd verifies the saved LLM config by fetching the model list
// and sending a tiny real chat request, then reports a clear diagnosis.
func newConfigCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Verify the configured model actually responds",
		Long: `Diagnose the saved LLM configuration in ~/.wikify/config.yaml.

It fetches the remote model list, checks whether the configured model is
present, then sends a minimal real chat request to confirm the model replies.
Failures are classified (network / auth / not-found / rate-limit / server /
empty-list / unrecognized) with an actionable message.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw := config.LoadRaw()
			if strings.TrimSpace(raw.LLM.BaseURL) == "" {
				return fmt.Errorf("base_url 未配置,请先运行 `wikify config` 设置")
			}

			fmt.Printf("正在检查配置 (%s)…\n", config.ConfigPath())
			fmt.Printf("  base_url: %s\n", raw.LLM.BaseURL)
			fmt.Printf("  model:    %s\n\n", raw.LLM.Model)

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			res := config.CheckModel(ctx, raw.LLM)

			// 模型列表。
			switch {
			case res.ModelListErr != nil:
				_, msg := config.ClassifyError(res.ModelListErr)
				if msg == "" {
					msg = res.ModelListErr.Error()
				}
				fmt.Printf("• 模型列表:获取失败 — %s\n", msg)
			case len(res.Models) == 0:
				fmt.Println("• 模型列表:网关未返回列表(将跳过存在性判定)")
			case res.ModelPresent:
				fmt.Printf("• 模型列表:已找到 %q (共 %d 个)\n", raw.LLM.Model, len(res.Models))
			default:
				fmt.Printf("• 模型列表:共 %d 个,但未找到 %q,请确认模型名\n", len(res.Models), raw.LLM.Model)
			}

			// 真实对话探测。
			if res.ProbeErr != nil {
				kind, msg := config.ClassifyError(res.ProbeErr)
				if msg == "" {
					msg = res.ProbeErr.Error()
				}
				fmt.Printf("• 对话探测:失败 — %s\n", msg)
				// 模型名错误时,直接把网关可用模型列出来,便于照着改。
				if kind == config.KindModelNotFound && len(res.Models) > 0 {
					fmt.Printf("  网关可用模型 (%d 个):%s\n", len(res.Models), strings.Join(res.Models, ", "))
				}
			} else {
				reply := res.ProbeReply
				if reply == "" {
					reply = "(空回复,但接口 2xx 正常)"
				}
				fmt.Printf("• 对话探测:成功 · 端点 %s · 回复 %q\n", res.Endpoint, reply)
			}

			fmt.Println()
			if res.OK() {
				fmt.Println("✓ 配置可用:模型能够正常响应。")
				return nil
			}
			return fmt.Errorf("配置检查未通过,请根据上面的提示修正后重试")
		},
	}
}

func printConfig(raw *config.Config) {
	masked := raw.LLM.APIKey
	if len(masked) > 8 {
		masked = masked[:4] + "****" + masked[len(masked)-4:]
	} else if len(masked) > 0 {
		masked = "****"
	} else {
		masked = "(not set)"
	}
	fmt.Printf("Config (%s):\n", config.ConfigPath())
	fmt.Printf("  language:                   %s\n", raw.Language)
	fmt.Printf("  doc_language:               %s\n", raw.DocLanguage)
	fmt.Printf("  llm.provider:               %s\n", raw.LLM.Provider)
	fmt.Printf("  llm.model:                  %s\n", raw.LLM.Model)
	fmt.Printf("  llm.api_key:                %s\n", masked)
	fmt.Printf("  llm.base_url:               %s\n", raw.LLM.BaseURL)
	fmt.Printf("  concurrency.max_concurrent: %d\n", raw.Concurrency.MaxConcurrent)
	fmt.Printf("  concurrency.max_retries:    %d\n", raw.Concurrency.MaxRetries)
}

// ── browse ────────────────────────────────────────────────────────────────────

func newBrowseCmd() *cobra.Command {
	var (
		dir  string
		port int
		open bool
		bld  bool
	)

	cmd := &cobra.Command{
		Use:          "browse",
		Short:        "Browse generated wiki docs in your browser",
		Long:         "Start a local HTTP server to browse the wiki generated by wikify generate.\nNo Node.js required. Use --build to export a static HTML site.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			targetDir := dir
			if targetDir == "" {
				targetDir, _ = os.Getwd()
			}
			absDir, _ := filepath.Abs(targetDir)
			siteDir := filepath.Join(absDir, ".wikify", "site")
			return browse.Run(absDir, siteDir, port, open, bld)
		},
	}

	f := cmd.Flags()
	f.StringVar(&dir, "dir", "", "Project directory (default: current directory)")
	f.IntVar(&port, "port", 3000, "Local server port")
	f.BoolVar(&open, "open", true, "Auto-open in browser")
	f.BoolVar(&bld, "build", false, "Build static site (no server)")

	return cmd
}

// ── version ───────────────────────────────────────────────────────────────────

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Print(`
██╗    ██╗██╗██╗  ██╗██╗███████╗██╗   ██╗
██║    ██║██║██║ ██╔╝██║██╔════╝╚██╗ ██╔╝
██║ █╗ ██║██║█████╔╝ ██║█████╗   ╚████╔╝
██║███╗██║██║██╔═██╗ ██║██╔══╝    ╚██╔╝
╚███╔███╔╝██║██║  ██╗██║██║        ██║
 ╚══╝╚══╝ ╚═╝╚═╝  ╚═╝╚═╝╚═╝        ╚═╝
         Turn any codebase into a beautiful wiki
──────────────────────────────────────────────────────────────────────
`)
			fmt.Printf("Version:    %s\n", appVersion)
			fmt.Printf("Go:         %s\n", runtime.Version())
			fmt.Printf("OS:         %s\n", runtime.GOOS)
			fmt.Printf("Arch:       %s\n", runtime.GOARCH)
		},
	}
}
