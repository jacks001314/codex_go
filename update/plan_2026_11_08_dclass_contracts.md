# D 类 29 条契约提案（2026-11-08）

> **schema 约束（2026-11-08 补充，先读这条）**：manifest 只接受「有用例的契约」——`parity/alignment_control_test.go:260` 要求每条 `Verifier != ""`，`parity/verifier_existence_test.go:27` 还要求这些名字在 Go 树里真实存在。所以**本提案里 `verifier: ""` 的条目不能直接进 manifest**，写 `"pending"` 也过不了第二关。
> 正确顺序是「行为落地 + 用例落地 → 再登记」：下文 `adopt` 的 13 条里，只有 `ui-tree-terminal-escape-stripping` 已在主干有实现与用例（因此已登记，138 条 / 113 complete / 25 partial），**其余 12 条实为待落地清单**，不是可提交的契约。实测：把 13 条一次性灌进 manifest 会让 `TestAlignmentContractInventoryIsTraceable`（`invalid alignment contract: … Verifier:""`）与 `TestContractVerifiersExistInGoTestFiles`（`has no verifier names`）同时变红。

来源：`parity/commits.json` 里 `status = partial` 且**未被任何现有契约引用**的 29 个 commit（= r7 对账报告 §6 的 D 类）。
目标 manifest：137 条（112 complete / 25 partial）。Rust oracle：`codex-rs @ 748ab55ca7de0b647d9a7a9d9a8e70e00f28666d`。
本文件只是提案：**没有改 `parity/**`、没有改 Go 代码、没有改既有 md**。

## 0. 结论

| 建议 | 条数 | 含义 |
|---|---|---|
| `adopt` 现在就立 | **13** | 行为面明确、Go 侧有对应面、能写出可断言 verifier |
| `defer` 建议押后 | **11** | 行为真存在，但 Go 侧当前**没有消费方/承载结构**，立了只是账面 partial |
| `skip` 建议不立 | **5** | 已被别的契约覆盖、纯内部实现、或 Go 侧不适用 |
| 其中已具备登记资格的 | **1** | `ui-tree-terminal-escape-stripping`（主干已实现 + 有 `TestUITreeTextSanitizerStripsControlStrings`）；其余 12 条 `adopt` 需行为与用例一起落地后才能进 manifest |

如果 13 条全立，契约数 137 → **150**，`partial` 25 → 38（因为 verifier 由你补，先落 `partial` 是诚实的中间态）。

## 1. 字段口径

- **`rustPaths`**：从 `git show --stat` 的真实改动文件里取，**相对 `codex-rs/`、不带前缀**，每个 commit 挑 1–4 个"承载行为"的源码文件（manifest 现有条目的 rustPaths 也是 1–5 个，不是全量）。已逐条用 `os.path.exists` 在 pin 上验证存在（见 §6 的两处偏差）。
- **`goPaths`**：按 Rust 改动面推断的 Go 目录/文件，且我**逐条核过这些目录在我的树里存在**（`plugin`、`telemetry`、`tui/analytics`、`agentboard`、`cloudclient`、`context`、`protocol`、`model`、`session`、`agent`、`state`、`appserver`、`tui/tea`、`app` 全部存在）。
- **`verifier`**：一律留空字符串（按你要求）。`TestContractVerifierAuditMatchesTree` 对空 verifier 只记 ledger gap、不 FAIL，我这边跑过确认这条口径成立。
- **`kind`**：只用 `source-surface` / `shared-fixtures`。判据：断言对象是 Rust 侧共享资产（`.snap` 快照、schema JSON、fixture 语料）→ `shared-fixtures`；断言对象是 Go 侧源码行为（wire 字段、工具面、并发语义）→ `source-surface`。
- **`phase`**：沿用我 backlog 的分档口径 —— 安全语义 > 行为可观测的 wire 差异 > 性能 > 纯结构。

## 2. 建议现在立（13 条）

| # | proposedId | commit | PR | kind | phase | goPaths | 一句话理由 |
|---|---|---|---|---|---|---|---|
| 1 | `ui-tree-terminal-escape-stripping` | `7e980a54fe` | #60020 | shared-fixtures | P1 | `tui` | **主干已实现**：`tui/ui_tree.go` 的 `escapeSequenceLen` 已处理 OSC/DCS/SOS/PM/APC，用例 `TestUITreeTextSanitizerStripsControlStrings` 通过、契约已登记（113 complete）。⚠️ r10 里我写的「`sanitizeUITreeText` 只处理 CSI、OSC-8 尾巴会漏出」是**镜像缺这段**造成的误判，已作废 |
| 2 | `answer-moderation-display-metadata` | `202b590e84` | #60172 | source-surface | P1 | `protocol`、`appserver`、`state` | final-answer 的 `display` 元数据（action/blockType/cyberTrustedAccess/checkTrustedContacts）是 wire 字段，与 `final-answer-moderation` 的"决定"面不重叠 |
| 3 | `guardian-bounded-original-evidence` | `ecef7e0936` | #60001 | source-surface | P1 | `state`、`agentboard`、`appserver` | Guardian 送审证据的预算裁剪是安全语义（送什么、裁到多少决定评审结论） |
| 4 | `guardian-read-only-evidence-tools` | `e4bd7871b1` | #59994 | source-surface | P1 | `appserver`、`agentboard`、`state` | 只读证据工具是**暴露给模型的工具面**（工具名/参数/只读约束），也是权限边界 |
| 5 | `guardian-subscription-tools` | `8d0098930e` | #59995 | source-surface | P1 | `agentboard`、`state`、`appserver` | 订阅工具与通道选择共享 selection 状态，两条必须一起验收 |
| 6 | `guardian-review-framing-elastic-limits` | `857415f7f2` | #59003 | source-surface | P1 | `state`、`context`、`appserver` | elastic 预算下必须保留 framing/host notice/planned action，裁错会改变评审结论 |
| 7 | `startup-model-from-thread-options` | `cf39544acd` | #59987 | source-surface | P1 | `model`、`session`、`appserver` | "启动前解析出的 model == 线程实际选中的 model"是强可断言行为（你上轮点名要占的位） |
| 8 | `guardian-model-driven-channel-selection` | `af83299ada` | #59999 | source-surface | P2 | `state`、`appserver` | 通道由模型采样输出决定，可观测（选了哪个通道、housekeeper 何时采样） |
| 9 | `guardian-evidence-source-order` | `5f82dc9e59` | #60000 | source-surface | P2 | `state`、`agentboard`、`appserver` | 投递顺序决定评审员读到的次序，与 #60001 同族、共享 verifier 族 |
| 10 | `plugin-starter-prompt-length` | `358e464528` | #60048 | source-surface | P2 | `plugin`、`appserver` | 128→512 是可直接断言的边界值，Go 有 `plugin/provider.go` 的 `DefaultPrompt` 面，成本最低 |
| 11 | `usage-analytics-turn-triggers` | `5ff78c5e66` | #60018 | source-surface | P2 | `tui/analytics`、`session`、`telemetry` | `turn_trigger` 进 analytics 事件、缺省时不得出现该字段，Go 有 `session/turn_attribution.go`、`telemetry/turn_event.go` |
| 12 | `async-host-service-tier-acceptance` | `f5ac7a757e` | #60075 | source-surface | P2 | `agent`、`model`、`state` | 异步读 + 本地接受回执 = 并发语义（等接受/可取消/重试不重复更新），你上轮点名的 id 就指这条 |
| 13 | `context-fragment-typed-content` | `ce0323f2f2` | #54776 | source-surface | P2 | `context`、`appserver`、`protocol` | typed content（文本标记只作用于 `InputText`）可直接断言；Go 已有 `context/typed_fragment_content_like_rust_test.go` 这个入口 |

### 2.1 这 13 条的 rustPaths

```
ui-tree-terminal-escape-stripping      tui/src/ui_tree/render.rs
answer-moderation-display-metadata     protocol/src/items.rs
                                       app-server-protocol/src/protocol/v2/item.rs
                                       app-server-protocol/src/protocol/thread_history.rs
guardian-bounded-original-evidence     ext/guardian-v3/src/delivery.rs
                                       ext/guardian-v3/src/lib.rs
                                       message-board/src/local/catalog.rs
guardian-read-only-evidence-tools      ext/guardian-v3/src/retrieval_tools.rs
                                       ext/guardian-v3/src/evidence.rs
                                       message-board/src/local/json_search.rs
                                       message-board/src/local/queries.rs
guardian-subscription-tools            ext/guardian-v3/src/subscription_tools.rs
                                       ext/guardian-v3/src/selection.rs
                                       message-board/src/local.rs
                                       message-board/src/tools.rs
guardian-review-framing-elastic-limits guardian-context/src/composition.rs
                                       guardian-context/src/action.rs
                                       guardian-context/src/preparation/repl.rs
                                       guardian-context/src/authorization.rs
startup-model-from-thread-options      core/src/session/mod.rs
                                       core/src/thread_manager.rs
guardian-model-driven-channel-selection ext/guardian-v3/src/selection.rs
                                       ext/guardian-v3/src/sampling.rs
                                       ext/guardian-v3/src/housekeeper.rs
                                       ext/guardian-v3/src/lib.rs
guardian-evidence-source-order         ext/guardian-v3/src/delivery.rs
                                       ext/guardian-v3/src/evidence.rs
                                       message-board/src/local/catalog.rs
                                       ext/guardian-v3/src/housekeeper.rs
plugin-starter-prompt-length           core-plugins/src/manifest.rs
                                       app-server-protocol/src/protocol/v2/plugin.rs
usage-analytics-turn-triggers          analytics/src/reducer.rs
                                       analytics/src/events.rs
async-host-service-tier-acceptance     core/src/agent/host/service_tier.rs
                                       core/src/thread_manager.rs
                                       core/src/session/mod.rs
                                       core/src/state/session.rs
context-fragment-typed-content         context-fragments/src/fragment.rs
                                       context-fragments/src/annotated_content.rs
                                       ext/extension-api/src/contributors/world_state.rs
                                       core/src/context/world_state/mod.rs
```

## 3. 建议押后（11 条，附触发条件）

共同原因：**Rust 侧行为是真的，但 Go 侧今天没有承载结构**。现在立契约 = 立刻多一条无法验收的 partial，等接线/合并后一条条立更划算。

| # | proposedId | commit | PR | phase | 押后到什么时候 |
|---|---|---|---|---|---|
| 1 | `cloud-settings-server-owned` | `654b2ec1f5` | #57267 | P1 | Go 侧 cloud lane 把 `app/cloud*.go` 的设置下送面接完（现在只有 local generation notice）；这是 P1，`defer` 只是排期不是降级 |
| 2 | `ui-tree-inherited-styling` | `1a2f5babe8` | #59988 | P2 | `StatusLineExtensionsTree` 在 `tui/` 内有生产调用方之后。现在 Go 把带 bold/italic/underline 或 `props.color` 的树**整棵判非法**，立了也无从验收 |
| 3 | `ui-tree-multiline-row-wrap` | `8e7ccd8472` | #60026 | P2 | 同上（Go 只有单行 `RenderLine`、无 `wrap` prop） |
| 4 | `ui-tree-status-multi-row-footer` | `cfc11eb483` | #60032 | P2 | 同上（Go footer 天生单行：`FooterHeight = len(FooterLines(props))`） |
| 5 | `ui-tree-status-line-unified-layout` | `5c7a1602c8` | #60031 | P2 | 需要 `measure_natural` 与扩展状态内容接线，两者 Go 都没有 |
| 6 | `ui-tree-inline-text-normalization` | `1a20f53c33` | #60021 | P3 | 与 #59988 同批（依赖 styled leaf 模型） |
| 7 | `ui-tree-semantic-colors` | `2157f98648` | #60019 | P3 | 与 #59988 同批（需要颜色字段先存在） |
| 8 | `ui-tree-status-line-native-renderer` | `66a56b17a1` | #60027 | P3 | 行为已等价（TUI lane 判 `aligned`），只剩防回归价值；等 #60032 那批接线时一起立最省 |
| 9 | `tui-inset-conversation-area` | `8c811e8d93` | #58718 | P3 | Go 侧没有 inset 消费方 |
| 10 | `subagent-picker-pending-approvals` | `181b4a853d` | #58720 | P2 | `agents-overview-*` / `approval-*` 那批收口后，避免一个行为两个契约 |
| 11 | `tui-workspace-shortcut-hud` | `194f2662b8` | #59739 | P3 | chooser/tabs 已被三条 sidebar 契约覆盖，只剩 shortcut HUD 与开合动画；等 TUI 接线 |

## 4. 建议不立（5 条）

| # | 提案 id（仅备查） | commit | PR | 不立的理由 |
|---|---|---|---|---|
| 1 | `message-board-read-view-namespace` | `09bd19a0de` | #60002 | **已被 `message-board-fixed-read-view` 覆盖**（该契约 verifier 里已有 `TestLocalBoardFixedReadViewFreezesCatalogAndCutoffLikeRust`）；本条多出的 namespace / subscription 两个入口建议并入那条的验收要点，不单独立 |
| 2 | `sidebar-tabs-nonblocking-side-chat` | `9df200a068` | #59767 | **已被 `sidebar-tab-keyboard-mouse-navigation` + `side-chat-persistence-and-tab-close` 覆盖**（tab、`leader t` 绑定、多 side chat 保持/关闭都在那两条里） |
| 3 | `ui-tree-prepared-measurement` | `d749ee847f` | #60022 | 纯内部结构拆分（`prepare` / `PreparedUiTree`）；可观测价值只通过 #60026 wrap 与 #60031 `measure_natural` 体现，已在那些条目覆盖 |
| 4 | `ui-tree-terminal-buffer-render` | `7cdb219883` | #60029 | 纯绘制实现路径（直绘 buffer）；输出等价物已由 Go 的 `UILine + OSC-8` 承担，可观测差异归 #59988/#60019/#60026 |
| 5 | `openai-docs-scope-narrowing` | `4980f9585a` | #59944 | 纯 skill 资产/文案改动（单文件 `openai.yaml` + catalog 快照），无行为面；Go 侧没有 vendored skills 树，属"行为在 Go 侧不适用" |

## 5. 与现有 137 条的重叠核对（为什么不是"全立"）

| 邻近契约 | 覆盖的 commit | 与本提案的关系 |
|---|---|---|
| `final-answer-moderation` | #59842 | 覆盖"审核决定随 final answer 下发"；#60172 是**display 元数据**，字段不同，故单立 |
| `message-board-fixed-read-view` | — | 已含 read-view 冻结；#60002 因此不立 |
| `sidebar-chooser-list-bindings-and-hover` / `sidebar-tab-keyboard-mouse-navigation` / `side-chat-persistence-and-tab-close` / `side-chat-policy-first-request` | — | 已含 chooser/tabs/持久化；#59767 不立、#59739 押后 |
| `guardian-verified-answer-whole` / `guardian-repl-provenance-bound` / `guardian-parent-compaction-bound` / `guardian-max-tool-call-lag` / `guardian-v2-managed-reviewers-disable` | — | guardian 族现有条目都在 elastic 预算与 v2 配置面；#59994/#59995/#59999/#60000/#60001/#59003 是 **v3 证据检索/选择/投递**面，不重叠 |
| `typed-prompt-markdown-render` | — | 那是"typed prompt 的 markdown 渲染"；#54776 是"上下文 fragment 的 typed content 透传"，面不同 |
| `session-startup-prewarm` | — | 那是 prewarm；#59987 是"startup model 解析必须复用线程启动的解析"，不同 |
| `cloud-thread-project-binding` | — | 那是 prewarm + project 绑定；#57267 是 cloud 设置的归属与 `thread/settings/update` 接受语义 |
| `agents-overview-dashboard` / `agents-overview-in-session` | — | 与 #58720 的 subagent picker 排序相邻但不同面（overview vs picker），故押后而非不立 |

## 6. 口径与三处已知偏差

1. **`tui/src/bottom_pane/status_line_content.rs` 在 pin 上已不存在**（#60027、#60021 都改过它，之后被合并/改名）。我没有把已删除的路径写进 `rustPaths`（否则 `TestAlignmentRustContractOraclePathsExist` 会 MISS），改用这两个提交里同样存在、且承载同一行为的现存文件：#60027 → `status_line_setup.rs` / `status_surface_preview.rs` / `chatwidget/status_surfaces.rs` / `footer/footer_content.rs`；#60021 → `ui_tree/render/text.rs` / `ui_tree/render.rs`。
2. **`ce0323f2f2`（#54776）的 `git show --stat` 有 9271 个文件**（本地镜像里这条带 rebase 基线，不是真实改动面）。我只取与 typed-content 行为面直接相关的 4 个文件（`context-fragments/*`、`ext/extension-api/.../world_state.rs`、`core/src/context/world_state/mod.rs`）。如果你希望以 GitHub 侧的真实 diff 为准，请给我那条 commit 的真实文件清单，我按它改。
3. **29 条全部在本地 Rust 镜像里存在**（`git cat-file -e` 全通过），所以 rustPaths 是 `git show` 实跑结果，不是从 subject 猜的。
4. **「Go 侧零命中 / 能力缺失」的结论一律先假设镜像落后**：本提案里 `ui-tree-terminal-escape-stripping` 就是反例——我按旧镜像判「只有 CSI 清洗」，主干其实是 `escapeSequenceLen` 覆盖 OSC/DCS/SOS/PM/APC。今后这类判断在正文里标「需主干确认」，不当已证事实写。

## 7. 复现方式

```bash
# D 类清单（status=partial 且无契约引用）
python3 - <<'PY'
import json; d=json.load(open('parity/commits.json'))
print([c['sha'][:10] for c in d['commits'] if c['status']=='partial'])
PY

# rustPaths 来源
git -C ~/jacks_dev/codex show --stat=200 --format=%s <sha>

# 每条 rustPath 在 pin 上存在
for p in <rustPaths>; do test -e "$HOME/jacks_dev/codex/codex-rs/$p" || echo "MISS $p"; done
```

## 8. 落地顺序建议（给三个 lane 排期用）

1. **P1 先做 2 条自成闭环的**：`plugin-starter-prompt-length`（边界值，128→512）→ `startup-model-from-thread-options`（wire 语义）。`ui-tree-terminal-escape-stripping` 已完成登记，不再排期。
2. **P1 guardian 五条一起做**（#60001/#59994/#59995/#59999/#60000 + #59003）：它们共享 `guardian-v3` 的 delivery/selection/evidence 状态，分开做会出现"工具能订阅但投递顺序还是旧的"这种中间态。
3. **`answer-moderation-display-metadata` 单独一批**（动 protocol + state 迁移，与 moderation 决定面相邻）。
4. **11 条押后项**：等 TUI 接线（`StatusLineExtensionsTree` 有生产调用方）与 cloud lane 落地后，一次性按 `ui-tree-*` 批量立契约。

## 9. 附：本包同时带的 2 行补丁

你上一轮点名的两处目录级路径已按你给的确切文件替换（`PATCHES/commits_evidence_paths.diff`，2 hunks）：

- `af9a98b074` → `Go mcp/tool_catalog_cache.go implements warming the process-scoped tool catalog cache for new chats; …`
- `b1a0dff158` → `Go context/node_repl_review_evidence.go implements bounding elastic REPL provenance headers by token cost instead of keeping every response; …`

`commits.json` 2 894 577 B `cf6cb6f9…74ccb00` → 2 894 636 B `963d9dd1…ec31f9c`，只动这 2 个字符串，反向校验逐字节还原为 `cf6cb6f9…`，`ledger_verify` 仍 GATE: PASS（计数 2975/639/59/4 不变）。
