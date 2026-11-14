# 未聚焦侧面板 status line（#61141，部分，2026-11-14）

## 交付

- `Model` 新增 `paneStatusText`，在 `renderStatusHeader` 渲染当前线程状态行时缓存其 `PlainText()`。
- `renderSideWorkspace` 为未聚焦的面板取该线程缓存的状态，`renderSidePaneTranscript` 在 transcript 底部追加状态内容：按面板宽度 `codextui.WrapLine` 换行、最多 3 行、逐行加 dim 并截断；不输出 hyperlink。聚焦面板不重复渲染（composer 下方已有状态行）。sidebar chooser 可见时左侧父面板作为未聚焦面板显示状态。

## 回归

`TestSidePaneStatusContentLikeRust`：缓存读取、聚焦面板为空、未聚焦面板返回状态、换行不超过 3 行且每行带 dim/宽度受限、pane transcript 含状态、聚焦 pane 不含状态。`go vet ./tui/tea` 退出 0，`go test ./tui/tea -count=1` 见 `side_pane_status_2026_11_14.log`。

## 与 Rust 的剩余差异

- Rust 按面板可用行做完整 footer/draft 预算，并保留结构化状态以便主题/超链接重排；Go 当前固定 3 行上限、只保留纯文本，颜色在未聚焦面板被完全降级为 dim。
- 状态只在主视图渲染该线程时更新缓存；从未聚焦过的线程没有缓存，面板不显示状态。
