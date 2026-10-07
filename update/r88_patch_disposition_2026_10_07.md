# round88 补丁处置清单（r88 finale，2026-10-07）

作者：syntropy（team leader）· 写入方式：leader 直写
用途：回答“`update/r86_patches/` 里那一堆补丁怎么办”——**已逐个人工判过并处置完，你不需要再做任何事**。

---

## 0. 一句话结论

`update/r86_patches/` 是 round86–88 的**候选补丁存档**（66 个文件），不是待办队列。
按内容实测分类（见 §1 判据）：

| 分类 | 数量 | 说明 |
|---|---|---|
| 已在 main 里 | **63** | 内容已在树中（反向 apply 通过，或新增行逐条命中） |
| 本轮 leader 落主 | （含在上面 63 内） | sync660–664，见 §2 |
| **被 Rust pin 卡住** | **1** | `syncw3_parity_manifest_51678.patch`，见 §3 |
| **故意不落的草稿** | **1** | `syncw3_49584.patch`（避让面），见 §4 |
| 被后继补丁取代 | **1** | `syncl3.patch`，见 §5 |

合计 66。**没有“漏掉的待办补丁”。**

---

## 1. 判定方法（可复跑）

落主树 `D:\qax\reagent\dev\codex_go_wt\integ86g`，tip `640b5603`：

```
# 反向 apply 通过 ⇒ 该补丁的内容已在树里（已落地）
git apply --check --reverse <patch>     # rc=0 → LANDED
# 正向 apply 通过 ⇒ 补丁尚未落地
git apply --check <patch>               # rc=0 → PENDING
```

首轮结果：LANDED **52** / PENDING **1** / 两者都不通过 **13**。
对 13 个“都不通过”的再做内容抽查（取补丁新增行中长度 >30 的行，`git grep -F` 于 `HEAD`）：

* **2 个**用 `-C0/-C1` 缩小上下文后反向 apply 通过 ⇒ 已落地；
* **9 个**新增行 6/6 命中树 ⇒ 上下文漂移但**内容已落地**；
* **1 个**（`syncw3_49584.patch`）正向 apply（`-C1`）通过、反向不通过 ⇒ **确未落地**；
* **1 个**（`syncl3.patch`）新增行仅 3/6 命中 ⇒ **被后继补丁取代的旧草稿**。

---

## 2. 本轮 leader 落主（5 笔，均已 push 到 `origin/main` 与 `origin/integ86g`）

| sync | commit | 来源 | 内容 | 门禁 |
|---|---|---|---|---|
| sync660 | `059c4de9` | `syncl4_39935c.patch` | MCP OAuth login 在 issuer 绑定被拒时失败、不回退猜 URL（Rust #39935） | apply 干净 + LF 归一后 3 文件 sha256 逐字节一致；gofmt/build/vet 干净；`./mcp/`+`./app/` 新增失败 0；值级 RC ×2；parity 绿 |
| sync661 | `b2e23a6b` | `syncl3_48761.patch` | transcript 提示改由 keymap 驱动、行宽不足时省略（Rust #48761 的规则搬到 Go 现有 marker 载体） | 7 文件 sha256 与已核补丁逐字节一致；gofmt/build/vet 干净；受影响 7 包与基线失败集**完全相同**；值级 RC ×4；LF 树下 parity 绿 |
| sync662 | `5c4488a7` | `syncl3_50786_b2.patch` | 补 `agents_overview_grouping` 主机读取键的覆盖（#50786） | 同上（同批验证） |
| sync663 | `dd495b29` | syncw1 C1（无补丁，现场实现） | `ListAgents` 前缀改按**段边界**匹配（`agent.AgentPath.MatchesPrefix`），修 `/root/work` 误返兄弟 `/root/worker` | gofmt/build/vet 干净；`./agent/ ./appserver/ ./exec/` 新增失败 0；值级 RC ×2（两处接线各自可复现 FAIL）；parity 绿 |
| sync664 | `640b5603` | syncl6（无补丁，现场实现） | Go fallback 的 `codex-auto-review` 补 `tool_mode=code_mode_only` / `multi_agent_version=v1` / `use_responses_lite=true` | 值级 RC ×3（每字段一条 FAIL 原文）；`./model/` 与基线同为 ok；parity 绿 |

> 说明：sync661 的落主口径为“**规则搬家**”——Go 无 Rust `transcript_view` 载体，故把“可配置 + 放不下则省略”搬到 Go 现有 marker 载体上；默认配置下输出与改动前**逐字节一致**，仅在“keymap 重绑定”“行宽不足”两个边角跟随 Rust 语义。此判断由 leader 裁定并在 commit message 中如实标注。

---

## 3. 唯一被卡住的补丁（不予落主）

`syncw3_parity_manifest_51678.patch`（623 B，etag `0525309a7e4dafab`）

```
- Path:     "core/tests/suite/windows_sandbox.rs",
+ Path:     "core/tests/windows_sandbox.rs",
```

原因：parity 检出 `C:\rw\codex-rs` 固定在 `5b0b253035`，该 pin 上

```
Test-Path C:\rw\codex-rs\core\tests\suite\windows_sandbox.rs   -> True
Test-Path C:\rw\codex-rs\core\tests\windows_sandbox.rs         -> False
```

`TestRustUnifiedExecSandboxSuiteManifest` 会读取 manifest 中 path 指向的 Rust 源文件并抽取 `#[test]` 名单；路径指向不存在的文件 ⇒ 必红。**解锁条件**：把 parity 检出推进到含 Rust #51678 的提交后，直接 `git apply` 即可（不需要重写）。

---

## 4. 故意不落的草稿

`syncw3_49584.patch`（4444 B）：2 文件 —— `appserver/turn_runtime.go` + `appserver/guardian_skills_context_test.go`。
`appserver/turn_runtime.go` 属既定**避让面**（本轮全部车道均约定不并行改），guardian skills context 子系统按既有裁决属 backlog ⇒ **不落**。

---

## 5. 被取代的旧草稿

| 草稿 | 后继 / 现状 |
|---|---|
| `syncl3.patch`（10 文件） | 由 `syncl3b.patch`（已落）与后续 split 补丁取代；新增行仅 3/6 命中 |
| `syncl4_39935.patch` / `syncl4_39935b.patch` | 由 `syncl4_39935c.patch`（= sync660）取代 |
| `syncl6.patch` | 由拆分后的 `syncl6_47590` / `syncl6_47657` / … 取代（均已落 sync654/655 等） |
| `integ86c.patch` | 集成包；新增行 6/6 命中树 |
| `syncw1_51652.patch` | 由 `syncw1_51652_windows_path.patch`（已落 sync644）取代 |

---

## 6. 复核入口

* 落主树：`D:\qax\reagent\dev\codex_go_wt\integ86g`（HEAD = round88 收尾 tip）
* 共享仓：`D:\qax\reagent\dev\codex_go`（`refs/heads/main` 已同步到同一 tip）
* 台账：`update/r86_landing_ledger_2026_10_07.md`
* 收尾单一事实源：`update/r88_shutdown_status_2026_10_07.md`
* 覆盖审计：`update/verify_round88b_sync652_659_2026_10_07.md`（GREEN）、`update/verify_main_c9fbd10f_2026_10_07.md`（GREEN）
