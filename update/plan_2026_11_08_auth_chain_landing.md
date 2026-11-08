# 鉴权链 4 条：Rust 形状核实 + 落地规格（不含 Go 改动）

- 作者：**ledger-align** · 2026-11-08
- 结论先行：**这轮我没有改 Go 业务代码**，两个原因都是硬事实，不是推托（§0）。本文件把你在派单里要求的"每条要核对的 Rust 形状"全部核实到行，并给出可直接转交 owning lane 的落地规格、verifier 规格与转 `complete` 的最小条件。
- Rust oracle 全长检出：`/home/jacks/jacks_dev/codex`，HEAD `748ab55ca7de0b647d9a7a9d9a8e70e00f28666d`；引用一律 `git show 748ab55ca7:<path>` 行号。

---

## 0. 为什么这一轮不能由我落代码

### 理由 A（职责边界，写在我的项目指令里）

我的 lane 职责是 `parity/{commits,baseline,domains}.json`、`parity/alignment_control_test.go`、`parity/contracts/manifest.json`（你登记）、`README.md`、`need_aligin.md`、`update/**`，并且明确写着 **"不要改动 Go 业务代码（mcp/model/turn/appserver 等），那是别的 lane"**。本次 4 条要动的正是 `appserver/realtime_runtime.go`、`realtime/*`、`runtimeauth/*`、`remotecontrol/*`、`model/responses_agent.go` —— 全部落在被禁止的那一侧。

上一轮我能给的极限就是"可开工的实现规格"，这一轮我把规格做得更细（§1–§3），但落地仍应走 owning lane，或者你给我一个明确的 lane 改派决定（那属于项目级授权，不是我在对话里能自行解除的约束）。

### 理由 B（技术上：我拿不到你说的那份基线）

你的派单前提是"开工前先 `project_fetch` 取最新字节"。但我的 `project_*` 工具作用域是 `project-krozjtbfkzjz`，而这条 A2A 通道的 `project_id` 是 `project-mklqpzt598ws` —— **不是同一个项目**。实测：

```
project_read parity/contracts/manifest.json
→ size 39834，81 contracts，targetRustCommit 55897425e839d943a744c658701445ff732568a0
```

也就是说我这边能读到的 `parity/contracts/manifest.json` 仍是 **81 条 / 锚点 `55897425e8`**，与你的"136 契约 / 100 complete / 36 partial"差着十几轮。同理：

```
grep -c finalAnswerModeration appserver/runtime_router.go   → 0
sha256 appserver/runtime_router.go = 21c6d69b85582460ab0901b61a45a6fc436679b08f67a63590353d20138aba72
```

你说 mac lane 已并入"`runtime_router.go`（5 行 finalAnswerModeration 字段）"，而我手里这份 `runtime_router.go` 一个 `finalAnswerModeration` 都没有。**所以我对这 4 条能用的最新字节就是 `748ab55ca7` 时期的那一份**（我在 `../gate_tmp` 里的校验副本），它与你的主干在 `runtime_router.go`、`turn_runtime.go`、`protocol.go`、`mcp/**`、`state/**`、`session/**`、`features/features.go` 上都已经分叉。

在这种前提下产出 `PATCHES/*.diff` 的后果是可预期的：补丁要么 hunk 失败，要么"成功"地把别的 lane 刚并进去的改动**改回去**（最坏情况是 `finalAnswerModeration` 那 5 行被静默删除，而且因为没有 diff 基线可核对，你合并时不会看到）。这条我不能冒。

> 若你要我改变结论，需要同时给两样：① 明确把 4 条的实现派到我的 lane（或由用户侧解除该约束）；② 用 zip 把你当前主干的这几个文件（或至少 `runtime_router.go`/`turn_runtime.go`/`responses_agent.go` 的 LF 版本）发我。两样齐了我可以按你们的 patch 形状出 `PATCHES/*.diff`，并且我会先跑 `git apply --check` 级别的校验再交付。

### 附带发现（你派单里点名要我 fetch 的包）

`websocketauth/` 与本轮 4 条**无关**：它是**入向** app-server/exec-server WebSocket 升级鉴权（`websocketauth/websocket_auth.go:3-8` 的注释："Shared authentication for incoming app-server and exec-server WebSocket upgrades"，Rust `codex-websocket-auth` #47447），而 4 条全部是**出向**连接的凭据解析。不要把它当成 #60055 的落点。

---

## 1. Rust 形状核实（逐条回答你点名要确认的问题）

### 1.1 `realtime-runtime-auth`（#60003）

**问：per-connect resolve 还是 per-start resolve？**
**答：选择（selection）在 start 一次，凭据物化（签名）在每次握手一次。**这两级必须分开实现，否则会做出"每次握手重新注册 runtime"或"整条连接复用同一份断言"两种错版。

| 级别 | 位置 | 事实 |
|---|---|---|
| per-start 选择 | `codex-rs/core/src/realtime_conversation.rs:1534-1563` | feature 开 + `is_openai_realtime_url(base_url)` → `validate_runtime_environment` → `manager.bind(...)`/`manager.bind_api_key(...)` → `select_runtime_auth_for(...)` → `validate_runtime_selectors(...)` → `websocket_auth = Some(selected.into_auth_provider(*mode))` |
| per-connect 物化 | `codex-rs/runtime-agent-instance/src/auth.rs:100-145` | `into_auth_provider` 的注释原文："Use this selection for a request and its retries; select auth again for the next request."；`RuntimeRequestAuth::auth_headers(mode)` 里 **每次调用都执行** `self.runtime().assertion()`（重新签名），再拼 header |
| 应用点 | `codex-rs/codex-api/src/endpoint/realtime_websocket/methods.rs:910-912`、`:1105-1107` | `with_auth(SharedAuthProvider)` 的注释："Resolve request auth on every handshake, including sideband retries and reconnects"；`*request.headers_mut() = auth.apply_auth_headers(headers).await?` |
| 显式凭据例外 | `realtime_conversation.rs:1542-1549` | `provider.env_key.is_some() \|\| provider.experimental_bearer_token.is_some()` 时走 `bind_api_key`（不透传 saved login runtime），"Saved and command-provided keys keep their existing credential lifecycle" |
| 未初始化 | `realtime_conversation.rs:1426-1430` | feature 开但 slot 空 → `CodexErr::InvalidRequest("runtime authentication is not initialized")`，**硬报错**，不退化成静态 key |

**问：失败时的错误类型？**
`RuntimeAuthError → codex_api::AuthError` 的映射表（`runtime-agent-instance/src/auth.rs:169-199`）：

| RuntimeAuthError 变体 | 映射 |
|---|---|
| `Policy(denied)` / `Registration(Policy(denied))` | **`AuthError::Policy(*denied)`**（提前 return，不走 transient 判断） |
| `Registration(Transport)` | `AuthError::Transient` |
| `Registration(Rejected{status})` 且 `status.is_server_error()` 或 `429` | `AuthError::Transient` |
| `Unavailable` / `Unsupported` / `UnsupportedResourceSelection` / `InvalidResourceSelection` / `Changed` / `RefreshFailed` / `RefreshTokenFailed` / `Task` / `KeyGeneration` / `Registration(InvalidEndpoint\|InvalidRequest\|InvalidResponse)` | `AuthError::Build`（终态） |
| 断言签名失败 | `AuthError::Build("failed to sign runtime assertion")` |
| dual 模式取 token 失败 | `AuthError::Build("runtime actor credential is unavailable")` |

**问：runtime 不符时的 denial 分类？**
`AuthError::Policy(denied)` 是唯一被单独分出来的类别；它在上层被当作**策略拒绝**（不可重试、不是凭据刷新能修的），与 `Transient`（可重试）和 `Build`（终态但非策略）三条互斥。Go 侧对应物应是"终态 + sign-in/策略"语义，而不是网络错误。

**header 形状（Go 必须逐字对齐，`auth.rs:113-145`）**
- `dual_header`：`Authorization: Bearer <actor token>` + `x-openai-agent-assertion: <assertion>`
- `assertion_only`：`Authorization: AgentAssertion <assertion>`（**不**带 `x-openai-agent-assertion`）
- 另加：ChatGPT actor → `ChatGPT-Account-ID`；FedRAMP 账号 → `x-openai-fedramp: true`
- 合并语义：`replace_auth_headers`（`auth.rs:203-213`）先**删除**这 4 个 header 名再 extend —— 是替换不是叠加。Go 现在 `mergeHTTPHeaders(headers, commonHeaders)` 是叠加，改造时必须"先 Del 这 4 个再 Set"。

### 1.2 `runtime-auth-websocket-revalidation`（#60055）

**问：复用已有连接前是否重新鉴权？**
**答：是，且判定依据是"凭据来源版本"而不是 token 字符串。**

| 位置 | 事实 |
|---|---|
| `codex-rs/core/src/client.rs:314-320` | 类型注释原文："The session establishes a Responses WebSocket connection lazily and **reuses it across multiple requests within the turn**. Runtime-authenticated requests **reconnect between requests to revalidate the registered credential source**." |
| `client.rs:369` | `last_connected_auth_revision: Option<Option<u64>>`（外层 None = 从未握手成功；内层 None = provider 无 managed auth）。**跨 reset 保留**，用于和上一个 socket 比较 |
| `client.rs:1870-1914` | 复用判定：`reset_reason`（协议/header/connection_key/runtime_id/routing affinity 变化）或 `owner_changed`（`auth_owner != auth_owner` 或 `!owner.is_current()`）→ `reset()` + 重连；`reauth = last_connected_auth_revision.is_some_and(|prev| prev != auth_revision)`；握手后写回 `last_connected_auth_revision = Some(auth_revision)` |
| `ext/guardian-v2/src/async_scorer/sampler/connection_pool.rs:350/398-419/603-617` | guardian 采样池用 `auth_changes: Option<watch::Receiver<AuthChangeState>>` 触发重建，逻辑等价 |
| `app-server-transport/.../remote_control/auth.rs:56-67` | remotecontrol 侧对应物：`ensure_current()`（owner 校验）+ `auth_change_receiver()`（watch） |

**Go 侧现状对照**：`model/responses_agent.go:1129 session.markConnectedAuth(r.AuthSnapshot)` 是"比 snapshot 指针"，语义弱于 revision；`realtime` 侧完全没有这一层（`realtime/wire.go:40` 只克隆快照）。

### 1.3 `remote-control-runtime-auth`（#60073）

**问：runtime 选择 / 目标校验 / denial 分类三件事的 Rust 形状？**
都在 `codex-rs/app-server-transport/src/transport/remote_control/auth.rs`，**每请求**做一次：

```rust
// :82-105  RemoteControlConnectionAuth::request_context(&self, endpoint)
if self.runtime_auth.is_some() {
    codex_model_provider::validate_runtime_destination(Some(endpoint)).map_err(io::Error::other)?;  // ① 目标校验（配置了 runtime 才校验）
}
let runtime = self.runtime_auth.as_ref()
    .filter(|_| codex_runtime_agent_instance::is_openai_api_url(endpoint))                      // ② 只对 OpenAI API 目标注入
    .map(|(slot, mode)| slot.get().cloned()                                                      // ③ 每次取当前 slot（轮换即生效）
        .map(|manager| (manager, *mode))
        .ok_or_else(|| io::Error::new(ErrorKind::WouldBlock, "runtime authentication is not initialized")))?;
let resolved = codex_model_provider::auth_provider_for_request(&self.auth, runtime.as_ref(), &HeaderMap::new()).await
    .map_err(|error| auth_error(error.into()))?;                                                 // ④ 解析失败 → 分类
```

- **分类表**（`:216-242`）：`is_policy_denial(err)`（cause 是 `NetworkPolicyDenied`）、`is_auth_error(err)`（cause 是 `AuthError`）；`auth_error()` 把 `AuthError::Policy(denied)` → `ErrorKind::PermissionDenied`，其它 → `io::Error::other`；`request_error()` 把 `HttpError::Policy` → `PermissionDenied`。
- 结构上另有 `RemoteControlAuth::capture(manager, runtime_auth)`（`:33-52`：`ConnectionAuth::capture` + `authenticated = uses_codex_backend && account_id.is_some()`，owner 不是 current 就重试）、`unauthorized_recovery()`（`:60-65`）、`RECOVERY` 之后 `mark_recovery_auth_change_seen`（`:282`）。
- 测试锚点：`auth.rs:330 runtime_requests_reject_unsupported_openai_origins`、`:363 runtime_request_rejects_a_different_registered_account` —— 这两条就是"目标校验"和"身份不匹配"的 ground truth。

### 1.4 `runtime-auth-terminal-denials`（#60096）

**问：把四个 terminal denial code 接进 `recoverUnauthorizedAuth` 与 `isRetryableResponsesStreamError`。**
**答：Rust 的判定入口不是"散在 401 分支里"，而是 provider 层的单一谓词 + 协议层的 `retry_delay`。**这是我要更正上一轮 §2 的地方（上轮我只说"3 处 401 分支"，不够准确）。

| 层 | 位置 | 事实 |
|---|---|---|
| 谓词定义 | `codex-rs/protocol/src/error.rs:643-670` | `is_runtime_auth_denial(status, body, identity_error_code)`：仅 `401\|403`；`is_terminal(code) = runtime_assertion_invalid \| runtime_instance_paused \| runtime_instance_revoked \| invalid_agent_instance_assertion`；先看 header 侧 `identity_error_code`，再看 body `error.code` |
| 可恢复性判定（关键） | `codex-rs/model-provider/src/provider.rs:533-546` `ConfiguredModelProvider::is_recoverable_auth_error` | `status == 401 && (runtime_auth.is_none() \|\| !is_runtime_auth_denial(401, body, auth_error_code))` —— **runtime 开 + 命中终态码 → 不可恢复**（不跑 unauthorized recovery） |
| 重试延迟 | `codex-rs/protocol/src/error.rs:429-441` | `CodexErrorDetails::UnexpectedStatus(error) if is_runtime_auth_denial(...) => None`（终态，不给 retry delay） |
| code 来源 | `codex-rs/codex-api/src/api_bridge.rs:348-359` `extract_x_error_json_code` | base64 `x-error-json` → `/error/code`（与 Go `responseAuthorizationErrorCode` 逐字相同） |
| 用例 | `codex-rs/core/tests/suite/runtime_auth_denials.rs:35-36/88-113/129-140` | `CASES=["revoked","header-denial","expired"]` × `MODES=[dual,assertion]`；`expired` 期望**可恢复并重试成功**；注释："A mistaken retry would complete the turn, making either denial assertion fail" |

**Go 侧对应（最小面，比上轮更收敛）**：
1. 新增 `model/isRuntimeAuthDenial(status int, body []byte, code string) bool`（移植 §1.4 谓词，含 401/403 限制）；
2. 在 `model/auth_recovery.go:52 recoverUnauthorizedAuth` **入口**加短路：命中终态 → 返回一个"不可恢复"的 sentinel，让三个调用点（`responses_agent.go:1145/:1471/:3164`）直接surface 而不重试；
3. `model/responses_stream.go:576 isRetryableResponsesStreamError` 增加 `ResponsesAPIError` 分支：`isRuntimeAuthDenial(...)` → `false`；
4. `responseAuthorizationErrorCode`（`responses_agent.go:4766`）**不改**——它已与 `extract_x_error_json_code` 等价，缺的只是消费者。

---

## 2. Go 落地规格（转交 owning lane 用；我未实施）

> 行号基于**我手上的 `748ab55ca7` 期基线**；你合并时若行号漂移，以函数名为准（每个函数名在我这份树上唯一）。
> 每条只写"改哪个函数、一行说明"，以及**第二处消费点**。

### 2.1 #60003 realtime 每次 connect 解析

| 文件:行 | 函数 | 改动 |
|---|---|---|
| `realtime/transport.go:40-70` | `TransportBackendConfig` | 新增 `Auth func(ctx context.Context) (http.Header, error)`（per-handshake）与 `CallAuth`（per-call），保留 `Headers`/`CallHeaders` 作为回退 |
| `realtime/wire.go:40-45` | `dialRealtimeTransport` | `headers := backend.Headers.Clone()` 之后：若 `backend.Auth != nil` → 取 headers 并**按 `replace_auth_headers` 语义替换**那 4 个头（先 Del 再 Set） |
| `realtime/transport.go:276-287` | `createRealtimeWebRTCCall` | `PrepareCall` 与 `CallAuth` 共用同一 resolver（现在只有 `PrepareCall`） |
| `realtime/transport.go:394-400` | `newRealtimeSideband` | 去掉 `sidebandBackend.Headers = sidebandBackend.CallHeaders.Clone()` 的静态固化，改为继承 `CallAuth` 钩子（**保留**无钩子时的原行为，否则会打挂现有用例） |
| `appserver/realtime_runtime.go:415-467` | `realtimeStartOptions` | feature 开 && `runtimeauth.ClassifyOpenAIDestination(wsBaseURL) == DestinationSupported` → 构造 `runtimeauth.Provider`，未初始化则**报错**（对齐 `realtime_conversation.rs:1426-1430`）；`provider.EnvKey`/`ExperimentalBearerToken` 非空时保持现状（`bind_api_key` 语义） |

第二处消费点：`realtime/transport.go:399`（sideband 头来源）、`realtime/transport.go:276`（call 面）、`appserver/realtime_runtime.go:456`（`websocketProvider.Headers` 里已有 FedRAMP 等 provider 级头，runtime 头是**替换那 4 个**而不是清空全部）。

### 2.2 #60055 重连前复核

| 文件:行 | 函数 | 改动 |
|---|---|---|
| `realtime/wire.go:35-45` | `dialRealtimeTransport` | 每次 dial 记录"本次使用的凭据来源版本"；与上次成功握手版本不同则把 `reauth` 标记传上去（telemetry 可观测） |
| `realtime/transport.go:404-470` | `startRealtimeSideband` | 重连循环不缓存凭据；`ErrSourceChanged`/终态 denial **不计入** `rapidDisconnects`（`realtime/transport.go:435-443`） |
| `model/responses_agent.go:1129` 附近 | `markConnectedAuth` | 由"比 `AuthSnapshot` 指针"改为"比 source generation/revision"（对齐 `client.rs:369/1896-1914`） |

第二处消费点：`model/responses_agent.go` 的 WS 复用判定（现有 `TestWebsocketReauthTracksLastConnectedCredential` 锁着）、guardian 采样池（`appserver/guardian_reviewer.go` 一带，现有 `TestEnsureGuardianReviewerPrewarmsOnceAndFallsBackLazily`）。

### 2.3 #60073 remotecontrol

| 文件:行 | 函数 | 改动 |
|---|---|---|
| `remotecontrol/auth.go:27-55` | `RemoteControlConnectionAuth` + `NewRemoteControlConnectionAuthFromSnapshot` | 新增可选 `RuntimeAuth *runtimeauth.Provider` 与 `SourceGeneration func() uint64`；快照作为回退 |
| `remotecontrol/auth.go:457` | `RequestHeaders()` | runtime 可用 → 先 `HeadersForDestination(endpoint)` 再替换那 4 个头；目标非 OpenAI → 保持快照 |
| `remotecontrol/auth.go:480` | `ApplyRequest()` | 同上（per-request），并把解析错误按 `is_policy_denial`/`is_auth_error` 分成 PermissionDenied 与 other |
| `remotecontrol/websocket_loop.go:323` | `retireForAuthOwnerChange` | 增加"`SourceGeneration` 变化"也退役 |

第二处消费点：`remotecontrol/websocket_loop.go:148/178/197`（三处退役判定）、`remotecontrol/websocket_client.go:72`（"refreshing server token before reconnect" 那条路径仍用快照头）、`remotecontrol/server_api.go:190`（单点受益，无需改）。

### 2.4 #60096 终态 denial

见 §1.4 结尾的 4 步。第二处消费点：`model/responses_agent.go` 三个 401 入口（`:1145`/`:1471`/`:3164`）+ `model/agent_metrics.go:243/469`（只喂 telemetry，**不要**在那两处改语义）。

---

## 3. verifier 规格（你已登记的四个名字）

| contract | verifier | 必须断言 | 反证（改回旧行为必须红的那一行） |
|---|---|---|---|
| `realtime-runtime-auth` | `TestRealtimeAuthResolvesRuntimeAuthPerConnectLikeRust` | ①同一 session 内两次握手（首发 + sideband 重拨）各带一次**新签名**的断言（把两次 header 值记下来必须不同，断言带 nonce/时间戳语义）；②`assertion_only` 下 `Authorization` 以 `AgentAssertion ` 开头且**无** `x-openai-agent-assertion`；③`dual_header` 下两者都在；④feature 开 + 未初始化 → `Start` 报错而不是静默 Bearer；⑤既有 `TestExistingCallSidebandPrefersPerCallEndpointLikeRust`、`TestRealtimeSidebandReconnectHelpersLikeRust` 保持绿 | 删掉 `realtime/wire.go` 里的 per-dial Auth 合并（回到纯 `backend.Headers.Clone()`）→ 断言 ① 变红 |
| `runtime-auth-websocket-revalidation` | `TestWebsocketReconnectRevalidatesRuntimeAuthLikeRust` | ①轮换后重连的断言 ≠ 轮换前；②未轮换时 `reauth=false`（不得每次都重连）；③轮换后 `reauth=true`；④终态 denial 不进入退避重试；⑤既有 `TestWebsocketReauthTracksLastConnectedCredential` 保持绿 | 把轮换检测去掉（恒用旧值）→ 断言 ① 变红；把"总是重连"写死 → 断言 ② 变红 |
| `remote-control-runtime-auth` | `TestRemoteControlRequestsUseRuntimeAuthLikeRust` | ①`assertion_only` 下请求头只有 `AgentAssertion`，无 `Bearer`；②轮换后新请求头变化（per-request 解析）；③非 OpenAI 目标不注入 runtime 头；④非 OpenAI 目标 + 已配置 runtime → 目标校验报错（对齐 `auth.rs:330`）；⑤既有 `TestRemoteControlWebsocketLoopRetiresOnAuthOwnerChangeWhileConnected` 保持绿 | 删掉 `RequestHeaders()` 的 runtime 分支 → 断言 ① 变红；删掉目标校验 → 断言 ③/④ 变红 |
| `runtime-auth-terminal-denials` | `TestTerminalRuntimeDenialsSurfaceSignInLikeRust` | ①`403+runtime_instance_revoked`（body code）只请求 1 次；②`401+x-error-json=invalid_agent_instance_assertion` 只 1 次；③`401+token_expired` 恰好 2 次（恢复后成功）；④流层同上；⑤既有 `TestManagerCachesForbiddenAndRefreshesUnauthorizedLikeRust` 保持绿 | 把 `recoverUnauthorizedAuth` 入口的终态短路删掉 → 断言 ①② 变红（会重试）；把 `isRuntimeAuthDenial` 的 401/403 限制删掉 → 追加"429+revoked 必须可重试"子用例即红 |

> 反证输出请在 owning lane 的机器上跑（我这边无法编译你们的树）：每条只要贴 `go test ./<pkg> -run <Name>` 在"改回旧行为"分支上的 FAIL 行即可。

---

## 4. 判定：本轮 0 条可转 `complete`

| contract | 现状态 | 能否转 complete | 理由 / 转 complete 的最小条件 |
|---|---|---|---|
| `realtime-runtime-auth` | `partial` | **否** | 代码未落；转 complete 需：`realtime` 侧 per-handshake Auth 钩子 + `appserver` 侧 runtime provider 接线 + `TestRealtimeAuthResolvesRuntimeAuthPerConnectLikeRust` 真跑通 |
| `runtime-auth-websocket-revalidation` | `partial` | **否** | 同上，且必须与上一行同批（同一处 `transport.go:399`/`wire.go:40`），否则会出现中间态 |
| `remote-control-runtime-auth` | `partial` | **否** | 转 complete 需：`RemoteControlConnectionAuth` 带 runtime provider + 目标校验 + 分类 + `TestRemoteControlRequestsUseRuntimeAuthLikeRust` 通过 |
| `runtime-auth-terminal-denials` | `partial` | **否** | 转 complete 需：谓词 + `recoverUnauthorizedAuth` 短路 + stream 判定 + `TestTerminalRuntimeDenialsSurfaceSignInLikeRust` 通过 |

我在账本侧不做任何状态改动：`parity/commits.json` 里这 4 条对应 commit 仍按 `partial`，`manifest.json` 由你改。

---

## 5. 建议新增的 verifier 名字（你问的那一项）

四个已登记名字覆盖"主行为"，但下面四个**反证点**目前没有独立用例，容易在后续重构里悄悄退化。名字我在 `748ab55ca7` 期树上 `rg "func <name>\b"` 全为 0（与你们的树可能仍无冲突，登记前请再扫一次）：

| 建议名 | 锁住什么 | 为什么值得单独存在 |
|---|---|---|
| `TestRuntimeAuthHeadersReplaceActorCredentialLikeRust` | `replace_auth_headers` 语义：切换模式/轮换后不得残留上一模式的 `Authorization`/`x-openai-agent-assertion`/`ChatGPT-Account-ID`/`x-openai-fedramp` | Go 现有 `mergeHTTPHeaders` 是叠加语义；这是最容易写错又最难在端到端用例里暴露的一处 |
| `TestRuntimeAuthDenialIsNotRecoverableAuthErrorLikeRust` | provider 层谓词（`provider.rs:533-546` 的 Go 等价物）直测：runtime 开 + 终态码 → 不可恢复；runtime 关 → 仍可恢复 | 端到端用例无法区分"谓词正确"与"恢复路径恰好没触发" |
| `TestWebsocketRevalidationReusesConnectionWhenRevisionUnchangedLikeRust` | 没有变化时**必须复用**连接（防"每次都重连"） | 与 #60055 的主用例互为反向断言；只有主用例时，把实现改成"永远重连"仍然全绿 |
| `TestRemoteControlRuntimeAuthRejectsNonOpenAIDestinationLikeRust` | 目标校验（对齐 `auth.rs:330 runtime_requests_reject_unsupported_openai_origins`） | 这条是纯安全边界：把断言发给非 OpenAI host |

---

## 6. 基线（我方副本，供你判断分叉；**不是**你主干的值）

LF 归一化 sha256（我去掉 `\r\n` 后计算；你主干若不同，说明该文件已被别处改过）：

| 文件 | size | LF sha256 |
|---|---|---|
| `appserver/realtime_runtime.go` | 34,125 | `4bda7f9f8916f581d9eaa7e72bef012cbbfd0920431ede733bc30672e710c8f5` |
| `appserver/runtime_router.go` | 856,020 | `bc177a726aabbc7101680a0bbc948ea0bef46f216d5e0a82266d08d0433295bc` |
| `model/responses_agent.go` | 199,228 | `b556f7f5871df545261c018ee6ce4b99a52a8d032f5849e2db57812ba26b9ab3` |
| `model/agent_metrics.go` | 23,229 | `1e6884556e7fde6438ee8b574cbf26314487ae8d8ea65d3f6cebc80ba1ee3f48` |
| `model/responses_stream.go` | 113,215 | `0a76f6de4bf792a9941faf94c354abe1387a4ef751d69093b9525e4a51cb84ef` |
| `model/auth_recovery.go` | 11,240 | `60acdc38d15212c3a9f4bb205fd0958ea1052aa2e0507e75c10e46b81180d0f7` |
| `remotecontrol/auth.go` | 15,823 | `f0360d64084068278c6839b25ae88e157a9552f82577a6b3192207f3788bcd0e` |
| `remotecontrol/server_api.go` | 12,821 | `0e70c3a28d1120f1159c0a3a4b4dd7c027277ea68c0a70332df06d3f6274ae62` |
| `remotecontrol/websocket_client.go` | 3,430 | `2be548f1ba413a519b497a8d669c2950e516acd181ed41114ae0a445dbde3a83` |
| `remotecontrol/websocket_loop.go` | 15,305 | `39cca1f90a9968c6a8332753b37608878eaeba0635ef3e96e7b28adaf8df3bea` |
| `realtime/transport.go` | 36,045 | `b567a3848e244b489d0fdcb982719963c0c47276811e56017ff5f63c2ec43369` |
| `realtime/wire.go` | 32,492 | `f645764a10d6dabe39e6f7beb05cf6d8a2b8976d9e76b2729f9da68302e87db7` |
| `runtimeauth/config.go` | 4,151 | `7b6e840edb5a354a5fdab77e6c5ab644f87cb477325267e6a8cc59721fe8b837` |
| `runtimeauth/provider.go` | 7,295 | `9414bfd9c0e9a55128abffff34a7aa301b8809d3f44fdf9039c11293f8fc5179` |
| `runtimeauth/manager.go` | 13,733 | `17d18af01acd987a1892286ebab0c1dbd54de4917dfbacea34923c98558c40c0` |
| `runtimeauth/runtimeauth.go` | 11,589 | `d5b40b3124d9931a8c35474eace7b7cff996254ce232c69fe83e3ec0af293e3f` |

---

## 7. 不重复性检查（你要求"拆开做会重复就别硬凑"）

- `websocketauth/` = **入向**升级鉴权（app-server/exec-server），与 4 条无关（§0 末尾）。
- `#60003` 与 `#60055` **不能拆**：共用 `realtime/transport.go:399` 与 `realtime/wire.go:40`，拆开必出中间态。
- `#60073` 与 `#60055` 在 remotecontrol 上有一处语义重叠（"复用前复核"），但传输面不同（remotecontrol 是独立 WS+HTTP）：**共享同一个 revalidation 判定函数即可，不要两套实现**。
- `#60096` 与 #60014（managed mode）在断言模式用例上有前置关系，但与 #60003/#60073 只是共享同一个 `isRuntimeAuthDenial` 谓词 —— 谓词先落，其余复用。

## 8. 一句话结论

4 条的 Rust 形状已核实到行（§1），落地规格与 verifier 反证都齐（§2–§3）；但**实现必须由 owning lane 落**，且我这边拿不到你们 136 契约的基线（§0 理由 B），盲写补丁会有回退别人改动的风险。要我改判，请给"lane 改派 + 当前字节"两样。
