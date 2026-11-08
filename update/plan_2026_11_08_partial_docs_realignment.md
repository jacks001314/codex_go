# 三份 partial 文档的口径校正（只改计数与状态）

- 日期：2026-11-08
- 角色：ledger-align
- 授权口径：你给的 3 条规则（① 只改计数与状态，历史结论/红行原文/判定理由一字不动；② 死用例名加 `supersededBy`，条目保留；③ 已转 complete 的三条保留原判定 + 追加「2026-11-08 状态变更：<id> → complete，依据 …」，旧名保留并加 `supersededBy`）
- 基准：主干 `parity/contracts/manifest.json`，138 契约 / **133 complete / 5 partial**，sha256 `e139176836e9beaf365f765ac784402ba8c09db714d31c636acd11153813d75a`（67643 B）——所有数字都从 `contracts[].status` 现算，没有手写。

## 1. 改了什么（逐条）

### `partial_contract_verdicts_2026_11_08.json`

| 位置 | 改前 | 改后 |
|---|---|---|
| `source.sha256` | `8b8cc3635b3be9b1…`（73,948 B，r12 那次 reflow 的版本） | `e139176836e9beaf…`（67643 B） |
| `source.bytes` | 73948 | 67643 |
| `source.statusCounts` | complete 117 / partial 21 | complete 133 / partial 5 |
| 16 条 entry 的 `status` | `partial` | `complete`（逐条见下表） |
| `side-chat-policy-first-request` 的 `verifiers[0]`（`TestSideDeveloperInstructionsAppendsExistingPolicyMatchRust`） | `"exists": true` | `"exists": false` + `"supersededBy": "TestSideDeveloperInstructionsKeepExistingPolicyLikeRust"`（name / file / line 保留） |
| `account-security-setup-appserver-fetch` 条目 | — | 新增 `"supersededBy": "account-security-setup-fetch"`（`id` 保留旧名） |

`status` 变化的 16 条：`managed-runtime-auth-mode`, `mcp-app-invocation-context-refresh`, `message-board-fixed-read-view`, `cloud-thread-project-binding`, `sidebar-tab-keyboard-mouse-navigation`, `side-chat-persistence-and-tab-close`, `side-chat-policy-first-request`, `code-mode-incremental-exec-definitions`, `code-mode-incremental-exec-wiring`, `incremental-exec-message-overrides`, `incremental-exec-catalog-metrics`, `turn-context-required-permission-profile`, `provider-creation-during-signin`, `tui-custom-gateway-signin`, `account-security-setup-appserver-fetch`, `user-verification-platform-provider`。
`counts` / `total`（verdict 级计数）**未动** —— 它们是判定口径的计数，不是契约状态计数。

### `partial_contract_mutation_evidence_2026_11_08.json`

| 位置 | 改前 | 改后 |
|---|---|---|
| `source.sha256` / `bytes` | `918623495668733e…` / 67227 | `e139176836e9beaf…` / 67643 |
| `source.statusCounts` | complete 118 / partial 20 | complete 133 / partial 5 |
| 15 条 entry 的 `status` | `partial` | `complete` |
| `side-chat-policy-first-request` 条目 | — | 新增 entry 级 `"supersededBy": {"TestSideDeveloperInstructionsAppendsExistingPolicyMatchRust": "TestSideDeveloperInstructionsKeepExistingPolicyLikeRust"}`（`verifier` 数组与 `removalEvidence` 原文保留） |
| `account-security-setup-appserver-fetch` 条目 | — | 新增 `"supersededBy": "account-security-setup-fetch"` |

`status` 变化的 15 条：`managed-runtime-auth-mode`, `mcp-app-invocation-context-refresh`, `message-board-fixed-read-view`, `cloud-thread-project-binding`, `sidebar-tab-keyboard-mouse-navigation`, `side-chat-persistence-and-tab-close`, `side-chat-policy-first-request`, `code-mode-incremental-exec-definitions`, `code-mode-incremental-exec-wiring`, `incremental-exec-message-overrides`, `incremental-exec-catalog-metrics`, `turn-context-required-permission-profile`, `provider-creation-during-signin`, `tui-custom-gateway-signin`, `account-security-setup-appserver-fetch`。

### `plan_2026_11_08_partial_closeout.md`

| 位置 | 处理 |
|---|---|
| 第 3 行「对象：manifest …」 | 计数 `138 / 130 complete / 8 partial` → `138 / 133 complete / 5 partial`，sha `3647d8df…` → `e139176836e9beaf…` |
| 第 4 行后 | 追加 `> **2026-11-08 状态变更…**` 引用块：写作时口径 vs 当前口径、3 条已 complete、4 条仍 partial、1 条 `release-prerelease-cleanup` 在外 |
| §1 末尾 | 追加 `**2026-11-08 状态变更：\`managed-runtime-auth-mode\` → complete，依据 …**` + 一行说明「改动 2 的落点猜的是 `requirements_file.go`，实际是 `requirement_constraints.go`」（原文保留） |
| §3 末尾 | 追加 `**2026-11-08 状态变更：\`message-board-fixed-read-view\` → complete，依据 …**` |
| §7 末尾 | 追加 改名 + scope 变更 + `supersededBy: account-security-setup-fetch` + 依据 四行 |
| 「汇总」表后 | 追加 `**2026-11-08 状态变更（汇总表对应）**` 小表：7 条的「写作时判定 → 当前状态」 |

历史判定（needs-behaviour / complete / undecidable）、红行原文、每条的理由与"最小改动集"表格**一字未动**。

## 2. LF 副本 sha256（你要的每文件值）

| 文件 | 改前 LF | 改后 LF |
|---|---|---|
| `partial_contract_verdicts_2026_11_08.json` | 30,065 B `857d5fecd5c1922903a980f5c64e42145eab4d0c530aca5af9c6f081611386af` | 30,212 B `6399abd833aa2f454e97bf03d52b3e2a832d07908334ba724c6392e6eafeaa1f` |
| `partial_contract_mutation_evidence_2026_11_08.json` | 21,301 B `53f44df76fe1829fba2ac74bedc0f839477c3106cbb5b55a53b6d60495d4f6bf` | 21,518 B `c6dd50d41cfd6840e24a565f3168676627a356492d2243ca27231237bbef6d4e` |
| `plan_2026_11_08_partial_closeout.md` | 11,893 B `3ab291eb68fcfe84dcc2a7229d8f8e5a8926e813490007a787c21cc643d31aaf` | 15,519 B `2df507dd4507d36e1a57a3957acbc4a42e62f0b83d03af9735c0ab36e3e15df0` |

（工作区里的实际文件是 CRLF；这里按你"LF 副本"的口径算。CRLF 版本 sha256 见 `MANIFEST.tsv` 里的 diff 头与 `EVIDENCE/sha256_crlf.txt`。）

## 3. 不变量检查（我自己跑的，不是口头保证）

对两份 JSON 做了字段级比对：除 `status`、`supersededBy`、`source` 的 sha/bytes/statusCounts 之外，**任何字段与条目顺序都没有变化**（`verdict`、`rustAnchor`、`goImplementation`、`removalEvidence`、`residual`、`evidence`、`goAnchors`、`phase`、`kind`、`missing` 全部逐字段相等）；`counts` / `total` 未动。原始输出见 `EVIDENCE/json_invariant_check.log`。

## 4. 一个必须先解决的冲突（重要）

这批文件正是你上一条派给 win 做「口径对齐」的那三个。我这次是按你**这条**指令做的，但我只拿到附件、没有和 win 协商：**如果 win 也在同一分支改这三份，会撞车**。两条路你选一条：

1. 让 win 跳过这三个文件（只保留 `README_status_index.md` 那份），直接合我这个包；
2. 保留 win 的版本，把我这三份当作「差值报告」——每条的 before/after 都在 §1 表里，可由你手工搬到 win 的版本上。

我建议 1：我的改动是**字段级最小差分**（17/17/5 个 hunk，无重排、无 reflow），冲突面最小。

## 5. 已知残留（按你的规则**没改**，等你单独授权）

- `partial_contract_verdicts_2026_11_08.json` 里 `side-chat-policy-first-request` 的 `evidence[0]` 仍写着 "Covered by TestSideDeveloperInstructionsAppendsExistingPolicyMatchRust (tui/app/side_test.go:52)" —— 该用例已在死副本第 2 步被删除。按"历史结论一字不动"，我只加了 `supersededBy`，没有改这句话；要改的话给我一句授权，我把该句替换成 `TestSideDeveloperInstructionsKeepExistingPolicyLikeRust`（`tui/tea/side_developer_instructions_like_rust_test.go`）。
- `partial_contract_mutation_evidence_2026_11_08.json` 里同一条的 `goImplementation` 仍写 `tui/app/side.go:189-195 SideDeveloperInstructions`（该函数已删，生产实现在 `tui/tea/side.go:476`）——同样只加 `supersededBy`，未改原文。
