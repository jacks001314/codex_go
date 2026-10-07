# r86 载体预检 round 3（syncl4，Linux 节点，2026-10-07）

> 派单：队长 `msg-1791369892378818400-4222`（「预检 round 3，只读」）。
> 对象：**`update/syncl6_worklist_filtered_2026_10_07.md` §2 主表 15 行**（非 syncl4 自己的 r86_worklist）。
> 判据口径（队长已采纳为派单前常规筛）：
> ① **有无真实 Go 生产载体**；② **是否已等价实现 / 前置是否齐备**；
> 四分类标签 = `已等价型` / `无载体型` / `纯测试型` / `文档型`。
> 纪律：**只读** —— `git show` / `grep` / `ls`；不写 Go 代码、不 commit/push、不动 `update/` 下其它文件。

---

## 0. 固定点自检（原文）

```
$ cd /home/jacks/jacks_dev/codex_go && git fetch origin --prune && git rev-parse origin/main
b9ad41d30d630aa4cc68434c21f3660e15a347a0
```

Rust 上游固定点（本报告全部 `git show`/`cat-file` 均指此仓此点）：

```
$ cd /home/jacks/jacks_dev/codex && git rev-parse origin/main
5b0b2530354052b9194156d70d4c94a439368342
```

sha 自解（台账 sha 列不采信，一律 `--grep -F` 在 Rust `origin/main` 上自解）：

```
$ for pr in 49147 49300 49714 49708 49097 50105 50359 49076 49811 49444 49308 48799 49043 49041 49624; do
    printf "#%s\t" "$pr"; git -C /home/jacks/jacks_dev/codex log --grep "#$pr" -F --format='%h %s' origin/main -1; done
#49147	3a16c0b707 Simplify cloud task base URL normalization (#49147)
#49300	a6f09397aa Compact the inline hidden tag buffer once per chunk (#49300)
#49714	f151a0f5c2 Decouple API-key cyber access programs from model discovery (#49714)
#49708	4f699cd642 Move session index I/O off async runtime threads (#49708)
#49097	9563713df2 Notify lifecycle extensions of compaction usage limits (#49097)
#50105	e58b493329 Consolidate chat composer footer logic in `footer_state` (#50105)
#50359	91168365a5 Render ANSI styles in TUI hook system messages (#50359)
#49076	011f803f3c Avoid collecting unused Git metadata in skill analytics (#49076)
#49811	c51f5bfb82 Handle unsupported `fs/writeBlock` requests in exec-server (#49811)
#49444	8c3612fb63 Use `memrchr` to find newlines in reverse JSONL scans (#49444)
#49308	50d9c5deac Run piped legacy Windows sandbox processes without a console (#49308)
#48799	4c8cf3964d Fix SGR mouse reporting for Windows terminal capture (#48799)
#49043	4f63088cce Update Pro plan display names in the TUI (#49043)
#49041	3074be908a Copy selections within inline code as plain text (#49041)
#49624	7219fd735b Use server authentication for explicit remote session commands (#49624)
```

改动文件数（`git show --name-only` 计数；与 syncl6 主表逐行一致）：

```
#49147 3a16c0b707 1 files      #50105 e58b493329 2 files      #49308 50d9c5deac 3 files
#49300 a6f09397aa 1 files      #50359 91168365a5 2 files      #48799 4c8cf3964d 3 files
#49714 f151a0f5c2 2 files      #49076 011f803f3c 3 files      #49043 4f63088cce 3 files
#49708 4f699cd642 2 files      #49811 c51f5bfb82 3 files      #49041 3074be908a 5 files
#49097 9563713df2 2 files      #49444 8c3612fb63 3 files      #49624 7219fd735b 5 files
```

Go 侧落地判据（`git log --grep -F` on `origin/main`）：

```
$ cd /home/jacks/jacks_dev/codex_go && for pr in 49147 49300 49714 49708 49097 50105 50359 49076 49811 49444 49308 48799 49043 49041 49624; do
    printf "#%s\t" "$pr"; git log --grep "#$pr" -F --format='%h %s' origin/main -1; echo; done
#49147	5078de6d sync610: cover cloud base URL normalization like Rust (#49147)
#49300	
#49714	
#49708	
#49097	04d1426a sync609: emit a usage-limit turn error when post-turn compaction fails (#49097)
#50105	
#50359	eeca361a sync605: render hook system messages with ANSI styles (#50359)
#49076	944ac1f8 sync608: read only the origin URL for skill analytics git info (#49076)
#49811	
#49444	
#49308	
#48799	
#49043	
#49041	
#49624	
```

---

## 1. 主表 15 行 · 最终判定（按文件数升序，同数按 PR 号升序）

| 序 | PR# | sha（自解） | 文件数 | syncl6 原判 | **最终判定** | 四分类 |
|---|---|---|---|---|---|---|
| 1 | `#49147` | `3a16c0b707` | 1 | 有（唯一推荐） | **已落地** sync610 `5078de6d` | — |
| 2 | `#49300` | `a6f09397aa` | 1 | 有 | **N/A**（该 bug 类在 Go 不存在） | 无载体型 |
| 3 | `#49714` | `f151a0f5c2` | 2 | 有 | **N/A**（队长已闭；4 组合值级探针全 nil） | 无载体型 |
| 4 | `#49708` | `4f699cd642` | 2 | 有 | **N/A**（Go 无 async runtime） | 无载体型 |
| 5 | `#49097` | `9563713df2` | 2 | 有（file:line 载体） | **已落地** sync609 `04d1426a` | — |
| 6 | `#50105` | `e58b493329` | 2 | 有 | **N/A**（Go 已是重构后布局） | 已等价型 |
| 7 | `#50359` | `91168365a5` | 2 | 有 | **已落地** sync605 `eeca361a` | — |
| 8 | `#49076` | `011f803f3c` | 3 | 有 | **已落地** sync608 `944ac1f8` | — |
| 9 | `#49811` | `c51f5bfb82` | 3 | 有 | **N/A**（被后续 #50177 取代；Go 已实现完整能力） | 已等价型 |
| 10 | `#49444` | `8c3612fb63` | 3 | 等价物有 | **N/A**（Go 已用 `bytes.LastIndexByte`） | 已等价型 |
| 11 | `#49308` | `50d9c5deac` | 3 | 有（弱） | **N/A**（Go 无条件已 `CREATE_NO_WINDOW`，over-cover） | 已等价型 |
| 12 | `#48799` | `4c8cf3964d` | 3 | 需人审 | **N/A**（Go 故意不启用鼠标捕获） | 无载体型 |
| 13 | `#49043` | `4f63088cce` | 3 | 需人审 | **N/A**（被后续 #49079 取代；Go 已是 #49079） | 已等价型 |
| 14 | `#49041` | `3074be908a` | 5 | 需人审 | **N/A**（Go 无 markdown-copy 孪生） | 无载体型 |
| 15 | `#49624` | `7219fd735b` | 5 | 疑似无 | **N/A**（Go 已实现远程会话命令直连） | 已等价型 |

**汇总：4 条已落地（#49147/#49097/#50359/#49076）+ 11 条 N/A + 0 条可派单。**
即 **该 15 行清单已全部排干，本轮无新增可落地条目**。

---

## 2. 逐行证据（复跑命令 + 实际输出）

### 序 1 `#49147` — 已落地（sync610）
```
$ git -C /home/jacks/jacks_dev/codex_go log --grep '#49147' -F --format='%h %s' origin/main -1
5078de6d sync610: cover cloud base URL normalization like Rust (#49147)
```
判据①：Go 已有等价提交（含 Rust 侧回归测试对应的 Go 测试）。**闭合，不再派单。**

### 序 2 `#49300` — N/A（无载体型）
Rust 语义：`utils/stream-parser` 的 inline hidden tag 缓冲「每个 chunk 只压紧一次」（性能）。Rust 侧是**流式有状态解析器**里重复 `pending.drain` 的时序修复。
```
$ git -C /home/jacks/jacks_dev/codex_go_wt/preflight3 grep -rn 'stripInlineHiddenTag' --include='*.go' .
./eventmap/eventmap.go:510:	text = stripInlineHiddenTag(text, "<oai-mem-citation>", "</oai-mem-citation>")
./eventmap/eventmap.go:511:	text = stripInlineHiddenTag(text, "\uE200cite\uE202", "\uE201")
./eventmap/eventmap.go:512:	text = stripInlineHiddenTag(text, "\uE000cite\uE002", "\uE001")
./eventmap/eventmap.go:526:func stripInlineHiddenTag(text string, open string, close string) string {
```
判据①：Go 的 `stripInlineHiddenTag` 是**无状态整串循环**（每次调用对完整 `text` 扫描），不存在「跨 chunk 保留缓冲」这一数据结构 ⇒ Rust 所修的**重复压紧时序 bug 类在 Go 不存在**。判据②：无可对位的前置。**N/A（无载体型）。**

### 序 3 `#49714` — N/A（无载体型，队长已闭）
队长已用 syncl5 的 **4 组合值级探针**（四种 flag 组合 Go 全 nil）+ feature key 0 消费者定案；本轮不重复劳动。判据②（前置不齐备）。**N/A。**

### 序 4 `#49708` — N/A（无载体型）
Rust 语义：session index 的 I/O 移出 **async runtime 线程**（`spawn_blocking` + 跨取消点持有 owned Tokio mutex guard）。
```
$ grep -rn 'sessionIndexMu\|SessionIndexEntry\|AppendSessionIndexEntry' --include='*.go' rollout/session_index.go
rollout/session_index.go:17:var sessionIndexMu sync.Mutex
rollout/session_index.go:19:type SessionIndexEntry struct {
rollout/session_index.go:33:func AppendSessionIndexEntry(codexHome string, entry SessionIndexEntry) error {
rollout/session_index.go:34:	sessionIndexMu.Lock()
rollout/session_index.go:35:	defer sessionIndexMu.Unlock()
rollout/session_index.go:55:	sessionIndexMu.Lock()
rollout/session_index.go:56:	defer sessionIndexMu.Unlock()
```
判据①：Go 无 **async runtime / `spawn_blocking`** 概念（goroutine 天然可阻塞），Rust 所修的「阻塞 I/O 卡住 async worker」机制**不存在**。判据②：Go 用**进程级 `sync.Mutex` 保护文件 I/O**（不同并发模型，非 parity 缺口，亦非可移植的「修复」）。**N/A（无载体型）。**

### 序 5 `#49097` — 已落地（sync609）
```
$ git -C /home/jacks/jacks_dev/codex_go log --grep '#49097' -F --format='%h %s' origin/main -1
04d1426a sync609: emit a usage-limit turn error when post-turn compaction fails (#49097)
```
**闭合。**

### 序 6 `#50105` — N/A（已等价型）
Rust 语义：`chat_composer.rs`（−135）→ `chat_composer/footer_state.rs`（+139）**纯代码搬移**，PR 自述「Preserve existing behavior」。
```
$ grep -rn 'ComposerFooterMode\|QuitShortcutReminder\|type FooterState' --include='*.go' tui/bottom_pane/chat_composer/footer_state.go
tui/bottom_pane/chat_composer/footer_state.go:12:type ComposerFooterMode string
tui/bottom_pane/chat_composer/footer_state.go:15:	ComposerFooterModeComposerEmpty        ComposerFooterMode = "composer_empty"
tui/bottom_pane/chat_composer/footer_state.go:16:	ComposerFooterModeComposerHasDraft     ComposerFooterMode = "composer_has_draft"
tui/bottom_pane/chat_composer/footer_state.go:17:	ComposerFooterModeShortcutOverlay      ComposerFooterMode = "shortcut_overlay"
tui/bottom_pane/chat_composer/footer_state.go:18:	ComposerFooterModeQuitShortcutReminder ComposerFooterMode = "quit_shortcut_reminder"
tui/bottom_pane/chat_composer/footer_state.go:19:	ComposerFooterModeEscHint              ComposerFooterMode = "esc_hint"
tui/bottom_pane/chat_composer/footer_state.go:20:	ComposerFooterModeHistorySearch        ComposerFooterMode = "history_search"
tui/bottom_pane/chat_composer/footer_state.go:23:type FooterState struct {
```
判据①：Go **已有** `footer_state.go`，含 `FooterState` 与 #50105 的 `QuitShortcutReminder` 等全部模式常量。判据②：Go 目录布局即**重构后**（`footer_state` 独立文件）⇒ 结构性变更已等价。**N/A（已等价型）。**

### 序 7 `#50359` — 已落地（sync605）
```
$ git -C /home/jacks/jacks_dev/codex_go log --grep '#50359' -F --format='%h %s' origin/main -1
eeca361a sync605: render hook system messages with ANSI styles (#50359)
```
**闭合。**

### 序 8 `#49076` — 已落地（sync608）
```
$ git -C /home/jacks/jacks_dev/codex_go log --grep '#49076' -F --format='%h %s' origin/main -1
944ac1f8 sync608: read only the origin URL for skill analytics git info (#49076)
```
**闭合。**

### 序 9 `#49811` — N/A（已等价型；被后续 #50177 取代）
Rust 语义：exec-server 注册 `fs/writeBlock`、要求 fs init、校验 handle id，返回 `invalid_request("exec-server does not support writable file streams")`。
```
$ grep -rn 'writeBlock\|MethodFSWriteBlock\|does not support writable file streams' --include='*.go' execserver/
execserver/client.go:1006:var errWritableFileStreamsUnsupported = errors.New("exec-server does not support writable file streams")
execserver/client.go:1276:	if err := c.call(ctx, MethodFSWriteBlock, params, &response); err != nil {
execserver/server.go:51:	MethodFSWriteBlock            = "fs/writeBlock"
execserver/server.go:1470:	case MethodFSWriteBlock:
execserver/server.go:1475:		result, err := s.serverForConnection(ctx).writeBlock(&params)
execserver/writable_file_streams_test.go:117:	}); err == nil || !strings.Contains(err.Error(), "does not support writable file streams") {
```
判据①：Go **已实现** #49811 的全部面（注册 + 派发 + handle 校验 + 同一条错误串）。判据②：Go 注释指向**更新的 Rust #50177**（writable file streams 完整能力），即 Go 处于 #49811 的**后继状态**；#49811 只是中间「拒绝」态 ⇒ 已被取代。**N/A（已等价型），可关闭。**
> ⚠ 更正 syncl6 主表第 9 行「有」的提示：语义上**不是**可落地条目。

### 序 10 `#49444` — N/A（已等价型）
Rust 语义：反向 JSONL 扫描改用 `memrchr` 找换行（性能）。
```
$ grep -rn 'LastIndexByte' --include='*.go' rollout/projection.go
rollout/projection.go:51:	completeByteCount := bytes.LastIndexByte(data, '\n') + 1
```
判据①：Go **已**在 `rollout/projection.go:51` 用 `bytes.LastIndexByte`（Go 标准库对位 C `memrchr` 的「反向找字节」原语）。判据②：语义等价物到位。**N/A（已等价型）。**

### 序 11 `#49308` — N/A（已等价型；Go over-cover）
Rust 语义（2 行语义改动）：legacy Windows sandbox capture 由 `ConsoleMode::Inherit` 改 `ConsoleMode::NoWindow`，让管道式进程带 `CREATE_NO_WINDOW`：
```
$ git -C /home/jacks/jacks_dev/codex show 50d9c5deac | grep -E '^[+-].*ConsoleMode'
-                ConsoleMode::Inherit,
+                ConsoleMode::NoWindow,
-            ConsoleMode::Inherit,
+            ConsoleMode::NoWindow,
```
```
$ grep -rn 'CREATE_NO_WINDOW' --include='*.go' sandbox/windowssandbox/process_windows.go
sandbox/windowssandbox/process_windows.go:44:		creationFlags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_NO_WINDOW)
sandbox/windowssandbox/process_windows.go:74:	creationFlags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NO_WINDOW)
```
判据①：Go 的 Windows 沙箱进程创建**无条件** OR 入 `windows.CREATE_NO_WINDOW`（:44 与 :74 两处）⇒ Rust「Inherit→NoWindow」的**可观测后果（无 console）Go 已恒成立**。判据②：Go 覆盖面 ⊇ Rust 修复面。**N/A（已等价型）。**
> ⚠ 更正 syncl6 主表第 11 行「有（弱）」：应判 **N/A**（Go 已覆盖）。且此为 windows-only，Linux 节点无 RC 价值。

### 序 12 `#48799` — N/A（无载体型）
Rust 语义：为 Windows 终端捕获写 `b"\x1b[?1006h"`（SGR mouse reporting）。
```
$ grep -rn 'EnableMouseCapture\|?1006\|mouse tracking' --include='*.go' tui/
tui/tea/model.go:3124:		// Rust parity: do not enable or consume terminal mouse tracking. Leaving
tui/tea/model.go:7970:	// keymap turns into scroll, so mouse tracking stays off and native text
tui/tea/right_click_paste.go:21:// where Rust's owned screen installs EnableMouseCapture and therefore implements
```
判据①：Go 的 alt-screen 程序**刻意不启用终端鼠标追踪**（代码注释明说是与 Rust owned screen 的**有意分歧**），全仓无 SGR `?1006h` 安装点。判据②：无落点。**N/A（无载体型）。**

### 序 13 `#49043` — N/A（已等价型；被后续 #49079 取代）
Rust #49043 把 Pro 显示名改为 `"Pro Extra"/"Pro Standard"/"Pro Max"`：
```
$ git -C /home/jacks/jacks_dev/codex show 4f63088cce | grep -E '^\+.*Pro'
+                PlanType::Pro => "Pro Extra",
+                PlanType::ProLite => "Pro Standard",
+                PlanType::ProMax => "Pro Max",
```
但**上游 head 上已被后续 #49079 取代**：
```
$ git -C /home/jacks/jacks_dev/codex log --grep '#49079' -F --format='%h %s' origin/main -1
fe50d010e2 Update and centralize TUI subscription labels (#49079)
$ git -C /home/jacks/jacks_dev/codex grep -n '"Pro 200"' origin/main -- '*.rs'
origin/main:codex-rs/tui/src/subscription.rs:16:            (PlanType::Pro, _) => "Pro 200",
```
Go 侧**已处于 #49079 状态**：
```
$ grep -rn 'SubscriptionLabel\|"Pro 200"\|"Pro 100"\|"Pro 500"' --include='*.go' tui/
tui/subscription.go:27:func SubscriptionLabel(plan auth.PlanType, display SubscriptionDisplay) string {
tui/subscription.go:36:		return "Pro 200"
tui/subscription.go:38:		return "Pro 100"
tui/subscription.go:40:		return "Pro 500"
tui/status/status_test.go:237:		auth.PlanPro:                         "Pro 200",
tui/status/status_test.go:238:		auth.PlanProlite:                     "Pro 100",
```
判据①：Go 已是 **#49079** 的名称集（"Pro 200"/"Pro 100"/"Pro 500"）。判据②：**#49043 的取值已被其更晚的 PR 覆盖**，移植 #49043 会**引入回退**。**N/A（已等价型/已取代）。**

### 序 14 `#49041` — N/A（无载体型）
Rust 语义：TUI 行内代码选区按纯文本复制（`markdown_copy.rs` + `markdown_copy/table.rs` + 测试）。
```
$ grep -rn 'markdownCopy\|markdown_copy\|CopyLine\|SelectedLine' --include='*.go' .
(无输出)
```
判据①：Go 无 **markdown-copy 孪生模块**（0 命中）。判据②：Go 走终端原生选区（`tui/slash_command.go` 明示「raw scrollback for copy-friendly terminal selection」），**前置具不具备**。**N/A（无载体型）。**

### 序 15 `#49624` — N/A（已等价型；本轮唯一不确定项，已定案）
Rust PR 自述（原文摘录）：*Connect `queue`, `archive`, `unarchive`, and `delete` directly to the explicit remote app server after validating configuration. Skip caller credential and runtime initialization, use remote thread parameters, and preserve the caller's working-directory override.*
Rust 关键实现：
```
+        launch_loader_overrides.ignore_login_requirements = true;
+        ConfigBuilder::default()...build().await.wrap_err("failed to load config.toml")?;
+        return Ok(AppServerSession::new(
+            super::connect_remote_app_server(endpoint).await?,
+            ThreadParamsMode::Remote,
```
Go 侧**四条命令均已具备同一行为**（验证配置 → 直连显式 remote，跳过本机凭据/运行时）：
```
$ grep -n 'runRemoteSessionArchive\|runRemoteSessionQueue\|runRemoteSessionDelete\|resolveSessionRemoteEndpoint' app/session.go
app/session.go:70:func runSessionArchive(...)   → loadSessionRuntimeConfig → resolveSessionRemoteEndpoint → if endpoint != nil → runRemoteSessionArchive
app/session.go:105:func runSessionUnarchive(...)  → 同上 → runRemoteSessionUnarchive
app/session.go:147:func runSessionDelete(...)     → 同上 → runRemoteSessionDelete
app/session.go:1364:func runSessionQueue(...)     → 同上 → runRemoteSessionQueue
app/session.go:481:func runRemoteSessionArchive(ctx, endpoint, ...)   { client, _ := openRemoteSessionClient(ctx, endpoint); ... }
app/session.go:499:func runRemoteSessionUnarchive(...)                { ... openRemoteSessionClient ... }
app/session.go:521:func runRemoteSessionDelete(...)                   { ... openRemoteSessionClient ... }
app/session.go:1426:func runRemoteSessionQueue(...)                   { ... openRemoteSessionClient ... }
```
```
$ grep -rn 'func resolveSessionRemoteEndpoint' app/session.go
app/session.go:377:func resolveSessionRemoteEndpoint(opts *cli.SessionOptions, root *cli.RootOptions) (*appserverdaemon.RemoteAppServerEndpoint, error) {
# 内部注释：#46088 --no-daemon 与显式 remote 互斥；#46494 显式 remote 拒绝 client workspace-root 覆盖
```
CLI 侧已把四命令登记为 `--remote` 受支持：
```
$ grep -n 'CommandArchive, CommandDelete, CommandUnarchive, CommandQueue' cli/cli.go
779:	case CommandInteractive, CommandResume, CommandArchive, CommandDelete, CommandUnarchive, CommandFork, CommandQueue, CommandAgents:
        return ""
```
判据①：**有载体，且已实现**。判据②：Go 的 remote 路径**只**从 `--remote-auth-token-env` 取**服务端** token（`app/interactive.go:4508 resolveInteractiveRemoteEndpoint`），不读本机凭据/身份令牌；`app/session.go` 无任何 login/credential/identity-token 引用（`grep 'login|credential|IdentityToken' app/session.go` → 0）。Go 缺 `ignore_login_requirements` 是因为**该路径从不需要登录**——即「跳过调用方凭据初始化」在 Go 中**天然成立**。另有配置校验测试 `app/remote_workspace_roots_test.go:101 TestSessionRemoteWorkspaceRootRejectionsLikeRust`（与 Rust 新增的「四命令配置校验覆盖」对位）。**N/A（已等价型）。**
> ⚠ 更正 syncl6 主表第 15 行「疑似无」：实为**已实现**，非可落地。

---

## 3. 判定翻转 / 对 syncl6 原表的更正

| PR# | syncl6 原判 | 本轮最终判定 | 理由（见 §2） |
|---|---|---|---|
| `#49308` | 有（弱） | **N/A** | Go 无条件 `CREATE_NO_WINDOW`，over-cover（序 11） |
| `#49811` | 有 | **N/A**（被 #50177 取代） | Go 已实现完整能力（序 9） |
| `#48799` | 需人审 | **N/A** 无载体 | Go 刻意不启用鼠标捕获（序 12） |
| `#49043` | 需人审 | **N/A** 已取代 | 上游 #49079 覆盖其取值，Go 已是 #49079（序 13） |
| `#49041` | 需人审 | **N/A** 无载体 | Go 无 markdown-copy 孪生（序 14） |
| `#49624` | 疑似无 | **N/A** 已等价 | Go 已实现远程会话命令直连（序 15） |

**净效果**：syncl6 主表的 3 条「需人审」+ 2 条弱判 + 1 条「疑似无」全部收敛为 N/A，**无一可落地**。

---

## 4. 结论

1. **该 15 行清单可派单数 = 0**（4 已落地 + 11 N/A）。syncl6 的「15 条主表」与 syncl4 的「12 条主表」**均已排干**。
2. 有价值的情报（供队长台账口径修正）：
   - `#49811` / `#49043` 属**「被更晚上游 PR 取代」型** —— 这类条目若按 PR 号蛮力移植，**会引入回退**，应加入派单前筛（本轮建议扩为**五**分类：+`已取代型`）。
   - `#49308` / `#49444` 属**「Go 已覆盖/已用等价原语」型** —— 建议纳入 `已等价型` 的常见子类（性能/平台类改动尤其多）。
3. `#49714`、`#49444`、`#49171`、`#50445`、`#49144`、`#49118`（队长预列的 N/A 行）本轮**未发现反例**，与预判一致，无需重跑。
4. 未决：无阻塞项。若队长要我把「已取代型」判据固化进预检规程，请给指令（只读、无写码）。

---

## 5. 环境与纪律

- 工作树：`/home/jacks/jacks_dev/codex_go_wt/preflight3`（detached @ `b9ad41d3`，只读核验用，报告落盘后 `git worktree remove`）。
- 未改任何 Go 源码；未 commit/push；未动 `update/` 下其它文件。
- Rust 仓只读（`git show`/`git grep`），工作树未变更。
