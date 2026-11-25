# Linux 平台对齐交付

2026-11-25；Agent：linux-align；项目：project-1whyle5g4v99。

#63520 的最小完整 Ctrl-Z 暂停/恢复路径和 #61900 的 proc 挂载预检/回退已实现。指定两组 Linux 测试、proc 专项、race、go vet 均通过；SQLite 为 3.54.1。完整 TUI/sandbox 和仓库全量测试仍有失败，见下文。#61898 的 supervisor/代理生命周期架构未实现。

最新合并指引：lead 已合并 #63520 为 974090d28，且不采纳此前 go.sum 改动。请对后续 #61900 使用仅含三个 sandbox 文件的 [61900-only.patch](61900-only.patch)，不要重新套用完整补丁或 TUI 增量。此次请求未再修改 go.sum。\n\n## 补丁与修改清单

- 完整补丁：[linux-align.patch](linux-align.patch)，包含下列 11 个源码/依赖文件。
- 若已经合入首轮 #63520，使用增量补丁：[61900-and-windows-delta.patch](61900-and-windows-delta.patch)。增量包含 #61900 和 Windows Ctrl-Z 修正；不要再叠加完整补丁。
- 两个补丁分别对捕获的对应修改前文件执行 git apply --check，均退出 0。本镜像 .git 指向 Windows worktree，无法在本节点独立验证 Go commit；补丁基于实际取回的镜像内容。

| 项目相对路径 | diff 摘要 |
| --- | --- |
| tui/tea/model.go | 4 行接入：构造 Program 安装 SuspendMsg 过滤器；update 在全局/模态路由前处理 job control。 |
| tui/tea/job_control.go | Ctrl-Z → SuspendMsg；过滤器协调终端释放、暂停、foreground 检查、恢复；Windows 保留 Ctrl-Z undo。 |
| tui/tea/job_control_unix.go | controlling /dev/tty、TIOCGPGRP、进程组 SIGTSTP/SIGCONT、被忽略信号超时。 |
| tui/tea/job_control_other.go | 不支持平台明确返回错误。 |
| tui/tea/job_control_like_rust_test.go | LikeRust 消息路径、顺序、无终端/无所有权/终端丢失/取消/释放失败测试。 |
| tui/tea/job_control_linux_test.go | 生产 Program + 真实 controlling PTY；inline/alternate、后台 SIGCONT、foreground-only handoff、草稿保留、detached/orphan 场景。 |
| tui/tea/job_control_windows_test.go | Windows Ctrl-Z 不被 suspend 拦截，仍撤销 composer 输入；本节点仅交叉编译。 |
| sandbox/linuxsandbox/linux.go | 抽出共用 bwrap argv builder；按实际策略选择 launcher 后执行 proc 预检，必要时关闭 proc 挂载再构建真正命令。 |
| sandbox/linuxsandbox/proc_mount_linux.go | 精确诊断分类器、最小只读 true 预检、64KiB stderr 上限及持续排空、超时、bundled launcher 打开对象验证。 |
| sandbox/linuxsandbox/proc_mount_linux_test.go | 六个正例及反例；生产 launch 路径；真实 bwrap 回退、payload 只执行一次、退出码与静默诊断验证。 |
| go.sum | 修正已有 glamour v2.0.1 内容校验值，未改版本或关闭校验。 |

源码及交付物均通过项目工具同步至 Master，未修改 D:\\qax。最新检查无回写冲突/排队；10 个未记账本地文件对应只读参考附件。测试生成的本地运行目录已移到 /tmp 保存，不作为源码提交。

## #63520 行为和边界

Bubble Tea v2.1.1 内置 suspend 在发出 ResumeMsg 前就 RestoreTerminal；仅处理 ResumeMsg 无法保证后台恢复前不进入 raw mode。ExecCommand 的失败路径也会恢复终端。因此采用公开 WithFilter 拦截实际 SuspendMsg，使用公开 ReleaseTerminal/RestoreTerminal：

1. 打开 controlling /dev/tty 并验证当前进程组拥有 foreground；失败直接通知，不释放终端或发信号。
2. 释放终端后向整个进程组发送 SIGTSTP，接收 SIGCONT。
3. 仅轮询 foreground ownership，取回所有权后才恢复 raw mode/renderer，交付 ResumeMsg。shell 只交回 foreground、没有第二次 SIGCONT 也能恢复。
4. 释放后终端失效或 context 取消时返回 InterruptMsg；不主动重新进入 raw mode。无控制终端安全失败。

信号等待计数 100 个 10ms ticker tick，避免 wall timer 在整个组停止期间到期。真实 PTY 测试停止超过一秒，并验证后台 SIGCONT 时仍是 canonical 模式、foreground-only handoff 后才回 raw 模式、两种屏幕模式和草稿保留。Windows 条件守卫修复了首轮交付会拦截 Windows Ctrl-Z 的问题；请使用最终完整补丁或增量补丁。

此项仅声明请求的最小 job-control 路径完成。未移植 Rust 全部 DSR 光标重探、输入 flush、program-status suspend 附属行为；终端释放/恢复由 Bubble Tea 完成。绕过项目 newProgramWithModel 而直接外部构造 Bubble Tea Program 不会安装过滤器。持续等待 foreground 依赖可取消 context，nil 使用 Background；直接 Program.Kill 与后台等待交互未验证。其他 Unix 未原生运行。

## #61900 生产路径

execBubblewrap 先根据真实 filesystem policy 选择 launcher，再仅在需要新 proc 且非 transparent TCP 时做预检。NoProc / InheritPIDNamespace 不探测。预检复制必要选项、采用最小只读 filesystem、cwd=/、true 命令，共用 argv builder；不会运行或重试用户命令。

分类器逐行寻找 Can't mount proc on ，目的地必须精确为 /newroot/proc 或 /proc，错误必须精确为 Invalid argument / Operation not permitted / Permission denied。/proc-other、ENOSPC、跨行、错误后缀不触发降级。命中后以 mountProc=false 重建实际命令，透明 TCP 保留原路径。

stderr 最多保留 64KiB，并持续排空后续输出；stdout 丢弃，预检诊断不泄漏。启动/信号/10 秒超时错误安全返回；普通非 proc 非零错误交由真正调用诊断。WaitDelay=100ms 限制遗留 stderr 持有者造成的等待。bundled launcher 对实际打开对象校验后经 child fd3 执行。

专项测试不仅测试分类器，还通过生产 helper 运行真实 bwrap：wrapper 只注入预检 proc 拒绝诊断，随后真正调用系统 bwrap；两个目的地均验证 payload 一次、退出 23 保留、诊断静默。本机 bwrap 为 0.11.0，未真实安装 0.12，也未声称主机实际拒绝 proc 挂载；0.12 诊断通过注入验证。

## Linux 原生环境

详见 [round2-environment.json](round2-environment.json)。

| 项目 | 实测 |
| --- | --- |
| 工具链 | /usr/bin/go；go version go1.26.3 linux/amd64 |
| 内核 | Linux 6.18.12+kali-amd64，x86_64 |
| bwrap | /usr/bin/bwrap；bubblewrap 0.11.0 |
| SQLite | native linux/amd64 artifact，3.54.1 |
| unprivileged_userns_clone | 1 |
| max_user_namespaces | 126482 |
| AppArmor enabled | Y；当前进程 unconfined |
| apparmor_restrict_unprivileged_userns | sysctl 文件不存在 |
| unshare -Ur true | exit 0 |
| bwrap --unshare-user --unshare-pid --ro-bind / / --proc /proc -- /bin/true | exit 0 |

目标 Rust 3f65e9f13165d85d1b1fbe20ce06c55758f927b9；任务指定 Go 基线 e524c8d69，无法本地 git 验证。parity.json 仍记 rustBaseline=7ac6081623；parity/baseline.json 的 goStartCommit 为 3e02afa789243b32efc163b65bebae132aa4c25d。标识差异及参考附件 SHA256 均写入 [round2-run-manifest.json](round2-run-manifest.json)，未改全局认证或基线。

## 验证命令与真实输出

以下是最终源码下的命令；全部原始日志保留。退出码单列于 [validation-results.json](validation-results.json)，空日志不替代退出状态。

### 指定命令：均 exit 0

go test ./state ./sandbox ./network -count=1

[round2-core.log](round2-core.log)：

```text
ok   codex_go/state     5.976s
ok   codex_go/sandbox   0.045s
ok   codex_go/network   6.744s
```

go test ./appserver ./tui/... -run 'UserVerification|JobControl|Suspend|WorkingDirectory' -count=1

[round2-focused.log](round2-focused.log)，摘录：

```text
ok   codex_go/appserver   0.468s
ok   codex_go/tui/tea     4.358s
ok   codex_go/tui/tui     0.091s
```

其余包的通过/无匹配测试/无测试文件状态见完整日志，不算作每个包都有匹配断言。

CODEX_EXPECT_GOOS=linux CODEX_EXPECT_GOARCH=amd64 go test ./state -run 'SQLite' -count=1 -v

[sqlite.log](sqlite.log)，首轮运行有效且 SQLite 源码此后未改：

```text
=== RUN   TestNativeSQLiteArtifactPlatform
    sqlite_test.go:95: native artifact linux/amd64 embeds SQLite 3.54.1
--- PASS: TestNativeSQLiteArtifactPlatform (0.00s)
PASS
ok   codex_go/state   0.387s
```

### 专项、race、静态检查和交叉编译：均 exit 0

go test ./sandbox/linuxsandbox -run ProcMount -count=1 -v

[round2-proc.log](round2-proc.log)：

```text
real bwrap: /newroot/proc denial -> no-proc fallback; command ran once; exit 23 preserved
real bwrap: /proc denial -> no-proc fallback; command ran once; exit 23 preserved
PASS
ok   codex_go/sandbox/linuxsandbox   0.241s
```

go test -race ./sandbox/linuxsandbox ./tui/tea -run 'ProcMount|JobControl|Suspend|CtrlZ' -count=1

[round2-race.log](round2-race.log)：

```text
ok   codex_go/sandbox/linuxsandbox   1.253s
ok   codex_go/tui/tea                9.858s
```

go vet ./...：exit 0，无输出，[round2-vet.log](round2-vet.log)。

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test ./tui/tea -c -o /tmp/linux-align-tui-windows.test.exe：exit 0，无输出，[round2-windows-build.log](round2-windows-build.log)。这只证明 Windows 编译，不是原生运行通过。

新文件 gofmt 通过；model.go/linux.go 保留原 CRLF，消除 CRLF 后内容与 gofmt 输出完全一致。直接 gofmt -l 仍列出这两个 CRLF 文件，不声称该命令零输出。见 [round2-format.json](round2-format.json)。

首轮真实 job-control PTY/race 详细日志见 [jobcontrol.log](jobcontrol.log)；本节点 python3 可用，PTY 和真实 bwrap 场景都实际运行，没有 skip。

### 扩展/全量测试：exit 1，未隐藏失败

go test ./tui/... ./sandbox/linuxsandbox/... -count=1

完整 [round2-full-native.log](round2-full-native.log)。失败：

- tui/tea 的 8 项标签/路径断言：PendingKeyChordGuide 两项、PendingLeaderChordGuide 两项、KeyChordHUDOverlays 两项、ModelStatusLineAndTitleCommands、SideContextLabelHidesToggleWithoutBinding。主要是 Linux ^x/^c 与预期 ctrl+x/ctrl+c，以及工作目录 project 与预期 repo。
- TestExpandUnreadableGlobsWalksInProcessLikeRust：depth=1 返回 hidden/nested/top，预期仅 top。
- TestLinuxBwrapTwoHalfManagedGrantEndToEnd 的 proxy/proxy-grant：proxy_roundtrip=111。本次 /dev/vsock 已可打开，实际执行后失败；不是 skip。

使用 Go overlay 恢复本轮改动前文件重新验证，不改项目文件：前述 8 项 TUI 失败、代理 errno111 均可在恢复前实现时复现。见 [round2-baseline-tui.log](round2-baseline-tui.log)、[round2-baseline-proxy.log](round2-baseline-proxy.log)。此证据只覆盖上述具体失败，未把全仓库所有失败归因于基线。

go test ./... -timeout=120s

完整 [round2-all.log](round2-all.log)，244226 字节。失败包：app、appserver、appserverdaemon、doctor、execserver、mcp、model、parity、plugin、sandbox、sandbox/integrity、sandbox/linuxsandbox、shell、tool、tui/tea。appserver 的 TestRuntimeRouterAutoCompactsWhenTokenStatusRequiresIt 超时；app/plugin 等另有 Windows .git 路径问题。未扩散修改这些非本任务失败，也不声明整个仓库通过。

首轮 core 测试曾有 TestProbeSystemBwrapDoesNotWaitForDescendantHoldingStderr 失败，后续重复及最终 core 通过，未修此实现。sandbox/bwrap.go 的 readerDone 等待前 captured 快照存在可疑时序，保留为风险。

## #61898 未完成：具体缺口和最小计划

参考附件包含目标 launcher/linux_run_main/proxy_lifecycle/proxy_routing/tcp_net_setup 全文及 PR diff。该项不能只改 launcher 字符串或放一个 goroutine：

| 文件/函数 | 缺失结构 |
| --- | --- |
| sandbox/linuxsandbox/linux.go，execBubblewrap | 当前直接 exec；缺少通用 proxy supervisor → mount cleanup supervisor → bwrap 链、signal forwarding、退出状态传播、parent-death 与 reap。 |
| 同文件 prepareHostProxyRouteSpec | 路由准备立即启动 host bridge，JSON 存 UDS 路径；缺少可交接 listener/control FD 的所有权对象。 |
| 同文件 spawnProxyBridgeProcess/watchBridgeParent | 独立桥进程和父 PID 轮询不足以统一取消、完整失败回收、按路由 FD 预算、EMFILE 重试。 |
| sandbox/linuxsandbox/transparent_tcp_netns_linux.go | 已有透明 TCP 专用 re-exec，但未复用成 explicit proxy 与独立 mount cleanup 通用 supervisor；需在子阶段 namespace 准备并处理 credential/dumpability。 |

最小补丁计划：

1. 新增进程隔离的 re-exec supervisor 协议，明确 control/listener FD handoff、子进程组、信号、退出状态与 parent-death；不用普通 Go goroutine 模拟 Rust 的私有线程 FD table。
2. 将 host bridge 准备和启动拆开，纳入 supervisor 所有权；透明 TCP namespace 在 bwrap 子阶段建立，mount cleanup 放独立 supervisor。
3. 增加取消、父进程消失、启动失败回收、信号/退出码、FD 耗尽及代理稳定性的真实 Linux 集成测试；目前 proxy errno111 是现存阻碍，不能算作 #61898 验证完成。

Rust 的 CLOSE_RANGE_UNSHARE/CLONE_FILES 隔离不能直接放普通 Go goroutine：runtime 会复用线程，私有 FD table 会破坏其他 goroutine/runtime 对描述符的假设。此项超过 1–2 文件的局部修补范围。

## 依赖校验修复及剩余风险

Linux 首轮依据本节点下载和 sumdb 将 glamour v2.0.1 校验值从 h1:Vx2F81a4SBR5/IqAkDyeZ3mfuT7u+9AYxeXQ+J1n2Wc= 改为 h1:xl+r00A4aJWU0z8fgwKd9fQQ4rsphqGUzuEiXZP5n+c=；lead 未采纳此行，报告其节点镜像字节与 sumdb 不一致。不能将跨节点差异简化为原仓库校验值错误。此次不再修改 go.sum；追加取证 [module-provenance.json](module-provenance.json)：GOPROXY=https://goproxy.cn，GOSUMDB=sum.golang.org，GOFLAGS 为空，go mod download -json 退出0并报告缓存 Sum=xl+r00A4…。该命令可命中缓存，不证明当前代理新下载字节或跨节点来源一致。

尚未覆盖：其他 Unix 原生行为、Windows undo 原生执行、真实 bwrap 0.12/真实 proc-denial 主机、Rust 全套光标恢复、#61898 supervisor 架构和全量测试失败项。proc 预检每次增加一次最小 sandbox 启动，最坏额外等待 10 秒；未做缓存。此次证据属于定向契约与本机 PTY/bwrap 验证，不提升 SDK、真实模型或全局 parity 认证。

## lead 后续请求的精确验证

go test ./sandbox/linuxsandbox -run 'Bwrap|Proc|Mount' -count=1：exit 1，codex_go/sandbox/linuxsandbox 0.256s；完整输出 [lead-request-proc.log](lead-request-proc.log)。失败为 glob depth 与 proxy/proxy-grant errno111；Proc 还会匹配 Process 名称，未把混合测试失败隐藏为通过。此前 ProcMount 专项全部通过，见上文。

重新实测 bwrap 0.11.0、unshare -Ur true exit0、真实 bwrap userns+PID namespace+proc mount exit0，见 [lead-request-bwrap-environment.json](lead-request-bwrap-environment.json)。三文件最小补丁 [61900-only.patch](61900-only.patch) 对保存的修改前 linux.go 执行 git apply --check 退出0。实现单独放 proc_mount_linux.go，未污染现有跨平台 help probe；生产行为与请求相同。该补丁无 go.sum 改动。
