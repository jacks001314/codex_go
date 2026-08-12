# Go 版 Codex 与 Rust 版 Codex 功能比对报告（需对齐功能点汇总）

> 生成日期：2026-08-13
> 比对基线：
> - Rust（上游）：`D:\qax\reagent\dev\git\codex`，本地 `main` HEAD `0e82c62a44`（#38179），`origin/main` 已 fetch 至 `95aada11c4`（#38205，2026-08-12）
> - Go：`D:\qax\reagent\dev\codex_go`，HEAD `0764fd4`（"Align Go Codex with Rust next batch: #38183/#38188/#38197/#38204"），已对齐到 Rust `3d7f9b4637`（#38204）
> - 方法：以双方源码（`codex-rs/` vs `codex_go/`）逐领域比对 + 复核 `func.md`（2026-08-07 源码级报告）、`parity.json`（对齐登记）、`update/plan_*.md`（跟踪台账）与上游最新提交；本报告为**需对齐功能点**汇总，非全量功能清单。

---

## 一、总体结论

1. **整体对齐度极高**：Go 版是 Rust 版的功能级移植。28 个子命令、110 个特性开关 key（Go 109 个，仅缺 `psp`，Rust 专用）、TUI 目录结构、app-server 协议类型、工具集、hook 事件、MCP OAuth 流程等均逐项对应；大量 Go 单测以 `LikeRust` / `RustShape` / `MatchesRust` 命名断言与 Rust 行为一致。
2. **统计口径**（func.md 基线 237 项：已对齐 176 / 缺失 27 / 不匹配 22 / Go 额外 12）。自 2026-08-07 以来已闭环一批缺失项：`new_context` 工具、`model_catalog_json`、`file_opener`、imagegen 用量限制（#38024）、CIMD MCP OAuth（#38089）、skill 调用分析（#38066/#38074）、制品操作分析（#38057）、harness 元数据（#38058）、环境就绪配置（#38067）、workload identity 认证（#38188）、skill shadow LRU/fusion（#38197/#38204）、restrict_to 助手（#38183）、gRPC code-mode 客户端（#38041/#38087/#38072）、Windows 沙箱 app-root 授权（#38064）、远程 apply_patch 沙箱（#38043）等。
3. **当前未对齐上游**：Rust `origin/main` 仅领先 1 个提交 `95aada11c4`（#38205，delegate 非交互审批策略），Go 尚未实现。
4. **已知缺口集中在**：条件性工具（`request_permissions`/`test_sync`/`extension_tools`）、legacy rollout→paginated history 自动迁移（最大单项）、少量冷门配置键（`ghost_snapshot`/`js_repl_*`/`sqlite_home`）、WSL 路径归一化、macOS process-hardening、若干已跟踪的行为差异（见第三章）。
5. **不匹配点多为实现栈差异**（沙箱 bwrap/seatbelt/Windows job、TUI bubbletea vs ratatui、会话存储 JSONL vs SQLite、压缩触发时机），需持续“行为级”对齐测试兜底。

---

## 二、P0：未对齐的新上游提交（1 个）

| 上游提交 | 内容 | Go 现状 / 需要做的对齐 |
|---|---|---|
| `95aada11c4` **#38205**（2026-08-12）Enforce non-interactive approval policy for Codex delegates | ① delegate（子代理）会话强制 `never` 审批策略，拒绝任何可 prompt 策略的 delegate 创建；② 停止向父会话转发 delegate 的审批/权限请求，需要审批的命令与 MCP 工具调用在 delegate 内直接拒绝；③ 审批禁用（approval_policy == never）时跳过缺失 skill MCP 依赖的安装提示（`should_install_mcp_dependencies` 直接返回 false） | ① Go `agent.SpawnAgentArgs` 子代理创建处未强制 `never` 策略（`agent/`、`exec/agent_controller.go`、`appserver/agent_controller.go`），review/guardian 以 `model.AgentRunner` 直接运行，无 Rust `codex_delegate.rs` 对应物——需评估等价实现并加测试；② Go 需核对 delegate 的审批/权限请求路由是否已拒绝而不是转发；③ Go `skillMCPDependencyPromptAutoApproved`（`appserver/turn_runtime.go`）在 never + 沙箱禁用时**自动安装**（Rust 是**跳过不装**），never + 沙箱未禁用时 Go 仍会弹 `ToolRequestUserInput` 提示——语义与 Rust 不一致，需对齐为“never 下不提示、不安装”并补回归测试 |

---

## 三、P1：已跟踪的行为差异（tracked deltas，Go 侧已知未闭环）

| # | 跟踪项 | 内容 | 出处 |
|---|---|---|---|
| 1 | **legacy rollout→paginated history 自动迁移**（最大单项） | Rust `thread-store/src/local/rollout_migration.rs`（dry-run/apply、canonicalizer、journal、原子发布、rollback replay、subagent 有界重放）+ `rollout/src/maintenance.rs`（rollout-maintenance.lock 排他锁）；Go 仅有 legacy JSONL 读取兼容（`rollout/rollout.go` legacyRolloutItemWrapper）与 feature flag `background_paginated_rollout_migration`（`features/features.go`，StageUnderDevelopment），**无迁移状态机实现** | func.md 第二章；appserver_v2_suite_manifest_test.go |
| 2 | **MCP 审批 reviewer 按 server/connector 解析**（#38108 余项） | Rust 从 MCP 配置层解析 per-server/per-connector `approvals_reviewer`；Go 仅解析线程级有效配置 | plan_2026_08_12_deferred_completion.md |
| 3 | **持久化 MCP policy-amendment 审批**（#38108 余项） | `approved_mcp_policy_amendment` wire action + policy store；Go 枚举值已存在（`appserver` ReviewDecision）但未真正写入策略存储 | 同上 |
| 4 | **统一审批解析来源遥测**（#38108 余项） | Rust 单一 Hook/Guardian/User 来源遥测；Go 记录 hook runs + auto_review 元数据，未统一 | 同上 |
| 5 | **配置化外部认证（#38054）架构差异** | Rust login auth-manager 的 `ExternalAuthSource`（Configured vs Runtime、host 拥有不可变性、进程本地凭据、provider `classify_error`）；Go 仅 host 驱动的 `chatgptAuthTokens` 模式，无 Rust 式 ExternalAuth provider 层（已实现便携部分：`AuthsEqualForRefresh`） | plan_2026_08_12_next2.md |
| 6 | **workload identity 刷新接线（#38188 余项）** | Go `RefreshChatGPTTokens` 尚无 workload-identity 分支（底层 `WorkloadIdentityAuthForProcess().RefreshAuth()` 已实现，未接入 ChatGPT 刷新调用点） | plan_2026_08_12_next2.md |
| 7 | **executor 侧 fs-helper fail-closed（#38043 余项，Windows）** | `execserver/fs_helper.go` 在 `Disabled` 级别应 fail-closed（Rust `SandboxablePreference::Require` 语义）；Go 暂保留现状以避免破坏 skill 读取 | plan_2026_08_12_next2.md |
| 8 | **code-mode gRPC host 侧服务** | Go 仅作为 gRPC 客户端连接 host（`codemode/grpc_session_provider.go`），不提供 code-mode gRPC server；Rust 原生 gRPC host（#38041） | 同上 |
| 9 | **exec-server 启动瞬时失败重试（#38020）** | Rust 对 exec-server 启动瞬时失败重试；Go `execserver/remote.go` 有 Backoff（重连），启动期重试路径未闭环 | plan_2026_08_12.md |
| 10 | **execution-host 上下文 cloud config 解析（#38086）** | Rust 按执行宿主上下文解析 cloud config（`remote_sandbox_config`/`hostname` requirements 路由、`~` 展开）；Go config 加载器无此路由（pre-existing gap） | plan_2026_08_12_deferred_completion.md |

---

## 四、P2：功能缺失（Go 缺少，源码核验）

### 4.1 条件性工具处理器（Rust `core/src/tools/handlers/`）

| 工具 | Rust 证据 | Go 现状 |
|---|---|---|
| `request_permissions` | `handlers/request_permissions.rs` RequestPermissionsHandler | **缺失**：Go 仅有提示文本（`sandbox/permission_prompt.go`）、通知/审批请求处理（`appserver/notifications.go`、`appserver/review_analytics.go`）与 feature flag `request_permissions_tool`，未注册同名工具处理器 |
| `test_sync`（工具名 `test_sync_tool`） | `handlers/test_sync.rs` + `test_sync_spec.rs`（SDK 测试同步用） | **缺失**：Go 无对应工具 |
| `extension_tools` | `handlers/extension_tools.rs` + `tools/spec_plan.rs` ExtensionToolAdapter（扩展工具注册/发现） | **缺失**：Go 无对应模块（`plugin` 包未覆盖该工具面） |
| `new_context`（`new_context_window`） | `handlers/new_context_window.rs` NewContextWindowHandler | ✅ **已补齐**（2026-08-13 复核）：`tool/handlers.go` ContextWindowHandler + `NewContextWindow` 注册项 |

### 4.2 配置键 / 平台支持

| 功能点 | Rust 证据 | Go 现状 |
|---|---|---|
| `ghost_snapshot` 配置键 | `config/src/config_toml.rs` GhostSnapshotToml | **缺失**：全仓 grep 无匹配 |
| `js_repl_node_path` / `js_repl_node_module_dirs` | 同上 | **缺失**：全仓 grep 无匹配 |
| `sqlite_home` 配置键 | `config/src/config_toml.rs`（Option\<AbsolutePathBuf\>） | **部分**：Go 用环境变量 `CODEX_SQLITE_HOME` + `state.ResolveSQLiteHome` 近似，config.toml 键未支持 |
| WSL 路径归一化 | `cli/src/wsl_paths.rs` normalize_for_wsl（非 Windows 更新命令用） | **缺失**：Go 无对应模块，WSL 场景 update 路径处理可能不同 |
| macOS process-hardening | `process-hardening/src/lib.rs`（pre_main_hardening / disable_process_dumping） | **缺失**：Go 无对应模块（`cli/dispatch.go` 仅 arg0 分派） |
| install-context 详细环境探测 | `codex-rs/install-context`（shell 类型、安装脚本路径探测） | **部分**：Go `install` 包近似（CheckForUpdate），未逐一对应 |

### 4.3 会话 / 压缩 / 预热

| 功能点 | Rust 证据 | Go 现状 |
|---|---|---|
| legacy rollout→paginated history 自动迁移 | `thread-store/src/local/rollout_migration.rs` + `rollout/src/maintenance.rs` | **缺失**（见 P1 #1，最大单项） |
| session-startup-prewarm（启动预热） | `core/src/session_startup_prewarm.rs` | **缺失**：Go 仅有 guardian 模型预热（`appserver/guardian_reviewer.go` Prewarm），无会话启动预热模块（性能特性） |
| session-rollout-init-error 结构化处理 | `core/src/session_rollout_init_error.rs` | **待核对**：Go 错误路径可能不同 |
| rollout-budget 执行逻辑 | `core/src/rollout_budget.rs` | **部分**：Go 有 usage 跟踪（`model/agent.go` CodexRolloutBudgetUnits）与 feature key（`rollout_budget`），预算执行逻辑未完全对应 |

### 4.4 TUI / AppServer / MCP / Skills / 其他

| 功能点 | Rust 证据 | Go 现状 |
|---|---|---|
| TUI 独立 snapshots 目录 | `tui/src/snapshots/` | **部分**：Go 用 `tui/tea/snapshot_test.go` 内联快照，覆盖度取决于测试数量 |
| TUI keymap 和弦捕获完整交互 | `tui/src/keymap_setup/capture.rs`（AddAlternate、和弦编辑 UI 流程） | **待核对**：Go `KeymapEdit` set/unbind/unset 与 capture 处理已接（`interactive.go` OnKeymapEdit），和弦编辑 UI 流程待复核 |
| TUI history_pagination | `tui/src/app/history_pagination.rs` | **等效**：Go 通过 appserver 分页参数实现（HISTORY_ITEM_PAGE_LIMIT 在 appserver_session），无独立文件 |
| schema fixtures 生成器 | `app-server-protocol/src/schema_fixtures.rs` | **缺失**：Go 有 `appserver/schema.go` 生成器但无 fixture 工具 |
| 通用 file-watcher 库 | `file-watcher/src/lib.rs`（FileWatcher / ThrottledWatchReceiver / DebouncedWatchReceiver） | **部分**：Go `appserver/connection_file_watch.go` 直接实现 RPC 契约（对外等价，库级组件缺失） |
| experimental_api 宏 | `app-server-protocol/src/experimental_api.rs` + macros | **机制差异**：Go 手写实验性方法（对外等价） |
| rmcp-client logging client handler | `rmcp-client/src/logging_client_handler.rs` | **缺失**：Go MCP 客户端无对应日志 handler |
| skills 完整搜索索引（skill_search） | `codex-rs/skills` + core skill_search | **待核对**：Go `skillprovider` 有 SearchRequest/SearchResult，搜索实现深度待复核 |
| connectors 独立工具集 | `codex-rs/connectors` + `core/src/connectors.rs` | **部分**：Go 并入 apps（`apps/api.go` ConnectorMetadata、feature legacy "connectors"→"apps"），独立 connectors 工具未逐一对应 |

---

## 五、P3：行为/机制不匹配（已实现但语义有差异，需行为级对齐）

| 功能点 | Go | Rust | 差异与建议 |
|---|---|---|---|
| skill MCP 依赖安装提示（#38205 相关） | `skillMCPDependencyPromptAutoApproved`：never + 沙箱禁用 → 自动安装；否则提示 | `should_install_mcp_dependencies`：approval_policy == Never → 不提示也不安装 | 语义不一致，按 P0 #38205 对齐 |
| 沙箱实现栈 | `sandbox/linuxsandbox`（bwrap 封装）、`windowssandbox`（job/ACL）、`seatbelt.go`（macOS） | 纯 Rust sandboxing + linux-sandbox（bazel）+ windows-sandbox-rs | 功能对齐但机制不同；Linux unreadable-glob fail-closed（#38026）为 Rust Linux-only，Go 不实现（N/A） |
| TUI 终端框架 | bubbletea + vt100 模拟 | ratatui + crossterm | 渲染管线不同，靠快照测试对齐；alt-screen/尺寸重排逻辑均已实现 |
| 会话存储介质 | JSONL + 内存索引（`session/store.go`） | SQLite state_db + rollout jsonl | resume/fork 行为需跨介质对齐（Go 已内嵌 SQLite 状态库做 backfill） |
| 压缩触发时机 | `compact.go` Evaluate 纯函数由 turn 循环调用 | `compact_token_budget.rs` + turn 内嵌判定 | 触发阈值/窗口计算需持续核对 |
| 远程压缩后端 | compact.go RemoteRunner 接口注入 | `compact_remote.rs` 直调后端 | 协议一致，实现方式不同 |
| apply_patch 解析器 | `applypatch/applypatch.go` 手写解析 | `apply_patch.lark`（Lark 语法文件） | 需持续跟进 Rust 语法演进（Go 测试覆盖 Rust 形状） |
| 模型目录刷新策略 | Go model 包本地缓存 + 远端 | models-manager RefreshStrategy 显式策略 | 刷新策略枚举差异待核对 |
| telemetry 导出链路 | Go 自实现事件 sink | otel（OTLP provider + trace context） | 事件形状对齐，OTLP 导出链路可能不同 |
| MCP 协议版本演进 | 单文件 `mcp/protocol_2026.go`（2026-07-28） | rmcp-client 多模式协议 | Go 以单文件跟进，需持续同步 |
| 插件市场安装事务 | Go `plugin` 包 Marketplace | core-plugins marketplace.rs + 安装策略 | 功能对应；安装事务/锁定细节待核对 |
| image resize notice | feature `image_resize_notice` + 提示片段已实现；Go 不实际改尺寸 → 不产生 notice | Rust 实际 resize 并提示 | parity.json 标记 partial；需 Go 真实 resize 后才等价 |
| macOS 发布门禁 | 47 包编译通过 Darwin/arm64，原生 macOS workflow artifact 待补 | 原生 macOS 工作流 | parity.json 标记 partial |
| SDK Windows 中断命令诊断 | Go 终止进程树并安全恢复（更优） | Rust bbbf396839 存在缺陷（PowerShell 后代残留、resume 中止） | 需上游 Rust 修复；Go 不得为模仿缺陷而回退（partial，保持现状） |
| `--stream-assistant-deltas` | Go 额外标志 | Rust 无（JSONL 事件流替代） | Go 额外能力，非缺口 |
| exec 子命令结构 | 字符串分派 + 手写参数校验 | clap 强类型子命令（conflicts_with） | 边界行为可能偏差，需用例覆盖 |
| doctor JSON 输出 | map[id]JSONDoctorCheck | 扁平 checks Vec | 结构基本对齐（有 TestJSONReportMarshalRustShape） |

---

## 六、P4：Go 额外能力（Rust 无，无需对齐，保持即可）

- `update --json`（Rust update 命令无 --json）
- 独立二进制入口 `cmd/`（codex-command-runner / codex-code-mode-host / codex-windows-sandbox-setup；Rust 为单二进制 + arg0 分派）
- 本地无模型回退 `LocalAgentRunner` / `UnavailableAgentRunner`（`model/agent.go`，测试/离线场景）
- `catalog_priority` / `openai_attribution` 工具归因（`mcp/catalog_priority.go`、`mcp/openai_attribution.go`）
- Windows 进程树终止安全（gitutil Job Object + CREATE_SUSPENDED，见 parity.json `git-process-tree-termination`）

---

## 七、N/A 清单（Rust 专用 / 纯内部 / 测试基建，Go 无需对齐）

| 项 | 原因 |
|---|---|
| `psp` 特性开关与 PSP 进程路由（#38056） | Go 无 PSP 进程 |
| network proxy credential brokerage（#38049） | Rust network-proxy crate 内部；Go network 包是 HTTP client |
| Linux bwrap glob 展开 fail-closed（#38026） | Linux-only |
| `codex sandbox` debug 模式（#38061） | Go 无 sandbox debug 子命令 |
| CI merge-commit 策略（#38051）、插件 app-server 测试自动化环境（#38189）、skills 用户回合测试（#38186）、search tool Windows 集成测试（#38184） | Rust 测试/CI 基建 |
| lru/webbrowser 依赖升级（#38172）、模型 JSON 自动更新分支等 | Rust-only 依赖/仓库维护 |
| analytics / backend-client / websocket-client crate 独立性 | Go 已有等效实现（telemetry / chatgptapi.CloudClient / gorilla-websocket 直接封装），属结构差异 |

---

## 八、分领域现状速览

| 领域 | 状态 | 主要待对齐项 |
|---|---|---|
| 1. CLI / 配置 / 认证 / doctor / install | 高度对齐（28 子命令、109/110 特性 key） | #38205 认证/审批语义；`ghost_snapshot`/`js_repl_*`/`sqlite_home` 配置键；WSL 路径归一化；macOS process-hardening；外部认证架构（P1-5）；workload identity 刷新接线（P1-6） |
| 2. 核心循环 / 会话 / 压缩 / prompt / 记忆 | 高度对齐 | legacy rollout→paginated 迁移（P1-1）；session-startup-prewarm；rollout-budget 执行逻辑；压缩触发时机 |
| 3. 工具 / 执行 / 沙箱 | 高度对齐 | `request_permissions` / `test_sync` / `extension_tools` 工具；exec-server 启动重试（P1-9）；executor fs-helper fail-closed（P1-7）；apply_patch 语法演进跟踪 |
| 4. TUI | 高度对齐（目录镜像） | keymap 和弦捕获完整交互待复核；snapshots fixture 方式 |
| 5. AppServer / 协议 / code-mode / realtime | 高度对齐 | gRPC code-mode host 服务（P1-8）；file-watcher 库级组件；schema fixtures 工具 |
| 6. 模型 / API / 特性 / rollout / 状态 / 遥测 / 网络 | 高度对齐（特性 key 109/110） | `psp`（N/A）；模型目录刷新策略；OTLP 导出链路；execution-host cloud config（P1-10） |
| 7. MCP / Skills / 插件 / 代理 / hooks | 高度对齐 | #38205 delegate 审批；MCP 审批 reviewer 按 server 解析（P1-2）；持久化 policy-amendment（P1-3）；统一审批遥测（P1-4）；connectors 工具；skills 搜索深度；MCP 日志 handler |

---

## 九、建议的对齐执行顺序

1. **P0 #38205**（1 个提交，规模小）：delegate 审批策略 + skill MCP 依赖 never 语义，落地 `agent` / `exec` / `appserver` / `turn_runtime.go` 并补回归测试。
2. **P1-2/3/4 #38108 余项**：MCP 审批 reviewer 按 server/connector 解析、持久化 policy-amendment、统一审批遥测（与 #38205 审批面同域，合并处理）。
3. **P1-6 workload identity 刷新接线 + P1-5 外部认证架构**（认证域合并）。
4. **P2 条件性工具**：`request_permissions` → `test_sync` → `extension_tools`（对照 Rust handlers 逐个移植）。
5. **P1-1 legacy rollout 迁移**（最大单项）：按 Rust rollout_migration 的 dry-run/apply/journal/原子发布实现状态机，接入 `background_paginated_rollout_migration` feature。
6. **P2 配置键 / 平台项**：`sqlite_home` → `ghost_snapshot` → `js_repl_*` → WSL 路径 → macOS process-hardening（按用户价值排序）。
7. **持续项**：P3 行为级差异纳入 sdktests / tuialign 差分场景；每个新上游提交按 `codex-go-update` 流程审计入台账。

---

## 十、验证方式

- 单元级：`go test ./...`（含 `LikeRust`/`RustShape` 断言族）；`go vet ./...`；`go test -race ./mcp ./tool ./session ./appserver ./execserver`。
- 协议级：`appserver_v2_suite_manifest_test.go`（Rust 协议面清单）、`rust_protocol_surface_test.go`。
- 端到端：`sdktests`（SDK 事件差分）、`tuialign`（TUI 截图/事件差分），双实现跑同一场景矩阵对比协议行为与工作区副作用。
- 上游跟踪：每次 `git fetch origin` 后按 `update/plan_年月日*.md` 模板审计新提交，分类为 Implement / Equivalent / N/A，并更新 `parity.json` 登记。

---

> 附录：本报告结论依据
> - `func.md`（2026-08-07 源码级全量比对，237 项）
> - `parity.json`（对齐登记，`rustBaseline aac9f84247`，含 partial 项）
> - `update/plan_2026_08_12*.md`、`update/plan_2026_08_11*.md`（跟踪台账）
> - 2026-08-13 当日源码复核：工具注册（`tool/handlers.go`）、特性 key 对比（`features/features.go` vs `codex-rs/features/src/lib.rs`，109/110 仅差 `psp`）、TUI 目录镜像、`turn_runtime.go` skill MCP 依赖、上游 `95aada11c4`（#38205）diff 审阅
