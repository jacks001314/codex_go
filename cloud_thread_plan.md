# #57267 云托管会话（cloud thread）落地计划

范围：codex_go TUI 对齐 Rust `ThreadParamsMode::Cloud` 与
`ThreadSettingsOwner::Server`。Rust 只读参考为
`codex-rs/tui/src/app_server_session.rs`（下称 `session.rs`），Go 侧以
`app/remote_tui.go`、`app/remote_tui_managed_defaults.go`、
`app/interactive.go`、`app/model_selection.go`、`tui/tea` 为主。

结论先行：Go 缺的不是一个“服务器持有”标记，而是 **cloud app-server 传输**
以及把服务器所有权贯穿到所有“客户端注入设置”的位置。只补
`ThreadSettingsOwner` 而不改注入点，产生的确实是死代码。因此本计划先给出
完整路径，再落地一个能自洽、可测试的最小切片。

## 1. Rust 精确行为（逐条核对）

| 行为 | Rust 位置 | 语义 |
| --- | --- | --- |
| `ThreadParamsMode { Embedded, Remote, Cloud }` | `session.rs:360-366` | 传输决定模式；Cloud 注释：原生托管会话拥有自己的执行设置与工作区 |
| `ThreadSettingsOwner { Client, Server }` | `session.rs:368-375` | Client 允许请求覆盖；Server 只在专用 API 中修改 |
| `settings_owner()` | `session.rs:388-394` | Embedded/Remote → Client，Cloud → Server |
| `workspace_roots_from_config` | `session.rs:396-402` | Embedded 才把本地 `workspace_roots` 发出去，Remote/Cloud 由服务器解析 |
| `uses_remote_workspace()` | `session.rs:692` | Remote/Cloud 为真 |
| `start_dynamic_tool_mcp` | `session.rs:557-565` | `mode == Cloud` 直接 `return Ok(())`，不启动本地动态工具 MCP / worktree 工具 |
| bootstrap `configRequirements/read` | `session.rs:815-837` | 仅 Cloud 且错误码 `-32601` 时返回 `Ok(None)`，其余错误照常上抛 |
| `thread/start` 参数 | `session.rs:2348-2358` | Server 所有权直接返回 `ThreadStartParams::default()`：不发送 model/effort/cwd/sandbox/config/工具传输 |
| `thread/start` 启动选项 | `session.rs:1017-1054` | Cloud 带 permission profile 直接 bail；`model_provider/daybreak/thread tool transport` 只在 Client 所有权下写入 |
| `thread/resume` 参数 | `session.rs:2402-2417` | Server 所有权只发 `threadId` |
| `turn/start` 参数 | `session.rs:1610-1630` | 只有 Client 所有权才带 cwd/roots/approval/sandbox/permissions/model |
| fork | `session.rs:1195-1197` | `mode == Cloud` 直接 bail：`Cloud conversations cannot be forked in the TUI yet.` |
| `configRequirements` 的权限推导 | `permission_discovery.rs:92-98` | Server 所有权时权限列表直接报 `Cloud conversations use server-selected permissions.` |
| recap / 标题 / 模型默认值 | `app/recap.rs:190`、`app/thread_title.rs:71`、`app/model_defaults.rs:128` | Server 所有权时跳过本地生成线程；模型选择失败时提示云会话不支持 |
| 新会话/总览 cwd | `app/new_session.rs:68-74`、`app/agents_overview.rs:1006-1013` | Embedded 用本地 cwd，Remote/Cloud 用 `remote_cwd_override` 或 `.` |

补充事实：在本快照里 Rust 的 `AppServerClient::Cloud` 只出现在测试中
（`app_server_session.rs:2883/2901/3049/...`），`AppServerTarget::thread_params_mode`
只返回 Embedded/Remote（`lib.rs:400-406`）。也就是说 **Rust 生产路径同样还没有
cloud 传输**；Cloud 模式目前是一套由传输层注入的、已被测试锁定的不变式。

## 2. Go 现状与差距（文件级）

- `app/remote_tui.go:5571 remoteThreadStartParams`：无条件回填
  `CWD/Model/ApprovalPolicy/Sandbox/Config/DynamicTools/ThreadSource`，没有模式分支。
- `app/remote_tui.go:3622 startThread`：`applyTaskToolTransport` 与动态工具
  namespace 无条件启用；`app/remote_tui_managed_defaults.go:21` 还会叠加
  server 的 managed new-thread 默认值。
- `app/remote_tui.go:1723 interactiveRemoteSessionActionHandler`（UI fork）与
  `app/remote_tui.go:1335 interactiveRemoteStartSide` →
  `remoteSideThreadForkParams`（`app/remote_tui.go:5603`）：无 cloud 拒绝。
- `app/remote_tui.go:5655 remoteTurnStartParams`：无条件回填
  cwd/model/sandbox/config；Cloud 下这些属于服务器。
- `app/remote_tui.go:569-570`：`OnGenerateRecap/OnSuggestThreadName` 无条件接入
  `interactiveRemoteRecapGenerateHandler`；`app/interactive.go:1433-1434` 同理。
- `app/interactive.go:2477 interactiveLoadSettings` / `:2509 interactiveServiceTierRequirements`：
  本地读取 `configRequirements`，没有 Cloud 的 `-32601` 容忍。
- `app/daemon_compatibility.go:225-260`、`app/remote_tui_permissions.go:170-210`：
  已有“方法不存在就降级”的先例，可复用其错误判定写法。
- `app/model_selection.go:113`：模型选择同时发 `thread/settings/update` 并写本地
  `config/batchWrite`；Cloud 下只能走服务器 API，不能改本地配置。
- `appserver/protocol.go` 已有 `MethodThreadStart/Fork/Resume`、
  `MethodThreadSettingsUpdate`、`MethodConfigRequirementsRead` 与
  `JSONRPCInvalidRequestErrorCode(-32601)`；`appserverdaemon.RemoteAppServerEndpoint`
  只有 webSocket/unixSocket，没有 cloud 传输。
- `app/cloud.go runCloud` 是 ChatGPT 任务 API（list/exec/status），与会话无关，
  不能当作 cloud app-server。

## 3. 目标设计

### 3.1 模式与所有权（纯逻辑层）

新增 `app/cloud_thread_mode.go`：

- `ThreadParamsMode`（Embedded/Remote/Cloud）与 `ThreadSettingsOwner`（Client/Server）。
- `SettingsOwner()`、`UsesRemoteWorkspace()` 与 Rust 逐项一致。
- `appServerTransportKind` + `threadParamsModeForTransport`：**模式由传输决定**，
  而不是由 `--remote`、URL 主机名或环境变量猜测。
- `cloudThreadForkUnsupportedMessage` 与 `cloudThreadForkError(mode)`。

### 3.2 传输层（当前缺口）

Cloud 会话需要一个等价于 Rust `AppServerClient::Cloud(CloudClient)` 的传输：
进程内 channel/长连接，对上仍是 app-server JSON-RPC 方法集。Go 侧建议新增
`appserverdaemon` 的 cloud endpoint 种类或在 `app` 内引入
`cloudAppServerTransport`，由它构造 `remoteAppServerTUIClient{threadParamsMode:
ThreadParamsModeCloud}`。在它存在之前，生产路径不会进入 Cloud 分支，这是
**有意为之**，也是本计划“先类型后传输”的前提。

### 3.3 数据流

launch → 传输选择 → `threadParamsMode` → ①thread/start 参数 ②动态工具/工作区初始化
→ ③turn/start 参数 ④fork 允许性 ⑤recap/标题/模型默认值 ⑥权限发现
→ `thread/settings/update`（唯一设置写入口）→ `threadSettingsUpdated` 通知回填 UI。

## 4. 文件级改动点（分期）

### P0：本轮最小切片

1. `app/cloud_thread_mode.go`（新增）：模式/所有权/传输映射/fork 错误常量与判定。
2. `app/remote_tui.go`
   - `remoteThreadStartParamsForMode(mode, root, state)`：Server 所有权返回零值
     `appserver.ThreadStartParams{}`（对齐 `ThreadStartParams::default()`）；
     Client 所有权保持现有逻辑（行为不变）；`remoteThreadStartParams` 保留为
     Embedded/Remote 包装，避免影响既有调用点与测试。
   - `remoteAppServerTUIClient.threadParamsMode` 字段，构造时按传输写入；
     `startThread` 在 Server 所有权下不挂 task-tools MCP、不注入 DynamicTools。
   - `remoteManagedThreadStartParams` 在 Server 所有权下跳过 managed 默认值叠加。
   - `remoteSideThreadForkParamsForMode` 与 UI fork 分支接入
     `cloudThreadForkError`。
3. `app/cloud_thread_mode_like_rust_test.go`（新增）：模式/所有权/参数/拒绝。

### P1：传输与生命周期（未完成）

4. `appserverdaemon` + `app`：cloud app-server 传输（连接、initialize、事件流、
   断线重连），仅 cloud 传输可置 `threadParamsMode = Cloud`。
5. `app/remote_tui.go:5655 remoteTurnStartParams`：Server 所有权只发
   `threadId/input/clientUserMessageId/turnTrigger/additionalContext`。
6. `app/remote_tui.go:569-570`、`app/interactive.go`：Cloud 时 recap/标题生成
   返回服务器不可用提示（Rust `CLOUD_RECAP_UNAVAILABLE_MESSAGE`、
   `This cloud conversation does not support changing its model from the TUI.`）。
7. `app/interactive.go:2477`/`app/remote_tui.go:614/1200`：Cloud 的
   `configRequirements/read` `-32601` 容忍（复用 `remoteTUIPermissionDiscoveryUnsupported`
   的错误判定），非 Cloud 不得吞错。
8. `app/model_selection.go`、`app/remote_tui_permissions.go`：Cloud 的模型/权限
   变更只走 `thread/settings/update`，不写本地 config，也不列本地权限 profile。

### P2：工作区与总览（未完成）

9. 跳过本地 workspace/worktree/tool 初始化：`app/remote_tui.go` 的
   `applyTaskToolTransport`、`managed_worktree_tools.go`、
   `interactiveRemoteSessionPickerItems` 相关路径。
10. `app/remote_tui_managed_defaults.go`：Cloud 不读/不叠加
    managed new-thread 默认值（Rust 只把它用于 Client 所有权）。
11. `app/agents_overview_tui.go`、`app/agents_dashboard.go`：Cloud 会话的
    cwd/权限列使用服务器返回值，不回落本地 `--cd` 或本地配置文件。

## 5. 风险

- **不可达分支**：无 cloud 传输时 Cloud 分支只能被测试触达；必须用
  `*LikeRust` 测试锁语义，避免“写了但没人验证”。
- **半套所有权比没有更糟**：只改 thread/start、不改 turn/start 与设置写入，
  会造成“首帧服务器设置、后续回合客户端覆盖”的漂移。
- **本地副作用**：recap/标题/模型默认值/权限 profile 都会在本地生成线程或写
  config；Cloud 下必须整体跳过，否则本地配置被云端会话污染。
- **错误吞噬**：`-32601` 容忍必须严格绑定 Cloud 模式与 `configRequirements/read`，
  否则会把真实的方法缺失问题掩盖到其它模式。
- **其它线程创建路径**：fork 之外还有 side conversation、compact、review、
  goal continuation；它们都构造 thread/start 或 thread/fork，需统一走模式判定。
- **测试基线**：快照测试会内嵌 worktree 路径（`codex_go_lane_cloud`），
  `go test ./tui/...` 中此类失败可接受，但需显式说明。

## 6. 验收与测试计划

- P0：`go build ./...`；`go test ./app/... -run 'CloudThread' -count=1`；
  全量 `go test ./app/... ./appserver/... -count=1`。
- P1：增加 fake cloud transport 的端到端用例：thread/start 空参数、
  turn/start 空设置、`-32601` 容忍、设置更新只走服务器。
- P2：工作区初始化断言（不调用本地 MCP/checkout）与总览列回填断言。

## 7. 本轮未完成清单

- 没有 cloud app-server 传输，因此生产路径无法真正进入 Cloud 模式。
- `turn/start`、`configRequirements` 容忍、recap/标题、模型/权限写入、
  本地工作区与工具初始化、agents overview 的服务器所有权改造均未落地。
- 没有端到端 cloud 会话测试；只有模式映射、thread/start 参数与 fork 拒绝的
  单元测试。
