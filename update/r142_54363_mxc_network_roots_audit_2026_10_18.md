# Round 674：MXC implicit read-root filtering audit（#54363）

Rust 依据：`3df96b085c666b33d3775aa4e77219ec1ed828a0`。

## 审计结论

该提交只修改 native MXC 的 policy materialization：

- `windows::volume_roots` + `implicit_read_roots` 过滤不可 inspect 的 remote
  volume；
- `materialize_volume_roots` 只对 implicit `:root` read entry 使用过滤后的
  root，write/deny 仍覆盖全部 volume；
- policy inspection 与 command launch 共享同一过滤结果。

Go 没有 native MXC launcher/policy materializer：

- `UseMXC` 会在 execserver 中 fail closed 为 “native MXC is unavailable on this
  executor”；
- `windows_mxc_available` telemetry 也固定记录 false；
- Go policy 层没有 MXC `PathUri` volume-root 枚举或
  `materialize_volume_roots` 路径可插入该 predicate。

因此没有可安全移植的 landing site，记录为 not_applicable。

## 验证

```text
rg -n 'volume_roots|implicit_read_roots|materialize_volume_roots|uninspectable' --glob '*.go'
go test ./execserver -run 'MXC' -count=1
```
