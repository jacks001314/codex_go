# Linux 平台对齐交付

2026-11-25；Agent：linux-align；项目：project-1whyle5g4v99。

#63520 的最小完整路径已实现，并通过 Linux 原生 PTY 和 race 验证。两组指定测试最终全部通过；运行时 SQLite 为 3.54.1。#61898/#61900 完成调查，未实现：当前 Go 缺少它们依赖的 launcher supervisor、代理 FD 生命周期与 proc 预检基础设施，不能在 1–2 个文件内完整实现并验证。

## 修改文件与 diff 摘要

补丁：[linux-align.patch](linux-align.patch)。补丁对本轮修改前保存的镜像内容执行 `git apply --check`，退出码 0。

| 项目相对路径 | 改动 |
| --- | --- |
| `tui/tea/model.go` | +4 行：newProgramWithModel 安装 SuspendMsg 过滤器；model.update 在全局/模态路由前处理 job control。现定位 4402、4705 行。 |
| `tui/tea/job_control.go` | 新增 110 行：Ctrl-Z → Bubble Tea SuspendMsg；公开 ReleaseTerminal/RestoreTerminal 协调；恢复前等待 foreground；错误/取消安全停止。 |
| `tui/tea/job_control_unix.go` | 新增 75 行：持有 /dev/tty，TIOCGPGRP 对比本进程组，SIGTSTP 发给整个进程组，接收 SIGCONT。被忽略的暂停信号有约一秒的运行中等待预算。 |
| `tui/tea/job_control_other.go` | 新增 9 行：不支持平台返回明确错误，不发送信号。 |
| `tui/tea/job_control_like_rust_test.go` | 新增 170 行：事件路径与恢复顺序；无终端、无所有权、终端丢失、取消、释放失败等 LikeRust 测试。 |
| `tui/tea/job_control_linux_test.go` | 新增 187 行：真实 controlling PTY、生产 newProgramWithModel、真实 Ctrl-Z 解码和 OS 信号；inline/alternate 两种模式；detached 无终端与 orphan 进程组忽略 SIGTSTP。 |
| `go.sum` | 一条 glamour 模块内容校验值修复，版本和 go.mod 未变，见下文。 |

所有改动通过 project_write 同步至 Master。未修改 Windows 路径、未修改 sandbox 实现、未创建引擎 subagent。镜像中的 .git 指向 Windows worktree，本节点无法独立运行该仓库的 git rev-parse；Go 基线 e524c8d69 来自任务指定，补丁基于本轮取回内容，不声称已经在 Linux 解析确认该 commit。

## #63520 的行为与 API 接入

Rust 参考为指定目标：
https://raw.githubusercontent.com/openai/codex/3f65e9f1/codex-rs/tui/src/tui/job_control.rs

目标实现要求：发送 SIGTSTP 前确认可查询 controlling terminal；SIGCONT 后仅查询 foreground owner；bg 后 fg 可能不再发 SIGCONT，因此恢复前必须轮询所有权；终端丢失时不能重新启动原始模式和绘制。

Bubble Tea v2.1.1 的内置 tty.go:suspend 顺序是 releaseTerminal → suspendProcess → RestoreTerminal → ResumeMsg。只在 model.Update 处理 ResumeMsg 无法满足恢复前检查。ExecCommand.Run 出错时，Bubble Tea exec.go 同样会尝试 RestoreTerminal，不能直接用它处理终端丢失。

本次采用公开 WithFilter 接管 SuspendMsg，保留 Ctrl-Z 发出真实 SuspendMsg 的路径；过滤器同步释放终端、发送 SIGTSTP、接收 SIGCONT，再以 10ms 周期比较 TIOCGPGRP 与进程组。shell 只交还 foreground、没有再次 SIGCONT 的情况也会恢复。所有权到手后才 RestoreTerminal 并交付 ResumeMsg。没有 controlling tty 或初次查询失败时，保留界面并返回 Cannot suspend 通知；释放后终端失效、取消、释放或恢复失败时返回 InterruptMsg，避免主动重新进入原始模式。

暂停信号等待采用计数 ticker，避免 wall timer 在整个进程组停止期间过期、恢复时与 SIGCONT 派发竞争。真实 PTY 测试特意停止超过一秒，并检查恢复无错误通知；孤儿进程组测试确认被忽略的 SIGTSTP 不会永久阻塞。

## 原生环境与指定测试

实际执行环境：

```text
/usr/bin/go
go version go1.26.3 linux/amd64
Linux jacks 6.18.12+kali-amd64 #1 SMP PREEMPT_DYNAMIC Kali 6.18.12-1kali1 (2026-02-25) x86_64 GNU/Linux
/usr/bin/bwrap
bubblewrap 0.11.0
```

命令一，退出码 0；完整输出 [core.log](core.log)：

```sh
go test ./state ./sandbox ./network -count=1
```

```text
ok  	codex_go/state	7.800s
ok  	codex_go/sandbox	0.168s
ok  	codex_go/network	8.713s
```

命令二，退出码 0；全部包输出 [focused.log](focused.log)：

```sh
go test ./appserver ./tui/... -run 'UserVerification|JobControl|Suspend|WorkingDirectory' -count=1
```

```text
ok  	codex_go/appserver	0.448s
ok  	codex_go/tui/tea	3.988s
ok  	codex_go/tui/tui	0.033s
```

上面仅摘录三个包，其余包的通过、无匹配测试和无测试文件状态均保留在完整日志中。

SQLite 命令，退出码 0；完整输出 [sqlite.log](sqlite.log)：

```sh
CODEX_EXPECT_GOOS=linux CODEX_EXPECT_GOARCH=amd64 go test ./state -run 'SQLite' -count=1 -v
```

```text
=== RUN   TestNativeSQLiteArtifactPlatform
    sqlite_test.go:95: native artifact linux/amd64 embeds SQLite 3.54.1
--- PASS: TestNativeSQLiteArtifactPlatform (0.00s)
PASS
ok  	codex_go/state	0.387s
```

额外运行 job control race 验证，退出码 0；完整输出 [jobcontrol.log](jobcontrol.log)：

```sh
go test -race ./tui/tea -run 'JobControl|Suspend|CtrlZ' -count=1 -v
```

```text
=== RUN   TestJobControlPTYBackgroundContinueLikeRust/inline
    job_control_linux_test.go:40: native PTY: Ctrl-Z -> SIGTSTP; background SIGCONT kept canonical mode; foreground restored raw mode; draft preserved
=== RUN   TestJobControlPTYBackgroundContinueLikeRust/alternate
    job_control_linux_test.go:40: native PTY: Ctrl-Z -> SIGTSTP; background SIGCONT kept canonical mode; foreground restored raw mode; draft preserved
--- PASS: TestJobControlPTYBackgroundContinueLikeRust (5.11s)
=== RUN   TestJobControlNoTTYNativeLikeRust
    job_control_linux_test.go:162: no controlling terminal: open /dev/tty: no such device or address
        PASS
--- PASS: TestJobControlNoTTYNativeLikeRust (1.12s)
=== RUN   TestSuspendIgnoredSignalFailsSafelyLikeRust
    job_control_linux_test.go:186: timed out waiting for SIGCONT
        PASS
--- PASS: TestSuspendIgnoredSignalFailsSafelyLikeRust (2.14s)
PASS
ok  	codex_go/tui/tea	9.610s
```

## 基线问题与额外原生观察

首轮命令一确实失败，未隐藏该结果：

```text
ok  	codex_go/state	5.870s
--- FAIL: TestProbeSystemBwrapDoesNotWaitForDescendantHoldingStderr (0.00s)
    bwrap_probe_test.go:23: probe reported a user-namespace failure as usable
FAIL
FAIL	codex_go/sandbox	0.044s
ok  	codex_go/network	6.767s
FAIL
```

之后该单测 -count=10 和两次完整命令均通过，未修改相关实现。sandbox/bwrap.go:118–139 的 reader goroutine 与 captured 快照存在可疑时序：在等待 readerDone 前读快照。作为既有不稳定项留给 lead，最终通过不代表该问题已修复。

首次 TUI 编译因基线 go.sum 的 glamour hash 不符被 Go 拒绝。已查询公开数据库：
https://sum.golang.org/lookup/charm.land/glamour/v2@v2.0.1

数据库记录与下载内容一致：

```text
charm.land/glamour/v2 v2.0.1 h1:xl+r00A4aJWU0z8fgwKd9fQQ4rsphqGUzuEiXZP5n+c=
```

原记录是 h1:Vx2F81a4SBR5/IqAkDyeZ3mfuT7u+9AYxeXQ+J1n2Wc=。只修正此行；未关闭校验或使用未经验证的依赖替换。

额外调查命令（非指定验收命令）：

```sh
go test ./sandbox/linuxsandbox -run 'Bwrap|PIDNamespace|Proc|Proxy' -count=1 -v
```

退出码 1。其中：

- TestExpandUnreadableGlobsWalksInProcessLikeRust 失败，glob_expand_linux_test.go:66：depth=1 返回 hidden/nested/top 三个文件，而预期仅 top.key。实现入口是 glob_expand_linux.go:10 与 linux.go:759。未修改。
- TestLinuxBwrapScopedReadRootsEndToEndLikeRust 真实 bwrap 运行通过，输出包含 sandbox_pid=1、scope_usr=0、scope_proc=0、scope_home=2。该结果只证明当前主机的该场景，不证明 proc-denial fallback。
- TestLinuxBwrapTwoHalfManagedGrantEndToEnd 因本机没有 /dev/vsock 而跳过；不把此测试算作通过的代理 E2E。
- launcher 选择、bundled digest、PIDNamespace 等其余匹配测试通过。
- 正则 Proc 也匹配名称里的 Process，因此会运行上述 glob 测试。

## #61898/#61900 调查结论与最小补丁计划

已读取 PR 原始 diff，及目标 3f65e9f1 的 linux_run_main.rs：

- https://github.com/openai/codex/pull/61898.diff
- https://github.com/openai/codex/pull/61900.diff
- https://raw.githubusercontent.com/openai/codex/3f65e9f1/codex-rs/linux-sandbox/src/linux_run_main.rs

#61898 不只是更换 launcher 字符串。它构建 proxy supervisor → mount cleanup supervisor → bubblewrap，设置子进程组并转发信号、继承/验证父死亡行为、回收退出状态；host bridge 在外层 supervisor 启动，隔离每条路由的 FD 预算，使用 listener/control FD 传递，避免代理资源耗尽阻碍 mount cleanup。TCP namespace 准备只能在将 exec 的子进程内执行，host bridge 保留 host network namespace。

对应 Go 缺口：

| Go 文件与定位 | 当前行为 | 缺失的结构 |
| --- | --- | --- |
| `sandbox/linuxsandbox/linux.go:954` execBubblewrap | 组装 argv 后直接 unix.Exec；只保留需继承的 dataFDs | 无通用 bwrap child supervisor、signal forwarding、退出状态传播或独立 mount cleanup supervisor |
| `sandbox/linuxsandbox/linux.go:1287` prepareHostProxyRouteSpec | 立即启动 host bridge，路由 JSON 保存 UDS 路径 | 无 HostProxyBridge 资源所有权对象及可交接的 control/listener FD 生命周期 |
| `sandbox/linuxsandbox/linux.go:1530`、1553、1584 | exec 独立 host/local bridge；readiness 读一行；PPID 每 250ms 轮询 | 缺少与 sandbox 命令生命周期绑定的显式控制通道、失败时完整回收/reap、每路 FD 预算与统一 supervisor |
| `sandbox/linuxsandbox/transparent_tcp_netns_linux.go:81` | 有 transparent TCP 专用 re-exec/namespace child | 尚不能复用为含 explicit proxy 和 mount cleanup 的通用 supervisor |
| `sandbox/linuxsandbox/bwrap_linux.go:19` | 根据原策略选 launcher | 这部分已存在，但后续 preflight/supervisor 调用层缺失 |

不能把 Rust 的 private_bridge_control（CLOSE_RANGE_UNSHARE/CLONE_FILES）直接搬进普通 Go goroutine：Go runtime 会复用线程，隔离某 OS 线程 FD table 会破坏 runtime 与其它 goroutine 的描述符假设。Go 方案应优先复用 re-exec 进程隔离，通过受控 IPC 传递资源。

#61900 本身是诊断解析的小改动：在单行精确识别 “Can't mount proc on /newroot/proc” 或 “... on /proc”，只接受 Invalid argument、Operation not permitted、Permission denied，避免混合多行错误造成误判。但 Go 没有它所修复的基础设施：

- `linux.go:979` 只按 NoProc / InheritPIDNamespace 决定是否挂载 proc；NoProc 是手动参数。
- `execBubblewrap` 没有 build-preflight-argv、capture-stderr、proc-failure classifier 或自动重新构建路径。unix.Exec 成功后当前 helper 已不存在，不能在返回处解析 bwrap 失败并重试。
- `args.go:23` 虽提到 legacy proc fallback，并不代表该行为已有实现。
- 目标 Rust 是执行最小权限 true 命令的预检，然后在受识别的 proc-mount 错误下关闭 mount_proc，重新构建真正命令；不是在真实用户命令失败后重跑。透明 TCP/PID 隔离必须保留新 procfs，不能自动降级；显式 PID inheritance 也必须维持原语义。

建议最小分阶段补丁（超过 1–2 文件）：

1. 抽离可返回 argv、preserved FDs 与 cleanup 资源的 bwrap plan builder；先固定原权限策略选择 launcher，再以同一 launcher 构建最小预检。新增受限 stderr 捕获与 #61900 的精确单行分类器；只对非 transparent TCP 且需要 proc 的场景采用 mount_proc=false 重建。测试必须验证用户命令只执行一次、inherited PID 不探测、无关错误不降级，以及 /proc 与 /newroot/proc 两种格式。
2. 实现可复用的 re-exec supervisor、受控 FD/IPC handoff、启动失败回收、信号/状态转发与父进程死亡处理；把 host bridge 准备与启动拆开，再接 transparent TCP 子阶段及 mount cleanup。
3. 添加代理进程退出/崩溃、取消、父进程消失、FD 耗尽、proc-denial、显式继承 PID 的 Linux 原生集成测试。当前无 /dev/vsock 的主机不能验证全部已有 managed-grant 场景，不能用跳过代替这部分证据。

本轮不新增仅有 classifier、未接入生产执行路径的“已对齐”占位代码。

## 范围与风险

- #63520 使用本项目 NewProgram/Run 构造路径安装过滤器。绕过此路径，直接把 NewModel 交给外部 bubbletea.NewProgram，不会安装保护。
- 原生验证覆盖 Linux amd64；其他 Unix 和不支持平台未做跨平台构建/运行验证。
- PTY 测试依赖 python3，缺少时会明确 skip；本节点实际运行，没有 skip。
- 释放后终端失效会以 Bubble Tea InterruptMsg 安全停止，Run 返回 Bubble Tea 中断/结束错误；目前未将底层 tty 错误作为独立类型向 Run 透传。
- 持续等待 foreground 时应由调用者传入可取消 context。nil context 使用 Background；没有验证直接 Program.Kill 与后台 foreground 等待之间的交互。
- 本轮实现的是请求的最小 job-control 路径；未搬运 Rust 的全部光标 DSR 探测、输入 flush、program-status suspend 附属行为。屏幕释放与恢复由 Bubble Tea 公共 API 完成，两种屏幕模式已有真实 PTY 证据。
- #61898/#61900 仍是架构性未完成项；上面的可疑 bwrap 探测时序与 glob depth 失败也未修复。不得将两组指定测试通过解读为整个 Linux sandbox 测试集通过。
