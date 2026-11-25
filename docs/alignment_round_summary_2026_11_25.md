# 本轮 Go/Rust 对齐变更与验证总览（2026-11-25）

范围：Go 仓库 `D:\qax\reagent\dev\codex_go`（起点 `e524c8d69`）对齐 Rust `3f65e9f1`。
详细缺口分析见 `docs/alignment_gap_analysis_2026_11_25.md`；节点原始证据见 `deliverables/linux-align/`
与 `deliverables/darwin-align/`；Rust 对照源码见 `.refs/rust-3f65e9f1/` 与
`deliverables/darwin-align-rust-reference/`。

## 已合并的提交

| 提交 | 内容 | 关闭的台账项 |
| --- | --- | --- |
| `d862c1348` | MCP catalog 以 `ServerRegistrationKey(name, env)` 为键；`/cd` 跳过已关闭 agent；eval 归档用独立 purpose 的签名密钥 | #60905、#40625、#63528 |
| `1ac73af00` | macOS provider 选择测试拆分为「选择断言」与「readiness 探测」；新增 `!darwin \|\| !cgo` 反向断言；修掉把 darwin 当 unsupported 的两个 run-plan 测试 | #43624（证据，仍 blocked） |
| `63ef48240` | 协议常量白名单补 `account/voiceSettings/read\|write`；precomputed export pin 更新到 `3f65e9f1` 实际字节 | 认证检查修正 |
| `974090d28` | Ctrl-Z 挂起：`WithFilter` 接管 `SuspendMsg`，`/dev/tty` + `TIOCGPGRP` 预检，进程组 SIGTSTP/SIGCONT，拿到前台才 restore；Windows 保留 composer undo | #63520 |
| `9f6dce5a6` | macOS Keychain 统一 `kSecAttrLabel`；doctor 改走 `plutil -convert json`（5s/64KiB）并补 binary plist 等测试 | #43624 证据 |
| `74491bfd8` | Seatbelt 把 scratch 排除、root unlink 与 PROTECTED_WRITE deny 统一延后到所有 allow 之后 | 修复 /private/tmp 下 .git 被重新放开 |
| `54cc20024` | bwrap 探针先抽干 stderr 再判定（对齐 Rust 读到 WouldBlock） | 修复偶发把坏 launcher 判成可用 |
| `cbafa765b` | #61900 proc-mount 预检与 legacy fallback（Linux 节点实现） | #61900 |
| `46ea8874a` `77084e84d` | bwrap 探针 drain 截止时间改成当前时间 +50ms（原来的 `time.Now()` 会直接 deadline），并记录全量跑出的临时路径产物 | 修复 drain 用例 50/50 失败 |
| `9727927c4` `3d361033e` `4f76a3c3a` `8057cf9f1` | Windows 测试环境修整：no-network 环境用例的 homedir 缓存隔离、mxc 运行日志先关闭再加载、子 agent 结束前 `CloseAgent`；并把 #61945 的台账证据改成「能力已在 store 的 fork API，缺的是 host 协议面与拒绝守卫」 | 消除 TempDir 清理竞态、让阻塞证据可执行 |
| `f25809c92` | `TestSubagentSpawnServiceTierLikeRust` 在 `CloseAgent` 之后等 `WaitForTurnWorkers` 再返回：只关 agent 仍会让子 agent 的 turn worker 在 TempDir 清理时继续写盘 | 该用例 3/20 偶发失败 → 30/30 通过 |
| `863c88978` | #61919 侧栏图片预览与生成图在回答后呈现（darwin-align 实现）：PNG/JPEG/GIF/静态 WebP 解码，复用 Kitty / Kitty local-file / Sixel 输出与清理生命周期；远程图走授权 `fs/readFile` 字节；不支持终端、raw、Zellij、旧 iTerm、未测量 Sixel、tmux 无透传降级为路径加元数据提示；生成图按 thread/turn 排队，在完成/中断/失败后呈现，恢复历史去重排序；预览同时校验 requested 与 canonical 路径，顺带修掉 Darwin deny-read 回归 | 两个 sidebar contract 仍 partial（尺寸与文案残差） |
| `ac76dda16` | 补入 macOS 节点验证过的 darwin PTY 测试：真实 controlling PTY 的 SIGTSTP/SIGCONT、TIOCGPGRP 前台门控、CPR（`ESC[6n`）输入不会落进 composer，以及 darwin 撤销绑定保留 Ctrl-Z 给挂起 | #63520 的 darwin 侧证据进仓库 |
| `b2c815beb` | 采用带 `RequestCursorPosition` 的 `ResumeMsg`，并记录 inline viewport realign 未移植 | #63520 残差 |
| `d30ba1b79` | Seatbelt 只对 depth-one 系统别名（`/tmp`、`/var/tmp`）做归一：scratch 排除、PROTECTED_WRITE、protected ancestor 与 symlink-home 覆盖判断；嵌套可变软链仍需显式 opt-in | 关闭 macOS 默认 TMPDIR 下的 3 个 sandbox 失败 |
| `6ce6cb67b` | #61909/#61916 item-boundary 采样中断与 remote compaction summary 保留（subagent-align 实现）：工具取消与采样取消分离、保留已完成前缀、aborted 工具输出、follow-up 串行、mail 排队；默认关闭 | #61909、#61916 |
| `9eb91f2d8` `6b310f1fe` `decb18014` `cb8bee7f0` `0684cf0aa` `6976db562` | 缺口文档与台账修订：开项计数、sidebar contract 的阻塞原因、linux/darwin 原生 SQLite 证据、parity 门结论、macOS Info.plist 差异、glamour 校验分叉风险 | — |

台账现状：3,674 complete / 51 equivalent / 845 not_applicable / 11 partial / 14 blocked_precondition；
4 个 domain、4 个 contract 仍 open；`certificationReady: false`。

## 本机验证结果（Windows，CGO_ENABLED=0）

| 命令 | 结果 |
| --- | --- |
| `go test ./parity -count=1`（`CODEX_RUST_ROOT=...\codex\codex-rs`） | ok 105.3s，整包通过（含 ContractVerifierAudit / CrossPlatformBuilds / GoVet 三项） |
| `go vet ./...` | VET=0 |
| `go build ./cmd/codex` | 退出 0，产物 125,462,016 字节 |
| `go test ./tui/... -count=1` | 全部 ok |
| `go test ./sandbox/... -count=1` | 全部 ok |
| `go test ./tui/tea -count=1` | ok 42.5s |
| `go test ./appserver -count=1`（`CODEX_RUST_ROOT` 指向 3f65e9f1） | ok 480.2s，整包通过，含 `TestPrecomputedExportArtifactsMatchTargetRustCommit` |
| `go test ./turn ./agent ./state ./exec ./config -count=1` | 全部 ok（state 31.3s） |
| `go test ./turn ./appserver ./exec ./config -run '^TestSamplingInterrupt' -count=1` | 全部 ok |
| `go test ./... -count=1` | 141 个包 ok，2 个包失败；失败均为并发负载下的时序敏感用例（见下） |
| `GOOS=linux` / `GOOS=darwin` `go vet ./tui/tea ./doctor` | 通过（unix 实现可编译） |
| `go vet ./sandbox`、`GOOS=linux go vet ./sandbox/linuxsandbox` | 均通过（含 #61900 新代码） |
| `go test ./doctor -count=1` | ok 3.5s（走非 darwin 分支） |
| `go test ./sandbox -run TestDrainBwrapProbeStderrKeepsLauncherOutput -count=20` | ok，1.0s |

跑全量 `go test ./...`（136 个包 ok，7 个包有失败）时确认：把 `TMP`/`TEMP`/`GOTMPDIR` 指向本工作区
（路径里含 `.syntropy-run-...` 这种带连字符与点的目录）会制造一批**假失败**——Cursor 工程名解码、arg0 alias home
判断都依赖路径形态。改用 `C:\qaxtmp` 这类无连字符短路径后，`./config ./cli ./appserverdaemon ./gitutil ./review ./plugin`
全部转绿，`./appserver` 只剩一个 Windows TempDir 清理竞态（`TestSubagentSpawnServiceTierLikeRust`，已在测试结束前
`CloseAgent` 关闭子 agent 修掉）。建议后续统一用短且无连字符的临时根。

全量 `go test ./...` 的另一类失败是**并发负载下的时序敏感**，不是产品回归：`appserver` 的 `TestEnvironmentObserveConnectionAttemptsCancelledOnceWhenReplacedLikeRust/replace` 单独跑 20/20 通过、`go test ./appserver` 整包也通过，只在 141 个包并行时偶发把 attempt 记成 `failure`（reload 晚于客户端自身超时）；`mcp` 的 `TestMCPDiscoverLocalStdioHandshakeRecoveryLikeRust/legacy-slow-initialize` 是 2 秒自设 deadline 被抢占。`TestSubagentSpawnServiceTierLikeRust` 的 TempDir 竞态已用 `f25809c92` 修掉（3/20 → 30/30）。

仍存在的真实环境性失败：`plugin` 的 `TestMarketplaceUpgradeReusesInstalledGitObjectsLikeRust`
（Windows 长路径）、`mcp` 的 `TestDesktopSocketMCP*`（Windows AF_UNIX）。这两项需要长路径开关或 Unix 域套接字支持。

## 节点原生证据

- Linux（go1.26.3 linux/amd64）：`go test ./state ./sandbox ./network` 通过；`go test -race ./tui/tea -run 'JobControl|Suspend|CtrlZ'` 通过；真实 PTY 覆盖 inline/alternate、>1s 暂停、后台 SIGCONT、无第二次 SIGCONT 的 fg；SQLite artifact `linux/amd64 embeds SQLite 3.54.1`。
- macOS（Go 1.27.1 darwin/amd64，CGO=1）：UserVerification 与 doctor 指定测试通过；`go build ./cmd/codex` 产出 x86_64 Mach-O；SQLite artifact `darwin/amd64 embeds SQLite 3.54.1`；硬件 readiness 为 `DeviceSupported=false`、`com.apple.LocalAuthentication` -6 `Biometry is not available.`，故 Secure Enclave 交互签名未验证。

## 仍在进行 / 未实现

| 项 | 状态 | 负责 |
| --- | --- | --- |
| #61900 proc-mount 预检与 legacy fallback | 已完成并合并（残差：节点无真实 0.12 proc-denial 环境） | linux-align |
| darwin run-plan 断言复核、Keychain label、`plutil` 读 Info.plist、Seatbelt 系统别名 | 已合并（`1ac73af00` `9f6dce5a6` `74491bfd8` `d30ba1b79`）；macOS 节点报告 `CGO_ENABLED=1 go test ./sandbox` 0.985s 通过 | darwin-align 已完成 |
| #61909 item-boundary interrupt / #61916 remote compaction summary | 已合并（`6ce6cb67b`），Windows 本机整包验证通过 | subagent-align 已完成 |
| #61898 proxy supervisor / mount cleanup 交接 | 仅出方案：Go 是进程内 `unix.Exec`，需 re-exec 进程隔离 | 未排期 |
| #61896 rollout trace 写出器、#61385 Code Mode typed stream、#60075 host service-tier、#61945 `InheritedSessionId`、#61946 Guardian nested approval、#61919 侧栏图片预览 | Go 侧缺对应抽象，需先补底层 | 未排期 |

## 风险

1. `charm.land/glamour/v2 v2.0.1` 已统一到 sumdb 记录的制品：`h1:xl+r00A4…`（139,064 字节 zip）。此前 go.sum 锁的是
   2026-11-07 缓存里的旧制品 `Vx2F81a4…`（3,793,168 字节，含 `styles/gallery` 图片），项目镜像用的是新值，两边不一致
   才导致节点反复 checksum mismatch。现在仓库、镜像与本机 module cache 都是 `xl+r00A4…`，`go mod verify`、`go build ./...`
   与 `go test ./tui/...` 全部通过。重新发布的那一版把 gallery 图片换成了占位文件（239 个文件不变，4.66MB → 0.55MB），
   代码里没有 `go:embed` 依赖它们。
2. macOS 无 Touch ID 硬件，Secure Enclave 端到端签名仍缺真机证据；该机 arm64 未测，且存在 13.0→12.0 链接警告。
3. 六个原生平台的 SQLite 矩阵只观测到 3 个 amd64 单元；GitHub Actions 仍受账户计费限制。
