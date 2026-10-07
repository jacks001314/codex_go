# syncl4 · r87c 域扫描 #2：`state/` + `telemetry/` + `rollout/` + `mcp/`（只读）

- 生成：2026-10-07（Asia/Shanghai）· 节点：**Linux** · 车道：**syncl4**
- 纪律：**0 补丁 / 0 commit / 0 push / 0 tag / 0 ref 移动**。本报告只用 `git ls-remote` / `git log` / `git show` / `git grep`（含 `git grep <rev>` 直读 tree-ish，**未 checkout、未改工作树**）。
- 结论一句话：严格窗口内 **真缺口候选 2 条**（`#47565` 建议派单；`#42370` 诊断型、弱 RC，建议队长定夺）+ **1 条台账口径翻案线索**（`#39935`，commits.json 记 N/A，但 Go 侧该机制缺失，见 §5）。其余 47 条 ≤5 文件条目全部 **已落地 / 已等价 / 无载体 / 纯测试**。

---

## 0. 固定点与环境自检（原文）

```
$ git -C /home/jacks/jacks_dev/codex_go ls-remote origin main
ee2d4e1db819e1d706eaea8b5da0386e85ea1a42	refs/heads/main

$ git -C /home/jacks/jacks_dev/codex ls-remote origin main
b17c74cfd5ebb39fe70ffaff78de198120278636	refs/heads/main

$ git -C /home/jacks/jacks_dev/codex_go rev-parse HEAD
a32e1c35facdbfec20887cce46da126d6ac0e68b
$ git -C /home/jacks/jacks_dev/codex_go status --porcelain
?? scripts/loc_report.sh
$ git -C /home/jacks/jacks_dev/codex_go rev-parse --abbrev-ref HEAD
main

$ git -C /home/jacks/jacks_dev/codex rev-parse HEAD
5b0b2530354052b9194156d70d4c94a439368342
```

说明（透明口径）：
1. **Go 主仓工作树 checkout 落后**（`a32e1c35`），但远端 `origin/main = ee2d4e1d` 的对象**本地已有**（`git cat-file -t ee2d4e1d… → commit`）。为不改工作树/不移动 ref，本报告**全部载体核查用 `git grep <ee2d4e1d>` 直读 tree-ish**，因此 file:line 就是 **最新 main `ee2d4e1d` 的坐标**。
2. 四域中 `mcp/` 在 `a32e1c35..ee2d4e1d` **零改动**（`git diff --stat a32e1c35 ee2d4e1d -- mcp/` 为空）；`state/ telemetry/ rollout/` 有变动（见 §1），故直读 tree-ish 是必要的，不是多余。
3. **Rust 工作树 HEAD = `5b0b253035`**，而 pin = `b17c74cfd5`（更新）。Rust 侧 pin 的对象本地存在，全部 diff 用 `git show <sha>` / `git log <pin>` 直读，**未改 rust 工作树**。
4. 上一轮（round87）域扫描已覆盖 `network/proxy + sandboxing`，本轮为**从未系统扫过的**四域。

---

## 1. 方法与窗口

Rust 目录映射（Rust 无 `codex-rs/telemetry`，对应面是 `otel` + `analytics`）：

| Go 域 | Rust 目录 |
|---|---|
| `state/` | `codex-rs/state`, `codex-rs/thread-store` |
| `telemetry/` | `codex-rs/otel`, `codex-rs/analytics` |
| `rollout/` | `codex-rs/rollout`, `codex-rs/rollout-trace` |
| `mcp/` | `codex-rs/codex-mcp`, `codex-rs/rmcp-client` |

窗口：以 pin `b17c74cfd5` 为顶，取上述 Rust 目录的最近 **200 笔提交**：

```
$ git -C /home/jacks/jacks_dev/codex log --no-decorate --format='%H|%s' -n 200 b17c74cfd5 \
    -- codex-rs/state codex-rs/otel codex-rs/analytics codex-rs/rollout codex-rs/rollout-trace \
       codex-rs/codex-mcp codex-rs/rmcp-client
→ 200 行
```

从中保留 **改动文件数 ≤5** 的条目 = **52 条**（已剔除队长列出的 11 条已定案项）。逐条：
1. `sha` 由 `git log --grep '#<PR>' -F` **自解析**（不采信台账 sha 列）；
2. `git show --stat <sha>` 取改动文件数；
3. 对 Rust 新增标识符/字符串做 **Go 侧零命中命令**；
4. 命中则取 **Go 载体 file:line**。

Go 工作树 `a32e1c35..ee2d4e1d` 变动面（供局部性判断）：

```
$ git -C /home/jacks/jacks_dev/codex_go diff --name-only a32e1c35 ee2d4e1d -- state/ telemetry/ rollout/ mcp/
rollout/persistence_metrics.go            rollout/persistence_metrics_test.go
rollout/rollout.go                        rollout/session_index.go
rollout/session_index_blank_lines_test.go rollout/session_index_test.go
state/backfill.go                         state/backfill_test.go
state/guardian.go                         state/guardian_retained_context.go
state/guardian_retained_context_test.go   state/guardian_sender_messages.go
state/guardian_sender_messages_test.go    telemetry/metric_buckets.go
telemetry/metric_buckets_test.go
```

---

## 2. 主表（52 条 ≤5 文件，按文件数升序）

五分类：已落地型 / 已等价型 / 无载体型 / 纯测试型 / 已取代型。`载体` 列给出 **最新 main `ee2d4e1d` 的 file:line** 或 **零命中命令**。

| PR | Rust sha | 文件 | 语义 | 分类 | Go 载体 / 零命中证据 |
|---|---|---|---|---|---|
| #50129 | `7d3e696c4c` | 1 | 远程 MCP 服务器保留 Windows 环境变量 | 已落地 | `mcp/stdio_env.go:47` `"SYSTEMROOT"`（probe hitstrings=2） |
| #49959 | `d6c3b448a4` | 1 | session index 线程名 append/remove 测试 | 纯测试 | Go `rollout/session_index_test.go` 在库 |
| #48724 | `274d41a398` | 1 | 修 Linux ETXTBSY 竞态（MCP stdio 测试） | 纯测试 | Rust 改 `rmcp-client/tests/*`，Go 无对位测试面 |
| #44226 | `a2e83a783e` | 1 | 压缩 rollout 搜索失败时继续搜其余文件 | 无载体 | Go 无 rollout 内容搜索/snippet 模块（`git grep -l -i ripgrep` → 仅 `doctor/ install/ sandbox/ shell/ tool/`，无 `rollout/`） |
| #42767 | `88f87d907a` | 1 | streamable HTTP 测试避免端口竞态 | 纯测试 | Rust 改 `tests/`，Go 无对位 |
| #42603 | `7eee24ef51` | 1 | `codex-otel` 暴露全局 metrics 安装 | 已落地 | `telemetry/global_metrics.go:9` + `telemetry/global_metrics_test.go:11`（`InstallGlobalMetrics`） |
| **#42370** | `76f47103fe` | 1 | **MCP 启动错误日志改进** | **无载体（诊断型）→ 见 §3** | 零命中：见 §3 |
| #50189 | `a20fe6335f` | 2 | figma OAuth 例外换成 mercadopago | 无载体 | 零命中：见 §5（Go 无 provider-exception 表） |
| #49708 | `4f699cd642` | 2 | session index I/O 移出 async runtime 线程 | 无载体 | Rust `spawn_blocking` 重构；Go 无 async-runtime 对应（`git grep -l "spawn_blocking\|JoinHandle" ee2d4e1d` → 仅 `appserver/runtime_router.go` 注释 + `update/*.md`） |
| #49414 | `16a7c0f0eb` | 2 | SQLite 日志过滤 graceful-shutdown guard 事件 | 已落地 | `state/log_handler.go:186` |
| #49297 | `d5e6526362` | 2 | session index 反向扫描做批量线程名查询 | 已落地 | `rollout/session_index.go:110` |
| #47757 | `b0a9dcb843` | 2 | 工具遥测 product SKU 改白名单 | 已等价 | Rust 为**行为保持**的 if→allowlist 重构；Go `telemetry/binding_catalog.go:31` `ProductSKU` 已 bounded |
| **#47565** | `d6c4c6aea4` | 2 | **rollout 读失败按 reason/progress 分类** | **真缺口 → 见 §3** | 零命中：见 §3 |
| #47303 | `44b857c00e` | 2 | SQLite 清理遗留 Guardian 线程元数据 | 无载体（待队长确认） | Go `state/migrations/state/` 无 guardian 元数据清理迁移；Go 不写 `source='{"subagent":{"other":"guardian"}}'` 形状（`git grep -n -i '"guardian"' state/*.go appserver/*.go` → 仅 `appserver/guardian_reviewer.go` 的 `Originator/SubagentHeader`，无 state 表写入） |
| #47094 | `afe4249cfe` | 2 | Unix 本地 MCP 服务器限 stdio 描述符 | 已等价 | `mcp/stdio_process_unix.go:16` |
| #47026 | `23a6b705ba` | 2 | rollout 压缩元数据失败诊断 | 已落地 | `rollout/compression.go:133-138`（`completion_reason`/`read_metadata`/`trigger`） |
| #45964 | `2d90e054d6` | 2 | rollout 压缩指标暴露部分完成 | 已落地 | `rollout/compression.go:376` 注释 + `scan_finished`/`time_budget` 标签在库 |
| #45461 | `3fa9039bd7` | 2 | rollout 压缩失败按 stage/IO kind 打标 | 已落地 | `rollout/compression_metrics.go:96` `rolloutCompressionErrorKind` |
| #44938 | `2e572378f4` | 2 | 连接器鉴权失败检测（无 install URL） | 已落地 | `mcp/auth_elicitation.go:87` |
| #43927 | `9d88e9ae08` | 2 | state DB `thread_artifacts`→`thread_attachments` 改名 | 无载体 | `git grep -n -i "artifact\|attachment" ee2d4e1d -- state/migrations/**` → **0 命中**（Go state DB 无该表） |
| #42752 | `1cd78651e2` | 2 | fast collaborator 工具事件保留 response id | 已等价 | `rollout/rollout.go:1866-1867`（`item.ResponseID` 回填） |
| #51220 | `28b91c7c31` | 3 | 尊重 OTLP metrics temporality 偏好 | 已落地 | `telemetry/metrics_client.go:196-199`（`OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE`，注释即 `Rust #51220`） |
| #50035 | `cc31e374fc` | 3 | legacy 协议模式跟随 MCP 工具分页 | 已等价 | `mcp/pagination.go:66` 已拒重复 cursor、`:29` 限页/限项；分页与协议模式无关 |
| #49489 | `bcd6d9ab6b` | 3 | analytics 批次间账号切换回归覆盖 | 纯测试 | Rust 删 2 行 import + 加 `analytics/client_tests.rs` 测试 |
| #49102 | `c2d2f422e6` | 3 | 保留 SQLite vacuum 模式并暴露池初始化错误 | 已落地 | `state/sqlite.go:195,208,239`（marker `#49102`）+ `state/sqlite_settings_test.go:13`（引用 `c2d2f422e6 / #49102`） |
| #44636 | `8e2afc0912` | 3 | 503 时经 OIDC 恢复 OAuth 元数据发现 | 已落地 | `mcp/oauth_discovery.go:240,311-345`（OIDC fallback） |
| #44616 | `86661eb626` | 3 | 简化企业 OAuth 登录 helper + 扩测试 | 已等价 | `mcp/enterprise_oauth_flow.go:213-241`（`EnterpriseOAuthLoginOptions/Handle`） |
| #44359 | `d996b4f02a` | 3 | MCP 状态快照上报 OAuth 鉴权失败 | 已落地 | `mcp/reauth_status_test.go:61`（marker） |
| #44330 | `130d6e4fba` | 3 | state runtime 分页列线程附件 | 已落地 | `appserver/thread_attachments.go:9` |
| #45966 | `da18000cae` | 3 | 度量 rollout 读/物化耗时 | 已落地 | `rollout/persistence_metrics.go` 在库（`MaterializeDuration`） |
| #43494 | `d0a8dcd157` | 3 | 归档 rollout 读取限定请求线程 | 已等价 | `rollout/migrate.go:116,137` `matchesMigrationSelection(options.ThreadIDs, …)` |
| #49425 | `8ea2c0e0d4` | 4 | 按年龄/DB 大小周期清理诊断日志 | 已落地 | `state/logs_maintenance.go` + `state/logs_maintenance_test.go:11`（`LOG_DATABASE_BUDGET_BYTES`/`LOG_RETENTION_SECONDS`） |
| #49305 | `c2837d8ece` | 4 | 解析线程名时批量读元数据 | 已等价 | `rollout/session_index.go:107` `FindThreadNamesByIDs`（批量入参 `map[string]struct{}`） |
| #48238 | `4b9e0cc77f` | 4 | 本地 Windows MCP 服务器抑制控制台窗口 | 已落地 | `envutil/console_window_windows.go:12`（`CREATE_SUSPENDED`/`GetConsoleWindow`） |
| #47873 | `4021746a15` | 4 | 截断前后度量延迟工具命名空间片段 | 已落地 | Go 已有指标名 `codex.thread.tools.namespaces_total`、`codex.thread.tools.fragment_bytes` |
| #47326 | `2c2a42e65d` | 4 | MCP OAuth 授权端点限 HTTP(S) | 已落地（部分） | `mcp/oauth_discovery.go:472-486`（`mcpOAuthWebEndpoint`）——**只落 scheme 校验**，origin 绑定未落，见 §5 |
| #47038 | `87bc50f9d4` | 4 | 缓存 OS 发现（user agent / telemetry） | 无载体 | Go 用 `runtime.GOOS` 常量（`telemetry/turn_event.go:50`），无探测开销可缓存 |
| #45535 | `12b0164a48` | 4 | 工具 analytics 事件按调用来源分类 | 已落地 | `telemetry/turn_event_test.go:166` |
| #44586 | `818f1cca8c` | 4 | 从 skill 调用 analytics 移除 `repo_url` | 已等价 | `git grep -rn "repo_url" --include='*.go' ee2d4e1d` → **0 命中**（Go 已无该字段） |
| #44548 | `94697375cb` | 4 | Codex Apps 增加 MIME 过滤资源列举 | 已等价 | `mcp/pagination.go:13` `maxCodexAppsCatalogItems` + `mcp/pagination_test.go:100-130`（`codex_apps` 专用上限） |
| #44352 | `0447e4a1fd` | 4 | 从 Guardian 评审 analytics 移除带路径字段 | 已等价 | `telemetry/guardian_v2_event.go` 字段表**无 `path` 字段**（仅 `thread_id/turn_id/item_id/model/…`） |
| #44238 | `3436cad5ab` | 4 | 修 MCP elicitation 取消 + 重连重置状态 | 已落地 | `mcp/elicitation_cancellation_test.go:94` |
| #43947 | `5ac0b8768d` | 4 | OAuth 令牌过期无法刷新时上报 MCP 重连信号 | 已落地 | `mcp/oauth_refresh_adoption_test.go:51` |
| #43870 | `44ab72674e` | 4 | 客户端销毁时关闭 MCP stderr reader | 已落地 | `mcp/stdio_stderr_cleanup_test.go:12` |
| #42384 | `312709252d` | 4 | 加 RMCP OAuth 凭据存储适配器 | 已等价 | `mcp/oauth_dependency.go` + `mcp/oauth_credentials_store.go` |
| #51499 | `a9bc7bebfa` | 5 | 单 blocking worker 载入 rollout 历史 | 已落地 | `rollout/compressed_test.go:93` 直接引用 Rust 测试名 `full_history_load_preserves_records_and_errors_across_representations` |
| #49694 | `5aa92804d2` | 5 | 可取消 blocking worker 批量 rollout 列举扫描 | 无载体 | Rust 新增 `list_files.rs` + tokio blocking worker；Go 无 `spawn_blocking/JoinHandle`（见 #9 命令） |
| #49692 | `d1c4e3c3e6` | 5 | 压缩 rollout snippet 搜索固定单 worker | 无载体 | Rust 新增 `compression/blocking_reader.rs`；`git grep -c -E "blocking_reader\|blockingReader" ee2d4e1d` → **0 命中** |
| #47981 | `75e0e0aad9` | 5 | 直接按广告的工具身份准备调用 | 已落地 | `parity/rust_fixture_manifest_test.go:190`（`PrepareCall`） |
| #43621 | `4b0f44d304` | 5 | 线程 telemetry 增加 worktree 分类 | 已落地 | `telemetry/thread_initialized_event.go:27` |
| #42552 | `0650d6d1ca` | 5 | 工具调用保留 MCP 鉴权挑战 | 已落地 | `mcp/tool_executor.go:529` |
| #49444 | `8c3612fb63` | 3 | 反向 JSONL 扫描用 `memrchr` 找换行 | 已判 N/A（前轮） | 纯性能/实现细节；台账前轮已判 N/A（本轮复核仍 N/A） |

> 计数：窗口内 **52 条 ≤5 文件条目全部给出判定**（上表整 52 行）。

---

## 3. 真缺口候选（S/M，≤5 文件，报队长批）

### 候选 A（建议派单）· `#47565` `d6c4c6aea4` —— rollout 读失败按 reason / read_progress 分类

- Rust 规模：**2 文件** `rollout/src/compression.rs`(+21/-?) + `rollout/src/compression/read_metrics.rs`(+80)。
- Rust 行为：新增 `ReadFailureSource{Stream,ReaderBusy,TaskJoin}` 与 `reason`（`reader_busy`/`task_join`/`os_error`/`zstd_invalid_frame`/`zstd_corrupt_block`/`zstd_checksum`/`zstd_resource_limit`/`zstd_unsupported_frame`/`zstd_dictionary`/`stream_error`）+ `read_progress`（首行前/后）；并区分 open 期与 read 期的 blocking-task join 失败。

**Go 侧零命中（原文）**

```
$ git grep -c -E "reader_busy|task_join|read_progress|ReadFailureSource|read_any_line|zstd_invalid_frame|zstd_corrupt_block" ee2d4e1d -- ":(glob)rollout/**/*.go"
exit=1                      # 0 命中
```

**Go 现状载体（原文）** —— 只到 `#45461` 的 stage+error_kind 形态：

```
$ git grep -n "rolloutReadFailure|\"stage\"|error_kind" ee2d4e1d -- rollout/line_reader.go
ee2d4e1d:rollout/line_reader.go:39:type rolloutReadFailure struct {
ee2d4e1d:rollout/line_reader.go:88:		failure:  &rolloutReadFailure{stage: "open", kind: rolloutCompressionErrorKind(err)},
ee2d4e1d:rollout/line_reader.go:157:	r.failure = &rolloutReadFailure{stage: stage, kind: rolloutCompressionErrorKind(err)}
ee2d4e1d:rollout/line_reader.go:203:		"stage":      stage,
ee2d4e1d:rollout/line_reader.go:204:		"error_kind": errorKind,
```

- 预计 Go 落点：`rollout/line_reader.go`（`rolloutReadFailure` 加 `reason`；`ReadLine` 记 `readAnyLine`；`recordReadMetric` 加两标签）+ 可能 `rollout/compression_metrics.go`（新增 zstd reason 分类器）+ 1 测试 ⇒ **≤3 文件**。
- 避让面：**不撞**（`appserver/runtime_router.go` / `appserver/turn_runtime.go` / `tui/state.go` / `sandbox/windowssandbox/` 均不涉及）。
- RC 可行性：**可值级** —— 构造 `.jsonl.zst` 截断/损坏文件 → `OpenRolloutLineReader/ReadLine` → 断言 metric tags 的 `reason`/`read_progress`。
- ⚠️ 口径警告：Rust 的 `reader_busy`/`task_join` 是 tokio 专属；Go 同步 reader 无此二态，移植时需**明写「Go 无 reader_busy/task_join，等价面为 os_error + zstd_* + stream_error + read_progress」**，且 Go `klauspost/compress/zstd` 的错误文案与 Rust `zstd` crate 不同 ⇒ zstd 子类映射需按 Go 实际文案重定，不要照抄 Rust 字符串。

### 候选 B（诊断型、弱 RC，建议队长定夺）· `#42370` `76f47103fe` —— MCP 启动错误日志

- Rust 规模：**1 文件** `codex-rs/codex-mcp/src/rmcp_client.rs`（+6/-2）。
- Rust 行为：(1) 每次启动尝试失败时 `warn!(server_name, %error, "MCP server startup failed")`；(2) `StartupOutcomeError::from` 用 `format!("{error:#}")` 保留完整 error chain。

**Go 侧零命中（原文）**

```
$ git grep -n -- "MCP server startup failed" ee2d4e1d -- ":(glob)**/*.go"
exit=1                      # 0 命中（全仓）
```

**Go 现状载体（原文）**：启动失败只经**回调**上报，无日志面：

```
$ git grep -n "MCPStartupObserver" ee2d4e1d -- mcp/api.go
ee2d4e1d:mcp/api.go:53:// MCPStartupObserver reports one server's startup transition. failureReason
ee2d4e1d:mcp/api.go:56:type MCPStartupObserver func(name string, status MCPServerStartupState, failureReason *string, err error)
ee2d4e1d:mcp/api.go:1275:func (s *MCPService) ListStatusCheckedWithObserver(params *MCPListServerStatusParams, observer MCPStartupObserver) ...
ee2d4e1d:mcp/api.go:1387:func (s *MCPService) populateStatusInventories(params ..., observer MCPStartupObserver, ...) ...
```

- 判定：**无载体（或「已等价」——失败经 observer 已可见）**，属**纯诊断增强**。
- 为什么建议队长定夺：这条**没有机器可判的断言**（日志文本），RC 只能做「撤 warn ⇒ 日志断言 FAIL」，价值密度低（与队长此前 `128/32 字面值` 的口径同档）。若派单，落点 = `mcp/api.go`/`mcp/stdio_client.go` + 1 测试 ⇒ ≤3 文件，不撞避让面。

---

## 4. 排除项附录

### 4.1 L（>5 文件，未列入主表）
本轮窗口内 L 项均来自**背景台账已收录**，无新增；严格窗口对四域**未产出** L 级真缺口（Rust 四域在 `b17c74cfd5` 附近的改动多为 1–5 文件的小 PR）。

### 4.2 已定案 / 已排除（本轮按要求剔除，未重复劳动）
`#51651`、`#49032`、`#51140`、`#51133`、`#50510`、`#50564`、`#50454`、`#49778`、`#49269`、`#49280`、`#49467`。

### 4.3 「已判 N/A」但本轮复核成立的
- `#47326`：**只落了 scheme 校验**（`mcp/oauth_discovery.go:476`），Rust 同 PR 的 origin 绑定属另一 PR（`#39935`），Go 未落 —— 见 §5。
- `#49444`：前轮已判 N/A（纯性能实现细节），本轮复核**维持 N/A**。

### 4.4 21 条带 marker 命中的已落地项（也计入主表分类）
`#51499 #51220 #49425 #49414 #49297 #49102 #48238 #47981 #47326 #45535 #44938 #44636 #44359 #44330 #44238 #43947 #43870 #43621 #42552`（marker/测试文件命中），另有 `#47094 #50129 #44548 #42603 #45461 #45966 #45964 #47026 #44586 #47757` 经**符号/字符串正向命中**确认为已等价/已落地。

---

## 5. 未决问题 / 翻案线索（请队长裁定）

### 5.1 `#39935`（`7f9832d0d0`，2026-08-21，"Enforce issuer binding for MCP OAuth endpoints"）—— 台账口径疑点

- `parity/commits.json:114-121` 把它记为 **`"N/A (verified)"`**。
- 但本轮复核：**Go 侧该机制缺失**。

```
$ git grep -c -E "validate_authorization_server_endpoints|mercadopago|robinhood|mcp\.mercadopago" ee2d4e1d -- ":(glob)mcp/**/*.go"
exit=1                      # 0 命中

$ git grep -n -i "does not match the authorization|origin does not match|without issuer-bound" ee2d4e1d -- ":(glob)**/*.go"
ee2d4e1d:mcp/oauth_login.go:313:  return errors.New("MCP OAuth callback issuer does not match the authorization server metadata")
          ↑ 这是 RFC 9207 callback `iss` 校验（#40691），**不是** Rust #39935 的端点 origin 绑定
```

- Rust `issuer_binding.rs:11 validate_authorization_server_endpoints`：当授权服务器**未声明** issuer-bound callback 支持时，要求 `authorization_endpoint.origin() == issuer.origin()`（或 `== token_endpoint.origin()`），否则 `bail!`；两条窄例外（mercadopago / robinhood）。
- Go 现状：只有 `#47326` 的 scheme 白名单（`mcpOAuthWebEndpoint`）+ `#40691` 的 callback-mode/`iss` 校验；**没有 issuer↔authorization-endpoint 的 origin 一致性校验，也没有例外表**（故 `#50189` 的 mercadopago 例外改动在 Go **无载体**）。
- 影响：可能是**安全相关真缺口**（OAuth mix-up 防线缺一环），也可能是**有意差异**（Go 用 per-server callback ID 的 `CallbackSpecific` 模式替代）。
- **本车道的处置建议**：不出补丁、不改 commits.json；请队长裁定 **(a)** 维持 N/A（记录「Go 以 callback-specific 模式替代」的口径）或 **(b)** 重新立项（落点 `mcp/oauth_discovery.go` + 测试，≤3 文件，不撞避让面，可值级 RC：构造 issuer≠auth-endpoint-origin 的元数据 ⇒ 断言 discovery 被拒）。

### 5.2 待队长确认的「无载体」判定
- `#47303`（Guardian SQLite 元数据清理）：Go 侧无对应表形状/迁移，判无载体。若队长知道 Go 另存该数据，请回派。
- `#47038`（缓存 OS 发现）：Go 用 `runtime.GOOS` 常量，判无载体。
- `#43927`（`thread_artifacts` 改名）：Go state DB 无该表，判无载体。

### 5.3 本轮未做的事（边界声明）
- 未 checkout `ee2d4e1d` 到工作树（守 ref 纪律），故**未跑任何 `go build`/`go test`/`parity`** —— 本轮是**只读候选扫描**，不含门禁。
- 未验证 darwin/Windows-only 面（本节点 Linux）。
- 未 re-vendor 任何 `.zst`；未改 Rust 工作树。

---

## 6. 交付自检

- 补丁：**无**（0 文件）。
- commit / push / tag / ref 移动：**无**。
- worktree：本轮**未创建**任何 worktree（全部只读 grep），无需清理。
- 探针文件：未落仓（未产生）。
- 报告路径：`update/syncl4_domain_scan2_ee2d4e1d_2026_10_07.md`（etag 见 A2A 回执）。
