# Round 706: extension-system and Guardian module audits

以下 Rust 提交依赖 `codex-extension-api` 或独立 crate 边界；Go 没有扩展 API，
以现有同步 Guardian/tool 实现作为等价行为，逐条记为 `not_applicable`：

- `#40472` Add structured full approval reviews to extensions：
  Go 的 `modelGuardianReviewer` 同步返回 allow/deny，决策结构由
  `state.ReviewDecision` 承载。
- `#41202` Let extensions process MCP tool results：
  MCP 输出直接进入 turn/tool 管线与 retained-context recorder，没有 extension
  callback。
- `#47662` Expose tool dispatch and timing observations to extensions：
  dispatch/timing 由 turn runtime 与 telemetry 内部记录。
- `#45729` Move Guardian reviewer configuration into the extension：
  配置构建位于 `guardianSessionRunner`，没有 extension crate 可迁移。
- `#45420` Extract Guardian sampler execution into a dedicated module：
  Go 已在 `modelGuardianReviewer`/`guardianSessionRunner` 内隔离。

验证：

- `go test ./parity -count=1`

账本状态变化：`implementation_pending` 86 → 76，`not_applicable` 137 → 143。
