# 回退 tool schema bounds 保留（#40966）

上游 `#40966` 撤销了 `#40719` 的 `minimum` / `maximum` / `maxLength` 保留：
这些字段不属于最新 Rust `JsonSchema` 表示，解析时应丢弃，`maxLength` 单独出现
也不再推断 string。

Go 的 `jsonschema` subset 已回退到最新行为，并保留完整包测试。

验证：

- `go test ./jsonschema -count=1`
