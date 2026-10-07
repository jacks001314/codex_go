# syncl6 · r87b · 落主逐字段对抗性复核：sync634 的 GPT-6 fallback 目录

- 车道：`syncl6`（Linux `de1bb1e71f8f7ad6969025798b555057`）；派单 `msg-1791376308293469800-5212`
- 基线：**`origin/main = e9849503cff38b6379cb1e043e1443292aee8638`**
- 复核对象：**sync634 `c99ac97560522c1dbfd0880494faf2b70c00aee7`**（`model/catalog.go` + `model/catalog_test.go` + `app/app_test.go`）
- Rust 对照：`/home/jacks/jacks_dev/codex` @ `b17c74cfd5ebb39fe70ffaff78de198120278636`，`codex-rs/models-manager/models.json`
- 判据：**落主后的实际文件**（不是补丁草稿）。
- 性质：**只读复核** —— 0 补丁 / 0 commit / 0 push / 0 ref 移动；探针只放 `/tmp`。

---

## §0 环境自检与落主核验

```
$ git -C /home/jacks/jacks_dev/codex_go rev-parse origin/main
e9849503cff38b6379cb1e043e1443292aee8638
$ git -C /home/jacks/jacks_dev/codex_go status --porcelain
?? scripts/loc_report.sh
$ git -C /home/jacks/jacks_dev/codex_go merge-base --is-ancestor c99ac975 origin/main && echo yes
yes
$ git -C /home/jacks/jacks_dev/codex_go show e9849503:model/catalog.go | sha256sum
52f13b7496a134a9bed6744a77566cf0d34350db8bf89c0b943a028dd38b6bd0  -
$ git -C /home/jacks/jacks_dev/codex_go show c99ac975:model/catalog.go | sha256sum
52f13b7496a134a9bed6744a77566cf0d34350db8bf89c0b943a028dd38b6bd0  -
```

⇒ 落主文件与 sync634 提交**逐字节一致**（sha256 `52f13b74…` 与你的复核一致），且未被后续 sync635-638 改动。

**复核方法（机读）**：在 `e9849503` 的干净 detached 工作树里跑探针把**落主后的 fallback 目录**整体 dump 成 JSON，再与 Rust `models.json` 做逐字段比对。

```go
// /tmp/syncl6/zz_audit_dump_test.go（只放 /tmp，用 -overlay 注入）
d := dump{Fallback: fallbackBundledModelsResponse(), Effective: BundledModelsResponse()}
b, _ := json.MarshalIndent(d, "", "  ")
os.WriteFile("/tmp/syncl6/go_fallback.json", b, 0o644)
```

```
$ cd /tmp/wt-syncl6-audit && go test -overlay=/tmp/syncl6/overlay_audit.json ./model/ -run TestZZAuditDumpLandFallback -count=1 -v
    zz_audit_dump_test.go:27: fallback models=11 effective models=11
    zz_audit_dump_test.go:31: preset[00] model=gpt-6.1-sol        priority=1   isDefault=true  visibility=list
    ...（见 §2 全表）
    zz_audit_dump_test.go:33: GetDefaultModel = "gpt-6.1-sol"
```

比对脚本：`/tmp/syncl6/cmp4.py`（字段映射 + 归一化：`supported_reasoning_levels` 取 `effort`、`service_tiers` 取 `id`；补齐 Go 侧 `omitempty` 导致 dump 缺键的 `multi_agent_reasoning_effort` / `guardian`）。

---

## §1 逐字段对照（36 字段 × 11 条 = 396 个字段实例）

### 1.1 三类汇总

| 分类 | 实例数 | 说明 |
|---|---|---|
| **一致**（值相等） | **244** | 含 4 条 GPT-6 的**全部**指定字段 |
| **有意偏差**（系统性未承载） | **83** | 14 个字段，fallback 对 11 条一律不设（见 §1.4） |
| **表示等价**（Go 零值 ↔ Rust null） | **47** | 8 个字段，无语义差（见 §1.5） |
| **仍是漂移** | **22**（+2 个整条） | **全部落在既有条目**，GPT-6 4 条为 0（见 §1.6） |
| 整条集合差异 | 缺 2 条 / 多 2 条 | daybreak×2 缺；`gpt-5.2` / `gpt-5.4-mini` 多 |

### 1.2 ⭐ 4 条 GPT-6 的指定字段：**13/13 全部 OK（DIFF = 0）**

（`cmp` 输出原文节选，`st` 列 = OK/DIFF）

```
  gpt-6.1-sol    priority                     OK  Go=1                                     Rust=1
  gpt-6.1-sol    tool_mode                    OK  Go="code_mode_only"                      Rust="code_mode_only"
  gpt-6.1-sol    multi_agent_version          OK  Go="v2"                                  Rust="v2"
  gpt-6.1-sol    default_reasoning_level      OK  Go="low"                                 Rust="low"
  gpt-6.1-sol    supported_reasoning_levels   OK  Go=["low","medium","high","xhigh","max","ultra"]  Rust=同左
  gpt-6.1-sol    context_window               OK  Go=272000                                Rust=272000
  gpt-6.1-sol    max_context_window           OK  Go=872000                                Rust=872000
  gpt-6.1-sol    use_responses_lite           OK  Go=true                                  Rust=true
  gpt-6.1-sol    truncation_policy            OK  Go={"mode":"tokens","limit":10000}       Rust=同左
  gpt-6.1-sol    service_tiers                OK  Go=["priority"]                          Rust=["priority"]
  gpt-6-astra    ...（同上 10 项 + display_name/description/visibility 全 OK）
  gpt-6-sol      ... 全 OK（default_reasoning_level=medium）
  gpt-6-luna     ... 全 OK（supported_reasoning_levels=5 档，无 ultra）
GPT-6 requested-field DIFF count = 0
```

逐条结论：`gpt-6.1-sol` / `gpt-6-astra` / `gpt-6-sol` / `gpt-6-luna` 的
`priority`(1/2/3/4)、`visibility`(list)、`display_name`、`description`、`tool_mode`(code_mode_only)、
`multi_agent_version`(**全 v2**)、`default_reasoning_level`(low/low/medium/medium)、
`supported_reasoning_levels`（前三条 6 档含 `ultra`，luna 5 档无 `ultra`）、
`context_window`(272000)、`max_context_window`(872000)、`use_responses_lite`(true)、
`truncation_policy`(tokens/10000)、`service_tiers`([priority])
—— **与 Rust `models.json` 完全一致，零偏差**。既有条目 priority（5.6=5/8/9、5.5=13）也已对齐 Rust 全局值。

### 1.3 一致（值相等，244 实例）

除上述 GPT-6 字段外，还包括 5.6/5.5/auto-review 的大部分核心字段（context/truncation 之外的
`input_modalities`、`supports_parallel_tool_calls`、`include_skills_usage_instructions` 等）。
可复跑：`python3 /tmp/syncl6/cmp4.py`（打印 `class counts`）。

### 1.4 有意偏差 —— 「系统性未承载」（14 字段 / 83 实例）

这 14 个字段 Go fallback **对全部 11 条一律不设**（既有 7 条 + sync634 新增 4 条同一约定），落到 Go 类型零值：

| 字段 | Rust 侧非 null 的条目数 | Go 值 | Rust 值 |
|---|---|---|---|
| `shell_type` | 9/11 | `""` | `"shell_command"` |
| `apply_patch_tool_type` | 9/11 | `""` | `"freeform"` |
| `comp_hash` | 9/11 | `""` | `"3000"`/`"2911"` |
| `experimental_supported_tools` | 9/11 | `null` | `["send_user_message_async","clock"]` 等 |
| `additional_speed_tiers` | 9/11 | `null` | `["fast"]` |
| `supports_search_tool` | 9/11 | `false` | `true` |
| `supports_reasoning_summary_parameter` | 9/11 | `false` | `true` |
| `include_apps_usage_instructions` | 4/11 | `false` | `true` |
| `include_plugin_usage_instructions` | 4/11 | `false` | `true` |
| `upgrade` | 4/11 | `null` | gpt-5.5 的 GPT-6 Sol 迁移文案 |
| `node_repl_auto_review_required` | 3/11 | `false` | `true` |
| `default_service_tier` | 2/11（6-sol / 6-luna） | `""` | `"priority"` |
| `multi_agent_reasoning_effort` | 2/11（6.1-sol / astra） | `nil` | `"xhigh"` |
| `availability_nux` | 1/11（6.1-sol） | `null` | `{"message":"Maximize usage with GPT-6.1 Sol. …"}` |

**坦白说明（重要）**：这一类里有**会改变行为的字段**，不是「无差别」：
- `supports_reasoning_summary_parameter` / `include_apps_usage_instructions`：Go 的 **JSON 解析路径**有 default-true 语义
  （`model/catalog.go:712 defaultTrueBool(...)`、`:714 reasoningSummariesSupport(...)`，均在 `UnmarshalJSON` 内，
  `:637`），但 **fallback 是手写字面量，绕过解析** ⇒ **fallback 路径下为 `false`，与 Rust 的 `true` 相反**。
  ⟹ `BundledModelsResponse()` 的两条来源（磁盘 `models.json` vs fallback）在同一字段上给出**不同结果**。
- `multi_agent_reasoning_effort`（6.1-sol/astra 的 `xhigh`）与 `default_service_tier`（6-sol/luna 的 `priority`）是
  我上一单 §1 已**显式登记**的两处有意偏差。
- `supports_search_tool=false` / `node_repl_auto_review_required=false` / `upgrade=null` 等同样是语义差，但**对全部条目一致**。

⇒ 归为「有意偏差」的依据是**系统性（11 条一致、既有 7 条早已如此、非 sync634 引入）**，而非「无行为影响」。

### 1.5 表示等价（47 实例 / 8 字段）

Go 零值 ↔ Rust `null`，无语义差：
`auto_compact_token_limit`、`auto_review_model_override`、`model_specialty`、`effective_context_window_percent`(Go-only)、
`multi_agent_version`(5.5/5.2/5.4-mini 两空)、`tool_mode`、`default_service_tier`、`supports_reasoning_effort_updates`。

结构性（一侧无载体）：Rust-only `prefer_websockets` / `minimal_client_version` / `available_in_plans` /
`requires_sandboxed_review` / `supports_reasoning_summaries`(legacy 别名)；Go-only `base_instructions`（常量 vs Rust 每条
`model_messages.instructions_template`）/ `effective_context_window_percent`。

### 1.6 仍是漂移（22 实例 + 2 整条）—— **全部在既有条目，GPT-6 为 0**

```
  codex-auto-review default_reasoning_summary      Go=""           Rust="none"
  codex-auto-review default_verbosity              Go=""           Rust="low"
  codex-auto-review max_context_window             Go=1000000      Rust=872000            ← 显式设了不同值
  codex-auto-review multi_agent_version            Go=""           Rust="v1"
  codex-auto-review service_tiers                  Go=null         Rust=["priority"]
  codex-auto-review support_verbosity              Go=false        Rust=true
  codex-auto-review supported_reasoning_levels     Go=[low..xhigh] Rust=[low..max]        ← 少 max
  codex-auto-review supports_image_detail_original Go=false        Rust=true
  codex-auto-review tool_mode                      Go=""           Rust="code_mode_only"
  codex-auto-review truncation_policy              Go=bytes/10000  Rust=tokens/10000      ← 显式设了不同值
  codex-auto-review use_responses_lite             Go=false        Rust=true
  codex-auto-review web_search_tool_type           Go=""           Rust="text_and_image"
  gpt-5.5          default_reasoning_summary       Go=""           Rust="none"
  gpt-5.5          default_verbosity               Go=""           Rust="low"
  gpt-5.5          description                     Go="Frontier model for complex coding, research, and …"  Rust="Legacy coding model."   ← 文案已过时
  gpt-5.5          support_verbosity               Go=false        Rust=true
  gpt-5.5          supports_image_detail_original  Go=false        Rust=true
  gpt-5.5          truncation_policy               Go=bytes/10000  Rust=tokens/10000      ← 显式设了不同值
  gpt-5.5          web_search_tool_type            Go=""           Rust="text_and_image"
  gpt-5.6-luna     description   Go="Fast and affordable agentic coding model."      Rust="Older fast and efficient model."        ← 文案已过时
  gpt-5.6-sol      description   Go="Latest frontier agentic coding model."          Rust="Older generation workhorse model."      ← 文案已过时
  gpt-5.6-terra    description   Go="Balanced agentic coding model for everyday …"   Rust="Older balanced model for straightforward work."  ← 文案已过时
  gpt-5.2          <整条存在>      Rust 无此 slug
  gpt-5.4-mini     <整条存在>      Rust 无此 slug
```

注：`codex-auto-review` 的 `max_context_window` Go=1000000（**显式写入**，Rust=872000）与 5.5/auto-review 的
`truncation_policy` Go=`bytes` vs Rust=`tokens` 是**显式设了错值**（不是遗漏），属确定性漂移。
5.6 三条的 `description` 说明 Rust 已把这些条目改写为 "Older …" 文案，而 Go fallback 仍是旧文案。

### 1.7 条目集合差异（原文）

```
Go   : gpt-6.1-sol, gpt-6-astra, gpt-6-sol, gpt-6-luna, gpt-5.6-sol, gpt-5.6-terra, gpt-5.6-luna,
        gpt-5.5, gpt-5.2, gpt-5.4-mini, codex-auto-review            # 11 条
Rust : gpt-6-astra, gpt-6.1-sol, gpt-6-sol, gpt-6-luna, gpt-5.6-sol, gpt-5.6-terra, gpt-5.6-luna,
        gpt-daybreak-blue-latest, gpt-daybreak-red-latest, gpt-5.5, codex-auto-review   # 11 条
Go-only  : ['gpt-5.2', 'gpt-5.4-mini']
Rust-only: ['gpt-daybreak-blue-latest', 'gpt-daybreak-red-latest']
$ git grep -n 'gpt-daybreak-blue\|gpt-daybreak-red' e9849503 -- '*.go'   # 0 命中
（model/ 内仅 access-program 枚举 CyberAccessProgramDaybreakBlue/Red，是另一概念，非模型条目）
```

---

## §2 默认模型由 priority 驱动（确认）+ 残余漂移（确认）

### 2.1 priority 驱动（源码 + 探针双证）

落主源码（`e9849503:model/catalog.go`）：

```
1280:func BuildAvailableModels(remoteModels []ModelInfo) []ModelPreset {
1282:	sort.SliceStable(models, func(i, j int) bool {
1283:		return models[i].Priority < models[j].Priority      # 升序按 priority
1475:func markDefaultPresetByVisibility(presets []ModelPreset) {   # 首个 picker-visible 置 IsDefault
1674:func defaultModelFromAvailable(available []ModelPreset) string { ... return model.IsDefault ... }
```

Rust 同构（`codex-rs/models-manager/src/manager.rs:171`）：`remote_models.sort_by_key(|m| m.priority)` +
`ModelPreset::mark_default_by_picker_visibility(&mut presets)`。

探针实测（落主后的 preset 顺序 + 默认）：

```
    preset[00] model=gpt-6.1-sol        priority=1   isDefault=true   visibility=list
    preset[01] model=gpt-6-astra        priority=2   isDefault=false  visibility=list
    preset[02] model=gpt-6-sol          priority=3   isDefault=false  visibility=list
    preset[03] model=gpt-6-luna         priority=4   isDefault=false  visibility=list
    preset[04] model=gpt-5.6-sol        priority=5   isDefault=false  visibility=list
    preset[05] model=gpt-5.6-terra      priority=8   isDefault=false  visibility=list
    preset[06] model=gpt-5.6-luna       priority=9   isDefault=false  visibility=list
    preset[07] model=gpt-5.5            priority=13  isDefault=false  visibility=list
    preset[08] model=gpt-5.4-mini       priority=23  isDefault=false  visibility=hide
    preset[09] model=gpt-5.2            priority=29  isDefault=false  visibility=list
    preset[10] model=codex-auto-review  priority=43  isDefault=false  visibility=hide
    GetDefaultModel = "gpt-6.1-sol"
```

⇒ 顺序严格按 priority 升序；默认 = 最小 priority 的 picker-visible 条目 = `gpt-6.1-sol`。**确认 priority 驱动。**
（与你独立跑的 RC 一致：把 6.1-sol 的 priority 1→99 后默认落到次小的 `gpt-6-astra`。）

### 2.2 残余漂移确认（与我上一单一致）

- **缺** `gpt-daybreak-blue-latest`(Rust p11, hide) / `gpt-daybreak-red-latest`(Rust p12, hide) —— 落主后仍缺（§1.7）。
- **多** `gpt-5.2`(p29) / `gpt-5.4-mini`(p23, hide) —— 落主后仍在（`gpt-5.4-mini` 仍被
  `TestFallbackBundledCatalogDropsGPT54LikeRust` 依赖，用于 GPT-6 Luna 迁移提示的保存选择）。
⇒ 我上一单 §3 的残余漂移陈述**落主后依然成立**；此外本次复核**新增发现**了 §1.6 的 22 处既有条目字段漂移（此前未登记）。

---

## §3 路线 (c) 决策所需的「两列表」

### 3.1 Go 仍缺 / 仍多的条目

| Go **仍缺**（Rust 有） | Go **仍多**（Rust 无） |
|---|---|
| `gpt-daybreak-blue-latest`（priority 11，visibility hide，Daybreak Blue） | `gpt-5.2`（priority 29，visibility list） |
| `gpt-daybreak-red-latest`（priority 12，visibility hide，Daybreak Red） | `gpt-5.4-mini`（priority 23，visibility hide；被迁移提示测试依赖） |

### 3.2 不 embed ⇒ 会**持续漂移**的具体理由（逐条附证据）

| # | 理由 | 证据（本次实测） |
|---|---|---|
| 1 | **每次上游加/改模型都要手抄 + 改期望** | sync634 即为实例：手抄 4 条 + 改 6 条测试期望（`catalog_test.go` 4 处 + `app_test.go` 2 处） |
| 2 | **文案漂移会静默发生** | Rust 已把 5.6 三条改写为 "Older …"，Go fallback 仍是 "Latest frontier / Balanced / Fast and affordable"（§1.6） |
| 3 | **字段值漂移已存在且未登记** | 本次新发现 22 处（`codex-auto-review` 12 处、`gpt-5.5` 6 处、5.6 文案 3 处、+ 2 整条）；其中 `max_context_window` 1000000 vs 872000、`truncation_policy` bytes vs tokens 是**显式错值** |
| 4 | **两条来源（磁盘 json vs fallback）行为不等价** | fallback 是字面量，**绕过** `UnmarshalJSON` 的 default-true（`catalog.go:712/714`）⇒ 同一字段在 `models.json` 路径为 `true`、fallback 路径为 `false` |
| 5 | **新模型只能靠人记得补** | `multi_agent_reasoning_effort` / `default_service_tier` 两处偏差即因 fallback 条目形态所限而未写（上一单已披露） |
| 6 | **无自动化漂移检测** | 仓内现有测试只钉了少量显式断言（默认模型 / ultra / Bedrock v2），没有「fallback vs Rust models.json 全量比对」的用例；本次 22 处漂移此前无人发现 |

⇒ 若走 (c)：把 `models.json` 以 `//go:embed`（或跟踪 vendored 文件）接入，可一次性消除 1-6；成本估计见上一单
`update/syncl6_gpt6_bundled_2026_10_07.md §7`（vendored ≈472 KB + embed + CI sha 校验，L 级）。
**本单不擅自开工**（遵你指示：(c) 未批准）。

---

## §4 结论与交付物

- **复核结论**：sync634 落主文件与提交**逐字节一致**；**4 条 GPT-6 的指定字段 13/13 与 Rust 完全一致（0 偏差）**；
  默认模型由 priority 驱动、`gpt-6.1-sol` 为首个 picker-visible（= Rust 行为）；
  残余条目集合漂移（缺 daybreak×2 / 多 5.2+5.4-mini）**落主后仍成立**。
- **本次新增发现（需你入库/上报）**：
  1. 14 个「系统性未承载」字段中，`supports_reasoning_summary_parameter` / `include_apps_usage_instructions`
     在 fallback 路径与 `models.json` 路径**结果相反**（`false` vs `true`），根因是 fallback 字面量绕过 `UnmarshalJSON` 的 default-true。
  2. 22 处既有条目字段漂移（未在上一单登记），含 2 处显式错值（`codex-auto-review.max_context_window`、
     `gpt-5.5`/`codex-auto-review` 的 `truncation_policy`）与 3 处过时文案。
- **未决（需你裁定）**：
  1. 上述 2 项发现是否要单独立项（分别属「fallback 与 json 路径一致性」与「既有条目字段纠偏」），还是并入路线 (c) 一并解决；
  2. 路线 (c) 是否上报/立项（本单未开工）。
- **可复跑**：
  ```bash
  git -C /home/jacks/jacks_dev/codex_go worktree add --detach /tmp/wt-syncl6-audit e9849503
  cd /tmp/wt-syncl6-audit && go test -overlay=/tmp/syncl6/overlay_audit.json ./model/ -run TestZZAuditDumpLandFallback -count=1 -v
  python3 /tmp/syncl6/cmp4.py        # 打印 class counts / DRIFT / SYSTEMATIC / REPR
  ```
- 交付物：本报告 `update/syncl6_gpt6_landed_audit_2026_10_07.md`（只读复核，**无补丁**）。
