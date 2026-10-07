# syncl3 · round 87 · TUI 键位双落地（B ctrl-space / A 导航键优先级）

派单：`msg-1791375094558296500-5070`（队长裁定 A/B 均立项 S 级，`tui/` 写集归 syncl3）
基线：`185d03c933086ddd2b0a1f498fdcf7f9d2683d70`（派单指定，sync632）
纪律：0 commit / 0 push / 0 tag / 0 ref 移动；两个 detached worktree（未建分支）；探针只在 `/tmp`。

---

## 0. 环境自检

| 项 | 值 |
|---|---|
| Go 仓 | `/home/jacks/jacks_dev/codex_go` |
| `git rev-parse main`（本机 main） | `a32e1c35facdbfec20887cce46da126d6ac0e68b`（round 86 docs；车道不推进 main） |
| 派单基线 | `185d03c933086ddd2b0a1f498fdcf7f9d2683d70`（`git fetch origin main` 后核对） |
| `git ls-remote origin main`（落地作业时） | `185d03c9…`（= 派单基线） |
| `git ls-remote origin main`（回报前复查） | 已前进 1 笔 → `99a81b23f6db91344625e99b146a600f70a1c115`（`sync633 …（#47898）`） |
| Rust 仓 | `/home/jacks/jacks_dev/codex` |
| `git rev-parse origin/main` | `b17c74cfd5ebb39fe70ffaff78de198120278636` |
| Rust 检出工作树 HEAD | `5b0b2530354052b9194156d70d4c94a439368342`（**未动**，parity 用） |
| worktree | `/tmp/wt-syncl3tuiB`（B，detached @185d03c9）、`/tmp/wt-syncl3tuiA`（A，detached @185d03c9）、`/tmp/wt-syncl3tuiAB`（B+A 合并验证）、`/tmp/wt-tip-r87`（@99a81b23 apply 复核） |

---

## 1. 交付 B —— `ctrl-space` 运行期可表示

- 补丁：`update/r86_patches/syncl3_tui_ctrlspace.patch`
- **8309 字节**，sha256 `4b6a3d97a0c2be9c973f0078f2497cced9026fed45805fa62377cda6db89dc19`
- 文件（3，≤5）：`tui/tea/keymap_runtime.go`(+15/−5)、`tui/tea/side_shortcut_test.go`(改写)、`tui/tea/keymap_ctrl_space_test.go`(新增 69 行)
- `git apply --check -v`（打 **干净 185d03c9**）：

```
Checking patch tui/tea/keymap_ctrl_space_test.go...
Checking patch tui/tea/keymap_runtime.go...
Checking patch tui/tea/side_shortcut_test.go...
exit=0
```

- 追加复核（打回报时的最新 tip `99a81b23`）：`Checking patch …` ×3，`exit=0`。

### 1.1 落点

- `tui/tea/keymap_runtime.go:316-318`：新增 `key.Type == bubbletea.KeyCtrlAt && !key.Alt ⇒ "ctrl-space"`（ANSI/NUL 字节路径，`keyNUL`）
- `tui/tea/keymap_runtime.go:319-321`：`KeyRunes{NUL}` 由 `"ctrl-7"` 改为 `"ctrl-space"`（Windows coninput 路径）
- `tui/tea/keymap_runtime.go:302-307`：`KeyCtrlUnderscore(0x1f) ⇒ "ctrl-7"` **原样保留**

### 1.2 Rust 依据

- `codex-rs/tui/src/key_hint.rs:175`：`0x00 => Some(' ')` ⇒ NUL 归一为 `ctrl-space`
- `codex-rs/tui/src/key_hint.rs:177`：`0x1c..=0x1f => code - 0x1c + '4'`（0x1f → `7`）⇒ `ctrl-7`（已与 Go 一致）
- Rust 回归锚：`codex-rs/tui/src/app/owned_transcript_input_tests.rs:96` `"composer": {"submit": ["ctrl-space", "f9"]}`（`#50389` 的 `configured_ctrl_space_submit_wins_over_transcript_selection`，同时覆盖 B 与 A）
- bubbletea v1.3.10 事实（已实证，`/home/jacks/go/pkg/mod/github.com/charmbracelet/bubbletea@v1.3.10`）：
  - `key.go:168` `KeyCtrlAt KeyType = keyNUL`（`keyNUL = 0`，与 `KeyNull` 同值）；`key.go:263` `keyNUL: "ctrl+@"`；`key.go:665` 独立 NUL 字节 → `KeyMsg{Type: keyNUL}`
  - `key_windows.go:230-244` coninput **先按虚拟键码分派**：`VK_SPACE` → `default` 分支 → `switch e.Char`（Ctrl+Space 的 Char 为 NUL，列表无 `'\x00'` case）→ 落到 `return KeyRunes` ⇒ `KeyRunes{[0]}`；而 `VK_OEM_2`（Ctrl+/）命中 `e.Char == '\x1f'` → `KeyCtrlUnderscore`

### 1.3 门禁实跑（worktree `/tmp/wt-syncl3tuiB`）

- `gofmt -l <3 文件>` → 空
- `go build ./...` → `go-build-exit=0`
- `go vet ./tui/tea/ ./tui/` → `go-vet-exit=0`（无输出）
- `go test ./tui/tea/ ./tui/ -count=1`：失败集合 = `TestModelAppCommandUsesRustHistoryMessages`(tui/tea)、`TestSelectStartupTooltipMatchesRustPlanBranches`(tui)
  - 基线 `/tmp/wt-185d03c9` 同命令失败集合 = **完全相同的 2 条** ⇒ **新增失败 0**
- `CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1` → `ok codex_go/parity 0.790s`（**无需 re-vendor `.zst`**）

### 1.4 值级 RC（撤生产接线 ⇒ FAIL ⇒ 恢复 ⇒ ok）

RC-B1（一次性撤掉两处修复，复原旧行为）：`tui/tea/keymap_runtime.go` sha256 `5135e421b3bb899670cb1d3ee16e62def76322c2f3964136d90c864e821c7904`

```
$ go test ./tui/tea/ -run 'TestCtrlSpaceNormalizationLikeRust|TestConfiguredCtrlSpaceSubmitLikeRust' -count=1 -v
=== RUN   TestCtrlSpaceNormalizationLikeRust
    keymap_ctrl_space_test.go:28: ansi NUL byte (KeyCtrlAt): keySpecFromKeyMsg = "ctrl-@", want "ctrl-space"
--- FAIL: TestCtrlSpaceNormalizationLikeRust (0.00s)
=== RUN   TestConfiguredCtrlSpaceSubmitLikeRust
    keymap_ctrl_space_test.go:63: ansi NUL byte (KeyCtrlAt): Ctrl+Space should submit, requests=[]tea.SubmitRequest(nil) composer="send once"
--- FAIL: TestConfiguredCtrlSpaceSubmitLikeRust (0.00s)
FAIL
```

RC-B2（只撤 Windows coninput 那一支，`KeyRunes{NUL}` 回落 `"ctrl-7"`）：

```
    keymap_ctrl_space_test.go:28: windows coninput NUL rune (KeyRunes{0}): keySpecFromKeyMsg = "ctrl-7", want "ctrl-space"
--- FAIL: TestCtrlSpaceNormalizationLikeRust (0.00s)
```

恢复后：文件 sha256 回到 `5135e421b3bb899670cb1d3ee16e62def76322c2f3964136d90c864e821c7904`，三条测试全 PASS。

### 1.5 ⚠️ 对既有测试的更正（必须知悉）

原 `tui/tea/side_shortcut_test.go` 的 `TestCtrlSlashTerminalEncodingTogglesSideConversation` 把 `KeyRunes{NUL}` 当成 **Ctrl+/** 的第二编码（断言其归一为 `ctrl-7` 并 toggle 侧对话）。该前提**不成立**：bubbletea coninput **先按虚拟键码判定**（`key_windows.go:230`），Ctrl+/ 走 `e.Char=='\x1f'` → `KeyCtrlUnderscore`；`KeyRunes{NUL}` 来自 `VK_SPACE`+Ctrl（即 **Ctrl+Space**），Rust 侧也归一为 `ctrl-space`（`key_hint.rs:175`）。因此：

- 既有断言 `KeyCtrlUnderscore(0x1f) → "ctrl-7"` **保留**（并把该形态的 toggle 断言保留）；
- 去掉把 NUL 当 Ctrl+/ 的那条编码，改由新文件断言 `KeyRunes{0} → "ctrl-space"`。
- 行为侧无回归：Ctrl+/ 在 ANSI 与 Windows 两条路径都仍是 0x1f → `ctrl-7` →（`model.go:3003` 的 `ctrl-7 ↔ ctrl-/` 特判）toggle 侧对话。

---

## 2. 交付 A —— 导航键优先级（configured 绑定优先于 transcript 滚动）

- 补丁：`update/r86_patches/syncl3_tui_navkey_priority.patch`
- **7632 字节**，sha256 `de288da5142ca5294d63f6a6ed88731647513c539a37853632c33e271ad95cc1`
- 文件（2，≤5）：`tui/tea/model.go`(+62/−2)、`tui/tea/nav_key_priority_test.go`(新增 110 行)
- `git apply --check -v`（打 **干净 185d03c9**）：

```
Checking patch tui/tea/model.go...
Checking patch tui/tea/nav_key_priority_test.go...
exit=0
```

- 追加复核（打 `99a81b23`）：`exit=0`。

### 2.1 落点

- `tui/tea/model.go:8334-8345`：`transcriptNavigationKeymapContexts` = {global, chat, composer, editor}；`transcriptNavigationKeymapVimContexts` = {vim_normal, vim_operator, vim_text_object}（后者仅 `m.vimMode` 时计入）
- `tui/tea/model.go:8356-8377`：`configuredBindingTakesNavKey(keySpec)` —— 遍历 `codextui.KeymapActions(...)`，跳过 pager 等 overlay 上下文与 `find_transcript`/`focus_activity`，仅当 `ResolvedKeymapBindings(...)` 的 `custom == true`（含 `"custom global"` 回退）且 `m.keyMatches(...)` 命中时返回 true
- `tui/tea/model.go:8379-8404`：`applyTranscriptNavigationKey` 在消费 6 个导航键**之前**插入 gate（`model.go:8391`），命中即 `return false`（交回正常派发链：submit / queue / interrupt / editor / vim）
- `tui/tea/message_router.go:93` 也调用同一函数，故同享该 gate（该 `routeKeyMsg` 目前**全仓无调用点**，为遗留骨架，仅如实记录，未改）

### 2.2 Rust 依据

- `codex-rs/tui/src/app/owned_transcript.rs:501-524`（`#50389` = `f88a6efe437eec1329d8e8810c788c90ac547888`，标题 *Honor configured keybindings before transcript navigation*）：命中 `keymap_action_ids().any(...)` ⇒ `return Ok(false)` 交回 app；过滤 `context != Pager`、`!matches!(action, "find_transcript" | "focus_activity")`、`active_keymap_contexts().contains_action(...)`，并排除 `empty_enter_returns_to_latest` 时的 `composer.submit`
- global 回退语义：`codex-rs/tui/src/keymap/bindings.rs:167-190` `configured_binding_for_action`（`composer.submit/queue/toggle_shortcuts` 未设时读 `global.*`）；Go 侧由既有 `codextui.ResolvedKeymapBindings`（`tui/keymap_config.go:199-207`）提供同款回退，gate 直接复用 ⇒ **未省 global 回退**
- Rust 用例锚：`owned_transcript_input_tests.rs:76-95`（`configured_ctrl_space_submit_wins_over_transcript_selection`）、`:126`（`default_page_up_still_scrolls_the_transcript`）

### 2.3 门禁实跑（worktree `/tmp/wt-syncl3tuiA`）

- `gofmt -l tui/tea/model.go tui/tea/nav_key_priority_test.go` → 空
- `go build ./...` → `go-build-exit=0`
- `go vet ./tui/tea/ ./tui/` → `go-vet-exit=0`
- `go test ./tui/tea/ ./tui/ -count=1`：失败集合 = 与基线**相同的 2 条**（同上）⇒ **新增失败 0**
- `CODEX_RUST_ROOT=…/codex-rs go test ./parity/ -count=1` → `ok codex_go/parity 0.797s`

### 2.4 值级 RC（撤生产接线 ⇒ FAIL ⇒ 恢复 ⇒ ok）

`tui/tea/model.go` sha256 `d881169f58e16e7c83419de285a0ed4737960e45b74a962385fc242140f20433`

撤掉 `model.go:8391` 的 gate 调用后：

```
$ go test ./tui/tea/ -run 'TestConfiguredNavKeyWinsOverTranscriptScrollLikeRust|TestDefaultNavKeyStillScrollsLikeRust' -count=1 -v
=== RUN   TestConfiguredNavKeyWinsOverTranscriptScrollLikeRust/composer.submit
    nav_key_priority_test.go:49: configured PageUp should submit, requests=[]tea.SubmitRequest(nil)
=== RUN   …/global.submit_fallback
    nav_key_priority_test.go:70: global.submit PageUp should submit, requests=[]tea.SubmitRequest(nil)
=== RUN   …/chat.interrupt_turn
    nav_key_priority_test.go:90: configured PageDown should interrupt, interrupts=0
--- FAIL: TestConfiguredNavKeyWinsOverTranscriptScrollLikeRust (0.00s)
    --- FAIL: …/composer.submit (0.00s)
    --- FAIL: …/global.submit_fallback (0.00s)
    --- FAIL: …/chat.interrupt_turn (0.00s)
=== RUN   TestDefaultNavKeyStillScrollsLikeRust
--- PASS: TestDefaultNavKeyStillScrollsLikeRust (0.00s)
FAIL
```

恢复后：sha256 回到 `d881169f…`，两条测试 PASS。（默认用例在撤修复时仍 PASS，正是「gate 只作用于 configured 绑定」的反证。）

---

## 3. B + A 合并可应用性（供队长一次性 apply 参考）

`/tmp/wt-syncl3tuiAB`（detached @185d03c9）依次 apply 两份补丁：

```
both applied
$ gofmt -l tui/tea/            # 空
$ go build ./...               # exit 0
$ go test ./tui/tea/ ./tui/ -count=1
--- FAIL: TestModelAppCommandUsesRustHistoryMessages   # 既有
--- FAIL: TestSelectStartupTooltipMatchesRustPlanBranches  # 既有
$ go test ./tui/tea/ -run '<5 条新增/更正测试>' -count=1 -v   # 全 PASS
$ CODEX_RUST_ROOT=…/codex-rs go test ./parity/ -count=1       # ok
```

两补丁文件集**互不相交**（keymap_runtime.go / side_shortcut_test.go / keymap_ctrl_space_test.go 与 model.go / nav_key_priority_test.go），apply 顺序无关。

---

## 4. 残余差异 / 未决（如实登记，未擅自扩面）

1. **Rust gate 的「默认绑定 + 导航键」分支未完全照搬**。Rust 的 gate 有 `(plain PageUp|PageDown) || JumpTarget::from_key(key).is_some() || configured_binding…` 三个并列条件，即某些**默认**绑定也会阻止 transcript 消费；Go 侧只实现 `custom == true`（显式配置）一支。今日**可观测等价**：main-surface（global/chat/composer/editor/vim_*）**默认**绑定中没有任何动作绑定 `page-up/page-down/home/ctrl-home/end/ctrl-end`（`tui/keymap.go:138-153` 的 `page-up/home/end` 只在 `pager`/`list` 上下文，均被排除），故该分支在当前默认表下不可观测。要做到逐字等价需引入 Rust 的 `active_keymap_contexts()` 概念，超出本单范围，等队长裁定。
2. **Go 已有的前存差异（本轮未动）**：`applyTranscriptNavigationKey` 比 Rust 多消费 **plain Home/End**（Go 会 jump top/bottom；Rust 的 `handle_scroll_key` 对 plain Home/End 不处理）。与 A 的 gate 正交，未修改。
3. `tui/tea/message_router.go` 的 `routeKeyMsg` 全仓无调用点（遗留骨架），本轮仅让它随 `applyTranscriptNavigationKey` 同享 gate，未做结构改动。
4. Rust `Alt+Shift+,/.`、`Alt+</>` 的 transcript 跳转在 Go 侧无载体（`#46732` 族），本轮不在 A 范围。

## 5. 纪律

0 commit / 0 push / 0 tag / 0 ref 移动；工作树均为 **detached HEAD**（未创建分支）。补丁与报告是唯一交付形式；工作区改动保留未提交（含新增文件）。
