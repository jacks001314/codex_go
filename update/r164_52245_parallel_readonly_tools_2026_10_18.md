# `#52245` Enable parallel execution for read-only tools

Rust 依据：`c9e0fffbbc05c36478686444f6ab1edbadfe2768`。

Go 中有载体的只读工具已改为 `Parallel: true`：

- `skills.list` / `skills.read`
- `get_context_remaining`
- `list_available_plugins_to_install`
- multi-agent v2 `list_agents`

仍保持串行：goal 创建/更新、`request_user_input`、plan 与其他 mutation 工具。
Rust 的 memory/current_time/get_goal/user_message history 工具在 Go 中尚无对应
实现，因此其 parallel flag 无载体。

验证：

- `go test ./turn -run 'Parallel|Skills' -count=1`
- `go test ./agent -run 'MultiAgentV2|ListAgents|Parallel' -count=1`
- `go test ./tool -run 'Parallel|PluginInstall|ContextRemaining' -count=1`
