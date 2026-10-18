# Round 743: API-key model access program catalog constraint

Rust 依据：`c03bbff4831a23a6dfc6f178838b73a50a9f554a`（`#53689`）。

Go 对齐内容：

- API-key 会话在服务端 `api_key_cyber_access_programs` 关闭时，从初始
  `ModelPickerOptions` 和在线刷新 catalog 中移除
  `AvailableCyberPrograms`；
- 默认开启或 ChatGPT/其他凭据保留 catalog 元数据；
- `model.AccessProgramsForAuth` 继续负责请求侧的实际转发策略。

验证：

- `go test ./app -run 'TestConstrainAPIModelAccessProgramsLikeRust|TestLocalThreadStartForwardsOnlyExplicitModelProviderLikeRust' -count=1`
- `go vet ./app`
