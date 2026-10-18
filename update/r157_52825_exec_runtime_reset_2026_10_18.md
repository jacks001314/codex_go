# `#52825` Report exec runtime resets before accepting further requests

Rust 依据：`806d9732c974bc8a51b8317c1bd8985544fe627c`。

- `Execute` / `Wait` 在调用创建时捕获当前 live binding generation；
- 若调用过程中 recovery 替换了 binding，返回明确的 reset 错误，且不执行新
  runtime 上的代码；
- reset 文案说明 stored values/cells 丢失、外部 tool 操作可能已完成，并允许
  新的 `exec` 调用继续；
- 连接已失效时创建的新调用视为无 binding，可正常打开 replacement runtime；
- `Terminate` 只使用现有 binding，不再 `get_or_open`，避免 cleanup 掩盖 reset。

验证：

- `go test ./codemode -count=1`

新增覆盖：

- `TestRemoteSessionStaleGenerationReportsResetLikeRust`
- `TestRemoteSessionTerminateDoesNotOpenLikeRust`
