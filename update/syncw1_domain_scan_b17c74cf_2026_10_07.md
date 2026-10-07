# syncw1 · round87 · 域内候选扫描（只读）

> 车道：syncw1（Windows 节点，`D:\qax\reagent\dev\codex_go_wt\syncw1c1`）
> 派单：队长 `msg-1791374877567761400-4988`（round87 · 你的 V1 guidance 已入主 sync632 `185d03c9` + 新单只读扫描）
> 纪律：**只读**；**0 补丁 / 0 commit / 0 push / 0 ref 移动**；未改任何 Go 源码，产物仅本文件。
> Rust 上游：`D:\qax\reagent\dev\git\codex`（只 fetch/读 object，未动工作树）；Go 参考点：`origin/main` = **`185d03c9`**（= 队长所述 sync632，`git rev-parse origin/main` 实测）
> 扫描域（本单口径）：`agent/`、`tool/`、`turn/`、`exec/`（含与其直接相邻的库面，超出即标「域外」）

## 0. 复跑命令

```powershell
# ① Go 参考点
git -C D:\qax\reagent\dev\codex_go fetch origin --prune; git -C D:\qax\reagent\dev\codex_go rev-parse origin/main
# ② Rust 新 pin + range（本单：上一 pin a6baf8867c -> 新 pin b17c74cfd5）
git -C D:\qax\reagent\dev\git\codex fetch origin --prune
git -C D:\qax\reagent\dev\git\codex rev-list --count a6baf8867c..b17c74cfd5
git -C D:\qax\reagent\dev\git\codex log --oneline a6baf8867c..b17c74cfd5
# ③ 每条 sha 自解（不采信台账）
git -C D:\qax\reagent\dev\git\codex log --oneline -1 --grep '#51652' -F origin/main   # …同理 #51651/#51650/#51642/#51627
# ④ 规模
git -C D:\qax\reagent\dev\git\codex show --stat --format='%H%n%s' <sha>
# ⑤ Go 载体（全部打在 185d03c9）
git -C D:\qax\reagent\dev\codex_go grep -n --fixed-strings '<token>' 185d03c9 -- '*.go'
```

## 1. range 实测：**5 笔**（`a6baf8867c..b17c74cfd5`）

口径说明：派单写「从新 pin `b17c74cfd5` 起扫描」。实测 `b17c74cfd5` 是 `a6baf8867c` 的**后代**（`git merge-base --is-ancestor a6baf8867c b17c74cfd5` → exit 0），且 `origin/main == b17c74cfd5`，故 range 取 **上一 pin `a6baf8867c` .. 新 pin `b17c74cfd5`**。

```
$ git rev-list --count a6baf8867c..b17c74cfd5
5
$ git log --oneline a6baf8867c..b17c74cfd5
b17c74cfd5 Record telemetry for AGENTS.md changes made by apply_patch (#51652)
30bdfec59d Persist Guardian review failures for reports across restarts (#51651)
37eaae6eeb Require hostname authorization before proxy DNS lookups (#51650)
a513012869 Fix retained context handling for typed section content (#51642)
5b0b253035 Stabilize Guardian snapshot prefixes by separating assistant context (#51627)
```

sha 自解（`git log --grep '#<PR>' -F origin/main`）逐条与上表一致：`#51652`→`b17c74cfd5`、`#51651`→`30bdfec59d`、`#51650`→`37eaae6eeb`、`#51642`→`a513012869`、`#51627`→`5b0b253035`。

## 2. 五分类口径（沿用 `update/syncw4_round87_rescan_2026_10_07.md`）

| 标签 | 判据 | 处置 |
|---|---|---|
| **C1 可落地** | 有 Go 载体，真缺口，≤5 文件 | 报队长后开工 |
| **C2 L** | >5 文件 或 需架构决策（新 schema/新子系统） | 不开工，待裁定 |
| **C3 N/A·无载体** | Go 无该子系统/概念（grep 0 命中） | 剔除 |
| **C4 N/A·机制已承载** | Go 已有等价实现（给 file:line + 测试） | 剔除 |
| **C5 已落地/在飞** | 已入 main 或他车道承接中 | 跳过 |

## 3. 候选表（本域）

| # | Rust sha | 规模 | 五分类 | 级别 | Go 载体（打在 `185d03c9`） | 触及避让面 |
|---|---|---|---|---|---|---|
| **#51652** | `b17c74cfd5` | 1 file, +24/−1 | **C1 可落地（真缺口）** | **S**（含前置半片则 S/M） | `tool/apply_patch_executor.go:215` + `applypatch/applypatch.go:126-144`、`metrics/global.go:43` | **否** |
| #51651 | `30bdfec59d` | **17 files, +442/−74**（含新 migration） | **C2 L** ＋ Go 侧记录器整体缺席（C3 性） | L | 域外（`appserver/`、`state/`、`ext/`）；Go 0 命中 | 否 |
| #51650 | `37eaae6eeb` | 9 files, +357/−35 | **C5（他车道在飞/已裁定）**＋域外 | M/L | `network/network_policy.go`、`sandbox/seatbelt.go`（均域外） | 否（seatbelt ≠ `windowssandbox/`） |
| #51642 | `a513012869` | 2 files, +19/−15 | **C4 已承载 / C5**（前轮已判 N/A） | S | `state/guardian_retained_context.go:11`、`appserver/retained_context.go:34`（域外） | 否 |
| #51627 | `5b0b253035` | 7 files, +343/−46 | **C5 已落地** | — | Go `ab4b9f3e`(sync631) + `e176a441`(syncl3) | 否 |

避让面判定（`git show --stat b17c74cfd5 | Select-String 'runtime_router|turn_runtime|windowssandbox|tui/state'` → **空输出**；其余四笔同法检查，均未触及）。**本批无一笔触及避让面。**

## 4. 唯一域内 C1：`#51652`（`b17c74cfd5`）明细

### 4.1 Rust 语义（原文核对）

`codex-rs/core/src/tools/runtimes/apply_patch.rs`（+24/−1）：apply_patch 提交后，遍历 **committed delta**（`let delta = match … { Ok(delta) => delta, Err(failure) => failure.into_parts().1 }`，即 **含「后续 patch 失败之前已提交」的改动**），对每个 `change`：

```rust
for change in delta.changes() {
    let move_path = match &change.change {
        AppliedPatchFileChange::Update { move_path, .. } => move_path.as_ref(),
        AppliedPatchFileChange::Add { .. } | AppliedPatchFileChange::Delete { .. } => None,
    };
    for path in std::iter::once(&change.path).chain(move_path.filter(|path| *path != &change.path)) {
        if let Some(filename @ ("agents.md" | "agents.override.md")) =
            path.basename().map(|name| name.to_ascii_lowercase()).as_deref()
        {
            ctx.step_context.session_telemetry.counter("codex.agents_md.edit", 1, &[("filename", filename)]);
        }
    }
}
```

要点：① 计数名 `codex.agents_md.edit`；② tag 只有 `filename`（**basename 小写归一**）；③ 命中集合 `{agents.md, agents.override.md}`（大小写不敏感）；④ update 的 move 目标**另计**，但与源路径相同时去重；⑤ 累加 add/delete/update/去重后的 move 目标；⑥ **失败前已提交的改动也计**。

### 4.2 Go 载体（全部实测于 `185d03c9`）

| 要素 | Go 载体 | 说明 |
|---|---|---|
| 执行/提交点 | `tool/apply_patch_executor.go:215` `result, err := action.ApplyVerified(applyOptions)` | 计数落点（紧随其后） |
| committed delta 三要素 | `applypatch/applypatch.go:126-129` `ApplyResult{Updated, Changes}`；`:137-144` `AppliedChange{Kind, Path, MovePath, …}`；`:44-46` `ChangeAdd/ChangeDelete/ChangeUpdate` | `Path`/`MovePath`/`Kind` 与 Rust `change.path`/`move_path`/`AppliedPatchFileChange` 一一对应 |
| **前置缺口** | `applypatch/applypatch.go:335-346 applyCommitted`，其中 **`:339-341` `if err != nil { return nil, err }`** | 出错即丢 `result` ⇒ Go 现今**拿不到**「失败前已提交」的 delta（Rust 返回 `failure.into_parts().1`）。要含该半片需改为 `return result, err`（对现有 3 个调用方 `applypatch/cli.go:35`、`applypatch/applypatch.go:303`、`:471` 均为 err 优先，兼容） |
| 计数 API（**既有、生产已接线**） | `metrics/global.go:43 func Counter(name string, inc int, tags map[string]string)`（nil-safe）；安装点 `otelinit/otelinit.go:85 telemetry.InstallGlobalMetrics(provider.Metrics())` | 同款先例：`rollout/compression_metrics.go:84/92/111`、`rollout/line_reader.go:206`、`model/agent_metrics.go:253/378`、`state/logs.go:82`、`state/log_handler.go:343`、`appserver/previous_model_compact.go:276`、`appserver/plugin_install_runtime.go:113` |
| 缺口证据 | `git grep -c --fixed-strings 'agents_md.edit' 185d03c9 -- '*.go'` → **0 命中（exit 1）** | Go 侧无任何该 counter |
| 相邻字符串 | `config/user_instructions.go:13 LocalAgentsMDFilename = "AGENTS.override.md"`、`prompt/instructions.go:18` | 文件名常量已在别处存在（本项可自带小写归一，不必复用） |

**Rust `session_telemetry.counter` vs Go `metrics.Counter` 的口径说明（避免「结构性等价」式含糊）**：
Rust `SessionTelemetry::counter`（`codex-rs/otel/src/events/session_telemetry.rs:206-219`）= `tags_with_metadata(tags)`（追加 `session_source`/`model`/`app_version` 等，且受 `metrics_use_metadata_tags` 门控）后调 metrics client。Go 的 `telemetry.SessionTelemetry`（`telemetry/session_telemetry.go:66-77`）结构里**没有** metrics client（只有 Metadata/Logs/Tracer/Clock），因此 Go 侧 Rust「library 内发 codex.* counter」的既定通道就是进程全局 `metrics.Counter`（上列 7 处生产先例）。⇒ 本项用 `metrics.Counter("codex.agents_md.edit", 1, {"filename": …})` 与该先例族一致；**不需要**碰 `appserver/runtime_router.go`。

### 4.3 与 syncw4 的**重复认领**（请裁定归属）

`update/syncw4_round87_rescan_2026_10_07.md` 已把 `#51652` 标为本批「唯一可直接开工的新增项」，落点同为 `tool/apply_patch_executor.go:215`。其给出两条 seam：① 给 `ApplyPatchExecutorOptions` 加 counter sink —— 但该字段的生产接线点在 **`appserver/runtime_router.go:14303` `options.ApplyPatch.DecisionSink = r.sessionTelemetryForThread(threadID)`（避让面）**；② 在 appserver 的 tool-result 遥测里消费 `Output.Data`（同样落在 `appserver/`）。
**我发现第三条 seam：进程全局 `metrics.Counter`（`metrics/global.go:43`，生产由 `otelinit/otelinit.go:85` 安装），零避让面改动、零 `appserver/` 改动。** ⇒ 若仍归我，我按此方案出补丁；若归 syncw4，本表仅作方案输入。

### 4.4 若派我开工（建议范围，**尚未动一行代码**）

- 生产：`tool/apply_patch_executor.go`（新增计数；紧随 `:215`）；**可选** `applypatch/applypatch.go:339-341`（`return result, err`，补「失败前已提交」半片）。
- 测试：新增 `tool/apply_patch_agents_md_metrics_like_rust_test.go`（用 `metrics.InstallGlobal(recordingRecorder)`，覆盖：add/delete/update、move 目标另计与同路径去重、大小写不敏感、`AGENTS.override.md`、非 AGENTS 路径不计、**同 patch 后续失败时前面已提交的 AGENTS.md 仍计**）。
- 规模：1–2 生产文件 + 1 测试 ⇒ **S**，≤5 文件。
- 值级 RC（设计）：撤掉计数调用 ⇒ 断言 `codex.agents_md.edit` 计数 0（want ≥1）⇒ FAIL（贴原文）⇒ 恢复 ⇒ ok；文件 sha256 还原。
- 避让面：不触及 `appserver/runtime_router.go` / `appserver/turn_runtime.go` / `tui/state.go` / `sandbox/windowssandbox/`。

## 5. 其余 4 笔的判据（正向陈述）

**#51651 `30bdfec59d`（C2 L ＋ 域外）** — 17 文件 +442/−74，含**新 SQLite migration** `codex-rs/state/migrations/0060_guardian_review_feedback.sql`：把 Guardian failed-review 证据从进程内升级为 SQLite 持久（全局 ≤64 / 每 thread 8 / 8 MiB、级联删除、250 ms 超时＋内存回退），上传 `auto-review-failures.jsonl` 时合并去重。Go 侧实测：`auto-review-failures` / `MAX_GUARDIAN_REVIEW` / `GuardianReviewRecord` / `guardian_review_feedback` 在 `185d03c9` **全部 0 命中（exit 1）**；Go 里唯一近名物是 `appserver/feedback.go:358 FeedbackAutoReviewRolloutFilename` = `auto-review-rollout-<threadID>.jsonl`（**另一件东西**）。⇒ 不是「内存搬 SQLite」的增量，而是记录器＋schema＋上传合并整体缺席；且落点全在 `appserver/`、`state/`、`ext/`（**域外**）。**P.S.** syncw4 在 `r86_worklist_round87` 的同批扫描里对 `:11100 guardianRolloutPath` 恒 `nil` 亦有记录，与本节互补。

**#51650 `37eaae6eeb`（C5 ＋ 域外）** — 9 文件 +357/−35：`network-proxy` 需先过 allowlist/policy 才解析主机名、批准后复检 baseline 限制、`sandboxing/src/seatbelt.rs` 去掉外部 DNS 放行。Go 侧确有同面载体（`network/network_policy.go`、`network/policy_http.go`、`sandbox/seatbelt.go:18/34`），但落点均在 `network/`、`sandbox/`、`execserver/`（**域外**），且 `update/syncl1_51650_3_judgement_2026_10_07.md` + `update/r86_51650_gate_2026_10_07.md` 已在处理 ⇒ 归 **C5**。注意 `sandbox/windowssandbox/`（避让面）**未**被该笔触及。

**#51642 `a513012869`（C4/C5 ＋ 域外）** — 2 文件 +19/−15：`guardian-context/src/retained_instructions.rs` 把 `ContentItem::InputText` 匹配改为 `SectionContent::Other(ContentItem::InputText)`。Go 侧同面载体在 `state/guardian_retained_context.go:11`（自陈 "Rust parity: …/retained_instructions.rs"，且 `:51-52` 已有两个 section 标记、`:205 RetainedInstructionSections` 两段结构）与 `appserver/retained_context.go:33-34`／`appserver/retained_context_test.go:167` ⇒ **C4 已承载**（与 `r86_worklist_round87` 的「前轮已判 N/A」一致）；且 `state/`、`appserver/` 属**域外**。

**#51627 `5b0b253035`（C5 已落地 ＋ 域外）** — 7 文件 +343/−46（拆分 `RETAINED ASSISTANT CONTEXT` 段）。Go 已入主：`ab4b9f3e sync631: render retained snapshot per reviewer framing like Rust (#51627)` 与 `e176a441 syncl3: separate retained assistant context from the instruction prefix (#51627)`（`git log --oneline -3 185d03c9 -- state/guardian_retained_context.go` 原文）⇒ **无残余**。该笔即 `C:\rw` 的 parity frozen 点（`5b0b253035`），故 syncl5 的 round87 range（base 5b0b253035，4 笔）与本表（base a6baf8867c，5 笔）只差这一笔已落地项。

## 6. 未决 / 待队长裁定

1. **`#51652` 归属**：我（syncw1）域内 vs syncw4 已认领；**并请选方案**：(A) 全局 `metrics.Counter`（推荐：零避让面、零 `appserver/` 改动，与 7 处既有先例一致）/(B) `ApplyPatchExecutorOptions` 加 sink（须改 `appserver/runtime_router.go:14303` = 避让面）。
2. **是否含「失败前已提交」半片**：需 `applypatch/applypatch.go:339-341` 一行级改动（`applypatch/` 不在本单域内清单）——请授权我扩到该文件，或裁定只做成功 delta（我会在补丁注释与本文件中显式标注该偏差，不写成「等价」）。
3. 其余 4 笔我判 **域外 / 已落地 / 已承载**，**未接手任何一笔**；若要我破例接 `#51651` 等域外项，请先解除域外限制（我未动一行代码）。
4. 本表 range 口径为「上一 pin → 新 pin」。若你要的其实是「新 pin 之后的新提交」，请指示——当前 `origin/main == b17c74cfd5`，该 range 为空。
