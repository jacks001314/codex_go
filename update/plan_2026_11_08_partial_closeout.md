# 7 条 partial 的「最小可判 complete 改动集」（2026-11-08）

对象：manifest `138 / 133 complete / 5 partial`（sha256 `e139176836e9beaf…`）里剩下的 7 条（`release-prerelease-cleanup` 按你的要求保持 undecidable，不在清单内）。
每条只写「改哪个文件的哪个函数 / 需要哪条新用例 / 用例断言什么」，不写泛泛 gap。

> **2026-11-08 状态变更（只改计数与状态，原文一律保留）**
> 写作时口径 `138 / 130 complete / 8 partial`（manifest sha `3647d8df9ed129d2…`）；当前口径 `138 / 133 complete / 5 partial`（manifest sha `e139176836e9beaf365f765ac784402ba8c09db714d31c636acd11153813d75a`，67,643 B）。
> 本文件 7 节里已转 complete 的 3 条：§1 `managed-runtime-auth-mode`、§3 `message-board-fixed-read-view`、§7 `account-security-setup-appserver-fetch` → 改名/改 scope 为 `account-security-setup-fetch`（每条在该节末尾追加了一行依据）。
> 仍为 partial 的 4 条：§2 `mcp-network-policy-derivation`、§4 `sidebar-chooser-list-bindings-and-hover`、§5 `typed-prompt-markdown-render`、§6 `cloud-image-messages-and-draft-handoff`。
> 另 1 条 partial 是 `release-prerelease-cleanup`（不在本清单内，边界判定见 `plan_2026_11_08_release_prerelease_cleanup_boundary.md`）。

---

## 1. `managed-runtime-auth-mode`（规模 S）

**现状**：`config/runtime_auth_mode.go:61 Config.RuntimeAuthMode()` 返回 `(required, overridden)`；`:78 RuntimeAuthModeConflictWarning(configured, required)` 只产出 warning 文案，调用方打日志继续跑。

**最小改动集**

| 改动 | 位置 | 内容 |
|---|---|---|
| 1 | `config/runtime_auth_mode.go`（新增函数） | `func ValidateRuntimeAuthModeRequirement(values map[string]any, requirements *ConfigRequirements) error`：当 `requirements.RuntimeAuthMode != nil` 且配置里存在不同的 mode 时，返回 `errors.New(RuntimeAuthModeConflictWarning(configured, required))`（复用同一句话，保证文案逐字一致）；无冲突/无 requirement 返回 nil |
| 2 | `config/requirements_file.go:95 configRequirementsFromValidatedMap` 的调用方（或 `Config` 加载完成处） | 把上面这个幂等检查接进加载流程，使冲突走「加载期错误」通道而不是 warn |

**新用例**：`config/runtime_auth_mode_like_rust_test.go`

- `TestRuntimeAuthModeConflictIsALoadErrorLikeRust`
  - 断言 A：`values={"features":{"runtime_agent_instances":{"mode":"dual_header"}}}` + `requirements.RuntimeAuthMode=AssertionOnly` → `err != nil`
  - 断言 B：`err.Error()` **逐字等于** `RuntimeAuthModeConflictWarning(RuntimeAuthModeDualHeader, RuntimeAuthModeAssertionOnly)`
  - 断言 C：无 requirement 或要求与配置一致 → `nil`

**对应 Rust**：`config/src/config_requirements.rs:968-990`（`ConstraintError::InvalidValue`，加载期失败）。
**风险**：`config/requirement_constraints_test.go:29` 里已有「falling back to required value …」的期望文案——说明 Rust 在报错的同时**也**回落到 required 值；所以 Go 保留 `RuntimeAuthMode()` 的回落语义、只把错误面接出来即可，不要改成"直接拒绝启动且不回落"。

**2026-11-08 状态变更：`managed-runtime-auth-mode` → complete，依据** `ValidateRuntimeAuthModeRequirement` 落在 `config/runtime_auth_mode.go:75-93`，接线在 `config/requirement_constraints.go:73-78`（经 `config/config.go:571` 与 `config/api.go:2290` 两条加载路径 `return err`）；用例 `config/runtime_auth_mode_like_rust_test.go:115 TestRuntimeAuthModeConflictIsALoadErrorLikeRust`（含端到端 `LoadEffectiveWithOptions` 断言）。manifest verifier 现为 `TestModeFromConfigValuesLikeRust;TestRuntimeAuthModeConflictIsALoadErrorLikeRust`。
> 注：上表"改动 2"猜的落点是 `requirements_file.go` 的调用方；实际落点是 `requirement_constraints.go` 的 `applyManagedConstrainedOverrides`（同时看见 config values 与 requirements，且被两条加载路径调用）。原文保留，仅此说明。

---

## 2. `mcp-network-policy-derivation`（规模 M）

**现状**：`appserver/network_approval.go:1491 persistNetworkPolicyAmendment` 只 `execpolicy.AppendNetworkRule(...)`（落盘）；`network/network_policy.go:248 NetworkPolicyController` 与 `:281 Publish(revision, policy)` 已存在但这条路径没用。

**最小改动集**

| 改动 | 位置 | 内容 |
|---|---|---|
| 1 | `appserver/network_approval.go:1491` → 改名/改造成 `applyNetworkRuleAndPersist` | 顺序改成与 Rust 一致：① 取 `controller.Policy()`；② 克隆 `DestinationPolicy` 并加 allow/deny 规则；③ `controller.Publish(revision+1, policy)`；④ 若代理可用，更新 allow/deny 名单；⑤ 最后 `execpolicy.AppendNetworkRule` 落盘 |
| 2 | `appserver/network_approval.go:59`（已有 `persistAmendment func(*NetworkPolicyAmendment, NetworkApprovalProtocol) error` 注入点） | 再加一个 `appendRule func(...) error` 字段，让用例能注入"落盘失败" |
| 3 | `network/proxy_server.go`（若需要） | 补 `AddDeniedDomain/AddAllowedDomain`（Rust 侧是 `proxy.add_denied_domain`） |

**新用例**：`appserver/network_policy_amendment_like_rust_test.go`

- `TestNetworkPolicyAmendmentPublishesBeforePersistingLikeRust`
  - 注入 `appendRule` 返回 error → 断言 ① 函数返回错误；② `controller.Policy().Allows(host)` 已经是 deny（**deny 在落盘失败后仍然生效**，这正是你指出的缺口）
- `TestNetworkPolicyAmendmentTakesUpdateLockLikeRust`
  - 并发两次（allow 一个 host、deny 另一个）→ 断言最终 policy **同时**含两条（无丢失更新；Rust 用 `update_lock` 保证）

**对应 Rust**：`core/src/exec_policy.rs:567-640 apply_network_rule_and_persist`（`update_lock` → clone → `add_network_rule` → `Arc` 发布 → `proxy.add_denied_domain` → 落盘）。
**注意**：这是 7 条里唯一牵涉并发语义的，锁的实现建议直接用 `NetworkPolicyController` 内部的 revision CAS（`Publish` 返回 bool），失败重试而不是加新 mutex。

---

## 3. `message-board-fixed-read-view`（规模 M）

**现状**：`agentboard/read_view.go` 只有 `Cutoff()` / `Channels()` / `ReadPost()` + 一组 read-only 拒绝方法。

**最小改动集**

| 改动 | 位置 | 内容 |
|---|---|---|
| 1 | `agentboard/read_view.go` 新增方法 | `Identity() string`、`StorageNamespace() string`、`ListChannels(ctx, caller, ChannelQuery)`、`ListThreads(ctx, caller, ThreadQuery)`、`SearchPosts(ctx, caller, PostQuery)`、`ReadThread(ctx, caller, ReadThreadRequest)`、`AppendedRequestIDs(ctx, caller)`、`ReadPostTexts(ctx, caller)` |
| 2 | 同上 | 每个查询把 capture 时的 `postCutoff` 传进现有 `LocalAgentMessageBoard` 的查询函数（`local.go` / `local/queries` 已有分页与 cutoff 参数形态） |

**新用例**：`agentboard/local_read_view_surface_like_rust_test.go`

- `TestLocalBoardReadViewServesListSearchAndReadThreadLikeRust`
  - capture 之后再建 channel / 发帖 / 建 thread → 断言 `ListChannels` 不含新 channel、`ListThreads` 不含新 thread、`SearchPosts` 搜不到新帖、`ReadThread` 返回冻结的帖序列（按 seq 升序）
  - 断言 `AppendedRequestIDs` 只含 capture 前的 request id
- `TestLocalBoardReadViewSearchHonorsCutoffAfterBackdatingLikeRust`
  - 把新帖 `timestamp` 改成 1 → 断言 `SearchPosts` 仍搜不到（复刻已有的 "backdate 不能偷渡" 断言，但换到搜索面）

**对应 Rust**：`message-board/src/read_view.rs:22-60` 的 trait 方法表（identity / storage_namespace / channels / with_json_search / list_channels / list_threads / search_posts / read_thread / read_post / appended_request_ids / read_post_texts）。

**2026-11-08 状态变更：`message-board-fixed-read-view` → complete，依据** `agentboard/read_view.go` 已补出完整读面（list/search/read-thread，cutoff 后回溯语义），用例 `TestLocalBoardReadViewServesListSearchAndReadThreadLikeRust`、`TestLocalBoardReadViewSearchHonorsCutoffAfterBackdatingLikeRust` 在主干跑绿；manifest 已转 complete。

---

## 4. `sidebar-chooser-list-bindings-and-hover`（规模 M）

**现状**：`tui/tea/sidebar_chooser.go:162 const sidebarChooserPageSize = 5`，在 `:209`/`:212` 直接加减；`tui/tea` 内**没有任何** `tea.MouseMsg` 处理（`rg 'MouseLeft|tea.MouseMsg' tui/tea` → 0）。

**最小改动集**

| 改动 | 位置 | 内容 |
|---|---|---|
| 1 | `tui/tea/sidebar_chooser.go:162` | 常量 → `func (m *Model) sidebarChooserPageSize() int`，返回列表**可见行数**（= 按窗高算出的 rows，可复用 `renderSidebarTabStrip`/chooser 渲染时的行数计算）；:209/:212 改用该函数 |
| 2 | `tui/tea` 新增鼠标分支 | `Update` 里接 `tea.MouseMsg`（`MouseActionPress` + `MouseButtonLeft`）：命中 `renderSidebarTabStrip` 的可见单元格 → 切换 tab；miss → 保持不变 |
| 3 | 同一次命中计算 | 同时驱动 hover（把"只覆盖条带首行"扩到整条 tab 区域），复用同一个 `sidebarTabAt(col,row)` |

**新用例**：`tui/tea/sidebar_chooser_mouse_and_paging_like_rust_test.go`

- `TestSidebarChooserPagesByVisibleRowsLikeRust`
  - 窗高 20 与 50 两种：PageDown 后 `m.sidebarChoice` 的增量**等于各自可见行数**，并断言 `!= 5`（当前实现会两次都给 5）
- `TestSidebarTabStripMouseSelectsVisibleTabLikeRust`
  - 在第二个 tab 的第 3 行点一下 → 选中 index 1；点空白列 → 选中不变；点条带之外 → 不变

**对应 Rust**：`tui/src/sidebar_chooser.rs:215 page_size(area,count,choice)`（步长 = `choice_rows(...).chats.len().max(1)`）；`tui/src/sidebar_tab_strip.rs:81 hit(area, point)`。

---

## 5. `typed-prompt-markdown-render`（规模 L，建议由 TUI lane 做）

**现状**：`tui/history_cell/messages.go:256 markdownPromptLines` 先 `markdown.Render(message, wrapWidth)` 拿到**字符串**，再按行贴 gutter/缩进（`markdown.Render` 签名见 `tui/markdown/render.go:42`，返回 `string`）。

**最小改动集**

| 改动 | 位置 | 内容 |
|---|---|---|
| 1 | `tui/markdown/render.go`（新增入口） | `func RenderSpans(text string, width int) ([]StyledLine, error)`，`StyledLine = {Spans []Span{Text, Style, Column}, }`；内部复用现有 `RenderWithThemeCwd` 的 AST/样式逻辑，只在最后一步保留 span 边界而不是拼成字符串 |
| 2 | `tui/history_cell/messages.go:256` | `markdownPromptLines` 改为消费 `RenderSpans`：gutter 占 `LivePrefixCols`、续行缩进 2 列、链接/提及列按 **span 起始列**计算 |

**新用例**：`tui/history_cell/user_prompt_markdown_spans_like_rust_test.go`

- `TestTypedPromptMarkdownColumnsMatchSpanModelLikeRust`
  - 输入 `**bold** and [link](https://example.test) and @agent`，宽度 40
  - 断言 ① 首个 span 的 `Column == LivePrefixCols`；② 链接 span 的起始列与 Rust 快照一致（显式期望列号）；③ 样式 span 的字节区间**落在文本内**（不是整行同色，当前实现整行 `styleUserMessageLine`）

**对应 Rust**：`tui/src/markdown_render.rs:392 render_markdown_text(input) -> Text<'static>`（ratatui `Text` = 行 → span → 样式 + 列）。
**风险**：`tui/markdown` 有 50+ 文件与一批快照用例，改渲染出口会影响 agent 侧列宽；建议先在 `RenderSpans` 里做**只读叠加**（不改 `Render` 的输出），把用户 prompt 侧切过去，再评估是否统一。

---

## 6. `cloud-image-messages-and-draft-handoff`（规模 M，建议拆条）

**现状**：`cloudclient/turns.go:144 encodeAddUserMessageRequest` 只写一个 text content item（`rg -i image cloudclient/ cloudbackend/` → 0）。

**最小改动集（image 半）**

| 改动 | 位置 | 内容 |
|---|---|---|
| 1 | `cloudclient/turns.go` | 新增 `type UserContentItem struct{ Kind string; Text string; ImageURL string; ImageBytes []byte }`；`AddUserMessageRequest.Items []UserContentItem`（保留 `Text` 作单文本快捷路径） |
| 2 | `cloudclient/turns.go:144` | 编码改成遍历 items：text → `UserInput.content[]` 的 text 项；image → image 项（tag 与 Rust `cloud-client/src/items.rs` 对齐） |
| 3 | `cloudclient/transcript.go` | 解码侧同样展开 items（现在是纯文本） |
| 4 | `cloudbackend/submission.go` | 透传 items（不 reinterpret） |

**新用例**：`cloudclient/turns_image_test.go`

- `TestAddUserMessageEncodesImageContentItemLikeRust`：text+image 两项 → 断言编码字节里出现 image content 的 tag 与 payload、且顺序为 text→image；再断言只给 image 时文本项**不出现**空 text 项

**draft handoff 半**：Go 侧目前**没有任何** draft handoff 承载（`rg -i "draft" app/cloud*.go cloudclient/ cloudbackend/` 只命中无关文件）。建议把这条契约拆成两条：`cloud-image-messages`（上面的改动即可 complete）+ `cloud-draft-handoff`（等 cloud lane 有承载面再立）。

---

## 7. `account-security-setup-appserver-fetch`

不在这里给改动集——按你的要求走 scope 调整提案（见 `plan_2026_11_08_scope_adjustment_proposal.md`）。

**2026-11-08 状态变更：`account-security-setup-appserver-fetch` → `account-security-setup-fetch`（改名 + 改 scope）→ complete**

- `supersededBy`: `account-security-setup-fetch`（旧名保留，本节不删）
- scope 变更：`goPaths` 去掉 `appserver` → `[app, chatgptapi, tui]`；`rustPaths` 换成 `account_processor/security_setup.rs` + `app-server-protocol/src/protocol/v2/account.rs` + `tui/src/security_setup.rs`
- 依据：`app/security_setup.go`（`interactiveLocalSecuritySetupReader`）+ `chatgptapi/security_setup.go:55 GetSecuritySetup` 已是真实承载面，用例 `TestCloudClientGetSecuritySetupDropsInvalidNoticesLikeRust`、`TestSecuritySetupStaleIdentityAndBannerPriorityLikeRust` 在主干跑绿

---

## 汇总

| 契约 | 规模 | 关键新用例 |
|---|---|---|
| `managed-runtime-auth-mode` | S | `TestRuntimeAuthModeConflictIsALoadErrorLikeRust` |
| `mcp-network-policy-derivation` | M | `TestNetworkPolicyAmendmentPublishesBeforePersistingLikeRust`、`TestNetworkPolicyAmendmentTakesUpdateLockLikeRust` |
| `message-board-fixed-read-view` | M | `TestLocalBoardReadViewServesListSearchAndReadThreadLikeRust`、`TestLocalBoardReadViewSearchHonorsCutoffAfterBackdatingLikeRust` |
| `sidebar-chooser-list-bindings-and-hover` | M | `TestSidebarChooserPagesByVisibleRowsLikeRust`、`TestSidebarTabStripMouseSelectsVisibleTabLikeRust` |
| `typed-prompt-markdown-render` | L | `TestTypedPromptMarkdownColumnsMatchSpanModelLikeRust` |
| `cloud-image-messages-and-draft-handoff` | M（建议拆） | `TestAddUserMessageEncodesImageContentItemLikeRust` |

**2026-11-08 状态变更（汇总表对应）**

| 契约 | 写这份文档时的判定 | 当前状态 |
|---|---|---|
| `managed-runtime-auth-mode` | needs-behaviour（S） | **complete** |
| `mcp-network-policy-derivation` | needs-behaviour（M） | partial |
| `message-board-fixed-read-view` | needs-behaviour（M） | **complete** |
| `sidebar-chooser-list-bindings-and-hover` | needs-behaviour（M） | partial |
| `typed-prompt-markdown-render` | needs-behaviour（L） | partial |
| `cloud-image-messages-and-draft-handoff` | needs-behaviour（M） | partial |
| `account-security-setup-appserver-fetch` | needs-behaviour（改 scope） | **complete**（id/scope 已改为 `account-security-setup-fetch`） |
