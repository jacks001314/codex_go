# Codex Go in-session `/agents` dashboard - 2026-08-18 (#39094/#39112)

## 目标

standalone `codex agents` 仪表盘（上一轮）之后，补齐 Rust #39094 的 TUI 半部：
会话内 `/agents` 打开全屏仪表盘；嵌入会话显示 "Shared agents unavailable"
选择视图（Unix 提供 Start background server）；线程通知到达时自动刷新并保留
选中；选中根会话时直接挂接交互会话（Rust #39112 select_agents_overview_thread）。

## 实现

### 1. 命令面
- `tui/state.go`：`CommandAgents` + `/agents` 解析（无参命令，随
  slashInvocationDispatchable 走本地分发）。

### 2. tea Model 全屏仪表盘（`tui/tea/agents_overview.go`）
- Options 新增：`AgentsOverviewEmbedded`、`OnAgentsOverviewRefresh`、
  `OnAgentsOverviewDispatch/Stop/Rename`、`OnStartAgentsDaemon`。
- `applyAgentsCommand`：embedded → 选择视图；否则打开仪表盘（exitOnCancel=false）
  并刷新。
- 按键路由 `updateAgentsOverviewKey`（↑↓/PgUp/PgDn/Home/End/enter/esc/
  ctrl+f/s/n/r/x + 字符输入），动作经回调异步执行；esc 关闭恢复会话视图；
  ctrl+c 退出 TUI。
- 全屏渲染 `renderAgentsOverview`（复用 `tui/agents_overview.Render` + 提示行）。
- 刷新合并：`agentsOverviewInflight`/`agentsOverviewPending` 镜像 Rust
  refresh_pending；ThreadEventMsg/ThreadScopedEventMsg 到达时触发刷新（保留选中）。
- 打开：`openAgentsOverviewThread` 关闭仪表盘后走
  `applyAgentModalOption`/`onSwitchAgent` 挂接选中线程（当前线程 → no-op）。
- embedded 选择视图：ModalKindAgents + "Return to this session" /
  Unix-only "Start background server"（调用 `OnStartAgentsDaemon`）。

### 3. 主机接线
- `app/remote_tui.go`：remote TUI 提供 refresh/dispatch/stop/rename 回调
  （`app/agents_overview_tui.go`，复用 `remoteAgentsDashboardSource` 的
  ThreadLoadedList+Read / ThreadStart+TurnStart / TurnInterrupt / ThreadSetName）。
- `app/interactive.go`：local 嵌入 TUI `AgentsOverviewEmbedded: true` +
  `OnStartAgentsDaemon`（Unix 上 daemon lifecycle start，Windows 明确报不支持）。

## 测试与证据

- `tui/tea/agents_overview_test.go`：11 个测试（打开加载、导航/搜索/分组、派发带
  项目 cwd、停止、重命名、esc 关闭、打开挂接 onSwitchAgent、当前线程 no-op、
  embedded 选择视图、start-daemon、刷新错误提示、线程事件刷新与 pending 合并）。
- `tui/state_test.go`：`/agents` 解析。
- `app/agents_overview_tui_test.go`：Windows daemon 平台门禁 + 远程回调接线。
- 门禁：`go build ./...` 0 error；`go test ./... -count=1` 0 FAIL；`go vet` 干净。
- 登记：契约 `agents-overview-in-session`（manifest 65→66）；parity.json
  `agents-overview-in-session`（153→154 done）；commits.json #39094/#39112
  追加 in-session 证据。

## 待续（完整 Rust 端态）

- ✅ **切换会话草稿保留**：tea Model 增加 per-thread 草稿映射
  （`agentsOverviewDrafts` + `agentsOverviewPendingDraft`，镜像 Rust
  input_states/restore）：attach 时保存当前线程 composer 草稿、恢复目标线程
  已存草稿（消费），无存稿则清空 composer（Rust fresh chat widget），失败时丢弃
  pending 草稿。结构性说明：/agents 为 slash 命令独占 composer（与 Rust 相同），
  打开仪表盘前线程草稿在命令提交时清除；pending server requests 由既有
  backgroundThreadEvents 按线程缓冲覆盖。
- ✅ **ANSI 样式**：核心包段级渲染（span/SGR，`style.go`），`Render` 保持纯文本、
  `RenderStyled` 输出 Rust 同款样式（bold 标题/组头、dim 摘要/状态标签/占位符、
  状态点红/绿/青/灰、cyan bold 选中标记与 prompt 标签、footer 快捷键 bold）；
  tea Model 与 standalone dashboard 均切换为 RenderStyled。
- 待续：普通 /agent 切换路径（非仪表盘）的 per-thread 草稿管理（需 hook
  thread_event_store 式 per-thread input_state 记录，属会话生命周期改造）。
