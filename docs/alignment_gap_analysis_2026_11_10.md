# Go 版 Codex 对齐现状与上游增量分析（2026-11-10）

本文件接续 `docs/alignment_gap_analysis_2026_11_08.md`，只记录 11-08 之后的变化与当前可复核读数。
所有结论都给出代码位置或命令，便于复验；未实测的条目一律标为“需审计”，不冒充结论。

## 结论

1. **静态层已全绿**：`go test ./parity -count=1` 62s 通过，`update/ledger_verify_2026_11_08.py` 为 `GATE: PASS`（3690 条、全部 40 位 sha、0 个 open 状态、闭环均有 evidence）。
2. **缺口性质已从“类”转“量”**：账本 3016 complete / 639 not_applicable / 31 partial / 4 blocked_precondition；剩余量集中在 UI tree（9 条）与 Guardian 证据链（6 条），其余是 TUI 体验尾巴和单点行为。
3. **上游仍在快速前进**：本地镜像锚点 `e54a5713`（11-09）之后 `main` 又走 38 条到 `39b42aaacd`（11-10）。初判 2 条构建/依赖类 N/A、2 条已对齐、9 条确认缺口（其中 4 条很小），2 条部分对齐，其余 23 条需逐条审计。
4. **认证仍不可达，且原因不在代码**：5 个 domain、1 个 contract 全部卡在平台原生、GitHub 计费、L3 授权或上游缺陷上。

## 一、当前读数

| 事实源 | 读数 |
| --- | --- |
| `parity/commits.json` | 3690 条：3016 complete / 639 not_applicable / 31 partial / 4 blocked_precondition |
| `parity/domains.json` | 26 域：21 关闭，5 open（见第五节） |
| `parity/contracts/manifest.json` | 138 个契约：137 complete / 1 partial（`mcp-network-policy-derivation`） |
| `parity.json` | 232 项：225 done / 3 partial / 4 intentional_difference；`generatedAt` 仍为 2026-10-28，`rustUpstreamHead` 仍为 `67f0994b` |
| `parity/baseline.json` | `target.rustCommit = e54a5713`，`verified.rustCommit = 67f0994b`，`certificationReady: false` |
| 静态门禁 | `go test ./parity` 通过；`ledger_verify` GATE PASS |

`verified.rustCommit` 不前移是有意为之：它绑定 `parity.json` 的 SDK 证据，没有新的 parity/SDK 运行就前移等于伪造认证，`TestAlignmentBaselineIsFrozenAndTraceable` 也会挡住。

## 二、上游 delta `e54a5713..39b42aaacd`（38 条）

判定四档：**已对齐**（Go 有等价实现，补测试/台账即可）、**缺口**（有代码证据表明行为不同）、**需审计**（主题相关但未逐行核对）、**N/A**（构建/CI/发布专用）。

| # | PR | 主题 | 初判 | 依据 |
| --- | --- | --- | --- | --- |
| 1 | #60519 | 恢复历史中的未完成工具调用 | 已对齐(有差异) | `session/history.go:232` 已对缺失输出合成 aborted，模型可见输入一致；差别在 Rust 仅对 restored 序列内的调用放宽并对其余报错，Go 一律放宽、不报错 |
| 2 | #60517 | sandbox CLI 走共享 manager | 需审计 | Rust 改 `cli/src/debug_sandbox.rs` + `core/src/exec.rs` |
| 3 | #60513 | MCP 刷新记录 permission grant 原因 | 缺口(小) | Go 无 refresh reason 字段（`mcp`/`appserver` 均未命中） |
| 4 | #60511 | inference hook 拒绝视为终态错误 | 缺口(中) | Go 无 hooks 推理拒绝对应的错误类型/终态判定 |
| 5 | #60507 | Windows MSVC Bazel 用 hermetic LLVM | N/A | 构建专用 |
| 6 | #60506 | 关闭 tracing 时保留 MCP trace context | 需审计 | — |
| 7 | #60505 | MCP 刷新原因与插件元数据追踪 | 需审计 | 与 #60513 同族 |
| 8 | #60500 | sandbox 遥测标签直接取自 permission profile | 需审计 | 涉及 `session` + `otel` 事件字段 |
| 9 | #60498 | 统一 Guardian 各 reviewer 的上下文准备 | 缺口(中) | Go 有 `guardiancontext`，但 preparation 统一层未对齐 |
| 10 | #60496 | 保留 code-mode gRPC 会话准入的 typed 错误 | 需审计 | Go 有 codemode gRPC 客户端 |
| 11 | #60492 | Claude Code 本地技能迁移 | 缺口(小) | `config/external_core_import.go:116` 无 `claude-code-local-skills`，`:131` 无 `.trash` 排除、无 `synced` 语义 |
| 12 | #60489 | app-server daemon 启动尊重 feature 覆盖 | 需审计 | — |
| 13 | #60488 | 移除 Xcode 26.4 MCP 兼容 workaround | 需审计 | 兼容代码是否存在于 Go 需确认 |
| 14 | #60487 | 默认启用 untrusted context 工具输出 | 缺口(中) | `features/features.go:389` 仍是 UnderDevelopment/false；Rust 已 Stable/true |
| 15 | #60486 | app-server gateway OAuth 增加设备授权 | 需审计 | 涉及 `ClientRequest.json` 新方法 |
| 16 | #60482 | 各传输层统一 context length 错误 | 部分 | `model/responses_stream.go:2904` 已映射 code；websocket 传输等价性需确认 |
| 17 | #60481 | 非法 access program 流错误视为终态 | 需审计 | — |
| 18 | #60480 | workspace access 拒绝视为终态 | 需审计 | — |
| 19 | #60479 | content policy 违规视为终态 | 部分 | Go 已把 `cyber_policy`/`misalignment_policy_violation` 等列为 fatal（`model/responses_agent_test.go:4078`） |
| 20 | #60476 | access program 拒绝视为终态 | 需审计 | — |
| 21 | #60475 | 不再重试显式 invalid-workspace | 需审计 | — |
| 22 | #60473 | 本地压缩遵守错误重试策略 | 需审计 | `compact` 路径的重试分类 |
| 23 | #60472 | 默认线程权限按 permission profile 序列化 | 已对齐 | `rollout/canonical_migration_permission_policy_rust_shape_test.go` 已覆盖 permission_profile 形状 |
| 24 | #60466 | 诊断日志失败不影响线程存储 | 需审计 | — |
| 25 | #60462 | TUI 问题回答走 Cloud 消息准入 | 需审计 | Cloud 路径 |
| 26 | #60461 | executor 能力发现尊重 permission grant 范围 | 缺口(中) | `execserver/capability_discovery.go` 无 grant scope 维度 |
| 27 | #60455 | macOS 优先原生剪贴板图片并修正方向 | 需审计 | darwin 专用，本机无法验证 |
| 28 | #60454 | 升级 OpenSSL 依赖 | N/A | 依赖升级 |
| 29 | #60443 | realtime 附件支持取消与超时 | 需审计 | `realtime` 包 |
| 30 | #60436 | 预算检查后重新校验请求 auth 归属 | 需审计 | `runtimeauth` 相关 |
| 31 | #60435 | 账号额度恢复后关闭模型切换提示 | 需审计 | TUI banner |
| 32 | #60434 | turn 开始时重置 app-server turn summary | 需审计 | `appserver` thread state |
| 33 | #60433 | workload identity 源复用绑定 session claims | 需审计 | Go 已有 workload identity 刷新契约，绑定粒度需核对 |
| 34 | #60432 | 捆绑 SQLite 升到 3.54 | 缺口(小) | `state/sqlite.go:23` 最低 3.51.3，嵌入版本需同步 |
| 35 | #60423 | 资源列表暴露 deferred MCP server | 缺口(中) | `mcp` 有 deferred tool 暴露，但 resource listing 路径未覆盖 |
| 36 | #60418 | 后台终端运行时状态可变为 done | 缺口(小) | `tui/tea/program_status.go:82` 仍把 `UnifiedExecProcesses` 计入 Working；Rust 已删除该条件 |
| 37 | #60416 | 全屏 Markdown 保留可容纳的 URL | 需审计 | `tui/markdown_render` |
| 38 | #60414 | gateway OAuth 下启用模型目录发现 | 需审计 | Go 有 gateway OAuth 测试 |

统计：已对齐 2、缺口 9（其中 #60513/#60492/#60432/#60418 均为小改动）、部分 2、N/A 2、需审计 23。

## 三、已确认的小/中缺口

### 3.1 `untrusted_as_tool_output` 默认值（#60487）

- Rust：`FeatureSpec { stage: Stable, default_enabled: true }`。
- Go：`features/features.go:389` 仍为 `StageUnderDevelopment, DefaultEnabled: false`。
- 行为实现已存在（`appserver/additional_context.go:295` 读该特性），缺的是默认值翻转 + 测试期望更新；Rust 侧同一 PR 改了 core/app-server/TUI/SDK 四处期望，Go 需同步改 `appserver/additional_context*_test.go` 等显式传 `= true` 的用例。

### 3.2 后台终端不应阻止 Done（#60418）

- Rust 删除了 `!self.unified_exec_processes.is_empty()` 条件。
- Go：`tui/tea/program_status.go:82` 仍保留 `len(m.commandLifecycle.UnifiedExecProcesses) > 0`。
- 影响：turn 结束后仍有后台终端时，终端状态停在 Working，无法进入 Done/Idle。

### 3.3 Claude Code 技能迁移（#60492）

- 缺 `claude-code-local-skills` 迁移源（排除 home `skills/synced`）。
- 所有模式都应排除 home `skills/.trash`，默认模式导入 `synced` 但排除其中嵌套 `.trash`。
- Go 现状：`config/external_core_import.go:116` 只按目录枚举，`:131` 无任何排除规则。

### 3.4 `account/securitySetup/read` RPC（#60082）

- 这是 `app-server-schema` domain 的唯一缺口，也是 `ClientRequest.json`/`.ts` 与 Rust fixture 的唯一差异。
- Go 能力已具备：`app/security_setup.go:19`（本地身份复核）+ `chatgptapi.GetSecuritySetup`。
- 缺：在 `BuildProtocolSchema` 暴露该 method、app-server 处理（含 runtime auth generation 校验、3s 超时）、TUI 改走 app-server。

### 3.5 MCP network policy（#60046）

唯一 open contract（`mcp-network-policy-derivation`）。需按上游 PR 收口策略推导与“已保存拒绝”保留逻辑，涉及 `network/`、`execpolicy/`、`appserver/`、`mcp/`。

### 3.6 SQLite 3.54（#60432）

Rust 为 WAL 修复把捆绑版本升到 3.54；Go 最低版本常量仍为 3.51.3（`state/sqlite.go:23`），实测 `modernc.org/sqlite v1.46.2` 嵌入 3.51.3，当前可用的 `v1.57.0` 只到 3.53.3，因此这一步受上游 Go 驱动版本约束，不能只改常量。`sqlite-runtime-version` domain 还要求六平台原生产物证据。

## 四、31 条 partial 的分布

| 主题 | 条数 | 共同根因 |
| --- | --- | --- |
| UI tree / 扩展渲染 | 9 | `tui/ui_tree.go` 缺继承样式、语义配色、文本规范化、多行/折行、prepare/render 分离、terminal buffer 直写 |
| Guardian 证据链 | 6 | `agentboard/`、`guardiancontext/` 缺只读证据检索、订阅、模型驱动频道选择、按源顺序投递、有界原始证据、与本地句柄解耦 |
| TUI 侧栏/侧聊 | 5 | 动画侧滑、浮动面板、tab 命中测试、deferred MCP 通知、继承权限 profile |
| 模型/Provider/Code Mode | 4 | 线程创建选项选模型、非交互 provider、增量工具消息覆盖 |
| 其它 | 7 | Cloud 服务端所有权残余、usage turn_trigger 传播、plugin 提示词上限、异步 host service-tier、answer moderation display instructions 等 |

这些都是**已判定有残余**的条目，不是未审计噪声。

## 五、结构性/外部阻塞（12 项，均非本仓代码可单独关闭）

- **平台原生 4 条**：`#40625`（agent_navigation 关闭代理语义）、`#42841`/`#55378`（Windows 原生 MXC）、`#43624`（macOS Secure Enclave）。前三条需要平台原生绑定与真机验证。
- **5 个 open domain**：
  - `alignment-control`：L3 SDK 差分需 authorization/provider 门控 + 3 次连续干净运行（当前记录 2 次）。
  - `sqlite-runtime-version`：六平台原生 SQLite ≥3.51.3 产物，目前只有 windows/amd64。
  - `app-server-schema`：即 3.4 的单一缺口。
  - `sdk-cancellation`：等上游修 `bbbf396839` 的 PowerShell 孤儿进程；Go 保持更安全的进程树终止，不回退。
  - `cross-platform-release`：GitHub Actions 账号计费锁，macOS runner 零步骤失败。
- **1 个 open contract**：`mcp-network-policy-derivation`。

## 六、建议顺序

1. **P0（半天量级，先收 4 个小口）**：#60418、#60492、#60487、#60432。前三项都有明确代码位置，改完补 `LikeRust` 测试即可；#60432 只改版本常量并记录产物证据缺口。
2. **P1（1-2 轮）**：#60082（关闭 app-server-schema 缺口）、#60423（deferred MCP 资源）、#60046（唯一 open contract）、#60513（refresh reason）。
3. **P1.5 审计批**：把 21 条“需审计”按错误终态（#60473-#60482 一组）、MCP/trace（#60505/#60506/#60513）、权限 profile/telemetry（#60461/#60500/#60472 已对齐）三簇过一遍，能对齐的补测试锁住。
4. **P2（大块）**：Guardian 证据链 6 条、UI tree 9 条；先出设计再动手，避免逐条打补丁。
5. **外部依赖**：GitHub Actions 计费、L3 SDK 授权、上游 `bbbf396839`。

## 七、验证命令

```
go test ./parity -count=1                      # 62.478s，ok
python update/ledger_verify_2026_11_08.py      # GATE: PASS
git -C <rust> log --oneline e54a5713..39b42aaacd   # 38 条
```

后续每落一条，按 `djalign` 规则：先缩最小复现，改完跑相关包测试，最终把差异沉淀为契约（fixture/golden/manifest 条目）再回写台账。


