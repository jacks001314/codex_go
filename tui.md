# TUI 架构对比分析：Crush vs codex_go

> 基于 `D:\qax\reagent\dev\crush-main` (Charm Crush) 与 `D:\qax\reagent\dev\codex_go` (OpenAI Codex Go 复刻) 的深度对比。

## 一、整体概览

两个项目都是 Go 语言编写的终端 AI 编程助手，共享 Charmbracelet 生态（Bubble Tea、Lip Gloss、Glamour）。

| 维度 | Crush (Charm) | codex_go (OpenAI 复刻) |
|------|---------------|------------------------|
| TUI 框架 | Bubble Tea **v2** + Ultraviolet 双渲染器 | Bubble Tea (v1) |
| 渲染方式 | **Cell-buffer** (Ultraviolet `uv.Screen`) | 传统 `View() string` |
| TUI 代码量 | ~25,000 行（model/ + chat/ + dialog/ 等12包） | ~76,000 行（tui/ 根 + 7 子包） |
| 核心 Model 文件 | `model/ui.go` 4,830 行 | `tea/model.go` 3,813 行，**160 个方法** |
| 根目录文件数 | ~20 个（在 model/ 下） | **136 个 Go 文件** |
| UI 状态管理 | 4 状态显式状态机 | `Status string` 无显式状态机 |
| 设计原则 | 独立产品，追求最佳 UX | 精确复刻 Rust upstream，追求 parity |

---

## 二、架构对比

### 2.1 Crush — 组件化 UI Model

```go
// model/ui.go — 单一 UI struct，按功能域划分字段
type UI struct {
    *common.Common           // 共享引用（workspace/config/styles）
    *session.Session         // 当前会话
    *Chat                    // 聊天列表组件（包装 list.List）
    textarea.Model           // Bubble Tea textarea（底部编辑器）
    *completions.Completions // 文件 @-mention 补全弹窗
    *attachments.Attachments // 附件 chips
    *dialog.Overlay          // 对话框栈（统一弹窗管理）
    *Status                  // 底部状态/帮助栏
    *header                  // 顶部品牌行
    dialog.InlineEditor      // 内联编辑器（替换 textarea 用于问答表单）
    // 通知、sidebar、pills、LSP/MCP/skills 状态...
}
```

**关键设计**：每个子组件是独立 struct，有自己的 `Draw()` / `Update()` / 状态管理。

```
Crush 组件树：
UI
├── header              (顶部: 品牌、工作目录、模型、credits)
├── sidebar             (左侧: 会话名、文件、LSP/MCP/skills 状态)
├── Chat                (中间: 虚拟滚动消息列表)
│   └── list.List       (通用虚拟列表, version-based 增量失效)
│       └── chat/       (30+ 文件，每种消息类型独立渲染)
│           ├── user.go
│           ├── assistant.go       (Markdown 渲染 + thinking)
│           ├── assistant_info_item (每轮 footer: model/provider/duration)
│           ├── tools.go
│           ├── shell.go
│           ├── bash.go / fetch.go / search.go / file.go / symbol.go / ...
├── pills               (展开式 todo/queue 面板)
├── dialog.Overlay      (栈式对话框系统, ~27 文件)
│   ├── commands.go     (Ctrl+P 命令面板, 三 tab: System/User/MCP)
│   ├── models.go       (模型选择)
│   ├── sessions.go     (会话浏览/删除/重命名)
│   ├── permissions.go  (权限请求: allow/deny/allow-for-session)
│   ├── question_*.go   (YesNo/Single/Multi/FreeText/Confirm/Editor)
│   ├── question_form.go (批量多问题表单, Tab 导航)
│   ├── oauth.go / oauth_hyper.go / oauth_copilot.go
│   ├── api_key_input.go / arguments.go / notifications.go
│   ├── quit.go / reasoning.go / filepicker.go
├── completions         (文件/路径补全弹窗, @ 触发)
├── attachments         (附件 chips + 删除模式)
├── textarea            (底部编辑器)
└── status              (底部帮助栏)
```

### 2.2 codex_go — 扁平化 State + 上帝 Model

```go
// state.go — 纯数据容器，扁平结构
type State struct {
    ThreadID, Model, ReasoningEffort, PlanMode, ...
    Status   string  // "idle" 等字符串
    Messages []Message
}

// tea/model.go — 真正的 Bubble Tea Model，承担一切
// 3,813 行，160 个方法，没有分拆到子组件
```

```
codex_go TUI 包布局：
tui/
├── tea/model.go              ← 上帝 Model: 160 个 apply* 方法
│   ├── applyCommand()        ← 60+ slash 命令路由
│   ├── applyDelta()          ← 增量消息处理
│   ├── applyThreadEvent()    ← 协议事件
│   ├── applyItemStarted()    ← 项开始
│   ├── applyItemCompleted()  ← 项完成
│   ├── applyHookRun()        ← Hook 执行
│   ├── applyHistoryCell()    ← 历史单元格
│   ├── applyRateLimitSnapshot()
│   ├── applyStreamMessage()
│   └── ... (150+ 更多)
├── tea/slash_popup.go        ← slash 命令弹窗
├── tea/slash_misc_commands.go ← 杂项命令 (/plan /review /rename /theme /pets)
├── tea/session_picker.go     ← 会话选择器
├── tea/model_picker.go       ← 模型选择器
├── tea/theme_picker.go       ← 主题选择器
├── tea/resume_picker.go      ← 恢复选择器
├── tea/agent.go              ← Agent 切换
├── tea/skills.go / plugins.go / hooks.go / apps.go / usage.go / goal.go
├── tea/attachments.go / memories.go / review.go / windows_sandbox.go
├── tea/keymap_runtime.go     ← 键盘映射运行时
├── tea/status_controls.go    ← 状态行/终端标题控制
├── tea/external_editor.go    ← 外部编辑器集成
│
├── bottom_pane/              ← 底部面板子组件 (~15 文件)
│   ├── chat_composer.go      ← 输入框
│   ├── command_popup.go      ← 命令弹出
│   ├── slash_commands.go     ← slash 命令处理
│   ├── approval_overlay.go   ← 审批覆盖层
│   ├── file_search_popup.go  ← 文件搜索
│   ├── skill_popup.go        ← skill 弹出
│   └── ...
│
├── chatwidget/               ← 消息渲染 widget
│   ├── status_controls.go    ← 状态控件
│   └── ...
│
├── history_cell/             ← 历史记录单元格 (9+ 文件)
│   ├── base.go / messages.go / exec.go
│   ├── approvals.go / plans.go / hooks.go
│   ├── mcp.go / notices.go / patches.go
│   └── session.go / separators.go / search.go / request_user_input.go
│
├── exec_cell/                ← 执行单元格
├── markdown/                 ← Markdown 流式渲染
└── streaming/                ← 流式处理 (chunking / commit_tick / controller / table_holdback)
```

### 2.3 状态管理差异

| | Crush | codex_go |
|---|---|---|
| **UI 状态机** | `uiOnboarding → uiInitialize → uiLanding → uiChat` | 无，靠 `Status string` 字段 |
| **焦点状态** | `uiFocusNone / Editor / Main / Sidebar` | 分散在 keymap 和 Update 逻辑 |
| **布局系统** | `uiLayout` struct: header/sidebar/main/pills/editor/status | 直接在 `View()` 中硬编码计算 |
| **紧凑模式** | 自动检测 (w≤120 或 h≤30) + 手动切换 | 无 |
| **后端通信** | `workspace.Workspace` 接口 + `pubsub.Event[T]` | JSON-RPC v2 函数指针注入 |

---

## 三、主题/样式系统对比

### 3.1 Crush — 四层主题架构

```
quickstyle.go (1,058行)       ← 主题构建引擎（纯函数）
    ↑                         quickStyle(opts) → Styles
    │                         输入: ~40 个语义色
    │                         输出: 完整 Styles struct（500+ 属性）
themes.go (117行)              ← Provider → Theme 映射
    ↑                         CharmtonePantera() / HypercrushObsidiana()
styles.go (682行)              ← Styles struct（~50 个子结构体）
    ↑                         Markdown, Tool, Editor, Messages,
    │                         Dialog, LSP, Sidebar, Pills, ...
grad.go (71行)                 ← 渐变色渲染工具
                              ForegroundGrad() 用于 Logo/动画
```

**语义化颜色名**（`quickstyle.go` 输入参数）：
```
charple   (primary)     ← 主色
dolly     (secondary)   ← 辅色
bok       (accent)      ← 强调色
sash      (foreground)  ← 前景色
pepper    (background)  ← 背景色
coral     (destructive) ← 破坏性操作
sriracha  (error)       ← 错误
zest      (warning)     ← 警告
mustard   (warning-alt) ← 警告备选
citron    (busy)        ← 忙碌指示
malibu    (info)        ← 信息
julep     (success)     ← 成功
```

**关键特性**：
- **纯函数生成器**：`quickStyle(opts) → Styles`，相同输入 → 相同输出（可测试、可缓存）
- **Provider 自动配色**：切换模型提供商自动切换主题（`ThemeKeyForProvider`）
- **色度集成**：`ChromaTheme()` 把主题色转成 `chroma.StyleEntries`
- **主题切换优化**：`themeKey` 相同时跳过重建

### 3.2 codex_go — 扁平 RGB 函数

```go
// style.go (49行) — 只有 3 个样式函数
func AccentStyleFor(terminalBG *RGB, colorLevel StdoutColorLevel) StyleSpec
func UserMessageBackground(terminalBG RGB, colorLevel StdoutColorLevel) TerminalColor
func TableSeparatorStyleFor(terminalFG, terminalBG *RGB, colorLevel StdoutColorLevel) StyleSpec

// color.go (71行) — 颜色工具
func IsLight(bg RGB) bool
func BlendRGB(fg, bg RGB, alpha float64) RGB
func PerceptualDistance(a, b RGB) float64
// + sRGB → XYZ → Lab 转换
```

**问题**：
- 没有统一的 Styles 聚合结构体
- 硬编码 RGB 值（如 `RGB{0, 95, 135}` lightBG accent, `RGB{0, 255, 255}` dark accent）
- 样式散落在 `chatwidget/`、`bottom_pane/`、`markdown/` 各处
- `theme_picker.go` (709行) 只切换色度主题，不管理系统级样式

### 3.3 对比总结

| | Crush | codex_go |
|---|---|---|
| 样式聚合 | 统一 `Styles` struct（~50 子结构体） | 散落在各包中的独立函数 |
| 颜色命名 | 语义化（`charple`, `pepper`, `coral`...） | 硬编码 RGB 元组 |
| 主题数量 | 2（Pantera, Obsidiana），可扩展 | 色度主题切换 |
| 主题切换 | Provider 自动匹配 + 缓存避免重建 | `theme_picker.go` 手动切换 |
| 色度集成 | `ChromaTheme()` 自动转换 | 无统一转换 |

---

## 四、渲染性能对比

| | Crush | codex_go |
|---|---|---|
| **渲染方式** | Ultraviolet cell-buffer（双渲染器） | 传统 Bubble Tea string View() |
| **ANSI 解析** | 缓存预解码 buffer，避免每帧重解析 | 每帧完整 string → ANSI 重解析 |
| **消息列表缓存** | **3 级**：item 级 → chat 级(F6 memo) → 预解码 cell buffer | 依赖 Bubble Tea viewport 内置优化 |
| **增量失效** | Version-based：item 状态变 → 版本号+1 → 列表跳过未变项 | 无 |
| **Scrollbar** | 自定义实现：always/default/never + 2s 自动隐藏 | 无独立 scrollbar |
| **Resize 优化** | 抑制 O(N) 总高扫描 → 120ms 防抖 → 每帧 25 条增量预热 | 无特殊优化 |
| **动画帧分发** | 只发给可见项，离屏项暂停 + 追踪 ID | shimmer.go 简单实现 |

**Crush 的渲染流水线**：
```
UI.Draw(scr, area)
  → Chat.Draw(scr, area)
    → list.Render()
      → 逐项 Render(width)
        → lipgloss 样式字符串
          → uv.StyledString.Draw(scr, area)
            → 写入 cell buffer
  → buffer.Render() → ANSI string → 终端

缓存命中时：直接复制 cell buffer → 零 ANSI 重解析
```

---

## 五、关键子系统对比

### 5.1 Diff 显示

| | Crush (`diffview/` 7 文件) | codex_go (`diff_render.go` + `diff_model.go`) |
|---|---|---|
| 布局 | Unified + **Split (side-by-side)** | Summary 级别（添加/删除行数） |
| 语法高亮 | Chroma per-line（diff 背景色分离） | 无 |
| 行号 | 支持 | 无 |
| 滚动 | 横向/纵向 + 宽度约束 | 无 |
| 主题集成 | `common/diff.go` 便捷包装器 | 无 |

### 5.2 动画

**Crush** (`anim/anim.go` 533 行) — 完整动画引擎：
```
- 乱码字符动画: "0123456789abcdefABCDEF~!@#$...+"
- 交错入场: 每列随机出生步数
- HCL 渐变色斜坡: 2色 或 4色循环
- 标签动画省略号: "Thinking..." → "Thinking.." → "Thinking..." → "Thinking...." → ""
- 帧率: 20 FPS via tea.Tick
- 全局帧缓存: csync.Map, 按键控哈希, 零分配复用
- NoScramble 模式: 纯标签+省略号（非 LLM 场景）
```

**codex_go** (`shimmer.go`):
```
- 简单的 shimmer/闪烁效果
- 无完整动画引擎
```

### 5.3 对话框系统

**Crush** — 统一 Overlay 栈：

```go
// dialog/dialog.go
type Overlay struct { stack []Dialog }

// 每个 Dialog 实现：
type Dialog interface {
    HandleMsg(tea.Msg) Action  // 返回类型化 Action
}

// Action 类型：
type Action int
const (
    ActionSelectModel Action = iota
    ActionNewSession
    ActionQuit
    // ...
)

// UI.Update 统一 dispatch：
func (m *UI) Update(msg tea.Msg) {
    if action := m.dialog.HandleMsg(msg); action != nil {
        switch action.(type) {
        case ActionSelectModel: ...
        // 一个 switch 处理所有弹窗结果
        }
    }
}
```

- **Grace period**：防止异步打开弹窗时 in-flight 按键误触
- **27 个文件**：每种弹窗独立实现，但共享 Overlay 框架

**codex_go** — 每个 picker 独立在 Model 上：

```
tea/model.go 上直接处理:
- session_picker.go   → applyResumeCommand / applySessionSelection / applyResumeResponse
- model_picker.go     → applyModelSetting
- theme_picker.go     → applyThemeCommand / applyThemeModalOption
- 每个 picker 管理自己的显示/隐藏状态
```

### 5.4 命令面板

**Crush** (`Ctrl+P`):
```
dialog/commands.go
├── 三 Tab: System | User (自定义命令) | MCP (prompts)
├── FilterableList + 文本搜索
├── System 命令列表:
│   New Session, Sessions, Switch Model, Summarize,
│   Toggle Thinking/Reasoning/Compact, External Editor,
│   Docker MCP, To-Dos/Queue, Notification Style,
│   Toggle Yolo/Help, Initialize Project, Quit...
└── Tab 切换, Enter 执行, Esc 关闭
```

**codex_go** (`/` 前缀):
```
60+ slash 命令:
/help /keymap /status /usage /goal /statusline /title
/debug-config /new /clear /copy /raw /diff /ps /stop
/model /personality /permissions /approval /sandbox
/experimental /mcp /skills /plugins /apps /review
/rename /theme /pets /plan /side /btw /agent
/multi-agents /ide /vim /import /hooks /memories
/feedback /resume /fork /archive /unarchive /delete
/attach /image /url-image /clear-attachments /editor
/logout /quit /exit /fast /setup-default-sandbox
/sandbox-add-read-dir /rollout ...
```

---

## 六、值得借鉴的具体方案

### 6.1 ⭐⭐⭐ 拆分 `tea/model.go`（优先级最高）

**问题**：`tea/model.go` 有 3,813 行、160 个方法，所有 UI 逻辑集中在一个结构体上。

**方案**：参照 Crush，按功能域拆分子组件：

```go
// 重构后的 Model
type Model struct {
    *common.Common              // 共享引用
    
    // 核心 UI 组件（每个是独立 struct）
    Chat          *ChatWidget   // 消息列表
    Composer      *Composer     // 输入框 + 附件 + 补全
    Overlay       *Overlay      // 统一弹窗管理
    Status        *StatusLine   // 底部状态栏
    
    // 功能状态（每个管理自己的逻辑）
    SessionPicker *SessionPicker
    ModelPicker   *ModelPicker
    ThemePicker   *ThemePicker
    // ...
    
    // 纯数据
    state         *codextui.State
}
```

**收益**：每个 `apply*` 方法归属到对应子组件，Model 只做消息路由。

### 6.2 ⭐⭐⭐ 统一 Styles 结构体

**问题**：颜色散落在各包中（`style.go` 3 个函数 + 各处硬编码）。

**方案**：参照 Crush `styles.go`：

```go
// 新建 tui/styles/styles.go
type Styles struct {
    Chat struct {
        UserBubbleBg      lipgloss.Color
        AssistantText     lipgloss.Color
        ToolCallBorder    lipgloss.Color
        ThinkingText      lipgloss.Color
        ErrorBg           lipgloss.Color
        // ...
    }
    Editor struct {
        PromptFg          lipgloss.Color
        PlaceholderFg     lipgloss.Color
        BorderFg          lipgloss.Color
        // ...
    }
    Status struct {
        BarFg             lipgloss.Color
        BarBg             lipgloss.Color
        ThreadIDFg        lipgloss.Color
        // ...
    }
    Dialog struct {
        OverlayBg         lipgloss.Color
        TitleFg           lipgloss.Color
        BorderFg          lipgloss.Color
        SelectedBg        lipgloss.Color
        // ...
    }
    // ... 每个子系统一个子 struct
}

// 主题定义
func DefaultDark() Styles { ... }
func DefaultLight() Styles { ... }
```

**收益**：集中管理所有颜色，一处修改全局生效，易于添加新主题。

### 6.3 ⭐⭐ 对话框 Overlay 栈

**问题**：每个 picker（session/model/theme/resume...）在 Model 上管理自己的显示/隐藏状态，代码重复。

**方案**：参照 Crush 的 Overlay 模式：

```go
type Dialog interface {
    ID() string
    Update(msg tea.Msg) DialogAction
    View(width, height int) string
}

type DialogAction int
const (
    DialogNone DialogAction = iota
    DialogClose
    DialogSelectModel
    DialogSelectSession
    DialogSelectTheme
    // ...
)

type Overlay struct {
    stack []Dialog
    graceUntil time.Time  // 防抖
}

func (o *Overlay) Push(d Dialog) { ... }
func (o *Overlay) Pop() Dialog    { ... }
func (o *Overlay) Active() Dialog { ... }
```

**收益**：所有弹窗共享栈逻辑，Model 只需一个 `Overlay` 字段 + 一个 `DialogAction` switch。

### 6.4 ⭐⭐ 动画引擎

**方案**：参照 Crush `anim/anim.go`，实现一个独立的动画包：

```go
// tui/anim/anim.go
type Spinner struct {
    Label     string
    Scramble  bool          // 乱码字符动画
    Colors    [2]lipgloss.Color  // 渐变色
    FPS       int           // 默认 20
    // ...
}

func (s *Spinner) Start() tea.Cmd
func (s *Spinner) Update(msg tea.Msg) (string, tea.Cmd)
func (s *Spinner) View() string

// 全局帧缓存（按设置哈希键控，零分配复用）
var frameCache csync.Map[string, []string]
```

**收益**：独立模块，不影响 parity，提升 Thinking/Loading 指示的体验质感。

### 6.5 ⭐ Diff 组件独立化

**方案**：参照 Crush `diffview/`，将 diff 渲染逻辑从 `diff_render.go` 升级为独立组件：

```go
// tui/diffview/diffview.go
type DiffView struct {
    Layout     DiffLayout  // Unified | Split
    ShowLineNo bool
    ContextLines int
    Width      int
    Height     int
}

func (d *DiffView) SetDiff(unifiedDiff string) error
func (d *DiffView) ScrollUp(n int)
func (d *DiffView) ScrollDown(n int)
func (d *DiffView) View() string
```

**收益**：diff 预览能力从 summary 升级为内联 diff viewer。

---

## 七、不推荐引进的特性

这些特性虽然优秀，但会偏离 codex_go 的 parity 目标：

| 特性 | 原因 |
|------|------|
| **LSP 集成** | codex_go 已有多元代码理解工具链（grep/glob/bash），LSP 是产品方向差异 |
| **Pub/Sub 事件总线** | 改变 TUI-后端通信架构，与 JSON-RPC parity 模式冲突 |
| **Ultraviolet 双渲染器** | 改变核心渲染路径，增加依赖，当前性能可接受的话不值得 |
| **本地模型自动发现** | codex_go 模型 catalog 是静态配置，动态发现偏离 parity |
| **Workspace 多客户端** | codex_go 的 app-server 已经处理了多客户端场景 |

---

## 八、附录

### A. Crush TUI 包结构

```
internal/ui/
├── model/        (20 files)   核心状态机、布局、事件处理
├── chat/         (30 files)   消息类型渲染（user/assistant/tools/shell/...）
├── common/       (13 files)   共享工具、scrollbar、buttons、chroma、diff 包装
├── styles/       (4 files)    四层主题系统
├── dialog/       (27 files)   栈式对话框系统
├── list/         (6 files)    通用虚拟滚动列表 + 过滤
├── completions/  (4 files)    文件/MCP 资源补全弹窗
├── diffview/     (7 files)    Unified/Split diff 渲染 + 语法高亮
├── anim/         (1 file)     完整动画引擎（渐变 spinner）
├── image/        (2 files)    Kitty + Block 终端图片渲染
├── notification/ (7 files)    桌面通知后端（Native/OSC/Bell）
├── xchroma/      (1 file)     Chroma 语法高亮适配
├── attachments/  (2 files)    附件 chips
├── logo/         (3 files)    ASCII Logo + 渐变着色
└── util/         (1 file)     错误报告帮助命令
```

### B. codex_go TUI 包结构

```
tui/
├── state.go                     核心 State 结构体 + 60+ slash 命令
├── style.go / color.go          扁平 RGB 样式函数
├── diff_render.go / diff_model.go  Diff summary 渲染
├── shimmer.go                   简单闪烁动画
├── theme_picker.go (709行)      主题选择器
├── resume_picker.go (790行)     会话恢复选择器
├── keymap_config.go (636行)     键盘映射配置
├── model_picker.go (451行)      模型选择器
├── debug_config.go (444行)      调试配置
├── get_git_diff.go (418行)      Git diff 获取
├── clipboard.go (404行)         剪贴板操作
├── tooltips.go (362行)          工具提示
├── wrapping.go (338行)          文本换行
│
├── tea/                         Bubble Tea 适配层
│   ├── model.go (3,813行, 160方法)  上帝 Model
│   ├── slash_popup.go / slash_misc_commands.go
│   ├── session_picker.go / resume_picker.go
│   ├── model_picker.go / theme_picker.go
│   ├── agent.go / skills.go / plugins.go
│   ├── hooks.go / apps.go / usage.go
│   ├── goal.go / review.go / memories.go
│   ├── attachments.go / windows_sandbox.go
│   ├── keymap_runtime.go / key_hint.go
│   ├── status_controls.go / external_editor.go
│   ├── rate_limit_switch.go / debug_mcp_commands.go
│   └── terminal_restore_*.go
│
├── bottom_pane/                 底部面板
│   ├── chat_composer.go         输入框
│   ├── command_popup.go         命令弹出
│   ├── slash_commands.go        slash 命令
│   ├── approval_overlay.go      审批覆盖层
│   ├── skill_popup.go           skill 弹出
│   ├── file_search_popup.go     文件搜索
│   ├── custom_prompt_view.go    自定义提示
│   ├── experimental_features_view.go
│   ├── hooks_browser_view.go
│   ├── memories_settings_view.go
│   ├── mcp_server_elicitation.go
│   ├── paste_burst.go / prompt_args.go
│   ├── scroll_state.go / status_line_*.go
│   └── ...
│
├── chatwidget/                  消息渲染
├── history_cell/                历史单元格（9+ 文件）
├── exec_cell/                   执行单元格
├── markdown/                    Markdown 流式渲染
├── streaming/                   流式处理
└── app/                         应用级状态
```

### C. 快速参考：关键文件对照

| 功能 | Crush | codex_go |
|------|-------|----------|
| 核心 Model | `internal/ui/model/ui.go` (4830行) | `tui/tea/model.go` (3813行) |
| 状态定义 | `model.UI` struct（组件化） | `tui/state.go` State struct（扁平） |
| 主题系统 | `internal/ui/styles/*.go` (4文件) | `tui/style.go` + `tui/color.go` (2文件) |
| 键盘 | `internal/ui/model/keys.go` (286行) | `tui/keymap_config.go` (636行) + `tea/keymap_runtime.go` |
| Diff | `internal/ui/diffview/*.go` (7文件) | `tui/diff_render.go` + `tui/diff_model.go` |
| 动画 | `internal/ui/anim/anim.go` (533行) | `tui/shimmer.go` |
| 对话框 | `internal/ui/dialog/*.go` (27文件) | 分散在 `tea/*.go` 各 picker |
| 消息渲染 | `internal/ui/chat/*.go` (30文件) | `tui/history_cell/*.go` (9文件) |
| 命令面板 | `Ctrl+P` → `dialog/commands.go` | `/` 前缀 → `tea/slash_popup.go` |
| 文件补全 | `@` → `completions/completions.go` | `bottom_pane/file_search_popup.go` |
| 后端通信 | `workspace.Workspace` 接口 + pubsub | JSON-RPC v2 函数指针注入 |
