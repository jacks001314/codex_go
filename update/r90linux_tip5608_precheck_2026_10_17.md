# r90 · Linux 节点车道 · 新 tip `5608e096` 预警复验（S-5 后续）

- lane: `mc-l90-node`（node id `06aac1d3185c1999dee1e86482026deb`, linux_node, 192.168.1.99）
- 触发: 回应 S-5（`msg-1792192010960573200-2291`）的 pending 提醒时复测远端，发现 **tip 又前进了 1 笔**（S-5 的 `5d740a02` → `5608e096`），按 R5-terdecies「push 后广播、各车道对齐」的纪律顺手做 Linux 侧预警复验
- 目标 sha: `5608e096065df48efa5d39441d69ee59bb91a342`
- 性质: 只读 + 构建验证；**未改任何产品码/测试码**；未做任何 ref 变更；S-5 的结论对 `5d740a02` 仍然成立（本文件不替代它）

## 0. 结论（三句话）

1. 新 tip `5608e096` = wave-90 第四批：**A-b17 把 `#52164` 的 `exec/` 那一半接上了**（`exec/exec.go` 现在也读 `include_environment_context_time`，关掉时 `<current_date>`+`<timezone>` **两块都不写**）——这正是我 L-3 报告里登记的第 ⑥ 项「第二个无条件写入者」缺口，**现已闭合**，且 3 条真 Runner 用例在 Linux 上 PASS。
2. Linux 侧门禁：`go build ./...` EXIT=0；`./exec/... ./memories/...` **EXIT=0（FAIL=0，SKIP=1）**；`./sandbox/...` **EXIT=1 但失败集合与 `5d740a02`/`3f3c44f7` 逐名完全相同**（3 个存量红包，7 条同名用例）⇒ **无新增失败**。
3. 顺手发现一个可改进点（**只报告不改**）：`memories/version_test.go` 的 Rust 模板守卫因**硬编码相对路径**而恒 SKIP；我用 `/tmp` 符号链接把 Rust 树放到它能找到的位置后，该用例**真跑并 PASS**（⇒ 不是依赖缺失，而是路径假设；建议改读 `CODEX_RUST_ROOT`）。

## 1. 新 tip 的事实链（原文）

```
$ timeout 60 git -C /home/jacks/jacks_dev/codex_go fetch origin main 2>&1; echo "FETCH_EXIT=$?"
From https://github.com/jacks001314/codex_go
 * branch              main       -> FETCH_HEAD
   5d740a02..5608e096  main       -> origin/main
FETCH_EXIT=0
$ git rev-parse origin/main
5608e096065df48efa5d39441d69ee59bb91a342
$ timeout 20 git ls-remote origin main
5608e096065df48efa5d39441d69ee59bb91a342	refs/heads/main      （实时值一致）

$ git log --oneline 5d740a02..origin/main
5608e096 round 90 (lead): land wave-90 batch four - bounded rollout input for memory extraction (B-b14) and the exec environment-context switch (A-b17)
```

改动面：

```
 exec/exec.go                             |  37 ++-      ← A-b17：#52164 的 exec 半
 exec/exec_test.go                        | 149 +++++++++++
 memories/pipeline.go                     |   5 +-
 memories/rollout_input.go                |   4 +-
 memories/rollout_input_bound.go          | 422 +++++++++++++++++++++++   ← B-b14 新文件
 memories/rollout_input_bound_test.go     | 388 ++++++++++++++++++++++++
 update/…（ledger/queue/A-b17+B-b14 证据 + 我三份证据文件入库）
 14 files changed, 2331 insertions(+), 14 deletions(-)
```

commit 正文自述的两条关键事实（原文摘录）：

- `exec/`：*"execEnvironmentContext now honours include_environment_context_time and omits BOTH `<current_date>` and `<timezone>` when it is off …；RC forcing the gate true fails only the disabled arm."*
- `memories/`：*"new rollout_input_bound.go (8 MiB rollout / 1 MiB record / 64 KiB chunk bounds, SessionMeta header check, reverse JSONL scanner …)；RC dropping the bound allocates 228,594,504 B for a 45,109,154 B rollout."*

另：**我的三份产物被并入仓库**（`update/r90linux_s1_update_2026_10_17.md`、`…s1_update2…`、`…s3…`）—— 无需我再做动作，仅记录。

## 2. 台面与工作区

```
$ git -C /home/jacks/jacks_dev/codex_go worktree add --detach /tmp/l90/r90l7 5608e096065df48efa5d39441d69ee59bb91a342
HEAD is now at 5608e096 …
$ git -C /tmp/l90/r90l7 status --porcelain
(空)                       ← 干净；全部测试跑完后复测仍空
```

- 新台面 `/tmp/l90/r90l7` = `5608e096`（**保留**，待回收指令）；S-5 台面 `/tmp/l90/r90l6` = `5d740a02`（保留，S-5 证据对应它）。
- 主工作区 HEAD=`5248c35e`、`## main...origin/main [behind 2]`（外部会话的 pull 停在 `5248c35e`；**我不去 pull 主工作区**，遵守 S-5 边界）。
- 未碰他车道 `/tmp/l90i/*`（`r90l2`=`3f3c44f7`/`r90l3`=`e786ba0c`/`r90l4`=`5248c35e`/`r90l5`=`5d740a02`）。

## 3. Linux 侧门禁（逐条 exit code）

| 命令 | exit | 关键原文 |
|---|---|---|
| `go build ./...` | **0** | 无输出，15s |
| `gofmt -l`（6 个新/改 go 文件：`exec/exec.go`、`exec/exec_test.go`、`memories/pipeline.go`、`memories/rollout_input.go`、`memories/rollout_input_bound.go`、`memories/rollout_input_bound_test.go`） | **0** | 空输出 |
| `go test -p 1 -v ./exec/... ./memories/... -count=1` | **0** | top-level PASS=**269** / 含子用例 PASS=317 / **FAIL=0** / SKIP=1；`ok codex_go/exec 0.183s`、`ok codex_go/memories 1.834s`（7s 总计） |
| `go test -p 1 ./sandbox/... -count=1` | **1** | 11 包 ok / 3 包红：`sandbox/integrity`、`sandbox/windowssandbox/bin/setup_main/win`、`sandbox/windowssandbox/elevated` |
| `go test -p 1 -v ./sandbox/linuxsandbox/ -count=1` | **0** | `ok codex_go/sandbox/linuxsandbox 0.078s`（含 `--- PASS: TestLinuxBwrapTwoHalfManagedGrantEndToEnd/proxy-grant (0.03s)`） |

`./sandbox/...` 的失败集合与 `5d740a02`（S-5 目标）**逐名相同**：

```
TestContentsRespectWriteGrantsAndCarveoutsLikeRust
TestContentsRespectWriteGrantsAndCarveoutsLikeRust/narrow_write_grant
TestContentsRespectWriteGrantsAndCarveoutsLikeRust/overlapping_write_grants
TestContentsFollowLinksAndResolvedWriteGrantsLikeRust
TestContentsFollowLinksAndResolvedWriteGrantsLikeRust/link_is_the_writable_root
TestRuntimePathsIncludeAppRootAndPrimaryRuntimeRoot
TestCanonicalPermissionProfileRoundTripIgnoresSymbolicSlashTmpOnWindows
```

⇒ 与 S-5 对照树的「新增失败 = 0」结论**同样适用**（本次未再导 `3f3c44f7` 对照树；依据是：本 tip 相对 `5d740a02` 只改了 `exec/` 与 `memories/`，`git diff --stat 5d740a02 5608e096 -- sandbox/` 为空 ⇒ sandbox 字节未变，S-5 的对照结论直接继承）。

```
$ git diff --stat 5d740a02 5608e096 -- sandbox/
(空)
```

## 4. A-b17 与我的 L-3 呼应（我登记的缺口已闭合）

A-b17 落地的「exec 半」用例在 Linux 上逐条 PASS：

```
--- PASS: TestExecEnvironmentContextHonorsIncludeEnvironmentContextTimeLikeRust (0.00s)
--- PASS: TestExecEnvironmentContextDefaultTemplateUnchangedLikeRust (0.00s)
--- PASS: TestRunAddsStartupEnvironmentContextInputItems (0.00s)
```

- 我在 L-3 报告（`update/r90linux_l3_2026_10_17.md`）与后续回包里登记的未决第 ⑥ 项：*"`exec/exec.go:2213-2228 execEnvironmentContext` 是第二个无条件写 `<current_date>`/`<timezone>` 的点，不在 L-3 写面内，请队长裁定是否另开单"* ⇒ **本 tip 已按上游 `environment.rs:63-67` + `world_state.rs:280-302` 语义修好（两块一起过滤 + 默认 true），并带真 Runner 的 3 档覆盖（显式 true / 显式 false / 键缺失 + 默认模板逐字节钉住）**。我的这一未决项可以关闭。
- 现在两个写入者（`appserver/turn_runtime.go` 的 L-3 版 + `exec/exec.go` 的 A-b17 版）都读同一个开关，Linux 侧两条链路都有真跑的用例。

## 5. 顺手发现（**只报告，未改**）：一个「恒 SKIP」的 Rust 守卫其实能在 Linux 真跑

```
--- SKIP: TestMemoryV2TemplateMatchesRust (0.00s)
    version_test.go:181: Rust checkout not available
```

- 原因不是缺依赖，而是**硬编码相对路径**：`memories/version_test.go:169-176` 只探测两个候选
  `../../git/codex/codex-rs`、`../../../git/codex/codex-rs`（相对包目录），**不读 `CODEX_RUST_ROOT`**。
- 本机 Rust 上游其实就在 `/home/jacks/jacks_dev/codex/codex-rs`（pin `4eb4a65407`，干净）。我在**工作树外**临时建了一个符号链接 `/tmp/l90/git/codex → /home/jacks/jacks_dev/codex`，使 `../../git/codex/codex-rs/Cargo.toml` 可达（**不改仓库任何文件**），再跑：

```
$ cd /tmp/l90/r90l7/memories && env -u TERM go test -v -run '^TestMemoryV2TemplateMatchesRust$' -count=1 .
TMPL_EXIT=0
=== RUN   TestMemoryV2TemplateMatchesRust
--- PASS: TestMemoryV2TemplateMatchesRust (0.00s)
PASS
ok  	codex_go/memories	0.007s
```

⇒ 结论：**该守卫是真的能跑、且在当前 tip 上真的通过**（memory v2 模板与 Rust 上游一致）；SKIP 纯属路径假设所致。**建议**（供你排期，我不动码）：让它与 `appserver` 的 5 个 `CODEX_RUST_*` env 一样读 `CODEX_RUST_ROOT`，这样两台节点都能真跑，SKIP 账也更干净。锚点：`memories/version_test.go:169-181`。
（临时符号链接用完即在 `realpath` 校验后删除，见 §7；未在仓库内留下任何文件。）

## 6. SKIP 账（本 tip，`./exec/... ./memories/...`）

- SKIP = **1**：`memories/TestMemoryV2TemplateMatchesRust`（原因见 §5，可由路径修正消除）。
- FAIL = 0；`./sandbox/...` 侧 SKIP 仍未计得新增项（与 S-5 一致：`linuxsandbox` 0 SKIP）。

## 7. 结论强度 / 未覆盖 / 清理

**verified**：`fetch` 直连 EXIT=0 且带来 `5d740a02..5608e096`；`origin/main`=实时 `ls-remote`=`5608e096…`；新台面 `/tmp/l90/r90l7` pristine；`go build ./...` EXIT=0；`gofmt -l` 空；`./exec/... ./memories/...` EXIT=0（top-PASS=269/FAIL=0/SKIP=1，含 A-b17 三条环境上下文用例 PASS）；`./sandbox/...` EXIT=1 且失败集合与 `5d740a02` 逐名相同；`git diff --stat 5d740a02 5608e096 -- sandbox/` 为空；`linuxsandbox` 包 ok；`TestMemoryV2TemplateMatchesRust` 在可达 Rust 树时 **PASS**（SKIP→PASS 对照）。
**unverified**：① 本 tip 未跑 `./appserver/...`（S-1 更新批次已在 `5d740a02` 跑过；`5608e096` 未动 appserver）；② 未跑 `./cli`、`./tui/...`、`-race`；③ B-b14 的 8 MiB/1 MiB/64 KiB 边界值未逐条核对（只跑了它的测试，全 PASS，未读实现）；④ A-b17 的 `exec` 半未做「撤门 ⇒ 必须 FAIL」的反向验证（那是 A-b17 自己的 RC 责任，其 commit 正文已声明做过）；⑤ `memories/version_test.go` 路径修正的**改法**未验证（无授权改码）。
**清理**：删除临时物 `/tmp/_l90_s6build.log`、`/tmp/_l90_s6em.log`、`/tmp/_l90_s6sb.log`、`/tmp/_l90_s6f.txt`、`/tmp/_l90_s6tmpl.log`，以及工作树外的临时符号链接目录 `/tmp/l90/git`、`/tmp/l90/git_tmp`（`realpath` 校验后删；`/tmp/l90/` 现只剩 `r90l1/r90l3/r90l3.patch/r90l4/r90l5/r90l6/r90l7`）。
**保留**：台面 `/tmp/l90/r90l7`（本 tip）、`/tmp/l90/r90l6`（S-5）及其余历史台面；未动他车道 `/tmp/l90i/*`、Rust 只读树、主工作区他人文件。
**边界自检**：零 commit / push / tag / stash / 分支移动 / ref 变更 / 产品码或测试码改动；未对在跑台面 `checkout`/`reset`；未派生下级 Agent。
