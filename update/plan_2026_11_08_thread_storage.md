# 线程/存储组 6 条逐条判定（`55897425e8..748ab55ca7` delta，附录 A「线程/存储」组）

- 作者：ledger-align（对齐账本 lane）
- 日期：2026-11-08
- 性质：只读分析。**不改任何 Go 代码、不动 `parity/**`、不动 `parity/baseline.json` / 生成脚本 / `rustToInclusive`**（锚点维持 3584）。契约表仅为建议，登记由主干 lane 做。
- Rust 基线：`/home/jacks/jacks_dev/codex`（`codex-rs`，HEAD `748ab55ca7`），六条提交均可 `git show`；下文行号均为**该提交自身版本**的行号。
- Go 基线：本 lane 完整镜像；下文「零命中」为全树 `grep -rn --include='*.go'` 结果。
- 环境：本轮**没有**设置 `CODEX_RUST_ROOT`（只用 `git show` 取改动），避免把源码快照当 git 检出用。
- 实测环境（§7 的性能数字）：Linux x86_64、i7-1270P、`sqlite3`/python `sqlite3` **3.46.1**、Go 1.26.2。所有基准跑在**一次性副本** `/tmp/benchcopy`（`rsync` 出来的树）里，仓库本体未做任何改动，`git status --porcelain` 为空。

## 0. 判定汇总

| # | commit | PR | 主题 | 判定 | 一句话理由 |
| --- | --- | --- | --- | --- | --- |
| 54 | `df769889c0` | #60006 | Reduce SQLite write overhead for thread activity and history | **partial** | 三项子改动（token 窄更新 / 历史写准入 / 日志窗口帧）Go 三缺，但 pool、`ExecWrite` busy 重试、`BeginImmediate`、分区 prune 谓词这些底座都在 |
| 73 | `e428b9a16b` | #59969 | Reduce contention in thread writer lock coordination | **partial** | Go 的协调锁是**独占**且阻塞（30 s 超时、50 ms 重试）；`Close()` 也要先拿独占再删文件，Rust 已改成共享协调 + 拿不到就保留文件 |
| 61 | `eb485f57ba` | #59998 | Add atomic bulk channel subscription updates | **partial** | `set_subscriptions`（一次事务内多目标、非原子失败即整体回滚）完全不存在；Go 只有单条 `SetSubscription` |
| 64 | `632eee03e9` | #59993 | Add fixed read views for local message boards | **partial** | 没有 `LocalBoardReadView` / `post_cutoff` 快照；只有 `ReadOnly()` 实时连接 |
| 2 | `c9b4267c37` | #60190 | Expose agent tree IDs and environment removal | **partial** | environment 移除**已对齐**（`appserver.EnvironmentManager.Remove` + `TestManagerRemove`）；agent tree ID 缺失且语义不同（Go 复用 root thread id） |
| 14 | `43b4274f3f` | #60118 | Buffer payload JSON reads during rollout trace replay | **not_applicable** | Go 没有 trace bundle / per-payload 文件读取这条路径；`recordreplay` 的 JSONL 已用 16 MB `bufio` |

组内：`partial 5`、`not_applicable 1`、`ported 0`、`aligned 0`。

**性能类条目（#60006、#59969）的可量测口径见 §7**：哪条 SQL、benchmark 形状、我本地实测到的倍数都在那里，不接受「看起来可以优化」。

## 1. `df769889c0` #60006 — 降低 thread activity 与 history 的 SQLite 写开销

这条是三件事捆在一个 PR 里，必须分开判。

### 1.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `state/src/runtime/threads.rs:822-844` | 新增 `update_thread_token_usage`：**窄 UPDATE**，只写 `updated_at`/`updated_at_ms`/`tokens_used`；`WHERE id=? AND source=? AND (? OR EXISTS (SELECT 1 FROM thread_spawn_edges WHERE child_thread_id=threads.id))`，`total_tokens.max(0)` 夹取 |
| `thread-store/src/local/update_thread_metadata.rs:379` | 窄路径判据由 `is_empty_except_updated_at()` 放宽到 `is_empty_except_updated_at_and_token_usage()` |
| `…/update_thread_metadata.rs:384-390` | 无 token → `touch_thread_updated_at`；有 token 且 `history_mode` 未提升、`preview` 已存在或 `first_user_message` 为空 → `update_thread_token_usage`；否则 `Ok(false)` 回落全量修复 |
| `…/update_thread_metadata.rs:395-398` | 窄更新返回 `false`（source 变更 / spawn edge 缺失）时**重读当前行**再修复，防"用旧快照算出错误的 spawn parent" |
| `state/src/runtime/logs.rs:110`、`:193`、`:260` | 三处累计字节窗口加 `ROWS UNBOUNDED PRECEDING`，使 `SUM(...) OVER` 与同一 `ORDER BY` 的 `ROW_NUMBER() OVER` 成为**同一窗口定义**，SQLite 可一趟算完 |
| `thread-store/src/local/mod.rs:159`、`:288` | 新增 `thread_history_writer: Arc<Semaphore>`，`Semaphore::new(1)` |
| `thread-store/src/local/thread_history.rs:118-121`、`:262-266` | `apply_projection` 与 `delete_thread` 都**先 `acquire()` 再 `pool.begin_immediate()`**：排队等写锁的调用方不占连接，其余读请求还能用剩下的连接 |

### 1.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **token 一变就走全量 upsert（40 个 SET 列 = 37 个元数据列 + 时间戳/token）**：窄路径（`touchThreadUpdatedAt`）的判据把 `tokensUsed` 也算进「必须已经相同」 | 【F:state/backfill.go†L617-L618】；`rolloutThreadRowMatchesExceptTimestamps` 的 `stored.tokensUsed == intended.tokensUsed` 在 【F:state/backfill.go†L947-L975】 |
| 回落的完整 upsert 就是 `INSERT … ON CONFLICT DO UPDATE` 全套列，`tokens_used = excluded.tokens_used` | 【F:state/backfill.go†L620-L660】（`:655`） |
| 无 source 守卫、无 spawn-edge EXISTS 守卫、无 `max(0)` 夹取 | `grep -rn --include='*.go' "tokens_used = ?" state/` → 0 命中；`grep -rn --include='*.go' "thread_spawn_edges" state/backfill.go` 只在 upsert 参数里出现，不在 WHERE |
| **历史写不排队**：直接拿连接 + `BEGIN IMMEDIATE` | 【F:state/thread_history_projection.go†L168】`conn, err := BeginImmediate(ctx, db)`；删除路径同构 【F:state/thread_lifecycle.go†L148】 |
| 连接**先于**写锁被占用，重试期间一直占着 | 【F:state/sqlite_busy_retry.go†L81-L101】`db.Conn(ctx)` → `BEGIN IMMEDIATE`，失败关连接重来 |
| 池大小 5，history 库同样是 5 | 【F:state/sqlite.go†L33】`SQLiteMaxOpenConnections = 5`；【F:state/sqlite.go†L485-L486】；`OpenThreadHistoryDB`（【F:state/runtime.go†L230-L236】）走同一 opener |
| `threadHistoryMu` **不是**写串行化，只是懒开库 | 【F:state/runtime.go†L96】；唯一两处使用在 【F:state/runtime.go†L254-L259】（`Close`）与 【F:state/runtime.go†L297-L318】（`ThreadHistoryDB` 懒开） |
| **日志窗口没有帧**：`SUM(estimated_bytes) OVER (ORDER BY …)` 用默认 `RANGE`，与 `ROW_NUMBER()` 的 spec 不同 | 【F:state/logs.go†L199-L212】；`grep -rn "UNBOUNDED" state/` → **0 命中** |

### 1.3 判定：`partial`

- 子项 ①（token 窄更新）：**缺**，且 Go 的回落代价比 Rust 更高（40 列 + 无条件覆盖，见 §1.2 第 1–3 行）。
- 子项 ②（历史写准入）：**缺**，写入方在等写锁时占满 5 个连接中的若干个，读请求会被挤（§7.5 有量化）。
- 子项 ③（日志窗口帧）：**缺**，是可量测的计划层差异（§7.1 实测 1.83×）。
- 底座具备：连接池参数、busy 重试、`BeginImmediate`、分区 prune 谓词、`allocate_thread_timestamp` 高压水位都在，所以是 partial 而不是 missing。

### 1.4 落地切入点（若要现在改，最小面）

1. `state/backfill.go:617`：把 `tokensUsed` 从 `rolloutThreadRowMatchesExceptTimestamps` 移出，新增窄分支 → 新函数 `StateRuntime.UpdateThreadTokenUsage`（建议落在 `state/thread_query.go` 旁边），SQL 照抄 Rust：三列 + `source` 守卫 + `thread_spawn_edges` EXISTS 守卫 + `max(0)`；返回 `(updated bool, err)`，`false` 时重读行再修复。
2. `state/runtime.go`：加 `historyWriter chan struct{}`（cap 1，`NewStateRuntime` 里 make），在 `applyThreadHistoryProjection`（【F:state/thread_history_projection.go†L168】之前）与 `deleteThreadHistoryRows`（【F:state/thread_lifecycle.go†L148】之前）取/放。
3. `state/logs.go:202-207`：`ROWS UNBOUNDED PRECEDING` 一行。

三项互不耦合，可以分三个 PR；但 ①③ 的 verifier 都需要先落测试（见 §8）。

## 2. `e428b9a16b` #59969 — 降低 thread writer 锁协调的争用

### 2.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `rollout/src/writer_lock.rs:46-61` | `acquire`：先 `open_coordination()`（只开文件不锁），再用 `try_lock()` 试**独占**；只有独占立刻拿到才做目录级 stale 清理；否则 `wait_for_coordination(‖ file.try_lock_shared())` —— **共享**协调，多个写者可同时持有 |
| `…/writer_lock.rs:78-90` | 线程锁文件本身仍是 `try_lock()` 独占（同一 thread 仍然互斥），`WouldBlock` → `ErrConflict` |
| `…/writer_lock.rs:208-222` | `Drop`：`open_coordination()` 后 `try_lock()`；`WouldBlock` → **直接 return**（只释放所有权、保留文件给下一个写者），不再排队等 30 s 才删文件 |
| `…/writer_lock.rs:132-136` | `lock_coordination()`（独占 + 有界等待）保留，publication 路径仍走独占，语义不变 |

### 2.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| `acquire` 用**独占**协调锁包住整个"开锁文件 + TryLock"过程，拿不到就 50 ms 重试、30 s 超时 | 【F:session/writer_lock.go†L106-L125】；常量 【F:session/writer_lock.go†L15-L22】 |
| publication 保持独占（与 Rust 一致） | 【F:session/writer_lock.go†L127-L155】`tryAcquireForPublication` |
| `Close()` 也要先拿独占协调才 `os.Remove`，拿不到就**一直等**（30 s 后报超时，文件留着） | 【F:session/writer_lock.go†L175-L197】 |
| 无共享协调、无"拿不到就不删"的快速返回 | `grep -rn --include='*.go' "try_lock_shared\|RLock\|TryRLock" session/` → 0 命中 |

### 2.3 判定：`partial`

承载面全在（per-thread flock、协调锁文件、publication 独占、部分获取回滚 `TestAcquireWritersReleasesPartialAcquisitionOnConflict`），缺的是**协调锁的共享读语义**与 **Drop 的非阻塞删除策略**。

### 2.4 实测（细节见 §7.3，这个是本条真正的代价）

- **`Close()` 阻塞**：另一持有者占住协调锁 300 ms 时，Go 的 `WriterLock.Close()` 实测耗时 **304 ms**（跟着对方释放才返回）；Rust 新版 Drop 在同场景下是 `try_lock` → `WouldBlock` → 立即返回、文件留着。这条不是"看起来可以优化"，是**每次关闭都被别人拖住**。
- **吞吐**：我按 Rust 算法写了一份共享协调对照实现，在 2/4/8/16 并发下对比，比值 **1.01× / 1.00× / 0.89× / 1.00×** —— 也就是说在「协调临界区只有几微秒」的现实负载下，共享协调**测不出吞吐收益**。所以本条的正当理由要写成「**关闭路径的非阻塞语义 + 跨进程 50 ms 重试粒度下的等待放大**」，不能写成"提高并发吞吐"。

### 2.5 落地切入点

`session/writer_lock.go`：抽 `openCoordination()`（只 `MkdirAll` + `flock.New`）；`acquire` 改为 `TryLock` 独占 → 成功才 stale 清理 → 失败转 `tryRLock`（有界等待）；`Close()` 改为 `TryLock` 独占，拿不到就只 `Unlock` 线程锁并 **跳过 `os.Remove`**（返回 nil，不报错）。测试补「协调被占时 Close < 100 ms 且文件保留」。

## 3. `eb485f57ba` #59998 — 原子批量频道订阅

### 3.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `message-board/src/local/subscriptions.rs:22-60` | 新增 `set_subscriptions(caller, &BTreeMap<&str, SubscriptionChange>)`：先校验调用者 agent path 与**全部** channel 名，再起一个写事务 |
| `…/subscriptions.rs:37-49` | 一条 SQL 取回全部 channel 的当前订阅态；`membership.len() != channels.len()` → `invalid("channel not found in this board")` —— **整体失败，一条都不写** |
| `…/subscriptions.rs:52-58` | 只对**有 diff** 的目标调 `change_subscription`；未提及的频道不动 |
| `…/subscriptions.rs:60` | 单次 `tx.commit()` |
| `…/subscriptions.rs:64-95` | `change_subscription`：Subscribe → `DELETE subscription_opt_outs` + `INSERT OR IGNORE subscriptions`；Unsubscribe 反之（显式退订与隐式订阅的 opt-out 成对维护） |
| `…/subscriptions.rs:26-30` | 只读的 **live** 连接允许这个 self-only 操作；**fixed view 拒绝**；不发通知 |

### 3.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| `Board` 接口只有单条 `SetSubscription` | 【F:agentboard/api.go†L51-L54】 |
| 实现按单个 target 起事务，逐条写 opt-out / subscription | 【F:agentboard/local.go†L661-L749】（语句对在 `:733-L746`） |
| **无批量 API** | `grep -rn --include='*.go' -E "SetSubscriptions|SubscriptionBatch|Bulk" agentboard/` → **0 命中** |
| `SubscriptionChange` 常量已在（subscribe / unsubscribe） | 【F:agentboard/api.go†L79-L84】 |
| 只读 live 连接语义已在 | 【F:agentboard/connection.go†L39-L42】`ReadOnly()`；`:125-134` `requireWrite` |

### 3.3 判定：`partial`

缺的是**原子性本身**：调用方要改 5 个频道只能发 5 次单条请求，第 3 个失败时前 2 个已提交，且每次都要重开事务；Rust 是「全成或全不成」。

### 3.4 落地切入点

`agentboard/api.go` 加 `SetSubscriptions(ctx, caller string, changes map[SubscriptionTarget]SubscriptionChange) error`（`SubscriptionTarget` 需可比较：现在是含 `*string` 的结构，建议用 `TargetKey()` 字符串化后的 `map[string]SubscriptionChange`）；`agentboard/local.go` 把 `:733-L746` 的两条语句抽成 `changeSubscription(ctx, tx, target, agent, change)`；批量版按 Rust 只返回 error、不返回 `SubscriptionState`、不发通知。`inmemory.go` / `remote.go` 同步实现（remote 无批量能力时按现状返回「不支持」而不是静默降级）。

## 4. `632eee03e9` #59993 — 本地消息板的固定读视图

### 4.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `message-board/src/local/catalog.rs:31-51` | `LocalBoardReadView { board, view: Arc<ReadView> }`；`ReadView { post_cutoff: i64, channels: Vec<ChannelSummary> }` |
| `…/catalog.rs:81-84` | 快照取 `SELECT COALESCE(MAX(seq),0) FROM posts` —— `seq` 是库级 AUTOINCREMENT，之后任何 append 都在这条线之上 |
| `…/catalog.rs:71-104` | 同一事务内取完整 channel 目录（含 last message / 计数），**立刻 `commit`**：只冻结元数据与边界，**不持有事务、不保留已删除证据** |
| `local.rs`（`BoardMode::{Live{access}, Fixed{view}}`）、`…/connection.rs:…` | `FIXED_ACCESS = ReadOnly`：fixed view 上**所有 mutation（含订阅）被拒** |
| `local/queries.rs`（+167/−…） | search / thread listing / reply summary / 单条读都带 `seq <= post_cutoff` 过滤，**backdated append 也被排除** |

### 4.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| 只有「实时只读连接」，没有「快照视图」 | 【F:agentboard/connection.go†L117-L134】`accessLegacy / accessReadOnly`；【F:agentboard/connection.go†L39-L42】`ReadOnly()` |
| **无 read view 概念** | `grep -rn --include='*.go' -E "ReadView\|readView\|FixedView\|postCutoff\|post_cutoff" agentboard/` → **0 命中** |
| `Board` 接口无捕获入口 | 【F:agentboard/api.go†L18-L55】 |
| 查询侧无边界参数（`ListChannels/ListThreads/SearchPosts/ReadThread/ReadPost` 均无 cutoff） | 同上接口定义 |

### 4.3 判定：`partial`

`ReadOnly()` 覆盖了「不能写」这一半，但覆盖不了「读到一个稳定边界」：Go 的只读连接仍会看到之后追加的 post，也会看到并发删除。Rust 的 fixed view 还额外提供「捕获时刻的完整 channel 目录」（含 message_count），这是 Go 完全没有的返回值形状。

### 4.4 落地切入点

新增 `agentboard/read_view.go`：`func (b *LocalAgentMessageBoard) CaptureReadView(ctx) (*LocalBoardReadView, error)`，事务内取 `MAX(seq)` + channel 目录后立即提交；`LocalBoardReadView` 持 `channels []ChannelSummary` 与 `postCutoff int64`，并把 `accessMode` 扩一个 `accessFixed`（`boardPermissions` 全 false）；查询函数加 `postCutoff *int64` 参数（nil = live）。

## 5. `c9b4267c37` #60190 — 暴露 agent tree ID 与 environment 移除

这条两半，判定不同，必须分开写。

### 5.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `core/src/agent/control/runtime.rs:243-249` | `LocalAgentRuntime::tree_id() -> Uuid`：从 shutdown report 里读 tree id（内部访问器） |
| `core/src/codex_thread.rs:250-255` | `CodexThread::agent_tree_id() -> Uuid`：**对外**暴露"这个已加载 agent 树的身份"，root 与 native children 共享；**重载新树换新 ID，即使复用同一个持久化 thread ID** |
| `exec-server/src/environment.rs:423-431` | `EnvironmentManager::remove_environment(&self, id) -> Option<Arc<Environment>>`：从注册表移除、**不使已有 handle 失效**、**不改 default ID**；返回的 `Arc` 在最后一个 handle drop 前存活，且移除在注册表锁外 |
| `…/environment.rs:1698-1717` | 测试锁四件事：移除返回同一 `Arc`、再查为 `None`、再移除为 `None`、重新 upsert 得到**新** `Arc` |

### 5.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **environment 移除已对齐** | 【F:appserver/environment.go†L1336-L1345】`func (m *EnvironmentManager) Remove(environmentID string) bool`：不存在返回 false，存在则 `cancelConnectionAttempts` + `delete(m.records, …)` |
| 有测试覆盖 | `appserver/environment_test.go:186` `TestManagerRemove`（`:191` 断言首次 true、`:194` 断言再次 false） |
| 语义差异（小） | 返回 `bool` 而不是被移除的对象；额外做了 `cancelConnectionAttempts`（Rust 侧由调用方协调）；Go 的 default 来自 provider 快照（【F:appserver/environment.go†L1318-L1334】），移除记录不会改它 —— 与 Rust「不改 default ID」一致 |
| **agent tree ID 缺失** | `grep -rn --include='*.go' -E "AgentTreeID\|agent_tree_id\|AgentTreeId"` → **0 命中** |
| 现有的 `TreeID` 语义**不同**：是运行时相关性 ID，注释明说不是 thread ID | 【F:agent/shutdown_report.go†L62-L66】 |
| 且它由调用方用 **root thread id** 填进去，所以"重载换新 ID"这条性质不成立 | 【F:appserver/agent_controller.go†L754】`agent.NewAgentTreeShutdownState(firstNonEmpty(c.rootID, target))` |

### 5.3 判定：`partial`

- environment 移除：**aligned**（实现 + 测试都在，差异只是返回值形状与多一次 `cancelConnectionAttempts`，不构成行为缺口）。
- agent tree ID：**missing**，而且不是"少个 getter"——Go 现在根本没有"每次加载一棵树生成一个 UUID、root 与 native children 共享、复用 thread id 也要换新"这个对象。appserver 也没有任何 surface 暴露它（`grep -rn '"tree_id"'` 只有 shutdown report 的日志字段）。

### 5.4 落地切入点

1. 在 Go 的 agent 运行时（`agent/` 或 `session/` 的树状态持有者）引入 `treeID uuid.UUID`，在**加载**时生成（不是继承 root thread id）；`AgentTreeShutdownState` 改为持 UUID 并保留现有 `TreeID string` 展示形式（`String()` 输出不变，避免破坏 `TestAgentTreeShutdownReportFormattingIsPayloadFreeLikeRust`）。
2. 对外访问器放在 thread 句柄层（等价 `CodexThread::agent_tree_id`），appserver 的 thread 响应加字段（若协议侧要暴露）。
3. 测试补：同一 thread id 重新加载两次 → 两个不同 tree id；root 与 native child → 同一 tree id。

## 6. `43b4274f3f` #60118 — rollout trace replay 时给 payload JSON 读取加缓冲

### 6.1 Rust 侧改动点

| 文件:行（提交后） | 在解决什么 |
| --- | --- |
| `rollout-trace/src/reducer/mod.rs:268` | `serde_json::from_reader(file)` → `serde_json::from_reader(BufReader::new(file))`：trace bundle 的每个 payload 都是独立小文件，未缓冲时每次 `read` 只有几十字节 syscall |

### 6.2 Go 侧锚点

| 事实 | 证据 |
| --- | --- |
| **Go 没有 trace bundle 这条路径** | `grep -rn --include='*.go' -E "bundle_dir\|payload_path\|raw_payload\|BundleDir"` → **0 命中** |
| Go 的录制/回放是 JSONL，已带 16 MB 缓冲 | 【F:recordreplay/recordreplay.go†L96-L110】`bufio.NewReaderSize(file, 16<<20)`，逐行 `json.Unmarshal`（【F:recordreplay/recordreplay.go†L124-L146】） |
| 没有"每条 payload 一个文件"的解码点 | 同名 grep 0 命中；`Serde_json::from_reader` 的等价用法（`json.NewDecoder(file)` 直接接 `*os.File`）在树里也没有 trace 相关调用点 |

### 6.3 判定：`not_applicable`

不是"Go 写得更好"，而是 Go 在 trace replay 上**没有与 Rust 同构的结构**（Rust 是 bundle 目录 + 每个 payload 一个小 JSON 文件，Go 是单文件 JSONL）。缺陷面不存在，因此判 n/a；如果将来对齐 Rust 的 bundle 形状，这条的缓冲要求要一起带上（否则会精确复现 Rust 修掉的 syscall 放大）。

## 7. 性能类条目的可量测口径（#60006 / #59969）

要求：每条给 **①哪条 SQL ②量什么指标 ③benchmark 形状 ④预期放大倍数 ⑤我实测到的数**。下面 7.1–7.4 是我在本机跑出来的真实数字（脚本见 §9，可直接复现），7.5 是建议进仓库的 `go test -bench` 形状。

### 7.1 #60006(c) 日志窗口帧：`ROWS UNBOUNDED PRECEDING` —— 实测 1.83×

- **哪条 SQL**：【F:state/logs.go†L199-L212】（`pruneLogPartition` 的删除子查询）。
- **指标**：`EXPLAIN QUERY PLAN` 里窗口子查询的 co-routine 数（能否合并成一趟）+ 同一子查询的墙钟时间。
- **形状**：`logs` 表 200k 行、全部属于同一 `thread_id`，索引 `(thread_id, ts, ts_nanos, id)`；对「累计字节 + 行号」子查询跑 `SELECT count(*)`。
- **计划差异（实测，python sqlite3 3.46.1）**：

```
默认 RANGE 帧：  CO-ROUTINE ×3 + 1 SEARCH logs USING INDEX logs_thread_ts(thread_id=?) + 3 SCAN
ROWS UNBOUNDED： CO-ROUTINE ×2 + 1 SEARCH logs USING INDEX logs_thread_ts(thread_id=?) + 2 SCAN
                 （cumulative_bytes 与 row_number 的窗口 spec 相同后合并成一趟）
```

- **时间**（5 次，200k 行，上限 `rows=150000`）：

```
默认 RANGE 帧： 252.2 / 261.6 / 261.5 / 263.3 / 261.5 ms
ROWS UNBOUNDED：143.1 / 143.3 / 143.2 / 143.1 / 143.5 ms      → 1.83×（261.5 → 143.2 ms）
```

- **为什么是这个倍数**：少一趟窗口子查询的物化与扫描（两列由同一趟 co-routine 算出），省掉一份中间结果。倍数与分区内行数正相关而与表宽无关；单分区行数 < 几千时可忽略。
- **注意（避免过度声明）**：`ORDER BY ts, ts_nanos, id` 里 `id` 唯一，所以 `RANGE` 与 `ROWS` **结果集完全相同**，差别纯粹是「计划能否合并两趟」。不要把它写成"修正了计数错误"。

### 7.2 #60006(a) token 窄更新：40 列 upsert → 3 列 UPDATE —— 实测 3.3×

- **哪条 SQL**：Go 侧全量分支 【F:state/backfill.go†L620-L660】（`INSERT … ON CONFLICT(id) DO UPDATE SET`，40 个 SET 列 = 37 元数据 + `updated_at`/`updated_at_ms`/`tokens_used`）；Rust 目标形状 `state/src/runtime/threads.rs:822-844`。
- **指标**：每次 token 观测的墙钟 + WAL 帧数（`page_size` 4096）。
- **形状**：用**真实迁移 schema**（`state/migrations/state/*.sql` 62 个文件全量执行 → `threads` **43 列 / 23 个索引**），单连接、WAL、`wal_autocheckpoint=0`，同一行连续 2000 次观测（`updated_at/updated_at_ms/tokens_used` 递增，其余列保持原值）。
- **实测**：

```
narrow 3 列 :  6.6 ms / 2000 = 3 µs/obs,  WAL 5 帧  (~0.02 MiB)
full  40 列: 20.0 ms / 2000 = 10 µs/obs, WAL 14 帧  (~0.05 MiB)   → 3.3× 时间, 2.8× 帧数
```

- **预期倍数**：单线程热行 3.3×；**并发下差距会更大**，因为每次全量 upsert 持有 SQLite 写锁的时间是 3 列的 3 倍多，写锁排队是串行的。
- **口径提醒**：这是 python `sqlite3` + 单连接的数字，只用于确认量级与方向；登记契约前应在仓库里用当天驱动（modernc）跑 7.5 的 `BenchmarkThreadTokenObservation` 复核。

### 7.3 #59969 writer lock：`Close()` 阻塞 + 协调锁共享性

- **哪条 SQL/代码**：无 SQL；【F:session/writer_lock.go†L175-L197】`Close()`；【F:session/writer_lock.go†L106-L125】`acquire()`。
- **指标 1（阻塞时长）**：另一持有者占住 `.coordination.lock` 一段时间，量 `Close()` 的返回耗时。
- **实测 1**：对方持有 **300 ms** → Go `Close()` 耗时 **304 ms**（Rust 新版 Drop：`try_lock` → `WouldBlock` → 立即返回、保留文件）。

```
TestCloseWaitsForCoordination: Close() took 304ms while coordination was held for 300ms
```

- **指标 2（并发吞吐）**：N 个 goroutine、各自独立 coordinator（独立 fd，等价跨进程竞争）对**不同** thread 反复 acquire+Close，比较「独占协调」与「共享协调（Rust 算法）」的总耗时。
- **实测 2**：

```
workers= 2  ops=400   exclusive= 55.05ms  shared= 54.43ms  ratio=1.01x
workers= 4  ops=800   exclusive=156.96ms  shared=157.69ms  ratio=1.00x
workers= 8  ops=1600  exclusive=315.73ms  shared=355.29ms  ratio=0.89x
workers=16  ops=3200  exclusive=758.99ms  shared=759.56ms  ratio=1.00x
```

- **结论（重要，防过判）**：**测不出共享协调带来的吞吐收益**。临界区只有微秒级，抢不到的概率低。所以本条的正当理由是「**关闭路径不再被别人的协调锁拖住**」+「跨进程冲突时不再吃 50 ms 重试粒度」，**不是**吞吐。若要声称吞吐收益，需要构造"另一进程长期持有协调锁 + 大量 acquisition"的场景，而那恰好就是指标 1 的场景。

### 7.4 #60006(b) 历史写准入：连接先拿 vs 许可先拿

- **哪条 SQL/代码**：【F:state/sqlite_busy_retry.go†L81-L101】（conn 先拿）+ 【F:state/thread_history_projection.go†L168】。
- **指标**：并发写者存在时，**读请求**的 p50/p99/max 与 >100 ms 的比例。
- **形状**：池 5 连接、WAL、写者 8/16 个各做 100 次 `BEGIN IMMEDIATE → INSERT → COMMIT`（`busy_timeout=0`，失败即关连接重来，等价 Go 的 busy 重试），读者 4 个持续 `SELECT COUNT(*)`。
- **实测**：

```
writers=8  readers=4: conn-first p50=45µs p99=331µs max=1.391ms blocked>100ms=0
                      permit-first p50=14µs p99=224µs max=1.239ms blocked>100ms=0
writers=16 readers=4: conn-first p50=80µs p99=581µs max=1.267ms blocked>100ms=0
                      permit-first p50=11µs p99=301µs max=24.401ms blocked>100ms=0
```

- **结论**：读 p50 从 **45 → 14 µs（3.2×）**、**80 → 11 µs（7.3×）**，p99 从 331→224、581→301 µs。**但没有复现出读饥饿**（读者太轻 + WAL 允许并发读），所以这条的收益应描述为「读延迟分布改善」，不是"读被饿死"。要证明饥饿需要真实 history 写事务（多表 `INSERT` + 大 JSON payload，单事务毫秒级）作为写侧负载 —— 见 7.5 的 `BenchmarkHistoryWriteAdmission`。
- 注意 `writers=16` 时 permit-first 的 `max=24.4 ms` 是许可串行化后的一次调度毛刺，样本少，不作为结论。

### 7.5 建议进仓库的 benchmark（`go test -bench`，名字可直接用作 verifier）

| 契约 | benchmark / test 名 | 形状与断言 |
| --- | --- | --- |
| `thread-token-narrow-update` | `BenchmarkThreadTokenObservation`（`state`） | 建真库（迁移全量）→ 单 thread 连续 1e4 次 token 观测；报告 `ns/op` 与 `PRAGMA wal_checkpoint(PASSIVE)` 帧数。断言：窄路径 `ns/op` ≤ 全量路径的 1/2，且 WAL 帧数 ≤ 全量路径的 1/2（本地实测 3.3× / 2.8×） |
| `logs-prune-window-frame` | `TestLogsPruneWindowUsesSinglePassLikeRust`（`state`） | `EXPLAIN QUERY PLAN` 抓 `DELETE` 子查询文本，断言窗口子查询的 `CO-ROUTINE` 数 ≤ 2（加帧前是 3）；再用 `BenchmarkLogsPrunePartition`（20 万行单分区）断言 prune ≤ 全量的 0.6（实测 143/261 = 0.55） |
| `thread-history-writer-admission` | `BenchmarkHistoryWriteAdmission`（`state`） | 8 个写者跑真实 `MaterializeThreadHistory`（大 rollout 片段），4 个读者；断言读者 p99 ≤ 写者数为 1 时的 2×；用 5 连接池，写者数 ≥ 5 才有意义 |
| `thread-writer-lock-shared-coordination` | `TestWriterLockCloseDoesNotBlockOnHeldCoordination`（`session`） | 另开 fd 持有 `.coordination.lock` 300 ms，断言 `Close()` < 100 ms 且锁文件仍存在；再断言释放后能重新 acquire（所有权转移） |
| `message-board-bulk-subscriptions` | `TestLocalBoardBulkSubscriptionsAreAtomicLikeRust`（`agentboard`） | 5 个频道里第 3 个不存在 → 整体失败且前 2 个**未**写入；全存在 → 单事务提交，未提及频道不受影响；只读 live 允许 self-only、fixed view 拒绝 |
| `message-board-fixed-read-view` | `TestLocalBoardFixedReadViewFreezesCatalogAndCutoffLikeRust`（`agentboard`） | 捕获后 append（含 backdated）→ view 的 `ListThreads/SearchPosts/ReadThread` 都看不到；删除的 post 不复活；任何 mutation 返回权限错误；`channels()` 等于捕获时刻目录 |
| `agent-tree-id` | `TestAgentTreeIDChangesOnReloadAndIsSharedByChildrenLikeRust`（`agent`/`session`） | 同一 thread id 重载两次 → 两个不同 tree id；root 与其 native child → 同一 tree id |

> 登记约束提醒：`parity/verifier_existence_test.go` 的 `TestContractVerifiersExistInGoTestFiles` 要求 verifier 名字**现在**就在 Go 树里有同名 `func TestXxx`。上表这些除 `TestManagerRemove` / `TestRolloutRebuildTimestampOnlyTouchesTimestampsLikeRust` / `TestLogsPruneEachRustPartitionByRowAndByteLimit` / `TestLocalBoardSubscriptionsOptOutsAndDeletionLikeRust` / 两个 writer-lock 用例之外都是**新名字**，只能放「需先落测试」列。

## 8. 契约登记表（7 条建议；`#60118` 判 n/a 不占位）

| 来源 | contract id | kind | rustPaths | goPaths | phase | status | 可立即登记的 verifier（现存） | 需先落的 verifier + 要点 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `df769889c0` #60006(a) | `thread-token-narrow-update` | `source-surface` | `state/src/runtime/threads.rs`、`thread-store/src/local/update_thread_metadata.rs` | `state` | `P1` | `partial` | `TestRolloutRebuildTimestampOnlyTouchesTimestampsLikeRust` | `BenchmarkThreadTokenObservation` + `TestThreadTokenUpdateKeepsConcurrentMetadataLikeRust`（source 变更/无 spawn edge 时回落修复；负值夹取） |
| `df769889c0` #60006(b) | `thread-history-writer-admission` | `source-surface` | `thread-store/src/local/mod.rs`、`thread-store/src/local/thread_history.rs` | `state` | `P1` | `partial` | —（无同名测试） | `BenchmarkHistoryWriteAdmission`：写者 ≥ 池大小（5）时读者 p99 不明显劣化 |
| `df769889c0` #60006(c) | `logs-prune-window-frame` | `source-surface` | `state/src/runtime/logs.rs` | `state` | `P1` | `partial` | `TestLogsPruneEachRustPartitionByRowAndByteLimit` | `TestLogsPruneWindowUsesSinglePassLikeRust`（计划断言）+ `BenchmarkLogsPrunePartition` |
| `e428b9a16b` #59969 | `thread-writer-lock-shared-coordination` | `source-surface` | `rollout/src/writer_lock.rs` | `session` | `P1` | `partial` | `TestWriterLockRejectsCompetingStoreAndTransfersOwnership;TestTryAcquireForPublicationSkipsLiveWriterAndHoldsCoordination` | `TestWriterLockCloseDoesNotBlockOnHeldCoordination`（协调被占 300 ms 时 Close < 100 ms 且文件保留） |
| `eb485f57ba` #59998 | `message-board-bulk-subscriptions` | `source-surface` | `message-board/src/local/subscriptions.rs` | `agentboard` | `P1` | `partial` | `TestLocalBoardSubscriptionsOptOutsAndDeletionLikeRust` | `TestLocalBoardBulkSubscriptionsAreAtomicLikeRust`（多目标单事务、未知频道整体失败） |
| `632eee03e9` #59993 | `message-board-fixed-read-view` | `source-surface` | `message-board/src/local/catalog.rs`、`message-board/src/local/queries.rs` | `agentboard` | `P1` | `partial` | `TestLocalBoardPersistsPagesAndReadsLikeRust;TestLocalBoardAppendPersistsEvidenceWithoutSideEffectsLikeRust` | `TestLocalBoardFixedReadViewFreezesCatalogAndCutoffLikeRust` |
| `c9b4267c37` #60190 | `agent-tree-id` | `source-surface` | `core/src/codex_thread.rs`、`core/src/agent/control/runtime.rs`、`exec-server/src/environment.rs` | `agent`、`appserver` | `P1` | `partial` | `TestManagerRemove;TestAgentTreeShutdownReportFormattingIsPayloadFreeLikeRust` | `TestAgentTreeIDChangesOnReloadAndIsSharedByChildrenLikeRust` |

> 说明：`#60190` 我把它拆成一条契约（`agent-tree-id`），因为 **environment 移除这一半已经 aligned**，用 `TestManagerRemove` 就能挂上；把两半塞进一条会让"已对齐的部分"被"缺失的部分"掩盖。

## 9. 复现命令

**Go 侧零命中 / 定位证据**（在本 lane 镜像根目录执行）：

```
$ grep -rn "UNBOUNDED" state/                                                → 0 命中
$ grep -rn --include='*.go' -E "SetSubscriptions|SubscriptionBatch|Bulk" agentboard/   → 0 命中
$ grep -rn --include='*.go' -E "ReadView|readView|FixedView|postCutoff" agentboard/    → 0 命中
$ grep -rn --include='*.go' -E "AgentTreeID|agent_tree_id|AgentTreeId" .               → 0 命中
$ grep -rn --include='*.go' -E "bundle_dir|payload_path|raw_payload|BundleDir" .        → 0 命中
$ grep -rn --include='*.go' "try_lock_shared|TryRLock" session/                        → 0 命中
$ grep -rn "threadHistoryMu" state/*.go      → 仅 runtime.go:96/254/259/297/298（懒开库，不串行化写）
```

**Rust 侧**（`git show` 取提交自身版本，行号以此为准）：

```
$ cd /home/jacks/jacks_dev/codex
$ git show df769889c0:codex-rs/state/src/runtime/threads.rs | grep -n "fn update_thread_token_usage"     → 822
$ git show df769889c0:codex-rs/state/src/runtime/logs.rs    | grep -n "ROWS UNBOUNDED PRECEDING"          → 110 / 193 / 260
$ git show df769889c0:codex-rs/thread-store/src/local/mod.rs | grep -n "thread_history_writer"            → 159 / 288
$ git show df769889c0:codex-rs/thread-store/src/local/thread_history.rs | grep -n "acquire()"             → 118 / 262
$ git show e428b9a16b:codex-rs/rollout/src/writer_lock.rs   | grep -n "try_lock_shared\|impl Drop"        → 60 / 208
$ git show eb485f57ba:codex-rs/message-board/src/local/subscriptions.rs | grep -n "pub async fn set_subscriptions" → 22
$ git show 632eee03e9:codex-rs/message-board/src/local/catalog.rs        | grep -n "LocalBoardReadView\|post_cutoff" → 31 / 49
$ git show c9b4267c37:codex-rs/exec-server/src/environment.rs           | grep -n "fn remove_environment"    → 423
$ git show 43b4274f3f:codex-rs/rollout-trace/src/reducer/mod.rs         | grep -n "BufReader::new"           → 268
```

**§7 的四个实测**（一次性副本，仓库未被修改；需要 `python3`、`sqlite3`、`go`）：

```bash
# 7.1 日志窗口帧（200k 行单分区）
python3 - <<'PY'
import sqlite3, os
p="/tmp/frame.db"
if os.path.exists(p): os.unlink(p)
db=sqlite3.connect(p)
db.executescript("""CREATE TABLE logs(id INTEGER PRIMARY KEY, thread_id TEXT, ts INTEGER, ts_nanos INTEGER, estimated_bytes INTEGER);
CREATE INDEX logs_thread_ts ON logs(thread_id, ts, ts_nanos, id);""")
db.executemany("INSERT INTO logs VALUES (?,?,?,?,?)",
  [(i,"t1",1700000000+i//1000,(i%1000)*1000,200) for i in range(200000)])
db.commit(); db.execute("ANALYZE"); db.commit()
for frame in ("", "ROWS UNBOUNDED PRECEDING"):
    q=f"""SELECT count(*) FROM (SELECT id,
      SUM(estimated_bytes) OVER (ORDER BY ts DESC, ts_nanos DESC, id DESC {frame}) c,
      ROW_NUMBER() OVER (ORDER BY ts DESC, ts_nanos DESC, id DESC) rn
      FROM logs WHERE thread_id=?) WHERE c>? OR rn>?"""
    print("PLAN", frame or "RANGE")
    for r in db.execute("EXPLAIN QUERY PLAN "+q,("t1",10_000_000,150_000)): print("  ",r)
PY

# 7.2 token 窄更新 vs 全量 upsert：真实迁移 schema（43 列 / 23 索引）
#     见 /tmp/benchcopy 复现脚本；本 lane 实测 3 µs/obs vs 10 µs/obs（3.3×），WAL 5 帧 vs 14 帧

# 7.3 writer lock（Go，一次性副本里加测试，仓库不动）
rsync -a --exclude artifacts --exclude attachments <repo>/ /tmp/benchcopy/
cd /tmp/benchcopy && go test ./session -run 'TestCloseWaitsForCoordination' -v   # Close() 304ms / 协调被占 300ms

# 7.4 连接先拿 vs 许可先拿（池 5 连接、8/16 写者、4 读者）
cd /tmp/benchcopy && ZPOOL=1 go test ./internal/zzpool -run TestWriterAdmissionOrder -v
```

## 10. 结论

1. **6 条：partial 5 + n/a 1。** 没有一条是"完全没承载面"——Go 的 state/session/agentboard 底座都在（连接池与 busy 重试、per-thread flock + 协调锁、message board 的 opt-out 表与只读连接、EnvironmentManager 注册表），缺的都集中在**"并发/一致性语义"这一层**：窄更新、写准入、共享协调、批量原子性、快照视图、树身份。
2. **性能条目必须带数字说话**：日志窗口帧 **1.83×**（co-routine 3→2、少一趟窗口物化）、token 窄更新 **3.3×**（真 schema：43 列 / 23 索引，40 列 vs 3 列）、writer-lock `Close()` **被别人的协调锁拖满 300 ms**、历史写准入让读 p50 **45→14 µs / 80→11 µs**。
3. **同时也要防过判**：writer-lock 的共享协调在现实负载下**测不出吞吐收益**（0.89–1.01×），历史写准入也**没复现出读饥饿**。这两条要以"关闭路径非阻塞"和"读延迟分布"立项，不要写成吞吐结论。
4. **#60190 是 half-aligned**：environment 移除已对齐（`Remove` + `TestManagerRemove`），agent tree ID 才是真缺口，且 Go 现在的 `TreeID` 复用 root thread id，语义不同。拆成一条契约比笼统判 partial 更有用。
5. **#60118 判 n/a**：Go 没有 trace bundle 结构，`recordreplay` 已是 16 MB `bufio` + 逐行解码；若将来对齐 bundle 形状，这条要一起带上。
6. 本文件不改 Go 代码、不动 `parity/**`，锚点维持 **3584 / `55897425e8…`**，`#60006`/`#59969`/`#59998`/`#59993`/`#60190` 的判定按惯例进 §12 post-anchor 登记。
