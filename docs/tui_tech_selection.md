# TUI 技术选型 ADR

状态：Accepted
日期：2026-07-07
目标：让 Go 版 Codex 的交互入口追平 Rust `tui`，并保留可测试、可回退的核心状态层。

## Rust 基线

Rust TUI 的核心组合是：

| 能力 | Rust 实现 |
| --- | --- |
| 终端后端和布局渲染 | `ratatui` + `CrosstermBackend` |
| raw mode、alternate screen、focus、bracketed paste、事件流 | `crossterm`，启用 `bracketed-paste`、`event-stream` |
| 异步事件和 frame request | `tokio`、`tokio-stream`、`FrameRequester` |
| 组件体系 | `app`、`chatwidget`、`bottom_pane`、`selection_list`、`resume_picker`、`theme_picker`、`status_indicator_widget` |
| Markdown、代码和宽度处理 | `markdown_*`、`syntect`、`two-face`、`unicode-width`、`unicode-segmentation`、`textwrap` |
| 终端测试 | `vt100`、`insta`、`pretty_assertions` |
| 系统能力 | clipboard、webbrowser、terminal detection、terminal title、terminal hyperlinks |

关键源码对照：

- `D:\qax\reagent\dev\codex-main\codex-rs\tui\Cargo.toml`
- `D:\qax\reagent\dev\codex-main\codex-rs\tui\src\lib.rs`
- `D:\qax\reagent\dev\codex-main\codex-rs\tui\src\tui.rs`

Rust 不是简单行式 REPL，而是一个有原始终端模式、独立事件循环、可组合 widget、modal/picker、app-server local/remote session、终端 snapshot 的产品级 TUI。因此 Go 侧不能只扩展当前 line prompt。

## 候选方案

| 方案 | 优点 | 风险 | 结论 |
| --- | --- | --- | --- |
| Bubble Tea + Bubbles + Lip Gloss + Glamour | Go 生态成熟；事件循环、model/update/view 和 Rust TUI 的 app/event/render 分层接近；组件和样式可组合；适合渐进替换当前行式入口 | 需要新增依赖；terminal raw mode 细节要做 Windows/Linux smoke；复杂布局仍需自研组件 | 推荐 |
| tcell + tview | 终端底层能力强；现成控件多 | 更偏表单/控件框架，复杂 transcript、composer、modal、streaming render 与 Rust 自定义 widget 对齐成本较高 | 不选主线，可作为底层参考 |
| 手写 `x/term` raw mode + ANSI renderer | 依赖最少；完全可控 | 会重复造 `crossterm/ratatui` 同类能力，paste、resize、focus、鼠标、Windows 兼容、测试后端成本高 | 不选 |
| 保留当前行式 `internal/app` interactive | 已可运行；无新增依赖 | 无法对齐 Rust 的 transcript、composer、modal、picker、remote TUI 和 snapshot | 只保留为 fallback/test harness |

## 决策

采用 Bubble Tea 技术栈作为 Go TUI adapter：

- `github.com/charmbracelet/bubbletea`：root terminal event loop，对应 Rust `run_ratatui_app`、`TuiEventStream`、frame request 主循环。
- `github.com/charmbracelet/bubbles`：textarea、viewport、spinner、list 等基础组件，对应 composer、transcript、selection picker。
- `github.com/charmbracelet/lipgloss`：样式、边距、颜色、宽度，对应 Rust `style`、`color`、`width`、`render`。
- `github.com/charmbracelet/glamour`：Markdown 渲染起点，对应 Rust `markdown_render`。代码高亮和 diff 渲染后续按 Rust 行为补 fixture。
- `github.com/atotto/clipboard` 或平台原生小封装：clipboard 能力后置到 P1，优先完成 transcript/composer/modal。

当前 `internal/tui` 保持为无框架核心层，不直接依赖 Bubble Tea。它负责：

- transcript/message/status/options 的纯状态模型；
- slash command parser；
- 可 golden/snapshot 的文本渲染；
- local 和 remote TUI 共用的状态转换语义。

新增 Bubble Tea adapter 应放在 `internal/tuiapp` 或 `internal/tui/tea`，由 `internal/app/interactive.go` 启动。这样可以把“协议和状态对齐”与“终端框架细节”拆开测试。

## 模块拆分

| Go 模块 | 责任 | Rust 对照 |
| --- | --- | --- |
| `internal/tui` | 纯状态、命令、可测渲染、snapshot fixtures | `app_event`、`slash_command`、`status`、部分 `render` |
| `internal/tui/tea` | Bubble Tea root model、Update/View、terminal lifecycle | `tui.rs`、`run_ratatui_app` |
| `internal/tui/components` | composer、transcript viewport、status/footer、bottom pane、picker、modal | `chatwidget`、`bottom_pane`、`selection_list`、`resume_picker` |
| `internal/tui/markdown` | Markdown、code block、diff/file change render | `markdown_*`、`diff_render`、`exec_cell` |
| `internal/tui/session` | local embedded app-server session 和 remote app-server session adapter | `app_server_session`、`session_resume` |
| `internal/tui/testutil` | deterministic terminal tests、golden fixtures、keyboard scripts | `vt100`、`insta`、`test_backend` |

## 分阶段实现

1. 依赖引入闸门：新增 Bubble Tea 相关依赖，跑 `go mod tidy`、`go list -buildvcs=false ./...`、`go test ./internal/tui ./internal/app -count=1`。
2. Root model：实现 Bubble Tea root，支持 alternate screen、resize、focus、quit、error display，仍使用当前 `internal/tui.State`。
3. Composer 和 transcript：textarea + viewport，支持多行输入、paste、历史滚动、streaming assistant message。
4. Bottom pane 和 modal：approval、permission request、MCP elicitation、request_user_input、model/session picker。
5. Session adapter：local embedded app-server 与 turn runtime 打通，保留行式 interactive 为 fallback。
6. Remote TUI：支持 `ws://`、`wss://`、`unix://`、auth token env、connection file watch。
7. Snapshot/golden：补键盘脚本、布局、状态提示、approval、MCP elicitation、error display、Windows/Linux smoke。

## 验收标准

- `codex` 默认交互入口进入 Bubble Tea TUI，行式入口仅作为 fallback 或测试注入。
- `codex --remote` 交互入口能够连接 app-server，并与 local TUI 共用状态和协议语义。
- TUI 能覆盖 Rust 当前核心体验：transcript、streaming、composer、slash commands、approval、MCP elicitation、model/session picker、resume/fork、interrupt/cancel/continue。
- 默认测试不依赖真实终端；真实终端 smoke 通过 gated 环境变量启用。
- Windows 和 Linux 至少各有一个 raw terminal smoke，覆盖 resize、paste、Ctrl+C、Ctrl+D/quit、terminal restore。

## 参考链接

- Bubble Tea: https://github.com/charmbracelet/bubbletea
- Bubbles: https://github.com/charmbracelet/bubbles
- Lip Gloss: https://github.com/charmbracelet/lipgloss
- Glamour: https://github.com/charmbracelet/glamour
- tcell: https://github.com/gdamore/tcell
- tview: https://github.com/rivo/tview
