# syncl6 · round87 · 落地：GPT-6 家族进 Go fallback bundled 目录（路线 (a) 最小对齐）

- 车道：`syncl6`（Linux 节点 `de1bb1e71f8f7ad6969025798b555057`）；派单 `msg-1791375208054359900-5094`
- 基线：**`origin/main = 185d03c933086ddd2b0a1f498fdcf7f9d2683d70`**
- Rust 对照：`/home/jacks/jacks_dev/codex`，`b17c74cfd5ebb39fe70ffaff78de198120278636`（`codex-rs/models-manager/models.json`）
- 补丁：`update/r86_patches/syncl6_gpt6_bundled.patch`（**3 文件**：`model/catalog.go`、`model/catalog_test.go`、`app/app_test.go`）
- 纪律：**0 commit / 0 push / 0 ref 移动**；交付只有补丁一条路；探针只放 `/tmp`。

---

## §0 环境自检（原文）

```
$ git -C /home/jacks/jacks_dev/codex_go rev-parse origin/main
185d03c933086ddd2b0a1f498fdcf7f9d2683d70
$ git -C /home/jacks/jacks_dev/codex_go status --porcelain
?? scripts/loc_report.sh
$ git worktree add --detach /tmp/wt-syncl6-r87 185d03c9        # 隔离工作树（detached，无新 ref）
HEAD is now at 185d03c9 sync632: append inherited-model guidance to the V1 spawn description like Rust (#26114)
$ git -C /tmp/wt-syncl6-r87 status --porcelain                  # 改动前：空
```

Rust 值来源（原文）：

```
$ git -C /home/jacks/jacks_dev/codex grep -n '"slug"' b17c74cfd5 -- codex-rs/models-manager/models.json
codex-rs/models-manager/models.json:4:      "slug": "gpt-6-astra",
codex-rs/models-manager/models.json:177:      "slug": "gpt-6.1-sol",
codex-rs/models-manager/models.json:353:      "slug": "gpt-6-sol",
codex-rs/models-manager/models.json:524:      "slug": "gpt-6-luna",
```

---

## §1 改动落点（file:line）与 Rust 对照

**唯一生产改动**：`model/catalog.go:909 fallbackBundledModelsResponse()` —— 在 `Models: []ModelInfo{` 后新增 4 条，并把既有条目的 priority 对齐 Rust 全局值。

| 新增条目 | Go 行 | Rust 值（`models.json` @b17c74cfd5） |
|---|---|---|
| `gpt-6.1-sol` | `model/catalog.go:912` | priority 1、`v2`、default_reasoning_level=low、6 档（…max, **ultra**）、ctx 272000 / max 872000、`use_responses_lite=true`、`truncation_policy={tokens,10000}`、`tool_mode=code_mode_only`、description "Latest workhorse model for coding and everyday work." |
| `gpt-6-astra` | `model/catalog.go:924` | priority 2、`v2`、default low、6 档（含 ultra）、同 ctx，description "Frontier intelligence for the most demanding work." |
| `gpt-6-sol` | `model/catalog.go:936` | priority 3、`v2`、default **medium**、6 档（含 ultra），description "Previous generation workhorse model." |
| `gpt-6-luna` | `model/catalog.go:948` | priority 4、`v2`、default medium、**5 档（无 ultra）**，description "Fast and affordable model for easier tasks." |

**既有条目 priority 对齐 Rust**（`git diff` 原文）：

```
-				... Priority: 1, ...      # gpt-5.6-sol
+				... Priority: 5, ...
-				... Priority: 2, ...      # gpt-5.6-terra
+				... Priority: 8, ...
-				... Priority: 3, ...      # gpt-5.6-luna
+				... Priority: 9, ...
-				Priority:                       7,     # gpt-5.5
+				Priority:                       13,
```

（`gpt-5.2`=29、`gpt-5.4-mini`=23、`codex-auto-review`=43 未动，与 Rust 无冲突。）

**未触碰**（对照 `git diff model/catalog.go`，`grep -E 'bedrockModel|normalizeBedrockCatalog|normalizeBundledBedrockCatalog|AmazonBedrock'` → 0 命中）：`model/catalog.go` 的 Bedrock 段（`normalizeBundledBedrockCatalog` / `bedrockModel`，sync626 的面）**原样未动**；`tui/state.go`（避让面）也未动（`git status --porcelain tui/` 空）。

**有意偏差（与 Rust 的两处字段，沿用 fallback 既有条目形态）**：`multi_agent_reasoning_effort`（Rust：6.1-sol/astra = `xhigh`）与 `default_service_tier`（Rust：6-sol/luna = `priority`）未写入 —— 现有 fallback 的 GPT-5.6/GPT-5.5 条目同样不写这两项（Rust 对应值分别为 `None`/`None`）。若你要求逐字对齐，可另开一小单补齐（需在生产包加一个 `*string` 字面量助手）。

**测试期望同步（6 条）**：
- `model/catalog_test.go:160/162` `TestFallbackBundledModelsMatchCurrentRustDefault` → 默认模型 `gpt-5.6-sol` → **`gpt-6.1-sol`**。
- `model/catalog_test.go:1380-1390` `TestAmazonBedrockModelCatalog` 的 `wantVersion` map：4 个 GPT-6 Bedrock ID `""` → **`"v2"`**（+ 注释更新）。
- `model/catalog_test.go:1474` `TestBedrockCatalogKeepsUltraReasoningLikeRust`：`want` 增加 GPT-6 6.1-Sol / Astra / Sol（luna 无 ultra）。
- `model/catalog_test.go:1985-1992` `TestAmazonBedrockRuntimeCatalogDisablesWebSearchLikeRust` 的 `wantVersion` switch：4 个 GPT-6 → `"v2"`。
- `app/app_test.go:864` / `app/app_test.go:970` → `gpt-6.1-sol`。

**新增仓内回归测试**：`model/catalog_test.go:167 TestFallbackBundledCatalogCarriesGPT6FamilyLikeRust` —— 钉住 ①默认模型 = `gpt-6.1-sol`；②4 条存在且 `Visibility=list` / `SupportedInAPI` / `multi_agent_version="v2"` / `tool_mode=code_mode_only` / ctx 272000/872000 / `use_responses_lite`；③priority（1,2,3,4 / 5,8,9）与 picker 顺序（GPT-6 家族领跑）；④`ListModels` 首个 preset `IsDefault`。

---

## §2 ⚠️ 用户可见行为变更登记（必读）

> **离线默认模型 `gpt-5.6-sol` → `gpt-6.1-sol`（对齐 Rust）。**

- 触发条件：Go 侧没有 `models-manager/models.json`（普通检出常态）时，`BundledModelsResponse()` 落到 `fallbackBundledModelsResponse()`；此时默认模型与模型选择器首项从 `gpt-5.6-sol` 变为 **`gpt-6.1-sol`**（与 Rust 全局 priority 排序后的首个可见项一致）。
- 影响面（实测）：`TUI` 状态行、`/model` 选择器顺序、`app` 交互式启动横幅、以及未显式指定 `--model` 的会话默认模型。
- **回滚路径**（二选一，均 ≤3 文件）：
  1. `git revert`/反向应用本补丁（3 文件：`model/catalog.go`、`model/catalog_test.go`、`app/app_test.go`）；
  2. 或只把 4 条 GPT-6 的 `Priority` 调回 5.6 家族之后（例如 4/5/6/7），保留目录条目但不改默认 —— 需同步调整 `model/catalog_test.go` 的 priority/顺序断言与 `app/app_test.go` 两条期望回 `gpt-5.6-sol`。

---

## §3 残余组合漂移登记（不得静默）

相对 Rust `models-manager/models.json`（@b17c74cfd5，11 条），Go fallback 现状（11 条）仍有组合差异：

| 类别 | 条目 | 说明 |
|---|---|---|
| Go **缺** | `gpt-daybreak-blue-latest`、`gpt-daybreak-red-latest`（Rust priority 11 / 12，visibility hide） | 本次**不补**（daybreak 面另属其他链路） |
| Go **多出** | `gpt-5.2`（priority 29）、`gpt-5.4-mini`（priority 23，hide） | Rust 已移除；`gpt-5.4-mini` 仍被 `TestFallbackBundledCatalogDropsGPT54LikeRust` 依赖（GPT-6 Luna 迁移提示需要保存的选择） |

> **本次只做 GPT-6 对齐，不删条目**（`gpt-5.2` / `gpt-5.4-mini` 保留，`daybreak×2` 不补）。

---

## §4 门禁 6 步（原文）与基线对拍

```
$ cd /tmp/wt-syncl6-r87
$ gofmt -l model/catalog.go model/catalog_test.go app/app_test.go
(空)                                    # 1) gofmt 干净（LF 树）
$ go build ./...
build rc=0                              # 2) go build ./... 通过
$ go vet ./model/
model/responses_agent.go:1462:11: assignment copies lock value to clone: codex_go/model.ResponsesAgentRunner contains sync.Mutex
                                        # 3) 仅既有 lock-copy 告警（基线同款，非本补丁）
$ go test ./model/ ./app/ -count=1
--- FAIL: TestRefreshBedrockAWSCredentialsRunsCommandOnceAndReloads (0.00s)
    responses_agent_test.go:4131: refresh error = bedrock aws auth refresh failed: fork/exec .../aws-refresh.sh: exec format error
--- FAIL: TestRefreshBedrockAWSCredentialsBoundsProviderRecoveryPerRequest (5.00s)
    responses_agent_test.go:4167: ... no EC2 IMDS role found ...
FAIL	codex_go/model	29.822s
--- FAIL: TestNoDaemonRejectionsLikeRust (0.00s)
    daemon_startup_test.go:202: ... "TERM is set to \"dumb\"" ...
FAIL	codex_go/app	12.788s
$ CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1 -v   # 4) parity
     61 --- PASS
      1 --- SKIP
ok  	codex_go/parity	0.847s
```

**基线对拍（新增失败 0）**：

| 包 | 改动前失败集合 | 改动后失败集合 | 新增 |
|---|---|---|---|
| `./model/` | `TestRefreshBedrockAWSCredentialsRunsCommandOnceAndReloads`、`TestRefreshBedrockAWSCredentialsBoundsProviderRecoveryPerRequest`（环境型） | 同左（逐字） | **0** |
| `./app/` | `TestNoDaemonRejectionsLikeRust`（环境型 TERM=dumb） | 同左（逐字） | **0** |
| `./parity/` | 61 PASS / 1 SKIP / 0 FAIL | 61 PASS / 1 SKIP / 0 FAIL | **0** |

（未跑 `go test ./...`，遵硬规则 5。）

---

## §5 值级 RC（撤生产接线 ⇒ FAIL ⇒ 恢复 ⇒ ok，原文）

撤法：仅从生产 `fallbackBundledModelsResponse()` 移除 4 条 GPT-6（保留测试改动），其余不动。

**FAIL 段**（原文）：

```
$ go build ./model/ && go test ./model/ -run 'TestFallbackBundledCatalogCarriesGPT6FamilyLikeRust|TestFallbackBundledModelsMatchCurrentRustDefault' -count=1
--- FAIL: TestFallbackBundledModelsMatchCurrentRustDefault (0.00s)
    catalog_test.go:160: default model = "gpt-5.6-sol"
--- FAIL: TestFallbackBundledCatalogCarriesGPT6FamilyLikeRust (0.00s)
    catalog_test.go:192: gpt-6.1-sol Priority = 99, want 1
FAIL
FAIL	codex_go/model	0.010s
```

（默认回落 `gpt-5.6-sol`；4 条 GPT-6 不存在 ⇒ `GetModelInfo` 落到 `ModelInfoFromSlug` 的 Priority 99。）

**恢复段**（原文）：

```
$ go test ./model/ -run 'TestFallbackBundledCatalogCarriesGPT6FamilyLikeRust|TestFallbackBundledModelsMatchCurrentRustDefault' -count=1
ok  	codex_go/model	0.011s
```

---

## §6 补丁交付

```
$ cd /tmp/wt-syncl6-r87 && git add -A -N && git diff --binary > /tmp/syncl6/syncl6_gpt6_bundled.patch
$ wc -c /tmp/syncl6/syncl6_gpt6_bundled.patch
15125 /tmp/syncl6/syncl6_gpt6_bundled.patch
$ sha256sum /tmp/syncl6/syncl6_gpt6_bundled.patch
1c356f1e830b48290fc8b4f2eea3bc3f03646fa7a40b388c6d1b96e93c6ed075  syncl6_gpt6_bundled.patch
$ cd /home/jacks/jacks_dev/codex_go && git worktree add --detach /tmp/wt-r87-verify 185d03c9   # 干净 LF 树
HEAD is now at 185d03c9 ...
$ cd /tmp/wt-r87-verify && git apply --check -v /tmp/syncl6/syncl6_gpt6_bundled.patch
Checking patch app/app_test.go...
Checking patch model/catalog.go...
Checking patch model/catalog_test.go...
exit=0
```

端到端复验（干净树 `git apply` 后）：

```
$ git apply /tmp/syncl6/syncl6_gpt6_bundled.patch && gofmt -l model/catalog.go model/catalog_test.go app/app_test.go
(空)
$ go build ./... && echo rc=$?
rc=0
$ go test ./model/ ./app/ -run 'TestFallbackBundled|TestAmazonBedrock|TestBedrockCatalogKeepsUltra|TestInteractiveUIStateUsesSelectedModelDefaultReasoningEffort|TestInteractiveWithoutPromptRunsLineSession' -count=1
ok  	codex_go/model	0.055s
ok  	codex_go/app	0.256s
```

- 文件数：**3**（≤5）。
- `git diff --stat`：`app/app_test.go | 4 +-`、`model/catalog.go | 56 ++++--`、`model/catalog_test.go | 112 ++++--`；合计 `3 files changed, 151 insertions(+), 21 deletions(-)`。

---

## §7 路线 (c) 成本估计（结构性提案，供用户裁定）

**目标**：让 Go 侧内置的模型目录与 Rust 同源，使 `fallbackBundledModelsResponse()` 退化为「无文件时的兜底」而非唯一来源。

**改动集（估计）**：
1. `model/` 新增 vendored 资源：`model/models-manager/models.json`（约 **472 KB**，Rust @b17c74cfd5 的 `codex-rs/models-manager/models.json` 逐字节拷贝）+ 必要时 `model/models-manager/prompt.md` 与现有 embed 合并/并列。
2. `model/catalog.go`：新增 `//go:embed`（如 `//go:embed models-manager/models.json`），并让 `loadBundledModelsResponse()` 在磁盘路径全部失败后回落到 embed 内容；`bundledModelCatalogPaths()` 保持 dev-checkout 覆盖能力不变。
3. `model/catalog.go`：`fallbackBundledModelsResponse()` 可保留（防 embed 解析失败的最终兜底）或删除（需评估）。
4. 测试：`model/catalog_test.go` 的若干 `loadBundledModelsResponse` 用例（`:1331`、`catalog_messages_test.go:67`）目前 `Skipf("bundled Rust catalog unavailable")`，改为随 embed 生效即变成真跑 —— 期望值需按 11 条目录复核；`fallbackBundledModelsResponse` 直连的 3 个用例（`:158/:172/:191`）语义变化需一并处理。
5. 构建/体积：二进制体积增加约 0.5 MB（embed 未压缩）；`go:embed` 无 CGO/无新依赖。
6. **同源与 license 说明**：`models.json` / `prompt.md` 来自上游 `openai/codex`（`codex-rs/models-manager/`），与本仓已 embed 的 `model/prompt.md` 同源同 license；采用「vendored 文件 + 顶部来源注释（上游仓 + commit sha + 相对路径）」的方式登记，避免每次上游发布手动重抄。
7. 漂移治理：一旦 embed，需在 CI 加一条「vendored models.json 与上游 pin 的 sha256 一致」检查，或定期 regenerate 脚本。

**风险/权衡**：这是 L 级（跨 `model/` 资源 + 构建 + 测试期望 + 上游同步流程），但能一次性消除「每加一个模型都手改 fallback + 修测试 + 必然漂移」的问题；本次 (a) 的 4 条手抄即为该问题的又一次实例。

---

## §8 未决 / 交付物

- 未决（需你裁定）：
  1. 是否要补齐 §1 登记的**两处有意偏差**（`multi_agent_reasoning_effort` / `default_service_tier`）——需在生产包加 `*string` 助手，约 +1 小 hunk。
  2. 路线 **(c)** 是否立项（§7 成本估计）。
- 交付物：
  - 补丁 `update/r86_patches/syncl6_gpt6_bundled.patch`（15125 B / sha256 `1c356f1e830b48290fc8b4f2eea3bc3f03646fa7a40b388c6d1b96e93c6ed075`）
  - 本报告 `update/syncl6_gpt6_bundled_2026_10_07.md`
