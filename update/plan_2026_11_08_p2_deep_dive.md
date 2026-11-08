# P2 十七条深挖（可开工版）

- 作者：**ledger-align** · 2026-11-08 · 只读分析；不改 Go 代码、不改 `parity/**`、不改既有文件
- 范围：我那份 backlog §2「P2 可观测的 wire / 行为差异」**全部 17 条**，格式沿用 P1（① 复现路径 ② 最小改动面＋第二处消费点 ③ 失败模式 ④ 最小 verifier＋反证 ⑤ 依赖与顺序）
- 数据源：
  - 契约字段（goPaths / rustPaths / verifier）取自 `parity/contracts/manifest.json` 的 135 条快照（`748ab55ca7` 期）
  - Go 锚点：**我本地这份 `748ab55ca7` 期校验副本**（`../gate_tmp`）。⚠️ 与你主干（136 契约 / 104 complete）存在漂移，行号可能偏移；**每条我都给了函数名**，按名字定位即可
  - Rust oracle：真检出 `/home/jacks/jacks_dev/codex`，HEAD `748ab55ca7de0b647d9a7a9d9a8e70e00f28666d`
- 诚实约定：**我核不到的写零命中 + `rg` 模式，不猜**；下文每处「零命中」都附了模式与退出码含义

---

## 1. 顺序与依赖总表

| 序 | contract | #PR | 你的特别关注 | 我的判断 |
|---|---|---|---|---|
| 1 | `account-read-user-id` | #60004 | 构造点 ≥3 处 | 可做，最便宜 |
| 2 | `skills-tool-name-resolution` | #59954 | — | 可做 |
| 3 | `thread-model-metadata-exposure` | #59958 | — | 可做（需新增字段 + 一处 fallback 警告接线） |
| 4 | `code-mode-incremental-exec-definitions` | #60157 | 三条之一 | 必须排第一 |
| 5 | `code-mode-incremental-exec-wiring` | #60163 | 三条之二 | 依赖 §4 |
| 6 | `incremental-exec-message-overrides` | #60188 | 三条之外的文案层 | 与 §4/§5 同批最省 |
| 7 | `side-chat-policy-first-request` | #60067 | — | 可做（安全语义，两点入口） |
| 8 | `multi-agent-hosted-tools-guidance` | #60011 | — | 可做（纯文案，但需门控正确） |
| 9 | `typed-prompt-markdown-render` | #59983 | — | 可做，但会碰既有用例 |
| 10 | `skill-mention-punctuation-join` | #60180 | — | 可做（两条路径共用） |
| 11 | `cloud-thread-project-binding` | #60007 | — | 可做（协议字段 + 归一化） |
| 12 | `cloud-image-messages-and-draft-handoff` | #60061 | — | 部分可做（图片；draft handoff 需 lane 确认） |
| 13 | `provider-creation-during-signin` | #60186 | — | 可做 |
| 14 | `tui-custom-gateway-signin` | #60009 | — | 可做，但与 §13 共用写入口 |
| 15 | `analytics-runtime-auth` | #60101 | — | 可做（错误分类一层） |
| 16 | `agent-tree-id` | #60190 | — | **建议保持 partial**（见 §16、§19） |
| 17 | `release-prerelease-cleanup` | #59887/#59903 | 转 complete 条件 | **保持 partial**，直到真跑过 workflow |

---

## 2. `account-read-user-id` — #60004 `01ab354312`

### ① 复现路径

```go
// auth/account.go:565  结构体只有三个字段，没有 userId
type GetAccountResponse struct {
	Account            *Account           `json:"account"`
	RequiresOpenAIAuth bool               `json:"requiresOpenaiAuth"`
	WorkspaceRouting   *WorkspaceRouting  `json:"workspaceRouting"`
}
```
取值函数**已存在**：`auth/account.go:190 ChatGPTUserIDFromAuth(snapshot)`（Rust `get_chatgpt_user_id` 的等价物）。

复现：`account/read`（本地 ChatGPT 登录 + workspace routing 已发现）→ 客户端拿到 `account` + `workspaceRouting`，但**没有任何 `userId`**，只能自己再走 `auth/status` 拼身份；经 app-server 连进来的客户端更糟（拿不到本地 `auth.json`）。

### ② 最小改动面 + 第二处消费点

| 位置 | 函数 | 改动 |
|---|---|---|
| `auth/account.go:565` | `GetAccountResponse` | 新增 `ChatGPTUserID *string \`json:"chatgptUserId,omitempty"\``（Rust 侧字段名请以 `app-server-protocol/src/protocol/v2/account.rs` 为准） |
| `auth/account.go:1114` | `AccountManager.GetAccount` | 从 `ApplyAuthSnapshot` 保存的 snapshot 填 userID（此处只填「与当前 workspace 匹配」的那种） |
| `appserver/runtime_router.go:14994` | `getAccount` 的 `RequiresOpenAIAuth=false` 提前返回 | **必须**同样填 userID，否则 API-key/provider 分支永远没有该字段 |
| `appserver/runtime_router.go:15004` | refresh-failure 提前返回（`RequiresOpenAIAuth=true`） | 这里通常无身份，**保持不填**（并写注释说明为什么） |
| `appserver/runtime_router.go:15038` | `providerAccountResponse` 的返回构造 | provider 分支填 userID（若 `AccountState` 能给出） |

**第二处消费点 = 4 个构造点 + 1 个别名函数**（这正是你提醒的坑）：`AccountManager.GetAccount`、两个 `RequiresOpenAIAuth` 提前返回、`providerAccountResponse`，以及 `authAccountFromProviderAccount`（`runtime_router.go:15050`，只造 `Account`、不造外层）。少填任何一处，客户端就会遇到「有时有 userId、有时没有」的分裂行为。

### ③ 失败模式

- **把 `account_id` 当 `user_id`**：两者都来自 tokens，串了会导致客户端把两个用户的会话混在一起（Rust 专门有 `get_chatgpt_user_id` 与 `get_account_id` 两个入口，就是为了不混）。
- **只改 `GetAccount`、不改 provider 分支**：API-key provider 用户拿到空字段，客户端把「无字段」误读成「无身份」。
- **在 refresh-failure 分支也填**：那里 snapshot 已失效，填出来的是过期身份。

### ④ 最小 verifier

- `TestAccountReadResponseCarriesChatGPTUserIDLikeRust`
- 断言：①ChatGPT 登录 + 匹配 workspace → 字段存在且等于 `ChatGPTUserIDFromAuth`；②workspace 不匹配 → 字段**不存在**（与既有 `TestChatGPTAccountUserIDRequiresMatchingWorkspaceLikeRust` 的口径一致）；③provider/API-key 分支 → 字段按 provider 语义（有则填、无则省略）；④`requiresOpenaiAuth=true` 的失败分支 → 省略。
- 反证：删掉 `runtime_router.go:14994` 那处的赋值 → 子用例 ③ 变红；把 `user_id` 换成 `account_id` → 子用例 ① 变红。

### ⑤ 依赖与顺序

独立，**建议第一批做**（纯 wire 字段 + 4 处构造，成本最低、收益立刻可见）。与 §3（thread model metadata）无耦合。

---

## 3. `skills-tool-name-resolution` — #59954 `0768988e55`

### ① 复现路径

```go
// turn/skills_tools.go:988  别名匹配只覆盖 package id / 根别名
func skillPackageMatchesAlias(entries []skillprovider.CatalogEntry, packageID string, candidate string) bool
```
读路径：`turn/skills_tools.go:592 skillReadAuthorityForSource`、`:602 skillReadAuthorityForPackage`；列表/安装位置入口在 `:180`（`skills.list`）与 `:189`（`skills.installations`）。

复现：`skills.list` 返回的显示名（display name / 被截断的长名）→ 直接粘到 `skills.read` 的 `package` → `source_unavailable`。

### ② 最小改动面 + 第二处消费点

| 位置 | 函数 | 改动 |
|---|---|---|
| `turn/skills_tools.go:988` | `skillPackageMatchesAlias` | 增加「显示名」候选：优先精确 package，其次根别名，最后显示名；显示名匹配必须**唯一**，多解返回歧义错误而不是任取 |
| `turn/skills_tools.go:602` | `skillReadAuthorityForPackage` | 传入完整 catalog（现在只在有 source 时才查 entries） |

**第二处消费点 = 3 处**：①`skills.installations`（`:189`，它也必须用同一个解析函数，否则「read 能解析、installations 不能」）；②`skills.list` 的显示名生成处（显示名与解析必须同源，否则永远对不上）；③`turn/skills_tools.go:675` 的游标/分页路径（解析后要保证 cursor 仍按 package id 定位，不能按显示名）。

### ③ 失败模式

- **子串/大小写放宽**：`skill foo` 会匹配到 `skill foo bar`，读错文件。
- **显示名多解时任取第一个**：两个同名 skill（不同 source）会被静默选错——必须报歧义。
- **只改 read 不改 installations**：用户按提示「pass its provided package directly」操作后仍然是失败。

### ④ 最小 verifier

- `TestSkillsReadResolvesListedDisplayNameLikeRust`
- 断言：①`skills.list` 的每个显示名都能直接喂给 `skills.read` 并成功；②截断显示名同样成功；③同名多解 → 明确错误（含歧义提示），不返回任一结果；④`skills.installations` 用同一显示名也能定位；⑤既有 `TestSkillsReadAliasResolutionAndExecutorSkillRoot`、`TestSkillsInstallationsListsAndFiltersDiscoveryV2Sources` 保持绿。
- 反证：把 `skillPackageMatchesAlias` 的显示名分支删掉 → 断言 ① 变红；把唯一性检查去掉 → 断言 ③ 变红。

### ⑤ 依赖与顺序

独立。**与 §10（mention 标点）无关**（那条在 TUI 输入层，这条在工具执行层）。建议第 2 批。

---

## 4. `thread-model-metadata-exposure` — #59958 `12dc86beb3`

### ① 复现路径

```
$ rg -n --glob '*.go' "selected_model_info|SelectedModelInfo|models_manager_for_startup" .
→ exit=1，0 命中
```
所以 thread 无法对外说明「这个 thread 用自己的 catalog 选中了哪个模型」：`turn/start` 的 fallback 警告（现有 `TestTurnStartWarnsForFallbackModelMetadataLikeRust` 锁的那条）只能靠猜，客户端也无法解释「为什么用的是这个模型」。

复现：thread 用 account-scoped catalog 选中模型 A，随后账号 catalog 刷新（A 消失）→ `turn/start` 走 fallback 到 B 并 warn，但响应里没有「thread 选中的是 A」这一信息 → 客户端只能显示 B，用户以为是配置错了。

### ② 最小改动面 + 第二处消费点

| 位置 | 函数 | 改动 |
|---|---|---|
| `appserver/`（thread 快照/响应构造点） | 新增 `SelectedModelInfo`（字段名对齐 `core/src/thread_manager.rs` / `codex_thread.rs`） | 让 thread 暴露自己 catalog 的选中项 |
| `appserver/turn_runtime.go` 的 fallback 警告路径 | 在 warn 里带上「thread 选中项 vs 请求项」 | 让 fallback 可解释 |
| `model/` 侧 | 从 thread 的 `ModelsManager` 取 `GetDefaultModel(..., RefreshInMemory)` | 必须用 in-memory（与 §`child-agent-catalog-preservation` 同口径），否则读到磁盘旧目录 |

**第二处消费点 = 2 处**：①`appserver/runtime_router.go` 里 thread 启动/恢复响应的构造（不止 turn/start 一处）；②客户端 picker 数据源（若 picker 直接读 catalog 而 thread 读 `SelectedModelInfo`，两者会不一致）。

### ③ 失败模式

- **用当前 catalog 现算**而不是用 thread 捕获的选中项：catalog 刷新后 `SelectedModelInfo` 自己就变了，等于没记录。
- **在 fallback 分支覆盖 `SelectedModelInfo`**：把「thread 想要 A」改成「实际用 B」，警告失去意义。

### ④ 最小 verifier

- `TestThreadSelectedModelInfoUsesThreadCatalogLikeRust`
- 断言：①选中 A → 响应里是 A（不是当前默认）；②catalog 刷新后 A 消失 → `SelectedModelInfo` 仍是 A，同时 turn/start 的 warn 指出「A 不可用，回退 B」；③恢复 thread（冷加载）后仍能读出 A。
- 反证：把实现改成「读当前 catalog 默认」→ 断言 ② 变红。

### ⑤ 依赖与顺序

依赖 catalog 的 in-memory 语义（`child-agent-catalog-preservation` 已 complete，可直接复用 `RefreshInMemory` 口径）。第 2 批。

> 说明：下文 `§N` 与上表「序」的对应关系是 `序 i → §(i+1)`（§1 是总表）。

---

## 5. `code-mode-incremental-exec-definitions` — #60157 `2bc8e7f545`（三条之一，必须第一）

### ① 复现路径

```
$ rg -n --glob '*.go' "incremental_exec_tools" .
features/features.go:236:	{Key: "incremental_exec_tools", Stage: StageUnderDevelopment, DefaultEnabled: false},

$ rg -n --glob '*.go' "ExecDefinition" .
→ exit=1，0 命中
```
即：feature 只被**注册**，没有任何结构化定义；`functions.exec` 在顶层 tools 里只是「一个 hash key」，所以 exec 工具集变化只能整块重发。

Rust 对照：`core/src/context/world_state/top_level_tools.rs:57`
```rust
/// Diffable entries for the current CodeModeOnly catalog; absent in other tool modes.
exec_definitions: Option<Arc<Vec<ExecDefinition>>>,
```
`ExecDefinition` 定义在 `code-mode-protocol/src/description.rs`。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| 新文件 `codemode/exec_definition.go` | 定义 `ExecDefinition{Key, Hash, Description, …}` 与「从 exec 目录生成有序 entries」的函数（对齐 `code-mode-protocol/src/description.rs`） |
| `session/world_state.go`（现有 `session/world_state.go` + `typed_world_state_test.go`） | 快照增加 `Exec []Entry`（Rust `TopLevelToolsSnapshot::WithExec`） |
| `features/features.go:236` | 保持注册不动 |

**第二处消费点 = 3 处**：①`TopLevelToolsSnapshot` 的序列化/反序列化（Rust 有 `#[serde(untagged)]` 的 `WithExec`/`ToolsOnly` 两形，Go 侧要能同时读旧快照）；②`functions.exec` 顶层 declaration 的「冻结」逻辑（定义变更时不能重渲染声明，只能发 entries）；③非 CodeMode 工具模式必须保持 `exec_definitions == nil`（否则普通模式也会开始按 entry 比较）。

### ③ 失败模式

- **entry key 不稳定**（例如用 index 或含随机序）：每次比较都全量 changed，增量变成全量，指标也会爆。
- **把 exec 声明本身也放进 entries**：会出现「声明 + entries」双重下发，客户端解析顺序不确定。
- **非 CodeMode 模式也生成 entries**：普通模式下 `previous_exec` 恒 nil，会走 Rust 的「安装完整目录」分支，行为从「整块重发」变成「每轮重发完整目录 + 增量」，回归。

### ④ 最小 verifier

- `TestIncrementalExecDefinitionsCarryStructuredEntriesLikeRust`
- 断言：①CodeModeOnly 下 entries 数量与 exec 目录项一致且顺序稳定（两次生成逐字节相同）；②增删改任一 entry 只让该 entry 的 hash 变化；③非 CodeMode 模式 entries 为 nil；④既有 `TestTopLevelToolsNamespaceTransitionsLikeRust`、`TestIncrementalExecToolsFeatureRegisteredLikeRust` 保持绿。
- 反证：把 `Key` 改成按 index 生成 → 断言 ①（顺序稳定性）与 ② 变红。

### ⑤ 依赖与顺序

**本组第一条**，必须先于 §6、§7 之后才有意义的 §`#60177` 指标（见 §7 结尾）。

---

## 6. `code-mode-incremental-exec-wiring` — #60163 `33b3f80a09`（三条之二）

### ① 复现路径

```
$ rg -n --glob '*.go' "incremental_exec_tools" . | grep -v _test
features/features.go:236   ← 只有注册，没有任何消费点
```
也就是**死开关**：打开后没有代码路径用它，等于没实现。

Rust 对照（rustPaths）：`core/src/tools/router.rs`、`core/src/tools/spec_plan.rs`、`core/src/session/world_state.rs`。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `session/world_state.go` | 比较 `previous_exec` 与当前 entries，产出 changed 列表（对齐 `top_level_tools.rs:383-392`） |
| `session/`（工具规格生成，对应 Rust `tools/spec_plan.rs`） | 打开 feature 时把 exec 目录转成 entries 并交给 world state |
| `appserver/`（session 工具快照下发点） | 把增量 fragment 拼进下一轮请求（对齐 Rust `WorldStateUpdate::fragment(...).standalone()`） |

**第二处消费点 = 3 处**：①feature 关闭时**必须**走旧的整块重发路径（Rust 的 `else if previous.is_some()` 分支，`top_level_tools.rs:420-430`）；②`runtime_agent_instances` 一类「每轮重建 session」的路径要能复用快照；③retry/compaction 路径（compaction 会重置 world state，需确认不会把 entries 当成 changed 全量发一遍）。

### ③ 失败模式

- **在 feature 关闭时也走 compare**：把「整块重发」改成「按 entry 比较」，首次请求会只发零星 entries，模型看到的工具集不完整（最严重）。
- **compaction/retry 后 entries 基线丢失**：每次重试都全量 changed，指标与字节数翻倍。
- **只接 CodeMode、漏接 `functions.exec` 的单顶层 key 情形**：出现「顶层 hash 变了但 entries 没变」的静默不一致。

### ④ 最小 verifier

- `TestIncrementalExecUpdatesReachSessionToolSpecLikeRust`
- 断言：①feature 开：改一个 entry → 下一轮请求只含该 entry 的增量 + hint；②feature 关：同一改动 → 整块重发（与现状逐字节一致）；③compaction 后首轮 → 完整目录而非增量；④既有 `TestIncrementalToolCatalogAppendsChangedCatalogLikeRust`、`TestIncrementalToolCatalogRecordsWindowStartLikeRust` 保持绿。
- 反证：把 feature 门控删掉（恒走 compare）→ 断言 ② 变红。

### ⑤ 依赖与顺序

**必须晚于 §5**（没有 entries 就没有 compare 的输入），**早于 §7 的指标**。

---

## 7. `incremental-exec-message-overrides` — #60188 `c3936b4b8a`（文案层）

### ① 复现路径

```
$ rg -n --glob '*.go' "exec_tools_update_hint|ExecToolsUpdateHint|exec_tools_removed_definitions_header"
→ exit=1，0 命中
```
Go 的 override 表只有通用字段，缺 exec 专属的两条；增量更新时模型看到的提示文案与 Rust 不同（Rust 是 `prompts/src/model_messages/incremental_tools.rs` + `protocol/src/openai_models/incremental_tools.rs`）。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `prompt/`（模型消息 override 表） | 增加 `exec_tools_update_hint`、`exec_tools_removed_definitions_header` 两个字段与默认文案 |
| `session/world_state.go` 的渲染调用 | 用 override 后的 hint 渲染（Rust `render_exec_update(&changed, &self.messages.exec_tools_update_hint)`，`top_level_tools.rs:396`） |

**第二处消费点 = 2 处**：①`removed` 分支（Rust `top_level_tools.rs:453` 附近）也要用第二个 override；②资源/文案快照（若项目有 prompt 快照用例，需要同步 `TestTopLevelToolsCatalogMessageOverridesLikeRust` 的期望）。

### ③ 失败模式

- **只加 hint 不加 removed header**：删除 exec entry 时的文案仍与 Rust 不同（半对齐）。
- **override 为空时回退成空串**（而非默认文案）：提示丢字。

### ④ 最小 verifier

- `TestIncrementalExecMessageOverridesRenderLikeRust`
- 断言：①增改 entry → 渲染串里出现 override 的 hint 原文；②删 entry → 出现 removed header 原文；③override 未配置 → 用默认文案（非空）。
- 反证：把 hint 的 override 读取去掉（用硬编码默认）→ 断言 ① 变红。

### ⑤ 依赖与顺序：**为什么指标那条必须排在后面**

三条（+指标）我建议的顺序是：

```
§5 definitions  →  §6 wiring  →  §7 overrides  →  #60177 metrics
```

**理由能落到具体行**（Rust `748ab55ca7`）：

1. 指标计数的是「**changed entries**」，而 changed 由 `previous_exec` 与当前 `exec` 的 **entry 级 diff** 得出：
   `top_level_tools.rs:383-392`
   ```rust
   let changed = entries.iter().filter(|entry| previous_exec.and_then(|p| p.get(&entry.key)) != exec.get(&entry.key)).collect::<Vec<_>>();
   ```
   没有 §5 的 `ExecDefinition` 与 §6 的 diff 接线，`changed` 根本不存在，指标无输入。
2. 指标口径明确要求「**每次都按 entry 换算是 `schema_changed`**」：
   `otel/src/metrics/names.rs:115` — "schema_changed includes any definition change, such as description edits"
   `otel/src/metrics/names.rs:125` — "Excludes full declarations; schema_changed includes any entry replacement"
   这条口径只有在 entry 粒度存在后才可表达；在此之前只能统计「整块是否变化」（`TOOL_INCREMENTAL_UPDATES_METRIC` 的那一面）。
3. **完整目录下发不计入字节数**这条反证也依赖 wiring：`top_level_tools.rs:420-430` 的 `else if previous.is_some()` 分支（安装完整目录）**不发** `EXEC_TOOL_UPDATE_BYTES_METRIC`；若先做指标再做 wiring，就没有这条分支可断言，口径会写成错的。
4. 文案（§7）在指标之前是因为 `EXEC_TOOL_UPDATE_BYTES_METRIC` 计的是**渲染后**的字节（"UTF-8 bytes per rendered added/changed exec batch, including hint and framing"，`names.rs:127-129`）——hint 文案未定，字节口径不稳定。

---

## 8. `side-chat-policy-first-request` — #60067 `0ed34eb9ce`

### ① 复现路径

```go
// app/remote_tui.go:6149
developerInstructions := codextea.SideDeveloperInstructions("")
```
对比同仓库另一条入口：
```go
// app/side_tui.go:59
developerInstructions := codextea.SideDeveloperInstructions(existingInstructions)
```
`tui/tea/side.go:476` 与 `tui/app/side.go:189` 是同一函数的两份实现（两个 TUI 包）。

复现：远程侧聊（remote_tui 路径）+ 父 turn 处于「继承历史被压制」状态 → 首个模型请求里**没有侧聊策略**（只有 boundary 提示），侧聊可能接着父任务继续干。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `app/remote_tui.go:6149` | 传入真实 `existingInstructions`（与 `side_tui.go:59` 同源），或统一走一个 `sideDeveloperInstructionsForThread(threadID)` |

**第二处消费点 = 3 处**（这条最容易只改一处）：
1. `app/side_tui.go:59`（另一条入口，现状是对的——**别把它改坏**）；
2. `tui/tea/side.go:476` 与 `tui/app/side.go:189` 两份 `SideDeveloperInstructions` 实现必须行为一致（现在一份是 trim + 拼接，需确认另一份相同）；
3. boundary 提示注入点（`tui/tea/side.go:484 SideBoundaryPromptItem`）——策略与 boundary 的先后顺序要与 Rust `tui/src/app/side.rs` 一致。

### ③ 失败模式

- **只改 remote_tui**：TUI 内与远程侧聊行为继续分叉（这类「两入口」正是上一轮 §3 踩过的坑）。
- **把父策略整段塞进子请求**：重复注入，模型看到两遍侧聊策略。
- **在压制状态下只发 boundary**：等于没修（压制的正是继承历史，策略不能依赖它）。

### ④ 最小 verifier

- `TestSideChatPolicyReachesFirstRequestUnderInheritedSuppressionLikeRust`
- 断言：①压制状态下首个请求的 developer instructions **包含**侧聊策略原文；②boundary 提示仍在且顺序正确；③remote_tui 与 side_tui 两条入口产出的指令串**逐字节相同**；④既有 `TestSideDeveloperInstructionsAppendsExistingPolicyMatchRust`、`TestSideBoundaryPromptMarksInheritedHistoryReferenceOnlyMatchRust` 保持绿。
- 反证：把 `remote_tui.go:6149` 改回 `""` → 断言 ①③ 变红。

### ⑤ 依赖与顺序

独立（安全语义，成本低）。建议排在 code-mode 三条之后、其余 wire 条目之前。

---

## 9. `multi-agent-hosted-tools-guidance` — #60011 `e0aa0434f4`

### ① 复现路径

```
$ rg -n --glob '*.go' "hosted tool|HostedTool" agent/
→ exit=1，0 命中
```
复现：多 agent（collab）模式 + 关闭 native tools → 指引导语里没有「用 hosted 工具替代」这一段，模型会反复尝试不可用工具，表现为一轮里多次无效调用。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `agent/`（多 agent 指令拼装点） | 按 Rust `core/src/context/world_state/multi_agent_mode.rs` 的门控加一段 hosted-tools 指引 |
| `appserver/`（指令透传到 turn 的路径） | 确保该段与 existing developer instructions 的拼接顺序与 Rust 一致 |

**第二处消费点 = 2 处**：①`core/src/session/multi_agents.rs` 对应的 Go 侧（collab 工具门控 `TestCollabToolsGateMatchesRustLikeRust` 那条路径）；②node_repl/子 agent 的指令继承（若子 agent 也关 native tools，需要同一段）。

### ③ 失败模式

- **在 native tools 开启时也注入**：模型被要求用不存在的 hosted 工具。
- **只在根 agent 注入**：子 agent 仍反复试 native tools。

### ④ 最小 verifier

- `TestMultiAgentGuidanceNamesHostedToolsLikeRust`
- 断言：①collab 开 + native tools 关 → 指令含 hosted 工具名与「替代」语义；②native tools 开 → 不含；③子 agent 指令同源。
- 反证：删掉门控条件（恒注入）→ 断言 ② 变红。

### ⑤ 依赖与顺序

独立、纯文案，但门控条件要抄准。第 3 批。

---

## 10. `typed-prompt-markdown-render` — #59983 `78b1dda5f6`

### ① 复现路径

```go
// tui/history_cell/messages.go:138
UserMessagePresentationPlain UserMessagePresentation = iota   // 138
UserMessagePresentationSpoken                                  // 139
// :171 DisplayLines：按 '\n' 切行 + WrapLine，纯文本
func (c UserHistoryCell) DisplayLines(width int) []string
// :158 NewUserPrompt(...) 固定 Presentation: UserMessagePresentationPlain
```
复现：用户键入 `**bold** [x](url) $skill` → 历史 cell 原样显示标记符号；mention 高亮只做字符串级替换，没有 span/链接列。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `tui/history_cell/messages.go:171` | `DisplayLines` 对 Plain（typed）走 markdown 渲染（Rust `tui/src/markdown_render.rs`），Spoken 保持现状 |
| `tui/history_cell/messages.go:158` | 新增构造参数承载「渲染模式」，别改 `Presentation` 的既有语义（Speech 要保留红 marker） |

**第二处消费点 = 3 处**：①mention/链接列（Rust 有独立 gutter 列，Go 侧 `tui/history_cell/` 需补 span 列）；②`TestUserMessagePresentationLikeRust` 与 `TestUserAndAgentMessageCells` 需同步改写（否则会假红）；③`RawLines()`（`:432`，复制/导出路径必须仍给原始文本，不能给渲染后的 ANSI）。

### ③ 失败模式

- **把 Spoken 也渲染成 markdown**：语音转录里的 `*` 被当强调，红 marker 也被吃掉。
- **`RawLines` 跟着变**：复制到剪贴板的内容带上 ANSI/丢失 markdown 源文本。
- **不补 gutter 列直接渲染**：多行 markdown 与行号/mention 列错位。

### ④ 最小 verifier

- `TestTypedUserPromptRendersMarkdownLikeRust` + `TestUserPromptMarkdownKeepsGutterAndLinkColumnsLikeRust`
- 断言：①typed prompt 的 `**bold**` 渲染为样式而非字面星号；②链接产生可点击 span；③Spoken 不变（红 marker 仍在）；④`RawLines` 仍是原文；⑤既有两条用例（改写后）绿。
- 反证：把 `DisplayLines` 的 Plain 分支改回纯文本 → 断言 ①② 变红。

### ⑤ 依赖与顺序

独立，但**会动既有用例**（需要同步改期望，属「有回归风险」的一类）。建议排在文案/字段类之后，单独一批。

---

## 11. `skill-mention-punctuation-join` — #60180 `1455fda735`

### ① 复现路径

```go
// tui/mention_codec.go
:200 ParseLinkedToolMention(text, start, sigil) (id, sigilStr string, end int, ok bool)
:36  EncodeHistoryMentions(...)        // 历史回放编码
:44  EncodeHistoryMentionsAtElements(...)  // 带 TextElement 的编码
:183 parseHistoryLinkedMention(...)    // 解码
```
两条路径各自处理标点：**完成态 mention（composer 完成后拼接）**与**paste 路径**（`tui/src/bottom_pane/chat_composer/paste_input.rs` 的等价物在 Go 侧是 composer 的 paste 处理）。`$skill,` 这类输入在两条路径上得到不同的 mention 边界。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `tui/mention_codec.go:200` | 抽出「mention 结束边界」判定（含尾随标点）为单一函数 |
| composer 完成态拼接处（`tui/` 下 composer 相关文件） | 用同一个边界函数决定是否把标点并入 mention |
| paste 路径 | 同上，不得各写一套 |

**第二处消费点 = 3 处**：①`EncodeHistoryMentionsAtElements`（按 TextElement 编码，标点归属会改变 byte offset）；②`parseHistoryLinkedMention`（解码必须能还原同一 offset）；③`TestParseLinkedToolMentionPreservesByteOffsetsAndTrimsPathsLikeRust` 锁着的 byte offset 契约。

### ③ 失败模式

- **把标点并进 mention id**：`$skill,` 解析成 id `skill,`，工具名找不到。
- **两条路径各改一半**：历史回放与实时输入显示不一致（同一段文本渲染成两种）。
- **破坏 byte offset**：mention 高亮错位一个字符（TUI 里非常显眼）。

### ④ 最小 verifier

- `TestCompletedSkillMentionJoinsTypedPunctuationLikeRust`
- 断言：①`$skill,` 在完成态与 paste 路径产出**同一** mention 边界；②提交后的文本与渲染后的 cell 一致（往返）；③byte offset 与既有用例一致；④既有 `TestParseLinkedToolMentionPreservesByteOffsetsAndTrimsPathsLikeRust` 保持绿。
- 反证：让 paste 路径继续用旧边界函数 → 断言 ① 变红。

### ⑤ 依赖与顺序

独立；与 §10 同属 TUI 渲染面，**建议同批做**以减少 `recordreplay` 快照的重复更新。

---

## 12. `cloud-thread-project-binding` — #60007 `7de8a0afc0`

### ① 复现路径

```go
// cloudclient/prewarm.go:47  请求只编码 model / model_provider
func encodePrewarmRequest(model string, modelProvider string) []byte
// cloudclient/prewarm.go:15
func (c *Client) PrewarmDefaultThread(ctx context.Context, model string, modelProvider string) (*ThreadActivation, error)
```
即：cloud thread 启动不带本地 GitHub 项目——没有 ProjectEnvironment、没有 origin→GitHub 归一化，云端拿不到仓库上下文。

Rust 对照（rustPaths）：`cloud-client/src/prewarm.rs`（ProjectEnvironment）、`protocol/src/sanitized_git_url.rs`（origin 归一化）。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `cloudclient/prewarm.go:15/47` | 签名与 protobuf 编码增加 `ProjectEnvironment`（仓库 owner/name + 默认分支） |
| 新增 `protocol/`（或 `state/`）侧归一化：origin URL → GitHub owner/name | 对齐 `sanitized_git_url.rs`（去 `.git`、大小写、SSH→HTTPS 收敛） |

**第二处消费点 = 3 处**：①`cloudclient/activation.go` 的响应解码（Rust 侧 project environment 可能回填）；②`cloudbackend/requests.go` 的 thread 创建请求（同一环境信息要一致）；③`state` 侧的 workspace roots 来源（多 root 时取哪个必须是 Rust 的规则，不能任选）。

### ③ 失败模式

- **归一化不收敛**（`git@github.com:a/b.git` 与 `https://github.com/a/b` 被当成两个项目）：同一仓库被绑定成两个 cloud 项目。
- **多 workspace root 时任取第一个**：绑定到错误的仓库。
- **只在 prewarm 加、thread 创建不加**：云端创建时又丢项目。

### ④ 最小 verifier

- `TestPrewarmSendsLocalGitHubProjectLikeRust`
- 断言：①有 GitHub origin → 请求含 ProjectEnvironment(owner/name/branch)；②SSH 与 HTTPS 两种 origin 归一化成同一值；③非 GitHub origin（gitlab/本地路径）→ 不发该字段；④多 root → 按 Rust 规则选；⑤既有 `TestPrewarmRequestUsesManagedEnvironmentAndOptionalModelLikeRust` 保持绿。
- 反证：把归一化里的 `.git` 去除删掉 → 断言 ② 变红。

### ⑤ 依赖与顺序

独立。协议字段变更 → 建议与 §13 同批（两者都改 cloud 请求编码）。

---

## 13. `cloud-image-messages-and-draft-handoff` — #60061 `e6bf88ae02`

### ① 复现路径

```go
// cloudbackend/submission.go:90
return cloudclient.AddUserMessageRequest{}, rejectCloudInput("Cloud conversations currently accept plain text only.")
// :82 结构化输入直接拒
return ..., rejectCloudInput("Changing cloud settings or sending structured input is not supported yet.")
```
复现：Cloud 会话里发图片 → 被一句**逐字保留自 Rust 改前文案**的错误挡住（`"Cloud conversations currently accept plain text only."`）。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `cloudbackend/submission.go:90` | 允许 image part（对齐 Rust `cloud-client/src/items.rs`），保留文案常量给真正的「不支持」分支 |
| `cloudbackend/submission.go:82` | 区分「结构化输入（settings）」与「image part」——后者不应落这条拒绝 |

**第二处消费点 = 3 处**：①`cloudclient/` 的 AddUserMessageRequest 编码（images 数组）；②`cloudclient/` 的 typed receipt 解码（Rust 同批改）；③**draft handoff**：`rg -n "draft" cloudclient/ cloudbackend/ --glob '*.go'` → **exit=1，0 命中**（生产代码），所以「可编辑 draft 交接」在 Go 侧**完全不存在**，需要 lane 侧确认是否属本契约范围还是另一条（我倾向于它应单列，见 §19）。

### ③ 失败模式

- **把 image part 塞进 text 字段**（base64 进正文）：云端收到超长文本，模型侧直接失败。
- **删掉旧文案常量**：仍在「不支持」的分支上用到它，删了会改成泛化错误（客户端文案回归）。
- **draft handoff 硬凑**：没有 Rust 的 draft 协议形状（这是跨端交互），硬写会出现「Go 有、Rust 无」的反向差异。

### ④ 最小 verifier

- `TestCloudSubmissionCarriesImagePartsLikeRust`
- 断言：①图片输入 → 请求含 image part（不是文本里内嵌 base64）；②纯文本 → 请求形状与现状逐字节一致；③仍不支持的输入（settings 变更）→ 仍是那句原有文案；④既有 `TestAddUserMessageRequestEncoding`、`TestCloudConversationRequestsAndTypedReceiptsLikeRust` 保持绿。
- 反证：把 image 分支删掉（回到全局拒绝）→ 断言 ① 变红。

### ⑤ 依赖与顺序

图片部分与 §12 同批；**draft handoff 我建议单列**（见 §19 的「应保持 partial」）。

---

## 14. `provider-creation-during-signin` — #60186 `d960b36694`

### ① 复现路径

```go
// app/interactive_onboarding.go:126（注释原文）
// explicitly or left implicit. A custom provider stays excluded even when it
```
即登录流程里 custom provider 被**明确排除**：用户必须先登录、再回头改配置才能用自定义 provider。

Rust 对照（rustPaths）：`tui/src/app/provider.rs`、`tui/src/app/provider/write.rs`、`app-server/src/config_manager_service_provider_write.rs`。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `app/interactive_onboarding.go:126` 附近的分支 | 允许在登录流程里写入 custom provider（走与 /provider 相同的 write 校验） |
| `appserver/` 的 provider write 服务 | 确保登录态下同样可用（现在 `TestModelProviderConfigWrite*` 覆盖的是非登录路径） |

**第二处消费点 = 2 处**：①`tui/` 的 `/provider` 交互（写入口必须与 onboarding 共用一套校验）；②配置层 `featureSettings`/profile 选中（自定义 provider 写入后必须能被「当前 provider」解析，否则写进去也选不中）。

### ③ 失败模式

- **绕过 write 校验**：登录流程里写进非法 gateway 配置，比「不能用」更糟。
- **写完不切换当前 provider**：用户看到「已创建」但请求仍走默认。

### ④ 最小 verifier

- `TestSignInFlowCreatesCustomProviderLikeRust`
- 断言：①登录流程里创建 custom provider → 配置文件出现该 provider 且校验通过；②非法配置 → 与 /provider 路径**同样的**错误；③创建后当前 provider 指向它；④既有 `TestModelProviderConfigWriteCreatesAndPatchesGatewaysLikeRust`、`TestModelProviderConfigWriteValidatesCustomGatewaysLikeRust` 保持绿。
- 反证：把 onboarding 的排除分支恢复 → 断言 ① 变红。

### ⑤ 依赖与顺序

与 §15 共用写入口，**建议两者同批**（先 §14 再 §15，或反过来，但不要分两批交付）。

---

## 15. `tui-custom-gateway-signin` — #60009 `2497eab251`

### ① 复现路径

```
$ rg -n --glob '*.go' "gateway_setup_wizard|GatewaySetupWizard" .
→ exit=1，0 命中
```
即 TUI 登录**没有** gateway 向导（两条入口都没有）：只能选内置 provider，gateway 用户必须手工改配置。

Rust 对照（rustPaths）：`tui/src/onboarding/auth.rs`、`tui/src/app/provider.rs`、`tui/src/startup_orchestration.rs`。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `tui/`（onboarding/auth 流程） | 增加「自定义 gateway」选项与其表单校验（base URL / 认证方式） |
| `tui/`（provider 写入） | 复用 §14 的 write 路径 |

**第二处消费点 = 2 处**：①`startup_orchestration` 等价物（首次启动无凭据时的入口，与显式登录入口是两条）；②`app/` 侧的非 TUI 登录（`interactive_onboarding`），两条必须给出一致的 gateway 支持面。

### ③ 失败模式

- **向导自己拼配置写盘**（不复用 write 校验）：出现只有向导能写出的非法配置。
- **只在一条入口加**：`codex` 首次启动仍然没向导。

### ④ 最小 verifier

- `TestGatewaySetupWizardWritesProviderLikeRust`
- 断言：①向导选 gateway + 合法参数 → 落盘配置与 /provider 写入结果一致；②非法 base URL → 明确错误且不落盘；③两条入口都能进入向导；④既有 `TestConfigPanelProviderDefaultSelectionLikeRust`、`TestModelProviderConfigWriteCreatesAndPatchesGatewaysLikeRust` 保持绿。
- 反证：把向导的写盘改成直接 `os.WriteFile`（绕过校验）→ 断言 ② 变红。

### ⑤ 依赖与顺序

**与 §14 同批**（共用写入口与校验）。这组是 TUI 交互面，建议排在所有 wire/协议类之后。

---

## 16. `analytics-runtime-auth` — #60101 `cce9d3228b`

### ① 复现路径

```go
// tui/analytics/client.go:268
func isUnauthorizedAnalyticsError(err error) bool {
	var statusErr *chatgptapi.HTTPStatusError
	return errors.As(err, &statusErr) && statusErr.IsStatus(http.StatusUnauthorized)
}
```
只认「HTTP 401」这一种形状；runtime 侧返回的是**typed 错误**（`AuthError::Policy` / denial code），因此不会被映射成「Sign in again」。

两个消费点：`:118`（plan 加载失败）与 `:255`（reports 请求失败时的 recovery）。文案在 `:275 analyticsRequestErrorMessage`。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `tui/analytics/client.go:268` | 增加 typed runtime denial 的判定（复用 P1 §2 的 `isRuntimeAuthDenial` 谓词），命中即视为 sign-in |
| `tui/analytics/client.go:275` | 文案分支同步（denial → sign-in 文案，而不是 generic） |

**第二处消费点 = 3 处**：①`:118` 与 `:255` 两个调用点（一个是 plan、一个是 reports，路径不同）；②`chatgptapi.HTTPStatusError` 的构造处（若 runtime 解析失败根本没走到 HTTP，错误类型不同）；③`tui/analytics/client.go:223` 的 runtime provider 装配（`runtimeauth.ProviderForHarness`）——分类必须与之一致。

### ③ 失败模式

- **把任何 401 都当 sign-in**：普通 token 过期会被要求重新登录（与 P1 §2 的反证同构）。
- **只改 plan 不改 reports**：分析面板一半可用一半不可用。

### ④ 最小 verifier

- `TestAnalyticsRuntimeErrorMapsToSignInLikeRust`
- 断言：①runtime denial（typed）→ 「Sign in again」文案且**不**触发 generic 文案；②普通 401 → 仍走既有恢复；③两条请求路径（plan/reports）行为一致；④既有 `TestCloudClientResolvesRuntimeAuthPerRequestLikeRust`、`TestCloudClientGetAccountAnalyticsRoutesReportsLikeRust` 保持绿。
- 反证：把 `:268` 的 typed 分支删掉 → 断言 ① 变红。

### ⑤ 依赖与顺序

**依赖 P1 §2 的 `isRuntimeAuthDenial` 谓词**（4 条鉴权契约已改派 win lane：该谓词一旦合并即可直接复用；若尚未合并，本条先做不了）。建议在 P1 鉴权链合入后立刻做（改动极小）。

---

## 17. `agent-tree-id` — #60190 `c9b4267c37`

### ① 复现路径

```go
// agent/shutdown_report.go:64-65
// TreeID is a correlation ID for the runtime instance, not a thread ID.
TreeID string `json:"tree_id"`
// :118
return &AgentTreeShutdownState{report: AgentTreeShutdownReport{TreeID: strings.TrimSpace(treeID)}}
```
```
$ rg -n --glob '*.go' "TreeID" appserver/
→ exit=1，0 命中（我这份副本里没有生产者）
```
即：`TreeID` 只有消费者与结构体，**没有稳定的「agent tree id」生成/持久化点**；重载后沿用旧的运行时关联 id，跨重启无法把同一棵树关联起来（environment 移除那半已对齐，见 backlog 的拆分说明）。

### ② 最小改动面 + 第二处消费点

| 位置 | 改动 |
|---|---|
| `agent/` | 定义 agent tree id 的来源（持久化到 thread 记录 vs 每次重载生成），并让 `NewAgentTreeShutdownState` 用它 |
| `appserver/` | 在 root thread 的记录上持久化该 id（`rg TreeID appserver/` 目前 0 命中，说明这里完全缺失——**需你侧确认现状是否已漂移**） |

**第二处消费点**：①shutdown report 之外，运行期是否还有别处引用 tree id（`agent/` 内 rg 只有 `shutdown_report.go` 的 4 处）；②子 agent 的 tree id 继承（子树的 id 必须与父树的 id 有确定关系，不能各自随机）。

### ③ 失败模式

- **用 thread id 顶替**：就是现在注释里明确否定的做法（重载后 thread id 不变 → 跨重启永远关联成同一棵「旧树」）。
- **每次重载随机生成**：重启后无法把两段 shutdown report 关联起来（等于没有 id）。

### ④ 最小 verifier

- `TestAgentTreeIDChangesOnReloadLikeRust`
- 断言：①同一次运行内多次 shutdown report 的 tree id 相同；②重载后新树的 id **变化**；③子树 id 与父树 id 有确定关系；④既有 `TestManagerRemove`、`TestAgentTreeShutdownReportFormattingIsPayloadFreeLikeRust` 保持绿。
- 反证：把 id 改成直接用 thread id → 断言 ② 变红。

### ⑤ 依赖与顺序：**为什么我建议它保持 partial**

- 我这份副本里 `appserver/` 侧**完全没有** tree id 的生产者（`rg TreeID appserver/` exit=1），而 Rust 侧涉及 `core/src/codex_thread.rs`、`core/src/agent/control/runtime.rs`、`exec-server/src/environment.rs` **三处**协同；这不是「一行根因」，是一次跨包的结构改动。
- 更关键：**判定标准本身依赖持久化语义**（「重载后必须变化」还是「应按树稳定」），Rust 侧这两点在不同路径上并不完全一致（`#60190` 原文只保证 shutdown report 里的关联语义）。在把语义问清楚前硬写 verifier，会把一个含糊行为固化成契约。
- 建议：本条**保持 partial**，先在 plan 里把「tree id 的生命周期」写成 3 条明确断言，再决定是否需要新增契约字段。

---

## 18. `release-prerelease-cleanup` — #59887 `b6f53857fc` + #59903 `e3fe1ade2c`

### ① 复现路径

```yaml
# .github/workflows/release.yml:140（我这份副本）
uses: softprops/action-gh-release@v2
```
一步式建 release + 上传资产：上传失败会留下**公开但不完整**的 prerelease。

你那边已按 P1 §2 的判据实现：`.github/scripts/publish-github-release.sh` + `TestPublishReleaseCleansUpFailedPrereleaseById`（我在自己树上 `ls .github/scripts/*.sh` → 不存在，说明这段只在你的主干）。

### ② 最小改动面 + 第二处消费点

已由你落地，这里只列**验收时要看的第二处**：
1. `release.yml` 的 `PRERELEASE`/`TAG_NAME`/`NOTES_FILE`/`DIST_DIR` 环境变量与脚本入参必须一一对应（少一个 → 脚本静默走默认分支）；
2. stable 走 `draft=true` 建、prerelease 直接建——两条路径的 `trap` 清理条件不同（「本次创建」且「上传未完成」），要分别有用例覆盖；
3. 已存在 release 永不删、tag 永不碰——这两条是**安全边界**，属于脚本级用例必须断言的内容。

### ③ 失败模式

- **把「已存在」也当「本次创建」**：一次失败的上传会删掉别人的 release（最严重）。
- **上传成功后元数据 PATCH 失败也删**：资产已经上去了，删掉反而是坏结果（你自己在实现里也强调了这条）。

### ④ 最小 verifier（已有）

- `TestPublishReleaseCleansUpFailedPrereleaseById`（脚本级，四条子用例）。
- 反证：把 `trap` 里的「本次创建」判断去掉 → 「既有 release 不删」子用例变红。

### ⑤ 依赖与顺序：**转 complete 的真实条件**

按你的口径，脚本级用例只到 `partial`。**转 complete 的最小条件**（缺一不可）：

1. 仓库里存在一次真实的 workflow run，其日志里能看到：`gh api` 建 release → `gh release upload` → 成功路径不触发 `DELETE`；
2. 至少有一次**失败注入**的真实 run（例如故意指向不存在的 `DIST_DIR`），日志里能看到按 id `DELETE` 的调用，且事后 `gh release list` 里没有遗留该 tag 的 release；
3. 上述两次 run 的 URL 记进 plan（可追溯），而不是"我们相信脚本能行"。

在此之前**保持 partial**，我不会建议任何"因为脚本有测试所以 complete"的改判。

---

## 19. 我认为做不了 / 应保持 partial 的条目

| 条目 | 结论 | 理由 |
|---|---|---|
| `agent-tree-id`（§17） | **保持 partial** | 生产者在我副本里不存在（`rg TreeID appserver/` exit=1），Rust 侧三处协同；且「重载后 id 该变还是该稳」的语义未定，硬写 verifier 会把含糊行为固化成契约 |
| `release-prerelease-cleanup`（§18） | **保持 partial** | 契约要求「真跑过一次 workflow」，脚本级用例不构成 complete |
| `cloud-image-messages-and-draft-handoff`（§13）的 **draft handoff** | **建议拆条并保持 partial** | Go 侧 `rg "draft" cloudclient/ cloudbackend/` 生产代码 **0 命中**，说明不是「改一处」而是缺一整套跨端协议；与 image 部分合在一条会让已完成的图片那半被未完成的一半拖住（正是你上一轮强调的「混成一条会让已对齐的一半被缺失的一半掩盖」） |
| `typed-prompt-markdown-render`（§10） | 可做但**回归风险高** | 需要同步改写 `TestUserMessagePresentationLikeRust`/`TestUserAndAgentMessageCells`；若近期还有 TUI lane 在动 `history_cell`，建议排到最后单独一批 |
| `tui-custom-gateway-signin`（§15） | 可做，但**建议与 §14 同批** | 两条入口共用写入口，分开做会出现「一条能建、一条不能」的中间态 |

其余 12 条我判断都可以落地并转 complete（前提是各自 verifier 真跑通）。

---

## 20. 复现命令与「核到 / 未核到」清单

我本轮实跑（Go 侧，`748ab55ca7` 期副本）：

```bash
rg -n --glob '*.go' "selected_model_info|SelectedModelInfo|models_manager_for_startup" .   # exit=1，0 命中
rg -n --glob '*.go' "incremental_exec_tools" . | grep -v _test                            # 仅 features/features.go:236
rg -n --glob '*.go' "ExecDefinition" .                                                    # exit=1，0 命中
rg -n --glob '*.go' "exec_tools_update_hint|ExecToolsUpdateHint|exec_tools_removed_definitions_header" .  # exit=1，0 命中
rg -n --glob '*.go' "gateway_setup_wizard|GatewaySetupWizard" .                           # exit=1，0 命中
rg -n --glob '*.go' "hosted tool|HostedTool" agent/                                       # exit=1，0 命中
rg -n --glob '*.go' "draft" cloudclient/ cloudbackend/ | grep -v _test                    # exit=1，0 命中
rg -n --glob '*.go' "TreeID" appserver/                                                    # exit=1，0 命中
ls .github/scripts/*.sh                                                                    # zsh: no matches found
```

核到（Go，行号基于我的副本）：
`auth/account.go:190/565/1114`、`appserver/runtime_router.go:14992-15050`、`turn/skills_tools.go:180/189/592/602/675/988`、`tui/history_cell/messages.go:138/158/171/432`、`cloudclient/prewarm.go:15/47`、`cloudbackend/submission.go:42/82/90`、`app/remote_tui.go:6149`、`app/side_tui.go:59`、`tui/tea/side.go:476/484`、`tui/app/side.go:189`、`features/features.go:236`、`app/interactive_onboarding.go:126`、`tui/mention_codec.go:36/44/183/200`、`agent/shutdown_report.go:64/65/81/118/133`、`tui/analytics/client.go:118/255/268/275`。

Rust 侧（真检出 `748ab55ca7`）：
`otel/src/metrics/names.rs:115/125/127-129`、`core/src/context/world_state/top_level_tools.rs:57/383-392/396/405-419/420-430`、`runtime-agent-instance/src/auth.rs:100-145/169-199/203-213`（P1 复用）。

---

## 21. 一句话结论

17 条里 **14 条可落地并转 complete**（其中 `typed-prompt-markdown-render` 与 `tui-custom-gateway-signin` 建议单独批次以控制回归面），2 条（`agent-tree-id`、`release-prerelease-cleanup`）建议保持 partial，1 条（cloud 的 draft handoff）建议从 `cloud-image-messages-and-draft-handoff` 拆出后保持 partial。code-mode 三条的顺序是 **definitions → wiring → overrides → 指标(#60177)**，指标必须垫底的理由落在 `top_level_tools.rs:383-392`（changed 由 entry diff 得出）与 `names.rs:115/125`（`schema_changed` 含 entry replacement、完整声明不计入）。
