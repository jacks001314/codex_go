# syncl1 第三轮域扫描 — `cli/` + `chatgptapi/` + `utils/` + `analytics/`（pin `d83bb540`）

## 0. 环境自检
- Go 仓 `/home/jacks/jacks_dev/codex_go`；`origin/main = 8b2453a9ee619e7a84a95165b3e2474af082d71f`（sync641，已 fetch 对齐）。
- Rust 枚举 pin = `d83bb540ec64bf6b009bca0283b0be91ea33f26a`（上游新增 1 笔 `#51678`）。
- parity 用 Rust 检出保持冻结 pin `5b0b253035`（未动）。
- 本单**只读**：0 补丁 / 0 commit / 0 push / 0 ref 移动；临时树用 `git worktree remove --force` 回收。

## 1. 方法与域映射
- 窗口：**强制** `git log -200 d83bb540`（200 笔，其中 ≤5 文件 103 笔）；**扩展** `git log -1000 d83bb540`（1000 笔，其中 ≤5 文件 470 笔）作为「更早未处置」补扫。
- Rust→Go 域映射（以历史落点校准）：
  | Go 域 | Rust 来源 |
  |---|---|
  | `cli/` | `codex-rs/cli/` |
  | `chatgptapi/` | `codex-rs/chatgpt/`、`cloud-tasks/`、`cloud-tasks-client/`、`backend-client/`（校准：`#49147`=cloud-tasks→`chatgptapi/cloud_tasks_normalize_test.go`；`#50442`=backend-client→`chatgptapi/cloud_tasks.go`） |
  | `utils/` | `codex-rs/utils/`、`ansi-escape/`、`git-utils/`、`terminal-detection/`（校准：`#51482`=utils→`utils/pathuri.go`） |
  | `analytics/` | `codex-rs/analytics/`（**Go 无 `analytics/` 目录**，见 §5；载体面为 `appserver/*analytics*.go` + `config/analytics.go`） |
- 判定基准：Go `origin/main` 快照 + `git grep` 命中原文 + Go 侧 sync 提交（`git log --grep '#<PR>'`）。

## 2. 强制窗口（200 笔）in-domain ≤5 文件：9 条，**全部已定案/已落地**
| # | Rust sha | 主题 | 分类 | 证据（Go 载体 / sync） |
|---|---|---|---|---|
| `#51460` | `44984d2081` | Retry realtime sideband attachment | 已落地 | sync376 `2bfc8640`（载体 `codexapi/`+`realtime/`，本 4 域外） |
| `#51440` | `f6cf05af1d` | Honor Retry-After in WebSocket error events | 已落地 | sync359 `867cff9e`（`codexapi/`，域外） |
| `#50803` | `8f82b8a31c` | Managed daemon for eligible remote-control launches | 已落地 | sync565 `adee1b30`（`cli/cli.go:500`） |
| `#50700` | `b172810921` | Windows remote-control socket directory | 已落地（Windows-only） | sync354 `0c495c5d`；Rust 改动为 `#[cfg(windows)]` |
| `#50558` | `19e554bb70` | Avoid reading cwd when resolving absolute paths | 已定案 N/A | `d33b864c plan: mark #50558 N/A`；Go `filepath.Abs` 对绝对路径**短路不读 cwd**（见 §5） |
| `#50525` | `b65ab465ce` | Reject unknown TUI keys in strict config validation | 已落地 | sync533 `42fbf7f3`（`config/config.go` validateKnownTuiConfigFields） |
| `#50470` | `be48ae396e` | JSON overhead when truncating MCP tool results | 已等价 | Go `rollout/persist_policy.go:65 truncateMCPResultValue` 已含渐进预算（注释引 `#50458/#50470`）；载体 `rollout/`（域外） |
| `#50442` | `3629508849` | Native USD amounts in thread usage responses | 已落地 | sync408 `0269d580`（`chatgptapi/cloud_tasks.go` + 测试） |
| `#50418` | `44dd77b71e` | Retry-After headers in failed Responses events | 已落地 | sync344 `121e834e`（`codexapi/`，域外） |

## 3. 扩展窗口（1000 笔）in-domain ≤5 文件：47 条；未处置 16 条（已剔除队长排除项 `#49624` 等）
> 47 条中 31 条已在 Go 侧有 sync 落点（handled），下表只列**未处置**的 16 条全量判定。

| # | Rust sha | 主题 | 域 | 分类 | 判据（Go 载体 file:line / 0 命中原文） |
|---|---|---|---|---|---|
| `#49798` | `c538fbabe5cb` | Share cached exec-server env info with Arc | cli(测试)+exec-server | 无载体（语言级） | Go 无 Arc；`execserver/client.go:51 environmentInfoCache` 为值语义；载体域外 |
| `#49683` | `6996cde697b2` | Managed feature gate for in-app voice | cli(测试)+features | 域外（`features/`） | cli 仅 `cli/tests/features.rs` 测试面；生产面在 `features/`，非本 4 域 |
| `#48213` | `58670eeac4b0` | Isolate executable fixture copies in CLI tests | cli | 纯测试型 | 仅 `cli/tests/*`；Go `cli/*_test.go` 无对应夹具设施 |
| `#47358` | `722dae7afd75` | Color paths/URLs in `codex doctor` by status | cli | 域外（`doctor/`） | Go doctor 检查体在 `doctor/doctor.go`；`cli/doctor.go` 仅报告类型；`doctor/` 归 syncw2 |
| `#47330` | `3b7a8520efde` | Identify failed SQLite databases in `codex doctor` | cli | 域外（`doctor/`） | 同类语义已在 `doctor/doctor.go:4365`（SQLite 修复指引）；未逐项核验 |
| `#47143` | `517f51a6f710` | Extract exec-server CLI startup into a module | cli | 已等价（结构性重构） | 纯文件搬迁（`main.rs`→`exec_server_command.rs`）；Go `cli/cli.go:2349 parseExecServer` 已实现同一起动面 |
| `#46981` | `ffbd30aeb71a` | Canonicalize workspace trust key in doctor config test | cli | 纯测试型 | 仅 `cli/tests/doctor_path_safety.rs` |
| `#46524` | `5701a576ccc3` | Retry busy executable launches in packaged daemon tests | cli | 纯测试型 | 仅 `cli/tests/app_server_daemon.rs` |
| `#49416` | `ab84d71f5767` | Omit payloads from multiline ANSI warnings | utils(ansi) | 无载体 | `git grep 'ansi_escape_line'`=**0**、`'expected a single line'`=**0**；Go `utils/ansi_escape.go:48 ANSIFirstLine` 无 warn/日志路径 |
| `#49300` | `a6f09397aa59` | Compact the inline hidden tag buffer once per chunk | utils(stream-parser) | 无载体 | `git grep 'InlineHiddenTagParser'`=**0**；Go 为一次性 `eventmap/eventmap.go:526 stripInlineHiddenTag`，无「每分隔符 drain」性能面 |
| `#48238` | `4b9e0cc77f26` | Suppress console windows for local Windows MCP servers | utils(pty) | 已等价（Windows-only） | Go `appserver/console_window_windows.go:17 suppressChildConsoleWindow`（CREATE_NO_WINDOW，源自 `#48483`）；Linux 无法 RC |
| `#47856` | `df06bb054cc1` | Initialize media estimates outside the global cache lock | utils(cache/audio) | 无载体 | `git grep 'media_estimate'`=**0**、`'MediaEstimate'`=**0**；Go `utils/lrucache.go` 无 media-estimate 初始化面 |
| `#47704` | `6989c6548b37` | Fix spawn flag typing (utils/pty) | utils(pty) | 域外 | Go 无 `utils/pty`；进程面在 `processhardening/`（非本 4 域） |
| `#47613` | `15922a50aa5f` | Expand Linux spawn-helper lifecycle test coverage | utils(pty) | 纯测试型/域外 | 仅 `utils/pty/src/spawn_helper_tests.rs` |
| `#47603` | `897b7ae285b2` | Add a reap-only drop policy for child processes | utils(pty) | 域外 | `git grep 'child_reaper'`=**0**、`'ReapOnly'`=**0**、`'reap_only'`=**0**；Go 无此进程收割面 |
| `#49489` | `bcd6d9ab6b9f` | Regression coverage for account switches between analytics batches | analytics | 纯测试型 | Rust 仅新增 `analytics/src/client_tests.rs`（+108，改 2 行）；Go 无对应 analytics 客户端测试面 |

`#49624`（`7219fd735b`，cli 远程会话 server auth）= 队长已列「别重复报」，此处不判定。

## 4. 真缺口候选：**0 条**（4 域全量）
- **cli/**：窗口内 ≤5 文件条目 7 条已落地 + 8 条未处置中，**0 真缺口**（纯测试 3、域外 2、结构性重构等价 1、语言级无载体 1、features 域外 1）。
- **chatgptapi/**：1000 笔窗口内 in-module ≤5 文件 2 条（`#49147`、`#50442`）**均已落地**，未处置 0 条 → 该域无真缺口。
- **utils/**：7 条未处置中 0 真缺口（无载体 3、纯测试/域外 3、已等价 Windows-only 1）。
- **analytics/**：Go 无 `analytics/` 目录；Rust `analytics/` 在窗口内的 ≤5 文件条目仅 `#49489`（纯测试）与已裁决项 → 无真缺口。

## 5. 域级 0 命中证据（证明扫过）
命令均在 Go `origin/main`（`8b2453a9`）快照上实跑，原文：
```
$ git ls-tree -d --name-only origin/main | grep -x 'analytics'
(无输出: 0 hits)                       # Go 无 analytics/ 目录
$ git grep -c 'ansi_escape_line' origin/main -- '*.go'          -> 0 hits
$ git grep -c 'expected a single line' origin/main -- '*.go'    -> 0 hits
$ git grep -c 'InlineHiddenTagParser' origin/main -- '*.go'     -> 0 hits
$ git grep -c 'media_estimate' origin/main -- '*.go'            -> 0 hits
$ git grep -c 'MediaEstimate' origin/main -- '*.go'             -> 0 hits
$ git grep -c 'child_reaper' origin/main -- '*.go'              -> 0 hits
$ git grep -c 'ReapOnly' origin/main -- '*.go'                  -> 0 hits
$ git grep -c 'reap_only' origin/main -- '*.go'                 -> 0 hits
```
代表性正向命中（证明域内已扫到载体）：
```
Go chatgptapi/ landings: (#38217)(#38242)(#38270)(#49147)(#50442)
Go utils/ 代表文件: utils/ansi_escape.go / utils/pathuri.go / utils/outputtrunc.go / utils/lrucache.go
Go cli/   代表载体: cli/cli.go:2349 parseExecServer、cli/doctor.go（报告类型）、cli/dispatch.go
Go `filepath.Abs` 绝对路径短路（#50558 已等价依据）: utils/`*`、config/、cli/ 共 15+ 命中（见 §2）
Go 进程面载体: processhardening/（非本 4 域）
```

## 6. 未决 / 备注
- `#47358` / `#47330`（doctor 着色 / SQLite 库标识）若确需落地，载体在 `doctor/doctor.go`（**syncw2 域**），不属本 4 域；建议转派。本轮未逐项核验 Go doctor 的输出着色是否已覆盖。
- `#48238`、`#50700` 为 Windows-only，Linux 节点无法产出值级 RC。
- 扩展窗口（1000 笔）为我自主补扫，超出队长强制 200 笔窗口；结果与强制窗口一致：**0 真缺口**。
