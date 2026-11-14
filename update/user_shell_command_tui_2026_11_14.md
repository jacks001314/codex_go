# TUI 用户 shell 命令接线（#61216）

Rust 的 ChatWidget 在提交前识别 `!` 命令：不走模型回合，而是发出 `AppCommand::run_user_shell_command`，由 App 调用 app-server 的 `thread/shellCommand`；Cloud 会话直接拒绝并清掉 `user_turn_pending_start`。Go 的 tea TUI 之前把 `!cmd` 当成普通 prompt 发给模型，`chatwidget.SubmissionDecision.ShellCommand` 没有消费方。

本轮把这条链路接通：

- `tui/tea`：新增 `OnUserShellCommand` / `UserShellCommandResultMsg`。`submitRequest` 在 `!` 前缀且非 literal 输入时调用 `submitUserShellCommand`：空命令显示 “Shell commands / Type ! followed by a command to run it locally.”，真实命令设置 pending turn、状态 running、清空 `/copy` 目标并写入 prompt 历史（含 shell 模式标记），然后把命令交给宿主；steer 键遇到 shell 模式同样走 shell 路径而不是把 `!cmd` 当 steer 发给模型。
- 宿主拒绝（Cloud）时 `applyUserShellCommandResult` 清空 pending、状态回到 idle、渲染错误并调用 `submitNextQueued`，与 Rust `reject_user_shell_command` 一致。
- 远程 TUI：`interactiveRemoteUserShellCommand` 在 Cloud 模式返回精确文案 `Shell commands starting with ! are unavailable in Cloud conversations.`；其余模式向 app-server 发送 `thread/shellCommand`，参数为 threadId + command，命令的 turn/item 事件仍由订阅流渲染。
- 内嵌 TUI：新增 `appserver.RunLocalUserShellCommand`，以会话 shell 的 login 命令在会话 CWD 下执行（`/shell` 的显式全权限路径），返回 started/completed item 信息；`interactiveLocalUserShellCommand` 把它包装成 `turn.started → item.started → item.completed → turn.completed` 事件流，并把 `<user_shell_command>` 记录写入线程 rollout，供下一轮/恢复读取。

回归：

- `tui/tea/user_shell_command_like_rust_test.go`：`!echo hi` 只调用宿主、不产生模型 prompt、保持 pending/running 并记录历史；Cloud 拒绝清 pending 并显示文案；空 `!` 显示帮助；steer 键不会把 `!ls` 当 steer。
- `appserver/local_user_shell_like_rust_test.go`：本地执行成功/失败（exit 3）的 item 状态与退出码。
- `app/local_user_shell_command_like_rust_test.go`：内嵌事件顺序、item 输出与 rollout 记录。
- `app` 远程用例：Cloud 精确拒绝文案；非 Cloud 模式真实发送 `thread/shellCommand` 且参数正确。

验证：`go vet ./tui/tea ./app ./appserver` 退出 0；`go test ./tui/tea -count=1` 通过（47.5s）；`go test ./app -count=1` 通过（40.1s）；`go test ./appserver -run "ShellCommand|RunLocalUserShell" -count=1` 通过。日志见 `user_shell_command_tui_2026_11_14.log`。

说明：内嵌执行器优先使用环境中的登录 shell（`SHELL`/`COMSPEC`），未接入 app-server 的 shell snapshot 与线程环境策略；远程路径由服务端完整执行。这一差异记录为后续细化项。
