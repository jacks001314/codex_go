# `#43848` Runtime workspace roots across thread resume

Rust 依据：`6515a72db7a82e8cdebed940aad4ba1a159ce245`
（`Preserve runtime workspace roots across thread resume`）。

## 落地内容

- `rollout.SessionMeta` 增加顶层 `runtime_workspace_roots`，与 Rust 的
  SessionMeta 字段一致；读取 Rust 生成的 rollout 时回填到 Go 的
  `record.Metadata.Extra`，写入时不再只在嵌套 `extra` 对象里保存。旧的嵌套
  spelling 继续兼容读取。
- `thread_settings_applied` 快照携带 `thread_settings.runtime_workspace_roots`：
  - 非 nil 指针保留显式空列表 `[]`；
  - 缺失字段表示未知，不会被当成空选择；
  - `rollout.LatestPersistedOwnedThreadSettings` 只接受 owner 等于当前线程的
    最新快照。
- resume 的 roots 选择顺序与 Rust 对齐：
  1. 显式 `runtimeWorkspaceRoots`（含显式空列表）优先；
  2. 存在本线程 settings snapshot 时，只使用快照值；快照缺失 roots 时不回退到
     更早的启动元数据；
  3. 仅当线程完全没有 owned snapshot 时，才回退到启动元数据；
  4. 两者都没有时按有效 cwd 生成单根。
- 恢复前先按 saved cwd → resume cwd 做 `replace_path_and_deduplicate`，再对每个
  根执行 `utils.FromHostNativePath` 当前主机校验。跨 OS 路径返回
  `invalid_params`（-32602）并提示显式传入本地路径或空列表。
- resume 的 effective settings 会写入新的 settings checkpoint；显式覆盖即使
  没有运行 turn，也会在下一次 resume 恢复。turn/start、`thread/settings/update`
  和 revert 后的设置也写 checkpoint。cwd-only 更新会 retarget 已保存的 cwd
  root；显式 roots 更新同时写 record metadata 与 settings snapshot。
- revert 后追加 restored settings checkpoint，替换被回退后缀里可能已失效的
  resume checkpoint。

## 无载体部分

- Rust 在加载期间发现 roots 变化后重载 resume configuration；Go 在 dispatch
  内同步计算 roots 和 resume config，不存在加载期并发快照，故无独立载体。
- Rust 在 thread search 中用 `normalize_windows_device_path` 归一 rollout join
  key；Go 的 `thread/search` 直接遍历 store records，没有按 rollout path
  建 map 的 join 步骤，故无对应 key 可归一。

## 验证

- `go test ./rollout -count=1`
- `go test ./appserver -run 'ThreadResume|Resume|ThreadSettings|ThreadRevert|PersistThreadSettings|TurnStart|RuntimeWorkspaceRoots|Fork' -count=1`
- `go build ./...`
- `go vet ./appserver ./rollout`

新增回归：

- `TestSessionMetaRuntimeWorkspaceRootsRoundTripLikeRust`
- `TestLatestPersistedOwnedThreadSettingsLikeRust`
- `TestThreadResumeRestoredRuntimeWorkspaceRootsValidateHostLikeRust`
- `TestThreadResumePrefersOwnedSettingsSnapshotRootsLikeRust`
- `TestThreadResumeWithoutSavedRootsDoesNotValidateDefaultCWDLikeRust`

整包 `go test ./appserver -count=1` 仅剩既有 project-trust 基线失败
（`TestRuntimeRouterThreadStartProjectlessDoesNotPersistProjectTrustLikeRust`、
`TestRuntimeRouterThreadStartProjectTrustWriteGuards`），以及本轮拉取的新上游
`#54949` 引入的 v2 suite manifest 漂移
（`connection_handling_websocket_compression`），与本提交无关，留待下一轮同步。
