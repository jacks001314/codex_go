# Round 659：工具结果使用所属 step 的模型（#44243）

Rust 依据：`b64de2f3ad718ebee731aa8a5e1d4ace0594efc5`。

## 差异

Rust 在记录 conversation history 时显式携带 producing model 的 `ModelInfo`，
使一次 turn 内切换模型后，模型响应和工具结果按所属 step 的图片准备与输出截断
规则记录，而不是继续使用 turn 起始模型。

Go 的 agent loop 已经为每个 sampling step 解析 `stepModel`，但
`ToolDispatcher` 的输出截断策略在创建时固定为 `request.Model`（turn 起始模型）。
因此同一 turn 内切换模型后，工具结果仍可能按旧模型的 truncation policy 截断。

## 落地

- `ToolDispatcherOptions.TruncationForModel` 允许按模型解析输出截断策略。
- agent loop 在每个 step 设置刷新后调用 `SetStepModel`，在工具执行前把
  dispatcher 的策略更新为当前 producing step 的模型。
- 既有静态 `Truncation` 仍保留给直接构造 dispatcher 的调用方。
- 新增 `TestAgentLoopUsesStepModelForToolTruncationLikeRust`：两个 step 分别使用
  `gpt-initial` 与 `gpt-updated`，工具实际观察到 `11` 和 `22` 两个不同预算。

## 剩余

Go 通过 app-server 的 session item 切片记录历史，没有 Rust 的显式
`model_info` 参数贯穿所有记录 API；图片准备/历史记录的完整 step 参数化仍是
结构性差异。本轮修复的是可观察的 stale truncation policy 路径。

## 验证

```text
go test ./turn -run 'StepModelForToolTruncation|RefreshesStepSettings|BuildsStepInputItems' -count=1
go vet ./turn
```
