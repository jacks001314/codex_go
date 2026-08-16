---
name: djalign
description: 用动静结合（静态契约冻结 + 动态行为验证）的方法对齐 codex_go 与 Rust 版 codex：同步并复用 Rust 数据资产、schema 代码生成与清单快照、录制-重放与 SDK 差分、漂移驱动用例与差异沉淀契约的双向闭环、L0-L4 分层门禁与认证。用于制定和实施对齐计划、评估 Rust 上游变更、排查 Go/Rust 行为差异、沉淀回归契约。
---

# 动静结合对齐（djalign）

把静态与动态对齐编织成一个闭环体系，而不是两张并行清单。静态层负责"把 Rust 的契约冻成 Go 的代码和数据"（便宜的全量覆盖），动态层负责"用真实行为验证契约"（昂贵的抽样把关），两个结合点把它们焊死，最后统一收口到认证基线。

## 目标与边界

- **对齐目标是"可观察契约一致"，不是代码逐行相同，也不是输出逐字节相同**（模型措辞、TUI 像素、时间戳、UUID 不要求逐字一致）。
- **单一事实源是 Rust 上游仓库**：windows 位置 `D:\qax\reagent\dev\git\codex`，linux 位置需探测。Go 侧所有 schema、fixture、清单、golden 都从它同步、引用或校验，不手写第二份。
- **复用现有设施**：`parity/`、`sdktests/`、`vscodetests/`、`.tmp-slash-parity/`、`parity.json` 已有的对齐设施优先扩展，不另建框架。
- 先读当前仓库状态、`parity.json`、`parity/baseline.json` 和相关实现；不要假定二进制、上游仓库或 SDK 的位置。

## 核心机制：两个结合点

### 结合点 1：静转动（静态筛，动态验）

每次同步后，静态层产出**漂移报告**：新增了哪些 schema 字段、哪些关键文件 hash 变了、哪些 Rust API 未映射。动态层**只跑被漂移波及的域**（按 `parity/domains.json` 的域归属和 `parity/contracts/manifest.json` 的映射选择用例），不重跑全量，把 token 消耗和 CI 时间压到最小。

### 结合点 2：动转静（动态发现，静态固化）

动态层跑出的每个**行为差异**，必须沉淀为新的静态契约（golden / replay fixture / manifest 条目），否则不允许关闭。从此该差异进入静态层被永久冻结：每次同步的 hash 比对、每次回归测试都会盯住它，防止无声漂移。**"每个动态差异必须产出一个静态契约"是硬性验收标准。**

## 总体流程

### 0. 冻结基线

记录 Go 仓库 commit 与 dirty 状态、`parity.json` 的 Rust baseline、Rust 仓库当前 commit/tag；不一致时同时报告，不静默替换基线。输出 `run-manifest.json` 或等价记录（时间、OS/架构、两端 commit、二进制哈希、SDK 版本、模型、场景列表；环境变量只记录白名单名称，不记录秘密值）。

### 1. 同步数据资产

定位/拉取 Rust 仓库，把静态层需要的输入对齐到当前 baseline：

- schema：`app-server-protocol/schema/`（含 precomputed）、`core/config.schema.json`、`state/migrations` 等
- fixtures：`apply-patch/tests/fixtures`、`tools/tests/fixtures`、`http-client/tests/fixtures`、`backend-client/tests/fixtures`、`tui/tests/fixtures`（录制）等
- 清单：workspace members、关键文件、TUI snapshot 清单、feature keys 等

详细清单与做法见 [references/static-layer.md](references/static-layer.md)。

### 2. 静态冻结

运行生成器与清单比对，产出漂移报告：`新增 / 变更 / 未映射` 三类条目，每条注明涉及域与建议的动态用例。漂移报告是结合点 1 的输入。

### 3. 动态验证

按漂移报告选择用例，按成本从低到高执行：

1. 免 token：共享 fixtures 双跑、录制-重放
2. 烧 token：SDK 契约差分、CLI/TUI 黑盒差分
3. 补盲区：模糊/属性测试

方法与差异分类见 [references/dynamic-layer.md](references/dynamic-layer.md)。

### 4. 差异处置（动转静）

- 先确认不是认证、限流、模型服务、SDK 版本、超时等基础设施问题。
- 找到第一个分歧点，缩到最小复现，分类为 `go-bug` / `baseline-drift` / `model-nondeterminism` / `platform-difference` / `infra-failure`。
- 只有证据指向 Go 行为缺陷时才改 Go 代码；修复后先跑相关 Go 单元测试，再重跑最小场景。
- **每个差异必须沉淀契约**（新增/更新 fixture、golden、manifest 条目），否则不关闭。
- 不通过修改 Rust 基线、SDK 或场景预期来掩盖 Go 缺陷；Rust 自身失败时保留证据并报告为 baseline/infra 问题。

### 5. 门禁与认证

按 L0-L4 分层执行并记录证据，满足认证标准后更新 `parity.json`。门禁定义、认证标准与指标见 [references/gates.md](references/gates.md)。

### 6. 提交与总结

提交测试、契约、报告与漂移记录；总结对齐状态（哪些域 closed、哪些域有阻塞、下一个动作）。

## 分层门禁速览

| 层级 | 内容 | 成本 | 时机 |
|---|---|---|---|
| L0 静态快照 | 关键文件 SHA256、schema 一致性、生成无漂移、枚举清单 | 秒级，免 token | 每次同步/PR |
| L1 单元契约 | 共享 fixtures 双跑、协议 roundtrip、SQLite 迁移字节比对 | 分钟级，免 token | 每次同步 |
| L2 录制-重放 | Rust 录制重放进 Go，契约级比对 | 分钟级，免 token | 每次同步 |
| L3 SDK 差分 | `sdktests` 全场景双跑 | 烧 token | 每日一次/认证前 |
| L4 认证 | 全平台矩阵 + 连续干净运行 + 基线更新 | 最贵 | 发布/里程碑 |

## 通用原则

- 每次运行使用独立 `CODEX_HOME` 和工作区副本；除明确测试 resume/import 外不共享会话、配置或缓存。
- 不输出、复制或提交 API key、认证文件、完整用户配置；真实调用会产生费用，执行前确认凭据与场景范围，未经授权不扩大到高费用、长时间、外部写入或危险命令场景。
- 归一化时只移除已证明不稳定的字段（UUID、时间戳、绝对临时路径、耗时、后端 request ID），用一致占位符维护引用关系；每条 ignore/normalization 规则必须包含理由、示例与字段路径。
- 两端使用相同 prompt、模型、reasoning effort、SDK config、超时与起始 fixture；默认并发 1；多次采样时交替两端顺序。

## 相关资源

- [references/static-layer.md](references/static-layer.md)：静态对齐方法（代码生成、API 表面清单、hash/枚举快照、golden 复用）与输出物格式
- [references/dynamic-layer.md](references/dynamic-layer.md)：动态验证方法（共享 fixtures 双跑、录制-重放、SDK 差分、CLI/TUI 黑盒、模糊测试）与差异分类
- [references/gates.md](references/gates.md)：L0-L4 门禁、认证标准与指标
- 配合使用 `$sdktests`（SDK 差分）、`$tuialign`（TUI 差分）、`$codex-go-update`（每日同步与更新计划）
