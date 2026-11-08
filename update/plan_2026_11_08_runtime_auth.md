# 运行时鉴权 5 条逐条判定（`55897425e8..748ab55ca7` delta，附录 A「运行时鉴权」组）

- 作者：ledger-align（对齐账本 lane）
- 日期：2026-11-08
- 性质：**只读分析**。本文件不改任何 Go 代码，不改 `parity/**`；契约 id 与测试名仅为建议，登记由主干 lane 决定。
- Rust 基线：`/home/jacks/jacks_dev/codex`（`codex-rs`），HEAD `748ab55ca7`；五个提交均可在该检出内 `git show`。
- Go 基线：本 lane 完整镜像（`project_fetch --all`，6,954 个文件 / 约 72 MB）；下文所有「零命中」都是**在全树范围内**跑出来的，不是局部目录。
- 判定取值：`ported | aligned | not_applicable | partial`，每条只给一个。

## 0. 组内清单（附录 A 表内分类严格为「运行时鉴权」的 5 条）

| 附录 A 序号 | commit | PR | 主题 |
| --- | --- | --- | --- |
| 15 | `cce9d3228b` | #60101 | Use runtime authentication for Analytics requests |
| 16 | `efdacaf46f` | #60096 | Stop retrying terminal runtime authentication denials |
| 20 | `f4275a2a2e` | #60073 | Use runtime authentication for remote-control requests |
| 48 | `10cf2b9ea2` | #60014 | Honor managed runtime authentication modes and expand coverage |
| 56 | `44f1b0e867` | #60003 | Apply runtime authentication to realtime connections |

两条容易误入本组、但附录 A 表里归「其它」的相邻提交，**不计入本组**，单列在 §6.3 以免漏判：

- `0c58612c3c` #60035 Fix authentication for git-attribution settings requests（「其它」）
- `d4a341bd52` #60055 Reauthenticate runtime requests before reusing WebSocket access（「其它」）

## 1. 判定汇总

| commit | PR | 判定 | 一句话理由 |
| --- | --- | --- | --- |
| `44f1b0e867` | #60003 | **partial** | Go 的 realtime 面存在，但 WS 握手/WebRTC/sideband 仍用**一次性捕获的静态 Bearer**；`realtime/` 与 `appserver/realtime*.go` 对 `runtimeauth` 零引用 |
| `f4275a2a2e` | #60073 | **partial** | `remotecontrol` 用 `model.AuthHeaders`（快照静态头）+ `ApplyRequest` 签名；无 runtime 选择、无目标校验、无 denial 分类，`remotecontrol/` 对 `runtimeauth` 零引用 |
| `cce9d3228b` | #60101 | **partial** | Analytics 已接 runtime auth（早于本条，来自 Rust #59572），但缺本条新增的「typed runtime 错误 → Sign in again」映射；`isUnauthorizedAnalyticsError` 只认 HTTP 401 |
| `efdacaf46f` | #60096 | **partial** | 已能解析 `x-error-json` 的 `error.code`，但**只进 telemetry**；`recoverUnauthorizedAuth` 与 `isRetryableResponsesStreamError` 都不读它，四个 terminal denial code 全树零命中 |
| `10cf2b9ea2` | #60014 | **partial** | 用户配置里的 `features.runtime_agent_instances.mode` 已支持；但 `requirements.toml` 的 `FeatureRequirements` 是 `map[string]bool`，托管模式被**静默丢弃** |

组内统计（按本文件判定）：`partial 5`，`ported 0`，`aligned 0`，`not_applicable 0`。

> 说明为什么不是 `not_applicable`：这 5 条的**承载面在 Go 都存在**（realtime 包、remotecontrol 包、`tui/analytics`、`model` 重试、`config` requirements），且 Go 已有完整的 `runtimeauth` 原语包（`Manager`/`Provider`/`Auth`/`Mode`），缺的是接线与分类，不是缺能力面。

## 2. Go 现有 runtime-auth 基础设施（后续每条都引用它）

| 能力 | Go 位置 | 说明 |
| --- | --- | --- |
| 模式解析 `dual_header` / `assertion_only` | 【F:runtimeauth/runtimeauth.go†L20-L40】【F:runtimeauth/manager.go†L391-L406】 | 只从**用户配置** `runtime_agent_instances.mode` 读，默认 `dual_header` |
| 注册 + 断言签名 | 【F:runtimeauth/manager.go†L149-L200】 | `Auth` / `AuthWithSelection` / `AuthForSource`，源代次变更即失效 |
| 每请求头解析（含目标门控与环境校验） | 【F:runtimeauth/provider.go†L157-L205】 | `Provider.Headers` / `HeadersForDestination`；`HeadersForDestination` 内部 `IsOpenAIAPIURL` + `ValidateRuntimeEnvironment` |
| Harness 级 provider 工厂 | 【F:runtimeauth/config.go†L109-L130】 | `ProviderForHarness` 内部解析 installation id，再委托 `ProviderForConfig` |
| 特性开关 | 【F:runtimeauth/config.go†L13-L15】【F:features/features.go†L372】 | key `runtime_agent_instances`，`under_development` |
| installation id | 【F:install/installation_id.go†L28-L48】 | 带锁、UUID 校验、0644 收窄（与 Rust `resolve_installation_id` 等价） |
| 共享 manager（harness 内复用同一 runtime 身份） | 【F:runtimeauth/config.go†L22-L34】 | `SharedManager` 以 `codexHome|installationID` 为键 |
| 跨源重定向剥离敏感头 | 【F:runtimeauth/provider.go†L124-L150】 | **只有它自己的单测在调用**，见 §6.1 |

已接线面（`runtimeauth` 的真实调用点，全树 `rg "codex_go/runtimeauth"`）：

| 面 | 调用点 |
| --- | --- |
| 账号后端 / cloud tasks | 【F:appserver/runtime_router.go†L15262-L15285】、`chatgptapi.CloudClient` 每请求解析（【F:chatgptapi/cloud_tasks.go†L691-L703】） |
| TUI Analytics | 【F:tui/analytics/client.go†L195-L228】 |
| 云会话 / plan history | 【F:app/cloud.go†L260-L277】、`app/usage_plan_history.go:41` |
| 插件 catalog | 【F:app/plugin.go†L119-L172】 |
| MCP runtime 绑定 | 【F:appserver/turn_runtime.go†L12695-L12703】、`app/interactive.go:2211` |
| exec-server | 【F:exec/exec.go†L392-L587】 |

**未接线面（即本组 5 条的落点）**：`realtime/`、`remotecontrol/`、`model/` 的失败分类、`config/` 的托管模式。

## 3. 逐条判定

### 3.1 `44f1b0e867` #60003 — Apply runtime authentication to realtime connections

**判定：`partial`**

#### Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/codex-api/src/endpoint/realtime_websocket/methods.rs:883-912`、`:1105-1107` | 给 realtime WS 客户端加 `auth: Option<SharedAuthProvider>` 与 `with_auth`；**每次握手**（含 sideband 重试/重连）现算 `apply_auth_headers`，不再复用捕获头 |
| `codex-rs/core/src/client.rs:975-1000` | 新增 `realtime_sideband_auth`：把「选中凭据 + 请求作用域」编成 sideband 专用 auth provider |
| `codex-rs/core/src/runtime_auth.rs:56-69` | 新增 `is_openai_realtime_url`：`wss→https` 归一后校验目标；`ws:`/非默认端口/未知 openai 域直接报错（防把断言发给非受支持 origin） |
| `codex-rs/core/src/realtime_conversation.rs:1531-1560` | 启动时若「特性开 + OpenAI realtime 目标」，则 `validate_runtime_environment` → `manager.bind`（保存登录）/`bind_api_key`（env 或实验 bearer）→ `select_runtime_auth_for` → `bind_http_client_factory` → `websocket_auth` |
| `codex-rs/core/src/realtime_conversation/existing_call.rs:63-71` | existing-call sideband 复用同一 `SidebandAuth`，并把选中凭据的 http client 一起带上 |
| `codex-rs/model-provider/src/runtime_auth.rs:36-46` | `select_runtime_auth_for`：保留「捕获凭据的归属」的同时按请求头里的 selector 选作用域 |
| `codex-rs/cli/src/{main,marketplace_cmd,mcp_cmd,plugin_cmd}.rs` | 本地 marketplace / 纯 MCP / 指定 catalog 的 model 调试**跳过 runtime 初始化**（避免无谓注册） |

#### Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| realtime WS 头是**一次性构造的静态头** | 【F:appserver/realtime_runtime.go†L461-L470】：`headers := websocketProvider.Headers.Clone()` → `headers.Set("Authorization", "Bearer "+apiKey)`（+FedRAMP 变体）；同一份 map 放进 `TransportBackendConfig.Headers`（`:486`） |
| 握手只用这份静态头，**没有 per-handshake 钩子** | 【F:realtime/wire.go†L58】：`websocket.Dial(..., HTTPHeader: headers)`；重连循环在【F:realtime/transport.go†L398-L430】里复用同一 `backend`，没有重新解析凭据的入口 |
| 只有 WebRTC **建话单**有 per-call 钩子，且用的是 API-key 签名而非 runtime | 【F:realtime/transport.go†L280-L286】的 `PrepareCall`（钩子字段定义在 `:52`）；【F:appserver/realtime_runtime.go†L489-L491】传 `callAuth.Apply` |
| existing-call sideband「保留 runtime 头、不加 bearer」是**调用方自己塞头**，不是 runtime 解析 | `realtime/existing_call_test.go:65-140` 用 `CallHeaders: {"x-runtime-auth": "runtime"}` 断言透传 |
| `realtime/`、`appserver/realtime*.go` 完全没接 `runtimeauth` | `rg -n "codex_go/runtimeauth" realtime/ remotecontrol/` → **0 matches**；`rg -n "runtimeauth\|RuntimeAuth" appserver/realtime_runtime.go` → **0 matches**（`appserver/` 下仅此一个 realtime 生产文件） |
| 等价的实时目标校验不存在 | `rg -n "ValidateRuntimeEnvironment" -g '*.go' \| grep -v '^runtimeauth/'` → **0 matches**（该函数只在 `HeadersForDestination` 内部被调用） |

#### 缺什么 / 影响面 / 最小修复路径

- 缺：①每握手现算断言（含重连）；②`is_openai_realtime_url` 等价的目标门控（`wss:` 降级、非默认端口、未知 openai 域必须拒绝）；③「保存登录 vs env/实验 bearer vs 显式 key」三种凭据归属的绑定（Go 目前一律 bearer，无 `bind/bind_api_key` 区分）；④失败时**不回退 bearer**（Go 现在根本没有 runtime 分支，所以永远走 bearer）。
- 影响面：开启 `runtime_agent_instances` 后，realtime/voice 连接会以历史 bearer 身份出网，而 turn/cloud 已用 runtime 断言 → 同一会话两条身份链；实例 paused/revoked 时 realtime 侧不会 fail-closed，与 #60096 叠加会变成「被吊销后 realtime 仍在重连」。
- 最小修复路径（三处，均不碰业务逻辑）：
  1. `realtime`：在 `TransportBackendConfig` 增一个 `AuthForHandshake func(context.Context) (http.Header, error)`（与 `PrepareCall` 同形），在 `wire.go` 的 `Dial` 前与 `transport.go` 的重连分支里各调用一次；`nil` 时保持现状 → 向后兼容。
  2. `appserver/realtime_runtime.go`：`transportType == "websocket"` 且 `runtimeauth.FeatureEnabled` 且 `ClassifyOpenAIDestination(websocketProvider.BaseURL) == DestinationSupported` 时构造 provider（`ProviderForHarness` 或 `ProviderForConfig`+已解析 installation id），把 `HeadersForDestination` 包成 `AuthForHandshake`；`HeadersForDestination` 返回 nil（非 OpenAI 目标）时保持静态头。显式 `env_key`/`experimental_bearer_token` 的分支必须先短路（对齐 Rust 的 `bind_api_key` 语义）。
  3. 目标校验：把 Rust `is_openai_realtime_url` 的三条拒绝（`ws:`、非默认端口、未知 openai 域）移植为 `realtimeauth`/`appserver` 内部小函数，先单测后接线。
- 测试建议：`appserver/realtime_runtime_test.go` 增加「两次握手拿到两份不同断言」「`ws://api.openai.com` 被拒」「非 OpenAI 目标不附加断言」「sideband 重连重新解析」四个用例，形态照 `realtime/existing_call_test.go` 的 httptest 起法。

**建议契约**：`realtime-runtime-auth`（mkdir 位置建议 `parity/contracts/` 下与现有同构）。
**建议 verifier**：`TestRealtimeHandshakeUsesFreshRuntimeAssertionLikeRust`、`TestRealtimeRuntimeAuthRejectsUnsupportedOpenAIOriginsLikeRust`。

### 3.2 `f4275a2a2e` #60073 — Use runtime authentication for remote-control requests

**判定：`partial`**

#### Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/app-server-transport/src/transport/remote_control/auth.rs:22`、`:33-56` | `RemoteControlConnectionAuth` 增加 `runtime_auth: Option<(RuntimeAuthSlot, RuntimeAuthMode)>` 并在 `capture` 时快照；`ensure_current` 继续守账号切换 |
| `…/auth.rs:82-125` | `request_context`：有 runtime slot 时先 `validate_runtime_destination`，仅对受支持 OpenAI 目标解析；解析放进 `spawn_blocking`，避免把调用方栈撑大 |
| `…/auth.rs:126-145` | `request_headers`：按选中凭据构造头（runtime 断言 or 普通 auth） |
| `…/auth.rs:216-260` | `is_policy_denial` / `is_auth_error` / `request_error` / `recover_remote_control_auth`：把 runtime 错误与普通 401 恢复分开 |
| `…/remote_control/websocket.rs:264-269` | WS 连接持有 `auth_manager` + `auth_recovery` + `auth_change_rx`，重连时重新取头 |
| `…/remote_control/mod.rs:48`、`:69` | 传输层从 config 拿 `RuntimeAuthSlot` 与 `RuntimeAuthMode` 注入 |
| `codex-rs/app-server/src/lib.rs`（+3） | 装配处传入 slot |

#### Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| 连接鉴权 = 本地快照 → 静态头 | 【F:remotecontrol/auth.go†L32-L53】：`AuthUsesCodexBackend` + `model.AuthHeadersFromAuth(*snapshot)`（API key 直接 `ErrRemoteControlAPIKeyUnsupported`） |
| 每次请求只是重放这份静态头 + 签名 | 【F:remotecontrol/auth.go†L457-L476】`RequestHeaders`、`:480-L500` `ApplyRequest`（`AuthHeaders.ApplyRequest` 签名，`:487`） |
| 401 恢复走本地 `UnauthorizedRecovery`，与 runtime 无关 | `remotecontrol/auth.go` 的 `NewUnauthorizedRecoveryControllerForCodexHome`；`app/remote_control*.go` 只装配 loader |
| enrollment 的 bearer 是单独一个静态 token | `remotecontrol/enrollment.go:231` |
| 完全不接 runtimeauth | `rg -n "codex_go/runtimeauth" realtime/ remotecontrol/` → **0 matches**（全树唯一命中都在 `realtime/` 之外的面） |

#### 缺什么 / 影响面 / 最小修复路径

- 缺：①`RuntimeAuthSlot`+`Mode` 的捕获与随连接生命周期传递；②`request_context` 等价的「目标校验 + 仅 OpenAI 目标解析」；③runtime 错误与普通 401 恢复的分流（Go 目前所有 401 都进 `UnauthorizedRecovery`）；④WS 重连重新取头（Go 的 `websocket_connect.go` 走同一静态 loader 结果）。
- 影响面：remote-control 是「外部可控入口」，其请求身份与 turn 身份不一致时，服务端按 runtime 主体做的授权判定会与本地展示的账号不一致；实例 paused/revoked 时仍会触发本地凭据刷新循环（叠加 #60096 的同号问题）。
- 最小修复路径：
  1. `remotecontrol/auth.go`：给 `RemoteControlConnectionAuth` 加可选 `RuntimeAuth cloudRuntimeAuth` 形接口（可复用 `chatgptapi.CloudRuntimeAuth` 的 `HeadersForDestination(ctx, dest)` 形状，避免新造接口），`ApplyRequest` 在签名前先解析并覆盖断言头；`nil` 时完全保持现状。
  2. `NewRemoteControlAuthLoaderForCodexHome`：用 `runtimeauth.ProviderForHarness` 填入（目标 `runtimecontrol` 的 base URL）；`ClassifyOpenAIDestination != DestinationSupported` 时留空。
  3. 分流：`ApplyRequest`/`server_api.go` 的 401 分支在解析出 runtime 错误时跳过 `UnauthorizedRecovery`，直接上报终端错误。
- 测试建议：`remotecontrol/runtime_auth_like_rust_test.go`：两种 mode 各一条、`api.openai.com` vs 自定义域各一条、吊销后不刷新直接失败一条、重连重新解析一条（httptest + `websocket.Accept`）。

**建议契约**：`remote-control-runtime-auth`。
**建议 verifier**：`TestRemoteControlRequestsUseRuntimeAuthLikeRust`、`TestRemoteControlRuntimeDenialSkipsCredentialRecoveryLikeRust`。

### 3.3 `cce9d3228b` #60101 — Use runtime authentication for Analytics requests

**判定：`partial`**

#### Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/backend-client/src/analytics_session.rs:37-52` | `AnalyticsSession::from_config` 增加 `runtime_auth_mode: Option<RuntimeAuthMode>` 参数 |
| `…/analytics_session.rs:61-118` | `mode.filter(is_openai_api_url)`：命中则解析 installation id、建 `RuntimeAuthManager::for_codex`、用 `Client::from_auth_with_runtime_without_redirects` 建带 runtime 的客户端；未命中保持旧 `auth_provider_from_auth_manager` |
| `…/analytics_session.rs:88-121` | 把 `RuntimeAuthError::{RefreshFailed, RefreshTokenFailed, Registration(Rejected{401})}` 映射为 **"Sign in again to load Analytics."**，其余为 "Couldn't authenticate the Analytics request." |
| `codex-rs/tui/src/analytics/client.rs:74-80` | 传参：`features.enabled(RuntimeAgentInstances).then_some(config.runtime_auth_mode)` |
| `codex-rs/runtime-agent-instance/src/installation_id.rs:1-90` | 新增 `resolve_installation_id`：共享锁读、无效则占排他锁重读、写 UUID、0644 收窄 |
| `codex-rs/runtime-agent-instance/src/{binding,manager,lib}.rs` | `RuntimeAuthError` 增 `InvalidResourceSelection`/`Task`；`RuntimeAuthManager::for_codex` 收敛注册元数据 |

#### Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| Analytics 已接 runtime auth（**早于本条**，注释写的是 Rust #59572） | 【F:tui/analytics/client.go†L211-L228】：`runtimeauth.ProviderForHarness(codexHome, values, settings, baseURL, "codex-go")` |
| 客户端每请求解析、且禁重定向 | 【F:tui/analytics/client.go†L195-L210】`chatgptapi.NewCloudClientWithoutRedirects(..., RuntimeAuth: s.runtimeAuth())`；解析发生在【F:chatgptapi/cloud_tasks.go†L691-L703】（`HeadersForDestination`：目标门控 + 环境校验 + 每 attempt 现算） |
| 两种 mode 都被尊重 | `Provider.Mode` 来自 `ModeFromConfigValues`（【F:runtimeauth/manager.go†L391-L406】），`Auth.Headers(mode)` 分支发送（`runtimeauth/runtimeauth_test.go:199` 的 `TestRuntimeAuthModesAttachRustHeaders`） |
| **签名/归属不完全等价**：`ProviderForHarness` 每次都新解 installation id 并走 `SharedManager` | 【F:runtimeauth/config.go†L109-L130】；Rust 是显式 `runtime_auth_mode` 参数 + 独立 manager |
| **缺**：runtime 错误 → 登录话术的映射 | 【F:tui/analytics/client.go†L268-L271】`isUnauthorizedAnalyticsError` 只 `errors.As(*chatgptapi.HTTPStatusError)` 判 401；`analyticsRequestErrorMessage`（`:275-L288`）同样只按 HTTP 状态分类。runtime 注册/刷新失败是普通 error → 落到 `analyticsPlanLoadMessage`/`analyticsReportFailedMessage` |
| 已有对应文案常量（说明这条差的是**接线**不是文案） | 【F:tui/analytics/client.go†L24-L29】`analyticsSignInAgainMessage = "Sign in again to load Analytics."`（当前只在 plan 发现阶段被 HTTP 401 触发，`:117-L122`） |

#### 缺什么 / 影响面 / 最小修复路径

- 缺（相对本条而言，仅 1 项实质）：`RuntimeAuthError` 语义 → 文案的分级。Rust 区分「刷新失败/永久刷新失败/注册 401」→ Sign in again，其它 → Couldn't authenticate；Go 把这两种都归到「load 失败/请求失败」。
- 影响面：runtime 过期时用户看到的是「加载失败」而不是「重新登录」，与 #60096 的「永久刷新失败不该变成可重试错误」是同一根因的两面（Go 侧尚未有 runtime 错误类型）。
- 最小修复路径：
  1. `runtimeauth` 导出可判定的错误族（`ErrRefreshFailed`、`ErrRefreshTokenFailed`、`ErrRegistrationRejected` 带 status），或在 `Provider.Headers*` 返回值里保留 `*runtimeauth.RegistrationError`（现有 `registrationErrorEnvelope` 已在，只需包装导出）。
  2. `tui/analytics/client.go`：`isUnauthorizedAnalyticsError` 扩成 `isAnalyticsSignInError(err)`＝HTTP 401 **或** `errors.As` 到上述 runtime 错误；`analyticsRequestErrorMessage` 同源复用。
  3. 保持 `ProviderForHarness` 现状（installation id 已在内部解析，等价于 Rust 的共享解析）。
- 测试建议：`tui/analytics/runtime_auth_like_rust_test.go`（Rust 同名文件 `tui/src/analytics/runtime_auth_tests.rs` 239 行）：注册 401 → "Sign in again to load Analytics."；刷新失败 → 同；注册 500 → Couldn't authenticate；staging/regional 两个 base URL 各一条；跨域重定向被拒一条。

**建议契约**：`analytics-runtime-auth`。
**建议 verifier**：`TestAnalyticsSessionUsesRuntimeAuthLikeRust`、`TestAnalyticsRuntimeAuthFailureKeepsSignInGuidanceLikeRust`。

### 3.4 `efdacaf46f` #60096 — Stop retrying terminal runtime authentication denials

**判定：`partial`**

#### Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/protocol/src/error.rs:643-672` | 新增 `is_runtime_auth_denial(status, body, identity_error_code)`：401/403 且 code ∈ {`runtime_assertion_invalid`,`runtime_instance_paused`,`runtime_instance_revoked`,`invalid_agent_instance_assertion`}（body `/error/code` 或 identity 头） |
| `codex-rs/protocol/src/error.rs:430-443` | `retry_delay` 对 denial 直接返回 `None`（不消耗重试预算） |
| `codex-rs/model-provider/src/provider.rs:532-546` | `is_recoverable_auth_error`：401 且非 runtime denial 才允许恢复（runtime 未启用时保持旧行为） |
| `codex-rs/backend-client/src/client.rs:80-95`、`:445-455` | backend 请求同样跳过恢复；header 侧提取 `auth_error_code` |
| `codex-rs/response-debug-context/src/lib.rs:16`、`:40-L55` | `ResponseDebugContext.auth_error_code` 从 `x-error-json`（base64）解出 `error.code` |
| `codex-rs/model-provider/src/{runtime_auth,request_setup}.rs` | 永久刷新失败以 `RefreshTokenFailed` 上抛，不再变成可重试的流错误 |
| `codex-rs/{core,backend-client}/tests/**`（`runtime_auth_denials.rs` 215 行等） | 终局测试：denial 不重试、不刷新；过期凭据仍恢复 |

#### Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **解析已存在**（`x-error-json` → base64 → `error.code`） | 【F:model/responses_agent.go†L4762-L4780】`responseAuthorizationErrorCode`；使用点 `:1143`、`:1469`、`:3162` |
| 但只用于 telemetry，不参与判定 | 【F:model/responses_agent.go†L4945】写入 `debug.AuthErrorCode` → `telemetry/api_request.go:75`/`:201` 作为 `auth.error_code` 字段 |
| 401 恢复不看 code | 【F:model/auth_recovery.go†L52-L60】签名收 `debug authRecoveryDebug`，但函数体内**从不读** `debug.AuthErrorCode`，直接跑 provider 恢复 + plan 步骤 |
| 重试谓词不看 code | 【F:model/responses_stream.go†L576-L630】按 `codexapi.ErrorKind` 分类，无 denial 分支；HTTP 层 401 判定见 `model/responses_agent.go:4810`（`401 || 429||>=500` → 可重试） |
| 四个 denial code **全树零命中**（除测试夹具） | `rg -n 'runtime_assertion_invalid\|runtime_instance_paused\|runtime_instance_revoked\|invalid_agent_instance_assertion' -g '*.go'` → 仅 `runtimeauth/runtimeauth_test.go:345`、`:381` 两处**测试里伪造的响应体**，生产代码 0 |
| identity 错误码字段零命中 | `rg -n 'identity_error_code\|IdentityErrorCode' -g '*.go'` → **0 matches** |
| 注册期 403 已被缓存（这条**部分**已具备） | 【F:runtimeauth/manager.go†L253】、`:293` 的 `deniedRuntime` 缓存；`runtimeauth/runtimeauth_test.go:336` `TestManagerCachesForbiddenAndRefreshesUnauthorizedLikeRust` 已锁 403 缓存 + 401 刷新一次 |

#### 缺什么 / 影响面 / 最小修复路径

- 缺：**请求期**（非注册期）denial 的终端判定。精确说：
  - `401` 在 Go 里**一定走恢复 + 重试**：恢复由 `model/responses_agent.go:1138`、`:1464`、`:3157` 三处 `StatusCode == 401` 触发（调用 `model/auth_recovery.go:52`），请求层保留重试见 `responses_agent.go:4810`。所以「401 带 `runtime_instance_revoked`」在 Go 会白跑一轮刷新再重试——正是 Rust 本条要禁掉的行为。
  - `403` 目前既不在重试谓词（`:4810` 只列 401/429/5xx）里、也不触发恢复，**事实上已终局**；但这属于「碰巧对」，Go 没有按 code 判定的能力，一旦载体变成 401（Rust 明确对 401 与 403 两种都判）就会退化。
  - 永久刷新失败没有独立的可判定错误上抛（`auth.IsPermanentRefreshFailure` 存在，但没接到「不可重试」的出口）。
- 影响面：实例被 revoke/pause 后，每次请求要白跑一轮「provider 恢复 + token 刷新」并消耗重试预算；断言永久无效会表现成抖动而非明确失败；遥测里其实已经有 `auth.error_code`，说明**证据链断在判定这一步**。
- 最小修复路径（成本最低的一条，几乎纯加分支）：
  1. `codexapi`（或 `model`）加 `IsTerminalRuntimeAuthDenial(status int, body []byte, authErrorCode string) bool`，四个 code 常量与 Rust 逐字一致。
  2. `model/auth_recovery.go:52`：入口处若 denial → 直接返回 `errNoAuthRecoveryStep`（现有「无恢复步骤」语义）并记录 `authRecoveryOutcomeNotRun`，reason 用 `runtime_auth_denial`。
  3. `model/responses_stream.go:576` + `responses_agent.go:4810`：denial 一律返回不可重试。
  4. `auth` 包：把 `RefreshTokenFailed` 类永久失败导出为可判定错误（现有 `auth.IsPermanentRefreshFailure` 已存在，需确保它不被包装成可重试的流错误）。
- 测试建议：`model/runtime_auth_denials_like_rust_test.go`：body 版与 header 版各 1 条、保留 HTTP 诊断信息 1 条、过期凭据仍恢复 1 条、两种 mode 各 1 条。

**建议契约**：`runtime-auth-terminal-denials`。
**建议 verifier**：`TestTerminalRuntimeAuthDenialsStopRetryingLikeRust`、`TestRuntimeAuthDenialSkipsCredentialRecoveryLikeRust`。

### 3.5 `10cf2b9ea2` #60014 — Honor managed runtime authentication modes and expand coverage

**判定：`partial`**

#### Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/config/src/config_requirements.rs:956-998` | `FeatureRequirementsToml` 从「flat bool map」改成 `serde(from = FeatureRequirementsInput)`：允许 `runtime_agent_instances = { mode = … }`，同时保留旧布尔写法（`enabled` 折叠回 `entries`）；`is_empty` 把 mode 也算上 |
| `codex-rs/core/src/config/managed_features.rs:364-400` | `resolve_runtime_auth_mode`：无显式配置时用托管要求，显式配置与之冲突时**报错**，并保持旧布尔语义 |
| `codex-rs/core/src/config/mod.rs:3622-3626`、`:4090` | 解析顺序抽到 managed 层，配置侧不再自行推导 |
| `codex-rs/core/src/runtime_auth.rs`（+20） | 注册元数据/模式随托管值走 |
| `codex-rs/core/README.md`（+30） | 补实验性 runtime auth 的配置、凭据生命周期、bootstrap 说明 |
| `codex-rs/rmcp-client/**`、`app-server/tests/suite/v2/{rate_limits,realtime_conversation}.rs` | 覆盖面：MCP/realtime 重连现算断言、realtime 目标与环境限制 |

#### Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| 用户配置侧**已支持** `features.runtime_agent_instances.mode` | 【F:runtimeauth/manager.go†L391-L406】`ModeFromConfigValues` 读 `values["runtime_agent_instances"].(map)["mode"]`；`runtimeauth/generation_test.go:62-66` 锁两值 |
| 托管侧**只接受布尔** | 【F:config/api.go†L613】`FeatureRequirements map[string]bool`；解析在【F:config/requirements_file.go†L567-L569】走 `boolMapAnyKey` |
| 非布尔值被**静默丢弃**（不是报错） | 【F:config/requirements_file.go†L1518-L1534】：`for key, raw := range nested { if v, ok := boolAnyValue(raw); ok { out[key] = v } }` —— 表值既不进 map 也不报错 |
| 无托管→运行时的推导，也无冲突拒绝 | `rg -n 'runtime_auth_mode' -g '*.go'` → **0 matches**（Go 全树没有这个派生字段）；`Features`/`FeatureRequirements` 与 `runtimeauth` 之间无调用关系（`runtimeauth` 只吃 `FeatureSettings map[string]bool`，见【F:runtimeauth/config.go†L36-L45】） |

#### 缺什么 / 影响面 / 最小修复路径

- 缺：①`requirements.toml` 里 `features.runtime_agent_instances = { mode = "assertion_only" }` 的解析；②「无显式配置时用托管值」的合并；③「显式与托管冲突要报错」的校验。
- 影响面**比前几条更严重**：企业 MDM 下发的强制 `assertion_only` 会被静默忽略，客户端按默认 `dual_header` 出网 —— 不是功能缺失，是**策略未被执行且无告警**。
- 最小修复路径：
  1. `config`：`ConfigRequirements` 增 `RuntimeAuthMode *runtimeauth.Mode`（或 `*string` 以避免 config→runtimeauth 依赖倒置，由 `runtimeauth` 侧转换）；`requirements_file.go` 在 `features` 分支里先取 `runtime_agent_instances` 的表形态，取出 `mode` 后仍把 `enabled` 折叠进 `FeatureRequirements`（与 Rust `From<FeatureRequirementsInput>` 同构）。
  2. `runtimeauth.ModeFromConfigValues` 增加变体 `ModeFromConfigAndRequirements(values, required)`：显式与 required 冲突 → 返回 error（由 config 加载期上抛）。
  3. 校验已知字段：`config/config.go:1567` 定义的 `validateKnownFeatureFields`（调用点在 `:1345`）需接受 `runtime_agent_instances.mode`，否则表形态会在校验期被拒（现在被静默丢弃，两者都要避免）。
- 测试建议：`config/runtime_auth_requirements_like_rust_test.go`：托管 mode 生效 1 条、显式冲突报错 1 条、旧布尔写法仍工作 1 条、无托管时回到用户配置 1 条。

**建议契约**：`managed-runtime-auth-mode`。
**建议 verifier**：`TestManagedRuntimeAuthModeRequirementLikeRust`、`TestRuntimeAuthModeConflictRejectedLikeRust`。

## 4. 复现命令（本文件所有「零命中」的原始命令与输出）

在完整镜像根目录执行（`rg` 版本无关，`--no-filename` 未用于计数处）：

```
$ rg -n "codex_go/runtimeauth" realtime/ remotecontrol/
  (0 matches)

$ rg -n 'runtime_assertion_invalid|runtime_instance_paused|runtime_instance_revoked|invalid_agent_instance_assertion' -g '*.go'
  runtimeauth/runtimeauth_test.go:345:			return http.StatusForbidden, `{"error":{"code":"runtime_instance_revoked"}}`
  runtimeauth/runtimeauth_test.go:381:		return http.StatusForbidden, `{"error":{"code":"runtime_instance_revoked"}}`
  [total lines: 2]      # 均为测试夹具；生产代码 0

$ rg -n 'identity_error_code|IdentityErrorCode' -g '*.go'
  (0 matches)

$ rg -n 'runtime_auth_mode' -g '*.go'
  (0 matches)

$ rg -n 'ValidateRuntimeEnvironment' -g '*.go' | grep -v '^runtimeauth/'
  (0 matches)

$ rg -n 'ProtectSensitiveHeaders' -g '*.go' | grep -v '_test.go' | grep -v '^runtimeauth/provider.go'
  (0 matches)

$ rg -n "runtimeauth|RuntimeAuth" appserver/realtime_runtime.go
  (0 matches)
```

反证面（说明镜像完整、不是「文件没取回」导致假零命中）：

```
$ rg -n "codex_go/runtimeauth" -g '*.go' | wc -l     # 7 个真实调用点（见 §2 表）
  7
$ ls realtime/*.go remotecontrol/*.go | wc -l        # 两个包均已完整取回
```

## 5. 五条之间的依赖关系（给排期用）

```
#60014 (托管 mode 解析)
   └─ 决定 #60003 / #60073 运行时“用哪个 mode”
#60096 (denial 分类 + 错误族)
   ├─ #60003 realtime 失败要 fail-closed，而不是回退 bearer
   ├─ #60073 remote-control 401 要分流，不进本地恢复
   └─ #60101 Analytics 要把 runtime 错误映射成登录话术
#60003 / #60073 都是“接线型”，共用 §2 的 Provider/HeadersForDestination
```

落地顺序建议：**#60014 → #60096 → #60101 → #60073 → #60003**
理由：#60014 是策略入口（且是静默忽略，风险最高）；#60096 提供错误族，后三条都要用；#60101 改动最小（`tui/analytics` 单文件 + 一个 error 判定）；#60073 与 #60003 各自独立但要复用同一解析路径，先 remote-control（无媒体流，测试便宜）后 realtime（要起 WS/WebRTC 假服务）。

## 6. 顺带发现（不属于这 5 条，但影响账本口径）

### 6.1 `runtimeauth.ProtectSensitiveHeaders` 是死代码

`runtimeauth/provider.go:128` 定义了「跨源重定向剥离 `Authorization`/断言头」的 redirect policy，但全树**只有它的单测**在调用（`runtimeauth/provider_test.go:83`）。生产面目前靠 `chatgptapi.NewCloudClientWithoutRedirects` 的「一律不跟随」规避，其它 HTTP 路径（若将来接 runtime）没有这层保护。**若要落 #60003/#60073，必须同时决定用哪种机制**，否则又是一个「看起来有保护、实际没接」。

### 6.2 Go 的 Analytics 覆盖面比台账口径更靠前

`tui/analytics` 目录（35 个 `.go` 文件，含 dashboard/plot/summary/task_panel 等）完整存在，且 `client.go` 已带 runtime auth。这与「Go 的 TUI 是精简面」的旧描述不符。**建议账本后续在 `tui` 域的证据里显式写 `tui/analytics/**` 的存在**，否则同类提交会被误判为 `not_applicable`。本条不擅自改 `parity/**`。

### 6.3 附录 A 里两条相邻提交的归属

- `0c58612c3c` #60035（git-attribution 请求鉴权）：表里归「其它」。按主题它属于运行时鉴权覆盖面，建议下一轮把它并入本组复评，否则会漏。
- `d4a341bd52` #60055（复用 WS 通道前先重新鉴权）：同样是 realtime 鉴权语义，建议与 #60003 同批处理（它约束的正是「重连/复用要不要重新解析凭据」）。

### 6.4 附注：`go.sum` glamour h1 复核（一句话留痕）

`charm.land/glamour/v2 v2.0.1` 的 `h1:` 已由本 lane 从 goproxy.cn 模块 zip（139,064 字节、239 文件）按 `x/mod/sumdb/dirhash.Hash1` 独立复算为 `xl+r00A4aJWU0z8fgwKd9fQQ4rsphqGUzuEiXZP5n+c=`，与 sum.golang.org 一致；仓库 `go.mod` 行 `h1:3rgLDj8…` 与 sumdb 的 `jo9z8XqV…` 的差异属**单文件 Hash1 条目命名约定**，不是内容差异，不得「修正」。本 lane 树内无 `go.sum`，不做改动。

## 7. 结论

- 5 条全部 `partial`，无一条可以记 `complete`：Go 有承载面、有 `runtimeauth` 原语，但 5 条各自的关键判定逻辑（每握手解析、目标门控、denial 分类、托管 mode、错误分级）都缺。
- 其中 **#60014 是唯一「静默忽略企业策略」的缺陷**，按风险应最先修；**#60096 成本最低**（解析与遥测都已存在，只差判定分支）。
- 本文件不修改 `parity/**`、不改 Go 代码；契约 id 与 verifier 名仅为建议，登记请走主干 lane。
