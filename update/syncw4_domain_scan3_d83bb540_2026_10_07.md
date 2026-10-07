# round88 — 第三轮域扫描（`tool/` + `applypatch/` + `prompt/` + `agent/`）

- lane: **syncw4**（Windows 节点）；date: 2026-10-07
- 扫描 pin（Rust 上游枚举头）：**`d83bb540ec64bf6b009bca0283b0be91ea33f26a`**（`#51678` "Move Windows sandbox tests into a dedicated integration binary"，Oct 7 12:37 UTC）——由 `b17c74cfd5` 前进 1 笔（`git log --oneline b17c74cfd5..d83bb540` 恰 1 条）。
- Rust 参考树 **`C:\rw\codex-rs` 未动**：worktree HEAD 仍 `5b0b2530354052b9194156d70d4c94a439368342`、`git status --porcelain` 空；本轮只做了 `git fetch origin`（更新 remote-tracking refs）+ 只读 `git log/show/grep/ls-tree` 打共享对象库。
- 纪律：**只读** —— 0 补丁 / 0 commit / 0 push / 0 ref 移动 / 未写任何代码。

## 0. 方法与域映射（Go 目录 → Rust 路径）

`git log --numstat --format='@@%h|%s' -200 d83bb540`（200 条，UTF-8）；对每条取改动文件全集的**仓库根相对路径**，按下表判域。域前缀：

| Go 域 | Rust 路径前缀 |
|---|---|
| `tool/` | `codex-rs/core/src/tools/` |
| `applypatch/` | `codex-rs/apply-patch/` |
| `agent/` | `codex-rs/core/src/agent/`、`codex-rs/core/src/agent_communication.rs`、`codex-rs/core/src/agent_message_board.rs`、`codex-rs/agent-roles/`、`codex-rs/agent-identity/`、`codex-rs/agent-graph-store/`、`codex-rs/agent-message-board-client/` |
| `prompt/` | `codex-rs/prompts/`、`codex-rs/skills/`、`codex-rs/ext/skills/`、`codex-rs/ext/goal/`、`codex-rs/core/src/skills.rs`、`codex-rs/core/src/prompt_debug.rs`、`codex-rs/core/src/realtime_prompt.rs` |

映射依据（Go 载体反查）：Go `tool/` = handlers+runtimes（`shell*.go`、`unified_exec*.go`、`codex_mode_exec.go`、`apply_patch_executor.go`、`view_image.go`、`wait_for_environment.go`…）；Go `applypatch/` = `applypatch.go`/`line_endings.go`/`cli.go`；Go `agent/` = `control.go`/`graph*.go`/`identity.go`/`role*.go`/`registry.go`/`tools_v2.go`…；Go `prompt/` = `instructions.go`/`skills_render.go`/`skill_selector.go`/`skill_shadow_*.go`/`goal.go`/`realtime.go`/`debug.go`。

筛选链：**in-domain ∧ 改动文件总数 ≤5 ∧ PR 不在队长所给已定案清单**（清单逐字取自派单）。

## 1. 枚举与筛选计数（原始）

- 解析到 200 条上游提交。
- in-domain：**34 条**（= 4 条 ≤5 文件候选 + 29 条 >5 文件 + 1 条已被排除的 `#51652`）。
- ≤5 文件候选（去排除后）：**4 条**。
- `prompt/` 域：**in-domain ≤5 文件提交 = 0**；`applypatch/` 域：**≤5 文件提交 = 0**（`applypatch` 只出现在 21 文件的 `#51203`）。
- 排除命中：本轮 pin 窗口内只有 `b17c74cfd5 (#51652)` 一条落在清单里（其余清单项都早于窗口）。

## 2. ≤5 文件候选 —— 五分类（全量，无遗漏）

| commit | PR | 域 | 文件数 | 上游标题 | 判定 |
|---|---|---|---|---|---|
| `c2ae67d769` | #51332 | tool | 1 | Record multi-agent wait duration by outcome | **已落地** |
| `41acdad246` | #51331 | agent | 1 | Track sub-agent result delivery outcomes | **已落地** |
| `7f892275e3` | #50977 | tool | 1 | Isolate tracing in the strict third-party tool deferral test | **纯测试** |
| `d4eed6dca5` | #50445 | tool | 1 | Assert that only direct tool calls emit timing events | **纯测试** |

### 2.1 `#51332`（`c2ae67d769`，`core/src/tools/handlers/multi_agents_v2/wait.rs`）= 已落地

- Rust 行为：completed `wait_agent` 记 `codex.multi_agent.wait.duration_ms`，tag `outcome ∈ {mailbox, steered, timed_out}`；**dropped（取消）的 wait 不记**。
- Go 载体：`appserver/agent_controller.go:763`（`runtimeAgentController.WaitForActivity` 内 `recordWait`）——`appserver/agent_controller.go:756` 注释即引 `Rust #51332, core/src/tools/handlers/multi_agents_v2/wait.rs`；metric 名 `telemetry/metric_names.go:99`；tag 词表 `telemetry/multi_agent_metrics.go:5-25`；回归 `appserver/multi_agent_wait_metrics_test.go`。
- 证据（逐条对照）：
  ```
  func (c *runtimeAgentController) WaitForActivity(...) {
      started := time.Now()
      recordWait := func(outcome string) { ... RecordDuration(telemetry.MultiAgentWaitDurationMetric, time.Since(started), {"outcome": outcome}) }
      select {
      case <-ctx.Done():      return nil, ctx.Err()                                  // dropped ⇒ 不记
      case message := <-mailbox: recordWait(MultiAgentWaitOutcomeMailbox)  ...
      case <-timer.C:            recordWait(MultiAgentWaitOutcomeTimedOut) ...
      }
  }
  ```
- **等价，含一处已记录分歧**：Go 无 `steered` 唤醒（`telemetry/multi_agent_metrics.go` 明确写「Go 的 activity mailbox 没有 steer-only 唤醒，`WaitOutcomeSteered` 属上游词表但在 Go wait handler 不可达」）。属**已落地（等价）**，不是缺口。
- 规模 S；不撞避让面；非 Windows-only。

### 2.2 `#51331`（`41acdad246`，`core/src/agent/control/completion.rs`）= 已落地

- Rust 行为：把终态子 agent 结果交给父线程时 `codex.multi_agent.result_delivery` ++，tag `outcome ∈ {queued, failed}`。
- Go 载体：`appserver/multi_agent_result_delivery.go`（`RuntimeRouter.recordMultiAgentResultDelivery(err error)`，注释引 `Rust #51331 (41acdad246, core/src/agent/control/completion.rs)`）；调用点 `appserver/turn_runtime.go:2606`；metric 名 `telemetry/metric_names.go:103`；tag `telemetry/multi_agent_metrics.go:12-13`；回归 `appserver/multi_agent_result_delivery_test.go`。
- **等价，含一处已记录分歧**：Go 的 delivery 在 guard 之后没有可达失败路径（mailbox enqueue 只在 nil mailbox / 空 thread·turn id 时拒绝），`failed` 臂保留以对齐上游序列 —— 文件内注释已写明。
- 规模 S；不撞避让面；非 Windows-only。

### 2.3 `#50977`（`7f892275e3`，`core/src/tools/spec_plan_strict_third_party_tests.rs`）= 纯测试（被测机制在 Go 无载体）

- Rust diff 只改**测试**：去掉 `#[tracing_test::traced_test]`，改 `#[tokio::test(flavor = "current_thread")]` + thread-local subscriber + `MockWriter` 缓冲，断言 deferral 警告与 `client_echo`。
- Go 侧：无 `spec_plan*` 文件（`git ls-files | Select-String 'spec_plan|strict_third_party'` = 0 命中），故无同测试可改。
- 被测机制在 Go **无载体**（0 命中原文）：
  ```
  $ git grep -n --fixed-strings 'Deferring third-party tool' -- '*.go'
  exit=1                      # 0 命中
  $ git grep -in 'third-party tool|strict_3p|code_mode_only_strict|Deferring third' -- '*.go'
  features/features.go:317:	{Key: "code_mode_only_strict_3p_tools", Stage: StageUnderDevelopment, DefaultEnabled: false},
  ```
  即：`code_mode_only_strict_3p_tools` 在 Go **只有 feature 声明、0 消费者**；Rust 的 strict third-party deferral 实现在 `core/src/tools/spec_plan.rs`（生产 5 处引用）。
- 判定：本条本身 **纯测试**；其机制属**独立较大项**（对应上游 `#50687` / `#50741` / `#50962` 一族的 Go 承载），不宜作为 ≤5 的落地项，故不列入候选。

### 2.4 `#50445`（`d4eed6dca5`，`core/src/tools/parallel.rs` 的 `mod tests`）= 纯测试（同 tracing 事件在 Go 无载体）

- Rust diff 只改 `parallel.rs` 的 `mod tests`：为 `tool_call_timing_guard_ignores_code_mode_source` 加独立 subscriber + `MockWriter` 缓冲，断言 direct + nested code-mode 调用合计**恰好 1 条** `codex.tool_call` 事件。
- 生产侧事件在 Go **无载体**（0 命中原文）：
  ```
  $ git grep -n --fixed-strings 'codex.tool_call' -- '*.go'
  exit=1                      # 0 命中
  ```
  Rust 生产发射点 `core/src/tools/parallel.rs:449`（`event.name = "codex.tool_call"`）。Go 的对应机制是 `turn/turn.go` 的 `TimingState.BeginSampling` / `BeginToolBlocking` → `TimingGuard`（`turn/turn.go:60/140/144`），**另一套实现、不发同名 tracing 事件**。
- 判定：**纯测试**（其 tracing 断言对 Go 无意义）；无需落地。

### 2.5 结论（§2）

**≤5 文件的 in-domain 候选里没有真缺口**：2 条已落地（且带已记录的等价分歧说明），2 条纯测试。这不是「只报好消息」——是指纹窗口内确实没有小体量缺口。

## 3. >5 文件 in-domain 提交（29 条，超限，仅登记 + triage）

`#Go 引用` = `git grep -c '#<PR>' -- '*.go'` 命中文件数。**它只是 triage 信号，不是落地判据**（见 3.2 的假警报）。

| commit | PR | 域 | 文件数 | 上游标题 | #Go 引用 |
|---|---|---|---|---|---|
| `18e28fe1b9` | #51556 | tool | 7 | Complete dynamic tool lifecycles on cancellation | 7 |
| `24edd7b890` | #51515 | agent | 15 | Expose detailed agent tree shutdown failure reports | 5 |
| `9479e1fdb7` | #51493 | agent,prompt,tool | 34 | Bind capability roots to environment selections | 4 |
| `c9870d0157` | #51482 | prompt | 66 | Use PathUri for skill identity and path matching | 18 |
| `e79c498b5e` | #51480 | agent,prompt,tool | 27 | Preserve tool declaration mode across resumed context windows | 9 |
| `61969074a7` | #51465 | prompt | 38 | Add an optional JSON transcript format for Guardian | **0** |
| `b0a6b8d86f` | #51463 | agent,tool | 51 | Record resolved model and reasoning effort in sub-agent activity | 10 |
| `cfc946f4e1` | #51415 | agent,prompt,tool | 149 | Expose and persist turn lineage across the app server | 5 |
| `551bd409eb` | #51402 | agent | 50 | Preserve turn attribution across recovery and compaction | 19 |
| `c0c230e673` | #51355 | agent,tool | 16 | Add bounded diagnostics for agent spawn failures | 7 |
| `588f616e8b` | #51347 | tool | 6 | Measure shell snapshot use and wait time per command | 6 |
| `6221a217e2` | #51329 | agent,prompt,tool | 37 | Remove partial-history subagent forks | 7 |
| `7ac954ea24` | #51253 | agent | 43 | Enforce Fast and Ultra Fast policies independently | 15 |
| `989c01a41a` | #51249 | agent,prompt | 16 | Handle partial answers consistently across agent workflows | 11 |
| `d63a9b8344` | #51223 | prompt | 27 | Remove legacy personality template metadata | 2 |
| `2dbcab90e2` | #51221 | agent | 46 | Separate environment requests from runtime selections | **0** |
| `4d15794336` | #51209 | tool | 23 | Add ranked tool discovery to JavaScript code mode | 1 |
| `c19525e55e` | #51206 | agent | 11 | Record initialization analytics for resumed subagents | 0 |
| `685270a56a` | #51203 | applypatch,tool | 21 | Make apply_patch preserve line endings unconditionally | 10 |
| `42312d4ff4` | #51157 | prompt,tool | 33 | Enforce required environment skills before model inference | 12 |
| `8571b9eaa4` | #51117 | prompt | 28 | Install full context in compaction replacement history | 2 |
| `335c7f8eca` | #50962 | tool | 16 | Gate stable environment tool exposure behind a feature flag | 15 |
| `550eb50545` | #50741 | tool | 21 | Keep environment-backed tools exposed across readiness changes | 8 |
| `58ae3ba611` | #50687 | tool | 13 | Keep third-party tools deferred in strict Code Mode Only | 1 |
| `58ca099b03` | #50562 | tool | 29 | Keep Code Mode tool discovery guidance stable across catalog changes | 3 |
| `55b6f282a8` | #50546 | tool | 14 | Keep MCP resource helpers available in code mode | 3 |
| `fd75aa116c` | #50447 | tool | 15 | Remove the provider capability gate for tool namespaces | 0 |
| `0df76892b1` | #50441 | prompt | 53 | Support ordered response items in world-state context updates | 1 |
| `c5d242fa79` | #50402 | tool | 20 | Consolidate command execution output into `aggregated_output` | 2 |

### 3.1 逐条核对过的 `#Go 引用 == 0` 项（4 条）

- **`#51206`（`c19525e55e`）= 已落地（假警报）**。Go 有完整载体：`telemetry/thread_initialized_event.go:8` `CodexThreadInitializedEventType = "codex_thread_initialized"`、`:31` `InitializationMode string \`json:"initialization_mode"\``；`telemetry/turn_event_test.go:972` 断言 `"initialization_mode": "resumed"`。⇒ **引用计数不可当落地判据**。
- **`#50447`（`fd75aa116c`）= 已等价（Go 无门可撤）**。该 PR 从 `ProviderCapabilities` 删 `namespace_tools`、从 `ModelProviderCapabilitiesReadResponse` 删 `namespaceTools`。Go 的结构体本就没有该字段：
  ```
  model/provider.go:23  type ProviderCapabilities struct {
      ImageGeneration bool
      WebSearch       bool
      ExternalWebAccess bool
      RemoteCompaction RemoteCompactionSupport
  }
  $ git grep -in 'namespace_tools|NamespaceTools|namespaceTools' -- '*.go'
  appserver/runtime_router_test.go:26111,26115,26141,26145   # 只是测试里的局部变量名，非字段
  ```
  ⇒ 无「provider capability gate」可撤，属**已等价**。（注：该 PR 的 Rust 生产面在 `core/src/client.rs`、`core/src/session/world_state.rs`。）
- **`#51465`（`61969074a7`）= 疑似缺口，L（38 文件），待你裁**。Rust 新增 Guardian 复核 transcript 的 `TranscriptFormat`（`line|json`）+ `transcript_mode` 配置（生产面在 `core/src/guardian/`、`prompts/`、`ext/guardian-reviewer`）。Go：
  ```
  $ git grep -in 'TranscriptFormat|transcript_format|json transcript' -- '*.go'
  exit=1                      # 0 命中
  ```
  （Go 里出现的 `transcript_mode` 是 **TUI** 的 local-settings 项，`tui/tea/local_settings.go:30` 自陈「Go has no transcript mode」，与 Guardian transcript 格式不是同一物。）
  字段格式：`sha=61969074a7 / PR=#51465 / Rust 文件数=38 / Go 载体=0 命中 / 规模=L / 撞避让面=否 / Windows-only=否`。⇒ 超 5 文件规则，**不开工**，报你裁定是否切子项。
- **`#51221`（`2dbcab90e2`）= 疑似缺口，L（46 文件），待你裁**。Rust 引入 `TurnEnvironmentRequest` / `TurnEnvironmentSelection` 与 `default_thread_environment_requests`、`validate_environment_ids_and_cwds`。Go 侧未见该对偶抽象（Go 用 `tool/tool_environment.go` + `tool/unified_exec_environments.go` 的既有形态）。字段格式：`sha=2dbcab90e2 / PR=#51221 / Rust 文件数=46 / 规模=L / 撞避让面=否（但会牵动 appserver 环境选择面）/ Windows-only=否`。⇒ 超限，**不开工**。

### 3.2 备注

- `#588f616e8b (#51347)` 是 6 文件，**只比阈值多 1**，且已落地（Go `appserver/shell_snapshot.go:239` 引 `Rust #51347`）——若你把阈值放宽到 ≤6，本轮多一条「已落地」，无新增缺口。
- `#51556 / #51515 / #51493 / #51480 / #51463 / #51402 / #51329 / #51253 / #51249 / #51203 / #51157 / #50962 / #50741 / #50562 / #50546 / #50441 / #50402` 等均有 Go 引用，属大体量多批落地/在飞面，本轮不重复判定。

## 4. 结论与建议

1. **≤5 文件窗口内无真缺口候选**：`#51332`、`#51331` 已落地（各带一处已记录的等价分歧），`#50977`、`#50445` 纯测试。⇒ 你这轮不必为这 4 域派小体量落地单。
2. 两个**建议另行立项（L）**：`#51465`（Guardian JSON transcript 格式，38 文件）与 `#51221`（environment request/selection 拆分，46 文件）——都是「Go 0 载体」的真实形态，但规模超限、需架构决策，请裁定是否切子项。
3. 一个**结构性观察**：`code_mode_only_strict_3p_tools` 在 Go **已声明 feature、0 消费者**，其对应上游族（`#50687` / `#50741` / `#50962`）Go 侧引用稀疏（1 / 8 / 15）。若要补 strict third-party tool deferral 能力，建议整体立项而非逐 PR 落地。
4. `prompt/` 与 `applypatch/` 两域在 200 笔窗口内**没有小体量提交**（唯一的 `applypatch` 提交 `#51203` 是 21 文件且已落地），与 round86/87 的观感一致：这两域的上游演进已转入大改动节奏。

## 5. 附：① `syncw4e` 回收结果

`D:\qax\reagent\dev\codex_go_wt\syncw4e` **在派单到达前已不存在**（上一轮我按授权一并回收了 `syncw4e`/`syncw4e_base`），本轮复核证据：

```
$ Test-Path 'D:\qax\reagent\dev\codex_go_wt\syncw4e'
False
$ git -C D:\qax\reagent\dev\codex_go worktree list | Select-String 'syncw4'
（无输出）
$ Get-ChildItem 'D:\qax\reagent\dev\codex_go\.git\worktrees' -Directory
aud7_replay aud8_replay aud9_replay integ86b integ86c integ86c2 integ86e integ86f integ86g
syncw1 syncw1base syncw1c1 syncw1gate syncw1r86 wt2e wt624 wt632 wt632base wt632lf wt97 wt984 wt_c9c6
   # 无 syncw4e / syncw4e_base
$ git -C D:\qax\reagent\dev\codex_go worktree prune --dry-run -v
（无输出，exit=0）
```

**残留副本内容核实（无需担心丢失未落地内容）**：`syncw4e` 的唯一目的是那条 **appserver options-injection seam**（给 `runtime_router.go` 注入 `MetricSink`），已被你裁定**作废**（`#51652` 取进程全局 `metrics.Counter`）。该 seam **从未落主**：
```
$ git grep -n 'MetricSink' -- '*.go'          # 全部命中都是无关的既有 app/daemon_telemetry.go
app/daemon_telemetry.go:37  daemonMetricSink ...
   # tool/ 与 appserver/ 内 0 命中 ⇒ 注入字段/构造点从未存在
```
⇒ 无未落地内容随回收丢失。

## 附：可复跑命令

```
# 枚举（Rust 对象库，只读；不移动 C:\rw 的 HEAD）
git -C C:\rw\codex-rs fetch origin
git -C C:\rw\codex-rs log --numstat --format='@@%h|%s' -200 d83bb540ec64bf6b009bca0283b0be91ea33f26a
# 单笔核对
git -C C:\rw\codex-rs show c2ae67d769 ; git -C C:\rw\codex-rs show 41acdad246
git -C C:\rw\codex-rs show 7f892275e3 ; git -C C:\rw\codex-rs show d4eed6dca5
# Go 载体核对（Go 仓）
git grep -n '#51332|#51331' -- '*.go'
git grep -n --fixed-strings 'Deferring third-party tool' -- '*.go'   # exit=1
git grep -n --fixed-strings 'codex.tool_call' -- '*.go'              # exit=1
git grep -in 'TranscriptFormat|transcript_format|json transcript' -- '*.go'   # exit=1
# 本轮解析脚本（只读）：%TEMP%\scan3c.py、%TEMP%\scan3_tables.py
```