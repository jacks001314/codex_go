# Turn environment sandbox、Bridge schema 与 persistent reasoning

本轮关闭：

- `e24190caa9` Use turn environment settings for sandbox execution (#40771)
- `25a6e316c8` Make history and notes tools Bridge-compatible (#40775)
- `3e4707b34b` Support persistent reasoning effort (#40799)

## 结论

- Go 的 appserver 已按选中的 turn environment 解析权限 profile、workspace
  roots 和 `FileSystemSandboxContext`，而不是只使用 turn 级配置；
  `turnEnvironmentSelectionPermissionProfileResolution` 与
  `turnEnvironmentWorkspaceRoots` 是这条链路的入口。
- `historynotes` 的 schema 已使用 `anyOf` 表达 nullable 参数，移除
  `additionalProperties` 与客户端 maximum/maxLength 限制；
  `ValidateArguments` 对 JSON object 直接放行，非 object 仍由工具解码器拒绝。
- `model` 接受 `persistent` 本地值，并在 Responses wire 上映射为 `disabled`；
  TUI 显示 `Persistent`。

## 验证

- `go test ./appserver -run 'TurnPermission|Environment.*Sandbox|ExecutorSkillSandboxContextsPreserve' -count=1`
- `go test ./historynotes -count=1`
- `go test ./appserver -run 'HistoryNotes|TokenBudget' -count=1`
- `go test ./model -run 'ReasoningEffort|Persistent' -count=1`
