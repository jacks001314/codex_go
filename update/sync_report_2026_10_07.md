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

