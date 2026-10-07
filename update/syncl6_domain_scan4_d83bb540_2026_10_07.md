# syncl6 · r88d · 域扫描 round 4 —— `model/` + `telemetry/` + `config/`（只读）

- 派单：`msg-1791378939196472700-5576`（r88d，只读域扫描，0 补丁 / 0 commit / 0 push / 0 ref）
- 车道：syncl6（Linux 节点 `de1bb1e71f8f7ad6969025798b555057`）
- 上游 pin：**`d83bb540ec64bf6b009bca0283b0be91ea33f26a`**（Rust 枚举 pin，`git rev-parse d83bb540ec` 实测）
- 性质：**只读**；本文件为新建产物，未改任何 Go 源码。

## 0. 环境自检（原文）

```
$ cd /home/jacks/jacks_dev/codex_go && git rev-parse HEAD
a32e1c35facdbfec20887cce46da126d6ac0e68b
$ git rev-parse origin/main
d12d00dde5732a8f910db636b5452949a4594785      # sync648（含本车道 r88c）
$ git status --porcelain
?? scripts/loc_report.sh                        # 既存 untracked
$ cd /home/jacks/jacks_dev/codex && git rev-parse origin/main
3421c660d043e39d1bff8cee136c0ceeb006177c       # 远端已前进；本单按派单固定 pin 取样
$ git rev-parse d83bb540ec
d83bb540ec64bf6b009bca0283b0be91ea33f26a
```

## 1. 口径与复跑命令

域路径（Rust 侧，由 Go 包内 `codex-rs/...` 引用注释反推，实证见 G.1）：

| Go 包 | Rust 载体路径（本单取样的域） |
|---|---|
| `model/` | `codex-rs/model-provider/**`、`codex-rs/models-manager/**`、`codex-rs/protocol/src/openai_models.rs`、`codex-rs/codex-api/**`、`codex-rs/aws-auth/**` |
| `telemetry/` | `codex-rs/otel/**` |
| `config/` | `codex-rs/config/**`、`codex-rs/cloud-config/**` |

**窗口**：`--no-merges --since='30 days ago'`（2026-09-07 → `d83bb540ec`）。选 30 天是因为上一次 pin `5b0b253035`→`d83bb540ec` 仅 5 个提交（同日内 3.6 小时），窗太小无信息量；30 天窗口是本域「一个可复核的滚动窗」，条数稳定（202 条）。

```bash
# ① 域内提交
git -C /home/jacks/jacks_dev/codex log --no-merges --since='30 days ago' --format='%H|%s' \
  -- codex-rs/otel codex-rs/config codex-rs/cloud-config codex-rs/model-provider \
     codex-rs/models-manager codex-rs/protocol/src/openai_models.rs codex-rs/codex-api codex-rs/aws-auth
# ② 每条文件数（总文件数，判 ≤5）
git show --numstat --format= <sha> | grep -c .
# ③ 已落地判据（Go 侧）
git -C /home/jacks/jacks_dev/codex_go log --oneline origin/main --grep '#<PR>' -i
git -C /home/jacks/jacks_dev/codex_go log --oneline -S '<symbol>' origin/main
```

**漏斗**：

| 阶段 | 条数 |
|---|---|
| 域内提交（30 天窗口，`--no-merges`） | **202** |
| ├ 总文件 ≤5（可开工粒度） | **41** |
| └ 总文件 >5（L，本轮排除） | 161 |
| 41 条中：已落地（Go 有 `syncNNN`/`-S` 命中） | **24** |
| 41 条中：已等价（Go 机制在位，值/语义一致） | **5** |
| 41 条中：纯测试 / 无载体 → N/A | **5** |
| 41 条中：**真缺口候选** | **7** |
| └ 其中落在本域（`model/`/`telemetry/`/`config/`）可开工 | **3**（`#47590` `#47873` `#47657`） |
| └ 落在本域之外（`realtime/`、`codexapi/`+`turn/` 等） | **4**（`#46922` `#47956` + 下述 family 2 条） |

> ⚠ 口径声明：本单**未**按台账「排除清单」逐条剔除，而是按五分类**如实标注**每条的去向；凡属他车域（`tui/`、`appserver/`、`turn/`、`codexapi/`、`realtime/`、`execserver/`、`sandbox/`）者已在「去向」列写明。

## 2. 主表：41 条 ≤5 文件域内提交（五分类）

五分类标签 = **已落地** / **已等价** / **纯测试** / **无载体** / **真缺口候选**。

| # | PR# | sha（自解） | 文件 | 语义（上游一句话） | Go 载体 / 判据（`file:line` 或 0 命中原文） | 分类 |
|---|---|---|---|---|---|---|
| 1 | `#48130` | `0b430baeb4` | 1 | cloud config loader lifetime 测试改用 request notifications | Go `ed1f1866 sync203: no Go surface for the cloud config loader lifetime test` | 已落地 |
| 2 | `#47122` | `171dd0d39c` | 1 | OpenAI file blob 上传超时 60s→5min | Go `66f02a12 sync146`（与 #47393/#47926 同批） | 已落地 |
| 3 | `#49369` | `2a34aef794` | 1 | Bedrock GPT-6 Sol 目录测试期望 multi-agent V2（纯测试） | Go `model/catalog_test.go:1462` `AmazonBedrockGPT6SolModelID: "v2"` 已断言 | 已等价 |
| 4 | `#47902` | `975d30c043` | 1 | config 合并避免重复 clone 表（Rust 借用期性能重构） | Go `config/config.go:3071 mergeConfigMapsAt` 用 map 引用 + 单点 `cloneConfigValue`，无对应拷贝面 | 已等价 |
| 5 | `#46570` | `c223ff3170` | 1 | 远端模型拉取时长按 auth 模式打 tag | Go `model/models_endpoint.go:94 metricsAuthMode()`；`c84d8e50 sync49` | 已落地 |
| 6 | `#49813` | `d4a475adda` | 1 | 支持 AWS GovCloud region（Mantle） | Go `3151f0be sync430: port the Amazon Bedrock account RPC family` | 已落地 |
| 7 | `#47904` | `f0f38b68f7` | 1 | config key alias 支持嵌套 canonical 路径 | Go `368beeb2 sync319` | 已落地 |
| 8 | `#50354` | `f4e18a95bb` | 1 | alias 归一化跳过无关子树 | Go `368beeb2 sync319: skip unrelated subtrees during config alias normalization` | 已落地 |
| 9 | `#47926` | `0037b39cc1` | 2 | file blob 上传对 HTTP 502/504 重试 | Go `66f02a12 sync146` | 已落地 |
| 10 | `#44492` | `102fc57e4a` | 2 | 区分 HTTP quota 错误与限流 | Go `86486314 sync31`（已结案 residual） | 已落地 |
| 11 | `#48686` | `41f9084b30` | 2 | 从 info 日志移除 WS headers 与 tool payload | Go 0 命中（本车道 `update/syncl6_triage_48686_49564_49678_2026_10_07.md` §1 已判） | 无载体 |
| 12 | `#51460` | `44984d2081` | 2 | 已有 call 激活期间重试 realtime sideband 连接 | Go `realtime/existing_call_test.go:273`（`d5568ee9 sync374-376`） | 已落地 |
| 13 | `#46519` | `4b9e7f6473` | 2 | sampler / model catalog 超时测试改用 paused time | 纯测试；Go `git grep -n 'pausedTime' -- 'model/*.go'` → **0 命中**（无需对位） | 纯测试 |
| 14 | `#47908` | `5c8fc15cc9` | 2 | `tui.whimsy` 别名为 `tui.effects.starfield` | Go `368beeb2 sync319` 同族；消费面在 `tui/`（他车域） | 已落地 |
| 15 | `#46032` | `73bf181272` | 2 | 托管配置只接受 **forced** macOS preferences | Go `git grep -n 'CFPreferences\|CoreFoundation' -- '*.go'` → **0 命中**；Go 无 macOS 托管配置加载层 | 无载体 |
| 16 | `#43604` | `8e694e955a` | 2 | 从 models.json 移除 `base_instructions` | Go `model/catalog.go:744-745` 空值回落到共享 `BaseInstructions` 常量；观测行为一致 | 已等价 |
| 17 | **`#47590`** | `ae132dc50a` | 2 | 数字型 custom reasoning effort 以 **JSON number** 发送 | Go `model/responses_agent.go:329-333` `responsesReasoning{Effort string \`json:"effort,omitempty"\`}` ⇒ **始终发字符串** | **真缺口候选（S）** |
| 18 | `#47757` | `b0a9dcb843` | 2 | tool telemetry product SKU 改 allowlist | Go `mcp/catalog_telemetry.go:99 BoundedProductSKU`（`codex` 保留 / 其余 → `other`） | 已等价 |
| 19 | `#49295` | `cf12c86dc5` | 2 | 配置 fingerprint canonicalization 简化 | Go `b78a6898 sync583` | 已落地 |
| 20 | `#51440` | `f6cf05af1d` | 2 | WS error 事件遵守 Retry-After | Go `8d083ba7 sync359` | 已落地 |
| 21 | `#51220` | `28b91c7c31` | 3 | 遵守 OTLP metrics temporality 偏好 | Go `5c03bab0 sync362`（`telemetry/` 面） | 已落地 |
| 22 | `#48200` | `dd9a7101d9` | 3 | Responses header 转换抽到共享模块 | Go `1c08686c sync228` | 已落地 |
| 23 | `#49675` | `ed0cc1a4ab` | 3 | Responses 路由字段在大输入前序列化 | Go `f6aecaf6 sync597` | 已落地 |
| 24 | `#47903` | `0b78ddb035` | 4 | key alias 归一化移到合并之前 | Go `config/config.go:3110/3127`（`mergeConfigMapsAt` 内逐层 `normalizeConfigKeyAliases`）（`cb061915 Align config layer merging with Rust merge_toml_values`） | 已等价 |
| 25 | `#48229` | `10fd75bba7` | 4 | 抽出 Responses failure 解析模块（纯重构） | Rust 仅文件拆分（`responses.rs` 228→38 行 + 新 `responses_error.rs`）；Go 无对应模块边界 | 无载体 |
| 26 | `#47393` | `2c3295306f` | 4 | OpenAI file blob 上传瞬时失败重试 | Go `66f02a12 sync146` | 已落地 |
| 27 | **`#47873`** | `4021746a15` | 4 | 记录 deferred tool namespace 片段截断前后指标 | Go `telemetry/metric_buckets.go:9-14` 自述「the Go port **does not emit that metric family yet**」 | **真缺口候选（M）** |
| 28 | `#50418` | `44dd77b71e` | 4 | 失败 Responses 事件遵守 Retry-After | Go `f2715b53 sync344` | 已落地 |
| 29 | `#49910` | `819efd7273` | 4 | 保留非法 TUI keybinding 的校验错误 | Go `4d860670 sync373`（`tui/` 消费面，他车域） | 已落地 |
| 30 | `#47038` | `87bc50f9d4` | 4 | 为 user agents/telemetry 缓存 OS discovery | Go `ea77cba4 sync147 audited OS-discovery N/A` | 已落地 |
| 31 | `#45928` | `977193486d` | 4 | 限制 catalog decode 错误 + 归类请求超时 | Go `model/models_endpoint.go:706 decodeModelsEndpointResponse` 不回显 body；refresh 为 fire-and-forget（错误不外露 ⇒ 无分类面） | 已等价 |
| 32 | `#48469` | `b334d5b3f2` | 4 | 更多终端默认复制选区 | Go `ad588229 sync268` | 已落地 |
| 33 | `#50525` | `b65ab465ce` | 4 | strict config 校验拒绝未知 TUI key | Go `42fbf7f3 sync533` | 已落地 |
| 34 | **`#46922`** | `d1e3f9dfe3` | 4 | realtime V3 transcript 在 handoff 时对账 | Go `realtime/` 包有 V3 sideband（`realtime/existing_call_test.go:279`），**无 transcript 对账**；域外 | **真缺口候选（M，域外）** |
| 35 | `#47924` | `1866e51677` | 5 | project trust 查找路径显式化 + 延迟 root 解析 | Go `config/config.go:2447 ProjectTrustLookupFromNativePath` + `:2513 activeProjectTrustTarget`（`b8a73f45 sync150` 家族） | 已等价 |
| 36 | `#46508` | `2b84296288` | 5 | auth 变更后、turn 前刷新模型目录 | Go `model/models_endpoint.go:417 RefreshAfterAuthChange` + `model/api.go:232-238` + `appserver/runtime_router.go:3787/3793`（`de3810e2 sync42`） | 已落地 |
| 37 | `#45602` | `31ffe2bc9a` | 5 | 修正 throttling / quota 重试归类 | Go `4722050c sync62` | 已落地 |
| 38 | **`#47956`** | `51d66fdd3b` | 5 | image edit 请求支持 file reference | Go `codexapi/images.go:12` `ImageEditRequest.Images []ImageURL`（仅 `image_url`），`turn/image_generation.go:242` 传 `recentImageURLs` ⇒ **无 `file_id` 面**；域外（`codexapi/`+`turn/`） | **真缺口候选（S/M，域外）** |
| 39 | **`#47657`** | `5bae5b563e` | 5 | Bedrock GovCloud 用受限默认目录 | Go `model/provider.go:320` 只有 region allowlist；**无** host 前缀判定的 govcloud 目录裁剪（0 命中 `staticGov`/`us-gov-` 目录） | **真缺口候选（S）** |
| 40 | `#45441` | `b6a5d5bb14` | 5 | Guardian 跨采样保留父 response id | Go `164c0f16 sync98` | 已落地 |
| 41 | `#51547` | `e95abcdf49` | 5 | Windows MXC sandbox opt-out | Go `4481ffd8 sync389` | 已落地 |

## 3. 三条「真缺口候选」在本域（可开工）的落点与最小形态

> 依派单口径：**先只读报不落地**，等队长看写集与避让面后授权。

### 3.1 `#47590` — 数字型 custom reasoning effort 需按 JSON number 发送（S，`model/`）

Rust 原文（`ae132dc50a`，`codex-rs/codex-api/src/common.rs`）：

```
+    #[serde(
+        skip_serializing_if = "Option::is_none",
+        serialize_with = "serialize_reasoning_effort"
+    )]
     pub effort: Option<ReasoningEffortConfig>,
...
+fn serialize_reasoning_effort<S>(effort: &Option<ReasoningEffortConfig>, serializer: S)
+    -> Result<S::Ok, S::Error> {
+    if let Some(ReasoningEffortConfig::Custom(value)) = effort
+        && let Ok(value) = value.parse::<u64>()
+    { return serializer.serialize_u64(value); }
+    effort.serialize(serializer)
+}
```

Go 现状（`origin/main`）：

```
$ git grep -n 'type responsesReasoning' -A5 origin/main -- model/responses_agent.go
origin/main:model/responses_agent.go:329:type responsesReasoning struct {
origin/main:model/responses_agent.go:330:	Effort  string `json:"effort,omitempty"`
origin/main:model/responses_agent.go:331:	Summary string `json:"summary,omitempty"`
origin/main:model/responses_agent.go:332:	Context string `json:"context,omitempty"`
origin/main:model/responses_agent.go:333:}
```

custom 值确实会到达线上（`model/reasoning_effort.go` 的 `ResolveReasoningEffort` 默认分支原样透传，`IsKnownReasoningEffort` 之外即 custom），故 `model_reasoning_effort = "64"` 在 Go 被发成 `"effort":"64"`，Rust 发 `"effort":64`。

- **最小改动形态**：`responsesReasoning` 增加自定义 `MarshalJSON`（或在 `responsesReasoningParam` 后按需构造 `map[string]any`），仅当 `Effort` 可解析为 `uint64` 且非已知枚举名时输出 JSON number；其余维持字符串。落点 `model/responses_agent.go`（1 文件）+ 回归测试（1 文件）。
- 影响面：`effort` 是 Responses 请求字段，改动会进入 `model/responses_agent_test.go` 的请求体断言。**建议由队长确认**是否值得为一处「非默认路径的 wire 类型差异」开工。

### 3.2 `#47873` — deferred tool namespace 片段指标（M，`telemetry/` + `context/`）

Rust 原文（`4021746a15`，`codex-rs/otel/src/metrics/names.rs`）：

```
+pub const THREAD_TOOLS_NAMESPACES_TOTAL_METRIC: &str = "codex.thread.tools.namespaces_total";
+pub const THREAD_TOOLS_FRAGMENT_BYTES_METRIC: &str = "codex.thread.tools.fragment_bytes";
+pub const CONTEXT_FRAGMENT_BYTES_BUCKETS: &[f64] =
+    &[256., 512., 1_024., 2_048., 4_096., 8_192., 16_384.];
```

Go 现状（自述缺口）：

```
$ git grep -n 'fragment_bytes\|namespaces_total' origin/main -- '*.go'
origin/main:telemetry/metric_buckets.go:12:// for `codex.thread.tools.fragment_bytes` and `codex.thread.tools.namespaces_total`;
$ sed -n '9,14p' telemetry/metric_buckets.go
// Rust also defines `THREAD_TOOLS_METRIC_BUCKETS` (logarithmic through 32,768)
// for `codex.thread.tools.fragment_bytes` and `codex.thread.tools.namespaces_total`;
// the Go port does not emit that metric family yet, so only the skill families
// are carried here.
```

- **缺口成立**，但落点跨面：Rust 4 文件 = `core/src/context/world_state/tools.rs`（测量与分桶）+ `core/src/session/world_state.rs` + `core/src/context/world_state/tools_tests.rs` + `otel/src/metrics/names.rs`。Go 对应 = `telemetry/metric_buckets.go`（加 buckets/常量）+ `context/tools_state.go`（`maxDeferredToolsFragmentBytes = 4*1024`）+ **发射点**（Go 的 session telemetry 在 `appserver/`）。
- **需架构决策**：Go 的 session telemetry 观察者与 `context/` 的分层（`telemetry` 不能 import `turn`，见 `mcp/catalog_telemetry.go:15-16` 的注释）意味着要新增一条「context → telemetry」的注入缝。**建议单独立项评估**，不宜当 S/M 直接落地。

### 3.3 `#47657` — Bedrock GovCloud 受限默认目录（S，`model/`）

Rust 原文（`5bae5b563e`，`codex-rs/model-provider/src/amazon_bedrock/mod.rs`）：

```
-            BedrockEndpoint::Mantle => static_model_catalog(),
+            BedrockEndpoint::Mantle => {
+                let is_govcloud = ...host.starts_with("bedrock-mantle.us-gov-") && host.ends_with(".api.aws");
+                if is_govcloud { static_gov_model_catalog() } else { static_model_catalog() }
+            }
```

Go 现状（0 命中）：

```
$ git grep -n 'staticGov\|govModelCatalog\|us-gov-.*api.aws' origin/main -- 'model/*.go'
（0 命中）
$ git grep -n 'amazonBedrockMantleSupportedRegions' origin/main -- model/provider.go
origin/main:model/provider.go:320:var amazonBedrockMantleSupportedRegions = map[string]struct{}{
```

`model/catalog.go:1112` 的 Mantle 目录是端点级（`bedrockEndpointMantle`），**不看 host**，故 GovCloud Mantle 端点会拿到全量目录。

- **最小改动形态**：`model/provider.go` 加 host 判定（`bedrock-mantle.us-gov-*.api.aws`），`model/catalog.go` 加 `staticGovBedrockCatalog()`（保留 GPT-5.6 Terra/Luna + GPT-5.4、默认 Terra）并接线；+ 测试 1 文件。落点 2-3 文件，均在本域。
- 注意：Rust 同时改了 `tui/` 快照（`bedrock_govcloud_models.snap`）——那是 syncl3 域，Go 侧本次**不碰**。

## 4. 重点一条：`code_mode_only_strict_3p_tools` 族（`#50687` / `#50741` / `#50962`）

### 4.1 三族成员现状（自解 sha + Go 落点）

| PR | sha | 语义 | Go 现状 | 判定 |
|---|---|---|---|---|
| `#50741` | `550eb50545` | environment-backed tools 跨 readiness 变化保持暴露 | `turn/tools.go:308 TurnEnvironmentPlan`（注释直引 `Rust #50741/#50962`）；`tool/shell_executor.go:914`、`tool/write_stdin_executor.go:63` | **已落地**（`993e27c0 sync477`、`4dba00a2 sync442`） |
| `#50962` | `335c7f8eca` | 用 feature flag 门控 stable environment tool 暴露 | `turn/tools.go:288 StableEnvironmentTools: featureflags.Enabled(nil,"stable_environment_tools")`；`appserver/stable_environment_tools_like_rust_test.go` | **已落地**（同上两笔） |
| `#50687` | `58ae3ba611` | strict Code Mode Only 下第三方工具保持 deferred | `features/features.go:317` **仅声明 key**；`git grep -niE 'enforceStrict\|strict_3p\|deferThirdParty\|is_third_party_tool' -- '*.go'` → **0 命中** | **未落地（唯一真缺口）** |

### 4.2 结论：**逐 PR**（不是整体立项），且该条**不属于本域**

理由（全部可复跑）：

1. **族内 2/3 已在 Go 落地**（上表 §4.1 两行，`993e27c0`/`4dba00a2` 是 Go 的 `sync` 提交，标题直引 `(#50741, #50962)`）。只剩 `#50687` 一条 ⇒ 「整体立项」的前提不成立。
2. Go 侧「声明特征 key 但 0 消费者」是**注册表全量对齐**的副产物，不是待办标记：`update/plan_2026_10_07.md:1218` 记录该批次是「features registry 全量对齐（补 13 个 key）」。同批的 `code_mode_tool_search`（`#51209`）同样只有声明、0 消费者（`git grep -n 'code_mode_tool_search' -- '*.go'` → 仅 `features/features.go:314`）。把「声明」读成「立项」会凭空造活。
3. **`#50687` 的 Go 落点不在本域**：Rust 改动面 = `core/src/tools/spec_plan.rs`（`enforce_strict_3p_tools` 在 `finalize_tool_router` 之后运行）+ `core/src/tools/registry.rs` + `handlers/mcp.rs` + `handlers/dynamic.rs` + `features/src/lib.rs`。Go 的对应面 = `turn/tools.go`（spec plan）+ `tool/registry.go`（`Exposure`）+ `codemode/codemode.go`——**均为避让/他车域**，不在 `model/`+`telemetry/`+`config/`。

**给的规模/落点（供派单时参考，本单不落地）**：

- 规模：Rust 13 文件（生产 5 + 测试 6 + snapshot 1 + features 1）；Go 等价面最小集 = `turn/tools.go`(spec plan 接线) + `tool/registry.go`(`IsThirdPartyTool` 判定) + `codemode/codemode.go`(exec 路由保留) + 1-2 个 `*_like_rust_test.go`。**跨 2-3 个 Go 包**。
- 落点：Go 需要在 `finalize` 等价处（`turn/tools.go` 的 `resolveTurnEnvironmentPlan` 之后）新增 `enforceStrictThirdPartyTools`：把 `code_mode_only_strict_3p_tools` 为真、且有效 tool mode = `code_mode_only` 时的第三方（MCP/app/dynamic）条目强制置为 deferred，并忽略 `deferLoading` / `omit_tools_from`（Go 侧配置已在 `app/mcp.go:68`、`apps/connectors.go:310`、`mcp/config.go:542` 解析）。
- **建议**：交 `turn/`+`tool/` 域的车道（或队长裁定跨域协调），**不要**作为本域 item。

### 4.3 旁证：features 注册表已再次落后 pin 2 个 key（顺带登记）

```
$ git show origin/main:features/features.go | grep -oE 'Key: +"[a-z0-9_]+"' | ... | sort -u | wc -l   # 164
$ git show d83bb540ec:codex-rs/features/src/lib.rs | grep -oE 'key: "[a-z0-9_]+"' | ... | sort -u | wc -l  # 166
$ comm -23 rust_features.txt go_features.txt
remote_compaction_v2
use_xaa
$ comm -13 rust_features.txt go_features.txt
（空）
```

即 pin `d83bb540ec` 上 Go 缺 `remote_compaction_v2`、`use_xaa` 两个 key（0 个 Go-only）。`features/` 是他车域文件，此处仅作事实登记，不属本单写集。

## 5. 未落地项的正向陈述（可复跑）

| 项 | 正向陈述 | 可复跑命令（原文见 §2/§3） |
|---|---|---|
| `#46519` | 纯测试加固（Rust 引入 paused time 控制超时用例）；Go 无 paused-clock 测试设施，且无生产语义可对位 | `git show 4b9e7f6473 --stat`；`git -C /home/jacks/jacks_dev/codex_go grep -n 'pausedTime' origin/main -- 'model/*.go'` |
| `#46032` | macOS 专用托管配置加载（CoreFoundation `CFPreferencesAppValueIsForced`）；Go 未实现 macOS 托管偏好层 | `git grep -n 'CFPreferences\|CoreFoundation' origin/main -- '*.go'` → 0 |
| `#43604` | Rust 从 models.json 抽走 `base_instructions`（改由 prompt 模块提供）；Go 的 bundled 镜像本就在空值处回落同一 `BaseInstructions` 常量，观测一致 | `git show origin/main:model/catalog.go \| sed -n '744,746p'` |
| `#48229` | 纯模块拆分（`responses.rs` 228 行→新 `responses_error.rs` 138 行）；Go 无该模块边界，语义不变 | `git show 10fd75bba7 --stat` |
| `#48686` | 两条 info 日志在 Go 侧都不存在（0 命中） | 本车道 `update/syncl6_triage_48686_49564_49678_2026_10_07.md` §1.3 的三条 `git grep` |
| `#49369` | Rust 仅改测试期望（`MultiAgentVersion::V1→V2`）；Go 目录测试**已**断言 `v2` | `git show origin/main:model/catalog_test.go \| sed -n '1460,1464p'` |

## 6. 需要队长裁定 / 待派

1. **`#47590`（S，`model/`）** — 是否开工？(a) 做：`model/responses_agent.go` 自定义 marshal + 回归测试（2 文件）；(b) 不做：记为「非默认路径 wire 类型差异」。
2. **`#47873`（M，`telemetry/`+`context/`+`appserver/`）** — 需先定「context→telemetry 注入缝」的架构口径，建议单独立项，**不**当 S/M 落地。
3. **`#47657`（S，`model/`）** — 是否开工？（2-3 文件，本域内；Rust 侧 `tui/` 快照面不碰。）
4. **`#46922`（M，`realtime/`）**、**`#47956`（S/M，`codexapi/`+`turn/`）** — 真缺口但**域外**，请转派对应车道。
5. **`#50687`（strict 3P tools）** — 逐 PR 判定成立；落点 `turn/`+`tool/`+`codemode/`，**域外**，请转派或裁定跨域协调。
6. 本域 ≤5 文件的候选池**已基本穷尽**：41 条中 24 已落地、5 已等价、5 N/A、7 真缺口（3 条本域可开工 + 4 条域外）。
