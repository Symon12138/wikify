## 1. Track 推断统一

- [ ] 1.1 将 `inferTrack` 暴露为 `models.InferTrack`（或等价导出），统一「核心模块」边界
- [ ] 1.2 `trackForCategory` 与 `InferTrack` 对齐；修正 `InventoryRatio` 注释默认 0.20
- [ ] 1.3 `fromAllowlist` 按 section/title 推断 Track

## 2. Catalog 成功路径 rebalance

- [ ] 2.1 新增 `planner.EnsureTracks` / `RebalanceDualRail`：写满 Track；业务/入门轨过薄时从 seed 软补全
- [ ] 2.2 `runner.buildCatalog` 在 LLM 成功与 allowlist 路径调用 rebalance
- [ ] 2.3 单测：空 Track 补全、allowlist 分轨、软补全不超 MaxPages

## 3. 验证

- [ ] 3.1 `go test ./internal/...` 与 `go build`
- [ ] 3.2 ADS dry-run 或 fixture 确认 Track 覆盖与业务轨存在
