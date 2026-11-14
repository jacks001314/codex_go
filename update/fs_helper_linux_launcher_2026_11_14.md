# #61138 Linux sandbox launcher helper 读取授权

## 实现

Rust #61138 的 `helper_read_roots` 在非 legacy Landlock 路径同时授权当前 helper 和独立 `codex_linux_sandbox_exe`，但只授予可执行文件本身，不授予父目录。

Go 补齐了同一运行时链路：

- `execserver.Server` 新增可信启动字段 `codexLinuxSandboxExe` 与 `SetCodexLinuxSandboxExe`；`FileSystemSandboxContext` 以非序列化字段承接，供进程内路径使用。
- `app.runExecServer` 使用 `DispatchPaths`（或 Linux 当前可执行文件回退）设置该路径；非 Linux 保持为空。
- `fsHelperReadableRoots` 同时返回当前 helper 与 Linux launcher，按清理后的绝对路径去重，绝不加入父目录。
- `fsSandboxRunPlan` 将 launcher 写入 `CommandRunRequest.CodexLinuxSandboxExe`；legacy Landlock 仍不生成运行时可执行文件读取授权，非 legacy 才加入精确路径。

## 回归

- `TestFSHelperReadableRootsIncludeLinuxSandboxLauncherLikeRust`：两个精确路径、父目录不授权、相同路径去重。
- `TestFSHelperPlanCarriesLinuxSandboxLauncherLikeRust`：非 legacy 的权限 JSON 含 launcher 精确读取且不含父目录；legacy 不授权但仍把 launcher 传给 sandbox plan。
- `TestServerAppliesLinuxSandboxLauncherToFSSandboxContext`：服务器启动配置进入请求上下文。
- 现有 `TestFSHelperPermissionBoundaryLikeRust` 继续验证网络关闭、显式工作区与 helper 自身读取。

## 验证

- 目标测试通过 0.045s。
- `go test ./execserver` 通过 20.653s；`go test ./app -run "ExecServer"` 通过 5.905s。
- `go vet ./execserver ./app` 退出 0。

日志 update/fs_helper_linux_launcher_2026_11_14.log。该项补齐了 #61138 的 Linux launcher 读取/legacy 区分子项；真实 Linux 平台隔离、根别名规范化与完整 Windows wrapper 认证仍保持未完成，因此逐提交表继续标记部分。