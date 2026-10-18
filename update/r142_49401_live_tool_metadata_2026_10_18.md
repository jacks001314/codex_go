# Round 665/666：live tool-call metadata 跨请求窗口（#49401）

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

## Round 666 补齐

- `ExecutedToolCallRecorder` 新增 `directCalls map[string]ExecutedToolCall`，
  以 response item ID 索引完整 live direct-call 观察。
- `ToolResponseItem` 新增 `id`，缺少 ID 时分配带 `fco_` 前缀的稳定 ID；
  关闭 response item IDs 时通过 carrier interface 剥离。
- 普通 sampling 会从 `directCalls` 恢复被 persisted budget 剪掉/清空的
  direct metadata，并按当前请求窗口 prune 已离开的 observation。
- `AttachToCompactionPrompt` 走不 prune 的路径，因此 shortened compaction
  retry 不会丢失 live observation。
- `TestExecutedToolCallRecorderRestoresLiveDirectObservationLikeRust` 覆盖
  restore、compaction 保留与 sampling prune。

## 验证

```text
go test ./model -run 'ExecutedToolCall|ResponsesInputItemsRetainLarge' -count=1
go test ./turn -run 'ExecutedToolCall' -count=1
```

`go test ./model -count=1` 命中既有 timing flake
`TestResponsesHTTPOverloadKeepsRetryAdviceLikeRust`（5.9994937s vs 6s）；
`go vet ./model` 仍有既存 mutex-copy 警告。
