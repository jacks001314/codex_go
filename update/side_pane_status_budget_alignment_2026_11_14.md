# 未聚焦面板状态行预算对齐（#61141）

Rust d0edb0b1 的侧栏布局用 `FooterLayout` 分配底部行：先给 composer 保留行，再固定保留 1 行 hint，剩余空间才给状态行。Go 之前用 `clamp(height/3, 1, 4)`，在中等高度面板上会多留状态行、挤掉 transcript，而且在极矮面板上仍强行显示 1 行状态。

本轮把 Go 的实现改成与 Rust 相同的推导：

- `sidePaneComposerHeightLimit(height)` = `max(2/3·height, 8)` 且不超过 pane 高度。
- `sidePaneFooterCap(height)` = `clamp((limit+2)/4, 2, 4)`。
- `paneStatusRowBudget(height)`：先扣除 composer 行（`min(height-1, 3)`）得到可用行，再取 `min(cap, available) - 1`（那 1 行是保留的 hint 行），不足时为 0。
- `renderDimmedPaneStatus` 对 `maxRows == 0` 直接不渲染状态，避免在极矮面板上占用 transcript 的唯一一行。

对照 Rust 公式得到的状态行上限：高度 0/1/2 → 0，6/12 → 1，20 → 2，24/40 → 3。测试 `TestSidePaneStatusRowBudgetLikeRust` 改为这组期望值，其余状态换行、主题色与 reflow 用例保持不变。

验证：`go test ./tui/tea` 全包通过（47.5s），`go vet ./tui/tea` 退出 0；日志 `side_pane_status_budget_alignment_2026_11_14.log`。

仍未完成：Rust 在未聚焦面板的 composer 区绘制该线程的草稿文本，并在其下保留一行空白 hint；Go 的侧栏目前只绘制 transcript 与状态，草稿预览尚未接线，所以这里只是把预留行数对齐，面板底部还没有真正的草稿区。`#61141` 仍标为部分。
