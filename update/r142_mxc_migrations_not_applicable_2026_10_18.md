# Round 676：native MXC policy/SDK migration audits

Rust 依据：

- `d1696652a20d18f1e1188b0a1912ab3c1304ffff` `#44580`
- `1c7c43cd856a1ef451cb7ff9dc1b701ac7942c9d` `#51819`
- `4aa94dce270de668eff6e2fa8585c82385e84455` `#52707`

## 结论

Go 保留了 MXC 的配置与 wire 兼容，但没有 native MXC launcher、policy
materializer 或 MXC SDK integration：

- `UseMXC` 在 execserver 中 fail closed；
- 没有 `mxc-sdk` / `appcontainer_common` / `learning_mode_windows` /
  `wxc_common` 依赖；
- 没有 `volume_roots` / `materialize_volume_roots` / `PathUri` 的 MXC
  symbolic `:root` 展开。

因此：

- `#44580` symbolic `:root` MXC policy 无 landing site；
- `#51819` MXC 1.0 SDK migration 无 carrier；
- `#52707` split MXC crates migration 无 carrier。

三者均为 not_applicable（架构差异），不代表 native MXC 已实现。

## 验证

```text
go test ./execserver -run 'MXC' -count=1
go test ./config -run 'MXC|WindowsSandbox' -count=1
rg -n 'mxc-sdk|appcontainer_common|learning_mode_windows|wxc_common|materialize_volume_roots' --glob '*.go'
```
