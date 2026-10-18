# Round 714: Guardian Decisions API-key fallback audit

`#53633` 修改 Rust `guardian-v2` async_scorer 的 Decisions 凭据查找：

- 允许在默认 openai provider、无 custom base URL 时回退到 `OPENAI_API_KEY`；
- cached API key 仍优先；
- 凭据缺失的 failure metric 从 `provider_error` 改为
  `credentials_unavailable`。

Go 没有 async Guardian Decisions scorer，也没有 cached Decisions API-key 路径；
Guardian 审批由同步 model review 完成。该提交无 Go 载体，记为
`not_applicable`。

验证：

- `go test ./parity -count=1`
