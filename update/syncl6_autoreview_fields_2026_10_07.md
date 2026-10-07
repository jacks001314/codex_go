# r88e · syncl6 · `codex-auto-review` fallback 三字段缺口 —— 只读 scoping

## §0 元信息与只读声明

| 项 | 值 |
|---|---|
| 派单 | `msg-1791380399484729200-5774`（leader `syntropy` / `agent-74026fd8656bf4c18e7e3460`） |
| 任务性质 | **只读 scoping**：0 补丁 / 0 commit / 0 push / 0 ref 移动（探针只放 `/tmp`） |
| Go 基线 | `origin/main = c9fbd10f56d75e011b193f2bc3ad6e195bde05a9`（`sync659`，实测；派单发出时台账为 `b041cbfd`，已前进） |
| 工作树 | `git worktree add --detach /tmp/wt-syncl6-r88e c9fbd10f`（detached，未建任何 ref） |
| Rust 对照 pin | `b17c74cfd5ebb39fe70ffaff78de198120278636`（`codex-rs/models-manager/models.json`） |
| 探针 | `/tmp/syncl6/r88e/zz_probe_autoreview_test.go`、`zz_probe_resolve_test.go`；overlay `/tmp/syncl6/r88e/overlay*.json`（仓内零落盘） |

结论一句话：**synct5 的发现成立** —— Go fallback bundled 目录的 `codex-auto-review` 条目**缺 3 项**（`ToolMode` / `MultiAgentVersion` / `UseResponsesLite`），而 Rust `models.json @ b17c74cfd5` 明确给了 `code_mode_only` / `v1` / `true`。定级 **S（1 生产文件 + 1 测试文件）**，**先报不动手**，等放行。

---

## §1 独立复核（发现成立）

### §1.1 Rust 真源：`models.json @ b17c74cfd5`（11 条，三字段全表）

复跑命令（只读）：

```
$ cd /home/jacks/jacks_dev/codex
$ git show b17c74cfd5:codex-rs/models-manager/models.json > /tmp/syncl6/r88e/models.json
$ python3 -c "import json;d=json.load(open('/tmp/syncl6/r88e/models.json'));
[print(f\"{m['slug']:26} {str(m.get('tool_mode')):18} {str(m.get('multi_agent_version')):8} {str(m.get('use_responses_lite'))}\") for m in d['models']]"
```

实际输出：

```
slug                       tool_mode          multi_agent_version   use_responses_lite
--------------------------------------------------------------------------------------
gpt-6-astra                code_mode_only     v2                    True
gpt-6.1-sol                code_mode_only     v2                    True
gpt-6-sol                  code_mode_only     v2                    True
gpt-6-luna                 code_mode_only     v2                    True
gpt-5.6-sol                code_mode_only     v2                    True
gpt-5.6-terra              code_mode_only     v2                    True
gpt-5.6-luna               code_mode_only     v1                    True
gpt-daybreak-blue-latest   code_mode_only     v2                    True
gpt-daybreak-red-latest    code_mode_only     v2                    True
gpt-5.5                    None               None                  False
codex-auto-review          code_mode_only     v1                    True
```

`codex-auto-review` 三条**字段原值**（`json.dumps` 逐字段取，含类型）：

```
'tool_mode'           = 'code_mode_only'   (type=str)
'multi_agent_version' = 'v1'               (type=str)
'use_responses_lite'  = True               (type=bool)
```

（同条另给 `priority=43`、`visibility="hide"`、`context_window=272000`、`max_context_window=872000`、`truncation_policy={mode:"tokens",limit:10000}`、`supports_reasoning_summary_parameter=true`、`include_apps_usage_instructions=false` —— 与 Go fallback 现有值一致，**不在本缺口内**。）

### §1.2 Go 载体与逐条对照表

Go fallback 字面量目录：`model/catalog.go` → `fallbackBundledModelsResponse()`（`@origin/main` **909-1068**），共 **11** 条，末尾 `applyBundledCatalogBooleans(&response)`（1066）只补 `supports_reasoning_summary_parameter` / `include_apps_usage_instructions`（1078-1090），**不碰这三项**。

| slug | Go 块行（@origin/main） | `ToolMode` | `MultiAgentVersion` | `UseResponsesLite` | Rust 值 | 一致? |
|---|---|---|---|---|---|---|
| gpt-6.1-sol | 912-923 | `code_mode_only` @915 | `v2` @915 | `true` @920 | code_mode_only/v2/true | ✅ |
| gpt-6-astra | 924-935 | @927 | @927 | @932 | 同上 | ✅ |
| gpt-6-sol | 936-947 | @939 | @939 | @944 | 同上 | ✅ |
| gpt-6-luna | 948-959 | @951 | @951 | @956 | 同上 | ✅ |
| gpt-5.6-sol | 960-971 | @963 | @963 | @968 | 同上 | ✅ |
| gpt-5.6-terra | 972-983 | @975 | @975 | @980 | 同上 | ✅ |
| gpt-5.6-luna | 984-995 | @987 | `v1` @987 | @992 | code_mode_only/v1/true | ✅ |
| gpt-5.5 | 996-1014 | **零值** | **零值** | `false` | `null`/`null`/`false` | ✅（Rust 显式 null ≈ Go 零值） |
| gpt-5.2 | 1015-1021 | **零值** | **零值** | `false` | **Rust 无此条** | ⚠️ 无对位（D 组 backlog） |
| gpt-5.4-mini | 1022-1039 | **零值** | **零值** | `false` | **Rust 无此条** | ⚠️ 无对位（D 组 backlog） |
| **codex-auto-review** | **1040-1056** | **零值 ❌ 缺** | **零值 ❌ 缺** | **`false` ❌ 缺** | code_mode_only/v1/**true** | **❌ 真缺口** |

`codex-auto-review` Go 块**原文**（`git show origin/main:model/catalog.go`，1040-1056）：

```go
1040: 			{
1041: 				Slug:                          "codex-auto-review",
1042: 				DisplayName:                   "Codex Auto Review",
1043: 				Description:                   "Automatic approval review model for Codex.",
1044: 				Visibility:                    VisibilityHide,
1045: 				SupportedInAPI:                true,
1046: 				Priority:                      43,
1047: 				BaseInstructions:              BaseInstructions,
1048: 				DefaultReasoningLevel:         "medium",
1049: 				SupportedReasoningLevels:      []string{"low", "medium", "high", "xhigh"},
1050: 				TruncationPolicy:              TruncationPolicy{Mode: TruncationModeTokens, Limit: 10000},
1051: 				ContextWindow:                 272000,
1052: 				MaxContextWindow:              872000,
1053: 				EffectiveContextWindowPercent: 95,
1054: 				InputModalities:               []string{"text", "image"},
1055: 				SupportsParallelToolCalls:     true,
1056: 			},
```

⇒ 块内**无** `ToolMode:` / `MultiAgentVersion:` / `UseResponsesLite:` 任一行（对照同文件 915/927/939/951/963/975/987 行同族条目均显式给出）。

字段声明（`@origin/main`）：`ToolMode` @ `model/catalog.go:467`、`MultiAgentVersion` @ `:468`、`UseResponsesLite` @ `:484`。

### §1.3 探针：fallback 路径三项确为“零值 / direct”（原文）

探针（`-overlay`，只放 `/tmp`）：

```
$ cd /tmp/wt-syncl6-r88e
$ go test -overlay=/tmp/syncl6/r88e/overlay.json ./model/ -run TestProbeAutoReviewCarriesModelsJSONFields -count=1 -v
=== RUN   TestProbeAutoReviewCarriesModelsJSONFields
    zz_probe_autoreview_test.go:29: codex-auto-review fallback drift vs models.json @b17c74cfd5: [ToolMode= want code_mode_only MultiAgentVersion= want v1 UseResponsesLite=false want true]
--- FAIL: TestProbeAutoReviewCarriesModelsJSONFields (0.00s)
FAIL
FAIL	codex_go/model	0.005s
FAIL
```

生产解析路径（`ResolveToolMode` = 真实消费入口）：

```
$ go test -overlay=/tmp/syncl6/r88e/overlay2.json ./model/ -run TestProbeAutoReviewResolvedBehavior -count=1 -v
=== RUN   TestProbeAutoReviewResolvedBehavior
    zz_probe_resolve_test.go:8: fallback ToolMode="" ResolveToolMode(no features)="direct" ; MultiAgentVersion="" ; UseResponsesLite=false
    zz_probe_resolve_test.go:10: Rust models.json @b17c74cfd5: tool_mode=code_mode_only mav=v1 use_responses_lite=true
--- PASS: TestProbeAutoReviewResolvedBehavior (0.00s)
```

⇒ `codex-auto-review` 在 fallback 路径下 `ToolMode=""` 经 `ResolveToolMode` 落到 **`direct`**（Rust 为 `code_mode_only`），`MultiAgentVersion=""` ⇒ 模型**未声明**版本，`UseResponsesLite=false`。

---

## §2 落点 / 行为影响 / 最小补丁规模

### §2.1 落点

`model/catalog.go` 的 `fallbackBundledModelsResponse()` 内 `codex-auto-review` 块（**1040-1056**），在 `BaseInstructions` 之后（约 **1048 行**前）插入三行：

```go
ToolMode:          ToolModeCodeModeOnly,
MultiAgentVersion: "v1",
UseResponsesLite:  true,
```

（同族 GPT-6/5.6 条目即此写法，见 915 行 `ToolMode: ToolModeCodeModeOnly, MultiAgentVersion: "v2", …` + 920 行 `UseResponsesLite: true, …`。注意 review 条目是 **v1** 不是 v2。**不要动** `bedrockModel` / `normalizeBundledBedrockCatalog` 段 —— 那是 sync626 的面。）

### §2.2 三项各自行为影响（`file:line` 均打 `@origin/main`）

**(a) `ToolMode` —— code mode 工具面**
- 消费：`ResolveToolMode(modelToolMode, featureSettings)` @ `model/catalog.go:1460-1472` —— 显式 `tool_mode` 优先，否则回落 feature `code_mode_only` → `code_mode` → `direct`。
- 调用点：`appserver/runtime_router.go:14064`、`appserver/turn_runtime.go:7327`、`exec/exec.go:495`。
- 差异：Rust 对 `codex-auto-review` 强制 `code_mode_only`（fail-closed，见 `exec/exec_test.go:433` 的 `code-mode-only-fails-closed` 语义）；Go fallback 下为 `""` ⇒ **review 模型的工具面改由全局 feature 决定**，缺 feature 时拿 `direct` 工具面。**（可观测：探针 §1.3 第 2 段 `ResolveToolMode(...)="direct"`。）**

**(b) `MultiAgentVersion` —— multi-agent 版本**
- 消费：`runtimeModelMultiAgentVersion` @ `appserver/runtime_router.go:6021-6045`（读 `info.MultiAgentVersion`；只有 `v1`/`v2`/`disabled` 才 `declared=true`，否则 `("",false)`）；上层 `runtimeMultiAgentVersionForThread` @ `:5924`→`:5943`（优先级 = `multi_agent_v2` feature > 模型声明 > `multi_agent` feature 回落 V1）；另有 `runtimeV2SubagentToolsEnabled` @ `:6004`（子代理工具面仅在模型声明 v2 时可见，与 review 的 v1 无关）。
- 差异：Rust 声明 `v1` ⇒ 版本由**模型**决定；Go fallback 下未声明 ⇒ 退回 **feature 回落**，若 `multi_agent_v2` 打开则会得到与 Rust 不同的版本。

**(c) `UseResponsesLite` —— responses-lite 请求形状**
- 消费（`model/` 内）：`model/responses_agent.go:1648`（`reasoning.Context = "all_turns"`）、`:1750`（lite ⇒ 不挂 hosted image-generation 工具）、`:1544-1545`（lite ⇒ `parallel_tool_calls=false`）、`:1901` + 定义 `:2144 addResponsesLiteHeader`（`x-openai-internal-codex-responses-lite: true`）。
- 消费（`appserver/`）：`turn_runtime.go:7318/7412/10952/11018`、`incremental_tools.go:57`、`web_search_runtime.go:89`、`image_generation_runtime.go:159`、`compact_remote.go:141`。
- 差异：Rust 为 `true` ⇒ review 请求走 lite 形状（含 `reasoning.context="all_turns"`、lite 头、`parallel_tool_calls=false`、不挂 hosted 工具）；Go fallback 下为 `false` ⇒ **review 请求走 full 形状**、不带头、（`web_search_runtime.go:89` 甚至会因 `false` 而改判是否走 standalone web search）。

### §2.3 可达性（何时真的走到 fallback）

`BundledModelsResponse()` @ `model/catalog.go:860-865`：**优先**从磁盘读 `models-manager/models.json`（`bundledModelCatalogPaths()` @ `:1374`，含 `CODEX_GO_MODELS_JSON` 与若干开发布局路径），成功即返回；**只有全部路径读不到 / 解析失败 / 空**（`loadBundledModelsResponse` @ `:1357-1372` 返回 `os.ErrNotExist` 或 err）才落 `fallbackBundledModelsResponse()`。

- Rust 把该 JSON `include_str!` 进二进制 ⇒ **Rust 无此漂移**；Go 的 fallback 是**手写字面量**，必须人工与 JSON 保持同值 —— 这正是本缺口产生的原因。
- `codex-auto-review` 是 **approval review / guardian** 的默认模型：`model/provider.go:17 DefaultApprovalReviewPreferredModel = "codex-auto-review"`，使用点 `appserver/guardian_reviewer.go:277`、`appserver/agent_runtime.go:444/449`；`runtimeModelMultiAgentVersion` 在 `r.services.Models == nil` 时也直接走 `model.BundledModelsResponse()`（`:6033`）。
- ⇒ 在**无 bundled `models.json` 的安装/离线环境**下，review 模型的三项落到 Rust 的不同值，缺口**可达生产**。

### §2.4 最小补丁规模

**1 个生产文件**（`model/catalog.go`，+3 行）+ **1 个测试文件**（`model/catalog_test.go`）。**≤5 文件，S。**

---

## §3 定级与 RC 方案

### §3.1 定级

**S** —— 单文件 3 行字面量 + 回归测试；无跨面依赖、无架构决策、不撞避让面（`model/` 为本车道可写面；不碰 `appserver/`、`tui/state.go`、`model/catalog.go` 的 Bedrock 段）。

### §3.2 建议测试形态（钉值，不钉“函数被调用”）

在既有 `TestFallbackCatalogCarriesModelsJSONFieldValues`（`catalog_test.go:2154`，已按 `bySlug` 装载 `codex-auto-review`）内追加一段，**断言即 Rust 值**：

```go
// models.json @ b17c74cfd5 gives codex-auto-review code_mode_only / v1 / responses-lite.
if review.ToolMode != ToolModeCodeModeOnly {
    t.Fatalf("codex-auto-review tool_mode = %q, want %q (models.json @ b17c74cfd5)", review.ToolMode, ToolModeCodeModeOnly)
}
if review.MultiAgentVersion != "v1" {
    t.Fatalf("codex-auto-review multi_agent_version = %q, want v1 (models.json @ b17c74cfd5)", review.MultiAgentVersion)
}
if !review.UseResponsesLite {
    t.Fatalf("codex-auto-review use_responses_lite = false, want true (models.json @ b17c74cfd5)")
}
if got := ResolveToolMode(review.ToolMode, map[string]bool{}); got != ToolModeCodeModeOnly {
    t.Fatalf("ResolveToolMode(codex-auto-review) = %q, want %q (no features on)", got, ToolModeCodeModeOnly)
}
```

**可选加强（推荐）**：把 §r87c 那条「两条来源一致」测法（`catalog_test.go:2100-2145`，现枚举 `supports_reasoning_summary_parameter` / `include_apps_usage_instructions`）的 `catalogJSON` 子集补上 `tool_mode` / `multi_agent_version` / `use_responses_lite`，对**同一 slug** 断言 fallback 与 models.json 解析路径三项相等 —— 把类别从「值漂移」提升到「来源不一致」，正是本缺口性质。

**不建议**改 `TestFallbackBundledCatalogCarriesGPT6FamilyLikeRust`（`:174-241`）的 GPT-6 家族循环（`:198-215`）：它是 v2 家族专属，review 是 v1，混进去会削弱原断言语义。新增独立块更清晰。

### §3.3 值级 RC 方案（撤生产接线 ⇒ FAIL ⇒ 恢复 ⇒ ok）

| RC | 撤掉 | 预期 FAIL 原文 |
|---|---|---|
| RC-a | 删 `ToolMode: ToolModeCodeModeOnly,` | `codex-auto-review tool_mode = "", want "code_mode_only" (models.json @ b17c74cfd5)`（以及 §3.2 第 4 条 `ResolveToolMode(...) = "direct", want "code_mode_only"`） |
| RC-b | 删 `MultiAgentVersion: "v1",` | `codex-auto-review multi_agent_version = "", want v1 (models.json @ b17c74cfd5)` |
| RC-c | 删 `UseResponsesLite: true,` | `codex-auto-review use_responses_lite = false, want true (models.json @ b17c74cfd5)` |

恢复口径：`git checkout -- model/catalog.go` 或逐文件还原后 `sha256` 须与补丁态一致；三条 RC 各贴 FAIL 段 + 恢复 `ok` 段。

---

## §4 现有测试为何覆盖不到（正向陈述 + 可复跑命令）

```
$ cd /home/jacks/jacks_dev/codex_go
$ git grep -n 'codex-auto-review' origin/main -- 'model/*.go'
origin/main:model/catalog.go:1041:				Slug:                          "codex-auto-review",
origin/main:model/provider.go:17:	DefaultApprovalReviewPreferredModel      = "codex-auto-review"
origin/main:model/provider.go:166:	return DefaultApprovalReviewPreferredModel
origin/main:model/review_model_test.go:14:		preferredID = "codex-auto-review"
$ git grep -c 'codex-auto-review' origin/main -- 'model/catalog_test.go'
origin/main:model/catalog_test.go:7
```

`catalog_test.go` 内 7 处引用分布：
- `:2100-2123`（`catalogJSON` 子集 + `bySlug`）—— 只带 `supports_reasoning_summary_parameter` / `include_apps_usage_instructions`，**不含**本三项；
- `:2181-2188` —— 只钉 `truncation_policy`；
- `:2191-2203` —— 只钉 `MaxContextWindow` / `ContextWindow`。

⇒ **无任何测试断言 `codex-auto-review` 的 `ToolMode` / `MultiAgentVersion` / `UseResponsesLite`**（家族循环 `:198-215` 的 four-slug 列表为 `gpt-6.*`，不含 review）——即 §1.3 探针能 FAIL 而 `go test ./model/` 仍绿的原因。

基线 `./model/` 失败集（`c9fbd10f` 干净树，供落主时对拍）：

```
$ cd /tmp/wt-syncl6-r88e && go test ./model/ -count=1
--- FAIL: TestRefreshBedrockAWSCredentialsRunsCommandOnceAndReloads (0.00s)
    responses_agent_test.go:4131: ... exec format error
--- FAIL: TestRefreshBedrockAWSCredentialsBoundsProviderRecoveryPerRequest (5.00s)
    responses_agent_test.go:4167: ... ec2imds: GetMetadata ...
FAIL	codex_go/model	30.466s
```

（2 条**环境型**既有红，与本项无关；落地后须 `diff` 为空 ⇒ 新增失败 0。）

---

## §5 残余与未决

1. **`gpt-5.2`（1015-1021）/ `gpt-5.4-mini`（1022-1039）** 同样三项零值，但 `models.json @ b17c74cfd5` **已无此二条** ⇒ **无 Rust 对位可查**。属既有 D 组 backlog（「多 `gpt-5.2` / `gpt-5.4-mini`」），**本项不动**。若后续要与 Rust 完全对齐需先定产品口径（队长已记 backlog）。
2. `gpt-5.5`（996-1014）三项为 Go 零值 / `false`，Rust 为显式 `null`/`null`/`false` ⇒ **无偏差**（Rust serde `Option`/`bool` 语义对照 Go 零值一致）。
3. 本缺口只在 **fallback 路径**（无 bundled `models.json`）显现；有磁盘 `models.json` 时由 JSON 路径给出正确值。用户可见影响面：**离线 / 未随包分发 `models.json` 的安装**下的 approval-review / guardian 行为（工具面可能退化为 `direct`、responses-lite 形状丢失）。
4. 路线 (c)（`//go:embed` 跟踪 `models.json`）仍**未开闸**；本报告不含其方案，仅在 §2.3 说明「不 embed ⇒ 本类漂移会持续复发」。

## §6 复跑命令清单（只读）

```bash
# Rust 真源
git -C /home/jacks/jacks_dev/codex show b17c74cfd5:codex-rs/models-manager/models.json > /tmp/syncl6/r88e/models.json

# Go 载体
git -C /home/jacks/jacks_dev/codex_go show c9fbd10f:model/catalog.go | sed -n '1040,1056p'
git -C /home/jacks/jacks_dev/codex_go grep -n 'codex-auto-review' c9fbd10f -- 'model/*.go'

# 探针（overlay，仓内零落盘）
cd /tmp/wt-syncl6-r88e
go test -overlay=/tmp/syncl6/r88e/overlay.json  ./model/ -run TestProbeAutoReviewCarriesModelsJSONFields -count=1 -v   # 期望 FAIL
go test -overlay=/tmp/syncl6/r88e/overlay2.json ./model/ -run TestProbeAutoReviewResolvedBehavior -count=1 -v         # 期望 PASS（打印 direct/null）
go test ./model/ -count=1   # 基线：2 条环境型既有红
```

---

**状态**：只读复核完成，**未动任何补丁 / ref**。等队长放行后按 §2.1 + §3.2 出 `update/r86_patches/syncl6_autoreview_fields.patch`（含 §3.3 三条值级 RC 原文 + 门禁 + `git apply --check`）。
