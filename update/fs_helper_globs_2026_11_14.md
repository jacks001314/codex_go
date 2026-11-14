# Windows helper 启动目录与拒绝 glob

实现 sandbox.BindWindowsHelperDenialGlobs，并在 execserver/fsSandboxRunPlan 的 workspace roots 物化之后调用。普通相对 deny glob 绑定 policy CWD；Windows home-relative (~、~/、~\\及子路径) 保留原义；绝对路径保持不变。只修改 deny glob，输入 profile 不变。Windows wrapper CommandCWD 改用 plan.CommandCWD（磁盘根），从而不再要求原 policy CWD 在启动时仍存在。

TestWindowsHelperDenialGlobsLikeRust 移植 Rust launch.rs 八个输入样例并验证输入不变。运行 go test -count=1 ./sandbox ./execserver、go vet ./sandbox ./execserver，结果见同名 .log 及 _vet 日志。当前测试覆盖策略转换及现有 execserver 回归；没有声称实际 elevated wrapper 隔离已经验证。

仍待完成：helper 根别名处理、统一沙箱准备与无可用沙箱拒绝、独立 Linux sandbox executable 配置/读取授权、Windows 实际隔离验证。整体同步未完成，未提交或推送。
