# syncl5 · r88d 台账增量报告（2026-10-07 · Linux 节点 · 只读）

> 上游枚举点：Rust `/home/jacks/jacks_dev/codex` @ `origin/main = d83bb540ec`（pin 未变）。
> Go 参考点：`origin/main = d12d00dde5732a8f910db636b5452949a4594785`（= `integ86g`，round88 末）。
> 纪律：**0 commit / 0 push / 0 ref 移动**；未 `project_delete`；本报告 + 更新后的 `r86_landing_ledger_2026_10_07.md` 为仅有的两项产物。

---

## ① 七笔落主增量（sync642 → sync648）

追加进 `r86_landing_ledger_2026_10_07.md` 的新表「round88 落主增量」。列：**Go sha / Rust 上游 sha / PR / 文件数 / 主题 / RC 结论**。

| sync | Go sha | Rust 上游 sha | PR | 文件数 | 主题 | RC 结论 |
|---|---|---|---|---|---|---|
| sync642 | `fb24b81e` | `b6903c0669` | `#50431` | 2 | keep terminal hyperlinks in the command-center preview | 队长自跑：退回 `stripANSISGR` ⇒ `hyperlink_preview_like_rust_test.go:19` FAIL（恢复 sha256 `cfc9e2e1…` ok） |
| sync643 | `6315c3a7` | —（对齐 `models.json` / `protocol/src/openai_models.rs:438/441` serde-default） | — | 2 | keep the fallback catalog's serde-default booleans | 队长自跑：删 `applyBundledCatalogBooleans` 调用 ⇒ `TestBundledFallbackCarriesCatalogJSONBooleans` FAIL（恢复 sha256 `302e694a…` ok） |
| sync644 | `c9c6a200` | `b17c74cfd5`（本体 #51652） | `#51652 fix` | 2 | split AGENTS.md metric paths incl. Windows-native | 队长自跑：回退 `path.Base` ⇒ `TestApplyPatchAgentsMdEditMetricWindowsNativePathLikeRust` FAIL（恢复 sha256 `37b9982a…` ok） |
| sync645 | `5bddd813` | `95dafbc7b5`（#18190；`side.rs:428/433`+`549-558`；replay-only 细化 `ec0cfa5da8` #49800） | — | 2 | interrupt a side thread's turn before closing it | 队长自跑：阉割 interrupt ⇒ `TestInteractiveRemoteCloseSideInterruptsRunningSideTurnLikeRust` FAIL（恢复 sha256 `984b8181…` ok） |
| sync646 | `97107c7b` | `7f9832d0d0` | `#39935` | 2 | bind the MCP OAuth authorization endpoint to its issuer | 队长自跑：中和 `validateMCPOAuthAuthorizationServerEndpoints` ⇒ 跨源/lookalike FAIL（恢复 sha256 `9caa9cbf…` ok） |
| sync647 | `3140c46a` | `d6c4c6aea4` | `#47565` | 3 | classify rollout read failures by reason and progress | 测试载体 `rollout/line_reader_47565_test.go`（body 未附值级 RC 原文） |
| sync648 | `d12d00dd` | —（对齐 `models.json` @ pin `b17c74cfd5`） | — | 2 | align fallback catalog field values with models.json | 测试载体 `TestFallbackCatalogCarriesModelsJSONFieldValues`（body 未附值级 RC 原文） |

Rust sha 解析方式（自解，不用台账 sha 列）：
```
$ git -C /home/jacks/jacks_dev/codex log --oneline --grep '#50431' -F --format='%h %s' origin/main
b6903c0669 Preserve terminal hyperlinks in agents overview previews (#50431)
$ ... --grep '#51652' ...   → b17c74cfd5 Record telemetry for AGENTS.md changes made by apply_patch (#51652)
$ ... --grep '#39935' ...   → 7f9832d0d0 Enforce issuer binding for MCP OAuth endpoints (#39935)
$ ... --grep '#47565' ...   → d6c4c6aea4 Classify rollout read failures by reason and progress (#47565)
# sync645：body 未给 PR 号，按 side.rs 行号 blame 定位
$ git -C /home/jacks/jacks_dev/codex blame -L 428,433 -L 549,558 --date=short codex-rs/tui/src/app/side.rs
95dafbc7b5b (Eric Traut 2026-04-19 428)  pub(super) async fn discard_side_thread( … 433) self.interrupt_side_thread(…)
95dafbc7b5b (Eric Traut 2026-04-19 549)  async fn interrupt_side_thread( … 556) app_server.turn_interrupt(thread_id, turn_id)
# ⇒ 归属 95dafbc7b5 = #18190「Add /side conversations」；line 560 的 replay-only 细化来自 ec0cfa5da8 = #49800
# sync643 / sync648：body 显式对齐 codex-rs/models-manager/models.json 与 protocol/src/openai_models.rs，无单一 PR
```

## ② 一致性核对（原文证据）

### ②-1 文件数逐笔核（`git show --stat <sha>` 尾行 vs 台账）
```
$ for s in fb24b81e 6315c3a7 c9c6a200 5bddd813 97107c7b 3140c46a d12d00dd; do git -C /home/jacks/jacks_dev/codex_go show --stat --format='' $s | tail -1; done
 2 files changed, 103 insertions(+), 2 deletions(-)      # fb24b81e
 2 files changed, 91 insertions(+), 1 deletions(-)       # 6315c3a7
 2 files changed, 130 insertions(+), 5 deletions(-)      # c9c6a200
 2 files changed, 200 insertions(+), 2 deletions(-)      # 5bddd813
 2 files changed, 450 insertions(+)                      # 97107c7b
 3 files changed, 372 insertions(+), 11 deletions(-)     # 3140c46a
 2 files changed, 65 insertions(+), 7 deletions(-)       # d12d00dd
```
⇒ 与台账「文件数」列逐笔一致（2/2/2/2/2/3/2）。**无不一致**。

### ②-2 union == range
```
$ git -C /home/jacks/jacks_dev/codex_go log --format='%h' 8b2453a9..d12d00dd | sort
3140c46a
5bddd813
6315c3a7
97107c7b
c9c6a200
d12d00dd
fb24b81e
$ printf 'fb24b81e\n6315c3a7\nc9c6a200\n5bddd813\n97107c7b\n3140c46a\nd12d00dd\n' | sort
# diff 两者 → 空 ⇒ UNION == RANGE ✓
```
⇒ 区间 `8b2453a9..d12d00dd`（上一台账 tip → 现行 main）**恰为**本表 7 笔，无遗漏、无多余。

### ②-3 每笔 sha 在 `origin/main` 历史内
```
$ for s in fb24b81e 6315c3a7 c9c6a200 5bddd813 97107c7b 3140c46a d12d00dd; do git -C codex_go merge-base --is-ancestor $s origin/main; echo "$s -> $?"; done
fb24b81e -> 0
6315c3a7 -> 0
c9c6a200 -> 0
5bddd813 -> 0
97107c7b -> 0
3140c46a -> 0
d12d00dd -> 0
```
⇒ 7/7 均为 `origin/main` 祖先。

### ②-4 全台账 sha 的祖先性检查（防历史断链）
```
$ grep -oE '`[0-9a-f]{7,40}`' r86_landing_ledger_2026_10_07.md | tr -d '`' | grep -vE '^[0-9]{7,40}$' | sort -u   # 82 个
$ while read s; do git -C codex_go merge-base --is-ancestor "$s" origin/main || echo "NOT-ANCESTOR: $s"; done
NOT-ANCESTOR: 5b0b253035
NOT-ANCESTOR: b17c74cfd5
NOT-ANCESTOR: d83bb540ec
```
⇒ **79 个 Go 提交全部为 `origin/main` 祖先 ✓**；3 个例外是 **Rust 枚举 pin**（`5b0b253035` / `b17c74cfd5` / `d83bb540ec`，出现在「上游枚举 pin」节），**本就不属 Go 提交**，非不一致。

### ②-5 不一致清单
**无。**

## ③ 台账改动点

1. 头部「核验」行：`c9c6a200…` → **`d12d00dd…`**（round88d 实测）。
2. 头部「范围」行：追加 **round88 增量段（sync642 → sync648，7 笔）**。
3. 主表顶部追加 4 行（sync645/646/647/648；sync642/643/644 原已在表内）。
4. 新增节 **「round88 落主增量（sync642 → sync648，7 笔）」**（6 列 + sync）。
5. `## round87c 决议与 backlog` 末追加 **「round88 决议增量与状态刷新」**：新增 `#51465` / `#51221` / `code_mode_only_strict_3p_tools` 族；状态刷新 `#39935`→LANDED(sync646) + issuer-less 臂立项；`#50189`→被 sync646 覆盖。已记录且未变者（`#51211`/`#49130`/`#51678`/`#50480`/`#50781`/ws 采样/prefer_mxc）不重复。

## ④ 只读声明

- 仅只读 `git -C /home/jacks/jacks_dev/codex_go`（`fetch/log/show/merge-base/rev-parse`）与 `git -C /home/jacks/jacks_dev/codex`（`log/blame`）。
- **0 commit / 0 push / 0 ref 移动**；未 `project_delete`；未碰 parity 检出 `C:\rw\codex-rs`、`main` 或任何分支。

---
*生成：syncl5 · Linux 节点 · 2026-10-07 · Go `d12d00dd`（sync648）*

---

## 附录：续增量（sync649 → sync651，+3 笔 · 追加于同轮）

> 队长于本轮再次落主 3 笔（main `d12d00dd` → **`b041cbfd`**）。下表为 `r86_landing_ledger_2026_10_07.md` 同表头的续行。

| sync | Go sha | Rust 上游 sha | PR | 文件数 | 主题 | RC 结论 |
|---|---|---|---|---|---|---|
| sync649 | `60b97880` | `8ea2428c38` | `#49861` | 8 | add the Daybreak status-line surfaces | 队长自跑：删 `chatwidget/status_controls.go` 的 `case StatusLineDaybreak` ⇒ `daybreak_status_surfaces_like_rust_test.go:32` ×3 FAIL（恢复 sha256 `eb1257a5…` ok） |
| sync650 | `668150fb` | `8ea2428c38` | `#49861` | 7 | wire the Daybreak thread preference into the live status controls | 队长自跑：删 `tui/tea/status_controls.go` 的 `DaybreakEnabled: m.daybreakEnabled,` ⇒ `daybreak_status_surfaces_test.go:25` FAIL（恢复 sha256 `2eff22c6…` ok） |
| sync651 | `b041cbfd` | `8ea2428c38` | `#49861` | 2 | accept the daybreak terminal-title item in doctor | 队长自跑：删 `doctor/doctor.go` 的 `case "daybreak":` ⇒ `doctor_test.go:1140` FAIL（恢复 sha256 `6efb7a55…` ok） |

Rust sha 自解：
```
$ git -C /home/jacks/jacks_dev/codex log --oneline --grep '#49861' -F origin/main
8ea2428c38 Add Daybreak state to the status line and terminal title (#49861)
$ git -C /home/jacks/jacks_dev/codex rev-parse 8ea2428c38
8ea2428c38f8994e18d789669f5cbc5df75e1f17
```

一致性核对（重跑）：
```
$ git -C codex_go show --stat --format='' 60b97880 | tail -1   →  8 files changed, 206 insertions(+), 8 deletions(-)
$ git -C codex_go show --stat --format='' 668150fb | tail -1   →  7 files changed, 137 insertions(+), 43 deletions(-)
$ git -C codex_go show --stat --format='' b041cbfd | tail -1   →  2 files changed, 22 insertions(+)
# 台账文件数 8/7/2 一致 ✓
$ git -C codex_go log --format='%h' d12d00dd..b041cbfd | sort
60b97880 / 668150fb / b041cbfd      → 与 3 笔集合 diff 为空 ⇒ UNION==RANGE(3) ✓
$ for s in 60b97880 668150fb b041cbfd; do git -C codex_go merge-base --is-ancestor $s origin/main && echo "$s ancestor ✓"; done
$ git -C codex_go log --format='%h' 8b2453a9..b041cbfd | wc -l   →  10（642..651 共 10 笔）
```
⇒ **无不一致**。

---

## 附录二：续增量（sync652 → sync659，+8 笔 · 追加于同轮）

> main `b041cbfd` → **`c9fbd10f`**。下表为 `r86_landing_ledger_2026_10_07.md` 同表头的续行。

| sync | Go sha | Rust 上游 sha | PR | 文件数 | 主题 | RC 结论 |
|---|---|---|---|---|---|---|
| sync652 | `9762262c` | `8ea2428c38` | `#49861` | 4 | seed a new thread's Daybreak preference | 队长自跑：撤 wiring ⇒ `daybreak_default_test.go:34` FAIL |
| sync653 | `b52105cf` | `7f9832d0d0` | `#39935` | 5 | require issuer-less token endpoints to share the authorization origin（关闭 sync646 的 declared gap） | 队长自跑：撤 origin 绑定 ⇒ `oauth_issuer_binding_test.go:234` FAIL |
| sync654 | `e72df5b5` | `ae132dc50a` | `#47590` | 2 | send a numeric custom reasoning effort as a JSON number | 队长自跑：删 `MarshalJSON` ⇒ `responses_agent_test.go:4254` FAIL |
| sync655 | `4df8a518` | `5bae5b563e` | `#47657` | 3 | restrict the GovCloud Mantle model catalog | 队长自跑：撤 gov 接线 ⇒ `provider_test.go:343` FAIL |
| sync656 | `240913ad` | `95dafbc7b5`（#18190；blame `app/side.rs`） | — | 2 | interrupt a local side thread's turn before closing it | 队长自跑：撤 interrupt ⇒ `interactive_sideclose_test.go:104` FAIL |
| sync657 | `8fe41572` | `acf9818fae` | `#50786` | 5 | persist the Command Center grouping | 队长自跑：撤持久化 ⇒ `…persist_like_rust_test.go:45` FAIL |
| sync658 | `999e655d` | `acf9818fae` | `#50786` | 3 | carry the agents-overview grouping through startup settings | 队长自跑：撤回灌 ⇒ `…settings_like_rust_test.go:23` FAIL |
| sync659 | `c9fbd10f` | `a92ccbde53` | `#47974` | 2 | protect a resolved gitdir across writable roots | 队长自跑：撤 `addResolvedGitDirCarveouts` ⇒ Windows overlay 探针 FAIL（`resolved gitdir carveout missing`） |

Rust sha 自解：
```
#47590 → ae132dc50a Serialize numeric custom reasoning effort as JSON numbers (#47590)
#47657 → 5bae5b563e Restrict the default Bedrock GovCloud model catalog (#47657)
#50786 → acf9818fae Remember Command Center grouping across launches (#50786)
#47974 → a92ccbde53 Preserve Git directory protections across writable roots (#47974)
#39935 → 7f9832d0d0 Enforce issuer binding for MCP OAuth endpoints (#39935)   （同 sync646）
#49861 → 8ea2428c38 Add Daybreak state to the status line and terminal title   （同 sync649-651）
#18190 → 95dafbc7b5 Add /side conversations；sync656 无 PR 号，blame codex-rs/tui/src/app/side.rs:433 → 95dafbc7b5b
```

一致性核对（重跑）：
```
$ git -C codex_go show --stat --format='' <sha> | tail -1
9762262c →  4 files changed, 129 insertions(+), 15 deletions(-)
b52105cf →  5 files changed, 58 insertions(+), 43 deletions(-)
e72df5b5 →  2 files changed, 102 insertions(+)
4df8a518 →  3 files changed, 124 insertions(+)
240913ad →  2 files changed, 226 insertions(+), 1 deletions(-)
8fe41572 →  5 files changed, 229 insertions(+), 10 deletions(-)
999e655d →  3 files changed, 40 insertions(+), 2 deletions(-)
c9fbd10f →  2 files changed, 206 insertions(+)
# 台账文件数 4/5/2/3/2/5/3/2 逐笔一致 ✓
$ git -C codex_go log --format='%h' b041cbfd..c9fbd10f | sort   → 与 8 笔集合 diff 为空 ⇒ UNION==RANGE(8) ✓
$ merge-base --is-ancestor <sha> origin/main   → 8/8 exit 0 ✓
$ git -C codex_go log --format='%h' 8b2453a9..c9fbd10f | wc -l → 18（sync642..659）
```
⇒ **无不一致**。
