# syncl3 · r86 · `#49993` §7 未决问题的只读判定 —— **Go 存在 stateful 同步 reviewer ⇒ `#51627` 同步面残余缺口成立**

> 车道：syncl3（Linux 节点 `de1bb1e71f8f7ad6969025798b555057`）｜队长：syntropy（Windows）
> 派单：`msg-1791371670400766600-4686`（**只读判定，不出补丁**）
> 纪律：**0 commit / 0 push / 0 ref 移动**；`appserver/`、`model/` 全只读；探针只在 `/tmp`。
> 基线：Go `origin/main = 9a62462bc8e9e8153670aab0e776d5caeb5d2d3a`（batch 十一）；Rust 工作树 pin = `5b0b2530354052b9194156d70d4c94a439368342`（parity 用）。

---

## 0. 结论（三选一）

| 选项 | 判定 |
|---|---|
| `Go 无该路径 ⇒ #49993 确为 N/A` | ✗ |
| **`Go 有该路径 ⇒ #51627 残余缺口成立`** | **✔ 采纳** |
| `不可判定` | ✗ |

**一句话**：Go 的 Guardian reviewer **确实是 stateful 同步 reviewer**（同一个 `guardianSessionRunner` 跨 review 复用 `PreviousResponseID`，session kind 直译 Rust `GuardianReviewSessionKind`，该枚举在 Rust 只属于 `core/src/guardian/review_session.rs` ＝ **同步** reviewer）。而 Rust `#51627` 明确把「assistant context 拆到独立 `RETAINED ASSISTANT CONTEXT` 段」**只用于 async independent-snapshot** 路径，同步/stateful reviewer **保持既有单段序**（`composition.rs` 头注释原文：「Stateful reviewers retain their existing section order.」）。Go 侧 `state/guardian.go:639-667` **无条件**拆分 ⇒ **把 async 序套在了唯一的 sync 生产消费者上**，这正是 `#51627` 的同步面残余缺口（与 `#49993` 无关）。

- **最小落点**：`state/guardian.go:639-667`（+ `state/guardian_retained_context.go:270`），**`state/` 不在避让清单 ⇒ 不撞避让面**（详见 §5）。本单**未动任何文件**，等队长授权再出补丁。

---

## 1. Go 是否存在「同一 session 多轮 review、状态跨轮保留」的路径？—— **存在**

### 1.1 reviewer 本体是 stateful session

`appserver/guardian_reviewer.go:211-220`：

```go
type guardianSessionRunner struct {
	mu       sync.Mutex
	agent    model.AgentRunner
	previous string   // ← 跨 review 保留的 response id（会话主干）
	seeded   string   // 父 compaction 的种子 checkpoint
	priorReview bool
}
```

- `appserver/guardian_reviewer.go:286 Run(...)`：`clone.PreviousResponseID = r.previous`（`:304` 分支），一次 review 结束后把新 `response.ResponseID` 写回 `r.previous` ⇒ **review 之间是同一个模型会话主干**。
- `appserver/guardian_reviewer.go:226 reviewSessionAttribution()` 产出 `trunk_new / trunk_reused / ephemeral_forked`，注释明写 *mirrors Rust's `GuardianReviewSessionKind` selection*；该枚举在 Rust 定义了 `codex-rs/analytics/src/events.rs:327`，但其**唯一使用者是 `codex-rs/core/src/guardian/review_session.rs:26/335`**（＝ **同步** reviewer 的 session 模块），`tests.rs:2756 TrunkReused` 等断言也在同一同步子系统内。
- 跨轮/跨 compaction 生命周期：`SeedForReview`（`:360`）、`ResetAfterParentCompaction`（`:333`，调用点 `:1167`）、`DropStaleParentCompaction`（`:350`）、`markPriorReview`（`:246`，调用点 `:572`）—— 全是**长生命周期会话状态**，不是一次性调用。

### 1.2 session 生命周期跨 tool call（跨 turn）

reviewer 实例在 `RuntimeRouter` 层**只建一次并复用**：

```
appserver/agent_runtime.go:112  if r.services.GuardianReviewer != nil { return ... }
appserver/agent_runtime.go:115  reviewer := newModelGuardianReviewer(agent)
appserver/agent_runtime.go:143  r.services.GuardianReviewer = reviewer
appserver/guardian_reviewer.go:376  agent: &guardianSessionRunner{agent: agent}
```

### 1.3 `Review()` 与 thread/turn 的关系

`appserver/guardian_reviewer.go:390`：

```go
func (r *modelGuardianReviewer) Review(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error)
```

生产调用点（每次工具批准调一次，**thread/turn 级**，同一 reviewer 实例串行复用同一 session）：

```
appserver/runtime_router.go:14698   reviewer.Review(ctx, threadID, turnID, callID, action)
appserver/runtime_router.go:16847   reviewer.Review(ctx, threadID, turnID, itemID, action)
appserver/network_approval.go:334   reviewer.Review(ctx, active.threadID, active.turnID, "", action)
appserver/mcp_elicitation.go:107    h.reviewer.Review(ctx, request.ThreadID, request.TurnID, ...)
```

⇒ **是**：同一 thread 的多次 tool-call review 共享 `guardianSessionRunner` 的 `previous`，跨轮保留 review 状态。

### 1.4 framing：Go 侧 **从不选择** framing，恒为 SyncFull

```
state/guardian.go:583-585        Presentation ActionPresentation   （BuildPromptOptions 字段）
state/guardian_action_prompt.go:198-206  type ActionPresentation int
state/guardian_action_prompt.go:202      ActionPresentationSyncFull ActionPresentation = iota  ← 零值
```

- `git grep -n 'Presentation:' 9a62462b -- '*.go'`：生产代码**只有** `realtime/` 的 Bem 相关赋值，**没有任何** `Presentation: state.ActionPresentation…`。
- 两个生产 prompt 组装点 `appserver/guardian_reviewer.go:469` 与 `:478` 传入的 `state.BuildPromptOptions{...}` **不含 `Presentation`** ⇒ 零值 = `ActionPresentationSyncFull`。
- `Presentation` 唯一的消费点是 `state/guardian.go:678 joinPlannedAction(action, options.Presentation)`（只影响 planned-action 的 framing：`guardian_action_prompt.go:238` async 分支 / `:269` SyncDelta 分支）；**保留段拆分完全不看 `Presentation`**。

⇒ 与 syncl6 §7 的「高信度事实」一致：Go 生产调用点用 SyncFull framing，且**无拆分条件**。

---

## 2. 对照 Rust `#51627`（`5b0b253035`）的「sync 拆分条件」

### 2.1 上游 ground truth（原文）

`codex-rs/guardian-context/src/composition.rs:1-10`（HEAD，`#51627` 改过的头注释）：

```
//! Async retained context initially follows the transcript. Independent snapshot
//! preparation moves retained user instructions ahead of it, leaving assistant context here.
//! Stateful reviewers retain their existing section order.
```

- **拆分动作**在 `codex-rs/guardian-context/src/retained_instructions.rs:201 deduplicate_transcript_instructions()`（把 assistant originals + omission notice partition 到独立 `retained_assistant_context` 段，并把 instruction prefix 重新插回 `intro|root_conversation|sender_user_messages` 之后）。
- **唯一生产调用点**（非测试）：

```
$ git grep -n 'deduplicate_transcript_instructions' HEAD -- '*.rs' | grep -v test
HEAD:codex-rs/ext/guardian-v2/src/async_scorer/transcript.rs:95:  context.deduplicate_transcript_instructions();
HEAD:codex-rs/guardian-context/src/retained_instructions.rs:201:  pub fn deduplicate_transcript_instructions(&mut self) {
```

  `ext/guardian-v2` ＝ **async scorer**。
- **sync 路径不用它**：`codex-rs/core/src/guardian/input_budget.rs:150` 与 `request_budget.rs:92` 走 `context.retain_new_instructions(&history)`；`retain_new_instructions` 只**管理**已存在的 `retained_assistant_context`（清空/保 banner），**不创建**它（`retained_instructions.rs:269`）。同步侧的 test 佐证 `retained_instructions_tests.rs:415-421`：

```rust
let deduplicate = |context: &mut ComposedContext| match profile.target {
    crate::ContextTarget::Sync  => context.retain_new_instructions(&[]),
    crate::ContextTarget::Async => context.deduplicate_transcript_instructions(),
};
```

- **`RETAINED ASSISTANT CONTEXT` 标记在 Rust 只出现在 async 面**：

```
$ git grep -n 'RETAINED ASSISTANT CONTEXT' HEAD -- '*.rs'
HEAD:codex-rs/ext/guardian-v2/src/async_scorer/extension_tests.rs:2140/2142      ← async scorer 测试
HEAD:codex-rs/guardian-context/src/retained_assistant_context.rs:25/26           ← 标记定义
HEAD:codex-rs/guardian-context/tests/cache_prefix.rs:294/295/347                 ← #51627 新增回归（只用 ContextTarget::Async）
```

  `#51627` 同时改的 `codex-rs/app-server/tests/suite/v2/guardian_v2.rs` 也是 **app-server v2 guardian（＝ guardian-v2 ext，async）** 的断言（新断言：`">>> RETAINED USER INSTRUCTIONS END\n\n"` 紧跟 `">>> TRANSCRIPT START\n"`）。**sync 侧（`core/src/guardian`）没有任何 `RETAINED ASSISTANT CONTEXT` 断言**，且 `core/src/guardian/prompt.rs:338-345` 的 `render_retained_assistant` 把 assistant 原文渲染**塞进同一个 retained 段**。

### 2.2 sync 的段序（`session_id` 条件，`#49993` 引入、`#51627` 保留）

```
composition.rs:232   ContextSection::RetainedUserInstructions { items } => (
                         if session_id.is_some() { 2 } else { 9 },   // sync=2(transcript@4 之前)，async=9(之后)
```

⇒ Rust **sync**：retained 段（用户指令 ＋ assistant 原文，**同一段**）在 transcript **之前**，**没有** `RETAINED ASSISTANT CONTEXT` 段。

### 2.3 Go 侧对应面 = 哪一处？

- **不是** `state/guardian_sender_messages.go`（那是 `sender_user_messages` 段，与拆分无关）。
- **是**：
  - `state/guardian.go:639-667`（`BuildPromptWithOptions` 内的组装，注释显式写 `#51627`）：
    ```
    :642  retainedSections := RenderRetainedInstructionSections(options.RetainedContext)
    :643-645  if len(retainedSections.Instructions) > 0 { ... }        ← transcript 之前
    :652-664  if len(transcript) > 0 { ... "Recent transcript:" ... }  ← transcript
    :665-667  if len(retainedSections.AssistantContext) > 0 { ... }    ← transcript 之后
    ```
  - `state/guardian_retained_context.go:270 RenderRetainedInstructionSections`（**无 presentation 参数**）、`:257 retainedInstructionSectionPair`、`:227 SplitRetainedInstructionFragments`、`:198 RetainedUserInstructionsSectionItems`、`:283 RetainedAssistantContextSectionItems`、`:370 RetainNewRetainedInstructions`、`:402 DeduplicateRetainedInstructions`。
  - Go 侧「不拆分」的渲染器**已经存在**：`state/guardian_retained_context.go:317 retainedInstructionsSectionItems(fragments, legacy)` 就是 Rust sync 的单段形态。

### 2.4 引入该拆分的 Go commit（本车道历史）

```
$ git log --oneline -S 'Retained snapshot sections are split' 9a62462b -- state/guardian.go
e176a441 syncl3: separate retained assistant context from the instruction prefix (#51627)
```

`e176a441^:state/guardian.go`（拆分前）在 transcript 之前只渲染 **一个合并段** `RetainedUserInstructionsSectionItems(...)`（＝当时 Rust sync 序）；`e176a441` 之后变成**无条件**两段 ⇒ 这一步把 async 序套到了 Go 唯一的 sync 消费者上。

---

## 3. 值级探针（复跑 syncl6 的探针，打在**新基线** `9a62462b`；探针只在 `/tmp`）

```
$ cd /tmp/wt-9a62462b && git rev-parse HEAD
9a62462bc8e9e8153670aab0e776d5caeb5d2d3a
$ go test -overlay=/tmp/syncl6probe49993/overlay.json ./state/ -run TestZZProbe49993RetainedOrdering -count=1 -v
=== RUN   TestZZProbe49993RetainedOrdering
gen=0 root=0 retained=251 transcript=723 assistant=771 | order(root<retained<transcript<assistant)=true | prefix-stable=true
gen=1 root=0 retained=251 transcript=723 assistant=771 | order(root<retained<transcript<assistant)=true | prefix-stable=true
gen=2 root=0 retained=251 transcript=723 assistant=771 | order(root<retained<transcript<assistant)=true | prefix-stable=true
gen=3 root=0 retained=251 transcript=723 assistant=771 | order(root<retained<transcript<assistant)=true | prefix-stable=true
--- PASS: TestZZProbe49993RetainedOrdering (0.00s)
ok  	codex_go/state	0.006s
```

探针走**生产入口** `BuildPromptWithOptions`（默认 `Presentation = SyncFull`），`assistant=771 > transcript=723` ⇒ **Go 的 sync prompt 里确实带独立的 `RETAINED ASSISTANT CONTEXT` 段，且位于 transcript 之后**。Rust sync 侧不存在该段（§2.1 的 grep）。

> 注：Go 侧已落地的 `appserver/retained_context_test.go:204/216/334`、`state/guardian_retained_context_test.go:391/579/672` 正是**断言这个同步拆分**的回归 —— 也就是说当前 Go 测试集把「sync 也拆」编码成了期望值。

---

## 4. 三选一判定的证据汇总

| 事实 | Go | Rust（`#51627` 后） | 一致？ |
|---|---|---|---|
| 存在 stateful session（跨轮复用 trunk） | ✔ `guardian_reviewer.go:211/286` | ✔ `core/src/guardian/review_session.rs`（sync） | — |
| framing | 恒 `SyncFull`（零值，无选择逻辑） | `compose()` 按 `ContextPresentation` 四选一（`composition.rs:155-190`） | Go 只有 sync 面 ⇒ 不是缺口 |
| retained 用户指令位置 | transcript **之前**（`:643-645`） | sync：position 2 ＝ transcript 之前 | ✔ |
| assistant 原文位置 | 独立段，transcript **之后**（`:665-667`） | sync：**同一 retained 段内**（transcript 之前）；只有 async 才拆到之后 | ✗ **缺口** |
| `RETAINED ASSISTANT CONTEXT` 标记 | sync prompt 里出现 | sync prompt 里**从不**出现 | ✗ **缺口** |
| 拆分是否有条件 | 无（`RenderRetainedInstructionSections` 不看 `Presentation`） | 有（仅 `deduplicate_transcript_instructions`，async-only） | ✗ **缺口** |

---

## 5. 最小落点与避让面

**最小落点（待授权）**
1. `state/guardian.go:639-667`：把拆分限定在 `options.Presentation == ActionPresentationAsync`；`SyncFull`/`SyncDelta` 走**单段**（Rust sync 序：`retained_user_instructions` 含两族，置于 transcript 之前，无 `RETAINED ASSISTANT CONTEXT`）。
2. 复用现成渲染器 `state/guardian_retained_context.go:317 retainedInstructionsSectionItems(RenderRetainedInstructions(ctx), legacy)`（＝Rust sync 单段形态）；如需保留 API 形状，可给 `RenderRetainedInstructionSections` / `RetainNewRetainedInstructions` / `DeduplicateRetainedInstructions` 加 presentation 维度。
3. 需同步更新的**我自己的**测试期望：`appserver/retained_context_test.go:204/216/334`、`state/guardian_retained_context_test.go:391/579/672`（plus `guardian.go:639` 注释）。

**避让面判定：不撞。**
`state/` 与 `appserver/retained_context_test.go` 均**不在**禁碰清单（禁碰＝`appserver/runtime_router.go`、`appserver/turn_runtime.go`、`tui/state.go`、`sandbox/windowssandbox/`、`appserver/message_board.go`、`appserver/{agent_controller,environment_inheritance}.go`、`rollout/`、`model/`、`config/`、`execserver/`、`turn/`）。本单**未修改任何文件**。

---

## 6. 可复跑命令

```zsh
cd /home/jacks/jacks_dev/codex_go && git fetch origin --prune -q && git ls-remote origin main   # 9a62462b
git grep -n 'Presentation:' 9a62462b -- '*.go'
git grep -n 'ActionPresentation' 9a62462b -- '*.go'
git grep -n 'DeduplicateRetainedInstructions\|RetainNewRetainedInstructions\|HasSplitAssistantOmission' 9a62462b -- '*.go'
git grep -n 'RETAINED ASSISTANT CONTEXT' 9a62462b -- '*.go'
git log --oneline -S 'Retained snapshot sections are split' 9a62462b -- state/guardian.go

cd /home/jacks/jacks_dev/codex
git grep -n 'deduplicate_transcript_instructions' HEAD -- '*.rs'
git grep -n 'RETAINED ASSISTANT CONTEXT' HEAD -- '*.rs'
git show HEAD:codex-rs/guardian-context/src/composition.rs | sed -n '1,12p;224,240p'
git show HEAD:codex-rs/guardian-context/src/retained_instructions_tests.rs | sed -n '412,424p'

cd /tmp/wt-9a62462b && go test -overlay=/tmp/syncl6probe49993/overlay.json ./state/ \
  -run TestZZProbe49993RetainedOrdering -count=1 -v
```

## 7. 未决 / 待裁

1. **是否授权出补丁**（`update/r86_patches/syncl3_51627_sync_split.patch`，口径＝§5 步骤 1-3）。本单按派单要求**只出结论**。
2. 若队长认为「Go 的 sync 消费者按 async 序也 acceptable（有意偏离）」，请明确裁定 —— 但按 Rust `#51627` 原文（composition.rs 头注释）与 grep 证据，**sync 不拆**是上游的显式语义，Go 当前实现与上游不一致。
3. `#49993` 本身维持 syncl6 的结论：**已取代型 N/A**（其 async 序被 `#51627` 覆盖）—— 本单只闭掉它的 §7 未决项。
