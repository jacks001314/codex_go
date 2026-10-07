# syncw1 · `#51652` 落主后缺陷修复：Windows 原生路径漏计 `codex.agents_md.edit`

- 车道：syncw1（Windows，PowerShell / go 1.26.5 / python 3.12）
- 被修对象：`#51652` 的 seam A 版本（已落主 `sync638 = e9849503`）
- **新基线 = `8b2453a9`**（`origin/main`，sync641 已落）
- 来源：syncw4 对抗性验证报告 `update/syncw4_verify_51652_2026_10_07.md` 的 **REFUTED §3.6**（Windows 原生路径漏计）
- Rust 参考：`C:\rw\codex-rs`（LF，HEAD `5b0b253035`，只读）；上游 pin `b17c74cfd5`
- 纪律：**0 commit / 0 push / 0 tag / 0 ref 移动**。产物 = 补丁 + 本报告。

---

## 0. 结论

| 项 | 结论 |
|---|---|
| `sub\\AGENTS.md`（反斜杠原生分隔符）提交后计数 | **修复 → 计 1**（`filename=agents.md`）；修前计 0 |
| 绝对 `C:\\...\\AGENTS.md` 提交后计数 | **修复 → 计 1**；修前计 0 |
| POSIX 行为 | **不变**（`filepath.Base` 在 Linux 只切 `/`，与 `path.Base` 同） |
| 可选 ②b（`move_path` 只在 `Update` 生效） | **一并忠实镜像**（加 `Kind == ChangeUpdate` gate）+ 单测 + 独立 RC |

**根因**：计数取 basename 用的是 POSIX-only 的 `path.Base`（只切 `/`），但被计数路径 `AppliedChange.Path` 是**补丁原文**（未归一，Windows 上含 `\`）。同一仓内展示摘要用 `filepath.ToSlash`（承认原生路径），指标却用 `path.Base` ⇒ 内部不一致。
**Rust 依据**：`PathUri` 在 Windows convention 下先 `path.replace('\\', "/")` 再按 `/` 切段（`codex-rs/utils/path-uri/src/lib.rs:514-517`），`basename()` 于 `:273-282` 取最后一段 ⇒ Windows 原生路径仍得 `AGENTS.md`。`filepath.Base` 在 Windows 切 `\` 与 `/`、在 POSIX 只切 `/` ⇒ 两 convention 都对上。

---

## 1. 补丁与落盘

产物：`update/r86_patches/syncw1_51652_windows_path.patch`

- **6856 bytes**，`crlf=0`（"\r\n" 计数 0），2 files changed, **+130 / −5**
- sha256 = `d2a8f69ab4a0e04f22b783276597ea11f96beea0812d30d08d76b9f243013eda`
- 生成方式：独立临时 git 仓（`git init` + `git add -A` 基线）+ 覆写 2 文件 + `git diff --cached --no-color --binary`（**直写字节**，不经 PowerShell `>`，无 BOM）

### `git apply --check --verbose`（干净 LF `8b2453a9` 树）

```
$ cd %TEMP%\syncw1_probe\lf_8b2453a9_fresh          # 干净 @ 8b2453a9
$ git -c core.autocrlf=false -c core.eol=lf apply --check --verbose <patch>
Checking patch tool/apply_patch_agents_md_metrics.go...
Checking patch tool/apply_patch_agents_md_metrics_like_rust_test.go...
apply-check exit=0
```

`git apply`（真打）exit=0；随后 `--stat`：

```
 tool/apply_patch_agents_md_metrics.go              |  17 ++-
 ...apply_patch_agents_md_metrics_like_rust_test.go | 118 +++++++++++++++++++++
 2 files changed, 130 insertions(+), 5 deletions(-)
```

落盘（fresh-apply 树 == 工作树，逐文件 sha256 相同）：

| 文件 | LF sha256 | 
|---|---|
| `tool/apply_patch_agents_md_metrics.go` | `37b9982a11b0f5477819b5e85b63bd161c6f35eade3fdcfb692d77133b729cdb` |
| `tool/apply_patch_agents_md_metrics_like_rust_test.go` | `9a9a86138641a89b1b178384e334e456b484a8808b4f616f179a392b3119eb25` |

### 生产改动（`tool/apply_patch_agents_md_metrics.go`）

```diff
 import (
-	"path"
+	"path/filepath"
 	"strings"
@@
 	for _, change := range result.Changes {
 		paths := []string{change.Path}
-		if change.MovePath != "" && change.MovePath != change.Path {
+		// Rust carries a move destination only on an update
+		// (`AppliedPatchFileChange::Update { move_path }`), so a stray MovePath on
+		// another kind never contributes a second path.
+		if change.Kind == applypatch.ChangeUpdate && change.MovePath != "" && change.MovePath != change.Path {
 			paths = append(paths, change.MovePath)
 		}
 		for _, changedPath := range paths {
-			// Rust's `PathUri::basename` returns the last `/`-separated path
-			// segment, so `path.Base` (not `filepath.Base`) mirrors it.
-			filename := strings.ToLower(path.Base(changedPath))
+			// Rust's `PathUri::basename` splits the normalized path on `/`, and its
+			// Windows convention rewrites `\` to `/` first
+			// (path-uri/src/lib.rs:514-517), so a native Windows path still yields
+			// `AGENTS.md`. `filepath.Base` matches both conventions: on Windows it
+			// splits on `\` and `/`, on POSIX only on `/` (identical to Rust's
+			// Posix convention).
+			filename := strings.ToLower(filepath.Base(changedPath))
 			if filename == "agents.md" || filename == "agents.override.md" {
 				metrics.Counter(agentsMdEditMetricName, 1, map[string]string{"filename": filename})
 			}
```

`path` 已无其它用法（当前文件内 `path.` 仅此一处）⇒ import 同步改为 `path/filepath`。

---

## 2. 回归测试（扩展 `tool/apply_patch_agents_md_metrics_like_rust_test.go`）

新增 3 个测试（**原 3 个用例未改、全绿**）：

- `TestApplyPatchAgentsMdBasenameNeighborsLikeRust`
  - 负例保持 0：`AGENTS.md.bak`、`xAGENTS.md`
  - 正向（大小写归一后计）：`docs/AGENTS.MD` ⇒ `filename=agents.md`
  - 断言：filenames == `"agents.md"`（仅 docs 那个）
- `TestApplyPatchAgentsMdEditMetricWindowsNativePathLikeRust`（2 子测）
  - `native separator`：补丁写 `sub\\AGENTS.md`（反斜杠）⇒ **Windows 计 1 / POSIX 计 0**（与 Rust convention 同态；用 `runtime.GOOS` 显式分支）
  - `absolute path`：`filepath.Join(dir,"AGENTS.md")`（主机原生分隔符，Windows 为 `C:\\...\\AGENTS.md`）⇒ 计 1，且断言文件真落盘（`os.Stat`）
- `TestRecordAgentsMdEditMetricsMovePathOnlyForUpdatesLikeRust`（②b 单测）
  - `Kind=Delete/Add` + `MovePath=AGENTS.md` ⇒ **0 计**
  - 对照 `Kind=Update` + 同 MovePath ⇒ 计 1

保留的既有正向对照：`AGENTS.md` + `nested/AGENTS.Override.MD`（大小写归一）⇒ `agents.md,agents.override.md`。

---

## 3. 门禁（全在 LF 树；本机 autocrlf=true，落盘会 CRLF ⇒ gofmt 会假红，故对 LF 归一内容跑）

```
$ gofmt -l tool/apply_patch_agents_md_metrics.go tool/apply_patch_agents_md_metrics_like_rust_test.go
   （空）  exit=0
$ # BOM 检查（前 3 字节）
metrics first3: 112 97 99        # 'p','a','c'  ⇒ 无 EF BB BF BOM
test    first3: 112 97 99
$ go build ./...                         exit=0
$ go vet ./tool/                         exit=0   （无任何发现）
$ go test ./tool/ -run 'AgentsMd|RecordAgentsMd' -count=1   -> ok  codex_go/tool
```

### ④ 受影响包整包对拍（`go test ./tool/ -count=1`）

```
# 改动后（8b2453a9 + 本补丁）
--- FAIL: TestApplyPatchPreflightReadsSelectedEnvironmentFileSystemLikeRust (0.00s)
--- FAIL: TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust (0.01s)
    --- FAIL: TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust/restores_executor_directories (0.00s)
    --- FAIL: TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust/login_invocation_with_non-login_argv (0.00s)
--- FAIL: TestLocalShellLaunchRestoresExecutorPathDirsLikeRust (0.01s)
    --- FAIL: TestLocalShellLaunchRestoresExecutorPathDirsLikeRust/restores_executor_directories (0.00s)
FAIL	codex_go/tool	105.009s

# 改动前（纯 8b2453a9）
--- FAIL: TestApplyPatchPreflightReadsSelectedEnvironmentFileSystemLikeRust (0.00s)
--- FAIL: TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust (0.02s)
    --- FAIL: TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust/restores_executor_directories (0.00s)
    --- FAIL: TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust/login_invocation_with_non-login_argv (0.00s)
--- FAIL: TestLocalShellLaunchRestoresExecutorPathDirsLikeRust (0.01s)
    --- FAIL: TestLocalShellLaunchRestoresExecutorPathDirsLikeRust/restores_executor_directories (0.00s)
FAIL	codex_go/tool	104.617s
```

⇒ **逐条同名同数，新增失败 = 0**。

### ⑤ parity

```
$ $env:CODEX_RUST_ROOT='C:\rw\codex-rs'; go test ./parity/ -count=1
ok  	codex_go/parity	12.661s      exit=0
```

（LF 的 `C:\rw\codex-rs` 下 `TestRustCollaborationModeTemplatesMatchGo` 绿；该条在 CRLF 工作树会假红，与本补丁无关。）

---

## 4. 反向对照（值级）

### RC-A — 撤 basename 修复（`filepath.Base` → `path.Base`，import 同步回 `path`）

```
$ python rc_wa.py
RC-A applied: filepath.Base -> path.Base (import reverted)
broken sha256: 2091e746662b3e438166277740835cb850302c0c9027c3a71a17befacd4fecea

$ go test ./tool/ -run 'AgentsMd|RecordAgentsMd' -count=1
--- FAIL: TestApplyPatchAgentsMdEditMetricWindowsNativePathLikeRust (0.01s)
    --- FAIL: TestApplyPatchAgentsMdEditMetricWindowsNativePathLikeRust/native_separator (0.00s)
        apply_patch_agents_md_metrics_like_rust_test.go:372: codex.agents_md.edit filenames = "", want "agents.md"
    --- FAIL: TestApplyPatchAgentsMdEditMetricWindowsNativePathLikeRust/absolute_path (0.00s)
        apply_patch_agents_md_metrics_like_rust_test.go:398: codex.agents_md.edit filenames = "", want "agents.md"
FAIL
FAIL	codex_go/tool	0.494s
rc_exit=1
```

⇒ 值级 FAIL（断言，非 build failed），两条 Windows 用例精确命中既有 bug。

**恢复**（从 fresh-apply 树 `lf_8b2453a9_fresh` 逐文件复制，非手工反向编辑）：

```
$ copy lf_8b2453a9_fresh\tool\apply_patch_agents_md_metrics*.go -> lf_8b2453a9\tool\ ...
RESTORED-MATCH tool/apply_patch_agents_md_metrics.go
   now : 37b9982a11b0f5477819b5e85b63bd161c6f35eade3fdcfb692d77133b729cdb
   want: 37b9982a11b0f5477819b5e85b63bd161c6f35eade3fdcfb692d77133b729cdb
RESTORED-MATCH tool/apply_patch_agents_md_metrics_like_rust_test.go
   now : 9a9a86138641a89b1b178384e334e456b484a8808b4f616f179a392b3119eb25

$ go test ./tool/ -run 'AgentsMd|RecordAgentsMd' -count=1   -> ok  codex_go/tool
$ gofmt -l <2 files>                                        -> （空）exit=0
```

⇒ 恢复后逐文件 sha256 与 RC 前**完全一致**。

### RC-B — 撤 `Update` gate（`change.Kind == applypatch.ChangeUpdate &&` 删除）

```
$ python rc_wb.py
RC-B applied: ChangeUpdate gate removed
broken sha256: 43b97fd4785638b2ba835f4e7e783d6386e12b1fd878ec6c1e4ed66165973c15

$ go test ./tool/ -run RecordAgentsMd -count=1
--- FAIL: TestRecordAgentsMdEditMetricsMovePathOnlyForUpdatesLikeRust (0.00s)
    apply_patch_agents_md_metrics_like_rust_test.go:418: non-update move paths counted "agents.md,agents.md", want none
FAIL	codex_go/tool	0.030s
rc_exit=1
```

恢复（同样从 fresh-apply 树复制）⇒ sha256 还原 `37b9982a...`，6 个测试全 PASS：

```
--- PASS: TestApplyPatchAgentsMdEditMetricLikeRust (0.01s)
--- PASS: TestApplyPatchAgentsMdMoveDestinationMetricLikeRust (0.02s)
--- PASS: TestApplyPatchAgentsMdEditCountsCommittedPrefixOnFailureLikeRust (0.01s)
--- PASS: TestApplyPatchAgentsMdBasenameNeighborsLikeRust (0.01s)
--- PASS: TestApplyPatchAgentsMdEditMetricWindowsNativePathLikeRust (0.01s)
--- PASS: TestRecordAgentsMdEditMetricsMovePathOnlyForUpdatesLikeRust (0.00s)
ok  	codex_go/tool	0.086s
```

---

## 5. ②b 裁定：忠实镜像（非 N/A）

- Rust：`move_path` 只在 `AppliedPatchFileChange::Update { move_path }` 上存在；`Add`/`Delete` 结构上无该字段。
- Go：`AppliedChange` 有 `MovePath` 字段，`parseAdd/parseDelete` 与 `applyAdd/applyDelete` 均不设置 ⇒ 生产不可达（与 syncw4 §3.2b 一致）。
- 处理：加 `change.Kind == applypatch.ChangeUpdate` gate（一行表达），使类型级语义与 Rust 一致；并以 `TestRecordAgentsMdEditMetricsMovePathOnlyForUpdatesLikeRust` + **RC-B** 覆盖。生产行为不变（真 update 的 `Kind` 恒为 `ChangeUpdate`）。

---

## 6. 未决 / 风险

1. Windows 反斜杠路径的**计数**已对齐；但注意 Go 侧 `AppliedChange.Path` 仍是补丁原文（未归一），而 Rust delta 存的是归一后的绝对 `PathUri`。本补丁只影响 basename 提取，**不改** `Path` 存储语义（避免越界到 `applypatch/` 的路径归一重构）。若日后要求 tag 值之外还对齐「归一后路径」，请另立 item。
2. `filepath.Base` 在 Windows 会把 `sub\AGENTS.md` 判为文件 `AGENTS.md`——这正是本修复期望；POSIX 侧不变。已在测试用 `runtime.GOOS` 显式分支并注释。
3. 既存失败（非本轮引入，两侧同）：`TestApplyPatchPreflightReadsSelectedEnvironmentFileSystemLikeRust`（fake env FS 的 POSIX 键 vs 主机 `C:\` 解析）、`TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust`(+2 子测)、`TestLocalShellLaunchRestoresExecutorPathDirsLikeRust`。parity 在 LF rust root 下全绿。
4. 文件数 = 2（≤2），零避让面（未碰 `appserver/runtime_router.go` / `appserver/turn_runtime.go` / `tui/state.go` / `sandbox/windowssandbox/`）。
