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
5. 已实测的**抖动红**（同代码不同次结果不同，不得计入"新增失败"）：`TestRuntimeRouterTurnEventReportsMultiAgentVersionLikeRust/persisted_v2_thread`、`TestRuntimeRouterTurnCompletionEmitsPerModelTokenUsage`、`TestRuntimeRouterCommandExecutionEmitsAnalyticsLikeRust`、`TestRuntimeAgentControllerV2SpawnCarriesInternalInputLikeRust`、`TestRuntimeRouterTurnStartResizeNoticeEnabledEmitsDeveloperFragmentLikeRust`（后两条的签名是 `testing.go:1464 TempDir RemoveAll cleanup: unlinkat … The directory is not empty`，Windows 清理竞态）。两名车道独立观测到全包红数在 6↔7 之间抖动：**判"新增红"前必须先隔离 `-count=3` 复核当次那条**，只有隔离仍红才算红。（车道 V 在 `5beeb279` 上实测：该包确定性红 6 条，另 1 条每次不同；`TestRuntimeRouterTurnStartResizeNoticeEnabledEmitsDeveloperFragmentLikeRust` 隔离 `-count=3` ⇒ PASS ×3。）
6. 既有基线红（主工作区，与车道改动无关）：`./tool/` 2 条（`TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust`、`TestLocalShellLaunchRestoresExecutorPathDirsLikeRust`）、`./model/` 1 条（`TestApiKeyCyberAccessProgramsFromConfigLikeRust` —— 断言与 `features/features.go:326` 的 `StageStable / DefaultEnabled: true` 自相矛盾）、`./tui/tea/` 1 条（`TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge`）、`./app/` 2 条（见第 3、4 点）。
7. `go vet ./...` 白名单 3 条（长期既有，勿"修"）：`sandbox/linuxsandbox/bwrap_test.go:182`、`model/responses_agent.go:1500`、`exec/agent_controller.go:909`。
### R5-quinquies · A2A 收件箱积压 与「陈旧声称」复核（2026-10-17 第 90 轮 turn#25，leader 实测）

1. **收件箱的 `remaining_count` 不是待办队列**：turn#25 实测 inbox 积压 **66–72 条未读**，抽样 7 条（A 车道 batch2/A6、C 车道 batch3、B 车道 batch3、V 车道 V2/V3、TUI T1、pin phase2）——**全部是当日早些时候的响应，其工作都已并入 main**（A6 #54060 = `2eae50f6`；C5 #54257 = `f5a4f781`；pin phase2 的 schema/manifest 重锚 = 后续 pin 批次）。新响应走**自动唤醒**投递，不会因积压被挡住 ⇒ **不要为了清空计数停下手头工作**，只在有空档时抽样；抽样结果要按「当前 main 是否已含该工作」判，而不是按回单自述判。
2. **陈旧回单里的「缺口」必须先用当前 main 复核再派工**（本轮三条实测已修）：
   - `tool/code_mode_exec.go` 的 cell/嵌套 ID 串号（pin phase2 §⑤）⇒ **已修**：`nextCellID` 与 `nextID` 已拆分（`:76` 注释、`:693` `cell-%d` 走 `e.nextCellID.Add(1)`），`TestRuntimeRouterCodeModeExecEndToEnd…` 已不在 appserver 红名单。
   - `appserver/protocol.go` 缺 `thread/readState/changed`（pin phase2 §⑥）⇒ **已修**：`appserver/protocol.go:205` `NotificationThreadReadStateChanged`、`:3232` 通知结构齐备。
   - `TestPrecomputedExportArtifactsMatchTargetRustCommit` 期望常量陈旧（V3 §⑥）⇒ **已修**。
3. **一条被误报的「漂移」，固化口径**：pin 阶段报「`CodeModeDefaultExecYieldTime` 30s vs Rust `DEFAULT_EXEC_YIELD_TIME_MS` 10s」。实测 **两条是不同的东西**：
   - Rust **配置默认** `DEFAULT_CODE_MODE_EXEC_YIELD_TIME_MS = 30_000`（`codex-rs/core/src/config/mod.rs:1204`，经 `description.rs:31` 的 `{{ default_exec_yield_time_ms }}` 渲染进 tool 描述）⇒ Go `tool/code_mode_exec.go:35` 的 30s 对齐，`code_mode_yield_default_like_rust_test.go:29` 已钉。
   - Rust `DEFAULT_EXEC_YIELD_TIME_MS = 10_000`（`codex-rs/code-mode-protocol/src/runtime.rs:15`）**只在 `description.rs` 的 `#[test]` 里当夹具**（`:756/:775/:834/:879/:986/:1026/:1071/:1088` 全在测试函数内）；用户可见的 "Defaults to 10000 ms." 是 **`wait` 工具的静态文案**（`description.rs:50`），Go 逐字镜像于 `tool/code_mode_exec.go:576`，并由 `:591` 只替换**第一处**。
   ⇒ 判 **无漂移，不要改**。教训：比对两个「同名近似」常量前，先确认各自消费者（配置路径 vs 协议夹具/静态文案）。
### R5-sexies · 第 90 轮 turn#26 修订（2026-10-17，leader 实测）

1. **R5-quater 的一处措辞被推翻**（P-b10 车道实测 + leader 复核）：原文说「车道台在 `dev\codex_go_wt\<name>`（两层）就 SKIP」——**不成立**。决定 RUN/SKIP 的是 **env 里的绝对路径**，不是台面层级：只要显式给全 5 个 env，任意层级的台面守卫都真 RUN（`=== RUN` 且 PASS）。⇒ 口径改为「**必须显式给全 5 env；给全后任意层级都真 RUN**」。否则车道为了凑「主工作区」把自己的改动挤出台面，反而测不到自己的改动。

2. **新增已知 flake（Windows 清理竞态，勿判红线）**：`appserver` 整包可能报
   `--- FAIL: TestRuntimeAgentControllerSpawnUsesCapturedStepSettingsLikeRust`，
   全文只有 `testing.go:1464: TempDir RemoveAll cleanup: unlinkat ...\<dir>: The directory is not empty.` —— **无任何断言失败**。
   实测：孤立 `-count=3`（合并树）全 PASS、`-count=5`（pristine `007ffa2f`）全 PASS。
   ⇒ 判红前先看 FAIL 行是不是**只有 cleanup 文案**；是则判环境型，不计入新增失败。

3. **「0 命中 ⇒ N/A」必须带时点**（TR-7 自纠，leader 采纳）：TR-7 在 `007ffa2f` 上判 `#52273` 未落地（`leader`/`tui_keymap` 0 命中、`keymap.go:37` 仍绑 `ctrl-x`）——证据全真、**结论全错**，因为落地物在下一笔 `f6bf18a2`。
   ⇒ 跑 `git grep` 前先 `git log --oneline <base>..<真 tip>`；base 陈旧就按 tip 重跑，档案里的 0 命中必须同时写**时点 sha**。

4. **资产类改动按路径判，不要按符号 grep**（TR-6 → TR-7 教训，已修）：`#53745`/`#53780` 曾被判 N/A（Rust 新符号 0 命中），实际 Go 仓**镜像了这些资产文件本身**，已实际落地（`007ffa2f`）。⇒ `assets/`、`scripts/`、`*.schema.json`、`*.snap` 一律先 `git ls-files -- '<同路径>'` 查载体。

5. **平台专属 ≠ 可批量 N/A**（TR-6 纠正，leader 终裁）：`#[cfg(target_os)]` 的 MXC / Windows 沙箱系在 Go 侧**有真实后端本体**（`mxc` 413 / `bubblewrap` 55 / `seatbelt` 140 命中）。`#51841`/`#51840`/`#51831`/`#51828` 判 **R2/R3**；`#51835`/`#51812`（flag-only）判 **R2**（仅注册、消费者未接）。

6. **账目时点（`f6bf18a2` 之后）**：账本 `update/r90_commit_ledger_2026_10_16.tsv` = **127 行**（表头 + 126 数据；`landed` 44）；底表 `update/r90_dispatch_queue_2026_10_17.tsv` = **218 行**（表头 + 217 数据）；两表 sha 交集 = **0**。本轮新入账 `#52937`/`#51828`/`#52273`（三条均已从底表删除）；`#53932` 记 `blocked_precondition`（`compaction_output` 在 `compact/**` 0 命中）；`#53723` 标 in-flight。

7. **推送通道：间歇性不可达，已在 turn#27 自行恢复**。turn#26 内 `github.com:443` 连续 5 次失败（`Connection was reset` / `Failed to connect ... port 443`，含 `git ls-remote`），turn#27 第 6 次**成功**：`fe19d8c5..b77357dc  main -> main`，`git rev-list --count origin/main..main` = **0**。
   ⇒ 规则：**推送失败不要改写交付结论**（本地提交即已落地），但也不要假定"永远推不上"；每轮用一次 `git push` 探活即可，不做 sleep 重试；恢复后**必须回头修正**档案里"未推送"的陈述（本条即如此修正）。
### R5-septies · 第 90 轮 turn#27 修订（2026-10-17，leader 实测 + B-b10 / V-b10 回执）

1. **夹带检查的基数必须取「真父」**：`git rev-list --parents -1 <sha>`（不要用更早的祖先）。
   V-b10 实测：`38e21026` 的**唯一父是 `d74ead74`**，`3ad49960` 只是更早祖先；
   用 `3ad49960` 当基数 ⇒ `git diff --stat` = 71 文件，会把其它车道已入库的提交**误报成夹带**。
   同上：`f6bf18a2` 的父是 `007ffa2f`。

2. **派工单里的 base sha 必须是「派工时刻的 tip」**。本轮我犯过一次：给 B-b10 写「`007ffa2f` 已含 B-b9 的 `sandbox/integrity/**`」，
   但 B-b9 的入库点是**之后**才产生的 `f6bf18a2`（`git ls-tree 007ffa2f -- sandbox/integrity` = 0 文件，`f6bf18a2` = 5 文件）。
   车道 B **没有擅自 rebase**、而是在回包里指出 ⇒ 正确行为，保留。
   规则：能先 commit 就**先 commit 再派工**；不得不先派工时，必须写明「你的写面与即将落地的文件集不相交」，并在 base 一行注明"以我落地后的 tip 为准"。

3. **设计类 N/A 要单列，不要混进"未落地缺口"**。B-b10 证明 Go 的 deny-glob 快照展开是**进程内走查、从不启 `rg`**
   （`sandbox/linuxsandbox/linux.go:591/596`、`sandbox/windowssandbox/deny_read_resolver.go:136`，由
   `sandbox/linuxsandbox/linux_test.go:142-148` 的注释冻结）⇒ 上游那族"显式环境扫描器入口"**永远不会有 Go 对应物**。
   引入外部扫描器属**设计变更**，不是同步。登记见下表。

4. **新建登记表：`update/r90_redundant_guards_2026_10_17.md`**（冗余实现 / 无效守卫 / 设计类 N/A）。
   当前 4 条：RG-1 plugin merge 层策略空转 + 守合成输入的用例（只登记）；
   RG-2 appserver 别名门控守卫有洞（已派修 P-b11）；RG-3 设计类扫描器系列；RG-4 方法类。
   **新增规则的用途**：防止后续车道把空转代码当缺口去"补"，或把无效守卫当护栏去信任。

5. **同一前置的 blocked 项应合并成一条 backlog**，不要按 PR 拆成 3 条永远读不完的"未决"。
   B-b10 判定 `#51831`/`#51840`/`#51841`（+`#51842`）是**一整块**：必须先建 exec-server integrity 的
   backend 接口 + 三适配器 + runner + telemetry 骨架；这与「不落骨架」裁决直接冲突 ⇒ 维持 blocked，
   但按**一条**记账。附：整个 `sandbox_integrity` 家族在 pin `4eb4a65407` 就已存在 ⇒ 这是
   「pin 内生产可达子系统 Go 侧无对应物」，**不是**上游新增漂移。


## R5-nonies · 账本行尾事故的三个订正 + 一条回收纪律（V-b14 独立复核）

1. **实测与我先前的表述不一致**：`a54cb9fb` 版 `update/r90_commit_ledger_2026_10_16.tsv` 是 **127 行双 CR + 12 行单 CR**（139 = 表头 + 138 数据行），不是“138 行双 CR”。字节账自洽：53236 − 52970 = **266 = 127 + 139**。说一句“138 行错位”是对的结果、错的原因描述。
2. **判行尾要看 blob，不要看工作区**：该仓 `.tsv` 的约定是 **blob 内 LF-only**（另 3 个 `update/*.tsv` 的 CR 全为 0）；我的 “crlf=139” 只在工作区检出（`core.autocrlf=true`）成立。结论：同一份文件 “工作区 CRLF / blob LF” 是本仓常态，两种读法的 **lone-CR 都是 0** 才是可靠判据。
3. **修复无数据丢失**（V-b14 独立复核）：两版各自 `Replace(“\r”,“”)` 后**逐行 0 mismatch、整串相等** ⇒ 字段文本零改动。
4. **回收纪律（新）：不要让车道在验证者可能还需要“独立副本”时回收自己的台面。**我批准 T 车道删 `r90tui10`，恰好落在 V-b14 复查 T9 字节一致性的窗口内** ⇒ V 只能把“T9 5 文件字节一致性”记为**未覆盖**（他的快照里该台上还在，再查已消失）。以后：并入某批后，**先确认没有在跑的验证车依赖该台，再删台**。

### V-b14 结论（对象 `05fdcd50` + `7786dc34`）

- 夹带检查 24 项**零夹带**；字节一致性 A-b11/C-b10/AC-1/P-b11 **12/12 same=True**（T9 5 文件未覆盖，原因见上）；门禁 A/B 失败集合**逐名相同、新增失败 0**；appserver 整包 A 低负载复跑 **2437/2430/0/7 = ok**（高负载下那 1 条 `TestStdioServerGoalSetResponsePrecedesGoalNotification` 定位为环境型 flake：隔离 5× 及并发 150 次全绿）；6 组反例全部 “撚⇒FAIL⇒复原⇒PASS”。
- **新开待办备选（RC-4c）**：`model/responses_stream.go` 的 `jsonNumberLiteral` 收尾守卫**承重但零 shipped 覆盖** —— 把它改成 `return true` 会让 V 自建探针红（带引号数字 / `null` 成员）而**同树 3 个 shipped 新用例仍绿**。建议补 2 个子用例：`quoted_budget_units`、`null_token_member`。


## R5-decies · 推送纪律（用户指示，2026-10-17）

1. **不要频繁 push**。攒批推送：**一次“并入选批”完成后再 push**，不要每个小改动/每个 commit 都推。
2. 单次 push 失败（`Failed to connect` / `Connection was reset`）时：**记录现状就此打住**（本地 sha、`origin/main` sha、领先几个提交），**不要在同一 turn 内连续重试**。实测教训：上一轮我连试 5–6 次，第三次起就变成 `Connection was reset` —— 频繁重试会加剧故障。下一个并入选批后自然重试即可（当时一次成功）。
3. 失败时**不改写交付结论**：如实写“本地已提交、远端落后 N 个提交、等通道恢复”，不能写成“已交付”也不能写成“交付失败”。
4. 车道侧不变：R9 仍禁止车道 `commit/push/tag/stash`，所以推送节流只作用于队长。

## R5-undecies · 节点事实纪律（用户纠正后，2026-10-17）

1. **换节点唯一入口 = 派生创建**（`agent_admin_create_derived` + `target_node_id`），只有队长能用；`target_node_id` 必须是**真实 node_id**，**禁止把显示名当 id**（实测：传 `"linux_node"` 被拒，错误文案是 `ResourceExhausted: node resources are unavailable`，我误判成"节点资源不足"，连续多轮写进 blocked，实际是我的 id 写错）。
2. **node_id 查询路径**（无专门工具，实测有效）：Master 侧 `C:\Users\huoga\.syntropy\master\.syntropy-run-master\master.out.log` —— deploy 行 `deploy node <id> to <ip>:22 using artifact <artifact>` 与每 60s 的心跳行 `receive a heartbeat request from node:<id>,name:<name>`；本机自身画像在同名 run 日志的 `Ping master node ok` 行（含 name/os/arch/privIP/user）与 `conf\nbot.json` 的 `nodeId`。
3. 拿到 `ResourceExhausted` 先做**两个**判断再下结论：① id 是否正确（未知节点与资源不足共用同一句话）；② 该节点最近心跳是否新鲜（master.out.log 尾部）。
4. **已知节点（2026-10-17）**：Windows worker `19ff0e1d456d25a010ee9b62127663ae`（local_windows_amd64 / 192.168.1.37 / 16 vCPU·15.7GB / 全部 Windows 车道）；Linux worker `06aac1d3185c1999dee1e86482026deb`（linux_node / 192.168.1.99 / Kali Rolling 2026.1 / 16 核·31GiB·603GB / go1.26.3 / bwrap 0.11.0 + landlock）。

## R5-duodecies · 跨节点车道纪律（Linux 节点，2026-10-17 实测）

1. 跨节点**不共享本地文件系统**：唯一同源手段是 `git push/fetch`（队长 push 后车道 `fetch` 即可对齐）或经 Master 传 patch。派工单必须写清**目标节点上的仓库绝对路径**（Linux: `/home/jacks/jacks_dev/codex_go`；Rust 上游: `/home/jacks/jacks_dev/codex`）。
2. 车道台落后队长 tip 时，它的"门禁通过"只对**那个旧时点**成立（L-0 的结论只对 r88 成立）。
3. **Linux 节点已证能力**：`./sandbox/linuxsandbox/` 20/20 PASS（Windows 上该包编译失败）、bwrap 真沙箱 EXIT=0、windows-skip 的 symlink 用例真跑真过、`go build ./...` 43s（Windows 60–140s）、`./appserver/` 整包可在 Linux 跑（Windows 车道因 CPU 争抢不敢跑）。
4. **Linux 节点缺口**：无 powershell/jq；裸 curl 到 github/proxy.golang.org 超时（goproxy.cn 通、git-over-https 通）；`appserver` 整包需 5 个 `CODEX_RUST_*` env 指向 Linux 侧路径，否则成片 SKIP。
5. 放置规则：Linux-only 门禁与实现（bubblewrap/landlock/symlink/NFS/loopback e2e/zsh-fork）放 Linux 节点；改同一批文件的车道仍留 Windows 节点。同一文件同一时刻只能一个写入者。
6. **陈旧重放**：同一回包可能在多轮后重复投递（实测收到 7 小时前的 T2 回包，其 §8 警告的内容早已入库 `8e5a51f0`）。并入前先查当前 tip 是否已包含（`git log --oneline -1 -- <path>` / `git cat-file`），不要按陈旧回包重复派工。

## R5-terdecies · push 后的跨节点同步广播（队长硬纪律，2026-10-17 立）

1. 任何一次 `git push origin main` **成功后，同一 turn 内**必须向**每个其它节点**的在跑车道广播 sync 通知（`send_a2a_message`，`type=request`）。通知必须给：新 `origin/main` 的 **40 位 sha**、该节点**仓库绝对路径**、要求 `git -C <repo> fetch origin main`、以及"用新 sha **新建** worktree"；**禁止** `checkout`/`reset` 已在跑的台面。
2. 车道回包必须含三项，缺一项即视为**未同步**：`git rev-parse origin/main` 实值、`git cat-file -t <新sha>`、`git rev-parse main`（是否与队长一致）。
3. **直连不通时的替代通道（已实测可用）**：`git bundle create <p> <旧sha>..main`（**只打包增量**；实测 `3f3c44f7..89a19fb7` = 145,388 B）→ `git bundle verify` → 经 Master `project_file_sync` 落到 `update/r90_sync_<sha>.bundle` → 车道 `project_fetch` 后**先核 bundle 的 SHA-256**，再 `git fetch <bundle> <sha>:refs/heads/bundle-<sha>`，用该 sha 建台面。bundle 是**临时传输物**，远端 push 成功后即删。
4. **push 的单一权威仍在队长**：车道**不得**自行 push，除非派工单显式授权且三条前置全部满足——`git cat-file -t <sha>` = `commit`；`git ls-remote origin main` 现值 == **预期旧 sha**；只允许 `git push origin <sha>:refs/heads/main`（快进，**禁 `--force`/`--mirror`**）。推完必须回报 `ls-remote` 现值。
5. 通道可用性的**最小探针**（先跑再下结论，别猜）：Windows `Test-NetConnection github.com -Port 443`；Linux `timeout 10 bash -c "</dev/tcp/github.com/443"`；两平台都补 `git ls-remote origin main`。**DNS 能解析 != 通道可用**：本机实测 DNS 正常解析到 `20.205.243.166`，而 443 连续三次 False、`git ls-remote` 21s 超时（2026-10-17 06:5x）。
6. 队长侧 push 失败时的纪律（沿用 R5-decies）：**失败即止、不在同 turn 重试**；如实记"本地已提交、远端落后 N 笔"，**不改写结论**；下一轮可再试一次，并同时启用 §3 的 bundle 通道保证其它节点不缺代码。
7. **已完成同步的判定要看两侧**：远端 `origin/main` 与新 tip 一致 **且** 每个其它节点的车道回包三项齐全。只有前者不算同步完成（车道台面可能仍停在旧 sha）。

8. **本机（Windows）通道根因与正确做法（2026-10-17 实测）**：`github.com` 直连 443 被阻断（DNS 解析到 `20.205.243.166` 正常、`Test-NetConnection` 三次 False、`git ls-remote` 21s 超时），但本机有**本地代理在监听 `127.0.0.1:7897`**（注册表 `HKCU:\...\Internet Settings` 的 `ProxyEnable=1` / `ProxyServer=127.0.0.1:7897`，`netsh winhttp` 却是 Direct），而 **git 默认不读 WinINET 代理** ⇒ 直连必失败。正确做法：给 github 作用域配代理，例如 `git config http.https://github.com/.proxy http://127.0.0.1:7897`（或单次 `git -c http.proxy=... push`）。配后 `ls-remote` **1.2s** 返回。**教训**：报"网络不通"前先查本地代理端口（`7890/7897/1080/10809/8889`）与 `HKCU` 代理设置，别把"缺代理配置"误报成"节点没网"。

## R5-quaterdecies · flake 名单的 Linux 侧更正（2026-10-17，由 L-1/L-3 两批独立复跑给出）

1. `appserver TestOtelProviderReloadsAfterAccountChange` **在 Linux 上不是 flake，是稳定红**：L-3 在自己的台面隔离跑 3/3 FAIL，又在**pristine 未改动台面**（`/tmp/l90/r90l1`，`git status` 为空）隔离跑 3/3 同一断言 FAIL（`otel_reloader_test.go:103: the telemetry exporters were not rebuilt after the account change`）。它在**整包**里时红时绿（受整包次序/负载影响）⇒ 归类应为"Linux 稳定红 + 整包次序敏感"，不要再当负载 flake 折扣掉。
2. `appserver TestExistingThreadLoadsAPICuratedMCPAfterAuthSwitchSync` 在 Linux 隔离 `ok 0.025s` ⇒ **是真 flake**，两边都按 flake 处理。
3. 判红时的正确做法：**先隔离复跑**（同一台面 ×3 + pristine 台面 ×3）；隔离稳定红 = 稳定红，整包时红时绿 ≠ flake。**同一台机器的整包计数不能跨时点直接比**（第 5 槽会换人），必须**逐名比失败集合**。
4. Linux 侧 5-env 整包的稳定红基线（截至 `e786ba0c`）：`TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust`、`TestPluginContentHashMatchesRustBundleFingerprintLikeRust`、`TestPluginListHonorsPerRepositoryConfigLikeRust`、`TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust`、`TestOtelProviderReloadsAfterAccountChange`（后一条按 §1 口径）；唯一 SKIP = `TestServerRequestBrokerIntersectsPermissionsApprovalLikeRust`（Windows 专属 slash_tmp）。
5. **Win 侧 appserver 基线（截至 `e786ba0c`）**：`RUN=2475 PASS_ALL=1883 FAIL=0 SKIP=7`（对象树，5 env 全设、清 `TERM`、CWD=包内）；`e786ba0c` 之前一笔 `89a19fb7` 是 `RUN=2471`，差值 = L-3 新增 4 条用例。
