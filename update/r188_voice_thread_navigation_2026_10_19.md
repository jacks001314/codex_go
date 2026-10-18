# Round 745: voice conversations across thread navigation

Rust 依据：`a837a656ff15a90e86f4ae4d5f2e6833114d6f61`（`#47381`）。

Go 的语音会话由 Model 级 `VoiceConversation` 持有，不随 thread-scoped state
重置。线程切换后 voice owner 仍是原 thread，返回时 retained captions 按线程
恢复；agents overview 会标记 origin root/child 的 voice owner。

验证：

- `go test ./tui/tea -run 'TestVoiceConversationSurvivesThreadNavigationLikeRust|TestRetainedVoiceCaptionsRemainThreadScopedLikeRust|TestAgentsOverviewMarksVoiceOwnerLikeRust|TestAgentsOverviewMarksChildVoiceOwnerLikeRust' -count=1`
