# Codex Go 上游功能同步计划

更新日期：2026-07-21
Go 仓库：`D:\qax\reagent\dev\codex_go`
上游仓库：`D:\qax\reagent\dev\git\codex`
上一轮上游基线：`315195492c`（2026-07-16，`Support custom transports for Amazon Bedrock`）
本轮目标基线：`1e20272fa5`（2026-07-20，`Avoid cloning thread history for token usage replay`）
本轮提交范围：`315195492c..1e20272fa5`，共 81 个提交、571 个变更文件（+27262/-4697）
最近可达 Rust tag：`rust-v0.143.0-alpha.10`；本轮以 commit 固定基线，不以滞后 tag 代替 commit
Go 比对基线：`eeacecb`（2026-07-18）；工作区另有未提交修改，执行同步时不得覆盖

## 1. 目标

将 Go 版本同步到上述 `openai/codex` Rust 基线，重点保证以下外部行为兼容：

- CLI 参数、输出、错误文本和退出码。
- app-server v2 JSON-RPC method、schema、通知和错误码。
- Responses 请求、raw response、usage 和缓存 token 统计。
- 配置加载、Agent 角色、模型 provider 和会话恢复语义。
- MCP、skills、插件、终端进程和多 Agent 工具行为。
- TUI 历史编辑、会话分叉、分页历史和恢复行为。
- Windows、Linux、macOS 的平台安全与沙箱语义。

本计划不以文件数量或主观百分比作为完成依据。每项功能必须由上游源码、schema、fixture、snapshot 或集成测试证明。

## 1.1 本轮差异结论（2026-07-20）

对 `315195492c..1e20272fa5` 的提交、schema 和关键运行时源码完成首轮分类。必须同步的外部行为包括：

- app-server 新增 `app/installed` 与实验性 `thread/searchOccurrences`，并扩展 thread/turn/realtime schema。
- 用户输入协议新增远程音频与本地音频；动态工具和 code mode 输出新增音频内容项。
- 插件安装响应新增 interstitial requirements，share update discoverability 增加发布语义。
- 配置新增 `resume_cwd`、`SessionEnd` hooks 与 `executor_capability_discovery`，world state 开始显式跟踪 collaboration mode、permissions 和 realtime 状态。
- 分页 thread history 增加 legacy view、继承 prefix、持久化名称和 occurrence search；不支持的 history mode 必须明确拒绝。
- exec policy 增加旧 allow rules 迁移；远程 executor 增加 managed network proxy 与批量 capability discovery。
- TUI 新增恢复目录选择、只读父级 Agent thread、批量历史搜索、音频输入、inline visualization 和有界流式命令预览。

2026-07-21 完成审计补充：SessionEnd 已覆盖 RuntimeRouter graceful shutdown；external-agent session import 已传播稳定 `subErrorType`；external-agent memory 已支持项目检测、SHA-256 校验、scope/provenance metadata、路径隔离和过期资源清理；realtime V3 已增加 `initialItems`、`codexResponseHandoffMode` 和 appserver 流式 Codex handoff 路由，支持 commentary/final 通道、最终消息前缀、UTF-8 安全截断及 client-managed/codexResponsesAsItems 跳过；inline visualization 已改为 Rust 的 `::codex-inline-vis{file="..."}` 指令并使用受限 viewer；compressed rollout doctor inventory 已支持 `.jsonl.zst` canonical path、普通文件优先和损坏压缩文件诊断；active-turn completion backfill 已确认为 Go direct-agent exec 架构差异。本轮 README、候选版本追踪和发布说明已收口；剩余的是 Linux/macOS 原生发布门禁。

当前自动漂移检查结果：

- `go test ./parity -count=1` 在新 Rust HEAD 上失败，已确认 Rust exec fixture `18 -> 19`、TUI snapshot `576 -> 599`、`Cargo.toml` hash 漂移。
- feature surface 出现 `executor_capability_discovery` 差异：Go 已提前声明该 key，Rust 新基线现已正式加入，因此需要刷新 parity 预期，而不是删除 Go feature。
- Go 顶层目录 inventory 还报告本地 `bin`、`deliverables`；这是仓库清单维护问题，不能混入 Rust 功能缺口统计。

纯 Rust 内部的 clone/缓存/布局优化、无用户行为变化的 helper 清理、Bazel/发布元数据变化不逐文件移植；只有可复现的性能、内存上限或交互行为才进入 Go 验收。

## 1.2 本轮同步批次

### Batch 8：固定新基线并恢复漂移门禁（P0，0.5-1 天）

- [x] 将 parity baseline、关键文件 hash、workspace/fixture/TUI snapshot manifest 更新到 `1e20272fa5`。
- [x] 更新 app-server method/schema、feature、ConfigToml 和 CLI surface 探测，先输出真实 missing/partial 清单。
- [x] 将 `bin`、`deliverables` 明确标记为 Go 本地产物目录或移出 inventory，避免污染上游差异。
- [x] 更新 `parity.json` 的 baseline、生成时间、证据和状态；禁止在功能未实现前批量标记 `done`。

验收：`go test ./parity ./features ./cli -count=1`

### Batch 9：app-server 与输入/输出协议（P0，3-5 天）

- [x] 实现 `app/installed`：force refresh、已安装 runtime state、runtime name、工具策略投影及上游错误语义。
- [x] 实现实验性 `thread/searchOccurrences`：分页大小、opaque cursor、可见消息过滤和时间顺序。
- [x] 对齐 audio/localAudio 用户输入、模型 input modalities、Responses 转换与历史过滤。
- [x] 对齐动态工具/code mode 音频输出 content item、事件映射、序列化和 app-server 通知。
- [x] 对齐 plugin interstitial requirements、share publish discoverability 及 nullable/required 规则。
- [x] 用新 Rust schema 全量验证 method、params、response、notification 和错误码。

验收：`go test ./appserver ./protocol ./model ./codemode ./plugin ./apps -count=1`

### Batch 10：会话、分页历史与持久化（P0/P1，3-5 天）

- [x] 支持 paginated thread legacy view，并保持 read/resume/list/fork/rollback 的 cursor 与可见历史一致。
- [x] 保存并恢复 inherited rollout prefix 与 thread name，覆盖 SQLite、rollout、thread store 和远程 thread store。
- [x] 批量读取 message history，供 reverse search 使用；实现 occurrence search 的持久层查询。
- [x] 加载 rollout 时拒绝不支持的 history mode，错误必须可区分且不产生部分恢复状态。
- [x] 优化 token usage replay 时不得复制完整历史，并用大历史 fixture 验证有界内存行为。

验收：`go test ./history ./session ./rollout ./state ./appserver ./tui/... -count=1`

### Batch 11：配置、world state 与生命周期（P1，2-4 天）

- [x] 增加 `resume_cwd = current|session`，覆盖 resume/fork、本地/远程 session、记忆选择和持久化失败回退。
- [x] 增加 `SessionEnd` hook，在正常结束、归档、进程退出和失败清理时只执行一次，并发送 started/completed 通知。
- [x] 将 collaboration mode、permission instructions、realtime conversation state 纳入 world state diff/replay，保证 fork/resume 首轮上下文一致。
- [x] 补齐 external agent session import 的详细错误类型和来源标记。

验收：`go test ./config ./context ./session ./appserver ./tui/... -count=1`

### Batch 12：执行器、网络代理与安全迁移（P1，3-5 天）

- [x] 实现 batched executor capability discovery，一次发现选定 roots 的 plugin/skill manifest，并与现有单项发现兼容。
- [x] 在 remote executor 启动和回收 managed network proxy，保持认证、作用域、端口和失败清理语义。
- [x] 迁移旧 exec policy allow rules；迁移需幂等、可审计，不能扩大权限或覆盖显式新规则。
- [x] 覆盖多 exec-server workspace write isolation、zsh tied PATH snapshot 和 code-mode yield grace period。

验收：`go test ./execpolicy ./execserver ./network ./sandbox/... ./tool ./appserver -count=1`

### Batch 13：TUI 行为同步（P1/P2，4-7 天）

- [x] 父级拥有的 sub-agent thread 在 TUI 中只读，输入、设置快捷键和审批入口均需拒绝且提示一致。
- [x] 对齐 Rust slash command popup：恢复 `/btw`、`/multi-agents`、`/quit` 的独立 canonical 项；`/fast` 仅在当前模型 catalog 声明 fast tier 时出现，使用实际 tier ID 与 default 切换、持久化 config alias，并传播到本地/远程 turn。
- [x] 逐项审计并补齐 slash command dispatch：`/clear` 对齐 Rust `ClearUi`；`/app` 启动 Codex Desktop thread deep link，`/rollout` 读取活动 thread rollout path，`/sandbox-add-read-dir` 校验并加入 readable root，`/import` 调用 external-agent detect/import，`/memories` 提供真实设置 modal 和配置持久化。
- [x] 恢复/分叉时实现 CWD 选择与 remembered mode；path-backed Agent 可在 picker 中选择并正确恢复。
- [x] 历史搜索使用 bounded batch lookup；大历史、空结果、并发切换 thread 时不阻塞或串线。
- [x] 支持音频附件的提交、恢复、模型能力过滤和 unsupported media 提示。
- [x] 支持 inline visualization directive 的流式跟踪、最终链接和 overlay viewer；终端不支持时保留文字内容。
- [x] 流式 Markdown 增量渲染和命令输出预览必须有界；最终 transcript 不得因预览裁剪丢失完整结果。
- [ ] TUI bootstrap 请求并行化、重绘/缓存优化只以无竞态、无错序和可测性能收益为验收标准（Go 启动阶段没有 Rust 对应的双远程请求；已记录为 intentional difference，仍需补启动链路证据）。

验收：`go test ./tui/... -count=1`，并补充大输出、窄终端和快速切换 session 的 PTY/snapshot 测试。

### Batch 14：收口、平台验证与发布（P2，2-4 天，不含平台排队）

- [x] 更新 Rust fixture/TUI snapshot 证据并清空所有未解释 parity failure。
- [x] Windows 执行全量、race、ConPTY、WFP/ACL 与远程 executor 回归。
- [x] Linux 执行 Landlock/bwrap、Unix socket、network proxy 与 zsh 回归；native Kali runner evidence recorded below.
- [ ] macOS 执行 seatbelt、native trust roots、Unix socket 与 desktop app 回归；Go seatbelt backend、Unix socket allowlist 和 workflow 已实现，需在 macOS runner 执行并保存 artifact 后勾选。
- [x] 更新 README、版本追踪、`parity.json` 和发布说明；候选版本使用 `go-1e20272fa5-parity.1`，不沿用旧 `v0.145.0-alpha.20` 候选名。

验收：`go test ./... -count=1`；支持 race 的平台执行 `go test -race ./... -count=1`。

## 2. 同步范围与状态

### P0：协议和核心运行时

| 上游更新 | 上游提交 | Go 当前判断 | Go 主要位置 | 状态 |
| --- | --- | --- | --- | --- |
| `apps/read` app-server API | `726b6378d2` | 协议、生产 provider、缓存和 Rust-shaped fixtures 已实现 | `appserver`, `apps`, `chatgptapi` | 完成 |
| 统一 `[agents]` 多 Agent 配置 | `03bb3b1236` | 配置、角色解析、生产 spawn、reload/resume 和持久化 metadata 已实现 | `config`, `agent`, `appserver` | 完成 |
| 自动压缩前 fallback 阶段 | `768330dd6c`, `8aae858958` | fallback 配置、token budget phase、metadata 和 analytics 已实现 | `config`, `compact`, `turn`, `appserver` | 完成 |
| Amazon Bedrock 自定义 transport | `315195492c` | transport overrides、AWS profile/region、签名和 account response 语义已实现 | `model`, `config`, `appserver` | 完成 |
| raw response cache-write tokens | `1d941253e9`, `2edad72de3` | 已实现 | `model`, `appserver`, `telemetry` | 完成 |

### P1：Agent、工具和 Skill

| 上游更新 | 上游提交 | Go 当前判断 | Go 主要位置 | 状态 |
| --- | --- | --- | --- | --- |
| 不同 terminal session 并发 `write_stdin` | `f64233d142` | 已实现跨 session 并发、同 session 串行和交互期间防裁剪；Windows race 已通过 | `tool`, `execserver`, `appserver` | 完成 |
| MCP tool output 保留 encrypted content | `cbc83d961e` | 已保留 MCP wire item 并序列化为 Responses content items | `mcp`, `model`, `protocol` | 完成 |
| 子 Agent reload 时恢复角色 | `b7983c2a07` | reload/resume 从持久化 thread metadata 恢复 role/nickname 并重建 spawn graph | `agent`, `session`, `appserver` | 完成 |
| spawn role 后校验 reasoning effort | `8a7c854bff` | spawn 后应用并校验角色 reasoning effort | `agent`, `model`, `config` | 完成 |
| spawned agent 使用配置模型默认值 | `21c37fb374` | 未指定角色模型时使用配置默认模型 | `agent`, `model`, `turn` | 完成 |
| 仅在配置角色时暴露 Agent 类型 | `9ff47868eb` | 未配置角色时不暴露 Agent 类型 | `agent`, `turn` | 完成 |
| imported memory 保留 scope/provenance | `693b8c2ba4` | 项目 memory 检测、hash、scope.json、source/project/cwd metadata 和资源清理已实现 | `config`, `memories`, `session` | 完成 |
| Skill character n-gram 选择器 | `c983a53f20` | bounded 算法、shadow metrics 和 opt-in production selection 已接入 turn | `appserver`, `prompt` | 完成 |
| Skill fielded BM25 选择器 | `a47c661ea9` | field weighting、IDF 与上游 fixture 已实现并纳入选择链路 | `appserver`, `prompt` | 完成 |
| Skill multi-query lexical 选择器 | `0f44bca915` | weighted lexical、query views 与合并排序已实现并可通过配置启用 | `appserver`, `prompt` | 完成 |

### P2：会话、环境和 TUI

| 上游更新 | 上游提交 | Go 当前判断 | Go 主要位置 | 状态 |
| --- | --- | --- | --- | --- |
| app-server 分页 thread history | `da61f7d8e1` | start/read/resume/review/rollback fixture 已覆盖 | `appserver`, `session`, `rollout` | 完成 |
| 编辑旧 prompt 时分叉 conversation | `469ce0db51` | `thread/fork.beforeTurnId`、首条 prompt fresh thread 和失败恢复已覆盖 | `tui`, `appserver`, `session` | 完成 |
| retry/edit 保留 thread context | `d88db19144` | session context、in-flight turn/request、composer input 和 originator 已保留 | `turn`, `session`, `tui` | 完成 |
| interrupted prompt 保留在历史中 | `70a0b1eef8` | 中断 prompt 写入 conversation history，并由 Tea history fixture 覆盖 | `tui`, `session`, `rollout` | 完成 |
| safety-buffer retry 使用 forked thread | `9cddda7556` | safety-buffer retry 在 forked thread 上执行并覆盖 fork-point 校验 | `tui`, `appserver` | 完成 |
| active-turn environment 在设置更新时保持稳定 | `c4ce0493dc` | active turn 使用深拷贝 environment snapshot，后续 turn 使用新设置 | `appserver`, `turn`, `execserver` | 完成 |
| 所有 session 刷新 step world state | `71448a29e7` | 每个 turn 按捕获 CWD 重建 run config/AGENTS 指令并覆盖跨 CWD fixture | `appserver`, `turn`, `plugin` | 完成 |
| standalone extension 转发 thread originator | `78ba047bda` | thread originator 贯通 model request header 与 telemetry product client id | `appserver`, `model`, `telemetry` | 完成 |
| `environment/status` app-server/exec-server API | upstream v2 fixture | 四态 response、exec-server ready、unknown/disconnected/pending 语义和 schema surface 已实现 | `appserver`, `execserver` | 完成 |

### P3：性能、清理和非运行时事项

- 并发扫描 skill roots、加载 executor plugin declarations 和 workspace connectors。
- 避免重复发送 app list cache update notification。
- 插件安装时将 plugin commands 迁移为 skills。
- TUI approval payload 结构重构只同步线协议和行为，不复制 Rust 内部结构。
- 上游删除 `realtime-webrtc` crate 不要求 Go 删除现有 realtime 能力；仅确认不存在失效协议依赖。
- Rust SDK、Bazel、Nix、npm 发布结构不属于 Go runtime 必须同步范围，除非发布流程明确依赖。

## 3. 开发批次

### Batch 0：固定上游基线和差异检测

目标：后续同步可以重复执行，避免依赖人工比对。

- [x] 将 `parity` 的 Rust workspace、关键文件 hash 和 fixture manifest 更新到 `315195492c`。
- [x] 将默认上游路径切换为 `D:\qax\reagent\dev\git\codex\codex-rs`，同时保留 `CODEX_RUST_ROOT` 覆盖。
- [x] 修正 app-server method 漂移解析，使其能识别 Rust macro 中的显式 wire method。
- [x] 增加 feature key、ConfigToml 顶层字段和 tool discovery surface 漂移测试。
- [x] 已有 app-server schema fixture 测试继续作为完整 schema 漂移入口。
- [x] 输出独立的机器可读差异清单 `parity.json`，状态仅允许：`done`、`partial`、`missing`、`intentional_difference`。
- [x] 每个 intentional difference 必须记录原因、用户影响和测试，并由 manifest fixture 强制校验。

当前基线刷新结果：

- Rust workspace member：123 -> 124。
- 新增：`http-client`、`websocket-client`、`ext/agent`、`ext/items`。
- 移除：`execpolicy-legacy`、`external-agent-sessions`、`realtime-webrtc`。
- app-server v2 fixture 文件：84 -> 88。
- core suite fixture 文件：138 -> 146。
- TUI snapshot：539 -> 576。
- unified exec 新增 `write_stdin_calls_run_in_parallel_across_sessions`。
- 已确认 app-server method 缺口：无；`app/read` 与 `environment/status` 已同步并由 Rust surface/schema 测试覆盖。
- 已确认 feature key surface 已与 Rust 对齐；`imagegenext` 已降级为 `image_generation` legacy alias；`external_agent_memory_import` 的项目 memory detect/import、scope/provenance 和资源生命周期已同步。
- Rust `ConfigToml` 当前有 96 个顶层字段；已锁定 `agents`、自动压缩、provider 和 tools 等关键配置字段。
- Rust tool discovery 的 `tool_search`、`list_available_plugins_to_install`、`request_plugin_install` 均已由 Go registry 覆盖。

验收：

```powershell
go test ./parity ./features ./cli -count=1
```

### Batch 1：app-server 和 raw response 协议

目标：先同步会影响 IDE、SDK、TUI 和外部客户端的硬契约。

- [x] 新增 `apps/read` method。
- [x] 新增 `AppsReadParams`、`AppsReadResponse`、`AppToolSummary`、`ConnectorMetadata`。
- [x] 实现 ID 去重、请求顺序、partial missing、本地/static metadata 和 include-tools 分层缓存。
- [x] 接入 ChatGPT `/ps/apps/batch` 生产 metadata provider。
- [x] 对齐 Bearer、`ChatGPT-Account-ID`、`OAI-Product-SKU` headers 和默认 `codex` SKU。
- [x] 后端失败不覆盖已有成功缓存；已缓存 ID 后续仍可独立读取。
- [x] 解析 Responses `input_tokens_details.cache_write_tokens`。
- [x] 接入 `concurrent_reasoning_summaries` feature；OpenAI provider 且 reasoning summary 有效时发送 `stream_options.reasoning_summary_delivery = "sequential_cutoff"`，`summary = "none"` 或非 OpenAI provider 时省略。
- [x] 在 `rawResponse/completed` 和 thread token usage 中输出 `cacheWriteInputTokens`。
- [x] 贯通 exec JSON、累计 history usage、compaction metadata 和 telemetry。
- [x] 对齐 camelCase、nullable、空数组和错误码语义（由下方 schema validation 与 Rust fixture 覆盖）。
- [x] 对齐新增字段的 camelCase、nullable、required、空数组和错误码语义。
- [x] 将 `AppsReadParams`、`AppsReadResponse`、`RawResponseCompletedNotification` 纳入 Rust schema payload validation。
- [x] 迁移上游 `app_read.rs` 的去重、顺序、partial missing、缓存分层、100 ID 限制和生产请求关键场景。

验收：

```powershell
go test ./appserver ./apps ./chatgptapi ./model -count=1
```

完成标准：新增 Batch 1 协议已通过 Rust schema validation；全量 schema 树仍由 parity 测试持续监控。

### Batch 2：配置、Agent 角色与 Bedrock transport

目标：同步配置格式及其对 runtime 的实际影响。

- [x] 支持统一 `[agents]` 配置表和命名角色。
- [x] 对齐 `description`、`config_file`、`nickname_candidates` 等角色字段，并保留 nickname 声明顺序。
- [x] 支持旧键 `agents.max_threads`；与 canonical key 同时存在时按 Rust 语义静默采用 `max_concurrent_threads_per_session`。
- [x] `config_file` 必须校验存在、为普通文件，并使用 Rust 风格错误文本。
- [x] spawn 后应用角色模型、reasoning effort 和角色 metadata；app-server 使用真实持久化子线程控制器，记录 parent、role、nickname、模型和 spawn graph，并执行会话并发上限。
- [x] reload/resume sub-agent 时从持久化 thread metadata 恢复角色和 nickname，并重新登记 spawn graph。
- [x] 未配置角色时不暴露角色类型。
- [x] 支持 Amazon Bedrock transport overrides：`base_url`、`auth`、`http_headers`、`aws.profile`、`aws.region`，并保持区域端点、AWS 签名和模型 catalog 行为。
- [x] 对齐 app-server account response：使用 `usesCodexManagedCredentials`，外部 AWS chain/command auth 为 false，Codex 管理的 Bedrock API key 为 true。

验收：

```powershell
go test ./config ./agent ./model ./appserver -count=1
```

### Batch 3：token budget 与自动压缩 fallback

目标：在自动 context rollover 前提供一次有界的模型整理机会。

- [x] 增加 `auto_compact_fallback_prompt` 配置。
- [x] 增加 `auto_compact_fallback_buffer_tokens` 配置。
- [x] 校验 prompt 长度上限、buffer 为正数及成对配置规则。
- [x] 在 token budget 状态机中加入 fallback phase，并将 prompt 作为 developer message 注入同一 turn 的后续采样。
- [x] fallback 后重新计算上下文余量，再决定是否执行自动压缩或 rollover。
- [x] 保证 fallback 每个 context window 只执行一次，不形成递归或无限重试。
- [x] 在 thread metadata 中记录 fallback turn、follow-up 次数、上下文 token 和结果，并在后续 compaction analytics 中附加触发状态、结果与 buffer 大小。
- [x] 保持远程 compaction v2 和本地 compaction 的现有选择逻辑。

验收：

```powershell
go test ./config ./compact ./turn ./appserver -count=1
```

### Batch 4：Unified Exec、MCP 与并发

目标：同步工具执行时序和模型可见输出。

- [x] 检查 `write_stdin` 是否存在全局或 process-manager 级串行锁。
- [x] 允许不同 terminal session 并发写入。
- [x] 同一 session 内仍保证输入顺序、退出清理和事件顺序。
- [x] 增加跨 session 并行、同 session 串行和交互期间防裁剪 fixture。
- [x] 在安装 GCC 兼容 C 工具链后运行 race detector；Windows 使用 MSYS2 UCRT64 GCC 16.1.0，`CGO_ENABLED=1`、`CC=gcc`，`go test -race ./tool ./execserver ./mcp ./appserver -count=1` 已通过。
- [x] MCP tool output 完整保留 encrypted content，不降级为字符串或丢弃字段。
- [x] 对齐 MCP metadata 中已移除的 template ID。
- [x] 保持 terminal call ID、原始 exec call ID 和 analytics attribution 正确；并行 session 完成顺序反转时元数据仍互不串线。

验收：

```powershell
go test ./tool ./execserver ./mcp ./protocol ./appserver -count=1
go test -race ./tool ./execserver ./mcp ./appserver -count=1
```

本轮补验还通过了完整 race：`go test -race ./... -count=1`。

### Batch 5：动态 Skill 选择

目标：增强中英文、拼写变体和复合查询下的 skill 匹配质量，同时控制上下文和计算上限。

- [x] 实现 bounded character n-gram selector。
- [x] 实现 fielded BM25，名称权重大于描述。
- [x] 实现 multi-query lexical selector，每个子查询保留候选 leader。
- [x] 对候选数量、字段长度、query 数量和 n-gram 数量设置硬上限。
- [x] `skill_search` Rust feature 已接入并默认开启 shadow selection；记录差异但不改变 production catalog；保留 `[skills].shadow_selection_enabled` 作为兼容启用路径，按方法记录 run、duration、catalog、selected、query terms 和 reduction 指标。
- [x] shadow fixture 稳定后通过 `[skills].selection_enabled` 切换为可选 multi-query lexical selector；默认关闭、空 query 保持完整 catalog、explicit-only skill 不被裁剪。
- [x] 覆盖 CJK、拼写错误、稀有词、字段权重、无匹配候选、多子句和 bounded input 场景。

验收：

```powershell
go test ./appserver ./prompt ./features -count=1
```

### Batch 6：会话历史、环境和 TUI

目标：确保编辑、重试、恢复和多 Agent 会话不会丢失或污染历史。

- [x] 用上游 fixture 验证 paginated thread history 的 start/read/resume/review/rollback，包括稳定 turns/items head cursors、detached review 拒绝和 rollback 拒绝。
- [x] 编辑较早 prompt 时使用 `thread/fork.beforeTurnId` 创建新 thread；编辑第一条 prompt 时创建 fresh thread，原 thread 保持不变。
- [x] fork 失败时恢复原 thread session、turns、选择和完整 composer snapshot，并输出分支失败信息。
- [x] retry/edit 保留 thread session context、in-flight turn/request、composer input 和 originator；prompt edit fork 继承 session metadata。
- [x] interrupted prompt 写入 conversation history；Tea submitted history fixture 覆盖中断后保留。
- [x] safety-buffer retry 在 forked thread 上执行；中断、读取、fork-point 校验、`beforeTurnId` 分叉及 fork thread 提交均有 Rust-shaped fixture。
- [x] history summary 使用 final answer，遵守 final-answer boundary；commentary 不作为 summary，终态旧记录允许无 phase fallback。
- [x] active turn 启动后，设置更新不得替换其 environment；active turn 保存深拷贝 snapshot，后续 turn 使用更新后的线程设置。
- [x] world-state refresh 应覆盖全部 session，但不得破坏 active turn 稳定性；Go runtime 每个 turn 均按捕获 CWD 重建 run config/AGENTS 指令，不受 `deferred_executor` 开关限制，并由跨 CWD turn fixture 覆盖。
- [x] 新增 `environment/status` app-server v2 method 和 exec-server RPC，覆盖 `ready`、`pending`、`disconnected`、`unknown` response 语义，并移除 Rust protocol surface 的缺失豁免。

验收：

```powershell
go test ./session ./rollout ./appserver ./tui/... -count=1
go test ./appserver ./execserver -run "EnvironmentStatus|EnvironmentInfo|RustProtocolMethodSurface|ProtocolPayloads" -count=1
```

### Batch 7：跨平台发布门禁

目标：把过去的“环境验证缺口”转为正式发布门禁。

- [x] Windows：完整测试、race、elevated sandbox、网络代理和 PTY。
- [x] Linux：原生 Landlock、bwrap、permission profile、managed network。
  - 已在 Kali Linux 6.18.12 上验证：`/usr/bin/bwrap` 可用，`codex-linux-sandbox` helper 与 `execserver` sandbox 路径通过。
  - `appserver` 侧 Rust schema fixture 对照仍缺少 `AppsReadParams`、`AppsReadResponse`、`EnvironmentStatusParams`、`EnvironmentStatusResponse`、`RawResponseCompletedNotification` 这些单文件 fixture；这属于上游基线/fixture 缺口，不是 Linux 沙箱本身失效。
- [ ] macOS：原生 seatbelt、Unix socket、native trust roots；Go 已生成参数化 SBPL、保护工作区元数据并限制网络，Darwin packages 交叉编译通过，仍需原生 smoke report。
- [ ] 三个平台分别验证 CLI help、sandbox exposure 和 unsupported 错误。
- [ ] 对无法跨平台执行的 fixture 使用明确的 build tag 和理由，不允许静默跳过。

全局验收：

```powershell
go list -buildvcs=false ./...
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
```

当前 Windows 本机验证（2026-07-20，Rust 基线 `1e20272fa5`）：

- `go list -buildvcs=false ./...` 通过。
- `go test ./... -count=1` 通过。
- `go test -race ./... -count=1` 通过。
- `go vet ./...` 通过。
- `sandbox/windowssandbox/elevated`、`network`、`appserver` PTY 相关测试均包含在上述完整测试与 race 覆盖中。

## 4. 推荐执行顺序与里程碑

| 里程碑 | 包含批次 | 预计工作量 | 可交付结果 |
| --- | --- | ---: | --- |
| M0 基线可追踪 | Batch 0 | 0.5-1 天 | 自动发现上游漂移 |
| M1 协议兼容 | Batch 1 | 2-3 天 | app-server 客户端可安全升级 |
| M2 核心配置兼容 | Batch 2-3 | 5-7 天 | Agent、Bedrock、compaction 行为同步 |
| M3 工具与 Skill 同步 | Batch 4-5 | 4-6 天 | 并发工具和新 Skill selector 可用 |
| M4 会话体验同步 | Batch 6 | 3-5 天 | TUI 编辑、恢复、分页历史对齐 |
| M5 跨平台发布 | Batch 7 | 依赖平台资源 | 达到发布门禁 |

建议每个 Batch 独立提交，单个非机械提交控制在约 500 行核心逻辑以内；大型协议生成物可单独提交。

## 5. 发布策略

候选版本追踪名：`go-1e20272fa5-parity.1`，不得沿用上一轮 `go-v0.145.0-alpha.20-parity.1`。

发布必须满足：

- [x] 本轮 P0（Batch 8-10）全部完成。
- [x] P1 不存在已知协议、持久化或安全语义缺口；realtime V3 Codex handoff 已完成 appserver 流式路由和有界输出验证。
- [x] app-server v2 schema 在 `1e20272fa5` 基线上无未解释差异。
- [x] 默认生产路径不使用 synthetic/local stub。
- [x] 上游新增 fixture 已迁移，或已有书面的平台/语言差异说明。
- [ ] Windows、Linux、macOS 平台报告完整（Linux 已验证，macOS 待原生 runner）。
- [x] 全局 Go 验收命令在新基线上全部通过；原生平台发布门禁单独跟踪，不以 Windows 结果替代。
- [x] README 中 Go 版本要求与 `go.mod` 一致。

当前结论：不能宣称 Rust `1e20272fa5` 的 Batch 8-14 已全面完成；Windows 代码与测试验证已完成，但上列 partial 行为和原生平台验证仍阻止发布门禁关闭。

## 6. 风险与约束

- 上游在高频开发，计划执行期间必须固定 commit；发现新 HEAD 时先生成差异，不在当前 Batch 中无界追加需求。
- app-server、raw response、config 和 rollout 是外部兼容面，字段增删必须优先处理。
- TUI 使用 Bubble Tea，不能机械复制 ratatui 内部结构；以最终渲染和交互状态为验收依据。
- Skill selector 首先使用 shadow 模式，避免排序变化直接影响生产任务选择。
- 自动压缩 fallback 必须有硬 token 上限和单次执行保护。
- 并发 `write_stdin` 必须通过 race test，不能以牺牲 session 内事件顺序为代价。
- 平台沙箱只能在目标系统原生验收；交叉编译通过不等于安全语义通过。

## 7. 维护规则

- 每次同步前记录上游 commit、tag、提交时间和本地工作区状态。
- 每完成一个条目，同时更新状态、测试名称、验收命令和剩余风险。
- 不再使用未经 fixture 支撑的“99.x% 对齐”表述。
- 新上游提交按以下顺序分类：协议破坏、配置/持久化、运行时行为、UI、性能/重构。
- 发现 breaking change 时立即提升为 P0，并暂停依赖旧协议的新功能开发。
- 不覆盖或回滚用户的无关工作区修改。

## 8. 下一步

1. 先执行 Batch 8，只刷新基线证据和差异清单，不在同一提交中实现业务功能。
2. 按 Batch 9 优先同步 app-server schema、`app/installed`、`thread/searchOccurrences` 和音频 content item，解除客户端协议阻塞。
3. Batch 10-12 完成持久化、world state、exec policy 与远程执行器语义后，再进入 TUI 大规模同步。
4. 旧发布门禁继续保留：macOS seatbelt/native trust roots 仍需原生报告；Linux/macOS 的最终发布报告不能由 Windows 测试替代。
