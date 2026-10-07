# syncw2 · r88e · 本地 side-close 同类不对称（agent 切换 / 替换 side）与 `#50781` 本地对应面 —— 只读判定

- 派单：`msg-1791380415625931800-5804`（r88e；④ 只读结论，真缺口才出补丁）
- 基线：**`c9fbd10f56d75e011b193f2bc3ad6e195bde05a9`**（`origin/main` 实时值 = sync659）；只读树 `D:\tmp\syncw2\wt_local2`（detached @ `c9fbd10f`，干净）
- Rust 参照：`D:\qax\reagent\dev\git\codex` @ `origin/main` = `d83bb540ec64bf6b009bca0283b0be91ea33f26a`
- 已落主前置：`sync656 240913ad`（本地 side-close 先 interrupt）—— 本报告全部结论都建立在它已入库之上
- 纪律：**0 补丁 / 0 commit / 0 push / 0 ref 移动**；探针只放 `%TEMP%`（`-overlay`，未落仓）；未碰避让面

---

## 1) 两项结论（先行）

| 项 | 结论 |
| --- | --- |
| **(A) 本地 agent 切换 / 替换 side 关 side 时是否也要 interrupt** | **已等价（无缺口，不出补丁）** —— 两条路径都经**同一个** `Options.OnCloseSide` handler，而该 handler 自 sync656 起就是「先 interrupt 再 delete」；Rust 同序（interrupt → unsubscribe）。 |
| **(B) `#50781` 的本地对应面是否真无载体** | **确认无载体（N/A 成立）** —— Rust 该缺陷的载体（app-server 通知按线程路由 + 每线程事件通道 + `McpServerStatusUpdated` 越权分支）在 Go 侧**只有 parity 库与测试**，**0 生产消费者**；活 TUI 的 MCP 启动状态是本进程自产、单轮、无 thread 维度。 |

⇒ 本单**不产出补丁**（两项均非真缺口），故无 `syncw2_local_sideclose2.patch`、无 apply-check。

## 2) (A) 证据链：所有关 side 的 UI 路径 → 单一 handler → interrupt

**Go 侧（file:line）**

1. `tui/tea/side.go:272-290`（**替换 side**：`/side` 在 activeSide 存在且未显示时）：`:280` `closer := m.onCloseSide` → `:283` `closer(SideCloseParams{…SideThreadID…})`。
2. `tui/tea/side.go:355-367`（**Ctrl+C 返回父线程 / 退出 side**）：`:360` `closer := m.onCloseSide` → `:364` `closer(params)`。
3. `tui/tea/agent.go:213-224`（**切 agent**）：`:214` `closer := m.onCloseSide` → `:217` `closer(SideCloseParams{…})`；该函数 `applyAgentModalOption` 同时是 `/agent` 选择（`tui/tea/modal.go:787`）、alt+←/→ 快速切换（`agent.go:437 navigateAgent` → `:474 switchAdjacentAgent` → `:489`）、agents overview 选择（`agents_overview.go:872`）的唯一汇聚点。
4. `tui/tea/model.go:2247` `onCloseSide: options.OnCloseSide` —— 上述 `m.onCloseSide` 就是 Options 里那一个。
5. `app/interactive.go:1290` `OnCloseSide: interactiveLocalSideClose(interrupts, sideCoordinator)` → `app/interactive.go:512-517`：`:514` `interrupts.interruptThread(params.SideThreadID)` **先**中断，`:515` `coordinator.Close(params)` 后删除（sync656 落主内容，`:473` 为 `interruptThread`）。

**Rust 侧同序（先 interrupt 再 unsubscribe/discard）**

- 切 agent / 切线程：`tui/src/app/side.rs:688-705 select_agent_thread_and_discard_side` → `:699 discard_side_thread_in_background`（`:450-467`：先 `TurnInterrupt` 再 `ThreadUnsubscribe`）；入口 `input.rs:449/466`（alt+←/→）、`event_dispatch.rs:2964`、`agents_overview.rs:871`。
- 替换 side：`side.rs:728-735 handle_start_side` → `:730 discard_side_thread`（`:428-447`：`:433 interrupt_side_thread` → `:438 thread_unsubscribe`）。
- 退出 side：`side.rs:372 select_agent_thread_and_discard_side(parent)`。

**行为探针（真实模型 + 真实生产 handler，`-overlay`，探针未落仓）**

探针：`%TEMP%\...\zz_probe_sideclose_paths_test.go`（overlay 到 `tui/tea/zz_probe_sideclose_paths_test.go`；复用仓内 test helper `typeText/key/runTeaCmd`）。
复跑：
```
go test ./tui/tea/ -overlay=D:\tmp\syncw2\probe_teapaths\overlay.json \
  -run 'TestProbeReplaceSideInvokesCloseHandler|TestProbeAgentSwitchInvokesCloseHandler' -count=1 -v
```
实跑原文（exit=0）：
```
=== RUN   TestProbeReplaceSideInvokesCloseHandler
    zz_probe_sideclose_paths_test.go:61: RESULT: replace-side path invoked OnCloseSide with tea.SideCloseParams{ParentThreadID:"thread-parent", SideThreadID:"thread-side"}
--- PASS: TestProbeReplaceSideInvokesCloseHandler (0.00s)
=== RUN   TestProbeAgentSwitchInvokesCloseHandler
    zz_probe_sideclose_paths_test.go:125: RESULT: agent-switch path invoked OnCloseSide with tea.SideCloseParams{ParentThreadID:"thread-main", SideThreadID:"thread-side"} before switching to "thread-worker"
--- PASS: TestProbeAgentSwitchInvokesCloseHandler (0.00s)
PASS
ok  	codex_go/tui/tea	0.082s
```
探针步骤（两条都走真实模型状态机，不是直接调 handler）：`/side`＋Enter 建 side → `Ctrl+/`（`side.go:71 toggleSideConversation`）回到父线程但 side 保留 → ①再次 `/side`＋Enter（触发 `side.go:272-287` 替换分支）②`/agent`＋Enter→Down→Enter（触发 `agent.go:213-224`）。两条都证明「走到 close 时调用的就是那个 handler」，而该 handler 在 main 上已经先 interrupt（其值级 RC 见 sync656 落主记录 / 我的 `update/syncw2_local_sideclose_2026_10_07.md`）。

**结论：已等价** —— 加上一层：Rust 对 **Embedded** 目标本身也**跳过** side 清理（`event_dispatch.rs:3633-3638` / `:3701-3706` 的 `if !matches!(self.app_server_target, AppServerTarget::Embedded)` 守卫，/archive 与 /delete 两处），所以 Go 的 `app/interactive.go:1088 defer sideCoordinator.CloseAll()`（退出时只 delete、不 interrupt）与 Rust embedded 行为一致（进程退出即终止 in-process turn），不构成缺口。

## 3) (A) 附带披露：三条「只丢本地 `activeSide`、根本不关 side」的路径（**不计入本单缺口**）

`git grep -nI 'OnCloseSide' -- 'tui/tea/session_picker.go' 'tui/tea/working_directory.go'` ⇒ **0 命中**（exit=1），但这三个文件确实会把 `activeSide` 置空：

- `tui/tea/session_picker.go:440` `m.activeSide = nil`（从会话选择器 resume 另一线程）
- `tui/tea/working_directory.go:181` `m.activeSide = nil`（切换工作目录替换会话）
- `tui/tea/agent.go:206` `m.activeSide = nil`（**仅 `m.onSwitchAgent == nil` 时可达**；真实 TUI 在 `app/interactive.go:1266` 已接线 `OnSwitchAgent` ⇒ 实际不可达）

判读（如实标注证据边界）：
1. 这三条**不是「关 side」路径**：它们既不 `thread/delete` 也不 unsubscribe，本项命题（关 side 时是否 interrupt）对它们不适用；side 线程与其 turn 保持存活，属「本地登记被丢弃」的另一类问题。
2. Rust 侧同类先例：`session_lifecycle.rs:836 self.side_threads.clear()`（`reset_thread_event_state`）同样只清本地登记、不 discard。**但我没有逐行证完** `resume_target_session`（`session_lifecycle.rs:1295`）在 resume 时是否 discard side（`shutdown_side_threads` 的调用点只有 `event_dispatch.rs:3634/3702`（/archive、/delete）与 `voice_owner.rs:128`，不含 resume 主路径）⇒ **本单不下缺口结论**。
3. 若要立项，落点会是 `tui/tea/session_picker.go` / `tui/tea/working_directory.go`（**与 syncl3 车道文件同目录**），按你「撞文件先问」的规矩，请单独派单并先确认避让面。

## 4) (B) `#50781` 本地对应面 = 无载体（N/A 成立）

**Rust 缺陷本体**（`f365d5754b`「Restrict TUI MCP startup notifications to owned threads」，5 文件 +159/−30）：
- 新增 `codex-rs/tui/src/app/app_server_thread_ownership.rs`：`owns_thread_for_routing`（primary / thread_event_channels / side_threads / agent_navigation 四源）+ `owns_untracked_notification`（`ThreadStarted` 取 `SubAgent(ThreadSpawn{parent_thread_id})`；`McpServerStatusUpdated` 走带上限重试的 `thread/read` 取 parent）。
- `app_server_events.rs:460-476`（旧）里 `&& !matches!(notification, McpServerStatusUpdated)` 让**无关线程的 MCP 启动通知绕过归属校验**，从而可创建事件通道 → 之后该线程的审批请求可能出现在当前会话。修复后统一走归属判定。

**Go 侧正向陈述（3 条，均可复跑）**

① 该载体在 Go 只有 parity 库 + 测试，**0 生产消费者**：
```
$ git grep -nI -E "RouteServerNotification|HandleServerNotificationEventDecision" -- "*.go"
tui/app/app_server_events.go:75:func HandleServerNotificationEventDecision(...)
tui/app/app_server_events.go:100:	route, threadID := RouteServerNotification(primaryThreadID, &notification)
tui/app/app_server_events.go:150:func RouteServerNotification(...)
tui/app/app_server_events_test.go:17/22/34/39/44/49/54/60/61/66/71/76: ...
tui/app/app_server_event_targets_test.go:125/129/133/137: ...
(exit=0；除定义外全部命中 `_test.go`)
```
② 每线程登记表 / 归属 helper 在 Go **0 命中**：
```
$ git grep -nI -E "threadEventChannels|thread_event_channels|ownsThreadForRouting" -- "*.go"
(exit=1，无输出)
```
③ 活 TUI（`tui/tea` + `app/`）对 MCP 状态**通知** 0 消费：
```
$ git grep -nI "McpServerStatusUpdated" -- "app/*.go" "tui/tea/*.go"
(exit=1，无输出)
$ git grep -nI "NewThreadEventChannel" -- "*.go"
tui/app/thread_events.go:53:func NewThreadEventChannel(capacity int) *ThreadEventChannel {
tui/app/thread_events.go:60:func NewThreadEventChannelWithSession(...)
tui/app/thread_events_test.go:101/111: ...
(exit=0；无生产消费者)
```

**活 TUI 的实际机制（为什么没有宿主）**
- 嵌入式 TUI 的 MCP 启动状态由**本进程**产生：`app/interactive.go:1105 initialMessages = interactiveMCPStartupMessages(startupCtx, mcpService, runner, mcpExpectedServers)`（实现 `app/interactive.go:1633`），以 `codextea.MCPStartupUpdateMsg` 投给**单个** chat widget（`tui/tea/model.go:2552` → `:4420 applyMCPStartupUpdate`，状态体是单轮 `tui/chatwidget/mcp_startup.go:42 McpStartupRoundState`）。既无 app-server 推送，也无 thread 维度 ⇒ 「无关线程的 MCP 启动通知创建事件通道」在 Go 无观测面。
- 活 TUI 的 thread 维度事件面（`tui/tea/model.go:564 ThreadScopedEventMsg`）**已有归属过滤**：`tui/tea/side.go:504-528 applyThreadScopedEvent` 只处理「当前线程」或「activeSide 的父/子线程」，其余丢弃 ⇒ 与 Rust 的 ownership 检查同构（不同机制的等价物），即使将来接入 app-server 推送也不会直接串台。
- remote TUI（`app/remote_tui.go`）同样不消费 MCP 状态通知（唯一 `ServerNotification` 命中是 `:4117` 关于 ConfigWarning 的注释）。

**如实披露（不是本项缺口，但值得记账）**：parity 库自身仍是**修复前**的 Rust 语义 —— `tui/app/app_server_events.go:84` 的 `ServerNotificationMcpServerStatusUpdated` 分支在 `:75` 的决策入口里**不经过**任何归属校验（`:100` 的路由在这之后；`:150-163 RouteServerNotification` 只看 primary 与 target）。也就是说：**一旦把该 parity 库接进生产，等价缺陷会重现**。但「把 parity 库接生产」是架构决策（`tui/app` 整体无消费者，syncl3/syncl5 已同判），不属于本条缺口。若你要的是「把 `tui/app` 决策库接生产」，请另立单。

**结论：无载体（N/A 成立），接受 syncl3/syncl5 的原裁定。**

## 5) 复跑命令清单（本报告全部证据）

```powershell
cd D:\tmp\syncw2\wt_local2   # detached @ c9fbd10f，干净
git grep -nI -E "RouteServerNotification|HandleServerNotificationEventDecision" -- "*.go"
git grep -nI -E "threadEventChannels|thread_event_channels|ownsThreadForRouting" -- "*.go"
git grep -nI "McpServerStatusUpdated" -- "app/*.go" "tui/tea/*.go"
git grep -nI "NewThreadEventChannel" -- "*.go"
git grep -nI "OnCloseSide" -- "tui/tea/*.go" "app/*.go"
Remove-Item Env:\TERM -ErrorAction SilentlyContinue
go test ./tui/tea/ -overlay=D:\tmp\syncw2\probe_teapaths\overlay.json -run "TestProbeReplaceSideInvokesCloseHandler|TestProbeAgentSwitchInvokesCloseHandler" -count=1 -v
```

## 6) 未决 / 需你裁示

1. §3 的三条「只丢 `activeSide`」路径：要不要立项（需先把 Rust `resume_target_session` 是否 discard 逐行证完；落点 `tui/tea/session_picker.go` / `working_directory.go`，**与 syncl3 同目录**，需你确认避让面）。
2. §4 末尾的「`tui/app` parity 库是否接生产」= 架构决策，不在本单。
3. 本单**未改任何文件**，因此 `wt_local2` 无需保留（只读树，可随时回收）；探针与证据留在 `D:\tmp\syncw2\probe_teapaths\`。
