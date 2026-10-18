# `#42588` Guardian compaction provenance audit

- Rust 依据：`ad8ee16a5f4c7445253b57a10cb1f8489c8c3e6a`。
- Go 的 Guardian parent-compaction 复用要求 reuse 开启，且 checkpoint
  producer hash 与当前评分模型 hash 均非空并相等；缺失、空、mismatch、
  陈旧 auth identity 均丢弃 seed。
- local-fallback compaction 写入生产模型的 comp_hash；
  `compaction_model_hash` 随 thread metadata 保存并在 resume 后读取。
- Go 没有独立的 async Guardian sampler/extension 路径，Rust 的 sampler
  拒绝与同步 review escalation 没有载体；不注入不兼容 checkpoint 是等价
  的 fail-closed 行为。

验证：

- `go test ./appserver -run 'Guardian.*Compaction|GuardianParentCompaction|CompactionModelHash|ContextCompaction' -count=1`
- `go test ./compact -count=1`
