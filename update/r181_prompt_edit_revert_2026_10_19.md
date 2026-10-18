# Round 738: prompt edit reverts the current thread

Rust 依据：`ffae979216bfbe94070bd21868d1695277105a63`（`#45845`）。

Go 的 backtrack prompt edit 之前通过 `thread/fork` 创建新线程；本轮改为：

- 解析选中的 user prompt ordinal 到 `beforeTurnId`；
- 通过 `thread/revert` 原地截断当前线程历史，不再 fork 或新建线程；
- 重新读取保留的 turns 并恢复选中 prompt、文本元素与图片附件；
- 成功后在 transcript 写入
  `Conversation reverted to this point. File changes are unchanged.`；
- steer、in-progress turn、失败路径仍保留原 session 和 composer 草稿；
- remote 与 embedded 两条 wiring 共用同一 revert 客户端接口。

验证：

- `go test ./tui/app -run PromptEdit -count=1`
- `go test ./tui/tea -run Backtrack -count=1`
- `go test ./app -run 'TestInteractiveRemotePromptEdit' -count=1`
- `go vet ./tui/app ./tui/tea ./app`
