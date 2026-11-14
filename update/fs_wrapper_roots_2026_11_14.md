# Windows helper 工作区传递修复（#61138/#61276 部分）

继续审计上一轮权限边界时发现：windowssandbox/wrapper.go 在 CreateWindowsSandboxCommandArgsForPermissionProfile 和 validateParsed 两处使用 len(roots)==0 回退到 CommandCWD。上层已物化权限，但 wrapper 的 workspace 元数据与最终 CaptureRequest 仍错误扩大，不符合显式空工作区契约。

修复：仅 nil 输入保留兼容回退。非 nil 空列表通过 Go 内部 wrapper 参数 --empty-workspace-roots 显式传递，解析端保留非 nil 空列表，cloneStrings 继续保留该区别，最终 CaptureRequest 可正确交给 ResolveCapturePermissionProfile。此参数仅 Go 自身 wrapper 使用，不是新增 Rust 对外协议。

TestWrapperPreservesExplicitEmptyWorkspaceRoots 覆盖 omitted/empty/selected，逐一断言序列化解析后的 WorkspaceRoots 和最终 CaptureRequest。修改前 empty 分支两处均失败，日志 fs_wrapper_roots_before_2026_11_14.log。修改后 go test -count=1 ./sandbox/windowssandbox ./execserver 通过，分别 2.357s / 21.020s；go vet ./sandbox/windowssandbox ./execserver 退出0。

尚未完成：Windows helper CommandCWD 与 policy CWD 的拆分/相对拒绝 glob 绑定、helper 固定 Preserve proxy 语义、helper operation 统一准备、无可用沙箱的拒绝及 Linux 原生运行。#61138/#61276 仍部分完成。此前全模块 session29603 在继续，不能替代最新改动的最终全量验证。整体同步未完成，未提交或推送。
