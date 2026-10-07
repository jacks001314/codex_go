# 队长订正台账（2026-10-07，leader 维护）

本文件只记录**队长亲自复核过**的台账订正与流程事实，不与
`update/remaining_ledger_2026_10_07.md`（作者：verify51482）互相覆盖：
verifier 继续追加其分节，本文件独立，避免同一文件并发写入冲突。

## C1. `#49262` 与 `#51249`：台账 ✅ 属**过度声明**，实际只落地 1/3

| 台账位置 | 原判定 | 订正 |
|---|---|---|
| `remaining_ledger_2026_10_07.md:401`（`#49262`） | ✅（Go commit `46d9a9eb`） | **部分落地 1/3** |
| `remaining_ledger_2026_10_07.md:509`（`#51249`） | ✅（引用同一个 Go commit `46d9a9eb`） | **待重新审计**（该 commit 的标题与 `#49262` 相同，引用可疑） |

上游 `a7660cd154`（#49262 "Trace turn phases and correlate accepted input with turns"，
6 文件 / +398 −10）含**三项**独立改动，Go 只落地第三项：

| 上游改动 | Go 证据 | 结论 |
|---|---|---|
| ① `codex.turn_input` span（started / steered / recovered 的已接受 `turn.id`，跨 trace 关联） | `grep -rn '"codex.turn_input"' --include='*.go' .` = **0** | **未落地** |
| ② `codex.turn.phase` span（`codex.sampling` / `codex.tool_blocking` / `codex.compaction`，且 sampling span 必须在 drain 工具**之前**结束） | `grep -rn '"codex.turn.phase"\|"codex.sampling"\|"codex.tool_blocking"' --include='*.go' .` = **0** | **未落地** |
| ③ 邮箱抢占事件 `codex.mailbox_preemption` | `grep -rn '"codex.mailbox_preemption"' --include='*.go' .` = **4**；main `11b6bd1f sync485` + `46d9a9eb sync488` | 已落地 |

- Go 侧不是「无落点」：span 基建齐备（`telemetry/session_telemetry.go` 的 `StartSpan`、
  `telemetry/traces_client.go`），故属**真缺口**，不是结构性 N/A。
- 复跑命令（可复核）：
  ```bash
  cd /home/jacks/jacks_dev/codex_go
  grep -rn '"codex.turn_input"' --include='*.go' . | wc -l                 # 0
  grep -rn '"codex.turn.phase"\|"codex.sampling"\|"codex.tool_blocking"' --include='*.go' . | wc -l  # 0
  grep -rn '"codex.mailbox_preemption"' --include='*.go' . | wc -l         # 4
  ```

## C2. 流程事实：A2A 未闭合 request 积压

`poll_a2a_inbox` 在 2026-10-07 晚返回 `remaining_count` 达 **92–93**，最早条目来自
main `a9e852bc` / `2eef2b1a` 时代（早已回答过的历史派单）。这类 `type=request`
在未收到 `type=response` 前会一直留在队列里，属**记账积压**，不是新派单。
- 处置：队长不再逐个重答历史 request（重发会被 Master 以
  `A2A request already has a response from this agent` 拒绝）。
- 影响：**队列长度不能当作工作量指标**；判断某车道是否有活，以
  `list_a2a_agents` 的 `Phase` + `git cherry main <lane-tip>` 为准。

## C3. 已并入的一笔「交付回退」拦截（承前）

`#49804`（TUI 平台标签表）在 main 已有 `edfabadd sync486`；车道两次提交
（`8c90ce4e` / `fb89d560`，patch-id 相同 `5d704a6c25b69fd5c64decb85ab7905317ba2dc2`）
把它**改回硬编码**，方向与上游 `d2f2c40095` 相反，已驳回并令其删除分支
（车道回报：`git branch -a | grep -c synctui9` = 0，两版不再被任何 ref 引用）。

## C4. `#49325`：行为半已落地，上游的模块级测试半**不可移植**（记录为有意差异）

- **已落地**（sync532 `fe88a5a8`）：`sandbox/windowssandbox/elevated/runner_client.go` 的
  `RetryRunnerSpawnOnce` 增 `errorServiceAlreadyRunning = 1056` 常量与「同凭据重试一次、
  **不刷新**」分支，位置与上游一致（在 refreshable 分支**之前**）；三条回归用例
  （recovery / 两次上限 / child-startup 排除）逐条对照 Rust 测试名。
- **未落地**：上游同 PR 还给 `windows-sandbox-rs/src/unified_exec/backends/elevated_tests.rs`
  加了 +83 行「统一执行层」用例，断言重试**保留原始请求与凭据**、命令**只跑一次**。
- **不可移植的判据（正向陈述 + 可复跑命令）**：
  ```bash
  cd /home/jacks/jacks_dev/codex_go
  grep -n "RetryRunnerSpawnOnce" sandbox/windowssandbox/elevated_impl.go   # :72 与 :168 两个调用点
  grep -n "go:build" sandbox/windowssandbox/elevated_impl.go               # 文件为 Windows-only
  ```
  该层的 spawn 闭包**硬编码** `elevated.SpawnRunnerTransport`（Windows 实现 / 非 Windows 走
  `runner_client_stub.go`），没有注入口；要复刻上游那条用例必须**给生产代码加测试缝**，
  与本轮纪律（不新增生产 API）冲突。故按「行为已落地、上游测试半无等价注入口」登记，
  **不是静默省略**。
- 复核命令（行为半可自证）：
  ```bash
  go test ./sandbox/windowssandbox/elevated/ -count=1   # 4 项 PASS（含 3 条 ...LikeRust）
  ```
