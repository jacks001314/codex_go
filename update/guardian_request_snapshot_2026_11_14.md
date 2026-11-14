# Guardian 请求快照复用（#61185）

日期：2026-11-14
上游提交：`d4242793f70c4ee471d7903a2c448bface1bc38e` Defer Guardian context limits to request preparation (#61185)

## 上游要点

- 把根会话证据、保留指令、assistant context、verified answers 保留到请求准备阶段，固定上限与弹性分配共享同一份原始证据。
- 固定根视图（`RootConversationInput` 的 `fixed` 字段）在构造时准备一次并复用，弹性准备仍从原始 source 重新分配。
- 同步与异步复核共用 section 准备；异步评分器把分类器搭建与最终输入组装拆开，并在去重与 token 计算前规范化新图像。

## Go 现状与缺口

复核请求准备此前每一趟都重新抓取宿主 transcript：

- `modelGuardianReviewer.review` 在每次尝试开头调用 `transcriptSelection(threadID, Fixed)`；
- 弹性预算分支在 `buildPrompt` 内再次调用 `transcriptSelection(threadID, Elastic)`；
- 一次弹性复核因此对同一份历史抓取两次，重试时还会重复抓取，两次准备之间没有共享同一份原始证据。

Go 侧与之对应的等价物是 `guardianCollectTranscriptWithProfile` + `prepareGuardianTranscriptSelection`：收集不做任何上限处理，固定/弹性差异只体现在准备阶段。

## 实现

- 新增 `guardianReviewTranscriptSnapshot`：持有一次 `guardianCollectTranscriptWithProfile` 的原始收集、复核开始时解析的 profile，以及惰性准备的固定视图。
- `selection(Fixed)` 只准备一次并缓存，`selection(Elastic)` 仍从同一份原始收集按弹性策略整条分配（保留原始文本，供 section 预算决定取舍）。
- `modelGuardianReviewer.transcriptSnapshot` 由 router 的 `guardianReviewTranscriptSnapshotForReview` 注入；`review` 在尝试循环前抓取一次，固定与弹性两条准备路径、以及同一复核的重试都复用该快照。
- 保留原 `transcriptSelection` 钩子作为回退路径，未注入快照的 reviewer（包括直接构造 `modelGuardianReviewer` 的既有测试）行为不变。
- 悬停一致性检查（`guardianReviewContextChanged`，Rust #53797）仍然读取实时历史，用于判断复核期间证据是否变化；它复用快照就会失去检测能力，因此保持原样。

## 需要澄清的两项（审计结论）

- 异步评分链路：Go 复核器是简化的同步 reviewer，没有 Rust guardian-v2 的 async_scorer（`appserver/guardian_reviewer.go` 中 `guardianActionExceedsReviewBudget` 注释已注明 "Go has no async scorer"）。上游 async_scorer 内的 section 准备、observation 快照改动在 Go 无对应代码，属架构性不适用。
- 其它图像路径：上游改动集中在 `async_scorer/sampler.rs`、`observation.rs`、`transcript.rs`，同为异步评分器路径；Go 同步复核的图像准入（`RetainNewImages` 去重 + `guardianAdmitImages` 预算）此前已有回归（`guardian_state_full_2026_11_14.md`）。

## 验证

```
go test ./appserver -run "TestGuardianReviewTranscriptSnapshotCachesFixedViewLikeRust|TestGuardianReviewCapturesTranscriptOncePerRequestLikeRust" -count=1 -v
--- PASS: TestGuardianReviewTranscriptSnapshotCachesFixedViewLikeRust (0.00s)
--- PASS: TestGuardianReviewCapturesTranscriptOncePerRequestLikeRust (0.05s)

go test ./appserver -run "Guardian" -count=1
ok  	codex_go/appserver	8.320s

go vet ./appserver        # 无输出
go test ./context -count=1
ok  	codex_go/context	0.042s
```

新增回归：

- `TestGuardianReviewTranscriptSnapshotCachesFixedViewLikeRust`：固定视图准备一次后即使改写入参也不再变化；弹性准备从同一份抓取保留原始工具输出（固定准备已按其上限截断）。
- `TestGuardianReviewCapturesTranscriptOncePerRequestLikeRust`：真实线程历史 + 弹性配置下，一次复核只抓取一次宿主 transcript，请求仍带计划动作、section 预算省略提示、保留的用户指令，并只投递被 section 预算保留的工具条目子集。

## 结论

#61185 的 Go 可落地部分已闭环：请求快照缓存、固定视图复用、弹性准备共享原始证据均已实现并有回归；异步评分链路与异步图像路径在 Go 架构下不适用。