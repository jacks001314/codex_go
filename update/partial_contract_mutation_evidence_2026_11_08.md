# 20 条 partial 的变异式重判（2026-11-08）

对象：`parity/contracts/manifest.json`（sha256 `918623495668733e…6c41d0`，138 契约 / 118 complete / 20 partial）。
方法：**把主干树整份复制到 `/tmp/m13`，逐条改一处实现 → 跑指定用例 → 记录变红的那一行 → 还原（sha256 复核）**。凡是没真跑出红线的条目，一律不给 `complete`。

## 0. 结论

| 结论 | 条数 |
|---|---|
| `complete`（有真跑红的 `removalEvidence`） | **12** |
| `needs-behaviour` | **7** |
| `undecidable` | **1** |

逐条字段：`{id, verdict, rustAnchor, goImplementation, verifier, removalEvidence{test, assertion, mutation, command}, residual}`，见 `update/partial_contract_mutation_evidence_2026_11_08.json`。

## 1. 你的四条反证：全部成立，我撤回 4 条 `complete`

| 契约 | 我上轮的判法 | 你的反证 | 我现在的判法 |
|---|---|---|---|
| `mcp-network-policy-derivation` | complete | `appserver/network_approval.go:1491 persistNetworkPolicyAmendment` 只做 `execpolicy.AppendNetworkRule`（落盘），无 update_lock、无内存发布、无 proxy denylist | **needs-behaviour**（我核了 Rust `core/src/exec_policy.rs:567-640`：`update_lock` → clone → `add_network_rule` → `Arc` 发布 → `proxy.add_denied_domain` → 落盘，确实是「先内存/代理、后写盘」） |
| `sidebar-chooser-list-bindings-and-hover` | complete | hover 只覆盖条带首行、无鼠标点选、步长写死 5 | **needs-behaviour**（核实：Go `tui/tea/sidebar_chooser.go:162 const sidebarChooserPageSize = 5`，Rust `sidebar_chooser.rs:215 page_size()` 按可视行数；Rust `sidebar_tab_strip.rs:81 hit(area,point)` 有鼠标命中，`tui/tea` 里 `rg MouseLeft\|tea.MouseMsg` → 0） |
| `message-board-fixed-read-view` | complete | Go 只覆盖 `Channels`/`ReadPost`/`Cutoff` | **needs-behaviour**（核实：`agentboard/read_view.go` 只有 `Cutoff`/`Channels`/`ReadPost` 三个读方法，其余是 read-only 拒绝） |
| `typed-prompt-markdown-render` | complete | Go 复用 agent 列 + 字符串上色，Rust 是 span 列模型 | **needs-behaviour**（核实：`tui/history_cell/messages.go:256 markdownPromptLines` 调 `markdown.Render` 后按行贴 gutter/缩进；Rust `markdown_render.rs` 是 span 级列模型） |

你说得对：我上轮的标准只证明"有东西"，不证明"契约描述的完整"。另外我自己也承认过 `typed-prompt-markdown-render` 需要实跑确认——同一个标准上我自己就不放心，那标准本来就不该用来翻牌。

## 2. 12 条带 mutation 证据的 `complete`

每条都给出「改哪一行 → 哪个用例变红 → 变红断言行原文」，命令都可直接复制跑。

| 契约 | 变异点 | 变红的断言（原文） |
|---|---|---|
| `mcp-app-invocation-context-refresh` | `mcp/binding.go:167` `if !b.tools[server][tool]` → `if false && …` | `binding_test.go:13: invisible tool err=<nil>` |
| `cloud-thread-project-binding` | `cloudclient/prewarm.go:50-51` 去掉 environments 信封 | `prewarm_test.go:66: default request omitted environments` |
| `sidebar-tab-keyboard-mouse-navigation` | `tui/tea/leader_chord.go:126-133` 不填 implicit workspace completions | `side_focus_like_rust_test.go:29: leader - = "", want "global.hide_pane"` |
| `side-chat-persistence-and-tab-close` | `tui/tea/sidebar_chooser.go:30` 只返回 `tabs[:1]` | `sidebar_tabs_like_rust_test.go:90: sidebar tabs = 1, want 2` |
| `side-chat-policy-first-request` | `tui/app/side.go:190-194` 丢弃 existingInstructions | `side_test.go:55: SideDeveloperInstructions() missing existing policy: "…"` |
| `code-mode-incremental-exec-definitions` | `session/top_level_tools.go:297` `changed()` 恒 true | `top_level_tools_test.go:178: known -> same = [...]` |
| `code-mode-incremental-exec-wiring` | `appserver/incremental_tools.go:146` mode 判定恒空 | `incremental_tools_test.go:224: changed catalog result = {... Active:false ...}, want an appended update` |
| `incremental-exec-message-overrides` | `session/top_level_tools.go:71-76` `resolve()` 恒 fallback | `top_level_tools_test.go:290: namespace instruction override = [...]` |
| `incremental-exec-catalog-metrics` | `session/top_level_tools.go:429` 函数体首行 return | `incremental_tools_metrics_test.go:179: counts = map[], want map[...]` |
| `turn-context-required-permission-profile` | `appserver/runtime_router.go:20444` workspace roots → `nil` | `turn_permission_environment_test.go:47: workspace roots = ["…/001"], want the captured environment root "…/002"` |
| `provider-creation-during-signin` | `appserver/model_provider_write.go:35` `{Edits: edits}` → `{}` | `model_provider_config_write_like_rust_test.go:114: provider mismatch` |
| `tui-custom-gateway-signin` | `tui/tea/config_provider_browser.go:793` 去掉 `customGateway` case | `provider_default_and_editor_like_rust_test.go:111: editor save produced no command; error="This provider configuration is read-only."` |

复现方式（任一条）：把主干树复制一份，按 `mutation` 改一行，跑 `removalEvidence.command`，然后还原。

## 3. 两处必须提醒你的发现

1. **`tui-custom-gateway-signin` 登记的 verifier 不覆盖它自己**：登记的是 `TestConfigPanelProviderDefaultSelectionLikeRust` + `TestModelProviderConfigWriteCreatesAndPatchesGatewaysLikeRust`，这两个在「删掉 customGateway 分支」时**仍绿**；真正变红的是 `TestConfigPanelProviderEditorWritesSelectedFieldsLikeRust`（还有 `…PreservesDraftOnFailureLikeRust`）。**建议把前者加进 verifier 再转 complete**，否则这条 complete 靠的是一个没人跑的红线。
2. **`SideDeveloperInstructions` 有两份实现**：`tui/app/side.go:189` 与 `tui/tea/side.go:476` 同名同形。我先改 `tui/tea` 那份时用例**没有**变红（说明登记的 verifier 只走 `tui/app`）。请确认哪份是生产路径；若两份都在用，verifier 需要各覆盖一次。

## 4. 7 条 `needs-behaviour` 的落点（一句话）

| 契约 | 缺什么 | 落点 |
|---|---|---|
| `managed-runtime-auth-mode` | Rust 在**配置加载期**把「配置值被 managed requirement 禁止」当错误中止；Go 应用 required 值 + 启动 warning 继续跑 | `config/runtime_auth_mode.go:61-76` + `:78 RuntimeAuthModeConflictWarning`；要对齐得把冲突升级成加载期错误 |
| `mcp-network-policy-derivation` | 先内存发布 + 代理 allow/deny 更新，再落盘；当前只落盘 → 写盘失败时 deny 不生效 | `appserver/network_approval.go:1491 persistNetworkPolicyAmendment` |
| `message-board-fixed-read-view` | read view 的完整读面（线程列表/分页/搜索） | `agentboard/read_view.go`（现只有 Cutoff/Channels/ReadPost） |
| `cloud-image-messages-and-draft-handoff` | image content item 编码 + draft handoff | `cloudclient/turns.go:144 encodeAddUserMessageRequest` |
| `sidebar-chooser-list-bindings-and-hover` | 步长按可视行数、鼠标点选、hover 覆盖多行 | `tui/tea/sidebar_chooser.go:162` / 缺鼠标层 |
| `account-security-setup-appserver-fetch` | app server 侧 fetch 面（`rg -i securitysetup appserver/ protocol/` → 0） | scope 调整提案单独出（按你说的） |
| `typed-prompt-markdown-render` | span 列模型 | `tui/history_cell/messages.go:256` |

## 5. `release-prerelease-cleanup`：`undecidable`

脚本级 4 分支用例可红可绿（`.github/scripts/publish-github-release.sh` + `parity/release_publish_script_test.go:121`），但转 complete 需要真跑一次 release workflow（含一次上传失败注入），Linux 工作区提供不了。

## 6. 与上一轮的差异

上一轮我给 18 条 complete；这一轮**只有 12 条**给了 complete（都有真跑红线），另外 4 条被你证伪、2 条（`cloud-image`、`account-security`）维持 needs-behaviour、1 条 undecidable。`user-verification-platform-provider` 已由你转 complete（macOS 真机 46/46），不在本轮清单。
