# integ86b 增量深审报告（round 86 · syncl4 · Linux 节点 · 只读）

- 对象：`origin/integ86b` tip **`1306de38978c97bbbb861ef910ac48e0b4792e6e`**
- 基线：`ed6f52b7`（= 现 `origin/main`，含已发布的 5 笔 integ86）
- 增量 4 笔（从旧到新）：`b153fe59` #48983 / `ee19c38d` #49075 / `de6ff942` #50200 / `1306de38` #49145
- 纪律：**只读**——未 commit / 未 push / 未改任何 Go 文件（RC 探针跑完逐字节还原，每轮 `git status --porcelain` 为空）。核验后 worktree 已 remove。

## 0. 结论

**Green —— 这 4 笔可合入 main**：门禁全绿（含 `go vet` 零输出）、受影响包对拍**新增失败 0**、12 条新测试全 PASS、4/4 笔 RC 均可复现 FAIL 并恢复 ok。唯一非基线项是已知 flaky `TestOtelProviderReloadsAfterAccountChange`（两侧都抖，勿修）。

## 1. 固定点与增量清单

```
$ git fetch origin --prune
$ git rev-parse origin/integ86b
1306de38978c97bbbb861ef910ac48e0b4792e6e
$ git log --oneline ed6f52b7..origin/integ86b
1306de38 syncw2: hide reasoning summaries in the /status card for server connections (#49145)
de6ff942 syncw2: report the configured TUI mode in doctor (#50200)
ee19c38d syncw3: inherit only ready or starting environments when spawning subagents (#49075)
b153fe59 syncw1: keep timestamp-only thread metadata observations narrow (#48983)
$ git diff --stat ed6f52b7 origin/integ86b
 appserver/agent_controller.go             |  10 +-
 appserver/environment_inheritance.go      | 172 +++++++++++++++
 appserver/environment_inheritance_test.go | 345 ++++++++++++++++++++++++++++++
 appserver/runtime_router.go               |  10 +-
 doctor/configured_tui_mode_test.go        | 115 ++++++++++
 doctor/doctor.go                          |  29 +++
 state/backfill.go                         | 211 ++++++++++++++++--
 state/backfill_test.go                    | 139 ++++++++++
 tui/state.go                              |  15 +-
 tui/state_test.go                         |  33 +++
 tui/tea/model.go                          |   5 +
 tui/tea/model_test.go                     |  29 +++
 12 files changed, 1084 insertions(+), 29 deletions(-)
```
逐笔文件数：`b153fe59`=2（state）、`ee19c38d`=4（appserver）、`de6ff942`=2（doctor）、`1306de38`=4（tui + tui/tea）。四笔 scope 与 3 个包族互不重叠（除 #49075 触及 appserver 的既有两文件）。

## 2. 工作树

```
git worktree add --detach ../codex_go_wt/integ86b-verify 1306de38   # 候选（clean）
# 基线复用 ../codex_go_wt/integ86c-base (detached ed6f52b7, clean)
```
注：`../codex_go_wt/integ86c-base` 同时是本轮批次三（T2）的基线，也是本轮对拍基线——同一棵树 `ed6f52b7`，只读使用。

## 3. 门禁

```
$ gofmt -l $(git diff --name-only ed6f52b7..HEAD)     # 12 个改动文件
                                                      # ← 空输出，rc=0
$ go build ./... ; echo $?
0
$ go vet ./state/ ./tui/... ./doctor/ ./appserver/ ; echo $?
0                        # ← 本轮 vet 完全干净（与 T2 批次不同，没有 copylocks 噪声）
$ CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1
ok  	codex_go/parity	0.864s
```

## 4. 受影响包对拍（基线 = `ed6f52b7` 单独检出）

```
go test ./state/ ./tui/... ./doctor/ ./appserver/ -count=1 -timeout 25m
```
候选 `1306de38`：
```
ok  	codex_go/state	10.774s
--- FAIL: TestSelectStartupTooltipMatchesRustPlanBranches (0.00s)
FAIL	codex_go/tui	0.473s
ok  	codex_go/tui/agents_overview 0.142s / tui/anim 0.081s / tui/app 0.185s / tui/bottom_pane 0.194s /
        tui/chatwidget 0.154s / tui/diffview 0.138s / tui/exec_cell 0.105s / tui/footerhint 0.013s /
        tui/history_cell 0.124s / tui/ide_context 0.018s / tui/markdown 0.812s / tui/notifications 0.012s /
        tui/onboarding 0.033s / tui/overlay 0.010s / tui/pets 0.756s / tui/status 0.106s /
        tui/streaming 0.145s / tui/styles 0.014s / tui/tui 0.024s      （全 ok）
--- FAIL: TestModelAppCommandUsesRustHistoryMessages (0.00s)
FAIL	codex_go/tui/tea	23.920s
--- FAIL: TestReportBuildsLocalChecks (0.01s)
--- FAIL: TestFilesystemPathsCheckListsWithoutProbingLikeRust (2.01s)
FAIL	codex_go/doctor	5.634s
--- FAIL: TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust (0.02s)
--- FAIL: TestOtelProviderReloadsAfterAccountChange (5.01s)          ← 已知 flaky
--- FAIL: TestPluginListHonorsPerRepositoryConfigLikeRust (0.00s)
--- FAIL: TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust (2.02s)
FAIL	codex_go/appserver	37.639s
```
基线 `ed6f52b7`：
```
--- FAIL: TestSelectStartupTooltipMatchesRustPlanBranches (0.00s)     FAIL codex_go/tui 0.515s
--- FAIL: TestModelAppCommandUsesRustHistoryMessages (0.00s)          FAIL codex_go/tui/tea
--- FAIL: TestReportBuildsLocalChecks (0.01s)
--- FAIL: TestFilesystemPathsCheckListsWithoutProbingLikeRust (2.00s)  FAIL codex_go/doctor 6.900s
--- FAIL: TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust (0.02s)
--- FAIL: TestPluginListHonorsPerRepositoryConfigLikeRust (0.00s)
--- FAIL: TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust (2.02s)  FAIL codex_go/appserver 30.077s
（state/tui/* 子包全 ok）
```
判据：
```
$ diff /tmp/b_base_set.txt /tmp/b_ver_set.txt
3a4
> TestOtelProviderReloadsAfterAccountChange
base=7  verify=8
```
→ **新增失败 0**；`tui`/`tui/tea`/`doctor` 的既有失败集与基线**逐条相同**（tui 1 / tui/tea 1 / doctor 2 / appserver 3，多出的 1 条为已知 flaky）。

**12 条新测试全部 PASS**（原文）：
```
--- PASS: TestRolloutRebuildTimestampOnlyTouchesTimestampsLikeRust (0.02s)            ok codex_go/state 0.055s
--- PASS: TestInheritableEnvironmentSelectionsLikeRust (0.00s)
--- PASS: TestRuntimeAgentControllerChildInheritsInheritableEnvironmentSelectionsLikeRust (0.00s)
--- PASS: TestRuntimeRouterPropagatesOwnerEnvironmentResultToDescendantsLikeRust (0.00s)
--- PASS: TestRuntimeRouterPropagatesOwnerEnvironmentFailureToDescendantsLikeRust (0.00s)
--- PASS: TestRuntimeRouterInheritedEnvironmentConfigKeepsChildConfigurationLikeRust (0.00s)   ok codex_go/appserver 0.169s
--- PASS: TestConfigCheckReportsConfiguredTUIModeLikeRust (0.00s)
--- PASS: TestConfigCheckConfiguredTUIModeDefaultsToFullscreenLikeRust (0.00s)
--- PASS: TestRenderHumanShowsConfiguredTUIModeRowLikeRust (0.00s)
--- PASS: TestConfigCheckReportsConfiguredTUIModeInJSONLikeRust (0.00s)                ok codex_go/doctor 0.029s
--- PASS: TestStateRenderStatusCardHidesReasoningSummariesForServerConnectionLikeRust (0.00s)  ok codex_go/tui 0.055s
--- PASS: TestNewModelCarriesServerConnectionToStatusCardLikeRust (0.00s)              ok codex_go/tui/tea 0.100s
```

## 5. 逐笔 RC（值/行为级；破坏→FAIL→逐字节还原→ok，每轮 `status=[]`）

### 5.1 `b153fe59` #48983 —— 时间戳-only 观测不得重写其它元数据
破坏：拆掉 `upsertRolloutThread` 里的窄写短路
```
	if exists && rolloutThreadRowMatchesExceptTimestamps(existing, intendedRolloutThreadRow(...)) {
		return r.touchThreadUpdatedAt(ctx, metadata.id, metadata.updatedAt.Unix(), updatedMillis)
	}
```
```
=== RUN   TestRolloutRebuildTimestampOnlyTouchesTimestampsLikeRust
    backfill_test.go:327: timestamp-only rebuild writes = timestamp:1 full:1, want timestamp:1 full:0
--- FAIL: TestRolloutRebuildTimestampOnlyTouchesTimestampsLikeRust (0.02s)
FAIL	codex_go/state	0.025s
```
恢复：`ok codex_go/state 0.028s`。

### 5.2 `ee19c38d` #49075 —— 子代理只继承 ready / starting 环境
破坏：让 `inheritableEnvironmentSelections` 的 failed 过滤失效（保留编译）
```
=== RUN   TestInheritableEnvironmentSelectionsLikeRust
    environment_inheritance_test.go:47: … want ids [remote starting local]        （got 多出 broken）
    environment_inheritance_test.go:47: … = [{…failed…}] want ids []               （drops_the_failed_attachment_only）
--- FAIL: TestInheritableEnvironmentSelectionsLikeRust (0.00s)
    environment_inheritance_test.go:100: child selections = [… remote-broken …], want the ready and pending attachments only
--- FAIL: TestRuntimeAgentControllerChildInheritsInheritableEnvironmentSelectionsLikeRust (0.00s)
FAIL	codex_go/appserver	0.042s
```
恢复：两条 + 另外两条 Propagates 全家 PASS（`ok codex_go/appserver 0.051s`）。

### 5.3 `de6ff942` #50200 —— doctor 报告配置的 TUI 模式
破坏：删掉 `configCheck` 里的 `"configured TUI mode: " + configuredTUIModeForDoctor(cfg)` 行
```
configured_tui_mode_test.go:35: fullscreen_transcript=true details = […], want configured TUI mode: fullscreen
--- FAIL: TestConfigCheckReportsConfiguredTUIModeLikeRust (0.00s)
configured_tui_mode_test.go:47: details = […], want the fullscreen default without a config
--- FAIL: TestConfigCheckConfiguredTUIModeDefaultsToFullscreenLikeRust (0.00s)
configured_tui_mode_test.go:76: human report missing "      configured TUI mode      scrollback":
--- FAIL: TestRenderHumanShowsConfiguredTUIModeRowLikeRust (0.00s)
configured_tui_mode_test.go:112: checks.config.load.details[configured TUI mode] = <nil>, want "fullscreen"
--- FAIL: TestConfigCheckReportsConfiguredTUIModeInJSONLikeRust (0.00s)
```
恢复：4/4 PASS（`ok codex_go/doctor 0.016s`）。

### 5.4 `1306de38` #49145 —— 服务端连接时 /status 卡片隐藏 reasoning summaries
破坏：`tui/tea/model.go` 的 `state.RemoteConnection = options.LocalDaemonSession || options.RemoteAppServer` → `= false`
```
=== RUN   TestNewModelCarriesServerConnectionToStatusCardLikeRust
    model_test.go:8995: RemoteConnection = false, want true      （local_background_daemon）
    model_test.go:8995: RemoteConnection = false, want true      （remote_app_server）
--- FAIL: TestNewModelCarriesServerConnectionToStatusCardLikeRust (0.00s)
FAIL	codex_go/tui/tea	0.034s
```
（in-process session 子用例仍 PASS，符合负例预期。）恢复：`tui/tea` 与 `tui` 两条均 PASS。

## 6. 结论表

| 判据 | 结果 | 依据 |
|---|---|---|
| 固定点 tip `1306de38` / base `ed6f52b7` | ✅ | §1 |
| gofmt（12 文件） | ✅ 空 | §3 |
| `go build ./...` | ✅ rc=0 | §3 |
| `go vet state/tui/.../doctor/appserver` | ✅ **rc=0（零输出）** | §3 |
| parity | ✅ `ok codex_go/parity 0.864s` | §3 |
| 受影响包 vs 基线，新增失败 0 | ✅（diff 仅 flaky 1 条） | §4 |
| 12 条新测试 | ✅ 全 PASS | §4 |
| 4 笔逐笔 RC | ✅ 4/4 FAIL→恢复 ok | §5 |
| 遗留失败（7 条） | 环境性/预存在（tui 1、tui/tea 1、doctor 2、appserver 3） | §4 |

**→ integ86b 这 4 笔判 Green，可合入 main**（发布由队长/发布操作员执行）。

## 7. 边界与未决

1. 本报告只覆盖 `ed6f52b7..1306de38` 的**增量 4 笔**；其基数（integ86 的 5 笔）见 `update/verify_integ86_2026_10_07.md`。
2. 沿用既有口径：`TestOtelProviderReloadsAfterAccountChange` 为已知 flaky，未计新增失败、未修。
3. 未做 `go test ./...`；未跑 Rust 侧 cargo 对照（本次无 Rust 工作树改动需求）。
