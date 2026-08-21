# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.3] - 2026-08-21

### Added
- **多格式导出**：`wikify export --format docusaurus|mkdocs` 零 LLM 成本转换（`internal/export/format.go` + frontmatter / mkdocs.yml）
- **文档 Lint**：`wikify lint` 检查断链/薄页/结构性问题（`internal/export/doclint.go`）
- **代码示例提取**：从 `*_test.go` / `*Test.java` 自动提取可运行片段，注入为 Examples 小节（`internal/export/examples.go`）
- **CI/CD 模板**：`docs/ci-template.yml` GitHub Actions 示例

### Changed
- **CLI Help**：root Long 增加 Workflow 三步说明（`generate -> browse/polish -> export/lint`）
- **README 同步**：中英文均补充 `polish` / `export` / `config check` 命令参考

## [0.1.2] - 2026-08-20

### Added
- **包级文件内容缓存**：`internal/tools` 进程级 `abspath|mtime|size -> bytes`
- **并发限流自适应**：`adaptiveLimiter` 指数退避/回落，双路径 `Wait` + `OnThrottle`
- **错误恢复补强**：同类永久错误 3 次早停 + 双路径失败汇总
- **首次运行引导**：无 API Key 时中文友好提示（含 `WIKIFY_API_KEY` 备选）

### Changed
- **README**：安装示例 `v0.1.1` -> `v0.1.2`

### Fixed
- **config 兼容**：`NormalizeBaseURL` 自动剥离 `/responses` 等后缀（opencode.ai 兼容）
- **探针**：推理模型（`xhigh/high`）`MaxTokens 1 -> 50`，避免 400

## [0.1.1] - 2026-08-04

### Changed
- **版本注入机制**：源码构建显示 `dev`，发布版本通过 `-ldflags` 注入真实版本号
- **清理硬编码内容**：移除个人路径和服务名，改用通用示例

### Fixed
- 修正 `internal/config/apierror.go` 中的网关示例为通用名称
- 修正 `internal/config/baseurl_test.go` 测试用例使用通用域名
- 清理脚本中的硬编码 GOROOT 路径

## [0.1.0] - 2026-07-26

### Added
- **首次开源发布**
- **核心功能**：两阶段 ReAct 智能体流水线、TUI、草稿恢复等
- **发布工具链**：GoReleaser + GitHub Actions
- **文档**：中英双语 README、MIT 许可证
