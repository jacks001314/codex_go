# syncl3 · round87 · 两条 TUI 既有差异候选 —— 只读定案

- 车道：syncl3（Linux 节点 `de1bb1e71f8f7ad6969025798b555057`）｜队长：syntropy（Windows）
- 派单：`msg-1791374874791911700-4982`
- 基线：Go `origin/main = 185d03c933086ddd2b0a1f498fdcf7f9d2683d70`（含我这笔 `ab4b9f3e sync631 #51627`）；Rust 对照 `origin/main = b17c74cfd5ebb39fe70ffaff78de198120278636`
- 只读工作树：`/tmp/wt-185d03c9`（detached，`git status --porcelain` 空）；探针只在 `/tmp`（`-overlay`）
- 纪律：**0 commit / 0 push / 0 ref 移动 / 未改任何源码**；本单**无补丁**；Rust parity 检出仍停在 `5b0b253035`（未碰）

## 0. 结论一览

| # | 候选 | Rust PR/sha（`git log --grep -F` 自解） | Go 载体 `file:line` | 五分类 | 级别 | 避让面 |
|---|---|---|---|---|---|---|
| A | `applyTranscriptNavigationKey` 吞掉**被显式配置**的导航键 | `#50389` = `f88a6efe437eec1329d8e8810c788c90ac547888`（规则来源）；gate 源自 `#46733` = `f1feda2299990ab3678e715fe9d39d75a02f0491` | `tui/tea/model.go:8328`（调用点 `tui/tea/model.go:3023`、`tui/tea/message_router.go:93`） | **真缺口** | **S** | **不撞**（`tui/tea/*` 不在禁碰清单；但这在 syncw4 的 TUI 写集范围内，见 §3.3） |
| B | `ctrl-space` 在 Go 运行期**不可表示**（配置可写、按不到） | `#18593` = `5e737372ee09db8a0941ca0946385f64a854de61`（C0→ctrl 归一的出处）；被 `#46732` = `d7d9f2b9b53b53568b30c4dea7e095ec276ebe9c`、`#50389` 实际使用 | `tui/tea/keymap_runtime.go:300 keySpecFromKeyMsg`（`:305-306` / `:315-316` 是错别名）；配置侧 `tui/keymap_config.go:342 NormalizeKeybindingSpec`、`:685 keyName` | **真缺口** | **S** | **不撞**（同上） |

两条都**不是**「无载体型」：Go 缺少的是**规则/归一化**，而不是整个子系统（Rust 的 `owned_transcript` 在 Go 确实不存在，但这两条的行为在 Go 侧的对应载体都存在且可观测，见 §1.2 / §2.3）。两条也**都不是**「已取代型」：`b17c74cfd5` 上 `owned_transcript.rs` / `key_hint.rs` 仍是活代码，且 Go 侧无替代实现。

---

## 1. 候选 A：显式配置的导航键被 transcript 导航吞掉

### 1.1 Rust 语义（`b17c74cfd5`）

`codex-rs/tui/src/app/owned_transcript.rs:501-524`（`#50389` 之后）：

```rust
if let TuiEvent::Key(key) = event
    && !self.transcript_view.has_active_interaction()
    && keymap_action_ids().any(|action| {
        action.context != KeymapContext::Pager
            && !matches!(action.action, "find_transcript" | "focus_activity")
            && self.active_keymap_contexts().contains_action(action)
            && !(empty_enter_returns_to_latest && … "submit")
            && ((key.modifiers.is_empty() && matches!(key.code, PageUp | PageDown))
                || JumpTarget::from_key(*key).is_some()
                || configured_binding_for_action(&self.local_settings.tui.keymap, action)
                    .is_some_and(std::option::Option::is_some))     // ← #50389 新增
            && bindings_for_action(&self.keymap, action.context.config_name(), action.action)
                .is_some_and(|bindings| bindings.is_pressed(*key))
    })
{
    return Ok(false);   // 交回 app：让 keymap（composer.submit / chat.* 等）生效
}
```

- 规则一句话（`#50389` commit message 原文）：*"Honor configured keybindings before transcript navigation"* —— **用户显式配置**给某个 active（非 pager）动作的键，优先于 transcript 导航。
- `configured_binding_for_action`（`keymap/bindings.rs:167`）含 **global 回退**（`composer.submit` 未设时看 `global.submit`）；`bindings_for_action`（`:208`）读 runtime keymap。
- 该 gate 的骨架（`keymap_action_ids().any(...)` + `return Ok(false)`）由 `#46733` = `f1feda2299` 建立；`#46732`（`d7d9f2b9b5`）引入 transcript 选择/导航键。
- **Rust 行为**：`composer.submit = page-up` 时，按 page-up **不会**滚动 transcript，而是交回 app ⇒ 提交（`#50389` 前后一致；`#50389` 额外把「非 PgUp/PgDn/JumpTarget 形状但被显式配置」的键也纳入）。

### 1.2 Go 载体（正向证据）

```
$ grep -n 'applyTranscriptNavigationKey' tui/tea/*.go        # @185d03c9
tui/tea/message_router.go:93:	if m.applyTranscriptNavigationKey(msg) {
tui/tea/model.go:3023:		if m.applyTranscriptNavigationKey(msg) {
tui/tea/model.go:8328:func (m *Model) applyTranscriptNavigationKey(msg bubbletea.KeyMsg) bool {
```

`tui/tea/model.go:8328-8347`：**硬编码 6 个键**（`KeyPgUp/KeyPgDown/Home+CtrlHome/End+CtrlEnd`）直接滚动 transcript，
**从不查询 keymap**；调用点 `:3023`（主派发链）位于 `global.*` 之后、`composer.submit`(`:3068`)/`composer.queue`/`chat.*` **之前**，
`message_router.go:93` 亦然（输入历史之前）。

```
$ git grep -n 'keymap_action_ids\|configured_binding_for_action' 185d03c9 -- tui/tea/model.go
（无命中：Go 该函数没有任何 keymap 查询）
```

### 1.3 值级探针（`-overlay`，测试只在 `/tmp/probe50389/`）

```
$ cd /tmp/wt-185d03c9 && go test -overlay=/tmp/probe50389/overlay185.json ./tui/tea/ \
    -run TestProbe50389ConfiguredBindingVsTranscriptNav -count=1 -v
--- PASS: TestProbe50389ConfiguredBindingVsTranscriptNav (0.00s)
    --- PASS: .../default                                  zz_probe50389_test.go:42: default page-up: offset 155->151 composer="hello there"
    --- PASS: .../composer.submit=page-up                    zz_probe50389_test.go:50: configured composer.submit=page-up, pressed page-up: offset 155->151 composer="hello there"
```

- 默认 page-up ⇒ 滚动（`155→151`）＝ 与 Rust 默认一致 ✔
- **显式配置 `composer.submit=page-up` 后按 page-up ⇒ 仍然滚动（`155→151`）、composer 未提交**
  ⇒ Go 吞掉已配置快捷键；Rust 会交回 app 提交 ⇒ **值级分歧成立**。

### 1.4 判定与最小落点（若立项）

- **真缺口 / S 级**：≤3 文件（`tui/tea/model.go` + 可能的 `tui/keymap_config.go`/`tui/tea/keymap_runtime.go`）。
- 最小落点：`applyTranscriptNavigationKey`（或其在 `model.go:3023` / `message_router.go:93` 的调用点）在消费导航键前，
  先判断该键是否被**显式配置**给某个 active 且非 pager 的动作；是则不消费、交回后续派发。
  Go 现成查询面：`codextui.KeymapConfig.HasCustomBinding`（`tui/keymap_config.go:109`）、`Binding`（`:94`）、
  `ResolvedKeymapBindings` / `KeymapActionHasBinding`（`:328`）、`Model.keyMatches`（`tui/tea/keymap_runtime.go:287`）。
  需**照抄 Rust 的 global 回退语义**（`composer.submit` 未配时看 `global.submit`；`keymap/bindings.rs:167-183`），否则留下第二处差异。
- 仓内回归测试：`tui/tea/*_test.go` 增加「configured `composer.submit=page-up` ⇒ 提交而非滚动」+「默认 page-up 仍滚动」两条。
- 避让面：**不撞**（`appserver/runtime_router.go` / `appserver/turn_runtime.go` / `tui/state.go` / `sandbox/windowssandbox/` 均不涉及）；但见 §3.3 的车道写集提醒。

---

## 2. 候选 B：`ctrl-space` 在 Go 不可表示

### 2.1 Rust 语义（`b17c74cfd5`）

- 配置解析：`codex-rs/tui/src/keymap.rs:2703` `"space" => KeyCode::Char(' ')` ⇒ `ctrl-space` = `Char(' ')` + `CONTROL`。
- 匹配归一：`codex-rs/tui/src/key_hint.rs:126-133`，`KeyBinding::is_press` 对**两侧**都跑 `normalize_key_parts`；
  `:153-177`：

```rust
fn c0_control_char_to_ctrl_char(ch: char) -> Option<char> {
    let code = u32::from(ch);
    match code {
        0x00 => Some(' '),                                    // ← Ctrl+Space 的 C0 编码
        0x01..=0x1a => char::from_u32(code - 0x01 + u32::from('a')),
        0x1c..=0x1f => char::from_u32(code - 0x1c + u32::from('4')),   // 0x1f → '7'（Ctrl+/）
        _ => None,
    }
}
```

- 运行期形状（`#50389` 的回归测试原文，`app/owned_transcript_input_tests.rs:112`）：
  `TuiEvent::Key(KeyEvent::new(KeyCode::Char(' '), KeyModifiers::CONTROL))` —— 即 Rust 用 `Ctrl+Space` 提交 turn。
- 出处：`c0_control_char_to_ctrl_char` 由 **`5e737372ee`（`#18593` "feat(tui): add configurable keymap support"）** 引入；
  `ctrl-space` 作为**被使用的绑定**出现在 `#46732`（`d7d9f2b9b5`）与 `#50389`（`f88a6efe43`）。

### 2.2 Go 载体（正向证据）

```
$ grep -rn 'ctrl-space' --include='*.go' .        # @185d03c9（含测试）
（0 命中）
$ grep -rn 'ctrl-7\|KeyCtrlUnderscore\|Runes\[0\] == 0' tui/tea/keymap_runtime.go
305:	if key.Type == bubbletea.KeyCtrlUnderscore && !key.Alt {
306:		return "ctrl-7"                                  # 0x1f → ctrl-7 ✔（与 Rust 一致）
315:	if key.Type == bubbletea.KeyRunes && len(key.Runes) == 1 && key.Runes[0] == 0 && !key.Alt && !key.Paste {
316:		return "ctrl-7"                                  # ← 0x00 被错别名成 ctrl-7（Rust: ctrl-space）
```

- NUL（0x00，Ctrl+Space）在 Go 的 `keySpecFromKeyMsg`（`tui/tea/keymap_runtime.go:300-355`）里
  **没有**到 `space` 的映射：ANSI 路径 bubbletea 给 `KeyCtrlAt` ⇒ `message.String()`="ctrl+@" ⇒ 归一 `"ctrl-@"`；
  conhost 路径给 `KeyRunes{NUL}` ⇒ 代码里被当作 **Ctrl+/ 的 NUL 别名**返回 `"ctrl-7"`。
- 配置侧却**接受**该键：`tui/keymap_config.go:342 NormalizeKeybindingSpec` + `normalizeKeyName`（`:663-690`，`:685` 显式允许 `"space"`）
  ⇒ `ctrl-space` 能写进 `tui.keymap`，但运行期**永不匹配**（探针见 §2.3）。
- Go 测试只覆盖 ctrl-7 别名（`tui/tea/side_shortcut_test.go:33`），无 `ctrl-space` 用例。

### 2.3 值级探针（同 §1.3 探针）

```
    --- PASS: .../key-spec_mapping   zz_probe50389_test.go:53: KeyPgUp -> "page-up"
                                     zz_probe50389_test.go:54: KeyCtrlAt(nul) -> "ctrl-@"
                                     zz_probe50389_test.go:55: KeyRunes NUL -> "ctrl-7"
                                     zz_probe50389_test.go:57: cfg.Set(composer,submit,ctrl-space) err = <nil>
    --- PASS: .../composer.submit=ctrl-space  zz_probe50389_test.go:65: configured composer.submit=ctrl-space, pressed ctrl-space: offset 155->155 composer="hello there"
```

- `cfg.Set("composer","submit",["ctrl-space"])` **成功**（配置侧可表达）
- 按 Ctrl+Space（`bubbletea.KeyCtrlAt`）⇒ 既未提交（composer 文本不变）也未滚动（`155→155`）⇒ **配置的绑定是死键**
  ⇒ Rust 在该配置下会提交（`#50389` 的正是这个场景） ⇒ **值级分歧成立**。

### 2.4 判定与最小落点（若立项）

- **真缺口 / S 级**：单文件可修 —— `tui/tea/keymap_runtime.go:keySpecFromKeyMsg` 增加 NUL→`ctrl-space` 的归一
  （`KeyCtrlAt` 与 `KeyRunes{NUL}` 两个入口），并把 `KeyRunes{NUL}` 的 `"ctrl-7"` 错别名改掉；
  与 Rust 的 `0x00 => Some(' ')` 对齐（Rust 的 `0x1c..=0x1f → ctrl-4..ctrl-7` 说明 ctrl-7 只对应 0x1f）。
- 仓内回归测试：`tui/tea` 增加 `KeyCtrlAt → "ctrl-space"`、`KeyRunes{0} → "ctrl-space"`，
  以及「configured `composer.submit=ctrl-space` ⇒ 提交」的值级用例。
- 避让面：**不撞**（无禁碰文件）。
- 备注：Rust 侧 `KeyCode::Null` 是合成哨兵（`app.rs:1082` 等），不参与 keymap 匹配；真实 Ctrl+Space 走
  `Char(' ')+CONTROL`（crossterm）或 `Char('\0')`+C0 归一（`key_hint.rs:172-177`）。Go 两个入口都有、但都归错了。

---

## 3. 说明与提醒

### 3.1 为什么不是「无载体型」
Rust 的 `app/owned_transcript.rs`、`transcript_view/` 在 Go 确实**不存在**（上一轮 `#50389` 的 triage 结论不变），
但这两条候选的分歧**发生在 Go 已有的对应载体上**：A 在 `tui/tea/model.go:8328`（Go 自己的 transcript 视口导航），
B 在 `tui/tea/keymap_runtime.go:300`（Go 自己的键位规范化）。两者都有**值级探针**可复现 ⇒ 记**真缺口**。

### 3.2 与 `#50389` 的关系（避免重复立项）
`#50389`（PR 本体：`owned_transcript` 的谓词重构）在 Go **无载体**，本轮结论不变；
但 A/B 是它**固化下来的两条可移植规则**在 Go 侧的缺失（A 的优先级规则、B 的 ctrl-space 键位）。
若队长立项，建议**不挂在 `#50389` 名下**，而以「Go TUI 键位/派发优先级」独立工单推进。

### 3.3 车道写集提醒（**建议先协调**）
上一轮台账在飞写集里 `rollout/` + **TUI** 属 **syncw4**。A/B 的落点都在 `tui/`（`tui/tea/model.go`、
`tui/tea/keymap_runtime.go`、可能的 `tui/keymap_config.go`），**未命中任何显式禁碰面**，但**与 syncw4 写集可能重叠**。
⇒ 按派单规则：「若发现可裸落的 S 级真缺口，先报我再动手」——**本单已按此停手**，等队长裁定 + 协调写集后再决定是否落地。

---

## 4. 复跑命令

```zsh
# 环境
cd /home/jacks/jacks_dev/codex_go && git rev-parse origin/main           # 185d03c9
cd /home/jacks/jacks_dev/codex    && git rev-parse origin/main           # b17c74cfd5

# sha 自解
cd /home/jacks/jacks_dev/codex
for p in 50389 46733 46732 18593; do printf "#%s -> " $p; git log --grep="#$p" -F --format='%H %s' -1 origin/main; done

# Rust 语义
git show b17c74cfd5:codex-rs/tui/src/app/owned_transcript.rs | sed -n '495,545p'
git show b17c74cfd5:codex-rs/tui/src/key_hint.rs | sed -n '150,185p'
git show b17c74cfd5:codex-rs/tui/src/keymap.rs | sed -n '2696,2710p'
git show b17c74cfd5:codex-rs/tui/src/keymap/bindings.rs | sed -n '165,225p'
git show b17c74cfd5:codex-rs/tui/src/app/owned_transcript_input_tests.rs | sed -n '96,120p'

# Go 载体（只读工作树）
cd /tmp/wt-185d03c9
grep -n 'applyTranscriptNavigationKey' tui/tea/*.go
sed -n '8328,8348p' tui/tea/model.go
grep -n 'ctrl-7\|KeyCtrlUnderscore\|Runes\[0\] == 0' tui/tea/keymap_runtime.go
grep -n 'func NormalizeKeybindingSpec\|func (c \*KeymapConfig) HasCustomBinding' tui/keymap_config.go

# 值级探针（只读，测试只在 /tmp）
go test -overlay=/tmp/probe50389/overlay185.json ./tui/tea/ -run TestProbe50389ConfiguredBindingVsTranscriptNav -count=1 -v
```

## 5. 未决（需队长裁定）

1. A（configured 导航键被吞，S）是否立项？落 `tui/tea/model.go`，**与 syncw4 写集需协调**。
2. B（`ctrl-space` 不可达，S）是否立项？落 `tui/tea/keymap_runtime.go`，**同样需与 syncw4 协调**。
3. 若两条同时立项，建议 B 先（单文件、风险低），A 后（需抄 global 回退语义 + 优先级回归）。
