# GO-C 清理报告 — round 86（仅本地）

- **执行者**：synct5（Windows 节点）
- **授权**：队长 GO-C，msg-1791369894481686400-4228（**仅限本地**；冻结已解除）
- **日期**：2026-10-07
- **范围**：删除本人创建的 worktree + 删除本地冗余分支 `integ86b`。**禁止 push / 删除远端 ref**。

## 0 现状核对（权威）

```
$ git ls-remote --heads origin
ed6f52b7406055444a6ac3023788b8a0f588e6b2  refs/heads/integ86
1306de38978c97bbbb861ef910ac48e0b4792e6e  refs/heads/integ86b
9c4d578f6fad093b4b16fc537c8412d95e4247b0  refs/heads/integ86c
9fafc187052eeff1651dc22a995d943a692c3cea  refs/heads/integ86d
937836c5a6dc53cccbd39dfd996f8c619b90a5b9  refs/heads/integ86e
589703d87bfc50359cee430105c635c1d656b323  refs/heads/integ86f
b9ad41d30d630aa4cc68434c21f3660e15a347a0  refs/heads/integ86g
b9ad41d30d630aa4cc68434c21f3660e15a347a0  refs/heads/main
30e47642d76d29a3acbd8384496e947632523564  refs/heads/syncl1
31863015ce76ba3d6de4c526a9af01894691bac7  refs/heads/syncl3
```
- `origin/main = origin/integ86g = b9ad41d3`，与队长口径一致；本地 `main` 引用亦为 `b9ad41d3`。本轮网络恢复正常（`ls-remote` exit=0）。
- **`syncl1` / `syncl3` 远端仍在**（按指令未动）。

## 1 worktree 清理（7 个，全部由本人创建）

| # | 路径 | 清理前 HEAD | 清理前脏文件数 | 命令 | 结果 |
|---|---|---|---|---|---|
| 1 | `…\codex_go_wt\base-gate` | 39dd8e94 (detached) | 0 | `git worktree remove` | exit=0 |
| 2 | `…\codex_go_wt\integ86-gate` | ed6f52b7 (detached) | 0 | `git worktree remove` | exit=0 |
| 3 | `…\codex_go_wt\integ86b-gate` | 1306de38 (detached) | 0 | `git worktree remove` | exit=0 |
| 4 | `…\codex_go_wt\audit-base` | ed6f52b7 (detached) | 0 | `git worktree remove` | exit=0 |
| 5 | `…\codex_go_wt\audit-head` | 9c4d578f (detached) | 0 | `git worktree remove` | exit=0 |
| 6 | `…\codex_go_wt\audit-head2` | 9fafc187 (detached) | 0 | `git worktree remove` | exit=0 |
| 7 | `…\codex_go_wt\audit-replay` | ed6f52b7 (detached) | 19 | `git worktree remove --force` | exit=0 |

`audit-replay` 用 `--force` 的原因：内含我**故意**留下的重放 scratch（tree-sha 握手用的 `git apply` + `cherry-pick -n` 结果），不是有价值的工作；脏文件恰为 19 个且与审计对象一致：
`appserver/{agent_controller.go,environment_inheritance.go,environment_inheritance_test.go,runtime_router.go}`、`codemode/{grpc_admission_test.go,grpc_session_provider.go,remote_provider.go}`、`doctor/{configured_tui_mode_test.go,doctor.go}`、`model/{responses_agent.go,responses_agent_test.go}`、`prompt/{skills_render.go,skills_render_test.go}`、`state/{backfill.go,backfill_test.go}`、`tui/{state.go,state_test.go}`、`tui/tea/{model.go,model_test.go}`（= A 批 7 + Δ4 12）。
### `git worktree list` 前后对照

**BEFORE（24 个）**
```
D:/qax/reagent/dev/codex_go                  ed6f52b7 [integ86]
D:/qax/reagent/dev/codex_go_wt/audit-base    ed6f52b7 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/audit-head    9c4d578f (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/audit-head2   9fafc187 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/audit-replay  ed6f52b7 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/base-gate     39dd8e94 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/check9fa      937836c5 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/integ86-gate  ed6f52b7 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/integ86b      9fafc187 [integ86d]
D:/qax/reagent/dev/codex_go_wt/integ86b-gate 1306de38 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/integ86c      59d94a0a (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/integ86c2     9c4d578f [integ86c]
D:/qax/reagent/dev/codex_go_wt/integ86e      937836c5 [integ86e]
D:/qax/reagent/dev/codex_go_wt/integ86f      589703d8 [integ86f]
D:/qax/reagent/dev/codex_go_wt/integ86g      b9ad41d3 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/integ86g_base 589703d8 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw1        bddaff34 [syncw1]
D:/qax/reagent/dev/codex_go_wt/syncw1base    ed6f52b7 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw1gate    9620629e (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw1r86     937836c5 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw2        ed6f52b7 [syncw2]
D:/qax/reagent/dev/codex_go_wt/syncw3        940075cb [syncw3]
D:/qax/reagent/dev/codex_go_wt/syncw4        6269b849 [syncw4]
D:/qax/reagent/dev/codex_go_wt/syncw4b       ed6f52b7 [syncw4b]
D:/qax/reagent/dev/codex_go_wt/syncw4c       b9ad41d3 [syncw4c]
```

**AFTER（18 个；本人 7 个已移除，其余 17 个未触碰）**
```
D:/qax/reagent/dev/codex_go                  ed6f52b7 [integ86]
D:/qax/reagent/dev/codex_go_wt/check9fa      937836c5 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/integ86b      9fafc187 [integ86d]
D:/qax/reagent/dev/codex_go_wt/integ86c      59d94a0a (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/integ86c2     9c4d578f [integ86c]
D:/qax/reagent/dev/codex_go_wt/integ86e      937836c5 [integ86e]
D:/qax/reagent/dev/codex_go_wt/integ86f      589703d8 [integ86f]
D:/qax/reagent/dev/codex_go_wt/integ86g      b9ad41d3 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/integ86g_base 589703d8 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw1        bddaff34 [syncw1]
D:/qax/reagent/dev/codex_go_wt/syncw1base    ed6f52b7 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw1gate    9620629e (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw1r86     937836c5 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw2        ed6f52b7 [syncw2]
D:/qax/reagent/dev/codex_go_wt/syncw3        b9ad41d3 (detached HEAD)
D:/qax/reagent/dev/codex_go_wt/syncw4        6269b849 [syncw4]
D:/qax/reagent/dev/codex_go_wt/syncw4b       ed6f52b7 [syncw4b]
D:/qax/reagent/dev/codex_go_wt/syncw4c       b9ad41d3 [syncw4c]
```
7 个目录在磁盘上均已不存在（`Test-Path` = False）。

## 2 本地分支 `integ86b` 删除

```
$ git rev-parse main integ86b
b9ad41d30d630aa4cc68434c21f3660e15a347a0
1306de38978c97bbbb861ef910ac48e0b4792e6e
$ git merge-base --is-ancestor integ86b main ; echo $LASTEXITCODE
1                          # 提交对象非祖先（main 上是 re-author 的等价提交）
$ git rev-list --count main..integ86b
4
$ git branch -d integ86b
error: the branch 'integ86b' is not fully merged      # 预期（re-authored）
# 内容等价性已由 9fafc187 审计证明，且 9fafc187 是 main 的祖先（merge-base --is-ancestor 9fafc187 main → exit 0）：
#   b153fe59 (c6880369…) == b2a153b0 (c6880369…)   #48983
#   ee19c38d (b77b645e…) == f15258cf (b77b645e…)   #49075
#   de6ff942 (f6ea7051…) == eb9d894c (f6ea7051…)   #50200
#   1306de38 (6e0974ff…) == 9fafc187 (6e0974ff…)   #49145
$ git branch -D integ86b
Deleted branch integ86b (was 1306de38).
```
确认：`git branch --list integ86b` → 0 行（已删除）。该分支此前未被任何 worktree 检出，删除不影响他人。

## 3 未推 / 未删远端确认

- 本轮执行的命令仅为：`git worktree list/remove`、`git branch -d/-D`、`git status/rev-parse/merge-base/rev-list/show/patch-id`、`git ls-remote --heads origin`（只读）。
- **未执行任何 `git push`**、**未执行任何远端 ref 删除**。`ls-remote` 显示 `main=b9ad41d3`、`syncl1=30e47642`、`syncl3=31863015` 等全部保持原状。

## 4 遗留

- 主工作区仍在分支 `integ86` @ `ed6f52b7`（未改动），其未跟踪产物：`update/triage5_2026_10_07.md`、`update/verify_main_9c4d578f_2026_10_07.md`、`update/verify_main_9fafc187_2026_10_07.md`、`update/goc_cleanup_2026_10_07.md`、`update/r86_patches/` 等 —— 按协议留队长提交。
- 不由本人创建、本轮**未触碰**的 worktree：`check9fa`、`integ86b`(on integ86d)、`integ86c`、`integ86c2`、`integ86e`、`integ86f`、`integ86g`、`integ86g_base`、`syncw1*`、`syncw2`、`syncw3`、`syncw4*`。