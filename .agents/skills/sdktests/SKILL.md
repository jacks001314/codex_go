---
name: sdktests
description: 通过官方 Codex TypeScript SDK 对 Rust 与 Go 版 Codex CLI 进行真实端到端差分测试，建立场景、运行双实现、采集并归一化 SDK 事件、比较协议行为与工作区副作用、定位差异并沉淀回归测试。用于验证 codex_go 功能对齐、复现 SDK 兼容性问题、评估 Rust 上游变更，或在单元测试之外执行真实模型、工具、会话和错误路径测试。
---

# SDK 实景对比测试

把 Rust CLI 当作行为基线，通过同一版本的官方 TypeScript SDK 和同一场景分别驱动 Rust、Go 二进制。比较可观察契约，不要求非确定性的模型文本逐字相同。

## 基本原则

- 先读当前仓库状态、`README.md`、`parity.json` 和相关实现；不要假定二进制、上游仓库或 SDK 的位置。
- 默认在当前仓库的 `sdktests/` 建立可提交的测试工程与场景。若已有 SDK 差分设施，扩展它而不是另建框架。
- 优先使用本地 Rust 上游 `sdk/typescript`，让 SDK 与 Rust 基线来自同一提交。windows 上游位置是 `D:\qax\reagent\dev\git\codex`，linux 上游位置是"../codex" 但必须探测并记录实际路径与 commit。
- 使用 SDK 的 `codexPathOverride` 注入待测二进制；测试代码不得绕开 SDK 直接为两端实现不同的调用逻辑。
- 每次运行使用独立 `CODEX_HOME` 和工作区副本。除明确测试 resume/import 外，不共享会话、配置或缓存。
- 不输出、复制或提交 API key、认证文件、完整用户配置。报告中只记录认证方式是否可用。
- 真实调用会产生费用并可能改动文件。执行前确认凭据可用、目标模型和场景范围；未经用户授权，不扩大到高费用、长时间、外部写入或危险命令场景。

## 工作流

### 1. 冻结基线

1. 记录 Go 仓库 commit、dirty 状态和 `parity.json` 的 Rust baseline。
2. 定位 Rust 仓库并记录 commit/tag；若与 `parity.json` 不一致，同时报告，不静默替换基线。
3. 定位或构建两端二进制，分别运行 `--version` 和一个无网络 smoke 命令。Go 默认可用 `go build -o <temp>/codex-go[.exe] ./cmd/codex`；Rust 构建方式以其仓库说明为准。
4. 定位 Node、包管理器和 TypeScript SDK。优先复用上游 lockfile 与 workspace 命令，禁止随意升级 SDK 依赖。
5. 生成 `run-manifest.json`，记录时间、OS/架构、Node/Go/Rust 版本、两个 commit、SDK 版本、二进制哈希、模型、配置覆盖和场景列表。环境变量只记录白名单名称，不记录秘密值。

### 2. 建立 harness

若 `sdktests/` 尚不存在，创建最小 TypeScript 工程。至少提供：

- 一个场景清单，声明名称、输入、thread/turn 选项、超时、期望不变量、允许副作用和比较规则。
- 一个 runner，用完全相同的场景函数构造 `new Codex({ codexPathOverride, env, config })`，先后运行 `rust` 与 `go`。
- 一个 recorder，消费 `runStreamed()` 的完整 async event stream，同时保存最终结果、异常、退出状态、stderr、持续时间和运行后的工作区快照。
- 一个 normalizer，只按显式规则清除不稳定字段。
- 一个 comparator，输出机器可读 JSON diff 和人可读 Markdown 报告，并以退出码区分 pass、behavior mismatch、infra failure。

支持以下命令或等价接口：

```text
test:smoke                  最小、低费用的连通性检查
test:parity --scenario X    双端执行指定场景
test:parity --all           执行已批准的完整矩阵
report                      从保存的原始记录重新比较，不再次调用模型
```

原始记录必须不可变地保存在 artifacts 下，归一化结果另存。报告能给出复现命令。将凭据、临时 home、模型生成文件和大体积 artifacts 加入 `.gitignore`；提交场景、归一化规则、期望不变量和精简的失败 fixture。

### 3. 设计场景矩阵

先跑小矩阵，再按差异扩展。至少覆盖：

1. 单轮纯文本：thread 创建、turn 生命周期、usage 与最终 agent message。
2. 流式事件：事件类型、必要字段、每个 item 的 started/completed 顺序和 turn terminal event。
3. 多轮与 resume：同一 thread 连续运行，并用持久化 thread ID 恢复。
4. 工作区读取：要求读取固定 fixture 并输出 JSON Schema 约束的事实。
5. 文件写入：在临时工作区完成确定性编辑，比较最终文件树、内容和补丁语义。
6. 命令执行：运行无网络、跨平台或按平台声明的确定性命令，比较命令、状态、退出码和输出语义。
7. 配置映射：model、reasoning effort、sandbox、approval policy、working directory、additional directories 和 config overrides。
8. 输入类型：文本、图片（环境支持时）和 structured output。
9. 中断与错误：AbortSignal、无效工作目录、无效 schema、认证失败或受控网络失败。

每个场景都要写可验证的不变量。避免“让模型自由修改项目”这类不可比较提示；使用小型 fixture、精确目标、固定 schema 和明确终止条件。涉及审批交互、MCP、web search、网络访问或外部系统时，拆成显式 opt-in suite。

### 4. 公平执行

- 两端使用相同 prompt、模型、reasoning effort、SDK config、超时和起始 fixture。
- 分别复制同一 pristine fixture，禁止第二次运行继承第一次运行的文件变化。
- 尽可能使用同一种认证方式；若共享认证材料，只复制到隔离 home，运行结束后安全清理。
- 记录测试顺序。多次采样时交替 `rust -> go` 与 `go -> rust`，避免后端时段漂移只影响一端。
- 默认并发为 1，避免 rate limit 和共享资源造成伪差异。
- 为每个 turn 设置超时，并在超时后终止子进程；报告 timeout，而不是挂起整个 suite。
- smoke 通过后才运行写文件、长上下文、多轮或网络 suite。

### 5. 归一化与比较

保留 raw event；归一化时只移除已证明不稳定的值，例如 UUID、时间戳、绝对临时路径、耗时和后端 request ID。用一致占位符维护引用关系，例如首个 thread ID 始终映射为 `<THREAD_1>`，不能直接删除所有 ID。

分三层判定：

1. **严格契约**：进程退出、SDK 是否抛错、事件类型及因果顺序、必填字段、item 状态、thread ID 可恢复性、structured output schema、工具退出码、文件副作用。默认严格相等或满足明确不变量。
2. **兼容契约**：允许新增可选字段、相邻独立 delta 的分块差异或无关事件交错，但必须保持 SDK 可解析性、生命周期和引用完整性。
3. **语义结果**：模型自然语言不做字符串 diff。通过 JSON Schema、关键事实、工具参数、最终文件内容或场景专用断言判断。无法稳定断言时标记 `informational`，不得据此宣称 parity。

禁止为了让测试通过而宽泛删除事件、错误、空值差异或未知字段。每条 ignore/normalization 规则必须包含理由、示例和适用字段路径。

### 6. 诊断差异

发现 mismatch 后按以下顺序缩小：

1. 从 raw artifacts 确认不是认证、限流、模型服务、SDK 版本或超时等基础设施问题。
2. 找到第一个发生分歧的事件，而不是只比较最终回答。
3. 将场景缩减为最少 prompt、fixture 和选项，并从已保存记录离线重复 comparator。
4. 对照 Rust 当前实现、协议类型与 SDK 参数生成逻辑，定位 Go 所属模块。
5. 分类为 `go-bug`、`sdk-assumption`、`baseline-drift`、`model-nondeterminism`、`platform-difference` 或 `infra-failure`。
6. 只有证据指向 Go 行为缺陷时才修改 Go 代码。修复后先运行相关 Go 单元测试，再重跑最小 SDK 场景和 smoke matrix。

不要通过修改 Rust 基线、SDK 或场景预期来掩盖 Go 缺陷。若 Rust 自身失败，保留证据并报告为 baseline/infra 问题。

### 7. 产出与完成标准

每轮生成报告，至少包含：

- 基线 manifest 与准确复现命令。
- 各场景 pass/fail/infra/informational 状态和耗时。
- 首个语义差异、对应 raw event 路径、归一化 diff 和工作区 diff。
- 差异分类、置信度、关联 Go/Rust 源码位置及建议修复。
- 新增或修改的测试、实际执行过的命令，以及未执行项和原因。

只有在双端都成功运行同一场景且严格/兼容契约通过时，才能标记该场景 parity。仅 Go 成功、仅单元测试通过、或因缺少凭据而跳过，都不能标记 parity。

## 迭代策略

- 首次实施先交付 3 个高信号场景：单轮流式、结构化输出、确定性文件编辑。
- 每个真实差异都沉淀为最小场景或离线 fixture；优先增强 comparator，再扩大场景数量。
- 保持 live suite 与离线 replay 分离。日常 CI 跑类型检查、normalizer/comparator 和 replay；有凭据的受控任务再跑 live parity。
- 当 Rust baseline 或 SDK 更新时，先重跑旧 fixtures，再有意识地更新 normalization 和场景预期，并在报告中说明契约变化。
