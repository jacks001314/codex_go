# 冻结 MCP plugin root attribution（#41117）

`ServerRegistration.PluginHostRoot` 已从 host 发现的 plugin registration
冻结，并进入 `PluginAttribution` 和 MCP 连接缓存键；同一 plugin ID 的不同
root 不会复用旧连接。Rust 的 `with_host_root` builder 在 Go 中对应这个保留
字段。

验证：

- `go test ./mcp -run 'FreezesPluginHostRoot|PluginAttribution' -count=1`
