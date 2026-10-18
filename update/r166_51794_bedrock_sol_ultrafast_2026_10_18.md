# `#51794` Advertise Ultrafast for GPT-6.1 Sol on Amazon Bedrock

Rust 依据：`87be737b664508d48402f08f57d09aba48e5f17a`。

- Mantle `openai.gpt-6.1-sol` 与 Runtime `global.openai.gpt-6.1-sol` /
  `us.openai.gpt-6.1-sol` 现在广告 `ultrafast` service tier；
- 默认 tier 仍为空，选择保持 opt-in；
- 与已有 Astra 行为一致，其他 Bedrock 模型不获得 premium tier。

验证：

- `go test ./model -run 'Bedrock.*Ultrafast|BedrockCatalogAdvertises|AmazonBedrockRuntimeModelCatalog' -count=1`
- `go test ./model -count=1`

同时修正 `TestResponsesHTTPOverloadKeepsRetryAdviceLikeRust` 的既有时间精度
flake：`RemainingDelay` 从 deadline 起算，允许 5s..6s 的合理剩余区间，而不是
要求精确 6s。
