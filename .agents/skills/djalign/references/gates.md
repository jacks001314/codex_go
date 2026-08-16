# 分层门禁与认证

## L0-L4 门禁

| 层级 | 内容 | 成本 | 时机 | 通过标准 |
|---|---|---|---|---|
| L0 静态快照 | 关键文件 SHA256、schema 一致性、代码生成无漂移、枚举/清单比对 | 秒级，免 token | 每次同步/PR | 无漂移或漂移全部入册 |
| L1 单元契约 | 共享 fixtures 双跑、协议 roundtrip、SQLite 迁移字节比对 | 分钟级，免 token | 每次同步 | 全部 pass |
| L2 录制-重放 | Rust 录制重放进 Go，契约级比对 | 分钟级，免 token | 每次同步 | 事件序列与文件副作用一致 |
| L3 SDK 差分 | `sdktests` 场景子集（漂移波及域）或全量 | 烧 token | 每日一次/认证前 | 无 behavior mismatch（infra 例外需证据） |
| L4 认证 | 全平台矩阵 + 连续干净运行 + 基线更新 | 最贵 | 发布/里程碑 | 见下方认证标准 |

L0-L2 免 token 且全覆盖，L3-L4 只做最终背书——静态兜底、动态把关。

## 认证标准

对齐 `parity/baseline.json` 的既有 requirements（冻结时为准，当前参考值）：

- `zeroExceptions`：`domains.json` 无 open 状态异常
- 平台矩阵：windows/linux/darwin × amd64/arm64 原生或交叉构建通过
- `requiredConsecutiveDifferentialRuns`：连续 N 次干净差分运行（当前 3）
- `requiredConsecutiveSDKRuns`：连续 N 次干净 SDK 运行（当前 3）
- 每个 contract 在 `parity/contracts/manifest.json` 中 `complete` 且有 verifier 证据

认证通过后更新 `parity.json`（冻结基线 + 证据路径 + 生成时间），并更新 `parity/baseline.json`。

## 指标

- 静态：`契约覆盖度`（manifest complete 占比）、`再生成无漂移`（生成器跑完 diff 为空）、`未映射 API 数`（应为 0 或白名单）
- 动态：`差分干净率`（契约一致场景/总场景）、`差异→契约转化率`（每个差异都有 fixture/manifest 落地，应为 100%）、`连续干净次数`

## 与既有文件的关系

- `parity.json`：最后认证的 Rust/Go SDK 基线记录
- `parity/baseline.json`：冻结的 Rust 目标、Go 起始 commit、平台矩阵、认证阈值
- `parity/commits.json`：两个基线之间的每个 Rust commit 的账目
- `parity/domains.json`：逐域实现状态与验收标准（`complete`/`equivalent`/`not_applicable` 是封闭状态，需要证据）
- `parity/contracts/manifest.json`：Rust 协议/数据 oracle 到 Go 实现与 verifier 的映射

`certificationReady` 只有在每个 domain 和 commit 关闭、每个 contract 完整、无异常状态时才成立；不要为了推进认证而弱化标准。
