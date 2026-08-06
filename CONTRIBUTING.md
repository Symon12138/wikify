# 参与贡献

感谢你对 wikify 的兴趣！欢迎提交 Issue 和 Pull Request。

## 开发环境

**要求：** Go 1.21+

```bash
# 克隆仓库
git clone https://github.com/Symon12138/wikify.git
cd wikify

# 安装依赖
go mod download

# 构建
go build -o wikify .

# 运行测试
go test ./...

# 代码检查
go vet ./...
```

## 项目结构

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
├── tools/      # 智能体工具实现
├── tui/        # Bubbletea TUI 模型
└── wikiplan/   # wiki_plan.yaml 读写
```

## 提交规范

本项目使用 [Conventional Commits](https://www.conventionalcommits.org/) 规范：

- `feat:` 新功能
- `fix:` 修复 Bug
- `docs:` 文档更新
- `chore:` 构建/工具链变更
- `refactor:` 代码重构（不改变行为）
- `test:` 测试相关
- `ci:` CI/CD 配置

示例：
```
feat: add --max-pages flag to limit catalog size
fix: handle empty drafts directory gracefully
docs: update README installation instructions
```

## Pull Request 流程

1. **Fork 仓库**并创建你的分支：
   ```bash
   git checkout -b feat/your-feature
   ```

2. **编写代码**并确保测试通过：
   ```bash
   go test ./...
   go vet ./...
   ```

3. **提交变更**（遵循 Conventional Commits）：
   ```bash
   git commit -m "feat: add new feature"
   ```

4. **推送到 Fork 仓库**：
   ```bash
   git push origin feat/your-feature
   ```

5. **创建 Pull Request**并描述：
   - 改动的目的
   - 如何测试
   - 相关 Issue（如有）

## 报告问题

提交 Issue 时请包含：

- **问题描述** — 预期行为 vs 实际行为
- **复现步骤** — 最小可复现示例
- **环境信息** — 运行 `wikify version` 的输出
- **相关日志** — 使用 `--verbose-catalog` 或 `--verbose-pages` 捕获

## 代码风格

- 遵循 Go 官方风格指南
- 使用 `gofmt` 格式化代码
- 公开 API 必须有文档注释
- 优先使用表驱动测试（table-driven tests）

## 许可证

提交代码即表示你同意将贡献按 MIT 许可证开源。
