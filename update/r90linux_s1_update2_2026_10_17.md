# r90 · Linux 节点车道 · S-1 对齐回执（补遗 update2）

- lane: `mc-l90-node`（node id `06aac1d3185c1999dee1e86482026deb`, linux_node, 192.168.1.99）
- 触发: 同一条 S-1 更新单（`msg-1792191546272153500-2213`）的 pending 提醒 + 期间远端又前进了两笔
- 本文件是 `update/r90linux_s1_update_2026_10_17.md` 的**增量补遗**（前文结论仍有效，此处只写状态变化与新证据）
- 观测时刻: 2026-10-17 07:06:54 +0800 → 07:11 +0800

## 0. 状态变化（三件大事）

1. **远端又前进了两笔**：`e786ba0c` → `5248c35e` → **`5d740a02`**（本次实测权威 tip）。
2. **我的 L-3 已被队长合并落地**：`5248c35e` = “land #52164 environment-context time switch (**Linux lane L-3**) and #53506 duplicate-Host regression guard (A-b16)”——且**逐字节一致**（§2）。
3. **主工作区被第三次外部 pull 顶起**（`5248c35e HEAD@{0}: pull origin main: Fast-forward`，约 07:06），**不是我执行的**（我本批只 `fetch`）。

## 1. 对齐动作（仍然走通道 1 直连）

```
$ timeout 60 git -C /home/jacks/jacks_dev/codex_go fetch origin main 2>&1; echo "FETCH_EXIT=$?"
From https://github.com/jacks001314/codex_go
 * branch              main       -> FETCH_HEAD
   5248c35e..5d740a02  main       -> origin/main
FETCH_EXIT=0

$ git -C /home/jacks/jacks_dev/codex_go rev-parse origin/main
5d740a0214e50f12aae8f0aa32c32c0914351198

$ timeout 20 git -C /home/jacks/jacks_dev/codex_go ls-remote origin main; echo "LSREMOTE_EXIT=$?"
5d740a0214e50f12aae8f0aa32c32c0914351198	refs/heads/main
LSREMOTE_EXIT=0

$ git log --oneline 3f3c44f7..origin/main
5d740a02 round 90 (lead): land #52610 residual - the dangerous unix-socket grant now reaches seccomp (Linux lane L-4) plus a real-bwrap two-half e2e
5248c35e round 90 (lead): land #52164 environment-context time switch (Linux lane L-3) and #53506 duplicate-Host regression guard (A-b16)
e786ba0c round 90 (lead): runbook R5-terdecies section 8 - the real cause of the github failures on this box (git ignores the WinINET proxy on 127.0.0.1:7897) and the scoped-proxy fix (ls-remote 1.2s after configuring it)
4db410ba round 90 (lead): runbook R5-terdecies - mandatory cross-node pull broadcast after every successful push, the incremental git-bundle fallback channel, and the precondition-bound push authorization for lanes
89a19fb7 round 90 (lead): land wave-90 batch three - seven lanes plus the Linux seccomp window
6ee30f8d round 90 (lead): book the L-2 dispatch of #52610 in the queue note (Linux-node sandbox naming and seccomp semantics audit)
b33306fa round 90 (lead): salvage V-b15's independent verification evidence from its worktree (18710 B, sha256 A4F3B75E, byte-identical) during the stale-worktree audit
e908a6b4 round 90 (lead): runbook R5-undecies/R5-duodecies - node-id lookup discipline, cross-node lane rules and the Linux node profile
count=8
```

- 通道 = **直连**（本节点 443 可用，未配代理；同 §2 of update1）。
- `cat-file -t e786ba0c709cef5d314e32fb20bfaf54e7df5dbe` = `commit`（复测）。
- **push：仍未执行**（本单无 push 授权，且授权已作废）；未删任何 bundle。

## 2. 我的 L-3 落地核对（逐字节）

```
$ git show --stat --oneline 5248c35e
5248c35e round 90 (lead): land #52164 environment-context time switch (Linux lane L-3) and #53506 duplicate-Host regression guard (A-b16)
 .../environment_context_time_like_rust_test.go     | 167 ++++++++
 appserver/turn_runtime.go                          |  23 +-
 network/proxy_duplicate_host_like_rust_test.go     | 253 +++++++++++
 update/leader_runbook_2026_10_07.md                |   8 +
 update/r90_commit_ledger_2026_10_16.tsv            |   3 +-
 update/r90_dispatch_queue_2026_10_17.tsv           |   4 +-
 update/r90a_2026_10_16.md                          | 136 ++++++
 update/r90linux_l3_2026_10_17.md                   | 467 +++++++++++++++++++++
 update/r90linux_l3_2026_10_17.patch                | 229 ++++++++++
 update/r90linux_s1_2026_10_17.md                   | 127 +++++++++
 10 files changed, 1409 insertions(+), 8 deletions(-)

$ # 我的补丁重建出的 blob（update1 §6.2）
$ echo "mine: 41ee31f8de85e41fd04b9987c52a05a2312e53c1  appserver/turn_runtime.go"
$ echo "mine: 89a531b621d4e122d2bbe33408992e25f5451f30  appserver/environment_context_time_like_rust_test.go"

$ git rev-parse 5248c35e:appserver/turn_runtime.go
41ee31f8de85e41fd04b9987c52a05a2312e53c1          # == 我的
$ git rev-parse 5248c35e:appserver/environment_context_time_like_rust_test.go
89a531b621d4e122d2bbe33408992e25f5451f30          # == 我的

$ git rev-parse 5d740a02:appserver/turn_runtime.go
41ee31f8de85e41fd04b9987c52a05a2312e53c1
$ git rev-parse 5d740a02:appserver/environment_context_time_like_rust_test.go
89a531b621d4e122d2bbe33408992e25f5451f30

$ diff <(git show 5248c35e:appserver/environment_context_time_like_rust_test.go) /tmp/l90/r90l4/appserver/environment_context_time_like_rust_test.go && echo TESTFILE_IDENTICAL
TESTFILE_IDENTICAL
```

⇒ **L-3 落地与我的交付物逐字节相同**（工作区文件、blob hash 双证），且到 `5d740a02` 仍未被改动。队长同时把我的两份产物（`update/r90linux_l3_2026_10_17.md` / `.patch`）与 `update/r90linux_s1_2026_10_17.md` 一并入库。

## 3. 合并后 tip `5d740a02` 的 Linux 真机门禁（本批新证据）

### 3.1 构建

```
$ cd /tmp/l90/r90l6 && go build ./... ; echo "BUILD_EXIT=$?"
BUILD_EXIT=0        BUILD_ELAPSED=15s
```

### 3.2 `./sandbox/...`（L-4 的落点，Linux 独有）

```
$ cd /tmp/l90/r90l6 && env -u TERM go test -p 1 ./sandbox/... -count=1 ; echo "SANDBOX_EXIT=$?"
SANDBOX_EXIT=1      SANDBOX_ELAPSED=7s
ok_pkgs=11   FAIL_pkgs=3
FAIL	codex_go/sandbox/integrity	0.008s
FAIL	codex_go/sandbox/windowssandbox/bin/setup_main/win	0.004s
FAIL	codex_go/sandbox/windowssandbox/elevated	0.009s
--- FAIL: TestContentsRespectWriteGrantsAndCarveoutsLikeRust（+ 2 子用例，contents_test.go:106 not a directory）
--- FAIL: TestContentsFollowLinksAndResolvedWriteGrantsLikeRust/link_is_the_writable_root（contents_test.go:189 not a directory）
--- FAIL: TestRuntimePathsIncludeAppRootAndPrimaryRuntimeRoot（setup_runtime_bin_test.go:44，Windows 分隔符）
--- FAIL: TestCanonicalPermissionProfileRoundTripIgnoresSymbolicSlashTmpOnWindows（ipc_framed_test.go:144）
ok  	codex_go/sandbox	0.020s
```

⇒ **与 `e786ba0c` 上的 3 红逐名完全相同**（`integrity` ENOTDIR ×3 + 两条缺 GOOS 门禁的 Windows 用例）；`5d740a02` 改了 `sandbox/run.go`、`sandbox/linuxsandbox/*`，但 `codex_go/sandbox` 与 `codex_go/sandbox/linuxsandbox` **都是 ok** ⇒ **L-4 没有在 Linux 上引入新红**。

### 3.3 `sandbox/linuxsandbox`（L-4 主战场：seccomp + 真 bwrap e2e）

```
$ cd /tmp/l90/r90l6/sandbox/linuxsandbox && env -u TERM go test . -count=1 -v ; echo "LSB_EXIT=$?"
LSB_EXIT=0
RUN=46（含子用例）  PASS=31（顶层）  FAIL=0  SKIP=0
```

31 条顶层用例**全部真跑、全部 PASS、零 SKIP**（对比：`3f3c44f7` 时 20 PASS、`e786ba0c` 时 26 PASS）。其中 L-4 新增的 Linux-only 用例逐条 `--- PASS`：

```
--- PASS: TestLinuxSeccompFilterMatchesUpstreamRustSemantics (0.01s)
--- PASS: TestLinuxSeccompProbeChildProcess (0.00s)
--- PASS: TestLinuxSandboxE2EProbeProcess (0.00s)
--- PASS: TestLinuxBwrapTwoHalfManagedGrantEndToEnd (0.04s)      ← “真 bwrap 两半 e2e”
--- PASS: TestCreateCommandArgsManagedNetworkContextEnablesProxyMode (0.00s)
--- PASS: TestParseBwrapHelpLikeRust (0.00s)
--- PASS: TestSelectBwrapLauncherLikeRust (0.00s)
--- PASS: TestAppendUnreadableRootBwrapArgsUsesDistinctDescriptors (0.00s)
--- PASS: TestLinuxNetworkSeccompModeForProfile (0.00s)
```

（本机 `bwrap --version` = 0.11.0，LSM 含 landlock，`unprivileged_userns_clone=1`；这些用例在 Windows 节点上编译/运行都是另一回事——**只有本节点能给出这个真结论**。）

### 3.4 appserver 整包（合并后 in-situ 复核，5 个 `CODEX_RUST_*` env + `env -u TERM`，`-count=1 -p 1 -v`，顶层用例口径）

```
$ cd /tmp/l90/r90l6/appserver && env -u TERM go test . -count=1 -p 1 -v ; echo "APPSERVER_EXIT=$?"
APPSERVER_EXIT=1        ELAPSED=48s
TOP-LEVEL: RUN=1886 PASS=1880 FAIL=5 SKIP=1
--- FAIL: TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust
--- FAIL: TestOtelProviderReloadsAfterAccountChange          ← flake 槽（见 update1 §7）
--- FAIL: TestPluginContentHashMatchesRustBundleFingerprintLikeRust
--- FAIL: TestPluginListHonorsPerRepositoryConfigLikeRust
--- FAIL: TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust
--- SKIP: TestServerRequestBrokerIntersectsPermissionsApprovalLikeRust
--- PASS: TestEnvironmentContextTimeEnabledWritesDateAndTimezoneLikeRust
--- PASS: TestEnvironmentContextTimeDisabledOmitsDateAndTimezoneLikeRust
--- PASS: TestEnvironmentContextTimeDefaultsTrueLikeRust
--- PASS: TestCompactEnvironmentContextHonorsIncludeEnvironmentContextTimeLikeRust
```

⇒ 4 条稳定红**逐名相同**、无新增失败；我的 4 条 L-3 用例**在合并后的树里真跑且全 PASS**（不再是"补丁台面"上的结论）。

## 4. 台面与卫生

```
$ git -C /home/jacks/jacks_dev/codex_go worktree list | grep -E '/tmp/l90|/tmp/l90i'
/tmp/l90/r90l1   3f3c44f7 (detached)   ← L-1 台面（未动）
/tmp/l90/r90l3   3f3c44f7 (detached)   ← L-3 原台面（未动）
/tmp/l90/r90l4   e786ba0c (detached)   ← L-3 新 base 台面（未动）
/tmp/l90/r90l5   e786ba0c (detached)   ← pristine 对照（未动）
/tmp/l90/r90l6   5d740a02 (detached)   ← 本批新建（合并后 tip）
/tmp/l90i/r90l2  3f3c44f7  /  /tmp/l90i/r90l3  e786ba0c   ← 他车道（未动）
```

- 主工作区：`## main...origin/main`，未跟踪文件仅 `?? scripts/loc_report.sh`（他人文件，未动）。
- reflog 近三条（全部 `pull origin main: Fast-forward`，**均非我执行**）：`5248c35e`(07:06 观测) / `e786ba0c` / `3f3c44f7`。
- `refs/heads/probe-origin-main` 仍停在 `e786ba0c`（已过时）；**删它属敏感动作，等队长批准**。
- 本批我：零 commit / 零 push / 零 tag / 零 stash / 零分支移动 / 零产品码写回；未对在跑台面 `checkout`/`reset`。

## 5. 未决（较 update1 的更新）

1. ~~L-3 待合并~~ → **已合并**（`5248c35e`），逐字节核对通过（§2）。
2. `update/r90_sync_3f3c44f7_to_e786ba0c.bundle` 在 Master 上**仍不存在**（`**/*.bundle` 全空）⇒ 请复核上传；我未删任何 bundle。
3. 外部 pull 已**三次**顶起主工作区（`3f3c44f7` → `e786ba0c` → `5248c35e`）：请确认归属。
4. `sandbox/integrity` ENOTDIR ×3 与两条缺 GOOS 门禁的 Windows 用例（现仍在 `5d740a02` 上红）**无人认领**，修法待授权。
5. `exec/exec.go:2213-2228 execEnvironmentContext` 仍是第二个无条件写 `<current_date>/<timezone>` 的点（L-3 未覆盖该写面）。
6. 插件指纹跨平台口径（向量把 Windows `\` 烘死）待上游确认。

## 6. 结论强度

- **verified**：直连 fetch 到 `5d740a02`（`5248c35e..5d740a02`，EXIT=0）；L-3 落地 blob 与我交付物逐字节相同（双 hash + diff）；合并后 `5d740a02` 上 `linuxsandbox` 31/31 PASS/0 SKIP（含真 bwrap e2e）；`./sandbox/...` 3 红与 `e786ba0c` 逐名相同（无新红）；appserver 1886/1880/5/1 且 4 稳定红逐名相同、L-3 4 用例 PASS；未 push、未删 bundle。
- **likely**：OTel 与 `integrity` 的根因（沿用前文分析，未重读实现）。
- **unverified**：bundle 未落地的队长侧原因；`./cli`、`./tui/...`、`-race` 未跑；`5248c35e` 里 A-b16 的 `network/proxy_duplicate_host_like_rust_test.go` 未单独复跑（本轮只随 appserver 整包跑，属通过集）。

## 7. 清理

- 本批新建台面 **保留**：`/tmp/l90/r90l6`（`5d740a02`，合并后 tip 台面）。
- 本批删除（我的临时物）：`/tmp/_l90_l6sb.log`、`/tmp/_l90_l6lsb.log`、`/tmp/_l90_l6app.log`、`/tmp/_l90_l6PASS.txt`、`/tmp/_l90_l6FAIL.txt`、`/tmp/_l90_l6SKIP.txt`（删前 `realpath` 校验为 `/tmp/_l90_*`）。
- **未动**：`/tmp/l90/{r90l1,r90l3,r90l3.patch,r90l4,r90l5}`、他车道 `/tmp/l90i/*` 与 `/tmp/_l90i_*`、`/home/jacks/jacks_dev/codex`（Rust 只读）。
