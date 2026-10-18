# Round 661：model-specific indirect tool prefixes（#47642）

Rust 依据：`8c66a5a243d11339b56c6683b60aa5273c7ecb22`。

## 落地

- `model.ToolMessages.indirect_description_prefixes` 解析
  `namespaces` 与 `mcp_servers` 两组 selector；`cloneModelInfo` 深拷贝两个
  map。
- `tool.IndirectNamespacePrefixes` 将已注册 MCP server 展开为 callable
  namespace，统一 trim，空值不添加前缀；同一最终 namespace 被两个 selector
  赋不同值（包括空值）时返回 `IndirectNamespacePrefixConflict`，在模型请求前
  失败。
- `BuildToolRegistry` 将前缀应用到 tool search 的 deferred spec 副本，并把
  解析结果交给 Code Mode runtime；直接注册的 tool spec 保持不变。
- Code Mode runtime 在 `ALL_TOOLS` 与 nested tool description 中应用前缀；
  Code ModeOnly exec prompt 同时渲染 `mcp__*` namespace 文档与默认
  `functions` namespace 前缀。
- 每个 turn 重新设置前缀，因此模型切换会清除旧前缀并应用新模型 guidance。

## 验证

```text
go test ./tool -run 'Indirect|MCPServerNamespaces' -count=1
go test ./turn -run 'IndirectDescriptionPrefixes' -count=1
go test ./codemode -count=1
go test ./model -run 'IndirectDescriptionPrefixes' -count=1
go test ./appserver -run 'CodeMode|ToolSearch|ToolRegistry' -count=1
go vet ./tool ./turn ./codemode ./appserver
```

`go test ./tool -count=1` 仍命中既有 login-shell path 基线失败；
`go vet ./model` 仍有既存 `responses_agent.go` mutex-copy 警告，本轮未改动该文件。
