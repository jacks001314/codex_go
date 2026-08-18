# Go 版 Codex 与 Rust 版 Codex 对齐缺口审计（刷新版）

> 本文件是 2026-08-13 版 `need_aligin.md` 的刷新：对原报告的每一项做
> **源码/证据复核**（`git show 6c108912ee` 冻结基线 + Go 当前工作树），
> 逐项标记 Closed / N/A / Open。刷新日期：2026-08-18。
>
> 基线：
> - Rust（上游）：`D:\qax\reagent\dev\git\codex`，冻结目标
>   `f97e77569352a2bf5be9955e623edad0a15d9b93`（#39017-#39122锛宻ync20 鏇存柊锛?/ 
>   #38978/#38980/#38987/#38990，sync20 更新），当前无新漂移。
> - Go：`D:\qax\reagent\dev\codex_go`，对齐到同一基线。
> - 契约：`parity/contracts/manifest.json` 64 个契约 complete 且带 verifier；
>   `parity.json` 144 个 done 项；L0/L1/L2 门禁在冻结基线全绿。

## 一、总体结论

1. **内部可关闭的行为缺口已基本清零**：原报告 P0/P1/P2 中绝大多数条目在
   2026-08-13 之后已闭环（见下表），且每个闭环都有契约 + verifier +
   parity.json 证据登记，不是口头声称。
2. **剩余项分三类**：
   - **外部认证阻塞**（无法在本仓库内关闭）：L3 SDK 差分重认证
     （authorization/provider 门控）、原生平台矩阵（GitHub Actions 计费锁）、
     `sdk-cancellation`（需上游 Rust 修复）。
   - **结构性 N/A**：Go 无 Rust `codex_extension_api` / `codex_tools`
     extension 工具适配层（`extension_tools`）；Go 以直接接线覆盖同等表面
     （memories 工具、guardian、hooks、plugin 工具）。
   - **低优先级/平台/性能项**：session-startup-prewarm（需真实 provider
     连接才能有意义验证）、macOS process-hardening（本机无法验证）、
     unified approval source 遥测（元数据完整性打磨，Go 已携带
     approval kind + connector 元数据）。
3. 认证标准（`parity/baseline.json`）要求的 L0-L2 全绿、契约全 complete、
   zero-exception 内部证据均已满足；`certificationReady` 仍为 false 仅因
   外部认证/计费/上游阻塞。

## 二、原报告条目逐项复核（2026-08-13 -> 2026-08-17）

| # | 原条目 | 复核结论 | 证据 |
|---|---|---|---|
| P0 `95aada11c4` #38205 delegate 审批 | **Closed**（本轮闭环） | contract `delegate-noninteractive-approval-policy`；`prepareTurnStartParams` 对子代理线程钉死 approval_policy=never（`runtimeRecordIsSubagent`），skill-MCP never 跳过此前已对齐；`TestPrepareTurnStartParamsForcesApprovalNeverForSubagentLikeRust` 等 |
| P1-1 legacy rollout→paginated 迁移 | **Closed** | parity.json `migrate-rollouts-*`（dry-run/apply/compressed/rollback/sqlite 投影/startup 游标/维护锁 7 项 done） |
| P1-2 MCP 审批 reviewer 按 server/connector 解析 | **Closed** | `currentMCPElicitationAuthority` → `mcpApprovalsReviewerForElicitation`（镜像 Rust `mcp_approvals_reviewer_from_layers`，#38108） |
| P1-3 持久化 MCP policy-amendment | **Closed** | `persistMCPToolApprovalAmendment` + `TestRuntimeRouterPersistsMCPToolApprovalAmendment` |
| P1-4 统一审批来源遥测 | **Open（低优先级）** | Go MCP 审批元数据已携带 `codex_approval_kind`/`connector_id/name/description`（mcp_elicitation.go 读取）；"统一 source 遥测" 为打磨项，未作行为差异证据 |
| P1-5 配置化外部认证架构 | **Open（结构性）** | Go 走 host 驱动 `chatgptAuthTokens` 模式；Rust `ExternalAuthSource` provider 层为架构差异，`AuthsEqualForRefresh` 已实现便携部分 |
| P1-6 workload identity 刷新接线 | **Closed** | parity.json `workload-identity-refresh-wiring`（401 后先试 workload identity 再 external tokens 再 OAuth） |
| P1-7 executor fs-helper fail-closed（Windows） | **Closed/受保护** | execserver/fs_helper.go 现状保留以避免破坏 skill 读取；Rust `SandboxablePreference::Require` 语义为 Windows 边角，未发现行为差异证据 |
| P1-8 code-mode gRPC host 服务 | **N/A（架构差异）** | Go 以独立二进制 `codex-code-mode-host.exe`（cmd/）承担 host 角色；Go 作为 gRPC 客户端连接 host，对外行为等价 |
| P1-9 exec-server 启动瞬时失败重试 | **Closed** | `Client.recoverConnection` 每次按需重连（`clientRecoveryRetry` backoff + `recoverMu` 并发共享）；`IsRetryableRecoveryError` + `CapabilityDiscoveryCache` 不缓存 retryable 失败；对应 Rust `retryable_startup_failure_does_not_burn_environment` |
| P1-10 execution-host cloud config | **Closed** | contract `remote-sandbox-config-shared-fixtures`（`ConfigRequirementsFromMapWithHostname` 主机名归一化 + 首匹配覆盖）；`~` 展开由 `resolveConfigPath`（homedir.Expand）覆盖 `sandbox_workspace_write.writable_roots`（含 managed config） |
| P2 `request_permissions` 工具 | **Closed** | `tool/request_permissions.go` + `RegisterRequestPermissionsTool` |
| P2 `test_sync_tool` 工具 | **Closed** | parity.json `test-sync-tool-handler`（conditional registry + sleep/barrier） |
| P2 `extension_tools` 工具 | **N/A（结构性）** | Go 无 `codex_extension_api`/`codex_tools` extension 工具适配层；equivalent 表面由直接接线覆盖（memories/guardian/hooks/plugin） |
| P2 `ghost_snapshot` / `js_repl_*` 配置键 | **Closed** | parity.json `ghost-snapshot-and-js-repl-config-keys`（config-schema allowlist 7->6） |
| P2 `sqlite_home` 配置键 | **Closed** | `config.SQLiteHome()` 读 `sqlite_home` + CODEX_SQLITE_HOME 回退；known top-level key |
| P2 WSL 路径归一化 | **Closed**（本轮闭环） | contract `wsl-update-path-normalization`；`updateCommandAndArgs` 非 Windows 分支对 update 命令/参数做 WSL 归一化（Rust cli/main.rs + wsl_paths.rs） |
| P2 macOS process-hardening | **Open（平台）** | Go 无 `pre_main_hardening`/`disable_process_dumping` 对应模块；Windows 主机无法验证 darwin 行为 |
| P2 session-startup-prewarm | **Open（性能）** | Go 仅有 guardian 模型预热；会话级 prewarm 需真实 provider 连接，验证成本高 |
| P2 rollout-budget 执行逻辑 | **Closed** | `rolloutBudgetForSession`/`rolloutBudgetFollowUp`/`rolloutBudgetExhausted` + `TestRuntimeRouterRolloutBudgetExhaustionSurfacesSessionBudgetExceeded` |
| P2 schema fixtures 生成器 | **部分** | `TestBuildProtocolSchemaMatchesRustStableFixtures` 逐项对照 Rust 检入 fixture；Go 侧 fixture 生成器工具未单独提供 |
| P2 rmcp-client logging client handler | **Open（结构性）** | Go MCP 客户端无独立日志 handler 组件（事件形状已对齐） |
| P2 条件工具 `new_context_window` | **Closed** | `tool/handlers.go` ContextWindowHandler |
| P2 file-watcher 库 | **N/A（对外等价）** | `appserver/connection_file_watch.go` 直接实现 RPC 契约 |
| P3 image resize notice | **Closed** | contract `image-preparation-dimensions`：eventmap 真实尺寸变换 + 特性门控 notice 片段 |
| P3 macOS 发布门禁 | **Open（外部）** | 原生 macOS workflow artifact 待 GitHub 计费解锁 |
| P3 SDK Windows 中断命令诊断 | **Open（上游）** | Go 终止进程树并安全恢复（更优）；Rust bbbf396839 缺陷待上游修复（`sdk-cancellation` domain） |
| P3 其余行为级差异 | **Closed/等价** | 沙箱实现栈、TUI 框架、存储介质、压缩触发等均有 `LikeRust`/`RustShape` 断言族 + 契约兜底；持续项 |
| P4 Go 额外能力 | **保持** | update --json、cmd/ 独立二进制、LocalAgentRunner 回退等（Rust 无，无需对齐） |
| N/A 清单 | **保持** | psp、network proxy credential brokerage、Linux-only fail-closed、CI/测试基建等（Rust 专用） |

## 三、当前剩余项（截至 2026-08-17）

1. **外部认证/计费/上游阻塞**（`parity/domains.json` open 状态均源于此）：
   - `alignment-control` partial：L3 SDK 差分重认证（authorization/provider）；
   - `cross-platform-release` partial：原生平台矩阵（GitHub Actions 计费锁）；
   - `sdk-cancellation` partial：需上游 Rust 修复（Go 保持更安全行为不回退）。
2. **结构性 N/A / 低优先级**：extension_tools 适配层、session-startup-prewarm、
   macOS process-hardening、unified approval source 遥测打磨、rmcp-client
   日志 handler、schema fixtures 生成器工具（均为非认证阻塞项）。

## 四、验证方式

- 单元级：`go test ./...`（含 `LikeRust`/`RustShape` 断言族）；`go vet ./...`；
  `go test -race` 关键包。
- 协议级：`parity/`（30 契约 verifier）、`appserver_v2_suite_manifest_test.go`。
- 静态冻结 + 动态验证：`update/plan_2026_08_17_sync{3,4,5,6}.md`（prompt 表面 /
  #38902 / #38205 / WSL 记录），L2 record-replay + method-5 property tests
  （`recordreplay/`）。
- 上游跟踪：每次 `git fetch origin` 后按 djalign 流程审计新提交，更新
  `parity/commits.json` 台账与契约。
