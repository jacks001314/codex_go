# Standalone tool outputs、retained image budget、skill aliases、gRPC trace

本轮关闭：

- `b9c4b9a0cf` Support standalone function outputs in turn routing (#40991)
- `528fd7ace5` Enable retained-image budgeting by default (#40994)
- `7c3747941a` Reduce skill catalog prompts with path aliases (#41011)
- `eed1dee69e` Propagate trace context through gRPC code mode (#41017)

## 结论

- `turn.TurnToolOutput` 已支持无用户 prompt 的 standalone function-call output，
  覆盖 start/steer、Retain 标记和输入大小校验。
- `compaction_image_budget` 已 stable/default-on，retained images 计入远程
  compaction 上下文预算并按需裁剪旧图。
- skill catalog 在预算充足时也评估 alias，并在包含数、描述保留和总成本更优时
  选择 alias；host/executor/orchestrator 合并目录共用该选择。
- gRPC code mode 携带 W3C traceparent/tracestate，remote provider 从 context
  注入 trace。

## 验证

- `go test ./turn -run 'ToolOutput|Standalone' -count=1`
- `go test ./appserver -run 'StandaloneToolOutput|TurnToolOutput|Image.*Budget|ImageReference' -count=1`
- `go test ./prompt -run 'Skill|Alias' -count=1`
- `go test ./codemode -run 'Trace' -count=1`
