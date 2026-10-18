# live turn 设置、遥测回灌、tmpdir 与退出摘要

本轮关闭 5 笔上游提交：

- `a10c8127f7` Add live turn settings updates (#40653)
- `2f4b105514` Prevent telemetry exporters from feeding back into themselves (#40658)
- `ba9567daf3` Propagate temporary directories into sandbox policy contexts (#40660)
- `afb797cae6` Enable content item kinds by default (#40669)
- `731d969d03` Clarify session exit messages (#40670)

## 已有实现

- `turn/settings/update` 已支持对运行中 turn 稀疏更新 model、effort、summary、
  service tier、approvals reviewer 和 environments；后续 step 通过
  snapshot/provider 读取新值，future thread settings 不受影响；托管
  model/approval/Guardian 约束在发布前重新校验。
- `features.Registry` 已把 `content_item_kinds` 标为 stable 且默认启用。

## 本轮修复

### #40658

`state.persistSlogRecord` 增加 SQLx 目标前缀过滤：

- `sqlx::*`、`sqlx_core::*`、`sqlx_sqlite::*` 全部丢弃；
- `sqlx_application` 等相似但无关目标保留；
- Go trace 导出只发送显式安全事件，不自动导出 h2 transport span。

### #40660

- `execserver.FileSystemSandboxContext` 增加 `temporaryDirectories`，参与
  URI 校验与规范化；
- `EnvironmentInfoResponse` 保留 executor 上报的临时目录；
- executor skill sandbox context 为 local 与已缓存 remote 环境携带对应目录；
- `:tmpdir` 的实际策略解析仍在拥有该环境的 execserver 进程内执行，使用其
  自身 `TMPDIR`/`TEMP`。

### #40670

- 新增 `ExitReasonArchived` 与 `AppExitInfo.ArchivedThreadID`；
- 成功归档输出 `Session archived: <thread-id>`；
- 没有 resume hint 时输出 `Session ID: <thread-id>`；
- deleted thread 仍保持独立的 `thread_removed` 语义。

## 验证

- `go test ./state -run 'LogDBHandler' -count=1`
- `go test ./tui/app -run 'ExitReason|AppExitInfo|DescribeExitReason' -count=1`
- `go test ./execserver -run 'FilesystemWireSandboxPaths|NormalizeFSSandbox|LocalEnvironmentInfoReportsTemporaryDirectories' -count=1`
- `go test ./tool -run 'FileSystemSandboxContext' -count=1`
- `go test ./appserver -run 'ManagerInfoFetchesRemoteExecServer|ExecutorSkillSandboxContexts|TurnSettingsUpdate|TurnStepSettings' -count=1`
- `go test ./turn -run 'TurnSettings' -count=1`
