# 文件系统辅助操作权限边界（#61138，部分完成）

Rust 6aa83e5 的 exec-server/fs_sandbox 与 sandboxing/launch 审计发现，辅助操作须使用显式工作区列表并关闭网络。Go 原实现把空列表替换成 CWD，且没有关闭网络；Windows 包装器还使用调整前的原始 profile。

本轮修改：删除 CWD 回退，保留非 nil 空工作区；提取 fsSandboxRunPlan 供实际启动路径使用，通过公共 BuildCommandRunPlan 的 SandboxDisableNetwork 强制限制网络；Windows wrapper 消费 plan.PermissionProfile，使其包含最终工作区物化和 helper 读取授权。

新增 TestFSHelperPermissionBoundaryLikeRust 覆盖缺省、显式空、单工作区三种输入，验证网络元数据和序列化都 restricted、写入授权严格等于输入工作区、helper 自身可读、调用方 profile 不变。修改前失败见 fs_helper_boundary_before_2026_11_14.log；修改后 execserver 全包结果见 fs_helper_boundary_after_2026_11_14.log。旧 override 测试更新为新空列表契约。go vet ./execserver 退出 0。

尚未完成：helper operation 在 sandbox 层集中建模；Linux sandbox 可执行文件读取/legacy Landlock 区分；别名根处理；Windows glob/CWD 与 #61276 完整核对；无沙箱可用时拒绝执行及真实平台隔离验证。本轮不是 #61138/#61276 完成认证。

现有全模块回归 session 29603 继续运行，其中 appserver 已 pass（461.589s）；该全模块运行开始于本轮代码修改前，不能替代本轮 execserver 回归。整体同步未完成，未提交或推送。
