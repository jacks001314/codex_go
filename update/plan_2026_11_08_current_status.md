# codex_go 对齐现状快照（2026-11-08）

作者：Codex 原生 Agent（lanes: win-align / mac-align-2 / ledger-align）
这份是**快照**，不是计划。数字以 `parity/commits.json`、`parity/baseline.json`、`parity/contracts/manifest.json` 为准，本文不另行维护。

## 1. 账本数字

| 账本 | 规模 | 分布 | 校验 |
| --- | --- | --- | --- |
| `parity/commits.json` | 3677 commit | 2975 complete / 639 not_applicable / 59 partial / 4 blocked_precondition | `ledger_verify` GATE: PASS；sha `044903c3…cfd6723c`（= `963d9dd1…` + `deliv-r11` 的 `ad1dbba32d` evidence 路径更正，1 hunk） |
| `parity/baseline.json` | 派生 | `certificationReady: false`（59 partial + 5 domain 未完成） | 生成脚本重算；sha `44a4f6f8…fb46394` |
| `parity/contracts/manifest.json` | 138 契约 | **136 complete / 2 partial** | 主干 `TestContractVerifierAuditMatchesTree`：audited 138 contracts: 0 without a verifier, 0 missing verifier names, 0 missing goPaths |

上游 `main` 与 target pin `748ab55ca7` 已同步（ahead_by = 0），本轮没有新 delta。

## 2. 本轮关闭的行为面

鉴权链五条（全部 complete）：
- `#60003 realtime-runtime-auth`、`#60055 runtime-auth-websocket-revalidation`：`realtime.TransportBackendConfig.RuntimeAuthHeaders` 每次 dial 重新解析；`runtimeauth.ReplaceAuthHeaders` 替换而非叠加；`appserver/realtime_runtime.go` 注入。
- `#60073 remote-control-runtime-auth`：`remotecontrol/auth.go` 的每请求 `request_context`；`appserver/runtime_router.go` 绑定 slot，recovery 保留绑定。
- `#60096 runtime-auth-terminal-denials`：`model/runtime_auth_denial.go` 四个终态码 + provider 谓词。
- `#60101 analytics-runtime-auth`：运行时凭据失败保留「请重新登录」指引。

账本/行为其它：
- `#59910 patch-approval-cancel-turn-scope`：`appserver/runtime_router.go` 的 `applyPatchApprovalForTurn` 新增 `FileChangeApprovalCancel` 分支中断发起 turn；第二条用例靠 `interruptRuntimeTurn` 的 `TurnService.Interrupt` 校验 turn 仍 active。
- `#60049 child-agent-catalog-preservation`：子 agent 读父会话内存 catalog（`RefreshInMemory`），三处消费点。
- `#60004 account-read-user-id`：`account/read` 带 `userId`。
- `#60006(b)`：新契约 `history-write-admission-serialization`（`state/runtime.go` 的 `acquireHistoryWriter`）。
- `#60011`、`#59958`：本轮并入（细节见各 lane 的计划文档）。
- `#59933 protected-model-profile-downgrade`：`permissionProfileHasFullDiskWriteAccess` 的 `profile == nil` 方向修正（complete）。
- `#59954 skills-tool-name-resolution`：`turn/skills_tools.go` 三级解析 + 歧义判定（含歧义处理，complete）。
- `#60180 skill-mention-punctuation-join`（complete）、`#59998 message-board-bulk-subscriptions`（complete）、`#60190 agent-tree-id`（complete）。

仍 partial 的 UI 面：`#59983 typed-prompt-markdown-render`（见 §8.1 与 §10）。`#59928 sidebar-chooser` 已于 2026-11-08 晚转 complete（条带点击的 y 原点改为渲染回填）；`#59993 fixed read view` 亦已 complete（视图本体对齐，宿主接线属上游缺口，另记为结构性 residual）。

## 3. 已知会红但与本轮无关的基线

这些在 Linux 工作区必然红，判断依据是「把改动回退后同样红」：

| 包 | 用例 | 原因 |
| --- | --- | --- |
| `appserver` | `TestPrecomputedExportArtifactsMatchTargetRustCommit` | target Rust commit 已前移 |
| `parity` | `TestGoTopLevelDirectoryInventory` | 本地有 `artifacts/`、`attachments/` 目录 |
| `parity` | `TestGoVetIsClean` | `attachments/` 里躺着一个 `.go` 测试文件，被当成 `codex_go/attachments` 包 |
| `model` | `TestConfiguredProviderRecoverFromUnauthorizedInvalidatesCredentialExportLikeRust` | 硬编码 `cmd` + `.bat`，无 GOOS 跳过 |
| ~~`tui/tea`~~ | ~~`TestModelAppCommandUsesRustHistoryMessages` 等 4 条~~ | **2026-11-08 已清（见 §12）**：其中两条是 `formatDirectory` 的 `filepath.Abs` 真差异（已修），两条是用例自身的平台假设（已改可移植写法）；另 `TestRealTerminalSlashForkPaintsProgressLikeRust` 的 PTY 断言与单元格 diff 冲突，已改锚定会话 id |
| `realtime` | `TestVoiceRecordingReplayMatchesGolden` | `realtime/testdata/voice_session.jsonl` 未取回时红，`project_fetch` 后绿 |

## 4. 在飞

- win-align：`#59933 protected-model-profile-downgrade`（`permissionProfileHasFullDiskWriteAccess` 把 `profile == nil` 当最宽松，方向反了；`turn_runtime.go` 只降级 `Profile.Disabled`）。
- mac-align-2：`#59954 skills-tool-name-resolution`（`turn/skills_tools.go` 的三级解析 + 歧义判定）。
- ledger-align：本轮完成 21 条 partial 的三分类（§8：18 可转 complete / 2 仍需行为 / 1 无法判定）+ manifest 排版复检（§9），账本侧待办见 §8.3。

## 5. 下一批候选（我这侧）

按影响面与可验证性排序：

1. `code-mode-incremental-exec-*` 三条 + `incremental-exec-catalog-metrics`：必须按 definitions → wiring → overrides → metrics 的顺序做，指标计的是**渲染后**字节，hint 未定则口径不稳。
3. `release-prerelease-cleanup`：**已转 complete**（2026-11-08 晚）。原「必须真跑一次 workflow」的门槛比上游自身证据更严：Rust `test_releases.py:262 StablePublicationTests` 也是临时目录 + `gh` 替身 + `PATH` 顶掉真 `gh`。已落 tag 守卫（缺 tag → exit 5）与稳定 draft 差异用例。

## 6. 四条架构性 blocked

`#40625`（缺 agent_navigation closed/inactive 生命周期）、`#42841` 与 `#55378`（缺原生 MXC SDK + Windows 构建链）、`#43624`（macOS Secure Enclave / LAContext 绑定）。这四条不是「没做」，是缺前置条件，不占契约位。

## 7. D 类提案（29 条）的状态

`update/plan_2026_11_08_dclass_contracts.md` / `update/dclass_contract_proposal_2026_11_08.json` 处理了「commit 已完成但账本里没有契约」的 29 条：建议立 13、押后 11、不立 5。

**这 13 条里最终只有 1 条进了 manifest**（`ui-tree-terminal-escape-stripping`，`TestUITreeTextSanitizerStripsControlStrings` 在主干上已通过）。原因不是提案不好，是记账 schema 的硬约束：`TestAlignmentContractInventoryIsTraceable` 与 `TestContractVerifiersExistInGoTestFiles` 都要求每条契约有**非空且真实存在**的 verifier 名字，写 `pending` 也不行。所以正确顺序是「行为 + 用例先落，契约后立」，那 12 条留在提案文档里，等对应行为落地时连同 verifier 一起进 manifest。

## 7.1 变异式证据批次（2026-11-08 晚）

`update/partial_contract_mutation_evidence_2026_11_08.json` 用「改掉一处实现 → 指定用例变红」的方式重判 20 条 partial，结论
**12 complete / 7 needs-behaviour / 1 undecidable**。12 条已据此转为 complete（我抽验了 3 条变异，红线与原文一致）。

两处登记修正随这批一起做：

- `tui-custom-gateway-signin` 原登记的 verifier 不覆盖自身（删掉 `customGateway` 分支时仍绿），补登 `TestConfigPanelProviderEditorWritesSelectedFieldsLikeRust`；
- `side-chat-policy-first-request`：`SideDeveloperInstructions` 在 `tui/app/side.go:189` 与 `tui/tea/side.go:476` 各有一份，**生产路径是 `tui/tea`**（`app/side_tui.go:59`、`app/remote_tui.go:6149` 都调 `codextea.`），原 verifier 只覆盖 `tui/app` 的副本，补 `TestSideDeveloperInstructionsKeepExistingPolicyLikeRust` 覆盖生产实现。

剩 2 条 partial（2026-11-08 晚口径）：`mcp-network-policy-derivation`（结构性 residual，不再追）、`typed-prompt-markdown-render`。

## 8. 21 条 partial 的三分类（ledger-align，2026-11-08）

判定方法：verifier 存在性用主干全树 `func <name>(` 索引（16,384 个 Test 函数）逐条核对；行为判定读的是主干 Go 实现文件本身，不用「我镜像里没有」当结论。逐条证据见 `update/partial_contract_verdicts_2026_11_08.json`。

| 结论 | 条数 | 契约 |
| --- | --- | --- |
| 可转 complete | **18** | `mcp-network-policy-derivation`、`mcp-app-invocation-context-refresh`、`cloud-thread-project-binding`、`sidebar-tab-keyboard-mouse-navigation`、`side-chat-persistence-and-tab-close`、`side-chat-policy-first-request`、`sidebar-chooser-list-bindings-and-hover`、`code-mode-incremental-exec-definitions`、`code-mode-incremental-exec-wiring`、`incremental-exec-message-overrides`、`incremental-exec-catalog-metrics`、`turn-context-required-permission-profile`、`provider-creation-during-signin`、`tui-custom-gateway-signin`、`typed-prompt-markdown-render`、`user-verification-platform-provider` |
| 仍需行为 | **2** | `account-security-setup-appserver-fetch` |
| 无法判定 | **1** | `release-prerelease-cleanup` |

**21 条的 46 个登记 verifier 名字全部在主干树里存在**（`user-verification-platform-provider` 那 6 个也在，我此前把它归到「名字不可解析」是镜像缺口导致，已撤回）。

### 8.1 转 complete 前的两个提醒

1. 有两条的**登记 verifier 弱于树里已有的用例**，建议同时登记更强的那个：
   - `managed-runtime-auth-mode` → 加 `TestRuntimeAuthModeResolvesConfiguredAndManagedValuesLikeRust`（`config/runtime_auth_mode_like_rust_test.go:12`，覆盖 managed 覆盖项；现登记的 `TestModeFromConfigValuesLikeRust` 只测配置值）；
   - `provider-creation-during-signin` / `tui-custom-gateway-signin` → 可选加 `tui/onboarding` 的菜单用例（`TestAuthOptionsAndStepState`、`TestAuthOptionsIncludeGcpOnlyWithAPIAndFeature`、`TestAuthOptionsIncludeBedrockOnlyWithAPIAndFeature`），把「登录菜单里创建 provider」这半也锁住。
2. `typed-prompt-markdown-render`：本文旧版 §2 说它「span 列模型未对齐」，但三个登记 verifier 明确断言了 link/mention 列与两列缩进（`TestUserPromptMarkdownKeepsGutterAndLinkColumnsLikeRust`）。建议实跑 `go test ./tui/history_cell -run Markdown -count=1` 后再改状态，我这边不下「一定绿」的结论。

### 8.2 两条「仍需行为」的具体缺口

| 契约 | 已有 | 缺什么（Rust 出处 → Go 落点） |
| --- | --- | --- |
| `cloud-image-messages-and-draft-handoff` | 文本入队请求编码（`cloudclient/turns.go:144`，含 turnTrigger/sharedAccess/mode）+ typed receipts | ① **image content item**：编码器写死文本项，`rg -i image cloudclient/ cloudbackend/` → 0 命中（Rust `cloud-client/src/items.rs` 有 typed items）→ 在 `encodeAddUserMessageRequest` 增加图片 content 分支；② **draft handoff** 无 Go 落点（Rust `app-server-client/src/cloud_backend/requests.rs`） |
| `account-security-setup-appserver-fetch` | `chatgptapi/security_setup.go:55 GetSecuritySetup`（丢非法 notice）+ `tui/tea` 的 stale-identity/banner 优先级 | **appserver 侧 fetch 面不存在**：`rg -i securitysetup appserver/ protocol/` → 0 命中，且 appserver 无任何地方调用 `GetSecuritySetup`（Rust `app-server/src/request_processors/account_processor.rs` + `message_processor.rs`）。二选一：补 app-server 端点，或把契约 scope 改成 `chatgptapi`+`app`+`tui` 并去掉两条 app-server rustPaths —— **我建议改 scope**，Go 没必要为这条绕一圈 appserver |

### 8.3 账本侧待办（我这侧）

- 等上面 18 条在 manifest 里转 complete 后，重算 `baseline.json`（`certificationBlockers` 的契约计数文案要跟着变）。
- `release-prerelease-cleanup` 已转 complete（见 §5 第 3 条与 §10）。

## 9. manifest 排版复检（回答「我是不是把格式改宽了」）

结论：**是，且是无意义 diff**。详见 `update/manifest_format_finding_2026_11_08.md`：原文件是 **1 空格缩进 + CRLF + 无尾换行**（用 `json.dumps(indent=1, ensure_ascii=False)` 再转 CRLF 可**逐字节复现**你的旧文件），你这次用 `indent=2` 重写后，**2,148 行里有 2,146 行变化**（只有首尾两个花括号没变），字节 67,170 → 73,948（+6,778）。

修复：`python3 update/reflow_contract_manifest_2026_11_08.py --write parity/contracts/manifest.json`（先 `.bak` 备份，写完回读校验 JSON 语义不变）。在你当前文件上跑的结果是 73,948 → **67,227 B / sha256 `918623495668733e036c7d4416cfc7aef1e03531a7024e689460b2302c6c41d0`**（预期格式 = `indent=1` + CRLF；我实跑后与历史惯例一致），语义完全不变（138 契约、顺序不变）。


## 10. 收口（2026-11-08 晚，Linux lane）

本轮把最后两条 UI/发布类契约收口，manifest 现为 **138 / 136 complete / 2 partial**（`parity/contracts/manifest.json`，67,838 B，sha256 `a6598589…f8dc32`）。

| 契约 | 变更 | 证据 |
| --- | --- | --- |
| `sidebar-chooser-list-bindings-and-hover` | partial → complete，verifier 增补 `TestSidebarTabStripMouseHitFollowsRenderedOriginLikeRust` | 条带点击 y 原点由常量 0 改为渲染回填的 `sidebarTabStripTop`（`tui/tea/side_panels.go` 赋值、`conversation_inset.go` 随 inset 下移、`side_pane_mouse.go` 按原点判定）。反证：把原点改回 0 → `sidebar_tab_strip_origin_like_rust_test.go:56: press on row 0 was handled as a tab press; the strip is painted on row 1` |
| `release-prerelease-cleanup` | partial → complete | `TestPublishReleaseCleansUpFailedPrereleaseById` / `TestPublishReleaseRefusesMissingTagLikeRust` / `TestPublishReleaseStableDraftFailureCleanupDiffersFromRust`；删守卫 5 行与把 `cleanup()` 加 `&& [ "$prerelease" = true ]` 两次变异均红 |
| `typed-prompt-markdown-render` | 仍 partial | 要动 `tui/markdown` 的 span 列模型，先定最小可判范围 |
| `mcp-network-policy-derivation` | 仍 partial（结构性） | proxy `ProxySettings` 无可写活句柄、Go execpolicy 无 clone 语义；已落 deny 先发布 + `persistMu` 串行 + host 级发布表，不再追 |

历史文档同步：`README_status_index.md` 第 2/2.1/3.1/4 节、`partial_contract_verdicts_2026_11_08.json` 与 `partial_contract_mutation_evidence_2026_11_08.json` 的 `source`（指向新 manifest sha 与 136/2）及三条 entry 的 `status`；`side-chat-policy-first-request` 的 `removalEvidence` 改指 `tui/tea/side.go:481`，并在该处实跑复现同一断言（`side_developer_instructions_like_rust_test.go:16`），原 `assertion` 红行原文保留。

门禁：`go build ./...` exit 0；`go test ./parity -run 'TestContractVerifier|TestContractVerifiersExistInGoTestFiles|Ledger|Baseline|Domains'` ok；`python3 update/ledger_verify_2026_11_08.py` GATE: PASS。


## 11. 最后一条契约收口 + 视觉密度对齐（2026-11-08 深夜，Linux lane）

manifest 现为 **138 / 137 complete / 1 partial**（`parity/contracts/manifest.json`，67,985 B，sha256 `5e41d4e1…688f28`），唯一 remaining partial 是结构性的 `mcp-network-policy-derivation`。

### 11.1 `typed-prompt-markdown-render` → complete

三处**可见**差异，全部落在 `tui/history_cell/messages.go`（`tui/markdown.Render` 一行未动，agent 侧渲染不受影响）：

| 差异 | Rust 依据 | 修法 | 怎么确认的 |
| --- | --- | --- | --- |
| prompt 底色在内层 `\x1b[0m` 之后断掉 | `history_cell/messages.rs` 把 prompt 样式折叠进每个 span、并强制每行 bg | `reapplyPromptSurface` 把每个内层 `\x1b[0m` 换成 `\x1b[0m` + 基样式 | 新用例自带 SGR 状态机，断言每个非空可见文本段的 bg 等于同行前缀的 `48;5;235`；关掉重注入即红 |
| markdown 行不按视口换行 | `markdown.rs` 用 `word_wrap_line_with_source` | `wrapPromptRow` → `tui.AdaptiveWrapLine(BreakWords:true)` | `width=40` 下长 URL 原本画到 100 列（我实测），现每行 ≤ width（40/80 两档断言） |
| 双 gutter `› › ` | 无（Go 侧缺陷） | markdown 分支直接 `append(lines, wrappedMessage...)`，不再二次加前缀与样式 | 首行只出现一个 `› `，续行两空格 |

契约 verifier 由 3 条扩到 5 条（加 `TestTypedPromptMarkdownKeepsPromptSurfaceLikeRust`、`TestTypedPromptMarkdownWrapsToViewportLikeRust`），原有 4 条 prompt 用例一条未改仍绿。

**残留（写进契约口径）**：换行后链接 source 的重映射未对齐——Rust `tui/src/markdown.rs:65 remap_source_wrapped_line` 把每个 hyperlink 的 destination 映射到换行后的物理行，Go 的字符串渲染路径没有 span→source 映射，长链接被换行后无法标注「哪一行承载链接的哪一段」。为它重建渲染器不值。

### 11.2 条带 tab 单元宽度对齐（视觉密度，不单立契约）

`tui/tea/sidebar_chooser.go` 的 tab 单元从「当前最大标签宽度」（≥2 tab 时 8 列）改为 Rust 固定的 `" Side chat "` = 11 列，预算照搬 `TabStrip::targets`（`inner = width-1`、`addWidth = min(inner,3)`、`available = inner - addWidth`，右边缘留 1 列）。渲染 / hover / 点击仍共用同一套列映射。新增 `TestSidebarTabStripUsesFixedRustTabWidthLikeRust`（数量公式 `count == available / 11`、超宽标签在单元边界裁剪不串格、`+` 仍可点），并登记进 `sidebar-chooser-list-bindings-and-hover` 的 verifier。反证：把 tab 宽退回「最大标签宽度」→ `width 23 tab cell = 12, want the fixed 11 columns`。

既有 10 条 sidebar/inset 用例**一条断言都没改**，全绿。


## 12. Windows lane 验证回执与三处修正（2026-11-08）

win-align 在 Windows/amd64 上用源码包 `cgmain-1794138616.zip`（manifest `a6598589…`）跑完五条门禁，全 exit 0；`tui/tea` 整包在 Windows 上是**全绿**的，`tui/history_cell` 81 个用例全 PASS，`-run 'Image|UserMessage'` 11/11 PASS。它同时指出三件我们这边看不到的事，逐条处置如下。

### 12.1 真差异：session header 对目录做了 `filepath.Abs`（已修）

Rust `history_cell/session.rs:347-372` 的 `format_directory` **原样显示**目录（只在 `$HOME` 之下时收缩成 `~/sub`），从不绝对化；Go 的 `formatDirectory` 先 `filepath.Abs` 再 `Clean`。后果：POSIX 主机上 `D:\repo\project` 是相对路径，`Abs` 把它拼到进程 cwd 后面，header 显示成真实工作目录。Windows 上 `D:\repo\project` 本身是绝对路径，所以同一段代码在那里「看起来是对的」——这正是它一直没被发现的原因。

修法（`tui/history_cell/session.go`）：删掉 `filepath.Abs`，补 `relativizeDirectoryToHome` 对齐 Rust 的 `relativize_to_home`。修后 `TestClearHeaderHonorsRawOutputModeLikeRust`、`TestModelViewCanShowRustStyleSessionHeader`、`TestClearReinsertsBorderlessSessionHeaderLikeRust` 三条从红转绿。

### 12.2 两处测试自身的平台假设（已改成可移植写法）

| 用例 | 问题 | 处置 |
| --- | --- | --- |
| `TestModelDefaultTerminalTitleMatchesRust` | fixture 写 `D:\repo`、断言项目名 `repo`；Rust 用 `Path::file_name`（`status_surfaces.rs:562`），POSIX 上 `D:\repo` 是一个完整路径段，标题就该是 `D:\repo` | 非 Windows 用 `/work/repo` 作 fixture，断言仍是「取 cwd 的末段」 |
| `TestModelAppCommandUsesRustHistoryMessages` | `/app` 在 Rust 里是 `cfg!(any(target_os="macos", target_os="windows"))`（`slash_command.rs:318`），Go 的 `builtinFrameAvailable` 同款门控；Linux 上 popup 里没有 `app` 行，键入 `/app` 过滤后首个前缀匹配是 `approve`，回车执行的是 `/auto-review` | 用例加同款 GOOS 门控并写明理由；行为与 Rust 一致，不改代码 |

第二条值得留意：Linux 上 `/app`+回车会执行 `/approve`，Rust 同样如此（两边都只有 exact+prefix、且 `approve` 在 `app` 之前）。这是既有语义，不是新引入的回归。

### 12.3 `tui/tea` 最后一条红已修：`TestRealTerminalSlashForkPaintsProgressLikeRust`

原用例在 PTY 流里等整句 `Forked session`，但 bubbletea 是按**单元格 diff** 重绘的：footer 先画 `Forking conversation…`，完成时只重写变化的格子，流里自然只出现 `…ed session <id>`（真终端上看到的就是完整的 `Forked session <id>`）。所以这条断言在原写法下永远不可能成立。改成锚定「notice 文本 + fork 出的新会话 id」（`session 01910000-0000-7000-8000-000000000002`），并保留原有关键断言：进度行先出现、结果至少 700ms 后才到、`thread/fork` 只发一次。修完 `go test ./tui/tea -count=1` **整包全绿**（65.8s），`go test ./tui/... -count=1` 全绿（29 个有测试的包）。

补充平台事实（win-align 报的）：该文件是 `//go:build !windows`，Windows 上根本不编译，`-run` 只会返回 `no tests to run`；Windows 侧的 `/fork` 覆盖走 `slash_parity_pty_windows_test.go` 的 ConPTY 用例，那组是全绿的。

### 12.4 环境层：`go.sum` 里的 glamour 校验和不一致是 `GOPROXY=direct` 造成的

win 在冷缓存 + `GOPROXY=direct` 下报 `charm.land/glamour/v2@v2.0.1: checksum mismatch (go.sum: h1:xl+r00A4…)`。核查结论：**go.sum 是对的**。`https://goproxy.cn/sumdb/sum.golang.org/lookup/charm.land/glamour/v2@v2.0.1` 返回的正是 `h1:xl+r00A4aJWU0z8fgwKd9fQQ4rsphqGUzuEiXZP5n+c=`，用干净 `GOMODCACHE` 经 goproxy.cn 拉取也得到同一 `ziphash`。差别只在他走 VCS 直连（`GOPROXY=direct`）。结论：冷缓存 CI 只要走代理即可，**不要**改 go.sum；需要离线时给模块缓存，而不是替换校验和。

另：包内文本是 CRLF 不是打包失误——本仓 `.go`/`go.mod`/`go.sum` 本来就是 CRLF，只有 `.github/**` 下的 `.sh`/`.yml` 是 LF；交付包里的 LF 是 agent 侧统一归一化后的形状。
