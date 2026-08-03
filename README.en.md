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
[![Release](https://img.shields.io/github/v/release/symon/wikify?style=flat-square)](https://github.com/symon/wikify/releases)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey?style=flat-square)]()

**Language:** [中文](README.md) | English

*Open-source reimplementation of [legacy-wiki-cli](https://www.npmjs.com/package/legacy-wiki-cli) — same features and config format, fully open source.*

</div>

---

## ✨ What is wikify?

**wikify** is a CLI tool that uses an AI agent to deeply understand your codebase and generate structured, human-readable wiki documentation — automatically.

`legacy-wiki-cli` is a Go program distributed via npm (`npm install -g legacy-wiki-cli`). **wikify** is its open-source reimplementation — same configuration format, same workflow, downloadable as a single binary without npm.

Key highlights:

- 📦 **No npm required** — download a single binary directly from [Releases](https://github.com/symon/wikify/releases)
- 🖥️ **Live TUI** — color-coded progress table with per-page retry
- 🔁 **Draft & resume** — pick up where you left off after interruption
- 🔌 **Config-compatible** — stores settings in `~/.wikify/config.yaml`
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

Go to [Releases](https://github.com/symon/wikify/releases) and download the archive for your platform:

```bash
# Linux amd64
curl -L https://github.com/symon/wikify/releases/latest/download/wikify_v0.1.0_linux_amd64.tar.gz | tar xz
sudo mv wikify-linux-amd64 /usr/local/bin/wikify

# macOS Apple Silicon
curl -L https://github.com/symon/wikify/releases/latest/download/wikify_v0.1.0_darwin_arm64.tar.gz | tar xz
sudo mv wikify-darwin-arm64 /usr/local/bin/wikify
```

**Build from source**

```bash
git clone https://github.com/symon/wikify.git
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

## 🆚 vs `legacy-wiki-cli`

> `legacy-wiki-cli` is a Go program distributed via npm. wikify is its open-source equivalent — install directly without npm.

| Feature | `legacy-wiki-cli` | **wikify** |
|---------|-------------|--------------|
| Installation | `npm install -g legacy-wiki-cli` | Download binary directly |
| Source code | ❌ Closed source | ✅ MIT open source |
| Config format | `~/.wikify/config.yaml` | ✅ Own config |
| Two-phase generation | ✅ | ✅ |
| Live TUI | ✅ | ✅ |
| Per-page retry (`r`) | ✅ | ✅ |
| Skip failed (`s`) | ✅ | ✅ |
| Draft / resume | ✅ | ✅ |
| Browse command | ✅ | ✅ |

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