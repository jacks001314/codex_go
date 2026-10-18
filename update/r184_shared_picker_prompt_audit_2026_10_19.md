# Round 741: shared picker and prompt layout audit

Rust 依据：`c9d13e8c757cd330a572e9a39e365866069aef07`（`#46695`）。

Go 已有等价的共享 picker/prompt 基础：

- `picker_rows.go` 统一列宽、换行、选中行、单行渲染与高度测量；
- `selection_picker_layout.go` 统一分配 header、tabs、search、rows、side
  区域并避免过度分配；
- `SelectionViewRows` 统一按显示宽度截断；
- custom prompt 与 request-user-input 使用共享 footer/prompt 几何和窄屏策略。

验证：

- `go test ./tui/bottom_pane -run 'Picker|Selection|CustomPrompt|RequestUserInput' -count=1`
- `go test ./tui/chatwidget -run 'SelectionView|Picker|Prompt' -count=1`
