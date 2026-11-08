# 状态文档索引

> 这是索引，不是口径。最后核对：2026-11-08（Windows lane）。
> 读契约状态请看 `parity/contracts/manifest.json`；`update/` 下所有数字都是写作当天的快照，包括本文第 2 节。

## 1. 权威来源

| 文件 | 管什么 | 什么时候读它 |
| --- | --- | --- |
| `parity/contracts/manifest.json` | 契约清单：`id` / `status` / `verifier` / `rustPaths` / `goPaths` | 判断某条契约当前是什么状态、由哪个用例守 |
| `parity/commits.json` | 上游提交面（`rustFromExclusive` → `rustToInclusive`） | 判断账本追到哪一条上游提交 |
| `parity/baseline.json` | 当前对齐目标（Rust target、平台矩阵、认证阈值） | 判断"对的是哪个 pin" |
| `parity.json` | **最后一次全量认证**的 Rust/Go SDK 基线快照 | 判断上次认证的时间与内容；**不是**当前增量状态 |
| `parity/domains.json` | 按行为域的实现状态与验收口径 | 判断某个域是否闭环 |

## 2. 当前计数（2026-11-08，账本记录）

| 项 | 值 |
| --- | --- |
| 契约总数 | 138 |
| `complete` | 137 |
| `partial` | 1 |

本表只是索引：与 `manifest.json` 不一致时**以 manifest 为准**，并顺手改这里。

> 2026-11-08 状态变更：`cloud-image-messages-and-draft-handoff` → `cloud-image-messages`（改名，砍掉 draft 那半：Rust `cloud-client/src` 里 `draft` 零命中，本 pin 无对应物）→ **complete**；故 partial 由 5 条降为 4 条。

> 同日再两条转 complete：`sidebar-chooser-list-bindings-and-hover`（条带点击的 y 原点已从常量 0 改为渲染回填的 `sidebarTabStripTop`，verifier 增补 `TestSidebarTabStripMouseHitFollowsRenderedOriginLikeRust`）、`release-prerelease-cleanup`（与 Rust 同等证据强度：Rust 自己的 `test_releases.py` 也是 `gh` 替身级证据；已落 tag 守卫 exit 5 与稳定 draft 差异用例）→ 136 / 2；随后 `typed-prompt-markdown-render` 补齐三处可见差异（prompt 底色在内层 `\x1b[0m` 后重注入、markdown 行按视口换行、修掉双 gutter `› › `）→ **137 / 1**。

### 2.1 一条 partial

| 契约 | 归属 | 现状 |
| --- | --- | --- |
| `mcp-network-policy-derivation` | ledger residual | proxy `ProxySettings` 无活句柄、execpolicy 无 clone 语义；判定为结构性 residual，不再追 |

### 2.2 不进 manifest、也不算欠账的结构性 residual

| 项 | 口径 |
| --- | --- |
| `guardian-v3-evidence-retrieval` | 上游 `ext/guardian-v3` 的 selection / retrieval / delivery 在本 pin 只有 crate API 与集成测试，宿主（core / app-server）未接线；Go 侧无对应物。双方都不可达，等上游接线。 |

## 3. `update/` 里哪些该看

### 3.1 当前（可引用）

| 文档 | 作用 |
| --- | --- |
| `README_status_index.md` | 本文件：索引与计数口径 |
| `plan_2026_11_08_partial_closeout.md` | 2026-11-08 partial 收口改动集（**文件中计数已过期**，见第 4 节） |
| `partial_contract_verdicts_2026_11_08.json` | 逐条 partial 的判定与红行原文（`source` 计数已于 2026-11-08 同步到 136 / 2；判定与红行仍是写作当天原文） |
| `partial_contract_mutation_evidence_2026_11_08.json` | 变异证据：把实现改回旧行为后的红行（同上，`source` 已同步） |

### 3.2 程序性参考（日期旧，方法仍适用）

| 文档 | 作用 | 备注 |
| --- | --- | --- |
| `leader_runbook_2026_10_07.md` | lane 协作、门禁、交付流程 | 2026-10-07 版，命令与路径可能已变 |
| `flake_ledger.jsonl` | 已知 flaky 用例清单 | 追加式，按需读 |

### 3.3 历史（只读，**不要引用其中的数字**）

| 范围 | 文件数 | 说明 |
| --- | --- | --- |
| `plan_20260722.md` … `plan_2026_11_08_win.md` | 398 | 每轮工作记录；里面的"契约 N 条 / partial M 条"一律是当天快照 |
| `alignment_audit_2026_10_17.md` | 1 | 2026-10-17 全量审计 |
| `coverage_audit_2026_10_07.md` | 1 | 2026-10-07 覆盖率审计 |
| `goc_cleanup_2026_10_07.md` | 1 | 2026-10-07 清理清单 |
| `handoff_2026_10_07.md` | 1 | 2026-10-07 交接说明 |
| `leader_corrections_2026_10_07.md` | 1 | 2026-10-07 纠偏记录 |

## 4. 已知失效口径

| 文档 | 旧口径 | 现口径 |
| --- | --- | --- |
| `plan_2026_11_08_partial_closeout.md` | partial 7 条 | 1 条（第 2 节） |
| `partial_contract_verdicts_2026_11_08.json` | 判定集 20 条 | 第 2 节 |
| `mutation_evidence_2026_11_08.json` | 随上一条 | 第 2 节 |

校正原则：

1. **只改计数与状态**；历史结论（谁判成什么、红行原文）一字不动。
2. 用例改名后，历史 JSON 保留旧名，加一个 `supersededBy` 指向新名，不删除旧条目：

```json
{
  "verifier": "TestSideDeveloperInstructionsAppendsExistingPolicyMatchRust",
  "supersededBy": "TestSideDeveloperInstructionsKeepExistingPolicyLikeRust"
}
```

3. 判定从 `partial` 转 `complete` 时，保留原判定依据，另加一条带日期的状态变更记录。

## 5. 维护规则

1. 新轮次不改旧文件，新建 `plan_YYYY_MM_DD_<lane>.md`。
2. 计数只在本文件第 2 节与 `manifest.json` 之间同步；历史文件里的数字不动（第 4 节那三份例外，它们是"当前"文档）。
3. 引用契约一律写 `id`，不写序号（序号会变）。
4. 文档里出现"当前 N 条"必须带日期，否则按历史处理。
