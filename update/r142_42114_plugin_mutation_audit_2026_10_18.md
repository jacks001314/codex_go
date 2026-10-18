# Round 668：remote plugin mutation centralization audit（#42114）

Rust 依据：`ef76e6ac30990071b10c8ae7f2bcaf55dc447afc`。

## 已具备

- Go 的后端 install/uninstall orchestration 已位于
  `plugin.PluginService.Install` / `Uninstall`。
- app-server 的 `handlePluginInstall` / `handlePluginUninstall` 不再直接执行后端
  变更，只负责：
  - 获取 remote-installed cache mutation gate；
  - 调用共享 `PluginService`；
  - invalidate plugin/skill/MCP/hook derived caches；
  - emit install/uninstall analytics。

## 剩余

Rust 把 mutation gate 与结构化 `RemotePluginInstallOutcome`、
`RemotePluginUninstallOutcome`、`RemotePluginOperationError(Kind)` 一并移入
PluginsManager。Go 的 mutation gate 与 telemetry/refresh 仍在 app-server，
`PluginService` 返回既有 response/error 类型，没有对应的 typed remote mutation
outcome API。因此架构目标只完成了一半。

## 验证

```text
go test ./plugin -run 'Install|Uninstall' -count=1
go test ./appserver -run 'PluginInstall|PluginUninstall|PluginInstallRuntime' -count=1
```
