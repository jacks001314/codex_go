# Round 663：canonical plugin connector ownership（#45755）

Rust 依据：`af1fc2dbff641e78298c272c4b45c9fec5c33898`。

## 落地

- `remoteInstalledPlugin` 解析 `canonical_app_id`，并存入
  `PluginDetail.CanonicalAppID`。
- canonical ownership 绑定当前 remote-installed 认证身份；账号或认证模式变化后，
  旧 metadata 不参与排除判断。
- `PluginService.CanonicalAppIDsForDisabledPlugins` 按
  `name@marketplace` 匹配 disabled owner，返回其 canonical connector IDs。
- `filterCanonicalDisabledConnectors` 从剩余 enabled capabilities 的
  `AppConnectors`、`Apps` 与 `AppTemplates` 中移除 canonical connector，覆盖
  “另一个 enabled plugin 也贡献同一 connector”的场景。
- step capability snapshot 与即时 `pluginCapabilitiesForThread` 路径都会应用
  该过滤。

## 剩余

Go 没有 Rust 的持久化 remote-installed plugin metadata cache；canonical
ownership 在 app-server 的 installed-plugin sync `PluginService` 状态中。
启动同步完成前，或 backend 已标记 disabled 因而被 Go 跳过的条目，canonical
排除可能暂时不可用。该窗口不影响已同步缓存中的 canonical owner。

## 验证

```text
go test ./plugin -run 'CanonicalApp' -count=1
go test ./appserver -run 'CanonicalDisabled|DisabledPluginCapabilities|RemoteInstalledPluginDisplayRefresh' -count=1
go test ./plugin -count=1
go test ./appserver -run 'DisabledPlugin|Canonical|PluginRemote|RemoteInstalledPlugin' -count=1
go vet ./plugin ./appserver
```
