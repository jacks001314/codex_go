# syncw2 · round87c · side-close 缺 `turn/interrupt`：立项 + 落地

派单：`msg-1791376860098666200-5261`（r87c）。
结论：**确认真缺口（非 N/A）**，已出补丁；Rust 的 `else` 分支（startup interrupt，空 `turn_id`）在 Go 侧**无服务端承载**，属越界项，**请队长裁定**。

- 分支：**无**（我的 worktree `D:\tmp\syncw2\wt2e` 是 detached @ `ee2d4e1db819e1d706eaea8b5da0386e85ea1a42`）
- 交付形态：只读补丁 + 本报告。**0 commit / 0 push / 0 tag / 0 ref 移动**
- 补丁：`update/r86_patches/syncw2_sideclose_interrupt.patch` = **11108 B**，sha256 `f5dd6682c293449c9efb04f13d14ea39496798112440213437f75ffeed9825c5`
- `git apply --check --verbose` 打在 **`ee2d4e1d`** ⇒ exit=0（原文见 §6）

---

## 1) Rust 可观测面（原文对照）

`App::discard_side_thread` 在**关闭 side 时先 interrupt 再 unsubscribe**：

- `codex-rs/tui/src/app/side.rs:428` `discard_side_thread()` → `:433` `self.interrupt_side_thread(app_server, thread_id)`，失败 **return false（保留 side 可见）** → `:438` `app_server.thread_unsubscribe(thread_id)`
- `codex-rs/tui/src/app/side.rs:450` `discard_side_thread_in_background()` → `:459` 取 `active_turn_id_for_thread(thread_id)` → spawn 内先 `ClientRequest::TurnInterrupt{thread_id, turn_id}`（失败只 `tracing::warn`）再 `ThreadUnsubscribe`
- `codex-rs/tui/src/app/side.rs:549-558` `interrupt_side_thread()`：
  ```
  if let Some(turn_id) = self.active_turn_id_for_thread(thread_id).await {
      app_server.turn_interrupt(thread_id, turn_id).await
  } else {
      app_server.startup_interrupt(thread_id).await
  }
  ```
- `codex-rs/tui/src/app_server_session.rs:1472` `turn_interrupt(thread_id, turn_id)`；`:1491` `startup_interrupt(thread_id)` = `turn_interrupt(thread_id, String::new())` ⇒ **空 turn_id 是合法请求**。

服务端可观测效果（Rust app-server `request_processors/turn_processor.rs`）：

- `:1610` `turn_interrupt_inner`；`:1616` `let is_startup_interrupt = turn_id.is_empty();`
- `:1649` `.submit_core_op(request_id, thread.as_ref(), Op::Interrupt)` ⇒ 真的把中断 op 递到 core；`:1652` `Ok(_) if is_startup_interrupt => Ok(Some(TurnInterruptResponse{}))`（空 turn_id 直接 ack）
- ⇒ **可观测面 = 该 side 线程上正在跑的 turn 被中止**（core `Op::Interrupt`），并且这是唯一手段：

关键佐证（「只 unsubscribe」不会中止在跑的 turn）：`codex-rs/app-server/src/request_processors/thread_lifecycle.rs:56-59`

```
fn unloading_target(&self) -> Option<Instant> {
    match (self.has_subscribers, self.is_active) {
        ((false, has_no_subscribers_since), (false, is_inactive_since)) => ...checked_add(self.delay)
        _ => None,
    }
}
```

卸载/回收要求 **同时**「无订阅」**且**「非 Active」；`:44/:71` 的 `is_active` 即 `ThreadStatus::Active`（仍有 turn 在跑）。**turn 在跑 ⇒ 永不进入卸载 ⇒ Rust 必须显式 interrupt。**

## 2) Go 对应观测点（改动前基线 `ee2d4e1d`）

- 落点：`app/remote_tui.go:1238` `interactiveRemoteCloseSide()`：只发 `appserver.MethodThreadUnsubscribe`，**全程无 `MethodTurnInterrupt`**；接线 `app/remote_tui.go:695-697 OnCloseSide`。
- 服务端同侧：`appserver/runtime_router.go:3957 handleThreadUnsubscribeRuntime` → `:3969` `unsubscribeThreadConnection` → `appserver/thread_manager.go:588 ThreadManager.Unsubscribe`：**只改订阅表，不碰 turn**。
- Go 唯一的中止 turn 路径：`appserver/runtime_router.go:7798 handleTurnInterrupt` → `:7833 interruptRuntimeTurn` → `turn.TurnService.Interrupt`（`turn/api.go:680`）。
- `git grep -n -i 'startupinterrupt|startup_interrupt' <main> -- '*.go'` = **0 命中**（Go 无 startup interrupt 概念）。

### 行为探针（真实生产码，`go test -overlay=`，探针只放 `%TEMP%`，**未落仓**）

`%TEMP%\...\zz_probe_test.go`（`package appserver`，overlay 到 `appserver/zz_probe_test.go`）：

```
=== RUN   TestProbeThreadUnsubscribeKeepsRunningTurnActive
--- PASS: TestProbeThreadUnsubscribeKeepsRunningTurnActive (0.03s)
=== RUN   TestProbeThreadDeleteAbortsRunningTurn
    zz_probe_test.go:91: RESULT thread/delete LEFT the running turn active
--- PASS: TestProbeThreadDeleteAbortsRunningTurn (0.05s)
PASS
ok  	codex_go/appserver	0.215s
```

`TestProbeThreadUnsubscribeKeepsRunningTurnActive` 断言链（值级）：

1. 播种活跃 turn（`requireTurns().Start`）→ 发 `thread/unsubscribe`（最后一个订阅者离开，回 `unsubscribed`）；
2. 之后对同一 turn 再发 `turn/interrupt` **仍然成功** ⇒ **unsubscribe 没有中止 turn**（若它中止了，这里会 `turn ... is not active` 而 FAIL）；
3. 再发第二次 `turn/interrupt` **失败** ⇒ `turn/interrupt` 才是真正的中止点。

⇒ 与 §1 的 Rust 面一一对应：**「只 unsubscribe」= 侧面 turn 在服务端继续跑**，这正是 Rust 加 `interrupt_side_thread` 的原因。

## 3) 落地（补丁内容）

写范围 `app/`（2 文件，未碰 `tui/`、未碰 `appserver/`）：

| 落点 | 内容 |
|---|---|
| `app/remote_tui.go:151` | 新增 `remoteTUIInterruptController.activeTurn(threadID)`：复用**既有**控制器单槽，按 thread 过滤（对应 Rust `active_turn_id_for_thread`） |
| `app/remote_tui.go:715-717` | **生产接线**：`OnCloseSide` 把既有 `interrupts` 传进 close-side |
| `app/remote_tui.go:1275-1280` | close-side 在 unsubscribe **之前**发 `turn/interrupt`（`appserver.MethodTurnInterrupt`，`turn.TurnInterruptParams{ThreadID,TurnID}`）；失败 `slog.Warn` best-effort |
| `app/remote_tui.go:1293` | `activeRemoteTUISideTurn(interrupts, sideThreadID)` |
| `app/app_test.go:6065` | `TestInteractiveRemoteCloseSideInterruptsRunningSideTurnLikeRust`（断言 wire 顺序：initialize → **turn/interrupt** → thread/unsubscribe，且 params 正确） |
| `app/app_test.go:6149` | `TestInteractiveRemoteCloseSideSkipsInterruptForOtherThread`（负例：控制器跟踪别的线程时**不得**发 interrupt） |

- 规模：`app/remote_tui.go` +53/−2、`app/app_test.go` +149；共 2 文件 ≤5 ✔
- 语义取舍（如实声明）：Rust 阻塞版 `discard_side_thread` 把 interrupt 失败视为「关闭失败、保留 side」；background 版只 warn。Go 的**单一** close 入口同时服务三种调用方（replacement close `tui/tea/side.go:280`、agent switch `tui/tea/agent.go:214`、ctrl+c return `tui/tea/side.go:360`），且 callers 已在本地 `abandonSideThread`，故按 **background 版 best-effort** 实现，保留原 unsubscribe 错误语义不变（不改既有失败行为）。
- 可达场景（例）：side 会话 turn 在跑 → `ctrl+/` 切回主线程（`m.activeSide.ShowingSide=false`）→ 再 `/side …`；`tui/tea/side.go:242` 的守卫只挡 `ShowingSide==true`，故 `:278-283` 会先 close 上一条 side —— 改动前该侧 turn 继续在服务端跑，改动后被中断。

### 未覆盖（越界，请裁定）

Rust 的 `else → startup_interrupt(thread_id)`（**空 `turn_id`**）在 Go 没有对应承载：

- `turn/api.go:509 TurnInterruptParams.Validate()` 要求 `threadId` **与** `turnId` 均非空；
- `appserver/runtime_router.go:7833 interruptRuntimeTurn` 也无「空 turnId ⇒ 中止该线程当前活跃 turn」分支（空值只会走 `Validate` 报错）。
- 要补齐需改 **`turn/`** 与 **`appserver/runtime_router.go`**（后者是在飞避让面）⇒ **越界，我未动，请裁定**。影响面：reconnect/replay-only 场景下 TUI 不知道 side 的 turn id 时，Rust 用空 turn_id 兜底，Go 目前只能跳过（本补丁在该场景等价于原状）。

## 4) 门禁实跑（6 步）

| 步骤 | 命令 | 结果 |
|---|---|---|
| ① gofmt | `gofmt -l`（LF 副本判定，工作树是 `autocrlf=true` CRLF） | **空** |
| ② build | `go build ./...` | exit=0 |
| ③ vet | `go vet ./app/` | exit=0 |
| ④ 整包对拍 | `go test ./app/ -count=1` | 改动前 `ok codex_go/app 21.457s` / 改动后 `ok codex_go/app 22.313s` ⇒ **新增失败 0** |
| ⑤ RC | 见 §5 | 撤修复 ⇒ **FAIL（值级）**；恢复 ⇒ ok |
| ⑥ parity | `CODEX_RUST_ROOT=C:\rw\codex-rs go test ./parity/ -count=1` | LF 检出 `ok codex_go/parity 15.619s` |

注：
- `TERM` 必须 unset；本机默认 `TERM=dumb` 会让**既有**测试 `TestNoDaemonRejectionsLikeRust` 红（`daemon_startup_test.go:202: interactive rejection = "ERROR: TERM is set to \"dumb\". ..."`，`FAIL codex_go/app 22.144s`）——与本次改动无关，前后一致。
- CRLF 工作树跑 parity 只红既有 `--- FAIL: TestRustCollaborationModeTemplatesMatchGo (0.21s)`（= 队长已定性的 CRLF 假红）；改用 LF 检出后全绿（`ok 15.619s`）。

## 5) RC 原文（反向对照）

撤掉 **close-side 内 interrupt 生产接线**（连带回滚随之不再被引用的 `log/slog` 导入，避免变成 build failure）：

```
$ go build ./...            # 撤修复后仍编译通过（确保不是 build failed）
build exit=0
$ go test ./app/ -run 'TestInteractiveRemoteCloseSideInterruptsRunningSideTurnLikeRust' -count=1 -v
=== RUN   TestInteractiveRemoteCloseSideInterruptsRunningSideTurnLikeRust
    app_test.go:6135: second request = "thread/unsubscribe", want turn/interrupt before thread/unsubscribe
--- FAIL: TestInteractiveRemoteCloseSideInterruptsRunningSideTurnLikeRust (0.00s)
FAIL
FAIL	codex_go/app	0.119s
```

恢复（文件 sha256 还原校验：撤前 `0253A0C68DD2552F1966E5B486DD08A7BA6B8ACEE00B48ECD4ED95ED2973A270` == 恢复后）：

```
--- PASS: TestInteractiveRemoteCloseSideUnsubscribesThread (0.00s)
--- PASS: TestInteractiveRemoteCloseSideInterruptsRunningSideTurnLikeRust (0.00s)
--- PASS: TestInteractiveRemoteCloseSideSkipsInterruptForOtherThread (0.00s)
PASS
ok  	codex_go/app	0.104s
```

（`app/app_test.go` 未参与 RC，仅 `app/remote_tui.go` 被撤/恢复。）

## 6) 补丁元数据 + apply-check 原文

- 路径：`update/r86_patches/syncw2_sideclose_interrupt.patch`
- bytes：**11108**；sha256：`f5dd6682c293449c9efb04f13d14ea39496798112440213437f75ffeed9825c5`（patch 为 LF，0 个 CR）
- 生成基点：`ee2d4e1db819e1d706eaea8b5da0386e85ea1a42`（= 派单基线，`origin/main`）

```
$ cd D:\tmp\syncw2\wte_chk        # detached @ ee2d4e1d 的干净检查 worktree
$ git status --porcelain          # 空
$ git apply --check --verbose D:\tmp\syncw2\syncw2_sideclose_interrupt.patch
Checking patch app/app_test.go...
Checking patch app/remote_tui.go...
                                   # exit=0
$ git apply --verbose <patch>
Applied patch app/app_test.go cleanly.
Applied patch app/remote_tui.go cleanly.
$ git diff --stat
 app/app_test.go   | 149 ++++++++++++++++++++++++++++++++++++++++++++++++++++++
 app/remote_tui.go |  53 ++++++++++++++++++-
 2 files changed, 200 insertions(+), 2 deletions(-)
$ go test ./app/ -run 'TestInteractiveRemoteCloseSide' -count=1 -v
--- PASS: TestInteractiveRemoteCloseSideUnsubscribesThread (0.01s)
--- PASS: TestInteractiveRemoteCloseSideInterruptsRunningSideTurnLikeRust (0.00s)
--- PASS: TestInteractiveRemoteCloseSideSkipsInterruptForOtherThread (0.00s)
ok  	codex_go/app	1.765s
```

补丁保真校验：在 `wte_chk` apply 后 `git diff` 与我工作树 `git diff` **逐字节相同**（11108 B / sha256 `f5dd6682…`）。

## 7) 未决问题 / 风险

1. **越界项（待裁定）**：startup interrupt（空 `turn_id`）需 `turn/` + `appserver/runtime_router.go`；且 `appserver/` 是在飞避让面（syncw3）。不补 ⇒ reconnect/replay-only 场景的 side turn 仍不会被中断。
2. **单槽限制**：`turn_id` 取自既有共享 `remoteTUIInterruptController`（一次只跟踪一个 thread/turn）。并发双 turn 时会互相覆盖 ⇒ 本补丁在该竞态下 best-effort（可能跳过 interrupt）。Rust 是 per-thread 跟踪（`active_turn_id_for_thread`）。若要我升级成 per-thread 表，请单开派单（会扩大到 `app/remote_tui.go` 的通知分发面）。
3. **本地（in-process）side close 未定案**：`app/side_tui.go:149 interactiveLocalSideCoordinator.Close` → `thread/delete`。探针显示 `thread/delete` 之后 **turn 注册表内该 turn 仍活跃**（`RESULT thread/delete LEFT the running turn active`），但本地 turn 由进程内 session 驱动，`ReleaseLiveThreads → liveThread.Close()` 是否中止它需另做行为验证 ⇒ **本轮范围外**，仅登记。
4. 我另外建了一个 detached 检查 worktree `D:\tmp\syncw2\wte_chk`（@`ee2d4e1d`，已 apply 补丁做验证），**未动任何分支/引用**；如需清理可 `git worktree remove --force D:\tmp\syncw2\wte_chk`。
