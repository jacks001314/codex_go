# A 类 13 条状态差修正（parity/commits.json）

生成日期：2026-11-08 · lane：ledger-align · 授权范围：**只改 A 类 13 条契约对应的 `status` 字段**

## 0. 结论

- A 类 14 条里 **13 条是「行为已落地、台账滞后」**，本轮把对应 commit 的 `status` 从 `partial` 上调为 `complete`；第 14 条 `image-original-detail-token-estimate` 按你的裁决是**关联错位**（权威 commit 是 #54776 `ce0323f2f2`，#59908 `bbf57cec15` 单列 `not_applicable` 是对的），**不在本轮范围，一字节未动**。
- **13 条契约只对应 12 个 commit**：`df769889c0`（#60006）同时承载 `logs-prune-window-frame` 与 `thread-token-narrow-update`，所以物理改动是 **12 行 `status`**，不是 13 行。结构化的 13 条记录在 `update/ledger_status_updates_2026_11_08.json`（一条契约一条记录）。
- 除这 12 行 `status` 外，`commits.json` **其余字节零变化**（反向校验见 §5，还原后与改前逐字节相同）。

## 1. 逐条清单（13 条）

| # | contract | PR | sha10 | commit 主题 | before | after |
|---|---|---|---|---|---|---|
| 1 | `account-read-user-id` | #60004 | `01ab354312` | Expose ChatGPT user IDs in `account/read` responses | partial | complete |
| 2 | `agent-tree-id` | #60190 | `c9b4267c37` | Expose agent tree IDs and environment removal | partial | complete |
| 3 | `child-agent-catalog-preservation` | #60049 | `44ccf39457` | Preserve model catalogs when starting child agents | partial | complete |
| 4 | `final-answer-moderation` | #59842 | `ddd1812a51` | Carry targeted moderation decisions on final-answer messages | partial | complete |
| 5 | `guardian-repl-provenance-bound` | #59891 | `b1a0dff158` | Bound REPL provenance costs in elastic context preparation | partial | complete |
| 6 | `guardian-verified-answer-whole` | #59902 | `b8725c1896` | Keep verified question/answer records whole under elastic budgets | partial | complete |
| 7 | `logs-prune-window-frame` | #60006 | `df769889c0` | Reduce SQLite write overhead（子项 c：日志窗口帧） | partial | complete |
| 8 | `thread-token-narrow-update` | #60006 | `df769889c0` | 同上（子项 a：token 窄更新） | partial | complete |
| 9 | `mcp-lazy-stdio-startup` | #60039 | `af9a98b074` | Add opt-in lazy startup for cached stdio MCP servers | partial | complete |
| 10 | `message-board-bulk-subscriptions` | #59998 | `eb485f57ba` | Add atomic bulk channel subscription updates | partial | complete |
| 11 | `patch-approval-cancel-turn-scope` | #59910 | `ad1dbba32d` | Scope file change approval cancellation to the requesting turn | partial | complete |
| 12 | `runtime-auth-terminal-denials` | #60096 | `efdacaf46f` | Stop retrying terminal runtime authentication denials | partial | complete |
| 13 | `thread-writer-lock-shared-coordination` | #59969 | `e428b9a16b` | Reduce contention in thread writer lock coordination | partial | complete |

完整 40 位 sha、理由、Go 锚点、verifier 名单与「本地是否可见」见 `update/ledger_status_updates_2026_11_08.json` 的 `updates[]`。

## 2. 依据（哪条 verifier 承载）

**重要前提**：我这边的镜像落后主干（`state/**`、`mcp/**`、`session/**` 等 lane 文件不在本地），所以下面这些 verifier **我没有在本地独立复跑**；依据是「契约在主干 manifest 记为 complete」+ 你本轮的合并声明。`presentInMyMirror` 字段如实记录哪些名字在我镜像里能解析到。

| contract | 主干 manifest 登记 verifier | 本地可见性 |
|---|---|---|
| `account-read-user-id` | `TestChatGPTAccountUserIDRequiresMatchingWorkspaceLikeRust`、`TestRuntimeRouterGetAccountWorkspaceRoutingSelectionLikeRust` | 2/2 可见 |
| `agent-tree-id` | 你报的主干用例 `TestAgentTreeIDChangesOnReloadAndIsSharedByChildrenLikeRust`；我这份 manifest 登记的是 `TestManagerRemove`、`TestAgentTreeShutdownReportFormattingIsPayloadFreeLikeRust` | 主干用例本地缺失（镜像落后）；旧登记名 2/2 可见 |
| `child-agent-catalog-preservation` | `TestConfigPanelModelProvidersRefreshesAfterFirstChatLikeRust` | 1/1 可见 |
| `final-answer-moderation` | `TestModelSafetyRecourseDefersBlockedTurnAndExplainsIt` | 1/1 可见 |
| `guardian-repl-provenance-bound` | `TestNodeReplElasticBoundsProvenanceHeadersLikeRust`、`TestNodeReplElasticKeepsOversizedAnswerBodyLikeRust`、`TestNodeReplFixedBudgetStillChargesBodyBytesLikeRust`、`TestNodeReplElasticRenderingKeepsStoredTextImagesAndOmissionsLikeRust` | 1/4 可见 |
| `guardian-verified-answer-whole` | `TestGuardianElasticKeepsVerifiedAnswersWholeLikeRust`、`TestGuardianElasticDropsVerifiedAnswersWholeLikeRust`、`TestGuardianElasticEntryCapsBeforeAllocationLikeRust`、`TestRuntimeRouterRecordsVerifiedAnswersLikeRust` | 2/4 可见 |
| `logs-prune-window-frame` | `TestLogsPruneEachRustPartitionByRowAndByteLimit`、`TestLogsPruneWindowUsesSinglePassLikeRust` | 1/2 可见 |
| `thread-token-narrow-update` | `TestThreadTokenUpdateKeepsConcurrentMetadataLikeRust`、`TestRolloutRebuildTimestampOnlyTouchesTimestampsLikeRust` | 1/2 可见 |
| `mcp-lazy-stdio-startup` | `TestMcpLazyStdioStartupDefersCachedServersLikeRust` 等 7 个 | 2/7 可见 |
| `message-board-bulk-subscriptions` | `TestLocalBoardSubscriptionsOptOutsAndDeletionLikeRust` | 1/1 可见 |
| `patch-approval-cancel-turn-scope` | 你本轮合并的 `TestFileChangeApprovalCancelInterruptsRequestingTurnLikeRust`、`TestPatchApprovalCancelStaysScopedToRequestingTurnLikeRust`；旧登记名 `TestCancelCommandApprovalAbortsTurnLikeRust` | 主干用例本地缺失（镜像落后）；旧名可见 |
| `runtime-auth-terminal-denials` | 旧登记名 `TestManagerCachesForbiddenAndRefreshesUnauthorizedLikeRust`；r5 派单口径的 `TestTerminalRuntimeDenialsSurfaceSignInLikeRust` 在我镜像里不存在 | 旧名可见 |
| `thread-writer-lock-shared-coordination` | `TestWriterLockCloseDoesNotBlockOnHeldCoordination`、`TestTryAcquireForPublicationSkipsLiveWriterAndHoldsCoordination`、`TestWriterLockRejectsCompetingStoreAndTransfersOwnership` | 2/3 可见 |

另外声明：你主干上的 `contract_verifier_audit` 报 `audited 136 contracts: 0 without a verifier, 0 missing verifier names, 0 missing goPaths`，是比我本地更强的依据——我本地那份 135 条 manifest 有 41 个名字解析不到，全是镜像缺文件导致（见 §6.2）。

## 3. 计数对比（全部由 commits.json 现算）

| 指标 | 改前 | 改后 | 差 |
|---|---|---|---|
| total | 3677 | 3677 | 0 |
| uniqueSha | 3677 | 3677 | 0 |
| sha40 | 3677 | 3677 | 0 |
| prefixOnly10 | 0 | 0 | 0 |
| complete | 2963 | **2975** | +12 |
| not_applicable | 639 | 639 | 0 |
| partial | 71 | **59** | −12 |
| blocked_precondition | 4 | 4 | 0 |
| 文件字节数 | 2 894 357 | 2 894 369 | +12 |
| 文件 sha256 | `51987b587ca49fa54c9880fd6988cdc182d484221e6ef430a66e9e9d733a520b` | `df4c073b2809bd0439a5c6cf7b798405a053328bc9c5ab638ff9b8942dc4b9fc` | — |

改前 sha256 与 `deliv` 里那份 `parity/commits.json`（`2026-11-08-ledger-prefix-and-backlog.zip` 内的 `51987b58…a520b`，你已合并的那版）**逐字节相同**，所以下面的 diff 应该能干净 apply。

## 4. `ledger_verify` 原文输出

改前：`GATE: PASS`（2963 / 639 / 71 / 4）。改后：

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
 "kindCounts": {
  "persistence": 208,
  "protocol": 389,
  "release": 170,
  "runtime": 2371,
  "security": 351,
  "test": 188
 },
 "domainSets": {
  "codex_go": 3677
 },
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

命令：`cd <repo> && python3 update/ledger_verify_2026_11_08.py`，退出码 `0`。

## 5. 反向校验（12 处改回 → 逐字节还原）

做法：按 sha 定位每个 commit 的 `status` 行，把 `"complete"` 替换回 `"partial"`（只动这 12 行），再与改前文件做字节比较。

```
reverted sha256 51987b587ca49fa54c9880fd6988cdc182d484221e6ef430a66e9e9d733a520b
before   sha256 51987b587ca49fa54c9880fd6988cdc182d484221e6ef430a66e9e9d733a520b
BYTE-IDENTICAL: True
```

## 6. Go 用例（确认没有把计数断言打红）

### 6.1 账本相关用例逐个跑

```
$ go test ./parity -run '^TestAlignmentCommitLedgerIsComplete$' -count=1                        → ok
$ go test ./parity -run '^TestAlignmentBaselineIsFrozenAndTraceable$' -count=1                   → ok
$ go test ./parity -run '^TestAlignmentDomainsCannotOverclaim$' -count=1                         → ok
$ go test ./parity -run '^TestAlignmentCertificationReadinessIsDerivedFromEvidence$' -count=1    → ok
$ go test ./parity -run '^TestAlignmentCommitLedgerMatchesRustGitRange$' -count=1                → ok（未设 CODEX_RUST_ROOT，SKIP 路径）
$ go test ./parity -run '^TestContractLedgerIsComplete$' -count=1                                → ok
```

结论：**没有任何用例断言具体 complete/partial 计数**；`TestAlignmentCommitLedgerIsComplete` 只断言 3677 条、sha 唯一/40 位、status 枚举合法、首末 sha 边界。`ReadinessIsDerived` 里派生值仍为 `false`（还有 59 partial + 5 domain 未完成），与 `baseline.certificationReady: false` 一致，所以照样绿。

### 6.2 唯一红的用例是镜像落后造成的，改前就红

```
$ go test ./parity -run '^TestContractVerifiersExistInGoTestFiles$' -count=1
    verifier_existence_test.go:31: contract "..." verifier "..." has no matching test function anywhere in the Go tree   （共 41 条）
FAIL
```

改前（用原 `commits.json` 跑同一命令）：**同样 41 条**。即与本次改动无关，原因是本地镜像缺 lane 文件（你主干上 `contract_verifier_audit` 报 0/0/0）。**没有因此停下，因为红不是本次改动引入的**；如果你的口径要求"本地全绿才允许提交"，请告诉我，我会先补镜像再复跑。

## 7. diff 与可 apply 性验证

```
$ git apply -p1 --check PATCHES/commits_status_13.diff        → exit 0（Checking patch parity/commits.json...）
$ patch -p1 --dry-run < PATCHES/commits_status_13.diff        → checking file parity/commits.json
$ git apply -p1 PATCHES/commits_status_13.diff                → 应用后 sha256 = df4c073b…c4b9fc（= 目标状态）
```

diff 形状：`12 hunks / 12 个 -行 / 12 个 +行`，行尾 **CRLF 保持原样**（`cat -A` 可见 `^M$`）。如果 apply 失败，用 `PATCHES/apply_status_updates.py`（按 sha 定位，不依赖行号/上下文）：

```
$ python3 PATCHES/apply_status_updates.py parity/commits.json
applied 12 status updates (13 contracts; df769889c0 covers 2)
sha256=<前> → <后>
```

## 8. `baseline.json` 建议同步（可选，脚本重算，非手写）

`baseline.certificationBlockers` 的文案里**内嵌了** `2963 complete … 71 partial` 和那份 71 个 PR 的清单，改完 commits.json 它就与账本不一致了（`ledger_verify` 和 Go 用例都不会因此变红，但账目自相矛盾）。

我用你指定的 `update/ledger_baseline_2026_11_08.py` 重算，只变 **2 行**（`PATCHES/baseline_counts_sync.diff`）：

- `2963 complete / 71 partial` → `2975 complete / 59 partial`
- 71 个 PR 清单 → 59 个（剔除 `#59842, #59891, #59902, #59910, #59969, #59998, #60004, #60006, #60039, #60049, #60096, #60190`）

```
$ git apply -p1 --check PATCHES/baseline_counts_sync.diff → exit 0
$ git apply -p1 PATCHES/baseline_counts_sync.diff → baseline sha256 = 44a4f6f892709e0a98824389aed594f5872ffe4c82ce572584647d705fb46394
改后再跑 python3 update/ledger_verify_2026_11_08.py → GATE: PASS
改后再跑 go test ./parity -run 'Baseline|ReadinessIsDerived|LedgerIsComplete' → ok
```

**要不要一起合，你定**：只合 commits 的 diff，`ledger_verify` 也照样 PASS，但 baseline 文案会停在 2963/71。我建议一起合。

## 9. 剩余项与风险（4 条，需要你决策）

1. **evidence 文案与 complete 语义相反（必须后续处理）**。你本轮要求"只改 status、其他字段一字节不动"，我照做了；但这 12 条的 `evidence[]` 现在还是改判前的缺口描述，例如：
   - `ddd1812a51`：「缺 final-answer 的定向审核决定与 display metadata…」
   - `ad1dbba32d`：「缺「patch 审批取消按发起 turn 收敛」…」
   - `01ab354312`：「缺 account/read 的 userId…」
   也就是说这 12 条现在是 `status: complete` + `evidence: 缺…`。`ledger_verify` 的 `closedWithEvidence` 只校验非空，所以不报；但这是账本的自相矛盾。
   建议下一轮单独授权一批「evidence 重写」：每条写成「Go 文件路径 + 证明它的测试函数名」。改名后这些 verifier 就能被 `contract_verifier_audit` 和后续 verifier 复核直接吃。**需要你给我主干上的确切测试函数名**（我镜像里 5 个还看不到），否则我只能写你口述的名字。
2. **`df769889c0`（#60006）的三个子项里，第 2 项没有契约**。Rust 提交原文的三项是：(a) token 窄更新 → 契约 `thread-token-narrow-update`；(b) 本地 history 写在拿事务连接前先序列化（让读能用剩余连接）；(c) 日志 byte/row 限额单趟计算 → 契约 `logs-prune-window-frame`。**(b) 在 manifest 里没有任何契约覆盖**，我镜像里 `rg -i "historyWrite|history write|serializ" state/ rollout/` 也是零命中（但 `state/**` 在我这儿是旧版，判不了）。commit 级 status 无法只标子项，所以这次 `complete` 等于同时断言 (b) 成立。**请确认主干是否落了 (b)**：落了 → 建议补一条契约（如 `history-write-admission-serialization`）；没落 → 这条应回退 partial，或拆子项。
3. **A 类第 14 条的口径要在报告 v2 改掉**：`image-original-detail-token-estimate` 的关联 commit 应为 #54776 `ce0323f2f2`，`#59908 bbf57cec15` 与它无映射关系。我下一版 `plan_2026_11_08_status_reconciliation.md` 会改；本轮没动报告。
4. **我这份 manifest 仍落后**（135 条 / 98 complete / 37 partial，你是 136 / 104 / 32），所以 §2 的 verifier 名单里有 7 条契约在我这儿还是 partial、5 个关键 verifier 名看不到。判断上我按你的主干口径执行，但下次给我派活时最好连 manifest 一起同步，能省掉这种"口径靠口述"的环节。
