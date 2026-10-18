# Round 735: menu wording and voice/transcript audits

## `#47329` Standardize TUI menu description wording and punctuation

Rust 依据：`00d92ad3ee7f29142bca187a3391b8f38064baf0`。

Go 的内置 permission presets 已统一使用简洁、无句号的描述，并在
permissions popup、named-profile picker 与 inline `/permissions` catalog 之间
共享同一文案；disabled reason 保持 `Disabled by requirements`。

验证：

- `go test ./tui/chatwidget -run 'TestPermissionPresetDescriptionsMatchRustMenuWording|TestInlinePermissionPresetDescriptionsShareMenuCopy' -count=1`

## `#46722` Cancel pending transcript Home jumps

Rust 依据：`392f56a611c412b9b2eb1d9d4e59a3b42bee483a`。

Go 的 transcript pager 同步渲染已加载的完整 transcript，没有 Rust 的
`LoadingBeginning` 异步分页请求，也没有需要被后续导航取消的 pending Home
jump。Home 直接调用 viewport 跳转，因此该提交记为 N/A。

## `#44924` Refresh the speaker format when restarting voice output

Rust 依据：`7ef70f95d5c07976f3c992e413a02a6db238b0d0`。

Go voicehost 在同一 session 中先打开输入再打开输出，且没有 Rust 的 speaker
restart API，不会恢复 microphone 打开前的旧 `SupportedStreamConfig`。该提交
依赖的 stale speaker format 路径在 Go 中不存在，因此记为 N/A。
