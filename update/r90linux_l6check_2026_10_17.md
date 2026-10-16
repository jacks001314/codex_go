# r90linux_l6check_2026_10_17 · mc-l90-node · tip `7eea017f`（L-6）Linux 真机复核（只读/只跑测试）

- lane：`mc-l90-node`（Linux 节点 `06aac1d3185c1999dee1e86482026deb` / 192.168.1.99）
- 触发：队长广播 R5-terdecies 第 11 次 push 声明 tip = `dfe110f6` 且「Go 树零变化、无需重跑」；本车道复测时远端已再前进 1 笔到 **`7eea017f`**，而该笔**确实改了 Go 树**（`sandbox/linuxsandbox/**`）⇒ 广播前提不再成立，故按车道职责在真机复跑 Linux-only 门禁。
- 性质：**只读 + 只跑测试**。零 `commit`/`push`/`tag`/`stash`/移分支/改 ref；零产品码与测试码改动（修复意见仅以 `文件:行号` 形式报告，不改）。

## 0. 结论

| 项 | 队长 L-6 声称 | 本车道实测 | 判定 |
|---|---|---|---|
| linuxsandbox 用例数 | 33 → **36 PASS**，无新增失败 | **36 PASS / 0 FAIL / 0 SKIP**（`go test -v -p 1 ./sandbox/linuxsandbox/ -count=1`，EXIT=0） | **一致** |
| 新增用例 | R3 scoped-read parity | 新增 3 条：`TestLinuxBwrapScopedReadRootsUseTmpfsRootLikeRust`、`TestLinuxBwrapPlatformDefaultReadRootsUseTmpfsRootLikeRust`、`TestLinuxBwrapScopedReadRootsEndToEndLikeRust`（33 + 3 = 36） | **一致** |
| 无新增失败 | — | `./sandbox/...` 失败集合与既有基线**逐名相同**（见 §3） | **一致** |
| 平台默认读根表与 Rust 一致 | 新增该表 | Rust `LINUX_PLATFORM_DEFAULT_READ_ROOTS`（`bwrap.rs:53-62`，8 项）与 Go `linuxPlatformDefaultReadRoots`（`linux.go:481-490`）**逐项逐序相同** | **一致**（§5） |
| `--tmpfs /` + scoped `--ro-bind` | 上移 Rust R3 | Rust pin 同语义存在于 `linux-sandbox/src/bwrap.rs:392-407`（doc）+ `:520/:791/:840` | **一致**（§5） |
| 「R1/R2/full-write argv 逐字节不变」 | 声称不变 | **未独立重导**（见 §5 末） | **unverified** |

⇒ **L-6 的 Linux-only 门禁声称在真机上复现成立**；本 tip 的 Go 树相对 `15aef9b7` 只动 `sandbox/linuxsandbox/{linux.go,linux_test.go}`，`appserver`/`exec`/`memories`/`network` 子树哈希与 `15aef9b7` 相同，故本车道 S-9/S-11 的其余结论**不受影响**。

## 1. tip 与新增内容（原文）

```
$ git fetch origin main
   dfe110f6..7eea017f  main       -> origin/main          FETCH_EXIT=0   # 直连，未走代理
$ git rev-parse origin/main
7eea017f7f69752b5e29e4d56d755957e9e8064f
$ timeout 25 git ls-remote origin main
7eea017f7f69752b5e29e4d56d755957e9e8064f	refs/heads/main   LSREMOTE_EXIT=0

$ git log --oneline 3f3c44f7..7eea017f | wc -l
15
$ git log --format='%h' 3f3c44f7..7eea017f | tr '\n' ' '
7eea017f dfe110f6 43e8d1b3 137d53b2 791cca8d 15aef9b7 5608e096 5d740a02 5248c35e e786ba0c 4db410ba 89a19fb7 6ee30f8d b33306fa e908a6b4
```

```
$ git show --stat --oneline 7eea017f
7eea017f round 90 (lead): land L-6 upstream R3 scoped-read parity - when the restricted profile has no readable root, mount an empty
tmpfs root plus per-root read-only binds (and re-issue --proc under it), add the platform default read-root table, and keep the
R1/R2/full-write argv byte-identical (linuxsandbox 33 -> 36 PASS, no new failures)
 sandbox/linuxsandbox/linux.go         | 143 +++++++++++++++++++-----
 sandbox/linuxsandbox/linux_test.go    | 168 ++++++++++++++++++++++++++++++
 update/r90linux_impl_l6_2026_10_17.md | 205 ++++++++++++++++++++++++++++++++++
 3 files changed, 488 insertions(+), 28 deletions(-)
```

**Go 树不再「零变化」**（这是广播 #11 前提失效之处）：

```
$ git diff --name-only dfe110f6 7eea017f -- . ':(exclude)update'
sandbox/linuxsandbox/linux.go
sandbox/linuxsandbox/linux_test.go
$ git diff --stat 15aef9b7 7eea017f -- sandbox/linuxsandbox/
 sandbox/linuxsandbox/linux.go      | 143 ++++++++++++++++++++++-------
 sandbox/linuxsandbox/linux_test.go | 168 ++++++++++++++++++++++++++++++
 2 files changed, 283 insertions(+), 28 deletions(-)
$ for p in appserver exec memories network; do printf '%-10s %s\n' "$p" "$(git rev-parse 7eea017f:$p)"; done
appserver  a1c3fd7c91ea0a09f73e8036b518aad1316106c1     # == 15aef9b7 侧，未变
exec       93c5fbdd1213b327a1ba24ca3f1c6342c007ee44     # == 15aef9b7 侧，未变
memories   4a35af2811419c1c34624e7485df7cd89089c102     # == 15aef9b7 侧，未变
network    67ef37751c32b01f8f05e07651113da571a40bcc     # == 15aef9b7 侧，未变
# sandbox 子树由 b5011edf… 变为 824cb011f0f842ec265d8040a12be9ffd5f58b4d（因 linuxsandbox 改动）
```

## 2. 台面与前置门禁

```
$ git worktree add --detach /tmp/l90/r90l9 7eea017f
Preparing worktree (detached HEAD 7eea017f)     WT_EXIT=0
$ cd /tmp/l90/r90l9 && git status --porcelain   # （空，无未提交改动）
$ gofmt -l sandbox/linuxsandbox/linux.go sandbox/linuxsandbox/linux_test.go
                                                GOFMT_EXIT=0   # 两文件 LF 镜像、已格式化
$ go vet ./sandbox/...                          VET_EXIT=0
$ go build ./...                                BUILD_EXIT=0   ELAPSED=14s
```

## 3. `./sandbox/...` 全包（既有失败集合未变）

```
$ go test -p 1 ./sandbox/... -count=1
ok    codex_go/sandbox                                     0.021s
--- FAIL: TestContentsRespectWriteGrantsAndCarveoutsLikeRust (contents_test.go:106, ×2 子用例)
--- FAIL: TestContentsFollowLinksAndResolvedWriteGrantsLikeRust (contents_test.go:189, ×1 子用例)
FAIL  codex_go/sandbox/integrity                          0.006s
ok    codex_go/sandbox/linuxsandbox                        0.109s
ok    codex_go/sandbox/sandboxpath / windowssandbox / … (其余 11 包全 ok)
--- FAIL: TestRuntimePathsIncludeAppRootAndPrimaryRuntimeRoot (setup_runtime_bin_test.go:44)
FAIL  codex_go/sandbox/windowssandbox/bin/setup_main/win   0.005s
--- FAIL: TestCanonicalPermissionProfileRoundTripIgnoresSymbolicSlashTmpOnWindows (ipc_framed_test.go:144)
FAIL  codex_go/sandbox/windowssandbox/elevated             0.005s
SANDBOX_ALL_EXIT=1  ELAPSED=7s
```

⇒ 失败集合与本车道既有基线**逐名相同**（`sandbox/integrity` ENOTDIR ×3 + `setup_main/win` ×1 + `elevated` ×1，均为**存量**、非本波引入）⇒ **L-6 新增失败 = 0**。

## 4. linuxsandbox 详单与 e2e 探针

```
$ go test -v -p 1 ./sandbox/linuxsandbox/ -count=1
LSB_EXIT=0  ELAPSED=1s
top-level PASS=36  FAIL=0  SKIP=0        # 含子用例共 51 个 PASS 标记

$ go test -v -p 1 -run 'TestLinuxBwrapScopedReadRootsEndToEndLikeRust|TestLinuxBwrapTwoHalfManagedGrantEndToEnd' ./sandbox/linuxsandbox/ -count=1
E2E_EXIT=0   PASS=5 FAIL=0 SKIP=0
=== RUN   TestLinuxBwrapTwoHalfManagedGrantEndToEnd/vm
        codex-seccomp-probe sandbox_pid=1
    linux_test.go:990: vm: open(/dev/vsock) inside the sandbox = errno 2 (host open succeeds)
=== RUN   TestLinuxBwrapTwoHalfManagedGrantEndToEnd/proxy
        codex-seccomp-probe sandbox_pid=1
    linux_test.go:990: proxy: open(/dev/vsock) inside the sandbox = errno 2 (host open succeeds)
=== RUN   TestLinuxBwrapTwoHalfManagedGrantEndToEnd/proxy-grant
        codex-seccomp-probe sandbox_pid=1
    linux_test.go:990: proxy-grant: open(/dev/vsock) inside the sandbox = errno 2 (host open succeeds)
--- PASS: TestLinuxBwrapTwoHalfManagedGrantEndToEnd (0.07s)   # vm / proxy / proxy-grant 3 子用例
=== RUN   TestLinuxBwrapScopedReadRootsEndToEndLikeRust
        codex-seccomp-probe sandbox_pid=1
    linux_test.go:1350: scoped-read-roots: /home inside the sandbox = errno 2
--- PASS: TestLinuxBwrapScopedReadRootsEndToEndLikeRust (0.02s)
```

`sandbox_pid=1` 由沙箱内探针自报 ⇒ errno 读数为**沙箱内**结果（非宿主探针误读）；新增的 scoped-read e2e 亦复现「受限根下 `/home` 不可见（ENOENT）」。

36 个顶层 PASS 全名（按输出顺序）：`TestCreateCommandArgsWithSandboxExeOverride` … `TestLinuxBwrapScopedReadRootsEndToEndLikeRust`（完整清单见命令日志；与 `15aef9b7` 的 33 条基线相比，**唯一增量 = 上述 3 条新用例，无删改**）。

## 5. 与 Rust pin 的独立 parity 抽查（只读参照，未 fetch）

参照：`/home/jacks/jacks_dev/codex`，HEAD = **`4eb4a65407d98cebc4c83bf13be440b9fbc78a7c`**（本车道未 fetch、未改配置）。

```
# Rust：平台默认读根表（bwrap.rs:53-62）
$ sed -n '50,62p' codex-rs/linux-sandbox/src/bwrap.rs
const LINUX_PLATFORM_DEFAULT_READ_ROOTS: &[&str] = &[
    "/bin", "/sbin", "/usr", "/etc", "/lib", "/lib64", "/nix/store", "/run/current-system/sw",
];
# Go：linux.go:478-490
var linuxPlatformDefaultReadRoots = []string{
	"/bin", "/sbin", "/usr", "/etc", "/lib", "/lib64", "/nix/store", "/run/current-system/sw",
}
```
⇒ **8 项、顺序、字面量全部一致**（Go 侧注释亦正确标注了 `bwrap.rs:53-62`）。

其余抽查：
- Rust `include_platform_defaults` 定义于 `codex-rs/protocol/src/permissions.rs:962` ✔（Go 注释引用 `permissions.rs:962-974` 属实）。
- Rust 用 `LINUX_PLATFORM_DEFAULT_READ_ROOTS` 的位置：`bwrap.rs:533-538`，并在 `options.inherit_pid_namespace` 时复用调用方 procfs ✔（与 Go 注释语义一致）。
- `--tmpfs /` + scoped `--ro-bind` 的 Rust doc：`bwrap.rs:392-407`；实际 `--tmpfs` 推入点 `bwrap.rs:520 / :791 / :840` ✔。
- **低危 nit（注释行号偏 8 行，无行为影响）**：Go `linux.go:1010` 注释写「upstream mounts the fresh procfs after the filesystem arguments (`bwrap.rs:377-381`)」，但 Rust 中该 fresh `--proc /proc` 推入点是 **`bwrap.rs:369-371`**（`create_bwrap_flags`，位于 `create_filesystem_args` 结果 `args.extend` 之后，:337-339）；`377-381` 实为 `--chdir` 段。语义描述正确，仅锚点漂移。按只读纪律**只报告、不修改**。
- **unverified**：「R1/R2/full-write argv 与上游逐字节相同」未独立重导（需要在 Rust 侧生成 argv 与 Go 侧对拍，不在本单范围）。

## 6. 结论强度自评

- **verified**：tip 三值核对；`7eea017f` 改动的文件集合与 stat；`gofmt`/`go vet`/`go build` 三项 exit 0；`./sandbox/...` 失败集合逐名不变；linuxsandbox **36 PASS / 0 FAIL / 0 SKIP**；e2e 5 个 PASS 与 `sandbox_pid=1` + errno 探针原文；读根表 Rust↔Go 逐项一致。
- **likely**：L-6 的「无新增失败」对本 tip 全仓成立（仅依据 `./sandbox/...` 与子树哈希，未跑其它包）。
- **unverified**：「R1/R2/full-write argv 逐字节不变」；`./appserver/` 在 `7eea017f` 上的重跑（本批未做，`appserver` 子树与 `15aef9b7` 哈希相同，故按 §0 推断不受影响）。

## 7. 未决与清理清单

未决：① 广播 #11 声明「Go 树零变化、无需 repoint」在 `7eea017f` 上**已失效**，请队长按新 tip 更新口径（本文件即该复跑结果）；② `linux.go:1010` 注释锚点漂移（低危，待队长决定是否修）；③ 存量项仍待口径：`sandbox/integrity` ENOTDIR ×3、两条缺 GOOS 门禁的 Windows-only 用例、`memories/version_test.go:169-181` 硬编码路径；④ 主工作区外部 `pull` 归属待确认（本单**未动**主工作区）。

清理：本单自建临时物 = 2 个日志文件 `/tmp/_l90l6_lsb.log`、`/tmp/_l90l6_e2e.log`（内容已全文摘入本文件 §4）—— 见回包中的删除复核。
**保留**：新台面 `/tmp/l90/r90l9` @ `7eea017f`（按纪律保留待回收令）；此前 7 个台面（`r90l1`/`r90l3`=`3f3c44f7`、`r90l4`/`r90l5`=`e786ba0c`、`r90l6`=`5d740a02`、`r90l7`=`5608e096`、`r90l8`=`15aef9b7`）未移动。他车道 `/tmp/_l90i_*`、`/tmp/l90i/*` **未触碰**。
