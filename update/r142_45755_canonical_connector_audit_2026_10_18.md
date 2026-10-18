# `#45755` canonical connector disable audit

- Rust 依据：`af1fc2dbff641e78298c272c4b45c9fec5c33898`。
- Go `PluginService` 的 remote installed-plugin 缓存已保留
  `CanonicalAppID` 与 `CanonicalAppAuthIdentity`，与 Rust manager 的内存
  cache 对应。
- `CanonicalAppIDsForDisabledPlugins` 按 plugin id 或 name+marketplace 匹配
  canonical owner；auth identity 不匹配的元数据被忽略；owner bundle 无需
  本机存在。
- `filterCanonicalDisabledConnectors` 同时过滤 AppConnectors、Apps 与
  canonical/materialized template IDs，并在 step snapshot 与 immediate
  thread capability 两条路径调用。

验证：

- `go test ./plugin -run 'CanonicalApp' -count=1`
- `go test ./appserver -run 'CanonicalDisabled|DisabledPluginCapabilities|RemoteInstalledPluginDisplayRefresh' -count=1`
- `go vet ./plugin ./appserver`
