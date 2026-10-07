# syncl3 · r87c · TUI 候选包 triage + 落地（基线 ee2d4e1d）

派单：`msg-1791376857353199600-5255`。基线 `ee2d4e1db819e1d706eaea8b5da0386e85ea1a42`（integ86g）。
纪律：0 commit / 0 push / 0 ref；worktree detached `/tmp/wt-r87c`、`/tmp/wt-r87c-base`（均 @ee2d4e1d）。

## 0. 环境自检
- Go 仓 `/home/jacks/jacks_dev/codex_go`；派单基线 `origin/main` = `ee2d4e1d`（sync640）。
  落地/门禁复跑时 `origin/main` 已前进到 **`8b2453a9`（sync641 `#49855`，Windows 提权启动告警，与 TUI 零交集）**：
  补丁 `git apply --check` 打 `ee2d4e1d` 与打 `8b2453a9` **均 exit 0**（两条均已实测）。
- Rust `/home/jacks/jacks_dev/codex`；`origin/main` = `b17c74cfd5ebb39fe70ffaff78de198120278636`；检出 HEAD `5b0b253035`（未动，parity 用）。
- 六个 sha 自解（`git log --grep '#<PR>' -F origin/main`）：
  - #50431 `b6903c0669df044e4a089dab949fd44d54aa9355`（3 文件）
  - #50786 `acf9818faef932ad0b26049c6f7f8b1acd94149b`（13 文件）
  - #49861 `8ea2428c38f8994e18d789669f5cbc5df75e1f17`（10 文件）
  - #49564 `0b43721d8d1f734658e41bffe12a6ba6c9240abd`（5 文件）
  - #48761 `e75b6b1e0c87b13b36ff552d6201755107553cce`（12 文件）
  - #50781 `f365d5754bb643983079676c29e8f7562399019e`（5 文件）

---

## 1. 已落地：#50431（agents overview 预览的终端超链接）

补丁 `update/r86_patches/syncl3_50431.patch` — **4914 B**，sha256
`4ef772a031b89ef6a97544ef2cca786be3c7e9cb48d7fe445493200dc8996cf7`，2 文件（1 源码 + 1 回归测试）。
`git apply --check -v`（打干净 `ee2d4e1d`）：`Checking patch tui/agents_overview/hyperlink_preview_like_rust_test.go... / Checking patch tui/agents_overview/style.go...` → **exit 0**。

### 1.1 triage 更正（syncw2 的前提不成立）
syncw2 报「现有 `RenderMarkdown func(text,width) []string` + span{text,raw} **无 link target/OSC8**」——不成立。Go 侧 markdown 渲染器**已经**输出 OSC-8：
`tui/tea/agents_overview.go:194` 注入 `markdown.RenderWithThemeCwd`，而 `tui/markdown/render.go:344-364` 的
`annotateWebLinkLabels` / `annotateLocalFileLinks` 会把链接目标包成 `OSC8Hyperlink`（见 `tui/terminal_hyperlinks.go:90`）。
实测（probe，`markdown.RenderWithThemeCwd("see [the docs](https://example.com/a/very/long/path/that/wraps) now", 40, "", "")`）：
`"see \x1b]8;;https://example.com/a/very/long/path/that/wraps\athe docs\x1b]8;;\a"` → **OSC-8 已在**。

### 1.2 真正的缺口（值级）
`tui/agents_overview/style.go:85-87` 的 `ansiAwareWidth` 只剥离 SGR（`stripANSISGR` 仅处理 `\x1b[`），OSC-8 的
目标串被当作可见宽度计数，随后 `truncateSpans`（`:174-198`）对 `raw`（预渲染 markdown）行直接 `break` ⇒ **整行被丢弃**。
probe 原文（生产函数级）：
```
ansiAwareWidth("\x1b]8;;https://example.com/docs\x07docs\x1b]8;;\x07 and more text") = 50   // 可见宽 22
renderLine("", []span{{text: osc, raw: true}}, 40, true)  = ""    // 行被丢弃
renderLine("", []span{{text: "docs and more text longer than before ok", raw: true}}, 40, true) = 保留（对照）
```
另有 plain 通道泄漏：`stripANSISGR` 不剥 OSC-8，`view.Render()`（plain）会带出超链接转义。

### 1.3 落地与门禁
- 落点：`tui/agents_overview/style.go:60-108`（新增 `stripOSC8Hyperlinks` / `stripTerminalEscapes`，`ansiAwareWidth` 与 plain 渲染分支改用后者）。
- 回归（仓内）`tui/agents_overview/hyperlink_preview_like_rust_test.go`：详情面板保留链接文本与目标；plain 渲染无 OSC-8；**省略号行不带链接**；超宽 raw 行不会被切断转义序列。
- 门禁：`gofmt -l` 空 / `go build ./...`=0 / `go vet ./tui/agents_overview/`=0 /
  `go test ./tui/agents_overview/ ./tui/ ./tui/tea/ -count=1` 失败集合 = 基线（`TestModelAppCommandUsesRustHistoryMessages`、`TestSelectStartupTooltipMatchesRustPlanBranches`）⇒ **新增失败 0** / parity `ok codex_go/parity`（无需 re-vendor）。
- **值级 RC**（`style.go` 改动前/恢复后 sha256 均为 `cfc9e2e10817d68efc0016bb3685fc43e3b4a9971f49840919726cbb608af23b`）：
  用 python 断言式替换把 `style.go:119`（`ansiAwareWidth`）与 `style.go:263`（plain 渲染分支）的 `stripTerminalEscapes(` 退回 `stripANSISGR(`
  （`assert lines[i] ...` 两处，生产接线，非 build break）⇒
  `go test ./tui/agents_overview/ -run Hyperlink -count=1` 原文：
  ```
  --- FAIL: TestTaskDetailsKeepsPreviewHyperlinksLikeRust (0.00s)
      hyperlink_preview_like_rust_test.go:19: details pane dropped hyperlink text:
  FAIL
  FAIL   codex_go/tui/agents_overview    0.002s
  ```
  恢复两处调用 ⇒ `sha256` 回到 `cfc9e2e1…`（与撤除前一致）⇒ 该包 `-count=1` 全绿（`ok … 0.028s`）。

---

## 2. 只读 triage（未落地）

### 2.1 `#49564`（markdown copy：把选中路径按纯文本复制）→ **无载体**
Rust 改 `markdown_copy.rs` / `markdown_copy/table.rs` / `markdown_render.rs`（owned-transcript 选区→markdown 的 source-text 映射）。
Go：`grep -rni "markdown_copy|markdowncopy|copySelection" --include='*.go' .` → **0 命中**；
`tui/clipboard_copy.go` 的 `ClipboardCopySequence` 全仓**唯一消费者是测试**（`tui/clipboard_copy_test.go`、`tui/rust_parity_test.go`）；
`tui/app/copy_on_select.go` 讲的是终端级 copy-on-select（`CopyOnSelectForTerminal`），与 Rust 的选区→markdown 无关。
⇒ 宿主子系统（transcript 选区 + source-text 映射）在 Go 不存在，与上一轮 `#48775`/`#50389` 同类。

### 2.2 `#50781`（MCP 启动通知限定到 owned thread）→ **N/A（宿主路径未接线）—— 裁定：syncl5 的方向对，syncw2 的落点前提不成立**
- syncw2 的落点 `tui/app/app_server_events.go:100/150 RouteServerNotification`：这些函数**没有任何生产调用点**——
  `grep -rn "HandleServerNotificationEventDecision\|RouteServerNotification(" --include='*.go' . | grep -v _test` 只回定义本身
  （`app_server_events.go:75/100/150`），其余全部命中 `app_server_events_test.go`。整文件是「Rust parity subset」库。
- `NewThreadEventChannel`（`tui/app/thread_events.go:53/60`）同样只有 `thread_events_test.go` 消费者；Go 侧**没有**
  `thread_event_channels` / `agent_navigation` / `side_threads` 这套按线程登记表的活代码（`grep` 仅命中 `tui/app/event_dispatch.go:141` 的一个入参）。
- 活 TUI（`tui/tea`）的 MCP 启动状态是**单轮**状态 `chatwidget.NewMcpStartupRoundState`（`tui/tea/model.go:2068`），
  不存在「未跟踪线程的通知创建事件通道」这条链路 ⇒ Rust 的缺陷（无关线程的 MCP 启动通知可打开事件通道 → 后续审批串台）在 Go **没有宿主**。
- 另外 syncl5 引用的 `tui/app_server_session.go` 是 10 行 stub（`AppServerSessionState`）——是事实，但它并不是该缺陷的载体；两条前提都需修正。
⇒ 结论：**N/A（无生产路由面）**。若要我按 syncw2 的落点补，产出会是死代码，按 AGENTS 规则不做。

### 2.3 `#50786`（跨启动记住 Command Center 分组）→ **真缺口，且需配置读写面**
- Go：`tui/agents_overview/overview.go:848` `ToggleGrouping()` 只改内存（`v.State.Grouping = v.State.Grouping.Next()`），
  `tui/tea/agents_overview.go:424` 直接调用，**无持久化/无启动恢复**（`grep` 无 `tui.agents_overview_grouping` 之类键）。
- Go 有现成的设置写入通道可复用：`m.onWriteSettings`（`tui/tea/experimental_features.go:167`、`tui/tea/memories.go:197`、`tui/tea/model.go:1051-1060`），
  以及 `tui/app/config_persistence.go`。
- 规模估计：`tui/agents_overview/overview.go`（Grouping 的 wire id 解析）+ `tui/tea/agents_overview.go`（toggle→持久化 + 失败提示）+ 启动恢复
  （settings 读取，可能落到 `tui/tea/model.go` / `config/` 读取面）+ 回归测试 ⇒ **4-6 文件，且可能触及 `config/` 读取面**（`config/` 上一轮属 syncl6）。
  ⇒ **超 ≤5 边界 + 需你裁定是否允许我动 config 读取面**，本轮不擅自开工。

### 2.4 `#49861`（状态行/终端标题加 Daybreak 项）→ **真缺口，卡在 ≤5 边界，请批**
- Go 状态面已具备同构骨架：`tui/bottom_pane/status_line_setup.go:101/161/222/265`、`status_line_style.go:41/193`、
  `status_surface_preview.go:40/98/252`、`title_setup.go:31/133`、`tui/chatwidget/status_surfaces.go:41/73/215/275/323`（均有 fast-mode 对位）。
- 数据源在 Go **已存在**：`session/store.go:227 DaybreakEnabled *bool`、`appserver/protocol.go:715 DaybreakEnabled *bool`；
  但 `grep -rn DaybreakEnabled --include='*.go' tui/` → **0** ⇒ 状态面尚未接该字段；Rust 侧语义是
  `daybreak_enabled && !side_conversation_active ⇒ "Daybreak on" else "Daybreak off"`（`status_surfaces.rs:815-822`）。
- 规模：上述 5 个 source 文件 + 1-2 个测试 ⇒ **≈5-7 文件**，我判断**需要你批**（且要确认 Go 侧「side conversation 中显示 off」的对位字段在哪）。

### 2.5 `#48761`（compact activity 隐藏行数 + 配置化快捷键）→ **部分已等价 + 残余真缺口，建议单独立项**
- 已等价部分：Go 已有隐藏行计数与提示 `tui/exec_cell/render.go:413-414 OutputEllipsisText`（`… +N lines (…)`，调用点 `:71/:356/:381`）
  与 `tui/history_cell/patches.go:61`（`… +N lines (ctrl+t to view transcript)`）。
- 缺口：① 提示里的键**硬编码** `transcriptHint = "ctrl+t to view transcript"`（`tui/exec_cell/render.go:19`），Rust 用**配置的 `open_transcript` 绑定**；
  ② 未实现「放不下或该 action 未绑定 ⇒ 省略提示」；③ 未区分「保留但被截断」与「存储已丢弃」的行数（Rust `ActivityDisclosure::OutputLines` 只数 revealable）。
- Rust 面 12 文件、含 layout 缓存失效与 snapshot；Go 侧最小切片（①②）约 2-4 文件，但③需要引入 storage 截断信息（`history_cell` 链路），
  ⇒ 建议按工单立项（可先做①②），**本轮不硬做**（与你「很可能 L/N/A，先判不要硬做」一致）。

---

## 3. 未决 / 待你裁定
1. `#50786`：是否允许我动 `config/` 读取面（或你指认 Go 的 settings 读取落点）？否则无法做启动恢复。
2. `#49861`：是否批准 ≈5-7 文件的落地（含状态面 + 标题面 + preview）？
3. `#48761`：是否立为工单（先做「配置化 open_transcript 键 + 未绑定/放不下省略」切片）？
4. `#50781`：建议按「N/A（宿主未接线）」结案；若你要的是「把 parity 库接到生产」，那是架构决策，请另立单。
5. 其余 `#49564` 建议 N/A 结案。
