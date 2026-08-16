# 动态层：行为验证

动态层用真实行为验证静态层冻结的契约。按成本从低到高执行；免 token 层每次同步都跑，烧 token 层按漂移报告选择或认证前全量跑。

## 方法 1：共享 fixtures 双跑（免 token）

两端吃同一份 fixture，比可观察结果：

- apply-patch 24 个场景：Go 与 Rust 各自应用 patch，比较最终文件树、内容和拒绝语义
- config 解析：同一 toml/JSON 输入，比较解析结果与默认值
- 协议 roundtrip：同一消息序列化→反序列化，比较字节与字段
- SQLite 迁移：迁移后的 schema 与 Rust 字节级一致（已有 `TestStateMigrationsMatchRustTarget`）

## 方法 2：录制-重放（免 token，性价比最高）

把 Rust 真实录制的会话/事件原样喂给 Go 的 app-server/核心，契约级比对：

- 录制来源：`tui/tests/fixtures/oss-story.jsonl`（TUI 事件轨迹）、rollout JSONL、thread history
- 重放方式：按事件逐条驱动 Go 实现，或把 rollout 作为输入恢复会话
- 比对内容：事件序列、item 状态、文件副作用、可恢复性；模型文本不做字符串 diff
- 复用 `sdktests` 的归一化逻辑；一次录制可无限回归、不烧 token、可进 CI

录制文件本身是静态资产（结合点 2 的产物），重放 harness 是动态设施。

## 方法 3：SDK 契约差分（烧 token）

使用官方 TypeScript SDK 驱动双实现，录事件流、归一化、比"可观察契约"而非模型措辞。完整工作流见 `$sdktests` skill；本 skill 场景下：

- 按漂移报告的 `affectedDomains` 选择场景子集，不默认全量
- 每个失败差异**沉淀为回归 fixture**（录 Rust 行为为 golden），进入静态层
- 与 `parity.json` 的认证基线保持一致，不静默换基线

## 方法 4：CLI/TUI 黑盒差分

- CLI：同一命令/参数/输入，比较 stdout/stderr/退出码/文件副作用；已有 `.tmp-slash-parity/` 与 `scripts/slash-parity-runner.mjs`
- TUI：同一按键序列，比较 VT 事件、截图或渲染输出；工作流见 `$tuialign` skill；`.playwright-cli/` 是页面级录制
- 场景沉淀为**剧本文件**（按键序列 + 输入 fixture），双实现共用

## 方法 5：模糊/属性测试（补盲区）

对纯函数层（apply-patch、config 解析、协议序列化、rollout 写入）生成同一随机输入，双实现跑同一输入比结果：

- Rust 二进制或编译产物做 oracle；Go 直接调用比对
- seed 语料可以取 Rust 测试输入，保证覆盖同一空间
- 发现的差异进入结合点 2 沉淀契约

## 差异分类与处置

发现 mismatch 后：

1. 从 raw artifacts 确认不是认证、限流、模型服务、SDK 版本、超时等基础设施问题。
2. 找到第一个发生分歧的事件，而不是只比较最终结果。
3. 缩到最少 prompt/fixture/选项，从已保存记录离线重复 comparator。
4. 对照 Rust 当前实现、协议类型与 SDK 参数生成逻辑，定位 Go 所属模块。
5. 分类：`go-bug` / `sdk-assumption` / `baseline-drift` / `model-nondeterminism` / `platform-difference` / `infra-failure`。
6. 只有 `go-bug` 才改 Go 代码；修复后先跑相关 Go 单元测试，再重跑最小场景。
7. **沉淀契约**：新增/更新 fixture、golden 或 manifest 条目，否则不关闭（结合点 2）。

禁止为了让测试通过而宽泛删除事件、错误、空值差异或未知字段；Rust 自身失败时保留证据并报告为 baseline/infra 问题。
