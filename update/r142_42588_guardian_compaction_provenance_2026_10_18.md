# Round 658：Guardian compaction provenance fail-closed（#42588）

Rust 依据：`ad8ee16a5f4c7445253b57a10cb1f8489c8c3e6a`。

## 差异

Rust 在 thread-owned Guardian context 模式下，只有父 compaction checkpoint 的
producer compatibility hash 与评分模型一致且非空时才允许复用。缺失或未知
provenance 必须 fail closed，不能因为异步评分或缓存批准而省略 compacted
context；sampler 也会拒绝不兼容 checkpoint。

Go 已有 `compact.Result.CompactionModelHash` 与
`retainedctx.HarnessMetadata.CompactionModelHash`，但 Guardian reset 此前只在
“新旧 hash 都非空且不同”时丢弃种子，缺失 hash 会继续复用；本地 fallback
compaction 也没有记录 producer hash。

## 落地

- `guardianParentCompactionSeedAllowed` 要求 reuse 开启且两侧 hash 均非空并相等；
  缺失、空白、未知或不匹配都返回 false。
- `resetGuardianAfterParentCompaction` 在不可复用时调用
  `DropStaleParentCompaction`，不再把旧 reviewer checkpoint 带入新上下文。
- 运行时 compaction 在本地 fallback 结果缺少 hash 时，用当前/记录模型解析
  `comp_hash` 补齐，并把 `compaction_model_hash` 写入线程记录元数据。

## 剩余

Go 没有 Rust 的独立 async Guardian sampler/extension 路径；Rust 的 sampler
`IncompatibleCompaction` 错误和 synchronous-review escalation 在 Go 中体现为
丢弃 seeded checkpoint，而不是单独的 sampler 错误类型。历史 checkpoint 的
provenance 仍以线程记录元数据为主，尚未逐项写入每个 compaction item。

## 验证

```text
go test ./appserver -run 'Guardian.*Compaction|GuardianParentCompaction|CompactionModelHash|ContextCompaction' -count=1
go test ./compact -count=1
```
