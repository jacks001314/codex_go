# Windows helper cleanup 与 Guardian async score N/A

本轮关闭：

- `#40808` Windows sandbox helper cleanup：Go `execserver/fs_helper.go` 已有
  newline-delimited response、并发 bounded stderr drain、reap timeout 和 stuck
  helper kill；相关测试通过。
- `#40901` Record reviewed actions with security risk scores — N/A：属于 Rust
  guardian-v2 async scorer，Go 没有该扩展。
- `#40911` Make Guardian risk score persistence opt-in — N/A：Go 能解析
  `persist_scores`，但没有 async score store 可持久化。
- `#41006` Trust invoked user skills in Guardian reviews — N/A：同样属于
  async scorer trusted-skill context。

## 验证

- `go test ./execserver -run 'Helper|FSHelper' -count=1`
- `go test ./parity -count=1`
