# syncl1 · round86 · TUI 三连行为级比对（#49800 / #51458 / #50416）

车道：syncl1（Linux 节点）· 基线 `origin/main = 9cdcd43e5b676ffc2f81bb5d2d3058e17ff72c1e`
纪律：0 commit / 0 push / 0 tag / 0 ref move。三项均判**无载体（机制性 N/A）**，故无补丁交付。

## 0. 环境自检
- Go 仓：`/home/jacks/jacks_dev/codex_go`（remote `https://github.com/jacks001314/codex_go.git`）
- `git ls-remote origin main` = `9cdcd43e5b676ffc2f81bb5d2d3058e17ff72c1e  refs/heads/main`（与派单基线一致）
- Rust 仓：`/home/jacks/jacks_dev/codex`（origin `git@github.com:openai/codex.git`），已 `git fetch origin main`
- 主仓工作目录只读使用（`git grep`/`git show`/`git ls-tree`），未改任何文件。

## 1. 上游 sha 自解（台账 sha 列不可信，自行解析）
```
$ git -C /home/jacks/jacks_dev/codex log origin/main --grep '#49800' -F --format='%H %s' -1
ec0cfa5da8e9fac464b94cb36396904546eee4e8 Allow cleanup of replay-only side conversations with missing threads (#49800)
$ ... --grep '#51458' -F ...
858aea34491fec77cb5069548fc842d4a177a84a Make URLs clickable in user verification prompts (#51458)
$ ... --grep '#50416' -F ...
dff5270b298b7ee5fb1c47460a3d6e0d89985cad Clarify Git worktree choices for new and forked conversations (#50416)
```

## 2. #49800 — 无载体（机制性 N/A）

### Rust 改动（`ec0cfa5da8`，2 文件 +48/−13）
`codex-rs/tui/src/app/side.rs` `interrupt_side_thread`（:542）新增守卫（:553-562）：
`turn/interrupt`（或 `startup_interrupt`）返回 `TypedRequestError::Server{method=="turn/interrupt", code==-32600, message=="thread not found: {id}"}`
且 `thread_event_channels[thread_id].attachment() == ThreadEventAttachment::ReplayOnly` ⇒ `return Ok(())`。
仍然**继续发送** interrupt（replay-only 侧可能在线）。
配套 `codex-rs/tui/src/app/tests/session_lifecycle_requests.rs`（archive 生命周期 × {Live, ReplayOnly×2}）。

### Go 正向陈述
1. **Go 的侧会话关闭路径不含 `turn/interrupt`**：
   - 本地：`app/side_tui.go:149` `Close` → `:160` → `:211` `deleteSideLocked` → `:214` `MethodThreadDelete`。
   - 远端：`app/remote_tui.go:1238` `interactiveRemoteCloseSide` → `:1252` `MethodThreadUnsubscribe`。
   全仓 `git grep -n "TurnInterrupt" origin/main -- app/side_tui.go` = **0 命中**。
2. **守卫的操作数在 Go 里不存在**：Rust 检查 `thread_event_channels` 注册表中的 ReplayOnly 附着；Go 只有类型/方法，没有该注册表，且 `MarkReplayOnly()` **零生产调用方**：
```
$ git grep -n "MarkReplayOnly\|thread_event_channels\|ensureThreadChannel" origin/main -- '*.go'
origin/main:tui/app/thread_events.go:67:func (c *ThreadEventChannel) MarkReplayOnly() {
origin/main:tui/app/thread_events_test.go:105:	channel.MarkReplayOnly()
```
3. **Rust 修复的用户可见症状（关闭被阻断）在 Go 主路径上已不存在**：`tui/tea/side.go:350 returnFromSideConversation` 在调用 closer **之前**先 `:361 m.abandonSideThread(...)`，随后 `applySideCloseResult`（:372）对已 abandon 的侧会话直接返回，关闭失败只写一条 notice，不阻断“返回主线程”。
4. **唯一把关闭错误当致命的 Go 路径**是切换 agent：`tui/tea/agent.go:214-221`（closer 出错 ⇒ `AgentSwitchResultMsg{Err}` ⇒ 切换失败）。但那里可能拿到的错误只来自 `thread/delete`（本地）/`thread/unsubscribe`（远端），**不是**本 PR 的 `turn/interrupt` 机制；且 tea 层没有 replay-only 附着状态可判。

### 可复跑命令 + 实际输出（原文见上）
```
$ git grep -n "TurnInterrupt" origin/main -- app/side_tui.go        # 无输出（exit 1）
$ git grep -n "MarkReplayOnly" origin/main -- '*.go'
origin/main:tui/app/thread_events.go:67:func (c *ThreadEventChannel) MarkReplayOnly() {
origin/main:tui/app/thread_events_test.go:105:	channel.MarkReplayOnly()
```
### 结论
本 PR 的**具体机制（side close 里的 turn/interrupt + ReplayOnly 守卫）在 Go 无载体**。
旁注（供队长裁定，未开工）：Go 本地关闭是 `thread/delete`，对已缺失的 thread 记录会返回 `-32600 "thread not found: <id>"`（`appserver/router.go:2418/2427/2473`），
在 `tui/tea/agent.go:214` 这条切换路径上会让切换失败——与 Rust 症状同类但机制不同，且落在 `app/`（现属 syncw2），**未动**。

## 3. #51458 — 无载体

### Rust 改动（`858aea3449`，2 文件 +38/−4）
`codex-rs/tui/src/bottom_pane/user_verification.rs`：`prompt_header` 标题（:264）与 `request_details`（:286）改用 `terminal_hyperlinks::HyperlinkText`，使 URL 带 OSC8 终端超链接（含 waiting-for-verification 视图）。测试文件 `user_verification_tests.rs` 同步。

### Go 正向陈述
Go **没有** `bottom_pane/user_verification.rs` 的 TUI 载体：仓库里 `user_verification` 只存在于 **app-server 服务端 RPC 面**，TUI 侧零文件。
```
$ git ls-tree -r --name-only origin/main | grep -i user_verification
appserver/user_verification.go
appserver/user_verification_metadata_test.go
appserver/user_verification_runtime.go
appserver/user_verification_test.go
$ git grep -in "UserVerification" origin/main -- 'tui/*.go' 'tui/**/*.go'   # 无输出（exit 1）
$ git ls-tree -r --name-only origin/main | grep 'tui/bottom_pane/' | grep -i verif   # 无输出（exit 1）
```
Go TUI 的 URL 型 elicitation 走的是**另一个** Rust 模块的镜像 `tui/bottom_pane/app_link_view.go`（Rust `bottom_pane/app_link_view.rs`，本 PR 未触碰），且其 `linkContentLines`（:400 起）是纯文本行、未经过 hyperlink 包装。
超链接基础设施本身存在（`tui/terminal_hyperlinks.go`），但**没有** verification 提示视图可用它。
### 结论
**无载体**：Rust 改的渲染模块（`bottom_pane/user_verification.rs`，`prompt_header`/`request_details`）在 Go 不存在；无同源可迁移的落点。

## 4. #50416 — 无载体

### Rust 改动（`dff5270b`，4 文件 +14/−9）
`codex-rs/tui/src/chatwidget/worktree_picker.rs` **仅** `show_session_checkout_picker`（:22）两项文案：
`"Current checkout"` → `"Use current Git worktree"`（:49）、`"New worktree"` → `"Create new Git worktree"` + 新描述（:67）；
另 2 个 snapshot + `worktree_picker_tests.rs`。**未触碰**同文件 `show_managed_worktree_picker`（:87，`/worktree` 选择器）。

### Go 正向陈述
1. Go **没有** `show_session_checkout_picker` 的 `/new`·`/fork` 二选一选择器，文案不存在：
```
$ git grep -n "Use current Git worktree\|Create new Git worktree\|Current checkout\|New worktree\|Keep using the current working directory" origin/main -- '*.go'   # 无输出（exit 1）
```
2. Go 的 `/new`·`/fork` 不询问 checkout，直接执行：
```
tui/tea/model.go:7239  case codextui.CommandNew:  m.startFreshNamedSession(invocation.Args, "Started a new local thread.")
tui/tea/model.go:7277  case codextui.CommandFork: return m.applyForkCurrentSession(invocation.Args)
```
3. Go 确有的 `/worktree` 选择器（`tui/tea/worktree_browser.go:118-133`）文案为 `Continue current conversation` / `Start new conversation` / `Browse worktrees`——它镜像的是 Rust 的 `show_managed_worktree_picker`，**正是本 PR 没改的那一段**。
### 结论
**无载体**：本 PR 改的选择器在 Go 从未被移植；Go 已有的 worktree 选择器不在本 PR 的改动面内，改它不属于该上游等价面。

## 5. 未决问题
- 三条均为 `无载体` ⇒ 本单**无补丁、无 RC、无 apply-check**（无落点）。若队长认为应把「Go `/new`·`/fork` 缺 checkout 选择器」整体作为移植项（#43286 残面）立项，请裁定；那会是一个新 item，且落 `tui/tea/`（`model.go` / 新增 picker），不在本 PR 等价面内。
- #49800 的旁注（本地 `thread/delete` 的 `thread not found` 在 agent 切换路径致切换失败）落在 `app/`，与 syncw2 撞面，未动。
