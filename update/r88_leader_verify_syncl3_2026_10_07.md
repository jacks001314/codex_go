# leader 独立验证：`syncl3` 两补丁（`#48761` 方案1 + `#50786` B 补测行）（r88n，2026-10-07）

对象：
- `update/r86_patches/syncl3_48761.patch` — 14273 B / etag `68650741a42c7422`（7 文件：4 源 + 3 测试）
- `update/r86_patches/syncl3_50786_b2.patch` — 2479 B / etag `7d0e47992edb556f`（`config/config_test.go`）

基线 `c9fbd10f56d75e011b193f2bc3ad6e195bde05a9`（sync659）
工作树 `D:\tmp\leader_48761`（`git worktree add --detach … c9fbd10f`，apply 前干净）
纪律：**0 commit / 0 push / 0 ref 移动**；只在 scratch 工作树内；未碰 `C:\rw\codex-rs`。

---

## 1. apply 与预处理

```
$ git apply --check <A>   -> exit 0
$ git apply --check <B>   -> exit 0
$ git apply <A> <B>       -> exit 0
$ git status --porcelain
 M config/config_test.go
 M tui/exec_cell/exec_cell_test.go
 M tui/exec_cell/render.go
 M tui/history_cell/patches.go
 M tui/tea/model.go
 M tui/tool_output_preview.go
 M tui/tool_output_preview_test.go
?? tui/tea/transcript_hint_keymap_like_rust_test.go
```
该仓 `core.autocrlf=true` ⇒ 落盘 CRLF，已按 `git diff --name-only` 逐文件 LF 归一（共去 12878 个 CR）。

**字节级同一性（与提交者报告比对，全部命中）**：
```
tui/tea/model.go              053f85560e1db01824e3807367bd1ec52230b1b503f2b587eef161ede7818af0
tui/tool_output_preview.go    83943868aba0d52518503498b8c056b877cbd60f2c675f85554cefad2230163d
tui/exec_cell/render.go       478a377d417448ead8425c076aab56779eb7fbeb74701c5dfca5b47da0235d1c
```
（验证过程中一度对 `render.go` 产生过一次误改，已按字节还原到上列 sha256 并复跑通过；下列结论均基于还原后状态。）

## 2. 静态门禁

```
$ gofmt -l <7 个改动 .go>   -> (空)
$ go build ./...            -> exit 0
$ go vet ./tui/ ./tui/exec_cell/ ./tui/history_cell/ ./tui/tea/ ./config/   -> exit 0
```

## 3. 受影响包整包（本机，`-count=1`）

| 包 | 结果 |
|---|---|
| `./tui/` | **ok** 2.168s |
| `./tui/exec_cell/` | **ok** 0.101s |
| `./tui/history_cell/` | **ok** 0.075s |
| `./tui/tea/` | FAIL `TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge`（**既有基线**） |
| `./config/` | **ok** 0.597s |

**既有基线的证明**：该 `tui/tea` 测试在**未打补丁的冻结终点工作树**（`integ86g` @ c9fbd10f）上单独复跑同样 **FAIL**
⇒ 与补丁无关（环境/既有问题）。除此之外**新增失败 0**。
（提交者在 Linux 上见到的 `./config/` 4 条 Windows 面红，在本 Windows 节点上全绿，方向一致。）

## 4. 值级 RC（leader 自跑，4/4 复现；撤生产接线 ⇒ FAIL ⇒ 字节还原 ⇒ ok）

| RC | 落点与改法 | 结果 |
|---|---|---|
| RC-1 | `tui/tea/model.go:2271` 注入点 `SetOpenTranscriptHintProvider(...)` → `if false { … }` | FAIL `TestOpenTranscriptHintFollowsKeymapLikeRust` |
| RC-2 | `tui/tool_output_preview.go:114` 宽度门 `if p.width > 0 && …` → 恒真 | FAIL `TestToolOutputPreviewBoundsLongInput` |
| RC-3 | `tui/exec_cell/render.go:426` 的 hint 来源 → `""` | FAIL `TestOutputLinesTruncatesWithTranscriptHint`（`exec_cell_test.go:76: output lines:`） |
| RC-4 | `tui/tool_output_preview.go:49` 空值省略 `if label == ""` → 恒假 | FAIL `TestTranscriptDisclosureHintFollowsKeymapLikeRust` |

还原后 3 个文件 sha256 与 §1 完全一致；`./tui/` rc=0、`./tui/exec_cell/` rc=0、
`./tui/tea/ -run TestOpenTranscriptHintFollowsKeymapLikeRust` rc=0。

## 5. 结论

**两补丁在 `c9fbd10f` 上经 leader 独立验证：apply 干净、静态门禁全过、受影响包相对基线新增失败 0、
4 条值级 RC 全部复现且还原字节一致。**
⇒ 技术面**可落**。但**是否落主取决于用户对语义口径的裁定**（提交者已披露：Go 这 3 个渲染点并非
Rust `#48761` 真正改动的面，本补丁是把「配置化 + 放不下则省略」规则搬到 Go 现有的 marker 载体上；
三选一：①认可搬家直接落 ②改成与 Rust marker 面逐字节一致 ③改挂 `transcript_view`+footer 面）。
**leader 不擅自代替用户做该口径决定。**

## 6. 现场

- 保留：`D:\tmp\leader_48761`（两补丁就位，供复核）。
- 若选 ①：可直接 `git commit -F <msgfile>` → push `main` + 强推 `integ86g`。
- 若选 ②/③：需提交者（已下线）或新车道重做；当前两补丁保持未落主。
