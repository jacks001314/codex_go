# syncw3 · parity manifest 跟进（`#51678` 路径移动）· round88d

> 派单：队长 `msg-1791379083288074900-5624`（§②，已批准）。
> 纪律：**0 commit / 0 push / 0 tag / 0 ref 移动**；改动仅 1 文件（`parity/`，非避让面）；产物 = patch + 本报告。
> apply 基线：**`d12d00dde5732a8f910db636b5452949a4594785`**（`origin/main` 实测）。
> 行号锚：Go `d12d00dd`；Rust 只在 `git` 层读（未动 `C:\rw\codex-rs`，未动 `D:\qax\reagent\dev\git\codex` 的工作树）。

---

## 0. 结论摘要（**含一处派单前提纠正，需你裁定**）

1. 你要的改动本身**已完成且验证正确**：`parity/rust_unified_exec_sandbox_manifest_test.go:141` `Path: "core/tests/suite/windows_sandbox.rs"` → `"core/tests/windows_sandbox.rs"`（1 行；patch 623 B / sha256 `82ac85cc…`；`git apply --check` 打 `d12d00dd` = **rc 0**；应用后与源 worktree **逐字节一致** 11090 B）。
2. ⚠️ **派单前提需纠正**：你要求「给出 `C:\rw\codex-rs` @ `5b0b253035` 下**新旧路径都存在**的原文」——**实测新路径不存在**（`core/tests/windows_sandbox.rs` = **False**；只有旧路径 `core/tests/suite/windows_sandbox.rs` = True）。因此：
   - **现 parity pin（`5b0b253035`）下，本 patch 会把 `TestRustUnifiedExecSandboxSuiteManifest` 打红**（原文见 §4），**不能**在当前 pin 落地；
   - 本 patch 是**与 parity 快照绑定**的改动：**只有在 parity 快照前进到含 `#51678` 的点之后（或同时）**才应落主（原文证明见 §5）。
3. 因此**门禁第 4 项「`go test ./parity/` = ok」在现 pin 下无法成立**（非我改坏：基线也红 1 条既有项，本 patch 再 +1 条）；我按事实给原文，不伪装。
4. **请求裁定**：(a) **hold** 本 patch，待 parity 快照推进到 ≥ 含 `#51678` 的点时随 pin 升级一起落（我推荐）；或 (b) 由你同步推进 parity 快照与 9 条 critical-file pin，再落本 patch；或 (c) 你要我改成「对两种 pin 都绿」的双路径容错写法（**超出 1 行**，需你另行批准）。

---

## 1. 上游事实（只读）

```
$ git -C D:\qax\reagent\dev\git\codex log --grep '#51678' -F --format='%H %s' -1
d83bb540ec64bf6b009bca0283b0be91ea33f26a Move Windows sandbox tests into a dedicated integration binary (#51678)
```
改动之一：`codex-rs/core/tests/suite/windows_sandbox.rs` **重命名/移动**为 `codex-rs/core/tests/windows_sandbox.rs`。

路径存在性矩阵（`git cat-file -e <rev>:<path>`，只读）：

| rev | `core/tests/suite/windows_sandbox.rs`（旧） | `core/tests/windows_sandbox.rs`（新） |
|---|---|---|
| `5b0b253035`（= **现 parity pin**，`C:\rw\codex-rs` HEAD） | **存在** | **不存在** |
| `b17c74cfd5`（上一枚举 pin） | **存在** | **不存在** |
| `d83bb540ec`（现枚举 pin，含 #51678） | **不存在** | **存在** |

`C:\rw\codex-rs` 实测原文：

```
$ git -C C:\rw\codex-rs rev-parse HEAD
5b0b2530354052b9194156d70d4c94a439368342
$ Test-Path C:\rw\codex-rs\core\tests\suite\windows_sandbox.rs
True
$ Test-Path C:\rw\codex-rs\core\tests\windows_sandbox.rs
False
$ git -C C:\rw\codex-rs ls-tree --name-only HEAD core/tests/suite/ | Select-String windows
core/tests/suite/windows_sandbox.rs
```

⇒ **新旧路径并非「都存在」**；现 parity pin 停在 `#51678` **之前**。

## 2. 「测试函数集合逐字相同 + `TestCases: 0` 仍成立」的证据

对两个版本做只读比对（`git show <rev>:<path>` 后解析 `fn`）：

| | `5b0b253035:core/tests/suite/windows_sandbox.rs` | `d83bb540ec:core/tests/windows_sandbox.rs` |
|---|---|---|
| 测试函数（`#[test]`/`fn`） | 9 个：`windows_sandbox_cli_preserves_managed_deny_reads_across_launches` / `windows_elevated_setup_rejects_default_root_deny` / `windows_restricted_token_rejects_exact_and_glob_deny_read_policy` / `windows_elevated_temp_only_core_and_direct_spawn_enforce_carveouts` / `windows_elevated_does_not_create_missing_workspace_metadata` / `windows_elevated_enforces_deny_read_and_protects_setup_marker` / `windows_elevated_powershell_preserves_relative_paths` / `windows_elevated_unified_exec_enforces_large_recursive_deny_reads` / `windows_elevated_approved_git_pull_preserves_deny_read` | **同上，逐字相同**（另 5 个 helper 亦相同：`codex_home_for_windows_sandbox_test` / `stage_windows_sandbox_helpers` / `escape_toml_path` / `stage_windows_sandbox_cli` / `assert_managed_deny_probe`） |
| `#[test_case(` 计数 | **0** | **0** |

⇒ manifest 的 `Tests: [...]`（9 条）与 `TestCases: 0` **无需改**，**只需改 `Path`**。

## 3. 门禁（`D:\qax\reagent\dev\codex_go_wt\syncw3c`，detached @ `d12d00dd`）

```
$ gofmt -l parity/rust_unified_exec_sandbox_manifest_test.go     # CRLF 归一后跑
GOFMT_BAD: []
$ go build ./...
build rc=0
$ go vet ./parity/
vet rc=0
```

## 4. 现 parity pin（`5b0b253035`）下的门禁实跑——**本 patch 在此 pin 下必红**

**基线（未改，`d12d00dd` + `C:\rw\codex-rs`）**：

```
--- FAIL: TestRustCollaborationModeTemplatesMatchGo (0.22s)
FAIL
FAIL	codex_go/parity	44.563s
```
（唯一红 = 既有 collaboration-template 一条，与 #51678 / 本 patch 无关；`TestRustUnifiedExecSandboxSuiteManifest` 在基线 **PASS**。）

**应用本 patch 后（同一 pin 同一树）**：

```
--- FAIL: TestRustCollaborationModeTemplatesMatchGo (0.21s)
--- FAIL: TestRustUnifiedExecSandboxSuiteManifest (0.00s)
    rust_unified_exec_sandbox_manifest_test.go:31: ReadFile(core/tests/windows_sandbox.rs) error = open C:\rw\codex-rs\core\tests\windows_sandbox.rs: The system cannot find the file specified.
FAIL
FAIL	codex_go/parity	9.779s
```

⇒ 现 pin 下 patch **不可落地**（新增失败 1）。

## 5. 决定性前向证据：新 pin 树（含 `#51678`）下 patch 才是对的

为不改动 `C:\rw`，用 `git archive`（只读）导出 `d83bb540ec` 的必要子树到临时 LF 目录：

```
$ git -C D:\qax\reagent\dev\git\codex archive --format=zip -o D:\tmp\rs_d83.zip d83bb540ec64bf6b009bca0283b0be91ea33f26a codex-rs/Cargo.toml codex-rs/core/tests codex-rs/core/src/tools codex-rs/exec/tests
$ Expand-Archive -Path D:\tmp\rs_d83.zip -DestinationPath D:\tmp\rs_d83 -Force
$ Test-Path D:\tmp\rs_d83\codex-rs\Cargo.toml            # True（rustSnapshotRoot 需要它）
$ Test-Path D:\tmp\rs_d83\codex-rs\core\tests\windows_sandbox.rs      # True
$ Test-Path D:\tmp\rs_d83\codex-rs\core\tests\suite\windows_sandbox.rs # False
```

`CODEX_RUST_ROOT=D:\tmp\rs_d83\codex-rs go test ./parity/ -run TestRustUnifiedExecSandboxSuiteManifest -count=1`：

| manifest | 结果 | 原文 |
|---|---|---|
| **未改（旧路径）** | **FAIL** | `rust_unified_exec_sandbox_manifest_test.go:31: ReadFile(core/tests/suite/windows_sandbox.rs) error = open D:\tmp\rs_d83\codex-rs\core\tests\suite\windows_sandbox.rs: The system cannot find the file specified.` |
| **本 patch（新路径）** | **PASS** | `--- PASS: TestRustUnifiedExecSandboxSuiteManifest (0.02s)` / `ok  codex_go/parity 0.289s` |

⇒ **patch 在含 `#51678` 的 pin 下是必需的、且充分**。

## 6. RC 原文（按你的口径：人工构造不存在路径 ⇒ 断言 `ReadFile` error ⇒ 还原）

在 `D:\tmp\rs_d83\codex-rs`（新 pin 树，patch 已生效且 PASS）上，把 `Path` 临时改成不存在的
`core/tests/suite/__rc_nonexistent_manifest_probe__.rs`：

```
--- FAIL: TestRustUnifiedExecSandboxSuiteManifest (0.00s)
    rust_unified_exec_sandbox_manifest_test.go:31: ReadFile(core/tests/suite/__rc_nonexistent_manifest_probe__.rs) error = open D:\tmp\rs_d83\codex-rs\core\tests\suite\__rc_nonexistent_manifest_probe__.rs: The system cannot find the file specified.
FAIL
FAIL	codex_go/parity	0.188s
```

还原后：

```
ok  	codex_go/parity	0.197s
```

⇒ 该 manifest 确实**因缺文件而红**（值级/行为级，不是 build failed）。按你的指示：「把 `Path` 改回旧路径不会失败」这一条**在现 pin 下成立、在新 pin 下不成立**，故未用它当 RC，改用上面的构造法。

## 7. 交付物

```
update/r86_patches/syncw3_parity_manifest_51678.patch   623 B   sha256 82ac85cc8835335e8317b2ae807d9ae080a2c7c948406c16a34118095c7c9c5a
```

- 内容 = 1 hunk / 1 行（`-` 旧路径 `+` 新路径），**无 BOM**，补丁内无 CRLF。
- `git apply --check --verbose` 打干净 detached `d12d00dd`：

```
rc = 0
Checking patch parity/rust_unified_exec_sandbox_manifest_test.go...
```

- 保真：apply 后 `parity/rust_unified_exec_sandbox_manifest_test.go` = **11090 B，与源 worktree 逐字节一致**（worktree 侧 CRLF 文件）。

## 8. 未决 / 需你裁定

1. **落主时机**（核心）：现 parity pin `5b0b253035` 不含 `#51678` ⇒ 本 patch 现在落会红。**我建议 hold**，等 parity 快照（与 9 条 critical-file pin）推进到含 `#51678` 的点时随 pin 升级一起落。若你坚持现在落，`TestRustUnifiedExecSandboxSuiteManifest` 会立刻红（原文见 §4），请确认你接受该中间态。
2. 若你希望「两种 pin 都绿」，需要把 manifest 条目改成**多候选路径/存在性容错**（超过 1 行、动 `parity/rust_unified_exec_sandbox_manifest_test.go` 的结构），**需你另行批准**（我未擅自扩大范围）。
3. 我的 scratch worktree `syncw3c`（detached `d12d00dd`）已按惯例回收；patch 已自证可复现（§7）。`D:\tmp\rs_d83`（临时 Rust 树）我保留到本单结束，若你要复核可直接用它跑上面两条命令；如需清掉请说一声。
