# syncl6 · r87c 交付：fallback 目录与 models.json 两条来源的布尔字段一致性（发现①）

- 派单：`msg-1791376862274370900-5267`（队长，round87）
- 车道：syncl6（Linux 节点 `de1bb1e71f8f7ad6969025798b555057`）
- 交付：**1 条补丁 / 2 文件 / 0 commit / 0 push / 0 ref 移动**
  - `update/r86_patches/syncl6_fallback_json_consistency.patch`（5880 B / sha256 `7480a89dc4c9543162b2fd59916ccc0d12ad473232cf02b6db9b9c60b4951d14`）
  - 本报告 `update/syncl6_fallback_json_consistency_2026_10_07.md`
- 基线：Go `origin/main` 起点 = **`ee2d4e1d`**（派单指定）；补丁 `git apply --check` 在 `ee2d4e1d` 干净 LF 树 = **exit 0**（另附对当前最新 `8b2453a9` 亦 exit 0 的实测，见 §5）

---

## §0 环境自检（原文）

```text
$ cd /home/jacks/jacks_dev/codex_go && git fetch origin --prune && git rev-parse origin/main
ee2d4e1db819e1d706eaea8b5da0386e85ea1a42          # 开工时
$ git status --porcelain
?? scripts/loc_report.sh                            # 既存 untracked，未动

$ git -C /home/jacks/jacks_dev/codex_go worktree add --detach /tmp/wt-syncl6-cons ee2d4e1d
$ cd /tmp/wt-syncl6-cons && git rev-parse HEAD
ee2d4e1db819e1d706eaea8b5da0386e85ea1a42
$ git status --porcelain                            # 改动前：干净
$ go version
go version go1.26.3 linux/amd64

# Rust 对照仓（模型数据真源）
$ cd /home/jacks/jacks_dev/codex && git rev-parse origin/main
b17c74cfd5ebb39fe70ffaff78de198120278636
```

> **基线漂移披露（非我造成）**：交付完成时 `origin/main` 已被并发车道推进到
> **`8b2453a9`**（`sync641: launch embedded with a warning from an elevated Windows terminal like Rust (#49855)`）。
> `git diff --stat ee2d4e1d..8b2453a9 -- model/` 为空 ⇒ **新提交未触碰我的写集**，
> 我的补丁对 `8b2453a9` 的 `git apply --check --cached` 亦为 exit 0（§5 有原文）。

改动后的工作树状态（只有我这两笔，无其它脏文件）：

```text
$ git status --porcelain
 M model/catalog.go
 M model/catalog_test.go
```

---

## §1 问题：同一 slug 同一字段，两条来源给出相反结果（可复跑）

### 1.1 Rust 真源

Rust 的 bundled catalog **就是 `models-manager/models.json` 本身**（编译期 `include_str!` + serde 解析，
所以显式写的值胜出、只有字段缺席才吃 `default_true`）：

```text
$ git -C /home/jacks/jacks_dev/codex show b17c74cfd5:codex-rs/models-manager/src/lib.rs | sed -n '13,17p'
/// Load the bundled model catalog shipped with `codex-models-manager`.
pub fn bundled_models_response()
-> std::result::Result<codex_protocol::openai_models::ModelsResponse, serde_json::Error> {
    serde_json::from_str(include_str!("../models.json"))
}
```

两个 `default_true` 字段（**只有这两个**；`include_skills_usage_instructions` / `include_plugin_usage_instructions`
是朴素 `#[serde(default)]`，不受影响）：

```text
$ git -C /home/jacks/jacks_dev/codex show b17c74cfd5:codex-rs/protocol/src/openai_models.rs | sed -n '435,442p'
    #[serde(default)]
    pub include_skills_usage_instructions: bool,
    #[serde(default)]
    pub include_plugin_usage_instructions: bool,
    #[serde(default = "default_true")]
    pub include_apps_usage_instructions: bool,
    /// Whether the model accepts the Responses API `reasoning.summary` parameter.
    #[serde(default = "default_true", skip_serializing_if = "is_true")]
    pub supports_reasoning_summary_parameter: bool,
```

`models.json` 里这 11 条的**实际值**（真源，非默认推断）：

```text
$ git -C /home/jacks/jacks_dev/codex show b17c74cfd5:codex-rs/models-manager/models.json > /tmp/syncl6/models_json_b17c74cfd5.json
$ python3 -c "
import json
for m in json.load(open('/tmp/syncl6/models_json_b17c74cfd5.json'))['models']:
    print(m['slug'], '| rs=', m.get('supports_reasoning_summary_parameter'), '| apps=', m.get('include_apps_usage_instructions'))
"
gpt-6-astra | rs= True | apps= False
gpt-6.1-sol | rs= True | apps= False
gpt-6-sol | rs= True | apps= False
gpt-6-luna | rs= True | apps= False
gpt-5.6-sol | rs= True | apps= True
gpt-5.6-terra | rs= True | apps= True
gpt-5.6-luna | rs= True | apps= True
gpt-daybreak-blue-latest | rs= True | apps= True
gpt-daybreak-red-latest | rs= True | apps= False
gpt-5.5 | rs= True | apps= True
codex-auto-review | rs= True | apps= False
```

⇒ `supports_reasoning_summary_parameter`：**11/11 显式 true**；`include_apps_usage_instructions`：**true 仅 5 条**
（5.6 三条 + daybreak-blue + 5.5）。

### 1.2 Go 两条来源

| 来源 | 落点 | default 语义 | 结果 |
|---|---|---|---|
| 磁盘 `models.json` 解析路径 | `loadBundledModelsResponse()` → `json.Unmarshal` → `ModelInfo.UnmarshalJSON`（`model/catalog.go:695`）内 `defaultTrueBool`（`:712`）/ `reasoningSummariesSupport`（`:714`），实现在 `:1366` / `:1377` | 字段缺席 ⇒ **true** | 与文件一致 |
| fallback 字面量路径 | `fallbackBundledModelsResponse()`（`model/catalog.go:909`，改成 `response := ModelsResponse{...}` 前为裸 `return ModelsResponse{...}`） | **完全绕过 `UnmarshalJSON`** ⇒ Go 零值 `false` | 与文件**不一致** |

⇒ 同一 slug 同一字段，两条来源结果相反（改动前）：

| 字段 | Go fallback（改动前） | Rust models.json | 相反条目数 |
|---|---|---|---|
| `supports_reasoning_summary_parameter` | `false`（全部 11 条） | `true`（全部） | **9 条共有 slug 全中**（5.2 / 5.4-mini 无对照） |
| `include_apps_usage_instructions` | `false`（全部 11 条） | 4 条共同 slug 为 `true` | **4 条**（5.6-sol / 5.6-terra / 5.6-luna / 5.5） |

**行为影响（非文案问题）**：`SupportsReasoningSummaries` 是生产消费点
`model/responses_agent.go:1592`（`if !info.SupportsReasoningSummaries { return nil }`）的门。
离线默认模型（null-models.json 环境）走 fallback ⇒ 改动前 Go **不发** `reasoning.summary` 参数，与 Rust 相反。

---

## §2 修法（2 文件，≤5 文件约束内）

`model/catalog.go`：

```diff
 func fallbackBundledModelsResponse() ModelsResponse {
-	return ModelsResponse{
+	response := ModelsResponse{
 		Models: []ModelInfo{
@@ 末尾（原函数体闭合处）
 		},
 	}
+	// The fallback catalog below is the offline mirror of the bundled
+	// models-manager/models.json catalog (Rust include_str!s that file into the
+	// bundled models and parses it via serde), but the literals below bypass
+	// ModelInfo.UnmarshalJSON, where Rust's two
+	// `#[serde(default = "default_true")]` booleans are reproduced
+	// (catalog.go:712/714). Stamp the catalog's values on so the same field
+	// cannot read false here and true on the models.json path.
+	applyBundledCatalogBooleans(&response)
+	return response
 }
+
+// applyBundledCatalogBooleans fills the two serde-default-true booleans
+// (codex-rs/protocol/src/openai_models.rs:438/441) of the hand-written fallback
+// catalog with the values codex-rs/models-manager/models.json @ b17c74cfd5
+// carries: supports_reasoning_summary_parameter is true for every bundled
+// entry, while include_apps_usage_instructions is on only for the 5.6 family
+// and gpt-5.5. Slugs that catalog no longer lists (the legacy gpt-5.2 /
+// gpt-5.4-mini fallbacks) keep Rust's model_info_from_slug descriptor, i.e.
+// reasoning summaries on and no apps guidance (openai_models.rs:1072-1075).
+func applyBundledCatalogBooleans(response *ModelsResponse) {
+	appsUsageInstructions := map[string]bool{
+		"gpt-5.6-sol":   true,
+		"gpt-5.6-terra": true,
+		"gpt-5.6-luna":  true,
+		"gpt-5.5":       true,
+	}
+	for i := range response.Models {
+		model := &response.Models[i]
+		model.SupportsReasoningSummaries = true
+		model.IncludeAppsUsageInstructions = appsUsageInstructions[model.Slug]
+	}
+}
```

`model/catalog_test.go` 新增 `TestBundledFallbackCarriesCatalogJSONBooleans`（`:2099`，+59 行）：
把 `models.json`（b17c74cfd5）里 9 条共有 slug 的两个字段值以 JSON 字面量入测试，
**走正常解析路径** `json.Unmarshal` 得到源值，再逐 slug 与 `fallbackBundledModelsResponse()` 比对；
2 条 Go-only slug（`gpt-5.2` / `gpt-5.4-mini`）与 `ModelInfoFromSlug()`（= Rust `model_info_from_slug`）比对。

### §2.1 两处设计选择（**请队长裁定/确认**）

1. **没有采用「纯 defaulting ⇒ 两个字段全 true」的读法**。理由是它会让
   `include_apps_usage_instructions` 在 **5 条**（gpt-6.1-sol / 6-astra / 6-sol / 6-luna / codex-auto-review）
   上与 `models.json` 显式 `false` **相反** ⇒ 目标「两条来源一致」反而被破坏、并**新增** 5 处 Rust 偏差。
   我按「fallback 是 Rust bundled catalog 的离线镜像（Rust 侧 = `include_str!(models.json)` + serde）」
   取 **models.json 的实际值**。若你要的是「不指定即 default_true」的字面语义（即 apps 也全 true），
   把 `applyBundledCatalogBooleans` 第二行换成 `= true` 即可，1 行改动、测试同步换断言；请给一句话裁定。
2. **2 条 Go-only slug**：catalog 已无对照，取 Rust `model_info_from_slug`
   （`openai_models.rs:1072-1075`：`rs=true`、`apps=false`）⇒ `rs` 由 false 变 true、`apps` 维持 false（零行为变化）。

---

## §3 门禁（原文）

```text
$ gofmt -l model/catalog.go model/catalog_test.go
(空)

$ go build ./...
(无输出) → rc=0

$ go vet ./model/
model/responses_agent.go:1462:11: assignment copies lock value to clone: codex_go/model.ResponsesAgentRunner contains sync.Mutex
# 仅既存告警（非我的补丁，基线同）；rc=0
```

### 3.1 整包基线对拍（判据：新增失败 0）

**改动前**（`ee2d4e1d` 干净树，`env -u TERM`）：

```text
$ go test ./model/ -count=1
--- FAIL: TestRefreshBedrockAWSCredentialsRunsCommandOnceAndReloads (0.00s)
    responses_agent_test.go:4131: refresh error = bedrock aws auth refresh failed: fork/exec .../aws-refresh.sh: exec format error
--- FAIL: TestRefreshBedrockAWSCredentialsBoundsProviderRecoveryPerRequest (5.00s)
    responses_agent_test.go:4167: first refresh error = ... no EC2 IMDS role found, operation error ec2imds: GetMetadata, request canceled, context deadline exceeded
FAIL	codex_go/model	29.499s
$ go test ./app/ -count=1
ok  	codex_go/app	12.870s
```

**改动后**：

```text
$ go test ./model/ -count=1
--- FAIL: TestRefreshBedrockAWSCredentialsRunsCommandOnceAndReloads (0.00s)
--- FAIL: TestRefreshBedrockAWSCredentialsBoundsProviderRecoveryPerRequest (5.00s)
FAIL	codex_go/model	29.263s
$ go test ./app/ -count=1
ok  	codex_go/app	12.798s

$ diff <(grep -oE "^--- FAIL: [A-Za-z0-9_]+" base_model.txt | sort) \
       <(grep -oE "^--- FAIL: [A-Za-z0-9_]+" after_model.txt | sort)
(空)          → 新增失败 0
```

### 3.2 连带面（额外自查，非派单门禁）

```text
$ CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1
ok  	codex_go/parity	0.814s
$ ... -v | grep -c "^--- PASS[^ ]*"      → 190（含子测试）
$ ... -v | grep -c "SKIP"                → 1
$ ... -v | grep -c "^--- FAIL"           → 0

$ go test ./tui/ -count=1                # 模型选择器消费 BundledModelsResponse()
--- FAIL: TestSelectStartupTooltipMatchesRustPlanBranches (0.00s)
    tooltips_test.go:138: linux paid app tooltip = "Try the **Desktop app** on Linux: ...", true, want none
# 用 git stash 回到基线复跑同一命令：同一 FAIL、同一行 ⇒ 与本次改动无关（Linux 环境型），失败集合相同
```

`BundledModelsResponse()` 的生产消费点（`grep -rn "BundledModelsResponse()" --include=*.go`）：
`tui/model_picker.go:61`、`model/{responses_agent.go:1661,provider.go:287/291,models_endpoint.go:291,lazy_models.go:32/43,api.go:227/276,catalog.go:1097}`、
`exec/{exec.go:1469/1504/4545/6676/6684/6704/6804,agent_controller.go:322/359/387/419/438}`、
`appserver/{turn_runtime.go:11008,runtime_router.go:5921/6033,agent_controller.go:326}`、
`app/{interactive.go:4396/4401/4406,app.go:1606}`。其中 `catalog.go:1097` = `AmazonBedrockModelCatalog()` 经
`bedrockModel()` 继承 bundled 条目 ⇒ Bedrock 目录也一并修正（**未触碰** `bedrockModel` / `normalizeBundledBedrockCatalog`）。

---

## §4 值级 RC（原文）

固定后 `model/catalog.go` sha256 = `302e694a43428063c13be3b1c57cc6dcb291404146973d567da796b6809cf6df`。

**RC-1：撤生产接线**（`applyBundledCatalogBooleans(&response)` 一行）⇒ FAIL：

```text
$ go test ./model/ -run TestBundledFallbackCarriesCatalogJSONBooleans -count=1
--- FAIL: TestBundledFallbackCarriesCatalogJSONBooleans (0.00s)
    catalog_test.go:2138: gpt-6.1-sol: supports_reasoning_summary_parameter = false on the fallback path, true on the models.json path
FAIL
FAIL	codex_go/model	0.006s
FAIL
```

**RC-2：只撤 apps 赋值（`model.IncludeAppsUsageInstructions = appsUsageInstructions[model.Slug]`）** ⇒ FAIL：

```text
$ go test ./model/ -run TestBundledFallbackCarriesCatalogJSONBooleans -count=1
--- FAIL: TestBundledFallbackCarriesCatalogJSONBooleans (0.00s)
    catalog_test.go:2142: gpt-5.6-sol: include_apps_usage_instructions = false on the fallback path, true on the models.json path
FAIL
FAIL	codex_go/model	0.006s
FAIL
```

**恢复**（从保存副本还原 + sha256 校验）：

```text
$ cp /tmp/syncl6/r87c/catalog.go.fixed model/catalog.go && sha256sum model/catalog.go
302e694a43428063c13be3b1c57cc6dcb291404146973d567da796b6809cf6df  model/catalog.go    # 与改动后一致
$ go test ./model/ -run TestBundledFallbackCarriesCatalogJSONBooleans -count=1
ok  	codex_go/model	0.006s
```

---

## §5 行为探针（`-overlay`，探针只在 `/tmp`）

探针 `/tmp/syncl6/r87c/probe/zz_probe_fallback_consistency_test.go`（`package model`，overlay 注入，不落仓），
走**生产入口** `BundledModelsResponse()` + **生产消费者** `responsesReasoningParam`：

```text
$ cd /tmp/wt-syncl6-cons && go test -overlay=/tmp/syncl6/r87c/probe/overlay.json ./model/ \
    -run TestZZProbeFallbackBooleansOnProductionPath -count=1 -v
=== RUN   TestZZProbeFallbackBooleansOnProductionPath
    zz_probe_fallback_consistency_test.go:39: production path: gpt-6.1-sol -> effort="low" summary="" context="all_turns"
--- PASS: TestZZProbeFallbackBooleansOnProductionPath (0.00s)
PASS
ok  	codex_go/model	0.004s
```

探针断言：① 该次 `BundledModelsResponse()` 走的是 fallback 分支（以 Go-only slug `gpt-5.2` 存在为证）；
② 11 条 `SupportsReasoningSummaries` 全 true；③ `include_apps_usage_instructions` 恰为 4 条 true；
④ 生产消费者对离线默认 `gpt-6.1-sol` **确实产出了** reasoning 参数。

**探针 RC**（同一次撤掉接线）⇒ 生产路径直接证伪：

```text
$ go test -overlay=/tmp/syncl6/r87c/probe/overlay.json ./model/ \
    -run TestZZProbeFallbackBooleansOnProductionPath -count=1
--- FAIL: TestZZProbeFallbackBooleansOnProductionPath (0.00s)
    zz_probe_fallback_consistency_test.go:22: gpt-6.1-sol: SupportsReasoningSummaries=false on the production offline path
FAIL
FAIL	codex_go/model	0.006s
FAIL
```

---

## §6 交付物校验

```text
$ wc -c < /tmp/syncl6/r87c/syncl6_fallback_json_consistency.patch
5880
$ sha256sum /tmp/syncl6/r87c/syncl6_fallback_json_consistency.patch
7480a89dc4c9543162b2fd59916ccc0d12ad473232cf02b6db9b9c60b4951d14
$ grep -c $'\r' /tmp/syncl6/r87c/syncl6_fallback_json_consistency.patch
0                                                     # CRLF 计数 0（LF 树）

# 干净 LF 树（git worktree add --detach，未建任何 ref）@ 派单基线 ee2d4e1d：
$ cd /tmp/wt-syncl6-applycheck && git status --porcelain
(空)
$ git apply --check /tmp/syncl6/r87c/syncl6_fallback_json_consistency.patch
(无输出) exit=0
$ git apply ... && sha256sum model/catalog.go model/catalog_test.go
302e694a43428063c13be3b1c57cc6dcb291404146973d567da796b6809cf6df  model/catalog.go
54f921e53082e010d7cec0f92ae7bcc6766f2c32b8da1e2d437e404e305737f0  model/catalog_test.go
# 与工作树逐字节一致；该临时 worktree 已 git worktree remove 清理

# 对交付时最新 main 8b2453a9 的追加校验（用临时 index，不动任何 ref/工作树）：
$ GIT_INDEX_FILE=<tmp> git read-tree ee2d4e1d && git apply --check --cached <patch>   → exit 0
$ GIT_INDEX_FILE=<tmp> git read-tree 8b2453a9 && git apply --check --cached <patch>   → exit 0
$ git diff --stat ee2d4e1d..8b2453a9 -- model/catalog.go model/catalog_test.go
(空)
```

diffstat：`model/catalog.go | 33 +++-`、`model/catalog_test.go | 59 +++`，共 +91 / -1。

---

## §7 未落地项 / 需裁定

1. **发现②（既有条目 22 处字段漂移：`codex-auto-review.max_context_window` 1000000 vs 872000、
   `gpt-5.5`/`codex-auto-review` 的 `truncation_policy` bytes vs tokens 等）**：
   **未动**，按你的口径等「先报要改哪些行 → 你批准 → 再动」。本单**零**发现②改动（补丁里不含任何 5.2/5.4-mini/5.5/auto-review 的字段纠偏）。
2. **路线 (c)**（`//go:embed` 跟踪 `models.json`）：**未开工**（未批准）。
3. **§2.1 第 1 条的语义选择**：我按「与 Rust models.json 的实际值一致」实现（apps 4 条 true）；
   若你要「纯 default_true ⇒ apps 全 true」，我 1 行切换并同步测试断言，请一句话裁定。
4. `appserver/` 的消费点（retire-on-replace / stale-MCP-refresh / EMA-suspended gate）等**未触碰**；
   `model/catalog.go` 的 Bedrock 段未触碰。
5. 本次只改 `SupportsReasoningSummaries` / `IncludeAppsUsageInstructions` 两个字段；
   其余「系统性未承载」字段（`shell_type`/`comp_hash`/`supports_search_tool`/… ）仍为存量偏差，未在本单扩范围。

---

## §8 复跑清单（供独立复核）

```bash
git -C /home/jacks/jacks_dev/codex_go worktree add --detach /tmp/wt-syncl6-recheck ee2d4e1d
cd /tmp/wt-syncl6-recheck
git apply /path/to/project/update/r86_patches/syncl6_fallback_json_consistency.patch
gofmt -l model/catalog.go model/catalog_test.go          # 空
go build ./...                                           # rc=0
go vet ./model/                                          # 仅 responses_agent.go:1462 既存告警
env -u TERM go test ./model/ -count=1                    # 仅 2 条既存 Bedrock/AWS 环境失败
env -u TERM go test ./app/ -count=1                      # ok
env -u TERM go test ./model/ -run TestBundledFallbackCarriesCatalogJSONBooleans -count=1 -v   # PASS
CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1               # ok
```
