# `#51974` Windows sandbox availability logging

- Rust 依据：`ea27864f99f0b086cec2f9f0251b7190fb9844f1`。
- 新增 `sandbox.WindowsMXCAvailable`；Go 没有 native MXC launcher，因此返回
  false，并复用同一值更新 availability metric。
- app-server startup 在 `.sandbox` 中同时写入：
  - `MXC availability: false`；
  - `Windows sandbox selection: mxc|disabled|unelevated|elevated`。
- Go 的 `thread/settings/update` 协议没有 Windows sandbox level 字段，不存在
  能改变 backend 的 accepted settings update，因此没有 settings-change
  re-log 载体；startup 日志覆盖所有可达变化。

验证：

- `go test ./sandbox -run 'MXC' -count=1`
- `go test ./appserver -run 'WindowsSandboxLogging|WindowsSandbox' -count=1`
- `go build ./...`
