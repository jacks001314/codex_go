# 独立发布审计 — main @ `9cdcd43e`（sync621/622/623 三笔）

- **审计员**：synct5（Windows 节点，只读审计）
- **日期**：2026-10-07
- **被审对象**：`9cdcd43e5b676ffc2f81bb5d2d3058e17ff72c1e`
- **基线**：`4ee41b7ad414d2d21be1305465a70e0ce5642b90`
- **总判定：GREEN** —— 3 笔全部**真实**（文件集与声明逐一相符、无夹带、无 CRLF 污染）；tree-sha 握手一致；门禁**新增失败 0**；新增 8 个测试全 PASS。
- **无「不实」结论**。全程未 push / 未 commit / 未移动分支指针 / 未删远端 ref。

## §1 ① 远端对齐 + tree-sha 握手

```
$ git ls-remote --heads origin main integ86g
9cdcd43e5b676ffc2f81bb5d2d3058e17ff72c1e  refs/heads/integ86g
9cdcd43e5b676ffc2f81bb5d2d3058e17ff72c1e  refs/heads/main          # main 与 integ86g 已同步 ✅
$ git rev-parse origin/main origin/integ86g
9cdcd43e5b676ffc2f81bb5d2d3058e17ff72c1e
9cdcd43e5b676ffc2f81bb5d2d3058e17ff72c1e
$ git merge-base --is-ancestor 4ee41b7a 9cdcd43e ; echo $LASTEXITCODE
0
$ git log --merges --oneline 4ee41b7a..9cdcd43e
(空)                                        # 无 merge commit
$ git show -s --format='%T | %P' 9cdcd43e
ccd7ed203840385ff96242453345816f3ab6588f | cd1024263d2dbb6111b828247f14c035d4fc48cc
$ git log --oneline 4ee41b7a..9cdcd43e
9cdcd43e sync623: persist the realtime transcript tail without inference like Rust (#50531)
cd102426 sync622: read the origin URL through a .git gitfile like Rust
91260ed7 sync621: pin the sandbox console mode like Rust (#49308)
```

**tree-sha 握手（零 commit）**：以 `4ee41b7a` 为基线逐笔 replay → `git write-tree`

```
$ git cherry-pick -n 91260ed7 cd102426 9cdcd43e    ; exit=0（无冲突）
$ git add -A ; git write-tree
ccd7ed203840385ff96242453345816f3ab6588f
```
重放 tree == `9cdcd43e^{tree}` == `ccd7ed203840385ff96242453345816f3ab6588f` ✅

台账对齐说明：`update/` 现有台账**尚无** `9cdcd43e`/`91260ed7`/`cd102426` 的记录（属本轮新推），故 ① 的「与台账对齐」只能对到基线侧；相关 PR 台账条目见 §4。

## §2 ② LF 门禁独立复跑

```
$ git -c core.autocrlf=false -c core.eol=lf archive --format=tar -o head4c.tar 9cdcd43e ; tar -xf ...
$ git -c core.autocrlf=false -c core.eol=lf archive --format=tar -o base4c.tar 4ee41b7a ; tar -xf ...
（head 3814 文件 / base 3812 文件；+2 = 两个新增测试文件，自洽）

head @9cdcd43e：
(1) gofmt -l utils/gitinfo.go utils/gitinfo_test.go sandbox/windowssandbox/process_windows.go \
            sandbox/windowssandbox/console_mode_windows_test.go appserver/realtime_runtime.go \
            appserver/realtime_transcript_tail_test.go
    -> 输出为空, exit=0
(2) gofmt -l .   -> 32 行（与既有基线一致，非本批引入）
(3) go build ./...  -> 输出为空, exit=0
(4) go vet ./utils/ ./sandbox/windowssandbox/ ./appserver/ ./rollout/  -> 无输出, exit=0
(5) go test ./utils/ ./rollout/ ./sandbox/windowssandbox/... ./realtime/ -count=1
    head: ok utils 1.362s | ok rollout 4.848s | ok sandbox/windowssandbox 2.912s
          | ok …/bin/command_runner 0.175s | ok …/bin/command_runner/win 0.267s
          | ok …/bin/setup_main 0.211s | ok …/bin/setup_main/win 0.403s
          | ok …/conpty 0.398s | ok …/elevated 0.334s | ok …/unified_exec 0.282s
          | ok …/unified_exec/backends 0.197s | ok …/wfp 0.268s | ok realtime 3.427s   -> exit=0
    base: 同集合全 ok -> exit=0
(6) go test ./appserver/ -run 'RealtimeTranscriptTail|Guardian|ExecutorSkill|ShellSnapshot' -count=1
    head: FAIL 4 条 | base: FAIL 4 条（逐条相同）
      TestExecutorSkillPathEqualsMatchesWindowsIdentityLikeRust
      TestShellSnapshotCommandMetricsLikeRust
      TestShellSnapshotCommandMetricsReportProtectedAndUnavailableLikeRust（+protected/unavailable 2 子项）
      TestShellSnapshotCommandMetricsSkipNonDirectLaunchesLikeRust
    => **新增失败 0**（均为既有平台基线；本 -run 子集不含 MultiAgentWaitDuration / RequiredSkillsPreSampling）
(7) $env:CODEX_RUST_ROOT="C:\rw\codex-rs"; go test ./parity/ -count=1  -> ok  codex_go/parity  51.567s
```
### §2.1 本批新增测试是否真的在跑（-v）

```
$ go test ./appserver/ -run 'TestRealtimeTranscriptTailFlush' -count=1 -v
--- PASS: TestRealtimeTranscriptTailFlushRecordsHistoryWithoutInferenceLikeRust (0.14s)
--- PASS: TestRealtimeTranscriptTailFlushJoinsActiveTurnWithoutSteeringLikeRust (0.41s)
ok  codex_go/appserver  0.853s
$ go test ./sandbox/windowssandbox/ -run 'TestSandboxConsoleFlagsLikeRust|TestPipedLegacySandboxSpawnRequestsNoConsoleWindow' -count=1 -v
--- PASS: TestSandboxConsoleFlagsLikeRust (0.00s)
--- PASS: TestPipedLegacySandboxSpawnRequestsNoConsoleWindow (0.01s)
ok  codex_go/sandbox/windowssandbox  0.052s
$ go test ./utils/ -run 'TestGitOriginURLFromDir|TestGitInfoUnresolvableGitfileLikeRust' -count=1 -v
--- PASS: TestGitOriginURLFromDirLikeRust (0.00s)
--- PASS: TestGitOriginURLFromDirLinkedWorktreeLikeRust (0.00s)
--- PASS: TestGitOriginURLFromDirSubmoduleLikeRust (0.00s)
--- PASS: TestGitInfoUnresolvableGitfileLikeRust (0.00s)
ok  codex_go/utils  0.043s
```
**8/8 新增测试 PASS**。

## §3 ③ 逐笔真实性核验（无夹带 / 无 CRLF）

`git show --stat` 原文（每笔恰为 2 文件，与声明逐一相符）：

```
--- 91260ed7 sync621: pin the sandbox console mode like Rust (#49308)
 sandbox/windowssandbox/console_mode_windows_test.go | 83 ++++++++++++++++++++++
 sandbox/windowssandbox/process_windows.go           | 36 ++++++++--
 2 files changed, 114 insertions(+), 5 deletions(-)

--- cd102426 sync622: read the origin URL through a .git gitfile like Rust
 utils/gitinfo.go      | 102 ++++++++++++++++++++++++++++++++++++++++++----
 utils/gitinfo_test.go |  99 ++++++++++++++++++++++++++++++++++++++++++++++++
 2 files changed, 193 insertions(+), 8 deletions(-)

--- 9cdcd43e sync623: persist the realtime transcript tail without inference like Rust (#50531)
 appserver/realtime_runtime.go              |  98 ++++++++++++++++--
 appserver/realtime_transcript_tail_test.go | 176 +++++++++++++++++++++++++++++
 2 files changed, 264 insertions(+), 10 deletions(-)
```

文件集一致性（**无夹带**）：

```
$ git show --name-only --format='' <c>   # 逐笔
91260ed7 => sandbox/windowssandbox/console_mode_windows_test.go | sandbox/windowssandbox/process_windows.go
cd102426 => utils/gitinfo.go | utils/gitinfo_test.go
9cdcd43e => appserver/realtime_runtime.go | appserver/realtime_transcript_tail_test.go
$ git diff --name-only 4ee41b7a 9cdcd43e
union_count=6  range_count=6  Compare-Object -> 无差异        # 三笔并集 == 区间文件集
```

CRLF 污染检查（blob 级，`\r` 计数）：

```
$ git grep -n -P '\r' 9cdcd43e -- <上述 6 文件>
(无输出, exit=1 = 无匹配)                       # 新增/改动文件均无 CR
$ git grep -n -P '\r' 4ee41b7a -- utils/gitinfo.go sandbox/windowssandbox/process_windows.go appserver/realtime_runtime.go
(无匹配, exit=1)                                # 既有文件同样干净
```

## §4 台账交叉核对（发现一处需你留意的语义定位）

```
update/plan_2026_10_07.md:2767
  - **N/A 8**：… · `#49261`/`#49308`/`#49325`（Go 已符合 / 已落 sync532 fe88a5a8） · …
update/r86_preflight_round3_2026_10_07.md:41
  #49308	50d9c5deac Run piped legacy Windows sandbox processes without a console (#49308)
update/queue_r86_2026_10_07.md:198
  | #50531 | `7d5f55bdad` | L(452) | core,app-server | … 「in-flight（他车道）」见 r86_preflight_round4:161
```

- **`#49308`（sync621）**：旧台账判 **N/A**（理由「Go 已符合 / 已落 sync532 `fe88a5a8`」）。实际 diff 复查：本笔把 piped 路径的 `CREATE_NO_WINDOW` 抽成 `ConsoleMode`/`SandboxConsoleFlags`（上游 `console_flags` 表形状），**piped 路径行为不变**；无 stdio 的第二条路径**保留** `CREATE_NO_WINDOW` 并显式注释「上游表在此给 0，Go 有意保留以防无控制台父进程给子进程新开控制台」。
  ⇒ 定性为**结构/表形状对齐 + 有据的显式偏离**，**不是行为修复**。**不构成「不实」**（提交信息只声称 "pin the sandbox console mode"，且确实引入该 API），但建议在发布说明里标为「结构对齐」，勿当作行为变更。
- **`#50531`（sync623）**：台账确有该 PR（Rust sha `7d5f55bdad`）；本笔为 `appserver/realtime_runtime.go` + 新增测试，与台账载体一致 ✅。
- **sync622（gitinfo gitfile）**：无 PR 号，台账无对应条目；本轮为新增能力 + 4 个新测试。

## §5 结论与告警

| 检查项 | 结果 |
|---|---|
| ① 远端对齐 + 无 merge + 快进 | ✅ main == integ86g == `9cdcd43e`；`--merges` 空；单亲线性 |
| tree-sha 握手 | ✅ 重放 tree `ccd7ed203840385ff96242453345816f3ab6588f` == tip tree |
| ② LF 门禁 | ✅ gofmt 空 / build 0 / vet 0 / 目标包全 ok / parity ok 51.567s / appserver 子集**新增失败 0** |
| ③ 逐笔真实性 | ✅ 3/3 只改声明文件、无夹带、无 CRLF 污染 |
| ④ 新增测试有效性 | ✅ 8/8 PASS（非空跑） |
| 告警 | **无**。仅一条**建议**：`#49308` 在发布说明标注为「结构对齐」（台账原判 N/A），避免被误读为行为修复 |

## §6 只读声明 / 资源

- 未 push、未 commit、未移动任何分支指针、未删远端 ref；未触碰他人 worktree。重放走 `cherry-pick -n`（零 commit）。
- 本轮 scratch：`codex_go_wt\aud4c_head` / `aud4c_base`（archive 解包 LF 树）、`aud4c_replay`（临时 worktree，**已 `git worktree remove --force` 清理**）、`head4c.tar` / `base4c.tar`。
- 累积未清 scratch（本机策略**拦截递归删除**，`Remove-Item -Recurse` 被 policy 拒）：`aud4b_head` / `aud4b_base` / `aud4b_probe` / `head.tar` / `base.tar` + 本轮 `aud4c_head` / `aud4c_base` / `head4c.tar` / `base4c.tar` —— 需你或后续有权限的步骤清理；全部可由 §7 命令重建。
- 产物：本文件（未跟踪，留队长提交）。

## §7 复跑命令

```
git ls-remote --heads origin main integ86g
git show -s --format=%T 9cdcd43e            # ccd7ed203840385ff96242453345816f3ab6588f
git diff --name-only 4ee41b7a 9cdcd43e
git grep -n -P '\r' 9cdcd43e -- <改动文件>  # 应无输出

git -c core.autocrlf=false -c core.eol=lf worktree add <dir> --detach 4ee41b7a
cd <dir>; git cherry-pick -n 91260ed7 cd102426 9cdcd43e ; git add -A ; git write-tree

git -c core.autocrlf=false -c core.eol=lf archive --format=tar -o head4c.tar 9cdcd43e
tar -xf head4c.tar -C <head_dir>
cd <head_dir>; gofmt -l <6 改动文件> ; gofmt -l . ; go build ./... ; go vet ./utils/ ./sandbox/windowssandbox/ ./appserver/ ./rollout/
go test ./utils/ ./rollout/ ./sandbox/windowssandbox/... ./realtime/ -count=1
go test ./appserver/ -run 'RealtimeTranscriptTail|Guardian|ExecutorSkill|ShellSnapshot' -count=1
$env:CODEX_RUST_ROOT="C:\rw\codex-rs"; go test ./parity/ -count=1
```