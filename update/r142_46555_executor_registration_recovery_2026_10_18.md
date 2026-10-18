# Round 657：executor registration 与 recovery Noise identity（#46555）

Rust 依据：`328feb0c298ffa093f4b37a6eb7761930caed7d7`。

## 已落地

- `execserver.Client` 记录已安装 Noise 会话的
  `executor_registration_id`，并通过 `CachedExecutorRegistrationID()` 在没有新连接
  的情况下暴露；非 Noise transport 和尚未安装会话时返回 absent。
- recovery 允许同一 executor 续期 registration：新连接安装时才发布新的
  registration ID，session 与运行中的 processes 保持不变。
- recovery 固定创建会话时的 executor Noise public key。registry lookup 返回不同
  key 时，新连接被关闭、registration 不发布，并返回
  `executor key changed during session recovery`。
- `clientConnectionOpener` 返回连接元数据，Noise rendezvous opener 从 connect
  bundle 中带上 executor key 与 registration ID；stdio/accepted/WebSocket opener
  返回空元数据。

## 已有能力

Rust 的 `active_turn_environment_selections` 在 Go 中由 app-server 的
`activeTurnParams` 深拷贝快照与 `mcpRuntimeEnvironmentSelections` 提供，已有
`TestActiveTurnEnvironmentSnapshotRemainsStableAcrossLaterUpdatesLikeRust` 和
`TestMCPRuntimeTracksActiveTurnEnvironmentSelectionsLikeRust` 覆盖 turn 启动后
设置变化不影响当前 turn。

## 未落地

Go 没有 Rust 的 `LazyRemoteExecServerClient` / environment connection refresh
路径，也没有 `Environment::cached_executor_registration_id()` 对应的刷新载体。
因此以下部分没有可靠的 Go landing site，不能仅凭符号存在标为 complete：

- refresh 同时匹配 registration 与 executor key；
- stale refresh lookup 重试且不得退休 recovery 已更新的连接。

## 验证

```text
go test ./execserver -run 'NoiseRendezvous|ExecutorRegistration|ChangedExecutorNoiseKey' -count=1
go test ./execserver -run 'RecoveryFetchesFreshBundle' -count=1
go vet ./execserver
```

`go test ./execserver -count=1` 仍在本机命中既有基线失败
`TestWindowsSandboxPowerShellFallbackWithoutCompatibleShellLikeRust`，与本轮改动无关。
