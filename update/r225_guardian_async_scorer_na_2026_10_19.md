# Guardian V2 async scorer 相关提交标记 N/A

本轮关闭：

- `e8b938b02e` Use typed outcomes for Guardian V2 classification (#41146)
- `df9f537a6e` Remove locking from trusted skill collection (#41150)
- `4f2a1d8666` Fail closed on unbounded Guardian parent compactions (#41152)

这三笔都只修改 Rust 的 `ext/guardian-v2/src/async_scorer`。Go 目前没有该异步
scorer/classifier 扩展（`model/catalog.go` 明确记录 classifier 字段属于 Rust
async scorer），因此没有对应的 Go carrier；同步 Guardian reviewer 路径不受影响。
