# Codex Go interactive agents dashboard - 2026-08-18 (#39094/#39112)

## 目标

把 sync20 中标记为 "文本概览；交互式仪表盘待后续" 的 `codex agents` 提升为
Rust #39094/#39112 的交互式任务仪表盘：全屏列出根会话、按项目/状态分组、搜索、
派发新任务、重命名、停止活动任务、宽终端详情面板，并在非交互终端保持文本概览回退。

## 实现

### 1. 纯仪表盘核心 `tui/agents_overview`（镜像 Rust `agents_overview_view.rs`）

- `Group`（NeedsYou/Working/Ready/Finished）+ `GroupForStatus`（active+waiting flags、
  idle、systemError、notLoaded 映射，含 "Needs input/Working/Ready/Finished" 标签与
  ●/○/✓ 状态点）。
- `View` 状态机：`VisibleIndices`（搜索过滤 + 项目/状态分组排序）、`MoveSelection`/
  `JumpTop`/`JumpBottom`/`PageUp`/`PageDown`、`Activate`（输入非空→派发、空输入→打开
  选中线程、renaming→应用重命名）、`Cancel`（搜索→输入→exit_on_cancel 退出）、
  `ToggleSearch`/`ToggleGrouping`/`ClearNew`/`BeginRename`/`StopSelected`、
  `TypeChar`/`Backspace`/`Paste`、`ApplyRefresh`（保留选中，线程消失时取消 renaming）。
- `Render(width, height)` 纯文本渲染：header/summary 计数/divider/列表（组头 + 行 +
  选中滚动）/宽终端（≥90 列）详情面板（Task details/Project/Branch/Latest activity）/
  prompt 行（New task › / Search › / Rename › + 占位符）/footer 快捷键提示；
  runewidth 感知的宽度截断与换行。

### 2. `app/agents_dashboard.go`（bubbletea 程序接线）

- `agentsDashboardSource` 数据源接口（List/Dispatch/Stop/Rename/Close）：
  - **remote**（`--remote` 或 Unix 本地 daemon）：`ThreadLoadedList` 分页 +
    `ThreadRead` 读根线程（子代理折叠进根行）；`ThreadStart`+`TurnStart` 派发、
    `TurnInterrupt` 停止、`ThreadSetName` 重命名。
  - **local fallback**（Windows/无 daemon）：session store 列表 + 重命名；派发/停止
    给出 `codex app-server daemon start` / `--remote` 指引。
  - `newAgentsDashboardSourceForLocal`：Unix 上按 #39114 自动 `LifecycleStart` 本地
    daemon 并通过控制 socket 走 remote 源。
- `agentsDashboardModel`：`WindowSizeMsg`/`KeyMsg` 驱动 view，异步 List/Dispatch/Stop/
  Rename（busy 去重），错误与结果提示行，esc 退出、enter 打开返回
  `agentsDashboardResult{OpenedThreadID}`。
- `runAgentsCommandWithIO`：交互终端 → 仪表盘；否则 → 原文本概览。新增
  `--cd`（远程新任务 cwd）与 `--no-alt-screen`。
- v1 的 "打开" 语义：仪表盘退出并打印会话摘要 + `codex resume <id>` 提示；
  直接挂接交互会话（TUI attach）为下一增量。

## 测试与证据

- `tui/agents_overview`：16 个核心测试（分组/计数/搜索/项目排序/派发/打开/重命名/停止/
  取消/刷新保选中/渲染布局/状态分组/窄终端/粘贴清洗）。
- `app`：8+ 个仪表盘测试（线程行构建、store 行构建、加载渲染、派发带项目 cwd、
  打开返回线程、esc 退出、停止、重命名、搜索/分组/清空、列表错误提示、非终端文本回退）。
- CLI：`--cd`/`--no-alt-screen`/`-C` 解析测试。
- 门禁：`go build ./...` 0 error；`go test ./... -count=1` 0 FAIL；`go vet` 干净。
- 登记：契约 `agents-overview-dashboard`（parity/contracts/manifest.json 64→65）；
  parity.json `agents-overview-interactive-dashboard`（152→153 done）；
  commits.json #39094/#39112 从 "N/A (deferred)" → complete。

## 下一增量

- ✅ in-session `/agents`：见
  [plan_2026_08_18_agents_dashboard_in_session.md](./plan_2026_08_18_agents_dashboard_in_session.md)
  （tea Model 全屏仪表盘 + 嵌入会话 "Shared agents unavailable" 选择视图 +
  通知驱动刷新）。
- ✅ 仪表盘 "打开"：复用 onSwitchAgent/applyAgentModalOption 挂接交互会话。
- 待续：切换会话时保留上一线程的草稿输入与 pending 请求（Rust
  select_agents_overview_thread 输入状态保留链路的完整版）；仪表盘 ANSI 样式打磨。
