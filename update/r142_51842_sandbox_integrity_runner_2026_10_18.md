# Round 675：sandbox integrity runner（#51842）

Rust 依据：`c2eb1f42a0914bf7a204fccb54797c17badacd5a`。

## 落地

- `sandbox/integrity.RunIntegrityChecks` 作为 observational API 导出，默认使用
  进程全局 metrics，也支持注入 `RunnerMetrics`。
- `RunnerRequest` 携带 sandbox type、override、filesystem entries、cwd 和
  command；runner 不改变 launch 结果。
- 跳过：
  - sandbox type 与当前平台 backend 不匹配；
  - `escalatedSandboxWithRestrictions`；
  - 真正的 full-disk-write policy。
- 普通 root-write + deny/read carveout policy 仍会检查。
- preparation 与 dependency checks 在 goroutine 中执行；等待上限 5 秒，超时后
  不再接收 late result。
- 依赖包括 codex executable，以及 Linux/Windows 的 command launcher。
- metrics 记录 `codex.sandbox_integrity.check`、`preparation_ms`、
  `duration_ms`，tags 包含 backend/checker/dependency/outcome/error_kind、
  deny-glob、resolution/hard-link/write-root 状态，不包含文件路径。

## 验证

```text
go test ./sandbox/integrity -count=1
go vet ./sandbox/integrity
```

Windows 测试覆盖 finding、clean、ineligible skip、取消/超时和 metrics 不含路径。
