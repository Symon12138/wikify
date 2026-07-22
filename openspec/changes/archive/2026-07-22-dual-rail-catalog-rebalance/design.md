## 实现说明（tweak 精简 design）

### 决策

1. **不推翻「LLM 主导 catalog」**：模型仍可自由规划标题/层级；rebalance 只保证：
   - 每页 `Track` 非空且 ∈ {foundation, business, technical}
   - 若 LLM 产出几乎无业务轨且 seed inventory 有业务页，则 **mergeUnique 补入** 缺失业务/入门页（软补全，受 MaxPages 约束）
2. **Track 推断单一来源**：优先 `models` 的 `inferTrack` 语义；planner 的 `trackForCategory` 与其对齐，避免两套规则漂移。
3. **allowlist**：`fromAllowlist` 用 section+title 走同一推断，不再硬编码 business。

### 主要改动点

| 文件 | 改动 |
|------|------|
| `internal/models` | 导出或复用 `InferTrack`；边界规则与 planner 对齐 |
| `internal/planner` | `fromAllowlist`；`trackForCategory`；注释；可选 `RebalanceTracks` 助手 |
| `internal/runner` | catalog 成功路径：ApplyHierarchyPaths 后调用 rebalance / ensure tracks |
| `*_test.go` | allowlist track、LLM 空 track 补全、业务轨软补全 |

### 非目标

- 不做 browse UI 三轨分组
- 不改 knowledge 导出
- 不把 catalog 改回纯确定性 seed 主导（可后续 full 变更）
