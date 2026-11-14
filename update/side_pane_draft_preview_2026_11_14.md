# 未聚焦面板草稿预览与固定 footer 对齐（#61141）

Rust `side_pane.rs` 的 `paint_companion` 把未聚焦半边拆成固定行：transcript（含空行分隔）→ 状态行 → 未聚焦会话的草稿预览（首行 `› `，后续行两空格缩进、整体 dim）→ 一行空白 hint。Go 之前的 `renderSidePaneTranscript` 把状态直接拼进 transcript，草稿预览完全没有绘制，tab strip 也只是拼在 transcript 文本前面，tail 窗口下会被滚出可视区。

本轮把侧栏渲染改成同一套行分配：

- 新增 `sidePaneFrameFor(height)`：提示行固定 1 行，composer 取 `min(height-1, 3)`，状态行沿用 `paneStatusRowBudget`，transcript 拿走剩下的行并保留 1 行分隔；空间不足时先压缩 composer，再放弃 hint 行。
- 新增 `sidePaneDraftPreviews(sideFocused)`：只预览失去焦点那一侧的 `ParentDraft` / `SideDraft`；聚焦侧继续使用真实 composer，避免把旧草稿重复画进面板。
- `composeSidePane` 按 frame 拼出 windowed transcript、dim 状态、dim 草稿预览，并补空行到面板高度；`zipSidePanes` 按高度逐行拼接左右两栏。
- `renderSidePaneContent` 从 `renderSidePaneTranscript` 中拆出，负责纯 transcript；chooser 路径仍用原来的 `joinSidePanes`。
- tab strip 通过 `prependSidePaneRow` 放在侧栏第一行，面板高度保持不变，不再覆盖 transcript 内容。

回归 `TestSidePaneDraftPreviewFrameLikeRust` 断言 12 行面板输出恰好 12 行、最后一行是空白 hint、草稿行带 `› !echo draft` 与 `\x1b[2m`、状态行位于草稿上方；`TestSidePaneDraftPreviewsOnlyUnfocusedSideLikeRust` 断言只有未聚焦一侧返回草稿预览。

验证：`go vet ./tui/tea` 退出 0，`go test ./tui/tea -count=1` 通过（47.5s），日志 `side_pane_draft_preview_2026_11_14.log`。状态行 reflow/主题色与行预算的既有用例继续通过。

至此 #61141 的两处缺口（多行结构化状态、footer 行分配与草稿/提示保留）都已接线；状态 span 内容本身仍由既有 `paneStatusSpans` 提供，plan-mode 指示器是否进入 spans 属于状态内容来源的后续工作，不在本 PR 的布局范围内。
