# `#47096` short-terminal clipping order

- Rust 依据：`e8cd975a55a1e28955195f1f69a9cb81a5e4898d`。
- `bottomReservedRows` 现在计入 queued/attachment preview 与 quick-dial HUD，
  正常高度下先收缩 transcript，而不是丢弃这些行。
- 仍超高度时，owned/fullscreen 布局按 hint → HUD → activity → preview 顺序
  裁剪；composer 与 status 行保留，transcript 最后从顶部收缩。
- 裁剪后重新映射 footer/working/composer section index，避免选中区域与
  welcome-blossom 插入点错位。
- inline 布局保持原有“hint 在 composer 下方”的顺序。
- composer gap 现在优先显示 quota usage notice：详情目标半行宽，窄终端允许
  最短完整警告占满行；Plus/Team 5h 阈值为 50%，其余 75%；有 workspace
  credits 时抑制；reset 详情仅在放得下时显示。
- `RateLimitWindow` 补齐 `ResetsAt`，从 protocol snapshot 映射，用于
  `resets at ...` 文案。

结论：`#47096` 的短终端裁剪与 usage notice 宽度策略均已落地。`#46912`
仍保留 partial：rolling snapshot 已合并稀疏窗口、保留元数据，并只在 reset
窗口前进时接受低用量恢复；剩余 app-server request-id 的 stale account-read
拒绝与账户切换清理。

验证：

- `go test ./tui/... -count=1`
- `go vet ./tui/tea ./tui/markdown ./tui/streaming ./tui/history_cell ./tui/bottom_pane`
