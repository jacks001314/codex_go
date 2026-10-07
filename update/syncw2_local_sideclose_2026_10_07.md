# syncw2 · r88d · 本地 in-process side-close 缺中断 —— 修复落地（补丁交付，未提交）

- 派单：`msg-1791378931785011800-5552`（r88d；上游 r88c 只读 scoping = 真缺口，本单批准落地）
- apply 基线：**`d12d00dde5732a8f910db636b5452949a4594785`**（`origin/main` 实时值 = sync648「align fallback catalog field values with models.json」）
- 工作树：`D:\tmp\syncw2\wt_localside`（`git worktree add --detach` @ `d12d00dd`，非分支 ⇒ 无 ref 创建/移动）
- 对照基线树：`D:\tmp\syncw2\wt_localbase`（同 SHA，干净，用于 apply-check 与改动前整包测试）
- Rust 上游参照：`D:\qax\reagent\dev\git\codex` @ `origin/main` = **`d83bb540ec64bf6b009bca0283b0be91ea33f26a`**；`codex-rs/tui/src/app/side.rs` blob = `150583dc29187c2b3908bd05cd40f8495e04ed71`
- 纪律：**0 commit / 0 push / 0 tag / 0 分支移动**；探针与临时文件只放 `%TEMP%` / `D:\tmp`；未碰避让面

---

## 1) 一句话结论

已按批准口径落地：**embedded TUI 的 `OnCloseSide` 在删除 side 会话之前，先中断「属于该 side 线程」的在跑 turn**（best-effort，与已落主的 remote 侧 sync645 同口径）。2 文件（1 生产 + 1 仓内回归测试），`+226 / −1`。

## 2) Rust 行为依据（逐行，`d83bb540`）

- `codex-rs/tui/src/app/side.rs:428-447` `discard_side_thread()`：`:433` 先 `interrupt_side_thread(app_server, thread_id)`（失败 ⇒ `tracing::warn!` + 保留 side，`return false`）→ `:438` 才 `app_server.thread_unsubscribe(thread_id)`。
- `codex-rs/tui/src/app/side.rs:450-467` `discard_side_thread_in_background()`：`:458-461` 取 `active_turn_id_for_thread(thread_id)`，spawn 内发 `TurnInterrupt`（失败只 warn）再 `ThreadUnsubscribe` ⇒ **best-effort 形态的官方先例**。
- `codex-rs/tui/src/app/side.rs:549-559` `interrupt_side_thread()`：有活跃 turn ⇒ `app_server.turn_interrupt(thread_id, turn_id)`；无 ⇒ `startup_interrupt(thread_id)`（空 `turn_id`）。
- 该路径对 embedded（in-process）同样成立：`tui/src/lib.rs:321-330` 三态 transport、`app_server_connection.rs:17-19`（Embedded = 无 remote connection，即 in-process）、`app-server-client/src/lib.rs:328 InProcessAppServerClient`。

⇒ Rust 的 side close 顺序恒为 **interrupt → unsubscribe/discard**；Go embedded 侧原先只有「删记录」，不对称。

## 3) Go 改动（基线 `d12d00dd`；改动后行号）

生产接线（`app/interactive.go`）：

| 落点 | 内容 |
| --- | --- |
| `app/interactive.go:473` | 新增 `func (c *interactiveInterruptController) interruptThread(threadID string) bool`：仅当 `c.threadID == threadID` 时 cancel，并清 `cancel/threadID/turnID`（线程作用域 = Rust `active_turn_id_for_thread` 的等价物）；`nil` 接收者、空 `threadID`、线程不匹配、无 `cancel` 一律返回 `false` 且不产生副作用 |
| `app/interactive.go:496` | `mailbox.Clear(&turn.SteerDrainParams{...})`：清该 turn 的 steer mailbox，保持 `begin()` 的 `done()` 原有清空保证（见 §6 说明） |
| `app/interactive.go:512-517` | 新增 `func interactiveLocalSideClose(interrupts *interactiveInterruptController, coordinator *interactiveLocalSideCoordinator) codextea.SideCloseFunc`：`:514` 先 `interrupts.interruptThread(params.SideThreadID)`，再 `:515` `coordinator.Close(params)` |
| `app/interactive.go:1286` | 接线：`OnCloseSide: interactiveLocalSideClose(interrupts, sideCoordinator)`（原为 `sideCoordinator.Close`） |

新增仓内回归测试：`app/interactive_sideclose_test.go`（169 行）

| 落点 | 内容 |
| --- | --- |
| `app/interactive_sideclose_test.go:22-38` | `localSideCloseRunner`：fake runner，按真实 hook（`exec/exec.go` → `app/interactive.go` `execRequest.OnTurnStarted = controller.setActive`）上报 turn 后阻塞至 ctx 取消；`var _ interactiveTurnRunner = (*localSideCloseRunner)(nil)` |
| `:40` | `newLocalSideCloseTestStore`：真 `session.Store` + 真 parent 记录 |
| `:53` | `startLocalSideCloseTestTurn`：真 `interactiveTurnCommandWithRequest` 起 turn |
| `:75` | `TestInteractiveLocalSideCloseInterruptsRunningTurn`：调**生产 handler** `interactiveLocalSideClose(interrupts, coordinator)`，断言① store 记录已删（`session.ErrThreadNotFound`）② turn ctx 在 5s 内被 cancel |
| `:111` | `TestInteractiveLocalSideCloseKeepsOtherRunningTurn`：正在跑的是 parent turn 时，关 side **不得**取消它；随后控制组 `interrupts.interrupt()` 仍能取消 ⇒ 证明探针非空转 |
| `:140` | `TestInteractiveInterruptControllerInterruptThreadScopesToThread`：空 id / 无活跃 turn / 线程不匹配 ⇒ `false` 且不 cancel；匹配 ⇒ `true` 且 cancel；调用后追踪状态被清空 |

## 4) 语义口径（best-effort，与 sync645 同口径）

- 有活跃 turn 且属于被关的 side 线程 ⇒ cancel；否则**静默 no-op**（等价 Rust `active_turn_id_for_thread` 返回 `None` 时的 `startup_interrupt` 分支前的判定/no-op 语义）。
- **未加 `slog.Warn`**：进程内 `cancel()` 没有可失败的投递路径；sync645（remote）的 `warn` 只覆盖 RPC 失败。若对「无 turn 可中断」也 warn，会在每次正常关闭空闲 side 会话时刷日志（即常态噪声），故按同口径留空。**如你要求字面 warn，我补 1 行即可。**
- 未照搬 Rust 阻塞版（失败则保留 side）：Go 本地 close 是删除语义，保持既有行为不变。

## 5) 门禁实跑（工作树 `D:\tmp\syncw2\wt_localside`）

① `gofmt -l app/interactive.go app/interactive_sideclose_test.go` ⇒ 空输出，`exit=0`

② `go build ./...` ⇒ `exit=0`（无输出，约 156s）

③ `go vet ./app/` ⇒ 空输出，`exit=0`

④ 受影响包整包对拍（跑前 `Remove-Item Env:\TERM -ErrorAction SilentlyContinue`）

- 改动前基线（`D:\tmp\syncw2\wt_localbase` @ `d12d00dd`，干净）：
  ```
  ok  	codex_go/app	21.152s
  ```
- 改动后（`wt_localside`）：
  ```
  ok  	codex_go/app	23.872s
  ```
  ⇒ **新增失败 0**（两侧均全绿，无既有红需要豁免）

新增测试定点 `-v`：
```
=== RUN   TestInteractiveLocalSideCloseInterruptsRunningTurn
--- PASS: TestInteractiveLocalSideCloseInterruptsRunningTurn (0.03s)
=== RUN   TestInteractiveLocalSideCloseKeepsOtherRunningTurn
--- PASS: TestInteractiveLocalSideCloseKeepsOtherRunningTurn (0.32s)
=== RUN   TestInteractiveInterruptControllerInterruptThreadScopesToThread
--- PASS: TestInteractiveInterruptControllerInterruptThreadScopesToThread (0.00s)
PASS
ok  	codex_go/app	0.456s
```

⑤ `parity`

- CRLF 工作树（`wt_localside`，`core.autocrlf=true`）+ `CODEX_RUST_ROOT=C:\rw\codex-rs`：
  ```
  --- FAIL: TestRustCollaborationModeTemplatesMatchGo (0.26s)
  FAIL	codex_go/parity	10.236s
  ```
  这是你已确认的 **CRLF 假红**（`//go:embed` 读工作树），非本改动引入。
- **LF 检出（你规定的口径）**：`git -c core.autocrlf=false archive --format=tar -o lf_localside.tar d12d00dd` → 解包到 `D:\tmp\syncw2\lf_localside` → 覆盖本改动 2 文件（已核验 CRLF 对 = 0）→
  ```
  ok  	codex_go/parity	61.797s
  ```
  ⇒ **parity 全绿**。

## 6) RC 反向对照（原文）

撤掉真实生产接线的唯一一行 `app/interactive.go:514`（`interactiveLocalSideClose` 内，`Options.OnCloseSide` 实际调用的就是它）：

```python
needle = b"\t\tinterrupts.interruptThread(params.SideThreadID)\n"
assert data.count(needle) == 1          # 命中 1
```
- 撤前 `app/interactive.go` sha256 = `b5d55bf689db7a050e2b02d4fa43eb00ee9383cdc6198c1400914e1dbd7a8677`
- 撤后 sha256 = `9b4e946d1429eb7cb83f83370091a61b68d1feccfdb0e6754f202a1542f501d4`（删除 50 字节）

`go test ./app/ -run 'TestInteractiveLocalSideCloseInterruptsRunningTurn' -count=1`（`exit=1`）：
```
--- FAIL: TestInteractiveLocalSideCloseInterruptsRunningTurn (5.05s)
    interactive_sideclose_test.go:104: local side close left the running turn alive: turn context was never cancelled
FAIL
FAIL	codex_go/app	5.409s
FAIL
```
⇒ **值级 FAIL**（行为断言，非编译失败）。

逐文件还原（python 从 `D:\tmp\syncw2\rc_local_sideclose_interactive.go.bak` 字节级写回）：
```
restored SHA256: b5d55bf689db7a050e2b02d4fa43eb00ee9383cdc6198c1400914e1dbd7a8677
matches backup: True
```
还原后 `go test ./app/ -run 'TestInteractiveLocalSideClose|TestInteractiveInterruptControllerInterruptThread' -count=1 -v`：
```
--- PASS: TestInteractiveLocalSideCloseInterruptsRunningTurn (0.09s)
--- PASS: TestInteractiveLocalSideCloseKeepsOtherRunningTurn (0.38s)
--- PASS: TestInteractiveInterruptControllerInterruptThreadScopesToThread (0.00s)
PASS
ok  	codex_go/app	0.667s
```
⇒ 撤 ⇒ FAIL（值级）／还原 ⇒ ok，且 **sha256 与撤前一致**。

## 7) 补丁与 apply-check

- 文件：`update/r86_patches/syncw2_local_sideclose.patch`（**10475 字节**，UTF-8 无 BOM，LF）
- sha256：`b0e724535e82f4108c7942afb26406a8c3719488a9e2722d846431cf6b75426a`
- 生成方式：`wt_localside` 内 `git add -N app/interactive_sideclose_test.go` + `git diff`（未 commit）
- `git apply --check` 打在 **`d12d00dd`**（`wt_localbase`，干净树）原文：
  ```
  $ git apply --check --verbose D:\tmp\syncw2\syncw2_local_sideclose.patch
  Checking patch app/interactive.go...
  Checking patch app/interactive_sideclose_test.go...
  (exit=0)
  ```
  `git apply --stat`：
  ```
   app/interactive.go                |   58 ++++++++++++-
   app/interactive_sideclose_test.go |  169 +++++++++++++++++++++++++++++++++++++
   2 files changed, 226 insertions(+), 1 deletion(-)
  ```

## 8) 需要你复核的两点 / 未决

1. **超出你规格的一处**：`interruptThread` 在清 `threadID/turnID` 之前先排空 steer mailbox（`app/interactive.go:496`），以保持 `begin()` → `done()` 原有的 mailbox 清理保证；否则被打断 turn 的 steer 条目会滞留。若你认为不该动 mailbox，我删掉这 3 行（不影响 RC）。
2. **字面 warn**：见 §4，目前对「无可中断 turn」静默；要 warn 请一句话，我补。
- 工作树文件行尾：改动文件在 `wt_localside` 中为 **LF**（`core.autocrlf=true` 的仓内 git 视为与 index 一致，diff 干净、无整文件噪声）。这是为了本机 `gofmt`/探针可直接跑；落主后由 git 按 `autocrlf` 规范处理。
- 未覆盖（按你裁定仍为 backlog，本单不动）：startup interrupt（空 `turn_id`）分支；per-thread 跟踪升级；`app/remote_tui.go` 侧无改动。
