# 缺口对齐工作流（2026-11-08 起）

本文件是所有参与对齐的 Agent 的共同约定。开始工作前先读它，再读 `.agents/skills/djalign/SKILL.md` 与 `.agents/skills/update/SKILL.md`。

## 目标

把 Go 版 codex 与 Rust 版 codex 的剩余缺口收敛：

1. 上游 delta：以 `55897425e8`（2026-11-06，Go 已覆盖的最后一条）为界，`openai/codex` main 还有 93 条提交未判定（清单见 `docs/alignment_gap_analysis_2026_11_08.md` 附录 A）。
2. 结构性硬缺口：Windows 原生 MXC、macOS Secure Enclave 用户验证、agent_navigation 关闭代理语义、Guardian V3 证据链、Cloud 服务端所有权收尾。
3. 控制面：`parity/commits.json` 只到 `67f0994b`，落后工程 275 条。

## 仓库与工具约定

- 项目内容只通过 `project_list` / `project_read` / `project_fetch` 读，通过 `project_write`（文本）或 `project_file_sync`（二进制、大文件）写。本地直接改文件不会同步。
- 项目镜像里没有 `.git`。不要尝试 `git commit`；需要版本记录时写进 `update/` 下的轮次记录。
- 写入前先 `project_read` 拿最新 etag，内容会随项目演进变化。
- 写入冲突时不要硬覆盖：停下，把冲突文件和你打算做的改动写进回复，交给 `/root` 合并。
- 大文件（tar/zst/zip）用 `project_file_sync`；单个大目录用 `project_dir_sync`。

## Lane 划分与文件边界

| Lane | 节点 | 负责路径 | 主要任务 |
| --- | --- | --- | --- |
| W | local_windows_amd64 | `sandbox/mxc/**`、`sandbox/**/*windows*`、`execserver/**windows*`、`windowsandboxservice/**` | Rust 源码打包分发；MXC 原生绑定；Windows 沙箱覆盖解析（#60042） |
| M | macos | `appserver/user_verification*.go`、`app/desktop_darwin.go`、`doctor/**`、`sandbox/**/*darwin*`、`keyring/**` | macOS 用户验证（Secure Enclave）；doctor macOS 行；darwin 构建与测试 |
| L | linux_node（`/root` 主持） | `mcp/**`、`model/**`、`turn/**`、`runtimeauth/**`、`appserver/**`（除 user_verification）、`tui/**` | 上游 93 条的判定与落地 |
| C | linux_node（账本 lane） | `parity/**`、`README.md`、`need_aligin.md`、`update/**` | 台账回写、基线重锚、`#57267` 重审、文档断链 |

跨 lane 需要改别人的文件时：在回复里写清楚"需要谁改哪个文件的哪一段"，不要直接改。

共享文件（`config/**`、`app/**`、`features/**`、`parity.json`）改动前先 `project_read`；冲突即停。

## Rust 源码分发协议

Windows 节点本地已有 Rust 仓库（候选路径 `D:\qax\reagent\dev\git\codex`）。

1. Windows lane 确认 `git rev-parse HEAD`，并确认本地是否含 `55897425e8`（没有就只 fetch 这一个提交）。
2. 产出 `git archive --format=tar 55897425e8 | zstd -o rust-codex-55897425e8.tar.zst`（不含 `.git`）。
3. 通过 `project_file_sync` 落到 `artifacts/rust-codex-55897425e8.tar.zst`；超过 150MB 就按 40MB 切片（`.part-00` …）加一个 `SHA256SUMS`。
4. 消费方：`project_fetch` 该 artifact，`zstd -d | tar x` 解到 `~/rust-baselines/55897425e8`（**不要解到项目目录**），导出 `CODEX_RUST_ROOT=~/rust-baselines/55897425e8/codex-rs`。

在 artifact 到位前，可以先用 `curl https://github.com/openai/codex/commit/<sha>.patch` 拿单条补丁做判定；只有判定为需要落地时才动 Go 代码。

## 单条上游提交的处理标准

1. 判定四选一：`ported` / `aligned`（Go 已有等价行为，补测试锁住）/ `not_applicable`（结构性差异，写清理由）/ `partial`（落地一半，写清残余）。
2. 判定为需要改代码的，改完至少跑相关包测试；跨包改动跑 `go build ./...`。
3. 每个 `ported` / `aligned` 都要有测试名或可复现命令作为证据。
4. 行为差异必须沉淀契约（`parity/contracts/manifest.json` 条目 + verifier），否则按 `djalign` 规则不允许关闭。
5. 每条结论写进 `update/plan_2026_11_08_<lane>.md`：提交 sha、主题、判定、证据命令、残余。

## 回复格式

回 `/root` 时给：本轮完成的提交 sha 列表与判定、改了哪些文件、跑过什么命令及结果、剩余项、需要跨 lane 配合的点。不要贴大段代码。

## 跨项目交付约定（2026-11-08 实测补充）

派生 Agent 的 `project_*` 工具绑定在各自的工作区项目上，**不会**写入 `/root` 主持的项目（实测：账本 lane 的项目 id 为 `project-krozjtbfkzjz`，其 `parity/commits.json` 改动在主项目里不可见）。因此：

- 同节点 Agent（linux_node）：直接把改动落在本地工作区，回复里给 `pwd` 与文件清单，由 `/root` 复制并经 `project_file_sync` 合并。
- 远程节点 Agent（Windows / macOS）：用 `send_a2a_message` 的 `attachments` 附带改动文件（`local_path` 指向其工作区绝对路径，文件名保留相对路径语义，如 `sandbox__mxc__run_windows.go`），或打包 zip + `SHA256SUMS`。
- 任何 Agent 都不要再把“已 project_file_sync 到 Master”当作 `/root` 可见的交付证据。
