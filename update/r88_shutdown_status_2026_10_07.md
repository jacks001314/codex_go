# round88 收尾状态（r88f → r88l，2026-10-07）— v3

作者：syntropy（team leader）· 写入方式：leader 直写
口径：**只做收尾**——不再派新单；剩余车道回包 → 记录产物 → 下线该车道。
本 v3 相对 v2 的变化：**补丁全部处置完毕**（sync660–664 落主并 push）、车道全部下线、
`update/` 归档提交、并给出唯一被卡项的确切解锁条件。

---

## 1. 冻结态（实测命令原文）

| 项 | 值 | 取证 |
|---|---|---|
| Go remote `refs/heads/main` | `640b56032ae96007b870822fed253d98d65e4ba3` | `git ls-remote origin refs/heads/main refs/heads/integ86g` |
| Go remote `refs/heads/integ86g` | 同上 ⇒ **main == integ86g** | 同上 |
| 落主树 `D:\qax\reagent\dev\codex_go_wt\integ86g` | HEAD `640b5603` | `rev-parse HEAD` |
| 共享仓 `D:\qax\reagent\dev\codex_go` `refs/heads/main` | `640b5603` | `rev-parse refs/heads/main origin/main` |
| round88 已落主 | **23 笔**：`8b2453a9..640b5603`（sync642..sync664） | `git log --oneline 8b2453a9..640b5603` |
| 下一个可用的 sync 号 | **sync665** | — |
| Rust 枚举 pin | 台账锚 `d83bb540ec`；Rust `origin/main` 已 fetch 到 `95ec468619` | 本文件 §6 |
| parity 用 Rust 检出 | `C:\rw\codex-rs`，固定 `5b0b253035`（**勿动**） | 既往约定 |

### 1.1 收尾门禁（leader 亲跑）

```
$ cd D:\qax\reagent\dev\codex_go_wt\integ86g        # HEAD = 640b5603
$ $env:CODEX_RUST_ROOT='C:\rw\codex-rs'; go test ./parity/ -count=1
ok      codex_go/parity  5.606s      exit=0            # 无 FAIL
```

受本轮改动影响的包整包对拍（patched vs 同 tip 干净树）：

| 包 | 干净基线失败集 | patched 失败集 | 新增失败 |
|---|---|---|---|
| `./tui/ ./tui/exec_cell/ ./tui/history_cell/ ./tui/tea/ ./tui/bottom_pane/ ./tui/chatwidget/ ./config/` | `tui/tea TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge` | 同左 | **0** |
| `./agent/ ./appserver/ ./exec/` | appserver 6 条 | appserver 6 条（+1 条并行下的时序 flaky，单跑 3/3 PASS） | **0** |
| `./model/` | ok | ok | **0** |

> 既有基线（勿记新增）：`tui/tea` 1 条 Windows 基线；`appserver` 6 条；`tool` 3 条 env-FS；`doctor` 2 条；
> `model` 2 条 env；vet 既有 2 条 lock-copy（`model/responses_agent.go:1500`、`exec/agent_controller.go:909`）。
> parity 的 `TestRustCollaborationModeTemplatesMatchGo` 在 **CRLF 检出**下是**假红**（LF 树全绿；9312−9184 = 128 = 该文件 LF 行数）。

---

## 2. 车道终态（全部下线）

| 车道 | 最终任务 | 产物（Master，字节/etag） | 结论 |
|---|---|---|---|
| syncl5 | 台账补 8 行 + 一致性重跑 | `r86_landing_ledger_2026_10_07.md` 28528 B / `fc2c9e445377744e`；`syncl5_ledger_increment_2026_10_07.md` 12990 B / `b1a2457d476927b1` | 无不一致；`#39935` issuer-less 臂 **CLOSED**（sync653） |
| syncl1 | Rust pin triage `d83bb540ec..1199b39762` | `syncl1_pin_triage_1199b397_2026_10_07.md` 8593 B / `c0256e463ce21e0b` | 域内 0、critical 0 漂移、manifest 0 变化；pin 前进的 2 个先决条件见 §5 |
| syncl6 | `codex-auto-review` 三字段缺口 | `syncl6_autoreview_fields_2026_10_07.md` 18598 B / `e5476765292c02b6` | 缺口成立；**已由 leader 落主（sync664）** |
| syncl4 | 档 A 补丁（3 文件） | `r86_patches/syncl4_39935c.patch`；`syncl4_39935c_2026_10_07.md` | **已落主（sync660）** |
| syncl3 | `#48761` 方案 1 + `#50786` B 补测 | `r86_patches/syncl3_48761.patch` / `syncl3_50786_b2.patch`；`syncl3_48761_2026_10_07.md` | **已落主（sync661 / sync662）** |
| syncw1 | 路径前缀比较归一审计（只读） | `syncw1_pathprefix_audit_2026_10_07.md` 15792 B / `04f3b4fa2a6633f6` | C1 真分歧（值级已证）；**已由 leader 落主（sync663）**；C2/C3 建议不修 |
| syncw2 | 本地 side-close / `#50781` 核查（只读） | `syncw2_local_sideclose2_2026_10_07.md` 12546 B / `966204c39d5509ac` | 两项均**无缺口、不出补丁** |
| syncw3 | `>5 文件` 域内提交切片潜力（只读） | `syncw3_sliceable_l_2026_10_07.md` 15653 B | 仅 1 条可切（`#50559`，判 M）；`syncw3_parity_manifest_51678.patch` **被 Rust pin 卡住**（见补丁清单 §3） |
| syncw4 | 对抗性验证 sync652..659 | `verify_round88b_sync652_659_2026_10_07.md` 23797 B / `41ad63ba157b0e2e` | **GREEN，0 REFUTED**；tree-sha 握手 8/8；值级 RC 7/7 |
| synct5 | 独立发布审计 `d12d00dd..c9fbd10f`（11 笔） | `verify_main_c9fbd10f_2026_10_07.md` 28596 B / sha256 `57035F64…` | **GREEN / 可发布**；tree-sha 握手 11/11；逐笔 RC 11/11；`c9fbd10f` 的 1 笔后继（sync660）由 leader 另行验证 |

### 2.1 审计覆盖（18 + 5 = 23 笔全区间覆盖）

* `verify_main_8b2453a9` → sync642..648（7 笔）
* `verify_round88_sync647_651` → sync647..651（5 笔）
* `verify_round88b_sync652_659` → sync652..659（8 笔，GREEN）
* `verify_main_c9fbd10f` → sync642..659 复算（11 笔区间，GREEN）
* leader 自验 → sync660（`r88_leader_verify_syncl4_39935c`）、sync661/662（`r88_leader_verify_syncl3`）、sync663/664（本文件 §1.1）

---

## 3. 本轮落主（5 笔，已 push）

| sync | commit | 主题 |
|---|---|---|
| sync660 | `059c4de9` | MCP OAuth login 在 issuer 绑定被拒时失败（#39935） |
| sync661 | `b2e23a6b` | transcript 提示跟随 keymap / 行宽不足则省略（#48761，规则搬家） |
| sync662 | `5c4488a7` | `agents_overview_grouping` 主机读取键覆盖（#50786） |
| sync663 | `dd495b29` | `ListAgents` 前缀按段边界匹配（修兄弟目录误返） |
| sync664 | `640b5603` | `codex-auto-review` fallback 三字段对齐 models.json |

补丁逐个处置见：`update/r88_patch_disposition_2026_10_07.md`。

---

## 4. 纪律

* 本节所有提交 **只含代码**；`update/` 归档走单独的 `(docs)` 提交。
* 全程未碰 parity 检出 `C:\rw\codex-rs`、未碰 Rust 镜像仓、未动避让面
  （`appserver/runtime_router.go` / `appserver/turn_runtime.go` / `tui/state.go` / `sandbox/windowssandbox/`）。
* 工作树残留（`D:\tmp\*`、`codex_go_wt\*` 若干 detached worktree）需用户侧清理：
  本机 `Remove-Item` 被策略拦截，leader 无法代清。

---

## 5. 未决 / backlog（需用户裁示，非本收尾阻塞）

1. **parity 检出 pin 前进**：决定 `syncw3_parity_manifest_51678.patch` 的落主时机（§补丁清单 §3）。前进前需先重钉 `core/tests/suite/mod.rs`，并在 Go 侧补 `features` 的 `code_mode_tool_description_first`（Rust #51690 使 FEATURES 167→168，Go 0 命中，否则 parity 红）。
2. `#50559` 文件计数阈值（生产 5 卡线 vs 含测试 6–7）与是否允许改顶层 `install/` 包。
3. syncw2 的两条披露项（`tui/app` parity 决策库未接生产；三条“只清本地 activeSide”路径）是否立项。
4. 5 组冻结子系统是否纳入目标；是否放宽“≤5 文件”口径；路线 (c) embed `models.json`。
5. Rust `origin/main` 已前进到 `95ec468619`（含 #51690），枚举 pin 仍锚 `d83bb540ec`；下一轮同步以最新 pin 为准。
