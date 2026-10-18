# Round 673：Windows sandbox selection separation audit（#45730）

Rust 依据：`d4e11a9b97ce405c2e1172e3fbb5afc046f6589a`。

## 已具备

- Go 保留 `windowsSandboxLevel` wire 字段和 `disabled` / `restricted-token` /
  `elevated` / `mxc` 四个序列化值。
- process launch 在 legacy/restricted-token 选择前显式处理 `mxc`：限制 TTY/arg0
  与 managed networking，并在无 native MXC runner 时拒绝。
- filesystem helper 会解析并拒绝未知 level；MXC 最终也因没有 native runner 而失败。

## 剩余

- Go 没有 `WindowsSandboxSelection` 类型，process 与 filesystem helper 各自解析
  level，没有 Rust `select_sandbox` 的共享 selection 结果。
- filesystem helper 对 MXC 直接落到 legacy wrapper 的 unsupported level 错误，
  而不是与 process path 一致的 “native MXC unavailable” 诊断。
- capability discovery / skill reads 与 process launch 尚未共享同一个 typed
  selection。

因此记录 partial；由于 Go 没有 native MXC，本轮不引入伪实现。

## 验证

```text
go test ./execserver -run 'WindowsMXC|SandboxOverride|WindowsSandboxLevel' -count=1
```
