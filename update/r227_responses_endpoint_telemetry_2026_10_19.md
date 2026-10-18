# Responses endpoint telemetry 与 Guardian 专线路由

本批完成：

- `#40892`：Guardian 专用 `/guardian` 路由、配置开关、HTTP/WS 路由、
  service tier/routing hint 省略和 endpoint-aware WS 缓存键。
- `#40906`：`APIRequestRecord.Endpoint` 改为读取实际
  `AgentRequest.Endpoint`，不再硬编码 `/responses`。

## 验证

- `go test ./model -run 'ResponsesEndpoint|SelectedGuardianEndpoint|PostsResponsesRequest|WebSocket' -count=1`
- `go test ./appserver -run 'GuardianReviewEndpoint|Guardian.*Review|GuardianTurnStart' -count=1`
- `go test ./config -run 'GuardianV2' -count=1`
