# 独立发布审计 — main @ `9fafc187`（A 批 3 笔 + Δ4 rebase 4 笔）

- **审计员**：synct5（Windows 节点；只读审计）
- **日期**：2026-10-07
- **被审对象**：`9fafc187052eeff1651dc22a995d943a692c3cea`（= `origin/integ86d`）
- **基线**：`ed6f52b7406055444a6ac3023788b8a0f588e6b2`
- **总判定：GREEN** —— tree-sha 握手一致；Δ4 内容保持性成立；门禁**新增失败 0**
- 告警条件（tree sha 不一致）**未触发**。全程**未 push / 未 commit / 未移动任何 ref**。

## §1 结构核对

```
$ git merge-base --is-ancestor 9c4d578f 9fafc187 ; echo $LASTEXITCODE
0
$ git show -s --format='%T | %P' 9fafc187
466b12d70487894fc974c6a0795742272d82fc1a | eb9d894c7b47b9b6d07855ad14f4597e1fe901ca     （单亲，无 merge）
$ git log --oneline 9c4d578f..9fafc187
9fafc187 syncw2: hide reasoning summaries in the /status card for server connections (#49145)
eb9d894c syncw2: report the configured TUI mode in doctor (#50200)
f15258cf syncw3: inherit only ready or starting environments when spawning subagents (#49075)
b2a153b0 syncw1: keep timestamp-only thread metadata observations narrow (#48983)
$ git diff --shortstat ed6f52b7 9fafc187
 19 files changed, 1887 insertions(+), 37 deletions(-)
```

`9fafc187^{tree}` = `466b12d70487894fc974c6a0795742272d82fc1a`，与队长给的**候选期望值完全相同**。
## §2 tree-sha 握手（两次重放，**零 commit**）

为不产生任何 commit / ref 移动，我用 `git cherry-pick -n`（只写 index+worktree）等价替代 `git rebase --onto`：rebase 即「逐笔 replay 同一 diff」，`-n` 得到同一裸树。

```
# 基线检出（LF）：git -c core.autocrlf=false -c core.eol=lf worktree add <dir> --detach ed6f52b7
apply syncl3b.patch       exit=0
apply syncl5b.patch       exit=0
apply syncl5_51185.patch  exit=0
$ git add -A ; git write-tree
7b091de54311546c4f52df6517e61380e4ca5d6a      <- tip A（= 期望 7b091de5…） OK
$ git cherry-pick -n b153fe59 ee19c38d de6ff942 1306de38    exit=0（无冲突）
$ git add -A ; git write-tree
466b12d70487894fc974c6a0795742272d82fc1a      <- tip B（= 期望 466b12d7…） OK
```

**断言成立**：`B^{tree}` == `9fafc187^{tree}` == 候选期望 `466b12d70487894fc974c6a0795742272d82fc1a`
⇒ **main 上的内容与「A 批 3 补丁 + Δ4 四笔」逐字节相同**。

Δ4 逐笔 patch-id 匹配（原始 dangling 提交 vs rebase 后提交，两侧完全相同）：

```
b153fe59 -> c6880369468286938f5c045c36a79fbcf6651d6c
b2a153b0 -> c6880369468286938f5c045c36a79fbcf6651d6c     (#48983)
ee19c38d -> b77b645e6aa2e5ca9684381dbf441dd058bc6e40
f15258cf -> b77b645e6aa2e5ca9684381dbf441dd058bc6e40     (#49075)
de6ff942 -> f6ea7051b863fe7a353b782776a494e50756d948
eb9d894c -> f6ea7051b863fe7a353b782776a494e50756d948     (#50200)
1306de38 -> 6e0974ff523ce893152c2022f7c250a79409baaf
9fafc187 -> 6e0974ff523ce893152c2022f7c250a79409baaf     (#49145)
```

## §3 Δ4 内容保持性（并修正一处描述）

```
$ git diff --stat 9fafc187 1306de38
 codemode/grpc_admission_test.go   | 182 ---------
 codemode/grpc_session_provider.go |  52 -----
 codemode/remote_provider.go       |  38 ---
 model/responses_agent.go          |  92 +----
 model/responses_agent_test.go     | 205 ----------
 prompt/skills_render.go           |  89 +----
 prompt/skills_render_test.go      | 153 --------
 7 files changed, 8 insertions(+), 803 deletions(-)
```

- 这 7 个文件是 **A 批**（prompt/ model/ codemode/）的文件 —— **不是**「batch-four 的 7 个文件」（队长 §1 的描述有误，但对结论无害）。
- 成因：`1306de38` 建自 `ed6f52b7 + Δ4`，**从未包含 A 批**；因此两 tip 之差恰为 A 批内容。
- 关键正向证据：**Δ4 的 12 个文件（state 2 / appserver 4 / doctor 2 / tui 2 / tui-tea 2）在两 tip 间零差异** ⇒ rebase 未改变 Δ4 内容。
- 19 = A 批 7 + Δ4 12，与 §1 的 `19 files changed` 自洽。
## §4 独立门禁（LF 树；head=9fafc187 vs base=ed6f52b7）

```
(1) gofmt -l <19 个改动文件>   -> 输出为空, exit=0
(2) gofmt -l .（全树）         -> 32 == 32, 与基线 Compare-Object 无差异（既有基线）
(3) go build ./...             -> 输出为空, exit=0
(4) go vet ./prompt/ ./model/ ./codemode/ ./config/ ./appserver/ ./state/ ./doctor/ ./tui/ ./tui/tea/
    -> exit=1，仅 1 条且两侧同因（既有基线）：
       model/responses_agent.go:1448:11 assignment copies lock value to clone: ... contains sync.Mutex
       （基线同处为 :1370，行号随 #49675 插入约 78 行位移）
(5) $env:CODEX_RUST_ROOT="C:\rw\codex-rs"; go test ./parity/ -count=1   -> ok  codex_go/parity  34.508s
(6) 9 包整包对拍  go test <9 包> -count=1
    第 1 轮  head: ++TestSkillInstructionsSelectAppsTheyMentionLikeRust  （仅本轮多出 1 条）
    定性    该测试单独跑 `-run -count=3`：head 3/3 PASS、base 3/3 PASS
    第 2 轮  head appserver FAIL 6 条 == base appserver FAIL 6 条（逐条相同）
    ⇒ 该条为 **flaky**（包内并发/环境干扰），**非新增失败**
    两侧稳定失败集 11 条：
      prompt    : TestCollectExplicitSkillMentions
                  TestCollectExplicitSkillMentionsResolvesStructuredPathsLikeRust
                  TestDetectImplicitSkillInvocationForCommandMatchesPathIdentityLikeRust
      appserver : TestExecutorSkillPathEqualsMatchesWindowsIdentityLikeRust
                  TestMultiAgentWaitDurationRecordsOutcomeLikeRust
                  TestRequiredSkillsPreSamplingValidationReleasesSatisfiedTurnsLikeRust
                  TestShellSnapshotCommandMetricsLikeRust
                  TestShellSnapshotCommandMetricsReportProtectedAndUnavailableLikeRust（含 protected/unavailable 2 子项）
                  TestShellSnapshotCommandMetricsSkipNonDirectLaunchesLikeRust
      state     : TestOpenRuntimeDBValidatesBeforeMigrateLikeRust
      tui/tea   : TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge
    model / codemode / config / doctor / tui 两侧均 ok
    => 新增失败 0
```

## §5 与前提的偏差（需注意）

1. 本次 `git fetch origin --prune` **失败**：`fatal: unable to access 'https://github.com/jacks001314/codex_go.git/': Recv failure: Connection was reset`（网络，非权限）。故远端口径以**本会话内最后一次成功 fetch** 为准。
2. 该口径下 **`origin/main` 已不是 `9fafc187`**：`origin/main = 937836c5a6dc53cccbd39dfd996f8c619b90a5b9`（在 9fafc187 之后又有 `d5dbe694`/`c84a80ab` #49269、`84518d3f` #49852、`937836c5` #48982 等）。`origin/integ86d = 9fafc187` 正确。
   ⇒ 「main 现为 9fafc187」这一前提**再次偏旧**（连续多轮同现象）；被审对象 `9fafc187` 自身已验证 GREEN。
3. `C:\rw\codex-rs` HEAD = `5b0b2530354052b9194156d70d4c94a439368342`，工作树干净，符合 parity 必须用 LF 检出的规程。
4. 提示：队长「Windows 基线 9 条」表只覆盖 state/appserver/execserver；本次 9 包口径下实际稳定失败为 **11 条**（另含 prompt 3、tui/tea 1、appserver 6、state 1）。建议按包补齐基线表。

## §6 只读声明 / 资源

- **未** push / commit / ref 移动；本文件为未跟踪产物，留队长提交。
- 本轮新增 LF 审计树：`…\codex_go_wt\audit-head2`（9fafc187）。
- 另有 `audit-base`(ed6f52b7) / `audit-head`(9c4d578f) / `audit-replay`(ed6f52b7+A 批+cherry-pick -n)，以及先前的 `base-gate`/`integ86-gate`/`integ86b-gate` —— 全部**待 GO-C，暂缓清理**。

## §7 复跑命令

```
git fetch origin --prune
git merge-base --is-ancestor 9c4d578f 9fafc187 ; echo $LASTEXITCODE
git show -s --format='%T' 9fafc187          # 466b12d70487894fc974c6a0795742272d82fc1a
git -c core.autocrlf=false -c core.eol=lf worktree add <dir> --detach ed6f52b7
cd <dir>; git apply ...\r86_patches\syncl3b.patch ; git apply ...\syncl5b.patch ; git apply ...\syncl5_51185.patch
          git add -A ; git write-tree          # 7b091de54311546c4f52df6517e61380e4ca5d6a
          git cherry-pick -n b153fe59 ee19c38d de6ff942 1306de38 ; git add -A ; git write-tree
                                               # 466b12d70487894fc974c6a0795742272d82fc1a
$env:CODEX_RUST_ROOT="C:\rw\codex-rs"; go test ./parity/ -count=1
go test ./prompt/ ./model/ ./codemode/ ./config/ ./appserver/ ./state/ ./doctor/ ./tui/ ./tui/tea/ -count=1
```