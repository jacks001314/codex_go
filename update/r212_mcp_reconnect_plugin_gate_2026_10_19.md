# MCP 重连、cloud 配置恢复与 executor 插件禁用审计

本轮核对并关闭 5 笔上游提交：

- `f5853cbe90` Reconnect MCP servers when authentication mode changes (#40634)
- `20c3f9733f` Restart pending MCP connections when startup timeout changes (#40636)
- `e44bd9b64d` Retry timed-out cloud config loads in the background (#40637)
- `5ce04789fb` Honor managed plugin disablement for executor capabilities (#40640)
- `da4cf1cdea` Preserve Windows proxy settings in Guardian sessions (#40641)

## 已有实现

- MCP 连接缓存键由 `mcpConnectionCacheKeyWithFeatures` 序列化
  `ServerConfig.Auth`，auth mode 改变会替换连接；对应
  `TestMCPConnectionCacheKeyIncludesAuthModeLikeRust`。
- HTTP/stdio 缓存仅在连接已初始化或 `StartupTimeout` 仍匹配时复用；pending
  启动在超时配置变化后重建，ready 连接保留。对应
  `TestMCPStdioConnectionReuseHonorsStartupTimeoutLikeRust`。
- Guardian turn 在 shell/patch 校验中设置
  `WindowsSandboxProxySettingsPreserve`，普通 turn 保持 reconcile；对应
  `TestGuardianTurnStartSelectsWindowsProxyPreserveMode`。

## 本轮修复

`#40640` 的 Go 侧存在一个可观察缺口：`plugins` 被托管 requirements 关闭时，
selected executor root 仍会进入技能提示元数据。修复如下：

- `runtimeStepCapabilitySnapshot` 记录本 step 生效配置的 `plugins` 开关；
- `selectedCapabilitySkillEntriesForRuntimeSnapshotWithSandbox` 在该开关关闭时
  不返回 executor selected-root skills；
- selected root 本身仍保留在 thread metadata 中，供环境选择、过滤和 resume
  使用；
- `executorSkillProviderForThread` 原有的 feature gate 继续保证技能工具不暴露；
- Go 不从 selected root 投影 MCP server、apps 或 connectors，因此这些能力没有
  额外的泄漏路径。

新增 `appserver/executor_plugins_feature_like_rust_test.go`，覆盖 plugins
启用/禁用两种 requirements、root identity 保留、技能目录、provider 安装和
模型指令可见性。

`#40637` 的 Go loader 没有 Rust 的“缓存 timeout 错误并等待后台刷新”窗口：
`CloudConfigLoader.Get` 每次配置加载都会重新调用 loader，因此下一次加载即可
恢复；最后成功 bundle 在其他刷新失败时仍被保留。新增
`TestCloudConfigLoaderRetriesTimeoutOnNextLoadLikeRust` 固定该行为。

## 验证

- `go test ./mcp -run 'ConnectionReuse|CacheKeyIncludesAuth|StartupTimeout' -count=1`
- `go test ./config -run 'CloudConfigLoader' -count=1`
- `go test ./appserver -run 'ManagedPluginsRequirement|SelectedExecutor|ExecutorSkill|GuardianTurnStartSelectsWindowsProxy' -count=1`
