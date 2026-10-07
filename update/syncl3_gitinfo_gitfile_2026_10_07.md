# syncl3 · C2 `gitfile` 支持（`utils/gitinfo.go`）落地报告

- 日期：2026-10-07（round 86）
- 车道：syncl3（Linux 节点）
- 基线：`main = 4ee41b7ad414d2d21be1305465a70e0ce5642b90`（`git ls-remote origin main` 实测一致）
- 工作树：`/home/jacks/jacks_dev/codex_go_wt/syncl3g`（detached @ `4ee41b7a`，**未 commit / 未 push**）
- 交付：`update/r86_patches/syncl3_gitinfo_gitfile.patch`
  - 字节数：**10431**
  - sha256：**e8976249dad2e9aa607828cce16a07695a75d5fcc939b9a86d5b45173fe81db8**
  - `git apply --check` 打 `4ee41b7a` → **OK**（`APPLY-CHECK-OK`）
  - 范围：`utils/gitinfo.go`（+94/−8）、`utils/gitinfo_test.go`（+99），2 文件 +193/−8

## 1. 缺口（syncw1 发现，队长已复核）

- Go（改动前）：`utils/gitinfo.go` 的 `CollectGitInfoFromDir` / `GitOriginURLFromDir` 用 `os.Stat(<root>/.git)` + `!stat.IsDir()` ⇒ `.git` **文件**（linked worktree / submodule）被当作「非 Git 工作树」，返回 `("", false)`。
- Rust：`codex-rs/git-utils/src/info.rs:21-22` 注释明写 `get_git_repo_root` 向上找「a `.git` **file or directory**」；`:63-64` `get_git_origin_url` 直接 `git remote get-url origin`（gitfile 由 git 自己解析）。⇒ Rust 在 linked worktree / submodule 里能取到 URL。
- 可观测差异（真实 git 复现，见 §4）：调用方 `appserver/turn_runtime.go:10521 skillInvocationRepo` 的祖先遍历用不带 `IsDir` 的 `os.Stat`，会命中 `.git` 文件并把其所在目录当 `repoRoot`，随后 `utils.GitOriginURLFromDir` 返回空 ⇒ skill analytics **有 repoRoot、无 repoURL**；Rust 两者都有。

## 2. 口径选择：读 `.git` 文件 + `commondir`（不起子进程）

Rust 走 `git remote get-url origin` 子进程；Go 侧**不**照搬，改为纯文件解析：

1. `.git` 是目录 → `gitDir == commonDir == <root>/.git`（原行为不变）。
2. `.git` 是文件 → 读 `gitdir: <path>`（相对 `.git` 所在目录解析，`filepath.Clean`，必须是已存在目录）。
3. 若 `gitDir/commondir` 存在（linked worktree）→ 按相对 `gitDir` 解析出共享 `commonDir`；无则 `commonDir = gitDir`（普通仓库 / submodule）。
4. 从 `commonDir/config` 读 `[remote "origin"] url`；`CollectGitInfoFromDir` 另从 `gitDir/HEAD` 读 HEAD、分支 ref 先查 `commonDir/refs/heads/<b>` 再查 `gitDir/...`。

理由：
- Go 侧既有实现本来就是**纯文件读**（`readGitInfoOriginURL` 直接解析 `.git/config`），保持同一族实现、无子进程、无 5s 超时/无 PATH 依赖；
- Rust 之所以 shell out 是 `git2`/二进制不可用时也想要正确结果，其**语义**是「gitfile 也要能解析」；上面 4 步与 git 自身对 gitfile + commondir 的解析一致，等价覆盖 linked worktree 与 submodule；
- submodule 的 `.git` 文件只含 `gitdir: ../.git/modules/<name>`（无 `commondir`），其 config 就在目标目录内，第 3 步自然退化为 `commonDir = gitDir`。

## 3. 落点（`file:line`）

| 位置 | 说明 |
| --- | --- |
| `utils/gitinfo.go:22` | `CollectGitInfoFromDir`：改用 `gitInfoGitDirs`；ref 走 commonDir→gitDir |
| `utils/gitinfo.go:61` | `GitOriginURLFromDir`：改用 `gitInfoGitDirs`，读 `commonDir/config` |
| `utils/gitinfo.go:76` | `gitInfoGitDirs`：目录 / gitfile 分流 |
| `utils/gitinfo.go:95` | `resolveGitInfoGitfile`：解析 `gitdir:` 指针 |
| `utils/gitinfo.go:125` | `gitInfoCommonDir`：解析 linked worktree `commondir` |
| `appserver/turn_runtime.go:10521` | `skillInvocationRepo` 调用点（`:10524` `utils.GitOriginURLFromDir`），**未改动**，仅确认行为 |

测试（`utils/gitinfo_test.go`）：`TestGitOriginURLFromDirLinkedWorktreeLikeRust:129`、`TestGitOriginURLFromDirSubmoduleLikeRust:177`、`TestGitInfoUnresolvableGitfileLikeRust:203`。

## 4. 门禁实跑

- `gofmt -l utils/gitinfo.go utils/gitinfo_test.go` → 空（exit 0）
- `go build ./...` → exit 0
- `go vet ./utils/` → exit 0
- `go test ./utils/ -count=1`：改动后 `ok codex_go/utils`（0.290s）；基线 `ok codex_go/utils`（0.319s）→ **新增失败 0**
- `go test ./appserver/ -count=1` 失败集合对拍：
  - 基线（`4ee41b7a`）4 条：`TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust`、`TestOtelProviderReloadsAfterAccountChange`(flaky)、`TestPluginListHonorsPerRepositoryConfigLikeRust`、`TestRuntimeRouterTurnStartFileChangeApplyFailureLikeRust`
  - 改动后 3 条：同上但 flaky 那条本轮通过 ⇒ **新增失败 0**（改动后 ⊆ 基线）
- parity：`CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1` → `ok codex_go/parity 0.771s`（全绿；无需 re-vendor `.zst`）

## 5. 值级 RC（撤生产接线 ⇒ FAIL ⇒ 恢复 ⇒ ok）

撤掉 `gitInfoGitDirs` 的 gitfile 分支（4 行 → `return "", "", false`，`assert s.count(old)==1`）：

```
--- FAIL: TestGitOriginURLFromDirLinkedWorktreeLikeRust (0.00s)
    gitinfo_test.go:163: GitOriginURLFromDir(linked worktree) = ""/false, want git@example.com:linked.git/true
--- FAIL: TestGitOriginURLFromDirSubmoduleLikeRust (0.00s)
    gitinfo_test.go:197: GitOriginURLFromDir(submodule) = ""/false, want git@example.com:sub.git/true
FAIL
FAIL	codex_go/utils	0.003s
FAIL
```

恢复后：三条测试全 PASS（`ok codex_go/utils 0.003s`）。

## 6. 真实 git 行为探针（`-overlay`，测试文件在 `/tmp` 不落仓）

用真实 `git init` + `git worktree add ../wt -b feature`（origin = `https://user:token@example.com/owner/repo.git`，`wt/.git` 是 regular file，`commondir = ../..`）：

- 基线（`4ee41b7a`）：`GitOriginURLFromDir(wt) = "" ok=false`；调用点式祖先遍历 `repoRoot="/tmp/.../wt" repoURL=""` ⇒ **复现「有 repoRoot 无 repoURL」**
- 改动后（syncl3g）：`GitOriginURLFromDir(wt) = "https://user:token@example.com/owner/repo.git" ok=true`；调用点式遍历 `repoRoot="/tmp/.../wt" repoURL="https://user:token@example.com/owner/repo.git"` ⇒ **PASS**

探针：`/tmp/probe_gitfile_overlay/{zz_probe_test.go,overlay.json,overlay_base.json}`，工作树 `/tmp/probe_gitfile_393853`。

## 7. 未决 / 备注

- 交付为**未提交补丁**，等待队长 apply + 复核 + 提交；本地 `syncl3g` 工作树改动保留（detached HEAD，无分支指针移动）。
- 无越界改动：未触碰 `appserver/turn_runtime.go`（对照确认即可）、未触碰任何避让面（`runtime_router.go` / `message_board.go` / `rollout/` / `turn/` / `model/` / `config/` / `execserver/`）。
- Rust `origin/main` 现为 `a5130128`（已越过 pin `5b0b253035`）；parity 使用 Rust 工作树 HEAD=`5b0b253035`，全绿。
