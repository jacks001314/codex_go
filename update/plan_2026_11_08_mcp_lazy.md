# MCP 懒启动组 5 条逐条判定（`55897425e8..748ab55ca7` delta，附录 A「MCP」组）

- 作者：ledger-align（对齐账本 lane）
- 日期：2026-11-08
- 性质：只读分析。不改 Go 代码，不动 `parity/**`（契约表仅为建议，登记由主干 lane 做）。
- Rust 基线：`/home/jacks/jacks_dev/codex`（`codex-rs`，HEAD `748ab55ca7`），五条提交均可 `git show`。
- Go 基线：本 lane 完整镜像（6,954 文件 / 约 72 MB）；下文「零命中」为全树结果。
- 环境注意（承接你的提醒）：本轮**没有**设置 `CODEX_RUST_ROOT`，只用 `git show` 取改动，避免把解压 tar 当 git 检出用。

## 0. 判定汇总

| commit | PR | 主题 | 判定 |
| --- | --- | --- | --- |
| `fb2b06a829` | #60038 | 缓存工具调用准备时上抛 MCP 启动错误 | **partial** |
| `7ccf78dec4` | #60037 | 延迟启动时保留 MCP sharing discovery | **partial** |
| `af9a98b074` | #60039 | 缓存 stdio server 的 opt-in 懒启动 | **partial** |
| `b7d2321ef7` | #60046 | 推导 MCP 网络策略并保留已保存拒绝 | **partial** |
| `8fcabd75a0` | #59991 | MCP 工具刷新时保留 app 调用上下文 | **partial** |

组内：`partial 5`、`ported 0`、`aligned 0`、`not_applicable 0`。

## 1. 链式依赖：`#60037`/`#60038` 必须先于 `#60039`（结论：是，且两条都要）

`#60039` 引入的是「有缓存目录 → 不启动 server，先发缓存工具，首次调用再起」这条路径。它一旦存在：

1. **`#60038` 必须已在**：延迟启动后「第一次调用才发现 server 起不来」成为**常态**而不是边角。没有 `#60038`，模型只能拿到一个和「工具不存在/被过滤」无法区分的提示（Rust 原来的 `Option` 语义就是这个问题）。→ **先 #60038**。
2. **`#60037` 必须已在**：`#60039` 让 deferred server 带着 `DiscoverLocalStdio` 的意图启动，而 Go 现在的做法是「用 legacy 目录模式做 key，现代协商就作废旧目录」（见 §3.1）。直接开 `#60039` 会精确复现 Rust #60037 修掉的那个 bug——子代理首次调用时无法协商连接共享，只能退回 legacy 协议。→ **先 #60037**（或与 #60038 同批）。
3. 建议落地顺序：**`#60038` → `#60037` → `#60039`**。契约层面对应三个独立 id（见 §7），互相不阻塞登记，但实现上后者的 verifier 必须跑通前两者的用例。

`#60046` 与 `#59991` 与本链**无依赖**，可并行。

## 2. `fb2b06a829` #60038 — 准备缓存工具调用时上抛启动错误

### 2.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/codex-mcp/src/connection_manager/tool_catalog.rs:567-620` | `prepare_call_for_tool` 从 `Option<PreparedMcpCall>` 改为 `Result<PreparedMcpCall>`：`servers.get(server)`、tool filter、catalog 命中、`prepare_call` 五处失败分别返回错误 |
| `…/tool_catalog.rs:583` | 连接失败时 `.with_context(‖ format!("Could not start MCP server `{server_name}`"))`——把启动失败与「工具不可用」区分开 |
| `codex-rs/codex-mcp/src/tools.rs:57-64` | 新增 `ToolInfo::unavailable_to_model_error()`：保留「工具缺失/被过滤/对模型不可见」时的统一文案 |
| `codex-rs/codex-mcp/src/runtime.rs:652-668` | `McpRuntime::prepare_call` 返回 `anyhow::Result`，注释即本条语义：「A failed connection returns its startup error even if the cache advertised the tool」 |
| `codex-rs/core/src/mcp_tool_call.rs`（−72/+…） | 调用方按 `Result` 处理：错误进模型可见输出，不再吞成「未找到工具」 |
| `codex-rs/core/src/mcp_tool_call/recorded.rs`、`core/src/session/mcp_runtime.rs`、`core/src/tools/handlers/mcp.rs:232-280` | 记录式审批准备同样保留错误链；handler 改用 `prepared.is_some()` 语义 |

### 2.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| Go **会**把缓存目录里的工具发给模型，即使 server 未就绪 | 【F:mcp/startup_readiness.go†L5-L20】`cachedCatalogStartupTools`（注释即「a server may expose before its live connection is ready」）；`mcp/startup_readiness_test.go:11`/`:243` 两个用例锁这条行为 |
| 加上 `startup_readiness = "catalog"` 的绑定语义 | 【F:mcp/binding.go†L29-L32】`cachedRevisions`；`mcp/binding_test.go:21` `TestBindingPreservesCachedToolsForNonReadyServersLikeRust` |
| **等启动时把错误吞掉** | 【F:mcp/api.go†L1507-L1540】`WaitForServerStartup`：等 `inventoryStatusForConfig` 跑完即 `return nil`，**丢掉了 `goto done` 之外的任何失败**；而状态里明明有错误字段（`MCPServerStatus.Error *string`、`ToolsError *string`，【F:mcp/api.go†L297-L304】） |
| 执行侧的入口就是它 | 【F:mcp/tool_executor.go†L717-L727】`WaitUntilReady` → `WaitForServerStartup` |
| 无「不可用工具」统一文案的等价物 | `rg -n "not available to the model" -g '*.go'` → **0 matches** |

### 2.3 缺什么 / 影响面 / 最小修复路径

- 缺：①`WaitForServerStartup` 的失败上抛；②错误文案里带 server 名（`Could not start MCP server \`X\``）；③「工具对模型不可见 vs 启动失败」两类错误的显式区分。
- 影响面：`startup_readiness = "catalog"` 命中时，server 起不来的唯一表现是模型收到一个含糊的调用失败（或超时），排障只能看日志；一旦 `#60039` 落地，这会变成**默认路径**。
- 最小修复（单文件，约 15 行）：
  1. 【F:mcp/api.go†L1507-L1540】等完之后重新读一次状态：`if status.State == MCPServerFailed && status.Error != nil { return fmt.Errorf("Could not start MCP server %q: %s", name, *status.Error) }`；`ToolsError` 非空同样包装。
  2. 【F:mcp/tool_executor.go†L717-L727】把该错误原样返回给工具层（现有 `Execute` 的 `tool.RespondToModel(err.Error())` 路径已能把它变成模型可见输出）。
  3. 新增 `mcp.ToolInfo.UnavailableToModelError()` 等价的格式化函数，供「server 不存在 / 被 tool filter 过滤 / 对模型不可见」三处统一使用，避免又混回启动错误。
- 测试要点：cached 工具 + server 启动失败 → 模型输出里必须出现 `Could not start MCP server`；工具被 filter 掉 → 仍必须是「不可用」文案而不是启动错误；两种 readiness（`catalog` / `connection`）各一条。

## 3. `7ccf78dec4` #60037 — 延迟启动时保留 sharing discovery

### 3.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/codex-mcp/src/connection_manager.rs:684-686` | deferred 连接也把 `before_stdio_discovery` 设为 `expected_protocol_mode == DiscoverLocalStdio`——不再把 deferred 子代理强行降级成 `Legacy`（删掉了原来那段 `expected_protocol_mode = Legacy` 的改写） |
| `…/connection_manager.rs:951` | 把 `defer_startup` 传进 `AsyncManagedClient::new`（供下面两处分支用） |
| `codex-rs/codex-mcp/src/rmcp_client.rs:529-537` | 拆出 `pool_catalog`（仅非 discovery 连接可写池目录）与 `advertised_catalog`（dormant 会话可预先广告 legacy 目录）；`:575` 池连接只拿 `pool_catalog`，`:624` 客户端只拿 `advertised_catalog` |
| `codex-rs/codex-mcp/src/rmcp_client.rs:1190-1196` | 现代握手改调 `record_modern_stdio_discovery(ticket)`（原为 `invalidate_if_newest`）——**不再直接作废**旧 legacy 目录 |
| `codex-rs/codex-mcp/src/tool_catalog_cache.rs:59-66`、`:82`、`:135-140` | 状态拆成 `last_catalog_generation` 与 `last_modern_discovery_generation`；`before_stdio_discovery` 的读者（discovery 客户端）在现代发现之后**拒绝**旧 legacy 目录，而 legacy 客户端仍可复用 |
| `codex-rs/codex-mcp/src/tool_catalog_cache.rs:250-262` | `record_modern_stdio_discovery`：只推进 modern 代次，不清空 snapshot |

### 3.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| Go 有 discovery / sharing 的全部底座 | 【F:mcp/connection_pool.go】连接池；【F:mcp/api.go†L2263-L2295】`mcpToolCatalogGraceKeyWithFeatures`（**发现连接按 legacy 目录模式做 key**，注释直引 Rust #57210：「a modern negotiation invalidates that legacy catalog instead of publishing into it」） |
| 测试已锁「发现连接保持 legacy 目录身份」 | `mcp/stdio_discovery_like_rust_test.go:262` `TestMCPDiscoverLocalStdioKeepsLegacyCatalogIdentity`、`:204` `TestMCPConnectionSharingGatesLocalStdioDiscoveryLikeRust` |
| **没有** `before_stdio_discovery` 等价概念 | `rg -n "before_stdio_discovery|BeforeStdioDiscovery" -g '*.go'` → **0 matches** |
| **没有**「catalog 代次 / 现代发现代次」的双计数 | `rg -n "last_modern_discovery|ModernDiscoveryGeneration" -g '*.go'` → **0 matches**；Go 只有 per-connection `clientToolCatalog.revision`（【F:mcp/client_tool_catalog.go†L11-L20】） |
| **没有**延迟启动，所以这条的宿主路径不存在 | `rg -n "deferStartup|deferredStartup|LazyWhenCached|DeferCached" -g '*.go'` → **0 matches** |

### 3.3 判定理由与影响面

- 判 `partial` 而不是 `not_applicable`：Go **实现了该提交之前的 #57210 语义**（发现连接用 legacy 目录 key，现代协商即作废旧目录），也就是这条要修的那个“硬作废”状态；缺的是「按读者区分新旧目录」与「延迟启动时别降级协议」。等 `#60039` 落地，这段差异会立刻变成真实 bug（子代理首次调用拿不到连接共享）。
- 影响面（若不做 `#60039`）：仅影响 discovery 目录复用的边界，无用户可见回归；**一旦做 `#60039` 就是功能回归**。
- 最小修复路径：
  1. 在 Go 的目录身份状态里加两个代次（`catalogGeneration`、`modernDiscoveryGeneration`），把 `mcpToolCatalogGraceKeyWithFeatures` 的“现代协商即换 key/作废”改为「推进 `modernDiscoveryGeneration`」；
  2. 让 discovery 读方在 `modernDiscoveryGeneration > catalogGeneration` 时拒绝 legacy 目录（Go 目前用 key 隔离，等价手段是读方额外携带 `beforeDiscovery` 标志）；
  3. 与 `#60039` 同批时，`deferStartup` 分支**不要**把 `ProtocolMode` 从 `DiscoverLocalStdio` 改写成 `Legacy`（这是 Rust 本条删掉的那段代码在 Go 侧的对位）。
- 测试要点：dormant 子代理首次调用必须协商出共享连接并复用同一 server 进程；legacy 兄弟保留缓存定义且不启动 server；更旧的 legacy fetch 不能覆盖现代发现。

## 4. `af9a98b074` #60039 — cached stdio server 的 opt-in 懒启动

### 4.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/features/src/lib.rs:288-291`、`:1745-1752` | 新增 `Feature::McpLazyStdioStartup`（默认关） |
| `codex-rs/config/src/mcp_types.rs:331-337` | `can_defer_stdio_startup()`：stdio 传输 **且**（非 required **或** `startup_readiness == Catalog`） |
| `codex-rs/core/src/session/mcp_runtime.rs:379-392` | 启动策略：子代理 → `DeferCachedServers`；普通会话 + 特性开 → `DeferCachedStdio`；否则 `Eager` |
| `codex-rs/codex-mcp/src/connection_manager.rs:409-412`、`:684-700`、`:983-1000`、`:1114-1150` | 把一个大 `JoinSet` 拆成 `eager_workers`/`deferred_workers`；`eager_starts` 在**任何终态之前**整批广播 `Starting`；deferred worker 不进首轮摘要，由首次调用自行启动并单独上报 |
| `codex-rs/tui/src/chatwidget/mcp_startup.rs:1-49`、`tui/src/app/app_server_events.rs:1-36` | TUI 侧能接收“后到”的启动事件，dormant server 在后续轮次独立报告 |
| `codex-rs/core/tests/suite/scenarios.rs`、`…dormant_in_a_new_chat_until_first_use.snap` | 场景：无关工作中保持 dormant → 首次使用才启动 → 刷新工具定义与可见性 |

### 4.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **特性开关不存在** | `rg -n "mcp_lazy_stdio_startup" -g '*.go'` → **0 matches**；【F:features/features.go】注册表里无此项 |
| **启动策略枚举不存在** | `rg -n "McpStartupPolicy|StartupPolicy" -g '*.go'` → **0 matches**（Go 没有 Eager/Defer 概念，启动即全量） |
| 但有“缓存目录可直接用”的能力 | 【F:mcp/startup_readiness.go†L5-L20】`cachedCatalogStartupTools`；【F:mcp/binding.go†L29-L32】`cachedRevisions`；配置侧 `startup_readiness` 解析见 `mcp/config.go`（`rg -n "startup_readiness" mcp/config.go` 有命中） |
| 有共享连接/池 | 【F:mcp/connection_pool.go】；`mcp/connection_sharing_like_rust_test.go:155` 已锁「legacy 与 startup-interactive server 保持私有」 |
| 启动事件有既有管道 | `MCPService.ListStatusCheckedWithObserver`（【F:mcp/api.go†L1542-L1560】）、`MCPStartupObserver`；但**没有**“eager 批次先整批广播 Starting、deferred 不进首轮摘要”的排序语义 |

### 4.3 缺什么 / 影响面 / 最小修复路径

- 缺：①特性开关与注册表项；②`StartupPolicy` 三分支（Eager / DeferCachedStdio / DeferCachedServers）；③`canDeferStdioStartup` 谓词（stdio + 非 required 或 readiness=catalog）；④eager 批次整批 `Starting` 广播 + deferred 不入首轮摘要；⑤TUI/事件侧对“后到启动”的接纳。
- 影响面：纯性能/启动时延（每个会话都会拉起所有 stdio server，即使整轮未用），无正确性风险；但**不实现它，#60037 就永远是纸面差异**。
- 最小修复路径（按依赖顺序）：
  1. `features/features.go` 加 `mcp_lazy_stdio_startup`（`StageUnderDevelopment`，默认 false）；
  2. `mcp` 包加 `StartupPolicy` 与 `canDeferStdioStartup(config)`（对齐 Rust 谓词，含 `required` 与 `startup_readiness` 两个条件）；
  3. 启动编排处（`MCPService` 的启动入口）拆 dual-set：先对 eager 集合整批发 `Starting`，再并发起；deferred 只登记“首次调用时启动”的钩子（Go 已有 `WaitForServerStartup`，可复用为触发器，但**必须先有 §2 的错误上抛**）；
  4. 事件侧：把 deferred 的启动更新作为后续轮次发出，首轮 summary 排除它们。
- 测试要点：dormant 期间不 spawn 进程（用 helper 进程计数）；首次调用才启动并成功；`startup_readiness=connection` 的 required server 仍 eager；显式 reconnect 仍 eager；开启/关闭特性两态各一条。

## 5. `b7d2321ef7` #60046 — 推导 MCP 网络策略 + 保留已保存拒绝

### 5.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/core/src/config/network_proxy_spec.rs:41-49` | 新增 `McpNetworkRoute { primary_allows_proxy, local_binding, owner }`——“主连接控制共享代理，执行该调用的 executor 提供 local-binding 语义” |
| `…/network_proxy_spec.rs:389-470` | 新增 `mcp_network_policy(controller, controller_profile, route, permission_profile, approval_policy, exec_policy) -> NetworkPolicyMetadata`：按 owner/controller/直连三种情形组合，**保守拒绝**需要 review / request hook / DNS 检查而元数据表达不了的流量（注意：本条里它标了 `expect(dead_code, reason = "used by the next PR")`，即**生产接线在后续 PR**） |
| `…/network_proxy_spec.rs:281-360` | `compose_environment_policy` 抽出 `routed_binding: Option<LocalBindingPolicy>`；仅在 routed 时校验 managed 约束 |
| `codex-rs/network-proxy/src/config.rs:327-352` | 新增 `apply_saved_domain_rules(allowed, denied)`：**先应用 allowed，再覆盖 denied**——保证陈旧 grant 不会盖掉已保存的 deny |
| `codex-rs/core/src/exec_policy.rs:567-645` | `append_network_rule_and_update` → `apply_network_rule_and_persist`：先更新运行时 allow/deny 列表；**denial 在可取消的落盘之前就先发布**；grant 等落盘成功后才发布（`notify_after_save`） |
| `codex-rs/core/src/session/mod.rs:3186-3205` | 调用方简化：代理更新与落盘都委托给 exec policy（不再在 session 里各自做一遍） |

### 5.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| 有 `NetworkPolicyMetadata` **契约** | 【F:mcp/network_policy.go†L20-L38】`NetworkPolicyMetadata` + `NetworkPolicyMetadataProvider`，注释直引 Rust #56278 |
| 有代理侧组合原语 | 【F:network/network.go†L260-L300】`NewSpecForEnvironment(config, requirements, permissionKind, policy, execRules)`；`:374-L400` `WithExecPolicyNetworkRules`/`ApplyExecPolicyNetworkRules` |
| 有环境策略与本地绑定 | 【F:network/network.go†L149-L180】`EnvironmentNetworkPolicy`；`LocalBindingPolicy` 见 `network/`+`appserver/managed_network_constraints.go:88-95` |
| 有域权限 upsert | 【F:network/proxy_config.go†L204】`ProxySettings.UpsertDomainPermission` |
| **没有 MCP 侧的推导函数** | `rg -n "McpNetworkPolicy|mcp_network_policy" -g '*.go'` → **0 matches** |
| **生产上没有任何 provider 实现** | `NetworkPolicyMetadataForCall` 的唯一实现是测试替身（【F:mcp/network_policy_like_rust_test.go†L24】）；`rg -n "NetworkPolicyMetadata:" -g '*.go'` 命中的全是测试 → 生产里 `networkPolicyMetadata == nil`，于是【F:mcp/network_policy.go†L41-L47】`networkPolicyAdvertisedForServer` 恒为 false：**Go 的 MCP 调用从不携带网络策略元数据** |
| 保存规则的应用顺序未对齐 | Go 侧【F:appserver/network_approval.go†L1492-L1512】只 `execpolicy.AppendNetworkRule` 落盘；代理侧 upsert 在【F:appserver/network_approval.go†L1521-L1536】的 `applyExecPolicyNetworkRules` 里**重读整个 policy 文件**后统一 upsert，没有“deny 先发布、grant 落盘后发布”的顺序保证；`apply_saved_domain_rules` 等价物零命中 |
| 已有测试锁“保留继承的拒绝” | `network/network_environment_policy_test.go:9` `TestEnvironmentNetworkPolicyApplyToPreservesInheritedDenials` |

### 5.3 缺什么 / 影响面 / 最小修复路径

- 缺：①`McpNetworkRoute` + `McpNetworkPolicy(...)` 的推导（含“元数据表达不了就保守拒绝”）；②生产 provider（把推导接到 `mcp.RuntimeConfig.NetworkPolicyMetadata`）；③`applySavedDomainRules`（deny 覆盖 stale grant）；④runtime 更新的发布顺序（deny 先、grant 落盘后）。
- 影响面：③④是**安全语义**——陈旧 grant 可能盖掉有效 deny；落盘失败时内存策略与原生执行不一致。①②是**能力缺失**——Go 的 MCP server 永远收不到网络策略，信任的 ChatGPT MCP server 无法按调用权限收窄网络。
- 最小修复路径：
  1. `network` 包加 `McpRoute` + `McpNetworkPolicy(...)`（把 Rust 的三分支照搬：owner 走 `NewSpecForEnvironment`、routed controller 走 `recompute_for_permission_profile` + exec 规则、直连走 `from_config_and_constraints` 等价物）；
  2. `appserver` 里实现 `mcp.NetworkPolicyMetadataProvider`（用 thread 的 permission profile + 环境策略 + approval policy + exec policy），在构造 `mcp.RuntimeConfig` 时注入（现在这条线是空的）；
  3. `network.ProxySettings` 加 `ApplySavedDomainRules(allowed, denied)`：allowed 先、denied 后；
  4. `appserver/network_approval.go`：deny 时先 `UpsertDomainPermission` 再落盘；grant 落盘成功后再 upsert。
- 测试要点：组合优先级（managed > saved deny > saved grant）；stale grant 不得盖掉 deny；落盘失败时 deny 仍生效、grant 不生效；混合 executor 的 local-binding；无策略协商能力的受信 server 仍兼容。

## 6. `8fcabd75a0` #59991 — 工具刷新时保留 app 调用上下文

### 6.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/core/src/mcp_tool_call.rs:136-145` | 新增 `apps_invocation_context(tool)`：从 `_meta["_codex_apps"]["invocation_context"]` 取“广告时的上下文” |
| `…/mcp_tool_call.rs:190-200` | `context_changed`：server 是 codex_apps 且已准备调用的 tool_info 的上下文 ≠ 广告时的上下文 |
| `…/mcp_tool_call.rs:200-215` | **只有发起该上下文的连接**才允许在刷新后继续带它：`prepared_call.filter(‖ !context_changed ‖ call.uses_connection_from(&step_context.mcp))`，否则按「不可用」跳过 |
| `…/mcp_tool_call.rs:243-252` | 未变连接时，把**广告时的**上下文写回 metadata（而不是刷新后的值） |
| `codex-rs/codex-mcp/src/binding.rs:286-292` | 新增 `PreparedMcpCall::uses_connection_from(binding)` |
| `codex-rs/codex-mcp/src/connection_manager.rs:1198-1204` | 新增 `shares_server_connection`：按 `Arc::ptr_eq(&left.connection, &right.connection)` 判定同一连接 |

### 6.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| Go 有 invocation_context 的**投影** | 【F:mcp/model_search.go†L79-L95】`Binding.SearchInvocationContext` / `ModelSearchInvocationContext`；`:172-L179` 从 `appsMeta["invocation_context"]` 读出 |
| 调用时用它拼 `_codex_apps` meta | 【F:turn/tools.go†L1221-L1233】`mcpRuntimeToolRequestMeta` |
| 有既存测试 | `mcp/model_search_like_rust_test.go:11`、`turn/model_search_meta_like_rust_test.go:10` |
| **没有**“连接同一性”判定 | `rg -n "shares_server_connection|SharesServerConnection" -g '*.go'` → **0 matches** |
| **没有**“上下文变即按不可用跳过” | `rg -n "context_changed|contextChanged" -g '*.go'` → **0 matches** |
| 刷新后语义由 `Binding` 代次控制，但**取的是刷新后的值** | 【F:mcp/binding.go†L11-L40】`Binding` 冻结 catalog revision；`mcp/binding_test.go:8` `TestBindingRejectsCatalogRefreshAndInvisibleTools` 只锁“刷新后不可见就拒绝”，没有 invocation_context 的保留规则 |

### 6.3 缺什么 / 影响面 / 最小修复路径

- 缺：①把 `invocation_context` 冻结在**广告时的 tool info** 上并随 prepared call 携带；②连接同一性判定（Go 可用 per-server `clientToolCatalog` 指针或 `binding.catalogs` 里的 client 标识做等价判断，不需要新造 Arc 语义）；③上下文在**不同连接**上变化时按「不可用」跳过而非静默换值。
- 影响面：codex_apps 工具在 catalog 刷新后可能带着**新的**（未必属于当前单元格）上下文发出，与服务端记录的授权/归属不一致；严重时把一个 cell 的调用打到另一个 connector 上。「换连接」场景下还会静默成功，比失败更难查。
- 最小修复路径：
  1. `mcp.RuntimeToolInfo`（或 `Binding.toolInfos`）保留广告时的 `_codex_apps.invocation_context` 副本，并在 `TurnTools` 组装 meta 时优先用它；
  2. `mcp.Binding` 增加 `SharesServerConnection(other, server) bool`（比较 `catalogs[server].client` 指针，等价 `Arc::ptr_eq`）；
  3. 调用准备处（`mcp` 执行器或 `turn` 的 meta 组装）实现「上下文变化 + 连接不同 → 返回不可用错误」；上下文变化但**同**连接 → 用广告值。
- 测试要点：同一连接刷新后 `resource_uri` 更新但 `invocation_context` 保持广告值；换连接后上下文变化 → 调用被跳过（不可用），而不是用新值成功；非 codex_apps server 不受影响。

## 7. 契约登记表（5 条，格式同 manifest）

字段约束同上一组：`id/kind/rustPaths/goPaths/phase/status/verifier`；**`TestContractVerifiersExistInGoTestFiles` 要求 verifier 名字在 Go 树里已存在**，所以下面「可立即登记」列都是现存测试（已逐个 `rg` 核对），「需先落测试」列是实现同批要补的。

| # | contract id | kind | rustPaths | goPaths | phase | status | 可立即登记的 verifier | 需先落的 verifier + 要点 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `fb2b06a829` #60038 | `mcp-cached-tool-startup-errors` | `source-surface` | `codex-mcp/src/connection_manager/tool_catalog.rs`、`codex-mcp/src/runtime.rs`、`codex-mcp/src/tools.rs` | `mcp`、`appserver` | `P1` | `partial` | `TestBindingPreservesCachedToolsForNonReadyServersLikeRust;TestCatalogStartupReadinessRejectsMissingLiveConnectionOnExecution` | `TestCachedToolCallSurfacesMCPServerStartupErrorLikeRust`：cached 工具 + 启动失败 → 模型输出含 `Could not start MCP server`；被过滤工具仍是「不可用」文案 |
| `7ccf78dec4` #60037 | `mcp-sharing-discovery-deferred-startup` | `source-surface` | `codex-mcp/src/connection_manager.rs`、`codex-mcp/src/rmcp_client.rs`、`codex-mcp/src/tool_catalog_cache.rs` | `mcp` | `P1` | `partial` | `TestMCPDiscoverLocalStdioKeepsLegacyCatalogIdentity;TestMCPConnectionSharingGatesLocalStdioDiscoveryLikeRust` | `TestDeferredStartupKeepsDiscoverLocalStdioSharingLikeRust`：dormant 子代理首次调用协商共享并复用同一进程；legacy 兄弟保留缓存且不启动；旧 legacy fetch 不覆盖现代发现 |
| `af9a98b074` #60039 | `mcp-lazy-stdio-startup` | `source-surface` | `config/src/mcp_types.rs`、`codex-mcp/src/connection_manager.rs`、`core/src/session/mcp_runtime.rs`、`features/src/lib.rs` | `mcp`、`appserver`、`features` | `P1` | `partial` | `TestCatalogStartupReadinessAcceptsFilteredVisibleCachedCatalog;TestMCPConnectionSharingKeepsLegacyAndStartupInteractiveServersPrivate` | `TestMcpLazyStdioStartupDefersCachedServersLikeRust`：特性关=全量启动；特性开+dormant 不 spawn 进程；首次调用启动成功；required+`connection` 仍 eager；explicit reconnect 仍 eager |
| `b7d2321ef7` #60046 | `mcp-network-policy-derivation` | `source-surface` | `core/src/config/network_proxy_spec.rs`、`core/src/exec_policy.rs`、`network-proxy/src/config.rs` | `network`、`execpolicy`、`appserver`、`mcp` | `P1` | `partial` | `TestMCPNetworkPolicyCapabilityOnlyAdvertisedToTrustedChatGPTOriginsLikeRust;TestEnvironmentNetworkPolicyApplyToPreservesInheritedDenials` | `TestMcpNetworkPolicyDerivedFromControllerAndSavedDenialsLikeRust`：组合优先级（managed > saved deny > saved grant）；stale grant 不盖 deny；落盘失败时 deny 生效/grant 不生效；本地绑定跟随 executor |
| `8fcabd75a0` #59991 | `mcp-app-invocation-context-refresh` | `source-surface` | `codex-mcp/src/binding.rs`、`codex-mcp/src/connection_manager.rs`、`core/src/mcp_tool_call.rs` | `mcp`、`turn` | `P1` | `partial` | `TestModelSearchDiscoveryProjectsConnectorInvocationContextLikeRust;TestBindingRejectsCatalogRefreshAndInvisibleTools` | `TestAppInvocationContextPreservedAcrossCatalogRefreshLikeRust`：同连接刷新保留广告上下文、取新 `resource_uri`；换连接且上下文变化 → 按不可用跳过 |

> 备注：`#60046` 的 `mcp_network_policy` 在 Rust 本条里标了 `expect(dead_code, reason = "used by the next PR")`，即**生产接线在后续提交**。登记 status 用 `partial` 是准确的（契约先行），但完成度判定要等那个后续 PR 一起看，别只凭本条验收。

## 8. 复现命令

```
$ rg -n "deferStartup|deferredStartup|LazyWhenCached|DeferCached" -g '*.go'      → 0 matches
$ rg -n "mcp_lazy_stdio_startup" -g '*.go'                                       → 0 matches
$ rg -n "before_stdio_discovery" -g '*.go'                                       → 0 matches
$ rg -n "last_modern_discovery|ModernDiscoveryGeneration" -g '*.go'              → 0 matches
$ rg -n "McpNetworkPolicy|mcp_network_policy" -g '*.go'                          → 0 matches
$ rg -n "ApplySavedDomainRules|apply_saved_domain_rules" -g '*.go'               → 0 matches
$ rg -n "shares_server_connection|SharesServerConnection|context_changed" -g '*.go' → 0 matches
$ rg -n "not available to the model" -g '*.go'                                   → 0 matches
$ rg -n "NetworkPolicyMetadata:" -g '*.go'   → 仅 mcp/network_policy_like_rust_test.go（生产 0）
$ rg -n "WaitForServerStartup" -g '*.go'     → mcp/api.go:1507（吞错处）+ mcp/tool_executor.go:717
```

已具备（反证，证明不是镜像缺文件）：

```
$ rg -n "cachedCatalogStartupTools|startup_readiness" mcp/*.go | head   # 缓存目录直用面存在
$ rg -n "clientToolCatalog|connection_pool" mcp/*.go | head             # 连接共享面存在
$ rg -n "NewSpecForEnvironment|UpsertDomainPermission" network/*.go | head
```

## 9. 结论

- 5 条全部 `partial`：MCP 侧的承载面（缓存目录直用、连接共享/池、discovery 协议模式、网络策略原语、invocation_context 投影）Go 都有，缺的是**懒启动这条链**（策略/开关/编排）、**启动错误上抛**、**目录代次分离**、**MCP 网络策略推导与 provider 接线**、**刷新时的上下文冻结**。
- **链式结论**：`#60038` 与 `#60037` 都必须先于 `#60039`；否则 `#60039` 会把「启动失败不可见」与「首次调用无法协商共享」两个 bug 一起引入。建议顺序 `#60038 → #60037 → #60039`。
- 风险排序：#60046 的 ③④（stale grant 盖掉 deny、落盘失败时内存与原生不一致）是**安全语义**，建议与这个特性无关地先修；#60038 成本最低（单函数 + 一个错误格式）。
- 本文件不改 Go 代码、不动 `parity/**`。
