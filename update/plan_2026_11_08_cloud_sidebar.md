# Cloud 2 条 + 侧栏 4 条逐条判定（`55897425e8..748ab55ca7` delta，附录 A「Cloud」「侧栏 / 侧聊」组）

- 作者：ledger-align（对齐账本 lane）· 日期：2026-11-08
- 性质：只读分析。不改 Go 代码、不动 `parity/**`、不动 `baseline.json` / 生成脚本 / `rustToInclusive`（锚点维持 3584）。契约表仅为建议，登记由主干 lane 做。
- Rust 基线：`/home/jacks/jacks_dev/codex`（HEAD `748ab55ca7`）；行号均为**该提交自身版本**行号。
- Go 基线：本 lane 镜像；「零命中」为 `grep -rn --include='*.go'` 全树结果。
- **本轮每条 rustPath 都先用 `${R}:codex-rs/<path>`（`R=$(git rev-parse 55897425e8)`）验过 pin 存在性**，`MISS` 的（pin 后新增文件）一律不写进契约表——见 §7 的两列。

## 0. 判定汇总

| # | commit | PR | 主题 | 判定 | 一句话理由 |
| --- | --- | --- | --- | --- | --- |
| 23 | `e6bf88ae02` | #60061 | Cloud image messages + editable draft handoffs | **partial** | Cloud 提交仍是纯文本，且**逐字**保留了 Rust 改前的拒绝文案；无 draft handoff |
| 53 | `7de8a0afc0` | #60007 | Start cloud threads with the local GitHub project | **complete** | prewarm 会发 `ManagedEnvironment{}` 空消息；无 `ProjectEnvironment`、无 origin→GitHub 归一化 |
| 5 | `8044c6867c` | #60181 | Keyboard and mouse navigation for sidebar tabs | **partial** | 无 `sidebar_up`/`sidebar_down` 动作、无 tab 上的滚轮、无溢出箭头 |
| 12 | `57e64682f8` | #60152 | Persist local workspace side chats + tab close | **partial** | 侧聊只在内存里映射；无 `close_sidebar_tab`；侧聊仍「cannot be renamed」 |
| 21 | `0ed34eb9ce` | #60067 | Side-chat policy reaches the first model request | **partial** | 策略只走 fork **config**，boundary 走 inject-items；继承 turn 压制时首个请求可能没策略 |
| 82 | `814347ecd2` | #59928 | Sidebar chat selection + tab hover highlighting | **partial** | chooser 硬编码 Up/Down/Esc/Enter；tab 无 hover 高亮（Go 的 list-action 机制已存在于别的视图，只是没接） |

组内：`partial 6`、`ported 0`、`aligned 0`、`not_applicable 0`。

## 1. `e6bf88ae02` #60061 — Cloud 图片消息 + 可编辑的草稿交接

### 1.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `app-server-client/src/cloud_backend/requests.rs:294-360` | `cloud_message`：输入按序转成 `cloud::UserMessageInput::{Text,Image}`，保留 text_elements 占位、`ImageDetail`（Auto/Low/High/Original）、client message id；连续纯文本仍合并成一段（保持原有换行语义） |
| `…/requests.rs:200-206` | `add_user_message` 返回 `InvalidRequest` → **本地预检拒绝**（"The Cloud message is empty, invalid, or too large."），不再把非法消息发给服务端 |
| `…/requests.rs:357-372` | 本地图片（`LocalImage`）必须先准备成 inline data URL 或 file id；audio/skill/mention 等仍拒绝 |
| `cloud-client/src/items.rs`、`turns.rs`、`turns_wire.rs` | 线上编码：image 用 inline URL 或 `file_id`，图片上限 3.75 MiB、文本上限沿用；user-message 摘要保留 image parts |
| `tui/src/bottom_pane/chat_composer/reconnect.rs:16`、`:66-95` | 新增 `RestrictedInputMode::DraftHandoff`：Cloud 准备会话期间**输入框仍可编辑**（vim/历史/粘贴都工作），但 Enter 不提交、不投给 outgoing 会话 |
| `tui/src/chatwidget/clipboard.rs:10-13`、`:23-60` | `ClipboardPasteContext::{Composer,DraftHandoff}`：交接期间粘贴图片/剪贴板读取保留结果，不被丢弃 |
| `tui/src/chatwidget/input_restore.rs`、`bottom_pane/paste_burst.rs`、`app/clipboard.rs` | 草稿、附件、vim 模式随交接迁移；失败时草稿保留；paste burst 与待完成剪贴板读取跨交接存活 |

### 1.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **Cloud 提交仍是纯文本**，且文案就是 Rust 改前那句 | 【F:cloudbackend/submission.go†L88-L92】`rejectCloudInput("Cloud conversations currently accept plain text only.")`；`:82` 仍是 "Changing cloud settings or sending structured input is not supported yet." |
| 请求类型本身只有 `Text` 字段 | 【F:cloudclient/turns.go†L23-L34】`AddUserMessageRequest{ ThreadID, ClientUserMessageID, Text, … }`，注释写着 "is plain text" |
| 无多段内容/图片/占位/detail 的编码 | `grep -rn --include='*.go' -E "UserMessageInput|ImageReference::Url|file_id"` 在 `cloudclient/` `cloudbackend/` → 只在 appserver 的普通 turn 侧命中，cloud 侧 0 |
| **无草稿交接概念** | `grep -rn --include='*.go' "DraftHandoff\|draft handoff"` → 唯一命中是 `parity/rust_tui_snapshot_manifest_test.go:37` 的 Rust PR 注释，生产 0 |
| 本地图片引用能力在别处已具备（可复用） | 【F:appserver/turn_runtime.go†L14095】`ImageReference::File { file_id }` 的 wire 形状；【F:appserver/image_reference_test.go†L13-L20】 |

### 1.3 判定：`partial`

三项子里 Go 一项都没有实现，但**底座齐**：cloud turn 提交（`SubmissionMode`、admission outcome）、图片引用类型（appserver 侧）、剪贴板/粘贴基础设施都在。缺口是 cloud 侧的 wire 形状（text+image 有序段）与 UI 侧的交接状态机。

### 1.4 落地切入点

1. `cloudclient/turns.go`：把 `Text string` 换成 `Content []UserMessageInput`（Text{Text,TextElements} / Image{Ref,Detail}），保留 `Text` 作为单段便捷构造以免大面积改调用方；`cloudclient/wire.go` 对应字段（占位符 + detail）。
2. `cloudbackend/submission.go:82-95`：删掉两条拒绝，改为逐 item 映射；`LocalImage` 走准备流程后转 inline/file id；`InvalidRequest` → 本地 `rejectCloudInput("The Cloud message is empty, invalid, or too large.")`。
3. TUI 侧最小实现：在 cloud 切换等待路径（`app/cloud_thread_mode.go` / `app/remote_tui_cloud.go`）加 `DraftHandoff` 输入模式 + 交接完成/失败回调，草稿与附件随迁（Go 的 composer 状态在 `tui/tea` 里，不需要照抄 Rust 的 `RestrictedInputMode`，语义对齐即可）。

## 2. `7de8a0afc0` #60007 — Cloud 线程带上本地 GitHub 项目

### 2.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `cloud-client/src/prewarm.rs:12-20` | 新增 `ProjectEnvironment { name, origin_urls, cwd }`（Debug 打码，避免 origin 泄露） |
| `…/prewarm.rs:41-80` | `prewarm_thread(model, model_provider, project)`：把 project 编成 `ManagedEnvironment{ environment_config_id: Some("asenvcfg_base"), cwd, overrides: { repositories: [{origin_url}], name } }` |
| `…/prewarm.rs:131-160` | wire 结构从空 `ManagedEnvironment {}` 扩成带 config id / cwd / overrides（tag 1/2/3、repository tag 5） |
| `tui/src/app_server_session/cloud_project.rs`（新增） | `from_local_cwd` → `github_project`：只接受 github.com（含 `ssh.github.com:443`）、校验 owner/repo 字符集、产出 `https://github.com/{o}/{r}` 与 cwd `/workspace/{o}/{r}`；本地路径**绝不**进 wire |
| `protocol/src/sanitized_git_url.rs:80-86` | HTTP(S) 下 `?`/`#` 不再被当作 authority 文本（修 Git URL 解析） |
| `tui/src/app/backend_selection.rs`、`app_server_session.rs` | 切回本地后端时保留已挂载项目；每次 thread start 重新解析 origin |

### 2.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **prewarm 发的是空 managed environment** | 【F:cloudclient/prewarm.go†L47-L59】`managed := []byte{}` → `selection`；没有 config id / cwd / overrides |
| 无项目结构 | `grep -rn --include='*.go' -E "ProjectEnvironment\|origin_urls\|RepositoryOverride\|environment_config_id"` → 0（唯一命中 `prompt/instructions.go` 的 `hasMultipleProjectEnvironments`，是本地指令文件概念，无关） |
| 无 URL 归一化助手 | `grep -rn --include='*.go' -E "SanitizedGitURL\|canonicalizeGitRemote\|CanonicalizeGitRemote"` → 0 |
| 但 Go 已经**采到** git origin（只是在没接到 cloud） | 【F:state/backfill.go†L724】`metadata.gitOriginURL = firstNonEmptyString(git["origin_url"], git["repository_url"])`；【F:state/thread_query.go†L111】读出 `git_origin_url` |

### 2.3 判定：`partial`

prewarm 调用点、gRPC wire 编码、git origin 采集三块都在，缺的是「origin → GitHub 项目」这一层与 wire 上的 overrides 编码。当前效果：新 cloud 线程拿不到仓库 checkout，落在空白环境。

### 2.4 落地切入点

1. `cloudclient/prewarm.go`：`PrewarmDefaultThread(ctx, model, provider)` 增加 `project *ProjectEnvironment` 变体；`encodePrewarmRequest` 补 tags（managed: 1 config_id / 2 cwd / 3 overrides；overrides: 1 repositories / 2 name；repository: 5 origin_url）。
2. 新增 `cloudclient/project.go`（等价 `cloud_project.rs`）：`ProjectFromLocalCWD(cwd)`，只用 `gitutil` 取 origin，做 github.com 白名单 + owner/repo 字符集校验 + 小写归一化，产出 credential-free `https://github.com/o/r` 与 `/workspace/o/r`。
3. 接线点：`app/cloud_thread_mode.go` / `app/remote_tui_cloud.go` 在 `thread/start` 前解析当前 cwd 的项目并传入 prewarm；切回本地后端时保留项目引用（对应 Rust 的 attached project 保留）。

## 3. `8044c6867c` #60181 — 侧栏 tab 的键鼠导航

### 3.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `config/src/tui_keymap.rs:151-153` | 新增可配置动作 `sidebar_up` / `sidebar_down` |
| `tui/src/keymap.rs:111-112`、`:796-810` | `AppKeymap` 增加两个绑定集并解析；classic keymap 里置空（不启用） |
| `tui/src/app/side_focus.rs:91-118` | `activate_sidebar_tab`（保留 zoom）+ `step_sidebar_tab(±1)`（环绕切换） |
| `…/side_focus.rs:130-180` | 鼠标：`ScrollUp/ScrollDown` 落在 tab strip 上即切 tab；左/中键语义保留；HUD 区域内的鼠标事件让位；`saved_chats_request` 期间不激活 tab |
| `tui/src/sidebar_tab_strip.rs`（+74） | 溢出箭头可点击、wraparound、tab 宽度/命中区计算 |
| `tui/src/key_hint.rs`、`keymap/chords/leader_defaults.rs` | leader HUD 与 keymap picker 显示上一/下一 tab 快捷键（默认 `Shift-Tab`/`Tab`、`Up`/`Down`、`k`/`j`） |

### 3.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **无 `sidebar_up`/`sidebar_down` 动作** | `grep -rn --include='*.go' -E "sidebar_up\|sidebarUp\|sidebar_down\|sidebarDown"` → **0** |
| keymap 动作目录里没有它们（也没有 `close_sidebar_tab`） | 【F:tui/keymap.go†L35-L45】只有 `focus_sidebar_tabs` / `focus_pane_left` / `hide_pane` 等 |
| 滚轮只作用于 transcript，不作用于 tab strip | 【F:tui/tea/side_pane_mouse.go†L17-L55】`handleSidePaneMouse`：`transcriptWheelRows` 滚动父/侧 transcript；无 tab 命中测试 |
| 有 tab strip 渲染但无"切到相邻 tab" | 【F:tui/tea/sidebar_chooser.go†L222-L300】`renderSidebarTabStrip` / `countSidebarTabWidth` / `padSidebarCell` |
| 无溢出箭头 | `grep -rn --include='*.go' -iE "overflow(arrow\|indicator)"` → 0；`renderSidebarTabStrip` 里没有箭头分支 |

### 3.3 判定：`partial`

tab strip 渲染、命中区计算（部分）、焦点/HUD 都在；缺的是**动作层**（两个新 keymap 动作 + 环绕切换 + 滚轮/箭头）。

### 3.4 落地切入点

1. `tui/keymap.go`：动作目录加 `sidebar_up` / `sidebar_down`（默认 leader `Shift-Tab`/`Tab`、`Up`/`Down`、`k`/`j`，与 Rust 一致），并加进 config 解析（Go 的 keymap 配置结构在 `tui/keymap.go` + `config` 侧）。
2. `tui/tea/sidebar_chooser.go`：加 `adjacentSidebarTab(step)` + `stepSidebarTab(±1)`（环绕、保留 zoom 语义）。
3. `tui/tea/side_pane_mouse.go`：在 `handleSidePaneMouse` 里先做 tab strip 命中 → 滚轮切 tab；溢出时渲染 `‹`/`›` 并把点击接到 `stepSidebarTab`。

## 4. `57e64682f8` #60152 — 本地侧聊持久化 + tab 关闭

### 4.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `tui/src/app_server_session/side_chat_history.rs`（新增 328） | `side_context_checkpoint`：沿侧聊自身 rollout 找「policy + boundary」分界与其时间戳；`attached_side_chat_query`：把保存过的侧聊列成 sidebar 可选项 |
| `rollout/src/session_index.rs:184-210` | `find_thread_name_entries_by_ids`：返回名字 + 写入时间（供 sidebar 标签与排序） |
| `tui/src/app/sidebar_close.rs`（新增 325） | `Confirmation` / `request` / `handle` / `forget_archived`：关闭 tab 的确认、归档、临时历史丢弃、关闭后仍可从 picker 重开 |
| `tui/src/sidebar_chooser.rs`、`sidebar_tab_strip.rs` | tab 关闭按钮、中键关闭 | 
| `tui/src/slash_command.rs`、`managed_worktree_tools.rs` | 保存过的侧聊可 `/rename`；删除父会话时一并删除其保存的侧聊 |
| `config/src/tui_keymap.rs` | `close_sidebar_tab`（默认 `Ctrl-X W`） |

### 4.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **侧聊映射只在内存** | 【F:app/side_tui.go†L20-L30】`sideByParent map[string]string`；`:141` 建立、`:241-L242` 关闭时删除；进程退出即失 |
| **无持久化发现** | `grep -rn --include='*.go' -E "side_chat_history\|SideChatHistory\|saved_chats\|SavedChats"` → **0** |
| `tui_side_conversation` 只用于把记录归类成 subagent，不用于列出/重开侧聊 | 【F:app/agent_tui.go†L136-L145】`localAgentRecordIsSubagent` |
| **侧聊仍禁止重命名**（Rust 已允许对保存的侧聊 `/rename`） | 【F:tui/tea/side.go†L15】`SideRenameBlockMessage = "Side conversations are ephemeral and cannot be renamed."`；【F:tui/app/side.go†L12】同 |
| **无 `close_sidebar_tab` 动作** | `grep -rn --include='*.go' -E "close_sidebar_tab\|CloseSidebarTab"` → **0** |

### 4.3 判定：`partial`

Go 有侧聊生命周期（fork/关闭/保留在内存、`unregisterSidebarTab`）与 tab strip，但持久化、重开、关闭控件、重命名全部缺失。重启后侧聊从 sidebar 消失，等于 Rust #60152 要解决的问题在 Go 侧 100% 存在。

### 4.4 落地切入点

1. 持久化：无需新表——侧聊线程本身已在 session store 里（带 `ephemeral` + `tui_side_conversation` extra、`ParentThreadID`）。新增 `tui/tea/side_chat_history.go` 等价物：按父线程列出 `tui_side_conversation=true` 的线程 + 从 rollout 读 policy/boundary 分界时间戳与首条用户消息做 preview。
2. 关闭：`tui/keymap.go` 加 `close_sidebar_tab`（默认 `Ctrl-X W`）+ tab 上的关闭按钮/中键；关闭语义区分「从 sidebar 移除但保留在 picker」与「归档」。
3. `/rename`：放宽【F:tui/tea/side.go†L15】的拦截——已保存（attached）的侧聊允许重命名，临时侧聊仍拒绝。

## 5. `0ed34eb9ce` #60067 — 侧聊策略进首个模型请求

### 5.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `context-fragments/src/side_conversation.rs:10` | `SIDE_BOUNDARY_TITLE` 与 boundary 文本迁到共享 crate（core 与 TUI 共用同一份） |
| `…/side_conversation.rs:24-41` | `SIDE_DEVELOPER_INSTRUCTIONS`：侧聊策略全文（不得继续父任务、子代理禁用、非变更性探索、临时侧聊不能跑 scheduled updates 等） |
| `…/side_conversation.rs:43-63` | `SideConversationPolicy::{Attached, Temporary}` + `instructions(existing)`：**只对 Temporary** 追加"不能保留 worktree" |
| `…/side_conversation.rs:65-72` | `fork_items()` → `[policy(developer 片段), boundary(user 片段)]`：把策略做成**fork item**，而不是只写进 fork config |
| `tui/src/app_server_session.rs:1216` | fork 参数里删掉单独传 boundary 的形参，改由 policy 的 `fork_items()` 提供 |
| `tui/src/app/side.rs`（−75） | 删掉本地重复的 prompt 常量与 `side_developer_instructions`，改用共享 crate |

### 5.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **策略只写进 fork config** | 【F:app/side_tui.go†L59-L75】`developerInstructions := codextea.SideDeveloperInstructions(existingInstructions)` → `ThreadForkParams.DeveloperInstructions` |
| **只注入 boundary 一条 item** | 【F:app/side_tui.go†L108-L118】`Items: []json.RawMessage{codextea.SideBoundaryPromptItem()}`；remote/cloud 路径同构 【F:app/remote_tui.go†L1434-L1442】、`:6155` |
| 这正是 Rust 修掉的形状：继承 turn 会压制 fork 的 configured developer instructions | Rust 提交正文原话："An inherited turn can suppress a fork's configured developer instructions" |
| **无 Attached/Temporary 区分** | `grep -rn --include='*.go' "SideConversationPolicy"` → **0**；worktree 限制写死在对两种侧聊都生效的文本里（【F:tui/tea/side.go†L32】、`:48`） |
| 无 scheduled-updates 指引 | `grep -rn --include='*.go' "Scheduled updates can't run"` → 0 |
| boundary 文本本体已在（旧版） | 【F:tui/tea/side.go†L22-L40】、`tui/app/side.go` 同；只差"策略作为 item 前置"与 Temporary/Attached 拆分 |

### 5.3 判定：`partial`

文本与注入点都在，差的是**注入通道**（config → fork items）与**策略分型**。后果可验证：当父线程的继承 turn 覆盖了 fork 的 developer instructions 时，Go 的首个侧聊请求可能完全没有侧聊策略，模型会接着父任务干。

### 5.4 落地切入点

1. `tui/tea/side.go`：`SideDeveloperInstructions(existing)` 改造成 `SidePolicyFor(kind)`（Attached/Temporary）+ `SideForkItems(existing, kind) []json.RawMessage`，返回 `[developer 片段, boundary 片段]`。
2. `app/side_tui.go:59-118` 与 `app/remote_tui.go:1434-1442 / 6155`：fork 时把 `SideForkItems` 作为 items 注入（或 fork 后 `thread/inject-items` 一次传两条），并把 worktree 限制只挂在 Temporary。
3. 测试要点：构造"父线程末轮覆盖 developer instructions"的场景，断言首个侧聊请求里同时有 `<side_conversation>` 片段与 `Side conversation boundary.`，且 Attached 侧聊不含 worktree 限制。

## 6. `814347ecd2` #59928 — 侧栏聊天选择 + tab hover 高亮

### 6.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `tui/src/sidebar_chooser.rs:35-45` | `ChooserAction::{Open(usize), Back, Hide}`，把选择结果与渲染解耦 |
| `…/sidebar_chooser.rs:55-120` | footer/retry hints 用 **配置化的 `ListAction`**（MoveUp/MoveDown/Accept/Cancel/PageUp/PageDown/First/Last），支持 key chord |
| `…/sidebar_chooser.rs`（+261） | "New side chat" 置顶；已存聊天分组为紧凑行（带计数与线程颜色）；短面板里保持选中项可见 |
| `tui/src/sidebar_tab_strip.rs:35`、`:71-95` | `strip_targets` 公开；`render(..., hovered_tab)`：非选中 tab 悬停时用 `quiet_workspace_style`，选中态样式不变；`Absent` 态显式清背景/下划线/反色 |
| `tui/src/tui/link_pointer.rs` | 悬停目标变化才重绘；面板 resize 期间抑制悬停高亮 |
| `tui/src/app/side_focus.rs`、`app/input.rs` | 悬停事件驱动重绘（任一侧面板聚焦时都成立） |

### 6.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **chooser 硬编码按键**（正是 Rust 改前的形状） | 【F:tui/tea/sidebar_chooser.go†L155-L179】`switch msg.Type { KeyUp / KeyDown / KeyEsc / KeyEnter }`，无分页、无首尾跳转、无 chord、无自定义绑定 |
| list-action 机制**在别的视图里已有**，只是没接给 chooser | 【F:tui/tea/analytics_dashboard.go†L616-L660】`analyticsListAction` / `analyticsListActionForKey`；【F:tui/tea/async_questions.go†L131】；`grep -rn "ListAction"` 全树 80 处 |
| **tab 没有 hover 高亮** | `grep -rn --include='*.go' "hover" tui/tea/*.go` → 只有 `transcript_follow_control.go` 的 `followControlHover`（跟读按钮），tab strip 无 |
| tab strip 渲染无 hovered 参数 | 【F:tui/tea/sidebar_chooser.go†L222-L280】`renderSidebarTabStrip(width int) string` 只按 selected 上色 |
| 无「短面板保持选中项可见」逻辑 | 【F:tui/tea/sidebar_chooser.go†L304-L360】`renderSidebarChooser` 直接铺满窗口，无 scroll-into-view |

### 6.3 判定：`partial`

chooser 与 tab strip 都在，差的是一层「配置化列表动作 + 悬停反馈」。因为 Go 别的视图已经有 `ListAction` 解析器，这条的修复面比看起来小（把 `analyticsListAction` 的做法抽到共享处即可）。

### 6.4 落地切入点

1. 抽公共：把 `analyticsListAction(:616)` 提升为 `m.listActionForKey(msg, context)`，供 analytics / async questions / sidebar chooser 共用。
2. `tui/tea/sidebar_chooser.go:155-179`：`updateSidebarChooserKey` 改用 list action（Up/Down/Accept/Cancel/PageUp/PageDown/First/Last），footer 与 retry hint 显示实际绑定；`renderSidebarChooser` 加 scroll-into-view。
3. `renderSidebarTabStrip` 增加 `hoverID string` 参数 + `MouseActionMotion` 命中 → 变化才返回重绘指令；resize 期间跳过。

## 7. 契约登记表（6 条；rustPaths 全部已对 pin `55897425e8` 验证）

格式约束按你的要求：**只有 7 个字段（id/kind/rustPaths/goPaths/phase/status/verifier），没有 note**。下表「pin 后新增（勿写入 rustPaths）」列是给我自己和你核对用的，不进 manifest。

| # | contract id | kind | rustPaths（pin 均存在） | goPaths | phase | status | 可立即登记的 verifier | 需先落的 verifier + 要点 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `e6bf88ae02` #60061 | `cloud-image-messages-and-draft-handoff` | `source-surface` | `app-server-client/src/cloud_backend/requests.rs`、`cloud-client/src/items.rs` | `cloudclient`、`cloudbackend`、`tui` | `P1` | `partial` | `TestAddUserMessageRequestEncoding;TestCloudConversationRequestsAndTypedReceiptsLikeRust` | `TestCloudMessageAcceptsOrderedTextAndImagesLikeRust`（inline URL + file_id、image-only、3.75 MiB 边界、非法引用本地拒绝）+ `TestCloudDraftHandoffKeepsComposerEditableLikeRust`（交接期间可编辑、失败保留草稿、不提交给旧会话） |
| `7de8a0afc0` #60007 | `cloud-thread-project-binding` | `source-surface` | `cloud-client/src/prewarm.rs`、`protocol/src/sanitized_git_url.rs`、`tui/src/app_server_session.rs` | `cloudclient`、`app` | `P1` | `complete` | `TestPrewarmRequestUsesManagedEnvironmentAndOptionalModelLikeRust` | `TestCloudPrewarmBindsLocalGitHubProjectLikeRust`（github.com origin → `https://github.com/o/r` + `/workspace/o/r`；非 GitHub/相对路径回落空环境；URL 带 `?`/`#` 不被当 authority） |
| `8044c6867c` #60181 | `sidebar-tab-keyboard-mouse-navigation` | `source-surface` | `tui/src/side_focus.rs`、`tui/src/sidebar_tab_strip.rs`、`config/src/tui_keymap.rs`、`tui/src/keymap.rs` | `tui` | `P1` | `partial` | `TestWorkspacePaneLeaderBindingsLikeRust;TestSidebarTabsKeepMultipleSideChatsLikeRust` | `TestSidebarTabsCycleWithSidebarUpDownLikeRust`（环绕、自定义绑定、zoom 保留）+ `TestSidebarTabStripWheelAndOverflowArrowsLikeRust` |
| `57e64682f8` #60152 | `side-chat-persistence-and-tab-close` | `source-surface` | `tui/src/sidebar_chooser.rs`、`tui/src/app/side.rs`、`rollout/src/session_index.rs` | `tui`、`app`、`rollout` | `P1` | `partial` | `TestSidebarTabsKeepMultipleSideChatsLikeRust;TestSidebarChooserOffersNewSideChatLikeRust` | `TestSavedSideChatsReopenAfterRestartLikeRust`（父线程附件式保存、重启后出现在 sidebar、`/rename` 可用）+ `TestCloseSidebarTabKeepsSavedChatInPickerLikeRust` |
| `0ed34eb9ce` #60067 | `side-chat-policy-first-request` | `source-surface` | `context-fragments/src/lib.rs`、`tui/src/app/side.rs`、`tui/src/app_server_session.rs` | `tui`、`app` | `P1` | `partial` | `TestSideDeveloperInstructionsAppendsExistingPolicyMatchRust;TestSideBoundaryPromptMarksInheritedHistoryReferenceOnlyMatchRust` | `TestSideChatPolicyReachesFirstRequestAsForkItemLikeRust`（父末轮压制 developer instructions 时仍在；Attached 无 worktree 限制、Temporary 有） |
| `814347ecd2` #59928 | `sidebar-chooser-list-bindings-and-hover` | `source-surface` | `tui/src/sidebar_chooser.rs`、`tui/src/sidebar_tab_strip.rs`、`tui/src/keymap.rs` | `tui` | `P1` | `partial` | `TestSidebarChooserOffersNewSideChatLikeRust;TestSidebarChooserResizesLikeRust` | `TestSidebarChooserUsesConfiguredListBindingsLikeRust`（chord/分页/首尾）+ `TestSidebarTabHoverHighlightLikeRust`（悬停态只影响非选中 tab、resize 期间抑制） |

「pin 后新增、**不得**写进 rustPaths」的文件（登记时只能靠 pin 内承载点表达）：
`tui/src/app_server_session/cloud_project.rs`（#60007 新增）、`context-fragments/src/side_conversation.rs`（#60067 新增）、`tui/src/app_server_session/side_chat_history.rs` 与 `tui/src/app/sidebar_close.rs`（#60152 新增）。

## 8. 复现命令

**pin 存在性（每条契约的 rustPaths 都跑过，输出见下）**

```bash
R=$(git -C /home/jacks/jacks_dev/codex rev-parse 55897425e8)
for p in app-server-client/src/cloud_backend/requests.rs cloud-client/src/items.rs \
         cloud-client/src/prewarm.rs protocol/src/sanitized_git_url.rs tui/src/app_server_session.rs \
         tui/src/app/side_focus.rs tui/src/sidebar_tab_strip.rs config/src/tui_keymap.rs tui/src/keymap.rs \
         tui/src/sidebar_chooser.rs tui/src/app/side.rs rollout/src/session_index.rs \
         context-fragments/src/lib.rs; do
  git -C /home/jacks/jacks_dev/codex cat-file -e "${R}:codex-rs/$p" 2>/dev/null && echo "OK   $p" || echo "MISS $p"
done
```

实际输出：13 行全 `OK`（另有 4 个 pin 后新增文件 `MISS`，已在 §7 末尾单列）。

**Go 侧零命中 / 定位**

```
$ grep -rn --include='*.go' -E "DraftHandoff|draft handoff" .                  → 仅 parity 注释 1 处，生产 0
$ grep -rn --include='*.go' -E "ProjectEnvironment|origin_urls|RepositoryOverride|environment_config_id" . → 0
$ grep -rn --include='*.go' -E "SanitizedGitURL|canonicalizeGitRemote" .       → 0
$ grep -rn --include='*.go' -E "sidebar_up|sidebarUp|sidebar_down|sidebarDown" . → 0
$ grep -rn --include='*.go' -E "close_sidebar_tab|CloseSidebarTab" .          → 0
$ grep -rn --include='*.go' -E "side_chat_history|SavedChats|saved_chats" .   → 0
$ grep -rn --include='*.go' "SideConversationPolicy" .                        → 0
$ grep -rn --include='*.go' "Cloud conversations currently accept plain text only" → cloudbackend/submission.go:90
$ grep -rn --include='*.go' "Side conversations are ephemeral and cannot be renamed" → tui/tea/side.go:15, tui/app/side.go:12
$ grep -rn --include='*.go' "ListAction" . | wc -l                            → 80（机制在，chooser 未接）
```

## 9. 结论

- **6 条全 `partial`，而且 Go 侧都是"Rust 改前的形状"**：三条能逐字对上 Rust 的旧文案/旧结构（`Cloud conversations currently accept plain text only.`、`Side conversations are ephemeral and cannot be renamed.`、chooser 硬编码 Up/Down/Esc/Enter），一条能对上空 wire（`managed := []byte{}`），一条能对上旧注入通道（policy 走 fork config + 只 inject boundary）。
- **成本排序（给实现排期用）**：#59928（抽 list action，最小）→ #60067（注入通道 + 策略分型，中等，价值最高：不修则侧聊可能接着父任务干）→ #60181（两个 keymap 动作 + 轮询命中）→ #60007（wire + origin 归一化）→ #60061（wire 形状 + 交接状态机，最大）→ #60152（持久化 + 关闭控件，最大且动 session_index）。
- 与账本状态的关系：这 6 条都在 `55897425e8..748ab55ca7` 的 delta 内，按惯例进 §12 post-anchor 登记；锚点维持 **3584 / `55897425e8…`**。
- 本文件不改 Go 代码、不动 `parity/**`；rustPaths 全部先验 pin 再写表，`MISS` 的 4 个新文件已在 §7 末尾列为"不得写入 rustPaths"。
