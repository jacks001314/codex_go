# syncw3 · 域候选扫描（Windows 相关 appserver/TUI 面）· round87

> 派单：队长 `msg-1791374880035117100-4994`（round87 · 裁定 + 新单）。
> 纪律：**只读**。本单 **0 补丁 / 0 commit / 0 push / 0 tag / 0 ref 移动**；未改任何 Go 源码；产物只写本文件。
> 固定点：Rust 上游 **`b17c74cfd5`**（`git rev-parse origin/main` 实测）；Go 参照 **`185d03c9`**（`git -C D:\qax\reagent\dev\codex_go rev-parse main` 实测）。
> Rust 仓用 `D:\qax\reagent\dev\git\codex`（只取 ref / 读 commit，不动任何工作树；该工作树是 CRLF 且停在旧点，仅用于 `show`/`log`）。
> 行号全部打在 Go **`185d03c9`**；每条附可复跑 `git grep`。

## 0. 口径与复跑命令（全部本次实跑）

```powershell
# ① 固定点
git -C D:\qax\reagent\dev\git\codex fetch origin main
git -C D:\qax\reagent\dev\git\codex rev-parse origin/main          # b17c74cfd5ebb39fe70ffaff78de198120278636
git -C D:\qax\reagent\dev\codex_go      rev-parse main            # 185d03c933086ddd2b0a1f498fdcf7f9d2683d70

# ② 新 pin 窗口（= 上一固定点 a6baf886 / LF head 5b0b253035 之后的新提交）
git -C D:\qax\reagent\dev\git\codex log --oneline a6baf8867cb4c9726213c0884a5c8b11f0cfd8bf..b17c74cfd5
git -C D:\qax\reagent\dev\git\codex rev-list --count 5b0b253035..b17c74cfd5          # 4

# ③ sha 自解（台账 sha 列不可信，逐条 --grep 自解）
git -C D:\qax\reagent\dev\git\codex log --grep '#51652' -F --format='%H' -1 b17c74cfd5
git -C D:\qax\reagent\dev\git\codex show --stat --format='%h %s' <sha>

# ④ 台账已审窗口与新 pin 的距离（本单第 1 个关键判定，见 §1）
git -C D:\qax\reagent\dev\git\codex rev-list --count 5a3140176e..b17c74cfd5          # 8（台账窗口端点之后只有 8 笔）
git -C D:\qax\reagent\dev\git\codex rev-list --count 5f3180c793..b17c74cfd5          # 507（台账窗口 5f3180c793..5a3140176e = 499 笔）

# ⑤ 域过滤：域路径 = windows-sandbox-rs / windows-sandbox-service / **/windows* /
#    shell-command / utils/pty / app-server / tui / sandboxing / exec-server
git -C D:\qax\reagent\dev\git\codex log --format='%h %ad %s' --date=short 5f3180c793..b17c74cfd5 -- ^paths   # 283 行
# 再按 Windows/跨平台语义关键词过滤（windows|win32|conpty|powershell|pwsh|cmd.exe|console|terminal|
# drive|reparse|job object|process tree|shell|path|descriptor|spawn|sandbox|mxc|pipe|stdio|elevat） # 50 行

# ⑥ Go 侧「有没有 PR 号引用」判据（打 185d03c9，只算 *.go，不算 update/*.md）
git -C D:\qax\reagent\dev\codex_go grep -I -h -o -E '#[0-9]{5}' 185d03c9 -- '*.go' | Sort-Object -Unique   # 1111 个
# 50 条域 PR 中，**零 .go 引用** = 24 条（§3 候选表）

# ⑦ 逐条 Go 载体探针（示例）
git -C D:\qax\reagent\dev\codex_go grep -n -i -E '<token>' 185d03c9 -- '*.go'
git -C D:\qax\reagent\dev\codex_go grep -n 'SuppressConsoleWindow' 185d03c9 -- '*.go'
```

判据口径：

- Go 侧「已落地」的**下界**判据 = Go 提交标题含 `(#PR)` 或 `.go` 源码注释含 `#PR`（只取 `*.go`，**不含** `update/*.md`，因为 `update/` 里的 lane 报告也会写 PR 号，会把「仅被讨论过」误判成「已落地」）。
- `git grep` 的 `-i`（不区分大小写）在 PowerShell 下逐条给「命中行 / (0 hits)」，命中行即载体 `file:line`。
- 「机制性 N/A」= Rust 改动**全部**落在测试 / 快照 / lock / CI / 依赖清单 / `mod tests` 内。

## 1. 结论摘要

1. **新 pin 窗口（5 笔，见 §2）在「Windows 相关 appserver/TUI」域内 = 0 条可落地**。5 笔分别是 core 遥测、guardian 持久化、network-proxy、guardian-context ×2 —— 无一条含 Windows 语义。
2. **新 pin 窗口本身已无未扫空间（diff vs 派单「从新 pin 起扫」）**：台账（`update/remaining_ledger_2026_10_07.md`）已审窗口是 `5f3180c793..5a3140176e`（499 笔），而 `5a3140176e..b17c74cfd5` **只有 8 笔**，其中域路径命中 5 笔（= §2 那 5 笔，含 `5b0b253035`）。⇒ 若只看新 pin，域扫描是空表；故本单把域扫描铺到**台账已审窗口的域子集**上（§3），并用「零 `.go` 引用」做机械筛。
3. **域子集规模**：`5f3180c793..b17c74cfd5` 域路径命中 **283 笔**；按 Windows/跨平台语义关键词收窄到 **50 笔**；其中**零 `.go` 引用 = 24 笔**（§3 的候选表）。
4. **找到 2 条 S 级真缺口候选（非避让面，先报后做，未开工）**：
   - **`#49642`**（managed requirements 无法禁用 Windows MXC）—— Go 只有**用户配置**面的 `windows.allow_mxc` opt-out（`config/windows_sandbox_mode.go:108` + `:123`，自陈 #51547），**managed requirements 面没有该字段**，`requirements.toml` / 云托管的 `windows.allow_mxc=false` 会被**静默忽略**。
   - **`#49855`**（提权的本地 Windows 会话应静默降级为 embedded 并给警告）—— Go 只有守护进程侧的**拒绝**（`appserverdaemon/elevation.go:7`），interactive 启动的 exclusion 列表里**没有 elevation 分支**（`app/daemon_startup.go:77-105` 与 `:345-370`），提权 + `daemon_auto_start` 时会**致命报错**而不是降级。
   两条都请队长裁定后再动手（本单不出补丁）。
5. **其余 22 条**：3.A 16 条 = **2 条 C1** + **4 条 C2 已等价** + **10 条 C3 无载体/撞避让面**；3.B **4 条非本域**（TUI 通用，转 TUI 车道）；3.C **4 条机制性 N/A**。
6. **零 `.go` 引用 ≠ 未落地**（重要）：`#49261`（台账 §12.1 判 ✅）、`#49019`、`#49164` 三条都是**零 `.go` 引用但已落地/已等价**。故 §3 全部结论都以**逐条载体探针**为准，不以机械筛为准。

## 2. 新 pin 窗口逐条（`a6baf886..b17c74cfd5`，5 笔；sha 全部经 `--grep` 自解命中）

| PR | Rust sha | `--stat` | 域内？ | 判定 | 依据 |
|---|---|---|---|---|---|
| `#51652` | `b17c74cfd5` | 1 文件 +24/−1（`core/src/tools/runtimes/apply_patch.rs`） | 否（core tools 遥测，非 appserver/非 Windows） | **C1 可落地，但非本域** | syncw4 `update/syncw4_round87_rescan_2026_10_07.md` 已给载体 `tool/apply_patch_executor.go:215` + `applypatch/applypatch.go:126-141`；本单不重复开工 |
| `#51651` | `30bdfec59d` | 17 文件 +442/−74（app-server `request_processors/feedback_*`、`core/src/guardian/feedback.rs`、`feedback/src/guardian.rs`、`state/migrations/0060_guardian_review_feedback.sql`、`state/src/runtime/guardian_feedback.rs` …） | 否（appserver 面但**无 Windows 语义**；guardian + 新 SQLite schema） | **C5 L（需架构决策）** | 同 syncw4：Go 侧**整个 guardian failed-review 记录器缺席**（`guardianRolloutPath` 生产接线恒 `nil`，`appserver/runtime_router.go:11100`） |
| `#51650` | `37eaae6eeb` | 9 文件 +357/−35（`network-proxy/*` + `sandboxing/src/seatbelt*`） | 否（seatbelt = macOS；network-proxy 平台无关） | **C5 L + 在飞** | syncl4 承接 (1)(2)(4)；(3) 经 syncl1 判已等价（`update/syncl1_51650_3_judgement_2026_10_07.md`） |
| `#51642` | `a513012869` | 2 文件 +19/−15（`guardian-context/src/retained_instructions.rs` + 测试） | 否 | **C2 已等价** | syncw4 已判：Go 无 `SectionContent` 包装层；行为面由 #51627 端口承载（`state/guardian_retained_context.go:330 HasSplitAssistantOmission`、`:340 splitAssistantOmission`） |
| `#51627` | `5b0b253035` | 7 文件 +343/−46（`guardian-context/*` + `app-server/tests/suite/v2/guardian_v2.rs`） | 否 | **C4 已落地** | `update/syncl3_51627_2026_10_07.md`；Go `state/guardian_retained_context.go`（同日 splitted-assistant-context 端口） |

## 3. 域候选表（台账已审窗口 `5f3180c793..b17c74cfd5`，Windows/跨平台语义子集，零 `.go` 引用 = 24 条）

### 3.0 五分类与正交列定义（本单采用）

| 标签 | 含义 | 处置 |
|---|---|---|
| **C1 可落地** | Go 有载体，S/M（≤5 文件），可做值级 RC | 报队长 → 授权后开工 |
| **C2 已等价** | Go 已有等价实现（**含零 PR 引用但确已落地**），附 `file:line` | 归档 |
| **C3 无载体** | Go 无对应子系统/包，附「0 命中」命令 | 归档（或登记为 cluster 前置） |
| **C4 被更晚 PR 取代** | 附取代者 sha / 已落地落点 | 归档 |
| **C5 需架构决策 / L** | >5 文件，或需新子系统/schema | 不开工，报队长 |

正交列：**级别**（S/M/L）·**避让面**（`appserver/runtime_router.go` / `appserver/turn_runtime.go` / `tui/state.go` / `sandbox/windowssandbox/`）·**机制性 N/A**。

### 3.A 域内（Windows / 跨平台语义）— 16 条

| PR | Rust sha | `--stat` | Go 载体 `file:line`（打 `185d03c9`） | 判定 | 级别 | 避让面 |
|---|---|---|---|---|---|---|
| `#49642` | `67727e7cf1` | 7 文件（`config/src/config_requirements.rs`、`core/src/config/windows_sandbox_config.rs`、app-server `config_processor.rs`、`tui/src/debug_config.rs`…） | 缺：`config/requirements_file.go:1154 windowsSandboxImplementationsFromMap` 只读 `allowed_sandbox_implementations`（其余键**静默忽略**）；已有的是**用户配置面** opt-out `config/windows_sandbox_mode.go:108 WindowsAllowMXCFromValues` / `:123 ValidateWindowsMXCOptOut` / `config/config.go:583`；`configRequirementsEmpty`（`requirements_file.go:1105`）无 `AllowMXC` | **C1 可落地（真缺口候选，先报后做）** | **S** | 否（`config/`） |
| `#49855` | `bc197b77bc` | 8 文件（`tui/src/daemon_startup.rs`、`startup_orchestration.rs`、`app-server-daemon/src/backend/windows.rs`、`lib.rs` + 测试/快照） | 缺：`app/daemon_startup.go:345-370 interactiveDaemonEndpoint` 的 exclusion 只取 `daemonStartupExclusion`（`:77-105`：`--no-daemon/--oss/workload identity/CODEX_EXEC_SERVER_URL/--profile` + `daemonConfigExclusion :106`）与 Windows detached-restriction（#48491，`:365`）、WSL DrvFS（#50555，`:355`）；**无 elevation 分支**。已有的是守护进程侧拒绝：`appserverdaemon/elevation.go:5-16 ErrElevatedDaemonLauncher`（`:5` 注释即指 Rust `ensure_not_elevated`）、`appserverdaemon/lifecycle.go:59`。探针：`git grep -F 'Running as administrator' 185d03c9 -- '*.go'` = **0 命中** | **C1 可落地（真缺口候选，先报后做）** | **S** | 否（`app/`、`appserverdaemon/`） |
| `#51350` | `e32365a2c6` | 3 文件 +59（`exec-server/src/shell_snapshot.rs` + 测试） | **无载体**：`git grep -i snapshot 185d03c9 -- 'execserver/*.go'` 仅命中 `EnvironmentProviderSnapshot`（`execserver/environments_toml.go`，无关）；`git grep -l ShellSnapshot 185d03c9 -- '*.go'` = `appserver/runtime_router.go` / `appserver/shell_snapshot.go` / `tool/shell_snapshot_builder.go` / `telemetry/metric_names.go`（**均为 appserver/tool 侧会话级快照，不是 exec-server 侧缓存**）。Rust 改的是 exec-server 侧「文件回放 vs 环境回放的分离上限」（512 KiB → 4 MiB / 8 MiB） | C3 无载体（需先移植 exec-server 侧快照缓存，#48078/#48549 族） | M | 否 |
| `#51256` | `580b18cb74` | 3 文件（`app-server/src/request_processors/windows_sandbox_processor.rs`、`windows-sandbox-rs/src/provisioning_client.rs`、`lib.rs`） | **无载体**：`git grep -i provisioning -- '*.go'` 仅命中**legacy 提权 setup**（`app/app.go:59 runWindowsSandboxProvisioningSetup`、`:268`、`:1407` 文档串、`app/sandbox_execpolicy_test.go:97`），**无** `windows-sandbox-service` / `CreateService` / provisioning IPC client | C3 无载体；若强行落地，落点在 `sandbox/windowssandbox/` ⇒ **撞避让面·转派** | M | **是** |
| `#50507` | `c542fb93ef` | 6 文件（`windows-sandbox-rs/src/service_diagnostics.rs`、`windows-sandbox-service/src/service/runtime_lifecycle.rs`…） | **无载体**：`git grep -i 'service_diagnostics\|ServiceStopReason\|stop_reason'` 仅命中无关的 `tool/unified_exec_spans.go:41` | C3 无载体；落点 `sandbox/windowssandbox/` ⇒ **撞避让面·转派** | S | **是** |
| `#50480` | `8f7a0f7a87` | 3 文件（`windows-sandbox-service/src/ipc/authentication.rs`、`machine_policy.rs`…） | **无载体**：`git grep -i 'machine_policy\|machinePolicy\|only_registered_refresh'` = **0 命中** | C3 无载体；⇒ **撞避让面·转派** | S | **是** |
| `#49818` | `a73898c249` | 2 文件（`exec-server/src/fs_helper.rs`、`sandboxed_file_open.rs`） | 已等价：新 `FsHelperOpenParams{path: PathUri}`（Rust 内部把 `fs/open` 从复用 `FsReadFileParams` 换成专用类型，**无可观测行为变化**）；Go 的 `fs/open` 本就是独立参数路径：`execserver/client.go:1137-1155`（`fs/open params are required` / path 校验）、`execserver/regular_file_windows.go:42`（自陈 Rust #50177 的 `fs/open` replacement mode）、`execserver/no_follow.go:10-14` | C2 已等价（重构，无行为差） | S | 否 |
| `#49690` | `7e8878f605` | 7 文件（`windows-sandbox-rs/src/{acl.rs,setup.rs,setup_provisioning/*}` 生产 + `core/tests/suite/windows_sandbox.rs`） | 台账 §12.1 判 **⬜**：`git grep -i 'relative_path\|relativePath' -- 'sandbox/'` = 0；落点 `sandbox/windowssandbox/acl_windows.go`（Go 有对应物但无相对路径保留语义） | **撞避让面·转派**（台账已登记 ⬜） | M | **是** |
| `#49261` | `f35a0fdc5d` | 1 文件 +5/−2（`windows-sandbox-rs/src/elevated/runner_client.rs`） | **已落地但零 PR 引用**：台账 §12.1 ✅（队长核）；Go `sandbox/windowssandbox/elevated/runner_client_windows.go:88-95` 在 `SetErrorMode` 前取 `createProcessWithLogon` err | C2 已等价/已落地 | S | 是（落点） |
| `#49164` | `fbc169827e` | **47 文件**（`core/src/spawn.rs`、`utils/process/src/lib.rs`、`utils/pty/src/win/job.rs`、`git-utils/*`、`core-plugins/*`、`rmcp-client/src/stdio_server_launcher.rs`、`hooks/src/engine/command_runner.rs`、`app-server/src/request_processors/feedback_doctor_report.rs`、`model-provider/*`…） | **已等价（机制 + 广覆盖已存在）**：机制 `envutil/console_window_windows.go:23 SuppressConsoleWindow`（#48483/#48238），调用点 ≥45 处，含与 Rust 清单一一对应的族：`gitutil/gitutil_windows.go:45`、`plugin/npm_source.go:115`、`plugin/marketplace_install.go:286`、`mcp/stdio_command_windows.go:38`、`mcp/http_headers_helper.go:120`、`appserver/hooks_runner.go:194`、`appserver/feedback_doctor_report.go:58`、`doctor/security_windows.go:67`、`doctor/filesystem_paths.go:211`、`model/credential_export.go:109`、`model/auth.go:190`、`review/git.go:199`、`worktree/manager.go:368`、`tui/get_git_diff.go:76`、`appserverdaemon/update_loop.go:403` / `updater_install.go:169,276` / `lifecycle.go:741`、`memories/workspace.go:448`、`tool/unified_exec_process_windows.go:82` … | C2 已等价 | L（Rust 47 文件）→已覆盖 | 否 |
| `#49067` | `5a5a4aa796` | 2 文件（`windows-sandbox-service/src/ipc.rs` + 测试） | **无载体**：`git grep -i 'policy_event\|PolicyEvent\|SandboxPolicyEvent'` = **0 命中**（Go 无该 service 包） | C3 无载体；⇒ **撞避让面·转派** | S | **是** |
| `#49019` | `4fd5745e84` | 5 文件（`core/src/tools/runtimes/mod.rs`、`unified_exec.rs`、`shell-command/src/shell_detect.rs`…） | **已等价（零 PR 引用）**：`execserver/windows_powershell_fallback.go:147 windowsSandboxPowerShellProgram(program, level)` 显式判 `level != windowsSandboxLevelElevated && level != windowsSandboxLevelMxc ⇒ 原样返回`，调用点 `execserver/sandbox_process_windows.go:92`；:93 `windowsSandboxCompatiblePowerShellPath`（**与 #49019 重命名后的 Rust 函数同名**）；:120/:123 依次试 `pwsh` / `powershell`；测试 `execserver/windows_powershell_fallback_test.go:71`（`windowsSandboxLevelMxc` 期望替换）；提权变体另有 `tool/shell_sandbox.go:103 fallbackPowerShellShellForElevatedWindowsSandbox`（#41227） | C2 已等价 | S | 否 |
| `#48829` | `e6f4af1d92` | 1 文件 +48（`windows-sandbox-rs/src/provisioning_client.rs`） | **无载体**：`git grep -i 'WaitNamedPipe\|provisioning_client\|ProvisioningClient'` = **0 命中** | C3 无载体；⇒ **撞避让面·转派** | S | **是** |
| `#48799` | `4c8cf3964d` | 3 文件（`tui/src/tui/alternate_screen.rs` + 测试） | **无载体·机制不适用（正向陈述）**：该 PR 让 **Rust TUI 自己启用**鼠标捕获时补发 SGR 编码请求（`\x1b[?1006h`，并把 `?1006h` 从 `EnablePointerCapture` 拆出单独 write+flush）。Go TUI **有意不启用终端鼠标跟踪**（#50564 已归档裁定）。探针：`git grep -E '1000h\|1002h\|1003h\|1006h' 185d03c9 -- '*.go'` = **0 命中**（Go 只写 `\x1b[?1007h/l` 备用滚动，`tui/tea/model.go:7993-7995`） | C3 无载体（机制不适用） | S | 否 |
| `#48531` | `06f97622f8` | 1 文件（`windows-sandbox-service/src/registered_runtime.rs`） | **无载体**：`git grep -i 'registered_runtime\|RegisteredRuntime'` = **0 命中** | C3 无载体；⇒ **撞避让面·转派** | S | **是** |
| `#50720` | `447eac3b81` | 8 文件（`tui/src/tui/windows_key_sequence.rs` 新增 157 行、`event_stream.rs`、`tui.rs`、`Cargo.*`、`MODULE.bazel.lock` + 测试/快照） | **无仓内载体（依赖层）**：Rust 用 50 ms 截止窗口把 Windows Terminal `sendInput` 的 `ESC[13;2u`（被拆成独立 console key 事件）拼回 `Shift+Enter`。Go TUI 的裸键解码在 **bubbletea**（`go.mod` `github.com/charmbracelet/bubbletea v1.3.10`），仓内只有键位绑定：`tui/keymap.go:50`（editor `insert_newline` 已含 `shift-enter`）；`git grep -E 'windows_key_sequence\|13;2' -- '*.go'` = **0 命中** | C3 无载体（若需对齐，须在 bubbletea 输入读取器侧，不在本仓写范围） | S | 否 |

### 3.B 非本域（TUI 通用，无 Windows 语义）— 4 条，登记并建议转 TUI 车道

| PR | Rust sha | `--stat` | Go 载体 / 探针 | 判定 |
|---|---|---|---|---|
| `#50431` | `b6903c0669` | 3 文件（`tui/src/app/agents_overview_view.rs` + 测试 + 快照） | `git grep -i 'OSC8Hyperlink\|Hyperlink' 185d03c9 -- 'tui/agents_overview/*.go'` = **0 命中**（Go 的 OSC-8 工具在 `tui/tea/*`，如 `codextui.OSC8Hyperlink`，见 `tui/tea/backend_banner_fallback_test.go:35`） | 非本域（TUI 通用）→ 转 TUI 车道 |
| `#49861` | `8ea2428c38` | 10 文件（`tui/src/bottom_pane/status_line_setup.rs`、`title_setup.rs`、`status_surfaces.rs`、`cli/src/doctor/title.rs` + 快照） | Go 已有 daybreak 通道：`tui/daybreak.go:8`（自陈 Rust `tui/src/daybreak.rs`）、`tui/chatwidget/turn_runtime.go:128-131/532-534`；**状态行/终端标题是否带 Daybreak 未核** | 非本域（TUI 通用）→ 转 TUI 车道（附现有 daybreak 载体） |
| `#49564` | `0b43721d8d` | 6 文件（`tui/src/markdown_copy.rs`、`markdown_copy/table.rs`、`markdown_render.rs` + 测试/快照） | `git grep -i 'markdown_copy\|MarkdownCopy'` = **0 命中** | 非本域（TUI 通用）→ 转 TUI 车道 |
| `#48761` | `e75b6b1e0c` | 14 文件（`tui/src/transcript_view/*`、`exec_cell/*`、`history_cell/*` + 快照） | `git grep -i 'hiddenLines\|revealable'` = **0 命中**；Go 无 `tui/transcript_view` 包（#50564 裁定已记） | 非本域（TUI 通用）→ 转 TUI 车道 |

### 3.C 机制性 N/A（Rust 改动全在测试 / lock / 快照 / 依赖 / `mod tests`）— 4 条

| PR | Rust sha | 判据（实跑） |
|---|---|---|
| `#50018` | `595534314f` | 标题即「descriptor-safe helpers for **test fixtures**」；生产 `.rs` 命中 10 个（`arg0/src/lib.rs`、`git-utils/src/{baseline,info}.rs`、`tui/src/{external_editor,get_git_diff}.rs`、`model-provider/src/provider.rs`、`linux-sandbox/src/*`、`app-server-daemon/src/lib.rs`、`cli/src/desktop_app/mac.rs`），**逐个查看后确认改动全在 `mod tests` 内**（例：`git show 595534314f -- codex-rs/arg0/src/lib.rs`、`-- codex-rs/git-utils/src/info.rs` 均落在 `#[cfg(test)] mod tests`）。动机是 Linux ETXTBSY 并发 spawn。 |
| `#50058` | `b06b7d2f77` | Rust 依赖迁移 `windows-sys` → 0.61.2（99 文件 = `Cargo.toml` ×N + 随新 API 改写的 Windows 调用点）。Go 用**独立**依赖：`go.mod` `golang.org/x/sys v0.47.0`，无对应动作。 |
| `#49389` | `9212b3eca8` | `git show --name-only` = `.config/nextest.toml`、`Cargo.toml` ×4、`windows-sandbox-rs/BUILD.bazel`、`**/tests/**`、`tests/support/*` ⇒ **零生产 .rs**（台账 §12.3 同判）。 |
| `#49867` | `90d7f2715a` | `--stat` = **1 文件**，且是快照 `codex_tui__app__owned_transcript__warning_notice_tests__elevated_launch_warning_center.snap`（仅把提示里的复制键改成 `⌃o`）⇒ 无生产代码。 |

## 4. S 级真缺口候选（按派单「先报我」，本单**未开工**、未出补丁）

### 4.1 `#49642` — managed requirements 无法禁用 Windows MXC

- **Rust 改动**：`config/src/config_requirements.rs` 给 `WindowsRequirementsToml` 加 `allow_mxc: Option<bool>`，并在 `TryFrom<ConfigRequirementsWithSources>` 里给 `windows.sandbox` 装 validator：`Some(WindowsSandboxModeToml::Mxc)` ⇒ `ConstraintError::InvalidValue{allowed: "windows.allow_mxc = false"}`；另加 `core/src/config/windows_sandbox_config.rs`、app-server `config_processor.rs`、`tui/src/debug_config.rs`。
- **Go 现状（正向陈述 + file:line）**：Go **有** `windows.allow_mxc`，但只走**用户配置**面：`config/windows_sandbox_mode.go:108 WindowsAllowMXCFromValues`（自陈「added by Rust **#51547**」）→ `:123 ValidateWindowsMXCOptOut`（`windows.sandbox="mxc"` + `allow_mxc=false` ⇒ 报错）→ `config/config.go:583` 在 load 时调用。**managed requirements 面没有该字段**：`git grep -n 'AllowMXC' 185d03c9 -- 'config/*.go'` 只命中 `windows_sandbox_mode.go` 的两个函数名；`config/requirements_file.go:1154 windowsSandboxImplementationsFromMap` 只读 `allowed_sandbox_implementations`，**其它键静默忽略**（因此 `requirements.toml` 里的 `windows.allow_mxc=false` 不报错、不生效）；`configRequirementsEmpty`（`:1105`）亦无该字段；云托管侧 `config/cloud_config.go:605-606` 同样只合并 `AllowedWindowsSandboxImplementations`；`configRequirementsEmpty` 的调用点与 requirements→values 的接线在 `config/config.go:348-375`（`LoadRequirementsFile` → `applyCloudConfigBundle` → `applyManaged*Overrides`）。
- **可达性**：managed requirements（`$CODEX_HOME/requirements.toml` 或云托管 bundle）管理员无法阻止 MXC —— 与 Rust #49642 的意图直接冲突。
- **规模**：`config/requirements_file.go`（解析 + `configRequirementsEmpty` + validator 接线）+ `config/api.go`（字段/克隆/wire）+ `config/config.go`（apply）+ `config/windows_sandbox_mode.go`（复用现有校验）+ 测试 ⇒ **S（4–5 文件）**。
- **避让面**：否（`config/`）。**待队长确认**：派单把域限定为「Windows 相关的 appserver/TUI 面」，本条落在 `config/`；但语义是 Windows 沙箱配置面，且不在避让清单内。

### 4.2 `#49855` — 提权的本地 Windows 会话应静默降级为 embedded + 警告

- **Rust 改动**：`startup_orchestration.rs` 在 daemon 发现/启动**之前**判 `is_elevated()`：提权且非 `--no-daemon`/非 agents-overview 时置 `cli.no_daemon = true` 并给出 `ELEVATED_LAUNCH_WARNING`（"Running as administrator: shared background server disabled. …"）；记录 `elevated_windows` 作为 server selection reason。另改 `tui/src/daemon_startup.rs`、`app-server-daemon/src/backend/windows.rs`（`ensure_not_elevated` 仍只在显式 start/restart 拒绝）。
- **Go 现状**：守护进程侧**已有拒绝语义**：`appserverdaemon/elevation.go:5-16`（`ErrElevatedDaemonLauncher`，注释自陈对应 Rust `backend::windows::ensure_not_elevated`）、`appserverdaemon/lifecycle.go:59`（"Only start/restart perform the Windows elevation handoff the daemon …"）。**interactive 启动侧没有 elevation exclusion**：`app/daemon_startup.go:77-105 daemonStartupExclusion`（+ `:106 daemonConfigExclusion`）只有 `--no-daemon/--oss/workload identity/CODEX_EXEC_SERVER_URL/--profile`，`:345-370 interactiveDaemonEndpoint` 只对 WSL DrvFS（#50555，`:355`）与 `IsDetachedLaunchRestricted`（#48491，`:365`）降级；其余错误一律 `fmt.Errorf("%w\n%s", err, daemonFailureHint)` **致命**。探针：`git grep -F 'Running as administrator' 185d03c9 -- '*.go'` = **0 命中**。
- **可达性（需队长核）**：Windows + 提权终端 + `features.daemon_auto_start` 打开时，Go 会以 `ErrElevatedDaemonLauncher` **致命失败**（提示 `--no-daemon`），而 Rust 静默 embedded + 警告。若 auto-start 默认关闭，则 Go 走 `localDaemonEndpointForLaunch`（可回落 embedded）而不触发 —— **故须先确认 `daemonAutoStartFeature` 的默认值与触发条件**（我未在本单确认，属未决项）。
- **规模**：`app/daemon_startup.go`（新增 elevation exclusion + 警告串）+ 复用 `appserverdaemon` 的 elevation 查询 + 测试 ⇒ **S（2–3 文件）**。
- **避让面**：否（`app/`、`appserverdaemon/`）。

## 5. 避让面登记（本单只登记，不落地）

| PR | 若落地的落点 | 说明 |
|---|---|---|
| `#51256` | `sandbox/windowssandbox/`（新 provisioning client / service 启动） | Windows 沙箱 service 子系统，Go 未移植 |
| `#50507` | `sandbox/windowssandbox/`（service 停止诊断） | 同上 |
| `#50480` | `sandbox/windowssandbox/`（machine policy / 注册刷新） | 同上 |
| `#49690` | `sandbox/windowssandbox/acl_windows.go`、`setup*.go` | 台账 §12.1 已判 **⬜**，落点在避让面 ⇒ **转派** |
| `#49067` | `sandbox/windowssandbox/`（policy event 剔除配置值） | Go 无该 service 包 |
| `#48829` | `sandbox/windowssandbox/`（等 provisioning 服务） | Go 无该 client |
| `#48531` | `sandbox/windowssandbox/`（registered runtime 错误上下文） | Go 无该 service 包 |
| `#49261` | `sandbox/windowssandbox/elevated/runner_client_windows.go` | **已落地**（台账 ✅），仅登记 |

（`appserver/runtime_router.go` / `appserver/turn_runtime.go` / `tui/state.go` 本次**无**候选命中。）

## 6. 未完成 / 未决问题 / 风险

1. **未逐条对拍的 26 条**：Windows/跨平台语义子集 50 条中，**有 `.go` 引用**的 26 条我只采信「有锚点」，**未逐条做值级语义比对**（本单预算是 24 条零引用项）。若队长要全覆盖，请另派一轮，我按同一模板补 26 条。
2. **域边界**：#49642 落点在 `config/`（不在派单列举的域路径，也不在避让清单）。若队长认为它不属本域，可转派 config 车道 —— 但**证据已备齐**（§4.1）。
3. **#49855 可达性未闭合**：需先确认 `daemonAutoStartFeature`（`app/daemon_startup.go`）的默认值/开关名，才能把「提权 + auto-start ⇒ 致命失败」定成生产可达缺陷。我未在本单做该确认（避免越界改代码；只读探针已备）。
4. **`#50720` 的责任边界**：Go 用 bubbletea 做裸键解码，仓内无载体；若要对齐，需在 bubbletea 侧（第三方依赖）—— 本仓**不可落地**，请队长按「依赖层 N/A」记账。
5. **本单未跑门禁/RC**：本单是只读扫描，**没有**补丁，故无 6 步门禁与反向对照 RC 输出（派单亦未要求）。
6. **机械筛的已知局限**（重复台账 §10.B 的口径）：`git grep '#PR' -- '*.go'` 是「已落地」的**下界**。本次实证三例反例：#49261（已落地、零引用）、#49019（已等价、零引用）、#49164（已等价、零引用）—— 故 §3 一切结论均以载体探针为准。
