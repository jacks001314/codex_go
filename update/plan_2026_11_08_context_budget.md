# 上下文预算 3 条 + 1 条 Guardian 判定（`55897425e8..f49ae52079` 收尾组）

- 作者：ledger-align（对齐账本 lane）
- 日期：2026-11-08
- 性质：只读分析。不改 Go 代码，不动 `parity/**`（契约表仅为建议）。
- Rust 基线：`/home/jacks/jacks_dev/codex`（`codex-rs`，HEAD `748ab55ca7`），四条提交均可 `git show`；本轮**未设** `CODEX_RUST_ROOT`。
- Go 基线：本 lane 完整镜像（6,954 文件 / 约 72 MB）；「零命中」均为全树结果。

## 0. 判定汇总

| commit | PR | 主题 | 判定 |
| --- | --- | --- | --- |
| `bbf57cec15` | #59908 | original-detail 上下文估算不再整图解码 | **not_applicable**（并发现一条既存账本过判，见 §1.4） |
| `b8725c1896` | #59902 | elastic 预算下已验证问答整条保留 | **partial** |
| `b1a0dff158` | #59891 | elastic 准备里给 REPL provenance 加上界 | **partial** |
| `ddd1812a51` | #59842 | 终答消息携带定向审核决定 | **partial** |

组内：`partial 3`、`not_applicable 1`、`ported 0`、`aligned 0`。

## 1. `bbf57cec15` #59908 — original-detail 估算不再整图解码

### 1.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/core/src/context_manager/history.rs:1495-1530` | `estimate_original_image_bytes`：把 `image::load_from_memory`（整图解码）换成 `image::guess_format` + `ImageReader::with_format(...).into_dimensions()`，只读尺寸后按 `ORIGINAL_IMAGE_PATCH_SIZE` 折算 patch 数；读不出尺寸时仍返回 `None` 走回退 |
| 同上（import） | 新增 `image::ImageReader` 与 `std::io::Cursor` |

### 1.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| Go 的 inline 图片计费是**固定 resized 常量**，与 `detail` 无关 | 【F:context/history.go†L587-L598】`EstimateItemTokens`：`if content.Kind == ContentInputImage { bytes -= len(ImageURL); bytes += 7373 }` |
| 压缩侧同样固定 | 【F:compact/compact.go†L1377-L1386】`compactImageReferenceBytes`：只有 **file reference** 且 `detail == "original"` 时按 `compactOriginalImageMaxPatches * 4` 计费；inline（无 `FileID`）一律 `compactResizedImageBytes`（7373） |
| 全树没有 patch 估算 | `rg -in "patches" -g '*.go' -g '!*_test.go'` 只命中 applypatch/dispatch 等无关语义；`ORIGINAL_IMAGE_PATCH_SIZE` 等价常量不存在 |
| 有的只是“降级” | 【F:model/responses_agent.go†L3993-L4006】`downgradeResponseInputImageDetailInPlace`：某些路径把 `original` 改写成 `high`（这是另一种取舍，不是估算） |

### 1.3 判定：`not_applicable`

`#59908` 优化的是「已存在的 original-detail 尺寸估算」这一调用点；**Go 侧不存在这个调用点**（inline 图片一律 7373 字节），所以没有“整图解码”可去掉。按逐条口径记 `not_applicable`。

### 1.4 附带的账本问题（重要，建议单独处理）

引入 `estimate_original_image_bytes` 的是 **`ce0323f2f2` / #54776（2026-10-17，早于基线 pin）**：

```
$ git log --format='%h %ci %s' -S "estimate_original_image_bytes" -- codex-rs/core/src/context_manager/history.rs
ce0323f2f2 2026-10-17 Support typed content in contextual prompt fragments (#54776)
```

而 `parity/commits.json` 里这条是 **`complete`**，evidence 讲的全是 typed content（`eventmap.ContentItem`、`ToolOutputParts`、世界状态 typed fragment hash…），**没有任何一句涉及 original-detail 的 token 估算**。也就是说：这条的 `complete` 是**过判**，Go 少了一支行为。

影响面：`detail: original` 的大图在 Go 侧只被计 7373 字节（resized 值），而 Rust 按真实尺寸折算 patch（最大 10,000 patch）。长会话里叠加若干原图会**系统性低估**上下文占用，可能让上下文预算/压缩时机偏晚。

建议（二选一，都属于重锚那一批改动）：
1. 把 `#54776` 降为 `partial`，并补一条契约；或
2. 先落实现（成本低：`context.EstimateItemTokens` 里对 `ContentInputImage` 增加 “若 detail==original 且能解析尺寸 → patch 估算”，Go 用 `image.DecodeConfig` 即可，**仓库里已有三处现成用法**：【F:session/history.go†L769】、【F:exec/exec.go†L2616】、【F:tui/transcript_image/transcript_image.go†L76】），再维持 `complete`。

我本轮**没有**改 commits.json（重锚是你一次性做的），只把证据放这里。

## 2. `b8725c1896` #59902 — elastic 预算下已验证问答整条保留

### 2.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/guardian-context/src/enforcement.rs:45-52` | `Budgeted` 新增 `verified_answer: bool`；`:72-78` 新增 `Budgeted::verified_answer(content)` 构造器；`map`/`historical`/`optional` 全部补字段 |
| `codex-rs/guardian-context/src/authorization.rs:124-130` | root 会话里 `GuardianRootMessage::UserInput(_)`（已验证的用户回答）标成 `verified_answer`，优先于 `protected` |
| `codex-rs/guardian-context/src/composition.rs:336-340`、`:365-375`、`:399-436`、`:499-535` | 各处 `Budgeted` 构造与解构补 `verified_answer`；受信 user-answer 段按 `protected` 决定 `protected`/`verified_answer` |
| `codex-rs/guardian-context/src/preparation/elastic.rs:55-95` | 超上限时：`protected`/image/audio 跳过；**`verified_answer` 整条替换成 `Budgeted::protected(GuardianRootMessage::IncompleteVerifiedAnswers.render())`，并把 `retained_bytes` 记为 0**；其它文本才走 `truncate_content_to_fit` |
| `codex-rs/guardian-context/tests/cache_prefix.rs`（+138） | 两个问题 + 相反答案的回归：超上限的整条省略、能放下时完整保留 |

### 2.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| Go 有 elastic 条目标记与截断，但**没有 verified 标记** | 【F:state/guardian_elastic_allocation.go†L40-L70】`GuardianElasticItem{Exact, Protected, ImageURL, Text, …}`；`rg -n "erified" state/*.go \| grep -v _test` → 只命中 `guardian_root_messages.go` 的 **notice 文本**，条目结构里无该字段 |
| 截断对所有非 Exact/Protected 文本一视同仁 | 【F:state/guardian_elastic_preparation.go†L26-L76】超上限即 `guardianElasticTruncateTextToFit` + `Incomplete=true` + `Source.Complete=false` |
| Go 有“不完整已验证答案”的**通知文案**，但只在段级补 | 【F:state/guardian_root_messages.go†L26-L28】`RootMessageIncompleteVerifiedAnswers`；使用点【F:appserver/guardian_root_context.go†L193-L195】（当 `missingVerifiedAnswers` 时追加一条），**不是**逐条替换超限记录 |
| 已验证答案的数据面存在 | `retainedctx` 的 `VerifiedAnswer`（`retainedctx/retained_context_test.go:132-162` 已锁 `VerifiedAnswers()`/`VerifiedAnswersComplete()`）；`appserver/verified_answer_test.go:21` `TestRuntimeRouterRecordsVerifiedAnswersLikeRust` |
| root 会话把它当普通 user input | 【F:appserver/guardian_root_context.go†L466-L470】`state.RootMessage{Kind: RootMessageUserInput, Text: RenderOriginalVerifiedAnswer(...)}` → 进 elastic 后**会被截断** |

### 2.3 缺什么 / 影响面 / 最小修复路径

- 缺：①`GuardianElasticItem` 的 `VerifiedAnswer` 布尔；②root/受信段构造时按来源打标；③`PrepareGuardianElasticEntries` 里“超限 → 整条替换为受保护的 `IncompleteVerifiedAnswers` 通知 + `RetainedBytes=0`”这一分支。
- 影响面：被截断的“问答”会保留问题、丢掉答案，或反过来——审核者看到的是**半个问答**，却仍可能被当作授权依据；这正是该提交要避免的“关联被模糊”。
- 最小修复路径（三处，均无新依赖）：
  1. `state/guardian_elastic_allocation.go` 的 `GuardianElasticItem` 加 `VerifiedAnswer bool`；
  2. `appserver/guardian_root_context.go:466` 处把这类 root message 标为 `VerifiedAnswer=true`（受信 user-answer 段同理，按 `Protected` 分支决定）；
  3. `state/guardian_elastic_preparation.go` 的截断循环里，在 `Exact/Protected/ImageURL/空文本` 判断之后插入：`if item.VerifiedAnswer { item.Text = rootIncompleteVerifiedAnswersNotice; item.Protected = true; item.TokenCost = 0; append(Truncations, {OriginalBytes: len(text), RetainedBytes: 0}) ; continue }`。
- 测试要点：两个问题 + 相反答案；超上限时两条**整条**省略并出现通知；限额放宽时两条完整保留；`Truncations` 里 `RetainedBytes == 0`。

## 3. `b1a0dff158` #59891 — 给 REPL provenance 加上界

### 3.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/guardian-context/src/preparation/repl.rs:307-320` | 新增 `response_selection_bytes(header, body_bytes, budgeting)`：Fixed 用 `header.len()+body`；**Elastic 用 `TruncationPolicy::Tokens(content_tokens(header)+1).byte_budget()`**——把“受保护的响应头”按 wire 上的 token 成本计，而不是按原始字节 |
| `…/repl.rs:334-370` | `select_node_repl_responses` **删掉了 `if budgeting == Elastic { return 全部保留 }` 的早退**：elastic 也要做“最新连续后缀”选择；被丢掉的响应计入 `omitted_responses` |
| `…/repl.rs:145-165`、`:250-270` | 两条布局（Text / Multimodal）都用 `response_selection_bytes` 计每条的准入成本；`:160` 原先把 `budgeting` 参数往下传，现在改由成本函数承担 |
| `…/repl.rs:1-5` | 模块注释更新为「Elastic selection bounds protected provenance before allocating response bodies」 |

### 3.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| Go 有 REPL 证据面与 Fixed/Elastic 两档 | 【F:context/node_repl_review_evidence.go†L26-L40】`GuardianContextBudgeting` + `NodeReplReviewEvidenceMode`；`:133` `SnapshotSinceWithBudgeting(...)` |
| **Elastic 仍是“全保留”早退**（即本提交要改掉的旧行为） | 【F:context/node_repl_review_evidence.go†L234-L239】`if f.budgeting == GuardianContextBudgetingElastic { return 0, f.omittedResponses }`，注释写的是 Rust #58454「elastic preparation keeps every captured response」 |
| 每条成本用**原始字节**，不是 token 折算 | 【F:context/node_repl_review_evidence.go†L262-L273】`responseCost` = `len("[REPL response %d %s]\n")` + 正文字节 |
| provenance 本身有界（另一条，不是本条） | 【F:context/node_repl_review_evidence.go†L20】`maxNodeReplProvenanceBytes = 128` |
| 硬上限常量在 | 【F:context/node_repl_review_evidence.go†L18-L24】`maxNodeReplRenderedBytes` 等 |

### 3.3 缺什么 / 影响面 / 最小修复路径

- 缺：①Elastic 也要做后缀选择（删掉早退）；②Elastic 的每条 header 成本改成 token 折算（Go 侧没有 `TruncationPolicy::Tokens(t).byte_budget()` 的直接等价物，需要按 Rust 的 4 字节/ token 折算，或复用 `utils.ApproxTokenCount` 再加固定系数）；③被丢弃的响应计入 `omitted`。
- 影响面：**审查者预算被“受保护的响应头”吃光**——1 万条小响应时，光是 `[REPL response N tool=… cell=… call=…]` 头就能撑爆上下文，正文反而没分到额度；这正是提交描述里的场景。
- 最小修复路径：
  1. 【F:context/node_repl_review_evidence.go†L234-L239】删除 Elastic 早退，让两种 budgeting 都走后缀选择；
  2. 【F:context/node_repl_review_evidence.go†L262-L273】给 `responseCost` 增加 budgeting 参数：Elastic 时把 header 按 `tokens = ApproxTokenCount(header) + 1`、`cost = tokens * 4` 计（与 Rust `byte_budget()` 同义），正文仍按字节；
  3. 选择循环里把跳过的响应累加进 `omitted`（Go 现在只在 Fixed 分支累加）。
- 测试要点：1 万条小响应、Text 与 Multimodal 两种模式；保留的必须是**最新连续后缀**；正文与图片不被破坏；`omitted` = 存储丢弃 + 选择丢弃之和。

## 4. `ddd1812a51` #59842 — 终答消息携带定向审核决定

### 4.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `codex-rs/protocol/src/protocol.rs:2396-2400` | 新增 `TargetedAnswerModeration { item_id, moderated }`；`:2799-2810` `AgentMessageEvent` 增加可选 `moderation`（legacy 事件保留原 item id） |
| `codex-rs/protocol/src/items.rs:155-159`、`:163-172` | `AgentMessageModeration { moderated }`；`AgentMessageItem` 增加 `moderation: Option<…>`（`skip_serializing_if`） |
| `codex-rs/core/src/stream_events_utils.rs:335-348` | `apply_answer_moderation(item, decision)`：**仅**当 `TurnItem::AgentMessage`、`phase == FinalAnswer`、`answer.id == decision.item_id` 时挂上 `moderation` |
| `codex-rs/core/src/session/turn.rs:2977`、`:3182`、`:2795-2815`、`:3264-3268` | turn 内维护 `answer_moderation`；`ResponseEvent::Created` 时清空（新响应重置）；plan 模式与普通模式都调用 `apply_answer_moderation` |
| `codex-rs/app-server-protocol/src/protocol/v2/item.rs:153-168`、`:329-336`、`:976` | v2 `AgentMessageModeration` + `ThreadItemPayload::AgentMessage{ moderation: Option<…> }`，从 core 转换 |
| `codex-rs/app-server-protocol/src/protocol/thread_history.rs:627-645` | 历史重建时：**有调解决定就用它的 `item_id` 作为条目 id**（保留原生 id），并带上 `moderation` |
| `codex-rs/protocol/src/legacy_events.rs`（+34） | legacy 事件在存在调解决定时把多个文本片段合并成一条 |
| 预生成 schema/TS/Python（23 个 json + 3 个 ts + py） | 协议表面同步 |

### 4.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| Go **有**turn 级审核元数据通道（父能力） | 【F:appserver/notifications.go†L33】`NotificationTurnModerationMetadata = "turn/moderationMetadata"`；`:515` `TurnModerationMetadataNotification`；【F:protocol/exec_events.go†L730-L734】`TurnModerationMetadata`；`model/responses_stream.go:2172-2174` 解析 `openai_chatgpt_moderation_metadata` |
| **没有** `final_answer_moderation` 这个定向键 | `rg -n "final_answer_moderation\|TargetedAnswerModeration" -g '*.go'` → **0 matches** |
| **没有** agent message 上的 `moderation` 字段 | `appserver.ThreadItem`（【F:appserver/protocol.go†L1036-L1056】）无该字段；`rg -n '"agentMessage"' -g '*.go'` 的构造点（【F:state/thread_history_projection.go†L401-L410】、【F:rollout/turn_item.go†L238】）只处理 `phase`，无 moderation |
| 历史投影只在 `phase == final_answer` 时记 `final_agent_item_id` | 【F:state/thread_history_projection.go†L401-L410】 |
| 但安全资源通道已按 turn 工作 | `tui/tea/safety_recourse.go:14-30`、`tui/tea/model.go:2293`（`pendingSafetyRecourse`）——说明 Go 现在靠 **turn 级** 信号做安全提示，而不是逐条终答 |

### 4.3 缺什么 / 影响面 / 最小修复路径

- 缺：①解析 `final_answer_moderation`（含 `item_id` 匹配与“新响应重置”）；②`AgentMessageItem`/`ThreadItem`/历史重建的 `moderation` 字段；③legacy 事件在有决定时合并文本片段并保留原生 item id；④schema/TS 表面（与已知的 `app-server-schema` partial 域同源）。
- 影响面：客户端只能拿到“这一轮被审核过”的粗粒度信号，无法把提示精准挂到**那一条**终答上；多段素材/计划模式下尤其明显（一条被拦，其它同轮消息也一起吃提示）。
- 最小修复路径：
  1. `model/responses_stream.go` 解析 `final_answer_moderation`（`{item_id, moderated}`），在 `ResponseEvent::Created` 等价处清空（Go 的 `response.created` 处理分支），随 `handle_output_item_done` 等价路径传给收尾逻辑；
  2. `protocol`/`appserver` 的 agent message 结构加可选 `moderation`（`omitempty`），`state/thread_history_projection.go` 在 `phase=="final_answer"` 且 id 匹配时写入，并把 item id 用原生 id；
  3. legacy 事件：有决定时把同条消息的多个文本片段合并成一条（现在 Go 的 `protocol/exec_events.go` 按片段产 `agent_message`）；
  4. schema 导出：等 `app-server-schema` 域那次 re-vendor 一起补，别单独改 `.zst`。
- 测试要点：普通模式与 plan 模式各一条；只有 id 匹配的那条终答带 `moderation`；新响应开始时清空旧决定；历史重建后 item id 为原生 id 且 `moderation` 保留；无决定时序列化为 `null`/缺省。

## 5. 契约登记表（4 条）

字段同 manifest：`id/kind/rustPaths/goPaths/phase/status/verifier`。**`TestContractVerifiersExistInGoTestFiles` 要求 verifier 名字现在就在 Go 树里**，故「可立即登记」列全部是现存测试（已逐个 `rg` 核对）。

| # | contract id | kind | rustPaths | goPaths | phase | status | 可立即登记的 verifier | 需先落的 verifier + 要点 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `bbf57cec15` #59908 | `image-original-detail-token-estimate` | `source-surface` | `core/src/context_manager/history.rs` | `context`、`compact` | `P1` | `missing` | `TestMergeContextualMessagesAndTokenInfo`（只覆盖现行固定 7373 行为） | `TestOriginalDetailImageTokensUseDimensionsLikeRust`：`detail:original` 的 inline 图按尺寸折算 patch；读不出尺寸回退；不得整图解码（可用超大伪造头构造） |
| `b8725c1896` #59902 | `guardian-verified-answer-whole` | `source-surface` | `guardian-context/src/enforcement.rs`、`guardian-context/src/preparation/elastic.rs`、`guardian-context/src/authorization.rs` | `state`、`appserver`、`retainedctx` | `P1` | `partial` | `TestGuardianElasticEntryCapsBeforeAllocationLikeRust;TestRuntimeRouterRecordsVerifiedAnswersLikeRust` | `TestGuardianElasticKeepsVerifiedAnswersWholeLikeRust`：超限整条替换为受保护通知且 `RetainedBytes=0`；限额放宽时完整保留 |
| `b1a0dff158` #59891 | `guardian-repl-provenance-bound` | `source-surface` | `guardian-context/src/preparation/repl.rs` | `context`、`state` | `P1` | `partial` | `TestNodeReplElasticRenderingKeepsStoredTextImagesAndOmissionsLikeRust` | `TestNodeReplElasticBoundsProvenanceHeadersLikeRust`：1 万条小响应下 elastic 也做最新连续后缀选择；omitted 合并存储与选择；Text/Multimodal 双布局 |
| `ddd1812a51` #59842 | `final-answer-moderation` | `source-surface` | `protocol/src/items.rs`、`protocol/src/protocol.rs`、`app-server-protocol/src/protocol/v2/item.rs`、`app-server-protocol/src/protocol/thread_history.rs` | `appserver`、`state`、`protocol` | `P1` | `partial` | `TestModelSafetyRecourseDefersBlockedTurnAndExplainsIt`（turn 级通道，非逐条） | `TestFinalAnswerModerationAttachedToCompletedAnswerLikeRust`：id 匹配才挂；plan 模式同样生效；新响应重置；历史重建保留原生 id 与 flag |

> `#59908` 我建议**登记为 `missing` 而不是跳过**：它把一个既存过判（`#54776` 记 complete）显性化；如果你选“改账本降级 #54776”的路线，这条契约可以同时删掉，两条只能留一条。

## 6. 复现命令

```
$ rg -n "final_answer_moderation|TargetedAnswerModeration" -g '*.go'                 → 0 matches
$ rg -n "erified" state/*.go | grep -v _test                                        → 仅 guardian_root_messages.go 的通知文案
$ rg -in "patches" -g '*.go' -g '!*_test.go'                                        → 无 original-image patch 估算
$ rg -n "7373" context/*.go compact/*.go | grep -v _test                            → context/history.go:596、compact/compact.go:1347
$ rg -n "GuardianContextBudgetingElastic" context/node_repl_review_evidence.go      → :235（早退分支）
$ rg -n "NotificationTurnModerationMetadata|TurnModerationMetadata" -g '*.go' | head → appserver + protocol + exec
$ git log --format='%h %ci %s' -S "estimate_original_image_bytes" -- codex-rs/core/src/context_manager/history.rs
  ce0323f2f2 2026-10-17 Support typed content in contextual prompt fragments (#54776)   ← 早于 pin
```

反证（承载面存在、不是镜像缺文件）：

```
$ ls guardiancontext/            # 只有 context.go：Go 没有 guardian-context crate 的 elastic 实现，那份在 state/
$ ls state/guardian_elastic_*    # allocation/preparation/prompt/budget 全在
$ ls context/node_repl_review_evidence.go context/guardian_budgeting.go
```

## 7. 结论

- 4 条里 3 条 `partial`、1 条 `not_applicable`；与前两组不同，这一组的三个 `partial` 都是**在既有实现上差一个分支/一个字段**，没有结构性缺失（elastic 引擎、REPL 证据、turn 审核通道都在 `state/` 与 `context/`）。
- **最值得优先做的是 `#59891`**：删 4 行早退 + 改一个成本函数，就能消除“1 万条小响应把审查者预算吃光”的真实风险。
- **`#59908` 带出一条既存账本瑕疵**：`#54776`（`ce0323f2f2`）记 `complete`，但 `estimate_original_image_bytes` 这一支在 Go 完全不存在；`detail: original` 的大图被按 7373 字节计。建议你在重锚那一批里二选一处理（降级 or 补实现），我在 §1.4 给了两处最小落点。
- 本文件不改 Go 代码、不改 `parity/**`。

## 7. 主干处理（/root，2026-11-08）

- 已合并本文件；按 §A 选**降级路线**：`parity/commits.json` 里 `ce0323f2f2`（#54776）由 `complete` 改为 `partial`，evidence 追加事实（`context/history.go:596`、`compact/compact.go:1347` 固定 7373 字节、无 patch 估算）、影响（`detail:original` 系统性低估 → 压缩与预算时机偏晚）与修复路径（`image.DecodeConfig` 取尺寸 → patch 估算，上限 10000，不整图解码；仓库已有三处用法）。
- 因此 **`image-original-detail-token-estimate` 契约未登记**：过判已在账本层面显性化，再挂一条长期 `missing` 的契约只会让 `certificationReady` 多一个永久 false。等实现落地时再按 §5 那一行补登记（届时 status 直接 `complete`）。
- 其余 3 条契约已登记为 `partial`：`guardian-verified-answer-whole`、`guardian-repl-provenance-bound`、`final-answer-moderation`（verifier 均取「可立即登记」列的现存测试）。
- `#59908` 判定维持 `not_applicable`（Go 无 original-detail 尺寸估算调用点），过判归属 `#54776`，两者不混记。

