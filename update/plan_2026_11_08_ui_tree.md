# Rust 扩展 UI tree / 状态行这一组（11 条）逐条判定（2026-11-08）

范围：`#59988`、`#60019`、`#60020`、`#60021`、`#60022`、`#60026`、`#60027`、`#60028`、`#60029`、`#60031`、`#60032`。
方法：Rust 侧用 artifacts 里的检出（`~/jacks_dev/codex/codex-rs`，已含 `55897425e8`）逐条 `git show` 读原 diff；
Go 侧读 `tui/`、`tui/bottom_pane/`、`tui/chatwidget/`、`features/`、`appserver/protocol.go` 源码。**本轮不改任何 Go 代码。**

## 0. 判定口径

| 判定 | 含义 |
| --- | --- |
| `ported` | Go 已实现该提交的行为，并能指出承载函数 |
| `aligned` | Go 现有实现的可观察行为已等于该提交修复/确立的行为，无需落地 |
| `not_applicable` | 纯 Rust 内部实现变更（分配、缓存、模块拆分），无可观察行为，也无 Go 对应结构需要改 |
| `partial` | Go 有同一子系统，但该提交的行为缺失或只覆盖了一部分 |

## 1. 结论摘要

- 计数：`partial` 9、`aligned` 1、`not_applicable` 1、`ported` 0。
- **关键事实（决定这组怎么读）**：Go 的 `tui/ui_tree.go` 停在 `#57970/#59518` 那一版——节点只有 `text`/`link`/`row`/`string`，
  只有**单行** `RenderLine(width)`，没有样式、没有颜色、没有换行、没有宽度测量。
  而仓里唯一的树生产方 `chatwidget.StatusLineExtensionsTree` **在 tui/ 内没有生产调用方**（唯一调用是 `tui/chatwidget/status_line_extension_like_rust_test.go`；`chatwidget/status_controls.go` 也未引用），
  footer/composer 仍然走纯字符串状态行；`appserver/protocol.go`（247 KB）里也**没有任何 UiTree / StatusLine 载体**。
  也就是说这 11 条里除 `#60027`（行为已等价）与 `#60028`（纯内部优化）外，其余 9 条的差异今天都是"模型与渲染能力缺失"，
  一旦接线就会立刻暴露；其中 `#60020` 是**已有的安全隐患**（见下），不是"等接线"的问题。
- 另一条硬结论：Rust `#59988` 之后，任何带 `bold`/`italic`/`underline` 节点或 `props.color` 的扩展树，**在 Go 里会解析失败**
  （`tui/ui_tree.go:155` 未知字段、`:173` text 节点 props 必须为空、`:222` 未知节点类型、`:318` 未知 prop 全部返回错误），
  不是"渲染得难看"，而是整棵树被拒。

## 2. 逐条判定表

| sha | 主题 | 判定 | 证据（Go 现状） | 落地点 |
| --- | --- | --- | --- | --- |
| `1a2f5babe83` | Add inherited styling to TUI display trees (#59988) | partial | `tui/ui_tree.go:93` `UINode` 只有 `Kind/Text/Href/Title/Gap/Children`；`:46-52` 只认 `text/link/row/string`；`:155`/`:173`/`:222`/`:318` 对样式字段、text props、新节点类型一律报错；`:516` 只渲染首行 | `UINode` 加 `bold/italic/underline` 与 `props{color,weight,italic,underline}`、`row.background`；decode/validate/marshal 同步；渲染模型要从"纯文本 + 列区间"换成 styled run；`tui/line_truncation.go` 需要样式感知的省略号 |
| `2157f986489` | Add theme-aware semantic colors to extension UI trees (#60019) | partial | 同 `:93` 无任何 color/background 字段；主题侧 `tui/theme_scope_foreground.go`、`tui/theme_accent.go`、`tui/syntax_theme.go` 只服务 native span 渲染，`ui_tree` 无入口 | 在树的颜色解析里复用 `status_line_style` 的 scope 映射（Rust 就是这么做的），实现 `primary/surface/accent/status-*` 与 `codex.accent` 回退，终端不支持时回退默认色 |
| `7e980a54fee` | Strip terminal commands from UI tree strings (#60020) | partial | `tui/ui_tree.go:614` `sanitizeUITreeText` 只处理 CSI（`:641` `csiUITreeEnd`）；遇到非 `[` 的 ESC 只丢掉 ESC、保留后续字节，所以 `ESC ]8;;URL BEL` 会以可见文本 `]8;;URL` 渲染——正是该提交要修的 bug | 把 `:614` 换成完整序列清洗（仓内已有 `github.com/charmbracelet/x/ansi` 依赖，`ansi.Strip` 等价于 Rust 的 vtparse 路径），保留可打印文本/tab/换行；补 OSC-8 与未闭合序列的回归用例 |
| `1a20f53c331c` | Normalize inline UI tree text across styled leaves (#60021) | partial | `tui/ui_tree.go:544` `uiRowLine` 直接拼接子行，没有跨节点空白折叠、没有跨节点字素合并；`:509`/`:516` 对 string 只取首行。已对齐的一点：零宽子节点会被跳过（`uiRowLine` 里 `childWidth == 0` 分支） | 抽 `ui_tree/render/text.rs` 等价层：跨叶折叠空格/tab、trim、跨样式边界的换行机会、跨叶字素合并、hyperlink 仅当所有片段同目标 |
| `d749ee847f35` | Separate UI tree preparation from line rendering (#60022) | partial | 只有 `tui/ui_tree.go:496` `RenderLine(width)`，宽度仅在裁剪时用到（`:499-506`），没有 prepare/最小-首选宽度测量 | 加 `Prepare()` + `MeasureNatural()/MeasureConstrained()`（Rust `PreparedUiTree` 的等价物），`#60031` 的 `measure_natural` 依赖它 |
| `8e7ccd8472e2` | Add multiline layout and row wrapping to UI trees (#60026) | partial | 无 `wrap` prop（`:318` 未知名报错）；`:516` 只渲染首行；没有"高"的概念 | `row` 增加 `wrap: "wrap"`（默认 `nowrap`），渲染改为按视口宽高输出多行，兄弟最小宽度预留，垂直溢出用不带链接的省略号 |
| `66a56b17a120` | Keep configured status lines on the native renderer (#60027) | aligned | 配置状态行与 picker 预览始终走 native：`tui/bottom_pane/status_line_setup.go:22-37`（`StatusLineForItems` → `status_line_style.go:99 StatusLineFromSegments`）、`tui/chatwidget/status_controls.go:686`（带 `StatusLineUseThemeColors`）、预览 `tui/bottom_pane/status_surface_preview.go:173`；`tui_extensions_statusline` 只登记未读（`features/features.go:404`），映射函数 `status_line_style.go:137` 只被测试调用 | 无行为缺口。残余风险（非阻塞）：`StatusLineRendererFromFeature` 仍会在开关打开时返回 `Extension`，而 `StatusLineExtensionsTree` 是**无主题色的纯文本树**——将来接线时若直接用它，等于把 `#60027` 修掉的回归放回去。接线前应删掉该映射或改成"MCP provider only"，并把树做成主题感知 |
| `f8df49a196c4` | Reuse prepared UI trees for footer rendering (#60028) | not_applicable | 纯 Rust 内部优化（`Arc<PreparedUiTree>`、单次渲染内缓存颜色解析、叶子共享缓冲切片）。Go 的树在 `StatusLineExtensionsTree` 构造一次、`RenderLine` 渲染一次，既没有重复 prepare 也没有颜色缓存需求 | 无需落地；若将来 Go 也引入 prepare，再考虑复用与缓存 |
| `7cdb2198831e` | Render UI trees directly into terminal buffers (#60029) | partial | Go 输出 `UILine{Text, Hyperlinks}`（`tui/ui_tree.go:437-482`），由 `Render()` 在输出时贴 OSC-8；没有 terminal buffer 直绘，也没有 natural / viewport-constrained 尺寸测量。已对齐的部分：裁剪按字素（`tui/line_truncation.go` `TruncateToWidth`），溢出省略号不带链接（`ui_tree.go:496-506` 用 `ClipHyperlinksToWidth`） | 若要贴齐 Rust 的绘制语义，需在 styled run 模型之上做 cell 级绘制与测量；纯字符串路径可以先保留 |
| `5c7a1602c859` | Unify TUI status line layout for extension content (#60031) | partial | 没有任何扩展状态内容进入 footer（`StatusLineExtensionsTree` 在 tui/ 内无生产调用方）；native 布局是单行字符串拼接（`tui/bottom_pane/footer.go:159-200`，`footer.go:268 PassiveFooterStatusLine`，`tui/bottom_pane/chat_composer.go:232 ComposerFooterState.Render` 单行 + `TruncateWithEllipsis`）；`tui/chatwidget/status_state.go` 没有 reasoning-effort 过渡概念 | 接线时统一 shared footer 与独立 status line 的宽度分配（扣掉 mode indicator），用自然宽度测量而不是按最大宽度渲染；补"扩展内容替换状态行时清理 effort 过渡" |
| `cfc11eb483bb` | Allow extension status content to use multiple footer rows (#60032) | partial | footer 天生单行：`tui/bottom_pane/footer.go:163` `FooterHeight = len(FooterLines(props))`，`:268` 返回单个字符串；`tui/bottom_pane/chat_composer.go:232` 单行渲染后截断 | 需要 status 区域支持最多 4 行、按可用宽度重测、垂直空间不足时让位草稿、未聚焦 composer 保持单行 dim |

## 3. 逐条说明（表里放不下的细节）

### 3.1 `#59988` 继承样式与行背景

Rust 新增 `bold`/`italic`/`underline` 三种容器节点，`text` 节点获得 `color`/`weight`/`italic`/`underline` 四个属性，`row` 获得 `background`；
颜色接受 8 个 ANSI 名或 `#RRGGBB`，并"通过终端调色板解析"；溢出省略号保留最后一个可见 span 的样式但保持不带链接。
Go 侧连属性位都没有，`text` 节点甚至**不允许**任何 props（`tui/ui_tree.go:173`），所以这是"能力缺失"而不是"实现差异"。

### 3.2 `#60019` 语义色

Rust 在 `ui_tree/theme.rs` 增加 `primary`/`surface`/`accent` 与 `status-model|path|branch|state|usage|limit|metadata|mode|thread|progress`，
`accent` 优先取 `codex.accent` scope，不可用时回退终端默认色；快照覆盖 truecolor 与 ANSI16、深色/浅色/空主题。
Go 有 scope→前景色的映射（`tui/theme_scope_foreground.go`），但没有任何"把树里的语义色解析成实际颜色"的入口。

### 3.3 `#60020` 终端序列清洗（唯一"今天就有影响"的条目）

Rust 的改动说明写得很直白：原来的 CSI-only 清洗会把 OSC 命令内容（包括超链接 URL）留在可见文本里。
Go 的 `sanitizeUITreeText` 是同一类的 CSI-only 实现，且对非 CSI 的 ESC 采取"丢 ESC、留内容"的策略，
于是 `\x1b]8;;https://…\x07` 会变成可见的 `]8;;https://…`。这条建议按 P0 处理：即便今天树没接线，这也是将来接线时第一道会踩的坑，
而且清洗函数本身是安全相关代码。

### 3.4 `#60021` 跨叶文本归一化

Rust 新增 `ui_tree/render/text.rs`（281 行）：跨 inline 叶折叠空白、trim、只保留首行、跨样式边界准备断行机会、跨叶合并被切开的字素、
hyperlink 仅当所有片段目标一致才保留、零宽片段不产生 gap 也不改变省略号样式。Go 只有 `uiRowLine` 的朴素拼接，
唯一的对齐点是零宽子节点被跳过。

### 3.5 `#60022` / `#60026` 准备阶段与多行

`#60022` 把"准备（样式化 inline 文本 + 行结构 + 最小/首选宽度）"从"按视口渲染"里拆出来；`#60026` 在此之上做多行与 `wrap`。
Go 两者都没有：`RenderLine` 一次到底，且只取首行（`ui_tree.go:516`）。这也是 `#60031` 用 `measure_natural()` 的前提。

### 3.6 `#60027` 配置状态行留在 native 渲染器

Rust 删掉了 `status_line_content.rs`（65 行的 `StatusLineRenderer` + 纯文本树构造），配置项与其 picker 预览一律走 native，
"Use theme colors" 选项**始终显示**，`tui_extensions_status_line` 的语义改成"启用实验性的 MCP 状态行提供方"。
Go 的配置状态行与预览本来就走 native 并保留主题色（见表中三处引用），开关也没有生产读取方，所以**可观察行为已经等价**，判 `aligned`。
需要注意的是 Go 还留着"开关→Extension"的映射函数和一个无主题色的树构造器，属于将来接线时的雷，已在表中写明。

### 3.7 `#60028` 复用已准备的树

Rust 的性能改动：footer 的宽度测量与渲染原本各准备一次树，现在准备一次并用 `Arc` 共享，渲染期内缓存颜色解析，叶子共享文本缓冲切片。
Go 没有 prepared 表示，也没有"测量+渲染各一次"的重复（构造一次、渲染一次），因此没有对应行为需要移植，判 `not_applicable`。

### 3.8 `#60029` 直接绘制进 terminal buffer

Rust 用共享布局遍历测量 prepared 树并直接写入 buffer，新增 natural 与 viewport 约束两种测量，裁剪落在字素边界、溢出省略号不带链接。
Go 的等价物是"字符串 + 列区间 + 输出时贴 OSC-8"（`UILine`），裁剪与省略号链接语义已经对齐，但 cell 级绘制与测量没有。

### 3.9 `#60031` / `#60032` 状态行布局与多行

`#60031` 让 shared footer 行与独立状态行用同一个渲染器，扩展内容只拿 mode indicator 剩下的宽度，并跳过 effort 过渡；
`#60032` 让扩展状态内容最多占 4 行、按可用宽度重测、空间不足时让位草稿、未聚焦时保持单行 dim。
Go 的 footer 是单行字符串（`FooterHeight = len(FooterLines)`），扩展内容根本没进来，所以这两条在 Go 里既无实现也无"接线即可用"的基础——
必须先有 `#59988/#60022/#60026` 的模型，再谈布局。

## 4. 建议落地顺序（不在本轮执行）

1. **P0**：`#60020` 清洗（`tui/ui_tree.go:614`），顺手补 OSC-8 / 未闭合转义用例。
2. **P1**：`#59988` + `#60019` 模型与颜色（`UINode` 字段、`decodeUINode` 的字段/props 白名单、`validateUITree`、`MarshalJSON`），
   同时把渲染输出从"纯文本 + 列区间"升级成 styled run，`line_truncation` 的省略号要能带样式。
3. **P1**：`#60022` + `#60026` 的 prepare/measure 与多行 wrap —— 这两条是 `#60031`/`#60032` 的前置。
4. **P2**：`#60027` 收尾（删掉或改写 `StatusLineRendererFromFeature`，把树做成主题感知）+ `#60031`/`#60032` 的 footer/composer 多行布局接线
   （`tui/bottom_pane/footer.go` 的 `FooterLines`/`FooterHeight`、`tui/bottom_pane/chat_composer.go:232`）。
5. **P2**：app-server 侧补 UI tree 的传输通道 —— 目前 `appserver/protocol.go` 里没有任何 UiTree/StatusLine 载体，
   树只存在于 TUI 内部，这也是"接线"必须同时解决的一半。

## 5. 边界与未做的事

- 未改任何 Go 代码，未新增/修改测试。
- 未对 Go 的 `tui` 包跑测试：本地镜像只取回了相关的十来个文件，`go test ./tui/...` 需要完整工作区。
- `#60029`/`#60031` 的 Rust 快照（snap）只做语义比对，没有逐像素对齐 Go 的渲染输出。
- 判定基于 `55897425e8` 的 Rust 树与当前 Go 工作树；若 Go 侧 MCP 状态行提供方正在并行落地（另一 lane），`#60027`/`#60031`/`#60032`
  的结论需要在接线后再复核一次。
