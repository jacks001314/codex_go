# 2026-11-10 账本回写与静态层刷新

- Rust 目标（候选基线）：`e54a5713caf331f42a6eef0166186df5f7563c29`（#60391，`main`）
- 账本区间：`bd19459358f534ed1cae464ec13d56600aeb45f2..e54a5713caf331f42a6eef0166186df5f7563c29`，共 3690 条
- 本轮不改功能行为：只做**账本状态回写**与**静态层 pin 刷新**，让"还差什么"这个问题重新答得准。

## 0. 结果

| 指标 | 轮前 | 轮后 |
| --- | --- | --- |
| `parity/commits.json` partial | 58 | 31 |
| `parity/commits.json` complete | 2989 | 3016 |
| `parity/commits.json` blocked_precondition | 4 | 4 |
| L0 静态快照门禁 | 5 红 | 0 红（本轮的 5 个快照用例） |
| `parity/domains.json` open | 5 | 5（app-server-schema 的残余已更新为单一 issue） |

本轮**没有**前移 `baseline.json.verified.rustCommit`：它绑定的是 `parity.json` 的 SDK 证据，
没有新的 SDK/parity 运行就前移等于伪造认证，`TestAlignmentBaselineIsFrozenAndTraceable` 也会挡住。

## 1. 账本回写（58 -> 31 partial）

判定口径：partial 条目若其 evidence 已经指向一个 **complete 契约**，且该契约列出的 verifier 全部通过，
就说明行为已落地、只是 2026-11-08 那一轮只记了 plan 链接没回写状态，本轮补记为 complete。

回写 27 条（PR -> 契约）：

| PR | 契约 |
| --- | --- |
| #59887 / #59903 | `release-prerelease-cleanup` |
| #59928 | `sidebar-chooser-list-bindings-and-hover` |
| #59933 | `protected-model-profile-downgrade` |
| #59954 | `skills-tool-name-resolution` |
| #59958 | `thread-model-metadata-exposure` |
| #59963 | `turn-context-required-permission-profile` |
| #59983 | `typed-prompt-markdown-render` |
| #59991 | `mcp-app-invocation-context-refresh` |
| #59993 | `message-board-fixed-read-view` |
| #60003 | `realtime-runtime-auth` |
| #60009 | `tui-custom-gateway-signin` |
| #60011 | `multi-agent-hosted-tools-guidance` |
| #60014 | `managed-runtime-auth-mode` |
| #60055 | `runtime-auth-websocket-revalidation` |
| #60061 | `cloud-image-messages` |
| #60067 | `side-chat-policy-first-request` |
| #60073 | `remote-control-runtime-auth` |
| #60101 | `analytics-runtime-auth` |
| #60152 | `side-chat-persistence-and-tab-close` |
| #60157 | `code-mode-incremental-exec-definitions` |
| #60163 | `code-mode-incremental-exec-wiring` |
| #60177 | `incremental-exec-catalog-metrics` |
| #60180 | `skill-mention-punctuation-join` |
| #60181 | `sidebar-tab-keyboard-mouse-navigation` |
| #60186 | `provider-creation-during-signin` |
| #60188 | `incremental-exec-message-overrides` |

验证方式：把这 27 个契约的 verifier 名并成一条 `-run` 正则，在 verifier 实际所在的包里实跑，
并逐条用 `go test -list` 确认 **71 个 verifier 名字都能定位到包**，不存在"包选错导致静默跳过"：

- `go test ./agent ./agentboard ./app ./appserver ./auth ./chatgptapi ./codemode ./config ./context ./features ./mcp ./metrics ./model ./prompt ./protocol ./realtime ./remotecontrol ./runtimeauth ./sandbox ./session ./state ./tui ./tui/analytics ./turn -run '<71 verifiers>' -count=1`：全绿
- `go test ./tui/tea ./tui/app ./tui/history_cell ./parity -run '<22 verifiers>' -count=1`：全绿
- `go test ./cloudclient ./cloudbackend -run '<cloud-image-messages verifiers>' -count=1`：全绿

**保留 partial** 的例子：#57267（其 evidence 明确写着 Cloud 端设置仍由客户端拥有，属真实残余）、
#60082（见 §3）、Guardian v3 证据链 6 条、UI tree 9 条、#54776 等。

## 2. 静态层 pin 刷新（L0）

| 门禁用例 | 轮前 | 轮后 |
| --- | --- | --- |
| `TestFeaturesRegistryMatchesRust` | Go 缺 `gateway_setup_wizard` | `features/features.go` 补登记（Stable / 默认关，来自 #60009 `2497eab251`） |
| `TestRustCriticalFileHashesSnapshot` | `Cargo.toml` 漂移 | `Cargo.toml`、`exec/src/lib.rs`、`core/src/client.rs`、`app-server-protocol/src/protocol/common.rs`、`core/tests/suite/mod.rs` 五个哈希重取 |
| `TestRustGoldenFixtureRootsSnapshot` | `app-server/tests/suite/v2` 189 vs 188 | 修 188 -> 189；同时 `core/tests/suite` 456 -> 471 |
| `TestPrecomputedAppServerExportsMatchRustTarget` | vendored `.zst` 落后 | 两个 `.zst` 从 anchor 逐字节重新 vendored，`appserver/schema_test.go` 的 SHA pin 重取 |
| `TestRustTUISnapshotManifestCoversPrioritySurfaces` | 总数 1515 vs 1481 | 总数 1481 -> 1515；7 个目录计数更新；新增 `tui/src/bottom_pane/footer/snapshots`（1 个 golden） |

补充清理：`appserver/schema_test.go` 的 forward-ported 豁免已在本次 anchor 全部过期
（`modelProvider/list`、`modelProvider/config/write`、`gatewayOAuth` 可选参数都已进入 Rust fixture），
两个豁免 map 清空，稳定 fixture 比较不再豁免任何方法。

## 3. 本轮暴露出的真实缺口

刷新静态层后，`go test ./appserver` 的三条 fixture/surface 用例收敛成**同一个**缺口：

- `account/securitySetup/read`（#60082）没有在 Go 的 `BuildProtocolSchema` 中暴露，
  于是 `ClientRequest.json` / `ClientRequest.ts` 与 Rust fixture 差这一个方法。
- Go 侧已有能力：`chatgptapi.GetSecuritySetup`（云客户端）与 `app/security_setup.go`（本地读取 + 身份复核）。
  缺的是把它作为 RPC 暴露给 app-server，并让 TUI 走 app-server（Rust 的 #60082 正是这个方向）。
- 这三条用例在本轮**之前就是红的**（它们只依赖 Rust fixture 与 Go 生成器，与本轮改动无关），
  本轮把失败信息从"4 条 forward-ported 噪声 + 1 条缺失"收敛成"1 条缺失"。

## 4. 剩余口径（截至 2026-11-10）

- 31 partial：Guardian v3 证据链 6 条、UI tree 9 条、TUI/侧栏/侧聊、模型 provider、Code Mode 增量工具等。
- 4 blocked_precondition：#40625、#42841、#43624、#55378（均需平台原生绑定）。
- 5 open domains：`alignment-control`、`sqlite-runtime-version`、`app-server-schema`、`sdk-cancellation`、`cross-platform-release`。
- 1 open contract：`mcp-network-policy-derivation`。
- `parity.json` 的 `generatedAt` 仍是 2026-10-28、`rustUpstreamHead` 仍是 `67f0994b`；
  需要一次真实 SDK/parity 运行才能前移，本轮不动。

## 5. 验证

- `go test ./parity -count=1`（对齐门禁 + 全部快照用例）
- `go test ./parity -count=1 -run 'TestFeaturesRegistryMatchesRust|TestPrecomputedAppServerExportsMatchRustTarget|TestRustGoldenFixtureRootsSnapshot|TestRustTUISnapshotManifestCoversPrioritySurfaces|TestRustCriticalFileHashesSnapshot|TestRustWorkspaceMembersSnapshot'`
- `python update/ledger_verify_2026_11_08.py`（GATE PASS）
- `go build ./...`、`go vet` 受影响包

## 6. 顺带修掉的两处

- `appserver/appserver_v2_suite_manifest_test.go`：Rust anchor 已经把 #58845/#58876 的
  `model_provider_config_tests`、`model_provider_list_tests` 纳入 v2 suite，Go 侧清单缺这两条，
  属于静态清单漂移，本轮补登记。
- `appserver/remotecontrol_runtime_auth_wiring_like_rust_test.go`：router 为 manager backend 打开的
  注册库在测试结束后没有释放，Windows 上 `t.TempDir` 清理会失败
  （`state_5.sqlite ... being used by another process`）。用 `t.Cleanup` 关闭 `backend.Store`，
  已在 pristine 版本上复现失败、修复后通过。

## 7. 本轮未处理、已确认与改动无关的失败

- `TestRuntimeRouterActiveGoalContinuationStartsTurnWhenIdle`（`appserver`）：在干净 `HEAD`（`867baef3`）
  的独立 worktree 上同样失败，日志为 `read thread goal: sql: database is closed`，
  goal continuation 在 router 关闭后仍被读一次。与账本/静态层无关，需要单独一个 lane 查。
