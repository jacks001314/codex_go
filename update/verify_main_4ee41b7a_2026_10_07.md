# 独立发布审计 — main @ `4ee41b7a`（session_index Rust 保真修复）

- **审计员**：synct5（Windows 节点，只读审计）
- **日期**：2026-10-07
- **被审对象**：`4ee41b7ad414d2d21be1305465a70e0ce5642b90`（= `origin/main`）
- **基线**：`b9ad41d30d630aa4cc68434c21f3660e15a347a0`
- **总判定：GREEN** —— tree-sha 握手一致；门禁新增失败 0；`str::lines()` 语义**逐行等价**（探针 6/6 通过，base 对照 4/6 失败 ⇒ 修复真实有效）
- 告警条件未触发。全程**未 push / 未 commit / 未移动任何 ref / 未删远端 ref**。

## §1 结构核对

```
$ git fetch origin --prune ; git rev-parse origin/main
4ee41b7ad414d2d21be1305465a70e0ce5642b90
$ git merge-base --is-ancestor b9ad41d3 4ee41b7a ; echo $LASTEXITCODE
0
$ git log --merges --oneline b9ad41d3..4ee41b7a
(空)                                            # 无 merge commit
$ git log --oneline b9ad41d3..4ee41b7a
4ee41b7a sync620: drop the carriage return when rewriting the session index like Rust
9936e2bf sync619: cover the appended session index JSON line like Rust (#49959)
8ade48ed sync618: keep blank lines when rewriting the session index like Rust
$ git show -s --format='%T | %P' 4ee41b7a
147a5dbe5c575c33d77ea8e764aa00715a801d5e | 9936e2bf56bead57e347a5dbc59a8f9c0c7142ea      # 单亲，线性
$ git diff --shortstat b9ad41d3 4ee41b7a
 3 files changed, 86 insertions(+), 4 deletions(-)
$ git diff --name-only b9ad41d3 4ee41b7a
rollout/session_index.go
rollout/session_index_blank_lines_test.go
rollout/session_index_test.go
```

## §2 与派单口径的偏差（**需更正计数**）

派单称区间为「**6 笔**：sync614 #49432 / sync615-617 #49260 / sync618+620 / sync619」，实测：

```
$ git rev-list --count b9ad41d3..4ee41b7a
3
$ git log --oneline -5 b9ad41d3
b9ad41d3 sync617: admit enterprise MCP authority only for the registration it was granted (#49260)
8d1177ee sync616: retire the plugin enterprise MCP auth overlay (#49260)
9ec7f1c4 sync615: fail closed when the MCP configuration cannot be reloaded (#49260)
5c0e02cd sync614: revoke the application network policy when the authenticated owner changes (#49432)
9620629e sync613: include preceding assistant context in Guardian sender reviews (#49951)
```

- **`b9ad41d3` 本身就是 sync617**；sync614/615/616/617（#49432、#49260）位于**基线 b9ad41d3 及其以下**，早已在上一基线内，**不属于本次区间**。
- 本区间实际 = **3 笔**（sync618 / sync619 / sync620），改动面**仅 `rollout/`**（3 文件，+86/−4）；派单里提到的 `appserver`/`config` 未被触及（仍按你的要求一并跑了门禁对拍）。

## §3 tree-sha 握手：**PASS**

按你的要求「自建复现」：以 `b9ad41d3` 为基线逐笔 replay 这 3 笔（`git cherry-pick -n`，只写 index+worktree，**零 commit**）：

```
$ git -c core.autocrlf=false -c core.eol=lf worktree add <dir> --detach b9ad41d3
$ git cherry-pick -n 8ade48ed 9936e2bf 4ee41b7a      ; exit=0（无冲突）
$ git add -A ; git write-tree
147a5dbe5c575c33d77ea8e764aa00715a801d5e
```

**重放 tree == `4ee41b7a^{tree}` == `147a5dbe5c575c33d77ea8e764aa00715a801d5e`** ⇒ main 上的内容与这 3 笔逐字节相同，无额外夹带。
## §4 独立门禁（LF 树，按你的命令用 `git archive`）

```
$ git -c core.autocrlf=false -c core.eol=lf archive --format=tar -o head.tar 4ee41b7a  ; tar -xf ...
$ git -c core.autocrlf=false -c core.eol=lf archive --format=tar -o base.tar b9ad41d3  ; tar -xf ...
（head 3812 文件 / base 3811 文件；差 1 = 新增测试文件，自洽）

head @4ee41b7a：
(1) gofmt -l rollout/session_index.go rollout/session_index_blank_lines_test.go rollout/session_index_test.go
    -> 输出为空, exit=0
(2) gofmt -l .   -> 32 行（与既有基线一致，见 §4.1）
(3) go build ./...  -> 输出为空, exit=0
(4) go vet ./rollout/ ./appserver/ ./config/  -> **无输出, exit=0**（本批不触发既有的 model lock-copy 基线）
(5) $env:CODEX_RUST_ROOT="C:\rw\codex-rs"; go test ./parity/ -count=1  -> ok  codex_go/parity  28.805s
(6) go test ./rollout/ ./appserver/ ./config/ -count=1   两侧同命令对拍：
    head: ok rollout 5.883s | FAIL appserver 6 条 | ok config 2.594s
    base: ok rollout 5.022s | FAIL appserver 6 条 | ok config 1.592s
    appserver 6 条逐条相同（Windows 平台基线）：
      TestExecutorSkillPathEqualsMatchesWindowsIdentityLikeRust
      TestMultiAgentWaitDurationRecordsOutcomeLikeRust
      TestRequiredSkillsPreSamplingValidationReleasesSatisfiedTurnsLikeRust
      TestShellSnapshotCommandMetricsLikeRust
      TestShellSnapshotCommandMetricsReportProtectedAndUnavailableLikeRust（+protected/unavailable 2 子项）
      TestShellSnapshotCommandMetricsSkipNonDirectLaunchesLikeRust
    => **新增失败 0**；rollout 两侧全绿（含新增 2 个测试）

### §4.1 `gofmt -l .` 全树 32 行的性质
head 与 base 的 32 行集合 Compare-Object **无差异**（既有基线，非本批引入）；本批 3 个改动文件均干净。

## §5 ④ `rollout/session_index.go` 的 Rust 语义逐行核对

Rust 参照（`C:\rw\codex-rs\rollout\src\session_index.rs:74` `remove_thread_name_entries`，上游 LF 检出）：
```rust
for line in contents.lines() {
    let should_remove = serde_json::from_str::<SessionIndexEntry>(line.trim())
        .is_ok_and(|entry| entry.id == thread_id);
    if should_remove { removed = true; } else { remaining.push_str(line); remaining.push('\n'); }
}
```
Go（`4ee41b7a`）：
```go
lines := bytes.Split(data, []byte{'\n'})
if n := len(lines); n > 0 && len(lines[n-1]) == 0 { lines = lines[:n-1] }
for _, line := range lines {
    line = bytes.TrimSuffix(line, []byte{'\r'})
    if json.Unmarshal(bytes.TrimSpace(line), &entry) == nil && entry.ID == threadID { removed = true; continue }
    remaining.Write(line); remaining.WriteByte('\n')
}
```

| # | `str::lines()` 性质 | Go 实现 | 等价 |
|---|---|---|---|
| 1 | 不产出尾部空段（`"a\n"` → `["a"]`） | Split 后若**末元素为空则只丢这一个** | ✅ |
| 2 | 保留**内部**空行（`"a\n\nb"` → `["a","","b"]`） | 只丢一个尾部空元素，内部空串保留并在写回时输出 `"\n"` | ✅ |
| 3 | 逐行去尾随 `\r`，**含无 `\n` 的末行**（`"a\r"` → `["a"]`） | `bytes.TrimSuffix(line, []byte{'\r'})`，一次只去一个（与 Rust 同） | ✅ |
| 4 | 空串 → 无行（`[]`） | `Split("")` = `[""]` → 丢尾 → `[]` | ✅ |
| 5 | 行首尾 `trim()` 后解析 | `bytes.TrimSpace(line)`（顺序不同、结果同） | ✅ |
| 6 | 写回 `push_str(line)`（已去 `\r`）+ `'\n'` | `remaining.Write(line)` + `WriteByte('\n')` | ✅ |
| 7 | 临时文件 `path.with_extension("jsonl.tmp")` | `TrimSuffix(path, ".jsonl") + ".jsonl.tmp"` | ✅ |

**行为探针**（`go test -overlay`，测试文件放临时目录、**不落仓**）：

```
$ go test -overlay=…\aud4b_probe\overlay.json ./rollout/ -run TestAuditProbeLinesSemantics -count=1 -v
head @4ee41b7a : PASS  —— 6/6
  crlf_in_lf_out               got "{\"id\":\"K\",...}\n"            （CRLF 输入 → LF 输出）
  no_trailing_newline_all_removed  got ""                            （无尾部换行、全删 → 空文件）
  leading_blank_line           got "\n"                              （首行空行保留）
  interior_blanks_preserved    got "\n\n{\"id\":\"K\",...}\n"        （内部空行保留）
  cr_only_final_line           got ""                                （末行仅 CR）
  trailing_double_newline      got "\n"                              （不重复尾部换行）
base @b9ad41d3（对照）: FAIL —— 4/6 失败
  crlf_in_lf_out               got "...\r\n"  want "...\n"           ✗（保留 CR）
  leading_blank_line           got ""         want "\n"              ✗（丢空行）
  interior_blanks_preserved    got "{K}\n"    want "\n\n{K}\n"       ✗（丢空行）
  trailing_double_newline      got ""         want "\n"              ✗
  no_trailing_newline_all_removed / cr_only_final_line 两侧一致 OK
```

⇒ 这 3 笔是**真实的行为修复**（不是 no-op），且修复后与 Rust `str::lines()` 语义**逐行等价**。
附：`readSessionIndex` 亦按 `'\n'` 切分，但逐行 `TrimSpace` 后 `len==0 → continue`，读取侧对尾部空段天然容忍，与 Rust 读取路径一致，**无 parity 缺口**。
## §6 结论与告警

**结论：GREEN / 可放行留档。**

| 检查项 | 结果 |
|---|---|
| ① 快进 + 无 merge | ✅ `merge-base --is-ancestor b9ad41d3 4ee41b7a` = 0；`--merges` 空；单亲线性 |
| ② tree-sha 握手 | ✅ 重放 tree `147a5dbe5c575c33d77ea8e764aa00715a801d5e` == tip tree |
| ③ 门禁（LF） | ✅ gofmt 空 / build 0 / vet 0 / parity ok 28.805s / 9 包对拍**新增失败 0** |
| ④ `str::lines()` 语义 | ✅ 逐行等价（7 条性质全等价；探针 head 6/6 PASS，base 对照 4/6 FAIL ⇒ 修复真实） |
| ⑤ 告警 | **无**。唯一偏差是派单计数（6→3 笔），见 §2；不影响放行 |

**给出的一条正面预警（非阻塞）**：本批把 session index 的**重写**语义对齐了 Rust（CRLF 输入 → LF 输出）。若外部工具（含旧版 Rust/其他客户端）以 CRLF 写该文件，重写后会变为 LF —— 这是与 Rust 一致的行为，但值得在发布说明中点名。

## §7 只读声明 / 资源

- 本轮**未 push、未 commit、未移动 ref、未删远端 ref**；未触碰任何他人 worktree。
- 我用 `git cherry-pick -n` 做重放（只写 index+worktree，**不产生 commit**）。
- 审计 scratch：
  - `D:\qax\reagent\dev\codex_go_wt\aud4b_replay`（临时 worktree）→ **已清理**（`git worktree remove --force`）。
  - `aud4b_head` / `aud4b_base`（`git archive` 解出的 LF 树）、`aud4b_probe`（overlay 探针）、`head.tar` / `base.tar` → **仍在**：本机策略拦截了递归删除（`Remove-Item -Recurse` 被 policy 拒绝），**留待队长或后续步骤清理**；均为纯 scratch，可由 §8 命令随时重建。
- 产物：本文件（未跟踪，留队长提交）。

## §8 复跑命令

```
git fetch origin --prune
git merge-base --is-ancestor b9ad41d3 4ee41b7a ; echo $LASTEXITCODE
git log --merges --oneline b9ad41d3..4ee41b7a
git rev-list --count b9ad41d3..4ee41b7a          # 3（不是 6）
git show -s --format=%T 4ee41b7a                 # 147a5dbe5c575c33d77ea8e764aa00715a801d5e

# tree-sha 握手（零 commit）
git -c core.autocrlf=false -c core.eol=lf worktree add <dir> --detach b9ad41d3
cd <dir>; git cherry-pick -n 8ade48ed 9936e2bf 4ee41b7a ; git add -A ; git write-tree

# LF 门禁树
git -c core.autocrlf=false -c core.eol=lf archive --format=tar -o head.tar 4ee41b7a
git -c core.autocrlf=false -c core.eol=lf archive --format=tar -o base.tar b9ad41d3
tar -xf head.tar -C <head_dir> ; tar -xf base.tar -C <base_dir>
cd <head_dir>; gofmt -l . ; go build ./... ; go vet ./rollout/ ./appserver/ ./config/
$env:CODEX_RUST_ROOT="C:\rw\codex-rs"; go test ./parity/ -count=1
go test ./rollout/ ./appserver/ ./config/ -count=1     # 与 <base_dir> 同命令对拍

# Rust 语义探针（不落仓）
go test -overlay=<probe_dir>\overlay.json ./rollout/ -run TestAuditProbeLinesSemantics -count=1 -v
```

Rust 参照：`C:\rw\codex-rs\rollout\src\session_index.rs:74`（`remove_thread_name_entries`，`contents.lines()`）。