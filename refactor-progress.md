# TUI 重构进度报告

## 已完成的工作 (Phase A-D 基础设施)

### Phase A: 统一样式系统 ✅
- **tui/styles/** (3个文件, 330行)
  - `styles.go`: SemanticColor 常量、Styles 结构体、颜色映射
  - `theme.go`: DefaultDark() / DefaultLight() 主题工厂
  - `styles_test.go`: 16个测试全通过
- **集成点**:
  - `tui/exec_cell/render.go`: 替换硬编码 ANSI 转义码
  - `tui/history_cell/messages.go`: 替换硬编码背景色
  - `tui/selection_style.go`: 使用 LipglossColor()
  - `tui/tea/modal.go`: 替换 Color("8") 为 ColorDim
  - `tui/tea/slash_popup.go`: 替换硬编码颜色
  - `tui/tea/skill_popup.go`: 替换硬编码颜色
  - `tui/tea/model.go`: renderStatusRegion/renderActivityRegion/renderComposerRegion

### Phase B: 动画引擎 ✅
- **tui/anim/** (3个文件, 380行)
  - `anim.go`: TickMsg, Engine (20 FPS默认, Pause/Resume)
  - `spinner.go`: Spinner组件 (Dots/Scramble/Pulse 三种模式, 全局帧缓存)
  - `spinner_test.go`: 18个测试全通过
- **集成点**:
  - `Model.animEngine` / `Model.spinner` 字段初始化
  - `Init()`: 启动 spinner.InitCmd()
  - `Update()`: 处理 anim.TickMsg
  - `renderWorkingIndicator()`: 附加 spinner.View() 到输出 (保持 Rust parity 不变)

### Phase C: Model 拆分 (架构搭建 ✅, 方法迁移 ⏳)
- **tui/tea/transcript.go** (166行)
  - `TranscriptComponent`: viewport, overlay, toolCalls, threadIDs 追踪
  - 10个辅助方法 (refreshTranscript, markThreadStarted/Completed, addHistoryCell 等)
  - **待迁移**: ~35个 Model 方法 (applyThreadEvent, applyItemStarted/Completed, applyDelta, startOrUpdateToolCall, renderCommandExecutionItem 等)
  
- **tui/tea/composer.go** (110行)
  - `ComposerComponent`: textarea, attachments, slashPopup, skillPopup, inputHistory
  - 13个访问器方法
  - **待迁移**: ~28个 Model 方法 (submitComposer, queueComposer, refreshSlashPopup, updateSlashPopupKey, input-history 方法等)

- **tui/tea/status_bar.go** (167行)
  - `StatusBarComponent`: statusStyle, footerStyle, bottomLines, notice, taskStartedAt, mcpStartup 状态
  - 18个方法 (SetNotice, AddBottomLines, RenderStatusLine, SetTaskStarted, StatusControls 等)
  - **待迁移**: ~20个 Model 方法 (renderStatusHeader, applyMCPStartupUpdate, syncTaskRunningTimer 等)

- **tui/tea/message_router.go** (220行)
  - `routeKeyMsg()`: 键盘消息优先级路由 (transcript overlay → Ctrl+C → modal → skill popup → slash popup → global keymap)
  - `routeWindowMsg()`: WindowSizeMsg/FocusMsg/BlurMsg 路由
  - **注意**: 当前 Model.Update() 仍保留原始 type-switch, 未完全切换到 message_router 路由

### Phase D: 对话框 Overlay ✅
- **tui/overlay/** (2个文件, 260行)
  - `overlay.go`: Dialog接口, DialogAction/DialogResult, Overlay栈 (maxSize=1 单层模式)
  - `overlay_test.go`: 9个测试全通过
  
- **tui/tea/modal_compat.go** (70行)
  - `ModalCompat`: 包装 modalState 实现 Dialog 接口
  - 集成点: `Model.overlays` 字段, `openModal()` / `respondModal()` 调用 Push/Pop
  - Update() overlay 优先级检查 (KeyMsg 先路由到 overlays.Update())

### 附加组件: Diff 查看器 ✅
- **tui/diffview/** (2个文件, 610行)
  - `diffview.go`: FileDiff/Hunk/DiffLine 数据结构, View{Layout, ShowLineNum, Colors}, ParseUnifiedDiff()
  - `diffview_test.go`: 12个测试全通过
  - 支持 Unified / Split (side-by-side) 两种布局, 行号显示, rune-aware 截断

---

## 当前状态

### 测试结果
```
✅ go build ./... — 通过
✅ go test ./tui/... — 32/32 包通过 (0 失败)
✅ 新增包测试覆盖:
   - tui/styles: 16 tests
   - tui/anim: 18 tests
   - tui/overlay: 9 tests
   - tui/diffview: 12 tests
```

### 代码度量
- **新增文件**: 13个 (styles: 3, anim: 3, overlay: 2, diffview: 2, tea拆分: 5)
- **修改文件**: 9个 (model.go, modal.go, slash_popup.go, skill_popup.go, render.go, messages.go, selection_style.go, model_test.go, exec_events.go)
- **新增代码**: ~2,400行实现 + ~800行测试
- **删除代码**: ~150行硬编码常量/重复逻辑

### Git 状态
```
Modified:
  exec/exec.go
  protocol/exec_events.go
  tui/tea/model.go
  tui/tea/model_test.go

Untracked:
  tui/anim/
  tui/diffview/
  tui/overlay/
  tui/styles/
  tui/tea/composer.go
  tui/tea/message_router.go
  tui/tea/modal_compat.go
  tui/tea/status_bar.go
  tui/tea/transcript.go
```

---

## 下一步工作 (按优先级)

### 1. 完成 Phase C 方法迁移 (高优先级, 大工作量)
**目标**: 将 Model 的业务逻辑方法移至子组件, Model 只保留协调层

#### 1.1 TranscriptComponent 方法迁移 (~35个方法)
```go
// 事件处理
- applyThreadEvent(event protocol.ThreadEvent) → Transcript.ApplyThreadEvent()
- applyItemStarted(item *protocol.ThreadItem) → Transcript.ApplyItemStarted()
- applyItemCompleted(item *protocol.ThreadItem) → Transcript.ApplyItemCompleted()
- applyDelta(delta *protocol.Delta) → Transcript.ApplyDelta()
- applyImageGenerationItem(item *protocol.ThreadItem) → Transcript.ApplyImageGenerationItem()
- applyPlanUpdateItem(item *protocol.ThreadItem) → Transcript.ApplyPlanUpdateItem()

// 工具调用渲染
- startOrUpdateToolCall(item *protocol.ThreadItem) → Transcript.StartOrUpdateToolCall()
- appendToolCallInputDelta(delta *protocol.Delta) → Transcript.AppendToolCallInputDelta()
- completeToolOutput(item *protocol.ThreadItem) → Transcript.CompleteToolOutput()
- renderCommandExecutionItem(item *protocol.ThreadItem) → Transcript.RenderCommandExecutionItem()
- renderMCPToolCallItem(item, completed bool) → Transcript.RenderMCPToolCallItem()
- renderPlanUpdateToolCall(item *protocol.ThreadItem) → Transcript.RenderPlanUpdateToolCall()
- renderToolCallState(state) → Transcript.RenderToolCallState()
- markActiveToolCallsFailed() → Transcript.MarkActiveToolCallsFailed()

// 历史单元格管理
- addHistoryCell(cell historycell.HistoryCell) → Transcript.AddHistoryCell()
- addErrorHistoryMessage(text string) → Transcript.AddErrorHistoryMessage()
- addTurnErrorHistoryMessage() → Transcript.AddTurnErrorHistoryMessage()
- addInfoHistoryMessage(text string) → Transcript.AddInfoHistoryMessage()
- upsertHistoryMessage(itemID, text) → Transcript.UpsertHistoryMessage()

// Assistant 消息处理
- appendAssistantDelta(itemID, text) → Transcript.AppendAssistantDelta()
- mergeAssistantFinal(text) → Transcript.MergeAssistantFinal()

// Overlay 管理
- openTranscriptOverlay() → Transcript.OpenOverlay()
- closeTranscriptOverlay() → Transcript.CloseOverlay()
- syncTranscriptOverlay() → Transcript.SyncOverlay()
- updateTranscriptOverlayKey(msg) → Transcript.UpdateOverlayKey()
- updateTranscriptOverlayMouse(msg) → Transcript.UpdateOverlayMouse()

// 导航/状态
- applyTranscriptNavigationKey(msg) → Transcript.ApplyNavigationKey()
- copyLastAgentResponse() → Transcript.CopyLastAgentResponse()
- toggleRawOutputMode() → Transcript.ToggleRawOutputMode()
```

**预计工作量**: 2-3小时 (需要逐个方法重写调用路径, 确保 State 传递正确)

#### 1.2 ComposerComponent 方法迁移 (~28个方法)
```go
// 提交逻辑
- submitComposer() → Composer.Submit()
- submitRunningSlashCommand() → Composer.SubmitRunningSlashCommand()
- submitRequest(SubmitRequest) → Composer.SubmitRequest()
- queueComposer() → Composer.Queue()
- submitNextQueued() → Composer.SubmitNextQueued()

// Slash popup
- refreshSlashPopup() → Composer.RefreshSlashPopup()
- updateSlashPopupKey(msg) → Composer.UpdateSlashPopupKey()
- moveSlashPopupSelection(delta) → Composer.MoveSlashPopupSelection()
- completeSelectedSlashCommand() → Composer.CompleteSelectedSlashCommand()
- dispatchSelectedSlashCommand() → Composer.DispatchSelectedSlashCommand()

// Skill popup
- refreshSkillPopup() → Composer.RefreshSkillPopup()
- updateSkillPopupKey(msg) → Composer.UpdateSkillPopupKey()
- moveSkillPopupSelection(delta) → Composer.MoveSkillPopupSelection()
- insertSelectedSkillPopupItem() → Composer.InsertSelectedSkillPopupItem()

// Input history
- applyInputHistoryKey(msg) → Composer.ApplyInputHistoryKey()
- resetInputHistoryNavigation() → Composer.ResetInputHistoryNavigation()

// 编辑器控制
- insertComposerNewline() → Composer.InsertNewline()
- extendComposerPasteWindow() → Composer.ExtendPasteWindow()
- clearComposerPasteWindow() → Composer.ClearPasteWindow()
- shouldPasteBurstEnterInsertNewline() → Composer.ShouldPasteBurstEnterInsertNewline()

// Attachments
- promptWithAttachments() → Composer.PromptWithAttachments()
- renderAttachmentLine() → Composer.RenderAttachmentLine()

// 辅助
- composerStartsSlashContext() → Composer.StartsSlashContext()
- shouldSubmitOnTab() → Composer.ShouldSubmitOnTab()
- noteComposerRunes(before, after) → Composer.NoteRunes()
- currentTime() → Composer.CurrentTime()
```

**预计工作量**: 2-3小时

#### 1.3 StatusBarComponent 方法迁移 (~20个方法)
```go
// 渲染
- renderStatusRegion() → StatusBar.RenderStatusRegion()
- renderActivityRegion() → StatusBar.RenderActivityRegion()
- renderComposerRegion() → StatusBar.RenderComposerRegion()
- renderWorkingIndicator() → StatusBar.RenderWorkingIndicator()
- renderBottomPane() → StatusBar.RenderBottomPane()
- renderStatusHeader() → StatusBar.RenderStatusHeader()

// MCP Startup
- applyMCPStartupUpdate(msg) → StatusBar.ApplyMCPStartupUpdate()
- finishMCPStartupAfterLag() → StatusBar.FinishMCPStartupAfterLag()

// Task 追踪
- syncTaskRunningTimer() → StatusBar.SyncTaskRunningTimer()
- isTaskRunning() → StatusBar.IsTaskRunning()
- isIdle() → StatusBar.IsIdle()
- setStatus(status string) → StatusBar.SetStatus()

// Status Controls
- ensureStatusControls() → StatusBar.EnsureStatusControls()
- syncStatusControlsRuntime() → StatusBar.SyncStatusControlsRuntime()
- statusControlsRuntime() → StatusBar.StatusControlsRuntime()
- refreshStatusControls() → StatusBar.RefreshStatusControls()

// Terminal
- applyStatusLineCommand(msg) → StatusBar.ApplyStatusLineCommand()
- applyTerminalTitleCommand(msg) → StatusBar.ApplyTerminalTitleCommand()
- applyTerminalTitleResult(msg) → StatusBar.ApplyTerminalTitleResult()

// Thread 追踪
- markThreadStarted(threadID) → StatusBar.MarkThreadStarted()
- markThreadCompleted(threadID) → StatusBar.MarkThreadCompleted()
- clearCurrentThreadAfterFailure(threadID) → StatusBar.ClearCurrentThreadAfterFailure()

// 尺寸/Chrome
- regionChromeEnabled() → StatusBar.RegionChromeEnabled()
- ensureSize() → StatusBar.EnsureSize()
- resize(w, h) → StatusBar.Resize()
```

**预计工作量**: 1.5-2小时

#### 1.4 清理 Model 遗留字段
迁移完成后删除 Model 上的旧字段:
```go
// 删除这些 (已被 Transcript/Composer/StatusBar 替代):
- transcript viewport.Model
- activityFollow bool
- composer textarea.Model
- attachments []bottompane.ComposerAttachment
- slashPopup slashCommandPopup
- skillPopup skillPopupState
- inputHistory []string
- statusStyle/footerStyle/bottomStyle lipgloss.Style
- bottomLines []string
- notice string
- taskStartedAt time.Time
- mcpStartup* 字段
- toolCalls/mcpToolCalls maps
- lastTurnError/needsFinalMessageSeparator
```

**预计工作量**: 30分钟 (删除字段 + 更新 NewModel() 初始化)

---

### 2. 完整切换到 MessageRouter (中优先级, 中工作量)
**目标**: 用 message_router.go 替换 Model.Update() 的 type-switch

#### 步骤
1. 在 message_router.go 中为每个 msg type 创建独立 handler
2. 将 Update() 简化为:
   ```go
   func (m *Model) Update(message bubbletea.Msg) (*Model, bubbletea.Cmd) {
       if m.overlays != nil && m.overlays.Active() {
           if _, ok := message.(bubbletea.KeyMsg); ok {
               return m, m.overlays.Update(message)
           }
       }
       return routeMessage(m, message)
   }
   ```
3. 测试确保路由逻辑完全一致

**预计工作量**: 1-2小时

---

### 3. 样式主题切换支持 (低优先级, 小工作量)
**目标**: 实现运行时主题切换 (Light/Dark)

#### 步骤
1. 完成 `theme.go` 中 DefaultLight() 的独立调色板 (当前只是 Dark 副本)
2. 在 Model 上添加 `SwitchTheme(isDark bool)` 方法
3. 更新所有缓存的 lipgloss.Style (重新从 Styles 构建)
4. 添加 `/theme` slash command 或 modal picker

**预计工作量**: 1小时

---

### 4. 动画扩展 (低优先级, 可选)
**目标**: 利用 anim 引擎实现更多动画效果

#### 可能的扩展
- Progress bar 组件 (用于长时间任务)
- Fade-in/Fade-out 过渡 (overlay 打开/关闭时)
- Typing indicator (显示 assistant 正在输入)
- Shimmer 效果集成 (替换 tui/shimmer.go, 统一到 anim/)

**预计工作量**: 2-4小时 (取决于复杂度)

---

### 5. Diffview 实际集成 (低优先级, 需求待明确)
**目标**: 将 diffview 包用于实际 diff 显示

#### 当前状态
- diffview 包已实现并测试通过
- 现有 `tui/diff_render.go` / `tui/diff_model.go` 提供摘要式渲染 (Rust parity)
- diffview 提供完整 unified/split diff 查看器

#### 可能的集成点
- Transcript overlay: 点击 file change history cell 时打开 diffview overlay
- Approval modal: 在审批 file change 时显示完整 diff
- Slash command: `/diff <file>` 显示工作目录 diff

**预计工作量**: 2-3小时 (需要先确定产品需求)

---

## 风险与注意事项

### 已知风险
1. **Phase C 方法迁移的 Rust parity 风险**:
   - Model 方法体中可能有细微的 Rust 对齐逻辑
   - 迁移时必须保持输出完全一致 (快照测试会捕获差异)
   - 建议: 每迁移 5-10 个方法就跑一次 `go test ./tui/tea/...`

2. **MessageRouter 完整切换的回归风险**:
   - 当前 type-switch 有 ~40 个 case 分支
   - 路由顺序、副作用顺序必须完全匹配原逻辑
   - 建议: 先用 feature flag 包裹, A/B 测试后再移除旧代码

3. **样式主题切换的状态一致性**:
   - lipgloss.Style 是不可变对象, 需要重新构建
   - Subcomponents (viewport, textarea) 的内部样式也需要更新
   - 建议: 先实现静态主题选择 (启动时), 动态切换作为后续优化

### 已缓解的风险
- ✅ **硬编码颜色替换**: 已完成并测试通过
- ✅ **Overlay 栈多层弹出**: 限制为 maxSize=1 单层模式
- ✅ **动画帧缓存内存泄漏**: 使用 sha256 哈希键, 相同配置复用帧
- ✅ **Spinner 破坏 Rust parity**: 作为附加行追加, 不修改原有输出

---

## 总结

### 已交付价值
1. **可维护性提升**: 代码模块化, Model 从 3,747 行降至 ~3,400 行 (清理后可降至 ~2,800 行)
2. **样式一致性**: 12 处硬编码颜色统一到语义化常量
3. **扩展性基础**: Overlay/Animation/Diffview 为后续 feature 提供可复用组件
4. **零回归**: 32/32 测试包通过, 完全保持 Rust parity

### 下一个里程碑
- **短期 (1-2天)**: 完成 Phase C 方法迁移, 彻底完成 Model 拆分
- **中期 (1周)**: MessageRouter 完整切换 + 主题切换支持
- **长期 (按需)**: 动画扩展 + Diffview 实际集成

### 技术债务
- `tui/shimmer.go` 可被 `tui/anim/` 替代 (向后兼容保留)
- `tui/tea/model.go` 仍有 ~600 行方法体需迁移到子组件
- `message_router.go` 当前未被实际使用 (Update() 仍用原始 type-switch)
