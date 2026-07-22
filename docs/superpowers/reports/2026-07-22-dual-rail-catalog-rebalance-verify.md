# 验证报告：dual-rail-catalog-rebalance

- **日期**: 2026-07-22
- **变更**: `dual-rail-catalog-rebalance`
- **工作流**: tweak
- **verify_mode**: full（`comet state scale`：tasks=8、delta specs=1、changed files=15；本仓库为 Comet 隔离下的本地 git init，diff 含整包文件首次入库，真实 dual-rail 语义改动集中于 models/planner/runner/tests）
- **language**: zh-CN
- **base_ref**: `437bceab5f93c07c8e237dc11053c261f066eacb`
- **实现提交**: `6b4a0c7` — tweak: dual-rail catalog rebalance after free LLM planning
- **分支**: `tweak/dual-rail-catalog-rebalance`
- **review_mode**: off（跳过自动 code review；原因：tweak 预设默认 review_mode=off）

## 结论

**PASS** — 无 CRITICAL / IMPORTANT 失败项。

---

## A. 轻量六项

| # | 检查项 | 结果 | 证据 |
|---|--------|------|------|
| 1 | tasks.md 全部 `[x]` | PASS | 8/8 已勾选；未勾选 0 |
| 2 | 改动与 tasks 描述一致 | PASS | Track 统一、EnsureTracks/RebalanceDualRail、allowlist InferTrack、runner 接线、单测均与 tasks 1.x–3.x 对应 |
| 3 | 编译通过 | PASS | `go build -o wikify.exe .` exit 0（GOROOT=scoop go 1.26.5） |
| 4 | 相关测试通过 | PASS | `go test ./internal/... -count=1` exit 0；含 `TestEnsureTracksAndRebalance`、`TestFromAllowlistInfersTrack`、`TestInferTrack`、`TestDualRailTracksAndBudgets` 等 |
| 5 | 无明显安全问题 | PASS | 无硬编码密钥；runner 中 `APIKey` 仅为配置字段透传 |
| 6 | 代码审查 | SKIP | `review_mode: off` |

---

## B. 完整验证（OpenSpec / design 对照）

### Completeness

| 项 | 结果 |
|----|------|
| tasks 完成 | 8/8 PASS |
| openspec validate --strict | PASS：`Change 'dual-rail-catalog-rebalance' is valid` |
| delta capability `wiki-dual-rail` | 已实现（见 Correctness） |

### Correctness — 需求与场景

| 需求 / 场景 | 实现映射 | 测试 |
|-------------|----------|------|
| 每页 track ∈ foundation/business/technical；空 track 定稿后推断写回 | `models.InferTrack`；`planner.EnsureTracks`；`paths` 与 `agent/page` 空 track 回退 InferTrack | `TestInferTrack`、`TestFromAllowlistInfersTrack`、`TestEnsureTracksAndRebalance` |
| LLM catalog 无 track → 推断后 FormatCatalog 可三轨分组 | EnsureTracks + FormatCatalog 用 InferTrack | `TestFormatCatalogByTrack`、`TestEnsureTracksAndRebalance` |
| allowlist 非全部 business | `fromAllowlist` 使用 `InferTrack(title, section)` | `TestFromAllowlistInfersTrack` |
| 业务轨过薄 + seed 有业务页 → 软并入，≤ MaxPages | `RebalanceDualRail` floors + mergeUnique 风格补页 + trim | `TestEnsureTracksAndRebalance`（biz≥4，保留 LLM 技术页） |
| 接口/数据库 inventory → technical | InferTrack section 规则；核心模块/核心业务不进 business | `TestInferTrack`、`TestDualRailTracksAndBudgets` |
| 不污染 class-mirror 控制层标题 | 既有 planner 业务优先逻辑保留 | `TestBusinessDomainsPreferCustomerOverControl` |

### Coherence — design 决策

| 设计决策 | 对照 |
|----------|------|
| 不推翻 LLM 主导 catalog；rebalance 只保 Track + 软补全 | `runner.buildCatalog`：`usedLLM` 时 `RebalanceDualRail(wiki, inventory, MaxPages)`，否则 `EnsureTracks` |
| Track 推断单一来源 models.InferTrack | `trackForCategory` 委托 InferTrack；`fromAllowlist` 同 |
| allowlist 走同一推断 | `fromAllowlist` 已改 |
| 非目标：browse UI 三轨、knowledge 导出、纯确定性 seed 主导 | 未改这些范围 |

**Design Doc（docs/superpowers/specs）**：本 tweak 无独立 Superpowers Design Doc（preset 跳过 brainstorming）；以 change 内 `design.md` + delta spec 为准。无 spec/design 漂移需决策。

---

## C. 命令证据（本轮 fresh）

```text
go test ./internal/... -count=1          # exit 0
go build -o wikify.exe .                 # exit 0
openspec validate dual-rail-catalog-rebalance --strict  # valid
```

---

## D. 已知局限 / 非阻塞

1. 仓库为 Comet isolation 下 `git init` 的短历史；`git diff base...HEAD` 文件数被整包首次提交放大，scale 偏向 full，不影响代码正确性。
2. 真实 ADS 全量 LLM 生成对比 Qoder 属人工验收，不在本 verify 自动化范围（tasks 3.2 以 dry-run/fixture 与单测覆盖）。
3. `branch_status` 需用户选择分支处理后写入。

---

## E. 分支处理

待用户选择（finishing-a-development-branch）：

1. 本地合并到 base
2. 推送并创建 PR
3. 保持分支稍后处理
4. 丢弃工作
