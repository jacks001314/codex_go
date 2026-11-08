# evidence 重写（第一批，12 个 commit）

生成日期：2026-11-08 · lane：ledger-align · 范围：**只改这 12 个 commit 的 `evidence[]`**，`status` 与其余字段零变化

## 0. 结论

- r8 那 12 个 commit 原来的 `evidence[]` 是**改判前的缺口描述**（36 条全是「缺…」/「Go 锚点：…」，共 3 条/commit），现在全部换成 `Go <文件路径> implements <行为>; verified by <测试函数名>` 形状，**共 22 条**，每条都点名一个 Go 路径和一个主干登记的 verifier。
- 计数完全不变：`complete 2975 / not_applicable 639 / partial 59 / blocked_precondition 4`，total 3677、uniqueSha 3677、sha40 3677、`openStatusCount: 0`。
- 文件：2 894 369 B `df4c073b…c4b9fc` → 2 894 577 B `cf6cb6f9…74ccb00`（+208 B）。
- 反向校验：把这 12 块的 evidence 换回 r8 原文 → 与改前文件**逐字节相等**（`df4c073b…c4b9fc`）。

## 1. 12 个 commit 一览

| # | sha10 | PR | contract(s) | evidence 条数 | verified by |
|---|---|---|---|---|---|
| 1 | `01ab354312` | #60004 | `account-read-user-id` | 3 → **2** | `TestAccountReadResponseCarriesChatGPTUserIDLikeRust` |
| 2 | `c9b4267c37` | #60190 | `agent-tree-id` | 3 → **2** | `TestAgentTreeIDChangesOnReloadAndIsSharedByChildrenLikeRust` |
| 3 | `44ccf39457` | #60049 | `child-agent-catalog-preservation` | 3 → **1** | `TestChildAgentInheritsParentCatalogLikeRust` |
| 4 | `ddd1812a51` | #59842 | `final-answer-moderation` | 3 → **2** | `TestFinalAnswerModerationAttachedToCompletedAnswerLikeRust` |
| 5 | `ad1dbba32d` | #59910 | `patch-approval-cancel-turn-scope` | 3 → **2** | `TestFileChangeApprovalCancelInterruptsRequestingTurnLikeRust`；`TestPatchApprovalCancelStaysScopedToRequestingTurnLikeRust` |
| 6 | `efdacaf46f` | #60096 | `runtime-auth-terminal-denials` | 3 → **2** | `TestTerminalRuntimeDenialsSurfaceSignInLikeRust`；`TestRuntimeAuthDenialIsNotRecoverableAuthErrorLikeRust` |
| 7 | `df769889c0` | #60006 | `logs-prune-window-frame` + `thread-token-narrow-update` | 3 → **3** | `TestLogsPruneWindowUsesSinglePassLikeRust`；`TestThreadTokenUpdateKeepsConcurrentMetadataLikeRust`；`TestHistoryWriteAdmission` |
| 8 | `eb485f57ba` | #59998 | `message-board-bulk-subscriptions` | 3 → **1** | `TestLocalBoardBulkSubscriptionsAreAtomicLikeRust` |
| 9 | `e428b9a16b` | #59969 | `thread-writer-lock-shared-coordination` | 3 → **2** | `TestWriterLockCloseDoesNotBlockOnHeldCoordination`；`TestTryAcquireForPublicationSkipsLiveWriterAndHoldsCoordination` |
| 10 | `af9a98b074` | #60039 | `mcp-lazy-stdio-startup` | 3 → **2** | `TestMcpLazyStdioStartupDefersCachedServersLikeRust`；`TestToolCatalogCacheWarmsNewChatsLikeRust` |
| 11 | `b1a0dff158` | #59891 | `guardian-repl-provenance-bound` | 3 → **1** | `TestNodeReplElasticBoundsProvenanceHeadersLikeRust` |
| 12 | `b8725c1896` | #59902 | `guardian-verified-answer-whole` | 3 → **2** | `TestGuardianElasticKeepsVerifiedAnswersWholeLikeRust`；`TestGuardianElasticDropsVerifiedAnswersWholeLikeRust` |

合计：36 条旧 evidence → 22 条新 evidence。

## 2. 样例（前 / 后）

**`ad1dbba32d`（#59910 `patch-approval-cancel-turn-scope`）改前：**

```json
"evidence": [
 "缺「patch 审批取消按发起 turn 收敛」：Go 的取消路径不区分 turn 归属",
 "Go 锚点：appserver/（审批取消处理）、turn/（approval broker）",
 "…"
]
```

**改后：**

```json
"evidence": [
 "Go appserver/server_request.go implements scoping a file change approval cancel to the requesting turn instead of the default turn; verified by TestFileChangeApprovalCancelInterruptsRequestingTurnLikeRust",
 "Go appserver/turn_runtime.go implements interrupting only the requesting turn when a patch approval is cancelled; verified by TestPatchApprovalCancelStaysScopedToRequestingTurnLikeRust"
]
```

**`df769889c0`（#60006）改后**（三个子项各一条，含你刚确认落地的 (b)）：

```json
"evidence": [
 "Go state/logs.go implements pruning each Rust log partition with one ROWS UNBOUNDED PRECEDING window that computes byte and row limits in a single pass; verified by TestLogsPruneWindowUsesSinglePassLikeRust",
 "Go state/backfill.go implements a token-usage activity narrow update that touches only timestamps and token totals while keeping concurrent metadata edits; verified by TestThreadTokenUpdateKeepsConcurrentMetadataLikeRust",
 "Go state/runtime.go implements serializing local history writes through historyWriter/acquireHistoryWriter before BeginImmediate; verified by TestHistoryWriteAdmission"
]
```

全部 12 条的 before/after 原文在 `update/ledger_evidence_rewrite_2026_11_08.json` 的 `records[]`。

## 3. 校验

`python3 update/ledger_verify_2026_11_08.py`（改后）：

```
{
 "rustFromExclusive": "bd19459358f534ed1cae464ec13d56600aeb45f2",
 "rustToInclusive": "748ab55ca7de0b647d9a7a9d9a8e70e00f28666d",
 "total": 3677,
 "uniqueSha": 3677,
 "duplicateSha": [],
 "statusCounts": {
  "blocked_precondition": 4,
  "complete": 2975,
  "not_applicable": 639,
  "partial": 59
 },
 "kindCounts": { "persistence": 208, "protocol": 389, "release": 170, "runtime": 2371, "security": 351, "test": 188 },
 "domainSets": { "codex_go": 3677 },
 "sha40": 3677,
 "prefixOnly10": [],
 "openStatusCount": 0,
 "firstSha": "27c05a52e0cdf93ef7db5a96ccaf88d6b9095472",
 "lastSha": "748ab55ca7de0b647d9a7a9d9a8e70e00f28666d",
 "fieldsConsistent": true,
 "closedWithEvidence": true
}

prefix-only without evidence: []
GATE: PASS
```

`closedWithEvidence` 仍 `true`；与改前相比，`statusCounts` / `total` / `uniqueSha` / `sha40` / `firstSha` / `lastSha` **逐项相同**。

Go 用例（本镜像）：

```
TestAlignmentCommitLedgerIsComplete                        ok
TestAlignmentBaselineIsFrozenAndTraceable                  ok
TestAlignmentDomainsCannotOverclaim                        ok
TestAlignmentCertificationReadinessIsDerivedFromEvidence   ok
TestContractLedgerIsComplete                              ok
TestAlignmentCommitLedgerMatchesRustGitRange              ok（未设 CODEX_RUST_ROOT）
TestContractVerifiersExistInGoTestFiles                   仍是镜像落后的 41 名（与本轮无关，改前同数）
```

## 4. 反向校验与 diff 可 apply 性

```
$ # 把 12 块 evidence 换回 r8 原文
reverting 12 commits
reverted sha256 df4c073b2809bd0439a5c6cf7b798405a053328bc9c5ab638ff9b8942dc4b9fc
r8 sha256        df4c073b2809bd0439a5c6cf7b798405a053328bc9c5ab638ff9b8942dc4b9fc
BYTE-IDENTICAL: True

$ git apply -p1 --check PATCHES/commits_evidence_12.diff   → exit 0
$ patch -p1 --dry-run < PATCHES/commits_evidence_12.diff   → checking file parity/commits.json
$ git apply -p1 PATCHES/commits_evidence_12.diff           → sha256 cf6cb6f9…74ccb00（= 目标态）
```

diff 形状：**12 hunks / 36 − 行 / 22 + 行**，CRLF 原样（`^M$`）。改前 sha `df4c073b…` 就是你已合并的那份，所以能直接 apply。

## 5. 三件需要你确认的事

1. **你给的表格是 12 行、但其中 `image-original-detail-token-estimate` 标「不动」，所以实际只有 11 条可写；而 r8 那批是 12 个 commit / 13 条契约 —— 缺的两行是 `guardian-repl-provenance-bound`（#59891）与 `guardian-verified-answer-whole`（#59902）。** 这两个的 verifier 名我取自你附件 manifest 的 `verifier` 字段（`TestNodeReplElasticBoundsProvenanceHeadersLikeRust` 等），不是从你表格里。如果主干已改过这两个的名字，给我新的我替换。
2. **2 条 evidence 用的是目录级路径**（我镜像里没有对应实现文件，不编文件名）：`af9a98b074` 的第 2 条用 `Go appserver/`（tool 目录缓存预热），`b1a0dff158` 的第 1 条用 `Go context/`（REPL provenance 上界）。你给确切文件名（例如 `appserver/tool_catalog_cache.go`、`context/node_repl_elastic.go`）后我 5 分钟内替换掉。
3. **`df769889c0` 我多加了第 3 条**（(b) history 写准入 + `state/runtime.go` + `TestHistoryWriteAdmission`），因为你说它已落地、并会补契约 `history-write-admission-serialization`。如果你希望 commit 的 evidence 只写「有契约覆盖」的部分，我就把这第 3 条挪到新契约的 verification 里，commit 只留两条。
