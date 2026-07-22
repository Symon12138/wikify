## Why

双轨文档（foundation / business / technical）已在 planner seed、写页提示与 catalog 分组中落地，但 **Catalog LLM 成功路径**会整棵替换 seed 树，只做路径补全、不做 Track 强制推断与双轨语义保底，真实生成时业务轨可能被接口/库表清单冲淡。复核还发现 allowlist 全标 business、`InventoryRatio` 注释与默认不一致、`trackForCategory` 与 `inferTrack` 对「核心模块」边界不一致。需要在真实对比 Qoder 前把这些残留修干净。

## What Changes

- Catalog（含 LLM 产出与 allowlist）在 `ApplyHierarchyPaths` / 绑定阶段后，**空 Track 必须被推断并写回**，使 `FormatCatalog`、写页 track 注入与 browse-index 一致。
- 在 runner 的 catalog 成功路径增加 **轻量双轨 rebalance**（不改写模型标题，只保证 Track 与 foundation/business/technical 阅读语义可用；必要时用 seed inventory 补薄业务/入门轨页，或至少保证 Track 覆盖率 100%）。
- `fromAllowlist` 按 section/title 推断 Track，不再全部 `TrackBusiness`。
- `Options.InventoryRatio` 注释与代码默认 **0.20** 对齐。
- 统一 `trackForCategory` / `inferTrack` 对「核心业务模块 / 核心模块」等边界。
- 补充/扩展单测覆盖上述路径。

## Capabilities

### New Capabilities
- `wiki-dual-rail`: 双轨目录与页面的 Track 分配、catalog 分组、跨轨关联与 LLM catalog 后的 rebalance 语义。

### Modified Capabilities
- （无既有 main spec）

## Impact

- 代码：`internal/planner`、`internal/runner`、`internal/models`（必要时）
- 行为：生成目录的 Track 字段更稳定；browse / 写页提示更可靠
- 无 public CLI 破坏性变更；无 schema / 外部 API 变更
