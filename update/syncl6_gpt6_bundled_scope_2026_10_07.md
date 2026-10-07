# syncl6 · round87 · scoping：GPT-6 家族在 Go `BundledModelsResponse()` fallback 目录中的缺口

- 车道：`syncl6`（Linux 节点 `de1bb1e71f8f7ad6969025798b555057`），派单 `msg-1791374872012803500-4976`
- Go 基线：`origin/main = 185d03c933086ddd2b0a1f498fdcf7f9d2683d70`
- Rust 对照：`/home/jacks/jacks_dev/codex`，`origin/main = b17c74cfd5ebb39fe70ffaff78de198120278636`（`git log -1 b17c74cfd5` → `Record telemetry for AGENTS.md changes made by apply_patch (#51652)`）
- 性质：**只读 scoping**，0 补丁 / 0 commit / 0 push / 0 ref 移动。实验只在 `/tmp/wt-syncl6-r87`（`--detach 185d03c9`）内做，做完 `git checkout -- model/catalog.go` 复原（`git status --porcelain` 空）。

---

## ① Go fallback bundled 目录的定义位置、条数、为何不跟踪 models.json

### 1.1 定义位置

```
$ git -C /home/jacks/jacks_dev/codex_go grep -n 'BundledModelsResponse\|fallbackBundledModelsResponse\|loadBundledModelsResponse' 185d03c9 -- 'model/catalog.go'
185d03c9:model/catalog.go:860:func BundledModelsResponse() ModelsResponse {
185d03c9:model/catalog.go:861:	if catalog, err := loadBundledModelsResponse(); err == nil && len(catalog.Models) > 0 {
185d03c9:model/catalog.go:864:	return fallbackBundledModelsResponse()
185d03c9:model/catalog.go:909:func fallbackBundledModelsResponse() ModelsResponse {
185d03c9:model/catalog.go:1260:func loadBundledModelsResponse() (ModelsResponse, error) {
```

- 入口 `model/catalog.go:860`：先试磁盘 `loadBundledModelsResponse()`（`:1260`），拿不到就返回 `fallbackBundledModelsResponse()`（`:909`）。
- **7 条**，全部硬编码在 `model/catalog.go:909-1010`：

| # | slug | 行 | DisplayName | visibility | priority | MultiAgentVersion |
|---|---|---|---|---|---|---|
| 1 | `gpt-5.6-sol` | :913 | GPT-5.6-Sol | list | 1 | v2 |
| 2 | `gpt-5.6-terra` | :925 | GPT-5.6-Terra | list | 2 | v2 |
| 3 | `gpt-5.6-luna` | :937 | GPT-5.6-Luna | list | 3 | v1 |
| 4 | `gpt-5.5` | :948 | GPT-5.5 | list | 7 | （空） |
| 5 | `gpt-5.2` | :968 | GPT-5.2 | list | 29 | （空） |
| 6 | `gpt-5.4-mini` | :974 | GPT-5.4-Mini | hide | 23 | （空） |
| 7 | `codex-auto-review` | :992 | Codex Auto Review | hide | 43 | （空） |

### 1.2 探针实测（原文，`/tmp` overlay，`185d03c9`）

```
$ cd /tmp/wt-syncl6-r87 && go test -overlay=/tmp/syncl6/overlay.json ./model/ -run TestZZProbeBundledResolution -count=1 -v
=== RUN   TestZZProbeBundledResolution
    zz_probe_bundled_test.go:11: loadBundledModelsResponse: models=0 err=file does not exist
    zz_probe_bundled_test.go:13: fallbackBundledModelsResponse: models=7
    zz_probe_bundled_test.go:15: effective BundledModelsResponse(): models=7
    zz_probe_bundled_test.go:17:   [0] slug=gpt-5.6-sol display="GPT-5.6-Sol" visibility=list priority=1 multiAgent="v2" ctx=272000 maxCtx=872000
    zz_probe_bundled_test.go:17:   [1] slug=gpt-5.6-terra display="GPT-5.6-Terra" visibility=list priority=2 multiAgent="v2" ctx=272000 maxCtx=872000
    zz_probe_bundled_test.go:17:   [2] slug=gpt-5.6-luna display="GPT-5.6-Luna" visibility=list priority=3 multiAgent="v1" ctx=272000 maxCtx=872000
    zz_probe_bundled_test.go:17:   [3] slug=gpt-5.5 display="GPT-5.5" visibility=list priority=7 multiAgent="" ctx=272000 maxCtx=272000
    zz_probe_bundled_test.go:17:   [4] slug=gpt-5.2 display="GPT-5.2" visibility=list priority=29 multiAgent="" ctx=272000 maxCtx=272000
    zz_probe_bundled_test.go:17:   [5] slug=gpt-5.4-mini display="GPT-5.4-Mini" visibility=hide priority=23 multiAgent="" ctx=272000 maxCtx=272000
    zz_probe_bundled_test.go:17:   [6] slug=codex-auto-review display="Codex Auto Review" visibility=hide priority=43 multiAgent="" ctx=272000 maxCtx=1000000
    zz_probe_bundled_test.go:21: GetDefaultModel = "gpt-5.6-sol"
ok  	codex_go/model	0.005s
```

⇒ 普通检出里 effective bundled 目录 = **fallback 的 7 条**，默认模型 `gpt-5.6-sol`。

### 1.3 为何 `models-manager/models.json` 不被跟踪

`loadBundledModelsResponse()` 只从**磁盘路径列表**读（`model/catalog.go:1278 bundledModelCatalogPaths()`）：

```
		add(os.Getenv("CODEX_GO_MODELS_JSON"))
		add(filepath.Join("models-manager", "models.json"))
		add(filepath.Join("..", "git", "codex", "codex-rs", "models-manager", "models.json"))
		add(filepath.Join("..", "codex-main", "codex-rs", "models-manager", "models.json"))
		// + 从 runtime.Caller(0) 目录逐级向上找 models-manager/models.json
```

证据（原文）：

```
$ git -C /home/jacks/jacks_dev/codex_go ls-tree -r 185d03c9 --name-only | grep -i 'models\.json'
(无输出, rc=1)                       # Go 仓不跟踪任何 models.json
$ grep -n -i models .gitignore        # (rc=1) .gitignore 里也没有
$ ls -la /home/jacks/jacks_dev/codex_go/models-manager
ls: cannot access 'models-manager': No such file or directory
$ git -C /home/jacks/jacks_dev/codex_go grep -n 'go:embed' 185d03c9 -- 'model/*.go'
185d03c9:model/catalog.go:22://go:embed prompt.md        # 只 embed prompt.md，没有 models.json
```

对照 Rust：Rust **把 models.json 编进二进制**——

```
codex-rs/models-manager/src/lib.rs:15:    serde_json::from_str(include_str!("../models.json"))
codex-rs/models-manager/src/model_info.rs:16:pub const BASE_INSTRUCTIONS: &str = include_str!("../prompt.md");
```

⇒ Go 侧只移植了 `prompt.md` 的 embed，没移植 `models.json` 的 embed，也没跟踪该文件；因此 `loadBundledModelsResponse` 在普通检出里恒返回 `file does not exist`，**fallback 就是唯一 bundled 目录**（`model/catalog.go:1293` 的几条 dev-checkout 相对路径在本机也都不命中）。

---

## ② 把 4 个 GPT-6 slug 补进 fallback 的最小改动集

### 2.1 落点

`model/catalog.go:909 fallbackBundledModelsResponse()`，在 `Models: []ModelInfo{`（`:911`）后插入 4 个 `ModelInfo` 字面量。**1 个文件**。

### 2.2 Rust 值来源（原文）

4 条定义在 `codex-rs/models-manager/models.json`（@`b17c74cfd5`，共 1602 行 / 11 条模型）：

```
$ git -C /home/jacks/jacks_dev/codex grep -n '"slug"' b17c74cfd5 -- codex-rs/models-manager/models.json
codex-rs/models-manager/models.json:4:      "slug": "gpt-6-astra",
codex-rs/models-manager/models.json:177:      "slug": "gpt-6.1-sol",
codex-rs/models-manager/models.json:353:      "slug": "gpt-6-sol",
codex-rs/models-manager/models.json:524:      "slug": "gpt-6-luna",
codex-rs/models-manager/models.json:691:      "slug": "gpt-5.6-sol",
...
```

### 2.3 各字段值（Rust 原文值，逐条）

| 字段（Go `ModelInfo`，`model/catalog.go:436`） | `gpt-6.1-sol` | `gpt-6-astra` | `gpt-6-sol` | `gpt-6-luna` |
|---|---|---|---|---|
| `display_name` | GPT-6.1-Sol | GPT-6-Astra | GPT-6-Sol | GPT-6-Luna |
| `description` | Latest workhorse model for coding and everyday work. | Frontier intelligence for the most demanding work. | Previous generation workhorse model. | Fast and affordable model for easier tasks. |
| `visibility` | list | list | list | list |
| `supported_in_api` | true | true | true | true |
| `priority` | **1** | **2** | **3** | **4** |
| `tool_mode` | code_mode_only | code_mode_only | code_mode_only | code_mode_only |
| `multi_agent_version` | **v2** | **v2** | **v2** | **v2** |
| `multi_agent_reasoning_effort` | xhigh | xhigh | null | null |
| `default_reasoning_level` | low | low | medium | medium |
| `supported_reasoning_levels`（Go 存 effort 串） | low,medium,high,xhigh,max,ultra | 同左 | 同左 | low,medium,high,xhigh,max |
| `service_tiers` | priority | priority | priority | priority |
| `default_service_tier` | null | null | priority | priority |
| `additional_speed_tiers` | fast | fast | fast | fast |
| `support_verbosity` / `default_verbosity` | true / low | true / low | true / low | true / low |
| `web_search_tool_type` | text_and_image | text_and_image | text_and_image | text_and_image |
| `truncation_policy` | tokens/10000 | tokens/10000 | tokens/10000 | tokens/10000 |
| `supports_image_detail_original` | true | true | true | true |
| `use_responses_lite` | true | true | true | true |
| `default_reasoning_summary` | none | none | none | none |
| `context_window` | 272000 | 272000 | 272000 | 272000 |
| `max_context_window` | 872000 | 872000 | 872000 | 872000 |
| `input_modalities` | text,image | text,image | text,image | text,image |
| `supports_parallel_tool_calls` | true | true | true | true |
| `supports_reasoning_effort_updates` | true | true | （缺省） | （缺省） |
| `supports_reasoning_summary_parameter` | true | true | true | true |
| `supports_search_tool` | true | true | true | true |
| `supports_experimental_context` | false | false | false | false |
| `node_repl_auto_review_required` | true | true | true | **false** |
| `minimal_client_version` | 0.153.0 | 0.153.0 | 0.155.0 | 0.155.0 |
| `shell_type` / `apply_patch_tool_type` / `comp_hash` | shell_command / freeform / 3000 | 同左 | 同左 | 同左 |
| `availability_nux` | 有（"Maximize usage with GPT-6.1 Sol. …"） | null | null | null |
| `guardian` / `upgrade` / `model_specialty` / `auto_review_model_override` | null | null | null | null |
| `include_skills/apps/plugin_usage_instructions` | false/false/false | 同左 | 同左 | 同左 |
| `available_in_plans` | 26 项 | 25 项 | 24 项 | 24 项 |

- `BaseInstructions`：Rust 用 per-model `model_messages.instructions_template`（GPT-6 是 "You are Codex, an agent based on **GPT-6**. …"，巨型文本）；Go fallback 现有 7 条**都只用** `BaseInstructions`（= `models-manager/prompt.md` 的 `//go:embed`，`model/catalog.go:20-23`），故新 4 条沿用同一常量即可。
- **价格 / 计费字段**：Rust `models.json` **完全没有**任何 price/cost/pricing 字段（`pricing-ish keys: []`），Go `model` 包也没有（`git grep -niE 'price|pricing' 185d03c9 -- 'model/*.go'` → rc=1）。派单里的「价格」在 v2 目录下**不存在载体**，无需填。
- Go 无载体的 Rust 字段：`minimal_client_version`、`available_in_plans`（`git grep -n 'minimal_client_version\|available_in_plans' 185d03c9 -- '*.go'` → rc=1）。可忽略。
- `effective_context_window_percent` 是 Go 侧字段（Rust 无），现有 7 条一律 95，新 4 条沿用 95。

### 2.4 必须一并决定：priority 口径

- Rust 用**全局** `priority`（`gpt-6.1-sol`=1 … `gpt-5.6-sol`=5、`gpt-5.6-terra`=8、`gpt-5.6-luna`=9、`gpt-5.5`=13、`codex-auto-review`=43），默认 = 排序后第一个 picker-visible（`manager.rs:172 remote_models.sort_by_key(|m| m.priority)` + `ModelPreset::mark_default_by_picker_visibility`）。⇒ **Rust 的离线默认模型现在是 `gpt-6.1-sol`**。
- Go fallback 现在是**自己重排**的 priority（5.6 家族 1/2/3、5.5=7、5.2=29、5.4-mini=23、auto-review=43），默认 = 排序后第一个可见（`markDefaultPresetByVisibility`，`model/catalog.go:1427`）。
- ⇒ 若照 Rust 把 GPT-6 放在 priority 1-4，Go fallback 的默认模型会从 `gpt-5.6-sol` 变成 `gpt-6.1-sol`（见 ③ 实测）。

---

## ③ 连带影响面（**实测**：在 `/tmp/wt-syncl6-r87` 模拟「加 4 条 + 采用 Rust priority」后跑测试）

模拟内容：在 fallback 插入上述 4 条（priority 1-4），并把 5.6 家族调成 Rust 的 5/8/9、5.5 调成 13（`git diff --stat` = `model/catalog.go | 60 ++++++--, 1 file changed, 56 insertions(+), 4 deletions(-)`）。`gofmt -l` 空、`go build ./...` rc=0。

### 3.1 基线（未改）失败集合

| 包 | 基线失败 |
|---|---|
| `./model/` | `TestRefreshBedrockAWSCredentialsRunsCommandOnceAndReloads`、`TestRefreshBedrockAWSCredentialsBoundsProviderRecoveryPerRequest`（环境型：`exec format error` / 无 IMDS） |
| `./tui/` | `TestSelectStartupTooltipMatchesRustPlanBranches` |
| `./tui/tea/` | `TestModelAppCommandUsesRustHistoryMessages` |
| `./app/` | `TestNoDaemonRejectionsLikeRust` |

### 3.2 改动后**新增**失败（原文）

`./model/`（+4）：

```
--- FAIL: TestFallbackBundledModelsMatchCurrentRustDefault (0.00s)
    catalog_test.go:160: default model = "gpt-6.1-sol"
--- FAIL: TestAmazonBedrockModelCatalog (0.00s)
    catalog_test.go:1397: Bedrock model openai.gpt-6.1-sol multi_agent_version = "v2", want ""
--- FAIL: TestBedrockCatalogKeepsUltraReasoningLikeRust (0.00s)
    catalog_test.go:1478: openai.gpt-6.1-sol ultra = true, want false (levels []string{"low", "medium", "high", "xhigh", "max", "ultra"})
--- FAIL: TestAmazonBedrockRuntimeCatalogDisablesWebSearchLikeRust (0.00s)
    catalog_test.go:1986: global.openai.gpt-6.1-sol MultiAgentVersion = "v2", want ""
```

- 期望值来源（都在 `model/catalog_test.go`）：`:157 TestFallbackBundledModelsMatchCurrentRustDefault` 期望默认 = `gpt-5.6-sol`；`:1353-1397` 的 `wantVersion` map 把 4 个 GPT-6 Bedrock ID 的 `multi_agent_version` 记为 `""`（注释明写「Go's fallback bundled catalog carries only the GPT-5.6 family」）；`:1465-1479 TestBedrockCatalogKeepsUltraReasoningLikeRust` 只把 GPT-5.6 Sol/Terra 记为有 ultra；`:1982-1992` 的 runtime 目录同理。
- 这 4 条**都是「快照式期望」，改动后要同步改成 Rust 的真值**（默认 → `gpt-6.1-sol`；4 个 GPT-6 Bedrock 条目的 `multi_agent_version` → `v2`；ultra → 4 条 GPT-6 都有）。

`./app/`（+2）：

```
--- FAIL: TestInteractiveWithoutPromptRunsLineSession (0.21s)
    app_test.go:864: stdout = "...  Model:               gpt-6.1-sol (reasoning low, summaries auto) ..."
--- FAIL: TestInteractiveUIStateUsesSelectedModelDefaultReasoningEffort (0.00s)
    app_test.go:970: default state = model "gpt-6.1-sol" reasoning "low"
```

（另加基线的 `TestNoDaemonRejectionsLikeRust`。）

### 3.3 **未**受影响的包

- `./tui/` 与 `./tui/tea/`：失败集合与基线**逐字相同**（0 新增）。选择器测试都用合成 options（如 `tui/tea/model_test.go:8868`、`tui/chatwidget/model_popup_no_default_marker_like_rust_test.go:15`），不消费 bundled 目录。
- `./parity/`：`CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1 -v` → **61 PASS / 1 SKIP / 0 FAIL**（`ok codex_go/parity`）。parity 只在目录清单里列 `models-manager`（`parity/rust_inventory_test.go:162`、`parity/rust_snapshot_test.go:218`），不比对模型条目。
- `go vet ./model/`：仅既有 `responses_agent.go:1462` lock-copy（与本改动无关）。

### 3.4 避让面

- `tui/state.go`：不消费 bundled 目录，只做 `s.Model` 字符串透传/展示（`tui/state.go:69`、`:139`、`:313`、`:766`）。本次模拟未触及、也未受影响 ⇒ 保持避让。
- 其它避让面（`appserver/`、`turn/`、`prompt/`、`sandbox/` 等）本次改动均不需要。

---

## ④ 定级与建议

**定级：M（形式），但内含一个需要你定的口径 → 建议先定口径再开工。**

- 形式面：落地文件 = `model/catalog.go`（+4 条）+ `model/catalog_test.go`（4 条期望）+ `app/app_test.go`（2 条期望）= **3 文件 ≤5**，符合开工门槛。
- 但缺口不是「加 4 条」这么简单：Go fallback 是 **Rust models.json 的手工简化快照**，且 priority 被本地重排过。照 Rust 补 GPT-6（priority 1-4）**必定**把 Go 离线默认从 `gpt-5.6-sol` 改成 `gpt-6.1-sol`，并连带改 6 条测试期望 —— 这是**行为/产品口径变更**（离线用户的默认模型与选择器顺序），不是纯机械补数据。

**三条候选路径（请你选）：**

- **(a) 最小对齐**：只补 4 条 GPT-6，采用 Rust 的全局 priority（1-4），同步更新 §3.2 的 6 条期望。→ 默认模型变为 `gpt-6.1-sol`，与 Rust 现行为一致。3 文件。**我推荐这条**（Go 的默认若长期停在 gpt-5.6-sol，与 Rust 用户看到的模型列表直接不一致）。
- **(b) 只补条目不改默认**：给 GPT-6 4 条 priority 排在 5.6 家族之后（如 4/5/6/7），保持 Go 默认 = `gpt-5.6-sol`。破坏面更小（`TestAmazonBedrockModelCatalog` / ultra / runtime 三条仍会变，`TestFallbackBundledModelsMatchCurrentRustDefault` 与 2 条 app 测试可不动），但**默认模型与 Rust 不一致**，属有意偏差，需你在报告里记一笔。
- **(c) 根本方案（记为 L，需架构决策）**：像 `prompt.md` 那样把 `models.json` `//go:embed`（或跟踪该文件），让 fallback 退化为兜底而非唯一来源。理由：当前形态下**每加一个模型都要手改 fallback + 修测试**，且必然持续漂移；`models.json` 有 11 条而 fallback 只有 7 条（缺 GPT-6×4 / daybreak×2，多出 Rust 已无的 5.2 / 5.4-mini），漂移已经存在。

**建议**：纳入同步范围，走 **(a)**（3 文件、M）；同时把 **(c)** 登记为独立 L 项（「Go bundled catalog 的 embed/跟踪策略」），因为它决定 fallback 是否长期还需要手工维护。

**未决问题（需你裁定）**：选 (a)/(b)/(c) 哪条；若选 (a)，是否接受「Go 离线默认模型 = `gpt-6.1-sol`」这一行为变更。

---

## 附：可复跑命令清单

```bash
# 基线 / sha
git -C /home/jacks/jacks_dev/codex_go rev-parse origin/main            # 185d03c9...
git -C /home/jacks/jacks_dev/codex rev-parse origin/main               # b17c74cfd5...

# ① fallback 定位与探针
git -C /home/jacks/jacks_dev/codex_go grep -n 'fallbackBundledModelsResponse' 185d03c9 -- 'model/catalog.go'
git -C /home/jacks/jacks_dev/codex_go ls-tree -r 185d03c9 --name-only | grep -i 'models\.json'   # 空
cd /tmp/wt-syncl6-r87 && go test -overlay=/tmp/syncl6/overlay.json ./model/ -run TestZZProbeBundledResolution -count=1 -v

# ② Rust 值
git -C /home/jacks/jacks_dev/codex grep -n '"slug"' b17c74cfd5 -- codex-rs/models-manager/models.json

# ③ 影响面（模拟后）
cd /tmp/wt-syncl6-r87 && go test ./model/ -count=1
cd /tmp/wt-syncl6-r87 && go test ./tui/ ./tui/tea/ ./app/ -count=1
CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1
```
