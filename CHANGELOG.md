# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.1] - 2026-08-04

### Changed
- **版本注入机制**：源码构建显示 `dev`，发布版本通过 `-ldflags` 注入真实版本号
- **清理硬编码内容**：移除个人路径和服务名，改用通用示例

### Fixed
- 修正 `internal/config/apierror.go` 中的网关示例为通用名称
- 修正 `internal/config/baseurl_test.go` 测试用例使用通用域名
- 清理脚本中的硬编码 GOROOT 路径（`build-release.sh`、`run-e2e-generate.sh`、`verify-offline.sh`）

## [0.1.0] - 2026-07-26

### Added
- **首次开源发布**
- **核心功能**：
  - 两阶段 ReAct 智能体流水线（Catalog + Page）
  - 实时 TUI 进度展示（Bubbletea）
  - 草稿恢复机制
  - 配置文件 `~/.wikify/config.yaml`
  - 支持任意 OpenAI 兼容接口
  - 本地 Wiki 浏览器 `wikify browse`
- **发布工具链**：
  - GoReleaser + GitHub Actions 自动构建
  - 6 平台预编译二进制（Linux/macOS/Windows × amd64/arm64）
- **文档**：
  - 中英双语 README
  - MIT 许可证
  - 安装与快速开始指南

### Technical Details
- Go 1.21+ 单一二进制
- 轻量仓库扫描 + 确定性主题树规划
- `dependent_files` 证据绑定
- 智能体工具：`get_dir_structure`、`view_file_in_detail`、`run_bash`
- 输出：`.wikify/{content,meta,wiki_plan.yaml}`
