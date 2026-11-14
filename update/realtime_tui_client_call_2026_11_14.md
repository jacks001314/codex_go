# TUI 自建 WebRTC 实时语音链路（#61172）

日期：2026-11-14
上游提交：`72532ecae5efe500d9e722f3ed5214e63cc8098a` Negotiate realtime voice calls in the TUI (#61172)

## 上游要点

- TUI 用自身 provider 与凭据创建 WebRTC call，再用返回的 call id 通过 `existingCall` 让 app-server 挂接。
- 保留生效的 voice 偏好，并在建呼与后端 observer 之间共享实时提示词准备。
- 协商异步执行；停止语音时取消在途启动，忽略过期启动结果（含已经排队的旧结果）。

## Go 现状与缺口

- 内嵌与远端 TUI 都把 helper 的 SDP offer 交给各自 app-server，由服务端用服务端凭据创建 call；远端场景下建呼凭据属于远端主机，客户端本地凭据完全未参与。
- `realtime` 包已有 `existingCall` 传输、V3 建呼请求体（`/live`、multipart、session 更新）和服务端建呼实现 `createRealtimeWebRTCCall`，但没有对客户端暴露建呼入口。
- `voiceRuntime` 只支持“发 offer 给 app-server、等 SDP 通知”，没有本地建呼后直接应用 answer 的路径，也没有启动代际，停止语音后在途启动仍可能把 helper 挂到运行时。

## 实现

- `realtime.CreateWebRTCCall` 导出客户端建呼：返回 provider 的 SDP answer 与 call id，复用现有 V3 请求体、`Location` 解析与鉴权回调。
- 新增 `app/voice_call.go` 的 `clientOwnedVoiceCall`：按本地配置解析 provider（`model.ProviderForConfigID`）与凭据（auth store + `RuntimeProvider.APIProvider/APIAuth`），支持 `experimental_realtime_webrtc_call_base_url` 覆盖；建呼请求带 `session-id`/`thread-id` 与鉴权头，session 更新带生效 model、voice 和共享实时提示词（`prompt.PrepareRealtime`，与 app-server 在 `includeStartupContext=false` 时的准备一致）。
- `voiceRuntimeOptions.ownCall` 路径：先建呼、把 answer 应用给 helper，再发送仅含 `existingCall` 的 `thread/realtime/start`（V3、`realtimeSessionId`、无 model/voice/startup context，符合 existingCall 的会话配置约束）；挂接失败会退休 helper。
- 启动代际 + 最新尝试标记：停止语音使在途启动失效，后来的启动取代先前尝试；被取代的尝试既不应用 answer 也不发挂接请求。
- 远端 TUI 与内嵌 TUI 都启用 `ownCall`；app-server 仍保留 websocket 及服务端建呼能力，`existingCall` 分支只做挂接。

## 验证

```
go test ./app -run "TestRemoteVoiceOwnedCall|TestVoiceRuntime" -count=1     # 通过
go test ./app -count=1                                                     # ok 39.825s
go test ./realtime -count=1                                               # ok 4.539s
go vet ./app ./realtime                                                    # 无输出
```

新增回归（`app/voice_call_like_rust_test.go`）：

- `TestRemoteVoiceOwnedCallCreatesClientCallLikeRust`：本地 api-key 凭据建呼，请求落在 `/v1/live`，带 Bearer、`session-id`/`thread-id`，body 保留 SDP、提示词、model 与 voice；返回参数为 `existingCall` + V3 + 无会话配置覆盖。
- `TestRemoteVoiceOwnedCallRequiresCredentialsLikeRust`：无凭据在建呼前失败。
- `TestVoiceRuntimeAppliesOwnedCallAnswerBeforeAttachLikeRust`：answer 先于挂接应用。
- `TestVoiceRuntimeDropsOwnedCallSupersededByStopLikeRust`：协商中停止后不发挂接、退休 helper。
- `TestVoiceRuntimeKeepsOnlyNewestOwnedCallLikeRust`：重启后旧协商结果不会替换最新会话。

## 剩余

- 真实音频与平台认证（实际 WebRTC 媒体、麦克风/扬声器设备、服务端回声链路）仍需真实设备验证；本项覆盖到建呼请求、answer 应用顺序、挂接与取消语义。