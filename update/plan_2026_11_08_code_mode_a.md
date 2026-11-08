# Code Mode / 增量工具（A 包：4 条）—— `55897425e8..748ab55ca7` delta

- 作者：ledger-align（对齐账本 lane）· 日期：2026-11-08
- 性质：只读分析。不改 Go 代码、不动 `parity/**`、不动 `baseline.json` / 生成脚本 / `rustToInclusive`（锚点维持 3584）。
- Rust 基线：`/home/jacks/jacks_dev/codex`（HEAD `748ab55ca7`）；行号为**该提交自身版本**。
- **每条 rustPaths 先用 `${R}:codex-rs/<path>`（`R=$(git rev-parse 55897425e8)`）验 pin 存在性**；`MISS` 的一律不进契约表（见 §5）。
- 与 round 1317 的关系：`55897425e8`（#59832）我上次判 `ported`，给了 `incremental_tools` / `incremental_tools_legacy` 的 Go 落地。本包 4 条都建在它之上，所以**底座复用**、只判增量。

## 0. 判定汇总

| # | commit | PR | 主题 | 判定 | 一句话理由 |
| --- | --- | --- | --- | --- | --- |
| 11 | `2bc8e7f545` | #60157 | Add support for incremental code mode tool definitions | **partial** | Go 的增量更新只到「每个 top-level definition 一个 hash」，`functions.exec` 是**单个** hash key；没有 `ExecDefinition` 结构化条目 |
| 10 | `33b3f80a09` | #60163 | Wire incremental exec tool updates into Code Mode sessions | **partial** | `incremental_exec_tools` 在 Go 是**死开关**：只有注册与一条注册测试，没有任何消费点 |
| 3 | `c3936b4b8a` | #60188 | Allow model overrides for incremental Code Mode tool messages | **partial** | Go 的 override 表只有 5 个通用字段，缺 `exec_tools_update_hint` / `exec_tools_removed_definitions_header` |
| 7 | `5226f2fb4b` | #60177 | Add metrics for incremental exec catalog updates | **partial** | 4 个指标只有 2 个（`incremental_updates`、`spec_description_bytes`），缺 `exec_entry_updates` / `exec_update_bytes` |

组内：`partial 4`。

## 1. `2bc8e7f545` #60157 — 增量 code-mode 工具定义

### 1.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `code-mode-protocol/src/description.rs:165-181` | 新增 `ExecDefinition`：把 code-mode 的嵌套工具从"一整段 description 文本"提升为**结构化条目**（stable key + description + 所属 namespace），这是能做增量 diff 的前提 |
| `…/description.rs:139-143`、`:145-157` | `CodeModeToolKind` / `ToolDefinition`；`:158-164` `ToolNamespaceDescription`（namespace 级说明单独成条目） |
| `…/description.rs:188-270` | `parse_exec_source`：从 exec 源码里解析嵌套工具定义（用于稳定 key 与去重） |
| `…/description.rs:290-412` | `build_exec_tool_description_and_definitions`：一次构建同时产出「完整描述文本」与「结构化条目」，后者供增量更新使用 |
| `core/src/context/world_state/top_level_tools.rs:50-52` | `TopLevelToolsState` 增加 `incremental_exec_tools: bool` 与 `exec_definitions: Option<Arc<Vec<ExecDefinition>>>` |
| `…/top_level_tools.rs:114-121` | `with_incremental_exec_tools(entries)`：打开 granular 模式 |
| `…/top_level_tools.rs:188-215`、`:368-440` | 有 `exec_catalog` 时按 **entry key** 做 added / schema_changed / removed 三类 diff，产出 `## Namespace: functions` 批次或撤回清单 |
| `…/top_level_tools.rs:522-600` | `render_exec_update(definitions)` 与 `RemovedExecDefinitions(names)`：批次文本与撤回通知（stable key 列表） |

### 1.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| 增量更新粒度只到 top-level definition，`functions.exec` 是**一个** hash | 【F:session/top_level_tools.go†L286-L330】`TopLevelToolsUpdatesWithMessages` 用 `topLevelDefinitionName` + `TopLevelToolsHashes` 逐 definition 比对；测试里可以直接看到 `hashes["functions.exec"]`（【F:session/top_level_tools_test.go†L593】） |
| namespace 成员可按成员 diff，但那是「通用 namespace」，不是 code-mode 的 exec 条目 | 【F:session/top_level_tools.go†L318-L352】`topLevelDefinitionMembers` 分支 + namespace instructions 文本 |
| **无 `ExecDefinition` 概念** | `grep -rn --include='*.go' "ExecDefinition"` → **0** |
| code-mode 侧只有"整体描述构建"，没有结构化条目 | 【F:codemode/codemode.go†L214-L300】`BuildExecTool` / `BuildExecToolDescription*`（输出 `FreeformSpec`/字符串）；【F:codemode/codemode.go†L150-L186】`NamespaceDescription` + `IsNestedTool` |
| `incremental_exec_tools` 开关存在但没接线 | 【F:features/features.go†L236】`{Key: "incremental_exec_tools", StageUnderDevelopment, DefaultEnabled: false}`；全树只有 `features/features_test.go:120` 提到它 |

### 1.3 判定：`partial`

Go 的"增量工具"是**通用层**（hash 级），Rust 这条把它推进到 **code-mode 条目级**（每个嵌套工具一个 stable key，可单独 added/changed/removed）。Go 的 `codemode` 已经能构建完整 exec 描述，缺的是把构建过程**同时**产出结构化条目。

### 1.4 落地切入点

1. `codemode/codemode.go`：`BuildExecToolDescriptionWithOptions`（`:280`）内部已经在遍历 definitions/namespaces —— 在同一趟里额外产出 `[]codemode.ExecDefinition{Key, Description, Namespace, Kind}`（等价 Rust `description.rs:165`），并导出（供 session 层 diff）。
2. `session/top_level_tools.go`：`TopLevelToolsUpdatesWithMessages` 增加 `execDefinitions []ExecDefinition` 参数与 `incrementalExec bool`；当 `functions.exec` 属于本次 updates 且 `incrementalExec` 为真时，改走 entry 级 diff，产出 `## Namespace: functions` 批次 + 撤回清单。
3. 渲染文本沿用 Rust 的固定 hint（见 §3 的 override 字段），不要把 `functions.exec` 的完整描述再发一遍。

## 2. `33b3f80a09` #60163 — 把增量 exec 更新接进 Code Mode 会话

### 2.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `core/src/tools/router.rs:104`、`:169-171` | `ToolRouter` 持 `exec_definitions: Option<Arc<Vec<codex_code_mode::ExecDefinition>>>` 并提供访问器——结构化条目从"构建时"一路带到"组上下文时" |
| `core/src/tools/spec_plan.rs:497-506` | `register_code_mode_executors` 改为返回 `(code_mode_tool_names, exec_definitions)`，`finalize_tool_router` 一起塞进 router |
| `core/src/session/world_state.rs:211-217` | 组 world state 时：若 router 有 exec entries，则 `TopLevelToolsState::with_incremental_exec_tools(Some(entries))` —— 这就是"接进 Code Mode 会话"的那一步 |
| 特性门控（提交正文） | Responses Lite + `CodeModeOnly` + `IncrementalTools` + `IncrementalExecTools` 同时满足才开 granular |
| 语义约束（提交正文） | 变更的工具定义 / namespace 说明 / 撤回作为 developer 消息追加；**公共 exec 说明与共享类型冻结到 compaction**，之后重新下发完整目录 |

### 2.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **`incremental_exec_tools` 是死开关**（注册了，没有任何消费点） | `grep -rn --include='*.go' "incremental_exec_tools"` → 仅 【F:features/features.go†L236】与 `features/features_test.go:120` |
| session 无法从 router 拿到 exec 条目 | 全树 `ExecDefinition` → 0；Go 的路由（`tool.Registry` / `appserver` 侧 spec plan 等价物）只产出 `[]any` definitions 给增量层 |
| 增量层只有"整段 exec 描述替换"这一条路 | 一旦 `functions.exec` 的描述变了，整条 `additional_tools` 声明会重发（【F:session/top_level_tools.go†L353-L380】`toolsContainNamespace` → `ToolUpdateHint` + full declaration） |

### 2.3 判定：`partial`

缺少"结构化 exec 条目从构建层流到 session 层"的通道，以及 4 个特性的组合门控。后果：code-mode 的嵌套工具一有变化就重发整段 `functions.exec` 描述，token 成本随工具数线性增长（这也是 Rust 这条的 why）。

### 2.4 落地切入点

1. 路由层（Go 的等价物：`appserver` 的 spec 构建 / `tool.Registry` 聚合处）在生成 exec spec 时保留 `[]ExecDefinition`，挂到 turn 级上下文（对应 Rust `ToolRouter.exec_definitions`）。
2. 组 world state 处（Go 的 `appserver/incremental_tools.go` 构建 `IncrementalToolState`）读取该条目集，置 `incrementalExec = features.Enabled(cfg, "incremental_exec_tools") && info.UseResponsesLite && codeModeOnly`。
3. 与 compaction 的边界照抄 Rust：granular 期间冻结公共说明/共享类型，compaction 后下发完整目录。

## 3. `c3936b4b8a` #60188 — 增量 Code Mode 工具消息的模型级措辞覆盖

### 3.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `protocol/src/openai_models/incremental_tools.rs`（+6） | `IncrementalToolMessages` 增加 `exec_tools_update_hint`、`exec_tools_removed_definitions_header` 两个字段（model catalog 可下发） |
| `prompts/src/model_messages/incremental_tools.rs:8-16`、`:24-60` | `ResolvedIncrementalToolMessages` 增加同名字段；解析规则：**缺失/null/超过 512 UTF-8 字节** → 用内置措辞；**空串** → 省略该段引导但保留定义与撤回 key |
| `core/src/context/world_state/top_level_tools.rs:388-400` | `render_exec_update(definitions, hint)`：hint 为空时不再插入前导段，直接 `## Namespace: functions` |
| `…/top_level_tools.rs:456-470`、`:552-600` | `RemovedExecDefinitions { header, names }`：header 为空则只输出 key 列表 |
| 语义（提交正文） | **仅措辞变化不触发**catalog update（diff 仍按定义内容/键算） |

### 3.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| override 表只有 5 个**通用**字段 | 【F:session/top_level_tools.go†L49-L66】`IncrementalToolMessageOverrides{ToolUpdateHint, RemovedToolsHeader, RemovedNamespacesHeader, NamespaceInstructionsPrefix, NamespaceInstructionsCleared}` |
| 解析器已实现 512 字节上限 + fallback（可复用） | 【F:session/top_level_tools.go†L70-L85】`resolve`：`nil || len > 512 → fallback` |
| **没有 exec 专属两个字段** | `grep -rn --include='*.go' "exec_tools_update_hint\|exec_tools_removed_definitions_header"` → **0** |
| 空串语义未实现 | 现有 resolve 返回空串时，`ToolUpdateHint` 为空会在 【F:session/top_level_tools.go†L353-L357】跳过 hint（通用路径已对），但 exec 更新走的是同一段文本，没有"省略 exec 引导仍保留批次"的独立控制 |

### 3.3 判定：`partial`

机制（override + 512 上限 + 空串省略）在 Go 的**通用**字段上已经存在，缺的是 exec 专属的两个字段与其渲染分支——一旦 §1/§2 落地，这两个字段就是必需的接口，所以要一起登记。

### 3.4 落地切入点

1. `session/top_level_tools.go:51-66`：`IncrementalToolMessageOverrides` / `ResolvedIncrementalToolMessages` 各加 2 个字段（exec 专属），`ResolveIncrementalToolMessages` 复用同一 `resolve`。
2. `model.ModelMessages`（`model/` 侧的 catalog 结构）同步两个 JSON 字段，保证与 Rust 的 `IncrementalToolMessages` 同名。
3. 渲染：exec 批次用 `ExecToolsUpdateHint`（空则省略引导），撤回通知用 `ExecToolsRemovedDefinitionsHeader`（空则只列 key）；wording-only 变化不进入 diff（diff 只看 key + description hash）。

## 4. `5226f2fb4b` #60177 — 增量 exec 目录更新的指标

### 4.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `otel/src/metrics/names.rs:126`、`:129` | 新增 `EXEC_TOOL_ENTRY_UPDATES_METRIC = "codex.tools.exec_entry_updates"`、`EXEC_TOOL_UPDATE_BYTES_METRIC = "codex.tools.exec_update_bytes"` |
| `core/src/context/world_state/top_level_tools.rs:391-425` | 计数：`added` 与 `schema_changed`（**条目替换也算 schema_changed**）分别累加；histogram：本批渲染后的 UTF-8 字节数（含 hint 与框架，**不含**完整声明与撤回通知）；bucket 复用 `TOOL_SPEC_DESCRIPTION_BYTES_METRIC_BUCKETS` |
| `…/top_level_tools.rs:450-460` | `removed` 也计入同一个 counter，tag `action=removed` |
| 语义（提交正文） | 完整目录下发**不**算 entry delta；`exec` 被隐藏/恢复/未变化时不应产生指标 |

### 4.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| 只有通用两个指标 | 【F:metrics/tool_spec_metrics.go†L19】`ToolIncrementalUpdatesMetric = "codex.tools.incremental_updates"`；`:24` `ToolSpecDescriptionBytesMetric = "codex.tools.spec_description_bytes"` |
| 记录点已在、可按 tool 打 tag（可复用） | 【F:session/top_level_tools.go†L429-L500】`RecordChangedToolMetrics` / `recordChangedToolMetric`（`action` + `tool` tag）；测试见 `appserver/incremental_tools_metrics_test.go:104/321` |
| **缺 exec 专属两组** | `grep -rn --include='*.go' "exec_entry_updates\|exec_update_bytes"` → **0** |

### 4.3 判定：`partial`

指标基础设施完全具备（counter/histogram + bucket + tag 约定），缺的只是 exec 批次的两个指标名与三处记录点。**注意口径**：`schema_changed` 要把"条目替换"算进去，且**不要**把完整目录声明计入字节数——否则指标会被首次下发污染。

### 4.4 落地切入点

1. `metrics/tool_spec_metrics.go`：加两个常量（字符串与 Rust 完全一致）。
2. `session/top_level_tools.go`：在 exec 批次渲染处（§1 落点）按 added/schema_changed 计数、按渲染文本字节数打 histogram；撤回处打 `action=removed`。
3. 测试要点：首次完整目录不下发 entry delta（budget 为 0）；exec 隐藏→恢复不产生 delta；替换条目计 `schema_changed`；字节数随 hint 长度线性变化、与撤回通知无关。

## 5. 契约登记表（4 条；rustPaths 已对 pin 验证）

| # | contract id | kind | rustPaths（pin 均存在） | goPaths | phase | status | 可立即登记的 verifier | 需先落的 verifier + 要点 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `2bc8e7f545` #60157 | `code-mode-incremental-exec-definitions` | `source-surface` | `code-mode-protocol/src/description.rs`、`core/src/context/world_state/top_level_tools.rs` | `codemode`、`session` | `P1` | `partial` | `TestTopLevelToolsNamespaceTransitionsLikeRust;TestIncrementalExecToolsFeatureRegisteredLikeRust` | `TestIncrementalExecDefinitionsDiffByEntryKeyLikeRust`（added/schema_changed/removed 三类 + stable key 不变时不产更新） |
| `33b3f80a09` #60163 | `code-mode-incremental-exec-wiring` | `source-surface` | `core/src/tools/router.rs`、`core/src/tools/spec_plan.rs`、`core/src/session/world_state.rs` | `appserver`、`session`、`turn` | `P1` | `partial` | `TestIncrementalToolCatalogAppendsChangedCatalogLikeRust;TestIncrementalToolCatalogRecordsWindowStartLikeRust` | `TestCodeModeSessionEmitsIncrementalExecUpdatesLikeRust`（4 特性组合门控；公共说明冻结到 compaction；恢复后下发完整目录） |
| `c3936b4b8a` #60188 | `incremental-exec-message-overrides` | `source-surface` | `protocol/src/openai_models/incremental_tools.rs`、`prompts/src/model_messages/incremental_tools.rs`、`core/src/context/world_state/top_level_tools.rs` | `session`、`model` | `P1` | `partial` | `TestTopLevelToolsCatalogMessageOverridesLikeRust` | `TestIncrementalExecMessageOverridesRespectLimitLikeRust`（512 UTF-8 上限、独立 fallback、空串省略引导但保留批次与撤回 key、wording-only 不触发更新） |
| `5226f2fb4b` #60177 | `incremental-exec-catalog-metrics` | `source-surface` | `otel/src/metrics/names.rs`、`core/src/context/world_state/top_level_tools.rs` | `metrics`、`session` | `P1` | `partial` | `TestIncrementalToolCatalogRecordsChangedToolMetricsLikeRust;TestRecordChangedToolMetricsTagsPerToolLikeRust` | `TestIncrementalExecCatalogMetricsTagActionsLikeRust`（added/schema_changed/removed 计数；字节数含 hint 不含完整声明；隐藏/恢复不产 delta） |

**pin 后新增、不得写进 rustPaths**：本包没有（4 条全部复用 pin 内文件；`protocol/src/openai_models/incremental_tools.rs` 与 `prompts/src/model_messages/incremental_tools.rs` 在 pin 都存在，我已逐个 `cat-file -e` 验过）。

## 6. 复现命令

```bash
R=$(git -C /home/jacks/jacks_dev/codex rev-parse 55897425e8)
for p in code-mode-protocol/src/description.rs core/src/context/world_state/top_level_tools.rs \
         core/src/tools/router.rs core/src/tools/spec_plan.rs core/src/session/world_state.rs \
         protocol/src/openai_models/incremental_tools.rs prompts/src/model_messages/incremental_tools.rs \
         otel/src/metrics/names.rs; do
  git -C /home/jacks/jacks_dev/codex cat-file -e "${R}:codex-rs/$p" 2>/dev/null && echo "OK   $p" || echo "MISS $p"
done
# → 8 行全 OK
```

Go 侧零命中/定位：

```
$ grep -rn --include='*.go' "ExecDefinition" .                          → 0
$ grep -rn --include='*.go' "incremental_exec_tools" .                 → features/features.go:236 + features/features_test.go:120（无消费点）
$ grep -rn --include='*.go' "exec_tools_update_hint\|exec_tools_removed_definitions_header" . → 0
$ grep -rn --include='*.go' "exec_entry_updates\|exec_update_bytes" .  → 0
$ grep -rn --include='*.go' 'hashes\["functions.exec"\]' session/ appserver/ → 单 key（session/top_level_tools_test.go:593, appserver/incremental_tools_test.go:275）
```

## 7. 结论

- 4 条全 `partial`，共同根因只有一个：**Go 的增量工具层停在"top-level definition 一个 hash"**，而 Rust 这条链把它推进到 **code-mode 条目级**（结构化 `ExecDefinition` → 路由透传 → 会话内 entry diff → exec 专属措辞 → exec 专属指标）。四条是**同一条链的五个环节中的四个**，建议同批落地（顺序 `#60157 → #60163 → #60188 → #60177`）。
- 一个可以直接复用的成熟件：Go 的 override 解析（512 字节 + fallback + 空串省略）与指标录制（action/tool tag + bucket）都已经存在，`#60188`/`#60177` 只需加字段与记录点，不需要新基础设施。
- `incremental_exec_tools` 目前是**死开关**（注册 + 一条注册测试，零消费点）——这既是缺口证据，也意味着落地时不会有历史行为冲突。
- 本文件不改 Go 代码、不动 `parity/**`；锚点维持 3584，4 条进 §12 post-anchor 登记。
