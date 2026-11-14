# 文件系统 helper 必需沙箱的 Windows 拒绝路径

Rust exec-server 的 sandbox_selection 使用 RequirePlatform。Go fsSandboxCommand 原先在 Windows selection disabled 时返回裸 helper 命令，导致受限读取实际无沙箱执行。本轮改为返回 JSON-RPC invalid params (-32602)：filesystem sandbox cannot be enforced on this executor。该分支仅在操作已经判定需要沙箱时到达，不影响无需沙箱的读写。

最初沿用空 entries 测试，读成功后发现该配置没有触发受限读取，因此 before 日志不能证明绕过沙箱。已纠正测试：加入 *.txt 拒绝规则，先断言 prepareFSSandboxForOperation 返回 required=true，再断言 Windows disabled 请求拒绝。生产分支差异由源码核对确认；不声称有有效的修复前动态复现。原 removed-CWD 测试依赖关闭沙箱模拟执行，现改为 TestFSHelperPlanSurvivesRemovedPolicyCWDLikeRust：删除 policy CWD 后通过实际生产准备函数验证 command cwd 为磁盘根、policy cwd 不变、拒绝 glob 绑定原目录。不将该测试称为真实隔离测试。

验证：go test -count=1 ./execserver、go vet ./execserver，见 after/vet 日志。剩余 helper 根别名、Linux运行时配置及平台原生隔离；整体同步尚未完成，未提交或推送。
