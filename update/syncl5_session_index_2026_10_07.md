# syncl5 · round 86 · session_index 保真修复（S）交付报告（2026-10-07）

## 0. 基线 / 环境
- Go `origin/main = b9ad41d3`（sync617 #49260）；worktree `codex_go_wt/syncl5g @ b9ad41d3`（改动未提交）。
- Rust `/home/jacks/jacks_dev/codex` `origin/main = a513012869`（仅 fetch，未改他人工作树；参照对象为固定点 `5b0b253035`）。
- 探针只放 `/tmp`；未跑 `go test ./...`；未 commit/push。

## 1. 选题（leader §3 约束③）
- **选条：`rollout/session_index.go` `RemoveThreadNameEntries` 的 Rust 保真修复**（你上一轮已登记为「候选 S，待裁」；本域 appendix E 的 PR 已全部 N/A/L，见 §5）。
- 上游参照（非单条 PR，而是 Rust 现存实现）：
  - 载体 `git show 5b0b253035:codex-rs/rollout/src/session_index.rs`（`remove_thread_name_entries`，74–105 行）：`for line in contents.lines() { ... remaining.push_str(line); remaining.push('\n') }`
  - 包装它的 PR = **#49708**（`4f699cd64227f42ff1094b0d3704ff8c3d05c96d`，"Move session index I/O off async runtime threads"）；`contents.lines()` 循环本身来自更早的 `#25018`（`a19d43a40a`）。
- Rust `--stat`（#49708）：
```
 codex-rs/rollout/src/session_index.rs      |  ...
 codex-rs/rollout/src/session_index_tests.rs|  ...
```
（`git -C /home/jacks/jacks_dev/codex show --stat 4f699cd642`）

## 2. 缺口（值级、字节级）
Rust `contents.lines()`：不产生尾随空行、保留**内部空行**、把 `\r\n` 归一为 `\n`。
Go 旧实现 `bytes.Split(data, "\n")` + `if len(line)==0 { continue }`：**丢弃所有空行**且**保留 `\r`** ⇒ 同一输入下写回的 `session_index.jsonl` 与 Rust 逐字节不同。

## 3. 修复（`rollout/session_index.go`，+19/−6）
```go
	// Mirror Rust `remove_thread_name_entries`, which iterates `contents.lines()`:
	// a trailing newline does not yield a final empty line, an internal blank line
	// is preserved, and a `\r\n` terminator is normalized to `\n`. Go's
	// `bytes.Split` both invents a trailing empty element and (previously) dropped
	// every blank line, diverging from the Rust index file byte-for-byte.
	lines := []string{}
	if len(data) > 0 {
		lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		var entry SessionIndexEntry
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &entry) == nil && entry.ID == threadID {
			removed = true
			continue
		}
		remaining.WriteString(line)
		remaining.WriteByte('\n')
	}
```
（`removed == false` 时早退、不重写文件；`contents == ""` 不产生任何行 —— 与 Rust 一致。）

## 4. 新增测试（`rollout/session_index_test.go`，+28）
`TestRemoveThreadNameEntriesPreservesBlankLinesAndNormalizesCRLFLikeRust`：输入含内部空行 + `\r\n`，
断言写回结果逐字节等于 `"\nnot-json\n{keep}\n"`。既有
`TestRemoveThreadNameEntriesPreservesOtherAndMalformedLines` 保持 PASS。

## 5. 门禁（原文）
1. `gofmt -l rollout/session_index.go rollout/session_index_test.go` → 空
2. `go build ./...` → exit 0
3. `go vet ./rollout/` → exit 0
4. 整包对拍 `rollout`（基线 `b9ad41d3`）：baseline `ok codex_go/rollout 0.572s`（0 FAIL）；改动后 `ok codex_go/rollout 0.606s`（0 FAIL）⇒ **新增失败 0**
5. 值级 RC（§6）
6. parity：`CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1` → `ok codex_go/parity 0.764s`

## 6. 值级 RC 原文
```
stripped fix
=== go test with prod wiring stripped (expect FAIL) ===
--- FAIL: TestRemoveThreadNameEntriesPreservesBlankLinesAndNormalizesCRLFLikeRust (0.00s)
    session_index_test.go:100: remaining index = "not-json\r\n{\"id\":\"keep\",\"thread_name\":\"kept\",\"updated_at\":\"2\"}\r\n", want "\nnot-json\n{\"id\":\"keep\",\"thread_name\":\"kept\",\"updated_at\":\"2\"}\n"
FAIL
FAIL	codex_go/rollout	0.006s
=== restore ===
restored ok
=== go test after restore (expect ok) ===
ok  	codex_go/rollout	0.005s
```

## 7. 产物
- `update/r86_patches/syncl5_session_index.patch` — **3014 B**；sha256 `4ef0550aa55c9e2acef9ff828de35e2a0efcb00a4d80b77dd38ed46de70bf1a3`；etag `547b90717b69d354`
  `git apply --check`（打 `b9ad41d3`，worktree `/tmp/wt-b9ad41d3-base`）：
  ```
  Checking patch rollout/session_index.go...
  Checking patch rollout/session_index_test.go...
  apply-check-exit=0
  ```
- `git status --porcelain`：
  ```
   M rollout/session_index.go
   M rollout/session_index_test.go
  ```

## 8. 本域 appendix E 剩余条目的 triage（本轮新增证据）
| PR | 结论 | 证据 |
|---|---|---|
| `#50454` | **L / 需架构决策**（Go 侧 0 载体，非单文件可对齐） | `grep -rn 'rollout\.persistence\|item_bytes\|bytes_removed' --include='*.go' .` → **0**；`codex.rollout.*` 全仓只有 `rollout_compression.*`。faithful port = 整个 `persistence_metrics` 模块（`measure_and_filter_rollout_items` + turn 边界统计 + 采样率 + writer 挂点） |
| `#49692` | **机制性 N/A** | Rust 用 tokio blocking worker 消除 per-line 任务调度；Go 无 async runtime（`grep -rn 'spawn_blocking' --include='*.go' .` → 仅 `appserver/runtime_router.go:1745` 一条注释）。Go 的 snippet 提取是同步内存函数 `appserver/router.go:3640 searchSnippet` |
| `#49694` | **机制性 N/A** | 同上；且 Rust 自述行为是 "Retain scan limits, stable file ordering..."（保持 + async 取消语义）。Go 的 plain/`.zst` 优先级只有单一实现 `rollout/rollout.go:1392 for _, candidate := range []string{plain, plain + ".zst"}`，无 sync/async 双实现分歧 |
| `#50189` | **机制性 N/A**（附带核实） | Go 只移植了 #47326 的 scheme 检查（`mcp/oauth_discovery.go:472 fetchMCPOAuthAuthorizationServerMetadata`），**没有** provider exception table / issuer-token origin 比对；`grep -rn 'authorization server origin' --include='*.go' .` → 0 |
| `#49416` | **机制性 N/A**（附带核实） | Go `utils/ansi_escape.go ANSIFirstLine` 无 multiline warning 日志载体（`grep -rn 'ansi_escape_line' --include='*.go' .` → 仅 Go 自己的 `ANSIFirstLine`） |

⇒ 本域（`eventmap/ execserver/ gitutil/ rollout/ state/ model/ codemode/ chatgptapi/ utils/`）在 appendix E 的 11 条
（`#49147 #49300 #50454 #49118 #49708 #49798 #49444 #49811 #49946 #49692 #49694`）已全部处置：8 条前轮 N/A + 本条修 `session_index` + 3 条本轮结论。
