## ADDED Requirements

### Requirement: Every wiki page has a documentation track

系统 MUST 为每一页 wiki 分配 `track`，取值仅为 `foundation`、`business` 或 `technical`。空 track MUST 在 catalog 定稿（含 LLM catalog 与 allowlist）后被推断并写回，不得以空字符串进入写页与导出。

#### Scenario: LLM catalog pages without track

- **WHEN** Catalog Agent 返回的页面缺少 `track` 字段
- **THEN** 系统根据 section/title 推断 track 并写入该页，使 `FormatCatalog` 能按三轨分组

#### Scenario: Allowlist documents are not all business

- **WHEN** `wiki_plan` 文档白名单包含架构/接口类 section 的文档
- **THEN** 对应页 track MUST 按 section 推断为 technical（或 foundation），而非全部 business

### Requirement: Dual-rail catalog remains usable after free LLM planning

当 LLM 自由规划 catalog 成功时，系统 MUST 保留双轨阅读语义：入门轨、业务能力轨、技术参考轨在导航中可区分；若模型产出几乎无业务页且扫描 seed 含业务能力页，系统 MAY 将缺失业务/入门页软并入最终 catalog（受 MaxPages 约束），且 MUST NOT 以 class-mirror 控制层标题污染业务轨。

#### Scenario: Thin business rail after LLM catalog

- **WHEN** LLM catalog 中 business track 页数低于合理下限，且 seed inventory 含业务模块页
- **THEN** 最终 catalog 合并补入 seed 中的业务/入门页（去重），总页数不超过 MaxPages

#### Scenario: Inventory stays on technical rail

- **WHEN** 目录含接口/数据库类 inventory 页
- **THEN** 这些页的 track MUST 为 technical
