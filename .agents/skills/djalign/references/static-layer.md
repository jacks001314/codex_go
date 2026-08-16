# 静态层：契约冻结

静态层把 Rust 的契约冻成 Go 侧的代码和数据，成本低、可全量覆盖、每次同步都跑。产出物是**漂移报告**（结合点 1 的输入）。

## 数据资产清单（从 Rust 仓库同步/引用）

以下路径均相对 Rust 上游根（windows：`D:\qax\reagent\dev\git\codex`）：

- schema：`codex-rs/app-server-protocol/schema/`（含 `precomputed/app-server-exports-*.json.zst`）、`codex-rs/core/config.schema.json`
- 迁移：`codex-rs/state/migrations` 及 logs/goals/memories/thread_history 迁移
- 录制：`codex-rs/tui/tests/fixtures/`（如 `oss-story.jsonl`）、rollout JSONL 样例
- fixtures：`codex-rs/apply-patch/tests/fixtures/scenarios/`、`codex-rs/tools/tests/fixtures/`、`codex-rs/http-client/tests/fixtures/`、`codex-rs/backend-client/tests/fixtures/`
- 清单：`codex-rs/Cargo.toml` workspace members、TUI snapshot 目录（`tui/src/**/snapshots`）、feature keys 等

## 方法（按优先级）

### 1. schema 代码生成（消灭手写漂移，收益最大）

把 Rust 的 schema 作为单一事实源，生成 Go 类型与 vendored 数据：

- app-server protocol schema JSON → Go 协议类型
- `config.schema.json` → Go 配置结构
- rollout JSONL 格式 → Go 序列化结构

已有先例：`appserver/precomputed_exports.go`（zstd 字节级一致）、`state/migrations.go`（55 个 SQL 文件字节级比对）。关键要求：

- 生成器可重复运行，并配"**再生成无漂移**"测试：生成后工作区 diff 为空，否则失败。
- 生成的代码不得手改；需要改时改生成器或 schema。

### 2. API 表面清单（比 hash 更聪明）

用 `cargo-public-api` 或 rustdoc JSON 导出 Rust 公共 API 清单，Go 侧维护"已实现映射"。映射表要能区分：

- **新增项**（上游加了什么）→ 报"未映射"，进入待办
- **漂移项**（实现与 Rust 不一致）→ 报"不一致"，进入修复

已有先例：`parity/rust_snapshot_test.go`（workspace members、关键文件 SHA256）、`parity/rust_cli_surface_test.go`、`parity/rust_exec_server_protocol_test.go`。

### 3. hash / 枚举快照

- 关键文件 SHA256 快照（已有 `TestRustCriticalFileHashesSnapshot`）
- 枚举清单：feature keys、tool 名称、错误码、事件名、sandbox 类型、模型元数据——与 Rust 逐项比对
- TUI snapshot 清单：数量/路径/优先级/必选文件（已有 `TestRustTUISnapshotManifestCoversPrioritySurfaces`，当前基线 663 个）

### 4. golden 复用（两边吃同一份数据）

直接**引用** Rust 检入的 fixtures 作为 Go 测试输入，而不是复制后手改：

- `apply-patch` 24 个场景（patch.txt/input/expected）→ Go applypatch 测试
- `tools/tests/fixtures/json_schema_policy/`（google/outlook/notion/slack）→ Go 工具 schema 策略测试
- TUI snapshots → Go 渲染测试的期望输出

引用方式可以是同步脚本拷贝到 Go 侧 fixture 目录（保留来源 commit 记录），也可以是测试运行时直接读 Rust 仓库路径（需 `CODEX_RUST_ROOT` 之类环境变量，参考 `parity/README.md`）。

## 输出物：漂移报告

每次静态冻结后产出机器可读报告，至少包含：

```text
baseline: rust=<commit> go=<commit>
changed:  [schema 字段/文件 hash/枚举 ...]
added:    [新增未映射 API / 新增 fixture ...]
removed:  [...]
affectedDomains: [domains.json 中的域 id ...]   # 结合点 1 的动态用例选择输入
```

报告写入 `parity/` 下或当次 update plan（`update/plan_*.md`），并纳入提交。

## 与现有代码的对应

`parity/` 已有 verifier 可作为样板：`features`、`cli`、`app-server-schema-*`、`exec-server-protocol`、`state-migrations`、`rollout-layout`、`tui-snapshots`。新增静态契约时优先扩展 `parity/contracts/manifest.json`（新增 contract 条目：rustPaths / goPaths / verifier）。
