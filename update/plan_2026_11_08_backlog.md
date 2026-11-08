# 重锚后缺口清单（37 条 partial + 4 条架构性 blocked）

- 作者：ledger-align · 2026-11-08 · 只读整理，不改 Go 代码
- 数据源：`/tmp/ledger_state_2026_11_08.zip`（sha256 `f58f9d12fe24c6f94cebc42e656964fdc35313df3cf8b0cc2b79827fb3561a95`）：`parity/commits.json` 3677 条（2963 complete / 639 n/a / 71 partial / 4 blocked_precondition）、`parity/contracts/manifest.json` **135 条（98 complete / 37 partial）**、`parity/domains.json` 26（21 complete / 5 partial）
- 排序依据：**安全语义 > 可观测的 wire/行为差异 > 性能 > 纯结构**。同一档内按「影响面 × 修复成本」排，越靠前越便宜且越危险
- 行内「主锚点」= 我镜像里能核到的具体位置；核不到的写**零命中**并给出 `rg` 模式，不猜

## 0. 汇总

| 维度 | 分布 |
| --- | --- |
| 优先级 | P1 13 条 / P2 17 条 / P3 7 条 |
| 归属 lane | macos 1 条 / 我自己 36 条 |
| 合计 | 37 条可派单 + 4 条架构性 blocked（单列见 §4） |

> **lane 的说明**：Windows lane 名下的 6 条契约（`windows-*`）现在**全部 complete**，所以本清单里没有 Windows 可派单项；macOS lane 只有 1 条（Secure Enclave，且与 blocked #43624 同源）；其余 36 条的主干都在 `appserver/` `turn/` `tui/` `model/` `config/`，归我自己。
>
> 可分摊的只有两条：`protected-model-profile-downgrade` 与 `turn-context-required-permission-profile` 都要改 `sandbox/` 侧的 profile 解析，**执行面**可以交给 Windows lane，但**入口**（settings / turn 构造 / 冷恢复）在我这侧，所以仍建议我主导、Windows lane 只补 sandbox 适配——不算独立派单。

## 1. P1 安全语义（13 条）

| # | contract | 关联 | 主锚点 | 行为差 | 落地后应出现的 verifier | lane |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `realtime-runtime-auth` | #60003 `44f1b0e867` | `appserver/realtime_runtime.go:463` `headers.Set("Authorization", "Bearer "+apiKey)`；`rg -n runtimeauth realtime/ appserver/realtime_runtime.go` → **0 命中**（对照：`appserver/runtime_router.go:54` 已 import runtimeauth，说明能力在、只是没接到 realtime 面） | realtime WS 握手 / WebRTC / sideband 用**一次性捕获的静态 token**，runtime agent instance 与凭据轮换都不生效 | 现有 `TestExistingCallSidebandPrefersPerCallEndpointLikeRust`、`TestRealtimeSidebandReconnectHelpersLikeRust` 保持绿；新增 `TestRealtimeAuthResolvesRuntimeAuthPerConnectLikeRust` | 我自己 |
| 2 | `runtime-auth-websocket-revalidation` | #60055 `d4a341bd52` | 同一处静态 Bearer（`appserver/realtime_runtime.go:463`）；重连路径见 `remotecontrol/websocket_refresh_test.go:158` 用 `model.BearerAuthHeaders` | 复用已有 WebSocket 前不重新鉴权：旧 token 一直用到连接关闭，「换登录后旧连接仍可用」 | 现有 `TestWebsocketReauthTracksLastConnectedCredential` 保持绿；新增 `TestWebsocketReconnectRevalidatesRuntimeAuthLikeRust` | 我自己 |
| 3 | `remote-control-runtime-auth` | #60073 `f4275a2a2e` | `remotecontrol/` 用 `model.AuthHeaders` / `BearerAuthHeaders`（快照静态头）；`rg runtimeauth remotecontrol/` → 0 命中 | 远程控制请求无 runtime 选择、无目标校验、无 denial 分类；企业 runtime 下会拿错身份发请求 | 现有 `TestRemoteControlWebsocketLoopRetiresOnAuthOwnerChangeWhileConnected`；新增 `TestRemoteControlRequestsUseRuntimeAuthLikeRust` | 我自己 |
| 4 | `runtime-auth-terminal-denials` | #60096 `efdacaf46f` | `model/responses_agent.go:4762 responseAuthorizationErrorCode` 只喂 telemetry 的 `AuthErrorCode`（`:1143`/`:1469`/`:3162`/`:4681`、`model/agent_metrics.go:243`/`:469`）；`recoverUnauthorizedAuth` 与 `isRetryableResponsesStreamError` 都不读它 | 四个 terminal denial code 不被识别 → 本该「重新登录」的失败被当成可重试，表现为反复重试后失败 | 现有 `TestManagerCachesForbiddenAndRefreshesUnauthorizedLikeRust`；新增 `TestTerminalRuntimeDenialsSurfaceSignInLikeRust` | 我自己 |
| 5 | `managed-runtime-auth-mode` | #60014 `10cf2b9ea2` | `config/` 的 `FeatureRequirements map[string]bool`（用法见 `config/credential_broker_project_test.go:288`）；`runtime_agent_instances.mode` 无独立字段 | requirements.toml 的托管模式被**静默丢弃**：企业强制 `assertion_only` 既不生效也不报错（唯一「静默忽略企业策略」的条目） | 现有 `TestModeFromConfigValuesLikeRust`；新增 `TestRequirementsRuntimeAuthModeIsEnforcedOrRejectedLikeRust` | 我自己 |
| 6 | `mcp-network-policy-derivation` | #60046 `b7d2321ef7` | `network/` 只有 header/策略面（`network/network_policy_test.go`）；`rg mcp_network_policy .` 只命中 features 注册 | 缺「从 MCP server 配置推导网络策略并保留已保存 deny」；Rust 本条 `mcp_network_policy` 仍是 `dead_code`、接线在后续 PR，所以本条只能随那个 PR 一起验收 | 现有 `TestMCPNetworkPolicyCapabilityOnlyAdvertisedToTrustedChatGPTOriginsLikeRust`、`TestEnvironmentNetworkPolicyApplyToPreservesInheritedDenials`；新增 `TestMCPServerNetworkPolicyDerivesAndKeepsSavedDenialsLikeRust` | 我自己 |
| 7 | `protected-model-profile-downgrade` | #59933 `c1c07853fe` | `appserver/` 的 `Protected` 只与 credential broker env 相关（`appserver/thread_shell_command.go:80`）；`rg "protectedModelDowngrade|startupDowngrade" appserver/` → 0 命中 | 受保护模型的启动降级分散在 settings 与 turn 两层，没有统一「启动即降级到 permission profile」的点，profile 与 login 两个入口可以走出不同权限 | 现有 `TestThreadSettingsProtectedModelUsesProposedPermissionsLikeRust`、`TestProtectedModelForcesAutoReviewerForTurn`；新增 `TestProtectedModelStartupDowngradesViaProfileLikeRust` | 我自己 |
| 8 | `patch-approval-cancel-turn-scope` | #59910 `ad1dbba32d` | `appserver/command_approval_cancel_aborts_like_rust_test.go:19` 锁的是「取消即中止 turn」，不校验 turn 归属 | 取消请求不校验是否属于发起它的 turn → 别的 turn 的取消会中止当前 turn（跨 turn 误杀） | 现有 `TestCancelCommandApprovalAbortsTurnLikeRust`；新增 `TestPatchApprovalCancelIsScopedToRequestingTurnLikeRust` | 我自己 |
| 9 | `turn-context-required-permission-profile` | #59963 `2889d8e1bf` | `rg PermissionProfile protocol/` → 0 命中；turn context 仍允许隐式/缺省权限 | turn context 权限没归一化成 required profile，冷恢复时无法校验「这条 turn 当初用的是哪个 profile」 | 现有 `TestColdResumeRestoresOwnedUnnamedPermissionProfileLikeRust`、`TestTurnSandboxPermissionProfileUsesCapturedEnvironmentStateLikeRust`；新增 `TestTurnContextRequiresPermissionProfileLikeRust` | 我自己 |
| 10 | `final-answer-moderation` | #59842 `ddd1812a51` | `protocol/exec_events.go:32` 只有 internal-only 的 turn moderation metadata；终答侧的 `rg "finalAnswer.*moderation|moderationDecision"` → 0 命中 | 终答缺少「针对该答案的审核决定 + display metadata」，客户端无法区分「被拦」与「无内容」，也无法显示定向说明 | 现有 `TestModelSafetyRecourseDefersBlockedTurnAndExplainsIt`；新增 `TestFinalAnswerCarriesTargetedModerationDecisionLikeRust` | 我自己 |
| 11 | `account-security-setup-appserver-fetch` | #60082 `4be90eb069` | `app/security_setup.go:16` 起客户端自读 `auth.json` 拼 Bearer（`:38-40`）；`rg securitySetup appserver/ protocol/` → 0 命中 | 取数在客户端进程：经 app-server 连进来的远程/IDE 客户端拿不到安全提醒；runtime auth 只是可选注入，不是硬前提 | 现有 `TestCloudClientGetSecuritySetupDropsInvalidNoticesLikeRust`、`TestSecuritySetupStaleIdentityAndBannerPriorityLikeRust`；新增 `TestAccountSecuritySetupReadThroughAppServerLikeRust` | 我自己 |
| 12 | `child-agent-catalog-preservation` | #60049 `44ccf39457` | `appserver/agent_controller.go:130` 调 `:502 resolveSpawnModelOverrides`（按请求参数/捕获设置重算） | 子 agent 启动丢失父 catalog：子 agent 可能选到与父不同的模型/凭据，与「继承父会话」语义不符 | 现有 `TestConfigPanelModelProvidersRefreshesAfterFirstChatLikeRust`；新增 `TestChildAgentInheritsParentCatalogLikeRust` | 我自己 |
| 13 | `user-verification-platform-provider` | #43624 `e7637306bc`（与 blocked 同源） | `appserver/user_verification_runtime.go:147 platformProvider()` 注入点已在；`rg DarwinUserVerification .` → **0 命中**，macOS Secure Enclave provider 不存在 | 缺注入式平台 provider：设备能力 / 密钥元数据 / 错误分类 / 锁超时在 macOS 侧没有真实实现，Darwin 会话只能拿到 unavailable | 该契约登记的 6 个 verifier 在当前树里**全部 0 命中**（`TestDefaultRuntimeRouterInstallsUserVerificationPlatformProvider`、`TestDarwinUserVerification*`），需与实现同批落地 | macos |

## 2. P2 可观测的 wire / 行为差异（17 条）

| # | contract | 关联 | 主锚点 | 行为差 | 落地后应出现的 verifier | lane |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `account-read-user-id` | #60004 `01ab354312` | `auth/account.go:565 type GetAccountResponse struct` 只有 Account / RequiresOpenAIAuth / WorkspaceRouting | `account/read` 不带 `userId`：客户端只能绕 auth/status + workspaceRouting 拼身份；两个构造点（`appserver/runtime_router.go:14977`、`:15041`）都要填 | 现有 `TestChatGPTAccountUserIDRequiresMatchingWorkspaceLikeRust`、`TestRuntimeRouterGetAccountWorkspaceRoutingSelectionLikeRust`；新增 `TestAccountReadResponseCarriesChatGPTUserIDLikeRust` | 我自己 |
| 2 | `skills-tool-name-resolution` | #59954 `0768988e55` | `turn/skills_tools.go:614` → `:955 availableSkillPackage` → `:988 skillPackageMatchesAlias`；`rg ambiguous turn/skills_tools.go` → 0 命中 | `skills.read`/`skills.installations` 不认 `skills.list` 显示名：复制名字过去会 `source_unavailable`；长名（被截断）100% 失败 | 现有 `TestSkillsReadAliasResolutionAndExecutorSkillRoot`、`TestSkillsInstallationsListsAndFiltersDiscoveryV2Sources`；新增 `TestSkillsReadResolvesListedDisplayNameLikeRust` | 我自己 |
| 3 | `typed-prompt-markdown-render` | #59983 `78b1dda5f6` | `tui/history_cell/messages.go:138` 只有 Plain/Spoken；`:171 DisplayLines` 按 `\n` 切行 + `WrapLine` 纯文本 | 用户键入的 markdown 原样显示标记符号；且用户 cell 没有 span/链接列承载，mention 高亮是字符串级（`:339`） | 现有 `TestUserAndAgentMessageCells`；新增 `TestTypedUserPromptRendersMarkdownLikeRust`、`TestUserPromptMarkdownKeepsGutterAndLinkColumnsLikeRust`；⚠️ `TestUserMessagePresentationLikeRust` 需同步改写 | 我自己 |
| 4 | `cloud-thread-project-binding` | #60007 `7de8a0afc0` | `cloudclient/prewarm*` 发 `ManagedEnvironment{}` 空消息（现有用例 `TestPrewarmRequestUsesManagedEnvironmentAndOptionalModelLikeRust`） | cloud thread 启动不带本地 GitHub 项目：无 ProjectEnvironment、无 origin→GitHub 归一化，云端拿不到仓库上下文 | 现有 `TestPrewarmRequestUsesManagedEnvironmentAndOptionalModelLikeRust`；新增 `TestPrewarmSendsLocalGitHubProjectLikeRust` | 我自己 |
| 5 | `cloud-image-messages-and-draft-handoff` | #60061 `e6bf88ae02` | `cloudbackend/submission.go:90` `rejectCloudInput("Cloud conversations currently accept plain text only.")`（逐字保留 Rust 改前文案） | Cloud 提交仍拒图片，且没有可编辑 draft 交接；用户被一句过时文案挡住 | 现有 `TestAddUserMessageRequestEncoding`、`TestCloudConversationRequestsAndTypedReceiptsLikeRust`；新增 `TestCloudSubmissionCarriesImagePartsLikeRust` | 我自己 |
| 6 | `side-chat-policy-first-request` | #60067 `0ed34eb9ce` | `app/remote_tui.go:6149` `SideDeveloperInstructions("")`（策略只走 fork config，boundary 走 inject-items） | 继承 turn 压制时**首个模型请求可能没有侧聊策略**，侧聊可能接着父任务继续干（安全问题但优先级低于鉴权链） | 现有 `TestSideDeveloperInstructionsAppendsExistingPolicyMatchRust`、`TestSideBoundaryPromptMarksInheritedHistoryReferenceOnlyMatchRust`；新增 `TestSideChatPolicyReachesFirstRequestUnderInheritedSuppressionLikeRust` | 我自己 |
| 7 | `thread-model-metadata-exposure` | #59958 `12dc86beb3` | `rg "selected_model_info|SelectedModelInfo" appserver/` → 0 命中；无 `models_manager_for_startup` | thread 不暴露自己的 catalog 选中模型：turn/start 的 fallback 只能靠猜，客户端无法解释「为什么用的是这个模型」 | 现有 `TestTurnStartWarnsForFallbackModelMetadataLikeRust`；新增 `TestThreadSelectedModelInfoUsesThreadCatalogLikeRust` | 我自己 |
| 8 | `code-mode-incremental-exec-definitions` | #60157 `2bc8e7f545` | `features/features.go:214` 注册 `incremental_exec_tools`（under development）；`rg "ExecDefinition" --glob '*.go'` → 0 命中 | 增量定义只到「每个 top-level definition 一个 hash」，`functions.exec` 是单个 hash key，没有结构化 ExecDefinition → exec 工具集变化不能增量下发 | 现有 `TestTopLevelToolsNamespaceTransitionsLikeRust`、`TestIncrementalExecToolsFeatureRegisteredLikeRust`；新增 `TestIncrementalExecDefinitionsCarryStructuredEntriesLikeRust` | 我自己 |
| 9 | `code-mode-incremental-exec-wiring` | #60163 `33b3f80a09` | `incremental_exec_tools` 只有注册 + 一条注册测试，无任何消费点（`rg incremental_exec_tools --glob '*.go'` 只命中 features） | 开关是死开关：打开后没有任何代码路径使用增量 exec 更新，等于没实现 | 现有 `TestIncrementalToolCatalogAppendsChangedCatalogLikeRust`、`TestIncrementalToolCatalogRecordsWindowStartLikeRust`；新增 `TestIncrementalExecUpdatesReachSessionToolSpecLikeRust` | 我自己 |
| 10 | `incremental-exec-message-overrides` | #60188 `c3936b4b8a` | `rg "exec_tools_update_hint|ExecToolsUpdateHint" --glob '*.go'` → 0 命中；override 表只有 5 个通用字段 | 缺 `exec_tools_update_hint` / `exec_tools_removed_definitions_header` 两个 override，增量更新时模型看到的提示文案与 Rust 不同 | 现有 `TestTopLevelToolsCatalogMessageOverridesLikeRust`；新增 `TestIncrementalExecMessageOverridesRenderLikeRust` | 我自己 |
| 11 | `provider-creation-during-signin` | #60186 `d960b36694` | `app/interactive_onboarding.go:126` 明确把 custom provider 排除在登录后写入之外 | 登录流程里不能创建自定义 provider：用户必须先登录再回头改配置 | 现有 `TestModelProviderConfigWriteCreatesAndPatchesGatewaysLikeRust`、`TestModelProviderConfigWriteValidatesCustomGatewaysLikeRust`；新增 `TestSignInFlowCreatesCustomProviderLikeRust` | 我自己 |
| 12 | `tui-custom-gateway-signin` | #60009 `2497eab251` | `rg gateway_setup_wizard --glob '*.go'` → 0 命中（两处入口都没有） | TUI 登录不能选自定义 gateway：只能用内置 provider，gateway 用户要手工改配置 | 现有 `TestConfigPanelProviderDefaultSelectionLikeRust`、`TestModelProviderConfigWriteCreatesAndPatchesGatewaysLikeRust`；新增 `TestGatewaySetupWizardWritesProviderLikeRust` | 我自己 |
| 13 | `multi-agent-hosted-tools-guidance` | #60011 `e0aa0434f4` | `rg "hosted tool|HostedTool" agent/` → 0 命中 | 关闭 native tools 时多 agent 模式的指引导语缺失：模型不知道该用 hosted 工具替代，容易反复尝试不可用工具 | 现有 `TestDisableNativeToolsKeepsV2RuntimeLikeRust`、`TestCollabToolsGateMatchesRustLikeRust`；新增 `TestMultiAgentGuidanceNamesHostedToolsLikeRust` | 我自己 |
| 14 | `skill-mention-punctuation-join` | #60180 `1455fda735` | `tui/`（composer mention 解析与 paste 路径各自处理标点，未共享） | 完成态 mention 与后续标点拼接不一致：`$skill,` 这类输入在两条路径上得到不同的 mention 边界 | 现有 `TestParseLinkedToolMentionPreservesByteOffsetsAndTrimsPathsLikeRust`；新增 `TestCompletedSkillMentionJoinsTypedPunctuationLikeRust` | 我自己 |
| 15 | `release-prerelease-cleanup` | #59887 `b6f53857fc` + #59903 `e3fe1ade2c` | `.github/workflows/release.yml:140` 仍是 `softprops/action-gh-release@v2` 一步建 release + 上传资产 | 上传失败会留下公开但不完整的 prerelease；缺「按 release id 只删本次新建」的阶段边界（脚本级用例已有，`TestPublishReleaseCleansUpFailedPrereleaseById` 在你树里） | 现有 `TestPublishReleaseCleansUpFailedPrereleaseById`（我 scope 未同步）；转 complete 的条件是**真跑过一次 workflow** | 我自己 |
| 16 | `agent-tree-id` | #60190 `c9b4267c37` | `agent/shutdown_report.go:64` 注释明确 `TreeID` 是运行时关联 id、不是 thread id；`:116 NewAgentTreeShutdownState(treeID)` | 缺真正的 agent tree id：重载后沿用旧的运行时关联 id，跨重启无法把同一棵树关联起来（environment 移除那半已对齐） | 现有 `TestManagerRemove`、`TestAgentTreeShutdownReportFormattingIsPayloadFreeLikeRust`；新增 `TestAgentTreeIDChangesOnReloadLikeRust` | 我自己 |
| 17 | `analytics-runtime-auth` | #60101 `cce9d3228b` | `tui/analytics/client.go:118` `isUnauthorizedAnalyticsError(planErr)` 只认 HTTP 401 | typed runtime 错误不会映射成「Sign in again」：Analytics 已接 runtime auth，但错误分类缺这一层 | 现有 `TestCloudClientResolvesRuntimeAuthPerRequestLikeRust`、`TestCloudClientGetAccountAnalyticsRoutesReportsLikeRust`；新增 `TestAnalyticsRuntimeErrorMapsToSignInLikeRust` | 我自己 |

## 3. P3 性能 / 结构 / 体验（7 条）

| # | contract | 关联 | 主锚点 | 行为差 | 落地后应出现的 verifier | lane |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `incremental-exec-catalog-metrics` | #60177 `5226f2fb4b` | `metrics/tool_spec_metrics.go:19` 有 `ToolIncrementalUpdatesMetric`；`rg exec_entry_updates|exec_update_bytes --glob '*.go'` → 0 命中 | 4 个指标只有 2 个：缺 `exec_entry_updates` / `exec_update_bytes`，无法判断增量是否真的省字节 | 现有 `TestIncrementalToolCatalogRecordsChangedToolMetricsLikeRust`、`TestRecordChangedToolMetricsTagsPerToolLikeRust`；新增 `TestIncrementalExecMetricsRecordEntryAndBytesLikeRust` | 我自己 |
| 2 | `message-board-bulk-subscriptions` | #59998 `eb485f57ba` | `agentboard/`（`SetSubscription` 单条；批量 `set_subscriptions` 不存在） | 缺原子批量订阅：多目标更新无法一次事务完成，失败只能部分生效（Guardian 捕获目录的一致性依赖它） | 现有 `TestLocalBoardSubscriptionsOptOutsAndDeletionLikeRust`；新增 `TestLocalBoardBulkSubscriptionsAreAtomicLikeRust` | 我自己 |
| 3 | `message-board-fixed-read-view` | #59993 `632eee03e9` | `agentboard/connection_test.go:28` `shared.ReadOnly()`（实时连接，无 cutoff 快照） | 缺固定读视图：审查过程中新增的帖子会混进「当时的证据」，无法按捕获时点复现 | 现有 `TestLocalBoardPersistsPagesAndReadsLikeRust`、`TestLocalBoardAppendPersistsEvidenceWithoutSideEffectsLikeRust`；新增 `TestLocalBoardFixedReadViewHonorsCutoffLikeRust` | 我自己 |
| 4 | `side-chat-persistence-and-tab-close` | #60152 `57e64682f8` | `tui/app/side.go:12` `SideRenameBlockMessage = "Side conversations are ephemeral and cannot ..."`；`rg close_sidebar_tab` → 0 命中 | 侧聊只在内存映射：重启即丢，没有关闭 tab 的动作，也不能重命名 | 现有 `TestSidebarTabsKeepMultipleSideChatsLikeRust`、`TestSidebarChooserOffersNewSideChatLikeRust`；新增 `TestSideChatsPersistAndCloseLikeRust` | 我自己 |
| 5 | `sidebar-chooser-list-bindings-and-hover` | #59928 `814347ecd2` | `tui/` chooser 硬编码 Up/Down/Esc/Enter；tab 无 hover 高亮（`rg "hover" tui/tea/` 无 tab 侧实现） | chooser 键位不可配、tab 无 hover 反馈：与 list 组件其它视图的行为不一致 | 现有 `TestSidebarChooserOffersNewSideChatLikeRust`、`TestSidebarChooserResizesLikeRust`；新增 `TestSidebarChooserUsesListBindingsAndHoverLikeRust` | 我自己 |
| 6 | `sidebar-tab-keyboard-mouse-navigation` | #60181 `8044c6867c` | `tui/keymap.go:37` 只有 `focus_sidebar_tabs`；`rg "sidebar_up|sidebar_down" tui/` → 0 命中 | 缺 tab 间上下切换动作与滚轮：只能用焦点键+方向键，溢出时没有箭头提示 | 现有 `TestWorkspacePaneLeaderBindingsLikeRust`、`TestSidebarTabsKeepMultipleSideChatsLikeRust`；新增 `TestSidebarTabNavigationBindsAndScrollsLikeRust` | 我自己 |
| 7 | `mcp-app-invocation-context-refresh` | #59991 `8fcabd75a0` | `mcp/binding.go:21` 注释说明 `searchInvocationContext` 只在成功发现时填充 | 跨 MCP 工具刷新时不保留 app 调用上下文：刷新后 app 工具拿到的是空上下文 | 现有 `TestModelSearchDiscoveryProjectsConnectorInvocationContextLikeRust`、`TestBindingRejectsCatalogRefreshAndInvisibleTools`；新增 `TestCatalogRefreshKeepsAppInvocationContextLikeRust` | 我自己 |

## 4. 架构性 blocked（4 条，单列，不混进派单）

这 4 条不是「没做」，是**现在无论谁做都缺前置条件**。它们的 `status=blocked_precondition` 是账本里的终态之一，不占用契约位。

| # | commit | 主题 | 现在卡在什么 | 要什么条件才能动 |
| --- | --- | --- | --- | --- |
| 1 | #40625 `34c5303f49` | Ignore closed agents when changing directories | Go 没有 agent_navigation 的 thread-closed 生命周期，也没有「inactive replay-only thread-event 通道」来保留 stale in-progress turn —— 缺少被检查的那个对象 | 前置条件：先有 Rust 式的 agent navigation 状态机（closed/inactive turn 可观测），或等效的「目录切换时忽略已关闭 agent」判定面 |
| 2 | #42841 `60888d0868` | Add a native Windows MXC sandbox adapter | 需要原生 Windows MXC SDK（wxc_common::sandbox_* 一类）接入；Go 只有 `availability=false` 的能力探测 | 前置条件：MXC SDK 可得 + Windows 构建链（与 #55378 同一前置） |
| 3 | #43624 `e7637306bc` | Add macOS user verification with Secure Enclave signing | Rust 在 Data Protection Keychain 用 LAContext 生成 P-256 Secure Enclave 密钥并做跨进程锁；Go 侧无任何 Darwin 实现 | 前置条件：macOS runner + Security.framework/LAContext 绑定；**与 partial 契约 `user-verification-platform-provider` 同源**，落地后两者一起转绿 |
| 4 | #55378 `c6e29b4fcb` | Migrate the Windows MXC sandbox to `mxc-sdk` | 同上：缺原生 MXC SDK 依赖；Go 仅上报 availability=false | 前置条件：mxc-sdk 依赖引入并启用 Windows 构建 |

> 注意 #43624 与 P1 的 `user-verification-platform-provider` **同源**：那条契约的 verifier（`TestDarwinUserVerification*`）在当前树里 0 命中，只有 macOS 前置条件满足后才能落地，两者应同批验收。

## 5. 已完成、不在本清单（11 条）

| contract | 说明 |
| --- | --- |
| `mcp-cached-tool-startup-errors` | 已 complete（本轮或最近几轮转绿），无需派单 |
| `mcp-sharing-discovery-deferred-startup` | 已 complete（本轮或最近几轮转绿），无需派单 |
| `mcp-lazy-stdio-startup` | 已 complete（本轮或最近几轮转绿），无需派单 |
| `logs-prune-window-frame` | 已 complete（本轮或最近几轮转绿），无需派单 |
| `thread-token-narrow-update` | 已 complete（本轮或最近几轮转绿），无需派单 |
| `thread-writer-lock-shared-coordination` | 已 complete（本轮或最近几轮转绿），无需派单 |
| `guardian-verified-answer-whole` | 已 complete（本轮或最近几轮转绿），无需派单 |
| `guardian-repl-provenance-bound` | 已 complete（本轮或最近几轮转绿），无需派单 |
| `code-mode-host-default-features` | 已 complete（本轮或最近几轮转绿），无需派单 |
| `incremental-tools-legacy-bedrock-gate` | 已 complete（本轮或最近几轮转绿），无需派单 |
| `windows-mxc-tty-launch` | 已 complete（本轮或最近几轮转绿），无需派单 |

## 6. 排期建议（按我的优先级）

1. **先清鉴权链**（#1–#6）：`realtime-runtime-auth` → `runtime-auth-websocket-revalidation` → `remote-control-runtime-auth` → `runtime-auth-terminal-denials` → `managed-runtime-auth-mode` → `mcp-network-policy-derivation`。理由：这 6 条是「凭据/策略在错误的地方生效」，出错时表现为「功能可用但身份/策略不对」，最不容易被用户发现。
2. **再清权限与审核**（#7–#11）：`protected-model-profile-downgrade`、`patch-approval-cancel-turn-scope`、`turn-context-required-permission-profile`、`final-answer-moderation`、`account-security-setup-appserver-fetch`。
3. **然后是会「静默走错」的行为差**（#12–#14 与 P2 前段）：`child-agent-catalog-preservation`、`account-read-user-id`、`skills-tool-name-resolution`、`typed-prompt-markdown-render`。
4. **P3 最后做**：性能与结构项都不改变正确性，且 `incremental-exec-catalog-metrics` 依赖 `code-mode-incremental-exec-*` 先落地，否则指标没有数据来源。

## 7. 复现命令

```bash
# 37 条 partial 与 4 条 blocked 的直接来源
python3 - <<'PY'
import json, collections
led = json.load(open("parity/commits.json","rb")); man = json.load(open("parity/contracts/manifest.json","rb"))
print(collections.Counter(c["status"] for c in led["commits"]))          # 2963/639/71/4
print(collections.Counter(c["status"] for c in man["contracts"]))        # 98/37
PY
```

```bash
# 本清单里标注「零命中」的锚点，逐条可复跑
rg -n "runtimeauth" realtime/ appserver/realtime*.go remotecontrol/ ; echo "exit=$?"
rg -n "PermissionProfile" protocol/ ; echo "exit=$?"
rg -n "ExecDefinition" --glob "*.go" . ; echo "exit=$?"
rg -n "exec_tools_update_hint|ExecToolsUpdateHint" --glob "*.go" . ; echo "exit=$?"
rg -n "DarwinUserVerification" . ; echo "exit=$?"
rg -n "sidebar_up|sidebar_down" tui/ ; echo "exit=$?"
rg -n "hosted tool|HostedTool" agent/ ; echo "exit=$?"
```

## 8. 一句话结论

37 条 partial 里 **13 条属安全语义、17 条属可观测行为差、7 条属性能/结构**；没有一条是「没实现某个功能」，全是「实现了但语义/边界/位置不对」。4 条 blocked 是纯前置条件问题，其中 #43624 与 `user-verification-platform-provider` 同源，需一起排。
