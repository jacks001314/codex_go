# Round 656：远端插件同步的受影响能力提示（#41193）

Rust 依据：`56239d7a618896a37380c87002165ae1cc214e39`。

## 差异

Rust 的远端 bundle 同步在替换或删除缓存前读取旧 bundle 的声明，并把
`RemotePluginCapabilities` 的 MCP、Apps、Hooks、Skills 提示附加到
materialization/removal 结果。更新时旧、新 bundle 的声明取并集，避免新版本
移除了某类能力后，宿主的运行时失效逻辑漏掉旧版本曾暴露的能力。

Go 已有 `plugin.PluginReconcileChangedPlugin` 的四类 wire 字段，也已在
`plugin/reconcile` 中比较同步前后的 `PluginDetail`；但更新场景此前只使用同步后
的 detail，导致旧 bundle 独有的能力提示丢失。Go 没有独立的
`RemotePluginCapabilities` 同步结果类型，能力提示在 app-server reconcile
边界由 `PluginDetail` 派生，因此本次对齐落在该边界。

## 落地

- `appserver/runtime_router.go` 新增
  `pluginReconcileChangedCapabilities`：对同步前后都存在的插件合并两侧
  `MCPServers`、`Apps`/`AppTemplates`、`Hooks`、`Summary.HasSkills`。
- 删除的插件继续使用同步前（被删 bundle）的声明；新增插件只使用当前 bundle。
- `appserver/plugin_reconcile_test.go` 覆盖替换取并集、删除保留旧声明、新增只读
  当前 bundle 三个场景。

## 验证

```text
go test ./appserver -run 'PluginReconcile' -count=1
go test ./plugin -count=1
go vet ./appserver
```

未声称全仓测试通过；`model`、TUI 等目录仍包含其他 agent 的并行改动。
