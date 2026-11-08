# P1 安全语义 13 条深挖（可开工版）

- 作者：**ledger-align** · 2026-11-08 · 只读分析，不改任何 Go 业务代码，不改 `parity/**`
- 输入数据：
  - 账本/契约：`/tmp/ledger_state_2026_11_08.zip`（sha256 `f58f9d12fe24c6f94cebc42e656964fdc35313df3cf8b0cc2b79827fb3561a95`）：`parity/commits.json` 3677 条、`parity/contracts/manifest.json` 135 条（98 complete / 37 partial）、`domains.json` 26 条
  - Go 主干快照：我本地校验工作副本（由你同步的 `anchor_sync2` + 各 lane 合并结果组成，与 `748ab55ca7` 一致）
  - Rust 源：**真 git 检出** `/home/jacks/jacks_dev/codex`，HEAD = `748ab55ca7de0b647d9a7a9d9a8e70e00f28666d`；`CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs`
- 本文件性质：**排期与实现规格**，不是判定变更。13 条在 `parity/contracts/manifest.json` 里仍是 `partial`，本文件不动它们。
- 行号一律 `文件:行`；零命中一律给 `rg` 模式 + `exit=1`；Rust 侧引用给 `git show 748ab55ca7:<path>` 的行号。

---

## 0. 先回答你上一轮追问：静态 Bearer 是「一次性捕获」还是「每次重连复用同一个值」

**两者都是，而且是同一条链上的两段。** 调用链证据（全部为我本地实读）：

| # | 位置 | 事实 |
|---|---|---|
| 1 | `appserver/realtime_runtime.go:415` / `:418` / `:463` | `apiKey := ""` → `realtimeAPIKey(snapshot, providerInfo)` 解析一次 → `headers.Set("Authorization", "Bearer "+apiKey)`。**这一步把 token 固化进 `headers` 快照** |
| 2 | `appserver/realtime_runtime.go:470-472` | `callHeaders := callProvider.Headers.Clone()` + `mergeHTTPHeaders(callHeaders, callAuth.Headers)`；`callAuth, _ := runtimeProvider.APIAuth()`（`:455`）——**WebRTC/call 侧同样是快照**（`ConfiguredProvider.APIAuth` 返回静态 `AuthHeaders`，`model/provider.go:277-298`） |
| 3 | `appserver/realtime_runtime.go:484-492` | 快照被塞进 `realtime.TransportBackendConfig{Headers, CallHeaders, PrepareCall}`；`PrepareCall` 只签 **WebRTC POST**（`realtime/transport.go:276-287`），**不覆盖任何 WebSocket 握手** |
| 4 | `realtime/transport.go:396-400` | `newRealtimeSideband` 里 `sidebandBackend.Headers = sidebandBackend.CallHeaders.Clone()`——sideband 的 WS 头来源就是那份静态快照 |
| 5 | `realtime/transport.go:426` | sideband 重连循环 `for { ... dialRealtimeTransport(..., sideband.backend, ...) }`——**每次重连都拿同一个 `backend`** |
| 6 | `realtime/wire.go:40` | `headers := backend.Headers.Clone()`——每次 dial 只是把**同一份快照再克隆一次**，全链路没有任何「解析凭据 / 校验 source generation」的点 |

**结论**：值在 `Start` 构造期一次性捕获，之后 N 次重连**逐字复用同一个 token 字符串**；Go 侧不存在 per-dial 鉴权钩子（`rg -n "PrepareCall" --glob '*.go'` 只有 `realtime/transport.go:52/282` 与 `appserver/realtime_runtime.go:489` 三处，全部是 WebRTC POST 面）。

因此修复**不能只做其中一半**：
- 只做「重连时重算」而不做「一次性捕获处改造」→ 首发握手仍是旧值，且 `Headers` 快照与解析结果不一致；
- 只做「一次性换成 runtime auth」而不做 per-handshake 钩子 → 轮换后仍然复用旧断言（就是 #60055 要修的那条）。

Rust 侧的正确形状在这里（这就是契约的 ground truth）：

```
git show 748ab55ca7:codex-rs/codex-api/src/endpoint/realtime_websocket/methods.rs
:904  /// Use the transport captured with the selected authentication source.
:910  /// Resolve request auth on every handshake, including sideband retries and reconnects.
:911  pub fn with_auth(mut self, auth: crate::SharedAuthProvider) -> Self
:1105 if let Some(auth) = &self.auth {
:1107     *request.headers_mut() = auth.apply_auth_headers(headers).await?;
```

以及 runtime 选择点 `git show 748ab55ca7:codex-rs/core/src/realtime_conversation.rs:1426-1430`（`Feature::RuntimeAgentInstances` 未初始化时**硬报错** `"runtime authentication is not initialized"`，不是静默退化成静态 key）与 `:1534-1563`（`is_openai_realtime_url` → `validate_runtime_environment` → `bind_api_key`/`bind` → `select_runtime_auth_for` → `validate_runtime_selectors` → `websocket_auth = Some(selected.into_auth_provider(*mode))`）。

---

## 1. 依赖图与开工顺序（按「能独立验证的先后」，不按 PR 号）

### 1.1 必须一起改的组

| 组 | 成员 | 为什么必须同批 |
|---|---|---|
| **G-RT** | `realtime-runtime-auth`(#60003) + `runtime-auth-websocket-revalidation`(#60055) | 同一处静态 Bearer + 同一处 sideband 快照（`realtime/transport.go:399`）。若只改 #60003，会出现「主 WS 用 runtime 断言、sideband 仍用旧 Bearer」的中间态；若只改 #60055，重连时重算的仍是同一份静态头，等于空转 |
| **G-PERM** | `protected-model-profile-downgrade`(#59933) → `turn-context-required-permission-profile`(#59963) | #59933 判「启动降级」时读的是 effective permission profile；#59963 让 turn context **必带** profile。顺序反了会在 #59963 落地后立刻出现「历史 turn 无 profile → 无法判降级」的解析失败 |
| **G-MODE** | `managed-runtime-auth-mode`(#60014) → `runtime-auth-terminal-denials`(#60096) 的 assertion 分支 | Rust 的 denial 用例按 `MODES = [("dual", DualHeader), ("assertion", AssertionOnly)]` 参数化；Go 若没有 requirements 强制的 `assertion_only`，`AgentAssertion` 请求形状构造不出来 |
| **G-MCP** | `mcp-network-policy-derivation`(#60046) 内部：③④（陈旧 grant 不得盖掉有效 deny / deny 先发布后落盘）与 ①②（从 spec 推导 MCP 策略） | ③④ 是**当下就活的安全语义**；①② 在 Rust `748ab55ca7` 上仍是 dead code（见 §9 证据）。同一条契约按「③④ 先落、①② 等接线 PR」分两批验收 |

### 1.2 可独立验证的先后（推荐落地序）

| 序 | contract | #PR | 本地可验证性 |
|---|---|---|---|
| 1 | `runtime-auth-terminal-denials` | #60096 | ✅ 纯 model 层 + httptest（现有 `runtimeauth` fake server 可复用） |
| 2 | `patch-approval-cancel-turn-scope` | #59910 | ✅ `ServerRequestSinkFunc` 注入即可 |
| 3 | `child-agent-catalog-preservation` | #60049 | ✅ `ModelsManager` 可注入 fake |
| 4 | `protected-model-profile-downgrade` | #59933 | ✅ 配置 + settings 断言 |
| 5 | `turn-context-required-permission-profile` | #59963 | ✅ rollout round-trip |
| 6 | `final-answer-moderation` | #59842 | ✅ protocol/state round-trip（wire 级） |
| 7 | `account-security-setup-appserver-fetch` | #60082 | ✅ appserver handler + httptest |
| 8 | `managed-runtime-auth-mode` | #60014 | ✅ 配置解析（纯函数） |
| 9 | `mcp-network-policy-derivation` | #60046 | ⚠️ ③④ 可验；①② 需 Rust 接线后才有 ground truth |
| 10–11 | `realtime-runtime-auth` + `runtime-auth-websocket-revalidation` | #60003 / #60055 | ✅ 但需要 fake WS + fake 注册端点（可行，见 §11/§12） |
| 12 | `remote-control-runtime-auth` | #60073 | ✅ remotecontrol loop + fake 服务端 |
| 13 | `user-verification-platform-provider` | #43624 | ❌ linux_node 无法验证（macOS Secure Enclave 独占），条件见 §15 |

> 注：上表「序」是**建议落地顺序**；下文的 13 条按同一顺序展开，`序 N` 对应正文 `§(N+1)`（§0 是静态 Bearer 调用链溯源，§1 是依赖图与顺序）。

排序依据：① 判定不依赖「真实 runtime agent instance 注册」的条目先做（1–8 全部可用 httptest/fake 构造）；② 鉴权链（10–12）虽然也能本地构造，但改动横跨 `appserver/realtime`+`realtime`+`runtimeauth` 三层，放后面做容易被前面几条的 config 改动（#60014）反复 rebase；③ #60046 的 ①② 与 #43624 有外部前置条件，不占关键路径。

---

## 2. `runtime-auth-terminal-denials` — #60096 `efdacaf46f`

**判定保持**：`partial`（登记在 manifest，verifier 现状 `TestManagerCachesForbiddenAndRefreshesUnauthorizedLikeRust` 必须保持绿）。

### ① 行为差的精确复现路径

构造三个响应，打同一个 `/v1/responses` 端点（形状抄 Rust `core/tests/suite/runtime_auth_denials.rs:88-110`）：

| case | 响应 | Rust 期望 | Go 现状 |
|---|---|---|---|
| `revoked` | `403` + body `{"error":{"code":"runtime_instance_revoked",...}}` | 终态，turn 以「需要重新登录」结束，**不重试** | 403 不进 `:3162` 的 401 恢复分支，直接以 `HTTP 403` 泛化错误结束 → 客户端拿到的是网络/HTTP 错误，不是 sign-in |
| `header-denial` | `401` + header `x-error-json: base64({"error":{"code":"invalid_agent_instance_assertion"}})` + body 无 code | 终态，不重试 | `:3162` 命中 401 → 跑 `recoverUnauthorizedAuth`（`model/auth_recovery.go:52`）→ 刷新 token → `continue` 重试，直到 `requestMaxRetries` 用尽 |
| `expired` | `401` + body `{"error":{"code":"token_expired"}}` | **可恢复**：跑一次恢复、一次重试并成功 | 同左（这是反证分支，必须保持） |

终端码全集（Rust 原文，`git show 748ab55ca7:codex-rs/protocol/src/error.rs:643-670`）：`runtime_assertion_invalid` / `runtime_instance_paused` / `runtime_instance_revoked` / `invalid_agent_instance_assertion`；且**只在 401/403 上生效**，code 来源两处：`identity_error_code`（来自 `x-error-json`，`codex-api/src/api_bridge.rs:348-359`）或 body `error.code`。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| `model/responses_agent.go:4762 responseAuthorizationErrorCode` | **不改**（已与 Rust `extract_x_error_json_code` 逐字对齐：同一 header、同一 base64/JSON 取值路径） | 取码能力已在，缺的是消费者 |
| 新增 `model/auth_denial.go: isRuntimeAuthDenial(status int, body []byte, code string) bool` | 新增 | 移植 Rust `is_runtime_auth_denial`：401/403 + 四码（header 或 body 任一命中） |
| `model/responses_agent.go:1145`（WS 预热握手）、`:1471`（WS 重连）、`:3164`（HTTP 请求） | 三处 401 分支前置判断：`isRuntimeAuthDenial` 为真则**不跑** `recoverUnauthorizedAuth`，直接返回带 sign-in 语义的错误 | 把「终态拒绝」从「凭据可修复」里摘出来 |
| `model/responses_stream.go:576 isRetryableResponsesStreamError` | 新增 `ResponsesAPIError` 分支：`isRuntimeAuthDenial(StatusCode, Body, AuthorizationErrorCode)` → `false` | 流层不得把 denial 当可重试 |
| `model/responses_agent.go:4681`（`ResponsesAPIError` 构造） | 已带 `AuthorizationErrorCode`，**不改**；但需在 `ResponsesStream` 侧透传 body（现状 `Body` 已填，`:4676`） | 让上面两处能读到 code |

**第二处消费点**：不是 1 处而是 **3 处**（`:1143`/`:1469`/`:3162` 三条独立 401 路径）。这正是「门控类条目」的坑：只改 HTTP 路径会让 WS 预热/重连仍然重试。另外 `model/agent_metrics.go:243` 与 `:469` 也用同一函数**但只做 telemetry**——改它们无害且**不要**顺手改语义（那里是采样，不是判定）。

### ③ 失败模式（改错会怎样）

- **把普通 401 误判成终态**：用户 Bearer 正常过期（`token_expired`）时直接被要求重新登录，token 刷新链失效 → 这正是 Rust 用例里 `expired` 分支存在的理由。
- **只按 code 判、不看 status**：`{"error":{"code":"runtime_instance_revoked"}}` 出现在 429/500 上（网关兜底文案）会被误判终态，掩盖真实可重试故障。
- **在 `recoverUnauthorizedAuth` 里加判断**：会污染 Rust 的 recovery plan 游标语义（`model/auth_recovery.go:31` 注释里的 `planIndex`），recovery 事件会与 Rust 不一致。

### ④ 最小 verifier

- 测试名：`TestRuntimeAuthDenialsAreTerminalButOrdinary401RecoversLikeRust`（当前全树 0 命中）
- 必须断言（四条，缺一不可）：
  1. `revoked`（403+body code）后 `/v1/responses` 只被请求 **1 次**（`up_to_n_times(1)` 语义），且返回错误可用 `errors.As` 归类为 sign-in 需求；
  2. `header-denial`（401+`x-error-json`）同样只 1 次；
  3. `expired`（401+`token_expired`）**恰好 2 次**（1 次失败 + 1 次成功），且第二次成功 completion；
  4. 三种 case 的 `AuthErrorCode` telemetry 仍然被记录（`agent_metrics` 不变）。
- 反证方式：把 `responses_agent.go:3164` 那一行的 `isRuntimeAuthDenial(...)` 判断改成恒 `false` → 用例 1/2 变红（因为它会重试）；把 `isRuntimeAuthDenial` 的 status 判断删掉 → 加一条「403/429 带 revoked code 必须仍可重试」的子用例即可变红。

### ⑤ 依赖

- 依赖 `managed-runtime-auth-mode`(#60014) 才能构造 `assertion` 模式的请求形状（`AgentAssertion` 头）；`dual` 模式不依赖。
- 与 `remote-control-runtime-auth`(#60073)、`realtime-runtime-auth`(#60003) 共用同一个 `isRuntimeAuthDenial` → **先把本条的 helper 落地**，那两条直接复用。

---

## 3. `patch-approval-cancel-turn-scope` — #59910 `ad1dbba32d`

**判定保持**：`partial`（现有 verifier `TestCancelCommandApprovalAbortsTurnLikeRust` 保持绿，但它锁的是**命令**审批，不是文件变更审批）。

### ① 行为差的精确复现路径

1. turn A 发起 `apply_patch`，app-server 发 `FileChangeRequestApproval`（call id `call-1`），用户选 `cancel`；
2. 期望（Rust）：**A 被中断**，且「取消」只在它自己的 pending approval 上生效；
3. 实际的 Go：`appserver/runtime_router.go:20151-20160` 的 switch 只有 `accept` / `acceptForSession`，**`cancel` 落到 `default: return tool.ApplyPatchApprovalDecision{}`** —— 只当「拒绝」处理，**不中断 turn**；
4. 跨 turn 误杀：Rust 原 bug 是「延迟的 cancel 打断后来的 turn」。Go 的延迟 cancel 走 broker 的 request-id 解析（`appserver/server_request_broker.go:451-487`，按 uuid 唯一 id，且 `claimPending` 拿走即删），**因此 Go 这一半天然免疫**——但代价是「没有 `expected_turn_id` 语义，也没有 pending-approval 表」，无法表达 Rust 的 `take_pending_approval(id, Some(&turn_id))`（`git show 748ab55ca7:codex-rs/core/src/session/handlers.rs:227-240`）。

可直接写成测试的双 turn 形状（对齐 Rust `core/src/session/patch_approval_tests.rs:1-91`）：
- turn A 的 `call-1` 取消；
- 立刻在 turn B 用**同一个 call id `call-1`** 发起 `apply_patch` 并 `accept`；
- 断言：A 已中断、**B 的审批成功且 B 仍在 active**、B 的 patch 实际落地。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| `appserver/runtime_router.go:20151`（`applyPatchApprovalForTurn` 内的 decision switch，`default` 在 `:20157`） | 新增 `case string(FileChangeApprovalCancel):` → 记录 `ToolDecisionAbort` 后调用 `interruptRuntimeTurn(threadID, turnID, nil, turnAbortReasonInterrupted)`，再返回 `tool.ApplyPatchApprovalDecision{}` | 与命令审批的分支（`appserver/runtime_router.go:20010-20016`）对齐：cancel = 中断本 turn，不是 deny |
| `appserver/runtime_router.go:20083 applyPatchApprovalForTurn(threadID, turnID)` | **不改签名**，但要求 cancel 分支只能用闭包里捕获的 `turnID`（不是当前 active turn） | `expected_turn_id` 语义在 Go 里由闭包捕获 + broker request id 共同保证；显式写注释锁住这个不变量 |
| `appserver/runtime_router.go:18141-18143` | 确认 `options.ApplyPatch.Approval` 只在**该 turn 的 turnID** 上绑定（现状已是 `strings.TrimSpace(turnID)`） | 防止后续重构成「实时读 active turn」 |

**第二处消费点（两处，必须一起看）**：
1. **`server_request.go:524 FileChangeApprovalCancel` 的另一个消费方**：`app/remote_tui.go:5020` 会把 TUI 的「取消」映射成这个常量——它依赖的是「cancel 会中断」，改完必须回归 TUI 路径。
2. **legacy `applyPatchApproval` / `execCommandApproval` 两个旧方法**（`appserver/server_request.go:23`、`:24`，`schema.go:716` 都还在注册）走的是 `ServerRequestApplyPatchApproval` 分支。Rust 明确把 legacy `Op::PatchApproval{Abort}` 也**统一路由到 pending-approval 处理**（`handlers.rs:739-748`）。Go 若只改新方法，legacy 客户端仍是「cancel=deny」。**这两条必须同一批改。**

### ③ 失败模式

- **把 cancel 当 deny**（今天的现状）：用户以为停了，turn 继续跑后续工具；对审批型写入这是「用户意图被无视」。
- **改过头：cancel 中断「当前 active turn」而不是「发起审批的 turn」**：就是 Rust 修掉的那个 bug——延迟的 cancel 杀掉后来的 turn。
- **中断 + 又返回 deny**：工具调用会同时收到「中止」和「拒绝」，产物/事件序列与 Rust 不一致（Rust 只 `abort_turn_if_active` 并向 sender 发 `Abort`）。

### ④ 最小 verifier

- 测试名：`TestFileChangeApprovalCancelInterruptsOnlyRequestingTurnLikeRust`（0 命中）
- 断言：
  1. turn A 收到 `FileChangeApprovalCancel` 后，A 的 abort channel 在 2s 内关闭（复用 `TestCancelCommandApprovalAbortsTurnLikeRust:33-38` 的 `aborted` channel 形状）；
  2. 用同 call id 的 turn B 走 `accept`，B 的 `aborted` channel **不**关闭，且 `decision.Approved == true`；
  3. `emitToolDecisionRecords` 里 A 的记录是 `abort`、B 的记录是 `approved`（telemetry 不得串）。
- 反证方式：把新增的 `case string(FileChangeApprovalCancel)` 删掉（回到 `default`）→ 断言 1 变红；把 `interruptRuntimeTurn(threadID, turnID, ...)` 的 `turnID` 换成「当前 active turn」→ 断言 2 变红。

### ⑤ 依赖

- 与 `protected-model-profile-downgrade`(#59933) 无耦合；但**建议在 #60096 之后**，因为两者都改 `runtime_router.go` 的审批/错误分支，先后落地可以减少一次 rebase。

---

## 4. `child-agent-catalog-preservation` — #60049 `44ccf39457`

**判定保持**：`partial`。

### ① 行为差的精确复现路径

Rust 的 bug 描述（`git show 44ccf39457` 的 Why）：「子 agent 启动时若所选 catalog 未初始化、或磁盘数据比父进程内存快照旧，会丢掉 `tool_search` 这类远端能力」。

Go 对应形状（可写成测试）：
1. 父 runner 的 `ModelsManager` 先做一次 **online** 刷新，内存里拿到含 `tool_search` 的 catalog（`model/models_endpoint.go:727` 的 `case RefreshOnline: m.fetchAndUpdateModels()`）；
2. 让磁盘缓存停留在**旧版本**（不含 `tool_search`），或让缓存写入失败；
3. 起子 agent（`appserver/agent_controller.go:130` → `:502 resolveSpawnModelOverrides`）；
4. 现状：`:533` 是 `manager.ListModels(model.RefreshOffline)`，而 `RefreshOffline` 的语义是 `m.tryLoadFreshCache()`（`model/models_endpoint.go:731`）——**会去读磁盘缓存**，于是子 agent 的 presets 比父内存旧，`tool_search` 能力消失；
5. Rust 期望：非 root agent 用 `RefreshStrategy::InMemory`（`git show 748ab55ca7:codex-rs/core/src/session/mod.rs:605-615`），只读内存、不碰磁盘。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| `appserver/agent_controller.go:533` | `manager.ListModels(model.RefreshOffline)` → `model.RefreshInMemory` | 子 agent 的 catalog 只读父的内存快照（Go 已有 `RefreshInMemory`，`model/catalog.go:1319-1321`，注释就写着 Rust #57789） |
| `appserver/agent_controller.go:549` | `manager.GetModelInfo(selectedModel, nil)` 之后补充「同一次 spawn 内不二次刷新」的保证（若底层 `GetModelInfo` 会触发磁盘加载则显式传 in-memory 策略） | 避免「presets 走内存、info 走磁盘」的第二处读 |
| `appserver/agent_controller.go:462 modelsManagerForSpawn` | **不改**（已返回父 runner 的 `ModelsManager`，这正是「共享父 catalog」的正确前提） | 但要在注释里锁住：不得改成 `NewStaticModelsManager(BundledModelsResponse())` 的新实例（`:469` 的 fallback 仅在无 runner 时成立） |

**第二处消费点（两处）**：
1. `appserver/model_catalog_world_state.go:48` —— world state 里宣称的 catalog 策略是 `string(model.RefreshOffline)`。子 agent 与 world state 会看到**两份不同新鲜度**的目录，模型因此可能被建议一个「自己 catalog 里没有」的模型。
2. `exec/agent_controller.go:678` —— 非 app-server 路径（`codex exec`）的子 agent 列举同样用 `RefreshOffline`；这条不走 appserver，但**语义必须一致**，否则 SDK 差分会在 exec 与 app-server 之间冒出差异。

### ③ 失败模式

- **改成 `RefreshInMemory` 但父 manager 尚未初始化**：catalog 为空 → 子 agent 拿到零模型，`SpawnAgentModelName` 报「模型不存在」。Rust 的处理是「先初始化 provider/auth/originator catalog，再解析子模型」（`thread_manager.rs:1116-1130` 的 `models_for_thread(..., &source)`）。Go 必须在 spawn 前确认父 manager 至少完成过一次解析（例如 `resolveSpawnModelOverrides` 前 `if len(manager.ListModels(RefreshInMemory)) == 0 { … 回退到 OnlineIfUncached }`）。
- **顺手把 world state 也改成 in-memory**：world state 是给客户端看的，改成 in-memory 会让冷启动进程展示空目录。

### ④ 最小 verifier

- 测试名：`TestChildAgentSpawnReadsParentCatalogFromMemoryLikeRust`（0 命中）
- 断言：
  1. 父 manager 内存 catalog 含 `tool_search`，磁盘缓存为旧版本；子 agent 解析出的 `ModelPreset` **必须**含 `tool_search`；
  2. 磁盘缓存写入失败（返回 error）时同上；
  3. 父 catalog 为空且 `RefreshInMemory` 返回 0 条时，回退路径仍能拿到 bundled catalog（不得 panic）；
  4. 并发刷新：另一个 goroutine 同时在刷新父 catalog，子 agent 不得看到「半新半旧」的混合目录（Rust 用 `Arc` 快照保证，Go 需断言 `ListModels` 的一次调用内一致）。
- 反证方式：把 `:533` 改回 `RefreshOffline` → 断言 1 变红。

### ⑤ 依赖

- 独立。但若 `thread-model-metadata-exposure`(#59958, P2) 先落地，本条的 world state 断言要跟着改（两者都动 `model_catalog_world_state.go`）→ **建议本条先于 #59958**。

---

## 5. `protected-model-profile-downgrade` — #59933 `c1c07853fe`

**判定保持**：`partial`。

### ① 行为差的精确复现路径

Rust 改的是 `Session::new` 的启动限制（`git show 748ab55ca7:codex-rs/core/src/session/mod.rs:823-845`）：当模型 `auto_review_required_for_model` 且 reviewer 不是可信 Guardian 时，**只有**满足下列全部条件才降级到 `workspace_write`：

```
enforcement != External
&& network_sandbox_policy().is_enabled()
&& file_system.has_full_disk_write_access()
&& entries.iter().all(|e| e.legacy_writable_path().is_ok())   // 可投影成本地路径
```

否则对「full disk write」的 profile 走**拒绝**（错误文案含 `you need to use auto review`，见 `settings_constraints.rs:195-200`）；`read_only` 类 profile **保持不动**。

Go 现状两处：
- `appserver/turn_runtime.go:9007` 只在 `permissionProfile.Profile.Disabled` 时降级（即只有 Full Access），`Managed{Unrestricted, Enabled}`、`from_runtime_permissions(root_write, Enabled)` 这些等价形状**不降级**；
- `appserver/step_settings_constraints.go:88 permissionProfileHasFullDiskWriteAccess` 把 `profile == nil` 也算作 full disk write（`return profile == nil || profile.Disabled || profile.SandboxPolicy == nil || profile.SandboxPolicy.HasFullDiskWriteAccess()`）——**nil profile 被当成最宽松**，方向反了。

复现形状：`requirements` 里把 `gpt-5.2` 标为 protected（`config/api.go:778 AutoReviewRequiredForModel`），turn 以 `permission_profile = Managed{file_system: Unrestricted, network: Enabled}` 启动 → Go 不降级，模型在全盘可写 + 联网下跑自动审核；Rust 降级到 workspace-write。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| `appserver/turn_runtime.go:9006-9012` | 把 `Profile.Disabled` 单一判断替换为 `protectedModelStartupNeedsDowngrade(profile)` 判定（四条件，见上） | 让「全盘写 + 联网 + 可投影 + 非 External」统一降级 |
| 新增 `appserver/protected_model_profile.go: protectedModelStartupNeedsDowngrade(*sandbox.PermissionProfile) (downgrade bool, reject bool)` | 新增 | 移植 Rust 的四条件 + `legacy_writable_path` 投影检查（Go 已有 `HasFullDiskWriteAccess`，缺的是 network/External/可投影三项） |
| `appserver/step_settings_constraints.go:88 permissionProfileHasFullDiskWriteAccess` | `profile == nil` 改为 `false`（无 profile ≠ 全盘写），并改用同一 helper | 修掉「nil 当宽松」的反向默认 |
| `appserver/step_settings_constraints.go:127-146` | 用 `protectedModelStartupNeedsDowngrade` 的 reject 结果直接返回 `autoReviewRequiredModelError(model)` | 让「不可降级」= 明确拒绝，而不是静默放行 |

**第二处消费点（三处）**：
1. `appserver/runtime_router.go:16682`（`if autoReviewRequiredForModel(cfg, model)`）——另一条启动路径同样按 protected 处理，必须用同一判定；
2. `appserver/command_checkpoint.go:170`——审批检查点也按 protected 分支，若只改 turn 启动会留下「chat 里降级了、checkpoint 里没降级」；
3. `appserver/agent_runtime.go:374`——子 agent 的 protected 判定，第三处。

### ③ 失败模式

- **把 `nil` profile 继续当全盘写**：protected 模型的 nil-profile turn 被判「需要降级」→ 反而给出一个 workspace_write，行为从「默认」变成「收窄」，与 Rust 的 `read_only` 保持不动语义冲突。
- **漏掉 `External` 排除**：外部沙箱（Windows MXC / 远程执行器）会被塞进本地 workspace-write profile，`sandbox/` 侧直接不认这个组合。
- **漏掉「可投影」检查**：`file:///%00/...` 这类不可投影 URI（Rust 用例里的 `unprojectable`）会进 `legacy_writable_path()` 报错路径 → 启动 panic 或静默丢弃路径。

### ④ 最小 verifier

- 测试名：`TestProtectedModelStartupDowngradeUsesPermissionProfileLikeRust`（0 命中）
- 断言（表驱动，7 行照抄 Rust 用例的 Preserve/Downgrade/Reject 三分类）：
  1. `Disabled` / `Managed{Unrestricted,Enabled}` / `root_write+Enabled` / `projectable+Enabled` → **Downgrade**，且生效 profile 的 ID 是 `workspace-write`；
  2. `root_write+Restricted` / `External{Enabled}` / `External{Restricted}` → **Reject**，错误文案含 `you need to use auto review`；
  3. `read_only()` → **Preserve**（profile 逐字节不变）；
  4. 每行都断言 `approvals_reviewer == auto_review`。
- 反证方式：把 helper 的 `enforcement != External` 判断删掉 → 第 2 行（External）变红；把 `network.isEnabled()` 判断删掉 → `root_write+Restricted` 变红。

### ⑤ 依赖

- **必须先于** `turn-context-required-permission-profile`(#59963)：后者让 profile 变必填，本条是「用 profile 判降级」。
- 与 `sandbox/` 的 profile 解析有交叉，但**执行面**改动不由我提（若 Windows lane 要补 MXC 适配，接口是 `sandbox.PermissionProfile` 的四条件判定，不改语义）。

---

## 6. `turn-context-required-permission-profile` — #59963 `2889d8e1bf`

**判定保持**：`partial`。

### ① 行为差的精确复现路径

Rust 的改动（`git show 2889d8e1bf` 的 What changed）：`TurnContextItem.permission_profile` 变**必填**，删掉 legacy `sandbox_policy` / `file_system_sandbox_policy` 两个字段，并在**反序列化时**把老历史归一化成 profile（保留「畸形文件系统规则 → 保守回退」），但**序列化仍写 legacy 兼容字段**供老 reader 使用。

Go 现状：
- `rollout/rollout.go:1076 TurnContextRecord` 里 `SandboxPolicy any`（`:1089`）与 `PermissionProfile json.RawMessage`（`:1094`，`omitempty`）**并存**；
- 写入点 `appserver/turn_context_record.go:52-78`：`PermissionProfile` 只在 `runConfig.PermissionProfileJSON` 非空且 `json.Valid` 时才填（`:76-78`），否则**整个字段缺失**；
- 读出侧没有任何「legacy → profile」归一化（`rollout/canonical_replay.go:744/783` 只是把 legacy 字段做序列化对齐）。

复现形状（冷恢复）：
1. 写一条 turn_context 记录，只有 `sandbox_policy: "read-only"`、没有 `permission_profile`（老 rollout 文件，或 `PermissionProfileJSON` 为空的运行路径都能产生）；
2. `thread/resume` 冷恢复该 thread；
3. 现状：恢复时无法回答「这条 turn 当初用的是哪个 profile」，只能退回当前配置默认；
4. Rust：归一化成 `read_only` profile，并据此校验/恢复。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| `rollout/rollout.go:1076 TurnContextRecord` | `PermissionProfile` 去掉 `omitempty`，保留 `SandboxPolicy`（Rust 也保留 legacy 兼容字段） | 让「无 profile」在写入侧不可能发生 |
| 新增 `rollout/turn_context_normalize.go: NormalizeTurnContextPermissions(record) (profile, error)` | 新增 | 移植 Rust 反序列化归一化：优先 profile；否则由 `sandbox_policy` 合成；畸形规则 → 保守（read-only）并记录 reason |
| `appserver/turn_context_record.go:76-78` | 无法取到 profile 时**不回退为省略**，而是走 `NormalizeTurnContextPermissions` 由 cwd/approval 合成 | 堵住「允许隐式权限」的写入口 |
| `rollout/canonical_replay.go:744`（逐字段重放表） | 把 `permission_profile` 从可选改为必须存在（缺则用归一化结果补齐） | 让 round-trip 与 Rust 一致 |
| `state/extract.go`（对应 Rust `state/src/extract.rs`） | 从 profile 直接读权限，不再读 legacy `sandbox_policy` | Rust 本条明确「metadata 抽取改为消费 profile」 |

**第二处消费点（三处）**：
1. `appserver/turn_context_record.go:41`（`AppendTurnContext` 之前构造 payload 的另一条路径）；
2. `rollout/rollout.go:1104-1106` 注释里写明 Rust #51492 已把一批字段从 `TurnContextItem` 删除——**归一化时不能把这些删掉的字段再读回来**；
3. `history/src/rollout_payload.rs` 对应的 Go 侧（冷恢复 payload）：Rust 本条把「legacy 事件里多个 text part 合并成一条」的规则也放在同一处，Go 若只改写入不改读出，冷恢复会丢 profile。

### ③ 失败模式

- **把畸形规则静默降级成「无限制」**：Rust 的保守回退是「更严格」，方向搞反就是提权。
- **只在序列化侧必填、反序列化侧不归一化**：老 rollout 文件一读就失败 → 大量历史 thread 无法 resume（这是最可能的回归）。
- **归一化读回已删除字段**（如 `workspace_roots`/`network`，`rollout/rollout.go:1104-1106`）：会与 canonical replay 的字段白名单打架，`TestAlignment...` 类逐字节比较会红。

### ④ 最小 verifier

- 测试名：`TestTurnContextAlwaysPersistsPermissionProfileLikeRust`（0 命中）
- 断言：
  1. 任意 turn（含 `PermissionProfileJSON` 为空的路径）写出的记录**必含** `permission_profile`；
  2. legacy 记录（只有 `sandbox_policy`）反序列化 → 得到等价 profile（`read-only` / `workspace-write` / `danger-full-access` 三档）；
  3. 畸形文件系统规则 → 归一化到 read-only，且 reason 可观测；
  4. round-trip：写 → 读 → 再写，`permission_profile` 逐字节稳定（对齐 `rollout/canonical_replay.go` 的字段序注释 `:702`）；
  5. 序列化**仍含** legacy `sandbox_policy`（老 reader 兼容）。
- 反证方式：把 `appserver/turn_context_record.go:76` 的条件判断改回「空则省略」→ 断言 1 变红；删掉归一化的「畸形 → read-only」分支 → 断言 3 变红。

### ⑤ 依赖

- **在 `protected-model-profile-downgrade`(#59933) 之后**。
- 与 `final-answer-moderation`(#59842) 都改 rollout/history 的重放表 → **建议本条先落地**（字段必填是结构性改动，先落可减少 #59842 的合并面）。

---

## 7. `final-answer-moderation` — #59842 `ddd1812a51`

**判定保持**：`partial`。

### ① 行为差的精确复现路径

Rust 语义（`git show ddd1812a51` 的 What changed）：读 `final_answer_moderation` 元数据，**仅当 item id 匹配**时给 completed 的 final-answer message 挂 `moderation: { moderated: bool }`；normal 与 plan 两种模式都要挂；新 response 开始时清掉 pending decision；legacy 事件与 history 重建都要保留 item id 与原生的 moderation 标记，且「有 moderation 决定时把多个 text part 合并成一条 legacy 事件」。

Go 现状：
- `protocol/exec_events.go:127 ThreadItem` **没有** `moderation` 字段；`AgentMessageItemWithPhase`（`:266`）不带；
- 只有 turn 级 metadata：`protocol/exec_events.go:730 TurnModerationMetadata`（Rust #57363 的 safety recourse），消费方在 `tui/tea/safety_recourse.go:56`；
- 终答侧元数据：`rg -n "finalAnswerModeration|final_answer_moderation|AgentMessageModeration|TargetedAnswerModeration" --glob '*.go' .` → **exit=1，0 命中**。

复现形状：
1. mock 的 responses 流里带 `final_answer_moderation`（item id 指向本次 final answer）；
2. 客户端收到 `item/completed` 的 agent message；
3. 期望：消息带 `moderation.moderated`（被拦时客户端能区分「被拦」与「空内容」，并能显示定向说明）；
4. 实际：字段不存在 → 客户端只能退化成「无内容 + turn 级 metadata」，无法定位到**哪一条**答案被审核。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| `protocol/exec_events.go:127 ThreadItem` | 新增 `Moderation *ItemModeration \`json:"moderation,omitempty"\`` | wire 面承载（Rust `AgentMessageItem.moderation: Option<...>`） |
| `protocol/exec_events.go:266 AgentMessageItemWithPhase` 与 `:282 AsyncAgentMessageItem` | 增加携带 moderation 的变体（保留原函数签名，避免大范围改调用方） | 构造点不破坏现有调用 |
| `appserver/protocol.go:4695`（`agent_message` 映射）与 `:6800`（`agentMessage` 分支） | 把上游 moderation 决定按 item id 匹配后写进 item | 这就是「仅当 id 匹配才挂」的那一步 |
| `model/responses_stream.go:2172-2174`（已读 `openai_chatgpt_moderation_metadata`） | 把 `moderation` 决定从 stream 事件透传到 item done 处理（现在只流到 turn 级） | 让 per-answer 决定有来源 |
| `state/thread_history_projection.go:401`（`agentMessage` 投影） + `rollout/canonical_item_completed.go:657`（content 重放） | 保留 item id + moderation 标记；「有决定时合并 text parts 成一条 legacy 事件」 | 冷加载/历史重建后仍能显示定向说明 |

**第二处消费点（四处，这组最容易漏）**：
1. `appserver/protocol.go:4695` 与 `:6800` 是**两条**映射路径（新/旧事件形状），改一条另一条仍丢字段；
2. `exec/exec.go:1149` / `:1294` / `:3265` / `:3318`（exec JSON 输出侧的 4 个构造点）；
3. `state/thread_history_projection.go:331`（`agentMessage` 搜索投影）与 `:401`（明细投影）；
4. `recordreplay/core.go:279`（录制-回放归一化）——SDK 差分测试会比较这里，漏掉会出现「真实链路有、回放没有」。

### ③ 失败模式

- **不看 item id 就挂**：一条 turn 里有多条 agent message（plan 模式、async delivery）时，moderation 会挂到错的那条 → 客户端显示「这条被审核了」但其实是另一条。
- **新 response 开始不清 pending decision**：上一条答案的审核结论泄漏到下一条（Rust 明确要 reset）。
- **合并 text parts 的规则用错触发条件**：Rust 是「有 moderation 决定时才合并成一条 legacy 事件」；无条件合并会改变所有历史事件的字节形状。

### ④ 最小 verifier

- 测试名：`TestFinalAnswerMessageCarriesTargetedModerationLikeRust`（0 命中）
- 断言：
  1. item id 匹配 → completed agent message 的 `moderation.moderated == true`；
  2. item id 不匹配（元数据指向另一条）→ 该消息 **无** `moderation` 字段（不是 `false`）；
  3. 没有元数据时序列化为**省略**（Rust schema 是 nullable，Go 用 `omitempty` + 显式 nil 区分）；
  4. plan 模式同样成立（Rust 两种模式都改）；
  5. 冷加载后从 rollout 重建，`moderation` 仍在（这就是「history 重建保留标记」）。
- 反证方式：把 id 匹配判断删掉（无条件挂）→ 断言 2 变红；把 plan 模式分支漏掉 → 断言 4 变红。

### ⑤ 依赖

- **在 `turn-context-required-permission-profile`(#59963) 之后**（都动 rollout/history 重放表）。
- 与 turn 级 `TurnModerationMetadata`（#57363，已 complete）**不冲突**：turn 级保留，本条是 per-answer 增量。

---

## 8. `account-security-setup-appserver-fetch` — #60082 `4be90eb069`

**判定保持**：`partial`。

### ① 行为差的精确复现路径

Rust 把取数搬进 app-server（`git show 748ab55ca7:codex-rs/app-server/src/request_processors/account_processor/security_setup.rs:22-105`）：3s 超时；只接受「saved ChatGPT 凭据 + 非 FedRAMP + saved token == 当前 auth token」；请求走 **runtime auth 解析后的 provider**（`:50-62`）；响应后再校验 `source_generation` 未变 + account_id/user_id 未变 + 非 FedRAMP，否则回 `None`。

Go 现状（`app/security_setup.go:16-66`）：
- 客户端进程自己 `store.Resolve()`（`:24`）→ 自拼 `Bearer`（`:38`）；
- runtime auth 只是 `RuntimeAuth: runtimeAuthProviderForCloud(...)` 的**可选注入**（`:42-46`）；
- 事后校验身份（`:53-59`）但**没有 `source_generation`**，因此「同一账号但凭据源换代」不会被识别；
- `rg -n "securitySetup|SecuritySetup" appserver/ protocol/` → **exit=1，0 命中**：经 app-server 连进来的远程/IDE 客户端**根本拿不到**安全提醒。

复现形状：
1. 起 app-server，客户端用 websocket/stdio 连入（不是本地 TUI）；
2. 触发「账户安全提醒」读取；
3. 期望：`account/securitySetup` 请求返回 `notice{title,description,action}` 或 `null`；
4. 实际：无此方法 → 远程客户端只能拿到「无提醒」。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| 新增 `appserver/account_security_setup.go: (r *RuntimeRouter) handleAccountSecuritySetup(ctx)` | 新增 | 移植 Rust 的 3s 超时 + 四条守卫（saved ChatGPT / 非 FedRAMP / token 相等 / source_generation 不变） |
| `appserver/schema.go`（请求表）+ `appserver/server_request.go` 类型 | 注册 `account/securitySetup` 与 `GetAccountSecuritySetupResponse{notice *Notice}` | 让 wire 面存在（Rust 是本条新增的 client request） |
| `app/security_setup.go:16-66` | 改为「调用 app-server 方法」的薄壳；本地 TUI 也走同一条路 | 消除「客户端自拼 Bearer」这条第二实现 |
| `appserver/runtime_router.go`（就近处，`:15273` 附近的 cloud/runtime 解析） | 复用 `runtimeauth` 解析（`Provider.HeadersForDestination`）而不是 `Bearer` 拼接 | 与 runtime auth 语义统一 |
| `chatgptapi` 侧的 `GetSecuritySetup` | 保留（被 appserver 调用），但调用方变成 appserver | 取数逻辑不复制 |

**第二处消费点（三处）**：
1. `tui/tea/security_setup.go`（对应 Rust 被精简的 `tui/src/security_setup.rs`）——TUI 也必须改成走 app-server，否则「远程客户端有了、本地 TUI 还是旧实现」；
2. `app/remote_tui.go` 里如果存在独立的提醒读取路径，必须一并改（本条要求「单一取数点」）；
3. `auth.AccountIDFromAuthForRestrictions`（`:38-40` 用它拼 header）——appserver 侧要用 runtime provider 的 header 生成，不能再调用它自拼。

### ③ 失败模式

- **去掉 source_generation 校验**：账号 A 的用户在读取过程中被换成账号 B（或切了凭据源），会把 A 的提醒显示给 B —— Rust 明确把 `source_generation` 当成一等守卫（`:79-90`）。
- **把 timeout 做成「无超时」**：安全提醒是可选 UI 元素，卡住会拖慢启动（Rust 3s）。
- **回退成 `null` 的语义搞错**：Rust 在「非 ChatGPT / FedRAMP / token 不匹配 / 非 2xx / JSON 解析失败」**全部**回 `None`，不是报错。改成报错会让启动/连接直接失败。

### ④ 最小 verifier

- 测试名：`TestAccountSecuritySetupServedFromAppServerLikeRust`（0 命中）
- 断言：
  1. 正常：app-server 返回 `notice`，且取数请求的 header 里 **没有**客户端自拼的 `Authorization: Bearer <token>`（而是 runtime/ provider 解析结果）；
  2. token 不匹配（saved ≠ 当前 auth）→ `null`，且**不发**取数请求；
  3. FedRAMP 账号 → `null`；
  4. 取数过程中 `source_generation` 变化 → `null`；
  5. 超过 3s 未响应 → `null`（不是错误、不阻塞连接）；
  6. 已有用例 `TestCloudClientGetSecuritySetupDropsInvalidNoticesLikeRust`、`TestSecuritySetupStaleIdentityAndBannerPriorityLikeRust` 保持绿。
- 反证方式：删掉 `source_generation` 比较 → 断言 4 变红；把 `null` 改成 error → 断言 3/5 变红。

### ⑤ 依赖

- 依赖 `managed-runtime-auth-mode`(#60014) 的 runtime provider 解析（若 requirements 强制 assertion_only，这条取数也必须是断言形状）。
- 与 `user-verification-platform-provider`(#43624) 无耦合。

---

## 9. `managed-runtime-auth-mode` — #60014 `10cf2b9ea2`

**判定保持**：`partial`。**这是本组唯一的「静默忽略企业策略」条目，你也已把它列为自己先改的一条。**

### ① 行为差的精确复现路径

Rust（`git show 748ab55ca7:codex-rs/core/src/config/managed_features.rs:365-396`）：
- requirements 里的 `features.runtime_agent_instances.mode` **赢**；
- 若 config.toml 显式写了不同 mode → **报错**（`ConstraintError::InvalidValue`，field `features.runtime_agent_instances.mode`）；
- 两边都没有 → 默认值。
解析侧（`config/src/config_requirements.rs:960-996`）：`FeatureRequirementsToml` 是「bool map + 一个 typed `runtime_auth_mode`」，`runtime_agent_instances` 作为 `FeatureToml<RuntimeAgentInstancesConfigToml>` 单独解析，`enabled` 折进 bool map，`mode` 单独保存。

Go 现状：
1. `config/requirements_file.go:568` 用 `boolMapAnyKey(values, "feature_requirements", "featureRequirements", "features")` 解析 `[features]`；
2. `boolMapAnyKey`（`:1518-1534`）**只收 bool**：`:1529-1531` 的 `if value, ok := boolAnyValue(rawValue); ok` 对 table 值静默跳过；
3. 因此 `[features.runtime_agent_instances] enabled = true` / `mode = "assertion_only"` **整段消失**；
4. `rg -n "runtime_agent_instances" config/` → **exit=1，0 命中**（`features/features.go:372` 只把它注册成 bool feature）；
5. 唯一消费点 `runtimeauth/config.go:70 Mode: ModeFromConfigValues(options.Values)` 读的是 **config.toml values**，永远看不到 requirements。

复现形状：
1. `requirements.toml` 写 `[features.runtime_agent_instances] enabled = true, mode = "assertion_only"`；
2. 宿主 `config.toml` 不写 mode（或不写 features）；
3. 期望（Rust）：请求用 `AgentAssertion`（assertion_only）；
4. 实际（Go）：`ModeFromConfigValues` 返回 `dual_header` → 请求同时带 actor 凭据与断言；企业「只允许断言」被静默绕过。
5. 反向：requirements 强制 assertion_only、config.toml 写 `mode = "dual_header"` → Rust **启动报错**；Go 静默用 dual_header。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| `config/requirements_file.go:568` | 在 `boolMapAnyKey` 之后**单独**解析 `runtime_agent_instances`（表或 bool 两种形状） | 恢复 `FeatureRequirement` 的 typed 分支 |
| 新增 `config/requirements_feature_toml.go: parseFeatureRequirement(raw any) (enabled *bool, mode *string)` | 新增 | 对齐 Rust `FeatureRequirementsInput`/`FeatureToml::enabled` 语义：`enabled=false` 的 table 也要折进 bool map |
| `config/api.go:613 ConfigRequirements` | 新增 `RuntimeAuthMode *string \`json:"-"\``（内部字段，不上 wire，照 `Permissions`/`AllowMXC` 的既有做法） | 承载 managed mode |
| `runtimeauth/config.go:70` | `ModeFromConfigValues(values)` → `resolveRuntimeAuthMode(values, requirements)`：requirements 有值时比对，冲突返回 error | managed wins + 冲突即拒绝 |
| `runtimeauth/manager.go:393 ModeFromConfigValues` | 保留（纯 values 语义，供无 requirements 的路径用） | 不破坏现有用例 `TestModeFromConfigValuesLikeRust` |

**第二处消费点（两处）**：
1. `config/config.go:2608-2620` 已经把 requirements 的 **bool** features 折进 `featureSettings`（所以 `enabled` 是生效的），但**没有任何 mode 通道**——修完解析后必须确认这里不会把 mode 当 bool 丢掉；
2. `runtimeauth.FeatureEnabled`（`runtimeauth/config.go:13-15`）只看 bool；若 requirements 里给了 `mode` 而没给 `enabled`，Go 必须像 Rust 一样「table 存在即视为 enabled 分支可解析」，否则会出现「mode 存在但 feature 关闭」的半开状态（Rust 的 `FeatureToml::enabled()` 对 Config 变体返回 `Some(true)`，需逐字核对后照抄）。

### ③ 失败模式

- **把 requirements 的 mode 当「建议」**：冲突时静默取其一 → 企业策略被绕过（本条的核心）。
- **把 mode 塞进 `map[string]bool`**：table 值被 `boolAnyValue` 丢掉的原始 bug 会换一种形式复现。
- **在 `ConfigRequirements` 上加 `json` tag**：会污染 app-server wire schema（Rust 的 `runtime_auth_mode` 是 config 内部字段，不在 `ConfigRequirements` wire 上）→ 违反「manifest/生成器按固定字段读」。

### ④ 最小 verifier

- 测试名：`TestRequirementsRuntimeAuthModeIsEnforcedOrRejectedLikeRust`（0 命中）
- 断言：
  1. requirements 给 `mode = assertion_only` + config.toml 无 mode → 解析出的 provider `Mode == assertion_only`，且实际请求头**只有** `AgentAssertion`（无 `Authorization: Bearer`）；
  2. requirements 给 assertion_only + config.toml 给 dual_header → **返回错误**（错误串含字段名 `features.runtime_agent_instances.mode`）；
  3. requirements 只有 `enabled = true`（无 mode）+ config.toml 给 mode → config.toml 的 mode 生效；
  4. requirements 与 config.toml 都没有 → `dual_header`（默认）；
  5. `[features] runtime_agent_instances = true`（纯 bool 写法）仍能解析（向后兼容）。
- 反证方式：把 `runtimeauth/config.go:70` 改回 `ModeFromConfigValues(options.Values)` → 断言 1 变红；把冲突分支改成「取 requirements」→ 断言 2 变红。

### ⑤ 依赖

- **必须早于** `runtime-auth-terminal-denials`(#60096) 的 assertion 模式用例与 `realtime-runtime-auth`(#60003) 的 mode 参数化。
- 你已决定自己改这条（不走 analysis lane），所以本文件只给规格与验收口径。

---

## 10. `mcp-network-policy-derivation` — #60046 `b7d2321ef7`

**判定保持**：`partial`。**本条要拆两批验收**（③④ 现在就活；①② 在 pin 上仍是 dead code）。

### ① 行为差的精确复现路径

Rust 本条有四个子改动，存活状态不同：

| 子项 | Rust 位置 | 在 `748ab55ca7` 上的存活状态 |
|---|---|---|
| ① 从 controller/environment policy + permission profile + approval + saved rules **推导** MCP 网络策略 | `core/src/config/network_proxy_spec.rs:389 NetworkProxySpec::mcp_network_policy` | **dead code**：`git grep mcp_network_policy 748ab55ca7` 只命中测试文件（`config/mcp_network_policy_tests.rs:39/143/219`、`session/network_policy_tests.rs:61/166`、`config/network_proxy_spec_tests.rs:910`）与 `#[path]` 声明 |
| ② 陈旧 grant 不得盖掉有效 deny；畸形批次要保留裸 deny | 同 commit 的 `exec_policy.rs` / `network-proxy` | ✅ 活（有 `session/network_policy_tests.rs` 覆盖 managed veto / stale grant） |
| ③ **deny 先发布、grant 后发布**（写盘失败时 deny 仍生效） | `core/src/exec_policy.rs:566-640`（`apply_network_rule_and_persist`） | ✅ 活 |
| ④ 运行时更新与落盘**串行**（`update_lock` + semaphore） | 同处 `:570-580` | ✅ 活 |

Go 现状（四处）：
- **②③④ 缺失且是安全语义**：`appserver/network_approval.go:1492 persistNetworkPolicyAmendment` 只做 `execpolicy.AppendNetworkRule(...)`（`:1512`）——**先落盘、无锁、无内存发布、无 proxy denylist 更新**；失败时只是 warn + 记一条 policy denial（`:525-531`）。
- **① 只有接口没有实现**：`mcp/network_policy.go:36 NetworkPolicyMetadataProvider` 定义了 `NetworkPolicyMetadataForCall(profile, environmentID)`，但 `rg -n "NetworkPolicyMetadataForCall" --glob '*.go'` 的 3 处命中里**生产代码只有接口声明与调用点**（`:36`、`:119`），唯一实现是测试替身 `mcp/network_policy_like_rust_test.go:24`。也就是说：MCP 侧的**发布通道齐了，推导函数没有**。

复现形状（③，可直接写测试）：
1. turn 内批准一次 `networkPolicyAmendment`，`action = deny`，host = `blocked.example.com`；
2. 让 `execpolicy.AppendNetworkRule` 返回 error（磁盘只读 / 路径不存在）；
3. 期望（Rust）：内存策略**已**包含该 deny（`if decision == Decision::Forbidden { self.policy.store(...) }` 在写盘之前），后续同 host 请求仍被 deny，且代理 denylist 也加了；
4. 实际（Go）：只 warn + `recordPolicyDenialForThread`，内存策略与后续判决不变；若该 host 之前被 allow（陈旧 grant），则会继续放行。

复现形状（④，并发）：
1. 两个 turn 同时提交 grant/deny 修正案；
2. 期望：串行化，最终内存策略 = 落盘后的一致快照；
3. 实际：两次 `AppendNetworkRule` 各自追加，内存里没有「读-改-写」窗口保护。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| `appserver/network_approval.go:1492 persistNetworkPolicyAmendment` | 重写为 `applyNetworkRuleAndPersist`：①持锁（新增 `policyUpdateMu` + 通道信号量）②从当前 Policy 克隆并 `add_network_rule` ③deny 先 `store` 到内存 + 更新代理 denylist ④再写盘 ⑤写盘成功后发布 grant + allowlist | 一次性把 ③④ 做对 |
| `execpolicy/`（`Policy` 增 `WithNetworkRule` 克隆式 API） | 新增 | Rust 是「克隆 → 改 → `Arc` 替换」，Go 现在直接 append 到共享 slice |
| `network/`（`Spec`/`Config` 增 `AddDeniedDomain`/`AddAllowedDomain` 的运行时入口） | 新增 | Rust 在 `proxy.rs` 里对运行中的代理加 domain；Go 只有 `EnvironmentNetworkPolicy.ApplyTo`（`network/network.go:191`）这种静态组合 |
| 新增 `mcp/network_policy_derivation.go: (*Spec).MCPNetworkPolicy(...)` | 新增（**②③④ 落地后再做**） | 对应 Rust `NetworkProxySpec::mcp_network_policy`，产出 `NetworkPolicyMetadata` |
| `appserver/*`（把 derivation 注入 `mcp.MCPService.networkPolicyMetadata`） | 接线 | 让 `mcp/network_policy.go:57 networkPolicyAdvertisedForServer` 有真实 source |

**第二处消费点（三处，都在审批面）**：
1. `appserver/runtime_router.go:20005-20011`（**命令**审批的 `applyNetworkPolicyAmendment`）——现状只返回 `Approved true/false`，**完全不落盘**！这是比文件变更路径更彻底的缺失，必须同批修。
2. `appserver/network_approval.go:513` 与 `:665` 是两条独立 decision 分支（审批流 / 结果流），都要走同一个 `applyNetworkRuleAndPersist`。
3. `execpolicy/inheritance.go:116-122` 的 managed-denial 收敛检查只比「managed 要求 vs 快照」；若运行时发布路径绕过它，managed deny 会被陈旧 grant 架空——发布前必须复用这段比较。

### ③ 失败模式

- **deny 后发布（先写盘再发布）**：写盘失败时 deny 丢失，就是 Rust 修掉的那个漏洞（「失败写入让内存策略与原生强制不一致」）。
- **grant 也先发布**：用户批准的 grant 在写盘失败后仍生效，重启后消失 → 行为不可预测；Rust 明确 grant 只在成功后发布。
- **在锁内做网络/IO**：`spawn_blocking` 之外持锁会卡死整个审批流（Rust 把落盘放 `spawn_blocking`，锁只覆盖内存段）。
- **①提前上线**：Rust 的推导函数在 pin 上没有任何生产调用者，Go 先落地就等于「按测试的期望值实现一个上游还没接线的行为」——一旦上游接线 PR 改了推导细节，我们同步成本翻倍。**这就是我把 ①② 单列的原因。**

### ④ 最小 verifier

分两条测试（对应两批验收）：

- ③④（现在可登记并落地）：
  - 测试名：`TestNetworkRuleAmendmentPublishesDenialBeforePersistLikeRust`（0 命中）
  - 断言：①deny 修正案写入失败 → 内存 Policy 与代理 denylist **仍**含该 host，后续请求被 deny；②grant 修正案写入失败 → 内存 **不含** grant；③两个并发修正案的最终内存状态等于落盘顺序的最终状态；④deny 修正案成功 → 落盘内容与内存一致（可用临时 policy 文件读回比对）。
  - 反证方式：把「deny 先 `store`」的那一行挪到写盘之后 → ①变红；把 grant 的 `store` 挪到写盘之前 → ②变红。
- ①②（等上游接线）：
  - 测试名：`TestMCPNetworkPolicyDerivesFromSpecAndKeepsSavedDenialsLikeRust`（0 命中）
  - 断言照抄 Rust `config/mcp_network_policy_tests.rs` 的 7 行表：`restricted offline` / `controller proxy` / `managed proxy` / `managed disable` / `external network` / `disabled sandbox` / `managed veto in disabled sandbox` → 每行给出 `enabled/mode/allowedDomains/deniedDomains/hardDenyAllowlistMisses`。
  - **建议**：这条等上游把 `mcp_network_policy` 接进生产调用栈后再落（否则我们是在对齐一个 dead_code 的期望值）。

### ⑤ 依赖

- ③④ 独立，可立刻开工。
- ①② 依赖上游「接线 PR」；在它出现前，本条 status 保持 `partial`，不因 ③④ 变 complete（这也是你之前的验收口径）。

---

## 11. `realtime-runtime-auth` — #60003 `44f1b0e867`  ｜ 与 §12 同批

**判定保持**：`partial`。**G-RT 的第一半。**

### ① 行为差的精确复现路径

可写成测试的三步（对齐 Rust `runtime_auth_denials.rs` 的 harness 思路 + 现有 Go fake WS 服务）：

1. 起一个 fake WS endpoint（`httptest.NewServer` + `websocket.Accept`，形状见 `realtime/existing_call_test.go:81-95`），记录**每次**握手的 `Authorization` / `x-openai-agent-assertion` / `x-runtime-auth`；
2. 打开 `RuntimeAgentInstances`，runtime 注册端点用 `runtimeauth` 的 fake registration server（`runtimeauth/runtimeauth_test.go:337 newFakeRegistrationServer`），返回 `runtime_agent_instance_id` + actor；
3. `realtime/start`（websocket 传输）→ 观察握手头。

期望（Rust）：
- `dual_header` 模式：`Authorization: Bearer <actor 凭据>` + `x-openai-agent-assertion: AgentAssertion <proof>`；
- `assertion_only` 模式：**只有** `Authorization: AgentAssertion <proof>`，且不允许同时带 `x-openai-agent-assertion`（Rust 测试 `runtime_auth_denials.rs:129-140` 就是这么断言的）；
- runtime 未初始化且 feature 打开 → **报错**（`"runtime authentication is not initialized"`），不是静默降级；
- 目标不在 OpenAI realtime host / 环境不匹配（`validate_runtime_environment`）→ 不注入 runtime 凭据，保持原有 key 生命周期。

实际（Go）：`Authorization: Bearer <静态 apiKey>`（`appserver/realtime_runtime.go:463`），`rg -n "runtimeauth" appserver/realtime_runtime.go realtime/ --glob '*.go'` → **exit=1，0 命中**。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| `appserver/realtime_runtime.go:415-431` | 保留 `realtimeAPIKey` 作为**回退**；新增分支：feature 开 && `runtimeauth.ClassifyOpenAIDestination(wsBaseURL) == DestinationSupported` → `runtimeauth.ProviderForHarness(...)`，未初始化则报错 | 与 Rust `:1426-1430` 对齐（硬报错而非静默） |
| `appserver/realtime_runtime.go:456-467` | 不再把 token 写死进 `headers`；改为挂 `AuthProvider`（新字段）到 `TransportBackendConfig` | 一次性捕获 → per-handshake 解析 |
| `realtime/transport.go:40-70 TransportBackendConfig` | 新增 `Auth func(ctx) (http.Header, error)`（或 `AuthProvider` 接口）与 `CallAuth` | 给 WS 与 call 两条面各一个 per-request 钩子 |
| `realtime/wire.go:40-45` | `headers := backend.Headers.Clone()` 之后：`if backend.Auth != nil { 合并 backend.Auth(ctx) }` | **每次 dial 重解析**（#60055 的落点，见 §12） |
| `realtime/transport.go:394-400 newRealtimeSideband` | 克隆时**不要**把 `CallHeaders` 固化成 `Headers`；改为继承 `CallAuth` 钩子 | 干掉 §0 证据链第 4 步的静态化 |
| `realtime/transport.go:276-287`（WebRTC POST） | 保留 `PrepareCall`，并让它与 `CallAuth` 共用一个 resolver | WebRTC 侧已能 per-request 签名，只差 runtime 来源 |

**第二处消费点（三处，缺一就会出现你担心的中间态）**：
1. `realtime/transport.go:399`（sideband 的 `Headers ← CallHeaders`）——这是最隐蔽的一处，现有用例 `TestExistingCallSidebandPrefersPerCallEndpointLikeRust`（`realtime/existing_call_test.go:66`）已锁死「sideband 用 CallHeaders 且 `Authorization` 为空」，所以改造必须保留这个契约（无 Auth 钩子时行为不变）。
2. `realtime/transport.go:276`（call 请求 `request.Header = backend.CallHeaders.Clone()`）——call 面与 WS 面必须用同一个 resolver，否则「主 WS 用新断言、call 用旧 Bearer」。
3. `appserver/realtime_runtime.go:456`（`headers := websocketProvider.Headers.Clone()`）——`websocketProvider.Headers` 里可能已有 provider 级 header（如 FedRAMP），runtime 头必须**叠加**而不是覆盖。

### ③ 失败模式

- **只改 WS 不改 sideband**：主连接换新断言，sideband 仍用旧 Bearer → 依托 sideband 的实时语音在轮换后立刻 401，而主连接正常（最难排查的一种）。
- **解析失败当网络错误**：runtime 注册失败（403 `runtime_instance_revoked`）若被包装成 `connect realtime sideband: dial ...`，会触发 `sidebandShouldReconnect` 的无上限重连（`realtime/transport.go:435-443`），把「终态拒绝」变成「重连风暴」。**必须用 §2 的 `isRuntimeAuthDenial` 分流成终态。**
- **静默降级到无鉴权**：feature 开但 manager 为 nil 时退回静态 key，等于在要求断言的部署里发 Bearer。
- **覆盖 `Authorization` 而不是替换模式**：`assertion_only` 下若同时留 `x-openai-agent-assertion`，Rust 测试会判失败（`:129-140`）。

### ④ 最小 verifier

- 测试名：`TestRealtimeWebsocketResolvesRuntimeAuthPerHandshakeLikeRust`（0 命中）
- 断言：
  1. `dual_header`：握手 `Authorization` 以 `Bearer ` 开头，且带 `x-openai-agent-assertion`；
  2. `assertion_only`：握手 `Authorization` 以 `AgentAssertion ` 开头，且**没有** `x-openai-agent-assertion`；
  3. 同一个 session 内 sideband 与主 WS 的凭据来源一致（两处 header 都能追溯到同一次 runtime 解析）；
  4. feature 打开但 runtime 未初始化 → `realtime.Start` 返回错误（不是静默 Bearer）；
  5. 现有用例保持绿：`TestExistingCallSidebandPrefersPerCallEndpointLikeRust`、`TestRealtimeSidebandReconnectHelpersLikeRust`。
- 反证方式：把 `realtime/wire.go:40` 的 Auth 合并删掉（回到纯静态）→ 断言 3 变红；把 `:415-431` 的「未初始化报错」改成「回退静态 key」→ 断言 4 变红。

### ⑤ 依赖

- **必须与 §12 同批**（同一处 `realtime/transport.go:399` + `realtime/wire.go:40`）。
- 依赖 §9(#60014) 提供 mode；依赖 §2(#60096) 的 denial 分类来避免重连风暴。

---

## 12. `runtime-auth-websocket-revalidation` — #60055 `d4a341bd52`  ｜ 与 §11 同批

**判定保持**：`partial`。**G-RT 的第二半。**

### ① 行为差的精确复现路径

Rust 契约（`git show 748ab55ca7:codex-rs/core/src/client.rs:314-320` 原文）：

> Runtime-authenticated requests reconnect between requests to revalidate the registered credential source.

配套字段 `:369 last_connected_auth_revision`、`:1896-1914` 的连接复用判定：owner 变了或 revision 变了 → `reset()` + 重新连接，并把 `reauth` 传给 telemetry。Guardian 采样池另有等价逻辑（`ext/guardian-v2/src/async_scorer/sampler/connection_pool.rs:350/398-419/603-617`：`auth_changes` watch 通道触发重建）。

复现形状（可写测试）：
1. 建立 responses WS（或 realtime sideband），握手成功；
2. **轮换 runtime 凭据**（fake 注册端点下一次返回新的 `runtime_agent_instance_id`，或把 manager 的 `SourceGeneration` 递增）；
3. 触发重连（realtime：让 sideband 断链；model：下一次同 turn 请求）；
4. 期望（Rust）：重连握手带**新**断言；且 `reauth=true` 被记录；
5. 实际（Go）：`realtime/transport.go:426` 循环 → `realtime/wire.go:40` 只是把同一份快照再 Clone 一次 → 旧断言原样复用；`rg -n "lastConnectedAuthRevision|authRevision" realtime/ appserver/realtime_runtime.go` → 0 命中（Go 的等价物在 `model/responses_agent.go:1129 session.markConnectedAuth(r.AuthSnapshot)`，只服务 model WS）。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| `realtime/wire.go:35-45 dialRealtimeTransport` | 每次 dial 调 `backend.Auth(ctx)`；把结果与「上次成功握手的来源版本」比较，记录 `reauth` | per-reconnect 重解析 + 可观测的 reauth 标记 |
| `realtime/transport.go:404-470 startRealtimeSideband` | 重连循环里不缓存凭据；对 `ErrSourceChanged` 类错误**不要**计入 `rapidDisconnects` 退避 | 防止「凭据换代 → 指数退避 → 长时间不可用」 |
| `realtime/transport.go:394-400 newRealtimeSideband` | 同 §11（去掉静态固化） | 与 §11 同一处改动 |
| `runtimeauth/manager.go:168 Invalidate` / `:365 SourceGeneration` | 供 realtime 侧判断「来源是否换代」 | 现成能力，接上即可 |
| `model/responses_agent.go:1129 markConnectedAuth`（已有） | 对齐语义：Rust 用 revision 而非 snapshot 指针比较；Go 现在比的是 `AuthSnapshot` | 避免「同内容不同对象」被误判为 reauth |

**第二处消费点（两处）**：
1. `model/responses_agent.go:1896`-等价逻辑（Go 在 `:1129` 附近）——**model 侧的 WS 复用判定已存在**，本条不能把它改坏；现有 verifier `TestWebsocketReauthTracksLastConnectedCredential` 必须保持绿。
2. `ext/guardian-v2` 等价物：Go 的 guardian 采样池在 `appserver/guardian_reviewer.go` 附近（有 `TestEnsureGuardianReviewerPrewarmsOnceAndFallsBackLazily`）；Rust 本条把 `auth_changes` watch 也接到采样池，Go 若只改 responses/realtime，guardian 仍复用旧凭据。

### ③ 失败模式

- **每次请求都重连**（把「revision 变」写成「总是 reauth」）：连接抖动，sticky-routing token 每 turn 重建，指标全红。
- **把换代错误计入退避**：`runtime_instance_revoked` 是终态，计入 `rapidDisconnects` 会变成无限重试。
- **只比较「token 字符串」**：同一账号重新登录后 token 可能相同（缓存），但 `source_generation` 已变——Rust 比的是 revision。

### ④ 最小 verifier

- 测试名：`TestRealtimeSidebandReconnectRevalidatesRuntimeAuthLikeRust`（0 命中）
- 断言：
  1. 第一次握手头 = 断言 A；轮换后第二次握手头 = 断言 B，且 **B ≠ A**；
  2. 未轮换时重连，`reauth` 标记为 false（不得每次都 reauth）；
  3. 轮换后重连的 `reauth` 标记为 true（telemetry 可观测）；
  4. runtime 注册返回 403 终态 → 循环**退出**（不再按 backoff 重试）；
  5. 现有 `TestWebsocketReauthTracksLastConnectedCredential`、`TestRealtimeSidebandReconnectHelpersLikeRust` 保持绿。
- 反证方式：把 `realtime/wire.go` 的 per-dial 解析去掉 → 断言 1 变红；把终态分流删掉 → 断言 4 变红（观察 sideband 在 5s 内重试次数 > 1）。

### ⑤ 依赖

- 与 §11 同批（同一处代码）。
- 依赖 §2(#60096)：没有 denial 分类就无法区分「终态」与「可重连」。

---

## 13. `remote-control-runtime-auth` — #60073 `f4275a2a2e`

**判定保持**：`partial`。

### ① 行为差的精确复现路径

Go 现状：`remotecontrol` 的凭据是**快照**：
- `remotecontrol/auth.go:30 AuthHeaders *model.AuthHeaders`（快照字段）；
- `:41 model.AuthHeadersFromAuth(*snapshot)`（一次性转换）；
- `:457 RequestHeaders()` 把快照 header 逐条复制（WS 握手用）；
- `:480 ApplyRequest()` 先覆盖头再 `AuthHeaders.ApplyRequest`（per-request 只重签、不重解析）。

复现形状：
1. 打开 `RuntimeAgentInstances`，远端控制连接建立；
2. runtime 实例轮换（fake 注册端点换 id）或同一 owner 换凭据源（`SourceGeneration` 变）；
3. 期望（Rust `app-server-transport/src/transport/remote_control/auth.rs` + `mod.rs`）：后续请求带新 runtime 断言；目标不在允许 host → 不注入；denial code → 分类上报；
4. 实际（Go）：`RequestHeaders()` 继续吐旧头；`rg -n "runtimeauth" remotecontrol/ --glob '*.go'` → **exit=1，0 命中**。

注意：Go **已有** `remotecontrol/websocket_loop.go:323 retireForAuthOwnerChange`（Rust #44341），会在「auth owner 变化」时退役连接——这覆盖了「换登录」，但**不覆盖**「同一 owner 下的 runtime 实例轮换/凭据源换代」，而且退役后重连用的还是快照头。

### ② Go 侧最小改动面

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| `remotecontrol/auth.go:27-55 RemoteControlConnectionAuth` | 新增可选 `RuntimeProvider`（`runtimeauth.Provider`）与 `SourceGeneration`；`NewRemoteControlConnectionAuthFromSnapshot` 里按 feature 注入 | 保留快照作为回退（无 runtime 时行为不变） |
| `remotecontrol/auth.go:457 RequestHeaders()` | runtime 可用时：先解析 runtime 头（`HeadersForDestination`），与快照头合并（runtime 头优先） | WS 握手走 per-connection 解析 |
| `remotecontrol/auth.go:480 ApplyRequest()` | 同上，且把解析错误分类（denial → 终态） | per-request 重解析 |
| `remotecontrol/websocket_loop.go:323 retireForAuthOwnerChange` | 增加「`SourceGeneration` 变化」也是退役原因 | 覆盖 runtime 轮换 |
| `remotecontrol/server_api.go:190` | 无需改（已走 `auth.ApplyRequest`） | 单点受益 |

**第二处消费点（两处）**：
1. `remotecontrol/websocket_loop.go:148/178/197` 三处 `retireForAuthOwnerChange` 调用点——退役判定必须统一，否则「某条路径退役、另一条不复核」；
2. `model.AuthHeadersFromAuth` 的其它调用方（`rg -n "AuthHeadersFromAuth" --glob '*.go'`）——若有其它传输面也在用快照头，同样要接 runtime，否则形成一个混用快照与断言的面。

### ③ 失败模式

- **在 `RequestHeaders()` 里解析 runtime 但不校验目标 host**：把断言发给非 OpenAI host（Rust 有 `ClassifyOpenAIDestination`/`HeadersForDestination` 双重守卫）。
- **把 403 denial 当成「退役重连」**：`runtime_instance_revoked` 会变成无限重连（与 §12 同一类坑）。
- **改坏快照回退**：无 runtime 的普通用户会拿不到 `ChatGPT-Account-ID`（`:477` 强制要求非空且合法），直接连不上。

### ④ 最小 verifier

- 测试名：`TestRemoteControlRequestsCarryRuntimeAuthPerRequestLikeRust`（0 命中）
- 断言：
  1. runtime 打开时，`RequestHeaders()` 与 `ApplyRequest()` 产出的头都含 runtime 断言（`assertion_only` 下不得含 `Authorization: Bearer`）；
  2. runtime 轮换后（fake 注册端点返回新 id）→ 新请求头变化；`ApplyRequest` 两次调用之间若 `SourceGeneration` 变化，第二次必须是新值；
  3. `remote_control` 目标不在允许 host → 不注入 runtime 头（保持快照头）；
  4. 现有 `TestRemoteControlWebsocketLoopRetiresOnAuthOwnerChangeWhileConnected` 保持绿。
- 反证方式：把 `RequestHeaders()` 的 runtime 分支删掉 → 断言 1 变红；把 host 守卫删掉 → 断言 3 变红。

### ⑤ 依赖

- 依赖 §2(#60096) 的 denial 分类与 §9(#60014) 的 mode。
- 与 §11/§12 **不必同批**：传输面不同（remotecontrol 是独立 WS + HTTP），但**必须复用同一个 resolver 接口**，否则两套语义会分叉。

---

## 14. `user-verification-platform-provider` — #43624 `e7637306bc`

**判定保持**：`partial`，lane = **macos**。**本条我在 linux_node 上无法验证**（见 §15）。

### ① 行为差的精确复现路径

Rust 本条新增 macOS 平台 provider（`git show e7637306bc --stat`）：`user-verification/src/platform_macos/{provider.rs(293 行), key_protection.rs, error.rs}` + `lifecycle_lock.rs` + `native_operation.rs`。

Go 现状：
- 注入点**已存在**：`appserver/user_verification_runtime.go:147 platformProvider()`；
- 缺实现：`rg -n "DarwinUserVerification|SecureEnclave|platform_macos" --glob '*.go'` → **exit=1，0 命中**；
- 该契约登记的 6 个 verifier **全部 0 命中**（我逐个 `rg "func <name>\b"` 验过）：

| verifier | 现状 |
|---|---|
| `TestDefaultRuntimeRouterInstallsUserVerificationPlatformProvider` | 0 命中 |
| `TestUserVerificationDeviceSupportPrefersInjectedProvider` | 0 命中 |
| `TestDarwinUserVerificationKeyInfoMatchesSecurityFrameworkExport` | 0 命中 |
| `TestDarwinUserVerificationErrorClassificationCoversRustTable` | 0 命中 |
| `TestDarwinUserVerificationLockTimeoutKeepsHolderLockFile` | 0 命中 |
| `TestDarwinUserVerificationProviderStatusBranches` | 0 命中 |

复现形状（**需要 macOS**）：`user-verification/status` 在 Darwin 上应返回设备能力（`supported` / `unavailable(reason)`）+ key 元数据；`enroll` 走 Secure Enclave 生成密钥；`native operation` 带 lifecycle lock（锁超时保留 holder 的 lock 文件）。

### ② Go 侧最小改动面（规格，我无法在本地验）

| 函数 | 改动 | 一句话说明 |
|---|---|---|
| 新增 `appserver/user_verification_darwin.go`（`//go:build darwin`） | 实现 `UserVerificationProvider`（`appserver/user_verification_runtime.go` 的接口） | 注入点已就绪，只需实现 |
| `appserver/user_verification_runtime.go:147` 附近的默认装配 | `NewDefaultRuntimeRouter` 在有 provider 注册时安装（对应 `TestDefaultRuntimeRouterInstallsUserVerificationPlatformProvider`） | 「默认路由必须装上平台 provider」 |
| 新增 `appserver/user_verification_lock.go` + `_darwin`/`_other` | lifecycle lock（锁文件 + 超时后**保留** holder 文件） | 对齐 Rust `lifecycle_lock.rs` |
| 新增错误分类表 | 对齐 Rust `platform_macos/error.rs` 的分类表（逐条） | 错误文案与分类要能与 Rust 对照 |

**第二处消费点**：`appserver/user_verification_runtime.go:139-146 userVerificationPeerRestricted` —— 平台 provider 只在**本地**连接上可用；网络 peer 只能读 status（Rust `ConnectionOrigin::WebSocket | RemoteControl` arm）。实现时若忘了这条，远程客户端能触发 Secure Enclave 签名。

### ③ 失败模式

- **在非 Darwin 平台注册 provider**：CI 上会因为「找不到 Secure Enclave」而整片失败（必须 `//go:build darwin` + 非 Darwin 编译期返回 `UnsupportedUserVerificationProvider`）。
- **锁超时不删文件 vs 删文件搞反**：Rust 明确「超时保留 holder 的 lock 文件」（便于诊断死锁），删掉会掩盖问题；反过来永久不清理会卡死下一次 enroll。

### ④ 最小 verifier（不能本地跑，需 macOS）

- 6 个测试名已登记在 manifest（见上表），全部**需先落测试**；其中三个与 Darwin 强绑定：
  - `TestDarwinUserVerificationKeyInfoMatchesSecurityFrameworkExport`（需与 `security` 框架导出逐字段比对）
  - `TestDarwinUserVerificationErrorClassificationCoversRustTable`
  - `TestDarwinUserVerificationLockTimeoutKeepsHolderLockFile`
- 反证方式：把 provider 的 status 分支改坏 → `TestDarwinUserVerificationProviderStatusBranches` 变红；把 `//go:build darwin` 去掉 → linux CI 立刻红（这本身就是一条护栏）。

### ⑤ 依赖

- 与被 blocked 的 `#43624` 同源：**blocked_precondition 的是「架构性 blocked」清单里的 macOS Secure Enclave 项**（`parity/commits.json` 里 sha `e7637306bc` 的状态是 `blocked_precondition`），契约 `user-verification-platform-provider` 是它的 `partial` 投影。
- 必须先有 macOS 主机与签名能力，才能把契约从 `partial` 推到 `complete`。

---

## 15. 本地判不了 / 需要条件的条目

| 条目 | 卡点 | 需要的条件 | 在此之前能做什么 |
|---|---|---|---|
| `user-verification-platform-provider`(#43624) | macOS Secure Enclave 独占；`//go:build darwin` 的真实签名/密钥接口在 linux 上不存在 | 一台 macOS 主机（Apple Silicon，可访问 `security` 框架与 enclave）+ 签名能力；且需要 `parity/commits.json` 中 `e7637306bc` 从 `blocked_precondition` 解封 | 只能做「非 Darwin 编译期返回 `UnsupportedUserVerificationProvider`」这一半 + 错误分类表的纯逻辑部分（无平台调用）；6 个 verifier 里只有 `TestUserVerificationDeviceSupportPrefersInjectedProvider`、`TestDefaultRuntimeRouterInstallsUserVerificationPlatformProvider` 有条件在 linux 上用 fake provider 先写 |
| `mcp-network-policy-derivation`(#60046) 的 ①② | Rust `NetworkProxySpec::mcp_network_policy` 在 pin 上**无生产调用者**（`git grep` 只命中测试） | 上游把推导接进生产调用栈的那个 PR；否则我们只能对着测试期望值实现 | ③④ 现在就能落地+验收（§10 的第一条 verifier） |
| `realtime-runtime-auth`(#60003) 的「真实 runtime agent instance」端到端 | 真实注册端点不可达/不稳定 | 不需要：用 `runtimeauth` 的 fake registration server（`runtimeauth/runtimeauth_test.go:337`）+ httptest WS 即可覆盖除「真实服务端接受断言」外的全部逻辑 | 「服务端真的接受 `AgentAssertion`」这一步需要真实 runtime 环境，但它不是 Go 侧的判定点 |
| `runtime-auth-websocket-revalidation`(#60055) 的「真实轮换」 | 真实凭据轮换需要服务端配合 | 同上，用 fake 注册端点递增 `runtime_agent_instance_id` + 手动 `SourceGeneration` 递增模拟 | 「真实服务端在轮换后拒绝旧断言」需真实环境，Go 侧只需证明「重连带新值」 |
| `managed-runtime-auth-mode`(#60014) 的「企业下发」 | 需要真实 managed requirements 下发通道 | 不需要：requirements 解析是纯函数，用临时 `requirements.toml` 即可 | 端到端「企业控制台改配置→宿主生效」属验证性测试，不在判定点 |

---

## 16. 复现与核对命令（我本轮实跑的输出）

### 16.1 数据源与 Rust 锚点存在性

```bash
# Rust 检出必须是真 git 树（tar 快照没有 .git，相关 parity 用例会红）
git -C /home/jacks/jacks_dev/codex rev-parse HEAD
# → 748ab55ca7de0b647d9a7a9d9a8e70e00f28666d

# 13 条契约的 rustPaths（35 个）在 pin 上逐个存在性
# → rustPaths total 35 missing 0
```

### 16.2 Go 侧 goPaths 与 verifier 存在性

```bash
# 13 条契约的 goPaths（33 个）存在性（脚本逐条 os.path.exists）
# → checked 33 paths，0 MISS

# 13 条契约登记的 verifier（manifest 的 verifier 字段按 ';' 拆）：24 个
# → 18 个能在树里找到同名 func，6 个 0 命中（全部属 user-verification-platform-provider）
#   TestDefaultRuntimeRouterInstallsUserVerificationPlatformProvider
#   TestUserVerificationDeviceSupportPrefersInjectedProvider
#   TestDarwinUserVerificationKeyInfoMatchesSecurityFrameworkExport
#   TestDarwinUserVerificationErrorClassificationCoversRustTable
#   TestDarwinUserVerificationLockTimeoutKeepsHolderLockFile
#   TestDarwinUserVerificationProviderStatusBranches
```

### 16.3 关键零命中（本次深挖的事实基础）

```bash
cd <Go 主干>
rg -n --glob '*.go' "runtimeauth" appserver/realtime_runtime.go realtime/     # exit=1, 0 hits
rg -n --glob '*.go' "runtimeauth" remotecontrol/                              # exit=1, 0 hits
rg -n --glob '*.go' "isRuntimeAuthDenial|RuntimeAuthDenial"                   # exit=1, 0 hits
rg -n --glob '*.go' "finalAnswerModeration|final_answer_moderation|AgentMessageModeration|TargetedAnswerModeration"  # exit=1, 0 hits
rg -n --glob '*.go' "securitySetup|SecuritySetup" appserver/ protocol/        # exit=1, 0 hits
rg -n --glob '*.go' "DarwinUserVerification|SecureEnclave|platform_macos"     # exit=1, 0 hits
rg -n --glob '*.go' "PermissionProfile" protocol/                             # exit=1, 0 hits
rg -n --glob '*.go' "protectedModelDowngrade|startupDowngrade" appserver/     # exit=1, 0 hits
rg -n --glob '*.go' "CancelPatchApproval"                                     # exit=1, 0 hits
rg -n --glob '*.go' "runtime_agent_instances" config/                         # exit=1, 0 hits
# 全树唯一命中：features/features.go:372（注册为 bool feature）、runtimeauth/{config,manager}.go、测试
```

### 16.4 拟新增的 13 个 verifier 名（当前全树 0 命中，可安全登记）

```
TestRuntimeAuthDenialsAreTerminalButOrdinary401RecoversLikeRust
TestFileChangeApprovalCancelInterruptsOnlyRequestingTurnLikeRust
TestChildAgentSpawnReadsParentCatalogFromMemoryLikeRust
TestProtectedModelStartupDowngradeUsesPermissionProfileLikeRust
TestTurnContextAlwaysPersistsPermissionProfileLikeRust
TestFinalAnswerMessageCarriesTargetedModerationLikeRust
TestAccountSecuritySetupServedFromAppServerLikeRust
TestRequirementsRuntimeAuthModeIsEnforcedOrRejectedLikeRust
TestNetworkRuleAmendmentPublishesDenialBeforePersistLikeRust
TestMCPNetworkPolicyDerivesFromSpecAndKeepsSavedDenialsLikeRust
TestRealtimeWebsocketResolvesRuntimeAuthPerHandshakeLikeRust
TestRealtimeSidebandReconnectRevalidatesRuntimeAuthLikeRust
TestRemoteControlRequestsCarryRuntimeAuthPerRequestLikeRust
```
（`rg -c "func <name>\b" --glob '*.go' .` 每条均为 0；形如 `TestXxx` 的 13 个名字与现有 3584+ 条用例无冲突。）

### 16.5 Rust 侧关键证据（`git show 748ab55ca7:<path>`）

| 主题 | 位置 | 关键行 |
|---|---|---|
| realtime per-handshake 鉴权 | `codex-rs/codex-api/src/endpoint/realtime_websocket/methods.rs` | `:910-912`（`with_auth` 注释：每次握手/重试/重连都重解析）、`:1105-1107`（应用点） |
| realtime runtime 选择与硬报错 | `codex-rs/core/src/realtime_conversation.rs` | `:1426-1430`、`:1534-1563` |
| WS 复用前的凭据复核 | `codex-rs/core/src/client.rs` | `:314-320`（注释）、`:369`（`last_connected_auth_revision`）、`:1896-1914`（reset + reauth） |
| guardian 采样池 auth 变化 | `codex-rs/ext/guardian-v2/src/async_scorer/sampler/connection_pool.rs` | `:350`、`:398-419`、`:603-617` |
| denial 分类 | `codex-rs/protocol/src/error.rs` | `:643-670`（四码 + 401/403）、`:433-441`（`retry_delay` → `None`） |
| denial 用例 | `codex-rs/core/tests/suite/runtime_auth_denials.rs` | `:35-36`（`CASES`/`MODES`）、`:88-110`（三种响应）、`:113`（"A mistaken retry would complete the turn"） |
| 文件变更审批取消作用域 | `codex-rs/core/src/session/handlers.rs` | `:227-240`（`cancel_patch_approval`）、`:739-748`（legacy abort 也走同一路径） |
| protected 启动降级 | `codex-rs/core/src/session/mod.rs` | `:823-845` |
| 降级/保留/拒绝用例 | `codex-rs/core/tests/suite/settings_constraints.rs` | `:45-210`（Preserve/Downgrade/Reject 三分类） |
| turn context 必填 profile | `codex-rs/protocol/src/protocol.rs`（commit `2889d8e1bf` 的 200 行改动）+ `history/src/rollout_payload.rs` | 归一化在反序列化侧、legacy 字段保留在序列化侧 |
| 终答审核 | `codex-rs/protocol/src/items.rs`、`app-server-protocol/src/protocol/v2/item.rs`、`app-server-protocol/src/protocol/thread_history.rs` | `moderation: Option<...>` + item id 匹配 |
| 安全提醒取数 | `codex-rs/app-server/src/request_processors/account_processor/security_setup.rs` | `:22-105`（3s 超时 + 四条守卫 + source_generation 复核） |
| 子 agent catalog | `codex-rs/core/src/session/mod.rs`、`core/src/thread_manager.rs`、`core/src/thread_manager/model_catalog.rs` | `RefreshStrategy::InMemory`（非 root agent）、`models_for_thread(..., &source)` |
| MCP 网络策略（dead code） | `codex-rs/core/src/config/network_proxy_spec.rs` | `:389 mcp_network_policy`（生产无调用者） |
| 规则发布顺序 | `codex-rs/core/src/exec_policy.rs` | `:566-640`（deny 先 store、grant 后 store、update_lock） |
| macOS Secure Enclave | `codex-rs/user-verification/src/platform_macos/{provider.rs,key_protection.rs,error.rs}` + `lifecycle_lock.rs` | 需 macOS 才能验 |

---

## 17. 变更边界（本轮与后续的写入面）

- **本轮**：只写了本文件（zip 内 `update/plan_2026_11_08_p1_deep_dive.md`）。没有改 `parity/{commits,baseline,domains}.json`、没有改 `parity/contracts/manifest.json`、没有改 `parity/alignment_control_test.go`、没有改任何 Go 代码、没有改 generation 脚本。
- **13 条仍是 `partial`**：本文件给的是「怎么改、怎么验」，不是判定变更。等你按 §1.2 的顺序落地并合入 verifier 后，再按你现有的口径（verifier 在树里 + 行为可观测）逐条转 `complete`。
- **契约登记建议**：§16.4 的 13 个名字可以直接作为「需先落测试」列的候选；其中 6 条（`user-verification-*`）已登记但测试未落，属「同一契约补测试」而非新登记。
- **我没有把任何数字手写进结论**：`35`/`33`/`24`/`18`/`6`/`0` 全部由脚本从 `manifest.json` + 文件系统 + `git cat-file` 现算（命令见 §16）。

---

## 18. 一句话结论

13 条的现状可以归成三类：

1. **只差一个判定分支**（`#60096` 终态 denial、`#59910` 文件变更取消要中断 turn、`#60049` 子 agent 读内存目录）：改动 1–3 个函数，但每条的「第二处消费点」都有 2–4 处，只改主路径会留下可观测的不一致。
2. **缺一层结构**（`#59963` profile 必填、`#60082` 取数进 app-server、`#59842` 终答审核、`#60014` managed mode、`#60046` ③④ 发布顺序）：改动涉及 config/wire/rollout 三层，必须成对改「写入侧 + 读取侧」。
3. **必须成组落地**（`#60003` + `#60055` 的 realtime 鉴权链）：静态 Bearer 既是「一次性捕获」也是「每次重连复用同一值」，两半都要修，否则中间态比现状更难排查。
