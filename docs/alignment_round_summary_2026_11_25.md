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
| `9eb91f2d8` `6b310f1fe` `decb18014` `cb8bee7f0` `0684cf0aa` `6976db562` | 缺口文档与台账修订：开项计数、sidebar contract 的阻塞原因、linux/darwin 原生 SQLite 证据、parity 门结论、macOS Info.plist 差异、glamour 校验分叉风险 | — |

台账现状：3,671 complete / 51 equivalent / 845 not_applicable / 11 partial / 17 blocked_precondition；
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
| `go test ./appserver -count=1` | 228.9s，仅 2 个 Windows 长路径 git 用例失败（环境） |
| `GOOS=linux` / `GOOS=darwin` `go vet ./tui/tea ./doctor` | 通过（unix 实现可编译） |
| `go vet ./sandbox`、`GOOS=linux go vet ./sandbox/linuxsandbox` | 均通过（含 #61900 新代码） |
| `go test ./doctor -count=1` | ok 3.5s（走非 darwin 分支） |
| `go test ./sandbox -run TestDrainBwrapProbeStderrKeepsLauncherOutput -count=20` | ok，1.0s |

已知环境性失败（与改动无关）：`plugin` 的 `TestMarketplaceUpgradeReusesInstalledGitObjectsLikeRust`、
`TestAutomaticGitUsesTrustedRepositoryAndCleansUp`（Windows 长路径 / `$GIT_DIR too big`）；
`mcp` 的 `TestDesktopSocketMCP*`（Windows AF_UNIX）；`appserver` 的两个 plugin git 用例（长路径）。

## 节点原生证据

- Linux（go1.26.3 linux/amd64）：`go test ./state ./sandbox ./network` 通过；`go test -race ./tui/tea -run 'JobControl|Suspend|CtrlZ'` 通过；真实 PTY 覆盖 inline/alternate、>1s 暂停、后台 SIGCONT、无第二次 SIGCONT 的 fg；SQLite artifact `linux/amd64 embeds SQLite 3.54.1`。
- macOS（Go 1.27.1 darwin/amd64，CGO=1）：UserVerification 与 doctor 指定测试通过；`go build ./cmd/codex` 产出 x86_64 Mach-O；SQLite artifact `darwin/amd64 embeds SQLite 3.54.1`；硬件 readiness 为 `DeviceSupported=false`、`com.apple.LocalAuthentication` -6 `Biometry is not available.`，故 Secure Enclave 交互签名未验证。

## 仍在进行 / 未实现

| 项 | 状态 | 负责 |
| --- | --- | --- |
| #61900 proc-mount 预检与 legacy fallback | 已完成并合并（残差：节点无真实 0.12 proc-denial 环境） | linux-align |
| darwin run-plan 断言复核、Seatbelt 三处失败定性、`doctor/desktop_darwin.go` 的 plutil 改法 | 已给对照源码与最小改法，等节点回报 | darwin-align |
| #61909 item-boundary interrupt / #61916 remote compaction summary | 已派给新 Agent，参考资料 `.refs/rust-3f65e9f1/subagent/` | subagent-align |
| #61898 proxy supervisor / mount cleanup 交接 | 仅出方案：Go 是进程内 `unix.Exec`，需 re-exec 进程隔离 | 未排期 |
| #61896 rollout trace 写出器、#61385 Code Mode typed stream、#60075 host service-tier、#61945 `InheritedSessionId`、#61946 Guardian nested approval、#61919 侧栏图片预览 | Go 侧缺对应抽象，需先补底层 | 未排期 |

## 风险

1. `charm.land/glamour/v2 v2.0.1`：sumdb 记录 `xl+r00A4…`，两个公共 proxy 实际给的是 `Vx2F81a4…`（139064 字节，
   sha256 `C4A98F4E…C039`）。该版本被重新发布过，开启 sumdb 的环境会失败；仓库保留 `Vx2F81a4…`，节点不得修改。
2. macOS 无 Touch ID 硬件，Secure Enclave 端到端签名仍缺真机证据；该机 arm64 未测，且存在 13.0→12.0 链接警告。
3. 六个原生平台的 SQLite 矩阵只观测到 3 个 amd64 单元；GitHub Actions 仍受账户计费限制。
