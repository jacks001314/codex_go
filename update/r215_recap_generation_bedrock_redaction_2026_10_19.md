# Recap 生成、Bedrock 密钥脱敏与 Instructions 重命名

本轮关闭：

- `40ba7da7b4` Prepare automatic TUI recap generation (#40697)
- `6988d390b3` Generate automatic and manual conversation recaps (#40705)
- `13fe2bcb7a` Redact Bedrock API keys from debug output (#40706)
- `5ca4175295` Rename host instruction payload to `Instructions` (#40709)

## 结论

- `tui/tea/recap.go` 与 `app/recap.go` 已实现自动/手动 recap 的完整链路：
  focus 触发、turn 进度、stale-result 拒绝、单次重试、输出上限、临时结构化
  thread、有效配置读取、MCP/tool/skill 隔离和 read-only sandbox。
- `auth.BedrockAPIKeyAuth` 通过 `fmt.Formatter` 在所有格式化动词下脱敏 API key。
- Go 的 host instructions 载体本来就是 `config.Instructions` +
  `LoadedUserInstructions.Instructions`，没有旧 `UserInstructions` payload 类型
  需要重命名，加载行为不变。

## 验证

- `go test ./tui/tea -run 'Recap' -count=1`
- `go test ./app -run 'Recap' -count=1`
- `go test ./auth -run 'Bedrock' -count=1`
- `go test ./config -run 'Instructions' -count=1`
