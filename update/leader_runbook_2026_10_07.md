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

## R3 增补 · 行为探针（2026-10-07 第 85 轮续 8）
- 「PR 号 0 命中 + Rust 最长标识符在 Go 0 命中」**只对「Go 无该子系统」成立**。对**已有 Go 端口的子系统**（如 `voicehost/`、`mermaid/`、`sandbox/`、`sandbox/windowssandbox/`）会**误判为 N/A**——本轮 4 项即如此。
- 因此对「Go 有同子系统载体」的可疑项，**必须跑行为探针**：把 Rust 上游测试的输入/向量喂给 Go 真码（`go test -overlay=<tmp>/overlay.json`，测试文件放 /tmp，不落仓），比对可观测行为（计数、时间戳、返回错误）。
- 判定「可落地」必须给出：载体 file:line + 探针命令与输出 + 落点行数与规模档（S/M/L）。

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

## R6 当前团队（2026-10-07 第 85 轮续 8 四；宽度上限 10，稳态 4–6 落地 + ≤1 裁决）

main = `757aba59`（已 push）。在跑 **7 条**：

| 车道 | agent_id | 任务 | 写范围 |
|---|---|---|---|
| syncnext15 | `agent-1f27bd953dc51f2c8a30bcc4` | #49360 (b) hook PATH prepend | tool/shell*.go+unified_exec.go、appserver/runtime_router.go、turn/tools.go |
| syncnext18 | `agent-ffb3dfd067e731baff9d95ad` | `appserver/hooks_discovery.go` 哈希统一（收尾） | appserver/hooks_discovery*.go |
| syncnext23 | `agent-ac3df535dc42cff961b49dbc` | #48611 + #49032 | context/ session/ features/ state/ |
| syncnext24 | `agent-a4ee3c22dcf584158c31fc20` | #49441 | codexapi/ |
| syncnext27 | `agent-558ead9e9ae4b783c8627b98` | #48824（voicehost）；#48565 转为复核 N/A | voicehost/ sandbox/ |
| syncnext28 | `agent-da7d92480b8c072ac06a3056` | #48895（mermaid）+ #49058（ACL 长路径） | mermaid/ sandbox/windowssandbox/ |
| syncnext29 | `agent-5e1ba3bf4c240eac7c4430e0` | #48828（归档前物化）+ #49267（远程 message board） | appserver/router*.go、message_board*.go、config/config.go |

- 已回收（累计 31，含本轮 `syncnext26`）。
- **下一批待派（暂缓）**：`#49432`、`#49260`、`#49075`（均需 `appserver/runtime_router.go`，等 syncnext15 交付后成批派）。
- 待补：新 verifier（在飞 ≤3 时创建）。
- 附加规则（续 5/6/7/8）+ **R8 产出率优先** 见上文；**R3 增补**：对「已有 Go 端口的子系统」必须用行为探针判定。

## R9 车道禁止提交（用户指令，长期生效，2026-10-07 起）

用户原话：「每个 agent 都不要自己提交代码，由你 leader 自己验证和提交。」

- 所有下属 Agent（车道）**不得**执行 `git commit` / `git push` / 创建 tag / 移动分支指针 / `git stash` 自己的改动。
- 车道产物 = **工作区里的未提交改动**（含新增文件）；不再以「车道分支 + 提交」作为交付载体。
- 队长职责扩展为「**验证 + 提交 + 发布**」：队长亲自进入车道 worktree 复核改动、跑门禁（gofmt / `go build ./...` / `go vet` / 受影响包整包测试对基线 / 反向对照 RC），再由**队长提交**并 push main。
  - Windows 车道（同机同仓）：队长 `git -C <wt> add -A && git -C <wt> commit`（在车道分支上）→ cherry-pick 到 main。
  - Linux 车道（跨节点）：车道 `git add -A -N && git diff --binary > /tmp/<lane>.patch` → `project_file_sync` 到镜像 `update/r86_patches/<lane>.patch`；队长 `git apply --index` 后复核提交。
- 车道回报强制字段：`git status --porcelain` 原文 + `git diff --stat` + 每条落点 `file:line` + 门禁实跑输出 + **RC 原文**。
- 配套变化：`git patch-id` 判等不再适用于车道交付（无 commit）；改为「队长提交后 `git diff <lane-wt>` 为空」等价校验。
- 车道分支/ worktree 仅作隔离用途；回收前不再需要 `git log main..<branch>` 孤儿普查（车道不产生 commit）。
## R5-bis 基线失败清单修订（2026-10-16 · 第 90 轮实测，leader 复跑）

本节点（16 vCPU / 15.7GB）实测，判"新增失败"以本表为准（R5 原表已过时）：

| 包 | 基线失败数 | 具体用例 | 触发条件 |
| --- | --- | --- | --- |
| appserver | 6 | ExecutorSkillPathEqualsMatchesWindowsIdentityLikeRust / MultiAgentWaitDurationRecordsOutcomeLikeRust / RequiredSkillsPreSamplingValidationReleasesSatisfiedTurnsLikeRust / ShellSnapshotCommandMetricsLikeRust / …ReportProtectedAndUnavailableLikeRust / …SkipNonDirectLaunchesLikeRust | 恒触发 |
| appserver | +3（pin 漂移，非代码缺陷） | RustAppServerV2SuiteManifestCoversRustModules / BuildProtocolSchemaMatchesRustStableFixtures / BuildTypeScriptProtocolSchemaMatchesRustFixtures | **仅当 CWD 能解析到 `D:\qax\reagent\dev\git\codex\codex-rs`**（即主工作区 `codex_go\appserver` 这类两级上溯路径）；Rust 镜像已推进到 `4eb4a65407` 而 Go pin 仍是 `95ec468619` ⇒ 期望值漂移。在 `codex_go_wt\*` 判定台里这三条会 SKIP（fixture 找不到）⇒ **跨目录比较结论时要先对齐 CWD 语义** |
| state | 1 | TestOpenRuntimeDBValidatesBeforeMigrateLikeRust（`sqlite_validation_test.go:288: validation attempts after reopen = 1, want 2`） | 恒触发（R5 原表未收录 state） |
| parity | 13 | 见 `update/r90_verify_log_2026_10_16.md` / pin 车道报告 | 需设 `CODEX_RUST_ROOT`；单测台若解析不到 Rust 树会全 Skip（假通过） |

补充环境事实：

1. 跑 parity 必须显式 `$env:CODEX_RUST_ROOT`，否则会回落到陈旧检出（本机回落到镜像主工作树，其 rev 决定期望值）。
2. `CODEX_RUST_APP_SERVER_SUITE_V2` / `CODEX_RUST_APP_SERVER_PROTOCOL_SCHEMA` 未设时，`appserver` 的 fixture 测试按 CWD 上溯候选路径找 `..\..\git\codex\codex-rs\…` ⇒ 判定台目录层级会改变结果（在 `D:\qax\reagent\dev\codex_go_wt\*` 下跳过，在 `D:\qax\reagent\dev\<bench>` 下命中）。
3. 本机 `core.autocrlf=true`：新 worktree 全树 CRLF，`gofmt -l .` 会输出全部 .go（换行符产物）；门禁只对"本次改动文件"跑 gofmt。

### R5-ter · Rust 面向测试的 CWD 依赖（2026-10-16 实测，必读）
- `appserver` 里三条 Rust 面向测试用**相对路径**找 Rust 树：
  - `rustAppServerProtocolSchemaRoot` / `rustAppServerProtocolTypeScriptRoot` / `rustAppServerProtocolRustRoot`（`appserver/schema_test.go:754-819`）
  - `rustAppServerV2SuiteRoot`（`appserver_v2_suite_manifest_test.go:241-263`）
- 候选顺序默认是 `..\..\git\codex\codex-rs\...`、`..\..\codex-main\codex-rs`、`..\..\..\codex-main\codex-rs`；测试进程的 CWD = **包目录**。
  ⇒ `D:\qax\reagent\dev\codex_go\appserver` → `..\..` = `D:\qax\reagent\dev` → 命中镜像 `dev\git\codex\codex-rs`（**RUN**）
  ⇒ `D:\qax\reagent\dev\codex_go_wt\r90X\appserver` → `..\..` = `codex_go_wt` → 全不命中（**SKIP**）
- **同一份代码，appserver 包红数不同**：主工作区 9 红（既有 6 + pin 漂移 3）；车道 worktree 6 红（3 条被 SKIP）。
- 车道里要让它们真跑，必须设 env：`CODEX_RUST_ROOT`（需含 `Cargo.toml`）、`CODEX_RUST_APP_SERVER_SUITE_V2`（需含 `mod.rs`）、`CODEX_RUST_APP_SERVER_PROTOCOL_TYPESCRIPT`；报数前必须用 `-v` 证明是 `=== RUN` 而非 `--- SKIP`。
- 结论口径：**基线数字必须带上下文**（主工作区 / 车道台 / 是否设 env），否则会得出错误的「无新增失败」。
### R5-quater · 门禁口径（2026-10-17 第 90 轮，leader 实测；与 R5-bis / R5-ter 冲突时以本节为准）

1. **appserver 整包的唯一有效判定口径 = 主工作区 + 显式全 5 env + CWD=包目录 + 清 TERM**：
   - `GOCACHE=D:\qax\reagent\dev\.gocache-d`
   - `CODEX_RUST_ROOT=C:\rw\codex-rs`
   - `CODEX_RUST_APP_SERVER_PROTOCOL_SCHEMA=C:\rw\codex-rs\app-server-protocol\schema\json`
   - `CODEX_RUST_APP_SERVER_PROTOCOL_TYPESCRIPT=C:\rw\codex-rs\app-server-protocol\schema\typescript`
   - `CODEX_RUST_APP_SERVER_SUITE_V2=C:\rw\codex-rs\app-server\tests\suite\v2`  ← 是 `app-server\tests\suite\v2`，**不是** `app-server-protocol\tests\...`（写错会静默 SKIP 掉 suite v2 那批）
   - 命令：`go test . -count=1 -p 1`（CWD = `…\codex_go\appserver`）
   - 实测：`ok 121.3s / 131.7s / 175.4s`，**0 红**（含 P-b9 + A-b8 + B-b8 + C-b8 + TUI 全部并入之后）。
2. **SKIP 会被读成绿（本轮最大教训）**：依赖 Rust 源码/schema 的守卫找不到目标时走 `t.Skip`，整包照样打印 `ok`。P-b9 的 `TestRustStableClientSurfaceIsNotGatedAsExperimental` 在未设全 env 的车道台上正是 SKIP —— 台上"ok 161s"根本没跑它，差点让一个真缺口被判为已修。
   ⇒ **任何"守卫已绿"的声明必须附 `-v` 的 `=== RUN` + `--- PASS` 原文，以及 RUN/PASS/FAIL/SKIP 计数**；只有 `-v` 能区分 RUN 与 SKIP。
3. `./app/` 基线补一条：`TestFeaturesEnableUnderDevelopmentWarnsLikeRust`（`app_test.go:690`）**断言过时** —— `features/features.go:60` 已是 `StageRemoved`，Rust 同 pin `features/src/lib.rs` 也是 `Stage::Removed` ⇒ Go 行为正确、断言旧，属待改断言，不是产品缺陷。
4. `TestNoDaemonRejectionsLikeRust`（`./app/`）与 `TestMCPStdioLaunchedServerHasNoConsoleWindow`（`./mcp/`）在 `TERM` 未清时会红；清 `TERM` 后各自 PASS（隔离 ×3 复核）⇒ **判红前先 `Remove-Item Env:TERM`**。
5. 已实测的**抖动红**（同代码不同次结果不同，不得计入"新增失败"）：`TestRuntimeRouterTurnEventReportsMultiAgentVersionLikeRust/persisted_v2_thread`、`TestRuntimeRouterTurnCompletionEmitsPerModelTokenUsage`、`TestRuntimeRouterCommandExecutionEmitsAnalyticsLikeRust`、`TestRuntimeAgentControllerV2SpawnCarriesInternalInputLikeRust`（`testing.go TempDir RemoveAll cleanup: The directory is not empty`，Windows 清理竞态）。判 flake 的门槛 = 隔离复跑 ×3 全 PASS。
6. 既有基线红（主工作区，与车道改动无关）：`./tool/` 2 条（`TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust`、`TestLocalShellLaunchRestoresExecutorPathDirsLikeRust`）、`./model/` 1 条（`TestApiKeyCyberAccessProgramsFromConfigLikeRust` —— 断言与 `features/features.go:326` 的 `StageStable / DefaultEnabled: true` 自相矛盾）、`./tui/tea/` 1 条（`TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge`）、`./app/` 2 条（见第 3、4 点）。
7. `go vet ./...` 白名单 3 条（长期既有，勿"修"）：`sandbox/linuxsandbox/bwrap_test.go:182`、`model/responses_agent.go:1500`、`exec/agent_controller.go:909`。