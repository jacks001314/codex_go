# #53754 super 修饰键可行性判定（只判定，不实现）

- Date: 2026-11-02
- Worktree: `D:\qax\reagent\dev\codex_go_lane_gate` (`align/gate` @ 777c003f)
- 依赖版本（`go.mod`）：`github.com/charmbracelet/bubbletea v1.3.10`、
  `github.com/charmbracelet/x/ansi v0.11.6`（直接依赖）、
  `github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f`（Windows 输入）
- 结论先行：**v1.3.10 拿不到 super；升级 v2 是几周级全量迁移；不换依赖只能靠 fork + kitty
  协议做加法式改造。推荐 fork 的最小 Windows 切片先落地。**

## ① bubbletea v1.3.10 能不能拿到 super / ModSuper？—— 不能

证据（模块缓存源码 `C:\Users\huoga\go\pkg\mod\github.com\charmbracelet\bubbletea@v1.3.10`）：

1. **API 里没有修饰键位掩码。** `key.go:60-65`：

   ```go
   type Key struct {
       Type  KeyType
       Runes []rune
       Alt   bool
       Paste bool
   }
   ```

   唯一携带的修饰键是 `Alt`；ctrl/shift 被折叠成独立的 `KeyType` 常量（`KeyCtrlA`…`KeyShiftEnd`）。
   对整个模块 `rg "ModSuper|ModShift|ModCtrl|ModAlt"` → **0 命中**，不存在 `ModSuper` 这个概念。

2. **Windows 输入路径主动丢弃 Win 键。** `key_windows.go` 的 `readConInputs` 构造消息时：

   ```go
   msgs = append(msgs, KeyMsg{
       Type:  eventKeyType,
       Runes: runes,
       Alt:   e.ControlKeyState.Contains(coninput.LEFT_ALT_PRESSED | coninput.RIGHT_ALT_PRESSED),
   })
   ```

   而 Win 键（`VK_LWIN 0x5B` / `VK_RWIN 0x5C`，coninput `keycodes.go:89-90` 已导出）是以独立
   KEY_EVENT 到达的，`keyType()` 无对应分支；Windows 的 `dwControlKeyState` 本身也没有 win-key
   位（coninput `records.go:406-415` 只有 ALT/CTRL/SHIFT/CAPSLOCK 等）。即数据在 OS 层可用，
   但 bubbletea 既不发消息也不维持"win 键按下"状态。

3. **POSIX 路径把 CSI-u 当未知序列吞掉。** `key.go:614 detectOneMsg` → `key_sequences.go:57
   detectSequence`：先做静态序列表最长前缀匹配，再走
   `unknownCSIRe = ^\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]`（`key.go:610`），命中即返回
   **`unknownCSISequenceMsg`（未导出类型，`key.go:548`）**。kitty 的 `ESC[97;9u`（super+a）
   正好匹配该正则，于是变成未知 CSI 消息，**永远不会是 `KeyMsg`**，应用侧 516 处 `KeyMsg`
   处理点看不到它。
   整个模块 `rg -i "kitty|enhancement"` → **0 命中**：v1.3.10 不实现、也不请求 kitty 键盘协议。

**结论：不修改依赖的前提下，super 键在 v1.3.10 内不可达。**

## ② bubbletea v2 / 其它输入层

**本机无法验证 v2 源码**（如实记录，不靠记忆断言）：

- `go list -m -versions github.com/charmbracelet/bubbletea/v2` →
  `dial tcp 142.250.73.113:443: connectex: ... failed to respond`（proxy.golang.org 不可达）
- `git ls-remote --tags https://github.com/charmbracelet/bubbletea` →
  `Recv failure: Connection was reset`
- 模块缓存只有 v1.3.10（`cache/download/github.com/charmbracelet/bubbletea/@v/` 仅 v1.3.10.*），
  磁盘上不存在任何 `bubbletea/v2` 源码。

据上游发布说明（**offline 未验证，仅作背景**）：v2 把按键输入改为 `key.Key` + `Mod` 位掩码
（含 Shift/Alt/Ctrl/**Super**），在支持终端上启用 kitty keyboard protocol，消息类型改为
`tea.KeyPressMsg`；`View() string` 改为 `View() tea.View`，alt-screen/光标等移入 View 字段，
鼠标消息与部分 `tea.With*` 选项也有破坏性变化。

**升级代价（本仓库实测规模）**：

| 指标 | 数量 |
|------|------|
| 导入 bubbletea 的文件 | 330 |
| `tea.NewProgram` 调用点 | 8 |
| `tea.With*` 选项使用 | 31 |
| `tea.KeyMsg` 引用 | 516 |
| `KeyCtrl*`/`KeyRunes` 判定点 | 485 |
| `"ctrl+…"` / `"alt+…"` 绑定字符串 | 163 / 19 |

→ 全量迁移属于数周级、全仓库同时改动的工程，不可能作为单个 lane 的切片。**不推荐为了
super 键触发 v2 升级。**

## ③ 不改依赖用 kitty keyboard protocol（CSI-u）是否可行？

两个前提都具备，但都有硬限制：

- **可以发协议序列**：`x/ansi v0.11.6`（已是直接依赖）`kitty.go` 提供
  `KittyKeyboard(flags,mode)`、`PushKittyKeyboard(flags)`、`PopKittyKeyboard(n)`、
  `DisableKittyKeyboard`，即 `CSI > 1 u` 推送、`CSI < u` 弹出，无需新增依赖。
- **解码侧必须自己补**：如 ①.3 所述，v1 会把 CSI-u 交给 `unknownCSISequenceMsg`，所以要么在读
  tty 前做序列改写（`tea.WithInput` 包装 reader，把 `CSI cp;mod[:event] u` 翻译成 PUA 私有区
  rune 的 `KeyRunes`），要么 fork 依赖在 `key_sequences.go` 里解码。

**代价与风险**：

1. **Windows 上（a）方案完全无效**：bubbletea 走 `conInputReader` 读 Win32 记录，根本没有字节流；
   若为了拿到字节流改传自定义 reader，`newInputReader` 会退回 ANSI 读取（`inputreader_windows.go:28-31`），
   同时失去 mouse/resize 的 coninput 事件通道，且需要自行打开 `ENABLE_VIRTUAL_TERMINAL_INPUT`，
   老 conhost 不支持。
2. **开启 kitty 会改变所有按键的编码**（修饰键、重复、事件类型全变），仓库里 516 处 KeyMsg
   处理点都要经受未知 CSI 序列的冲击，属于高回归面。
3. **终端覆盖面**：kitty/WezTerm/Ghostty/foot/新版 Windows Terminal 支持；Terminal.app 完全不支持；
   iTerm2 部分支持。必须在退出/panic/SIGTERM 时 `PopKittyKeyboard`，否则终端停留在增强模式。
4. **super 常被系统/桌面环境截获**（macOS 需要系统级按键映射、Linux WM 常吃掉 Super 组合）。

## 推荐路径

**推荐：fork bubbletea v1.3.10（`replace` 到本仓内的薄补丁分叉），做加法式改造；分两步，不碰 v2。**

1. **最小可行切片（Windows 优先，本仓开发/CI 都在 Windows，且数据已可得）**：
   - 在 fork 的 `key_windows.go readConInputs` 里跟踪 `VK_LWIN/VK_RWIN` 的 down/up 状态
     （约 15 行），吞掉裸 Win 键事件；
   - `Key`/`KeyMsg` 增加 `Super bool` 字段（加法，现有 516 处处理点照常编译），`String()` 前缀
     `super+`；
   - 只挑一个动作（例如模型选择器里的某快捷键）验证端到端；
   - 单测覆盖：合成的 coninput 事件序列（LWIN down → 'k' down → LWIN up）应产生
     `Super && Runes=='k'`，且不产生裸 Win 键消息。
2. **只有当 POSIX 也需要 super 时**，在同一 fork 里补 CSI-u 解码（super 位 = 8），并在 POSIX
   上通过 `x/ansi` 推/弹 kitty flags（feature gate，默认关闭）。

**不做**：为 super 键升级到 bubbletea v2；也不建议用 `tea.WithInput` 改写 reader 的路线作为主线
（Windows 失效 + 丢失 mouse/resize 通道）。

## 风险清单

- fork 维护与上游漂移；若日后升级 v2，这份 fork 直接作废（可接受，因改动仅 ~15 行）。
- Windows 切片先落地会造成"只有 Windows 有 super 快捷键"的平台差异，需要 keymap 显式按平台注册。
- kitty 路线一旦默认开启会全局改变按键编码，必须 feature gate + 崩溃时恢复终端。
- 键盘层不是本仓库唯一入口：执行层/编辑器子进程（exec、vim 模式）也会收到同一批按键，
  需确认 super 不被当作普通字符插入。