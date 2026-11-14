# 侧面板状态行预算（#61141 续，2026-11-14）

上一轮固定 3 行。本轮把行预算抽成 `paneStatusRowBudget(height)`：约为面板高度的三分之一，上限 4 行，且始终给 transcript 留至少一行（高度 1 时仍给状态 1 行）。`renderSideWorkspace` 把预算传给 `renderSidePaneTranscript`，chooser 左侧父面板与双面板布局使用同一预算。

回归新增 `TestSidePaneStatusRowBudgetLikeRust`：高度 1/2/6/12/40/0 分别得到 1/1/2/4/4/1；原有 `TestSidePaneStatusContentLikeRust` 适配新签名后通过。

`go vet ./tui/tea` 退出 0；`go test ./tui/tea -count=1` 见 `side_pane_status_budget_2026_11_14.log`。

剩余差异（记录，不宣称完成）：Rust 会把结构化 status 保留下来按主题和超链接重排，并在面板内同时为 draft/footer 预留行；Go 仍是纯文本 + dim，面板没有独立 draft/footer 区域。
