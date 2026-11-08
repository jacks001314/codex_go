# 契约状态 × commit 台账 对账报告（2026-11-08）

- 作者：**ledger-align** · 2026-11-08 · **只读分析**：未改 `parity/commits.json`、未改你给的 `manifest.json`、未改任何 Go 代码
- 目的：把「契约侧（136 条 manifest）」与「commit 侧（我维护的 `parity/commits.json`，3677 条）」摆在一起，标出**互相矛盾的账目**；不替你改判

## 0. 数据源与口径

| 数据 | 来源 | 规模 | 状态分布 |
|---|---|---|---|
| 契约 | 你本轮附件 `manifest.json`（`targetRustCommit` = `748ab55ca7…`） | 136 条 | **106 complete / 30 partial** |
| commit 台账 | 我的 `parity/commits.json`（`rustToInclusive` = `748ab55ca7de0b647d9a7a9d9a8e70e00f28666d`） | 3677 条 | **2963 complete / 639 not_applicable / 71 partial / 4 blocked_precondition** |

两边的状态词汇**不同域**：契约只有 `complete|partial`；commit 侧有 4 个值。所以「一致」只能按映射关系判定，见 §1.2。

### 0.1 一个结构性事实（决定了本报告的覆盖率）

`manifest.json` 的契约对象**只有 7 个字段**：`id / kind / rustPaths / goPaths / phase / status / verifier` —— **没有任何 commit 或 PR 字段**。因此「契约 → commit」的映射只能来自我写的计划文档（`update/plan_*.md` 里的登记表与 deep-dive 标题），这本身就是一个长期漂移源。

## 1. 映射规则与覆盖率

### 1.1 提取规则（可复现，脚本见 §9）

只认 4 种**登记式**行，且要求「契约 id 以反引号出现在表格首列 / 标题里」：

```text
1) deep-dive 标题   ## N. `<id>` — #<PR> `<sha10>`
2) 登记表行         | `<sha10>` #<PR> | `<id>` | …          （含 #60006(a)/(c) 子项写法）
3) 双 commit 行     | `<sha10>` #<PR> + `<sha10>` #<PR> | `<id>` | …
4) 计划表行         | N | `<id>` | #<PR> `<sha10>` | …  或  | N | `<id>` | #<PR> | …
```

**不认**：正文里顺带提到 id 的行（例如「`realtime-runtime-auth`(#60003) + `runtime-auth-websocket-revalidation`(#60055)」这种依赖说明行同时含两个 id，会污染归属，已排除）。

解析后：`sha10` 先按 `commits[].sha` 前缀唯一匹配；不唯一或缺失时回退到 `subject` 末尾 `(#PR)` 唯一匹配。

### 1.2 判定口径

| 类 | 条件 |
|---|---|
| **A** | 契约 `complete`，且它关联的 commit 在 commits.json 里是 `partial` / `blocked_precondition` / `not_applicable` |
| **B** | 契约 `partial`，且它关联的 commit 是 `complete` |
| **C** | 关联 commit 在 commits.json 里查不到（sha 前缀解析不到且 PR 也对不上） |
| **D** | commits.json 里 `status=partial` 且**没有被任何已映射契约引用** |
| 一致 | 其余已映射组合（complete↔complete、partial↔partial） |

### 1.3 覆盖率

| 项 | 数量 | 占比 |
|---|---|---|
| 契约总数 | 136 | 100% |
| **有登记关联（可对账）** | **48** | 35.3% |
| **无登记关联（进 §8）** | **88** | 64.7% |

> 这 88 条**全部**是 `complete`（没有一条 partial），`kind` 以 `shared-fixtures`（44）与 `source-surface`（33）为主——它们是我这条 lane 之前就已完成的存量契约，manifest 里没有 commit 引用。**这不是「对上了」，而是「无从对账」**，必须单列。

## 2. 契约 → commit 映射总表（48 条）

列说明：`契约 status` = manifest；`commit status` = commits.json 中该 sha 的状态（多 commit 用 `/` 连接）。

| contract | PR | sha10 | 契约 status | commit status | 分类 |
|---|---|---|---|---|---|
| `account-read-user-id` | #60004 | `01ab354312` | complete | partial | A |
| `agent-tree-id` | #60190 | `c9b4267c37` | complete | partial | A |
| `child-agent-catalog-preservation` | #60049 | `44ccf39457` | complete | partial | A |
| `final-answer-moderation` | #59842 | `ddd1812a51` | complete | partial | A |
| `guardian-repl-provenance-bound` | #59891 | `b1a0dff158` | complete | partial | A |
| `guardian-verified-answer-whole` | #59902 | `b8725c1896` | complete | partial | A |
| `image-original-detail-token-estimate` | #59908 | `bbf57cec15` | complete | not_applicable | A |
| `logs-prune-window-frame` | #60006 | `df769889c0` | complete | partial | A |
| `mcp-lazy-stdio-startup` | #60039 | `af9a98b074` | complete | partial | A |
| `message-board-bulk-subscriptions` | #59998 | `eb485f57ba` | complete | partial | A |
| `patch-approval-cancel-turn-scope` | #59910 | `ad1dbba32d` | complete | partial | A |
| `runtime-auth-terminal-denials` | #60096 | `efdacaf46f` | complete | partial | A |
| `thread-token-narrow-update` | #60006 | `df769889c0` | complete | partial | A |
| `thread-writer-lock-shared-coordination` | #59969 | `e428b9a16b` | complete | partial | A |
| `account-security-setup-appserver-fetch` | #60082 | `4be90eb069` | partial | partial | 一致 |
| `analytics-runtime-auth` | #60101 | `cce9d3228b` | partial | partial | 一致 |
| `cloud-image-messages-and-draft-handoff` | #60061 | `e6bf88ae02` | partial | partial | 一致 |
| `cloud-thread-project-binding` | #60007 | `7de8a0afc0` | partial | partial | 一致 |
| `code-mode-host-default-features` | #59973 | `247934cfe5` | complete | complete | 一致 |
| `code-mode-incremental-exec-definitions` | #60157 | `2bc8e7f545` | partial | partial | 一致 |
| `code-mode-incremental-exec-wiring` | #60163 | `33b3f80a09` | partial | partial | 一致 |
| `incremental-exec-catalog-metrics` | #60177 | `5226f2fb4b` | partial | partial | 一致 |
| `incremental-exec-message-overrides` | #60188 | `c3936b4b8a` | partial | partial | 一致 |
| `incremental-tools-legacy-bedrock-gate` | #60040 | `762f87d5bb` | complete | complete | 一致 |
| `managed-runtime-auth-mode` | #60014 | `10cf2b9ea2` | partial | partial | 一致 |
| `mcp-app-invocation-context-refresh` | #59991 | `8fcabd75a0` | partial | partial | 一致 |
| `mcp-cached-tool-startup-errors` | #60038 | `fb2b06a829` | complete | complete | 一致 |
| `mcp-network-policy-derivation` | #60046 | `b7d2321ef7` | partial | partial | 一致 |
| `mcp-sharing-discovery-deferred-startup` | #60037 | `7ccf78dec4` | complete | complete | 一致 |
| `message-board-fixed-read-view` | #59993 | `632eee03e9` | partial | partial | 一致 |
| `multi-agent-hosted-tools-guidance` | #60011 | `e0aa0434f4` | partial | partial | 一致 |
| `protected-model-profile-downgrade` | #59933 | `c1c07853fe` | partial | partial | 一致 |
| `provider-creation-during-signin` | #60186 | `d960b36694` | partial | partial | 一致 |
| `realtime-runtime-auth` | #60003 | `44f1b0e867` | partial | partial | 一致 |
| `release-prerelease-cleanup` | #59887/59903 | `b6f53857fc;e3fe1ade2c` | partial | partial / partial | 一致 |
| `remote-control-runtime-auth` | #60073 | `f4275a2a2e` | partial | partial | 一致 |
| `runtime-auth-websocket-revalidation` | #60055 | `d4a341bd52` | partial | partial | 一致 |
| `side-chat-persistence-and-tab-close` | #60152 | `57e64682f8` | partial | partial | 一致 |
| `side-chat-policy-first-request` | #60067 | `0ed34eb9ce` | partial | partial | 一致 |
| `sidebar-chooser-list-bindings-and-hover` | #59928 | `814347ecd2` | partial | partial | 一致 |
| `sidebar-tab-keyboard-mouse-navigation` | #60181 | `8044c6867c` | partial | partial | 一致 |
| `skill-mention-punctuation-join` | #60180 | `1455fda735` | partial | partial | 一致 |
| `skills-tool-name-resolution` | #59954 | `0768988e55` | partial | partial | 一致 |
| `thread-model-metadata-exposure` | #59958 | `12dc86beb3` | partial | partial | 一致 |
| `tui-custom-gateway-signin` | #60009 | `2497eab251` | partial | partial | 一致 |
| `turn-context-required-permission-profile` | #59963 | `2889d8e1bf` | partial | partial | 一致 |
| `typed-prompt-markdown-render` | #59983 | `78b1dda5f6` | partial | partial | 一致 |
| `user-verification-platform-provider` | #43624 | `e7637306bc` | partial | blocked_precondition | 一致 |

## 3. A 类：契约 complete、commit 非 complete（14 条）

| contract | PR | sha10 | 契约 status | commit status |
|---|---|---|---|---|
| `account-read-user-id` | #60004 | `01ab354312` | complete | partial |
| `agent-tree-id` | #60190 | `c9b4267c37` | complete | partial |
| `child-agent-catalog-preservation` | #60049 | `44ccf39457` | complete | partial |
| `final-answer-moderation` | #59842 | `ddd1812a51` | complete | partial |
| `guardian-repl-provenance-bound` | #59891 | `b1a0dff158` | complete | partial |
| `guardian-verified-answer-whole` | #59902 | `b8725c1896` | complete | partial |
| `image-original-detail-token-estimate` | #59908 | `bbf57cec15` | complete | not_applicable |
| `logs-prune-window-frame` | #60006 | `df769889c0` | complete | partial |
| `mcp-lazy-stdio-startup` | #60039 | `af9a98b074` | complete | partial |
| `message-board-bulk-subscriptions` | #59998 | `eb485f57ba` | complete | partial |
| `patch-approval-cancel-turn-scope` | #59910 | `ad1dbba32d` | complete | partial |
| `runtime-auth-terminal-denials` | #60096 | `efdacaf46f` | complete | partial |
| `thread-token-narrow-update` | #60006 | `df769889c0` | complete | partial |
| `thread-writer-lock-shared-coordination` | #59969 | `e428b9a16b` | complete | partial |

### 3.1 逐条「该以哪边为准」

| contract | 判断 | 一句话理由 |
|---|---|---|
| `account-read-user-id` | **以 manifest 为准**（commit 侧应上调） | #60004 的实现与 verifier 已在主干（你本轮报的状态是已落地），commits.json 里 `01ab354312` 仍是 partial 属台账滞后 |
| `agent-tree-id` | **以 manifest 为准** | #60190 生产者（`agentTreeIDs`/`agentTreeID()`）已由 win lane 落地、用例跑绿；我此前判「生产者不存在」是镜像落后所致，已接受更正 |
| `child-agent-catalog-preservation` | **以 manifest 为准** | `RefreshOffline → RefreshInMemory` 已落，commits.json 未跟上 |
| `final-answer-moderation` | **以 manifest 为准** | mac lane 已并 `final_answer_moderation*`，台账未跟上 |
| `guardian-repl-provenance-bound` | **以 manifest 为准** | #59891 的边界实现已落（上下文预算批），台账仍 partial |
| `guardian-verified-answer-whole` | **以 manifest 为准** | #59902 同上 |
| `logs-prune-window-frame` | **以 manifest 为准** | #60006(c) 已落，台账的 `df769889c0` 未拆子项（见 §7.2） |
| `mcp-lazy-stdio-startup` | **以 manifest 为准** | #60039 懒启动已落 |
| `message-board-bulk-subscriptions` | **以 manifest 为准** | #59998 原子批量订阅已落 |
| `patch-approval-cancel-turn-scope` | **以 manifest 为准** | 你已按判据落地并加了两个用例（`TestFileChangeApprovalCancelInterruptsRequestingTurnLikeRust` 等） |
| `runtime-auth-terminal-denials` | **以 manifest 为准** | #60096 的调度已由 win lane 落地（`isRuntimeAuthDenial` 谓词 + 恢复短路） |
| `thread-token-narrow-update` | **以 manifest 为准** | #60006(a) 已落 |
| `thread-writer-lock-shared-coordination` | **以 manifest 为准** | #59969 共享协调已落 |
| `image-original-detail-token-estimate` | **⚠️ 需人工裁决（两边都不能直接采信）** | 契约 `complete` 意味着「Go 已对齐」；但 commits.json 里 #59908 `bbf57cec15` 是 `not_applicable`，含义是「Rust 侧无该行为面」。**complete 与 not_applicable 不可能同时为真**；且我在 P1 曾把 #54776 的过判降级为 partial，说明这条的历史判定有过反复。请以「verifier 是否真断言了 detail-aware 估算」为准，而不是任一侧的 status |

> 这 14 条的共同形态：**实现先行、台账滞后**。若你希望台账跟上，方向是把这些 commit 从 `partial` 上调为 `complete`（除最后一条），但这属于改 `parity/commits.json`，本轮我不动。

## 4. B 类：契约 partial、commit complete（0 条）

无。也就是说**没有出现「commit 早就完成、契约却还挂着 partial」**的情况——这对你是好消息：partial 的 30 条里，凡有登记关联的都在 commit 侧同样是 partial。

## 5. C 类：关联 commit 在 commits.json 里查不到（0 条）

无。48 条映射全部解析成功（48 个不同 sha，全部能在 3677 条里唯一命中）。

## 6. D 类：commits.json 里 partial、但没有契约引用（29 条）

commits.json 共 71 条 `partial`；被已映射契约引用的有 42 条，**未被任何契约引用的 29 条**如下（这些是「可能漏登记契约」的候选）：

| sha10 | PR | 主题分组 | subject |
|---|---|---|---|
| `194f2662b8` | #59739 | Cloud / 侧栏 | Add a sidebar chooser and floating workspace shortcut panels (#5 |
| `ecef7e0936` | #60001 | Guardian 评审链 | Add bounded original-evidence delivery for Guardian reviewers (# |
| `1a2f5babe8` | #59988 | 其它 | Add inherited styling to TUI display trees (#59988) |
| `af83299ada` | #59999 | Guardian 评审链 | Add model-driven Guardian channel selection (#59999) |
| `8e7ccd8472` | #60026 | UI tree / 渲染 | Add multiline layout and row wrapping to UI trees (#60026) |
| `e4bd7871b1` | #59994 | Guardian 评审链 | Add read-only evidence retrieval tools for Guardian reviewers (# |
| `9df200a068` | #59767 | Cloud / 侧栏 | Add sidebar tabs and nonblocking side-chat creation (#59767) |
| `8d0098930e` | #59995 | Guardian 评审链 | Add subscription tools for Guardian reviewers (#59995) |
| `2157f98648` | #60019 | UI tree / 渲染 | Add theme-aware semantic colors to extension UI trees (#60019) |
| `cfc11eb483` | #60032 | 其它 | Allow extension status content to use multiple footer rows (#600 |
| `358e464528` | #60048 | 其它 | Allow plugin starter prompts up to 512 characters (#60048) |
| `09bd19a0de` | #60002 | Guardian 评审链 | Decouple Guardian retrieval from local message-board handles (#6 |
| `654b2ec1f5` | #57267 | Cloud / 侧栏 | Keep cloud conversation settings server-owned in the TUI (#57267 |
| `66a56b17a1` | #60027 | UI tree / 渲染 | Keep configured status lines on the native renderer (#60027) |
| `4980f9585a` | #59944 | 其它 | Narrow openai-docs to product Q&A and first-time API setup (#599 |
| `1a20f53c33` | #60021 | UI tree / 渲染 | Normalize inline UI tree text across styled leaves (#60021) |
| `5f82dc9e59` | #60000 | Guardian 评审链 | Plan Guardian evidence delivery in source order (#60000) |
| `857415f7f2` | #59003 | Guardian 评审链 | Preserve Guardian review framing under elastic context limits (# |
| `202b590e84` | #60172 | 审核 / 分析 | Preserve display instructions in answer moderation (#60172) |
| `181b4a853d` | #58720 | 其它 | Prioritize pending approvals in the subagent picker (#58720) |
| `5ff78c5e66` | #60018 | 审核 / 分析 | Propagate turn triggers to usage analytics events (#60018) |
| `7cdb219883` | #60029 | UI tree / 渲染 | Render UI trees directly into terminal buffers (#60029) |
| `cf39544acd` | #59987 | 其它 | Resolve startup models using thread creation options (#59987) |
| `d749ee847f` | #60022 | UI tree / 渲染 | Separate UI tree preparation from line rendering (#60022) |
| `7e980a54fe` | #60020 | UI tree / 渲染 | Strip terminal commands from UI tree strings (#60020) |
| `f5ac7a757e` | #60075 | 其它 | Support asynchronous host service-tier reads and acceptance (#60 |
| `8c811e8d93` | #58718 | UI tree / 渲染 | Support rendering TUI conversations within an inset area (#58718 |
| `ce0323f2f2` | #54776 | 其它 | Support typed content in contextual prompt fragments (#54776) |
| `5c7a1602c8` | #60031 | UI tree / 渲染 | Unify TUI status line layout for extension content (#60031) |

分组计数：**UI tree / 渲染 9 条**、**Guardian 评审链 7 条**、**Cloud / 侧栏 3 条**、**审核 / 分析 2 条**、**其它 8 条**。

## 7. 汇总与「必然一致不了」的口径

### 7.1 四类计数

| 类 | 数量 | 说明 |
|---|---|---|
| A（契约 complete / commit 非 complete） | **14** | 全部是「实现先行、台账滞后」；1 条（`image-original-detail-token-estimate`）语义冲突需裁决 |
| B（契约 partial / commit complete） | **0** | — |
| C（commit 查不到） | **0** | — |
| 一致（complete↔complete + partial↔partial） | **34** | 含 `release-prerelease-cleanup`（双 commit 均为 partial） |
| D（partial commit 无契约引用） | **29** | 候选漏登记 |
| 无法对账（契约无 commit 关联） | **88** | 见 §8 |

### 7.2 结构性无法一一对应的情形（单独口径，不算 A/B/C/D）

1. **一个 commit 对应多条契约**：`df769889c0`（#60006 "Reduce SQLite write overhead…"）同时是 `logs-prune-window-frame`(#60006(c)) 与 `thread-token-narrow-update`(#60006(a)) 的承载点。commit 侧的 status 只有**一个**，无法同时表达两个契约的进展 → 这类条目**在两边状态不同时，不能判定为哪一边错**。
2. **一个契约跨多个 commit**：`release-prerelease-cleanup` 关联 `b6f53857fc`(#59887) + `e3fe1ade2c`(#59903)，两者在台账里都是 partial，契约也是 partial → 记为一致；但只要其中一个先完成，就会出现「语义上一半完成」而 status 只能取一个值的窘境。
3. **状态词汇不同域**：`not_applicable` 在 commit 侧是「上游不存在该面」，在契约侧没有对应值。`image-original-detail-token-estimate` 的 A 类冲突就源于此（§3.1 最后一行）。
4. **契约没有任何 commit 字段**（§0.1）：88 条无法对账的根因；要根治得给 manifest 加字段，而不是继续靠计划文档人工维护。

## 8. 无关联登记的契约（88 条）

这 88 条在 manifest 里是 `complete`，但在我的计划文档里**没有 PR / sha 登记**，因此无法与 commits.json 对账（不硬凑）。按 `kind` 分组：

**shared-fixtures（44 条）**：`agents-overview-dashboard`、`agents-overview-in-session`、`apply-patch-freeform-spec`、`apply-patch-scenarios`、`appserver-project-apis`、`appserver-reject-obsolete-permission-profile`、`backend-task-details-fixtures`、`collaboration-mode-templates`、`config-legacy-project-roots-alias`、`config-parse-shared-fixtures`、`config-windows-ignore-legacy-managed-config`、`delegate-noninteractive-approval-policy`、`doctor-network-failure-classification`、`env-noise-auth-token-scrub`、`environment-exec-policy-overlay`、`exec-apply-patch-freeform`、`goal-prompt-templates`、`gpt-5.6-long-context-window`、`guardian-max-tool-call-lag`、`guardian-parent-compaction-bound`、`guardian-v2-managed-reviewers-disable`、`hooks-command-input-schemas`、`hooks-command-output-schemas`、`http-client-ca-fixtures`、`image-preparation-dimensions`、`imagegen-description`、`managed-auth-backend-requirements`、`memory-tool-developer-instructions`、`queued-submission-load-dispatch`、`remote-sandbox-config-shared-fixtures`、`remote-tui-project-trust`、`session-startup-prewarm`、`shell-environment-policy-per-environment`、`skill-shadow-fused-selectors`、`skill-shadow-task-context-fusion`、`skills-max-context-tokens-budget`、`tool-schema-policy`、`tui-command-activity-cells`、`tui-edit-queued-message-vim-history-up`、`tui-editor-kill-yank`、`tui-vim-normal-mode`、`turn-environment-cwd-size-bound`、`windows-sandbox-nux-trust-decision`、`wsl-update-path-normalization`

**source-surface（33 条）**：`agents-dashboard-command`、`audio-preparation`、`cli`、`config-batch-write-mutation`、`config-schema-surface`、`desktop-doctor-diagnostics`、`edu-plus-pro-account-plans`、`exec-ignore-user-config-skips-user-layer`、`exec-server-trace-context`、`experimental-feature-catalog`、`features`、`ide-pipe-identification-only`、`in-app-chat-dictation-gates`、`invalid-grant-permanent-refresh-failure`、`mcp-redirect-same-origin`、`mcp-tool-name-128-boundary`、`model-provided-shell-resolved-by-type`、`process-hardening`、`queue-by-name-unique-session`、`queue-cli-command`、`sed-in-place-fail-closed`、`skill-model-delegation-removed`、`task-mentions`、`thread-realtime-surface`、`unarchive-prompt`、`user-verification-activity-check`、`voice-control-protocol`、`voice-helper-exit-stages`、`windows-mxc-job-ui-limit-capability`、`windows-mxc-managed-network-proxied-shape`、`windows-mxc-psec-availability`、`windows-mxc-tty-launch`、`windows-sandbox-filesystem-override-resolution`

**generated-protocol（3 条）**：`app-server-schema-experimental`、`app-server-schema-stable`、`strict-review-required-notification`

**terminal-ui（2 条）**：`cli-blackbox-slash-differential`、`tui-snapshots`

**record-replay（2 条）**：`record-replay-harness-generalized`、`story-recording-replay`

**wire-protocol（1 条）**：`exec-server-protocol`

**sqlite-schema（1 条）**：`state-migrations`

**differential-oracle（1 条）**：`apply-patch-differential-oracle`

**durable-data（1 条）**：`rollout-layout`

### 8.1 被我刻意排除的「散文级 PR 提及」

这 3 条在旧计划文档里出现过 PR 号，但都不在**登记表**里（是正文叙述或旧批次备注），按 §1.1 规则不予采信；列出来供你判断是否要补登记：

| contract | 散文里的 PR | 出处形态 | 为何不采信 |
|---|---|---|---|
| `agents-overview-in-session` | #39094 / #39112 | 「`agents-overview-in-session`（153→154 done）；commits.json #39094/#39112」 | 无 sha10、无登记表行；#39094 在 commits.json 里查不到（PR 编号早于当前账本覆盖范围） |
| `apply-patch-freeform-spec` | #41477 | 一行变更说明里的 `rustPaths` 修正备注 | 该行讲的是 rustPaths 迁移，不是契约登记 |
| `cli` | #57232 | 「N/A for TUI: `codex queue --resume` 由 cli/app 处理」 | 明确写的是「N/A」，不是登记 |

> 这 88 条集中在 `shared-fixtures`（44：schema/fixture 类）与 `source-surface`（33），属存量对齐工作；若要可对账，建议在 manifest 里增补 `commit` 或 `pr` 字段，或把这些契约统一挂到「其引入批次」的代表 commit 上（需你定口径）。

## 9. 复现方式（字段路径 + 筛选条件）

```python
# 1) 读两侧
man = json.load(open("manifest.json"))["contracts"]        # 契约：字段 id/status/...
led = json.load(open("parity/commits.json"))["commits"]    # commit：sha/subject/status/kind/domains/evidence

# 2) PR 解析（commit 侧的 PR 号只能从 subject 取）
re.search(r"\(#(\d+)\)\s*$", commit["subject"])         # → "#60004" 的 60004

# 3) sha10 → full sha（前缀唯一匹配）
[x for x in led if x["sha"].startswith(sha10)]

# 4) 四类判定
A = contract.status == "complete" and commit.status in ('partial', 'blocked_precondition', 'not_applicable')
B = contract.status == "partial"  and commit.status == "complete"
C = commit is None
D = [c for c in led if c["status"] == "partial" and c["sha"] not in referenced_shas]
```

映射来源文件（48 条覆盖到的计划文档）：`plan_2026_11_08_backlog.md`、`p1_deep_dive.md`、`p2_deep_dive.md`、`context_budget.md`、`mcp_lazy.md`、`cloud_sidebar.md`、`code_mode_a.md`、`code_mode_b.md`、`model_1.md`、`model_2.md`、`misc_2.md`、`misc_3.md`、`thread_storage.md`、`ci_3.md`、`tail5.md`。

## 10. 结论（只指出矛盾，不改判）

1. **唯一的实质矛盾是 A 类 14 条**：契约说「已完成」，台账说「还没完成」。其中 13 条形态一致（实现先行、台账滞后），**1 条 `image-original-detail-token-estimate` 是语义冲突**（complete vs not_applicable），建议优先裁决。
2. **B/C 均为 0**：说明「commit 已完成但契约仍 partial」这类漏改判没有发生；也没出现查不到的 commit。
3. **D 类 29 条**是漏登记候选，其中 **Guardian 评审链 7 条 + UI tree/渲染 9 条**最集中，若这些行为现在仍是 partial，值得单独立契约。
4. **最大风险不是 A/B/C/D，而是 88 条无法对账**（64.7%）：manifest 没有 commit 字段，一致性完全靠人工计划文档维系；上一轮我这边 `rg TreeID appserver/` 误判的根因也是同一类问题（两侧不同源）。
5. 我没有改任何一侧的数据；如果你要我把 A 类 13 条 + 裁决后的第 14 条写进 commits.json，那需要你明确授权我改 `parity/commits.json`（当前指令是只读）。
