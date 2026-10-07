# syncw2 · r88c · 本地 in-process side-close 是否同样缺中断（只读 scoping）

- 派单：`msg-1791377994051677400-5478`（r88c，只读 scoping，**不出补丁**）
- 基线：**`c9c6a200de74ab129493641d1ea0cfa1797099b5`**（= `git ls-remote origin main` 实时值；工作树 `D:\tmp\syncw2\wt_c9c6`，detached，干净）
- 纪律：0 commit / 0 push / 0 tag / 0 分支移动；探针只放 `%TEMP%`（`-overlay`，未落仓）；未碰避让面
- **结论（一句）：真缺口（S，≤2 文件，RC 可行）** —— Go 本地 in-process 关 side 只 `thread/delete`（删 store/rollout 记录），**不中断**正在跑的 turn；Rust 无论哪种 transport（含 embedded/in-process）都走 `AppServerSession` 的 `turn/interrupt`，两侧不对称。

---

## ① Rust 本地路径逐行依据

Rust TUI 有三种 transport，但**都是 app-server RPC**：

- `codex-rs/tui/src/lib.rs:321-330` `enum AppServerTarget { Embedded, LocalDaemon{endpoint,..}, Remote{endpoint} }`
- `codex-rs/tui/src/app_server_connection.rs:17-19`：`AppServerTarget::Embedded => bail!("embedded sessions have no remote connection")` —— Embedded 即 in-process，不经 socket
- in-process 也走同一 client：`codex-rs/app-server-client/src/lib.rs:3`（“wraps `codex_app_server::in_process` behind a single async API”）、`:328 InProcessAppServerClient`、`:31 DEFAULT_IN_PROCESS_CHANNEL_CAPACITY`

⇒ **Rust 没有「不走 RPC 的本地 side close」**。side close 的实现只有两处，**都是先 interrupt 再 unsubscribe**：

- `codex-rs/tui/src/app/side.rs:428` `discard_side_thread()`（阻塞）：`:433` `self.interrupt_side_thread(app_server, thread_id)`（失败 `return false`，保留 side 可见）→ `:438` `app_server.thread_unsubscribe(thread_id)`
- `codex-rs/tui/src/app/side.rs:450` `discard_side_thread_in_background()`：`:459` 取 `active_turn_id_for_thread(thread_id)` → spawn 内先 `TurnInterrupt`（失败只 warn）再 `ThreadUnsubscribe`
- `codex-rs/tui/src/app/side.rs:549-558` `interrupt_side_thread()`：有活跃 turn → `turn_interrupt(thread_id, turn_id)`；否则 → `startup_interrupt(thread_id)`（= 空 `turn_id`，`app_server_session.rs:1491`）
- 全部调用点都带 `app_server`（无一例外）：`thread_routing.rs:55`、`agents_overview.rs:700/704`、`side.rs:699`、`side.rs:730`、`side.rs:777/790/818`（经 `discard_side_thread_or_keep_visible:593`）
- 唯一**不发 RPC** 的是 `side.rs:511 discard_closed_side_thread()`：只用于「**服务端已关闭**的线程」（`thread_routing.rs:2177` 收到 ThreadClosed 后调用），只清本地状态 —— 与 Go `tui/tea` 的 `sideThreadAbandoned`（已 abandon 的 side 不再发请求）语义一致，不构成本项差异
- Rust 侧测试佐证：`app/tests.rs:5702/5732/5774`（`discard_side_thread*` 行为）、`:5821 discard_closed_side_thread_removes_local_state_without_server_rpc`

## ② Go 现状（file:line，基线 `c9c6a200`）

**关闭路径（生产接线）**

- `app/interactive.go:1229-1230`：`OnStartSide: sideCoordinator.Start` / `OnCloseSide: sideCoordinator.Close`；`sideCoordinator` 建于 `:1031`
- 同文件 `:1058` `interrupts := newInteractiveInterruptController()`，且 `:1232`（safety-buffering retry）、`:1247`（submit）**都已把 `interrupts` 传下去** —— 只有 `OnCloseSide` 没拿到
- `app/side_tui.go:149 Close()` → `:211 deleteSideLocked()` → `:214 localSideRequest(… MethodThreadDelete …)`；每次**新建** Router（`:212 router := appserver.NewRouter(c.store)`）

**服务端（in-process Router）对在跑 turn 无作用**

- `appserver/router.go:2418 handleThreadDelete`：删 store 记录 / rollout / thread-name，然后 `releaseLiveThreads`
- `appserver/thread_manager.go:124 ReleaseLiveThreads` → `:291 managedLiveThread.Close()`：**只关持久化句柄**，不中止 turn；而本地 close 用的是新建 Router，其 live-thread 表为空 ⇒ 对在跑的 turn 完全无影响

**本地 turn 的执行与唯一取消手段**

- `app/interactive.go:3685 interactiveTurnCommandWithRequest` → `:3696 turnCtx, done = interrupts[0].begin(ctx)`（`begin` 于 `:355` 记下唯一 `cancel`）→ 子协程 `runInteractiveTurn` → `:3866 runner.RunContext(ctx=turnCtx, …)`
- `:3859 execRequest.OnTurnStarted = controller.setActive`（exec 侧回调点 `exec/exec.go:83`、调用 `:350-351`）
- 唯一停止手段 = `interactiveInterruptController.interrupt()`（`:449-460` → `cancel()`），由 TUI 触发：`tui/tea/message_router.go:39`（Ctrl+C：running 先 interrupt）、`tui/tea/model.go:3045`（`chat.interrupt_turn` 键位）

**三条 close 触发路（与 remote 同构，只读）**：`tui/tea/side.go:280`（替换关闭）、`tui/tea/side.go:360`（Ctrl+C 且非 running）、`tui/tea/agent.go:214`（切换 agent 线程）

⇒ 与 remote 单结构相同：**Ctrl+C 在 running 时走 interrupt，但「替换 / 切线程」这条路的 close 不会中断**。本地版还更彻底：没有服务端 ephemeral 回收兜底（turn 由进程内 exec runner 驱动），线程记录被删之后 turn 仍继续跑。

## ③ 行为探针（真实生产码，`-overlay`，探针只在 `%TEMP%`，未落仓）

探针文件：`%TEMP%\...\zz_local_sideclose_probe_test.go`（`package app`，overlay 到 `app/zz_local_sideclose_probe_test.go`）。

```
=== RUN   TestProbeLocalSideCloseKeepsRunningTurnAlive
    zz_local_sideclose_probe_test.go:75: STATE: interrupt controller tracks "01a11675-8622-7e1e-98bd-3757febd4e56"/"turn-side-1" while the side turn runs
    zz_local_sideclose_probe_test.go:88: RESULT: local side Close() LEFT the running turn alive (turn context never cancelled)
    zz_local_sideclose_probe_test.go:96: CONTROL: interrupts.interrupt() did cancel the running turn
--- PASS: TestProbeLocalSideCloseKeepsRunningTurnAlive (0.53s)
ok  	codex_go/app	0.625s
```

探针步骤（全部走生产函数，无 helper-only）：

1. `newInteractiveLocalSideCoordinator(store).Start(...)` 真建 side 线程（真 `thread/fork` + boundary inject）；
2. `interactiveTurnCommandWithRequest(ctx, …, runner, state, …, interrupts)` 真起 turn：fake runner 在启动时按 `exec/exec.go:350` 调 `req.OnTurnStarted(sideThreadID, "turn-side-1")`，然后阻塞直到 ctx 被取消；
3. 调**生产 close** `coordinator.Close(codextea.SideCloseParams{…})`；
4. 断言 A：side 的 store 记录已删（`session.ErrThreadNotFound`）；
5. 断言 B（值级）：close 后 500ms 内 runner 的 ctx **仍未取消** ⇒ `RESULT … LEFT the running turn alive`（若 close 会中断，这里直接 `Fatal("local side Close() cancelled the running turn")`）；
6. **控制组**：`interrupts.interrupt()`（生产中断路径）确实取消了它 ⇒ 证明探针不是空转。

`STATE` 行额外证明：运行期间控制器跟踪的 threadID **就是**被关闭的 side 线程 id（`01a11675-…` = fork 出来的 side 线程）⇒ 修复可按「控制器活跃线程 == 关闭的 side 线程」精确判定，**无需新增跟踪**。

## ④ 结论 + 落点 + RC 方案（未落地，等批）

- **结论：真缺口（S，≤2 文件，RC 可行）**（不是 N/A，也不是「已等价」）
- **落点（建议，1 文件 + 可选 1 测试文件）**：`app/interactive.go`
  1. 给 `interactiveInterruptController` 加 `interruptThread(threadID string) bool`：仅当 `c.threadID == threadID` 时 `cancel()`（可顺带清 `cancel`/`threadID`/`turnID`，与 `begin` 的 `done` 同口径）；
  2. 把 `:1230 OnCloseSide` 包一层：**先** `interrupts.interruptThread(params.SideThreadID)`（best-effort），**再** `sideCoordinator.Close(params)`。
- **RC 方案**：撤掉包装里的 interrupt（保留 `sideCoordinator.Close`）⇒ 仓内回归测试 **FAIL（值级）**，例如 `local side close left the running turn alive: turn context was never cancelled`；恢复 ⇒ ok。回归测试可直接把 §③ 探针写成仓内测试（真 coordinator + 真 controller + fake runner + `interactiveTurnCommandWithRequest`），落在 `app/`（写范围 `app/`，无避让面冲突）。
- **需你确认两点**：
  1. 语义：建议 **best-effort（warn）** —— 与已交付的 remote 补丁同口径；理由是本地 close 在三条调用路上语义不同，且 Rust 阻塞版会「interrupt 失败则保留 side」，而 Go 本地 close 是删除语义，改成阻塞失败会改变既有行为。若你要严格对齐 Rust 阻塞版（失败则保留 side），请明示，我按你的口径改。
  2. 授权：修复点在 `app/interactive.go`（`app/` 域内，但属 TUI 装配点，rmdir 风险低）。

## 附：环境回收状态（按裁定 §4 执行）

- `D:\qax\reagent\dev\codex_go_wt\syncw2` **已回收**（`git worktree remove --force`，exit=0）。前置比对（vs 现 main `c9c6a200`）：
  - 8 个脏文件中 **7 个 diff 为空**（`app/recap.go`、`app/recap_test.go`、`doctor/doctor.go`、`doctor/configured_tui_mode_test.go`、`tui/state.go`、`tui/state_test.go`、`tui/tea/model_test.go`）⇒ 内容与 main 一致，无未落地内容；
  - `tui/tea/model.go` 显示 `−62/+2`：逐行看是**旧变体**（缺 main 已有的 Rust #50389 块 `transcriptNavigationKeymapContexts` / `configuredBindingTakesNavKey`，多出的 2 行只是旧版 `applyTranscriptNavigationKey` 里的 `default: return false`）⇒ 同样属「滞后」，非「未落地」。
  - 其 `update/` 下未跟踪产物（4 份 syncw2 报告 + `r86_patches/`，含 `syncw2_49799.patch`、`syncw2.patch.DEPRECATED.txt`）已先备份到 `D:\tmp\syncw2\backup_syncw2_update\`（105 个文件）；4 份报告在 Master 项目镜像中均在（`update/syncw2_*`）。
  - 分支 `syncw2` **未动**，仍指向 `ed6f52b7406055444a6ac3023788b8a0f588e6b2`。
- `D:\tmp\syncw2\wte_chk` **已回收**（exit=0）。
- 未回收（不是本轮指定项）：`D:\tmp\syncw2\wt2e / wt97 / wt624 / wt632 / wt632base / wt632lf / wt984` 等旧 detached 临时树（均无独有产物）；如需一并释放，请一句话指示。
- 本轮只读基线树 `D:\tmp\syncw2\wt_c9c6` 保留（detached @ `c9c6a200`，干净），便于你批准后立刻出补丁。
