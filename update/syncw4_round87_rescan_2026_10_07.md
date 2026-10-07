# syncw4 · round86 续 · lane worktree 清账 + 上游再扫（只读）

> 派单：队长 `msg-1791373117857725900-4886`（round86 · `#49714` 已落主 sync629 + 下一步）
> 纪律：**只读**；0 commit / 0 push / 0 ref 移动；未改任何 Go 源码，产物只写 `update/`。
> Go 参考点：`origin/main` = **`658dc9c1e0d25d3dedaa8f95f81b1775b1a710f5`**（`git -C D:\qax\reagent\dev\codex_go rev-parse main` 实测；`main` == 队长所述 sync629）
> Rust：`origin/main` = **`b17c74cfd5ebb39fe70ffaff78de198120278636`**（`git -C <rust> fetch origin main` 实测，见 §2.1）
> Rust 仓用 `D:\qax\reagent\dev\git\codex`（同 `C:\rw` 的 `--git-common-dir`，仅取 ref／读 commit，不动任何工作树）。

## 0. 复跑命令

```powershell
# ① Go 参考点
git -C D:\qax\reagent\dev\codex_go rev-parse main

# ② Rust 取新 commit（父目录/共享 .git 皆可，只读）
git -C D:\qax\reagent\dev\git\codex fetch origin main
git -C D:\qax\reagent\dev\git\codex log --oneline --no-decorate 5b0b253035..origin/main
git -C D:\qax\reagent\dev\git\codex rev-list --count 5b0b253035..origin/main

# ③ 每笔规模
git -C D:\qax\reagent\dev\git\codex show --stat --format='%H%n%s' <sha>

# ④ Go 侧载体 grep（全部打在 658dc9c1）
git -C D:\qax\reagent\dev\codex_go grep -c --fixed-strings '<token>' -- '*.go'

# ⑤ worktree 清账判据：逐个 dirty 文件「worktree 内容 == main 内容」
git -C <wt> diff --name-only main -- <file>     # 空 = 已入主
```

## 1. lane worktree 清账（派单第 1 项）

判据：对每个 worktree 的 **全部 dirty 条目**逐个跑 `git -C <wt> diff --name-only main -- <file>`，**空输出 ⇒ 该改动已在 main**（`main` 已含我 6 笔入主提交）。

| worktree | HEAD | dirty | 结论（一行） |
|---|---|---|---|
| `syncw4` | `6269b849` | 30 | **30/30 全部已入主（sync603–607），可丢**；唯一未跟踪项 `update/r86_patches/syncw4_49782.patch`（本地暂存，镜像已有同一份） |
| `syncw4c` | `b9ad41d3` | 2 | **2/2 全部已入主（sync621 `91260ed7` #49308），可丢** |
| `syncw4b`（顺带） | `ed6f52b7` | 30 | **29/30 已入主**；仅剩 `chatgptapi/cloud_tasks_normalize_like_rust_test.go`（#49147 纯测试件，队长已裁定**不收录**，实为重复覆盖）⇒ **可丢** |
| `syncw4d`（顺带） | `9cdcd43e` | 5 | **5/5 已入主（sync629 `658dc9c1` #49714），可丢** |
| `syncw4d_base` / `check9fa`（顺带） | `9cdcd43e` / `b9ad41d3` | 0 | 一次性「基点 / 复现」worktree（`apply --check` 用），**可丢** |

实测输出：

```
===== syncw4 =====  dirty_entries=30  still_differs_from_main=0
===== syncw4c ===== dirty_entries=2   still_differs_from_main=0
===== syncw4b ===== dirty_entries=30  still_differs_from_main=1   PENDING: chatgptapi/cloud_tasks_normalize_like_rust_test.go
===== syncw4d ===== dirty_entries=5   still_differs_from_main=0
```

我这 6 笔的入主落点（`git -C D:\qax\reagent\dev\codex_go log --oneline main | Select-String '#<PR>'` 原文）：

```
658dc9c1 sync629: decouple API-key cyber access programs from model discovery like Rust (#49714)
91260ed7 sync621: pin the sandbox console mode like Rust (#49308)
5078de6d sync610: cover cloud base URL normalization like Rust (#49147)        <- syncl6 版（非我）
589703d8 sync607: clean up failed shell snapshot capture process groups (#49782)
dc340eb5 sync606: show model and reasoning effort in task details (#50727)
eeca361a sync605: render hook system messages with ANSI styles (#50359)
106ce625 sync604: centralize TUI subscription labels (#49079)
9cf365ac sync603: support CLI uninstall of the legacy Windows sandbox (#50437)
```

另：`chatgptapi/cloud_tasks_normalize_like_rust_test.go` 在 main 上 **不存在**（`git ls-tree main -- <该路径>` 空输出，exit 0）⇒ 确认是我那份作废的差分测试，无遗留价值。

**结论：`syncw4` 与 `syncw4c` 均可丢，无 pending。**（顺带：`syncw4b` 亦无 pending；`syncw4d*`/`check9fa` 亦可丢。**是否 `git worktree remove` 由队长决定，我不自行删除。**）

## 2. 上游再扫（派单第 2 项）

### 2.1 range 实测：不是 2 笔，是 **4 笔**

```
$ git -C D:\qax\reagent\dev\git\codex fetch origin main
   37eaae6eeb..b17c74cfd5  main       -> origin/main
$ git -C D:\qax\reagent\dev\git\codex log --oneline --no-decorate 5b0b253035..origin/main
b17c74cfd5 Record telemetry for AGENTS.md changes made by apply_patch (#51652)
30bdfec59d Persist Guardian review failures for reports across restarts (#51651)
37eaae6eeb Require hostname authorization before proxy DNS lookups (#51650)
a513012869 Fix retained context handling for typed section content (#51642)
$ git rev-list --count 5b0b253035..origin/main
4
```

⚠️ **diff vs 派单**：派单写「当前应仅 2 笔：`a513012869` #51642、`37eaae6eeb` #51650」。实测 `origin/main` 已从 `37eaae6eeb` 前进到 **`b17c74cfd5`**，**多出 2 笔**：`30bdfec59d` **#51651**、`b17c74cfd5` **#51652**（`fetch` 的 FF 行 `37eaae6eeb..b17c74cfd5` 即证据）。下面 4 笔全查，**新出现的 2 笔（#51651 / #51652）**按派单要求重点给。

### 2.2 五分类口径（本单采用，若与队长台账不同请点名）

| 标签 | 含义 | 处置 |
|---|---|---|
| **C1 可落地** | 有 Go 载体，规模 S/M（**≤5 文件**） | 可派单开工 |
| **C2 L** | **>5 文件** 或需架构决策（新 schema／新子系统） | **不开工**，待队长裁定 |
| **C3 N/A·无载体** | Go 无该子系统／概念（grep 0 命中） | 剔除 |
| **C4 N/A·机制已承载** | Go 已有等价实现（给 file:line + 测试） | 剔除 |
| **C5 已落地 / 在飞** | 已入 main 或他车道承接中 | 跳过 |

### 2.3 逐笔：Windows 域载体预检 + 标签

#### ★ `#51652` `b17c74cfd5` — **C1 可落地（S：1 生产文件 + 1 测试）**
- **上游**（1 文件 +24/−1）：`codex-rs/core/src/tools/runtimes/apply_patch.rs`。语义：apply_patch 提交后遍历 **committed delta** 的每个 change（含「后续 patch 失败之前已提交」的路径），对 `basename().to_ascii_lowercase() ∈ {"agents.md","agents.override.md"}` 的路径 `session_telemetry.counter("codex.agents_md.edit", 1, &[("filename", …)])`（move 目标另计，去重同路径）。
- **Go 载体（file:line）**：`tool/apply_patch_executor.go:215` `result, err := action.ApplyVerified(applyOptions)` → `result *applypatch.ApplyResult`；`applypatch/applypatch.go:126-141` `ApplyResult{Updated []AppliedFile; Changes []AppliedChange}`、`AppliedChange{Kind ChangeKind; Path string; MovePath string; …}` —— **「committed delta + Path + MovePath」三要素齐备**，与 Rust `delta.changes()` 一一对应。
- **计数 API 已有**：`telemetry/metrics_client.go:263 func (c *MetricsClient) Counter(name string, inc int, tags map[string]string)`；`telemetry/turn_metrics.go:30-39 TurnMetricSink` 接口含 `Counter`。**Go 无该 counter**：`git grep -c --fixed-strings 'agents_md.edit' -- '*.go'` → **0 命中**（exit 1）。
- **唯一 seam（S 级）**：`tool/` 目前只有 tag 通道（`tool/registry.go:214 TelemetryTagProvider`），**无 metrics sink**。落点二选一：① 给 `ApplyPatchExecutorOptions`（`tool/apply_patch_executor.go:20-36`）加一个 counter sink（与既有 `DecisionSink` 同风格，最贴近 Rust「committed delta」语义）；② 在 appserver 的 tool-result telemetry（`appserver/turn_metrics.go:44 emitToolCallMetrics` 的既有模式）里消费 `Output.Data`。**①更忠实**。
- **Windows 相关性**：无平台特定（纯遥测），**Windows 节点可作业**。
- **建议**：本条是本批**唯一可直接开工**的新增项。

#### ★ `#51651` `30bdfec59d` — **C2 L（不开工）**，且 Go 连**前置**的 process-local 记录器都没有
- **上游**（**17 文件 +442/−74**，含**新 SQLite migration** `codex-rs/state/migrations/0060_guardian_review_feedback.sql`）：把 Guardian failed-review 证据从 **process-local** 升级为 **SQLite 持久**（全局 ≤64 条 / 每 thread 8 条 / 8 MiB；thread 删除级联删除；250 ms 写超时 + 内存回退），并在上传 `auto-review-failures.jsonl` 时把「持久 + 内存」两条来源**合并去重**。
- **Go 载体预检**：
  - `git grep -n -i --fixed-strings 'auto-review-failures' -- '*.go'` → **0 命中**
  - `git grep -n --fixed-strings 'MAX_GUARDIAN_REVIEW' -- '*.go'` → **0 命中**
  - `git grep -n --fixed-strings 'GuardianReviewRecord' -- '*.go'` → **0 命中**
  - Go 有 guardian 本体与反馈上传链路（`appserver/guardian_reviewer.go`、`appserver/guardian_review_metrics.go`、`state/guardian*.go`、`appserver/feedback.go`），**但**唯一涉及 guardian 附件名的地方是 `appserver/feedback.go:358 FeedbackAutoReviewRolloutFilename` = `auto-review-rollout-<threadID>.jsonl`（**与 Rust 的 `auto-review-failures.jsonl` 不是同一件东西**），且
    `appserver/runtime_router.go:11100 FeedbackAttachmentPaths(rolloutPaths, nil, threadID, nil, params.ExtraLogFiles)` —— **第 2 个参数 `guardianRolloutPath` 在生产接线里恒为 `nil`**；`git grep -n 'guardianRolloutPath\|GuardianRolloutPath' -- '*.go'` 只命中 `appserver/feedback.go:387/400` 的**形参自身**，**无生产者**。
  ⇒ **Go 侧不存在任何 failed-review 记录器（进程内或持久），也没有上传该附件的接线**。这不是「把内存搬到 SQLite」的增量移植，而是要新造记录器 + schema + 上传合并。
- **Windows 相关性**：无平台特定。
- **标签：C2 L**（>5 文件 + 新 DB schema + 需架构决策）⇒ 按车道规则（L 不开工）报队长裁定。**并附正向陈述**：Go 缺口不止 #51651 的「持久化」，而是**整个 guardian failed-review 记录器缺席**（决定性证据：上列 4 条 0 命中 + `runtime_router.go:11100` 的 `nil` 实参）。

#### `#51642` `a513012869` — **C4 N/A·机制已承载**（队长已判 N/A，本单补证据）
- **上游**（2 文件 +19/−15）：`guardian-context/src/retained_instructions.rs`，把
  `matches!(&item.content, ContentItem::InputText{..})` → `matches!(&item.content, SectionContent::Other(ContentItem::InputText{..}))`，
  并把 assistant 框标 `assistant_start/assistant_end` 经 `.into()` 转成 `SectionContent`。
  即：**修「内容实际被包在 `SectionContent::Other` 里、matcher 恒假」的类型包装 bug**（恒假 ⇒ 拆分/空 assistant-context 检测失效）。
- **Go 载体预检**：
  - `git grep -c --fixed-strings 'SectionContent' -- '*.go'` → **0 命中**（`SectionContent::Other` 亦 0）⇒ Go **没有这层包装**。
  - Go 的 fragment 模型是**扁平字符串**：`state/guardian_retained_context.go:65-69 type RetainedInstructionFragment{ Content string; Required bool; Source *retainedctx.RetainedSource }` ⇒ 「包装层导致 matcher 恒假」这一 bug **在 Go 不存在**。
  - Go 的拆分 / 空 assistant-context 检测**已在 #51627 落地**：`state/guardian_retained_context.go:288-298 HasSplitAssistantOmission`、`:302-310 splitAssistantOmission`、`:385` 消费点；测试 `state/guardian_retained_context_test.go:687 TestHasSplitAssistantOmissionLikeRust`、`:726 TestRetainNewRetainedInstructionsKeepsSplitAssistantOmissionLikeRust`（文件内注释即标 `(#51627)`）。
- **标签：C4 N/A·机制已承载**（正向陈述：Go 无 `SectionContent` 包装层，故 #51642 所修的类型包装 bug 不可表达；其行为面已由 #51627 的 Go 端口 + 两条测试承载）。

#### `#51650` `37eaae6eeb` — **C2 L + C5 在飞**（syncl4 承接 (1)(2)(4)；(3) 已判 C4）
- **上游**（**9 文件 +357/−35**）：`network-proxy/`（host_policy_tests / mitm / mitm_tests / network_policy / proxy / runtime）+ `sandboxing/src/seatbelt.rs`（+ `seatbelt_network_tests.rs` / `seatbelt_tests.rs`）。四件事：**(1)** hostname 解析前先要 allowlist/策略批准；**(2)** 本地/远端批准后**重查** baseline 限制；**(3)** 已批准 CONNECT 的**内层** HTTPS 请求仍跑 DNS 检查（含无持久 allowlist 条目主机）；**(4)** 去掉 Seatbelt 的外部 DNS 允许（保留 local binding + loopback 代理）。
- **已有证据**：`update/syncl1_51650_3_judgement_2026_10_07.md` 已对 **(3)** 出独立判定 = **Go 已等价**（`network/proxy_server.go` 内层 MITM 重走 `evaluateProxyPolicy` → `proxyHostResolvesToNonPublicIP:1749/1755`，探针 A/B/C 实测 403 + `not_allowed_local`），并给 syncl4 留下「实现 (1)(2) 时**必须保留**内层 DNS 门」的提示；**(4)** 是 darwin/Seatbelt 面。
- **Go 载体**：`network/`（proxy）已存在；`host_policy` 在 Go **仅 2 处非业务命中**（`mcp/read_only_tools_test.go:71`、`parity/...manifest_test.go:535`）。
- **Windows 相关性**：**(4) 是 macOS-only**；**(1)(2)(3) 平台无关**但属 `network/` + syncl4 面。
- **标签：C2 L（9 文件）；状态 C5 在飞（syncl4 承接 (1)(2)(4)；(3) 经 syncl1 判 C4）** ⇒ 本车道不开工。

## 3. 结论 / 建议

1. **清账**：`syncw4`（30 项）与 `syncw4c`（2 项）**均 0 pending，全部已入主，可丢**（顺带 `syncw4b` 亦 0 pending、`syncw4d`/`syncw4d_base`/`check9fa` 可丢）。删除动作**等队长指令**，我不自行 `worktree remove`。
2. **再扫 diff**：`5b0b253035..origin/main` 实为 **4 笔**（非派单所写 2 笔）——新增 **#51651**、**#51652**。
3. **唯一可派单的新增项 = `#51652`**（C1，S：1 生产文件 + 1 测试；载体 `tool/apply_patch_executor.go:215` + `applypatch/applypatch.go:126-141`，缺的只是一个 counter sink seam）。**建议派给我**（Windows 节点可作业，与在飞写集不相交）。
4. **`#51651` = C2 L 且 Go 缺整个记录器**，建议归 **Guardian retained-context / feedback 族 cluster**（与 #49796 同族），本轮不开工。
5. **`#51642` = C4（已承载）**、**`#51650` = C2 L + 在飞(syncl4)**，本车道不动作。
6. **未决**：本单 §2.2 的「五分类」是我按车道规则自定的口径（C1 可落地 / C2 L / C3 无载体 / C4 已承载 / C5 已落地·在飞）；若队长台账的 5 类定义不同，请点名，我按你的口径重贴标签（不需重跑 grep）。
