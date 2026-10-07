# r86 落主台账（landing ledger）· 2026-10-07

> 用途：把「哪个 Rust PR → 哪笔 Go 落主 commit」记成单一事实源，补 synct5 审计指出的台账缺口（`#49799`/`#26114`/`#49345` 等未在 `update/*.md` 命中）。
> 生成方式（可复跑，权威来源 = git 历史，非人工抄写）：
> ```
> git -C D:\qax\reagent\dev\codex_go log --format='%h %s' refs/heads/main
> ```
> 核验：`git ls-remote origin refs/heads/main refs/heads/integ86g` → 两者同为 **c9fbd10f56d75e011b193f2bc3ad6e195bde05a9**（round88e 实测；历史 `8b2453a9` → … → `b041cbfd` → `9762262c` → … → `c9fbd10f`）。
> 范围：round 86 窗口（sync576 → sync632）+ **round87 增量段（sync633 → sync641，9 笔）** + **round88 增量段（sync642 → sync659，18 笔）**，均含各车道命名提交。增量段由队长（syntropy）负责复核并自跑值级 RC。

| sync | Go sha | 日期 | PR | 提交主题 |
|---|---|---|---|---|
| sync659 | `c9fbd10f` | 2026-10-07 | (#47974) | sync659: protect a resolved gitdir across writable roots like Rust (#47974) |
| sync658 | `999e655d` | 2026-10-07 | (#50786) | sync658: carry the agents-overview grouping through startup settings like Rust (#50786) |
| sync657 | `8fe41572` | 2026-10-07 | (#50786) | sync657: persist the Command Center grouping like Rust (#50786) |
| sync656 | `240913ad` | 2026-10-07 |  | sync656: interrupt a local side thread's turn before closing it like Rust |
| sync655 | `4df8a518` | 2026-10-07 | (#47657) | sync655: restrict the GovCloud Mantle model catalog like Rust (#47657) |
| sync654 | `e72df5b5` | 2026-10-07 | (#47590) | sync654: send a numeric custom reasoning effort as a JSON number like Rust (#47590) |
| sync653 | `b52105cf` | 2026-10-07 | (#39935) | sync653: require issuer-less token endpoints to share the authorization origin like Rust (#39935) |
| sync652 | `9762262c` | 2026-10-07 | (#49861) | sync652: seed a new thread's Daybreak preference like Rust (#49861) |
| sync651 | `b041cbfd` | 2026-10-07 | (#49861) | sync651: accept the daybreak terminal-title item in doctor like Rust (#49861) |
| sync650 | `668150fb` | 2026-10-07 | (#49861) | sync650: wire the Daybreak thread preference into the live status controls like Rust (#49861) |
| sync649 | `60b97880` | 2026-10-07 | (#49861) | sync649: add the Daybreak status-line surfaces like Rust (#49861) |
| sync648 | `d12d00dd` | 2026-10-07 |  | sync648: align fallback catalog field values with models.json |
| sync647 | `3140c46a` | 2026-10-07 | (#47565) | sync647: classify rollout read failures like Rust (#47565) |
| sync646 | `97107c7b` | 2026-10-07 | (#39935) | sync646: bind the MCP OAuth authorization endpoint to its issuer like Rust (#39935) |
| sync645 | `5bddd813` | 2026-10-07 |  | sync645: interrupt a side thread's turn before closing it like Rust |
| sync644 | `c9c6a200` | 2026-10-07 | (#51652 fix) | sync644: split AGENTS.md metric paths like Rust, including Windows-native ones (#51652 fix) |
| sync643 | `6315c3a7` | 2026-10-07 |  | sync643: keep the fallback catalog's serde-default booleans like the bundled models.json |
| sync642 | `fb24b81e` | 2026-10-07 |  | sync642: keep terminal hyperlinks in the command-center preview like Rust (#50431) |
| sync641 | `8b2453a9` | 2026-10-07 | #49855 | sync641: launch embedded with a warning from an elevated Windows terminal like Rust (#49855) |
| sync640 | `ee2d4e1d` | 2026-10-07 | #49642 | sync640: honor the managed windows.allow_mxc requirement like Rust (#49642) |
| sync639 | `1a465823` | 2026-10-07 | #40665 | sync639: classify queue-dispatched turns with turn_trigger=queue like Rust (#40665) |
| sync638 | `e9849503` | 2026-10-07 | #51652 | sync638: record codex.agents_md.edit for committed apply_patch changes like Rust (#51652) |
| sync637 | `922e0d34` | 2026-10-07 | #50389 | sync637: honor configured keybindings before transcript navigation like Rust |
| sync636 | `8b15a117` | 2026-10-07 | #49624 | sync636: forward the caller cwd when resuming a thread from the remote TUI like Rust |
| sync635 | `e6dce293` | 2026-10-07 |  | sync635: normalize Ctrl+Space in the TUI keymap runtime like Rust |
| sync634 | `c99ac975` | 2026-10-07 |  | sync634: expose the GPT-6 model family in the bundled fallback catalog like Rust |
| sync633 | `99a81b23` | 2026-10-07 | #47898 | sync633: preserve local-binding inheritance in environment network policies like Rust (#47898) |
| sync632 | `185d03c9` | 2026-10-07 | (#26114) | sync632: append inherited-model guidance to the V1 spawn description like Rust (#26114) |
| sync631 | `ab4b9f3e` | 2026-10-07 | (#51627) | sync631: render retained snapshot per reviewer framing like Rust (#51627) |
| sync630 | `89832492` | 2026-10-07 | (#51650) | sync630: require host authorization before proxy DNS lookups like Rust (#51650) |
| sync629 | `658dc9c1` | 2026-10-07 | (#49714) | sync629: decouple API-key cyber access programs from model discovery like Rust (#49714) |
| sync628 | `80062941` | 2026-10-07 | (#50756) | sync628: show side-conversation slash commands as disabled rows like Rust (#50756) |
| sync627 | `af6815b6` | 2026-10-07 | (#49584) | sync627: build no host skill catalog for guardian turns like Rust (#49584) |
| sync626 | `9a62462b` | 2026-10-07 | (#49345) | sync626: keep the Bedrock catalog multi-agent version and ultra reasoning like Rust (#49345) |
| sync625 | `fa85639d` | 2026-10-07 | (#49799) | sync625: forward the launch web-search choice to the server like Rust (#49799) |
| sync624 | `d5bfa4bb` | 2026-10-07 | (#26114) | sync624: state the inherited-model default in the V2 spawn description like Rust (#26114) |
| sync623 | `9cdcd43e` | 2026-10-07 | (#50531) | sync623: persist the realtime transcript tail without inference like Rust (#50531) |
| sync622 | `cd102426` | 2026-10-07 |  | sync622: read the origin URL through a .git gitfile like Rust |
| sync621 | `91260ed7` | 2026-10-07 | (#49308) | sync621: pin the sandbox console mode like Rust (#49308) |
| sync620 | `4ee41b7a` | 2026-10-07 |  | sync620: drop the carriage return when rewriting the session index like Rust |
| sync619 | `9936e2bf` | 2026-10-07 | (#49959) | sync619: cover the appended session index JSON line like Rust (#49959) |
| sync618 | `8ade48ed` | 2026-10-07 |  | sync618: keep blank lines when rewriting the session index like Rust |
| sync617 | `b9ad41d3` | 2026-10-07 | (#49260) | sync617: admit enterprise MCP authority only for the registration it was granted (#49260) |
| sync616 | `8d1177ee` | 2026-10-07 | (#49260) | sync616: retire the plugin enterprise MCP auth overlay (#49260) |
| sync615 | `9ec7f1c4` | 2026-10-07 | (#49260) | sync615: fail closed when the MCP configuration cannot be reloaded (#49260) |
| sync614 | `5c0e02cd` | 2026-10-07 | (#49432) | sync614: revoke the application network policy when the authenticated owner changes (#49432) |
| sync613 | `9620629e` | 2026-10-07 | (#49951) | sync613: include preceding assistant context in Guardian sender reviews (#49951) |
| sync612 | `47f0e558` | 2026-10-07 | (#49785) | sync612: persist empty paginated threads when naming them (#49785) |
| sync611 | `985ccf59` | 2026-10-07 | (#49912) | sync611: respect approval policies in temporary structured threads (#49912) |
| sync610 | `5078de6d` | 2026-10-07 | (#49147) | sync610: cover cloud base URL normalization like Rust (#49147) |
| sync609 | `04d1426a` | 2026-10-07 | (#49097) | sync609: emit a usage-limit turn error when post-turn compaction fails (#49097) |
| sync608 | `944ac1f8` | 2026-10-07 | (#49076) | sync608: read only the origin URL for skill analytics git info (#49076) |
| sync607 | `589703d8` | 2026-10-07 | (#49782) | sync607: clean up failed shell snapshot capture process groups (#49782) |
| sync606 | `dc340eb5` | 2026-10-07 | (#50727) | sync606: show model and reasoning effort in task details (#50727) |
| sync605 | `eeca361a` | 2026-10-07 | (#50359) | sync605: render hook system messages with ANSI styles (#50359) |
| sync604 | `106ce625` | 2026-10-07 | (#49079) | sync604: centralize TUI subscription labels (#49079) |
| sync603 | `9cf365ac` | 2026-10-07 | (#50437) | sync603: support CLI uninstall of the legacy Windows sandbox (#50437) |
| sync602 | `937836c5` | 2026-10-07 | (#48982) | sync602: do not let message-board notifications reopen a final answer (#48982) |
| sync601 | `84518d3f` | 2026-10-07 | (#49852) | sync601: log diagnostics for skipped feedback attachments (#49852) |
| sync600 | `d5dbe694` | 2026-10-07 | (#49269 Windows guard) | sync600: make the cloud-config cache permission assertion platform-aware (#49269 Windows guard) |
| sync599 | `c84a80ab` | 2026-10-07 | (#49269) | sync599: align cloud-config bundle atomic publish and policy revision (#49269) |
| syncw2 | `9fafc187` | 2026-10-07 | (#49145) | syncw2: hide reasoning summaries in the /status card for server connections (#49145) |
| syncw2 | `eb9d894c` | 2026-10-07 | (#50200) | syncw2: report the configured TUI mode in doctor (#50200) |
| syncw3 | `f15258cf` | 2026-10-07 | (#49075) | syncw3: inherit only ready or starting environments when spawning subagents (#49075) |
| syncw1 | `b2a153b0` | 2026-10-07 | (#48983) | syncw1: keep timestamp-only thread metadata observations narrow (#48983) |
| sync598 | `9c4d578f` | 2026-10-07 | (#51185) | sync598: retry transient gRPC code-mode session admission (#51185) |
| sync597 | `f6aecaf6` | 2026-10-07 | (#49675) | sync597: unify Responses request wire ordering across HTTP and WS (#49675) |
| sync596 | `39b953b4` | 2026-10-07 | (#49127) | sync596: deduplicate cloud and executor skill listings before budgeting (#49127) |
| syncl5 | `ed6f52b7` | 2026-10-07 | (#49702) | syncl5: align file-handle naming and limits with upstream (#49702) |
| syncl3 | `2e898a12` | 2026-10-07 | (#48611) | syncl3: centralize persistent mode enablement checks (#48611) |
| syncl3 | `e176a441` | 2026-10-07 | (#51627) | syncl3: separate retained assistant context from the instruction prefix (#51627) |
| syncl1 | `67f1df26` | 2026-10-07 | (#48819) | syncl1: round the logarithmic context bucket boundaries to Rust's libm values (#48819) |
| syncl1 | `05859a28` | 2026-10-07 | (#48772) | syncl1: resolve long symlink control-socket paths in the daemon client (#48772) |
| sync595 | `39dd8e94` | 2026-10-07 | (#51595 window) | sync595: re-anchor critical-file parity pins to upstream head 5b0b253035 (#51595 window) |
| sync594 | `7bfe8349` | 2026-10-07 | (#50454) | sync594: measure rollout persistence size reductions (#50454) |
| sync593 | `7d0d586c` | 2026-10-07 | (#51611) | sync593: signal abandonment of unanswered MCP elicitations (#51611) |
| sync592 | `c4e0e9c9` | 2026-10-07 | (#48895) | sync592: cover the expanded Mermaid syntax on the TUI fence path (#48895) |
| sync589 | `531903df` | 2026-10-07 | (#51126) | sync589: port the promise settlement streaming helpers to code mode (#51126) |
| sync591 | `2b309b8b` | 2026-10-07 | (#22159) | sync591: resolve managed hook commands for the current platform (#22159) |
| sync590 | `3d0a9ae7` | 2026-10-07 | (#33895) | sync590: dispatch SessionEnd hooks by matcher like Rust (#33895) |
| sync588 | `a893bac3` | 2026-10-07 | (#49360) | sync588: carry shell invocation metadata through unified exec (#49360) |
| sync587 | `3e1aaa99` | 2026-10-07 | (#49267) | sync587: cover the remote message board loader and credentials (#49267) |
| sync586 | `0a1a21cc` | 2026-10-07 | (#48828) | sync586: assert the archived promptless thread on the RPC path (#48828) |
| sync585 | `26a8088f` | 2026-10-07 | (#49267) | sync585: support the remote multi-agent message board (#49267) |
| sync584 | `e35358ff` | 2026-10-07 | (#48828) | sync584: materialize promptless threads before archiving (#48828) |
| sync583 | `b78a6898` | 2026-10-07 | (#49295) | sync583: hash discovered hooks with Rust's canonical identity (#49295) |
| sync582 | `2badf18e` | 2026-10-07 | (#49360) | sync582: restore executor PATH directories for local launches (#49360) |
| sync581 | `f3da80d9` | 2026-10-07 | (#49058) | sync581: repair Windows sandbox ACLs for long runtime paths (#49058) |
| sync580 | `e635f087` | 2026-10-07 | (#48895) | sync580: expand native Mermaid flowchart syntax support (#48895) |
| sync579 | `cd978432` | 2026-10-07 | (#49441) | sync579: honor server retry advice for overload and retry-limit Responses errors (#49441) |
| sync578 | `071fe00f` | 2026-10-07 | (#49100) | sync578: reuse one HTTP connection pool for remote plugin requests (#49100) |
| sync577 | `6b226e02` | 2026-10-07 | (#48824) | sync577: keep voice RTP timestamps on the 20 ms packet grid (#48824) |
| sync576 | `d3d69a15` | 2026-10-07 | (#49360) | sync576: restore executor PATH directories inside POSIX login shells (#49360) |

合计 **78** 笔（round 86 窗口 66 笔 + round87 增量段 9 笔；以 `git log refs/heads/main` 为准）。

## round88 落主增量（sync642 → sync659，18 笔）

> 追加列：**Go sha / Rust 上游 sha / PR / 文件数 / 主题 / RC 结论**（RC 均为队长自跑的值级 RC；本表由 syncl5 只读核对 `git show --stat` 与 commit body）。
> 范围一致性：`git log --format='%h' 8b2453a9..c9fbd10f` = 本表 18 个 sha（**union == range ✓**）。

| sync | Go sha | Rust 上游 sha | PR | 文件数 | 主题 | RC 结论 |
|---|---|---|---|---|---|---|
| sync642 | `fb24b81e` | `b6903c0669` | `#50431` | 2 | keep terminal hyperlinks in the command-center preview | 队长自跑：退回 `stripANSISGR` ⇒ `hyperlink_preview_like_rust_test.go:19` FAIL（恢复 sha256 `cfc9e2e1…` ok） |
| sync643 | `6315c3a7` | —（对齐 `codex-rs/models-manager/models.json` / `protocol/src/openai_models.rs:438/441` serde-default，无单一 PR） | — | 2 | keep the fallback catalog's serde-default booleans | 队长自跑：删 `applyBundledCatalogBooleans` 调用 ⇒ `TestBundledFallbackCarriesCatalogJSONBooleans` FAIL（恢复 sha256 `302e694a…` ok） |
| sync644 | `c9c6a200` | `b17c74cfd5`（本体 #51652） | `#51652 fix` | 2 | split AGENTS.md metric paths incl. Windows-native | 队长自跑：回退 `path.Base` ⇒ `TestApplyPatchAgentsMdEditMetricWindowsNativePathLikeRust` FAIL（native_separator / absolute_path；恢复 sha256 `37b9982a…` ok） |
| sync645 | `5bddd813` | `95dafbc7b5`（#18190；`side.rs:428/433`+`549-558`；replay-only 细化见 #49800 `ec0cfa5da8`） | — | 2 | interrupt a side thread's turn before closing it | 队长自跑：阉割 interrupt 调用 ⇒ `TestInteractiveRemoteCloseSideInterruptsRunningSideTurnLikeRust` FAIL（want `turn/interrupt` before `thread/unsubscribe`；恢复 sha256 `984b8181…` ok） |
| sync646 | `97107c7b` | `7f9832d0d0` | `#39935` | 2 | bind the MCP OAuth authorization endpoint to its issuer | 队长自跑：中和 `validateMCPOAuthAuthorizationServerEndpoints` ⇒ 跨源/lookalike 用例 FAIL（恢复 sha256 `9caa9cbf…` ok） |
| sync647 | `3140c46a` | `d6c4c6aea4` | `#47565` | 3 | classify rollout read failures by reason and progress | 测试载体 `rollout/line_reader_47565_test.go`（提交 body 未附值级 RC 原文） |
| sync648 | `d12d00dd` | —（对齐 `models.json` @ pin `b17c74cfd5`，无单一 PR） | — | 2 | align fallback catalog field values with models.json | 测试载体 `TestFallbackCatalogCarriesModelsJSONFieldValues`（提交 body 未附值级 RC 原文） |
| sync649 | `60b97880` | `8ea2428c38` | `#49861` | 8 | add the Daybreak status-line surfaces | 队长自跑：删 `chatwidget/status_controls.go` 的 `case StatusLineDaybreak` ⇒ `daybreak_status_surfaces_like_rust_test.go:32` ×3 FAIL（恢复 sha256 `eb1257a5…` ok） |
| sync650 | `668150fb` | `8ea2428c38` | `#49861` | 7 | wire the Daybreak thread preference into the live status controls | 队长自跑：删 `tui/tea/status_controls.go` 的 `DaybreakEnabled: m.daybreakEnabled,` ⇒ `daybreak_status_surfaces_test.go:25` FAIL（恢复 sha256 `2eff22c6…` ok） |
| sync651 | `b041cbfd` | `8ea2428c38` | `#49861` | 2 | accept the daybreak terminal-title item in doctor | 队长自跑：删 `doctor/doctor.go` 的 `case "daybreak":` ⇒ `doctor_test.go:1140` FAIL（恢复 sha256 `6efb7a55…` ok） |
| sync652 | `9762262c` | `8ea2428c38` | `#49861` | 4 | seed a new thread's Daybreak preference | 队长自跑：撤 wiring ⇒ `daybreak_default_test.go:34` FAIL |
| sync653 | `b52105cf` | `7f9832d0d0` | `#39935` | 5 | require issuer-less token endpoints to share the authorization origin（**关闭 sync646 声明的 declared gap**） | 队长自跑：撤 origin 绑定 ⇒ `oauth_issuer_binding_test.go:234` FAIL |
| sync654 | `e72df5b5` | `ae132dc50a` | `#47590` | 2 | send a numeric custom reasoning effort as a JSON number | 队长自跑：删 `MarshalJSON` ⇒ `responses_agent_test.go:4254` FAIL |
| sync655 | `4df8a518` | `5bae5b563e` | `#47657` | 3 | restrict the GovCloud Mantle model catalog | 队长自跑：撤 gov 接线 ⇒ `provider_test.go:343` FAIL |
| sync656 | `240913ad` | `95dafbc7b5`（#18190；blame `app/side.rs`） | — | 2 | interrupt a local side thread's turn before closing it | 队长自跑：撤 interrupt ⇒ `interactive_sideclose_test.go:104` FAIL |
| sync657 | `8fe41572` | `acf9818fae` | `#50786` | 5 | persist the Command Center grouping | 队长自跑：撤持久化 ⇒ `…persist_like_rust_test.go:45` FAIL |
| sync658 | `999e655d` | `acf9818fae` | `#50786` | 3 | carry the agents-overview grouping through startup settings | 队长自跑：撤回灌 ⇒ `…settings_like_rust_test.go:23` FAIL |
| sync659 | `c9fbd10f` | `a92ccbde53` | `#47974` | 2 | protect a resolved gitdir across writable roots | 队长自跑：撤 `addResolvedGitDirCarveouts` ⇒ 自建 Windows overlay 探针 FAIL（`resolved gitdir carveout missing`） |

## 独立发布审计（synct5）收档

| 审计产物 | 区间 | 结论 |
|---|---|---|
| `update/verify_main_9fafc187_2026_10_07.md` | — | GREEN |
| `update/verify_main_9c4d578f_2026_10_07.md` | — | GREEN |
| `update/verify_main_9cdcd43e_2026_10_07.md` | — | GREEN |
| `update/verify_main_4ee41b7a_2026_10_07.md` (11420 B) | `b9ad41d3..4ee41b7a` 3 笔 | GREEN（计数更正 6→3） |
| `update/verify_main_9a62462b_2026_10_07.md` (13827 B) | `4ee41b7a..9a62462b` 6 笔 | GREEN（tree-sha `c27a7e39…`；RC 4 条锚点全复现） |
| `update/verify_main_658dc9c1_2026_10_07.md` (10243 B) | `9a62462b..658dc9c1` 3 笔 | GREEN（tree-sha `5b37cf22…`） |
| `update/verify_main_185d03c9_2026_10_07.md` (12691 B) | `658dc9c1..185d03c9` 3 笔 | GREEN（tree-sha `00a81805…`；逐笔行为可观测） |
| `update/verify_main_e9849503_2026_10_07.md` (17322 B) | `185d03c9..e9849503` 6 笔 | GREEN（tree-sha `e12a79bc…`；默认模型 priority 驱动已探针证明） |
| `update/verify_main_ee2d4e1d_2026_10_07.md` (17723 B) | `e9849503..ee2d4e1d` 2 笔 | GREEN（tree-sha `d69b63b5…`；sync640 端到端探针证明真接线） |
| （待出）`verify_main_*` | `8b2453a9..c9c6a200` 4 笔（sync641/642/643/644） | 已派单 synct5（batch15 已完，本段在飞） |

## 非阻塞提示（供发布说明）

1. **`#49308`（sync621 `91260ed7`）**：旧台账曾判 N/A；实为**结构/表形状对齐**（`creation_flags` 表与 Rust 逐格一致），piped 路径行为不变，且无 stdio 分支**有意保留** `CREATE_NO_WINDOW`（Rust 表给 0，Go 代码内已注明）。**请勿标为行为修复。**
2. **`#26114`** 只加 guidance 文案（V2 = sync624，V1 = sync632），功能面窄。
3. **`#51627`** 在 round 86 内被落过两次：`e176a441`（syncl3，先拆出 retained assistant context）→ `ab4b9f3e`（sync631，按 reviewer framing 收敛为「同步单段 / 仅异步拆分」）。后者是前者的修正，**以前者为基础的复核需以 sync631 为准**。

## 开放跟踪项（open tracking）——均已结项

| 项 | 状态 | 收口证据 |
|---|---|---|
| `#51652` 计数面 | **CLOSED**（sync638 `e9849503`） | `git grep -n 'codex.agents_md.edit'` 由 `exit=1` 变为有命中；采 seam A（进程全局 `metrics.Counter`，车道无关） |
| `#51652` Windows 原生路径漏计 | **已知缺陷 → 修复在飞**（预期 sync642） | `update/syncw4_verify_51652_2026_10_07.md` 的 REFUTED 段：`sub\AGENTS.md` / 绝对 `C:\...` 落盘但计 0；修法 `path.Base`→`filepath.Base` |
| D1 `turn_trigger=queue`（#40665 漏项） | **CLOSED**（sync639 `1a465823`） | 避让面 `appserver/runtime_router.go` 已开闸并落主；RC 撕接线 ⇒ `queued_dispatch_test.go:168/:209` FAIL |
| D2 remote resume cwd override | **CLOSED**（sync636 `8b15a117`，#49624） | `app/remote_tui.go` 转发调用方 cwd；RC 撕转发 ⇒ `thread/resume cwd=<nil>` FAIL |
| `#42370`（MCP 启动失败 warn 日志） | **暂不落**（低价值诊断面，无机器判据） | syncl4 域扫描 #2：`"MCP server startup failed"` 全仓 0 命中；Go 已有 `MCPStartupObserver` 回调面 |
| `#39935`（issuer binding 翻案） | **待事实前置** → 已派单 syncl4 只读取证 | `parity/commits.json` 记 N/A(verified)，但 Go 无 issuer↔authorization-endpoint origin 一致性校验（76f47103fe 无对应面）；先回答「多端点面是否存在」 |

## 例外登记与已知缺陷（exceptions / known defects）

| 项 | 结论 | 证据 |
|---|---|---|
| `syncw3_49855.patch` 的 UTF-8 BOM | 补丁给 `app/daemon_startup_test.go` 带 BOM，落主前删前 3 字节 ⇒ 该文件 `git apply --check --reverse` **必然失败，属预期**，不得判「未落地」 | 落主后 LF 22995 B、sha256 `265bea7f…`（首字节 `package `） |
| sync638 Windows 原生路径漏计 | **真实缺陷**：补丁路径含 `\` 时 `codex.agents_md.edit` 计 0（under-count）；根因 = POSIX-only `path.Base` 作用于未归一的 `AppliedChange.Path` | `update/syncw4_verify_51652_2026_10_07.md`（16465 B）；syncw1 修复 → sync642 候选 |
| sync641 由队长（syntropy）亲写 | 流程同车道：补丁 → 复核 → 自跑值级 RC → 落主，非例外 | `update/r86_batch16_2026_10_07.md` |

## 上游枚举 pin

- **当前枚举 pin = `d83bb540ec`**（2026-10-07，#51678 Move Windows sandbox tests into a dedicated integration binary）。
- 历史：`5b0b253035`（round86 起点）→ `b17c74cfd5`（round87：#51651/#51652）→ **`d83bb540ec`**。
- ⚠️ 与 parity 用的 Rust 检出 `C:\rw\codex-rs`（固定 `5b0b253035`，勿动）**不是同一概念**。

## round87c 决议与 backlog（2026-10-07）

（本节由队长记账；已落主项在上方主表，未落主者如下）

| 项 | 状态 / 裁定 | 证据 |
|---|---|---|
| `#51652` Windows 原生路径漏计 | **CLOSED**（sync644 `c9c6a200`） | `filepath.Base` + `ChangeUpdate` gate；syncw1 交付（`syncw1_51652_windows_path.patch` 6856 B / `d2a8f69a…`），队长自跑 RC：回退 `path.Base` ⇒ `apply_patch_agents_md_metrics_like_rust_test.go:372/:398` FAIL |
| `#50431` 命令中心预览保留终端超链接 | **LANDED**（sync642 `fb24b81e`，syncl3） | 队长自跑 RC：退回 `stripANSISGR` ⇒ `hyperlink_preview_like_rust_test.go:19` FAIL |
| 回退目录的 serde-default-两个布尔字段与 models.json 不一致 | **LANDED**（sync643 `6315c3a7`，syncl6） | 队长自跑 RC：删 `catalog.go:1066` 调用 ⇒ `catalog_test.go:2138` FAIL（离线默认模型不发 `reasoning.summary`） |
| `#39935` issuer 绑定（Go 多 AS 面存在） | **已批准实现**（syncl4，在飞） | `parity/commits.json` 原记 N/A(verified)，现翻案；`mcp/oauth_discovery.go:54/82/503` 遍历多 AS 且无 origin 绑定 |
| `#50786` 记住 Command Center 分组 | **已批准，拆 A/B 两笔**（syncl3 排队） | A = 写路径（`tui/`）；B = 启动恢复（`app/`+`config/` 读取面，已授权） |
| `#49861` Daybreak 进状态栏/标题 | **已批准为单笔 ≤7 文件**（显式放宽） | 数据源已存在（`session/store.go:227`、`appserver/protocol.go:715`），`grep DaybreakEnabled tui/` = 0 |
| `#48761` 切片（配置化 `open_transcript` 键 + 省略未绑定提示） | **已批准**（2-4 文件，syncl3 排队） | 第③点（区分保留截断 vs 存储丢弃）不做 |
| `#50781` MCP 启动通知限定 owned thread | **N/A 结案**（宿主未接线） | `RouteServerNotification` / `NewThreadEventChannel` 除定义外只被 `*_test.go` 调用，活 TUI 是单轮 MCP 状态 |
| `#49130` content-filter guidance 搬迁 | **N/A 结案** | Go 无 `handle_response_stream_error` 共享 handler（0 命中）、无 RemoteCompactionV2 路径 |
| `#51211` sandbox-writable bwrap | **N/A — 已落主**（sync449 `cbaf8ee6` + sync467 `bbd92988`） | Go `sandbox/sandboxpath/*` 逐条对位 + 7 条 LikeRust 测试 |
| `#51678`（新 pin 的那一笔） | **C3 无载体 N/A** | Go 无 nextest / test-group；Windows 测试本就 per-package binary + `CODEX_WINDOWS_SANDBOX_SMOKE` 环境门 |
| `#50480` registered_core+refresh_only | **N/A 结案（(a)）** | 命中全在 `sandbox/windowssandbox/`（避让面）；`LoadEffective|managedConfig` 在该目录 0 命中⇒ Go setup 纯 payload 驱动，无 managed-config 加载步骤 |
| Go 缺 `prefer_mxc` → MXC 链路 | **已知 Go↔Rust 功能缺口**（语义冻结，no driver） | synct5 batch15 审计：Rust `core/src/config/mod.rs:3541` → `resolve_windows_sandbox_type`；Go 无 `features.prefer_mxc` 消费点 |
| `#42370` MCP 启动失败 warn | **暂不落** | 无机器判据（同「128/32 字面值」那一档） |
| ws 采样路径缺 guidance/重试预算 | **已登记 backlog**（需先定架构口径） | `model/responses_agent.go:972 runWebSocket`；触发面 `appserver/guardian_reviewer.go:311`（避让面） |
| side-close 缺 `turn/interrupt` | **已交补丁，待落主**（syncw2） | `syncw2_sideclose_interrupt.patch` 11108 B / `f5dd6682…` / 2 文件 |
| side-close startup interrupt（空 `turn_id`）/per-thread 升级/本地 in-process 路径 | **已登记 backlog** | 空 `turn_id` 需改 `turn/` + `appserver/runtime_router.go`（避让面）；本地路径已派 syncw2 只读 scoping |
| 新增 flaky（整包顺序性） | 已登记已知 flaky 名单 | `TestRuntimeRouterTurnStartRestoresThreadDynamicTools`（base-only，隔离 3/3 PASS→整包耦合）同 `TestRuntimeRouterAppMention…` 族 |
| 新增 flaky（`tui/tea`） | 已登记 | `TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge`（本机现有基线，两侧同态） |

### round88 决议增量与状态刷新（syncl5 记账，2026-10-07）

> 仅列**本轮新增或状态变化**的条目；上表中已记录且本轮未变者（`#51211` / `#49130` / `#51678` / `#50480` / `#50781` / ws 采样 guidance·重试预算 / Go 缺 `prefer_mxc`→MXC 链路）不重复。

| 项 | 状态 / 裁定 | 证据 |
|---|---|---|
| `#39935` issuer 绑定 | **状态刷新：LANDED**（sync646 `97107c7b`，syncl4） | `mcp/oauth_discovery.go` 新增 `validateMCPOAuthAuthorizationServerEndpoints`（origin 比对 + web-endpoint 要求） |
| `#39935` 的 **issuer-less 臂** | **已立项**（≤7 文件） | Rust 要求 token endpoint 与 authorization endpoint 同源；落地会触及 12 个既有测试 fixture（≥4 文件）⇒ 现作为**已声明的 Go/Rust 差异**，由 `mcp/oauth_issuer_binding_test.go` 固定 |
| `#39935` issuer-less 臂（状态刷新） | **CLOSED**（sync653 `b52105cf`） | 关闭 sync646 声明的 declared gap；4 个 fixture 归位同源；RC：撤 origin 绑定 ⇒ `oauth_issuer_binding_test.go:234` FAIL |
| `#50189` Mercado Pago OAuth 例外 | **被 sync646 覆盖** | sync646 把 `issuer_binding.rs` 的例外表（`mercadopago` / `robinhood`）逐字带入 `mcp/oauth_discovery.go` |
| `#51465` | **backlog（待裁定）** | 本轮新增登记 |
| `#51221` | **backlog（待裁定）** | 本轮新增登记 |
| `code_mode_only_strict_3p_tools` 族 | **整体立项候选（backlog）** | 需整体立项，非单车道具移植 |

## 用户决策清单（累积）

1. 5 组冻结子系统 cluster 是否纳入目标（Guardian v2 异步评分器 / executor shell 快照 / TUI 核验视图 / windows-sandbox-service / TUI 快照 prune）。
2. 是否放宽「≤5 文件」（`#51651` 17 文件、`#50781` 需架构决策、GPT-6 bundled）。**（更正：`#50454` 已落 sync594 `7bfe8349`；`#49778` 已随 `#50177` 落 sync352 `1da0383b`——`fs/writeBlock`/`fileWriteStreaming`/`fs-open mode` 均在主上，二者均不在待议列表）**
3. 既有时序 flaky（`TestOtelProviderReloadsAfterAccountChange` 等）是否单独立项。
4. Bedrock TUI 引导屏族（`#50510` 前置）是否立项。
