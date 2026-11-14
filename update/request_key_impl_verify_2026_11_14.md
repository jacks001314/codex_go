# #61256 request_key 实现核实（2026-11-14）

计划表把该项标为“待核实实现与测试”。本轮核实结果：

- `sandbox/linuxsandbox/seccomp_groups.go`：`VmSocketRestricted` 之外的模式都把 `request_key` 加入拒绝集合，与 Rust 7eaaee42 的 syscall 列表一致。
- `sandbox/linuxsandbox/request_key_like_rust_test.go`：存在 Linux 原生探测用例，先安装 ENOSYS 基线以避免调用主机 key helper，并防止继承的 EPERM 掩盖回归。
- 当前树验证：`go test ./sandbox/linuxsandbox -count=1` 通过（Windows 上仅编译与非 Linux 分支）；`GOOS=linux GOARCH=amd64 go build ./sandbox/linuxsandbox` 退出 0。

结论：实现已在仓库中，缺的是在真实 Linux 主机上执行原生探测。计划状态改为“部分”，不宣称完成。
