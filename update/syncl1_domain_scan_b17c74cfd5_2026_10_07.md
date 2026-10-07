# syncl1 域内候选扫描（新 pin `b17c74cfd5`）— round 87

本单 = **只读候选扫描**（0 补丁 / 0 commit / 0 push / 0 ref 移动）。
域定义：`parity/ chatgptapi/ config/ utils/ realtime/ execserver/ clients/`（可微调）；避开 `network/` 与 `appserver/runtime_router.go` / `appserver/turn_runtime.go` / `tui/state.go` / `sandbox/windowssandbox/`。

## 0. 环境自检
- Go 仓：`/home/jacks/jacks_dev/codex_go`（存在）
  - `origin/main = 185d03c933086ddd2b0a1f498fdcf7f9d2683d70`（与队长给定基线一致）
  - 本地 `main = a32e1c35facdbfec20887cce46da126d6ac0e68b`（为 origin/main 的祖先，落后 48 笔；本地无独有提交；工作树仅 1 个未跟踪脚本 `scripts/loc_report.sh`，与本单无关）
  - `git remote -v`：origin = `https://github.com/jacks001314/codex_go.git`
  - 下文所有 Go 载体一律取自 `origin/main`（= `185d03c9`）快照
- Rust 上游仓：`/home/jacks/jacks_dev/codex`（origin = openai/codex，已 fetch）
  - 新 pin `b17c74cfd5ebb39fe70ffaff78de198120278636`、上一 pin `a6baf8867cb4c9726213c0884a5c8b11f0cfd8bf` 均在 origin/main

## 1. pin 区间 `a6baf8867c..b17c74cfd5`（5 笔）
命令：`git -C /home/jacks/jacks_dev/codex log --oneline a6baf8867cb4c9726213c0884a5c8b11f0cfd8bf..b17c74cfd5ebb39fe70ffaff78de198120278636`

| Rust sha | PR | 主题 | 分类 | 级别 | Go 载体 / 判据 | 避让面 |
|---|---|---|---|---|---|---|
| `5b0b253035` | #51627 | Stabilize Guardian snapshot prefixes | 无载体型（Guardian 簇） | L（7 文件 +343/−46） | Go 无 `guardian-context` 等价模块；`state/guardian_retained_context.go` 为简化实现 | 否 |
| `a513012869` | #51642 | Fix retained context handling for typed section content | 无载体型（= 已裁定 N/A） | S（2 文件） | 同上，仅 Rust `guardian-context/src/retained_instructions.rs` | 否 |
| `37eaae6eeb` | #51650 | Require hostname authorization before proxy DNS lookups | **已落主** | — | sync630 `89832492`（`network/proxy_server.go` +75/−9 + 新测试）；`network/` 归 syncl4 | 否 |
| `30bdfec59d` | #51651 | Persist Guardian review failures for reports across restarts | 无载体型（Guardian 簇） | L（17 文件 +442/−74，含 sqlite 迁移 `0060_guardian_review_feedback.sql`） | Go guardian reviewer 为简化同步实现、fail-closed，无持久化反馈库/表 | 否 |
| `b17c74cfd5` | #51652 | Record telemetry for AGENTS.md changes made by apply_patch | **真缺口** | S（Rust 1 文件 +24/−1） | Go `tool/apply_patch_executor.go` 全程无 Counter/metric（`git grep Counter tool/apply_patch_executor.go` 空）；全仓无 `codex.agents_md.edit`（`git grep agents_md -- '*.go' | grep -i 'metric\|counter'` 空） | 否（但载体 `tool/`+`telemetry/` 在给定域列表之外） |

## 2. 更早未处置的域内候选（16 条）
命令：`git log --format='%h %s' origin/main` 取 PR 号与已处置集（`/tmp/handled_prs.txt`，866 条）对拍，得到 16 条未处置项。

| # | Rust sha | 主题 | 分类 | 级别 | Go 载体 `file:line` / 判据 |
|---|---|---|---|---|---|
| `#51350` | `e32365a2c6` | Allow larger shell snapshots when replaying from a file | 域外（载体不在本域） | S（3 文件 +94/−14） | Rust `exec-server/src/shell_snapshot.rs`；Go 快照在 `appserver/shell_snapshot.go` + `shell/`（appserver 非本域，未逐行核验上限） |
| `#50786` | `acf9818fae` | Remember Command Center grouping across launches | **真缺口（载体在 tui/，域外）** | M（13 文件 +235/−12） | Go `config/config.go:865` 仅把 `agents_overview_grouping` 列入已知 TUI 键白名单；`tui/agents_overview/overview.go:66` 有 `Grouping` 枚举但无读取/持久化（`git grep agents_overview_grouping tui/` 仅注释命中） |
| `#50058` | `b06b7d2f77` | Upgrade Windows bindings to `windows-sys` 0.61.2 | 无载体型 | — | Go 无 windows-sys 依赖，Windows 绑定手写（如 `config/config.go:2405` 直接写 `%ProgramData%`） |
| `#50018` | `595534314f` | Use descriptor-safe helpers for executable test fixtures | 纯测试型 | L（76 文件 +196/−372，test-only） | 无生产载体 |
| `#49987` | `ecc78e4cf5` | Add renewable EMA HTTP authentication and credential versioning | **L（待队长裁定）** | L（23 文件 +2109/−61） | Go 有 `mcp/ema_auth_policy.go` / `mcp/ema_claims.go` / `mcp/ema_exchange.go`，但 `rmcp-client` 的可再生客户端（`ema_http_client.rs` +308）无对应；>5 文件，按规则不开工 |
| `#49972` | `a933dd77db` | Share byte buffers across exec-server output chunks | 无载体型（语言级） | S | Go `execserver/server.go:380 outputChunk` 为值类型、无 Arc 共享模型 |
| `#49856` | `c41a72cd3f` | Support Daybreak selection in `codex exec` | 域外（`app/`） | M | Go Daybreak 实现位于 `app/daybreak.go`；`app/` 归 syncw2 |
| `#49818` | `a73898c249` | Use dedicated parameters for sandboxed file opens | 已等价型 | S（2 文件 +11/−8） | Go `execserver/server.go:588 FSOpenParams` 本就是专用类型（非复用 readFile 参数）；且 Go `execserver/fs_helper.go` 操作集（`:109-183`：readFile/writeFile/createDirectory/getMetadata/canonicalize/readDirectory/walk/remove/copy）不含 `fs/open`，不存在该复用面 |
| `#49811` | `c51f5bfb82` | Handle unsupported `fs/writeBlock` requests in exec-server | 已取代型 | S（3 文件 +32） | Go `fs/writeBlock` 已**完整实现**：`execserver/server.go:2549 writeBlock`、`:447 FileWriteStreaming=true`、`execserver/writable_file_streams_test.go`。Rust 该 PR 只是「注册并拒绝」的中间态，Go 已超越（对应 Go 侧落的是 #50177） |
| `#49798` | `c538fbabe5` | Share cached exec-server environment info with Arc | 无载体型 | S | Go 无 Arc；`execserver/client.go:51 environmentInfoCache` 无共享指针语义 |
| `#49778` | `875bf9209b` | Define exec-server protocol types for streamed file writes | 已取代型 | S（6 文件 +80） | Go `execserver/server.go:588 FSOpenParams.Mode`（`:590` 注释引 Rust #50177）已含 read/replace 模式与 legacy 只读默认 |
| `#49642` | `67727e7cf1` | Allow managed requirements to disable the Windows MXC sandbox | **真缺口（S，本域 `config/`）** | S（Rust 7 文件；Go 侧 3 文件） | 见 §3 候选 A |
| `#49517` | `d42056091a` | Add a fork shortcut to the TUI command center | 无载体型 | M（32 文件，多为快照） | Go keymap 无 command-center fork 动作 |
| `#49389` | `9212b3eca8` | Serialize tests that share Windows sandbox accounts | 无载体型 | — | nextest/Windows-only 测试并行面，Go 无对应机制 |
| `#49300` | `a6f09397aa` | Compact the inline hidden tag buffer once per chunk | 无载体型 | S（1 文件 +52/−31） | Go 无流式 inline-hidden-tag 解析器：`git grep 'longestSuffixPrefix\|findNextOpen\|InlineTagSpec\|pushVisiblePrefix' -- '*.go'` 空；Go 为一次性 `eventmap/eventmap.go:526 stripInlineHiddenTag`，不存在「每分隔符 drain pending」的性能面 |
| `#49164` | `fbc169827e` | Suppress Windows console windows for background subprocesses | 已等价型 | S | Go `appserver/console_window_windows.go:17 suppressChildConsoleWindow` → `envutil.SuppressConsoleWindow`（CREATE_NO_WINDOW），并有 `appserver/console_window_windows_test.go` 回归 |

## 3. 推荐候选（**待批准后**再出补丁）

### 候选 A（首选，本域）：`#49642` — 托管 requirements 缺 `windows.allow_mxc`
- 分类：**真缺口**；级别 **S**；落点 **`config/`（本域）**；**非避让面**。
- Go 缺口证据（均在 `origin/main` = `185d03c9`）：
  - `config/requirements_file.go:319` 解析托管 requirements 的 `windows` 表时，仅调用 `windowsSandboxImplementationsFromMap(nested)`（`:320`）；该函数（`:1154`）只读 `allowed_sandbox_implementations`（`:1155`），**不读 `allow_mxc`**。
  - `git grep allow_mxc origin/main -- 'config/*.go'` 仅命中**本地配置**路径：`config/windows_sandbox_mode.go:104 WindowsAllowMXCFromValues` / `:124 ValidateWindowsMXCOptOut`（对应 Rust #51547）、`config/config.go:583`。**requirements 面完全缺席**。
  - `config/api.go:551 type ConfigRequirements struct` 无 MXC-block 字段（仅 `AllowedWindowsSandboxImplementations`，`:555`）；`:928 ResolveWindowsSandboxMode` 对显式 `mxc` 无条件放行。
- Rust 对照（`67727e7cf1`）：给 `WindowsRequirementsToml` 加 `allow_mxc: Option<bool>`；为 `false` 时给 `windows.sandbox` 加 validator（拒绝显式 mxc），并让 `config_allows_mxc` 在自动选择路径拒 mxc、`prepare_windows_sandbox_config` 对显式 mxc 走 `constraint.can_set`。
- 最小落点（3 文件）：`config/requirements_file.go`（读 `allow_mxc`）、`config/api.go`（新增字段，如 `AllowMXC *bool`）、`config/windows_sandbox_mode.go`（把该字段并入 `ResolveWindowsSandboxMode` / `WindowsAutomaticMXCAllowed` 判定）。
- 值级 RC（Linux 可测）：requirements 含 `windows.allow_mxc=false` 时，显式 `windows.sandbox="mxc"` 必须被拒 ⇒ 撤接线 FAIL ⇒ 恢复 ok。
- 备注：Go 侧当前无**隐式** MXC 选择（`WindowsAutomaticMXCAllowed` 注释已冻结该语义），因此「自动选择门」以语义冻结方式体现即可，核心可测面是「requirements 解析 + 显式 mxc 拒绝」。

### 候选 B（次选，域外邻近）：`#51652` — apply_patch 的 `codex.agents_md.edit` 遥测
- 分类：**真缺口**；级别 **S**（Rust 1 文件 +24/−1）；非避让面，但载体在 `tool/apply_patch_executor.go` + `telemetry/`，**不在本轮给定域列表**（`parity/ chatgptapi/ config/ utils/ realtime/ execserver/ clients/`）。
- 证据：`git grep Counter origin/main -- 'tool/apply_patch_executor.go'` 空；`git grep 'codex.agents_md' origin/main` 空。
- 若要落地：需 `telemetry/metrics_client.go:263 Counter` 的 seam + 把 session telemetry 传进 apply_patch 执行路径（Go 目前 `tool/` 无逐工具 metric 出口）。**建议先由队长裁定 `tool/`/`telemetry/` 归属**。
- 值级 RC：overlay 探针喂一个改 `AGENTS.md` 的补丁，断言 `codex.agents_md.edit` counter 恰发 1 次（标签 `filename=agents.md`）；`AGENTS.override.md` 大小写不敏感路径同理。

## 4. 未决 / 风险
- `#49987`（EMA 可再生认证）为 **L（23 文件 / +2109）**，按规则不开工，报队长裁定；Go 侧确有 `mcp/ema_*.go` 载体，属真缺口但超 S/M 界。
- `#50786`（Command Center 分组记忆）属真缺口，但行为面在 `tui/agents_overview/`（域外）；本域只涉及 `config/config.go:865` 的键白名单。
- `#51350` 载体在 `appserver/shell_snapshot.go`（域外），本轮未逐行核验 Go 端大小上限。
- 本单只读，未产生任何补丁；候选 A / 候选 B 均**待队长批准**后再落地。
