# Guardian 专用 inference endpoints（#40892）

## 实现

- `config.GuardianV2FreeGuardian` 读取 `[features.guardianv2].free_guardian`。
- `model.ResponsesEndpoint` 新增 `/guardian` 与 `/guardian-classifier`。
- `AgentRequest.Endpoint` 让 Guardian reviewer 选择专用路由。
- `guardianReviewEndpointForTurn` 只在以下条件同时满足时选择 `/guardian`：
  - `free_guardian=true`；
  - OpenAI/Codex-backend provider；
  - ChatGPT-style auth（chatgpt/chatgptAuthTokens/PAT/agent-identity）。
- API-key、custom provider 和未启用配置保持 `/responses`。
- 专用 endpoint 请求省略 `service_tier` 与 routing hint。
- WebSocket session key 包含 endpoint，避免 Guardian 连接复用普通 `/responses` socket。

`/guardian-classifier` 路径已提供；Go 当前没有 Guardian V2 async scorer，
因此没有 classifier 调用方。

## 验证

- `go test ./model -run 'ResponsesEndpoint|SelectedGuardianEndpoint|PostsResponsesRequest|WebSocket' -count=1`
- `go test ./appserver -run 'GuardianReviewEndpoint|Guardian.*Review|GuardianTurnStart' -count=1`
- `go test ./config -run 'GuardianV2' -count=1`
