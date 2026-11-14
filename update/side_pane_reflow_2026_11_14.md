# 结构化状态重排（#61141 续，2026-11-14）

`renderStatusHeader` 现在把 `bottompane.StatusLine.Spans` 转成带 SGR 的 `paneStatusSpan` 缓存（线程身份色、accent 色、dim 位都保留）。侧面板渲染优先使用这份结构化内容：`wrapPaneStatusSpans` 按显示宽度逐词换行，超长词按 rune 硬切，每行整体 dim、span 颜色在换行后仍然保留；行预算仍由 `paneStatusRowBudget` 控制。没有结构化缓存时退回“宽度足够就复用主题行、否则纯文本换行”的旧路径。

回归：
- `TestSidePaneStatusReflowsWithThemeLikeRust`：12 列下状态换成多行、每行 ≤12 显示宽度、带 dim 包裹，且 `\x1b[36m` 主题色在换行后仍存在。
- 原有内容、预算、主题行复用测试继续通过。

`go vet ./tui/tea` 退出 0；`go test ./tui/tea -count=1` 见 `side_pane_reflow_2026_11_14.log`。

剩余差异：plan 模式指示器仍以附加纯文本形式缓存（未进入 spans），面板内 draft/footer 预留区尚未实现。
