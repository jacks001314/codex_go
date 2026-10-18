# `#51843` Run sandbox integrity checks before commands and filesystem operations

Rust 依据：`cba716c0f1caa4f2528dedbb1075b50126709d7e`。

## 落地内容

- `sandbox.PermissionProfile.RuntimeFileSystemEntries` 暴露 canonical runtime
  JSON 中的 read/write/deny/glob/special entries；legacy profile 先序列化为
  runtime 表示，保证观测策略与启动器实际执行的策略一致。
- `execserver` 新增 `runSandboxIntegrityChecks`：把
  `sandbox/integrity.RunIntegrityChecks` 接到
  - 远程进程准备 `prepareExecProcess`（`BuildCommandRunPlan` 成功之后）；
  - sandboxed filesystem helper 准备 `fsSandboxCommand`；
  两处都发生在启动子进程之前。
- `codex sandbox` 的本地命令路径在 plan 校验后同样调用 runner。
- 观测语义与 Rust 一致：checker 失败/超时只记录 telemetry，不返回错误、不修改
  plan/command；disabled 与 full-disk-write profile 不进入 runner。
- `RunIntegrityChecks` 的内部 5 秒预算继续生效，超时后停止等待但不取消已开始的
  观测工作。

Go 没有 Rust 的 `ExecRequest::from_sandbox_exec_request` 单一构造函数；等价边界
是 exec-server 的 process/fs 准备与 CLI sandbox plan 三处。

## 验证

- `go test ./sandbox -run 'RuntimeFileSystemEntries' -count=1`
- `go test ./execserver -run 'RunSandboxIntegrityChecks' -count=1`
- `go test ./sandbox/integrity -count=1`
- `go vet ./sandbox ./execserver ./app`
- `go build ./...`

新增回归：

- `TestRuntimeFileSystemEntriesExposeCanonicalPolicyLikeRust`
- `TestRuntimeFileSystemEntriesSerializeLegacyProfileLikeRust`
- `TestRunSandboxIntegrityChecksObservesPreparationLikeRust`
- `TestRunSandboxIntegrityChecksSkipsFullAccessLikeRust`

本机是 Windows，远程 sandboxed process launch 在该平台本身不可用；进程路径的
接线由 `go build` 与 `go vet` 覆盖，FS helper/CLI 路径的 runner 观测由上述单测
覆盖。
