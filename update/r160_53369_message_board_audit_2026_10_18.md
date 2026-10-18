# Round 715: agent message board unavailable audit

`#53369` removes the requirement that `disable_direct_message` sessions expose
the board's `post` tool. Go 已具备等价行为：

- `toolRouterForTurn` 不因 board 缺失而拒绝 turn；
- `post`、`send_message`、`followup_task` 不进入 advertised tools；
- 缺失 board 上的调用返回 model-visible unsupported/tool-not-found 错误；
- 不创建 agent board database。

验证：

- `go test ./appserver -run TestDisableDirectMessageAllowsUnavailableBoardLikeRust -count=1`
- `go test ./appserver -count=1`
