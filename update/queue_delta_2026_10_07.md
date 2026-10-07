# 同步队列重扫增量清单 · 固定点 Go main `58c711c9`

> 只读车道 `syncnext21` 产出。**未 commit / 未 push / 未动 main / 未建 worktree / 未改 update/**；本文件在 `/tmp`。

## 0. 固定点与复跑命令（本次实跑）

```bash
cd /home/jacks/jacks_dev/codex_go && git rev-parse main
# 58c711c9d01af1c8a986f7fc1439df4ec66cd765
git -C /home/jacks/jacks_dev/codex rev-parse HEAD
# 5a3140176e668a2f72f3c098490eb7f7052d9d85
git -C /home/jacks/jacks_dev/codex rev-list --count 5f3180c793..5a3140176e   # 499
awk -F'|' '/^\| #[0-9]+ \|/' update/remaining_ledger_2026_10_07.md | wc -l   # 769（全文件）
# §2 段 = 行 50-310（2.A 261 行）+ 行 316-317（2.B 2 行）= 263 行
```

逐条四判据：①`git log --format='%h %s' main --grep '#<PR>' --fixed-strings`；②`rg -n --glob '*.go' --glob '!update/**' '#<PR>' .`；③§2 记录最长标识符 snake/camel/Camel 于 Go 非测试 `.go`；④`git -C /home/jacks/jacks_dev/codex show --stat <sha>`。

## 1. 计数（263 行全量重判）

| 类 | 数 |
|---|---|
| (a1) §2 ⬜ → **已落地·硬证据**（PR 号 / sync 提交）| 10 |
| (a2) §2 ⬜ → **已落地·弱证据**（符号锚点，无 PR 号提交）| 30 |
| (a3) §2 ⬜ → **N/A** | 46 |
| (a3') §2 待裁 → N/A | 1 |
| (a0) §2 已标 ✅ / N-A，本次复核一致 | 31 + 3 |
| (b) **仍 ⬜** | **141** |
| (c) **存疑需人审** | 1（另 6 条附条件）|
| 合计 | 263 |

## 2. (a1) 判定已变 · 已落地（硬证据，10 条）

| #PR | 上游 SHA | sync 提交（`git log --grep '#PR' main`）| Rust 改动面 |
|---|---|---|---|
| #48772 | `fdbce2080c` | `8eb3100f` | 2f +50-1 | app-server-transport(1f)+uds(1f) |
| #48819 | `456212ca21` | `549148fd` | 6f +140-28 | otel(2f)+core(2f)+ext(2f) |
| #49069 | `33a0f766a6` | `03d4c28c; 116c4376; 88aa753e` | 6f +595-6 | state(6f) |
| #49099 | `1260716393` | `740d71b6; d14e0aa9` | 13f +713-93 | core-plugins(11f)+app-server(2f) |
| #49119 | `8bd5a136ff` | `500bc57a; d1c90a16` | 23f +225-13 | core(9f)+codex-api(4f)+prompts(1f) |
| #49160 | `0d7b8117d3` | `6e7e54f8; 2eb25440; 77732afa` | 55f +1814-506 | tui(53f)+config(1f)+cli(1f) |
| #50788 | `b8dceb0d4f` | `1c2ab6b0` | 5f +70-2 | tui(5f) |
| #50803 | `8f82b8a31c` | `adee1b30` | 5f +86-11 | cli(2f)+app-server-daemon(1f)+tui(2f) |
| #50804 | `ab45264919` | `a173ba7b` | 4f +98-5 | core(2f)+tui(2f) |
| #51063 | `cacdc46619` | `58c711c9` | 2f +43-30 | core(2f) |

命令与输出要点（原样）：

```
git log --format='%h %s' main --grep '#48772' --fixed-strings
  8eb3100f sync556: resolve long symlink Unix socket paths before connecting (#48772)
git log --format='%h %s' main --grep '#48819' --fixed-strings
  549148fd sync557: give skill catalog context metrics explicit histogram boundaries (#48819)
git log --format='%h %s' main --grep '#49069' --fixed-strings
  03d4c28c sync545 / 116c4376 sync544 / 88aa753e sync543  (#49069 stage C / batch / worker)
git log --format='%h %s' main --grep '#49099' --fixed-strings
  740d71b6 sync559 / d14e0aa9 sync550
git log --format='%h %s' main --grep '#49119' --fixed-strings
  500bc57a sync558 / d1c90a16 sync549
git log --format='%h %s' main --grep '#49160' --fixed-strings
  6e7e54f8 sync562 (part b) / 2eb25440 sync560 (part a) / 77732afa sync554 (c1)
git log --format='%h %s' main --grep '#50788' --fixed-strings
  1c2ab6b0 sync561: open slash commands from empty drafts in Vim Normal mode (#50788)
git log --format='%h %s' main --grep '#50803' --fixed-strings
  adee1b30 sync565: use the managed daemon for eligible remote-control launches (#50803)
git log --format='%h %s' main --grep '#50804' --fixed-strings
  a173ba7b sync566: preserve the review lifecycle order on failure (#50804)
git log --format='%h %s' main --grep '#51063' --fixed-strings
  58c711c9 sync567: honor prior cancellation before starting Codex delegates (#51063)
```

## 3. (a2) 判定已变 · 已落地（弱证据 / 符号锚点，30 条）

> 派单前建议人工确认一次：这 30 条**无 PR 号锚点**，机械式①②③无法复现「该 PR 即此改动」。

| #PR | 上游 SHA | Rust 改动面 |
|---|---|---|
| #48549 | `75a714843b` | 14f +437-45 | tui(14f) |
| #48628 | `8f195c93d7` | 8f +153-2 | tui(8f) |
| #48761 | `e75b6b1e0c` | 12f +263-34 | tui(12f) |
| #49032 | `46fdd5ef39` | 4f +238-6 | app-server(2f)+state(2f) |
| #49037 | `64bf4e7e62` | 4f +107-3 | tui(4f) |
| #49076 | `011f803f3c` | 3f +17-15 | git-utils(2f)+analytics(1f) |
| #49144 | `ff3c82c8a9` | 2f +197-95 | tui(2f) |
| #49153 | `c6c7c8d270` | 5f +227-9 | tui(5f) |
| #49261 | `f35a0fdc5d` | 1f +3-2 | windows-sandbox-rs(1f) |
| #49280 | `18194bfd35` | 4f +171-401 | app-server(1f)+core(3f) |
| #49295 | `cf12c86dc5` | 2f +32-20 | config(2f) |
| #49308 | `50d9c5deac` | 3f +6-4 | windows-sandbox-rs(3f) |
| #49472 | `b588812e8c` | 32f +833-249 | tui(32f) |
| #49642 | `67727e7cf1` | 7f +135-25 | core(4f)+config(1f)+app-server(1f) |
| #49702 | `3b16b5a5b0` | 3f +20-20 | exec-server(3f) |
| #49778 | `875bf9209b` | 6f +80-1 | exec-server-protocol(1f)+exec-server(4f)+file-system(1f) |
| #49796 | `5ca55db09c` | 6f +242-27 | guardian-context(3f)+app-server(1f)+history(2f) |
| #49806 | `a5d56d8120` | 24f +303-69 | app-server-protocol(22f)+(root)/sdk(2f) |
| #49811 | `c51f5bfb82` | 3f +32-0 | exec-server(3f) |
| #49816 | `1182834d13` | 1f +0-4 | tui(1f) |
| #49857 | `f58ed54a9d` | 12f +66-350 | tui(12f) |
| #49880 | `1f52d40704` | 22f +912-413 | core(21f)+app-server(1f) |
| #49939 | `6b4daafdb4` | 19f +548-40 | core(6f)+exec(6f)+(root)/sdk(5f) |
| #50105 | `e58b493329` | 2f +139-135 | tui(2f) |
| #50140 | `cb6da58876` | 13f +141-57 | tui(13f) |
| #50396 | `d61c7a824f` | 3f +132-11 | tui(3f) |
| #50431 | `b6903c0669` | 3f +95-17 | tui(3f) |
| #50503 | `9ce35d337a` | 15f +161-84 | tui(15f) |
| #50531 | `7d5f55bdad` | 4f +382-70 | core(3f)+app-server(1f) |
| #51221 | `2dbcab90e2` | 46f +373-212 | core(39f)+protocol(2f)+app-server(4f) |

抽验（本次实跑，符号存在）：

```
rg -n -l -e 'setAgentsOverviewBlankSession' .            -> ./tui/tea/agents_overview.go
rg -n -l -e 'blockquoteTargetText' .                     -> ./tui/chatwidget/copy_target.go
rg -n -l -e 'CollectGitInfoFromDir' .                    -> ./appserver/turn_runtime.go
rg -n -l -e 'func configVersion' .                       -> ./config/...
rg -n -l -e 'initializeDatabaseSettings' .               -> ./state/sqlite.go
rg -n -l -e 'ComposerFooterModeQuitShortcutReminder' .   -> ./tui/bottom_pane/chat_composer/popup_footer_state_test.go
rg -n -l -e 'MethodFSWriteBlock|FileWriteStreaming' .    -> ./execserver/server.go
rg -n -l -e 'DeduplicateRetainedInstructions' .          -> ./state/guardian_retained_context.go
rg -n -l -e 'RecordGrantedPermissions' .                 -> ./state/...
rg -n -l -e 'FlushTranscriptTailOnEnd' .                 -> ./realtime/...
rg -n -l -e 'turnEnvironmentSelections|TurnEnvironmentSelection' . -> ./appserver/...
```

## 4. (a3) 判定已变 · N/A（47 条：46 由 ⬜ + 1 由待裁）

| #PR | 上游 SHA | N/A 依据 | 复跑命令 |
|---|---|---|---|
| #48686 | `41f9084b30` | §9.B：删除 info 日志行，Go 无该行 | - |
| #48727 | `18344a972d` | §9.B：Rust 测试支撑 crate（零生产面） | - |
| #48799 | `4c8cf3964d` | Go TUI 不自行开鼠标采集 | `rg -F "EnableMouse|MouseCellMotion|?1006" tui/` =0 |
| #48829 | `e6f4af1d92` | windows-sandbox-rs 专属 provisioning client，Go 无 | `rg -i "WaitNamedPipe|SandboxProvisioningResponse" --glob "*.go" .` =0 |
| #48983 | `c0d26949be` | Go `session/store.go:335 MetadataPatch` 无时间戳字段 | `rg -n "MetadataPatch" session/store.go` |
| #49067 | `5a5a4aa796` | windows-sandbox-service 包，Go 无 | `rg -i "machine_policy" --glob "*.go" .` =0 |
| #49082 | `46d2585ea4` | Go 无 Git-discovery 的 turn diff 推导（`cwd_relative_turn_diffs` 零 reader） | `rg -n "cwd_relative_turn_diffs" --glob "*.go" .` -> features/features.go:199（注册，无 reader） |
| #49100 | `bfdb157178` | `plugin/` 恒用 `http.DefaultClient`，已复用进程级池 | `rg -n "http.DefaultClient|client.Do" plugin/*.go` |
| #49114 | `69043f05c4` | 唯一文件 tests.rs | `git show --name-only 69043f05c4` |
| #49246 | `0462dcc062` | linux-sandbox 测试 + lock | `git show --stat 0462dcc062` |
| #49257 | `4f16bdc265` | ext/guardian-v2 async scorer；Go 无 | `rg -F "CachedApproval" --glob "*.go" .` =0 |
| #49305 | `c2837d8ece` | Go 本地列举为单次整表查询（无 Rust 的 N+1） | §9.C；`rg -n "get_threads" state/` =0 |
| #49318 | `b1e72963c3` | 22 个 `.snap` 快照 | `git show --name-only b1e72963c3` |
| #49389 | `9212b3eca8` | 零生产 `.rs`（nextest/Cargo/tests/BUILD） | `git show --stat 9212b3eca8` |
| #49411 | `eefe0ce1a8` | §9.B：Rust 借用生命周期修复 | - |
| #49489 | `bcd6d9ab6b` | §9.B：analytics 回归测试 + 删两行 | - |
| #49692 | `d1c4e3c3e6` | §9.B：Rust 运行时专属 | - |
| #49694 | `5aa92804d2` | §9.B：Rust 运行时专属 | - |
| #49706 | `17e5a4dbcb` | §9.B：.github/Bazel/Dylint | - |
| #49708 | `4f699cd642` | §9.B：tokio spawn_blocking；Go 同步实现 | - |
| #49713 | `18131270fe` | 仓库内开发者 `.codex/skills` | `git show --name-only 18131270fe` |
| #49782 | `1a913493dc` | §13.C：exec-server 测试/结构 | - |
| #49787 | `83cf88306e` | BUILD.bazel×2 | `git show --stat 83cf88306e` |
| #49792 | `5f300d3f74` | ext/guardian-v2 + guardian-context | `rg -F "asyncScorer" --glob "*.go" .` =0 |
| #49798 | `c538fbabe5` | Rust `OnceCell<Arc>` 所有权重构，无 wire/行为差（§8.A） | `git show c538fbabe5 -- "*protocol*"` = 空 |
| #49818 | `a73898c249` | §13.C：test/build | - |
| #49972 | `a933dd77db` | §9.B：`Arc<Vec<u8>>`，线格式不变 | - |
| #50058 | `b06b7d2f77` | §12.1：windows-sys 依赖迁移 | - |
| #50066 | `d91294c39e` | ext/guardian-v2 `async_scorer/decisions.rs` | `rg -F "asyncScorer" --glob "*.go" .` =0 |
| #50166 | `7135b303d9` | lock / deny / audit | `git show --stat 7135b303d9` |
| #50219 | `14a477ea89` | §13.B：快照/测试 | - |
| #50273 | `ca466061d6` | ext/guardian-v2 async_scorer | `rg -F "wrapperLag" --glob "*.go" .` =0 |
| #50480 | `8f7a0f7a87` | windows-sandbox-service | `rg -i "only_registered_refresh|machine_policy" --glob "*.go" .` =0 |
| #50507 | `c542fb93ef` | windows-sandbox service diagnostics | `rg -i "service_diagnostics|ServiceStopReason" --glob "*.go" .` =0 |
| #50808 | `e57fc9ea5a` | 97 文件全为 TUI 快照 prune + `mod tests`；Go `.snap`=0 | `find . -name "*.snap" -not -path "./.git/*" | wc -l` =0 |
| #51065 | `315f0efb34` | ext/guardian-v2 `classification.rs` | `rg -F "asyncScorer" --glob "*.go" .` =0 |
| #51070 | `823ea830c0` | ext/guardian-v2 `decisions.rs` | `rg -F "TrustedTool|trusted_tool" --glob "*.go" .` =0 |
| #51133 | `4c9f42f4f8` | ext/guardian-v2 `startup.rs` | `rg -F "asyncScorer" --glob "*.go" .` =0 |
| #51192 | `0d3868a30c` | §13.B：快照/测试 | - |
| #51200 | `ade17c62b0` | .bazelversion + lock | `git show --stat ade17c62b0` |
| #51256 | `580b18cb74` | windows-sandbox-rs + service | `rg -i "CreateService" --glob "*.go" .` =0 |
| #51257 | `162fcb3976` | Rust 安装脚本 install.ps1（Go 无该脚本面） | `git show --name-only 162fcb3976` |
| #51350 | `e32365a2c6` | Go 无 executor 侧快照子系统 | `rg -n "shellSnapshot|shell_snapshot" execserver/` =0 |
| #51378 | `73178e7ca6` | ext/guardian-v2 WS 池；Go 无 ConnectionPool/start_refill | `for s in ConnectionPool start_refill replenish; do rg -F "$s" --glob "*.go" -l .; done` =0 |
| #51396 | `57d57df608` | Go 无 Guardian v2 异步评分器 | `rg -F "cached_approval_is_current" --glob "*.go" .` =0 |
| #51400 | `a4ebc509f4` | 同上 | `rg -F "wrapperLag" --glob "*.go" .` =0 |
| #51458 | `858aea3449` | Go TUI 无用户核验提示视图 | `rg -in "userVerification|prompt_header" tui/ app/ --glob "!*_test.go"` =0 |

## 5. (b) 仍 ⬜ 的 141 条（派单队列）

排序=PR 号升序。`Rust 改动面` = codex-rs 顶层模块(文件数) / 总文件数 / +行 −行。`落点候选` 取自 §2 证据列。

| #PR | sha | Rust 改动面 | §2 落点候选 | TUI/Bedrock/thread-list |
|---|---|---|---|---|
| #48565 | `228ae3da8d` | 2f +248-1 | sandboxing(2f) | （sandboxing：无直接 Go 包） | - |
| #48611 | `814de47b69` | 6f +33-29 | core(5f)+features(1f) | context//session//turn//rollout//state/；（features：无直接 Go 包） | - |
| #48725 | `88235f881d` | 54f +3101-207 | core(32f)+ext(10f)+thread-store(2f) | （codex-rs：无直接 Go 包）；（app-server-protocol：无直接 Go 包） | thread-list,Guardian,ext |
| #48775 | `596f8c5c3c` | 4f +45-7 | tui(4f) | tui/（部分在 app/） | TUI |
| #48812 | `3f4668da20` | 9f +105-24 | core(8f)+features(1f) | context//session//turn//rollout//state/；（features：无直接 Go 包） | - |
| #48824 | `84cc4b3fb5` | 3f +30-11 | voice-host(3f) | voicehost/ | - |
| #48827 | `6af89155d0` | 9f +345-18 | tui(9f) | tui/（部分在 app/） | TUI |
| #48828 | `81d5405882` | 2f +35-54 | app-server(2f) | appserver/ | - |
| #48895 | `44fe510ce3` | 6f +265-82 | mermaid(5f)+tui(1f) | tui/markdown/；tui/（部分在 app/） | TUI |
| #48982 | `06971ec9aa` | 4f +249-14 | core(3f)+ext(1f) | context//session//turn//rollout//state/；（Go 无 ext/ 目录） | ext |
| #49019 | `4fd5745e84` | 5f +211-71 | core(4f)+shell-command(1f) | context//session//turn//rollout//state/；（shell-command：无直接 Go 包） | - |
| #49038 | `368e5eae2f` | 22f +795-106 | core(7f)+guardian-context(14f)+ext(1f) | context//session//turn//rollout//state/；（Go 无 ext/ 目录） | Guardian,ext |
| #49041 | `3074be908a` | 5f +131-13 | tui(5f) | tui/（部分在 app/） | TUI |
| #49043 | `4f63088cce` | 3f +12-12 | tui(3f) | tui/（部分在 app/） | TUI |
| #49058 | `df3e439c02` | 3f +126-47 | windows-sandbox-rs(3f) | sandbox/（Windows 面） | windows |
| #49073 | `15c08beee2` | 6f +112-15 | tui(6f) | tui/（部分在 app/） | TUI |
| #49075 | `3749d1eff7` | 14f +899-93 | core(14f) | context//session//turn//rollout//state/ | - |
| #49079 | `fe50d010e2` | 5f +53-78 | tui(5f) | tui/（部分在 app/） | TUI |
| #49089 | `222e24b737` | 11f +683-82 | tui(11f) | tui/（部分在 app/） | TUI |
| #49093 | `19892aee0c` | 13f +83-187 | tui(13f) | tui/（部分在 app/） | TUI,windows |
| #49097 | `9563713df2` | 2f +19-6 | core(2f) | context//session//turn//rollout//state/ | - |
| #49105 | `136391a23e` | 20f +355-100 | tui(20f) | tui/（部分在 app/） | TUI |
| #49106 | `8f6517772b` | 22f +668-126 | tui(22f) | tui/（部分在 app/） | TUI |
| #49112 | `0196495288` | 20f +712-107 | tui(20f) | tui/（部分在 app/） | TUI |
| #49117 | `2e6cc4ed8d` | 9f +715-11 | analytics(7f)+core(2f) | telemetry/（Go 无 analytics/ 目录）；context//session//turn//rollout//state/ | - |
| #49118 | `6c49240565` | 2f +6-5 | model-provider-info(1f)+core(1f) | context//session//turn//rollout//state/；（model-provider-info：无直接 Go 包） | Bedrock |
| #49127 | `13f580ef09` | 10f +776-122 | ext(6f)+core(4f) | context//session//turn//rollout//state/；（Go 无 ext/ 目录） | ext |
| #49130 | `a9118edae8` | 4f +36-31 | core(4f) | context//session//turn//rollout//state/ | - |
| #49135 | `458f7046a5` | 10f +409-97 | models-manager(4f)+model-provider(2f)+core(2f) | appserver/；context//session//turn//rollout//state/ | TUI,Bedrock |
| #49138 | `f53f5a6fed` | 9f +245-17 | core(7f)+ext(2f) | context//session//turn//rollout//state/；（Go 无 ext/ 目录） | ext |
| #49145 | `8d48f71922` | 5f +38-16 | tui(5f) | tui/（部分在 app/） | TUI |
| #49147 | `3a16c0b707` | 1f +34-4 | cloud-tasks(1f) | （cloud-tasks：无直接 Go 包） | - |
| #49161 | `3226512d47` | 19f +468-59 | tui(19f) | tui/（部分在 app/） | TUI |
| #49164 | `fbc169827e` | 44f +380-71 | utils(7f)+windows-sandbox-rs(5f)+hooks(3f) | （codex-rs：无直接 Go 包）；appserver/ | Bedrock,exec-server,windows |
| #49171 | `c248f6d48b` | 1f +1-0 | tui(1f) | tui/（部分在 app/） | TUI |
| #49260 | `af0d68a236` | 46f +1800-785 | core(27f)+app-server(5f)+config(5f) | appserver/；（codex-mcp：无直接 Go 包） | TUI,Guardian |
| #49267 | `68e1a421f5` | 14f +259-3 | core(7f)+features(4f)+config(1f) | （codex-rs：无直接 Go 包）；config/ | - |
| #49269 | `0b1b78a4f1` | 29f +1250-296 | core(11f)+app-server(7f)+cloud-config(6f) | appserver/；（cloud-config：无直接 Go 包） | - |
| #49290 | `4773a132c3` | 23f +546-12 | tui(23f) | tui/（部分在 app/） | TUI |
| #49294 | `d79a95bdf8` | 10f +58-6 | ext(5f)+core(2f)+analytics(3f) | telemetry/（Go 无 analytics/ 目录）；context//session//turn//rollout//state/ | Guardian,ext |
| #49300 | `a6f09397aa` | 1f +52-31 | utils(1f) | （utils：无直接 Go 包） | - |
| #49312 | `63475131ce` | 7f +336-9 | core(7f) | context//session//turn//rollout//state/ | Guardian |
| #49345 | `8ffd91e42a` | 9f +153-37 | model-provider(2f)+core(2f)+tui(5f) | context//session//turn//rollout//state/；（model-provider：无直接 Go 包） | TUI,Bedrock |
| #49353 | `804d6306e8` | 10f +756-66 | core(10f) | context//session//turn//rollout//state/ | windows |
| #49360 | `995138d71a` | 13f +107-56 | core(9f)+exec-server-protocol(2f)+cli(1f) | （codex-rs：无直接 Go 包）；cli/ | exec-server |
| #49384 | `a44afa527c` | 24f +1464-352 | login(8f)+app-server(6f)+cli(5f) | （codex-rs：无直接 Go 包）；appserver/ | - |
| #49392 | `05ea5f757e` | 25f +1279-187 | rmcp-client(17f)+core(4f)+codex-mcp(2f) | （codex-rs：无直接 Go 包）；appserver/ | - |
| #49401 | `87d3e06847` | 13f +637-1348 | core(10f)+protocol(3f) | context//session//turn//rollout//state/；（protocol：无直接 Go 包） | - |
| #49416 | `ab84d71f57` | 4f +91-2 | ansi-escape(3f)+Cargo.lock(1f) | （codex-rs：无直接 Go 包）；（ansi-escape：无直接 Go 包） | - |
| #49432 | `d8f69ea8bc` | 4f +149-8 | app-server(2f)+login(2f) | appserver/；（login：无直接 Go 包） | - |
| #49441 | `6ba4bf9e64` | 6f +434-26 | core(3f)+codex-api(2f)+protocol(1f) | codexapi/；context//session//turn//rollout//state/ | - |
| #49444 | `8c3612fb63` | 3f +3-1 | rollout(2f)+Cargo.lock(1f) | （codex-rs：无直接 Go 包）；rollout/ | - |
| #49467 | `76a6e55d5a` | 5f +447-20 | core(5f) | context//session//turn//rollout//state/ | - |
| #49473 | `c9b3924a62` | 9f +218-567 | rmcp-client(5f)+Cargo.lock(1f)+(root)/MODULE.bazel.lock(1f) | （MODULE.bazel.lock：无直接 Go 包）；（codex-rs：无直接 Go 包） | - |
| #49478 | `15fd656ddb` | 11f +1008-9 | rmcp-client(8f)+protocol(1f)+network-proxy(2f) | （network-proxy：无直接 Go 包）；（protocol：无直接 Go 包） | - |
| #49517 | `d42056091a` | 32f +378-86 | tui(30f)+core(1f)+config(1f) | config/；context//session//turn//rollout//state/ | TUI |
| #49564 | `0b43721d8d` | 5f +85-9 | tui(5f) | tui/（部分在 app/） | TUI |
| #49584 | `a5cce8895a` | 4f +221-7 | core(4f) | context//session//turn//rollout//state/ | Guardian |
| #49599 | `d7b0d4aa66` | 10f +203-127 | thread-store(6f)+core(4f) | context//session//turn//rollout//state/；session/ | thread-list,Guardian |
| #49600 | `92bc601ad6` | 47f +372-102 | thread-store(20f)+core(22f)+app-server(1f) | appserver/；context//session//turn//rollout//state/ | thread-list,Guardian |
| #49624 | `7219fd735b` | 5f +316-2 | cli(3f)+tui(1f)+Cargo.lock(1f) | （codex-rs：无直接 Go 包）；cli/ | TUI |
| #49675 | `ed0cc1a4ab` | 3f +71-6 | codex-api(3f) | codexapi/ | - |
| #49678 | `fcbed044c8` | 5f +103-0 | tui(5f) | tui/（部分在 app/） | TUI |
| #49686 | `49be2c7ab0` | 12f +360-61 | core(5f)+agent-message-board-client(5f)+ext(1f) | （codex-rs：无直接 Go 包）；（agent-message-board-client：无直接 Go 包） | ext |
| #49690 | `7e8878f605` | 7f +344-11 | windows-sandbox-rs(6f)+core(1f) | context//session//turn//rollout//state/；sandbox/（Windows 面） | windows |
| #49714 | `f151a0f5c2` | 2f +21-13 | app-server(1f)+core(1f) | appserver/；context//session//turn//rollout//state/ | - |
| #49715 | `60947e2341` | 23f +832-4 | tui(23f) | tui/（部分在 app/） | TUI |
| #49781 | `8953de1f1a` | 9f +87-15 | core(5f)+codex-mcp(4f) | （codex-mcp：无直接 Go 包）；context//session//turn//rollout//state/ | - |
| #49783 | `106772e3c6` | 8f +145-20 | tui(8f) | tui/（部分在 app/） | TUI |
| #49785 | `1f77c0cfa6` | 4f +200-0 | app-server(2f)+thread-store(2f) | appserver/；session/ | thread-list |
| #49786 | `f542ba68e5` | 3f +29-11 | core(3f) | context//session//turn//rollout//state/ | - |
| #49793 | `726f1492db` | 25f +872-130 | ext(17f)+app-server(2f)+core(1f) | appserver/；context//session//turn//rollout//state/ | Guardian,ext |
| #49795 | `47a8bd7321` | 20f +300-70 | guardian-context(11f)+app-server(2f)+ext(5f) | （app-server-protocol：无直接 Go 包）；appserver/ | Guardian,ext |
| #49799 | `606b139565` | 5f +332-46 | tui(5f) | tui/（部分在 app/） | TUI |
| #49800 | `ec0cfa5da8` | 2f +48-13 | tui(2f) | tui/（部分在 app/） | TUI |
| #49809 | `2fde968de8` | 8f +229-7 | tui(8f) | tui/（部分在 app/） | TUI |
| #49810 | `cca440697f` | 4f +115-19 | tui(4f) | tui/（部分在 app/） | TUI |
| #49812 | `0f8df3d214` | 8f +626-160 | ext(8f) | （Go 无 ext/ 目录） | ext |
| #49814 | `8e44ad94b0` | 22f +795-51 | core(19f)+thread-store(2f)+core-api(1f) | （core-api：无直接 Go 包）；context//session//turn//rollout//state/ | thread-list |
| #49846 | `408f48ce0c` | 20f +388-34 | core(17f)+app-server(2f)+core-api(1f) | appserver/；（core-api：无直接 Go 包） | Guardian |
| #49847 | `b44ca872b1` | 29f +531-343 | core(24f)+ext(5f) | context//session//turn//rollout//state/；（Go 无 ext/ 目录） | ext |
| #49852 | `236be1ad9f` | 1f +46-6 | feedback(1f) | （feedback：无直接 Go 包） | - |
| #49855 | `bc197b77bc` | 8f +140-19 | tui(4f)+cli(2f)+app-server-daemon(2f) | appserverdaemon/；cli/ | TUI,windows |
| #49858 | `e1522188e9` | 51f +763-8 | tui(51f) | tui/（部分在 app/） | TUI |
| #49859 | `cd4a9cbd25` | 15f +431-62 | tui(15f) | tui/（部分在 app/） | TUI |
| #49861 | `8ea2428c38` | 10f +68-11 | tui(9f)+cli(1f) | cli/；tui/（部分在 app/） | TUI |
| #49874 | `bdfbab9f4f` | 33f +40-46 | tui(31f)+protocol(2f) | （protocol：无直接 Go 包）；tui/（部分在 app/） | TUI,windows |
| #49875 | `7b88d09d61` | 14f +219-149 | tui(14f) | tui/（部分在 app/） | TUI |
| #49876 | `d2b254fd17` | 29f +16-197 | tui(29f) | tui/（部分在 app/） | TUI |
| #49894 | `08a7031b6a` | 30f +336-250 | core(30f) | context//session//turn//rollout//state/ | - |
| #49898 | `da2e174a66` | 22f +558-293 | ext(14f)+core(7f)+tools(1f) | context//session//turn//rollout//state/；（Go 无 ext/ 目录） | ext |
| #49912 | `444da310e1` | 5f +95-8 | tui(5f) | tui/（部分在 app/） | TUI |
| #49946 | `dd90f160ed` | 3f +177-39 | file-search(3f) | （file-search：无直接 Go 包） | - |
| #49951 | `f70810bcd2` | 4f +141-57 | core(4f) | context//session//turn//rollout//state/ | Guardian |
| #49987 | `ecc78e4cf5` | 23f +2109-61 | rmcp-client(20f)+exec-server(3f) | execserver/；mcp/ | exec-server |
| #49993 | `57ac6f5163` | 4f +70-12 | guardian-context(2f)+app-server(1f)+ext(1f) | appserver/；（Go 无 ext/ 目录） | Guardian,ext |
| #50013 | `b527ce4734` | 9f +171-40 | tui(9f) | tui/（部分在 app/） | TUI |
| #50018 | `595534314f` | 76f +196-372 | core(14f)+app-server-daemon(6f)+utils(6f) | （codex-rs：无直接 Go 包）；appserverdaemon/ | TUI,Bedrock,Guardian,exec-server,windows |
| #50026 | `2685e3a4ce` | 6f +256-55 | core(6f) | context//session//turn//rollout//state/ | Guardian |
| #50039 | `a97fb78085` | 27f +732-146 | tui(27f) | tui/（部分在 app/） | TUI |
| #50045 | `1cc9917508` | 22f +643-115 | tui(22f) | tui/（部分在 app/） | TUI |
| #50050 | `9561a34531` | 20f +441-177 | core(12f)+ext(7f)+Cargo.lock(1f) | （codex-rs：无直接 Go 包）；context//session//turn//rollout//state/ | ext |
| #50052 | `57a38c1beb` | 10f +139-36 | tui(10f) | tui/（部分在 app/） | TUI |
| #50109 | `6ece7bfc21` | 19f +673-88 | tui(19f) | tui/（部分在 app/） | TUI |
| #50113 | `b707714ae4` | 11f +801-0 | cloud-client(8f)+Cargo.lock(1f)+http-client(1f) | （codex-rs：无直接 Go 包）；（cloud-client：无直接 Go 包） | - |
| #50148 | `e7ea5f4a86` | 14f +1220-44 | tui(14f) | tui/（部分在 app/） | TUI |
| #50189 | `a20fe6335f` | 2f +14-14 | rmcp-client(2f) | mcp/ | - |
| #50199 | `3f97f2b3bd` | 13f +112-11 | tui(13f) | tui/（部分在 app/） | TUI |
| #50200 | `a75987455a` | 4f +34-1 | cli(4f) | cli/ | - |
| #50207 | `e3c2a83937` | 7f +216-68 | tui(7f) | tui/（部分在 app/） | TUI |
| #50216 | `37f53199a6` | 15f +376-71 | tui(15f) | tui/（部分在 app/） | TUI |
| #50345 | `84d5437b6e` | 10f +398-101 | tui(10f) | tui/（部分在 app/） | TUI |
| #50359 | `91168365a5` | 2f +63-9 | tui(2f) | tui/（部分在 app/） | TUI |
| #50389 | `f88a6efe43` | 4f +148-15 | tui(4f) | tui/（部分在 app/） | TUI |
| #50416 | `dff5270b29` | 4f +14-9 | tui(4f) | tui/（部分在 app/） | TUI |
| #50433 | `66561d0301` | 12f +62-17 | tui(12f) | tui/（部分在 app/） | TUI |
| #50434 | `c536ffcb18` | 38f +1712-65 | tui(38f) | tui/（部分在 app/） | TUI |
| #50437 | `12a30d4e6d` | 6f +148-7 | cli(3f)+windows-sandbox-rs(3f) | cli/；sandbox/（Windows 面） | windows |
| #50445 | `d4eed6dca5` | 1f +15-0 | core(1f) | context//session//turn//rollout//state/ | - |
| #50454 | `3c3a990da0` | 1f +82-15 | rollout(1f) | rollout/ | - |
| #50467 | `f5a2272c6c` | 10f +147-49 | tui(10f) | tui/（部分在 app/） | TUI |
| #50504 | `d42aecc56b` | 47f +649-213 | tui(47f) | tui/（部分在 app/） | TUI |
| #50510 | `af5d95f255` | 5f +270-32 | tui(5f) | tui/（部分在 app/） | TUI,Bedrock |
| #50564 | `b741e480e2` | 5f +189-33 | tui(5f) | tui/（部分在 app/） | TUI |
| #50720 | `447eac3b81` | 8f +436-23 | tui(5f)+Cargo.lock(1f)+(root)/MODULE.bazel.lock(1f) | （MODULE.bazel.lock：无直接 Go 包）；（codex-rs：无直接 Go 包） | TUI,windows |
| #50727 | `3e238776e8` | 15f +101-72 | tui(15f) | tui/（部分在 app/） | TUI |
| #50756 | `cd7d9e128c` | 4f +202-20 | tui(4f) | tui/（部分在 app/） | TUI |
| #50781 | `f365d5754b` | 5f +159-30 | tui(5f) | tui/（部分在 app/） | TUI |
| #50786 | `acf9818fae` | 13f +235-12 | tui(8f)+config(1f)+core(3f) | config/；context//session//turn//rollout//state/ | TUI |
| #50913 | `c2f7fe89d8` | 8f +210-38 | tui(8f) | tui/（部分在 app/） | TUI |
| #51067 | `39e013c8b7` | 7f +112-53 | core(7f) | context//session//turn//rollout//state/ | Guardian |
| #51117 | `8571b9eaa4` | 28f +512-583 | core(26f)+ext(2f) | context//session//turn//rollout//state/；（Go 无 ext/ 目录） | Guardian,windows,ext |
| #51126 | `28a264fbc7` | 9f +624-0 | code-mode-runtime(5f)+code-mode-host(1f)+core(3f) | （code-mode-host：无直接 Go 包）；codemode/ | - |
| #51137 | `5f8b37cc65` | 11f +568-149 | core(11f) | context//session//turn//rollout//state/ | Guardian |
| #51139 | `16cb72218c` | 3f +162-16 | ext(2f)+core(1f) | context//session//turn//rollout//state/；（Go 无 ext/ 目录） | Guardian,ext |
| #51140 | `3f1ccb7ceb` | 2f +29-19 | core(2f) | context//session//turn//rollout//state/ | Guardian |
| #51156 | `c9253c4977` | 81f +1269-882 | core(75f)+tui(1f)+codex-api(4f) | codexapi/；context//session//turn//rollout//state/ | TUI,Guardian,windows,ext |
| #51185 | `aa6635ece5` | 4f +165-22 | code-mode-host(2f)+code-mode(2f) | （code-mode-host：无直接 Go 包）；（code-mode：无直接 Go 包） | - |
| #51206 | `c19525e55e` | 11f +155-16 | core(6f)+analytics(5f) | telemetry/（Go 无 analytics/ 目录）；context//session//turn//rollout//state/ | Guardian |
| #51230 | `80e0b51c9e` | 13f +405-68 | tui(6f)+thread-store(3f)+state(2f) | rollout/；state/ | TUI,thread-list |
| #51355 | `c0c230e673` | 16f +385-36 | core(12f)+otel(2f)+protocol(2f) | context//session//turn//rollout//state/；telemetry//otelinit/ | - |

## 6. (c) 判定存疑 / 需人审

| #PR | 上游 SHA | 不确定点 | 补证建议（可复跑） |
|---|---|---|---|
| #48779 | `21eb35513d` | 台账 §9.A 判 ✅，§9.C 同时记「注释引 `c2bcb9a26b`，需比对」——两段自相矛盾 | `rg -n -F 'ResetAfterParentCompaction' appserver/`；`git log --grep '#48779' main`(=0)；比对上游 `21eb35513d` 与 `c2bcb9a26b` |
| #49345 | `8ffd91e42a` | Go 仍对 Bedrock 全量强制 multi-agent v1（`model/catalog.go:1159 model.MultiAgentVersion="v1"`、`:939` 为 v1），与 Rust #49345「默认启用 v2」相反 ⇒ 判 ⬜ 成立，但属「Go = 上游 revert 前行为」的边界 | `rg -n 'MultiAgentVersion' model/catalog.go`；`git show 8ffd91e42a -- codex-rs/model-provider` |
| #48828 | `81d5405882` | 台账 §13.E 记「Go 明确实现为相反行为（空线程不可归档）」——单靠标识符 grep 必漏 | `sed -n '1,60p' appserver/rollout_archive.go`；比对 Rust `thread_processor.rs` 的空线程分支 |
| #49785 | `1f77c0cfa6` | 同上（命名空分页线程的持久化）：§13.E 记 Go 为相反行为 | `rg -n 'thread_name|NameThread' appserver/`；比对 Rust `thread_name_persistence.rs` |
| #49032 | `46fdd5ef39` | 弱 ✅ 归因到 #49102（半落地）；若口径要求「整 PR 行为齐备」应降为 ⬜ | `rg -n -F 'initializeDatabaseSettings' state/sqlite.go`；比对 Rust `app-server/src/lib.rs` 的 stderr span 改动 |
| #49305 | `c2837d8ece` | §9.C：Go 本地列举是单次整表查询，Rust 的 N+1 路径可能本就不存在 ⇒ 可能是机制性 N/A | `rg -n 'get_threads|GetThreads' state/ appserver/ --glob '!*_test.go'` |
| #50472 | `604061ce51` | 部分落地：Mantle 半 sync542 已并，Runtime 变体记 N/A | `git show --format='%h %s' d4922ab5`；`rg -n 'Astra|ultrafast' model/catalog.go` |
| #49069 | `33a0f766a6` | 三阶段已并（sync543/544/545），§2 备注仍有「partial beds」 | `git log --format='%h %s' main --grep '#49069'`；`rg -n 'vacuum|reclam' state/` |
| #49100 | `bfdb157178` | 台账存在两版实现（分支 `syncnext2` `7e153d74` 未并入），N/A 属队长裁决而非机械结论 | `git show 7e153d74 --stat`（若分支存在）；`rg -n 'http.DefaultClient|client.Do' plugin/*.go` |

## 7. (d) 上游窗口内「判 N/A 但属子系统移植」聚类（供立项）

| 聚类 | N/A 条目（本窗口） | 决定性 0 符号面（本次实跑） | 关联仍 ⬜ |
|---|---|---|---|
| Guardian v2 异步评分器 | #51396 #51400 #51378 #49257 #49792 #50066 #50273 #51065 #51070 #51133 | `for s in asyncScorer CachedApproval wrapperLag cached_approval_is_current SynchronousApprovalReviewer scoreIndex; do rg -F "$s" --glob '*.go' -l .; done` → 全 0 | #48725 #49294 #49127 #49793 #49812 #51139 |
| executor 侧 shell 快照 | #51350（另 #51347 的 exec-server 半） | `rg -n 'shellSnapshot\|shell_snapshot' execserver/` → 0 | #49360（wire `prependPathDirs`） |
| TUI 用户核验提示视图 | #51458 | `rg -in 'userVerification\|prompt_header' tui/ app/ --glob '!*_test.go'` → 0 | （真正落地需先移植 #43708/#43712 视图） |
| windows-sandbox-service 守护进程 | #49067 #50480 #50507 #51256 #48829 #50058 #49389 | `rg -i 'CreateService\|machine_policy\|service_diagnostics\|only_registered_refresh' --glob '*.go' .` → 0 | #49690（提权相对路径） |
| TUI 快照 prune 体系 | #50808（另 #50219 #51192 #49818） | `find . -name '*.snap' -not -path './.git/*' \| wc -l` → 0 | — |
| Git-discovery turn diff | #49082 | `rg -n 'cwd_relative_turn_diffs' --glob '*.go' .` → 仅 `features/features.go:199`（零 reader） | — |

## 8. 不确定项清单（必读）

1. **30 条弱证据 ✅（§3）无 PR 号锚点**：符号面在，但无法机械复现「该 PR 即此改动」；建议派单前按 §13.H 的「行号无关锚点」人工比对一次。
2. **§2 与「263 条 ⬜」前提不符**：§2 段实际是 `261 + 2 = 263 行`，其中 31 行已标 `✅`、3 行已标 `➖ N/A`、2 行为 `⬜(待裁定)`；本报告按「行判定 vs 现判定」给增量。
3. **本次 ① 命中的 45 个 PR 里含 plan/ledger/docs 类提交**（如 #49028/#49074/#49096/#49100/#50472/#49069 只被台账自身提及）——已按「sync 前缀提交才算落地」剥离。
4. **工作区出现非本次审计所致的改动**：审计期间 `git status` 从「仅 `?? scripts/loc_report.sh`」变为再含 **` M model/provider_info.go`**（mtime 2026-10-07 15:46:11，`git diff --stat` = 3+/9−，属队长登记的 `AmazonBedrockGPT54ModelID` cleanup）。**不是本车道所为**（本车道只跑 `git log/show/rev-parse/status`、`rg`、`awk`，产物只写 `/tmp`）。提醒：本仓是**共享工作区**，其他车道在并发写入 ⇒ 单独以 `git status` 判断「谁改了什么」不成立。
5. **`#48604` 的 §12.3 备注已过时**：Go 现已由 `sync531 34b5faea` 删除内嵌 `plugin-creator`（`systemskills/assets/samples/` 下仅剩 imagegen/openai-docs/skill-creator/skill-installer）⇒ 应为 ✅ 已落地。
6. 上游窗口提交数 499 已复跑确认；窗口起点 `5f3180c793` 未另行核验（沿用队长口径）。
7. 本报告**未**读 Rust 生产 diff 做逐条行为对抗（263 条 × diff）；仅做「PR 号/符号面/构建面」三层机械判定 + 对 47 条 N/A 抽验决定性命令。

## 9. 新鲜度 / 移动靶（审计期间 main 前进）

**⚠️ Go 仓在本次审计过程中继续前进**（这正是台账 §10.A/§85 反复警告的「移动靶」）。

```bash
git rev-parse main            # 现为 02fad9c963276dfc6b1650a7bd84b6d324550056（审计开始时 = 58c711c9）
git merge-base --is-ancestor 58c711c9 main && echo YES   # YES（快进，非改写）
git rev-list --count 58c711c9..main                      # 4
git log --format='%h %s' 58c711c9..main
# 02fad9c9 sync570: route the Bedrock Runtime provider to its regional endpoint and SigV4 service (#38470)
# 83528f93 sync569: report executor PATH directories in environment metadata (#49360, wire half)
# 917752a1 sync568: retry previous-model compaction with the selected model only when it can succeed (#51117)
# 7f11583f plan+ledger+runbook: round 85-8 ...
```

对 §2 的影响（本报告 §1–§8 仍固定在 `58c711c9`）：

| #PR | 原判定（58c711c9）| 现判定（02fad9c9）| 证据 |
|---|---|---|---|
| #51117 | ⬜（§5 第 b 表）| **✅ 已落地** | `git log --grep '#51117' main` → `917752a1 sync568`；`rg '#51117' --glob '*.go'` → `appserver/previous_model_compact.go:105` |
| #49360 | ⬜（wire 面，§2.B 裁定可派单）| **✅ 已落地（wire half）** | `git log --grep '#49360' main` → `83528f93 sync569`；`rg '#49360' --glob '*.go'` → `execserver/server.go:426/:3546` + `execserver/server_test.go` |

⇒ 以 `02fad9c9` 计，(b) 仍 ⬜ 应为 **139**（141 − 2），(a1) 应 +2 = 12。

**条内标记**：§5 表中 `#51117`、`#49360` 两行的判定已被 `02fad9c9` 推翻（保留原行以维持 `58c711c9` 固定点可复跑性）。
