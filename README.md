<div align="center">

```
██╗    ██╗██╗██╗  ██╗██╗███████╗██╗   ██╗
██║    ██║██║██║ ██╔╝██║██╔════╝╚██╗ ██╔╝
██║ █╗ ██║██║█████╔╝ ██║█████╗   ╚████╔╝
██║███╗██║██║██╔═██╗ ██║██╔══╝    ╚██╔╝
╚███╔███╔╝██║██║  ██╗██║██║        ██║
 ╚══╝╚══╝ ╚═╝╚═╝  ╚═╝╚═╝╚═╝        ╚═╝
```

**将任意代码库一键转化为结构化 Wiki 文档**

[![Go Version](https://img.shields.io/badge/Go-1.21%2B-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)](LICENSE)
[![Release](https://img.shields.io/github/v/release/Symon12138/wikify?style=flat-square)](https://github.com/Symon12138/wikify/releases)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey?style=flat-square)]()

**语言 / Language：** 中文 | [English](README.en.md)

</div>

---

## ✨ 简介

**wikify** 是一款命令行工具，通过 AI 智能体深度理解你的代码库，自动生成结构化、可读性强的 Wiki 文档。

wikify 专注于以下优势：

- 🚀 **直接下载，无需包管理器** — 单一二进制，下载后直接使用
- 🖥️ **实时 TUI** — 彩色进度表格，支持单页重试
- 🔁 **草稿恢复** — 中断后可从断点继续生成
- 🔌 **配置文件** — 使用 `~/.wikify/config.yaml`
- 🌐 **支持任意 OpenAI 兼容接口** — DeepSeek、OpenAI、Ollama 等

---

## 📸 效果展示

```
Phase 2 — Generate Pages (12/28)

    #   Page                              Status
────────────────────────────────────────────────────────
❯   1   概述                              ✓  done
    2   快速开始                           ✓  done
    3   环境要求与安装                      ✓  done
    4   API密钥配置                        ⟳  requesting
    5   命令行工具使用                      ⚙  view_file
    6   架构设计理念                        ·  waiting
  ↓ 22 more

↑/↓: navigate  |  r: retry  |  s: skip failed & commit  |  ctrl+c: quit
```

> 状态颜色说明：**绿色** = 完成 · **青色** = 请求中 · **品红** = 工具调用 · **红色** = 失败

---

## 🚀 快速开始

### 安装

**方式一：下载二进制（推荐）**

前往 [Releases](https://github.com/Symon12138/wikify/releases) 下载对应平台的压缩包，解压后放入 `PATH`。

```bash
# Linux amd64
curl -L https://github.com/Symon12138/wikify/releases/download/v0.1.1/wikify_0.1.1_linux_amd64.tar.gz | tar xz
sudo mv wikify /usr/local/bin/wikify

# macOS Apple Silicon
curl -L https://github.com/Symon12138/wikify/releases/download/v0.1.1/wikify_0.1.1_darwin_arm64.tar.gz | tar xz
sudo mv wikify /usr/local/bin/wikify
```

Windows 用户直接从 [Releases](https://github.com/Symon12138/wikify/releases) 下载 `wikify_0.1.1_windows_amd64.zip`，解压后将 `wikify.exe` 放入 `PATH`。

**方式二：从源码构建**

```bash
git clone https://github.com/Symon12138/wikify.git
cd wikify
go build -o wikify .
```

### 1. 配置 LLM

```bash
# 交互式（推荐）：↑/↓ 选字段，←/→ 切换枚举，Enter 输入，s 保存
wikify config

# 或非交互：
wikify config --api-key sk-xxxxxxxx
```

配置写入 `~/.wikify/config.yaml`。

### 2. 生成文档

```bash
cd /path/to/your/project
wikify generate
```

wikify 将分阶段工作：
1. **扫描** — 轻量仓库扫描（结构/清单，仅作参考）
2. **目录规划** — Catalog 智能体按 Why/架构/受众/结构四步**自主规划**多级目录（路径由 section/group/title 推导）
3. **并发写页** — Page 智能体 ReAct 写内容（统一 `file://…#L` 引用），并绑定 `dependent_files`
4. **发布** — 草稿写入 `.wikify/drafts`，完成后发布到 `.wikify/{content,meta}` 并清空草稿

### 3. 浏览文档

```bash
wikify browse
```

自动在浏览器打开 `http://localhost:3000`，展示生成的 Wiki（基于 `.wikify`）。

### 4. 最终输出

`generate` 完成后只保留一套最终目录：

```
.wikify/
  wiki_plan.yaml
  drafts/                 # 生成中（提交后清空）
  content/**/*.md         # 最终 Markdown
  meta/
    wiki.json
    browse-index.json
    wiki-metadata.json
```

不生成 knowledge 知识卡目录。`wikify browse` 在生成中优先预览 `.wikify/drafts`，完成后打开 `.wikify/content`。

```bash
# 默认 wikify：多级规划 + 证据绑定 + 单套最终输出
wikify generate -y

# 控制规模
wikify generate --max-pages 80
```

页面会尽量包含 `<cite>`、`file://path#L` 引用与目录骨架；`wiki.json` 中保留 `dependent_files` / `content_path` 等字段以便恢复。

---

## ⚙️ 配置文件

配置存储于 `~/.wikify/config.yaml`，示例：

```yaml
language: zh           # 界面语言
doc_language: zh       # 文档输出语言（zh / en）

llm:
  provider: custom
  model: deepseek-chat
  api_key: sk-xxxxxxxxxxxxxxxx
  base_url: https://api.deepseek.com/v1  # 任意 OpenAI 兼容接口

concurrency:
  max_concurrent: 3   # 并发页面生成数
  max_retries: 2      # 单页失败后自动重试次数
```

通过命令行管理：

```bash
wikify config                           # 交互式编辑（TTY）/ 非 TTY 时打印当前配置
wikify config --api-key sk-xxx          # 设置 API Key
wikify config --base-url https://...    # 自定义接口地址
wikify config --model deepseek-chat     # 设置模型
wikify config --workers 5               # 设置并发数
wikify config --retries 2               # 设置重试次数
wikify config --lang en                 # 切换语言
```

### 环境变量覆盖

```bash
WIKIFY_API_KEY=sk-xxx         wikify generate
WIKIFY_BASE_URL=https://...   wikify generate
WIKIFY_MODEL=gpt-4o           wikify generate
```

---

## 📖 命令参考

### `wikify generate`

为当前工作区生成 Wiki 文档。

```
参数：
  -y, --yes               跳过所有确认，立即开始生成
      --draft string      草稿处理方式：resume（继续）| clear（清除）| cancel（取消）
      --skip-failed       自动跳过失败页面并提交剩余内容
      --lang string       覆盖文档输出语言
      --dir string        目标目录（默认：当前目录）
      --retries int       单页最大重试次数（默认：1）
      --workers int       覆盖并发数
      --verbose-catalog   显示目录智能体工具调用（关闭 TUI）
      --verbose-pages     显示页面智能体工具调用（关闭 TUI）
      --max-pages int     最大页数（默认 120）
      --export-lang string 内容语言标签 zh|en（默认来自扫描）
```

**TUI 实时交互快捷键：**

| 按键 | 功能 |
|------|------|
| `↑` / `↓` | 导航页面列表 |
| `r` | 重试选中的失败页面 |
| `s` | 跳过所有失败页面并提交 |
| `ctrl+c` | 退出 |

### `wikify browse`

在浏览器中浏览生成的 Wiki。

```
参数：
      --port int    本地服务端口（默认：3000）
      --open        自动在浏览器中打开（默认：true）
      --build       导出静态 HTML 站点（不启动服务）
      --dir string  项目目录（默认：当前目录）
```

### `wikify config`

查看或修改 `~/.wikify/config.yaml`。

无参数且为 TTY 时进入交互式表单：
- **↑/↓** 选择字段
- **←/→** 切换 Language / Workers / Retries；**Model 从 Base URL 的 `/models` 拉取后循环选择**
- **Enter** 编辑文本（API Key、Base URL、Model 也可直接输入任意模型名）
- **r** 重新从 Base URL 拉取模型列表
- **s** 保存 · **q** 退出

也可使用 `--api-key` / `--base-url` / `--model` / `--lang` / `--workers` / `--retries` 非交互写入。

### `wikify version`

显示版本、Go 运行时及平台信息。

---

## 🏗️ 工作原理

wikify 使用**两阶段 ReAct 智能体**流水线：

```
┌─────────────────────────────────────────────────┐
│  第一阶段 — 目录智能体（Catalog Agent）           │
│                                                 │
│  读取项目结构 → 生成结构化目录                    │
│  （章节 + 页面列表，JSON 格式）                   │
└──────────────────────┬──────────────────────────┘
                       │ drafts/wiki.json
┌──────────────────────▼──────────────────────────┐
│  第二阶段 — 页面智能体（并发执行）                │
│                                                 │
│  每个页面独立运行一个智能体：                     │
│  1. 通过工具读取相关源文件                        │
│  2. 将内容综合为 Markdown                        │
│  3. 草稿 → .wikify/drafts，完成后发布 content/meta │
└─────────────────────────────────────────────────┘
```

**智能体可用工具：**
- `get_dir_structure` — 列出目录树（尊重 .gitignore）
- `view_file_in_detail` — 读取源文件（可指定行范围）
- `run_bash` — 只读 shell 命令（检索/辅助，禁止写删）

**输出目录结构：**
```
.wikify/
├── drafts/              # 生成中的草稿（提交后清空）
├── content/             # 最终多级 Markdown
├── meta/                # wiki.json / browse-index / metadata
├── wiki_plan.yaml
└── build/               # browse --build 静态站点
```

---

## 🔄 草稿与恢复

生成中断后草稿会自动保存，下次运行时提示：

```
发现未完成的草稿（已完成 15/28 页）
? 请选择操作：
  > resume   — 从断点继续
    clear    — 清除草稿重新开始
    cancel   — 退出
```

也可通过参数跳过提示：

```bash
wikify generate --draft resume
wikify generate --draft clear
```

---

## 🌐 LLM 提供商兼容性

wikify 使用 **OpenAI 兼容的 Chat Completions API**，支持任意兼容提供商：

| 提供商 | base_url |
|--------|----------|
| DeepSeek（默认） | `https://api.deepseek.com/v1` |
| OpenAI | `https://api.openai.com/v1` |
| Ollama（本地） | `http://localhost:11434/v1` |
| Azure OpenAI | `https://{resource}.openai.azure.com/openai/deployments/{deploy}/` |
| 自定义代理 | 你的接口地址 |

---

## 🛠️ 参与开发

**环境要求：** Go 1.21+

```bash
# 构建
go build -o wikify .

# 直接运行
go run . generate

# 代码检查
go vet ./...
```

**项目结构：**

```
internal/
├── agent/      # ReAct LLM 智能体（目录 + 页面）
├── browse/     # 本地 Wiki HTTP 服务
├── config/     # 配置文件读写（~/.wikify/config.yaml）
├── evidence/   # 页面 dependent_files 证据绑定
├── models/     # 数据模型与 OpenAI 客户端
├── planner/    # 确定性主题树 / 代码清单规划
├── prompts/    # LLM 提示词模板
├── export/     # 最终 .wikify/{content,meta} 写出
├── runner/     # 编排层：TUI 模式 + 纯文本模式
├── scan/       # 轻量仓库扫描
├── tools/      # 智能体工具：get_dir_structure、view_file_in_detail、run_bash
├── tui/        # Bubbletea TUI 模型
└── wikiplan/   # wiki_plan.yaml 读写
```

---

## 📄 许可证

MIT © Symon

*wikify 是独立的开源项目，与 third-party wiki services 官方无关。*
