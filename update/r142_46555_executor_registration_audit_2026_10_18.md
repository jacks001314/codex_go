# `#46555` executor registration audit

- Rust 依据：`328feb0c298ffa093f4b37a6eb7761930caed7d7`。
- `execserver.Client` 在已安装 Noise session 上记录 registration；
  `CachedExecutorRegistrationID` 不连接、不刷新。
- recovery 保留原 executor Noise key；registration renewal 允许，但 key
  改变会关闭新连接并拒绝安装。新 registration 只随新 transport 一起发布，
  client 已关闭/retired 时不会安装。
- Go 没有独立的 `LazyRemoteExecServerClient` registry-refresh lookup；
  registration+key 匹配不变量在 connection installation 处强制。

验证：

- `go test ./execserver -run 'NoiseRendezvous|ExecutorRegistration|ChangedExecutorNoiseKey' -count=1`
- `go vet ./execserver`
