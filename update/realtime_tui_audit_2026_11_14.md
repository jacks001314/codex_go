# #61172 TUI 实时语音呼叫审计

## 已核实路径

Go 内嵌/本地 TUI 的语音链路是完整的：

1. `voiceRuntime.startCmd` 启动本地 `voicehost` 会话，取得 helper 的 WebRTC offer。
2. `startParams` 发送 V3 `realtime.StartParams`，transport 为 `webrtc(offer)`。
3. app-server `thread/realtime/start` 使用线程配置、认证与 Rust 默认值建立实时会话；真实测试覆盖 provider/auth 选择、V3 语音移交回合和冷恢复/fork 可用性。
4. app-server 返回 SDP 通知，TUI 解码后由 `applyAnswerCmd` 调用 helper 的 `ApplyAnswerSDP`。
5. 转写、音频、错误和关闭通知均有解码回归。

## 仍未等价的部分

Rust #61172 让 TUI 使用本地用户 provider/credentials 先创建 WebRTC call，再把 `existingCall(callID)` 交给 app-server，并自行把 answer 交给媒体 helper。Go 远端 TUI 目前仍把 `webrtc(offer)` 发给远端 app-server，由远端 app-server 用自己的配置与认证创建 call。

因此本地内嵌 TUI 行为可用，但远端 TUI 在“远端 app-server 无同账户凭据、用户本地有凭据”时仍不能按 Rust 方式协商。该提交保持部分完成，下一步需要导出/复用 realtime call 创建逻辑，在 app 侧加载本地线程配置与认证，创建 call 后发送 `existingCall`，并把本地 answer 应用到 helper。

## 验证

- `TestVoiceRuntimeStartsHelperAndPostsOffer`、`TestVoiceRuntimeApplyAnswerRequiresHelper`：helper offer 与 answer 应用入口。
- `TestInteractiveLocalVoiceCallbacksUseAppServerProtocol`：本地回调使用 app-server realtime 方法。
- `TestRealtimeStartOptionsUseThreadConfigAuthAndRustDefaults`：app-server 使用线程配置、认证与 Rust 默认值。
- `TestRealtimeV3HandoffStartsTurnAndWritesCodexOutputBack`：实时语音移交真实回合并写回输出。
- `TestRealtimeStartIsAlwaysAvailableLikeRust`：冷恢复/fork 的启动可用性矩阵。
- `TestDecodeThreadRealtimeNotifications`：started/sdp/transcript/audio/error/closed 通知解码。
- `go test ./realtime -run "TestRealtimeWebRTCCall|TestExistingCall|TestRealtimeWebRTCCallForwardsVoiceSessionID"` 通过 2.697s。

#61172 维持部分状态：本地路径已验证，远端本地凭据协商与 existingCall 挂接待实现。