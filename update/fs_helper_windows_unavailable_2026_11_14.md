# 文件系统 helper 的 SandboxUnavailable 边界（#61276）

Rust 50455895 把 Windows 文件系统 helper 的沙箱准备收拢到 `SandboxManager::for_file_system_helpers()`，并规定：helper 必须运行在某个沙箱里；当请求要的是 Windows 令牌沙箱、而执行器不是 Windows 时，返回 `SandboxUnavailable`（exec-server 映射为 `filesystem sandbox cannot be enforced on this executor`）。

Go 的 `fsSandboxCommand` 在 `runtime.GOOS != "windows"` 时直接返回 Unix 沙箱命令，忽略了请求里的 `windowsSandboxLevel`，等于把 Windows 令牌沙箱请求静默降级。

本轮改动：

- 新增 `fsHelperWindowsSandboxUnavailable(goos, ctx, cwd)`：仅在非 Windows 执行器、cwd 使用 Windows 路径约定、且 `selectWindowsSandboxForExecutor` 解析出的选择不是 `disabled` 时返回 true。
- `fsSandboxCommand` 在 Unix 分支前先做该检查，命中时返回 `filesystem sandbox cannot be enforced on this executor`（与 Rust 的 invalid_request 文案一致），不再启动未受限的 helper。
- 显式 `disabled`、Unix 路径、原生 Windows 执行器，以及空上下文都不受影响。

验证：新增 `TestFSHelperRejectsWindowsTokenSandboxOnNonWindowsLikeRust` 覆盖五种组合与 nil 上下文；`go test ./execserver` 全包通过（21.1s），日志 `fs_helper_windows_unavailable_2026_11_14.log`。

仍未完成：helper operation 在 sandbox 层集中建模、Linux sandbox 可执行文件读取/legacy Landlock 区分、根别名规范化、Windows glob/CWD 完整核对，以及真实平台隔离验证。`#61276`/`#61138` 继续为部分。
