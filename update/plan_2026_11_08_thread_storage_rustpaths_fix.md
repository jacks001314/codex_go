# 线程/存储 2 条被拦契约的 rustPaths 修正（对齐 pin `55897425e8`）

- 作者：ledger-align（对齐账本 lane）· 日期：2026-11-08
- 性质：只补路径，**不改判定、不改 verifier、不改 Go 侧 goPaths**。只读分析，未改任何 Go 代码、未动 `parity/**`。
- 触发：`TestAlignmentRustContractOraclePathsExist` 要求 `rustPaths` 在 pin 树 `55897425e8` 存在；我上一版把**delta 期间新建**的文件当成了 pin 文件。

## 0. 结论先说

| contract id | 旧 rustPaths（pin 树**不存在**） | 新 rustPaths（pin 树**存在**，可直接登记） | 新增语义的引入提交（契约 note 用） |
| --- | --- | --- | --- |
| `message-board-bulk-subscriptions` | `message-board/src/local/subscriptions.rs` | `message-board/src/local.rs`、`message-board/src/local/queries.rs` | `eb485f57ba` (#59998) 引入多目标一次性提交 |
| `message-board-fixed-read-view` | `message-board/src/local/catalog.rs` | `message-board/src/local/queries.rs`、`message-board/src/local.rs`、`message-board/src/local/connection.rs`（可选加 `message-board/src/local/paging.rs`） | `632eee03e9` (#59993) 引入 `LocalBoardReadView` + `post_cutoff` |

verifier 名单**原样不动**：
- `message-board-bulk-subscriptions` → `TestLocalBoardSubscriptionsOptOutsAndDeletionLikeRust`
- `message-board-fixed-read-view` → `TestLocalBoardPersistsPagesAndReadsLikeRust;TestLocalBoardAppendPersistsEvidenceWithoutSideEffectsLikeRust`

## 1. `message-board-bulk-subscriptions`

### 1.1 pin 树里的语义载体（行号取自 `55897425e8` 版本）

| 文件:行 | 承载了什么 |
| --- | --- |
| `message-board/src/local.rs:337-398` | `set_subscription`：**单目标**订阅变更，一个 `begin_write()` 事务内成对写 `subscription_opt_outs` 与 `subscriptions`（Subscribe：删 opt-out + `INSERT OR IGNORE`；Unsubscribe：删订阅 + `INSERT OR IGNORE` opt-out），末尾 `tx.commit()`；target 为 channel 时先 `channel_summary()` 校验频道存在，为 thread 时校验必须是 top-level post |
| `message-board/src/local.rs:427-441` | `subscribe`（隐式订阅辅助路径，同样的成对写语义） |
| `message-board/src/local.rs:476-519` | `channel_summary`：频道存在性 + last message 探测，批量版"未知频道整体失败"要用的就是它 |
| `message-board/src/local.rs:538-549` / `:559-...` | `validate_channel` / `target_key`：名称与 target 键归一化 |
| `message-board/src/local/queries.rs:86-108` | `subscribed_channels`：用 `EXISTS(SELECT 1 FROM subscriptions WHERE ... target=json_object('Channel',c.name) ...)` 探测订阅态 —— 批量版"取回全部目标当前态再决定写不写"的同一形状 |

### 1.2 为什么不能写 `local/subscriptions.rs`

```
$ git -C <rust> cat-file -e 55897425e8:codex-rs/message-board/src/local/subscriptions.rs   → 不存在
$ git -C <rust> log --diff-filter=A --format='%h %ad %s' -1 748ab55ca7 \
      -- codex-rs/message-board/src/local/subscriptions.rs
  eb485f57ba 2026-11-06 Add atomic bulk channel subscription updates (#59998)
```

即：该文件是**本条 delta 自己新建**的。"多目标、全量校验后一次提交、只写 diff"这个行为在 pin 树里根本不存在，pin 只有单目标 `set_subscription`。所以：
- `rustPaths` 必须指向 pin 里的承载文件（上表）；
- 契约 `note` 里写一句 **"bulk/atomic multi-target semantics introduced after pin by eb485f57ba (#59998); pin tree only has single-target set_subscription"**。

## 2. `message-board-fixed-read-view`

### 2.1 pin 树里的语义载体

| 文件:行 | 承载了什么 |
| --- | --- |
| `message-board/src/local/queries.rs:166-211` | `list_channels`：实时 SQL（`instr(c.name_search,…)` + `COALESCE(MAX(p.timestamp), c.timestamp)` 排序）+ 逐频道 `channel_summary`；**无 cutoff 参数** |
| `message-board/src/local/queries.rs:212-297` | `list_threads` |
| `message-board/src/local/queries.rs:298-357` | `search_posts` |
| `message-board/src/local/queries.rs:358-392` | `read_thread` |
| `message-board/src/local.rs:476-519` | `channel_summary`：fixed view 要冻结的"目录 + 计数 + last message"形状来源 |
| `message-board/src/local/connection.rs:40-46` / `:112-124` | `read_only()` 连接构造 / `Access` 枚举与 `require_write`：fixed view 的"所有 mutation 被拒"依赖的权限面 |
| `message-board/src/local/paging.rs:15-63` | `Window`：快照后仍要按窗口切页，fixed view 的 `list_channels` 直接复用它（`skip/take`） |

### 2.2 为什么不能写 `local/catalog.rs`

```
$ git -C <rust> cat-file -e 55897425e8:codex-rs/message-board/src/local/catalog.rs      → 不存在
$ git -C <rust> log --diff-filter=A --format='%h %ad %s' -1 748ab55ca7 \
      -- codex-rs/message-board/src/local/catalog.rs
  632eee03e9 2026-11-06 Add fixed read views for local message boards (#59993)
```

`LocalBoardReadView` / `ReadView { post_cutoff, channels }` / `BoardMode::Fixed` 全部是 pin 之后才有的结构。pin 树里"读到稳定边界"这件事**不存在**（四个读面都是实时查询、无 cutoff），所以：
- `rustPaths` 指向上面四个读面 + 权限面；
- 契约 `note` 写 **"fixed view (LocalBoardReadView + post_cutoff) introduced after pin by 632eee03e9 (#59993)"**。

## 3. 顺带做的一次全量审计（防止下一组再被拦）

把我这几个月提交过的所有 plan 文档里的 Rust 路径（共 **62** 个）逐个用 `git cat-file -e 55897425e8:<path>` 验了一遍：**9 个不在 pin 树**。除本轮被拦的 2 个外，其余 7 个**都没进 manifest**（只在散文/清单里当 upstream 指针），但为避免将来误登记，一并列正：

| 我文档里的写法 | pin 树 | 正确全路径 | 引入提交 / 说明 |
| --- | --- | --- | --- |
| `message-board/src/local/subscriptions.rs` | ✗ | pin 用 `local.rs` + `local/queries.rs` | `eb485f57ba` #59998（本轮已修正） |
| `message-board/src/local/catalog.rs` | ✗ | pin 用 `local/queries.rs` + `local.rs` + `local/connection.rs` | `632eee03e9` #59993（本轮已修正） |
| `guardian-v3/src/retrieval_tools.rs` | ✗ | `ext/guardian-v3/src/retrieval_tools.rs` | `e4bd7871b1` #59994；**我漏写了 `ext/` 前缀**，且该文件 pin 后新增 |
| `guardian-v3/src/subscription_tools.rs` | ✗ | `ext/guardian-v3/src/subscription_tools.rs` | `8d0098930e` #59995；同上 |
| `guardian-v3/src/selection.rs` | ✗ | `ext/guardian-v3/src/selection.rs` | `8d0098930e` #59995 / `af83299ada` #59999；同上 |
| `guardian-v3/src/delivery.rs` | ✗ | `ext/guardian-v3/src/delivery.rs` | `5f82dc9e59` #60000 / `ecef7e0936` #60001；同上 |
| `message-board/src/local/json_search.rs` | ✗ | 同路径（存在，但 pin 后新增） | `e4bd7871b1` #59994 |
| `message-board/src/read_view.rs` | ✗ | 同路径（存在，但 pin 后新增） | `09bd19a0de` #60002 |
| `tui/src/analytics/runtime_auth_tests.rs` | ✗ | 同路径（存在，但 pin 后新增） | `cce9d3228b` #60101 |

**pin 树里确实有 `codex-rs/ext/guardian-v3`**，可用文件是：

```
$ git ls-tree -r --name-only 55897425e8 -- codex-rs/ext/guardian-v3
  BUILD.bazel · Cargo.toml
  src/evidence.rs · src/housekeeper.rs · src/lib.rs · src/sampling.rs
  tests/evidence.rs · tests/housekeeping.rs · tests/snapshots/…
```

所以 guardian 组将来登记契约时：`rustPaths` 写 `ext/guardian-v3/src/{lib.rs,evidence.rs,housekeeper.rs,sampling.rs}` 里承载该逻辑的那个（**注意 `ext/` 前缀**）；`retrieval_tools.rs` / `selection.rs` / `subscription_tools.rs` / `delivery.rs` 属于 pin 后新增，只能配 note 使用。另外 pin 里还有 `ext/guardian-v2`、`ext/guardian-reviewer` 两个同级 crate，别混用。

## 4. 你给我复核时可直接跑的命令

```bash
R=$(git -C /home/jacks/jacks_dev/codex rev-parse 55897425e8)
for p in message-board/src/local.rs message-board/src/local/queries.rs \
         message-board/src/local/connection.rs message-board/src/local/paging.rs \
         message-board/src/local/catalog.rs message-board/src/local/subscriptions.rs; do
  git -C /home/jacks/jacks_dev/codex cat-file -e "${R}:codex-rs/$p" 2>/dev/null && echo "OK   $p" || echo "MISS $p"
done
```

我这边的实际输出：

```
OK   message-board/src/local.rs
OK   message-board/src/local/queries.rs
OK   message-board/src/local/connection.rs
OK   message-board/src/local/paging.rs
MISS message-board/src/local/catalog.rs          ← 被拦的原因
MISS message-board/src/local/subscriptions.rs     ← 被拦的原因
```

> 坑：**zsh 下必须写 `${R}:codex-rs/...`**。写 `"$R:codex-rs/..."` 会被 zsh 当成参数修饰符（`$R:h` 之类），六行全 `MISS`，看起来像 pin 树没有 message-board——我第一次就是这么误判的。

## 5. 判定与 verifier 不变（复核用）

| contract id | status | verifier（不变） |
| --- | --- | --- |
| `message-board-bulk-subscriptions` | `partial` | `TestLocalBoardSubscriptionsOptOutsAndDeletionLikeRust` |
| `message-board-fixed-read-view` | `partial` | `TestLocalBoardPersistsPagesAndReadsLikeRust;TestLocalBoardAppendPersistsEvidenceWithoutSideEffectsLikeRust` |

Go 侧零命中证据不变（`SetSubscriptions|SubscriptionBatch|Bulk` → 0；`ReadView|readView|FixedView|postCutoff` → 0），见 `update/plan_2026_11_08_thread_storage.md` §3.2 / §4.2。
