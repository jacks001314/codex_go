# Round 709: Guardian conversation-history tools audit

`#49036` Add opt-in conversation history retrieval to Guardian reviews
（`41ed72c32b`）依赖以下 Rust 能力：

- `user_message.search_messages` / `read_messages` Apps MCP 工具；
- Guardian reviewer 会话中的工具调用循环与 parent Apps 连接/身份绑定；
- 每次调用重新检查 app/tool policy，拒绝 disabled 或需要 approval 的调用；
- 4,000 token 默认输出预算、可覆盖 retrieval prompt、隔离 session 的显式
  extension registry。

Go 侧现状：

- `guardian_conversation_history_tools` feature 已注册；
- 普通 turn 有 Apps MCP tool executor，但 `guardianSessionRunner.Run` 是单次
  model call，不执行 function_call，也没有 parent Apps MCP 连接绑定；
- 没有 `user_message` 工具定义、历史检索指令、output budget 或 isolated
  session extension registry。

因此该提交转为 `blocked_precondition`，而不是伪造一个不可执行的历史检索
入口。前置工作是先给 Guardian reviewer 增加可审计的工具循环与 Apps 连接绑定。

验证：

- `go test ./parity -count=1`
