# Round 665：live tool-call metadata prompt cap 移除（#49401）

Rust 依据：`87d3e068478cb299cb25937e9793b5e5d5a7c668`。

## 已落地

- `responsesInputItemsWithStore` 不再调用
  `BoundExecutedToolCallsForPrompt`，因此不会在 prompt 构造时应用 128KB 聚合
  metadata cap。
- 新增 `SanitizeExecutedToolCallsForPrompt`，继续克隆输入并移除不可信的
  executed-tool metadata，避免为了去掉预算而放弃安全清洗。
- 出站请求仍通过 `boundedInputForResponseMessage` /
  `BoundExecutedToolCallsForMessage` 按实际 `MaxResponseMessageBytes` 预算裁剪，
  并保留逐调用参数限制。
- `TestResponsesInputItemsRetainLargeMetadataUntilMessageBoundLikeRust` 验证
  大于旧 128KB 上限的 live metadata 能进入请求项，直到实际消息预算才被裁剪。

## 剩余

Go recorder 尚未维护 Rust 的独立 `direct_calls: HashMap<ResponseItemId,
ExecutedToolCall>`，也没有在采样/压缩重试间 restore/prune 这些 live
observation；Go 的 `ToolResponseItem` 当前没有响应 item ID 字段。完整对齐仍需
把 live direct-call 观察与 persisted output 分离，并补 response-item-ID
绑定。

## 验证

```text
go test ./model -run 'ExecutedToolCall|ResponsesInputItemsRetainLarge' -count=1
go test ./turn -run 'ExecutedToolCall' -count=1
```

`go test ./model -count=1` 命中既有 timing flake
`TestResponsesHTTPOverloadKeepsRetryAdviceLikeRust`（5.9994937s vs 6s）；
`go vet ./model` 仍有既存 mutex-copy 警告。
