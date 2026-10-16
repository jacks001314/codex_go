# r90 · Linux 节点画像 + 可行性侦察（L-0 派工单交付）

- 车道：mc-l90-node（Linux 节点车道）
- 节点：id=`06aac1d3185c1999dee1e86482026deb` name=`linux_node`（192.168.1.99，os=linux/amd64）
- 侦察时点：2026-10-17 06:30–06:36 CST，机器 uptime 10:51，load avg 0.02/0.05/0.06
- 仓库根（Linux 侧，均已存在且自行验证）：`/home/jacks/jacks_dev/codex_go`（Go 移植仓）、`/home/jacks/jacks_dev/codex`（Rust 上游）
- 本单为只读侦察：**未改任何产品代码、未建/未删 worktree、未 commit/push/tag/stash、未派生 Agent**。
- 唯一交付物 = 本文件。

---

## 1. 判定表（侦察项 → 结论 → 证据锚点）

| # | 侦察项 | 结论 | 证据锚点 |
|---|--------|------|----------|
| 1 | 机器画像 | Kali GNU/Linux Rolling 2026.1，kernel 6.18.12+kali-amd64 x86_64；16 核 / 31.1 GiB RAM / 32 GiB swap / /home 所在卷 603 GB 可用 | 本文件 §2.1 |
| 2 | 工具链 | go1.26.3 linux/amd64；git 2.51.0；**bwrap 0.11.0 已装**；unshare(util-linux 2.41.3)、zsh 5.9、python3、gcc、make 均在；**powershell / jq 缺失** | 本文件 §2.2 |
| 3 | 仓库可达性 | ✅ **两个仓都存在且确认为正主**：`codex_go` = Go 移植仓（module `codex_go`，remote `github.com/jacks001314/codex_go.git`），HEAD=`09d31d67` **干净**（仅 1 个未跟踪文件）；`codex` = Rust 上游，HEAD=`4eb4a65407` **与队长 pin 完全一致**，工作区干净 | 本文件 §2.3 |
| 3b | 与队长时点差异 | ⚠️ **Linux 侧 `codex_go` 落后队长**：本地 HEAD=`09d31d67`（round 88，2026-10-07），队长给的 `7f8cad04`(Windows 未 push main) 与 `63e3e264`(origin/main) **本地都不存在** | 本文件 §2.3.2 |
| 3c | 远端真实 main | `git ls-remote origin main` = `63e3e2649f9be323a7114d81c8c582a1f2429b3b`，**与队长所述 origin/main 完全一致** → 本地 origin/main 只是 Oct-07 的陈旧 remote-tracking ref | 本文件 §2.3.2 |
| 4 | 构建可行性 | ✅ `go build ./...` **EXIT=0，43 秒，无 OOM**（31 GiB RAM 充裕） | 本文件 §2.4 |
| 5a | Linux-only 门禁：sandbox 编译 | ✅ `go test ./sandbox/linuxsandbox/ -count=1` → **ok，20/20 PASS**（Windows 上这条因 `probeBwrapHelp` 未定义而编译失败；该符号定义在 linux-only 文件 `bwrap_linux.go`） | 本文件 §2.5 |
| 5b | 沙箱后端 | ✅ `bwrap --version` = **bubblewrap 0.11.0**；真实冒烟 `bwrap --ro-bind / / --dev /dev --unshare-all --die-with-parent /bin/true` → **EXIT=0** | 本文件 §2.5 |
| 5c | landlock / userns | ✅ LSM 列表含 **landlock**；`unprivileged_userns_clone=1`，`max_user_namespaces=126482` | 本文件 §2.5 |
| 5d | Windows-SKIP 的 symlink 用例在 Linux 真跑 | ✅ `TestResolveSymlinkWritePathsCycleOnUnix`(utils) 与 `TestEditorDirectoryRejectsSymlink`(tui/tea) 均 **PASS**；全仓 **58** 个 test 文件含 `runtime.GOOS == "windows"` 跳过分支 | 本文件 §2.5 |
| 6 | 网络 | 🟡 **模块：goproxy.cn 通(200)，proxy.golang.org / github.com 的 curl 均超时(000)**；**Git：`git ls-remote` 走 https 到 github 成功(4.7s)** → 依赖下载可走 goproxy.cn，git fetch/clone 到 github 可用 | 本文件 §2.6 |

---

## 2. 命令原文 + exit code + 关键输出原文

> 约定：命令按原样粘贴（含 `$?`/`${pipestatus[n]}` 采集）；输出为原文摘录。临时物仅 `/tmp/_l90_build.log`。

### 2.1 机器画像

```
$ uname -a
Linux jacks 6.18.12+kali-amd64 #1 SMP PREEMPT_DYNAMIC Kali 6.18.12-1kali1 (2026-02-25) x86_64 GNU/Linux
   EXIT=0

$ cat /etc/os-release
PRETTY_NAME="Kali GNU/Linux Rolling"
NAME="Kali GNU/Linux"
VERSION_ID="2026.1"
VERSION="2026.1"
VERSION_CODENAME=kali-rolling
ID=kali
ID_LIKE=debian
   EXIT=0

$ nproc
16
   EXIT=0

$ free -m
               total        used        free      shared  buff/cache   available
Mem:           31838        3953       25336          12        3175       27885
Swap:          32504           0       32504
   EXIT=0

$ df -h /home
Filesystem      Size  Used Avail Use% Mounted on
/dev/sda2       884G  236G  603G  29% /
   EXIT=0

$ uptime
 06:30:36 up 10:51, 5 users,  load average: 0.02, 0.05, 0.06
   EXIT=0

$ id
uid=1000(jacks) gid=1000(jacks) groups=1000(jacks),4(adm),20(dialout),24(cdrom),25(floppy),27(sudo),29(audio),30(dip),44(video),46(plugdev),100(users),101(netdev),102(scanner),112(bluetooth),117(lpadmin),119(wireshark),123(kaboxer)
   EXIT=0
```

结论：**16 核 / 31 GiB RAM / 32 GiB swap / 603 GB 空闲磁盘**，跑 `go build ./...` 与逐个 package 的真机门禁资源充足；用户在 `sudo` 组。

### 2.2 工具链

```
$ go version
go version go1.26.3 linux/amd64
   EXIT=0

$ go env GOPATH GOCACHE GOMODCACHE GOPROXY GOOS GOARCH GOVERSION
/home/jacks/go
/home/jacks/.cache/go-build
/home/jacks/go/pkg/mod
https://goproxy.cn
linux
amd64
go1.26.3
   EXIT=0

$ go env GOTOOLCHAIN GOFLAGS GONOSUMDB GOSUMDB GOPRIVATE
auto
(空 GOFLAGS)
(空 GONOSUMDB)
sum.golang.org
(空 GOPRIVATE)
   EXIT=0

$ git --version
git version 2.51.0
   EXIT=0

$ for b in bwrap unshare zsh python3 powershell gcc make jq; do which $b; done
bwrap:     /usr/bin/bwrap
unshare:   /usr/bin/unshare
zsh:       /usr/bin/zsh
python3:   /usr/bin/python3
powershell: powershell not found        <-- 缺失
gcc:       /usr/bin/gcc
make:      /usr/bin/make
jq:        jq not found                 <-- 缺失
   EXIT=0

$ zsh --version
zsh 5.9 (x86_64-debian-linux-gnu)
   EXIT=0

$ du -sh /home/jacks/go/pkg/mod
3.3G    /home/jacks/go/pkg/mod

$ ls -d vendor
ls: cannot access 'vendor': No such file or directory   (无 vendor 目录，依赖走 module cache)
```

结论：Go 工具链 **1.26.3**，与 `codex_go/go.mod` 里 `go 1.26.2` 指令兼容（>= 即可）。GOPROXY 已配置为国内镜像 `goproxy.cn`（与本机网络状况吻合）。**缺 `powershell`、缺 `jq`**（若某门禁脚本依赖 jq 需先补装）。

### 2.3 仓库可达性（核心）

#### 2.3.1 目录存在性

```
$ ls -la /home/jacks/jacks_dev/
NSpoofing/  codex/  codex_go/  codex_go_wt/  git/  images/  recopilot-agent-go/  recopilot_agent_cluster/  test/
   EXIT=0

$ ls -la /home/jacks/jacks_dev/codex_go   (head -30, 摘录)
.git  .gitattributes  .github  .gitignore  .npmrc  .syntropy  README.md  RELEASING.md  VERSION
agent/  agentboard/  app/  applypatch/  apps/  appserver/  appserverdaemon/  attachmentstore/
audioutil/  auth/  bin/  chatgptapi/  cli/  cmd/  codemode/  codexapi/  codexuds/ ...
   EXIT=0

$ ls -la /home/jacks/jacks_dev/codex   (head -20, 摘录)
.bazelignore  .bazelrc  .bazelversion  .cargo  .codespellignore  .git  .github  .gitignore
.markdownlint-cli2.yaml  .npmrc  .prettierignore  .prettierrc.toml  .vscode  .worktreeinclude  BUILD.bazel ...
   EXIT=0
```

`codex_go` 是 Go 形制（有 `sandbox/`、`appserver/`、`tui/` 等 Go 包目录）；`codex` 是 Rust 上游形制（`.bazel*`、`BUILD.bazel`、`.cargo`）。**队长假设成立。**

#### 2.3.2 codex_go 的 git 身份与时点

```
$ cd /home/jacks/jacks_dev/codex_go
$ git log -1 --format='%H %ad %s'
09d31d673c5463493955dab817512504dc5298c9 Wed Oct 7 22:30:47 2026 +0800 round 88 (docs): note the landed-patch cleanup in the disposition list
   EXIT=0

$ git status --porcelain
?? scripts/loc_report.sh          <-- 仅 1 个未跟踪文件；侦察开始前即存在，非本次产生
   EXIT=0

$ git status -sb
## main...origin/main
?? scripts/loc_report.sh

$ git remote -v
origin  https://github.com/jacks001314/codex_go.git (fetch)
origin  https://github.com/jacks001314/codex_go.git (push)
   EXIT=0

$ git branch --show-current
main

$ git log -3 --format='%h %ad %s' --date=short
09d31d67 2026-10-07 round 88 (docs): note the landed-patch cleanup in the disposition list
dd3e83a3 2026-10-07 round 88 (cleanup): remove the 63 landed patches from update/r86_patches
08caefa8 2026-10-07 round 88 (docs): sync660-664 landings, patch disposition and lane audit trail at main 640b5603

$ grep -m1 '^module' go.mod
module codex_go
$ head -5 go.mod
module codex_go

go 1.26.2

require github.com/pelletier/go-toml/v2 v2.4.2

$ du -sh /home/jacks/jacks_dev/codex_go
333M    /home/jacks/jacks_dev/codex_go
   EXIT=0
```

**同一个 Go 仓 ✓，在哪：`main` = `09d31d67`（round 88，2026-10-07），工作区干净**（唯一 `?? scripts/loc_report.sh`）。

关键差异（必须记）：

```
$ git log -1 --format='%H %ad %s' --date=short origin/main
09d31d673c5463493955dab817512504dc5298c9 2026-10-07 round 88 (docs): note the landed-patch cleanup in the disposition list

$ git cat-file -t 7f8cad04          # 队长 Windows 未 push 的 main tip
fatal: Not a valid object name 7f8cad04     EXIT=128

$ git cat-file -t 63e3e264          # 队长所述 origin/main
fatal: Not a valid object name 63e3e264     EXIT=128

$ timeout 15 git ls-remote origin main
63e3e2649f9be323a7114d81c8c582a1f2429b3b    refs/heads/main
   EXIT=0
```

解读：本地 `origin/main`（09d31d67）是 **Oct-07 的陈旧 remote-tracking ref**；远端 GitHub 上真实的 `refs/heads/main` = `63e3e264`，**与队长所报 origin/main 一致**。队长 Windows 的 `7f8cad04`（round 89/90，未 push）在 Linux 侧**不存在**。→ 两边不同步的原因不是"仓不同"，而是 **Linux 侧自 Oct-07 起没再 fetch**，且队长的最新提交还没 push 到 origin。

#### 2.3.3 Rust 上游 codex

```
$ cd /home/jacks/jacks_dev/codex
$ git log -1 --format='%H %ad %s'
4eb4a65407d98cebc4c83bf13be440b9fbc78a7c Fri Oct 16 12:27:57 2026 +0000 Capture sender context for `codex_app.create_thread` deliveries (#54590)
   EXIT=0

$ git status --porcelain
(空)                <-- 干净

$ git remote -v
origin  git@github.com:openai/codex.git (fetch)
origin  git@github.com:openai/codex.git (push)
   EXIT=0
```

**HEAD = `4eb4a65407`，与队长给的"上游 Rust pin = 4eb4a65407"逐字符一致 ✓，工作区干净。** 也就是说 Linux 节点上就摆着一份与 pin 完全对齐的 Rust 上游源码树，可直接做差分/对照，无需联网抓上游。

#### 2.3.4 其它 worktree（仅记录，未触碰）

`git worktree list` 显示 `codex_go` 主仓另挂有 `/home/jacks/jacks_dev/codex_go_wt/*`（syncl1…syncl6e、integ86c-*）与 `/home/jacks/base-51482e-wt`、`/home/jacks/wt-51482j`，另有 **约 60 个 `/tmp/wt-*` 被标记 `prunable` 的僵尸 worktree**。按纪律 4 本单**未 prune、未删任何一个**；仅提示队长：`/tmp` 下僵尸 worktree 数量偏大，是否清理请队长另行授权。

### 2.4 构建可行性（只跑一次）

```
$ cd /home/jacks/jacks_dev/codex_go
$ start=$(date +%s); timeout 900 go build ./... > /tmp/_l90_build.log 2>&1; ec=$?; end=$(date +%s)
EXIT=0
WALL_SECONDS=43
--- tail -30 ---
(空，无任何编译输出)
--- dmesg(尾部，无 oom-kill 记录) ---
[   18.604928] br-35c3101ebf26: port 3(veth8ddecdf) entered forwarding state
[   19.102841] /proc/cgroups lists only v1 controllers, use cgroup.controllers of root cgroup for v2 info
```

**全量构建 EXIT=0，43 秒，零告警输出，无 OOM。** 资源（31 GiB RAM / 603 GB 盘）完全够，后续跑单个重包（如 `./appserver/`）也有余量。（注：本机无 `/usr/bin/time`，故用 `date +%s` 计时。）

### 2.5 Linux-only 门禁真机验证

**(a) Windows 上编译失败的那条** —— `probeBwrapHelp` 符号定位：

```
$ grep -rn 'probeBwrapHelp' --include='*.go' .
./sandbox/linuxsandbox/bwrap_linux.go:16:// probeBwrapHelp reads a launcher's `--help` output and reports which
./sandbox/linuxsandbox/bwrap_linux.go:18:func probeBwrapHelp(path string) (bwrapCapabilities, bool) {
./sandbox/linuxsandbox/bwrap_linux.go:35:        probeBwrapHelp,
./sandbox/linuxsandbox/bwrap_test.go:182:  launcher := selectBwrapLauncherForPolicy(writable, bin, "/", probeBwrapHelp, bundled)
./sandbox/linuxsandbox/bwrap_test.go:192:  launcher = selectBwrapLauncherForPolicy(sandboxpath.FilesystemPolicy{}, bin, "/", probeBwrapHelp, bundled)
```

→ 定义在 **`sandbox/linuxsandbox/bwrap_linux.go`**（linux-only 构建文件），Windows 上该文件被排除 → 正是 `bwrap_test.go:182 undefined: probeBwrapHelp` 的成因。

**真跑结果：**

```
$ time timeout 600 go test ./sandbox/linuxsandbox/ -count=1
ok      codex_go/sandbox/linuxsandbox   0.005s
(real 8.12s，含首次编译)

$ go test ./sandbox/linuxsandbox/ -count=1 -v | grep -E '^(--- (PASS|SKIP|FAIL)|ok|FAIL)'
--- PASS: TestCreateCommandArgsWithSandboxExeOverride
--- PASS: TestCreateCommandArgsUsesDefaultSandboxExe
--- PASS: TestCreateCommandArgsAllowsNetworkForProxy
--- PASS: TestParseBwrapHelpLikeRust
--- PASS: TestSelectBwrapLauncherLikeRust
--- PASS: TestInjectAsPid1LikeRust
--- PASS: TestApplyInnerCommandArgv0LikeRust
--- PASS: TestVerifyBundledBwrapDigestLikeRust
--- PASS: TestBundledBwrapResourceFallsBackToTheExecutableDirectory
--- PASS: TestSelectBwrapLauncherForPolicySkipsWritableCandidateLikeRust
--- PASS: TestExecveWrapperDenyResponseReturnsOne
--- PASS: TestExecveWrapperEscalateTransfersStdioFDs
--- PASS: TestParseLinuxPermissionProfileRustManagedShape
--- PASS: TestParseLinuxPermissionProfileGoShape
--- PASS: TestLinuxNetworkSeccompModeFor
--- PASS: TestRewriteProxyEnvValue
--- PASS: TestLinuxDenyReadGlobExpansion
--- PASS: TestLinuxDenyReadGlobExpansionMatchesDotfilesWithoutExternalBinary
--- PASS: TestLinuxDenyReadGlobExpansionIgnoresRipgrepConfiguration
--- PASS: TestAppendUnreadableRootBwrapArgs
```

**20/20 PASS，0 SKIP，0 FAIL。** 这是本单最硬的一条：Windows 上编译不过的 package，在 Linux 上真编译、真跑、真过。

**(b) 沙箱后端实际可用性：**

```
$ bwrap --version
bubblewrap 0.11.0
   EXIT=0

$ bwrap --ro-bind / / --dev /dev --unshare-all --die-with-parent /bin/true
   BWRAP_EXIT=0                     <-- 真实建起一个 unshare-all 的沙箱并跑通

$ unshare --version
unshare from util-linux 2.41.3

$ cat /sys/kernel/security/lsm
lockdown,capability,landlock,yama,apparmor,tomoyo,bpf,ipe,ima,evm     <-- 含 landlock

$ cat /proc/sys/kernel/unprivileged_userns_clone
1
$ cat /proc/sys/user/max_user_namespaces
126482
```

→ **bubblewrap 0.11.0 已装且能真正创建沙箱；内核 LSM 含 landlock；非特权 userns 开放。** 意味着 Linux 沙箱路径（bwrap + landlock + seccomp）在真机上是可执行、可验证的，不是纸上谈兵。

**(c) Windows 被 SKIP 的 symlink 用例在 Linux 真跑：**

扫描范围（58 个文件含 `runtime.GOOS == "windows"` 跳过分支）：

```
$ grep -rln 'runtime.GOOS == "windows"' --include='*_test.go' . | wc -l
58
```

挑两条有代表性、且**含 symlink 跳过分支**的用例真跑：

```
$ go test ./tui/tea/ -run 'TestEditorDirectoryRejectsSymlink' -count=1 -v
=== RUN   TestEditorDirectoryRejectsSymlink
--- PASS: TestEditorDirectoryRejectsSymlink (0.00s)
PASS
ok      codex_go/tui/tea        0.021s
   EXIT=0
   （对应源码 tui/tea/editor_directory_test.go:81-82： if runtime.GOOS == "windows" { t.Skip("symlink creation requires privileges on Windows") }）

$ go test ./utils/ -run 'TestResolveSymlinkWritePathsCycleOnUnix' -count=1 -v
=== RUN   TestResolveSymlinkWritePathsCycleOnUnix
--- PASS: TestResolveSymlinkWritePathsCycleOnUnix (0.00s)
PASS
ok      codex_go/utils  0.002s
   EXIT=0
   （对应源码 utils/pathutils_test.go:63-64：同样的 windows-skip 分支）
```

**两条在 Windows 上只会 SKIP 的 symlink 用例，在 Linux 上真建 symlink、真断言、真 PASS。** 这类"Windows 永远测不到"的分支，正是 Linux 车道的价值所在。

（注：`appserver/fs_test.go:227 TestServiceCopyPreservesSymlink` 也是同类 windows-skip symlink 用例，本单按边界要求**未**跑 `./appserver/` 整包，留后续批次。**未跑，标 unverified。**）

### 2.6 网络

```
$ curl -s -o /dev/null -w '%{http_code}\n' --max-time 10 https://proxy.golang.org/
000        EXIT=28      <-- 超时不可达
$ curl -s -o /dev/null -w '%{http_code}\n' --max-time 10 https://github.com/
000        EXIT=28      <-- 超时不可达
$ curl -s -o /dev/null -w '%{http_code}\n' --max-time 10 https://goproxy.cn/
200        EXIT=0       <-- 可达

$ go env GOPROXY
https://goproxy.cn
```

**但 git 协议能通！**

```
$ timeout 15 git ls-remote origin main
63e3e2649f9be323a7114d81c8c582a1f2429b3b    refs/heads/main
   EXIT=0       (耗时约 4.7s)
```

结论（只答能/不能）：- **能**：`go mod` 依赖下载（走 `goproxy.cn`）；`git fetch/clone/ls-remote` 到 github.com（https，实测成功）。
- **不能**：直连 `proxy.golang.org`、以及 `curl https://github.com/` 裸首页（超时）。→ **依赖 GitHub 的裸 curl 脚本（如某些 release 下载）大概率不通，需换镜像或走 git。** 本单未做长下载，未验证代理/环境变量细节。

---

## 3. 跨节点协作可行性结论

**Linux 节点能承接哪类工作（基于本单实测）：**

1. **真机门禁复跑（首选、已证明）**：Windows 上编译失败或必然 SKIP 的 Linux-only 门禁，本节点可**真编译、真跑、真过**。已当场证明 3 类：
   - `./sandbox/linuxsandbox/`（Windows 编译失败 → Linux 20/20 PASS）；
   - symlink 类 windows-skip 用例（utils / tui/tea 两条真 PASS）；
   - 真实沙箱创建（`bwrap … --unshare-all` EXIT=0 + landlock 在场）。
2. **Linux-only 实现与验证**：bubblewrap/landlock/seccomp 路径、symlink、Unix 信号/权限语义等只能在 Linux 上验的东西，本节点全量构建 43s、16 核 31 GiB、无 OOM，容量足够承接多轮迭代与批量测试。
3. **上游对照**：本节点已有一份 **与 pin 逐字符一致的 Rust 上游树（4eb4a65407，干净）**，可直接做 codex_go ↔ Rust 差分，无需联网。
4. **跨节点传输**：唯一通道是 Master 项目；Linux 侧可用 `project_write`/`project_file_sync` 回写证据与产物。

**硬缺口 / 阻塞（按严重度）：**

| 缺口 | 状态 | 影响 |
|------|------|------|
| `codex_go` 落后队长 2 轮 | ⚠️ 本地 HEAD `09d31d67`(r88)，队长 `7f8cad04`(r89/90) 不在本机 | **最重要的缺口**。Linux 复跑的门禁是"r88 代码"的门禁，不等于队长最新树的门禁。两轮之间若有改动落在 sandbox/linux 路径，复跑结论就不能直接代表队长 tip |
| 队长 tip `7f8cad04` 未 push | ⚠️ 远端 main 停在 `63e3e264` | 即使 Linux `git fetch`，也只能拿到 `63e3e264`，拿不到 `7f8cad04`；跨节点同步必须经 Master 传 patch/产物 |
| 缺 `powershell` / 缺 `jq` | 🟡 | 若门禁脚本依赖 powershell 或 jq 会失败，需先补装（Kali 上 `apt` 可装 jq；powershell 需另配源） |
| 网络只放行 goproxy.cn + git-over-https | 🟡 | 依赖 GitHub 裸 curl 的脚本不通；模块代理已指向 goproxy.cn，正常 `go mod` 无碍 |
| 无 `/usr/bin/time` | 🟢 已绕开 | 用 `date +%s` 计时 |

**一句话结论**：Linux 节点**完全具备**承接"Linux-only 真机门禁复跑 + Linux-only 实现"的能力（沙箱后端齐备、构建快、资源足、上游树在手），**唯一实质阻塞是代码同源**——本机 `codex_go` 停在 round 88，要把队长最新树（`7f8cad04`）同步过来后，复跑结论才够代表最新 tip。建议队长要么 push 到 origin、要么经 Master 把 patch 传过来。

---

## 4. 未决与阻塞

1. **代码时点不对齐（最高优先）**：Linux `codex_go` = `09d31d67`(r88)，队长 Windows = `7f8cad04`(r89/90，未 push)。本单所有复跑结论**仅对 r88 有效**。
2. **`./appserver/` 整包未跑**（按派工单边界）：`TestServiceCopyPreservesSymlink` 等 appserver 内 windows-skip symlink 用例**未验证**，标 `unverified`，留后续批次。
3. **缺 `powershell` / `jq`**：未验证是否有门禁脚本依赖它们（未跑相关脚本）。
4. **网络细节未穷尽**：只验证了 `curl`(三站) 与 `git ls-remote`；未验证 ssh(git@github)、未验证 release/对象存储类下载、未验证是否存在 HTTP 代理。标 `unverified`。
5. **Rust 上游未构建**：只确认 commit 与干净度，未跑 `cargo`/`bazel`（本单无此要求）。
6. **约 60 个 `/tmp/wt-*` prunable 僵尸 worktree** 未处理（纪律 4）。

## 5. 清理清单

- **删除**：无（本单只读）。
- **保留的台面**：
  - `/tmp/_l90_build.log`（0 字节，`go build` 输出留档）——如需可删，非必需。
  - 未建任何 worktree（未用 `git worktree add`）。
  - `/home/jacks/jacks_dev/codex_go` 与 `codex` 的 HEAD、分支、工作区**均未被改动**（§2.7 回读证据见下）。
- **未触碰**：任何既有 worktree（含 `/tmp/wt-*`、`codex_go_wt/*`）一律未删未 prune。

### 2.7 改动回读（侦察后复核）

```
$ cd /home/jacks/jacks_dev/codex_go
$ git status --porcelain
?? scripts/loc_report.sh          <-- 与侦察前完全一致（该文件早于本单存在）

$ git diff --stat
(空)
```

→ **本单对仓库零改动**：无 tracked 文件被修改，未跟踪文件仅 1 个且为既有物。

```
$ cat /sys/kernel/security/lsm
lockdown,capability,landlock,yama,apparmor,tomoyo,bpf,ipe,ima,evm
```

（附：本文件写入后的字节数与行数见 A2A 回包"产物回读确认"一节。）

---

## 附：本单命令与 exit code 速查

| 命令 | exit code |
|------|-----------|
| `uname -a` / `cat /etc/os-release` / `nproc` / `free -m` / `df -h /home` / `uptime` | 均 0 |
| `go version` | 0（go1.26.3 linux/amd64） |
| `go env GOPATH GOCACHE GOMODCACHE GOPROXY GOOS GOARCH GOVERSION` | 0 |
| `git --version` | 0（2.51.0） |
| `which bwrap unshare zsh python3 powershell gcc make jq` | 0（powershell/jq 缺失） |
| `ls -la /home/jacks/jacks_dev/` | 0 |
| `git log -1` / `status --porcelain` / `remote -v`（codex_go） | 0 / 0 / 0 |
| `git cat-file -t 7f8cad04` / `63e3e264` | **128**（对象不存在） |
| `git log -1`（codex，Rust） | 0（4eb4a65407） |
| `go build ./...`（timeout 900） | **0**（43s，无 OOM） |
| `go test ./sandbox/linuxsandbox/ -count=1` | **0**（ok，20/20 PASS） |
| `bwrap --version` | 0（0.11.0） |
| `bwrap --ro-bind / / --dev /dev --unshare-all --die-with-parent /bin/true` | **0** |
| `go test ./tui/tea/ -run TestEditorDirectoryRejectsSymlink -count=1 -v` | **0**（PASS） |
| `go test ./utils/ -run TestResolveSymlinkWritePathsCycleOnUnix -count=1 -v` | **0**（PASS） |
| `curl proxy.golang.org` / `github.com` / `goproxy.cn` | **28 / 28** / 0（000/000/200） |
| `git ls-remote origin main` | **0**（=63e3e264） |

*本文件由 mc-l90-node 在 Linux 节点 `06aac1d3185c1999dee1e86482026deb` 上生成，所有结论均基于本单实际跑过的命令与原始输出。*
