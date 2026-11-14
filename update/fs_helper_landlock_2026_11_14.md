# Helper Legacy Landlock 与 Windows proxy 对齐

上游 69231e1 的 exec-server/fs_sandbox.rs 在 use_legacy_landlock 为 true 时不加入 helper runtime readable paths。Go 原来无条件加入自身 executable read grant。本轮 fsSandboxRunPlan 按该开关决定是否添加读取授权，保持普通模式仅授权 executable 自身、网络始终 restricted、工作区显式物化。

TestFSHelperPermissionBoundaryLikeRust 扩展为普通/legacy 两种模式 × 缺省/空/单工作区，共六组输入。检查实际生成的 profile JSON 中 runtime executable read grant、写权限、网络、以及调用方 profile 不变。修复前 legacy 三组全部失败，见 fs_helper_landlock_before_2026_11_14.log；修复后 go test -count=1 ./execserver 通过21.705s，go vet ./execserver 退出0。此为 Windows 上的策略构造回归，未声称已执行 Linux Landlock 隔离。

上游 #61276 manager.rs 的 wrap_windows_file_system_helper 固定 WindowsSandboxProxySettingsMode::Preserve。Go helper wrapper 现固定 ProxySettingsPreserve，删除只在此调用的模式转换函数；公共请求 wire 字段保留兼容，不改变普通进程代理处理。代理更改通过源代码路径核对和全包回归，未实际修改系统代理验证。

剩余：不同 Linux sandbox executable 的 runtime path 配置（Go execserver 暂无该运行时配置入口）、Windows helper command/policy cwd 拆分及 glob绑定、别名根处理、统一helper准备和无可用沙箱拒绝、真实平台验证。#61138/#61276及整体同步仍未完成，未提交/推送。全模块session29603继续运行，不代替最新代码的最终全量验收。
