# `#46051` Track WebSocket continuation modes and full-input send reasons

Rust 依据：`c56dda711cc13f94625734343cd95d4a4bf496ed`。

## 落地内容

- 新增 `codex.websocket.continuation` counter，每次 `response.create` 发送都
  记录一次（包括发送失败前的尝试）。
- tags：
  - `phase=warmup` + `mode=full` + `reason=prewarm`：`Prewarm` 的空输入预热；
  - `phase=generation` + `mode=incremental` + `reason=previous_response`：
    调用方提供了 `previous_response_id`；
  - `phase=generation` + `mode=full` + `reason=connection_reused`：复用连接但
    没有可续接的响应；
  - `phase=generation` + `mode=full` + `reason=initial`：全新连接。

## 架构差异

Go 的 `previous_response_id` 由 turn runtime 计算并放入 `AgentRequest`，而
WebSocket session 只缓存连接；Rust 的 per-socket
`continuation_reset_reason`（restored history、forked history、account switch、
connection closed 等）没有同等的 session 状态机。Go 因此记录发送时可直接
观测到的 mode/reason/phase，而不是伪造不可观测的 reset reason。

## 验证

- `go test ./model -run 'ContinuationMetric|PrewarmUsesWebSocket|WebsocketContinuationTags' -count=1`
- `go test ./model -count=1`

新增覆盖：

- `TestWebsocketContinuationTagsLikeRust`
- `TestResponsesAgentRunnerWebSocketRecordsContinuationMetricLikeRust`
- `TestResponsesAgentRunnerPrewarmUsesWebSocketGenerateFalse`（新增 warmup metric 断言）
