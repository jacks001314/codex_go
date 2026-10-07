# integ86 独立门禁核验报告（round 86 · syncl4 · Linux 节点）

- 核验对象：`origin/integ86` tip `ed6f52b7406055444a6ac3023788b8a0f588e6b2`
- 基线：`origin/main` `39dd8e94b6569d4a32562ed14c4240b67441eb08`
- 核验环境：Linux / LF，Go `go1.26.3 linux/amd64`，Rust 工作树 `/home/jacks/jacks_dev/codex` @ `5b0b2530354052b9194156d70d4c94a439368342`（clean）
- 纪律：**未 commit / 未 push / 未建远端分支 / 未 stash**；核验结束后临时 worktree 已 `git worktree remove`；探针测试文件只落在 `/tmp`。

## 0) 结论

**Green —— integ86 可合入 main**（新增失败 0；唯一需留意项是环境和已知 flaky，见 §5/§7）。

| 判据 | 判定 | 依据 |
|---|---|---|
| 固定点一致 | PASS | §1（tip / base SHA 与派单逐字一致） |
| ① gofmt（17 个改动文件） | PASS（空） | §3.1 |
| ② `go build ./...` | PASS（rc=0） | §3.2 |
| ③ `go vet`（7 个受影响包） | PASS（rc=0） | §3.3 |
| ④ 受影响包整包测试 vs 基线 | PASS（新增失败 **0**） | §3.4（失败集合 diff 为空；两侧各 10 条同集合） |
| ⑤ parity（含 Rust 工作树三项） | PASS（全绿） | §3.5 |
| ⑥ 逐笔 RC（5/5 有原文 FAIL） | PASS | §5 |
| 交叉影响（appserver 双提交共存） | PASS | §4 |
| 唯一异常：`TestOtelProviderReloadsAfterAccountChange` | 已知 flaky，**非新增**，两分支均复现 | §5.6 |

## 1) 固定点自检（逐条比对）

```
$ cd /home/jacks/jacks_dev/codex_go && git fetch origin
From https://github.com/jacks001314/codex_go
   a32e1c35..39dd8e94  main       -> origin/main
 * [new branch]        integ86    -> origin/integ86
 * [new branch]        syncl1     -> origin/syncl1
 * [new branch]        syncl3     -> origin/syncl3

$ git rev-parse origin/integ86
ed6f52b7406055444a6ac3023788b8a0f588e6b2      # 派单要求 ed6f52b7406055444a6ac3023788b8a0f588e6b2 ✅
$ git rev-parse origin/main
39dd8e94b6569d4a32562ed14c4240b67441eb08      # 派单要求 39dd8e94b6569d4a32562ed14c4240b67441eb08 ✅
$ git merge-base origin/main origin/integ86
39dd8e94b6569d4a32562ed14c4240b67441eb08      # 线性 cherry-pick，无 merge 基分歧

$ git log --oneline -6 origin/integ86
ed6f52b7 syncl5: align file-handle naming and limits with upstream (#49702)
2e898a12 syncl3: centralize persistent mode enablement checks (#48611)
e176a441 syncl3: separate retained assistant context from the instruction prefix (#51627)
67f1df26 syncl1: round the logarithmic context bucket boundaries to Rust's libm values (#48819)
05859a28 syncl1: resolve long symlink control-socket paths in the daemon client (#48772)
39dd8e94 sync595: re-anchor critical-file parity pins to upstream head 5b0b253035 (#51595 window)
```

逐笔文件清单与派单表比对（`git show --stat`）：5/5 与派单描述**完全一致**。
```
05859a28  appserverdaemon/client.go | appserverdaemon/client_long_socket_path_test.go | appserverdaemon/manual_update.go
67f1df26  telemetry/metric_buckets.go | telemetry/metric_buckets_test.go
e176a441  appserver/retained_context_test.go | state/guardian.go | state/guardian_retained_context.go | state/guardian_retained_context_test.go
2e898a12  appserver/turn_runtime.go | context/fragments_test.go | context/persistent_mode.go | features/persistent_mode.go | features/persistent_mode_test.go
ed6f52b7  execserver/client_test.go | execserver/server.go | execserver/server_test.go
```
`git diff --stat origin/main origin/integ86` = 17 files changed, 810 insertions(+), 103 deletions(-)（与上表并集一致）。

补充：`origin/main` 的 `parity/rust_snapshot_test.go` 与上一轮本车道交付的重锚定文件**逐字节相同**（`diff` 空），即 sync595 `39dd8e94` 就是本车道的 9 条新 pin。

rust 工作树：`git -C /home/jacks/jacks_dev/codex rev-parse HEAD` = `5b0b2530354052b9194156d70d4c94a439368342`，`git status --porcelain` 空（LF checkout，syncl3 已快进）→ 满足"期望全绿"的 parity 前置条件。

## 2) 隔离工作树

```
$ cd /home/jacks/jacks_dev/codex_go
$ git worktree add ../codex_go_wt/integ86-verify -b integ86-verify ed6f52b7
Preparing worktree (new branch 'integ86-verify')
HEAD is now at ed6f52b7 syncl5: align file-handle naming and limits with upstream (#49702)
$ git worktree add --detach ../codex_go_wt/integ86-base 39dd8e94
Preparing worktree (detached HEAD 39dd8e94)
HEAD is now at 39dd8e94 sync595: re-anchor critical-file parity pins to upstream head 5b0b253035 (#51595 window)
```
主仓工作目录未被触碰（`/home/jacks/jacks_dev/codex_go` 仍 main a32e1c35，未做任何 checkout/diff 操作，除 `worktree add/remove` 与只读 `git grep/show/log`）。

## 3) 六步门禁（原文）

### 3.1 ① gofmt
```
$ cd /home/jacks/jacks_dev/codex_go_wt/integ86-verify
$ git diff --name-only origin/main..HEAD        # 17 个文件（见 §1）
$ gofmt -l $(cat /tmp/integ86_files.txt)
                                                # ← 空输出，rc=0
```
控制组（证明 gofmt 确实会报）：`gofmt -l /tmp/integ86_probe.go` → `/tmp/integ86_probe.go`。

### 3.2 ② go build
```
$ go build ./...
$ echo $?
0
```

### 3.3 ③ go vet（受影响包）
```
$ go vet ./appserverdaemon/ ./telemetry/ ./state/ ./appserver/ ./context/ ./features/ ./execserver/
$ echo $?
0
```

### 3.4 ④ 受影响包整包测试 vs 基线（同 worktree 内对比）

两侧命令相同：
```
go test ./appserverdaemon/ ./telemetry/ ./state/ ./appserver/ ./context/ ./features/ ./execserver/ \
  -count=1 -timeout 20m -skip 'TestOtelProviderReloadsAfterAccountChange'
```
（`-skip` 的动机见 §3.4 末尾"已知 flaky"小节：该测试两分支均 flaky，属环境噪声，不是新增失败。原始未 skip 的两侧输出同样保留在 §3.4 末。）

基线 `39dd8e94`（integ86-base）原文：
```
HEAD=39dd8e94b6569d4a32562ed14c4240b67441eb08
--- FAIL: TestPrepareDaemonInstallSeedsTheCLIPackage (0.42s)
--- FAIL: TestPrepareDaemonInstallIsIdempotent (0.42s)
--- FAIL: TestUpdateFromCLIPinsTheCLIPackage (0.22s)
--- FAIL: TestUpdateFromCLICancellationChangesNothing (0.22s)
FAIL
FAIL	codex_go/appserverdaemon	8.125s
ok  	codex_go/telemetry	0.272s
ok  	codex_go/state	6.254s
--- FAIL: TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust (0.02s)
--- FAIL: TestPluginListHonorsPerRepositoryConfigLikeRust (0.00s)
--- FAIL: TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust (2.02s)
FAIL
FAIL	codex_go/appserver	30.438s
ok  	codex_go/context	0.004s
ok  	codex_go/features	0.003s
--- FAIL: TestFileReadsRejectSymlinkLikeRust (0.00s)
--- FAIL: TestLocalEnvironmentInfoReportsTemporaryDirectoriesAndCapability (0.00s)
--- FAIL: TestFilesystemSandboxContextRunsThroughHelperLikeRust (0.00s)
FAIL
FAIL	codex_go/execserver	6.763s
FAIL
base2_rc=1
```

候选 `ed6f52b7`（integ86-verify）原文：
```
HEAD=ed6f52b7406055444a6ac3023788b8a0f588e6b2
--- FAIL: TestPrepareDaemonInstallSeedsTheCLIPackage (0.42s)
--- FAIL: TestPrepareDaemonInstallIsIdempotent (0.42s)
--- FAIL: TestUpdateFromCLIPinsTheCLIPackage (0.22s)
--- FAIL: TestUpdateFromCLICancellationChangesNothing (0.22s)
FAIL
FAIL	codex_go/appserverdaemon	8.120s
ok  	codex_go/telemetry	0.294s
ok  	codex_go/state	5.822s
--- FAIL: TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust (0.02s)
--- FAIL: TestPluginListHonorsPerRepositoryConfigLikeRust (0.00s)
--- FAIL: TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust (2.02s)
FAIL
FAIL	codex_go/appserver	31.635s
ok  	codex_go/context	0.003s
ok  	codex_go/features	0.003s
--- FAIL: TestFileReadsRejectSymlinkLikeRust (0.00s)
--- FAIL: TestLocalEnvironmentInfoReportsTemporaryDirectoriesAndCapability (0.00s)
--- FAIL: TestFilesystemSandboxContextRunsThroughHelperLikeRust (0.00s)
FAIL
FAIL	codex_go/execserver	7.550s
FAIL
ver2_rc=1
```

失败集合 diff（判据）：
```
$ diff /tmp/fails_base.txt /tmp/fails_ver.txt && echo "NO NEW FAILURES (diff empty)"
NO NEW FAILURES (diff empty)
counts: base=10 verify=10
```
10 条集合（两侧完全相同）：
```
TestFileReadsRejectSymlinkLikeRust
TestFilesystemSandboxContextRunsThroughHelperLikeRust
TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust
TestLocalEnvironmentInfoReportsTemporaryDirectoriesAndCapability
TestPluginListHonorsPerRepositoryConfigLikeRust
TestPrepareDaemonInstallIsIdempotent
TestPrepareDaemonInstallSeedsTheCLIPackage
TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust
TestUpdateFromCLICancellationChangesNothing
TestUpdateFromCLIPinsTheCLIPackage
```
分类（全部为环境性/预存在，与本分支 5 笔无关）：
- `appserverdaemon` 4 条 → `prepare_install_test.go:100/137`、`updater_install_test.go:88/139`：`local Codex package file bin/codex-code-mode-host is not executable`（缺可执行件）。
- `appserver` 3 条 → `gateway_oauth_test.go:152`（failed to save provider OAuth credentials）、`plugin_list_repo_test.go:63`（per-repository marketplace not surfaced）、`runtime_router_test.go:11608`（timed out waiting for completed item patch-failed）。
- `execserver` 3 条 → `regular_file_unix_test.go:63`（symlink read semantics）、`server_test.go:189`（TemporaryDirectories empty）、`server_test.go:1416`（缺 `codex-linux-sandbox` 可执行）。
断言首行两侧逐字相同（grep 结果一致），故不是"同名字不同错因"。

已知 flaky（额外调查，全部原文）——**未 skip** 的第一次两侧运行中，verify 侧多出 1 条：
```
--- FAIL: TestOtelProviderReloadsAfterAccountChange (5.00s)
    otel_reloader_test.go:103: the telemetry exporters were not rebuilt after the account change
```
甄别（单测隔离运行，与其它测试无关）：
```
# integ86-base (39dd8e94) 单测 -count=10
      2 --- FAIL: TestOtelProviderReloadsAfterAccountChange (5.00s)
      5 --- FAIL: TestOtelProviderReloadsAfterAccountChange (5.01s)
      2 --- PASS: TestOtelProviderReloadsAfterAccountChange (0.05s)
      1 --- PASS: TestOtelProviderReloadsAfterAccountChange (0.06s)
# integ86-verify (ed6f52b7) 单测 -count=5
--- FAIL: TestOtelProviderReloadsAfterAccountChange (5.00s)
--- FAIL: TestOtelProviderReloadsAfterAccountChange (5.00s)
--- PASS: TestOtelProviderReloadsAfterAccountChange (0.06s)
--- PASS: TestOtelProviderReloadsAfterAccountChange (0.05s)
--- PASS: TestOtelProviderReloadsAfterAccountChange (0.05s)
# 另一次 verify -count=10：10 条全 FAIL；base -count=5 早先一轮：5 条全 FAIL
```
两侧都 flaky、通过率随负载浮动；该文件（`appserver/otel_reloader_test.go`）**不在本分支改动集**内，`origin/main:update/handoff_2026_10_07.md:112` 已把它列为"已知 flaky（不计新增失败、勿修）"。故判定为**非新增失败**。未自行修改。

### 3.5 ⑤ parity
```
$ CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1
ok  	codex_go/parity	0.698s
parity_rc=0

$ CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1 -v \
    -run 'TestRustCriticalFileHashesSnapshot|TestPrecomputedAppServerExportsMatchRustTarget|TestRustWorkspaceMembersSnapshot'
=== RUN   TestRustWorkspaceMembersSnapshot
--- PASS: TestRustWorkspaceMembersSnapshot (0.00s)
=== RUN   TestRustCriticalFileHashesSnapshot
--- PASS: TestRustCriticalFileHashesSnapshot (0.00s)
=== RUN   TestPrecomputedAppServerExportsMatchRustTarget
--- PASS: TestPrecomputedAppServerExportsMatchRustTarget (0.00s)
PASS
ok  	codex_go/parity	0.022s
```
**未做任何 re-vendor**；`.zst` 两侧逐字节一致（上一轮已确认，本轮 parity 绿即复核）。

## 4) 交叉影响核查（appserver 双提交共存）

改动落在 `appserver/`（族）的两笔是 `e176a441`（#51627，`appserver/retained_context_test.go`）与 `2e898a12`（#48611，`appserver/turn_runtime.go`）；另两笔落在 `appserverdaemon/`（#48772）。单次调用、同包二进制内同时跑两笔的全部相关测试：
```
$ go test ./appserver/ -run 'TestRuntimeRouterRetainedContextRecordsThreadInstructionsLikeRust|TestRuntimeRouterRetainedContextSurvivesCompactionLikeRust|TestModelGuardianReviewerIncludesRetainedInstructionsLikeRust|TestRuntimeRouterRetainsAssistantContextLikeRust|TestApplyPersistentClockDefaults|TestPersistentModeInstructionsFragmentLikeRust' -count=1 -v
=== RUN   TestRuntimeRouterRetainedContextRecordsThreadInstructionsLikeRust
--- PASS: TestRuntimeRouterRetainedContextRecordsThreadInstructionsLikeRust (0.00s)
=== RUN   TestRuntimeRouterRetainedContextSurvivesCompactionLikeRust
--- PASS: TestRuntimeRouterRetainedContextSurvivesCompactionLikeRust (0.01s)
=== RUN   TestModelGuardianReviewerIncludesRetainedInstructionsLikeRust
--- PASS: TestModelGuardianReviewerIncludesRetainedInstructionsLikeRust (0.00s)
=== RUN   TestRuntimeRouterRetainsAssistantContextLikeRust
--- PASS: TestRuntimeRouterRetainsAssistantContextLikeRust (0.00s)
=== RUN   TestApplyPersistentClockDefaultsEnablesOnPersistentEffortLikeRust
--- PASS: TestApplyPersistentClockDefaultsEnablesOnPersistentEffortLikeRust (0.00s)
=== RUN   TestApplyPersistentClockDefaultsSkipsOrdinaryEffortLikeRust
--- PASS: TestApplyPersistentClockDefaultsSkipsOrdinaryEffortLikeRust (0.00s)
=== RUN   TestApplyPersistentClockDefaultsRespectsExplicitConfigLikeRust
--- PASS: TestApplyPersistentClockDefaultsRespectsExplicitConfigLikeRust (0.00s)
=== RUN   TestApplyPersistentClockDefaultsSkipsReviewTurnsLikeRust
--- PASS: TestApplyPersistentClockDefaultsSkipsReviewTurnsLikeRust (0.00s)
=== RUN   TestPersistentModeInstructionsFragmentLikeRust
--- PASS: TestPersistentModeInstructionsFragmentLikeRust (0.00s)
PASS
ok  	codex_go/appserver	0.097s
```
9/9 PASS，且整包 appserver 在 §3.4 与基线同集合（3 条环境性失败）→ 两笔互不干扰，也没有被彼此"遮蔽成真空通过"（RC-3/RC-4 已分别证明这两组测试对各自的生产接线敏感，见 §5.3/§5.4）。

## 5) 逐笔 RC（值/行为级，打在真实生产接线）

通用手法：改动**只发生在本地核验 worktree 的生产文件**上，先备份到 `/tmp/rc_backup`，跑测试取证，再按字节还原并复跑确认绿；每轮结束 `git status --porcelain` 均为空（工作树 pristine）。

### 5.1 `05859a28` #48772（长符号链接控制 socket 路径）

生产接线：`appserverdaemon/client.go` 的 `dialUnixSocketWebSocketURL` DialContext 与 `appserverdaemon/manual_update.go` 的 `connectManualUpdaterSocket`。

RC：把两处改回直接 `net.Dialer.DialContext(ctx, "unix", …)` / `net.DialTimeout("unix", …)` ⇒
```
=== RUN   TestControlSocketClientConnectsThroughLongAdvertisedSymlinkPath
    client_long_socket_path_test.go:56: raw dial error (expected): dial unix /tmp/TestControlSocketClientConnectsThroughLongAdvertisedSymlinkPath3834554209/001/xxxxxxxx…/codex-home/app-server-control.sock: connect: invalid argument
    client_long_socket_path_test.go:59: EnableRemoteControlOnSocket("…/app-server-control.sock") error = app server did not become ready on …/app-server-control.sock: failed to WebSocket dial: failed to send handshake request: Get "http://localhost/rpc": dial unix …/app-server-control.sock: connect: invalid argument
--- FAIL: TestControlSocketClientConnectsThroughLongAdvertisedSymlinkPath (5.00s)
=== RUN   TestManualUpdaterConnectsThroughLongAdvertisedSymlinkPath
    client_long_socket_path_test.go:85: connectManualUpdaterSocket("…/app-server-control.sock") error = dial unix …/app-server-control.sock: connect: invalid argument
--- FAIL: TestManualUpdaterConnectsThroughLongAdvertisedSymlinkPath (0.00s)
FAIL
FAIL	codex_go/appserverdaemon	5.008s
```
恢复后：
```
--- PASS: TestControlSocketClientConnectsThroughLongAdvertisedSymlinkPath (0.00s)
--- PASS: TestManualUpdaterConnectsThroughLongAdvertisedSymlinkPath (0.00s)
ok  	codex_go/appserverdaemon	0.015s
```
（`invalid argument` = EINVAL，正是 Rust #48772 的原始症状；测试自带 oracle 行 `raw dial error (expected)` 证明裸 dial 必失败、只有走 `codexuds.ConnectUnixSocket` 才通。）

### 5.2 `67f1df26` #48819（对数桶边界按 Rust libm 取值）

生产接线：`telemetry/metric_buckets.go` 的 `contextLogBuckets` → `roundedPowerOfTwo`。

RC：把 `roundedPowerOfTwo` 换回 `math.Pow(2, exponent)` ⇒
```
=== RUN   TestContextMetricBoundariesPreserveZeroAndFamilyRanges
--- PASS: TestContextMetricBoundariesPreserveZeroAndFamilyRanges (0.00s)
=== RUN   TestContextLogBoundariesMatchRust
    metric_buckets_test.go:137: context log boundaries drifted from Rust: sha256 = 15e2459663966ca5e4a59153600bbdd807774815ad6ae65d95b6693ca36907cd, want 8cacf78cbe23be0158fb5fa8f4be5d8768684969b45c3f48b8841d8e42c80fea
--- FAIL: TestContextLogBoundariesMatchRust (0.00s)
FAIL
FAIL	codex_go/telemetry	0.009s
```
恢复后整包：
```
ok  	codex_go/telemetry	0.260s
```
（注意弱判据 `TestContextMetricBoundariesPreserveZeroAndFamilyRanges` 在破坏下仍 PASS，说明 **digest pin 才是真正的判别门**，该 RC 打的是值级差异。）

### 5.3 `e176a441` #51627（retained assistant context 与指令前缀分离）

生产接线：`state/guardian.go` 的 `BuildPromptWithOptions` 中"将 AssistantContext 段写入 prompt"这一段。

RC：删掉该段写入 ⇒ 同包与**跨包**都红：
```
=== RUN   TestGuardianPromptIncludesRetainedInstructionsLikeRust            (state/)
    guardian_retained_context_test.go:393: retained assistant context section missing from prompt:
--- FAIL: TestGuardianPromptIncludesRetainedInstructionsLikeRust (0.00s)
=== RUN   TestRetainedInstructionSectionsSplitLikeRust                      (state/, 纯函数)
--- PASS: TestRetainedInstructionSectionsSplitLikeRust (0.00s)
FAIL
FAIL	codex_go/state	0.043s

=== RUN   TestModelGuardianReviewerIncludesRetainedInstructionsLikeRust     (appserver/)
    retained_context_test.go:209: prompt is missing ">>> RETAINED ASSISTANT CONTEXT START":
--- FAIL: TestModelGuardianReviewerIncludesRetainedInstructionsLikeRust (0.00s)
=== RUN   TestRuntimeRouterRetainsAssistantContextLikeRust
    retained_context_test.go:330: prompt is missing the assistant context:
--- FAIL: TestRuntimeRouterRetainsAssistantContextLikeRust (0.00s)
FAIL
FAIL	codex_go/appserver	0.084s
```
恢复后：
```
--- PASS: TestGuardianPromptIncludesRetainedInstructionsLikeRust (0.00s)
ok  	codex_go/state	0.017s
--- PASS: TestModelGuardianReviewerIncludesRetainedInstructionsLikeRust (0.00s)
--- PASS: TestRuntimeRouterRetainsAssistantContextLikeRust (0.00s)
ok  	codex_go/appserver	0.138s
```

### 5.4 `2e898a12` #48611（集中化 persistent mode enablement）

生产接线：`features.PersistentModeEnabled`（被 `appserver/turn_runtime.go` 的时钟默认与 persistent 指令片段、`context.PersistentModeInstructions` 共同消费）。

RC：令其恒返回 `false` ⇒ 三个消费点全红（含跨包）：
```
=== RUN   TestPersistentModeEnabledMatchesRust                (features/)
    persistent_mode_test.go:10: PersistentModeEnabled("persistent") = false, want true
--- FAIL: TestPersistentModeEnabledMatchesRust (0.00s)
FAIL
FAIL	codex_go/features	0.002s

=== RUN   TestApplyPersistentClockDefaultsEnablesOnPersistentEffortLikeRust   (appserver/)
    runtime_router_test.go:27719: applyPersistentClockDefaults() = false, want true for persistent effort
--- FAIL: TestApplyPersistentClockDefaultsEnablesOnPersistentEffortLikeRust (0.00s)
=== RUN   TestPersistentModeInstructionsFragmentLikeRust
    runtime_router_test.go:28383: persistent fragment = (*context.SimpleFragment)(nil)
--- FAIL: TestPersistentModeInstructionsFragmentLikeRust (0.01s)
FAIL
FAIL	codex_go/appserver	0.118s
```
（同批其余三条 `SkipsOrdinaryEffort/SkipsRespectsExplicitConfig/SkipsReviewTurns` 仍 PASS，属"应当不启用"的负例，符合预期。）
恢复后：
```
ok  	codex_go/features	0.004s
--- PASS: TestApplyPersistentClockDefaultsEnablesOnPersistentEffortLikeRust (0.00s)
--- PASS: TestApplyPersistentClockDefaultsSkipsOrdinaryEffortLikeRust (0.00s)
--- PASS: TestApplyPersistentClockDefaultsRespectsExplicitConfigLikeRust (0.00s)
--- PASS: TestApplyPersistentClockDefaultsSkipsReviewTurnsLikeRust (0.00s)
--- PASS: TestPersistentModeInstructionsFragmentLikeRust (0.00s)
ok  	codex_go/appserver	0.035s
```

### 5.5 `ed6f52b7` #49702（exec-server 文件句柄命名/上限对齐）—— 双证

**证一：符号面（重命名完备、无残留）**
```
$ git grep -n -E 'maxOpenFileReads|maxFileReadHandleIDBytes|validateFileReadHandleID' 39dd8e94 -- 'execserver/*.go'
execserver/client_test.go:386:	if len(stream.handleID) != maxFileReadHandleIDBytes {
execserver/client_test.go:402:	for i := 0; i < maxOpenFileReads+2; i++ {
execserver/server.go:91:	maxOpenFileReads              = 128
execserver/server.go:92:	maxFileReadHandleIDBytes      = 32
execserver/server.go:890:		fileSlots:          make(chan struct{}, maxOpenFileReads),
execserver/server.go:908:		fileSlots:  make(chan struct{}, maxOpenFileReads),
execserver/server.go:2419:	if err := validateFileReadHandleID(params.HandleID); err != nil {
execserver/server.go:2460:		return nil, requestError(-32600, fmt.Sprintf("at most %d file handles may be open per connection", maxOpenFileReads))
execserver/server.go:2505:	if err := validateFileReadHandleID(params.HandleID); err != nil {
execserver/server.go:2553:	if err := validateFileReadHandleID(params.HandleID); err != nil {
execserver/server.go:2594:	if err := validateFileReadHandleID(params.HandleID); err != nil {
execserver/server.go:2624:func validateFileReadHandleID(handleID string) error {
execserver/server.go:2625:	if len(handleID) > maxFileReadHandleIDBytes {
execserver/server.go:2626:		return requestError(-32600, fmt.Sprintf("file handle ID must not exceed %d bytes", maxFileReadHandleIDBytes))
execserver/server_test.go:1306:	if _, err := server.closeFile(&FSCloseParams{HandleID: strings.Repeat("x", maxFileReadHandleIDBytes+1)}); err == nil {
execserver/server_test.go:1341:	for i := 1; i < maxOpenFileReads; i++ {
execserver/server_test.go:1346:	if len(server.fileSlots) != maxOpenFileReads {
execserver/server_test.go:1347:		t.Fatalf("slots in use = %d, want %d", len(server.fileSlots), maxOpenFileReads)
execserver/server_test.go:1357:	if len(server.fileSlots) != maxOpenFileReads-1 {
execserver/server_test.go:1358:			t.Fatalf("slots in use after close = %d, want %d", len(server.fileSlots), maxOpenFileReads-1)

$ git grep -n -E 'maxOpenFileReads|maxFileReadHandleIDBytes|validateFileReadHandleID' ed6f52b7 -- '*.go'
            # ← 空（rc=1），旧名在 tip 全仓 0 处残留

$ git grep -n -E 'maxOpenFiles|maxFileHandleIDBytes|validateFileHandleID' ed6f52b7 -- 'execserver/*.go'
execserver/client_test.go:386:	if len(stream.handleID) != maxFileHandleIDBytes {
execserver/client_test.go:402:	for i := 0; i < maxOpenFiles+2; i++ {
execserver/server.go:94:	maxOpenFiles         = 128
execserver/server.go:95:	maxFileHandleIDBytes = 32
execserver/server.go:893:		fileSlots:          make(chan struct{}, maxOpenFiles),
execserver/server.go:911:		fileSlots:  make(chan struct{}, maxOpenFiles),
execserver/server.go:2422:	if err := validateFileHandleID(params.HandleID); err != nil {
execserver/server.go:2463:		return nil, requestError(-32600, fmt.Sprintf("at most %d file handles may be open per connection", maxOpenFiles))
execserver/server.go:2508:	if err := validateFileHandleID(params.HandleID); err != nil {
execserver/server.go:2556:	if err := validateFileHandleID(params.HandleID); err != nil {
execserver/server.go:2597:	if err := validateFileHandleID(params.HandleID); err != nil {
execserver/server.go:2627:func validateFileHandleID(handleID string) error {
execserver/server.go:2628:	if len(handleID) > maxFileHandleIDBytes {
execserver/server.go:2629:		return requestError(-32600, fmt.Sprintf("file handle ID must not exceed %d bytes", maxFileHandleIDBytes))
execserver/server_test.go:1306:	if _, err := server.closeFile(&FSCloseParams{HandleID: strings.Repeat("x", maxFileHandleIDBytes+1)}); err == nil {
execserver/server_test.go:1341:	for i := 1; i < maxOpenFiles; i++ {
execserver/server_test.go:1346:	if len(server.fileSlots) != maxOpenFiles {
execserver/server_test.go:1347:			t.Fatalf("slots in use = %d, want %d", len(server.fileSlots), maxOpenFiles)
execserver/server_test.go:1357:	if len(server.fileSlots) != maxOpenFiles-1 {
execserver/server_test.go:1358:			t.Fatalf("slots in use after close = %d, want %d", len(server.fileSlots), maxOpenFiles-1)
```
20 个旧名点 → 0；20 个新名点，一一位点对应（行号偏移与 `const` 块重排一致），且测试文件同步改到新名 ⇒ 重命名是**完备且被编译期/case-sensitive 机械验证**的（Go 里旧名残留必然编译失败）。

**证二：容量门行为（撤接线 ⇒ FAIL）**

探针 A：删掉 `openFile` 中的 `if !s.reserveFileSlot() { … }` 容量门 ⇒
```
=== RUN   TestFilesystemOpenCapacityBoundsInFlightOpensLikeRust
    server_test.go:1337: slots in use after duplicate = 0, want 1
--- FAIL: TestFilesystemOpenCapacityBoundsInFlightOpensLikeRust (0.00s)
FAIL
FAIL	codex_go/execserver	0.006s
```
探针 B：令 `validateFileHandleID` 直接 `return nil`（句柄 ID 字节上限失效）⇒
```
=== RUN   TestFilesystemHandleAndReadLimitsMatchRust
    server_test.go:1307: closeFile(long handle) error = nil
--- FAIL: TestFilesystemHandleAndReadLimitsMatchRust (0.00s)
FAIL
FAIL	codex_go/execserver	0.063s
```
恢复后：
```
--- PASS: TestClientFileReadStreamClosesExactBoundaryAndReleasesCapacityLikeRust (0.08s)
--- PASS: TestSessionRegistryIsolatesFileHandlesLikeRust (0.00s)
--- PASS: TestFilesystemHandleAndReadLimitsMatchRust (0.00s)
--- PASS: TestFilesystemOpenCapacityBoundsInFlightOpensLikeRust (0.04s)
--- PASS: TestFilesystemConcurrentDuplicateHandleIDsLikeRust (0.00s)
ok  	codex_go/execserver	0.135s
```

**诚实标注（该笔的核验边界）**：`maxOpenFiles = 128` 与 `maxFileHandleIDBytes = 32` 这两个**字面值**在 Go 侧没有被任何测试独立钉住——`TestFilesystemOpenCapacityBoundsInFlightOpensLikeRust` / `client_test.go` 都是**相对常量**循环（把 128 改成 127 测试仍会通过）。因此该笔目前可证的是：①符号面重命名完备；②容量门/字节上限的**机制**活着且被覆盖。**数值**与 Rust `MAX_OPEN_FILES` / `MAX_FILE_HANDLE_ID_BYTES` 的一致性只能靠人读 diff（本次 diff 显示确为 128/32，与 Rust #49702 的常量名对齐，但无机器判据兜底）。建议队长考虑后续给这两个值加一条 digest/literal pin（同 parity 快照范式），否则将来 Rust 调上限时 Go 侧不会被任何测试报警。**本轮未自行改动**。

### 5.6 核验后状态
```
$ cd /home/jacks/jacks_dev/codex_go_wt/integ86-verify
$ git rev-parse HEAD
ed6f52b7406055444a6ac3023788b8a0f588e6b2
$ git rev-list --count origin/main..HEAD
5
$ git diff --stat HEAD origin/integ86        # 空 = 未产生任何本地提交/改动
$ git status --porcelain                     # 空 = pristine
```

## 6) 合入结论

**integ86 可合入 main（Green）。**

| # | 判据 | 结果 | 依据 |
|---|---|---|---|
| 1 | 固定点（tip `ed6f52b7` / base `39dd8e94`） | ✅ | §1 |
| 2 | ① gofmt 空 | ✅ | §3.1 |
| 3 | ② `go build ./...` | ✅ rc=0 | §3.2 |
| 4 | ③ `go vet`（7 包） | ✅ rc=0 | §3.3 |
| 5 | ④ 受影响包 vs 基线，新增失败 **0** | ✅ | §3.4（diff 空；10 vs 10 同集合） |
| 6 | ⑤ parity（含 3 项 Rust 工作树测试） | ✅ 全绿 | §3.5 |
| 7 | ⑥ 5 笔逐笔 RC | ✅ 5/5 有原文 FAIL→恢复 ok | §5.1–§5.5 |
| 8 | appserver 交叉影响 | ✅ 9/9 PASS + 整包同基线集合 | §4 |
| 9 | 遗留失败归因 | ✅ 10 条全为环境性/预存在（缺二进制、symlink/临时目录语义）；flaky 1 条两侧同现 | §3.4 |

## 7) 未决 / 提示（未自行处理）

1. `TestOtelProviderReloadsAfterAccountChange` 是**跨分支 flaky**（base 7/10 FAIL、verify 5/10~0/10 FAIL 随负载变动），已由 `update/handoff_2026_10_07.md:112` 登记为"勿修"。若希望门禁可判读，建议后续单独处理（把 5s 轮询改为事件驱动或放宽时限），**不在本轮范围**。
2. §5.5 的数值 pin 缺口（`128` / `32` 无机器判据）。
3. 环境性失败的 3 个前置件（`bin/codex-code-mode-host`、`codex-linux-sandbox`、临时目录语义）与本次 5 笔无关；若队长希望把门禁做到"整包全绿"，需要先补这些环境前置（另立任务）。
4. 核验用的两个临时 worktree（`integ86-verify`、`integ86-base`）已 remove；核验分支 `integ86-verify` 未推送、未合并，按纪律停在本地（如需清理分支指针请队长指示）。

---

# 附：缩小范围复核（应答队长 msg-1791365599416080600-3280，含 `codexuds`）

队长把范围缩为 5 项（并新增 `./codexuds/` 包）。本节为**在缩小范围下重跑的新原文**，与 §1–§7 的结论一致；两处 worktree 为重新创建（`integ86-verify` @ `ed6f52b7`、`integ86-base` @ `39dd8e94` 家族），核验后再次 remove。

## A.1 `go build ./...`（ed6f52b7）
```
$ cd /home/jacks/jacks_dev/codex_go_wt/integ86-verify && git status --porcelain
$ go build ./... ; echo $?
0
build_rc=0
```

## A.2 八个包整包测试 vs 基线（含 `codexuds`）
命令（两侧相同，**未 skip**）：
```
go test ./appserver/ ./state/ ./context/ ./features/ ./telemetry/ ./execserver/ ./appserverdaemon/ ./codexuds/ -count=1 -timeout 20m
```
基线 `39dd8e94`：
```
HEAD=39dd8e94b6569d4a32562ed14c4240b67441eb08
--- FAIL: TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust (0.02s)
--- FAIL: TestPluginListHonorsPerRepositoryConfigLikeRust (0.00s)
--- FAIL: TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust (2.02s)
FAIL	codex_go/appserver	30.075s
ok  	codex_go/state	4.876s
ok  	codex_go/context	0.003s
ok  	codex_go/features	0.003s
ok  	codex_go/telemetry	0.271s
--- FAIL: TestFileReadsRejectSymlinkLikeRust (0.00s)
--- FAIL: TestLocalEnvironmentInfoReportsTemporaryDirectoriesAndCapability (0.00s)
--- FAIL: TestFilesystemSandboxContextRunsThroughHelperLikeRust (0.00s)
FAIL	codex_go/execserver	6.560s
--- FAIL: TestPrepareDaemonInstallSeedsTheCLIPackage (0.42s)
--- FAIL: TestPrepareDaemonInstallIsIdempotent (0.42s)
--- FAIL: TestUpdateFromCLIPinsTheCLIPackage (0.22s)
--- FAIL: TestUpdateFromCLICancellationChangesNothing (0.22s)
FAIL	codex_go/appserverdaemon	8.130s
ok  	codex_go/codexuds	0.003s
base_rc=1
```
候选 `ed6f52b7`：
```
HEAD=ed6f52b7406055444a6ac3023788b8a0f588e6b2
--- FAIL: TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust (0.01s)
--- FAIL: TestOtelProviderReloadsAfterAccountChange (5.00s)
--- FAIL: TestPluginListHonorsPerRepositoryConfigLikeRust (0.00s)
--- FAIL: TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust (2.01s)
FAIL	codex_go/appserver	36.428s
ok  	codex_go/state	4.899s
ok  	codex_go/context	0.005s
ok  	codex_go/features	0.004s
ok  	codex_go/telemetry	0.264s
--- FAIL: TestFileReadsRejectSymlinkLikeRust (0.00s)
--- FAIL: TestLocalEnvironmentInfoReportsTemporaryDirectoriesAndCapability (0.00s)
--- FAIL: TestFilesystemSandboxContextRunsThroughHelperLikeRust (0.00s)
FAIL	codex_go/execserver	6.569s
--- FAIL: TestPrepareDaemonInstallSeedsTheCLIPackage (0.42s)
--- FAIL: TestPrepareDaemonInstallIsIdempotent (0.42s)
--- FAIL: TestUpdateFromCLIPinsTheCLIPackage (0.22s)
--- FAIL: TestUpdateFromCLICancellationChangesNothing (0.21s)
FAIL	codex_go/appserverdaemon	8.122s
ok  	codex_go/codexuds	0.004s
ver_rc=1
```
失败集合 diff：
```
=== base set (10) ===        # appserver 3 / execserver 3 / appserverdaemon 4
=== verify set (11) ===      # 同上 + TestOtelProviderReloadsAfterAccountChange
=== diff ===
4a5
> TestOtelProviderReloadsAfterAccountChange
diff_rc=1
```
剔除已知 flaky 后（两侧同命令加 `-skip 'TestOtelProviderReloadsAfterAccountChange'`）：
候选侧输出与基线侧**逐行一致**（appserver 3 条、execserver 3 条、appserverdaemon 4 条，`state`/`context`/`features`/`telemetry`/`codexuds` 全 ok）：
```
--- FAIL: TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust (0.02s)
--- FAIL: TestPluginListHonorsPerRepositoryConfigLikeRust (0.00s)
--- FAIL: TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust (2.02s)
FAIL	codex_go/appserver	30.777s
ok  	codex_go/state	4.784s
ok  	codex_go/context	0.004s
ok  	codex_go/features	0.003s
ok  	codex_go/telemetry	0.268s
--- FAIL: TestFileReadsRejectSymlinkLikeRust (0.00s)
--- FAIL: TestLocalEnvironmentInfoReportsTemporaryDirectoriesAndCapability (0.00s)
--- FAIL: TestFilesystemSandboxContextRunsThroughHelperLikeRust (0.00s)
FAIL	codex_go/execserver	6.629s
--- FAIL: TestPrepareDaemonInstallSeedsTheCLIPackage (0.42s)
--- FAIL: TestPrepareDaemonInstallIsIdempotent (0.42s)
--- FAIL: TestUpdateFromCLIPinsTheCLIPackage (0.22s)
--- FAIL: TestUpdateFromCLICancellationChangesNothing (0.22s)
FAIL	codex_go/appserverdaemon	8.188s
ok  	codex_go/codexuds	0.003s
```
**新增失败 0**（含新加的 `codexuds`：base/verify 均 `ok`）。

## A.3 parity
```
$ CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1
ok  	codex_go/parity	0.808s
rust HEAD=5b0b2530354052b9194156d70d4c94a439368342
```

## A.4 两笔重头 RC（重跑原文）

**A.4.1 `e176a441` #51627** — 删掉 `state/guardian.go` 中把 `AssistantContext` 写入 prompt 的那一段：
```
=== RUN   TestGuardianPromptIncludesRetainedInstructionsLikeRust
    guardian_retained_context_test.go:393: retained assistant context section missing from prompt:
--- FAIL: TestGuardianPromptIncludesRetainedInstructionsLikeRust (0.00s)
FAIL
FAIL	codex_go/state	0.007s
=== RUN   TestModelGuardianReviewerIncludesRetainedInstructionsLikeRust
    retained_context_test.go:209: prompt is missing ">>> RETAINED ASSISTANT CONTEXT START":
--- FAIL: TestModelGuardianReviewerIncludesRetainedInstructionsLikeRust (0.00s)
=== RUN   TestRuntimeRouterRetainsAssistantContextLikeRust
    retained_context_test.go:330: prompt is missing the assistant context:
--- FAIL: TestRuntimeRouterRetainsAssistantContextLikeRust (0.00s)
FAIL
FAIL	codex_go/appserver	0.047s
FAIL
```
恢复（`status=[]` 空）后：
```
--- PASS: TestGuardianPromptIncludesRetainedInstructionsLikeRust (0.00s)
ok  	codex_go/state	0.010s
--- PASS: TestModelGuardianReviewerIncludesRetainedInstructionsLikeRust (0.00s)
--- PASS: TestRuntimeRouterRetainsAssistantContextLikeRust (0.00s)
ok  	codex_go/appserver	0.100s
```

**A.4.2 `ed6f52b7` #49702** — 改名类，双证。
① 符号面（fresh）：
```
$ git grep -c -E 'maxOpenFileReads|maxFileReadHandleIDBytes|validateFileReadHandleID' ed6f52b7 -- '*.go'
rc=1   # 旧名在 tip 全仓 0 处（base 39dd8e94 为 20 处）
$ git grep -n -E 'maxOpenFiles|maxFileHandleIDBytes|validateFileHandleID' ed6f52b7 -- 'execserver/server.go'
execserver/server.go:94:	maxOpenFiles         = 128
execserver/server.go:95:	maxFileHandleIDBytes = 32
execserver/server.go:893:		fileSlots:          make(chan struct{}, maxOpenFiles),
execserver/server.go:911:		fileSlots:  make(chan struct{}, maxOpenFiles),
execserver/server.go:2422:	if err := validateFileHandleID(params.HandleID); err != nil {
execserver/server.go:2463:		return nil, requestError(-32600, fmt.Sprintf("at most %d file handles may be open per connection", maxOpenFiles))
execserver/server.go:2508:	if err := validateFileHandleID(params.HandleID); err != nil {
execserver/server.go:2556:	if err := validateFileHandleID(params.HandleID); err != nil {
execserver/server.go:2597:	if err := validateFileHandleID(params.HandleID); err != nil {
execserver/server.go:2627:func validateFileHandleID(handleID string) error {
execserver/server.go:2628:	if len(handleID) > maxFileHandleIDBytes {
execserver/server.go:2629:		return requestError(-32600, fmt.Sprintf("file handle ID must not exceed %d bytes", maxFileHandleIDBytes))
```
② 容量门行为（fresh，删掉 `openFile` 的 `reserveFileSlot()` 门）：
```
=== RUN   TestFilesystemOpenCapacityBoundsInFlightOpensLikeRust
    server_test.go:1337: slots in use after duplicate = 0, want 1
--- FAIL: TestFilesystemOpenCapacityBoundsInFlightOpensLikeRust (0.00s)
FAIL
FAIL	codex_go/execserver	0.006s
```
恢复（`status=[]` 空）后：
```
--- PASS: TestClientFileReadStreamClosesExactBoundaryAndReleasesCapacityLikeRust (0.10s)
--- PASS: TestFilesystemHandleAndReadLimitsMatchRust (0.00s)
--- PASS: TestFilesystemOpenCapacityBoundsInFlightOpensLikeRust (0.05s)
ok  	codex_go/execserver	0.168s
```
（同 §5.5：`128`/`32` 字面值无机器判据，仅"改名完备 + 门机制活着"可证。）

## A.5 缩小范围结论
`go build` ✅ · 八包整包 **新增失败 0** ✅（含 `codexuds`）· parity ✅ · 两笔重头 RC 均可复现 FAIL/恢复 ok ✅
→ **integ86 可合入 main**；唯一非基线项是已知 flaky `TestOtelProviderReloadsAfterAccountChange`（两侧同现，勿修）。
