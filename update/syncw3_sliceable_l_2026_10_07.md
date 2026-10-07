# syncw3 · 超阈值（>5 文件）域内提交 · 「≤5 文件可独立成笔」切片潜力评估

- **round**：r88e（派单 `msg-1791380411591022100-5798` §④）
- **日期**：2026-10-07
- **Rust 枚举 pin**：`d83bb540ec64bf6b009bca0283b0be91ea33f26a`
- **Go 行号锚**：`d12d00dd`（`origin/main` 现已前进到 `c9fbd10f`=integ86g；本单只读、未按新 main 复算行号，如需请再派）
- **纪律**：本单 **只读** —— 0 补丁 / 0 commit / 0 push / 0 tag / 0 ref 移动；未改避让面（`appserver/runtime_router.go` / `appserver/turn_runtime.go` / `tui/state.go` / `sandbox/windowssandbox/`）。

---

## 0. 口径与复跑命令（全部本次实跑）

**集合定义**（15 笔 = 超阈值域内提交全集，与 round88 §6 同源）：

```
python D:\qax\reagent\dev\syncw3_scan\scan3_window.py   # 生成 win200.json（-200 @ d83bb540）
python D:\qax\reagent\dev\syncw3_scan\scan3_big.py      # 域 crate ∩ (文件数>5)  -> 15 笔
```

域 crate = `app-server-daemon / exec-server(+protocol) / sandboxing / linux-sandbox / mxc-sandbox / bwrap / windows-sandbox-rs / windows-sandbox-service / exec`。

**Go 载体探针**（只算 `*.go`，不含 `update/*.md`）：

```
git -C D:\qax\reagent\dev\codex_go grep -n -E '#<PR>' d12d00dd -- '*.go'
```

**Rust 对照**（Rust 工作树是 CRLF 且停在旧点，只读 `show`；不 checkout）：

```
git -C D:\qax\reagent\dev\git\codex show <sha> -- <path>
```

---

## 1. 结论摘要

1. 15 笔超阈值域内提交中，**唯一「未落地真缺口」= `#50559`**（`1a169eda11`，`app-server-daemon`）。
2. **切片潜力：1 条**（≤5 条上限内）——**S1 `#50559`**：生产 **5 文件**、**非避让面**、S/M；但含仓内回归测试合计约 **6–7 文件 ⇒ 略超 ≤5 阈值（边缘）**。已给出两种子切法供裁定。
3. 其余 **14 笔**判定：**已落地 8**（`#51650 #51595 #51525 #51502 #51415 #51347 #51157 #50465`）、**已落地但落点在避让面 1**（`#51512`）、**已落地（他人车道/don't-repeat）1**（`#50437`）、**已覆盖（预存在于避让面）1**（`#51211`）、**已等价/有意 N/A 1**（`#51207`）、**无载体（机制性 N/A）2**（`#50507`、`#50360`）。⇒ 均**不**构成可切笔（无 ≤5 文件残余生产改动）。
4. **没有**第二个「0 引用 + 可切」项：`#50559` 与 `#50360` 是本集合里仅有的两个 `#PR` 引用 = 0 的提交，其中 `#50360` 经正向证据判定为机制性 N/A（见 §3 尾注）。

---

## 2. 切片潜力表（本单产出，上限 5 条）

### S1 ｜ `#50559` ｜ sha `1a169eda11` ｜ **可切割 · 边缘（生产 5 文件）**

| 项 | 值 |
|---|---|
| Rust 标题 | Distinguish daemon release identity from executable contents (#50559) |
| Rust 文件数 | 6（4 生产 + 2 测试）：`managed_install.rs` `manual_update.rs` `prepare_install.rs` `update_loop.rs` + `managed_install_tests.rs` `update_loop_tests.rs` |
| Go 载体 | **`#50559` 引用 = 0**（原文见下）；机制已存在的近邻：`appserverdaemon/{daemon.go,update_lane.go,update_loop.go,lifecycle.go,manual_update.go,prepare_install.go}` + `install/context.go` |
| 规模 | **M**（4 个 Rust 生产 hunk 跨 identity 结构与重启判定） |
| 撞避让面 | **否**（落点 `install/` + `appserverdaemon/`，均不在避让面清单内） |
| Windows-only | **否**（跨平台；`path_digest` 有 unix/windows 两分支，Go 侧 `filepath.Clean` 即可，本机可跑值级 RC） |
| 类型 | **真缺口候选（行为级）** |

**Rust 语义（3 步）**

1. `managed_install.rs`：`ExecutableIdentity` 增加 `path_digest: Option<[u8;32]>`；`executable_identity()` 先 `canonicalize` 再摘要并写入 `path_digest`；新增 `same_contents()`（只比 `digest`）。
2. `manual_update.rs` / `prepare_install.rs`：比较由 `!=` 改为 `same_contents()`（内容级）。
3. `update_loop.rs`：`restart_mode` 映射改为 —— `Manual | RestoreProduction(_)` ⇒ 恒 `IfBinaryOrVersionChanged`（删去「release 变了就 `Always`」分支）；`Scheduled` 且 executable identity 变了 ⇒ `IfBinaryOrVersionChanged`（原 `Always`）；新增 `NotReady + Scheduled + IfBinaryOrVersionChanged ⇒ Continue/NotReady`。提交信息自陈意图：「stale updater should be able to hand off **without restarting an already-current daemon**」。

**Go 现状证据（未落地，原文）**

```
> git -C D:\qax\reagent\dev\codex_go grep -n -E '#50559' d12d00dd -- '*.go'
（无输出，exit=1）

> git ... grep -n -E 'path_digest|same_contents|SameContents|PathDigest' d12d00dd -- '*.go'
（无输出，exit=1）

install/context.go:80:  type ExecutableIdentity struct {
install/context.go:81:      Digest string `json:"digest"`
install/context.go:82:  }                       # 只有内容摘要，无 path digest
install/context.go:353: func ExecutableIdentityFromFile(path string) (ExecutableIdentity, error) {   # 未 canonicalize，未置 path digest
```

Go 侧仍是 **#50559 之前的上游映射**：

```
appserverdaemon/update_lane.go:157  func restartModeForTrigger(trigger UpdateTrigger, releaseChanged bool, managedIdentity, runningUpdaterIdentity install.ExecutableIdentity) RestartMode {
appserverdaemon/update_lane.go:159      case UpdateTriggerManual, UpdateTriggerRestoreProduction:
appserverdaemon/update_lane.go:160          if releaseChanged { return RestartAlways }        # Rust #50559 已删除该分支
appserverdaemon/update_lane.go:165          return RestartIfBinaryOrVersionChanged
appserverdaemon/update_lane.go:166      default:
appserverdaemon/update_lane.go:167          if managedIdentity != runningUpdaterIdentity { return RestartAlways }   # Rust #50559 已改为 IfBinaryOrVersionChanged
```

配套的内容级比较也未加（Go 现在是整结构 `!=`，加了 `PathDigest` 后会变成路径敏感，正是 Rust 用 `same_contents()` 规避的点）：

```
appserverdaemon/manual_update.go:356     updated := previousRelease != currentRelease || previousIdentity != currentIdentity
appserverdaemon/prepare_install.go:537   stagedIdentity != runningIdentity        # 需改 SameContents
appserverdaemon/lifecycle.go:464-478     RestartIfBinaryOrVersionChanged 由 *runningIdentity == managedIdentity 解析（同为内容摘要）
```

**缺口性质**：Go 用「整结构相等」当「内容相等」，且重启判定仍是旧映射 ⇒ 相对 Rust 会**过度重启**（`Manual/RestoreProduction` release 变化时无条件下 `RestartAlways`；`Scheduled` identity 变化时下 `RestartAlways`），即 #50559 想消除的「already-current daemon 被无谓重启」在 Go 仍存在。**行为级差异，非崩溃**；在任意 daemon 更新/重启流程可达。

**最小笔（建议落点 file:line，生产 5 文件）**

| # | 文件 | 落点 | 动作 |
|---|---|---|---|
| 1 | `install/context.go` | `:80-82` / `:316-319` / `:353-364` | 加 `PathDigest`（`json:"pathDigest,omitempty"`）+ `SameContents()`；`ExecutableIdentityFromFile` 先 canonicalize 并写 PathDigest；`ExecutableIdentityFromBytes` 置空 |
| 2 | `appserverdaemon/update_lane.go` | `:157-172` | `restartModeForTrigger` 改为 Rust 新映射（`Manual/RestoreProduction` 恒 `IfBinaryOrVersionChanged`；`Scheduled`+identity 变 ⇒ `IfBinaryOrVersionChanged`） |
| 3 | `appserverdaemon/update_loop.go` | `:316-323` / `:337-357` | 对齐 `AlreadyCurrent`（去掉 `trigger!=Scheduled` 限制）与新增 `NotReady+Scheduled+IfBinaryOrVersionChanged ⇒ Continue/NotReady` 分支 |
| 4 | `appserverdaemon/manual_update.go` | `:356` | `same_contents` ⇒ `previousIdentity.SameContents(currentIdentity)` |
| 5 | `appserverdaemon/prepare_install.go` | `:537` | `stagedIdentity.SameContents(runningIdentity)` |

加 1 条仓内回归测试（建议 `appserverdaemon/executable_identity_test.go`，纯单元级、直接构造 `ExecutableIdentity`）⇒ **合计 6 文件**；若同时扩 `install/context_test.go` ⇒ 7。

**值级 RC 计划**（落地时执行）：撤 `install/context.go` 的 `PathDigest` 写入 + 撤 `update_lane.go:160/167` 两处映射（断言式替换 `assert s.count(old)==1`）⇒ `go test ./appserverdaemon/ -run 'ExecutableIdentity|RestartMode' -count=1` 期望 **FAIL（值级：路径不同但内容相同的两版本被判等 / restart mode = always）** ⇒ 恢复 ⇒ ok。

**边缘说明 / 两种子切法（请队长裁定）**

- 子切 A：`install/context.go`（+`context_test.go`）= 2 文件「使能笔」：加 `PathDigest`+`SameContents`。可独立编译/测试，但**无生产消费者 ⇒ 单独不产生行为变化**。
- 子切 B：`appserverdaemon/{update_lane,update_loop,manual_update,prepare_install}.go`（+1 测试）= 5 文件：消费侧映射。**依赖 A 的 `SameContents`/`PathDigest` ⇒ 不满足「独立成笔」**。
- ⇒ 若「≤5 文件」只计生产文件，本笔 = 5，**卡线合格**；若计全部文件，= 6–7，**略超**。请裁定按一笔（含测试 6）落，还是允许 A→B 依序两笔。

---

## 3. 其余 14 笔全量判定（`#PR` 引用 = 生产/测试 `.go` 命中数，锚 `d12d00dd`）

| # | sha | PR | n | 标题 | 判定 | Go 载体（refs / 关键 file:line） |
|---|---|---|---|---|---|---|
| 1 | `37eaae6eeb` | 51650 | 9 | Require hostname authorization before proxy DNS lookups | **已落地** | refs=8；`network/proxy_server.go:1639,1663,1680,1800` + `proxy_server_51650_test.go`（don't-repeat） |
| 2 | `1fbe15c962` | 51595 | 31 | Add thread ID exclusions to `thread/list` | **已落地** | refs=10；`appserver/protocol.go:2326-2330,2495-2501,3431-3434`、`session/store.go:397-400,1918`、`thread_list_exclusions_test.go`、`parity/rust_snapshot_test.go:46,310`（Rust 侧 TUI 改动只是 `excluded_thread_ids: None` 机械补空，Go 用 `*[]string`+`omitempty` 无需同改） |
| 3 | `ed59a6c1cd` | 51525 | 13 | Preserve the CLI MXC preference in executor config reads | **已落地** | refs=1(测)；生产载体 `execserver/hostconfig/read.go:37,56-62`（`preferMXC`+`InsertSessionFlagsLayer`，注释自陈 mirrors Rust），`read_test.go:80` |
| 4 | `7efc49b258` | 51512 | 12 | Align Windows sandbox temp permissions with the child environment | **已落地 · 在避让面** | refs=2；`sandbox/windowssandbox/resolved_permissions.go:139` + `_test.go:63` ⇒ **撞避让面，只登记不落地** |
| 5 | `ddabe594e6` | 51502 | 7 | Bound relay connection attempts / handle pongs during blocked writes | **已落地** | refs=7；`execserver/remote.go:35,118,231,245`、`remote_relay.go:266`、`remote_reconnect_backoff_test.go:16,55` |
| 6 | `cfc946f4e1` | 51415 | 149 | Expose and persist turn lineage across the app server | **已落地**（整笔 L，非可切） | refs=6；`turn/api.go:277,571`、`appserver/protocol.go:851`、`appserver/turn_runtime.go:3883`（避让面内已有实现）+ 测试 |
| 7 | `588f616e8b` | 51347 | 6 | Measure shell snapshot use and wait time per command | **已落地** | refs=9；`appserver/shell_snapshot.go:125,239`、`tool/shell_executor.go:93,815`、`tool/shell_snapshot_builder.go:103,112`、`telemetry/metric_names.go:72` |
| 8 | `7aa8f51049` | 51211 | 15 | Reject sandbox-writable bubblewrap executables from PATH | **已覆盖（预存在于避让面）** | refs=13；`sandbox/linuxsandbox/bwrap*.go`、`sandbox/bwrap.go:74`、`sandbox/sandboxpath/*`；其中 `appserver/runtime_router.go:6598` 属避让面 ⇒ 按 r88d §②口径**计已覆盖、不算缺口**（前轮 sync449 `cbaf8ee6`+sync467 `bbd92988` 同口径） |
| 9 | `5ad6891696` | 51207 | 21 | Gate CLI Daybreak controls and selection behind an opt-in feature | **已等价 / 有意 N/A** | refs=3；`config/config.go:166-175,701-704`、`features/features.go:318`。Go 注释明确：「Rust additionally gates the CLI Daybreak selection behind the `cli_daybreak` feature (#51207); **Go only grounds the config key here**」⇒ Go 无 `cli_daybreak` feature 机制，属有意不实现 |
| 10 | `42312d4ff4` | 51157 | 33 | Enforce required environment skills before model inference | **已落地** | refs=39；`appserver/environment.go:153-486`、`appserver/required_skills.go`、`turn/required_skills.go`、`turn/agent_loop.go:189,342`、`mcp/required_environment_skills.go`、`execserver/environments_toml.go:94-151`（+测试） |
| 11 | `1a169eda11` | **50559** | 6 | Distinguish daemon release identity from executable contents | **真缺口候选（未落地）** | **refs=0**；见 §2 S1 |
| 12 | `c542fb93ef` | 50507 | 6 | Record Windows sandbox service stop diagnostics | **无载体（机制性 N/A）** | refs=0；r88d §①已裁定：Go 无 SCM 注册型 Windows 沙箱服务子系统，落点必在 `sandbox/windowssandbox/`（避让面）且 ≥6-10 文件=L |
| 13 | `ef8cfe5e96` | 50465 | 7 | Retry registry authentication outages and jitter executor reconnects | **已落地** | refs=8；`execserver/remote.go:88,97,333,363,469-490`、`remote_retry_test.go:121,164` |
| 14 | `12a30d4e6d` | 50437 | 6 | Add a CLI command to uninstall the legacy Windows sandbox | **已落地（他人车道 / don't-repeat）** | refs=9；`app/app.go:282,1400`、`cli/cli.go:1807`、`sandbox/windowssandbox/uninstall*.go`（syncw4 `syncw4_50437.patch` 已落） |
| 15 | `c73775f19e` | 50360 | 13 | Remove initial messages from session configuration events | **无载体（机制性 N/A）** | refs=0；见下方尾注 |

### 尾注：`#50360` 判「无载体（机制性 N/A）」的正向证据

Rust 改动 = 从 `SessionConfiguredEvent` 删除 `initial_messages` 字段、删除 `InitialHistory::get_event_msgs`、停止向 startup 事件挂 replay history。Go 侧**正向陈述**：不存在承载该行为的类型/字段/路径。

```
> git -C D:\qax\reagent\dev\codex_go grep -n -F 'initial_messages' d12d00dd -- '*.go'
d12d00dd:tui/chatwidget/protocol.go:9:  ReplayResumeInitialMessages ReplayKind = "resume_initial_messages"
      # 命中的是 TUI 客户端侧 replay kind 字面量，非 wire 字段
> git ... grep -n -F 'get_event_msgs' d12d00dd -- '*.go'
（无输出，exit=1）
> git ... grep -n -F 'SessionConfiguredEvent' d12d00dd -- '*.go'
（无输出，exit=1）
> git ... grep -n -F '"initial_messages"' d12d00dd -- '*.go'
（无输出）
```

⇒ Go 的 app-server 会话配置事件**从未携带 `initial_messages`**，也没有 `InitialHistory::get_event_msgs` 等价物；resume 回放由 TUI 侧 `InitialHistoryCells` / `ReplayResumeInitialMessages` 自有通路承载。**无删除对象 ⇒ 机制性 N/A**（非「已落地」，是「Go 无该机制」）。

---

## 4. 未决 / 待裁

1. **S1 `#50559` 的文件计数阈值**：生产 5 文件（卡线合格）vs 含测试 6–7 文件（略超）。请裁定按「一笔含测试」落，还是授权 A（`install/context.go` 使能）→ B（`appserverdaemon` 消费）依序两笔。
2. **S1 是否越域**：`install/context.go` 是顶层 `install/` 包（不在 r88 域 `appserverdaemon/ execserver/ sandbox/` 之列，也不在避让面）。若限域，需裁定是否允许改 `install/`（模态：可把 `PathDigest`+`SameContents` 只加在 `appserverdaemon/managed_install.go` 的包装层，但那样 struct 比较会漏路径语义，**不推荐**）。
3. **`#51207`（Go 有意不实现 `cli_daybreak` feature gate）** 与 **`#51211`（预存在于避让面）** 本单沿用既有口径，如需复核请单独派单。
4. 本单未对 15 笔中的**非 `*.go` 观众**（如 `appserver` 协议 schema/快照）逐条对照；如需可另派。
5. 行号锚为 `d12d00dd`；若 main 已前进到 `c9fbd10f`，需要新锚请明示。
