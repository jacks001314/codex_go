# round87 新料清单（syncl5 · Linux 节点 · 只读扫描）

- 车道：**syncl5**；任务：队长 `msg-1791373103104453400-4862`「round87 上游再扫」
- Rust 仓：`/home/jacks/jacks_dev/codex`；freeze pin = **`5b0b2530354052b9194156d70d4c94a439368342`**（工作树 HEAD 未动）
- Go 仓：`/home/jacks/jacks_dev/codex_go`；**Go 最新 = `658dc9c1e0d25d3dedaa8f95f81b1775b1a710f5`**（sync629，含 sync627 `#49584` / sync628 `#50756` / sync629 `#49714`）
- 纪律：**只读**（本报告以外 0 改动；0 commit / 0 push / 0 ref 移动；未执行任何落地）

## 0. 扫描命令与结果概览

```zsh
$ cd /home/jacks/jacks_dev/codex && git fetch origin main
From github.com:openai/codex
 * branch                  main       -> FETCH_HEAD
   37eaae6eeb..b17c74cfd5  main       -> origin/main
（fetch rc=0）

$ git log --format='%H %h %ad %s' --date=short 5b0b2530354052b9194156d70d4c94a439368342..origin/main
b17c74cfd5ebb39fe70ffaff78de198120278636 b17c74cfd5 2026-10-07 Record telemetry for AGENTS.md changes made by apply_patch (#51652)
30bdfec59ddf8df9eaf44e8f367ba7fef4b2ffbc 30bdfec59d 2026-10-07 Persist Guardian review failures for reports across restarts (#51651)
37eaae6eeb71c3bd3d165f52515086db8f5e7dee 37eaae6eeb 2026-10-07 Require hostname authorization before proxy DNS lookups (#51650)
a5130128697b10022a88e8f5eae6dca77393b4b0 a513012869 2026-10-07 Fix retained context handling for typed section content (#51642)
```

**⇒ 上游未穷尽：比你给的快照（2 笔）多出 2 笔。**
区间共 **4 笔**：`#51642`（前轮已判 N/A）、`#51650`（你标「在飞」）、**`#51651`（新）**、**`#51652`（新）**。
`origin/main` 由 `37eaae6eeb` 前进到 **`b17c74cfd5`**（你 fetch 之后上游又推了 `30bdfec59d` + `b17c74cfd5`）。

## 1. 逐笔明细

### 1.1 `#51642` · `a513012869` · **已知（前轮已判 N/A，沿用）**

```
$ git show --stat --format=%H%n%s a513012869
a5130128697b10022a88e8f5eae6dca77393b4b0
Fix retained context handling for typed section content (#51642)

 .../guardian-context/src/retained_instructions.rs    | 20 ++++++++------------
 codex-rs/guardian-context/tests/cache_prefix.rs      | 14 +++++++++++---
 2 files changed, 19 insertions(+), 15 deletions(-)
```

- **≤5 文件**：是（2）
- **五分类**：**已取代型**（前轮 N/A，且 Go 侧载体存在：`state/guardian_retained_context.go:11` 自陈 "Rust parity: codex-rs/guardian-context/src/retained_instructions.rs"，`appserver/retained_context.go:34`、`appserver/retained_context_test.go:166`、`appserver/retained_instruction_test.go:48` 均为同面回归）
- 处置：**沿用前轮 N/A，不再动**

### 1.2 `#51650` · `37eaae6eeb` · 新分析（你标「在飞」）

```
$ git show --stat --format=%H%n%s 37eaae6eeb
37eaae6eeb71c3bd3d165f52515086db8f5e7dee
Require hostname authorization before proxy DNS lookups (#51650)

 codex-rs/network-proxy/src/host_policy_tests.rs   | 129 ++++++++++++++++++++++
 codex-rs/network-proxy/src/mitm.rs                |   8 +-
 codex-rs/network-proxy/src/mitm_tests.rs          |  37 +++++++
 codex-rs/network-proxy/src/network_policy.rs      |  28 ++++-
 codex-rs/network-proxy/src/proxy.rs               |  23 +++-
 codex-rs/network-proxy/src/runtime.rs             |  67 ++++++++---
 codex-rs/sandboxing/src/seatbelt.rs               |   8 +-
 codex-rs/sandboxing/src/seatbelt_network_tests.rs |  88 +++++++++++++++
 codex-rs/sandboxing/src/seatbelt_tests.rs         |   4 -
 9 files changed, 357 insertions(+), 35 deletions(-)
```

- **≤5 文件**：**否（9）⇒ 规模 L**
- Rust 语义（逐 hunk）：`HostAuthorization{RequireAllowlist, Approved}`；`host_blocked*` 仅当 **`authorization == Approved` 或 allowlist 命中** 才做 `lookup_host` DNS 解析（`if is_authorized && host_resolves_to_non_public_ip(...)`）；批准后**重查** baseline（`host_blocked_with_local_binding(..., HostAuthorization::Approved)`）以防 approval 绕过 explicit deny / local-private；去掉 Seatbelt 的 external DNS 允许。

**Go 载体预检（打到 `658dc9c1`）**

```
$ git grep -n 'func .*evaluateProxyPolicy' 658dc9c1 -- 'network/*.go'
658dc9c1:network/proxy_server.go:1594:func (s *ProxyServer) evaluateProxyPolicy(ctx context.Context, request ProxyPolicyRequest) ProxyDecision {

$ git grep -n 'proxyHostResolvesToNonPublicIP' 658dc9c1 -- 'network/*.go'
658dc9c1:network/proxy_server.go:1629:		} else if (allowed || s.policyDecider != nil) && proxyHostResolvesToNonPublicIP(normalizedHost, request.Port) {
658dc9c1:network/proxy_server.go:1749:func proxyHostResolvesToNonPublicIP(host string, _ uint16) bool {

$ git grep -n 'net.DefaultResolver\|LookupIPAddr' 658dc9c1 -- 'network/*.go'
658dc9c1:network/proxy_server.go:1538:	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
658dc9c1:network/proxy_server.go:1752:	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)

$ git grep -n '53\|dns\|DNS' 658dc9c1 -- sandbox/seatbelt.go
（0 命中）
```

- **五分类**：**均不适用 ⇒ 候选真缺口**。理由（`file:line`）：
  1. Go 的 DNS 门是 `(allowed || s.policyDecider != nil)`（`network/proxy_server.go:1629`），而 Rust 新逻辑是 `approval || allowlist 命中`（`is_authorized`）。**decider 存在但尚未批准时 Go 仍会解析** ⇒ 与 Rust「resolve 前必须先授权」的目标相反。
  2. `proxyHostResolvesToNonPublicIP`（`network/proxy_server.go:1749-1764`）确为真实 DNS（`net.DefaultResolver.LookupIPAddr`，2s 超时）。
  3. Go 的 decider-Allow 分支（`evaluateProxyPolicy`，`proxy_server.go:1648-1660`）**没有**「批准后重查 baseline（deny / 非公网）」的步骤 ⇒ 对应 Rust 新增的第二条保护缺失。
  4. `dialCheckedTarget`（`proxy_server.go:1525`）对非 IP-literal 目标**无条件** `LookupIPAddr`（`:1538`），需要确认它只会在已授权路径到达。
  5. Seatbelt 半片：`sandbox/seatbelt.go` 无 `dns/DNS/53` 命中 ⇒ 该半片在 Go 侧形态不同（Go 的 seatbelt profile 未显式允许 external DNS），需单独确认是「已等价」还是「无载体」。
- 处置建议：**L + 需深读 ⇒ 不开工，报你裁定**（若你要收，建议先做一次只读深读，把上面 5 点逐一确证/证伪再定 S/M 切分）。
- 若一定要切一刀：`proxy_server.go` 单文件（Gate 1+2+3）可能是 M；但 4/5 两点跨文件 ⇒ 我倾向仍按 L 处理。

### 1.3 `#51651` · `30bdfec59d` · **新**

```
$ git show --stat --format=%H%n%s 30bdfec59d
30bdfec59ddf8df9eaf44e8f367ba7fef4b2ffbc
Persist Guardian review failures for reports across restarts (#51651)

 .../src/request_processors/feedback_processor.rs    |   3 +-
 .../src/request_processors/feedback_thread_index.rs |   6 +-
 codex-rs/app-server/tests/suite/v2/feedback.rs      |  71 ++++++++++----
 codex-rs/core/src/guardian/feedback.rs              |  11 ++-
 codex-rs/core/src/guardian/tests.rs                 |   2 +-
 codex-rs/core/tests/suite/guardian_review.rs        |  27 +++++-
 .../tests/suite/guardian_subagent_authorization.rs  |   6 +-
 codex-rs/ext/guardian-reviewer/src/feedback.rs      |  12 +--
 .../ext/guardian-reviewer/src/feedback_tests.rs     |  21 +++--
 codex-rs/feedback/src/guardian.rs                   | 104 +++++++++++++++------
 codex-rs/feedback/src/guardian_tests.rs             |  47 +++++++++-
 codex-rs/feedback/src/lib.rs                        |   1 +
 .../migrations/0060_guardian_review_feedback.sql    |   5 +
 codex-rs/state/src/lib.rs                           |   4 +
 codex-rs/state/src/runtime.rs                       |   5 +
 codex-rs/state/src/runtime/guardian_feedback.rs     |  92 ++++++++++++++++++
 codex-rs/state/src/runtime/guardian_feedback_tests.rs | 99 +++++++++++++++++
 17 files changed, 442 insertions(+), 74 deletions(-)
```

- **≤5 文件**：**否（17）⇒ 规模 L**
- Rust 语义：把 `feedback/src/guardian.rs` 里原来的**进程内** bounded failed-review 缓冲（`MAX_RECORDS=64` / `MAX_RECORDS_PER_THREAD=8` / `MAX_BYTES=8MiB`）升级为 **SQLite 持久化**（新迁移 `0060_guardian_review_feedback.sql` + `state/src/runtime/guardian_feedback.rs`），带 250ms 超时 + 内存 fallback，文件删除级联删除，上传 `auto-review-failures.jsonl` 时合并去重。

**Go 载体预检（打到 `658dc9c1`）**

```
$ git grep -n 'guardian_review_feedback\|guardian_feedback\|auto-review-failures\|GuardianReviewRecord' 658dc9c1
（无输出）exit=1

$ git grep -n 'recordGuardianReviewFailure\|GuardianReviewFailures\|guardianReviewFailures\|discardedRecords' 658dc9c1 -- '*.go'
（无输出）exit=1

$ git ls-tree -r --name-only 658dc9c1 -- state/migrations/state | tail -4
state/migrations/state/0047_rollout_migration_state.sql
state/migrations/state/0048_thread_section_appearance.sql
state/migrations/state/0049_projects.sql
state/migrations/state/0050_threads_section_empty_preview_indexes.sql
```

- **五分类**：**无载体型**。Go 侧**连被持久化的那个「进程内 failed-review 证据缓冲」都不存在**（`recordGuardianReviewFailure` / `GuardianReviewFailures` / `GuardianReviewRecord` 全 0 命中），Go `state/migrations/state/` 只到 `0050`，无 `0060_guardian_review_feedback.sql`；`appserver/guardian_reviewer.go` 只有 reviewer 本体与失败文案（`:162`/`:170`/`:176`），`appserver/feedback.go` 无 `auto-review-failures` 上传路径。
- 处置：**L + 无载体 ⇒ 机制性 N/A（建议）**；若要收，等于先移植「Guardian failed-review 证据缓冲」整条，再谈持久化 ⇒ 新立项、走用户决策清单。

### 1.4 `#51652` · `b17c74cfd5` · **新**

```
$ git show --stat --format=%H%n%s b17c74cfd5
b17c74cfd5ebb39fe70ffaff78de198120278636
Record telemetry for AGENTS.md changes made by apply_patch (#51652)

 codex-rs/core/src/tools/runtimes/apply_patch.rs | 25 ++++++++++++++++++++++++-
 1 file changed, 24 insertions(+), 1 deletion(-)
```

- **≤5 文件**：**是（1）⇒ 规模 S（24 行）**
- Rust 语义：`ApplyPatchRuntime::run` 里遍历 committed `delta.changes()`，对 basename 大小写不敏感等于 `agents.md` / `agents.override.md` 的路径（含 `Update{move_path}` 的不同目标）打计数 `ctx.step_context.session_telemetry.counter("codex.agents_md.edit", 1, &[("filename", filename)])`。

**Go 载体预检（打到 `658dc9c1`）**

```
$ git grep -n 'agents_md.edit\|agentsMdEdit\|agents_md_edit' 658dc9c1
（无输出）exit=1          # 计数器 0 命中（telemetry/metric_names.go 里也没有）

$ git grep -n 'FileChangeTracker\|\.Track(' 658dc9c1 -- '*.go' | grep -v _test
658dc9c1:appserver/turn_runtime.go:4584:	tracker.Track(runtimeFileChangeEnvironmentID(item), changes, true)

$ git show 658dc9c1:runtimeutil/diff_tracker.go | sed -n '12,30p'
type FileChangeKind string
const ( ChangeAdd FileChangeKind = "add"; ChangeDelete = "delete"; ChangeUpdate = "update" )
type FileChange struct { Kind FileChangeKind; Path string; MovePath *string; OldContent string; NewContent string; OverwrittenContent *string }

$ git grep -n 'func (r \*RuntimeRouter) sessionTelemetryForThread' 658dc9c1 -- appserver/session_telemetry.go
658dc9c1:appserver/session_telemetry.go:26:func (r *RuntimeRouter) sessionTelemetryForThread(threadID string) *telemetry.SessionTelemetry {
```

- **五分类**：**均不适用 ⇒ 真缺口（S）**。Go 侧**载体齐备但接线缺失**：`runtimeutil.FileChange{Kind,Path,MovePath}`（= Rust `AppliedPatchFileChange`）、committed-delta 位置 `appserver/turn_runtime.go:4584`、计数 API `appserver/session_telemetry.go:26` + `telemetry.SessionTelemetry.Counter`、计数器名注册表 `telemetry/metric_names.go`；唯独 `codex.agents_md.edit` **0 命中**。
- ⚠️ **落点在避让面**：`appserver/turn_runtime.go` 是你此前明示的避让文件（"若要用请先报备"）。⇒ **不开工，先报备待批**。

## 2. pin 建议

- 事实：区间 4 笔**无一位于 Go `658dc9c1` 的落地点**（`git log --grep` 反查：Go 侧最近入主的 `#49584/#50756/#49714` 均不在这 4 笔内）。
- 建议（二选一，请裁）：
  1. **pin 前进到 `b17c74cfd5`**（= 当前 upstream `origin/main`）——理由：区间 4 笔已全部有处置结论（`#51642` 已 N/A、`#51651` 无载体 N/A、`#51650` L 待裁、`#51652` S 待报备），把它们排除在「新料」之外可避免每轮重复枚举。
  2. **pin 暂留 `5b0b253035`**——理由：口径若为「pin 只覆盖已消化项」，则须等 `#51650`（裁 L/N-A 或落地）与 `#51652`（报备后落地或裁 N/A）处置完再前进。
- 我的倾向：**方案 2**（`#51652` 是 S 级真缺口、有明确 Go 载体，先落地再前进更符合历史 pin 语义）；若你要一次性清空新料，方案 1 亦可。

## 3. 真缺口单独登记（按你要求：只报不开工）

| 项 | 级别 | Go 载体 | 阻塞 |
|---|---|---|---|
| `#51652` AGENTS.md 编辑计数 | **S**（Rust 1 文件 24 行） | `runtimeutil/diff_tracker.go`（`FileChange`）、`appserver/turn_runtime.go:4584`（committed delta）、`appserver/session_telemetry.go:26` + `telemetry.SessionTelemetry.Counter`、`telemetry/metric_names.go` | 落点 `appserver/turn_runtime.go` = **避让面**，需你报备批准 |
| `#51650` resolve 前授权 + 批准后重查 baseline | **L**（Rust 9 文件） | `network/proxy_server.go:1594 evaluateProxyPolicy` / `:1629` 门 / `:1749 proxyHostResolvesToNonPublicIP` / `:1525,1538 dialCheckedTarget`；`sandbox/seatbelt.go`（无 dns/53 命中，形态不同） | 规模 >5；且第 4/5 点需深读确证 |

## 4. 附：上一轮交付的账本在本轮 Go 基线上仍然有效（未重跑，已用交集论证）

`9cdcd43e..658dc9c1`（sync624..sync629）新增改动的 20 个文件，与 `update/r86_patches/` 下**全部 31 个**补丁的 `diff --git` 文件集合求交，命中 7 个补丁：

```
integ86c.patch        -> model/responses_agent.go
syncl3.patch          -> appserver/turn_runtime.go
syncl3_49076.patch    -> appserver/turn_runtime.go
syncl5_50756.patch    -> tui/bottom_pane/command_popup.go, ..., tui/tea/slash_popup.go（= 本轮我自己交付、已被 sync628 收录）
syncl5b.patch         -> model/responses_agent.go（= 我的 #49675，已入主）
syncw1_49076.patch    -> appserver/turn_runtime.go
syncw1_49097.patch    -> appserver/turn_runtime.go
```

关键点：这 7 个在账本里**均已判为「已落地（冗余，禁重放）」或「整包作废」**，或就是本轮已入主的本车道补丁；账本中**唯一「可落地（待批）」的 `syncw4_49147.patch`**（单文件 `chatgptapi/cloud_tasks_normalize_like_rust_test.go`，你已裁定不收录）**不在**本轮新增改动文件集合内 ⇒ 账本的结论列不变。

## 5. 备注

- 本轮**只读**：未改任何 Go/Rust 文件，未生成补丁，未 commit/push/动 ref。
- Rust 仓仅执行了 `git fetch origin main`（remote-tracking 更新）与只读 `show/grep/log`；工作树 HEAD 仍 `5b0b253035`。
