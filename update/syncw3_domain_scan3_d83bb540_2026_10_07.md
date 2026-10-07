# syncw3 · 第三轮域扫描（`appserverdaemon/` + `execserver/` + `sandbox/`）· round88

> 派单：队长 `msg-1791377336559168000-5388`。
> 纪律：**只读**。本单 **0 补丁 / 0 commit / 0 push / 0 tag / 0 ref 移动**；未改任何 Go 源码（本文件与 worktree 回收除外）。
> 避让面 `appserver/runtime_router.go` / `appserver/turn_runtime.go` / `tui/state.go` / `sandbox/windowssandbox/` **未改**。
> 固定点：Rust pin **`d83bb540ec64bf6b009bca0283b0be91ea33f26a`**；Go 参照 **`8b2453a9ee619e7a84a95165b3e2474af082d71f`**。
> Rust 仓 `D:\qax\reagent\dev\git\codex`（只 `log`/`show`，不动其工作树）；parity 树 `C:\rw\codex-rs` 未动。

## 0. 复跑命令与实测输出

```powershell
# ① 固定点
git -C D:\qax\reagent\dev\git\codex fetch origin main
git -C D:\qax\reagent\dev\git\codex cat-file -t d83bb540ec64bf6b009bca0283b0be91ea33f26a   # commit
git -C D:\qax\reagent\dev\codex_go  fetch origin main
git -C D:\qax\reagent\dev\codex_go  rev-parse origin/main        # 8b2453a9ee619e7a84a95165b3e2474af082d71f

# ② pin 前进（b17c74cfd5 -> d83bb540）= 仅 1 笔
git -C D:\qax\reagent\dev\git\codex log --oneline b17c74cfd5..d83bb540ec64bf6b009bca0283b0be91ea33f26a
#   d83bb540ec Move Windows sandbox tests into a dedicated integration binary (#51678)

# ③ 窗口 = 最后 200 笔；<=5 文件 = 103 笔
git -C D:\qax\reagent\dev\git\codex log --numstat -200 d83bb540ec64bf6b009bca0283b0be91ea33f26a

# ④ 域 crate = app-server-daemon / exec-server(+protocol) / sandboxing / linux-sandbox / mxc-sandbox /
#    bwrap / windows-sandbox-rs / windows-sandbox-service / exec   -> <=5 文件命中 12 笔

# ⑤ Go 载体探针（打 8b2453a9，只算 *.go；不含 update/*.md）
git -C D:\qax\reagent\dev\codex_go grep -n -F '#50803' 8b2453a9 -- '*.go'
git -C D:\qax\reagent\dev\codex_go grep -n -F '#50940' 8b2453a9 -- '*.go'
git -C D:\qax\reagent\dev\codex_go grep -n -F '#51678' 8b2453a9 -- '*.go'    # 0 命中
```

判定口径：

- **已落地** = Go 生产代码/注释带该 `#PR`，或存在与 Rust 同语义的实现（给 `file:line`）。
- **已等价** = Go 用**不同机制**达到同一可观测行为（给 file:line + 机制差异）。
- **无载体（机制性 N/A）** = 该行为在 Go 侧无对应机制；给 `git grep` 0 命中原文（只算 `*.go`）。
- **纯测试** = Rust 改动仅测试 / 快照 / CI / lock。
- **真缺口候选** = 生产可达且 Go 无等价实现。

## 1. 结论摘要

1. **pin 只前进 1 笔** = `#51678`（Rust 测试基础设施）。Go 侧独立核验 = **已等价（无对应结构）**，见 §2。
2. **域 ≤5 文件提交 = 12 笔**（按路径前缀）+ pin 自身 `#51678`（按内容属域）= **13 笔，全量判定见 §3**：
   - 已落地 **5**：`#50803` `#50802` `#50782` `#50499` `#51483`
   - 已等价 **4**：`#51678` `#51527` `#51407` `#51511`
   - 已等价但落点在**避让面内（预存在，本单不改）** **1**：`#50940`
   - 无载体（**前轮已判**，队长勿重复清单）**2**：`#51350` `#51256`
   - **撞避让面，待裁定** **1**：`#50480`
3. **本单 0 条新的真缺口候选**：没有「生产可达 + Go 无等价实现 + 落点不在避让面」的 S/M 项。
4. 非本域 ≤5 文件提交 **90 笔** → §5 全量清单。
5. 超阈值（>5 文件）域提交 **15 笔** → §6（派单只要求 ≤5 文件，故未逐条分类；列出供取舍）。
6. `syncw3b` worktree 已回收（先比对 blob 证明三文件全部落主）→ §7。

## 2. pin 前进的独立核验：`#51678`（Windows 沙箱测试搬进独立 integration binary）

Rust 改动（3 文件 +10/−9）：

```
 codex-rs/.config/nextest.toml                      | 8 +-------
 codex-rs/core/tests/suite/mod.rs                   | 2 --
 codex-rs/core/tests/{suite => }/windows_sandbox.rs | 9 +++++++++
```

动机（commit 原文）：提权 Windows 沙箱测试共享 machine-wide 账户；跑在分片的 core suite 里会让分片在等账户锁时耗光 test deadline。改法：把 `core/tests/suite/windows_sandbox.rs` 移到 Windows-only 顶层 `core/tests/windows_sandbox.rs`（独立 integration binary），并把 nextest 的 account-sharing group 从「逐个 test 名」改成「整个 `binary(windows_sandbox)`」，使测试**先排队再计时**。

Go 侧核验（打 `8b2453a9`）：

- **Go 无 nextest / 分片配置**：`git ls-tree --name-only 8b2453a9` 无 `.config/`；`.github/workflows/` 仅 `macos-platform-gates.yml` / `parity-baseline.yml` / `release.yml` / `sqlite-platform-gates.yml`，无 test-sharding 配置；`git grep -n -i -e 'shard' -e 'nextest' 8b2453a9 -- '*.yml' '*.yaml' '*.toml' 'scripts/'` = **0 命中**。
- **Windows 沙箱测试本就在独立 test binary**：位于独立包（`sandbox/windowssandbox/`、`sandbox/windowssandbox/elevated/`、`sandbox/windowssandbox/unified_exec/...`），不在任何分片单体 suite 内。Go 的 `go test` 天然 per-package binary，等价于 Rust 的「dedicated integration binary」。
- **提权用例隔离机制 = 环境门（非分片 filter）**：`sandbox/windowssandbox/smoke_windows_test.go:66` `t.Skip("set CODEX_WINDOWS_SANDBOX_SMOKE=elevated or all to run")`（另 `:75` / `:104` / `:134`）。
- `git grep -n -F '#51678' 8b2453a9 -- '*.go'` = **0 命中**（该笔无 Go 生产行为）。

⇒ **判定：已等价（测试基础设施）**。Rust 的目标「提权用例不与其它分片争 machine-wide 账户锁、且先排队再计时」在 Go 结构上已满足（独立包=独立 binary），且 Go 无 nextest filter 需更新。**无 Go 侧可落地改动，也非缺口。**

## 3. 域 ≤5 文件提交 · 全量判定（13 笔）

| # | sha | PR | Rust 文件（数） | Go 载体 / 证据 | 判定 | 规模 | 避让面 | Win-only |
|---|---|---|---|---|---|---|---|---|
| 1 | `d83bb540ec` | 51678 | `.config/nextest.toml`, `core/tests/suite/mod.rs`, `core/tests/{suite=>}windows_sandbox.rs`（3） | 见 §2：Go 无 nextest/shard 配置、`sandbox/windowssandbox/smoke_windows_test.go:66` 环境门、测试已在独立包 | **已等价（测试基础设施）** | S | 否 | N/A |
| 2 | `ac9b5b8380` | 51527 | `linux-sandbox/src/bwrap.rs` + `tests/suite/denied_files_tests.rs`（2） | `sandbox/linuxsandbox/linux.go:591 expandLinuxDenyGlob`（in-process doublestar walker，从不 shell-out rg）；冻结测试 `sandbox/linuxsandbox/linux_test.go:148`（`:144-145` 自陈「Go never invokes ripgrep」） | **已等价** | S | 否 | 否 |
| 3 | `ccde2fc8b7` | 51407 | `linux-sandbox/src/bwrap.rs` + 测试（2） | 同上 walker；测试 `sandbox/linuxsandbox/linux_test.go:116`（`:111-115` 自陈 = Rust 的 protected-lookup fallback） | **已等价** | S | 否 | 否 |
| 4 | `9545947c6d` | 51511 | `exec-server/src/no_follow/windows{,_tests,_volume_fallback}.rs`（3） | `execserver/no_follow.go:19`（卷名+逐段 walk）；`execserver/no_follow_windows_test.go:19-25` 自陈「Go has no volume-root fallback because it never performs the strict NT open」、用 `FILE_FLAG_OPEN_REPARSE_POINT`；测试 `:26` | **已等价** | S | 否 | 是（RC 需 Windows） |
| 5 | `0b47040dc9` | 51483 | `exec-server/src/remote/connection_diagnostics.rs` + 测试（3） | `execserver/connection_diagnostics.go:5`（自陈 #51483）+ `execserver/remote.go:223` | **已落地** | S | 否 | 否 |
| 6 | `e32365a2c6` | 51350 | `exec-server/src/shell_snapshot.rs` + 测试（3） | 前轮判定：无载体（`update/syncw3_domain_scan_b17c74cfd5_2026_10_07.md` §L92） | **无载体**（勿重复清单） | S | 否 | 否 |
| 7 | `580b18cb74` | 51256 | `app-server/.../windows_sandbox_processor.rs`, `windows-sandbox-rs/src/provisioning_client.rs`, `lib.rs`（3） | 前轮判定：无载体（同报告 §L93 / §L147） | **无载体**（勿重复清单） | S | 否 | 是 |
| 8 | `de3721a7be` | 50940 | `core/...` + `windows-sandbox-rs/...`（3） | `sandbox/windowssandbox/deny_read_state.go:17/40/95`、`deny_read_state_io_windows.go:14/57`、测试 `deny_read_state_recovery_windows_test.go:29` | **已等价**（实现已存在；**落点在避让面内**、系预存在，本单不改） | S | **是（windowssandbox/）** | 是 |
| 9 | `8f82b8a31c` | 50803 | `app-server-daemon/README.md`, `cli/src/{main.rs,remote_control_cmd.rs}`, `tui/src/{daemon_startup.rs,lib.rs}`（5） | `app/remote_control.go:86` / `:131`、`app/remote_control_daemon_like_rust_test.go:19` / `:179`、`appserverdaemon/elevation.go:22`、`cli/cli.go:500`、`app/app.go:144` | **已落地** | S | 否 | 否 |
| 10 | `b989f795b1` | 50802 | `app-server-daemon/src/prepare_install_windows{,_tests}.rs`（2） | `appserverdaemon/prepare_install_windows.go:37` / `:85` / `:93`、测试 `prepare_install_windows_junction_test.go:16` | **已落地** | S | 否 | 是 |
| 11 | `d0759639f2` | 50782 | `app-server-daemon/src/prepare_install{,_windows,_windows_tests}.rs`（3） | `appserverdaemon/publish_release_windows.go:23`、`publish_release_other.go:12`、测试 `publish_release_test.go:12` / `publish_release_windows_test.go:13` | **已落地** | S | 否 | 是 |
| 12 | `55922b7984` | 50499 | `app-server-daemon/src/{manual_update.rs,update_loop.rs,update_loop_tests.rs}`（3） | `appserverdaemon/installer_windows.go:15`、`installer_other.go:27`、`update_job_windows.go:60`、测试 `installer_drain_other_test.go:12` / `installer_stderr_windows_test.go:12` | **已落地** | S | 否 | 部分（Windows 分支） |
| 13 | `8f7a0f7a87` | 50480 | `windows-sandbox-service/src/{ipc/authentication.rs,machine_policy.rs,machine_policy_tests.rs}`（3） | 唯一候选落点 = `sandbox/windowssandbox/`（**避让面**）；另 `git grep -E 'LoadEffective|config\\.Load|ConfigLayer|managedConfig|ManagedConfig' 8b2453a9 -- 'sandbox/windowssandbox/'` = **0 命中**（Go 该路径无 managed-config 加载步骤） | **撞避让面，待裁定**（亦可能判「已等价/无载体」，取决于 Go 是否真缺该 step） | S | **是** | 是 |

## 4. 真缺口候选

**本单 0 条**（无「生产可达 + Go 无等价 + 落点非避让面」的项）。

唯一待裁定项是 §3 第 13 行 **`#50480`**：

- Rust 行为：provisioning 请求同时带 `registered_core` + `refresh_only` 时，**跳过 managed configuration 加载**（registration-only 刷新沿用既有 sandbox，不该再拉一次云策略）。
- Go 探测：`registered_core` / `refresh_only` 只出现在 `sandbox/windowssandbox/`（`app_package.go:22/104`、`bin/setup_main/win/run_windows.go:49/126/192/226/242/271/332/337/508`、`setup_windows.go:52`）——**全部在避让面内**；`app/` `appserver/` `appserverdaemon/` 内 0 命中。
- 且 `git grep -E 'LoadEffective|config\.Load|ConfigLayer|managedConfig|ManagedConfig' 8b2453a9 -- 'sandbox/windowssandbox/'` = **0 命中**，即 Go 的 Windows 沙箱 setup 是**纯 payload 驱动**、根本没有 managed-config 加载步骤 ⇒ 即使放行避让面也大概率判「已等价/无载体」。
- ⇒ 请队长裁定：(a) 记为「无载体/已等价」收口；(b) 还是授权进 `sandbox/windowssandbox/` 做行为级 RC。

## 5. 非本域 ≤5 文件提交清单（90 笔，全量）

> 域 = §3 的 crate 集合；以下均不触域 crate（判定 = **非本域**，未逐条分类）。

| sha | PR | 顶层 crate | 标题 |
|---|---|---|---|
| `b17c74cfd5` | 51652 | `core` | Record telemetry for AGENTS.md changes made by apply_patch (#51652) |
| `a513012869` | 51642 | `guardian-context` | Fix retained context handling for typed section content (#51642) |
| `5a3140176e` | 51575 | `scripts/codex_package/cli.py,scripts/codex_pac` | Expose package assembly helpers and support gzip DotSlash artifacts (#51575) |
| `e95abcdf49` | 51547 | `config,core` | Add a Windows MXC sandbox opt-out (#51547) |
| `a9bc7bebfa` | 51499 | `rollout` | Load rollout history on a single blocking worker (#51499) |
| `b3b83ace17` | 51491 | `core-plugins` | Classify executor capability root ownership independently of parsing (#51491) |
| `0b863c69f5` | 51473 | `tui` | Preserve URL destinations in wrapped hook details (#51473) |
| `b0a5191bd6` | 51472 | `tui` | Preserve clickable URLs in TUI selection rows (#51472) |
| `4854cfa643` | 51471 | `tui` | Preserve clickable URLs in pending input previews (#51471) |
| `8519cde1ba` | 51470 | `Cargo.lock,app-server` | Raise the managed app-server file descriptor limit on Unix (#51470) |
| `5679e675f4` | 51467 | `app-server,core,protocol` | Keep submission logs useful without exposing payloads (#51467) |
| `44984d2081` | 51460 | `codex-api` | Retry realtime sideband attachment while an existing call activates (#51460) |
| `414d165b47` | 51459 | `tui` | Preserve wrapped help links in Windows sandbox prompts (#51459) |
| `858aea3449` | 51458 | `tui` | Make URLs clickable in user verification prompts (#51458) |
| `e44b9bb3da` | 51457 | `tui` | Preserve status usage hyperlinks when the URL wraps (#51457) |
| `4b5c111349` | 51452 | `tui` | Make banner URLs clickable across wrapped lines (#51452) |
| `5da229e54d` | 51451 | `tui` | Make URLs in the TUI warnings viewer clickable (#51451) |
| `eb2a33aa18` | 51450 | `tui` | Make URLs clickable in MCP elicitation prompts (#51450) |
| `44fe0289c6` | 51449 | `tui` | Make URLs clickable in TUI user input questions (#51449) |
| `a9abdeaff1` | 51441 | `core` | Fix core integration tests for updated turn APIs (#51441) |
| `f6cf05af1d` | 51440 | `codex-api,core` | Honor Retry-After in WebSocket error events (#51440) |
| `bc3ebcb77d` | 51439 | `tui` | Preserve clickable URLs in TUI approval headers (#51439) |
| `0ef50c9f66` | 51427 | `app-server-transport,core` | Prevent invalidated wakeups from starting a turn (#51427) |
| `6c2c8d5cfe` | 51426 | `.github/scripts/run-bazel-ci.sh,.github/workfl` | Remove the Bazel JVM override for Windows ARM64 voice builds (#51426) |
| `f71935b7f0` | 51425 | `.github/scripts/publish_r2_release.py` | Skip stable installer alias publishing for prereleases (#51425) |
| `f1dd04959c` | 51419 | `core` | Preserve turn attribution when queued mail wakes durable sleep (#51419) |
| `ff9ab4aed9` | 51411 | `tui` | Suppress repeated image paste presses in legacy terminals (#51411) |
| `a4ebc509f4` | 51400 | `app-server,ext` | Prevent later Guardian scores from releasing earlier pending reviews (#51400) |
| `dab2cd6056` | 51335 | `core` | Update collaboration snapshots for full-history fork instructions (#51335) |
| `79cae5f7fb` | 51334 | `ext` | Count Guardian denial-limit interruptions in telemetry (#51334) |
| `c2ae67d769` | 51332 | `core` | Record multi-agent wait duration by outcome (#51332) |
| `41acdad246` | 51331 | `core` | Track sub-agent result delivery outcomes (#51331) |
| `f5fa209bb0` | 51330 | `ext` | Measure total Guardian approval decision duration (#51330) |
| `162fcb3976` | 51257 | `scripts/install/install.ps1` | Fix installer checksum verification under Windows PowerShell (#51257) |
| `2f412e60d7` | 51235 | `tui` | Remove default model labels from TUI model pickers (#51235) |
| `28b91c7c31` | 51220 | `otel` | Honor the OTLP metrics temporality preference (#51220) |
| `93f8e79fd2` | 51202 | `core` | Distinguish namespace removals in incremental tool updates (#51202) |
| `ade17c62b0` | 51200 | `.bazelversion,MODULE.bazel.lock` | Upgrade Bazel to 9.2.0 and refresh the module lockfile (#51200) |
| `54dd21741d` | 51198 | `.github/workflows/rust-release.yml` | Allow concurrent release builds while serializing publication (#51198) |
| `b4e3726d73` | 51193 | `app-server` | Test thread archiving before the first turn (#51193) |
| `0d3868a30c` | 51192 | `tui` | Wait for SIGCONT when resuming the TUI (#51192) |
| `36df9544a3` | 51191 | `app-server-transport` | Clean up Unix app-server control-socket startup lock files (#51191) |
| `aa6635ece5` | 51185 | `code-mode,code-mode-host` | Retry transient gRPC code-mode session admission failures (#51185) |
| `7f21b5ee9c` | 51184 | `core` | Remove obsolete Guardian thread-context enables from tests (#51184) |
| `7c2ce90716` | 51158 | `.github/actions/windows-code-sign/action.yml,.` | Sign the PowerShell installer in Windows releases (#51158) |
| `3f1ccb7ceb` | 51140 | `core` | Isolate Guardian checkpoint recovery flags per review attempt (#51140) |
| `16cb72218c` | 51139 | `core,ext` | Force fresh Guardian sessions for parent-checkpoint recovery (#51139) |
| `4c9f42f4f8` | 51133 | `ext` | Allow Guardian Decisions to fall back to `OPENAI_API_KEY` (#51133) |
| `823ea830c0` | 51070 | `ext,guardian-context` | Preserve trusted-tool context in Guardian Decisions requests (#51070) |
| `315f0efb34` | 51065 | `ext` | Scope Guardian V2 response timing to snapshot sampling (#51065) |
| `c8949e55c2` | 51064 | `tui` | Ignore stale refresh responses in agents overview tests (#51064) |
| `cacdc46619` | 51063 | `core` | Honor prior cancellation before starting Codex delegates (#51063) |
| `435d1b3f19` | 51061 | `app-server` | Gate Guardian continuation tests on classifier request capture (#51061) |
| `7f892275e3` | 50977 | `core` | Isolate tracing in the strict third-party tool deferral test (#50977) |
| `afb436df8b` | 50811 | `tui` | Honor server reasoning summary defaults in new TUI threads (#50811) |
| `ab45264919` | 50804 | `core,tui` | Preserve review lifecycle ordering on failure (#50804) |
| `b8dceb0d4f` | 50788 | `tui` | Open slash commands from empty drafts in Vim Normal mode (#50788) |
| `f365d5754b` | 50781 | `tui` | Restrict TUI MCP startup notifications to owned threads (#50781) |
| `dde5f8c5ae` | 50764 | `tui` | Allow `/archive` while a turn is running (#50764) |
| `cd7d9e128c` | 50756 | `tui` | Show unavailable slash commands when searched in side conversations (#50756) |
| `b172810921` | 50700 | `cli` | Let the transport create the Windows remote-control socket directory (#50700) |
| `b741e480e2` | 50564 | `tui` | Allow transcript selection and copying while bottom modals are open (#50564) |
| `19e554bb70` | 50558 | `utils` | Avoid reading the current directory when resolving absolute paths (#50558) |
| `6b43e6fe1f` | 50555 | `tui` | Skip daemon auto-start for Windows-mounted WSL homes (#50555) |
| `7d5f55bdad` | 50531 | `app-server,core` | Persist realtime transcript tails before closure without inference (#50531) |
| `b65ab465ce` | 50525 | `cli,config` | Reject unknown TUI keys in strict config validation (#50525) |
| `86a54b051c` | 50516 | `core` | Add scenario coverage for remote `/compact` context preservation (#50516) |
| `af5d95f255` | 50510 | `tui` | Require GovCloud guidance acknowledgment after Bedrock setup (#50510) |
| `6c15cc4aaf` | 50477 | `tui` | Use the app-server default output cap for TUI workspace commands (#50477) |
| `be48ae396e` | 50470 | `utils` | Account for JSON overhead when truncating MCP tool results (#50470) |
| `a0bfaac70c` | 50464 | `core,features` | Add the `incremental_tools` feature flag (#50464) |
| `3c3a990da0` | 50454 | `rollout` | Measure rollout persistence size reductions (#50454) |
| `d4eed6dca5` | 50445 | `core` | Assert that only direct tool calls emit timing events (#50445) |
| `a4bfd07d51` | 50443 | `code-mode-runtime` | Stabilize paused-time code-mode service tests (#50443) |
| `3629508849` | 50442 | `backend-client,tui` | Preserve native USD amounts in thread usage responses (#50442) |
| `b6903c0669` | 50431 | `tui` | Preserve terminal hyperlinks in agents overview previews (#50431) |
| `44dd77b71e` | 50418 | `codex-api,core` | Honor Retry-After headers in failed Responses events (#50418) |
| `dff5270b29` | 50416 | `tui` | Clarify Git worktree choices for new and forked conversations (#50416) |
| `d61c7a824f` | 50396 | `tui` | Honor pager bindings for transcript page keys (#50396) |
| `f88a6efe43` | 50389 | `tui` | Honor configured keybindings before transcript navigation (#50389) |
| `2739e82858` | 50384 | `.github/scripts/macos-signing/sign_macos_code.` | Allow opting into 16 KiB ARM64 code pages for macOS signing (#50384) |
| `1ab8c6ef28` | 50380 | `app-server` | Fix thread unloading after disconnect during MCP startup (#50380) |
| `50b4c5e58c` | 50375 | `tui` | Use printable ASCII terminal titles under GNU Screen (#50375) |
| `91168365a5` | 50359 | `tui` | Render ANSI styles in TUI hook system messages (#50359) |
| `f4e18a95bb` | 50354 | `config` | Skip unrelated subtrees during config alias normalization (#50354) |
| `9d2b60303e` | 50348 | `app-server-transport` | Back off automatic remote control reconnects with jitter (#50348) |
| `4cedd0caac` | 50339 | `core` | Add end-to-end coverage for MCP sandbox state enforcement (#50339) |
| `ca466061d6` | 50273 | `ext` | Record Guardian V2 Decisions agreement and latency metrics (#50273) |
| `14a477ea89` | 50219 | `tui` | Bound tmux option probes to one second (#50219) |
| `d64360f628` | 50215 | `tui` | Support Ctrl+Insert for copying TUI selections (#50215) |

## 6. 超阈值（>5 文件）域提交（15 笔，未逐条分类）

> 派单口径只对 ≤5 文件的笔做五分类；以下列出供队长取舍。⚠️ 标注 = 看起来具安全/子系统性，可能值得单独排期。

| sha | PR | 文件数 | 标题 |
|---|---|---|---|
| `37eaae6eeb` | 51650 | 9 | Require hostname authorization before proxy DNS lookups (#51650) |
| `1fbe15c962` | 51595 | 31 | Add thread ID exclusions to `thread/list` (#51595) |
| `ed59a6c1cd` | 51525 | 13 | Preserve the CLI MXC preference in executor config reads (#51525) |
| `7efc49b258` | 51512 | 12 | Align Windows sandbox temp permissions with the child environment (#51512) |
| `ddabe594e6` | 51502 | 7 | Bound relay connection attempts and handle pongs during blocked writes (#51502) |
| `cfc946f4e1` | 51415 | 149 | Expose and persist turn lineage across the app server (#51415) |
| `588f616e8b` | 51347 | 6 | Measure shell snapshot use and wait time per command (#51347) |
| `7aa8f51049` | 51211 | 15 | Reject sandbox-writable bubblewrap executables from PATH (#51211) |
| `5ad6891696` | 51207 | 21 | Gate CLI Daybreak controls and selection behind an opt-in feature (#51207) |
| `42312d4ff4` | 51157 | 33 | Enforce required environment skills before model inference (#51157) |
| `1a169eda11` | 50559 | 6 | Distinguish daemon release identity from executable contents (#50559) |
| `c542fb93ef` | 50507 | 6 | Record Windows sandbox service stop diagnostics (#50507) |
| `ef8cfe5e96` | 50465 | 7 | Retry registry authentication outages and jitter executor reconnects (#50465) |
| `12a30d4e6d` | 50437 | 6 | Add a CLI command to uninstall the legacy Windows sandbox (#50437) |
| `c73775f19e` | 50360 | 13 | Remove initial messages from session configuration events (#50360) |

## 7. `syncw3b` worktree 回收（按流程：先证已落地）

blob 比对（`D:\qax\reagent\dev\codex_go_wt\syncw3b` vs `origin/main` = `8b2453a9`；本地为 CRLF 检出 ⇒ 先 CRLF→LF 归一，测试文件再剥 BOM）：

```
app/daemon_startup.go       norm-local 18957  landed 18957  IDENTICAL  753f9137065247a8
app/daemon_startup_test.go  norm-local 22995  landed 22995  IDENTICAL  265bea7f0ceb07da   (本地带 UTF-8 BOM，剥后一致)
app/interactive.go          norm-local 178877 landed 178877 IDENTICAL  c2250445eb0344ee
ALL_LANDED: True
```

（`265bea7f…` / 22995 B 与队长报告的落主后指纹一致，证实「BOM 已被队长落主时删除」这一有意偏差。）

```
$ git -C D:\qax\reagent\dev\codex_go worktree remove --force D:\qax\reagent\dev\codex_go_wt\syncw3b
remove rc=0
$ Test-Path D:\qax\reagent\dev\codex_go_wt\syncw3b
False
```

`git worktree list` 原文（本机，`syncw3b` 已消失；保留的是其它车道/队长的工作树）：

```
D:/qax/reagent/dev/codex_go               ed6f52b7 [integ86]
C:/Users/huoga/AppData/Local/Temp/v87b    e9849503 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/integ86b   9fafc187 [integ86d]
D:/qax/reagent/dev/codex_go_wt/integ86c   59d94a0a (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/integ86c2  9c4d578f [integ86c]
D:/qax/reagent/dev/codex_go_wt/integ86e   937836c5 [integ86e]
D:/qax/reagent/dev/codex_go_wt/integ86f   589703d8 [integ86f]
D:/qax/reagent/dev/codex_go_wt/integ86g   8b2453a9 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw1     bddaff34 [syncw1]
D:/qax/reagent/dev/codex_go_wt/syncw1base ed6f52b7 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw1c1   4ee41b7a (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw1gate 9620629e (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw1r86  937836c5 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw2     ed6f52b7 [syncw2]
D:/tmp/aud7_replay                        185d03c9 (detached HEAD)
D:/tmp/aud8_replay                        e9849503 (detached HEAD)
D:/tmp/syncw2/wt2e                        ee2d4e1d (detached HEAD)
D:/tmp/syncw2/wt624                       9cdcd43e (detached HEAD)
D:/tmp/syncw2/wt632                       185d03c9 (detached HEAD)
D:/tmp/syncw2/wt632base                   185d03c9 (detached HEAD)
D:/tmp/syncw2/wt632lf                     185d03c9 (detached HEAD)
D:/tmp/syncw2/wt97                        b9ad41d3 (detached HEAD)
D:/tmp/syncw2/wt984                       e9849503 (detached HEAD)
```

> 备注：此前轮次的 `syncw3` **分支**仍存在（`git branch --list 'syncw3*'` → `syncw3`），其 worktree 早前已按授权回收；本轮我未创建/未移动任何 ref。

## 8. 未决 / 待裁

1. **`#50480`**：撞避让面（唯一候选落点 `sandbox/windowssandbox/`），且 Go 该路径无 managed-config 加载步骤 ⇒ 请裁定记 N/A 还是授权动避让面。
2. **`#50940`**：Go 等价实现**已在避让面内**（`sandbox/windowssandbox/deny_read_state*.go`，预存在）。若队长认为「避让面内的已有实现」也需登记为已覆盖，请确认口径。
3. **§6 的 15 笔超阈值域提交**（含 `#51211` Reject sandbox-writable bubblewrap executables from PATH / 15 文件、`#50507` Windows sandbox service stop diagnostics / 6 文件）：均 >5 文件，按派单未逐条分类；如需展开请单独派单。
4. 本轮 pin 只前进 1 笔（测试基础设施），**域内无新的可落地行为缺口**。
