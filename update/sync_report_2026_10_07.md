# codex-go 同步总结：2026-10-07

## 上游基线

| 项 | 值 |
|---|---|
| 上游仓库 | `github.com/openai/codex`（本地 `/home/jacks/jacks_dev/codex`） |
| 本轮起点 | `4aaee872e3` #51539（sync385–388 已识别，14 项待落地） |
| 本轮终点 | `e95abcdf49` #51547（2026-10-07 01:42 UTC） |
| 新增加口 | `4aaee872e3..e95abcdf49` = 1 commit（#51547） |
| Go 仓库 | `/home/jacks/jacks_dev/codex_go` |

## 结果概览

本轮把第二批（4aaee872e3 一轮）遗留的 14 项继续收敛，并落地新上游提交 #51547；连同上一轮已提交但未推送的 sync385–388 一并推送。

| Go commit | 上游 | 一句话 |
|---|---|---|
| `sync389` | #51547 | `windows.allow_mxc = false` 拒绝显式 `windows.sandbox = "mxc"`（错误文案与 Rust 一致），并冻结「Go 不隐式选中 MXC」等价性 |
| `sync390` | #51511 | 冻结盘符 no-follow 打开语义（Go 不走 NT 别名，故为结构性满足 + Windows 测试） |
| `sync391` | #51502 | rendezvous 拨号 10s 有界、重连退避跨抖动连接保留（≥120s 稳定才重置）、中继数据写 60s 预算 |
| `sync392` | #51512 | Windows 沙箱 temp 根改由工作负载环境的绝对 TEMP/TMP 解析，去掉宿主回退与大小写重复键歧义 |

推送：`d1987a05..85d99889`（10 个提交，含 sync385–392 与两份计划提交）。

## 验证

- `go build ./...` 通过；本轮改动文件 `gofmt -l` 为空。
- 新增/更新测试：`config/windows_mxc_optout_test.go`、`execserver/no_follow_windows_test.go`（`//go:build windows`，Linux 上以 `GOOS=windows go vet` 验证）、`execserver/remote_reconnect_backoff_test.go`、`sandbox/windowssandbox/resolved_permissions_test.go` 用例。
- 既有失败（`git stash` 对照确认，与本轮无关）：`config` 4 项（缺 `/etc/codex/managed_config.toml`、Windows 路径语义）、`execserver` 3 项（缺 `codex-linux-sandbox`、`TEMP/TMP` 未设置、symlink ELOOP）、`sandbox` 2 项（Windows 路径/符号语义）。

## 剩余 10 项（结构性前置或 TUI）

| 上游 | 主题 | 前置依赖 |
|---|---|---|
| #51480 | 工具声明模式跨恢复窗口保留 | Go 未实现增量工具（incremental tools）模式 |
| #51482 | 技能身份/路径匹配改用 PathUri | Go 无技能身份类型，协议 surface 需先迁移 |
| #51491 | 执行器能力根归属与解析解耦 | Go 无 `ExecutorPluginProvider` 实现 |
| #51493 | 能力根绑定环境选择 | 需先移植协议侧绑定语义 |
| #51500 | Agent 命令中心共享任务置顶 | 需共享线程 section 读写（TUI，最后对齐） |
| #51503 | 向 MCP contributor 暴露已选环境 | Go 无 extension-api 贡献者抽象 |
| #51510 | 配置重载失败时保留实时 TUI 设置 | Go 无 `LocalSettings` 暂存机制 |
| #51515 | Agent 树关闭失败明细报告 | Go 无 `AgentTreeShutdown` 模型 |
| #51517 | 附件上传携带线程持久化意图 | Go 无 attachment-store 抽象 |
| #51525 | 执行器配置读取保留 CLI MXC 偏好 | Go exec-server 未实现 `environmentConfig/read`（能力位却上报 true，待核实） |
| #51539 | 完成后可感知的 realtime 附着 / 会话级 detach | Go 无 session Op 队列（oneshot 回报） |

## 环境说明

- `git push` 走 `https://github.com/jacks001314/codex_go.git` 无可用凭证（无 credential helper / token），本轮改用 SSH 推送：`git push git@github.com:jacks001314/codex_go.git main:main`（本机 `~/.ssh/id_ed25519` 对 `jacks001314` 已认证）。后续自动化需配置 pushurl 或提供 token。

## 追加（当日续跑）

- 上游 head 复核：`git pull` → `Already up to date`（`e95abcdf49`），无新区间。
- 交付镜像补同步：核验最近 30 个提交的 59 个改动文件，发现 `execserver/connection_diagnostics.go`、`connection_diagnostics_test.go`（`sync387`）此前未同步，已补齐；本轮共补同步 10 个文件，59/59 与本地 HEAD 一致。
- 新增 `sync393`：exec-server 不再上报未实现的 `environmentConfig/read` 能力位（`execserver/server.go` + `server_test.go` + 新增不变式测试 `TestEnvironmentConfigReadCapabilityMatchesDispatchLikeRust`）；#51525 主体（执行器本地配置读取子系统）仍受原始 TOML 分层栈 / 类型化 `RequirementSource` 缺失阻塞，证据见 `update/plan_2026_10_07.md` 第二十七轮。

## 追加（当日第三批：sync394）

- `sync394`（上游 #51525 载体）：实现执行器本地配置读取 `environmentConfig/read` —— 新增分层本地配置加载器（`config/local_layers.go`）、协议与宿主实现（`execserver/environment_config_read.go`、`execserver/hostconfig`）、CLI 仅保留 `features.prefer_mxc` 的启动偏好（`app/app.go`）；能力位从上一轮的 `false` 复位为 `true`，裸 stub 仍上报 `false`。
- 与 Rust 的差异（无 system 层 / MDM / macOS 管理 requirements / linked-worktree hooks 层；TOML 为解析后重序列化）已逐条记录在 `update/plan_2026_10_07.md` 第二十八轮。
- 验证：`go build ./...`、`gofmt -l` 空、`go vet` 无输出、`go test ./config/ ./execserver/... ./app/ -run ...` 仅既有基线失败、`go test ./parity` 通过。


## 追加（当日第四批：sync395）

- `sync395`（上游 #51539 "Add completion-aware realtime attachment and session-scoped detach"）：把"替换会话先发旧 close、会话级 detach 不误关替换会话、启动参数诊断脱敏"三条语义落地到 Go 的 realtime 子系统。
  - `realtime/realtime.go`：`StopParams.realtimeSessionId`（可选）+ `ErrRealtimeSessionMismatch`（陈旧 detach 不关闭、静默 no-op）；`StartWithOptions` 替换活动会话时先封存旧 timeline 并发 `thread/realtime/closed` 再发 `thread/realtime/started`；`StartParams.String()` 只输出 modality / initial item 数 / version。
  - `appserver/realtime_runtime.go`：`stopRealtimeConversationAsync` 吞掉 mismatch（与 Rust detach 的静默语义一致）。
  - 测试：`TestStartReplacementEmitsPreviousCloseLikeRust`、`TestStopSessionScopedDetachLikeRust`、`TestStartParamsStringRedactsCredentialsLikeRust`（+ 更新 `TestManagerLifecycle`）；app-server 全链路 `TestRealtimeStopScopedToSessionLeavesReplacementConnectedLikeRust`。
  - 结构性差异（详见计划第二十九轮）：Go 的 `Manager.Start` 本身同步且失败经 error 通知回报，故不新增独立 attach op；陈旧 fanout 清理 Go 早已按连接身份判定。
- 验证：`go build ./...` 通过；改动文件 `gofmt -l` 为空；`go vet ./realtime/ ./appserver/ ./app/` 无输出；`go test ./realtime/... ./eventmap/... ./parity/... -count=1` 通过；`go test ./appserver/ -count=1` 仅既有基线失败（OAuth、plugin repo、file-change apply 三项 + 两项既有 flaky）。
- 推送：`249ec903..c9d0c31c`（`git ls-remote` 确认 `origin/main = c9d0c31c`）。

## 追加（当日第五批：sync396）

- `sync396`（上游 #51493 "Bind capability roots to environment selections"）：能力根按环境选择绑定并保留原始顺序。
  - 新增 `appserver/environment_capability_roots.go`：`EnvironmentCapabilityRoots`（索引 + 根的模型）、`CapabilityRootsForEnvironment`、`CollectEnvironmentCapabilityRoots`（按原始索引恢复顺序，保证 executor skill 别名 `e0/e1…` 稳定）、`restrictCapabilityRootsToSelections`。
  - `appserver/runtime_router.go`：`selectedCapabilityRootsForThread` 把线程保留的根限制到当前选择的环境（活动 turn 优先、回落持久化选择）；`inspectSelectedCapabilityRootsForThread` 改用它，能力发现只在当前 turn 捕获的环境内生效；取消选择隐藏其根但保留，重选即恢复。
  - 测试：`appserver/environment_capability_roots_test.go`（拆/合并顺序稳定、环境顺序无关、取消/重选、端到端 store 视图 3/1/3 根）。
  - 结构性差异（详见计划第三十轮）：Go 无法表达"显式空选择集"（空即未设置）；`has_same_workspace` 在 Go 无落点（Go 对 FromThread 选择直接取线程配置，不做跨选择匹配）；绑定时机为读取时派生而非构造时快照，语义等价。
- 验证：`go build ./...` 通过；改动文件 `gofmt -l` 为空；`go vet ./appserver/` 无输出；`go test ./appserver/... ./prompt/... ./parity/... ./turn/... -count=1` 仅既有基线失败（OAuth、plugin repo、file-change apply 三项 + 一项既有 flaky）。
- 推送：`2e4c58bb..79a13c57`。

## 追加（当日第六批：sync397）

- 上游 head 复核：`git pull origin main` 拉到新提交 `18e28fe1b9`（#51556 "Complete dynamic tool lifecycles on cancellation"，前 head `e95abcdf49`）。
- `sync397`（上游 #51556）：动态工具在取消时完成生命周期。
  - `tool/registry.go`：`Spec.FinishesOnCancellation` + `Router.FinishesOnCancellation`（对应 Rust `CoreToolRuntime::finishes_on_cancellation`，默认 false）。
  - `turn/dynamic_tool_runtime.go`：动态工具 spec 置 true；`Execute` 请求前观察取消（不触达 sink）；取消导致的请求失败改用专用消息 `dynamic tool call was cancelled before receiving a response`。
  - `turn/tool_dispatcher.go`：新增 `ToolExecutionResult.Aborted`；声明 `FinishesOnCancellation` 的工具在 dispatch 被取消时转换为 respond-to-model 的 `tool call cancelled` 失败项并标记 aborted，未声明的工具保持 fatal 路径。
  - 测试：`turn/dynamic_tool_cancellation_test.go` 5 个（`...SettlesWithFailedItemLikeRust`、`...BeforeRequestLikeRust`、`...FinishesOnCancellationSpecLikeRust`、`...ReportsAbortedOutcomeLikeRust`、`...WithoutObserverStaysFatalLikeRust`）。
  - 结构性差异（详见计划第三十二轮）：项发布位置（dispatcher 统一发布 vs handler 内 emit）、无对应锁等待、post-tool hooks/输出处理的 ctx 门控沿用既有实现。
- 验证：`go build ./...` 通过；改动文件 `gofmt -l` 为空；`go vet ./turn/ ./tool/ ./appserver/` 无输出；`go test ./turn/... -count=1` 全通过；`go test ./tool/... -count=1` 仅 3 项既有环境失败（已 stash 复现确认与本轮无关）。
- 剩余：第三十一轮拆解的 8 项不变，继续按序推进。
