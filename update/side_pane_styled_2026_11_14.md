# 侧面板状态保留主题色（#61141 续，2026-11-14）

`renderStatusHeader` 现在同时缓存渲染后的主题状态行（含 plan 指示器）到 `paneStatusStyled`。侧面板渲染时若该行显示宽度不超过面板宽度，就直接用它并外层加 dim；超过面板宽度则回退到纯文本按宽换行 + 逐行 dim。这样短状态在未聚焦面板保留主题色，长状态仍能重排。

回归 `TestSidePaneStatusKeepsThemeWhenItFitsLikeRust`：40 列面板保留 `\x1b[36m` 主题色且整行被 dim 包裹；8 列面板不复用超宽主题行。既有内容/预算测试通过。

`go vet ./tui/tea` 退出 0；`go test ./tui/tea -count=1` 见 `side_pane_styled_2026_11_14.log`。

剩余差异：超宽状态在面板中仍丢失主题色（Rust 按结构化 span 重排并保留颜色），面板也没有独立 draft/footer 预留区。
