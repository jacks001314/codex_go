# Round 1318 — 提交账本回写与基线重锚（2026-11-08）

目标 Rust 锚点：`55897425e839d943a744c658701445ff732568a0`（`55897425e8`，#59832）。
冻结起点仍为 `bd19459358f534ed1cae464ec13d56600aeb45f2`（exclusive）。
本轮只动对齐控制面（`parity/*`、`README.md`、`need_aligin.md`、`update/**`），未改任何 Go 业务代码。

## 1. 入场状态

- `parity/commits.json` 的 `rustToInclusive` 停在 `67f0994b532b8740f14b5b738af7b7b2a915b1d6`，3309 条。
- 1195…1317 各轮（含 `plan_2026_11_04.md` / `11_05.md` / `11_06.md` / `round1316` / `round1317`）
  已逐条判定 `67f0994b..55897425e8` 的 275 个上游提交，但这些判定没有回写账本。
- `parity/baseline.json` 的 `certificationBlockers` 文本写的是
  “2753 complete, 547 not applicable, 0 audit_pending, 0 regression_pending, 4 partial, 5 blocked_precondition”，
  而 `commits.json` 实际是 2757 complete / 547 not_applicable / 0 partial / 5 blocked_precondition，
  即文本计数与账本对不上（4 partial 是旧锚点的残留描述）。
- `parity/domains.json`、`parity/contracts/manifest.json`、`parity/alignment_control_test.go`
  的 `targetRustCommit` / `candidateRustTo` 也都是旧锚点。

## 2. 采集方式（可复算）

采集脚本：`update/ledger_align_2026_11_08.py`（回写账本）、
`update/ledger_sha_resolution_2026_11_08.json`（sha 展开凭据）、
`update/ledger_baseline_2026_11_08.py`（重算 baseline）、
`update/ledger_verify_2026_11_08.py`（复核全部数字）。

解析范围限定为这 10 个文件的 “Commit ledger” 表（表头同时含 `Commit` 与 `Disposition` 两列）：

```
update/plan_2026_11_02.md  update/plan_2026_11_03.md  update/plan_2026_11_04.md
update/plan_2026_11_04_round1309.md  update/plan_2026_11_04_round1311.md
update/plan_2026_11_04_round1312.md  update/plan_2026_11_05.md
update/plan_2026_11_06.md  update/plan_2026_11_06_round1316.md
update/plan_2026_11_06_round1317.md
```

- 得到 275 个唯一 10 位 sha，与账本已有 3309 条**零重叠**，数量与“275 个上游提交”一致。
- 每轮“Ported in this round”表（列是 `Rust | Subject | Go change`）不算判定行，不参与解析，
  以免同一提交被重复计入。

## 3. sha 展开（10 位前缀 → 40 位）

判定表只记 10 位前缀，而账本与 `alignment_control_test.go` 要求 40 位。
展开凭据逐条记在 `update/ledger_sha_resolution_2026_11_08.json` 的 `source` 字段：

| 来源 | 条数 | 说明 |
| --- | --- | --- |
| `local-git-mirror` | 246 | 本机 Rust 镜像 `~/jacks_dev/codex/codex-rs` 能解析该前缀 |
| `github-pull-request-api` | 25 | 镜像缺对象，按 subject 里的 PR 号取 `merge_commit_sha`，并要求其前缀与该行记录的 10 位一致 |
| `unresolved` | 4 | 见下 |

4 条只保留 10 位前缀（`8d712cc542`、`45704d8c64`、`803d519ad3`、`24ed4a1b6c`）：
它们在本机镜像和上游 `openai/codex` 里都不存在；上游同标题提交是 rebase 后的另一个对象
（分别是 `c0100ad4ecd2…`、`d4902cedbf50…`、`2be3dd3ff3af…`、`86ac09165972…`）。
按“不要猜”的原则**没有**把上游对象写进账本，条目 evidence 第二条显式记录这一事实，
`baseline.certificationBlockers` 也单独列了一条，待权威镜像补齐 40 位后再改。

## 4. 顺序

按提交时间升序追加，等价于 `git rev-list --reverse` 的落地顺序。逐条比对后，275 条里有 273 条
与各轮记录顺序一致；`update/plan_2026_11_04_round1309.md` 的 3 条在源表里是**倒序**记录
（`f2b94ef8b5` → `75aba6768a` → `007f668c34`，而提交时间分别是 02:40:55 / 02:29:41 / 02:00:01），
回写时按时间改正为 `007f668c34` → `75aba6768a` → `f2b94ef8b5`。

## 5. disposition → status 映射

| 轮次记录 | status | 条数 |
| --- | --- | --- |
| `ported…` | `complete` | 172 |
| `aligned…` | `complete` | 26 |
| `n/a…` / `not applicable…` | `not_applicable` | 67 |
| `no go counterpart: …` | `not_applicable` | 3 |
| `refactor only, no behaviour delta in Go` | `not_applicable` | 1 |
| `TLS classification verified; CI packaging not applicable` | `not_applicable` | 1 |
| `partial…` | `partial` | 3 |
| `tracked: …`（未落地、已登记的后续项） | `partial` | 2 |
| `blocked…` | `blocked_precondition` | 0 |

没有任何一行落到 `audit_pending`：275 条全部在轮次记录里找到了判定行，`audit_pending` 清单为空。
找得到记录但映射不了的行（`tracked:` 2 条）按“未闭合”记 `partial`，evidence 保留原文，
不冒充 `complete`。

## 6. kind 归类规则

`kind` 是按 subject 关键词的确定性分类（同一函数在 `update/ledger_align_2026_11_08.py:kind_for` 里，
可重跑复算）：`release`（bazel/cargo/lockfile/npm/installer/packaging/release artifacts/CI/toolchain/rusty_v8/dependency audit）
→ `test`（tests/fixtures/test-only）→ `security`（auth/credential/token/permission/sandbox/security/oauth/tls/certificate/secret/policy）
→ `persistence`（sqlite/migrat/persist/rollout/database/storage/world state）
→ `protocol`（protocol/schema/json/rpc/app-server/appserver/sdk/wire/typescript/api）→ 其余 `runtime`。
275 条分布：runtime 181、security 48、test 19、release 15、persistence 6、protocol 6。

## 7. `#57267` 重审（`654b2ec1f5`）：blocked_precondition → partial

依据 `app/cloud_thread_mode.go`、`app/remote_tui_cloud.go`、`app/remote_tui.go` 现状：

- 已落地：`ThreadParamsMode`/`ThreadSettingsOwner` 映射（`cloud_thread_mode.go`，对应
  `app_server_session.rs:361-402,692,1195-1197`）；server-owned 的 `thread/start`
  （`remoteThreadStartParamsForMode`，`remote_tui.go:6080`）与 `turn/start`
  （`remoteTurnStartParamsForMode`，`:6237`）；跳过本地动态工具 MCP（`:423`、`:3898`）；
  `configRequirements/read` 仅在 cloud 模式容忍 `-32601`（`:1257`）；fork 拒绝（`:1810`、`:6136`）；
  recap/线程标题按所有权门控（`:611-612`）。云传输本身也已存在
  （`app/remote_tui_cloud.go`、`RemoteEndpointCloud`、`TestResolveInteractiveRemoteAddressSelectsCloudLikeRust`）。
- 仍残留（`cloud_thread_plan.md` §7）：生产路径的每轮 client 仍用
  `threadParamsModeForLaunch` 决定模式，而它只会返回 Embedded/Remote，所以 `--remote cloud`
  拿到的仍是 client-owned 设置（`threadParamsModeForEndpoint` 无调用方）；服务器侧
  model/permission 写入、服务器选定权限的提示文案、工作区与 agents overview 的 cwd、
  端到端 cloud 会话测试都还没有。
- 这些不是“架构不可达”，而是接线未做完，因此记 `partial` 而不是 `blocked_precondition`；
  条目 evidence 两条分别写“已落地”和“残留”。

## 8. 计数变化（全部由 `update/ledger_verify_2026_11_08.py` 从 commits.json 重算）

| 指标 | 回写前 | 回写后 |
| --- | --- | --- |
| 条目数 | 3309 | 3584 |
| complete | 2757 | 2955 |
| not_applicable | 547 | 619 |
| partial | 0 | 6 |
| blocked_precondition | 5 | 4 |
| audit_pending | 0 | 0 |
| 重复 sha | 0 | 0 |

新追加的 275 条里 5 条判为未闭合（3 条 `partial` + 2 条 `tracked:`），加上 `#57267` 由
blocked_precondition 转 partial，所以 partial 由 0 变 6、blocked_precondition 由 5 降到 4。

## 9. 其它同步

- `parity/baseline.json`：`target.rustCommit` = `55897425e839d943a744c658701445ff732568a0`；
  `frozenAt` = 2026-11-08；`plan` 指向本文件；`certificationBlockers` 三条由 commits.json 生成
  （总量/状态计数、4 条架构阻塞、6 条 partial），另加 1 条 10 位前缀说明。
  `verified.rustCommit` **保持** `67f0994b…`：它是 `parity.json` 的 `rustUpstreamHead`，
  只有在重跑 SDK/parity 并重新发布 `parity.json` 后才应前移；控制测试
  `TestAlignmentBaselineIsFrozenAndTraceable` 会把两者绑定，提前前移等于谎报认证。
- `parity/domains.json`、`parity/contracts/manifest.json`：`targetRustCommit` 同步到新锚点。
- `parity/alignment_control_test.go`：`candidateRustTo` 前移、条目数 3309→3584、
  sha 校验允许“有 evidence 说明的 10 位前缀”并 `t.Logf` 报数；`verified` 的断言改为
  “非空且 parityManifest 指向 parity.json”，由既有的 certified-head 断言保证可追溯。
- `README.md`：目标提交改为新锚点；`plan.md` / `desgn.md` / `docs/tui_tech_selection.md`
  三个断链分别改为 `update/plan_2026_11_08_ledger.md`、`parity/README.md`、`tui_alignment.md`。
- `need_aligin.md`：顶部加“已过期，最新口径见 docs/alignment_gap_analysis_2026_11_08.md”。

## 10. 校验

```
python3 update/ledger_align_2026_11_08.py      # 回写（幂等：已在账本里的 sha 跳过）
python3 update/ledger_baseline_2026_11_08.py   # 重算 baseline 文本与计数
python3 update/ledger_verify_2026_11_08.py     # 复核条目数/状态计数/重复 sha/头尾/字段
```

`ledger_verify_2026_11_08.py` 输出：`total 3584`、`uniqueSha 3584`、`duplicateSha []`、
`rustToInclusive 55897425e839d943a744c658701445ff732568a0`、`lastSha` 同值、
`sha40 3580`、`prefixOnly10` 4 条（见 §3）、`openStatusCount 0`、`fieldsConsistent true`。

## 11. 剩余项

1. 4 条 10 位前缀需要在权威 Rust 镜像上补齐 40 位（`update/ledger_sha_resolution_2026_11_08.json`
   里 `source = unresolved`），补齐后 `sha40` 应为 3584。
2. `go test ./parity`（含 `TestAlignmentCommitLedgerMatchesRustGitRange`）需要在带完整
   `bd19459358…55897425e8` 历史的 Rust 检出上跑；本机镜像缺 `67f0994b` 与上述 4 个对象，
   该用例在本机无法成立。本轮只做了 JSON 层复核与 Go 语法/gofmt 检查。
3. `parity/domains.json` 有 5 个 domain 为 `partial`（21 complete / 5 partial：
   `alignment-control`、`sqlite-runtime-version`、`app-server-schema`、`sdk-cancellation`、`cross-platform-release`），
   `certificationReady` 保持 `false`。其中 `app-server-schema` 是本轮新降级的，原因见 §13。
4. `docs/alignment_workstream_2026_11_08.md`、`docs/alignment_gap_analysis_2026_11_08.md`
   在本项目里不存在（`docs/` 只有 `release_notes_1e20272fa5.md` 与 `voice-session-lock.md`），
   本文件按 A2A 任务口径引用，待对应 lane 落盘。

## 12. 账本范围外的已落地提交（post-anchor）

`f49ae52079859e9cfff41ed92dd935d022a2da0b`（#59893 Recompute the approval reviewer when switching models）
由 linux_node lane 落地为 **ported**，但它**不在账本范围内**：它是 `55897425e8` 的后代（`git merge-base --is-ancestor
55897425e8 f49ae52079` 成立，反向不成立；提交时间 2026-11-06 12:04，锚点是 06:07）。
`commits.json` 的契约是"恰好等于 `bd19459358…55897425e8` 的 rev-list"，条目数 3584 与
`TestAlignmentCommitLedgerMatchesRustGitRange` 都按这个范围断言，所以**没有**把它塞进数组——塞进去会让两条门禁同时红。
记账位置改为本节，并列入下次重锚的清单。

落地内容（来源：linux_node lane 的本轮报告；`appserver/step_settings_constraints.go` 不在本工作区镜像里，未独立复核）：

- `appserver/step_settings_constraints.go`：新增 `resolveGuardianReviewer`，`validateTurnStepSettings` 在
  「模型变化 + 未显式给 reviewer + 非 Guardian basic-session」时重算 reviewer；写回判据改用 `inheritedReviewer`。
- `appserver/runtime_router.go`：`thread/settings/update` 与 `turn/start` 两条路径在校验后保留已存的
  `RequestedApprovalsReviewer`，避免被强制 `auto_review` 覆盖。
- 测试：`appserver/step_settings_constraints_test.go::TestModelSwitchRestoresRequestedApprovalsReviewerLikeRust`
  （锁 Rust 六步序列：user → protected→auto_review → ordinary→user → 显式 auto_review → protected → ordinary 仍 auto_review）。
- 验证：`go build ./appserver` EXIT=0；定向 5 用例 ok；`go test ./appserver -count=1` 122.8s，仅既有红（见 §13）。

锚点之后还有 3 个上游提交**尚未判定**（`git rev-list 55897425e8..f49ae52079` 共 4 条）：
`ddd1812a51`（#59842 Carry targeted moderation decisions on final-answer messages）、
`b6f53857fc`（#59887 Remove incomplete prereleases after asset upload failures）、
`b1a0dff158`（#59891 Bound REPL provenance costs in elastic context preparation）。
下次重锚到 `f49ae52079` 时，账本会一次增加 4 条（3584 → 3588），其中 3 条需要先判定。

## 13. 既有漂移：precomputed schema 导出落后于 pin（app-server-schema）

linux_node lane 报告的既有红，本轮独立复核（用 `git show 55897425e8:codex-rs/app-server-protocol/schema/precomputed/*.zst`
取 pin 树、`zstd -d` 解压后逐键比对，并对 `appserver/schema/precomputed/*.zst` 直接算 sha256）：

| 项 | stable | experimental |
| --- | --- | --- |
| vendored sha256 | `6e0fb0e51dd149b0add1013835b8caeab941467a6bbf9b62d4a6f3b3b500f072` | `6b336adc058aaacc53614253887415de3c79dae2a946611004be95538f483b8a` |
| `appserver/schema_test.go` 冻结值（round 1195 对 67f0994b 的 pin） | `5a93be76c2f389899b3a201bb470d69e48ac01ddc5233a431613bbff5a634a7f` | `72216e6360b4aadb896c46fe3608f43c2f1f1e46ff894da0f82539703faa5667` |
| typescript 条目（pin vs vendored） | 778 vs 776 | 943 vs 941 |
| json_schema 条目（pin vs vendored） | 328 vs 326 | 476 vs 474 |
| 共有条目内容差异 | 4 | 7 |
| pin 有而 vendored 无 | `v2/GatewayOAuthLoginParams`、`v2/GatewayOAuthReadParams`（typescript + nullable json_schema） | 同左 |

- vendored 侧没有任何"多出来"的条目，全部是落后，方向明确。
- 例：`v2/QueuedSubmission.ts` 缺 `responsesapiClientMetadata`；experimental 另有 `v2/ThreadQueueUpdateParams.ts`、
  `v2/ThreadQueueAddParams.json`、`v2/ThreadGuardianReviewResponse.json` 等。
- 结论：`TestPrecomputedExportArtifactsMatchTargetRustCommit` 在 linux_node 稳定红。

据此把 `parity/domains.json` 的 `app-server-schema` 由 `complete` 改为 **`partial`**（它的验收是"两个导出零 canonical diff"，
现在不成立），evidence 改为上面这组可复算的数字，并在 `baseline.certificationBlockers` 加了一条同口径的说明。
没有为此改动任何 commit 条目的 status：这条漂移是跨域不变量，不是某一条提交的移植结论。

修复路径（沿用 linux_node lane 的 plan）：从 pin 树 re-vendor 两个 `.zst` → 补 Go 协议导出缺的字段/类型
（`GatewayOAuthLogin/Read`、`responsesapiClientMetadata` 等）→ 重新冻结 pin → 跑 `Generate*` 无漂移用例。

## 重锚到 `748ab55ca7de0b647d9a7a9d9a8e70e00f28666d`（/root，2026-11-08）

`55897425e8..748ab55ca7` 的 93 条 delta 判定齐了（`git rev-list --count` 实跑 = 93，本机 `/home/jacks/jacks_dev/codex` 检出在 `748ab55ca7`），账本一次性推到 `748ab55ca7de0b647d9a7a9d9a8e70e00f28666d`：

| 文件 | 改动 |
| --- | --- |
| `parity/commits.json` | 追加 93 条（`update/ledger_delta_748ab55ca7.json`），`rustToInclusive` → `748ab55ca7…`；总 3677 条 |
| `parity/alignment_control_test.go` | `candidateRustTo` 前移，`len(Commits)` 断言 3584 → 3677 |
| `parity/baseline.json` | `update/ledger_baseline_2026_11_08.py` 重算（不手写） |
| `parity/domains.json`、`parity/contracts/manifest.json` | `targetRustCommit` 前移 |
| `README.md`、本文件 | pin sha 前移 |
| `update/ledger_verify_2026_11_08.py`、`update/ledger_align_2026_11_08.py` | 锚点前缀 → `748ab55ca7` |

重锚后计数：**2963 complete / 639 not_applicable / 71 partial / 4 blocked_precondition = 3677**。
其中两条在合并时从我方落地证据上调：`#60038`（`fb2b06a829`）`partial → complete`（`mcp.ServerStartupError` + `ModelVisibleError` + dispatcher 转工具结果，`TestCachedToolCallSurfacesMCPServerStartupErrorLikeRust`）、`#60037`（`7ccf78dec4`）`partial → complete`（用例冻结既有对齐行为，判 aligned）。
`#60027`（`66a56b17a1`）由 aligned 更正为 `partial`（预览路径仍按旗标切渲染器，与 live footer 不一致）；`#60018`（`5ff78c5e66`）新判 `partial`（六类 usage 事件缺 `turn_trigger`）。

### 一个必须先说清的来源不一致（不是本次引入）

`parity/commits.json` 里**重锚前的 3584 条**来自 GitHub compare（`api.github.com/repos/openai/codex/compare/...`），不是本机检出 `git rev-list` 的结果。本机 `/home/jacks/jacks_dev/codex` 上：

- `git rev-list --count bd19459358..748ab55ca7` = **983**，`..55897425e8` = **890**；
- 随机抽样 10 条旧账本提交，**4 条是 `748ab55ca7` 的祖先、6 条不是**——即旧账本里有相当一部分 SHA 属于另一条（被 rebase 过的）历史线。

本次追加的 93 条则是真 checkout 的 `git rev-list --reverse 55897425e8..748ab55ca7` 输出，来源与旧条目不同。后果：`TestAlignmentCommitLedgerMatchesRustGitRange` 在本机**必然红**（983 vs 3677），该用例默认靠 `CODEX_RUST_ROOT` 跳过，所以从未真正跑过。要不要把旧账本整体改挂到本机检出、还是以 GitHub compare 为唯一事实源，需要单独决策；在决定之前不要设 `CODEX_RUST_ROOT` 跑这条用例，也不要据此认定账本"对不上"。

`app-server-schema` 的 re-vendor 目标随之变为 **`748ab55ca7`**（`#60004`/`#60082` 都动了 `schema/precomputed/*.zst`，按旧 pin 补会白跑一版）。

## 判定合并与契约登记记录（/root，2026-11-08）

已合并的判定文档（全部取自 ledger lane `deliveries/` 根目录的 zip，逐条 `sha256sum -c` OK）：

| 批次 | 文档 | 条目 |
| --- | --- | --- |
| 其它组 1/2/3 | `plan_2026_11_08_misc_{1,2,3}.md` | `#60172`/`#60075`/`#60048`；`#60011`/`#59963`/`#59910`；`#59949`/`#60008`/`#60180` |
| 模型/Provider 1/2/3 | `plan_2026_11_08_model_{1,2,3}.md` | `#60186`/`#60009`/`#59987`；`#60049`/`#59958`/`#59933`；`#60080` |

两批 16 条里，`#60048`、`#59949`、`#60008` 不占契约位（前两条纯阈值/已 aligned，第三条纯重构），`#60080` 判 n/a，其余 12 条判 `partial`。

契约只登记"可立即登记"的 9 条（verifier 全部经全树 grep 确认存在，`TestContractVerifiersExistInGoTestFiles` 通过），均 `partial`：
`multi-agent-hosted-tools-guidance`、`turn-context-required-permission-profile`、`patch-approval-cancel-turn-scope`、`skill-mention-punctuation-join`、`provider-creation-during-signin`、`tui-custom-gateway-signin`、`child-agent-catalog-preservation`、`thread-model-metadata-exposure`、`protected-model-profile-downgrade`。
暂不占位：`answer-moderation-display-metadata`、`async-host-service-tier-acceptance`、`startup-model-from-thread-options`（树上无对应测试，落地同批再登记）。

契约现状：**129 条 / 90 complete / 39 partial**。门禁：`go test ./parity -run 'Contract|Verifiers|Domains|Readiness|Alignment'` ok；`update/ledger_verify_2026_11_08.py` → `GATE: PASS`；锚点仍 3584 / `55897425e8`。

下一批改做 **发布/CI 10 条**——`#60042`/`#60041`/`#60034` 已由 Windows lane 判完并落地（ported / ported / not_applicable），不再重复判。

### 发布/CI 10 条（2026-11-08 追加）

已合并 `update/plan_2026_11_08_ci_{1,2,3}.md`（取自 `deliveries/` 的 zip，逐条 `sha256sum -c` OK）。判定 **not_applicable 8 + partial 2**：
n/a —— `#60206`（Bazel 分片）、`#60173`（纯测试）、`#60017`（纯测试 +2 行）、`#60010`（Windows Bazel action，pin 树 MISS）、`#59980`/`#59974`（纯 Rust 测试，Go 已有对应校验面）、`#59972`（Opus 头文件 patch，pin MISS）、`#59955`（Cargo 版本号）；
partial —— `#59887` + `#59903`，Go 的 `.github/workflows/release.yml` 与 Rust 修前同构：`softprops/action-gh-release` 一步建 release + 上传资产，prerelease 直接公开，上传失败会留下"公开但不完整"的 release，而 release 事件只对新 release 触发，重跑无法补救。

**已落地**（本批唯一有实义的缺口，我自己做的，没派给 lane）：
- 新增 `.github/scripts/publish-github-release.sh`：先 `gh api` 建 release 并记住 id（stable 以 draft 建、prerelease 公开建），再上传资产，`trap` 里**只在"本次创建"且"上传未完成"**时按 id 删除；已存在的 release 永不删除，tag 永不触碰，上传成功后的元数据失败也不删。
- `.github/workflows/release.yml` 的 `softprops/action-gh-release` 步骤换成调用该脚本（env 传 `TAG_NAME`/`RELEASE_NAME`/`NOTES_FILE`/`DIST_DIR`/`PRERELEASE`）。
- 新增 `parity/release_publish_script_test.go`：`TestPublishReleaseCleansUpFailedPrereleaseById` 用 PATH 上的 stub `gh` 跑真实脚本，覆盖四条语义（上传失败删本次新建 / 既有 release 不删 / 成功则发布 draft / 上传完成后元数据失败不删）。
- 契约 `release-prerelease-cleanup` 登记为 `partial`（verifier 就是上面这条用例；真实 GitHub Actions 未跑过，所以不标 complete）。

契约现状：**130 条 / 90 complete / 40 partial**。
