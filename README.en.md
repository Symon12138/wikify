<div align="center">

```
██╗    ██╗██╗██╗  ██╗██╗███████╗██╗   ██╗
██║    ██║██║██║ ██╔╝██║██╔════╝╚██╗ ██╔╝
██║ █╗ ██║██║█████╔╝ ██║█████╗   ╚████╔╝
██║███╗██║██║██╔═██╗ ██║██╔══╝    ╚██╔╝
╚███╔███╔╝██║██║  ██╗██║██║        ██║
 ╚══╝╚══╝ ╚═╝╚═╝  ╚═╝╚═╝╚═╝        ╚═╝
```

**Turn any codebase into a beautiful wiki — in seconds.**

[![Go Version](https://img.shields.io/badge/Go-1.21%2B-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)](LICENSE)
[![Release](https://img.shields.io/github/v/release/Symon12138/wikify?style=flat-square)](https://github.com/Symon12138/wikify/releases)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey?style=flat-square)]()

**Language:** [中文](README.md) | English

</div>

---

## ✨ What is wikify?

**wikify** is a CLI tool that uses an AI agent to deeply understand your codebase and generate structured, human-readable wiki documentation — automatically.

Key highlights:

- 📦 **Single binary** — download directly from [Releases](https://github.com/Symon12138/wikify/releases), no package manager needed
- 🖥️ **Live TUI** — color-coded progress table with per-page retry
- 🔁 **Draft & resume** — pick up where you left off after interruption
- 🔌 **Config file** — stores settings in `~/.wikify/config.yaml`
- 🌐 **Any OpenAI-compatible API** — DeepSeek, OpenAI, Ollama, etc.

---

## 📸 Demo

```
Phase 2 — Generate Pages (12/28)

    #   Page                              Status
────────────────────────────────────────────────────────
❯   1   Overview                          ✓  done
    2   Quick Start                        ✓  done
    3   Installation                       ✓  done
    4   API Key Setup                      ⟳  requesting
    5   CLI Usage                          ⚙  view_file
    6   Architecture                       ·  waiting
  ↓ 22 more

↑/↓: navigate  |  r: retry  |  s: skip failed & commit  |  ctrl+c: quit
```

> Status colors: **green** = done · **cyan** = requesting · **magenta** = tool call · **red** = failed

---

## 🚀 Quick Start

### Installation

**Download binary (recommended)**

Go to [Releases](https://github.com/Symon12138/wikify/releases) and download the archive for your platform:

```bash
# Linux amd64
curl -L https://github.com/Symon12138/wikify/releases/download/v0.1.0/wikify_0.1.0_linux_amd64.tar.gz | tar xz
sudo mv wikify /usr/local/bin/wikify

# macOS Apple Silicon
curl -L https://github.com/Symon12138/wikify/releases/download/v0.1.0/wikify_0.1.0_darwin_arm64.tar.gz | tar xz
sudo mv wikify /usr/local/bin/wikify
```

Windows users: download `wikify_0.1.0_windows_amd64.zip` from [Releases](https://github.com/Symon12138/wikify/releases), unzip, and put `wikify.exe` on your `PATH`.

**Build from source**

```bash
git clone https://github.com/Symon12138/wikify.git
cd wikify
go build -o wikify .
```

### 1. Configure your LLM

```bash
# Interactive (recommended): arrows to navigate, Enter to type, s to save
wikify config

# Or non-interactive:
wikify config --api-key sk-xxxxxxxx
```

Writes to `~/.wikify/config.yaml`.

### 2. Generate docs

```bash
cd /path/to/your/project
wikify generate
```

### 3. Browse

```bash
wikify browse   # opens http://localhost:3000
```

---

## ⚙️ Configuration (`~/.wikify/config.yaml`)

```yaml
language: zh           # UI language
doc_language: zh       # Documentation language (zh / en)

llm:
  provider: custom
  model: deepseek-chat
  api_key: sk-xxxxxxxxxxxxxxxx
  base_url: https://api.deepseek.com/v1  # Any OpenAI-compatible endpoint

concurrency:
  max_concurrent: 3   # Parallel page workers
  max_retries: 2      # Auto-retries per page on failure
```

```bash
wikify config                        # Interactive editor (TTY) / print if non-TTY
wikify config --api-key sk-xxx       # Set API key
wikify config --base-url https://... # Custom endpoint
wikify config --model deepseek-chat  # Set model
wikify config --workers 5            # Set concurrency
wikify config --retries 2            # Set retries
wikify config --lang en              # Switch language
```

Environment variable overrides:

```bash
WIKIFY_API_KEY=sk-xxx        wikify generate
WIKIFY_BASE_URL=https://...  wikify generate
WIKIFY_MODEL=gpt-4o          wikify generate
```

---

## 📖 Commands

### `wikify generate`

```
Flags:
  -y, --yes               Skip confirmations and start immediately
      --draft string      Draft action: resume | clear | cancel
      --skip-failed       Auto-skip failed pages and commit
      --lang string       Override documentation language
      --dir string        Target directory (default: cwd)
      --retries int       Max retries per page (default: 1)
      --workers int       Override concurrent worker count
      --verbose-catalog   Show catalog agent tool calls (disables TUI)
      --verbose-pages     Show page agent tool calls (disables TUI)
      --max-pages int     Max pages (default 120)
      --export-lang string Content language label zh|en (default from scan)
```

**TUI controls:**

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate page list |
| `r` | Retry selected failed page |
| `s` | Skip all failures and commit |
| `ctrl+c` | Quit |

### `wikify browse`

```
Flags:
      --port int    Local server port (default: 3000)
      --open        Auto-open in browser (default: true)
      --build       Export static HTML site (no server)
      --dir string  Project directory (default: cwd)
```

### `wikify config`

View or edit `~/.wikify/config.yaml`.

With no flags on a TTY, opens an interactive form (↑/↓ select, ←/→ cycle enums and remote models from Base URL `/models`, Enter to type any model, r refresh models, s save, q quit). Flags remain available for scripts.

### `wikify version`

Print version, Go runtime, and platform info.

---

## 🏗️ How It Works

```
┌─────────────────────────────────────────────┐
│  Phase 1 — Catalog Agent                   │
│  Reads project structure → generates JSON   │
│  table of contents (sections + pages)       │
└──────────────────┬──────────────────────────┘
                   │ drafts/wiki.json
┌──────────────────▼──────────────────────────┐
│  Phase 2 — Page Agents (parallel)           │
│  Each page: read files → synthesize → save  │
│  Tools: get_dir_structure · view_file · run_bash │
└─────────────────────────────────────────────┘
```

Output layout:
```
.wikify/
├── drafts/          # In-progress drafts (cleared on publish)
├── content/         # Final multi-level markdown
├── meta/            # wiki.json / browse-index / metadata
├── wiki_plan.yaml
└── build/           # Static site (browse --build)
```

---

## 🌐 LLM Provider Compatibility

| Provider | base_url |
|----------|----------|
| DeepSeek (default) | `https://api.deepseek.com/v1` |
| OpenAI | `https://api.openai.com/v1` |
| Ollama (local) | `http://localhost:11434/v1` |
| Azure OpenAI | `https://{resource}.openai.azure.com/openai/deployments/{deploy}/` |
| Custom proxy | Your endpoint |

---

## 🛠️ Development

Requirements: Go 1.21+

```bash
go build -o wikify .
go vet ./...
```

```
internal/
├── agent/      # ReAct LLM agent (catalog + page runners)
├── browse/     # Local wiki HTTP server
├── config/     # Config file (~/.wikify/config.yaml)
├── evidence/   # dependent_files evidence binding
├── models/     # Data models & OpenAI client
├── planner/    # Deterministic topic tree / code inventory
├── prompts/    # LLM prompt templates
├── export/     # final .wikify/{content,meta} writer
├── runner/     # Orchestration: TUI + plain mode
├── scan/       # Lightweight repo scan
├── tools/      # Agent tools: get_dir_structure, view_file_in_detail, run_bash
├── tui/        # Bubbletea TUI model
└── wikiplan/   # wiki_plan.yaml loader
```

---

## 📄 License

MIT © Symon

*wikify is an independent open-source project and is not affiliated with third-party wiki services.*