# TUI 侧栏面板与焦点审计（#61167，2026-11-14）

Rust ae50f97 只是把 `SidebarLayout::Chooser` 改名为 `Panel`，并把几何、transcript 可见性、焦点路由和快捷键提示统一走 panel helper；唯一行为变化是切换面板焦点时复用侧栏当前可见内容，而不是显式选择 `SidebarContent::Chooser`。

Go 侧没有这个枚举，面板内容由 `sidebarChooserOpen` 与 `activeSide.ShowingSide` 派生，不存在“强制切回 chooser”的路径：

- 几何：`sidePaneLayout`、`sidebarChooserPanelHeight`、`joinSidePanes`（side_panels.go / sidebar_chooser.go）。
- transcript 可见性：`renderSideWorkspace` 依据 chooser 可见性与焦点选择左侧/右侧 transcript。
- 焦点路由：`focusSidebarContent` 在 chooser 可见时关闭并交还主输入框，否则切换侧对话；`closeSidebarChooser` 保留当前侧栏内容；`workspaceSidebarFocused` 供快捷键 HUD 使用。
- 快捷键提示：`key_chord_hud.go` 依赖 `sidebarChooserVisible && sidebarChooserFocused`。

因此该项在本仓库无对应重命名需要，行为语义已被上述 helper 覆盖。回归 `go test ./tui/tea -run 'Sidebar|SidePane|SideWorkspace|SideConversation|Focus' -count=1` 通过（0.386s）。

#61141（未聚焦面板的多行 status line）仍是真实缺口：Go 的侧面板只渲染 transcript，没有 status 内容与行分配，与本次审计的布局重构不同。
