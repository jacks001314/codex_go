# 第 90 轮 · 工作区清理与回写核对（2026-10-16）

依据：用户长期指令「以后工作的工程中，临时的文件用完就删除」+ 本轮遗留 287 格式化文件的处置。

## 1. 已删除的临时 worktree（git worktree remove --force + prune 实测）

| 位置 | 名称 | 数量 |
| --- | --- | --- |
| D:\qax\reagent\dev\codex_go_wt | integ86b, integ86c, integ86c2, integ86e, integ86f, integ86g, synct5v13base, synct5v13head, syncw1, syncw1base, syncw1c1, syncw1gate, syncw1r86 | 13 |
| D:\tmp | aud7_replay, aud8_replay, aud9_replay, leader_39935c, leader_48761, leader_48761b, rp12, rp13 | 8 |
| D:\tmp\syncw2 | wt2e, wt624, wt632, wt632base, wt632lf, wt97, wt984, wt_c9c6, wt_local2, wt_localbase, wt_localside | 11 |
| D:\qax\reagent\dev | _r90a_base（detached @138b313d，本轮车道基线台，车道已改用 r90* 分支台） | 1 |

合计 **33 个 worktree**，每个约 42 MB ⇒ 约释放 **1.4 GB**；`git -C D:\qax\reagent\dev\codex_go worktree list` 现在只剩：
```
D:/qax/reagent/dev/codex_go          <main>     [integ86]
D:/qax/reagent/dev/codex_go_wt/r90a  138b313d   [r90a]    (车道 A 在用)
D:/qax/reagent/dev/codex_go_wt/r90b  138b313d   [r90b]    (车道 B 在用)
D:/qax/reagent/dev/codex_go_wt/r90c  138b313d   [r90c]    (车道 C 在用)
D:/qax/reagent/dev/codex_go_wt/v90   138b313d   (detached; 判定台)
D:/qax/reagent/dev/codex_go_wt/v90ver 661abcbe  (detached; 本轮独立验证台)
```

## 2. 已删除的临时目录/中间产物（实测 gone）

| 路径 | 大小 | 说明 |
| --- | --- | --- |
| D:\qax\reagent\dev\codex_go\.tmp-tui-align | ~694 MB | 早前 TUI 对齐探针产物（`git clean -fdx` 清除） |
| D:\qax\reagent\dev\codex_go\sdktests\.tmp | ~426 MB | SDK 测试临时工作区 |
| D:\tmp\syncw2 | ~515 MB | 旧轮扫描残留（非 worktree 部分） |
| D:\qax\reagent\dev\r86_probe / probe_tmp / syncw3_scan / tmast / _r90a_tmp | ~4 MB | 旧轮探针目录 |
| D:\qax\reagent\dev\tmast | 0 | 空目录 |

合计释放约 **1.6 GB**。

## 3. 未能删除的残留（如实登记）

- `D:\tmp\wt-pin95`（84.9 MB）：目录内只剩一个带 hidden 属性的 `.git` 指针文件；删除该文件与 `.git` 的路径都会被本节点安全策略拦截（`Remove-Item` 被拒），**保留待用户在资源管理器手工删除**。无功能影响。

## 4. 明确保留（不是临时文件，禁止误删）

- `D:\qax\reagent\dev\backs`（12 GB，recopilot_agent_cluster 备份）、`D:\qax\reagent\dev\test`（用户其它任务产物）、`D:\qax\reagent\dev\_patch`（syntropy-public / mac-payload 补丁脚本）——均为用户其它资产，本轮不动。
- `update/r90_leftover_formatting_churn_2026_10_14.patch`（5,876,186 B）：287 个格式化文件的**唯一还原来源**（`git apply -R --check` exit=0），保留。

## 5. Master 回写核对（伪冲突处置）

`project_status` 曾报 7 个冲突文件（plan / commit_ledger / lane_batches / leftover_manifest / na_evidence / verify_log / raw_commits）。用 `project_fetch` 取回 Master 版本后与本地仓库副本做 SHA256 比对，**7/7 全部字节一致**：

| 文件 | mirror==repo | parked==repo |
| --- | --- | --- |
| update/plan_2026_10_16.md | True | True |
| update/r90_commit_ledger_2026_10_16.tsv | True | True |
| update/r90_lane_batches_2026_10_16.tsv | True | True |
| update/r90_leftover_dirty_manifest_2026_10_16.txt | True | True |
| update/r90_na_evidence_2026_10_16.md | True | True |
| update/r90_verify_log_2026_10_16.md | True | True |
| update/raw_commits_round90_2026_10_16.tsv | True | True |

结论：内容是**已交付**的，报出的 etag mismatch 属伪冲突（Master 侧 etag 持续推进）。已删除 `.syntropy/conflicts` 下的 parked 副本（重复内容，属临时文件），**不再重复重写**。

## 6. 本轮提交

- `661abcbe` sync666: support line breaks in Mermaid flowchart node labels (#54419)（3 files, +120/-7；门禁 `go test ./mermaid/ -count=1` ok、`go build ./...` exit=0）
- `30230fe8` round 90 (docs): lane C batch 1 evidence（update/r90c_2026_10_16.md）
