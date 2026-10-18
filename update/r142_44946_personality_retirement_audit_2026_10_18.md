# Round 662：Friendly/Pragmatic personality retirement audit（#44946）

Rust 依据：`132c73917181b88d9c6959e000c7f6e78efa4f51`。

## 审计结论

Go 已实现该提交的行为：

- `ModelInfo.SupportsPersonality()` 固定返回 false；generated presets 不再声明
  personality 支持。
- `ModelInstructions` 直接使用 literal instruction template，忽略遗留
  personality variables；`{{ personality }}` 只作为普通文本保留。
- 显式 `personality = "none"` 仍会剥离模板中的 `# Personality` section，且不再
  依赖已退休的 feature gate。
- app-server 不再生成 `<personality_spec>` developer/world-state item；已被
  退休的 friendly/pragmatic override 被忽略，`none` 在 thread start、turn 和
  resume/fork 路径保留。
- TUI `PersonalityOptOutOnly` 与 app request 构造仅保留 `none` opt-out。

## 验证

```text
go test ./model -run 'Personality|ModelInstructions' -count=1
go test ./appserver -run 'Personality' -count=1
go test ./app -run 'PersonalityOptOut' -count=1
go test ./tui/chatwidget -run 'Personality' -count=1
```
