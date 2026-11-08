# Linux lane：上游 delta 第一批评判（2026-11-08）

基线：Go 已覆盖 Rust `55897425e8`（2026-11-06），上游 `main` 为 `748ab55ca7`（2026-11-08），未审计 93 条。
本文件记录 Linux lane 对其中**与 Go 实现面无关**的条目作出的判定，以及后续分组。

## 已判定：发布 / CI / 构建工具链（Go 无对应面）

判定依据：Go 侧发布走 `scripts/build.{ps1,sh}`、`scripts/release.*`、`npm/scripts/*.mjs` 与 Go 模块，不使用 Cargo、Bazel 或 Rust 的 prerelease 工作流；这类改动落在 Rust 构建体系内部，不改变 CLI 的可观察行为。

| commit | 主题 | 判定 | 理由 |
| --- | --- | --- | --- |
| `b6f53857fc` | Remove incomplete prereleases after asset upload failures | not_applicable | GitHub prerelease 资产清理脚本，Go 发布流程无 prerelease 阶段 |
| `e3fe1ade2c` | Delete incomplete prereleases by release ID | not_applicable | 同上，release automation |
| `8842c09a7f` | Fix Opus header installation with relative symlink inputs | not_applicable | Rust musl 构建的 Opus 头文件安装（Bazel/构建脚本） |
| `a5a00d6c73` | Add a shared Windows Bazel setup action | not_applicable | Bazel CI action |
| `748ab55ca7` | Increase Bazel core unit test shards from 8 to 16 | not_applicable | Bazel 测试分片 |
| `ebe94e25c5` | Bump `ignore` to `0.4.30` | not_applicable | Cargo 依赖升级；Go 侧对应库版本各不相同 |
| `6abffd442d` | Test rejection of PNGs with readable dimensions but corrupt pixels | not_applicable | Rust 单测补强，Go 图像准备已有错误路径 |
| `1be4eb123e` | Test command completion before network denial grace expires | not_applicable | Rust 单测补强，Go 无该 grace 窗口实现 |
| `2f742f5885` | Disable bundled skills in the prompt-prefix comparison test | not_applicable | Rust 测试夹具设置 |

合计 9 条，均为 `not_applicable`，不产生 Go 代码改动。

## 待办分组（84 条）

| 主题 | 条数 | 下一轮切入点 |
| --- | --- | --- |
| Guardian 证据链 | 11 | 先判 `#59905/#59913/#59914`（凭据与策略绑定），再判证据检索工具族 `#59994-#60002` |
| 扩展 UI tree | 11 | 需先确认 Go 是否规划 extension UI tree；无规划则整体 `not_applicable` 并写明理由 |
| 模型 / Provider | 8 | `#59893` 切模型重算审批 reviewer 是行为修正，优先 |
| Code Mode / 增量工具 | 7 | `#60157/#60163` 与 round 1317 已落地的 `incremental_tools_legacy` 同源，可复用判定 |
| 线程 / 存储 | 6 | `#60006` SQLite 写放大、`#59969` 写锁争用属性能项，量测后再改 |
| 运行时鉴权 | 5 | `#60003` realtime、`#60101` Analytics、`#60073` remote-control 是同一套接线，可批量做 |
| MCP | 5 | `#60039` 懒启动依赖 `#60037/#60038`，三条一起做 |
| 侧栏 / 侧聊 | 4 | 依赖 TUI 侧栏已有实现，最后做 |
| 上下文预算 | 3 | 与 elastic context 相关，需先确认 Go 的预算模型 |
| 安全 / Windows | 3 | `#60041` 项目信任、`#60034` spawn 日志脱敏 |
| Cloud | 2 | `#60007` 云线程绑定本地 GitHub 项目、`#60061` 图片消息与草稿交接 |
| 其它 | 9 | 账号 user id、技能显示名、typed prompt Markdown 等零散项 |

## 下一轮执行顺序

1. 运行时鉴权三条（realtime / Analytics / remote-control）——同一接线模式，改动集中、可批量验证。
2. MCP 懒启动三条（`#60037/#60038/#60039`）——有明确开关，便于回退。
3. `#59893` 切换模型重算审批 reviewer——审批正确性。
4. Guardian 凭据与策略绑定三条。
5. 其余按上表推进。

## Round 1319：#60020 控制串清洗（ported）

- `tui/ui_tree.go`：新增 `escapeSequenceLen`，`sanitizeUITreeText` 从只识别 CSI 改为清洗完整转义序列——OSC 以 BEL 或 ST 结束，DCS/SOS/PM/APC 以 ST 结束，其余 ESC 按两字节序列跳过；未终止的序列仍按原行为截断。
- 修复的实际缺陷：`ESC ]8;;URL BEL`（OSC 8 超链接）此前只丢 ESC、留下可见的 `]8;;URL` 文本。
- 测试：`tui/ui_tree_like_rust_test.go` 新增 `TestUITreeTextSanitizerStripsControlStrings`（OSC-BEL、OSC-ST、CSI、DCS、两字节 ESC、制表/换行保留、未终止截断）。
- 验证：把三个函数原样抽到独立包 `go run` 跑同一组用例，7/7 通过；完整 `go test ./tui` 未在本机跑（模块未整体取回）。
- 残余：扩展 UI tree 在 Go 仍无生产调用方，`appserver/protocol.go` 也没有 UiTree/StatusLine 载体；9 条相关提交的判定与接线顺序见 `update/plan_2026_11_08_ui_tree.md`。

## Round 1320：#59893 切模型重算审批 reviewer（ported）

上游 `f49ae52079`（#59893 Recompute the approval reviewer when switching models）只动两处：Rust 在 `SessionConfiguration::apply` 的 `step_settings.apply()` 之后新增一段——当「不是 Guardian 基本会话 + 本次没有显式给 reviewer + 模型发生变化」时，用
`resolve_guardian_reviewer(original_config.requested_approvals_reviewer.unwrap_or(当前 step_settings.approvals_reviewer), Some(新模型), requirements)` 重算并写回。

Go 侧实测缺口：`applyStepSettingsConstraints` 只处理「进入受保护模型 → 自动切 auto_review」，离开时不恢复；而 `ThreadExtraService.UpdateSettingsWithCommit` 在 `ApprovalsReviewer` 非空时会同时写入 `RequestedApprovalsReviewer`，于是校验器强制的 auto_review 会覆盖用户先前的显式选择，切回普通模型仍是 auto_review。

改动：

- `appserver/step_settings_constraints.go`：新增 `resolveGuardianReviewer`（模型要求 auto review 优先 → 请求值在 managed 白名单内则保留 → 否则回落白名单首项 → 最终再校验）；`validateTurnStepSettings` 在 `modelChanged && !reviewerExplicit` 且非 Guardian basic-session 时按上述规则重算；写回判据改为与调用方原本会继承到的值比较（`inheritedReviewer`），否则「重算值恰好等于 selected」时不写回，实测会退化成 reviewer 为空。
- `appserver/runtime_router.go`：`thread/settings/update` 与 `turn/start` 两条路径在校验后，若 reviewer 是校验器强制替换的（调用方没显式给），把已存的 `RequestedApprovalsReviewer` 原样带过去，避免被 `UpdateSettingsWithCommit` 覆盖成 auto_review。

测试：`appserver/step_settings_constraints_test.go::TestModelSwitchRestoresRequestedApprovalsReviewerLikeRust`，锁 Rust 用例的六步序列（user → protected→auto_review → ordinary→user → 显式 auto_review → protected → ordinary 仍 auto_review）。

验证（linux_node，go1.26.3）：`go build ./appserver` EXIT=0；定向 5 个用例 ok；`go test ./appserver -count=1` 122.8s 仅 `TestPrecomputedExportArtifactsMatchTargetRustCommit` 失败（见下，与本改动无关）。

## 既有基线失败：vendored 预计算导出落后于 Rust pin

`TestPrecomputedExportArtifactsMatchTargetRustCommit` 在 linux_node 稳定红。vendored 的 `appserver/schema/precomputed/app-server-exports-{stable,experimental}.json.zst` 实际 sha256 为 `6e0fb0e5…` / `6b336adc…`，`appserver/schema_test.go` 冻结的是 `5a93be76…` / `72216e63…`（round 1195/1209 的 pin）。

解压后与 `artifacts/rust-codex-55897425e8` 的 `codex-rs/app-server-protocol/schema/precomputed/` 逐键比对：

| 导出 | typescript 条目 | json_schema 条目 | 共有条目内容不同 |
| --- | --- | --- | --- |
| stable | 776（Rust 778） | 326（Rust 328） | 4 / 4 |
| experimental | 941（Rust 943） | 474（Rust 476） | 7 / 10 |

差额固定含 Rust 有、vendored 无的 `v2/GatewayOAuthLoginParams`、`v2/GatewayOAuthReadParams`；内容差异方向是 vendored 更旧，例如 `v2/QueuedSubmission.ts` 缺 Rust 的 `responsesapiClientMetadata` 字段。

本轮只改 step-settings 三个文件，不触碰 `appserver/schema/**`，此红与本次改动无关。修复路径：从 pin 树原样 re-vendor 两个 `.zst`，把 `QueuedSubmission.responsesapiClientMetadata`、Gateway OAuth 两个参数类型补进 Go 协议导出，再重新冻结 pin 并跑 `Generate*` 无漂移用例。

