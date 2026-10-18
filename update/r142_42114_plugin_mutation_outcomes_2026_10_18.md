# `#42114` Centralize remote plugin mutations in `PluginsManager`

- Rust 依据：`ef76e6ac30990071b10c8ae7f2bcaf55dc447afc`。
- installed-plugin cache mutation gate 与 auth generation 从 app-server 移入
  `plugin` 包，按 Codex home 共享；reconciliation 的 `TryLock` 与安装/卸载
  的阻塞锁操作同一个状态。
- `PluginService.InstallWithOutcome` / `UninstallWithOutcome` 在 mutation
  gate 内完成 cache/config 变更，并返回：
  - staged `PluginDetail`；
  - `EffectivePluginsChanged`；
  - uninstall 的 `CacheRemovalError` 字段（Go 的移除失败仍按 error 返回）。
- app-server JSON-RPC install/uninstall、tool-driven install 使用 outcome
  驱动 analytics 与运行时刷新，不再自己持有 gate。
- Rust 的 `RemotePluginOperationErrorKind` 在 Go 中对应既有 sentinel error
  与 `errors.Is` 分类，保留 JSON-RPC invalid-request/internal 映射。

验证：

- `go test ./plugin -run 'WithOutcome' -count=1`
- `go test ./plugin -count=1`
- `go test ./appserver -run 'PluginInstall|PluginUninstall|PluginRemote|PluginReconcile|PluginAnalytics' -count=1`
