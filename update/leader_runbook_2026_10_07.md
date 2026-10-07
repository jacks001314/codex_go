# 队长运行手册（2026-10-07）

> 本文件是**长期生效**的团队运行规则（用户指令固化），与 `plan_2026_10_07.md`（逐轮记录）、
> `remaining_ledger_2026_10_07.md`（权威台账）、`leader_corrections_2026_10_07.md`（口径订正）并列。

## R1 上下文过大 ⇒ 不派新任务，回收换班（用户指令，长期）
- 当某个子 Agent 的上下文「过大」时（判据：已跨多轮、长回报、单 turn 反复读写大文件），
  **不再给它派新任务**。
- 只让它**把当前在办项收尾**（commit + 反向对照 + 回报），**不追加新项**。
- 收到其收尾回报后**立即 `agent_admin_remove`**（`confirm_delete=true`，`delete_descendants`/`cleanup_home` 按需）。
- 剩余未开工/未完成的工作**移交新 Agent** 接手；新 Agent 一律用 `agent_admin_create_derived`（**不 fork** 队长上下文）。
- 目的：避免把大上下文反复注入，**尽可能节约 token**。

## R2 边交付边入库（用户指令，长期）
- 车道一交付（commit + 证据齐）就立刻 `cherry-pick -n` → 逐 blob 判等 / patch-id 判等 → gofmt/build/vet → 包测试 →
  **队长自跑一条反向对照** → commit → `git push git@github.com:jacks001314/codex_go.git main:main` → `git ls-remote` 确认。
- 不为凑批次而积压；允许多笔小提交连续入库。

## R3 判据纪律
- 「未落地」不能只用 `git cherry`（rebase/改写会因 patch-id 不等误报）；必须回到 PR 号 `git log --grep` + 代码面 grep。
- 不可落地必须给「正向陈述 + 可复跑命令 + 实际输出」，禁用「结构性等价 / 语义相同」代替证据。
- 反向对照必须是「撤掉修复 ⇒ 具体测试 FAIL 原文」，恢复后 PASS。

## R4 常用口径
- parity 必须带 `CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs`（否则全 Skip 假通过）。
- push 必须 SSH：`git push git@github.com:jacks001314/codex_go.git main:main`。
- project 镜像不跑 git；文档用 `project_file_sync`（覆盖需 `base_etag`）。
- shell 是 zsh：`$VAR` 多值不词分割（落文件后 `while read`）；批量删文件用 `python3`。
- A2A：`type=request/response/message` + `mode=async`，不 sleep 不轮询；同一 request 不可重发。
- 提交编号只在 main 侧编排（车道自编号常撞号）。

## R7 并入必须「整提交并入」（2026-10-07 血案教训）
- `git cherry-pick -n <sha>` 之后**不要按路径子集 `git commit -- <paths>`**，除非先证明该车道提交的**文件集互不相交**。
- 正确做法二选一：① 整提交一次 commit；② 先 `git show --name-only <lane-sha>` 与 `git show --name-only <my-commit>` **比对文件集**，缺哪个补哪个。
- **实例**：sync534 `e721afed`(#49339) 与 sync535 `e9ff5187`(#47932) 漏掉 `appserver/runtime_router_test.go`（Bedrock provider-fallback 断言仍期望旧的 `gpt-5.6-sol`），导致 main 上 `TestRuntimeRouterThreadStartProviderModelFallbackUsesBedrockStaticCatalog` 失败，直到 sync545 跑全包才发现 → 用 **sync546 `4ba47a09`** 回补（`git checkout 35205331 -- appserver/runtime_router_test.go`）。
- **附加规则**：每次并入后跑**受影响包的整包测试**（不只是 `-run` 子集），并把「新增失败」与基线清单逐条比对。

## R5 已知基线失败（勿「修」）
`appserver` **当前 main 实测 3 项**（GatewayOAuth / PluginList per-repo / TurnStartFileChangeApplyFailure）—— 在 `4fcf61c6` 上 `TERM=xterm-256color go test ./appserver/ -count=1` 恰 3 FAIL；历史基线 `bb2dedd1` 上为 4–5 项（多 `TestOtelProviderReloadsAfterAccountChange` 偶发 + `TestRuntimeRouterThreadStartProviderModelFallbackUsesBedrockStaticCatalog`，后者已由 sync546 修复，勿再当基线）·
`tool` 3 · `execserver` 3（symlink / TemporaryDirectories / sandbox helper）· `config` 4 · `model` 2
（`TestRefreshBedrockAWSCredentialsRunsCommandOnceAndReloads`、`…BoundsProviderRecoveryPerRequest`）·
`applypatch` 1 · `mcp` 1 · `tui` 1（`TestSelectStartupTooltipMatchesRustPlanBranches`）· `tui/tea` 1（`TestModelAppCommandUsesRustHistoryMessages`）·
并发 flake（隔离单跑全绿）：`TestRuntimeRouterThreadShellCommandEmitsUserShellNotifications`、`TestStdioServerGoalSetResponsePrecedesGoalNotification`、`TestStartProcessHonorsArg0`。

## R6 当前团队（2026-10-07 第 85 轮续 7；派生宽度上限 10）

main = `f2175d07`（已 push）。在跑 **8 条**（写集互不相交为主，例外已加「先请示」约束）：

| 车道 | agent_id | 任务 | 写范围 |
|---|---|---|---|
| syncnext4 | `agent-62b358bf8556f8cbd689edaf` | #49160 part a 已交付（= `2eb25440`）；续做 **b/c2 判定** | config/ tui/onboarding/ tui/app/ app/ |
| syncnext5 | `agent-1fe48c6b2b6e6cf3cb811aaf` | #38470 Bedrock Runtime 专属目录（`global.`/`us.` 变体）+ preferred-model 漂移 | model/ |
| syncnext7 | `agent-69e1c7ae355e6cb869a7cd6d` | #50803 managed daemon 用于 remote-control 启动 → #49360（先侦察后定） | appserverdaemon/ cli/ exec/ |
| syncnext9 | `agent-c660727b1ec1ff85fe211ffe` | #50804 review 生命周期顺序 → #51063 delegate 先前取消 | turn/ session/ |
| syncnext11 | `agent-0499220c268f1de9da3cea7f` | #51117 压缩替换历史装完整上下文 | context/（turn/ 需请示） |
| syncnext12 | `agent-ac538099e50d4129bfabd665` | #51355 agent spawn 失败的有界诊断（先侦察） | tool/ appserver/ telemetry/ |
| syncnext13 | `agent-055c9e303eb75b35dbc30ac0` | #51378 WS 连接池并发预热（先侦察；同语义才碰 remotecontrol/） | codexapi/ |
| syncnext14 | `agent-414b7d5374d217880cff0748` | #50808 精简 TUI 快照并合并行为测试 | parity/ tui/（仅测试） |

- 已回收（累计）：`sync51480`、`syncmcpcfg`、`synctui`、`verify51482`、`sync51482`、`syncparity2`、`syncnew1`、`syncnew2`、`syncnew3`、`syncnext1`、`syncnext2`、`syncnext3`、`syncnext6`、`syncnext8`、`syncnext10`。
- 待补：新 verifier（在飞数量降到 ≤3 时创建）。
- **附加规则（续 5）**：车道可能在回报后**继续 amend 已交付 commit**；并入前必须比对最终 SHA 的 delta。
- **附加规则（续 6）**：写集相邻的车道**开工前先请示**。
- **附加规则（续 7）**：**反向对照必须打在真正的被测接线上**（sync559 实例：改 `readPluginManifestForRootCached` 不变红，真接点在 `List` 的 `loadMarketplacePlugins(..., s.manifests)`）。若反向对照「撤销后仍绿」，说明改错了位置，须重新定位后再报。
