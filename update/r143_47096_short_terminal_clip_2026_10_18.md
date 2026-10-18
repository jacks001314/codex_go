# `#47096` short-terminal clipping order

- Rust 依据：`e8cd975a55a1e28955195f1f69a9cb81a5e4898d`。
- `bottomReservedRows` 现在计入 queued/attachment preview 与 quick-dial HUD，
  正常高度下先收缩 transcript，而不是丢弃这些行。
- 仍超高度时，owned/fullscreen 布局按 hint → HUD → activity → preview 顺序
  裁剪；composer 与 status 行保留，transcript 最后从顶部收缩。
- 裁剪后重新映射 footer/working/composer section index，避免选中区域与
  welcome-blossom 插入点错位。
- inline 布局保持原有“hint 在 composer 下方”的顺序。

剩余差异：

- Rust 的 usage notice 详情宽度策略（半行宽、最短完整警告可占满行）仍无法
  落地，因为 Go 没有对应的 usage-notice 状态/行。

验证：

- `go test ./tui/... -count=1`
- `go vet ./tui/tea ./tui/markdown ./tui/streaming ./tui/history_cell ./tui/bottom_pane`
