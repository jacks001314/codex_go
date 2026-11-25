# Go 版 Codex 与 Rust 版对齐缺口分析（2026-11-25）

## 结论

本轮开始时 Go 仓库 HEAD 为 `e524c8d69`，Rust 目标为 `3f65e9f1`。按 `parity/commits.json` 的 4,595 个上游提交计算，分析时已关闭 4,563 个（3,667 complete、51 equivalent、845 not applicable），剩余 12 个 partial、20 个 blocked_precondition；本轮又关闭了 #40625、#60905、#63528、#63520 四项并按证据重算，当前为 3,671 complete / 51 equivalent / 845 not applicable / 11 partial / 17 blocked_precondition（本轮改动见各提交与末节记录）。另有 4 个 domain、4 个 contract 未关闭，`certificationReady` 仍为 false。

真正影响功能的缺口已经不多，但比较集中：

1. Guardian v3 缺少生产级的 per-agent reviewer runtime（ActorReviewer），现有 housekeeper、路由 worker、evidence carrier 已落地，但还没有把保留的 reviewer 会话接到真实线程生命周期上。
2. MCP 的“同名服务器跨 environment”只修到了连接和工具调用层，catalog 与 tool namespace 仍按 name 归并。
3. Code Mode 生产 host 只实现 `Transport` 帧通道，`OpenSession` / `SubscribeToToolCalls` 的 typed stream 与 heartbeat 仍缺。
4. 侧栏图片预览、rollout trace 采集器、realtime handoff 的持久化准入屏障、异步 host service-tier、subagent item-boundary interrupt 仍未完成。
5. 平台侧仍缺 Windows MXC、Linux bwrap launcher 的原生实现；macOS Secure Enclave 代码已存在，但缺少真机验证。
6. 台账本身有明显过期和矛盾，导致一部分已经完成的提交仍被算作 blocker，也掩盖了一部分真实的残余缺口。

## 当前台账快照

| 来源 | 当前状态 |
| --- | --- |
| `parity/commits.json` | 4,595 个提交；本轮按证据更新 #40625 / #60905 / #63528 后为 3,670 complete / 51 equivalent / 845 not_applicable / 11 partial / 18 blocked_precondition（详见末节「本轮进展」） |
| `parity/domains.json` | 26 个 domain；22 complete / 4 partial |
| `parity/contracts/manifest.json` | 217 个 contract；213 complete / 4 partial |
| `parity.json` | 232 项；225 done / 3 partial / 4 intentional_difference，但 `generatedAt` 为 2026-10-28，`rustUpstreamHead` 仍为 `67f0994b` |
| `parity/baseline.json` | 目标 Rust `3f65e9f1`，Go 起点 `3e02afa7`，`certificationReady: false` |

`parity/baseline.json` 的认证要求是 zero-exception、3 次连续 SDK 差分、6 个原生平台矩阵；当前证据只有 2 次 SDK run，以及 `windows/amd64` 一个原生 SQLite 结果。

## 验证门状态

使用正确的 Rust 根目录运行：

```powershell
$env:CODEX_RUST_ROOT = 'D:\qax\reagent\dev\git\codex\codex-rs'
go test ./parity -count=1
```

结果：本轮最终状态下 **`go test ./parity -count=1` 整包通过（38.4s，EXIT=0）**。此前记录的 5 个失败（`TestContractVerifierAuditMatchesTree`、`TestCrossPlatformBuildsCompile`、`TestGoVetIsClean`、`TestRustErrorCodeSurfaceAgainstGo`、`TestContractVerifiersExistInGoTestFiles`）单独复跑时也全部通过：当时失败于扫描 `third_party/bwrap/work/bubblewrap-0.13.0` 的 `Access is denied`，属于工作树状态问题，不是断言不一致。`go vet ./...` 在本轮结束时同样干净通过（VET=0）。

注意：`CODEX_RUST_ROOT` 必须指向 `...\codex\codex-rs`。如果只指向 `...\codex`，测试会回退到旧的 `codex-anchor-e54a5713`，从而把 Go 已对齐新 Rust 的行为报成漂移。本次误用旧锚点运行时出现了 20 多个假失败，包括 `untrusted_as_tool_output`、`code_mode_host_grpc`、feature registry 等。

另外跑了一组聚焦测试，确认 Guardian lifecycle、agent navigation 和插件 archive 代码至少能编译并跑通现有回归：

```powershell
go test ./appserver ./mcp ./plugin ./tui/app -run 'TestGuardianV3Host|TestAgentNavigation|Test.*Archive.*LikeRust|Test.*RegistrationScope|Test.*EnvironmentID' -count=1
```

`appserver`、`plugin`、`tui/app` 通过；`mcp` 该正则没有命中测试。MCP 的现有 catalog/环境测试仍由完整 parity 套件覆盖。

## 真正需要补的代码

### P0：Guardian v3 的 per-agent reviewer runtime

Rust 在 `codex-rs/ext/guardian-v3/src/host.rs` 的 `on_thread_ready` 中为每个有 board identity 的 agent 创建 `ActorReviewer`，把它注册到 thread store，并在 root agent 上启动 housekeeping。Go 已经有：

- `appserver/guardian_v3_host.go`：routing host、live delivery、housekeeper。
- `appserver/guardian_v3_lifecycle.go`：thread ready / turn end / thread stop 生命周期接线。
- `agentboard/guardian_reviewer_conversation.go`：保留会话、inbox drain、slot 的 carrier。
- `appserver/guardian_evidence_injector.go`：向保留线程注入 evidence 的 carrier。

但 `GuardianReviewerSlot` / `GuardianReviewerConversation` 在 `appserver` 中没有生产使用点，Guardian host 也没有创建 per-agent reviewer。因此以下行为仍缺：

- 每个 agent 的独立 reviewer 会话与 `ThreadManager` 启动；
- reviewer 空闲时 drain inbox，verdict 前保留会话；
- 同步 approval 的 attached reviewer、evidence catch-up、重试时重新预占会话；
- nested Guardian approval 的 enclosing tool context；
- 异步 Guardian scorer 与 serving probe 的并行评分。

对应台账：`#62134`、`#62135`、`#62136`、`#62309`、`#61946`、`#62571`。这是当前最大的一块功能缺口。

### P1：MCP 多 environment 的 catalog 与 tool namespace

`mcp/api.go` 已经有 `ServerRegistrationKey` 和 `EnvironmentID` 选择器，连接缓存、状态通知、工具调用已能按 scope 区分。但 `mcp/catalog_priority.go` 仍按 name 归并：

- `tierKey{name: action.Name, tier: ...}`
- `winners[action.Name] = action`

Rust 用 `McpServerKey`（name + environment id）贯穿 catalog、connections、tool calls、resources 和 approvals。Go 还缺 catalog 级别的 key 重构，以及 environment-scoped tool namespace / resource listing 的完整接线。对应 `#60905`。

### P1：Code Mode typed gRPC stream 与 heartbeat

`codemode/grpc_stdio_host.go` 的 `grpcStdioHostServer` 仍嵌入 `codemodev1.UnimplementedCodeModeHostServer`，生产实现只有 `GetProcessMetrics` 和 `Transport`。Rust 的 proto 已有：

- `OpenSession` server-streaming；
- `SubscribeToToolCalls` server-streaming；
- `heartbeat_interval_s` 与 `StreamHeartbeat`。

Go 的生成代码和测试 carrier 已存在，但生产 host 没有实现这些 RPC，因此 idle heartbeat 不会工作。对应 `#61385`。

### P1：侧栏图片预览

Rust 有 `tui/src/sidebar_image.rs`，`sidebar_document.rs` 会把图片解码成 `DocumentContent::Image` 并绘制；Go 的 `app/sidebar_document_host.go` 只接受 UTF-8 文本，非文本直接返回 `remotePreviewNotText`，没有 `SidebarImage` 类型或图片绘制路径。对应 `#61919`。

### P1：rollout trace 采集器

Go 已有 `rollout/trace_reduce.go` 的 reduce/replay 和 `[rollout_trace]` 配置解析，但缺少 Rust `rollout-trace` crate 对应的 writer/producer。结果是配置中的 `max_payload_bytes`、`max_bytes` 没有实际执行点，CLI 的 trace bundle 只能消费已有 bundle，不能约束采集。对应 `#61896`。

### P1：realtime handoff 的持久化与准入屏障

Go 已有 `ReserveHandoff` / `ReleaseHandoff` / `BindTurnSession`，能避免同一段语音被重复消费；但仍缺 Rust 的 prepared-input cache、acceptance barrier、持久化 call lifecycle（Open/Ending/Closed）以及 V3 reconnect 后未消费语音的恢复。对应 `#62288`。

### P1：异步 host service-tier 读取与接受

Rust 在 `core/src/agent/host/service_tier.rs` 中做异步 host 读取和 acceptance；Go 已有 service tier 选择、子 agent 继承和 per-turn 过滤，但没有 host 侧异步读取/接受的对应路径。对应 `#60075`。

### P1：subagent item-boundary interrupt 与 remote compaction summary

已补齐（`6ce6cb67b`）。Go 原来没有可挂在真实 active turn 上的中断槽位，只能取消工具或整轮硬中断。现在 `turn.SamplingInterrupt` 绑定单个 turn：注册按 turn id 校验，工具取消与采样取消分离，没有有效注册时原子地退回旧的硬中断；停止发生在 item 边界，已完成的输出前缀保留，未执行的 function/custom/search 调用补对应 wire shape 的 aborted 输出，并清空上游 response id 以免 resume 重新带出被丢弃的 item。remote compaction 注册到 checkpoint 安装之后才发布 interrupted terminal，中途压缩的 usage 估计不会被压缩前的高水位覆盖。开关 `multi_agent_v2.interrupt_at_item_boundary` 默认关闭，仅在 v2 且存在存储的 parent 关系时生效。

### P1：插件 eval archive 的 scoped signing key

Go 已经有通用的 `VerifyPluginArchive`、签名 key 拉取和按 endpoint/credential 划分的 cache，但 `plugin/archive_verification.go` 把 `keys` 和 `eval_keys` 合并后要求所有 key 的 purpose 都是 `PLUGIN_RELEASE_ARCHIVE_SIGNING`。Rust `#63528` 新增的是：

- `EVAL_PLUGIN_RELEASE_ARCHIVE_SIGNING` purpose；
- `keys` 只能是 marketplace/release key；
- `eval_keys` 只能是 eval key；
- eval key 仍按 endpoint、account、token 的 scope 缓存和解析。

因此 Go 当前会把合法的 eval key 以 `unsupported_key_purpose` 拒绝，或者无法区分 eval 与 release key 的用途。对应 `#63528`。这不是“完全没有 verifier”，而是 scoped eval-key 语义缺失。

### P2：`/cd` 的 closed-agent 守卫

Rust `change_working_directory` 会排除 closed agents，并在其他 open agent 仍运行时拒绝切换目录。Go 的 `canChangeWorkingDirectory` 只检查当前 session 的 queued input、background processes、steering 和 review mode，没有检查 `AgentNavigationState` 中其他 agent 的 running/closed 状态。对应 `#40625`。

### P2：Command Center 的多 backend 发现

Go 已经实现了 thread ownership 的“第一次观察者获胜”，但 agents overview 仍是 single-backend discovery：没有 per-backend discovery cursor、initialization、pin、backend-scoped refresh、usage 请求和后台 archive/delete 通知隔离。对应 `#60929`。

### P2：平台相关

- Windows 原生 MXC：`sandbox/mxc/run_windows.go` 仍返回 `native MXC is unavailable on this executor`，`execserver` 的 `windowsMxc` 能力为 false。对应 `#42841`、`#55378`。
- Linux bwrap launcher：proxy bridge 生命周期依赖 Go 尚未实现的 launcher 架构（`#61898` 仍未做）。Bubblewrap 0.12 的 proc mount fallback（`#61900`）已在本轮实现并合并：`execBubblewrap` 改为共用 builder 加最小只读 `/bin/true` 预检，命中同一行 `Can't mount proc on /newroot/proc|/proc: Invalid argument|Operation not permitted|Permission denied` 时去掉 `--proc` 重跑，真实用户命令只执行一次；残差是该节点没有真实的 0.12 proc-denial 环境，回退路径用包装脚本注入诊断验证。
- TUI Ctrl-Z suspend：`tui/tui/job_control.go` 只有 `SuspendKey` 常量和纯模型，tea model 没有 `SuspendMsg`，也没有恢复前的 foreground ownership 检查。对应 `#63520`。
- macOS Secure Enclave：`appserver/user_verification_macos_cgo.go` 已经包含 `LAContext` 和 `kSecAttrTokenIDSecureEnclave` 的 cgo 实现，缺的是 macOS 真机与已登记生物识别的验证，不是“完全没写”。对应 `#43624`。

## 台账已经过时或需要改写的项

以下条目在 `parity/commits.json` 中仍是 blocked/partial，但代码已经给出了对应实现，建议先做一次 focused re-audit：

- `#61795` / `#61797` / `#61798` / `#61799`：Go 已有 `guardian_v3_host.go`、`guardian_v3_lifecycle.go`、`guardianInferenceAgent`，并由 `installGuardianV3Host` 接到 thread lifecycle。台账中“没有 housekeeper / 没有 app-server host”的表述已经过期。
- `#43624`：macOS Secure Enclave 的 cgo carrier 已存在；正确状态应是“实现存在，真机验证阻塞”。
- `#61880`：`tui/markdown/local_links.go` 已能解析本地文件目标和 `:line:column` 位置，`tui/history_cell/spoken_artifacts.go` 也会给 spoken 输出补 workspace 文件链接；需要补一个 document-target differential 后再关闭。
- `#61889` / `#61890` / `#61922`：这些提交在 `parity/commits.json` 中已经是 complete，但仍被 `parity/baseline.json` 的 blocker 文案列为 blocked。
- `#62372`：Go 已有 Guardian V3 host 和 dedicated sampler，partial 中“没有生产 host”的 residual 需要重新核对。
- `#60487` / `#60418` / `#60492` / `#60432`：这些旧文档中的小缺口已经在代码中修复，包括 `untrusted_as_tool_output` 默认值、后台终端不再阻止 Done、Claude Code local skills 排除规则和 SQLite 3.54 下限。

`#63528` 不能直接关账：verifier 和 cache 已经有了，但 eval key 的 purpose 与 scope 语义仍然缺失，应改写为“partial：eval archive signing key scope”。

## 认证与台账缺口

`parity/baseline.json` 自身存在矛盾：

- 第 3 行说 12 partial / 20 blocked；
- 第 4 行说“24 commits are blocked”，但实际只列了 23 个，且包含已 complete 的 `#61889`、`#61890`、`#61922`；
- 第 5 行说“31 commits are partial”，而 `commits.json` 当前是 12 个 partial。

这会直接误导认证判断，建议先让 baseline 的 blocker 列表从 ledger 自动生成，或至少与 `commits.json` 做一致性测试。

另外：

- `parity.json` 仍停留在 `67f0994b`，没有推进到目标 `3f65e9f1`。
- 4 个 open domain：`alignment-control`、`sqlite-runtime-version`、`sdk-cancellation`、`cross-platform-release`。
- 4 个 open contract：`mcp-network-policy-derivation`、`usage-analytics-turn-trigger`、`sidebar-chooser-files-and-paths`、`sidebar-document-preview`。
  本轮复核：两个 sidebar contract 列出的全部 verifier 在这台 Windows 上都通过（`go test ./tui/tea ./app -run 'TestSidebarChooser|TestMiddleLabel|TestFilenameLabel|TestDocumentLabel|TestRemoteTUI|TestEmbeddedSidebar|TestSidebarProtocol|TestSidebarPreview'`），它们仍未关闭的原因不是 chooser/文档逻辑，而是 #61919 的图片预览能力缺失（Rust 侧 sidebar 会按 PNG/JPEG/GIF/WebP 打开图片并带格式与尺寸标签、无终端图形时降级）。因此这两条 contract 应等 #61919 落地后一起复核，不能凭 test-pass 直接改成 complete。
- SDK 证据只有 2 次 run，要求 3 次；原生 SQLite 只有 `windows/amd64`，要求 6 个平台。
- GitHub Actions 仍受账户计费问题影响，macOS/Linux 原生 runner 无法产出证据。
- `sdk-cancellation` 仍记录 Rust `bbbf396839` 的 PowerShell 子进程泄漏；Go 侧进程树取消更安全，不应为了对齐而回退。

## 依赖校验风险：charm.land/glamour/v2 v2.0.1 的 sumdb 与镜像内容分叉

这是本轮两个节点都撞到的一条外部风险，与代码对齐无关，最终按下面的实测结论收敛：

- sum.golang.org 的记录：`charm.land/glamour/v2 v2.0.1 h1:xl+r00A4aJWU0z8fgwKd9fQQ4rsphqGUzuEiXZP5n+c=`。
- 用全新 module cache + `GOPROXY=goproxy.cn` + `GOSUMDB=sum.golang.org` 下载，`go mod download -json` 返回的 `Sum`
  就是 `xl+r00A4…`，zip 139,064 字节；对该 zip 重新计算 h1 得到同一个值，说明 sumdb 与当前 proxy 一致。
- 本机 2026-11-07 起的缓存是另一份：zip 3,793,168 字节，解出 239 个文件共 4,660,169 字节（含 `styles/gallery/*.png`
  几张几百 KB 的图），h1 才是旧 go.sum 里的 `Vx2F81a4…`。该版本被上游重新发布过，新版把 gallery 图片换成了占位文件。
- 项目镜像当时已经采用 `xl+r00A4…`，目标仓库还锁着 `Vx2F81a4…`，两边不一致正是节点 checksum mismatch 的来源。

处置（已完成）：仓库 `go.sum`、项目镜像与本机 module cache 统一为 `xl+r00A4…`。本机缓存里 239 个文件按新制品逐个覆盖
（同名集合完全一致，无需删除），zip 与 ziphash 换成新制品，随后 `go mod verify` 通过、`go build ./...` 退出 0、
`go test ./tui/...` 全绿。代码里没有 `go:embed` 依赖 gallery 图片，渲染测试不受影响。


## 第三轮追加（#61900、probe 竞态、Seatbelt 规则顺序、macOS Keychain/plist）

- `#61900`：Linux 节点实现 proc-mount 预检与 fallback，已合并。
- 探针竞态：`sandbox/bwrap.go` 原来在读取协程结束前就取 stderr 快照，launcher 写了失败行但派生进程仍持有写端时，快照可能为空，于是把坏 launcher 判成可用。新增 `drainBwrapProbeStderr`：Linux 上把管道读 deadline 设为当下时刻，先抽干已写入字节再停止（等价 Rust 的读到 WouldBlock），不支持 deadline 的平台退化为 50ms 宽限加关闭读端；新增确定性回归 `TestDrainBwrapProbeStderrKeepsLauncherOutput`。
- Seatbelt 规则顺序：workspace 位于 `/private/tmp` 这类 scratch 别名下时，`PROTECTED_WRITE` 的 deny 会早于更宽的 `/private/tmp` allow 输出，而 Seatbelt 取最后匹配规则，导致 `.git` 被重新放开。现在把 scratch 排除、root anchor unlink 与 PROTECTED_WRITE 全部延后到最后统一输出（与 Rust 把 root_anchor_denies 追加在 allows 之后一致）；新增 `TestSeatbeltPolicyKeepsWriteDeniesAfterScratchAllowances`，该用例在旧顺序下失败、新顺序下通过。
- macOS Keychain：Rust 用 `kSecAttrLabel`（`ItemSearchOptions::label` 与 `GenerateKeyOptions::set_label`），Go 原来用 `kSecAttrApplicationLabel`，两边密钥互不可见；已统一到 `kSecAttrLabel` 并补 `kSecMatchLimitOne`。旧 Go 条目视为不存在，需要重新 enroll。
- doctor/desktop_darwin.go：改为 `plutil -convert json`（5s 超时、64KiB 上限），bundle id 不匹配视为不是 Codex，version 回退与 build 数字校验按 Rust 实现；新增 XML 实体转义、binary plist、非 Codex bundle 与软件 P-256 缺 SecureEnclave token 被真实 C validator 拒绝四条 darwin 测试。

## 建议顺序

第一波先处理台账与低成本缺口：

1. 用 `commits.json` 重新生成 `baseline.json` 的 blocker 列表，修掉 24/20、31/12、已 complete 仍在 blocker 里的矛盾。
2. 对 `#61795/#61797/#61798/#61799`、`#43624`、`#61880`、`#62372` 做 focused re-audit，能关的直接关。
3. 补 `/cd` 的 closed-agent / other-open-agent 守卫（`#40625`）。
4. 重新生成 `parity.json`，或明确它与 `baseline.json` 的职责边界。

第二波处理核心行为：

1. Guardian v3 `ActorReviewer` runtime（`#62134`，并连带 `#62135/#62136/#62309/#61946/#62571`）。
2. MCP catalog/tool namespace 的 `McpServerKey` 重构（`#60905`）。
3. Code Mode `OpenSession` / `SubscribeToToolCalls` 与 heartbeat（`#61385`）。
4. realtime handoff 持久化与 acceptance barrier（`#62288`）。
5. rollout trace recorder（`#61896`）。
6. 异步 host service-tier（`#60075`）。subagent boundary interrupt 已在后续轮次补齐（`#61909/#61916`）。
7. 侧栏图片预览与文档 preview 的剩余图片路径（`#61919`）。
8. 插件 eval archive 的 scoped signing key（`#63528`）。

第三波处理平台与外部认证：

1. Windows MXC、Linux bwrap launcher、macOS Secure Enclave 真机验证。
2. TUI Ctrl-Z suspend / foreground ownership（`#63520`）。
3. 6 平台原生 SQLite 与 3 次连续 SDK 差分；需要 GitHub 账户和授权环境恢复。

## 验证命令

```powershell
$env:CODEX_RUST_ROOT = 'D:\qax\reagent\dev\git\codex\codex-rs'
go test ./parity -count=1
```

在当前沙箱中，除 `third_party/bwrap/work/bubblewrap-0.13.0` 的 ACL 导致的 5 个扫描失败外，其余语义测试通过。建议在 CI 或可读的 checkout 中重跑一次，拿到完整绿色结果。

## 本轮进展（2026-11-25 第二轮，lead 本地落地）

已落地并通过定向测试的三项，代码在 `D:\qax\reagent\dev\codex_go`：

| 上游 | 改了什么 | Go 位置 | 验证 |
| --- | --- | --- | --- |
| #63528 | eval 插件归档用独立 purpose 的签名密钥 | `plugin/archive_verification.go`：新增 `PluginArchiveEvalSigningKeyPurpose`（`EVAL_PLUGIN_RELEASE_ARCHIVE_SIGNING`），`keys` 走 release purpose、`eval_keys` 走 eval purpose，跨两个列表按 KeyID 去重；空/超限返回 `invalid_signing_keys_response`，purpose 不符返回 `unsupported_key_purpose` | `go test ./plugin -run 'TestPluginArchiveEvalSigningKeysAreScopedLikeRust|TestEvalSigningKeyCacheIsScopedToEndpointAccountAndTokenLikeRust|TestVerifyPluginArchiveLikeRust|TestFetchPluginArchiveSigningKeysLikeRust'` |
| #40625 | `/cd` 忽略已关闭的 agent | `tui/tea/working_directory.go`：`canChangeWorkingDirectory` 改为 `workingDirectoryBlockReason`，新增 `otherAgentRunning`——跳过自身与 `IsClosed`，任一 `IsRunning` 即阻断为 "Cannot change: another agent is running." | `go test ./tui/tea -run TestWorkingDirectoryChange` |
| #60905 | MCP catalog 按 environment 隔离同名服务器 | `mcp/catalog_priority.go`：tier 桶、winners、最终 servers 全部以 `ServerRegistrationKey(name, effectiveEnvironmentID)` 为键；`disabled`/`requirementsDisabled` 这类 name 级 veto 仍按裸名；`WithMaterializedServers` 优先匹配已有 catalog key，否则按 `ServerRegistrationKey(displayMCPServerName(key), envID)` 回退，保留 env scope 与 Source/PluginAttribution/CredentialPolicy/RequestAuthProvider | `go test ./mcp -run 'TestResolveCatalog|TestMaterializedServersPreserveEnvironmentScope|TestPluginAttribution|TestSourceFromRegistration'` |

跨平台编译：`GOOS=linux`、`GOOS=darwin` 下 `./tui/... ./mcp/... ./plugin/... ./parity/...` 全部通过；`GOOS=windows` 全量 `go build ./...` 通过。

台账修正：上述三项在 `parity/commits.json` 改为 `complete` 并写入实现与测试证据，`parity/baseline.json` 的开项统计按实际状态重算（3,670 complete / 51 equivalent / 845 not_applicable / 11 partial / 18 blocked_precondition）。`go test ./parity -run TestAlignment` 通过。

仍存在的环境性失败（与本轮改动无关，需在对应平台复跑）：`plugin` 的 `TestMarketplaceUpgradeReusesInstalledGitObjectsLikeRust`（Windows 长路径）、`TestAutomaticGitUsesTrustedRepositoryAndCleansUp`（`$GIT_DIR too big`）；`mcp` 的 `TestDesktopSocketMCP*`（Windows AF_UNIX `bind: invalid argument`）；`parity` 中 5 个依赖 `third_party/bwrap/work` 的测试在 Windows 上因 ACL `Access is denied` 失败。

原生节点协同：`linux-align`（Linux 节点）在实现 #63520 Ctrl-Z suspend（SIGTSTP/SIGCONT、controlling terminal 守卫、恢复后光标重探、事件流暂停）与 #61900 proc-mount preflight fallback；`darwin-align`（macOS 节点）在补 darwin+cgo 的 provider 选择断言（具体 backend 类型、无硬件探测）并真机取证 LAContext readiness。两者交付后由 lead 合并进本仓库并统一复跑。

仍未动、且 Go 侧缺底层抽象的项：#61896 rollout trace 写出器（Go 只有配置与 reducer）、#61385 Code Mode typed session/tool-call stream（Go host 只有请求/响应帧）、#61945 host fork 的 `InheritedSessionId`、#61946 Guardian nested approval 的 enclosing tool context、#60075 host service-tier provider（#61909/#61916 已在后续轮次合并）。这些需要先补抽象再谈对齐，不适合与平台验证并行仓促合并。

## 第二轮追加（macOS 节点交付 + 认证检查修正）

macOS 节点（darwin-align，macOS 12.4 / darwin-amd64 / Go 1.27.1 / CGO=1）的交付已合并，证据保存在 `deliverables/darwin-align/`：

| 结论 | 事实 |
| --- | --- |
| provider 选择 | 平台工厂、nil-service fallback、默认 runtime router 三个入口都实际选中 `*darwinUserVerificationProvider` + `cgoDarwinUserVerificationBackend`；`CGO_ENABLED=0` 时选中 `UnsupportedUserVerificationProvider` |
| 原生测试 | `go test ./appserver -run UserVerification` 通过（2.022s）；`appserver`/`doctor` 的 UserVerification/Desktop/Platform 通过（3.154s / 1.041s）；`go build ./cmd/codex`（darwin/amd64）产出 x86_64 Mach-O |
| SQLite | Go 内嵌 3.54.1 实测通过；系统 `sqlite3` 为 3.37.0 |
| Secure Enclave 真机 | 未验证成功：非交互探测 `DeviceSupported=false`，`com.apple.LocalAuthentication` 返回 -6 `Biometry is not available.`，readiness 测试真实 SKIP；现有 P-256/SEC1 导出测试只证明软件路径 |
| 遗留 | 端到端签名需要 Touch ID 硬件 + 已登记生物识别 + 可用的本地认证会话；arm64 未测；该机把 Go 13.0 目标对象链接到 12.0 |

据此修掉两处**过时的 Go 断言**（`sandbox/run_test.go`）：两个 run-plan 测试原本只特判 Linux/Windows，把 macOS 落到「不支持」分支，而 macOS 实际返回 Seatbelt 计划。现在 darwin 分支断言 `SandboxTypeMacosSeatbelt`、`/usr/bin/sandbox-exec` 启动器以及空的 unsupported reason。该改动同时需要 macOS 节点复核（已发出验证请求）。

另外修掉两处会让认证门假失败/假通过的问题：

- `appserver/rust_protocol_surface_test.go` 的 Go 常量白名单漏了 `MethodAccountVoiceSettingsRead/Write`（协议常量、handler 与 like-rust 测试都已存在），导致 `account/voiceSettings/read|write` 被误报为缺口。
- `appserver/schema_test.go` 的两个 precomputed export 期望值仍指向旧锚点 `a0d5b0a661`；Go 内嵌的两个 `.zst` 与 Rust `3f65e9f1` 工作树中 `app-server-protocol/schema/precomputed/` 的文件 SHA-256 完全一致，因此把 pin 改到当前候选（`761cbc57…`、`d1d275e3…`）。

`go test ./appserver -run 'TestPrecomputedExportArtifactsMatchTargetRustCommit|TestRustProtocolMethodSurfaceAgainstGoConstants'` 通过。

macOS 还有一个本轮发现、尚未合并的 doctor 缺口：`doctor/desktop_darwin.go` 的 `desktopBundleMetadata` 把 `Contents/Info.plist` 当 XML 文本读，用 `<key>…</key>` 之后最近的 `<string>` 取值。Rust 3f65e9f1 并不自己解析 plist，而是执行 `/usr/bin/plutil -convert json -o -`（5 秒超时、`kill_on_drop`、stderr 丢弃、stdout 上限 64KiB），再按 JSON 键精确取值，`CFBundleIdentifier` 不等于 `com.openai.codex` 时返回「不是 Codex」而不是错误，`version = CFBundleShortVersionString ?? CFBundleVersion`，`CFBundleVersion` 必须能解析成数字。因此 Go 在二进制 plist、XML 实体转义、以及 key 与 string 之间夹其它元素这三种情况下会读错或读不到。对照源码已放进 `deliverables/darwin-align-rust-reference/`（含 README 的最小改法），由 macOS 节点实现并用 `plutil -convert binary1` 造 fixture 验证后由 lead 合并。

同一条线路上还有一处更关键的 Keychain 命名空间差异（macOS 节点核对 Rust 源码后发现，正在修）：Rust 的 `provider.rs::key_query` 用 `ItemSearchOptions::label(...)`、`create_key` 用 `GenerateKeyOptions::set_label(...)`，两者落到 `kSecAttrLabel`；而 Go 的 `appserver/user_verification_macos_cgo.go` 在 lookup、私有属性与 delete query 三处都用 `kSecAttrApplicationLabel`。也就是说 Rust 创建的 Secure Enclave key 与 Go 创建的根本不在同一命名空间，互相都找不到，这会让“Go 侧 verify”在真机上静默走不到 Rust 生成的密钥。对齐方向是写入只用 `kSecAttrLabel`（与 Rust 一致），查询可先用 `kSecAttrLabel` 再回退旧属性，删除两种属性都清；若不做回退则必须明确记录“旧 Go 条目视为不存在”。

macOS 节点报告的另外三处 sandbox 失败已定性为产品缺口并修掉（`d30ba1b79`）：Seatbelt 匹配的是 `/tmp`、`/var/tmp` 的 canonical 拼写，raw `/tmp` 的 deny 保护不到同一 inode 经 `/private/tmp` 的授权，所以 scratch 排除、`PROTECTED_WRITE`、protected ancestor 与 symlink-home 覆盖判断现在都只归一 depth-one 系统别名；嵌套可变 writable 软链仍要求显式 opt-in，空 trusted home 也不会退化成信任 cwd。节点用默认 `TMPDIR`（`os.TempDir=/tmp`）跑 `CGO_ENABLED=1 go test ./sandbox` 0.985s 通过，两个 native 子例实际执行而非 SKIP。

## Linux 节点交付（#63520 Ctrl-Z suspend 已合并）

linux-align（Linux 节点，go1.26.3 linux/amd64）实现了 #63520，代码已合并：

- `tui/tea/job_control.go`：用公开的 `bubbletea.WithFilter` 在 SuspendMsg 进入内置处理前接管。Bubble Tea v2.1.1 的内置 ResumeMsg 在 RestoreTerminal 之前就恢复 raw mode，所以这里自己做 release → 挂起 → 等到真正拿到前台 → restore 的顺序，并在拿到前台后才发 ResumeMsg。
- `tui/tea/job_control_unix.go`：打开 `/dev/tty`（而不是任意 stdin fd）做 controlling terminal 预检；`TIOCGPGRP` 查前台进程组；向整个进程组发 SIGTSTP；用「计 tick 而不是墙钟 deadline」的方式等 SIGCONT，因为整个进程组被停住时 Go 的 timer 可能已经不走了；tty 丢失或 ctx 取消时不重新进 raw mode。
- `tui/tea/job_control_other.go`：非 unix 平台保持可编译。
- `tui/tea/model.go`：在 `bubbletea.NewProgram` 前安装 filter，并在 `update` 最前面分发 job-control 消息。

节点上的真实 PTY 验证覆盖：inline 与 alternate 屏、暂停超过 1 秒、后台 SIGCONT 必须保持 canonical、只有 fg 没有第二次 SIGCONT 时必须恢复 raw mode 且草稿保留；另外覆盖无 tty 的 detached 进程与忽略 SIGTSTP 的进程组。命令结果：`go test ./state ./sandbox ./network`（7.800s / 0.168s / 8.713s）、`./appserver ./tui/...` 的 UserVerification|JobControl|Suspend|WorkingDirectory 全部通过、`go test -race ./tui/tea -run 'JobControl|Suspend|CtrlZ'`（9.610s）通过、SQLite 3.54.1 校验通过。

这里**没有采纳**该补丁里的 `go.sum` 改动。节点把 `charm.land/glamour/v2 v2.0.1` 的 h1 改成了 `xl+r00A4…`（sum.golang.org 上的记录），但本机与 proxy.golang.org / goproxy.cn 实际取到的模块字节计算出的 h1 都是 `Vx2F81a4…`。也就是说这个模块版本的公开发布记录与镜像当前提供的内容不一致，直接改成 sumdb 值会让所有依赖验证失败（`checksum mismatch`）。仓库保留原值，并把这条上游 provenance 不一致记为风险待上游确认。

#61900 已由该节点实现并合并（三个文件：`sandbox/linuxsandbox/linux.go`、`proc_mount_linux.go`、`proc_mount_linux_test.go`；单元测试与 race 通过，真实 bwrap 下两种诊断注入都能回退且 payload 只执行一次）。#61898 仍缺：Go 的 `execBubblewrap` 是进程内直接 `unix.Exec`，没有 Rust 那套 proxy supervisor → mount cleanup supervisor → bwrap 的交接结构；普通 goroutine 无法照搬 Rust 的 `CLONE_FILES` 私有线程 FD 表，需要 re-exec 进程隔离，方案见 `deliverables/linux-align/REPORT.md`。
