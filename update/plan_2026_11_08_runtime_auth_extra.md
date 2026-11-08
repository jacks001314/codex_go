# 运行时鉴权组补充判定（#60035、#60055）+ 7 条契约登记表

- 作者：ledger-align（对齐账本 lane）
- 日期：2026-11-08
- 性质：**只读分析**。不改 Go 代码；`parity/**` 只改 `domains.json` 的域证据文本（见 §6），不动 commits/baseline/contracts。
- Rust 基线：`/home/jacks/jacks_dev/codex`（`codex-rs`，HEAD `748ab55ca7`），两条提交均可 `git show`。
- Go 基线：本 lane 完整镜像（`project_fetch --all`，6,954 文件 / 约 72 MB），下文「零命中」均为全树结果。
- 承接：`update/plan_2026_11_08_runtime_auth.md`（5 条 partial）。本文件补 2 条并把 7 条收敛成可登记形态。

## 0. 判定汇总

| commit | PR | 主题 | 判定 |
| --- | --- | --- | --- |
| `0c58612c3c` | #60035 | Fix authentication for git-attribution settings requests | **not_applicable** |
| `d4a341bd52` | #60055 | Reauthenticate runtime requests before reusing WebSocket access | **partial** |

7 条合计：`partial 6`、`not_applicable 1`、`ported 0`、`aligned 0`。

## 1. `0c58612c3c` #60035 — git-attribution settings 的鉴权

### 1.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/ext/git-attribution/src/lib.rs:39-46` | 从 thread store 取 `ProviderAuthContext`，优先用它携带的 `credentials` 与 `runtime_auth`，而不是扩展自身持有的 `auth_manager` |
| `…/lib.rs:59-70` | 把 `runtime_auth` 透传给 `resolve_attribution_policy` |
| `…/lib.rs:74-90` | 策略未命中/超时改为 **直接 `break false`**（不再构造一个 `enabled=false` 的假 policy 继续走 generation 比较），让世界状态贡献能结束 |
| `…/policy.rs:58-62` | `resolve_attribution_policy` 新增 `runtime_auth: Option<&(Arc<RuntimeAuthManager>, RuntimeAuthMode)>` 参数 |
| `…/policy.rs:69-86` | 改用 `auth_manager.auth_with_http_client_factory()`；用 captured factory 的 network policy 做 `with_account_binding`；改用 `BackendClient::from_auth_with_runtime(...)`（即 **跑 runtime 断言的 backend client**） |
| `…/policy.rs:69-98` | 每次 auth generation 变化都重查（`recovery_generation`），避免把旧凭据下的策略写死 |
| `codex-rs/ext/git-attribution/src/world_state.rs:8-35` | `<git_attribution>` 世界状态片段：`ENABLED/DISABLED_INSTRUCTIONS`（`Co-authored-by: Codex <noreply@openai.com>`、PR 的 `Generated with [Codex](…)`），带 legacy matcher 与 retained-fragment matcher |
| `codex-rs/app-server/tests/suite/v2/git_attribution.rs`（+149） | 两种 mode × 默认/自定义目标；重复 401 只刷新一次并**省略** attribution 指令 |

### 1.2 Go 侧锚点（全树）

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 是否有 git-attribution 扩展 | `rg -n "git_attribution\|GitAttribution" -g '*.go'` | 仅 3 处：`eventmap/eventmap.go:205`（`contextualDeveloperPrefixes` 里登记 `<git_attribution>` 这个**前缀字符串**）、`eventmap/eventmap_test.go:221`、`appserver/appserver_v2_suite_manifest_test.go:80`（模块清单条目） |
| 是否发出该世界状态片段 | `rg -n "attribution" prompt/*.go` | 0 matches |
| 是否有 user-settings 后端调用 | `rg -n "get_user_settings\|UserSettings\|user_settings" -g '*.go'` | 0 matches |
| 是否有 extension API | `rg -n "extension_api\|codex_extension" -g '*.go'` | 仅 `tool/hooks.go:39`、`:52` 的**注释**引用 Rust 名字 |
| Go 唯一一处 trailer 落地 | `memories/workspace.go:27` | `baselineCommitMessage = "Initialize Codex git baseline\n\nCo-authored-by: Codex <noreply@openai.com>"`——Go 自己建基线提交时写死，与用户设置无关 |

> 说明 `appserver_v2_suite_manifest_test.go:80` 的 `{Module: "git_attribution", Owner: "appserver, session", Focus: "git attribution propagation"}` 指的是 **turn 级 attribution 传播**（`session/turn_attribution.go` 的 `TurnAttribution`），与 `ext/git-attribution` 的“提交/PR 归属”不是同一件事；这条我从 Go 测试文件反查确认，不把它当成本条已实现的证据。

### 1.3 为什么判 `not_applicable` 而不是 `partial`

Rust 这条改的是一个**在世界状态里注入归属指令的扩展**；它在 Go 侧**连承载面都不存在**：

1. 没有扩展注册表/世界状态贡献机制（`extension_api` 只有注释）；
2. 没有 user-settings 拉取（`get_user_settings` 零命中），而这段逻辑的全部价值就是“查服务端设置决定是否注入”；
3. `<git_attribution>` 只作为**待识别前缀**存在（用于把 Rust 生成的 prompt 片段从事件流里识别/剥离），Go 从不生成它。

因此“缺的是能力面而不是接线”，按账本口径记 `not_applicable`。**不建议登记契约**（理由见 §5：登记会让 `TestContractVerifiersExistInGoTestFiles` 永远缺一个 verifier）。

### 1.4 若将来要做（不是本条要求）

- 落点：`prompt/`（世界状态片段）+ 一个新的 `appserver` user-settings 客户端 + `memories/workspace.go` 的 trailer 逻辑改为受设置驱动；
- 前置：先决定 Go 是否引入“世界状态贡献”抽象（现在没有），否则只能写死一条 developer 指令；
- 测试要点：开启/关闭两态各一条、设置拉取失败时**宁可不注入也要结束**（对应 §1.1 的 `break false` 语义）。

## 2. `d4a341bd52` #60055 — 复用 WS 前重新鉴权

### 2.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/core/src/client.rs:318-320` | 文档化新语义：turn 内的 Responses WS 仍复用，但 **runtime 认证的请求在请求间重连**以重验凭据来源 |
| `…/client.rs:1749-1755` | `preconnect` 增加条件：`runtime_auth().is_some()` 时**直接 `return Ok(())`**——跳过投机预连接 |
| `…/client.rs:1866-1873` | 复用判定增加 `runtime_id.is_some()` 分支：只要运行时身份在场就**强制重连**，不看 headers/key 是否相同 |
| `codex-rs/ext/guardian-v2/src/async_scorer/sampler/connection_pool.rs:163-166` | `start_refill` 对 runtime 源直接不补池（不预暖） |
| `…/connection_pool.rs:234-267` | 取连接时：`runtime_id.is_some()` → 用 `runtime_connection()` 新建，不弹 idle 池 |
| `…/connection_pool.rs:472-529` | 新增 `runtime_connection`：受 `sockets` 信号量约束；`ConnectionTimeout` 冷却；**401/403/429 与 Policy/Build 错误必须上抛**，其余（如不支持 upgrade）返回 `None` 让上层回落 HTTP |
| `codex-rs/model-provider/src/workspace_routing.rs:88` | `ResponsesConnectionKey` 加 `Clone`（供新分支复用） |

### 2.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| Go **会**在一个 turn 内复用同一 WS（与 Rust 一致） | 【F:model/responses_agent.go†L1417-L1418】`connectionReused := conn != nil`；`:1418` 注释即 Rust 的 `connection_reused` |
| 复用只在 4 种情况下被打断：**父路由亲和性变化、凭据所有权变化（全清）、socket 已关、以及路径上的失败** | 亲和性：【F:model/responses_agent.go†L1410-L1416】；所有权变化：【F:model/responses_agent.go†L921-L931】（`closeResponsesWebsocketSession(..., "auth ownership changed")`）；其余见 `closeResponsesWebsocketSession` 的 20 处调用 |
| **没有「运行时就强制重连」这一分支** | `model/` 全包不 import `runtimeauth`（`rg -n "codex_go/runtimeauth" -g '*.go'` 的 7 个命中里没有 `model/`） |
| `websocketReauth` 只用于**指标打点**，不参与是否重连 | 定义【F:model/responses_agent.go†L332-L344】；调用点 `:1116`、`:1441` 都是 `reauth := ...` 后传给 `recordWebsocketConnectRecord`，没有 `if reauth { close... }` |
| 投机预连接**存在且默认开启** | 启动预暖：【F:appserver/session_startup_prewarm.go†L54-L63】`scheduleStartupPrewarm`（握手带 turn 元数据，“prepared connection”）；历史预暖 `:187`；模型侧预暖重连【F:model/responses_agent.go†L1071-L1076】 |
| Guardian 有 prewarm trunk 并在首审复用 | 【F:appserver/guardian_reviewer.go†L779-L800】、`:1008-L1010`；测试 `TestGuardianSessionRunnerReusesPrewarmResponseForFirstReview`、`TestEnsureGuardianReviewerPrewarmsOnceAndFallsBackLazily` |
| 无 runtime 感知，所以“pause 后旧 socket 仍可用”在 Go 侧同样成立 | 同上，缺 `runtime_id` 概念；Go 唯一的身份代次是 `connectedAuthRevision`（token 哈希，`websocketAuthRevision`【F:model/responses_agent.go†L315-L328】），token 不变则不断链 |

### 2.3 缺什么 / 影响面 / 最小修复路径

- 缺：
  1. `model` 侧的 **runtime 在场标志**（谁提供 provider、是否有 runtime provider），以及 `runWebSocket` 里的“每次请求前强制重连”分支；
  2. `preconnect`/startup prewarm/guardian trunk prewarm 在 runtime 在场时的**跳过**；
  3. Guardian 分类在 runtime 在场时的“新建连接而非复用预暖”，以及 **401/403/429 上抛、其余回落 HTTP** 的错误分流（Go 现在 prewarm 失败一律降级，见 `prewarm failed` 等分支）。
- 影响面：实例被 pause 后，Go 仍能在**已建立的** socket 上继续提交（服务端若不逐帧校验就放行）；revoke 后同理。这是“批准/样本采集安全性”相关面，且与 #60003 的静态 Bearer 缺陷叠加。
- 最小修复路径：
  1. `model.AgentRequest`/`ResponsesAgentRunner` 增 `RuntimeAuthenticated bool`（由 appserver 侧 `runtimeauth.ProviderForHarness(...) != nil` 决定，**不引入 model→runtimeauth 依赖**）；
  2. `runWebSocket`：`RuntimeAuthenticated && session.conn != nil` → `closeResponsesWebsocketSession(session, "runtime revalidation")` 后再拨号；`preconnect` 同条件直接返回；
  3. `appserver/session_startup_prewarm.go:54`：runtime 在场时 `scheduleStartupPrewarm` 直接不调度；
  4. Guardian：runtime 在场时不复用 trunk prewarm（`adoptPrewarm`/`takePrewarm` 前置判断）。
- 测试建议：`model/runtime_ws_revalidation_like_rust_test.go`：同一 turn 两次请求两次握手、pause 后第一次请求即失败、`assertion_only` 下同样成立、unpause 后恢复；`appserver/guardian_runtime_prewarm_like_rust_test.go`：runtime 在场不预暖、403 上抛不降级。

## 3. 【回答提问】`realtime_runtime.go` 的静态 Bearer 到底是哪种

**结论：两种都成立，但要按路径分开说。** 主 WebSocket 是“每次 start 一次性捕获”；WebRTC/existing-call 的 **sideband 在会话内每 N 次重连复用同一个 token 字面量**，且中途不重读凭据、不刷新、不因 401 终止。

### 3.1 调用链（自上而下，逐段可查）

```
①  RPC  thread/realtime/start
    → appserver/realtime_runtime.go:25-30 startRealtimeConversationAsync
       options, err := r.realtimeStartOptions(&params)      // 每个 start 调一次
       r.requireRealtime().StartWithOptions(&params, options)

②  realtimeStartOptions        appserver/realtime_runtime.go:380
    ├─ :549-558 realtimeAuthSnapshot  → resolveAuthWithLoginRestrictions(codexHome) 读凭据存储
    ├─ :560-581 realtimeAPIKey       → provider.APIKey() > ExperimentalBearerToken > snapshot api-key > OPENAI_API_KEY
    ├─ :455-470 transportType=="websocket" → headers.Set("Authorization", "Bearer "+apiKey)  ← 字符串字面量在此固定
    └─ :486 TransportBackendConfig{Headers: headers, CallHeaders: callHeaders, PrepareCall: callAuth.Apply}

③  realtime.Start（WebRTC 路径）
    newRealtimeSideband        realtime/transport.go:393-405
       sidebandBackend := cloneTransportBackendConfig(backend)   // clone 一次
       sideband.backend = sidebandBackend

④  重连循环                   realtime/transport.go:406-490
    for { dialRealtimeTransport(ctx, threadID, sideband.backend, ...) ... }
       └─ wire.go:33-36  headers := backend.Headers.Clone()      // 仅 clone 同一份 map
       └─ wire.go:58     websocket.Dial(..., HTTPHeader: headers) // 发出的仍是②里那个 token
```

### 3.2 三条可验证推论

1. **一次性捕获**：`apiKey` 只在 ② 求值一次，且与 FedRAMP 选路来自同一把 key（`:455-L462` 注释「Routing and the Authorization header must come from this same key」）。凭据存储此后不再读。
2. **重连复用同一值**：④ 的每次尝试都用 ③ clone 出来的 `sideband.backend`；`dialRealtimeTransport` 只做 `Headers.Clone()`（浅拷贝 map 的副本，值不变）。所以第 2 次、第 N 次重连发的是**逐字节相同**的 `Authorization`。
3. **401 不判终局 → 同一失效 token 无限重连**：`realtimeHandshakeTerminal`【F:realtime/transport.go†L563-L575】只把 **404/410** 当终局，其余状态码走重连；结合【F:realtime/transport.go†L412-L413】的“uncapped attempts、capped backoff（≤5s）”，实例 revoke 后会一直重连同一个 token。

补充一条边界（避免过度声明）：**普通 `transportType=websocket` 没有进程内重连**——`runRealtimeConnection` 读到错误即 `finishRealtimeTransport` 关闭会话（【F:realtime/transport.go†L649-L664】），重连由上层重新 `thread/realtime/start` 承担，那条路径会重新走 ②（仍无 runtime，但至少 token 会重读）。

### 3.3 对修复方式的含义（回答“两种修复方式不同”）

- sideband：**既要“重算”也要“失效”**——重连前重新解析断言（重算），并把解析失败/401-denial 判为终局（失效旧值，别再重连）。
- 主 WS：只需“重算”（本来每次 start 都重算），但要把 ② 换成 runtime 分支，否则重算出来的仍是同一把 bearer。
- 两处都必须加 **401/403 分类**，否则重算之后仍会落回③④的无限重连。

## 4. 复现命令

```
$ rg -n "git_attribution|GitAttribution" -g '*.go'
  eventmap/eventmap.go:205:	"<git_attribution>",
  eventmap/eventmap_test.go:221:		"<git_attribution>",
  appserver/appserver_v2_suite_manifest_test.go:80: ...Module: "git_attribution"...

$ rg -n "get_user_settings|UserSettings|user_settings" -g '*.go'
  (0 matches)

$ rg -n "attribution" prompt/*.go
  (0 matches)

$ rg -n "codex_go/runtimeauth" -g '*.go' | grep -c "model/"
  0

$ rg -n "websocketReauth\(" model/responses_agent.go
  332:func (s *responsesWebsocketSession) websocketReauth(...)
  1116:		reauth := session.websocketReauth(r.AuthSnapshot)
  1441:		reauth := session.websocketReauth(r.AuthSnapshot)
  # 两处调用均为指标打点，无 if reauth { close } 分支

$ rg -n "closeResponsesWebsocketSession\(session" model/responses_agent.go | grep -c "auth ownership"
  1   # :926，唯一与凭据相关的强制断链，且是“账号所有权变化”而非 runtime 状态
```

## 5. 7 条契约登记表（可直接登记）

### 5.1 登记格式与静态门禁（先说约束，避免登记后红）

`parity/contracts/manifest.json` 每条契约的字段（由【F:parity/alignment_control_test.go†L251-L272】与【F:parity/verifier_existence_test.go†L21-L40】确定）：

| 字段 | 约束 |
| --- | --- |
| `id` | 非空、全局唯一 |
| `kind` | 非空；现存取值有 `source-surface`(26)/`shared-fixtures`(44)/`generated-protocol`(3)/`terminal-ui`(2)/`record-replay`(2)/`wire-protocol`/`sqlite-schema`/`differential-oracle`/`durable-data` |
| `rustPaths[]` | 至少 1 条，且**必须真实存在于 Rust 快照根**（`TestAlignmentRustContractOraclePathsExist`） |
| `goPaths[]` | 至少 1 条，且**必须真实存在于 Go 树**（`TestAlignmentContractInventoryIsTraceable`，可为目录） |
| `phase` | 非空（现存 `P1`/`P2`/`P3`/`P7`） |
| `status` | `missing` / `partial` / `complete` 之一 |
| `verifier` | 非空，多个用 `;` 分隔；**每个名字都必须在 Go 树里存在同名 `func TestXxx(t *testing.T)`**，与 status 无关 |

> 关键提醒：`TestContractVerifiersExistInGoTestFiles` **对任何 status 都要求 verifier 已存在**，所以「先登记契约、测试后补」会直接把 `parity` 门禁打红。下表的 `verifier` 列因此分两种：**可立即登记**（测试已在树里）与 **需先落测试**（给出建议名 + 要点，测试落地后才能写进 manifest）。

### 5.2 7 条

#### ① `44f1b0e867` #60003 realtime runtime auth — 判定 partial

| 字段 | 建议值 |
| --- | --- |
| `id` | `realtime-runtime-auth` |
| `kind` | `source-surface` |
| `rustPaths` | `codex-api/src/endpoint/realtime_websocket/methods.rs`、`core/src/realtime_conversation.rs`、`core/src/runtime_auth.rs` |
| `goPaths` | `realtime`、`appserver`、`runtimeauth` |
| `phase` | `P1` |
| `status` | `partial` |
| `verifier`（可立即登记） | `TestExistingCallSidebandPrefersPerCallEndpointLikeRust;TestRealtimeSidebandReconnectHelpersLikeRust` |
| `verifier`（目标，需先落测试） | `TestRealtimeHandshakeUsesFreshRuntimeAssertionLikeRust`、`TestRealtimeRuntimeAuthRejectsUnsupportedOpenAIOriginsLikeRust` |
| 测试要点 | 同一会话两次握手拿到两份断言；`ws://`、非默认端口、未知 openai 域被拒；非 OpenAI 目标不附加断言；sideband 重连重新解析；401 不回退 bearer |

#### ② `f4275a2a2e` #60073 remote-control runtime auth — 判定 partial

| 字段 | 建议值 |
| --- | --- |
| `id` | `remote-control-runtime-auth` |
| `kind` | `source-surface` |
| `rustPaths` | `app-server-transport/src/transport/remote_control/auth.rs`、`app-server-transport/src/transport/remote_control/mod.rs` |
| `goPaths` | `remotecontrol`、`runtimeauth` |
| `phase` | `P1` |
| `status` | `partial` |
| `verifier`（可立即登记） | `TestRemoteControlWebsocketLoopRetiresOnAuthOwnerChangeWhileConnected` |
| `verifier`（目标，需先落测试） | `TestRemoteControlRequestsUseRuntimeAuthLikeRust`、`TestRemoteControlRuntimeDenialSkipsCredentialRecoveryLikeRust` |
| 测试要点 | 两种 mode；默认 vs 自定义目标；吊销后不刷新直接失败；重连重新解析；enrollment 的静态 token 不受影响 |

#### ③ `cce9d3228b` #60101 Analytics runtime auth — 判定 partial

| 字段 | 建议值 |
| --- | --- |
| `id` | `analytics-runtime-auth` |
| `kind` | `source-surface` |
| `rustPaths` | `backend-client/src/analytics_session.rs`、`tui/src/analytics/client.rs` |
| `goPaths` | `tui/analytics`、`chatgptapi` |
| `phase` | `P1` |
| `status` | `partial` |
| `verifier`（可立即登记） | `TestCloudClientResolvesRuntimeAuthPerRequestLikeRust;TestCloudClientGetAccountAnalyticsRoutesReportsLikeRust` |
| `verifier`（目标，需先落测试） | `TestAnalyticsSessionUsesRuntimeAuthLikeRust`、`TestAnalyticsRuntimeAuthFailureKeepsSignInGuidanceLikeRust` |
| 测试要点 | 注册 401/刷新失败 → `Sign in again to load Analytics.`；注册 500 → `Couldn't authenticate…`；staging/regional 两个 base URL；跨域重定向被拒 |

#### ④ `efdacaf46f` #60096 terminal runtime denials — 判定 partial

| 字段 | 建议值 |
| --- | --- |
| `id` | `runtime-auth-terminal-denials` |
| `kind` | `source-surface` |
| `rustPaths` | `protocol/src/error.rs`、`model-provider/src/provider.rs`、`response-debug-context/src/lib.rs` |
| `goPaths` | `model`、`codexapi`、`runtimeauth` |
| `phase` | `P1` |
| `status` | `partial` |
| `verifier`（可立即登记） | `TestManagerCachesForbiddenAndRefreshesUnauthorizedLikeRust` |
| `verifier`（目标，需先落测试） | `TestTerminalRuntimeAuthDenialsStopRetryingLikeRust`、`TestRuntimeAuthDenialSkipsCredentialRecoveryLikeRust` |
| 测试要点 | body `error.code` 与 `x-error-json` 头两种载体；保留 HTTP 诊断；过期凭据仍恢复；两种 mode 各一条；403 与 401 两种状态码都要覆盖 |

#### ⑤ `10cf2b9ea2` #60014 managed runtime auth mode — 判定 partial

| 字段 | 建议值 |
| --- | --- |
| `id` | `managed-runtime-auth-mode` |
| `kind` | `source-surface` |
| `rustPaths` | `config/src/config_requirements.rs`、`core/src/config/managed_features.rs` |
| `goPaths` | `config`、`runtimeauth`、`features` |
| `phase` | `P1` |
| `status` | `partial` |
| `verifier`（可立即登记） | `TestModeFromConfigValuesLikeRust` |
| `verifier`（目标，需先落测试） | `TestManagedRuntimeAuthModeRequirementLikeRust`、`TestRuntimeAuthModeConflictRejectedLikeRust` |
| 测试要点 | requirements 表形态生效；与显式配置冲突报错；旧布尔写法仍工作；无托管时回落到用户配置；**表形态不得再被静默丢弃** |

#### ⑥ `0c58612c3c` #60035 git-attribution — 判定 not_applicable

| 字段 | 建议值 |
| --- | --- |
| `id` | （建议**不登记**）若必须登记：`git-attribution-world-state` |
| `kind` | `source-surface` |
| `rustPaths` | `ext/git-attribution/src/lib.rs`、`ext/git-attribution/src/policy.rs` |
| `goPaths` | （无自然落点；若登记只能填 `prompt`、`appserver`） |
| `phase` | `P2` |
| `status` | `missing` |
| `verifier` | **无现存测试可用** → 登记前置：先落 `TestGitAttributionWorldStateSectionLikeRust` |
| 建议 | 记 `not_applicable` 即可，不占契约位；理由：Go 无 extension 世界状态机制、无 user-settings 客户端，契约会长期停在 `missing` 且拖住 `certificationReady`（`TestAlignmentCertificationReadinessIsDerivedFromEvidence` 要求所有契约 complete） |

#### ⑦ `d4a341bd52` #60055 WS 复用前重新鉴权 — 判定 partial

| 字段 | 建议值 |
| --- | --- |
| `id` | `runtime-auth-websocket-revalidation` |
| `kind` | `source-surface` |
| `rustPaths` | `core/src/client.rs`、`ext/guardian-v2/src/async_scorer/sampler/connection_pool.rs` |
| `goPaths` | `model`、`appserver` |
| `phase` | `P1` |
| `status` | `partial` |
| `verifier`（可立即登记） | `TestWebsocketReauthTracksLastConnectedCredential;TestEnsureGuardianReviewerPrewarmsOnceAndFallsBackLazily` |
| `verifier`（目标，需先落测试） | `TestRuntimeWebsocketRevalidationReconnectsPerRequestLikeRust`、`TestGuardianRuntimeRequestsSkipPrewarmLikeRust` |
| 测试要点 | 同 turn 两请求两次握手；pause 后首个请求即失败、unpause 恢复；两种 mode；Guardian 403 上抛不降级；不支持 upgrade 仍回落 HTTP |

## 6. 顺带修的一处 `parity/**`（按你的口径）

`parity/domains.json`：把 `tui/analytics` 这个完整面写进域证据，避免后续同类提交被误判 `not_applicable`。**只改 evidence 文本，status/计数不动**（`domains` 仍 21 complete / 5 partial，`app-server-schema` 仍 partial）。

| 域 | 改动 |
| --- | --- |
| `account-plans`（owner `auth,appserver,tui`） | 追加一条：`tui/analytics` 35 个 `.go` 的实现面 + 每请求 runtime auth + 对应验证命令 `go test ./tui/analytics ./chatgptapi ./auth -count=1`；并注明“TUI 分析类提交不得按 not_applicable 处理” |
| `tui-remote-handlers`（owner `tui,appserver`） | 追加一条指向 `tui/analytics/**` 的说明，避免只从 `./tui` 顶层包判断覆盖面 |

> 注意 `account-plans` 原来只写 `go test ... ./tui ...`，**不包含** `./tui/analytics` 子包（Go 的 `./tui` 不递归），这是口径漏洞之一，已在证据里显式列出子包。

## 7. 结论

- #60035 记 `not_applicable`（能力面不存在，不是接线问题）；#60055 记 `partial`（复用/预暖面都在，缺 runtime 在场时的强制重连与错误分流）。
- 关于 Bearer：**主 WS 是一次性捕获，sideband 是“一次性捕获 + 重连复用同一字面量 + 401 不终局”**；三处都要改，只改一处无效。
- 7 条中 6 条建议登记契约，均先给「可立即登记的 verifier」以便门禁通过，目标 verifier 需与实现同批落地（因为 `TestContractVerifiersExistInGoTestFiles` 不允许悬空名字）。
- 本文件不改 Go 代码、不改 commits/baseline/contracts；`domains.json` 仅证据文本两处追加。
