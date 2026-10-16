# r90 · Linux 节点车道 · S-1 对齐回执（sync update）

- lane: `mc-l90-node`（node id `06aac1d3185c1999dee1e86482026deb`, linux_node, 192.168.1.99）
- 触发单: `msg-1792191546272153500-2213`（S-1 更新：队长已 push 成功 → 各车道改为对齐）
- 日期: 2026-10-17（时区 Asia/Shanghai）
- 权威 tip（本单核对结果）: `e786ba0c709cef5d314e32fb20bfaf54e7df5dbe`
- 本单性质: 同步 + 通道探测（**不改任何产品码**）；附带把 L-3 补丁在权威 tip 上复验（§6，只读 + 新建台面）

## 0. 一句话结论

本节点 **github 443 直连完全可用**（与队长侧相反），`git fetch origin main` 直连成功（EXIT=0），本地 `origin/main` 已是 `e786ba0c`，要求的 6 笔提交逐条核对通过；**未执行 push**（前置②实测不成立 + 授权已作废）；**bundle 通道本单不需要**，且经核查 Master 上**不存在任何 `.bundle`**（§3.5）。顺带在 `e786ba0c` 上复验 L-3 补丁：clean 应用、4 条新用例全 PASS、无新增失败（§6）。

## 1. 判定表

| # | 项 | 命令（原文） | 结论 | 证据锚点 |
|---|---|---|---|---|
| 1 | TCP 443 | `timeout 10 bash -c '</dev/tcp/github.com/443'; echo "TCP443_EXIT=$?"` | TCP443_EXIT=0（通） | §2 |
| 2 | ls-remote | `timeout 20 git -C /home/jacks/jacks_dev/codex_go ls-remote origin main` | `e786ba0c…  refs/heads/main`，EXIT=0 | §2 |
| 3 | 通道选择 | `ss -ltnp \| grep -E ':(7890\|7897\|1080\|10809\|8889)'` | 只有 `127.0.0.1:10808`(xray)，**未配置任何 git 代理**；走**直连** | §3.1 |
| 4 | fetch | `git -C /home/jacks/jacks_dev/codex_go fetch origin main` | EXIT=0，`* branch main -> FETCH_HEAD` | §3.2 |
| 5 | 核对 origin/main | `git rev-parse origin/main` | `e786ba0c709cef5d314e32fb20bfaf54e7df5dbe` | §3.2 |
| 6 | 对象类型 | `git cat-file -t e786ba0c709cef5d314e32fb20bfaf54e7df5dbe` | `commit` | §3.3 |
| 7 | 提交数 | `git log --oneline 3f3c44f7..e786ba0c \| wc -l` | 恰好 6 笔，名字逐个匹配 | §3.3 |
| 8 | 4db410ba 位置 | `git merge-base --is-ancestor 4db410ba e786ba0c` | YES（已是祖先 ⇒ 推它只会被 non-fast-forward 拒） | §3.3 |
| 9 | bundle 通道 | `project_read update/r90_sync_3f3c44f7_to_e786ba0c.bundle` / `project_list glob=**/*.bundle` | `path not found` / `entries: []` ⇒ **Master 上无 bundle** | §3.5 |
| 10 | push | （未执行） | **未尝试**：前置②不成立 + 授权已作废 | §3.4 |
| 11 | 台面 sha | `git worktree list` | l1/l3/l2@`3f3c44f7`，l4/l5@`e786ba0c` | §4 |
| 12 | 主工作区卫生 | `git status --porcelain` / `git status -sb` | 仅 `?? scripts/loc_report.sh`；`## main...origin/main` 无 ahead/behind | §5 |
| 13 | L-3 在新 base 的可并入性 | `git apply --check /tmp/l90/r90l3.patch`（cwd=r90l4@e786ba0c） | EXIT=0，两条 cleanly；blob sha256 与 L-3 报告一致 | §6 |
| 14 | Linux 沙箱门禁 @e786ba0c | `go test -p 1 ./sandbox/... -count=1` | EXIT=1：11 包 ok，**3 包红（全部 base 无关）**；`linuxsandbox` 26/26 PASS | §8 |
| 15 | OTel 用例性质更正 | `go test -run '^TestOtelProviderReloadsAfterAccountChange$' -count=3` | pristine 与 patched **两侧同为 2/3 红** ⇒ flake，非"稳定红" | §7 |

## 2. Task 1 · 通道探针（原文 + exit code）

```
$ timeout 10 bash -c '</dev/tcp/github.com/443'; echo "TCP443_EXIT=$?"
TCP443_EXIT=0

$ timeout 20 git -C /home/jacks/jacks_dev/codex_go ls-remote origin main; echo "LSREMOTE_EXIT=$?"
e786ba0c709cef5d314e32fb20bfaf54e7df5dbe	refs/heads/main
LSREMOTE_EXIT=0
```

结论：本节点 443 直连可用（**与队长侧的"直连被阻断"结论相反**，这正是我在 S-1 里报过的"git-over-https 到 github 通"）；ls-remote 已直接给出队长 push 后的权威 tip。

## 3. Task 2/3 · 对齐动作（选了通道 1，直连）

### 3.1 代理端口探测（确认我没走代理）

```
$ ss -ltnp | grep -E ':(7890|7897|1080|10809|8889)'
LISTEN 0      4096       127.0.0.1:10808      0.0.0.0:*    users:(("xray",pid=337460,fd=4))

$ git -C /home/jacks/jacks_dev/codex_go config --get-regexp 'http.*proxy'
(no http proxy config)
```

说明：本机只有 `127.0.0.1:10808`（xray）在听，不是队长侧的 7897；**我没有配置任何 `http.https://github.com/.proxy`**，因为直连已通（通道 1 优先）。

### 3.2 fetch（原文）

```
$ timeout 60 git -C /home/jacks/jacks_dev/codex_go fetch origin main 2>&1; echo "FETCH_EXIT=$?"
From https://github.com/jacks001314/codex_go
 * branch              main       -> FETCH_HEAD
FETCH_EXIT=0

$ git -C /home/jacks/jacks_dev/codex_go rev-parse origin/main
e786ba0c709cef5d314e32fb20bfaf54e7df5dbe
$ git -C /home/jacks/jacks_dev/codex_go rev-parse FETCH_HEAD
e786ba0c709cef5d314e32fb20bfaf54e7df5dbe
```

`.git/FETCH_HEAD` mtime = `2026-10-17 07:01:14.709828403 +0800`（即本次 fetch）。

### 3.3 必须回报的核对（原文）

```
$ git cat-file -t e786ba0c709cef5d314e32fb20bfaf54e7df5dbe
commit

$ git log --oneline 3f3c44f7..e786ba0c
e786ba0c round 90 (lead): runbook R5-terdecies section 8 - the real cause of the github failures on this box (git ignores the WinINET proxy on 127.0.0.1:7897) and the scoped-proxy fix (ls-remote 1.2s after configuring it)
4db410ba round 90 (lead): runbook R5-terdecies - mandatory cross-node pull broadcast after every successful push, the incremental git-bundle fallback channel, and the precondition-bound push authorization for lanes
89a19fb7 round 90 (lead): land wave-90 batch three - seven lanes plus the Linux seccomp window
6ee30f8d round 90 (lead): book the L-2 dispatch of #52610 in the queue note (Linux-node sandbox naming and seccomp semantics audit)
b33306fa round 90 (lead): salvage V-b15's independent verification evidence from its worktree (18710 B, sha256 A4F3B75E, byte-identical) during the stale-worktree audit
e908a6b4 round 90 (lead): runbook R5-undecies/R5-duodecies - node-id lookup discipline, cross-node lane rules and the Linux node profile

$ git log --oneline 3f3c44f7..e786ba0c | wc -l
6

$ git rev-parse e786ba0c^{commit}
e786ba0c709cef5d314e32fb20bfaf54e7df5dbe

$ git merge-base --is-ancestor 4db410ba e786ba0c && echo "YES (ancestor)"
YES (ancestor)
```

⇒ **恰好 6 笔、逐名匹配**（`e908a6b4 / b33306fa / 6ee30f8d / 89a19fb7 / 4db410ba / e786ba0c`），与派工单要求的序列一致。

### 3.4 Task 3 · push：**未执行**

- 前置① `git cat-file -t 4db410ba` = `commit`：成立（本次虽未单跑该命令，但 §3.3 的 6 笔日志证明该对象在库内且可解析；`merge-base --is-ancestor 4db410ba e786ba0c` = YES 亦证明其存在）。
- 前置② `ls-remote origin main` 现值 == `3f3c44f74a81d2b8c61cd765a5f95842c077ac68`：**实测 = `e786ba0c…`，不成立**。
- 前置③ 命令形状：未构造（因②不成立）。
- 另：`4db410ba` 已是 `e786ba0c` 的祖先，把它推到 `main` 会 non-fast-forward，只能靠 `--force` —— 而 `--force` 被禁。
⇒ **未尝试 push**（回包口径：`未尝试（前置不成立 + 授权已作废）`）。也**未删除任何 bundle**。

### 3.5 bundle 通道（核查结论：Master 上不存在）

```
project_read update/r90_sync_3f3c44f7_to_e786ba0c.bundle
→ rpc error: code = NotFound desc = path not found

project_list path="" glob="**/*.bundle"
→ {"entries":[]}

project_status
→ hydratedFiles:5, untrackedFiles:0, masterReachable:true, "本地镜像与 Master 一致"
```

- 旧路径 `update/r90_sync_3f3c44f7_to_4db410ba.bundle` 在上一单 S-1 里我报过**同样不存在**；本单新路径 `…_to_e786ba0c.bundle` 也**不存在**。项目内 `**/*.bundle` 全空。
- ⇒ 我**没有**核 SHA-256（无从核）、没有 `bundle verify`、没有 `fetch <file>`、没有删/留动作（无可删）。
- 请队长复核上传是否真的成功（可能是上传未落地或落在别的路径）；**因为直连已通，本单不需要 bundle，不算阻塞**。

## 4. 各台面现状（sha）

```
$ git -C /home/jacks/jacks_dev/codex_go worktree list | grep -E 'l90'
/tmp/_l90i_tip                                     5248c35e (detached HEAD)   ← 他车道
/tmp/l90/r90l1                                     3f3c44f7 (detached HEAD)   ← 我的 L-1 台面（未动）
/tmp/l90/r90l3                                     3f3c44f7 (detached HEAD)   ← 我的 L-3 台面（含未提交改动，未动）
/tmp/l90/r90l4                                     e786ba0c (detached HEAD)   ← 本单新建（L-3 新 base，已应用补丁）
/tmp/l90/r90l5                                     e786ba0c (detached HEAD)   ← 本单新建（pristine 对照）
/tmp/l90i/r90l2                                    3f3c44f7 (detached HEAD)   ← 他车道
/tmp/l90i/r90l3                                    e786ba0c (detached HEAD)   ← 他车道（已自行对齐到新 tip）
```

- 主工作区 `/home/jacks/jacks_dev/codex_go` = `e786ba0c [main]`。
- **未对任何在跑台面做 `checkout`/`reset`**；两个新台面都用 `worktree add --detach` 建。
- `git worktree list | wc -l` = 44 行，`git worktree list --porcelain | grep -c prunable` = **0**（L-3 期已 prune：171/173 行 → 40 行，133 prunable → 0）。
- 不在我写面内的 `/tmp/l90i/*`、`/tmp/_l90i_*`、`/tmp/_l90i_base|r90l3|tip` 是他车道**活跃**产物（mtime 07:04–07:05），**一律未动**。

## 5. 主工作区卫生（原文）

```
$ git -C /home/jacks/jacks_dev/codex_go status --porcelain
?? scripts/loc_report.sh

$ git -C /home/jacks/jacks_dev/codex_go status -sb
## main...origin/main
?? scripts/loc_report.sh

$ git -C /home/jacks/jacks_dev/codex_go diff --stat
(空)
```

- 唯一未跟踪文件 `scripts/loc_report.sh` **早于本批存在**（不是我建的；我也没删它——不删他人产物）。
- reflog 最近两条：`06:58:37 e786ba0c HEAD@{0}: pull origin main: Fast-forward`、`06:33:55 3f3c44f7 HEAD@{1}: pull origin main: Fast-forward` ⇒ **外部 pull 两次顶起主工作区，不是我执行的**（我本批只做 `fetch`，`fetch` 不改工作区/本地分支）。已在 L-0/L-1/S-1 报过，这里再确认一次。
- 本批我**零 commit / 零 push / 零 tag / 零 stash / 零分支移动 / 零产品码落到主工作区**。

## 6. 附带复验 · L-3 补丁在权威 tip `e786ba0c` 上（只读 + 新台面）

### 6.1 base 无关性（为什么这次复验就够）

```
$ git diff --stat 3f3c44f7 e786ba0c -- appserver/turn_runtime.go
(空)
```

⇒ L-3 的目标文件在两个 base 之间**逐字节相同**；`89a19fb7`（wave-90 批次）没有碰 `appserver/turn_runtime.go`（它动的是 `appserver/workspace_routing.go`、`appserverdaemon/*`、`network/proxy_policy.go`、`sandbox/linuxsandbox/*`、`tui/*`）；`4db410ba`/`e786ba0c` 只改 `update/leader_runbook_2026_10_07.md`。

### 6.2 应用 + 静态门禁（cwd = `/tmp/l90/r90l4`，HEAD = e786ba0c）

```
$ git apply --check -v /tmp/l90/r90l3.patch; echo "APPLYCHECK_EXIT=$?"
Checking patch appserver/turn_runtime.go...
Checking patch appserver/environment_context_time_like_rust_test.go...
APPLYCHECK_EXIT=0

$ git apply -v /tmp/l90/r90l3.patch; echo "APPLY_EXIT=$?"
Applied patch appserver/turn_runtime.go cleanly.
Applied patch appserver/environment_context_time_like_rust_test.go cleanly.
APPLY_EXIT=0

$ gofmt -l appserver/turn_runtime.go appserver/environment_context_time_like_rust_test.go
(空, GOFMT_EXIT=0)

$ sha256sum appserver/turn_runtime.go appserver/environment_context_time_like_rust_test.go
1aff003e62672f64ed6c07644f7a7079380063782c8e335b98e678a526ddb55b  appserver/turn_runtime.go
6f6c120fd7e9a084c4d2524f8580069236ae79b7c483cada0840e827db63cf6a  appserver/environment_context_time_like_rust_test.go

$ git hash-object appserver/turn_runtime.go appserver/environment_context_time_like_rust_test.go
41ee31f8de85e41fd04b9987c52a05a2312e53c1
89a531b621d4e122d2bbe33408992e25f5451f30

$ git status --porcelain; git diff --stat e786ba0c
 M appserver/turn_runtime.go
?? appserver/environment_context_time_like_rust_test.go
 appserver/turn_runtime.go | 23 ++++++++++++++++++-----
 1 file changed, 18 insertions(+), 5 deletions(-)
```

⇒ blob 的 sha256 与 git 对象 hash **与 L-3 报告（`update/r90linux_l3_2026_10_17.md`）逐字节一致**，说明补丁在权威 tip 上重建出的就是那一份改动。

### 6.3 新用例 4/4 PASS（原文）

```
$ cd /tmp/l90/r90l4/appserver && env -u TERM go test . -run 'TestEnvironmentContextTime|TestCompactEnvironmentContextHonors' -count=1 -v
=== RUN   TestEnvironmentContextTimeEnabledWritesDateAndTimezoneLikeRust
--- PASS: TestEnvironmentContextTimeEnabledWritesDateAndTimezoneLikeRust (0.00s)
=== RUN   TestEnvironmentContextTimeDisabledOmitsDateAndTimezoneLikeRust
--- PASS: TestEnvironmentContextTimeDisabledOmitsDateAndTimezoneLikeRust (0.00s)
=== RUN   TestEnvironmentContextTimeDefaultsTrueLikeRust
--- PASS: TestEnvironmentContextTimeDefaultsTrueLikeRust (0.00s)
=== RUN   TestCompactEnvironmentContextHonorsIncludeEnvironmentContextTimeLikeRust
--- PASS: TestCompactEnvironmentContextHonorsIncludeEnvironmentContextTimeLikeRust (0.00s)
PASS
ok  	codex_go/appserver	0.038s
TEST_EXIT=0      # 4 条 --- PASS
```

### 6.4 构建 + vet @e786ba0c

```
$ cd /tmp/l90/r90l4 && go build ./... ; echo "BUILD_EXIT=$?"
BUILD_EXIT=0        BUILD_ELAPSED=10s

$ cd /tmp/l90/r90l4 && go vet ./appserver/... ; echo "VET_EXIT=$?"
VET_EXIT=0
```

### 6.5 appserver 整包 before/after（同一计数法，CWD=包内，5 个 `CODEX_RUST_*` env 全设，`env -u TERM`，`-count=1 -p 1 -v`）

| 台面 | base | L-3 补丁 | RUN | PASS | FAIL | SKIP | 耗时 | FAIL 集合 |
|---|---|---|---|---|---|---|---|---|
| r90l5 | e786ba0c | 否（pristine） | 1882 | 1876 | 5 | 1 | 56s | 4 稳定红 + **TestOtelProviderReloadsAfterAccountChange** |
| r90l4 | e786ba0c | 是 | 1886 | 1881 | 4 | 1 | 33s | 4 稳定红（逐名相同） |

- 4 条稳定红（两个台面逐名相同，也与 L-1/L-3 逐名相同）：
  `TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust`
  `TestPluginContentHashMatchesRustBundleFingerprintLikeRust`
  `TestPluginListHonorsPerRepositoryConfigLikeRust`
  `TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust`
- 唯一 SKIP：`TestServerRequestBrokerIntersectsPermissionsApprovalLikeRust`（本机 `CODEX_RUST_*` 已全设，仍是 1 条；L-1 报过"设 env 后 SKIP 12→1"）。
- 差值：RUN +4、PASS +5、FAIL −1 —— 即 4 条新用例全绿，另有 1 条原本红的 flake 这一轮转绿（见 §7）。
- **计数法声明（务必注意，勿与 L-1/L-3 的数字直接相减）**：上表是**顶层用例**口径（把 `=== RUN` / `--- PASS|FAIL|SKIP` 行去掉 `/子用例` 后缀后去重计数；本包另有 **587 条子用例全部 PASS**，子用例 `--- PASS` 行是缩进的）。L-1/L-3 报的 `RUN=2459 / 2463` 是**含子用例行**的口径。r90l4 在含子用例口径下为 `RUN=2473`。两套口径各自自洽（顶层：1881+4+1=1886=RUN；含子用例：子用例 587 条 RUN/PASS 成对）。

## 7. 更正（重要）· `TestOtelProviderReloadsAfterAccountChange` 是 flake，不是"稳定红"

我在 L-3 报告里写过它"**隔离 3/3 稳定红 ⇒ 非 flake**"。**本批的新证据推翻该表述**，特此更正：

```
# pristine e786ba0c (r90l5) 全包运行
1980:=== RUN   TestOtelProviderReloadsAfterAccountChange
1981-    otel_reloader_test.go:103: the telemetry exporters were not rebuilt after the account change
1982:--- FAIL: TestOtelProviderReloadsAfterAccountChange (5.01s)

# 同一 pristine 台面，隔离 -count=3
=== RUN   TestOtelProviderReloadsAfterAccountChange
--- PASS: TestOtelProviderReloadsAfterAccountChange (0.05s)
=== RUN   TestOtelProviderReloadsAfterAccountChange
    otel_reloader_test.go:103: the telemetry exporters were not rebuilt after the account change
--- FAIL: TestOtelProviderReloadsAfterAccountChange (5.00s)
=== RUN   TestOtelProviderReloadsAfterAccountChange
    otel_reloader_test.go:103: the telemetry exporters were not rebuilt after the account change
--- FAIL: TestOtelProviderReloadsAfterAccountChange (5.01s)

# 已应用 L-3 补丁的 r90l4，同样隔离 -count=3
--- PASS: TestOtelProviderReloadsAfterAccountChange (0.05s)
--- FAIL: TestOtelProviderReloadsAfterAccountChange (5.01s)
--- FAIL: TestOtelProviderReloadsAfterAccountChange (5.01s)
```

- **pristine 与 patched 表现完全相同（2/3 红）** ⇒ 与本单 L-3 改动**无关**。
- 红的时候恒为 5.00s 整（等待窗口超时），绿的时候 0.05s ⇒ 5s 等待窗口内的**时序竞态**，支持队长把它列进 flake 名单。**队长 flake 名单成立；我的"非 flake"表述作废。**
- 对判红口径的意义：Linux 上"第 5 条红"是**轮转的 flake 槽**——L-1 那轮是 `TestExistingThreadLoadsAPICuratedMCPAfterAuthSwitchSync`，本轮 pristine 是 OTel 这条。**稳定红恒为那 4 条**。

## 8. 附带结论 · Linux 真机 `./sandbox/...` @`e786ba0c`

```
$ cd /tmp/l90/r90l4 && env -u TERM go test -p 1 ./sandbox/... -count=1 ; echo "SANDBOX_EXIT=$?"
SANDBOX_EXIT=1        SANDBOX_ELAPSED=6s
11 个包 ok；3 个包 FAIL
```

三条红（原文）：

```
--- FAIL: TestContentsRespectWriteGrantsAndCarveoutsLikeRust (0.00s)
    --- FAIL: TestContentsRespectWriteGrantsAndCarveoutsLikeRust/narrow_write_grant (0.00s)
        contents_test.go:106: NewFileContentsChecker(): not a directory
    --- FAIL: TestContentsRespectWriteGrantsAndCarveoutsLikeRust/overlapping_write_grants (0.00s)
        contents_test.go:106: NewFileContentsChecker(): not a directory
--- FAIL: TestContentsFollowLinksAndResolvedWriteGrantsLikeRust (0.00s)
    --- FAIL: TestContentsFollowLinksAndResolvedWriteGrantsLikeRust/link_is_the_writable_root (0.00s)
        contents_test.go:189: NewFileContentsChecker(): not a directory
FAIL	codex_go/sandbox/integrity	0.008s

--- FAIL: TestRuntimePathsIncludeAppRootAndPrimaryRuntimeRoot (0.00s)
    setup_runtime_bin_test.go:44: runtimePaths() = []string{"C:\\Users\\user\\AppData\\Local/OpenAI/Codex", "C:\\Users\\user/.cache/codex-runtimes"}, want []string{"C:\\Users\\user\\AppData\\Local\\OpenAI\\Codex", "C:\\Users\\user\\.cache\\codex-runtimes"}
FAIL	codex_go/sandbox/windowssandbox/bin/setup_main/win	0.007s

--- FAIL: TestCanonicalPermissionProfileRoundTripIgnoresSymbolicSlashTmpOnWindows (0.00s)
    ipc_framed_test.go:144: round-trip profile = &sandbox.PermissionProfile{...}
FAIL	codex_go/sandbox/windowssandbox/elevated	0.007s
```

- ① `sandbox/integrity` = L-0/L-1 已报的 **ENOTDIR**（非目录根 + `ReadOnlySubpaths` 未清，`os.IsNotExist` 之外的真错误被当致命）；**未修**（无授权）。
- ②③ 两条是**名字里就写着 Windows 的用例却没做 GOOS 门禁**（`setup_runtime_bin_test.go`、`ipc_framed_test.go` 里 `grep -n GOOS` 无命中），在 Linux 上断言 `\` 分隔符 ⇒ 必红。这两条是**真正的"Windows-only 用例未跳过"实例**（与队长要找的"Windows 上被 SKIP / Linux 上必须真跑"互为镜像）。
- **base 无关性验证**：

```
$ git diff --stat 3f3c44f7 e786ba0c -- sandbox/
 sandbox/linuxsandbox/linux.go      |  61 +++++++-
 sandbox/linuxsandbox/linux_test.go | 292 +++++++++++++++++++++++++++++++++++++
 2 files changed, 346 insertions(+), 7 deletions(-)

$ cd /tmp/l90/r90l1 && go test ./sandbox/integrity ./sandbox/windowssandbox/bin/setup_main/win ./sandbox/windowssandbox/elevated -count=1
FAIL  codex_go/sandbox/integrity ...
FAIL  codex_go/sandbox/windowssandbox/bin/setup_main/win ...
FAIL  codex_go/sandbox/windowssandbox/elevated ...
OLDBASE_EXIT=1
```

⇒ 同一 3 包在旧 base `3f3c44f7` 上**同样 3 红** ⇒ 这 3 红是**存量问题**，不是 wave-90 引入。

- **亮点（本波唯一改动的 Linux 包）**：

```
$ cd /tmp/l90/r90l4/sandbox/linuxsandbox && env -u TERM go test . -count=1 -v
LINUXSANDBOX_EXIT=0     --- PASS = 26,  --- FAIL = 0,  --- SKIP = 0
```

⇒ `sandbox/linuxsandbox` 在 `e786ba0c` 上 **26 PASS / 0 FAIL / 0 SKIP**（L-1 在 `3f3c44f7` 是 20 PASS）——`89a19fb7` 新增的 6 条 **Linux seccomp window** 用例在真机上全绿。

## 9. 结论强度自评

**verified（有原始输出直接支撑）**
1. 本节点 443 直连可用 + ls-remote 返回 `e786ba0c`（§2）；
2. `fetch origin main` 直连 EXIT=0，`origin/main` = `FETCH_HEAD` = `e786ba0c`（§3.2）；
3. `cat-file -t` = `commit`；`3f3c44f7..e786ba0c` 恰好 6 笔且逐名匹配（§3.3）；
4. **未 push**、未删任何 bundle（§3.4/§3.5，且 Master 上压根没有 bundle —— `NotFound` + glob 空）；
5. L-3 补丁在 `e786ba0c` 上 `apply --check`/`apply` clean、`gofmt -l` 空、blob sha256 与 L-3 报告一致、4 条新用例全 PASS、`go build ./...` EXIT=0/10s、`go vet ./appserver/...` EXIT=0（§6）；
6. 整包 before/after：pristine 1882/1876/5/1 vs +L-3 1886/1881/4/1，4 条稳定红逐名相同、新增失败 0（§6.5）；
7. `./sandbox/...` @e786ba0c EXIT=1 = 3 包红 + 11 包绿；同 3 包在 `3f3c44f7` 亦红（存量）；`linuxsandbox` 26/26（§8）；
8. OTel 用例在 pristine/patched 两侧同为 2/3 红（§7）；
9. 主工作区无 tracked 改动、无 ahead/behind（§5）。

**likely（现象充分但未读实现根因）**
1. OTel 那条是 5s 等待窗口的时序竞态（依据：两侧同现、红恒 5.00s、绿恒 0.05s）；
2. `sandbox/integrity` 的 ENOTDIR 根因（沿用 L-1 的代码读取结论：`contents.go:52-66` / `sandboxpath.go:83-90` / `contents.go:124-133`），本轮只复现了症状、未重读代码。

**unverified（我不该当成事实）**
1. 队长侧 bundle 为何没落到 Master（我只能证明"Master 上没有该文件"）；
2. `./cli`、`./tui/...`、`appserver` 的其它子包、`-race` 全未跑；
3. `3f3c44f7..e786ba0c` 期间 appserver 其它文件的行为差异（本单只针对 `turn_runtime.go` 与 sandbox 做了 base 对比）；
4. `TestExistingThreadLoadsAPICuratedMCPAfterAuthSwitchSync`（L-1 的 flake）本轮整包未复现，但**未单独隔离跑** ⇒ 仍按 flake 对待，不当作已消除；
5. Linux 上那 4 条稳定红的根因（只知逐轮稳定、逐名相同）。

## 10. 未决与阻塞

1. **bundle 通道空**：`update/r90_sync_3f3c44f7_to_e786ba0c.bundle`（及上一版的 `…_to_4db410ba.bundle`）在 Master 上都不存在。请复核上传；我这边不删任何 bundle（本单也无可删）。
2. **`refs/heads/probe-origin-main` → `e786ba0c`**：这是我 L-0/S-1 期建的**只读** ref，现在与 `origin/main` 同值、已无用途。删 ref 属敏感动作，**等你批准我再删**（不自行执行）。
3. **外部 pull 两次顶起主工作区**（reflog `06:33:55 → 3f3c44f7`、`06:58:37 → e786ba0c`，均为 `pull origin main: Fast-forward`）：**不是我的动作**（我本批只 `fetch`）。请确认是你或其他 lane 的有意行为。
4. **L-3 可并入**：补丁在 `e786ba0c` 上 clean 应用且门禁全绿（§6）。请给合并回执；合并后我可在并入后的新 tip 上再跑一轮门禁。
5. 待授权修法（我不改产品码）：`sandbox/integrity` 的 ENOTDIR；`sandbox/windowssandbox/bin/setup_main/win` 与 `…/elevated` 两条缺 GOOS 门禁的用例。
6. 仍未裁定：`exec/exec.go:2213-2228 execEnvironmentContext` 是**第二个**无条件写 `<current_date>`/`<timezone>` 的点（不在 L-3 写面内，其 `exec/exec_test.go:1193` 现在断言 `<current_date>` 存在）。
7. 插件指纹跨平台口径（L-1 发现：向量把 Windows `\` 烘死）仍待上游确认。

## 11. 清理清单

- **本批新建台面（保留）**：`/tmp/l90/r90l4`（`e786ba0c` + L-3 补丁，作为 L-3 的新 base 台面）、`/tmp/l90/r90l5`（`e786ba0c` pristine，对照/后续基线）。
- **保留（未动）**：`/tmp/l90/r90l1`、`/tmp/l90/r90l3`（含未提交改动）、`/tmp/l90/r90l3.patch`；他车道的 `/tmp/l90i/r90l2`、`/tmp/l90i/r90l3`、`/tmp/_l90i_tip`、`/tmp/_l90i_*`。
- **本批删除（我自己建的，删前 `realpath` 校验为 `/tmp/_l90_*` 并逐条打印）**：11 项 —— `/tmp/_l90_l4test.log`、`/tmp/_l90_l4sandbox.log`、`/tmp/_l90_l4lsb.log`、`/tmp/_l90_l4app.log`、`/tmp/_l90_l5app.log`、`/tmp/_l90_run.txt`、`/tmp/_l90_res.txt`、`/tmp/_l90_rt.txt`、`/tmp/_l90_PASS.txt`、`/tmp/_l90_FAIL.txt`、`/tmp/_l90_SKIP.txt`。删除后 `ls /tmp/_l90_*` = 无匹配。
- **未动**：`/tmp/_l90i*`（他车道活跃产物，mtime 07:04–07:05）、`/home/jacks/jacks_dev/codex`（Rust 上游，只读参照）、主工作区的 `scripts/loc_report.sh`（他人文件）。
- **边界自检**：零 commit / 零 push / 零 tag / 零 stash / 零分支移动 / 零 ref 变更 / 零产品码写回主工作区；未派生任何下级 Agent；未对在跑台面 `checkout`/`reset`。
