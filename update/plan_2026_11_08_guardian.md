# Guardian 一组（11 条）逐条判定（2026-11-08）

范围：`#59905`、`#59913`、`#59914`、`#59960`、`#59994`、`#59995`、`#59999`、`#60000`、`#60001`、`#60002`、`#60064`。
方法：Rust 侧用 artifacts 里的检出（`~/jacks_dev/codex/codex-rs`，已含 `55897425e8`）逐条 `git show` 读原 diff；
Go 侧读 `appserver/`、`agentboard/`、`state/`、`memories/`、`tool/`、`features/` 源码。**本轮不改任何 Go 代码。**

## 0. 判定口径

| 判定 | 含义 |
| --- | --- |
| `ported` | Go 已实现该提交的行为，能指出承载函数 |
| `aligned` | Go 现有实现的可观察行为已等于该提交修复/确立的行为 |
| `not_applicable` | Rust 侧代码所在的子系统在 Go 里根本不存在（crate/feature 缺席），没有可对照的 Go 行为 |
| `partial` | Go 有同一子系统（如 agentboard），但该提交的行为缺失 |

## 1. 结论摘要

- 计数：`not_applicable` 4、`aligned` 1、`partial` 6、`ported` 0。
- 这 11 条其实分三簇，Go 现状差别很大：
  1. **Decisions / async scorer 簇**（#59905、#59914、#60064）：Go 只有 `features/features.go:471` 注册的
     `guardianv2_decisions_comparison` 开关，**没有任何 Decisions 客户端、没有 async scorer**（全仓 `.go` 里
     `Decisions` 仅出现在注释与 TUI 的 `AvailableDecisions` 里），也没有 `memories/write` 的 startup quota。
    这三条判 `not_applicable`，但要写进缺口清单——一旦要移植 Decisions，凭据绑定规则必须一起搬。
  2. **reviewer 策略来源簇**（#59913）：Go 的 reviewer 选择本来就是"按被审线程 + turn"解析（`reviewPlan(threadID, turnID)`），
     不存在 Rust 那个"manager 启动目录"的第二条路径，判 `aligned`。
  3. **guardian-v3 证据链簇**（#59994、#59995、#59999、#60000、#60001、#60002）：Go 有 `agentboard` 这套 message board
     （含 Guardian 命名空间），但**没有只读捕获视图、没有 guardian 专用检索/订阅工具、没有采样选频道、没有按源顺序的投递计划**，
     六条全部 `partial`，缺口在 §4 列成清单。
- 一条顺带发现的文档/代码不一致：`agentboard/guardian.go:17` 注释说 "reviewers call ReadOnly on it, which still permits
  their own subscriptions"，但 `agentboard` 包里**没有 `ReadOnly` 方法**（全包 grep 只命中这行注释），
  `Board` 接口（`agentboard/api.go:26`）也没有只读变体。这正好是 #60002 要补的东西，可以当作 Go 侧的锚点。

## 2. 逐条判定表

| sha | 主题 | 判定 | 证据（Go 文件与函数） | 落地切入点 |
| --- | --- | --- | --- | --- |
| `f57966695f` | Validate implicit API keys for Guardian Decisions (#59905) | not_applicable | 无 Decisions 客户端：`features/features.go:471` 只登记 `guardianv2_decisions_comparison`（UnderDevelopment/默认关），全仓无 cached-key precedence 代码 | 移植 Decisions 时在凭据选择点照搬三条规则：拒绝空/带空白/FedRAMP key，且不重写 key；显式 `CODEX_GUARDIAN_DECISIONS_API_KEY` 覆盖保持原样 |
| `2210380232` | Resolve Guardian policy from the reviewed thread's catalog (#59913) | aligned | `appserver/guardian_reviewer.go:236` `reviewPlan func(threadID, turnID string)`、`:2285-2286` `reviewPlanContext` 按被审线程解析；目录刷新也是按线程（`appserver/guardian_review_catalog_refresh.go:48 ensureGuardianCatalogRefresh`、`:102 guardianReviewCatalogModels` 做 origin 过滤）；reviewer 选择由 `model.SelectApprovalReviewModel` 负责（`appserver/guardian_reviewer_config.go:46` 注释） | 无行为缺口。若将来加 async classification，必须复用这条 per-thread 解析，不要再建 manager 级目录 |
| `2c502d8b3d` | Bind Guardian Decisions requests to their credential source (#59914) | not_applicable | 同上：无 async_scorer / 无 queued comparison，`decisions.rs`、`startup.rs` 的凭据源绑定在 Go 无对应物 | 移植时要点：从线程 `ProviderAuthContext` 取登录凭据、发送前重解析、凭据源变了就拒绝排队中的比较、runtime 绑定线程钉住启动时的源 |
| `4e688da437` | Skip the network denial grace period when remote decisions drain (#59960) | not_applicable | Go 的 unified exec 只有输出/早退宽限（`tool/unified_exec.go:41 unifiedExecTrailingOutputGrace`、`:43 unifiedExecEarlyExitGrace`），**没有 deferred network approval、没有 blocked-request observer、没有 executor-owned remote proxy 决策队列**（`NetworkApproval` 仅出现在 `appserver/guardian_reviewer.go:3153` 的协议字段） | 移植 remote proxy 的延迟网络审批时再处理：决策排空即可结束等待，共享 listener 出现时保留宽限 |
| `e4bd7871b1` | Add read-only evidence retrieval tools for Guardian reviewers (#59994) | partial | Go 有 `agentboard.Board`（`agentboard/api.go:26`，含 `ListChannels/ListThreads/SearchPosts/ReadPost`）与 Guardian 命名空间（`agentboard/guardian.go:8`），但：没有"捕获视图 + 固定截止点"；没有 guardian 检索工具集（`agentboard/tools.go:46-62` 只有通用 board 工具）；搜索是 `instr(body_search, ?)`（`agentboard/local.go:991`），而 `body_search` 只存 `foldCase(request.Text)`（`:600`），不是"解码后 JSON 字段/嵌套函数参数的字面量搜索" | ① 给 board 加捕获读视图（namespace + cutoff）；② 把 5 个只读工具绑到视图；③ 搜索改为"解码后 JSON 字面量 + 大小写折叠"，覆盖引号/Windows 路径/Unicode 转义 |
| `8d0098930e` | Add subscription tools for Guardian reviewers (#59995) | partial | Go 的 `Board.SetSubscription`（`agentboard/api.go:51-54`）注释明确"任何成员都可改其他成员的订阅"，没有 self-only 约束、没有"只在捕获目录内"的限定、也没有回传 captured post IDs | 加 `ReviewerSubscriptions`：捕获目录、只允许改调用者自己、拒绝目录外的频道与显式 agent target、返回捕获点后的 post id |
| `af83299ada` | Add model-driven Guardian channel selection (#59999) | partial | Go 侧完全没有采样驱动选择：`agentboard` 无 sampling、无 `OperationResult`、无"校验频道名唯一后原子替换订阅" | 复用 `model` 的采样调用，实现"按审查任务 + 当前订阅 + 捕获目录选频道"，带字节预算、取消/超时前置检查、失败回滚 disposition |
| `5f82dc9e59` | Plan Guardian evidence delivery in source order (#60000) | partial | 无投递计划：`agentboard` 只有分页读取（`agentboard/local.go:959 SearchPosts`、`:1016 ReadThread`），没有"按插入序跨频道去重、按源顺序、拒绝超出 cutoff 的证据"的规划器；catalog 也没有 source cutoff 字段 | 加 `DeliveryState`：按插入序列分页读 request id、跨频道去重、按源顺序规划、拒绝超过 cutoff 的历史（注意与 `EvidenceSnapshot` 的 cutoff 对齐） |
| `ecef7e0936` | Add bounded original-evidence delivery for Guardian reviewers (#60001) | partial | 无投递执行：没有 `guardian_original_evidence` 上下文通道、没有"整篇按页读 + 按模型 tool-output 预算分批、超预算原件拒绝"的逻辑，也没有 admission 成功后才记 ordinal 的账本 | 实现 delivery（整篇读、按预算分批、超限拒绝而不是截断），admission 成功后再推进 channel 位置，并保证重试不重复投递 |
| `09bd19a0de` | Decouple Guardian retrieval from local message-board handles (#60002) | partial | `Board` 接口（`agentboard/api.go:26-55`）没有 namespace/read-view/subscription 捕获方法；`LocalAgentMessageBoard` 是具体类型；`agentboard/guardian.go:17` 注释里的 `ReadOnly` 在包里并不存在 | 抽出 `BoardReadView`（只读 + 捕获边界 + JSON 搜索配置）与 `AgentMessageBoard`（namespace/快照/订阅），把 Guardian 消费者改为依赖接口，保持 Guardian 命名空间断言 |
| `1c414e0d65` | Use thread runtime auth for Guardian Decisions and memory quota checks (#60064) | not_applicable | 两半都缺：Decisions 客户端不存在（同 #59914）；`memories/` 只有读/合并侧（`memories/pipeline.go`、`usage.go`、`readiness.go`），全文 grep 不到 `quota`/`RuntimeAuth`/`ProviderAuth` | 移植 memories/write 时用线程 `ProviderAuthContext` 做 startup/quota 检查并加 runtime 断言；Decisions 侧按 runtime auth + 环境路由（staging）绑定 |

## 3. 逐条说明

### 3.1 #59905 / #59914 Decisions 凭据

Rust 的 Decisions 是商业端点，隐式继承的凭据必须排除 FedRAMP key；#59914 进一步把请求绑定到凭据来源
（线程 `ProviderAuthContext` 优先、发送前重解析、凭据变了拒绝排队比较、runtime 绑定线程钉住启动源）。
Go 只有开关（`features/features.go:471`）没有客户端，所以这两条在 Go 侧无可观察行为。
风险点：将来若直接照抄 `OPENAI_API_KEY` 做 Decisions 调用，会把 FedRAMP key 和过期登录态带进去。

### 3.2 #59913 reviewer 策略随线程走

Rust 的问题是 async classification 用了 manager 启动时的 catalog，导致被审线程的 reviewer 策略不生效。
Go 的 reviewer 每次审查都通过 `reviewPlan(threadID, turnID)` 现算（`appserver/guardian_reviewer.go:236`），
目录刷新是按线程共享一次（`guardian_review_catalog_refresh.go:48`），origin 过滤也按父模型做
（`guardian_review_catalog_refresh.go:102`），所以不存在"manager 目录"这条路径，判 `aligned`。
唯一需要注意的是 Go 没有 async classification 消费者，这条"对齐"是**结构上不会犯**而不是"移植过"。

### 3.3 #59960 网络拒绝宽限

Rust 改动在 `core/src/tools/network_approval.rs`+`unified_exec/process_manager.rs`+`network-proxy/execution_scope.rs`：
执行方的 remote proxy 决策排空后不必再等 100ms 宽限。Go 的 unified exec 没有这套 deferred network approval /
blocked-request observer（`tool/unified_exec.go` 里只有输出与早退宽限），因此无对照物。

### 3.4 #59994 retrieval 工具

关键差异有三处：**捕获边界**（Rust 把 review 开始时的 board 视图冻结，之后的帖子不可见）、
**工具集**（`guardian_board` 的 5 个只读工具，含未订阅频道的搜索）、
**搜索语义**（解码后 JSON 的字面量搜索，覆盖引号、Windows 路径、Unicode 转义）。
Go 的 `SearchPosts` 是 `instr(body_search, ?)` 且 `body_search` 只存 `foldCase(request.Text)`，
结构化参数里的路径根本搜不到。

### 3.5 #59995 订阅工具

Rust 的 `ReviewerSubscriptions` 捕获目录并只给调用者自己的 `subscribe`/`unsubscribe`，
拒绝目录外的频道与显式 agent target，返回捕获点之后的 post id。
Go 的 `SetSubscription` 是"任意成员可改任意成员"（`agentboard/api.go:51-54`），既无 self-only 也无目录限定。

### 3.6 #59999 采样选频道

Rust 用模型采样（带字节预算、取消/截止观察）在捕获目录里选频道，校验频道名唯一后**原子替换**调用者订阅，
目录外的订阅保留，失败时返回可回滚的 disposition。Go 完全没有这条链路。

### 3.7 #60000 / #60001 投递

#60000 只做"计划"：按插入序列分页读 request id、跨频道去重、按源顺序、拒绝超过 cutoff 的证据。
#60001 做"执行"：整篇按页读、按模型 tool-output 预算分批、放不进预算的原件整体拒绝（不截断）、
admission 成功后才记 ordinal、完成后推进 channel 位置、重试与重叠选择不重复投递。
Go 两者都缺；这条链是 Guardian 证据"可复核"的核心，缺了它，Guardian 只能在 prompt 里塞快照。

### 3.8 #60002 抽象

Rust 引入 `BoardReadView`（只读 + 捕获边界 + JSON 搜索配置）与扩展后的 `AgentMessageBoard`
（namespace、读视图捕获、订阅操作），Guardian 消费者改依赖 trait。
Go 的 `Board` 接口（`agentboard/api.go:26`）没有这些能力，`agentboard/guardian.go:17` 的注释已经"预告"了 `ReadOnly`
却并不存在——可以直接拿这条注释当 TODO 锚点。

### 3.9 #60064 runtime auth

两处：Decisions 用 runtime 认证（双 header / 仅断言、敏感 header 保护、staging 端点），
memory startup/quota 用线程 `ProviderAuthContext`。Go 侧 Decisions 无客户端，`memories/` 无 quota 与 runtime auth，
因此整条无对照物。

## 4. Go 缺的证据链组件清单

按"缺了会漏什么"排序，前 6 项是 guardian-v3 证据链本体，后 4 项是凭据与运行时。

| # | 组件 | Rust 位置 | Go 现状 | 缺了会怎样 |
| --- | --- | --- | --- | --- |
| 1 | 捕获读视图 `BoardReadView`（namespace 断言 + 固定 cutoff + 只读） | `message-board/src/read_view.rs`、#60002 | 无（`agentboard/api.go:26` 接口里没有；`guardian.go:17` 注释提到 `ReadOnly` 但方法不存在） | 评审期间新写入的帖子会进入证据，审查边界不封闭 |
| 2 | Guardian 只读检索工具集（5 个，可搜未订阅频道） | `guardian-v3/src/retrieval_tools.rs`、#59994 | `agentboard/tools.go:46-62` 只有通用 board 工具，无 guardian 变体 | 评审无法主动取证，只能吃快照 |
| 3 | `ReviewerSubscriptions`（self-only + 目录限定 + captured post id） | `guardian-v3/src/subscription_tools.rs`、`selection.rs`、#59995 | `SetSubscription` 任意成员可改任意成员（`api.go:51-54`） | 评审线程能改别的 agent 订阅，越权且污染后续证据 |
| 4 | 模型驱动频道选择（采样 + 原子替换 + rollback disposition） | `guardian-v3/src/selection.rs`、`sampling.rs`、#59999 | 无 | 频道选择只能靠静态规则，证据覆盖面不可自适应 |
| 5 | `DeliveryState` 投递计划（插入序分页、跨频道去重、源顺序、cutoff） | `guardian-v3/src/delivery.rs`、#60000 | 无 | 证据顺序抖动、重复投递、能读到捕获点之后的内容 |
| 6 | 有界原证据投递（整篇读、预算分批、超限拒绝、admission 后记 ordinal） | `guardian-v3/src/delivery.rs`、`message-board/src/local/catalog.rs`、#60001 | 无 | 长证据被截断或撑爆 tool-output 预算；重试重复投递 |
| 7 | 解码后 JSON 字面量搜索（引号/Windows 路径/Unicode 转义） | `message-board/src/local/json_search.rs`、#59994 | `instr(body_search, ?)`，`body_search=foldCase(text)`（`local.go:991`/`:600`） | 结构化参数里的路径搜不到，等于漏证据 |
| 8 | 按请求顺序整篇读取 `read_post_texts`（带 board 边界） | `message-board/src/local/catalog.rs`、#60001 | 无（只有 `ReadPost` 单条 / `ReadThread` 分页） | 投递阶段无法按计划顺序整篇取原件 |
| 9 | Decisions 客户端 + 凭据绑定（隐式 key 校验、凭据源钉住、runtime auth、staging 路由） | `guardian-v2/src/async_scorer/{startup,decisions}.rs`、#59905/#59914/#60064 | 只有开关 `features.go:471` | 一旦启用 Decisions 会带错凭据（含 FedRAMP key） |
| 10 | memories/write 的 startup quota + 线程 `ProviderAuthContext` | `memories/write/src/{start,guard,runtime}.rs`、#60064 | `memories/` 无 quota/runtime auth | 记忆写入的额度检查与账号绑定缺失 |
| 11 | deferred network approval + blocked-request observer | `core/src/tools/network_approval.rs` 等、#59960 | 无（`tool/unified_exec.go:41` 只有输出宽限） | 远程代理场景下命令完成被无谓拖 100ms；迟到拒绝可能丢失 |

## 5. 建议顺序

1. **P1 抽象先行**：`#60002`（`BoardReadView` + `AgentMessageBoard` 扩展）——不先抽象，后面 5 条都要绑死 `LocalAgentMessageBoard`。
2. **P1 取证**：`#59994`（检索工具 + JSON 字面量搜索）→ `#59995`（订阅工具）→ `#59999`（采样选频道）。
3. **P2 投递**：`#60000`（计划）→ `#60001`（执行），两者应与 `EvidenceSnapshot` 的 cutoff 对齐。
4. **P2 凭据/运行时**：`#59905` + `#59914` + `#60064` 一起做（共用凭据选择与 runtime auth），`#59960` 等 remote proxy 落地时补。
5. 另有一条低成本清理：`agentboard/guardian.go:17` 的注释要么跟着 #60002 兑现，要么先改掉。

## 6. 边界与未做的事

- 未改任何 Go 代码，未新增/修改测试。
- 未跑 `go test`：本地镜像只按需取回了 `appserver/`、`agentboard/`、`memories/`、`tool/` 的部分文件。
- `appserver/message_board.go`、`state/` 下的 Guardian 相关实现未逐行通读；判定依据是已取回文件与接口签名，
  若这些文件里有未暴露的捕获视图/投递逻辑，第 1、5、6 项的结论需要复核。
- `#59960` 的"Go 无 deferred network approval"是基于 `tool/unified_exec.go` 与已取回的 `appserver/*.go` 检索；
  `exec/` 包未取回，若那里有实现需重新判定。
