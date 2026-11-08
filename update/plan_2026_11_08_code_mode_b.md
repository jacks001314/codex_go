# Code Mode / 增量工具（B 包：3 条）—— `55897425e8..748ab55ca7` delta

- 作者：ledger-align（对齐账本 lane）· 日期：2026-11-08
- 性质：只读分析。不改 Go 代码、不动 `parity/**`、锚点维持 3584。
- 行号为**该提交自身版本**；rustPaths 全部先 `${R}:codex-rs/<path>` 验 pin。
- 与 A 包（#60157/#60163/#60188/#60177）分开打包，便于你按包合并。

## 0. 判定汇总

| # | commit | PR | 主题 | 判定 | 一句话理由 |
| --- | --- | --- | --- | --- | --- |
| 71 | `247934cfe5` | #59973 | Enable gRPC and host output truncation by default for Code Mode | **partial** | 两个特性 Go **都实现了**，但都停在 `under development` + 默认关（`features/features.go:483/486`） |
| 30 | `762f87d5bb` | #60040 | Restrict legacy incremental tools to Amazon Bedrock providers | **partial** | Go 仍把 `incremental_tools_legacy` 门控在 **OpenAI** provider（`appserver/incremental_tools.go:66`），与 Rust 现在的 Bedrock 门控**方向相反** |
| 13 | `57a808a392` | #60122 | Preserve code mode dispatch errors in rollout traces | **not_applicable** | Go 没有 rollout-trace 的 tool dispatch 记录器（`rollout-trace` crate 无对应物），缺陷面不存在 |

组内：`partial 2`、`not_applicable 1`。

## 1. `247934cfe5` #59973 — Code Mode 默认走 gRPC + host 侧输出截断

### 1.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `features/src/lib.rs:1321-1328` | `code_mode_host_grpc`：`Stage::UnderDevelopment / default_enabled: false` → **`Stage::Stable / true`** |
| `features/src/lib.rs:1329-1336` | `code_mode_host_output_truncation`：同样翻成 **`Stable / true`** |
| `core/tests/suite/code_mode.rs`（+165/−116）、`scenarios.rs` | 集成测试改为跑**默认配置**，并额外覆盖显式 opt-out（输出预算、截断提示、wait 终止）；stdio 重连用例显式配置该 transport |

### 1.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| 两个特性**都已实现**（gRPC provider / host 侧截断 provider） | 【F:appserver/runtime_router.go†L2126-L2140】`newRuntimeCodeModeProcessProvider`：`code_mode_host_grpc` → `NewGrpcProcessProvider`；`code_mode_host_output_truncation` → `NewLocalServerOutputProvider` 包裹 |
| 同一逻辑在进程内 exec 路径也接好了 | 【F:exec/exec.go†L1583-L1595】`features.Enabled(cfg, "code_mode_host_grpc")` / `"code_mode_host_output_truncation"` |
| **但两个特性仍是 under development + 默认关** | 【F:features/features.go†L481-L487】`{Key: "code_mode_host_grpc", Stage: StageUnderDevelopment, DefaultEnabled: false}`、`{Key: "code_mode_host_output_truncation", Stage: StageUnderDevelopment, DefaultEnabled: false}` |
| 有测试锁当前（关闭态）行为 | 【F:appserver/code_mode_host_grpc_feature_test.go†L9-L20】注释仍写 "the disabled-by-default code_mode_host_grpc feature" |

### 1.3 判定：`partial`

这不是"功能没做"，而是**默认值没翻**：Go 现在默认仍走 stdio 且不在 host 侧截断，行为与 Rust 新版不同（传输与输出预算位置都不同）。差异可观测：默认配置下 Go 的输出体积与 transport 选择和 Rust 不一致。

### 1.4 落地切入点

1. `features/features.go:483`、`:486`：改 `StageStable, DefaultEnabled: true`（两行）。
2. 同步注释与测试预期：`appserver/code_mode_host_grpc_feature_test.go:9` 的 "disabled-by-default" 描述要改成"默认开、可显式关闭"，并补两条 opt-out 用例。
3. 注意 Rust 的做法：stdio 重连用例**显式**配置 transport——Go 里同样要保证显式关闭时仍走 `NewProcessProvider`。

## 2. `762f87d5bb` #60040 — `incremental_tools_legacy` 收窄到 Amazon Bedrock

### 2.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `core/src/session/step_context.rs:61-70` | 门控从 `model_provider_id == OPENAI_PROVIDER_ID` 改为 `matches!(provider_id, AMAZON_BEDROCK_PROVIDER_ID | AMAZON_BEDROCK_RUNTIME_PROVIDER_ID)`；Responses Lite 仍走 `incremental_tools` |
| `features/src/lib.rs:308-311` | 文档注释同步："Publish incremental tool declarations on regular **Amazon Bedrock** Responses." |
| `core/tests/suite/scenarios_incremental_tools.rs` | 覆盖两个 Bedrock provider ID × legacy 开/关；"tool removal" 场景改用 Bedrock provider |

### 2.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| Go **仍门控在 OpenAI provider** | 【F:appserver/incremental_tools.go†L58-L72】`case strings.TrimSpace(providerID) == model.OpenAIProviderID: feature = "incremental_tools_legacy"`；`:73-74` 其他 provider 返回 false |
| 注释也是旧口径 | 【F:appserver/incremental_tools.go†L54-L59】"regular OpenAI Responses windows opt in through `incremental_tools_legacy` (Rust #59832)" |
| Bedrock provider ID 在 Go 里**有**，只是没用在这里 | 【F:plugin/curated_routing.go†L15】`AmazonBedrockModelProviderID = "amazon-bedrock"`；`appserver/model_provider_list_like_rust_test.go:89` 里还有 `amazon-bedrock-runtime` |
| Responses Lite 分支不受影响（与 Rust 一致） | 【F:appserver/incremental_tools.go†L61-L64】`info.UseResponsesLite → "incremental_tools"` |

### 2.3 判定：`partial`

这是**语义方向相反**的偏差，不只是"少一条规则"：Go 会对普通 OpenAI Responses 启用 legacy 增量声明（Rust 已不再），而对 Bedrock 反而不启用（Rust 现在启用）。两端都会产生真实的 wire 差异。

### 2.4 落地切入点

1. `appserver/incremental_tools.go:58-72`：把 `== model.OpenAIProviderID` 换成 `providerID == "amazon-bedrock" || providerID == "amazon-bedrock-runtime"`；建议在 `model` 包补两个常量（现在只有 `plugin.AmazonBedrockModelProviderID`，且没有 `-runtime` 常量）。
2. 注释与 `appserver/incremental_tools_test.go:484-520`（`TestIncrementalToolsEnabledForTurnLikeRust`）的 provider 参数改成 Bedrock；补两个 ID × 开/关四条。
3. 检查是否有别处硬编码"OpenAI → legacy"的假设（`grep -rn "incremental_tools_legacy" --include='*.go'` 目前只落在 `appserver/incremental_tools.go`、`features/features.go`、`model/responses_agent.go:2319/2677` 的注释与测试）。

## 3. `57a808a392` #60122 — rollout trace 保留 code mode 派发错误

### 3.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `rollout-trace/src/tool_dispatch.rs:146-152` | `ToolDispatchTraceContext::start` 删掉 `suppresses_tool_dispatch_trace(&invocation)` 早退：code mode 的 custom payload 也能记录 dispatch 生命周期 |
| `…/tool_dispatch.rs:189-196`（删除） | 删除 `suppresses_tool_dispatch_trace`：原先对 `ToolDispatchPayload::Custom` + 无 namespace + `tool_name == codex_code_mode::PUBLIC_TOOL_NAME` 的调用**完全抑制** |
| 语义（提交正文） | exec 请求可能在 `CodeCellStarted` 之前就失败；抑制 dispatch trace 会把"面向调用方的错误"丢掉。现在 dispatch 独立记录，不依赖 code cell 事件 |

### 3.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| Go **没有** rollout-trace 的 tool dispatch 记录器 | `grep -rn --include='*.go' -E "ToolDispatch\|tool_dispatch\|dispatch_lifecycle"` → 只有**生产**派发实现（`turn/tool_dispatcher.go` 等）与遥测 span，没有"把 dispatch 生命周期写进 trace bundle"的记录器 |
| Go 的录制/回放是 JSONL 事件流，不含 dispatch 生命周期条目 | 【F:recordreplay/recordreplay.go†L96-L110】`Parse` 逐行解事件；无 tool dispatch 结构 |
| 无 code cell 事件类型可关联 | `grep -rn --include='*.go' "CodeCellStarted\|code_cell"` → 0 |
| 现有的 trace 抽象只是 span 透传 | 【F:tracehook/tracehook.go†L1-L20】`Span`/`WithSpan`，不落盘 |

### 3.3 判定：`not_applicable`

不是"Go 做得更好"，而是 Go 没有与 Rust `rollout-trace` 同构的 dispatch trace 面（Rust 是 bundle + 事件条目，Go 是 JSONL 事件流 + span）。被抑制的那条路径在 Go 里不存在，因此无缺陷面。若将来对齐 Rust 的 trace bundle 形状，这条要作为验收项一起带上（否则会精确复现"exec 在 CodeCellStarted 前失败 → 调用方错误丢失"）。

### 3.4 落地切入点

无（n/a）。仅作为将来 bundle 形状对齐时的验收条件记录。

## 4. 契约登记表（2 条；`#60122` 判 n/a 不占位）

| # | contract id | kind | rustPaths（pin 均存在） | goPaths | phase | status | 可立即登记的 verifier | 需先落的 verifier + 要点 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `247934cfe5` #59973 | `code-mode-host-default-features` | `source-surface` | `features/src/lib.rs` | `features`、`appserver`、`exec`、`codemode` | `P1` | `partial` | `TestCodeModeHostGrpcFeatureSelectsProcessProviderLikeRust` | `TestCodeModeHostDefaultsUseGrpcAndHostTruncationLikeRust`（默认配置 = gRPC + host 截断；显式关闭回退 stdio/不截断；截断提示与 wait 终止仍成立） |
| `762f87d5bb` #60040 | `incremental-tools-legacy-bedrock-gate` | `source-surface` | `core/src/session/step_context.rs`、`features/src/lib.rs` | `appserver`、`features`、`model` | `P1` | `partial` | `TestIncrementalToolsEnabledForTurnLikeRust` | `TestIncrementalToolsLegacyGatesOnBedrockProvidersLikeRust`（两个 Bedrock ID × 开/关；普通 OpenAI Responses 不再启用 legacy；Responses Lite 仍用 `incremental_tools`） |

**pin 后新增、不得写进 rustPaths**：本包没有（`features/src/lib.rs`、`core/src/session/step_context.rs`、`rollout-trace/src/tool_dispatch.rs` 在 pin 都存在，已 `cat-file -e` 验过）。

## 5. 复现命令

```bash
R=$(git -C /home/jacks/jacks_dev/codex rev-parse 55897425e8)
for p in features/src/lib.rs core/src/session/step_context.rs rollout-trace/src/tool_dispatch.rs; do
  git -C /home/jacks/jacks_dev/codex cat-file -e "${R}:codex-rs/$p" 2>/dev/null && echo "OK   $p" || echo "MISS $p"
done
# → 3 行全 OK
```

Go 侧定位：

```
$ grep -n "code_mode_host_grpc\|code_mode_host_output_truncation" features/features.go
  483: {Key: "code_mode_host_grpc", Stage: StageUnderDevelopment, DefaultEnabled: false}
  486: {Key: "code_mode_host_output_truncation", Stage: StageUnderDevelopment, DefaultEnabled: false}
$ grep -n "incremental_tools_legacy" appserver/incremental_tools.go    → :67（OpenAI provider 分支）
$ grep -rn --include='*.go' "amazon-bedrock" . | head                  → plugin/curated_routing.go:15 等（ID 存在，未用于 legacy 门控）
$ grep -rn --include='*.go' -E "ToolDispatch|tool_dispatch" . | grep -v _test  → 仅生产派发，无 trace 记录器
```

## 6. 结论

- 2 条 `partial` 都属于"**开关/门控方向不对**"，实现代码本身基本齐全：`#59973` 只需翻两个默认值（外加测试口径），`#60040` 只需把 provider 判断换成 Bedrock 两个 ID。两条都是小改动、行为可观测，建议排在 A 包的链式改造**之前**先做掉。
- `#60122` 判 `not_applicable`：Go 没有 rollout-trace dispatch 面；将来若实现 bundle 形状，必须带这条验收（exec 在 `CodeCellStarted` 前失败时，调用方错误不能丢）。
- 本文件不改 Go 代码、不动 `parity/**`；锚点维持 3584，2 条进 §12 post-anchor 登记。

## 7. 落地记录（/root，2026-11-08）

### `#60040`（`762f87d5bb`）legacy 增量声明收窄到 Amazon Bedrock —— 已落地

- `appserver/incremental_tools.go`：门控从 `providerID == model.OpenAIProviderID` 改为 `model.IsBedrockProviderID(strings.TrimSpace(providerID))`（两个 ID：`amazon-bedrock`、`amazon-bedrock-runtime`），注释同步为 #60040 口径。
- **第二处门控（文档 §2.4 漏了）**：`model/responses_agent.go` 的 `incrementalLayout` 还 AND 了一次 `responsesProviderIsOpenAI(...)`，只改 appserver 会让 Bedrock 的 `IncrementalTools=true` 在 model 层被再挡一次。已改名 `responsesProviderPublishesLegacyIncrementalTools` 并用 `ProviderInfo.IsAmazonBedrock()`（同时接受两个显示名与两个 ID）。
- 测试：`TestIncrementalToolsEnabledForTurnLikeRust` 改成两个 Bedrock ID × 开/关，并断言 OpenAI 不再启用 legacy；新增 `TestIncrementalToolsLegacyGatesOnBedrockProvidersLikeRust`（真实请求体：两个 Bedrock provider 都产出 `additional_tools`，OpenAI 保留完整目录）；`model/incremental_tools_legacy_like_rust_test.go` 的三个用例改为 Bedrock/显式传 provider 名。

### `#59973`（`247934cfe5`）gRPC + host 截断默认开 —— 已落地，但比文档估计多两处

文档说"只需翻两个默认值"，实际不成立：`appserver/runtime_router.go` 的 `newRuntimeCodeModeProcessProvider` 直接读 `options.FeatureEnablement[key]` 原始 map，而该 map 只装 CLI 覆盖项，**永远拿不到 features.go 的默认值**。只翻默认值会造成 exec（用 `features.Enabled`）默认走 gRPC、appserver（原始 map）仍走 stdio 的分裂。

落地的三处：
1. `features/features.go`：`code_mode_host_grpc`、`code_mode_host_output_truncation` → `StageStable, DefaultEnabled: true`。
2. `appserver/runtime_router.go`：改用 `features.Enabled(options.FeatureEnablement, key)`，缺键时回落到注册默认值，显式 false 仍回 stdio / 不截断。
3. `app/app.go`：`featureEnablementFromRoot(root, cfg)` 现在先用 config 各层的 `featureSettings()` 播种，再叠加 CLI `--enable/--disable` 与 `features.*=` 覆盖 —— 否则 `config.toml` 里的 `features.code_mode_host_grpc = false` 到不了 router，"显式关闭仍走 stdio"这条验收就不成立。

测试：`TestCodeModeHostGrpcFeatureSelectsProcessProviderLikeRust` 改为"默认 = 截断包装 + gRPC、显式关两键 = stdio、只关 transport = stdio 外面仍包截断"；`TestRuntimeRouterSelectsCodeModeProviderFromStartupPolicy` 解包截断包装并接受 gRPC/stdio 两种进程 transport；`TestFeatureEnablementFromRoot` 补 config 播种与 CLI 覆盖两条断言。

风险：gRPC 成为默认后，缺少 code-mode host 可执行文件的机器会得到 `GrpcProcessProvider` 的 availability 错误（与 Rust 一致，构造时就带可用性错误，不 panic）。本机（linux）只做了交叉编译与单测，真机 Windows/macOS 的 host 二进制路径未验证。
