# Sobek JavaScript Engine：Rust Codex Code Mode 对齐与测试计划

## 1. 目标

在 Go 版 Codex 中使用 [`github.com/grafana/sobek`](https://github.com/grafana/sobek) 实现受控 JavaScript runtime，替换当前 `codemode.SessionRuntime.renderLocalExecution` 和 `tool/code_mode_exec.go` 中基于文本/正则的临时执行路径。

最终目标不是提供 Node.js 环境，而是对齐 Rust Codex `0.145.0` 的 code mode 可观察契约：

- 模型通过自由格式 `custom_tool_call(name="exec")` 提交 JavaScript。
- JavaScript 在隔离 runtime 中运行，只能通过注入的 `tools.*` 访问 Codex 工具。
- 嵌套工具继续经过现有 Router、sandbox、approval、hooks、network policy 和 telemetry。
- 支持 Promise、异步工具调用、并行组合、结构化输出、cell/yield/wait/terminate。
- rollout、SDK 事件、fresh-process resume 和错误恢复与 Rust 基线一致。

## 2. 基线与范围

### Rust 基线

- Rust repository：`../codex`，运行时必须记录实际绝对路径和 commit。
- Rust binary：测试时记录路径、`codex --version` 和 SHA-256。
- 当前已验证基线：Codex CLI `0.145.0`。
- 重点参考：
  - `codex-rs/core/src/tools/code_mode/execute_handler.rs`
  - `codex-rs/core/src/tools/code_mode/execute_spec.rs`
  - `codex-rs/core/src/tools/code_mode/mod.rs`
  - `codex-rs/code-mode-protocol/src/description.rs`
  - Rust code mode service、wait handler、response adapter 和 rollout trace。

### Go 现状

- `codemode/` 已具备协议类型、cell store、wait/terminate 基础结构。
- `codemode/session_runtime.go` 的 `renderLocalExecution` 仅模拟执行。
- `tool/code_mode_exec.go` 仅识别有限的 `tools.exec_command({...})` 文本。
- `custom_tool_call(exec)`、历史持久化、resume 和 `command_execution` 基础映射已经建立。

### 本期范围

本计划覆盖 Sobek runtime、通用工具桥接、输出 helper、异步/cell 生命周期、持久化和 Rust 差分测试。

不提供：

- Node.js API、`require`、npm 包、文件系统或网络直通。
- 浏览器 DOM、Web API 全兼容。
- 绕过 Codex 工具权限直接执行 shell、patch、MCP 或网络请求。

## 3. 核心设计原则

1. **引擎与 Codex 协议解耦**：Sobek 封装在 `codemode` 内，工具层只依赖 runtime 接口。
2. **默认拒绝能力**：runtime 不注入 `process`、`require`、`fetch`、`WebSocket`、`fs`、`net`、`child_process` 或真实 `console`。
3. **工具调用不走捷径**：所有 `tools.*` 必须进入现有工具 Router/Dispatcher。
4. **每个 exec 使用独立 JS global**：避免跨调用泄露；只有明确的 session store/cell 状态可跨调用保存。
5. **context 是取消根源**：SDK abort、turn cancel、session shutdown 必须中断 JS 和所有嵌套工具。
6. **原始协议优先**：保留服务端 `ctc_` ID，生成 Rust 前缀规则一致的 `ctco_`，不重建或重复模型历史。
7. **测试先于扩面**：每增加一种 JS/helper/tool 语义，先补离线测试，再跑真实 Rust/Go SDK 差分。

## 4. 建议架构

### 4.1 Runtime 接口

在 `codemode` 中定义引擎无关接口：

```go
type Engine interface {
    Execute(ctx context.Context, req EngineRequest) (*EngineResult, error)
    Interrupt(cause error)
    Close() error
}

type EngineFactory interface {
    NewEngine(session EngineSession) (Engine, error)
}
```

`EngineRequest` 至少包含：

- `ToolCallID`
- `Source`
- `EnabledTools`
- `YieldTimeMS`
- `MaxOutputTokens`
- `ThreadID` / `TurnID`

`EngineSession` 提供：

- 通用 nested tool caller
- session `store/load`
- 内容输出 sink
- cell/yield 控制器
- clock/timer abstraction
- cancellation 与 telemetry context

### 4.2 Sobek 适配器

新增建议文件：

```text
codemode/engine.go
codemode/sobek_engine.go
codemode/sobek_event_loop.go
codemode/sobek_tools.go
codemode/sobek_helpers.go
codemode/sobek_values.go
codemode/sobek_engine_test.go
```

每次 `Execute` 创建新的 `sobek.Runtime`，安装受控 globals，编译并运行用户源码。不得把 `sobek.Runtime` 在 goroutine 间并发使用；所有 JS 操作由单一 event-loop goroutine 串行执行。

### 4.3 通用工具桥接

根据当前 model-visible/deferred 工具注册表动态生成：

```js
tools.exec_command
tools.apply_patch
tools.update_plan
tools.mcp__server__tool
```

桥接接口：

```go
type NestedToolCaller interface {
    Call(ctx context.Context, request NestedToolRequest) (NestedToolResult, error)
}
```

要求：

- function tool 接收 object/string，并按 schema 传递。
- freeform tool 接收原始 string。
- namespaced 工具名按 Rust 规则转换为合法 JS identifier。
- Promise resolve 返回结构化 JS object；业务工具失败按 Rust 语义决定 resolve 失败结果还是 reject。
- 保留 nested call ID、父 `exec` call ID、thread/turn ID 和 invocation source。
- 并行性由 Dispatcher/Router 的工具能力决定，不能由 JS 绕过。

## 5. JavaScript 运行语义

### 必须支持

- 普通表达式、变量、对象、数组、函数和条件分支。
- 顶层异步执行包装和 `await tools.*`。
- `Promise.resolve/reject/all`。
- `try/catch/finally`。
- 顺序与并行 nested tool calls。
- `setTimeout` / `clearTimeout`。
- 未 await Promise 在 isolate 结束时丢弃，与 Rust一致。
- JS exception、Go error 和 context cancellation 的稳定转换。

### 明确禁用

- `eval`/`Function` 是否允许需与 Rust 实测后决定；默认禁用或限制。
- 动态 import、Node module、系统环境变量。
- runtime 直接文件、网络、进程访问。
- console 输出；模型可见输出只能走 helpers。

## 6. Global helpers 对齐

按 Rust 工具描述实现：

- `text(value)`：追加 text content；非字符串按 JSON/string 规则转换。
- `image(value, detail?)`
- `audio(value)`
- `generatedImage(result)`
- `store(key, value)` / `load(key)`：session scope、可序列化、容量受限。
- `notify(value)`：立即注入额外 `custom_tool_call_output`，需保证顺序和去重。
- `setTimeout` / `clearTimeout`
- `yield_control()`：立即产出当前内容并保持 cell running。
- `exit()`：成功终止当前脚本。
- `ALL_TOOLS`：只包含允许公开的工具元数据，不泄露 secrets/schema 内部信息。

每个 helper 都要有：参数校验、输出上限、取消检查、Rust fixture 和 SDK 场景。

## 7. Event loop、Promise 与取消

Sobek 本身不提供完整 Node event loop，需要实现最小调度器：

1. JS runtime 固定在一个 goroutine。
2. nested tool 在受控 goroutine 执行，将完成消息投递回 event loop。
3. event loop 负责 resolve/reject Promise，并运行 Sobek job queue。
4. timer 使用可取消 Go timer，回调回投 event loop。
5. `ctx.Done()` 时调用 Sobek interrupt，并取消所有 nested tool context。
6. runtime 终止后拒绝新回调、停止 timers、释放引用。

必须验证：

- cancellation 无死锁；
- Promise 不重复 resolve；
- 结束后无 goroutine/timer/shell 泄露；
- 并发回调不直接触碰 Sobek runtime；
- abort 能在限定时间内结束。

## 8. Cell、yield、wait 和 terminate

复用并重构现有 `codemode.SessionRuntime`：

- `Execute` 创建 cell 并启动 Sobek engine。
- 到达 `yield_time_ms` 或调用 `yield_control()` 时返回 `Yielded`。
- cell 后台继续执行并累计内容。
- `wait` 只返回上次游标后的新输出；完成后关闭 cell。
- `terminate` 中断 engine 和 nested tools，返回 terminated 状态。
- session shutdown 清理全部运行中 cell。

需补充：

- 每个 wait consumer 的输出游标；
- terminal response 只发布一次；
- cell ID 唯一且与 rollout trace 关联；
- fresh-process resume 对 running cell 的 Rust行为实测：终止、标记 aborted 或可恢复，不能自行假设。

## 9. 输出、错误和资源限制

### 输出

- 使用已有 `ContentItem`/response adapter。
- 保持 helper 调用顺序。
- 支持 text/image/audio/generated image。
- `max_output_tokens` 与 Rust 截断策略做 fixture 对比。
- nested command 事件展示真实 stdout/stderr/exit code，但外层历史只保存 `exec` call/output。

### 错误分类

至少区分：

- JS syntax error
- JS runtime exception
- invalid helper argument
- nested tool business failure
- nested tool infrastructure failure
- timeout/cancel/terminate
- output or memory limit
- engine internal failure

错误文本不要求字节级完全相同，但 SDK terminal lifecycle、是否可继续、是否进入模型历史必须与 Rust一致。

### 限制

首版建议配置项：

- 最大源码字节数
- 单 cell 最大 wall time
- 最大同步执行时间/interrupt deadline
- 最大 pending Promise/tool/timer 数
- 最大内容项数和总输出 token
- session store 总字节数

Sobek 无法提供严格硬内存沙箱时，应通过输入/对象/输出限制、超时和进程级监控降低风险，并在文档中明确与 V8 isolate 的差异。

## 10. 持久化和协议对齐

模型可见历史必须保持：

```text
custom_tool_call(id=ctc_..., name=exec, input=<source>)
custom_tool_call_output(id=ctco_..., call_id=..., output=<content items>)
```

要求：

- 保留服务端 call item 原始 ID。
- output 缺失 ID 时分配 `ctco_` UUIDv7。
- call/output 成对且顺序稳定。
- 不把内部 nested tool call 重放成新的顶层 Responses input。
- nested tool 只映射为 SDK/TUI observable items 和 telemetry。
- resume 不重放已完成工具，不重复 agent message，不依赖失效的 `previous_response_id`。
- interrupted/orphan pair 归一化与 Rust `ConversationHistory` 一致。

## 11. 分阶段开发计划

### Phase 0：冻结基线和依赖

- 记录 Rust/Go commit、binary hash、SDK commit 和 Sobek version。
- 评估 Sobek license、Go version、Windows/Linux 构建兼容性。
- 使用固定版本加入 `go.mod/go.sum`，禁止浮动 main/master。
- 保存当前通过 artifact：streaming smoke、structured read、two-command resume。

完成标准：依赖可在 Linux/Windows 构建，现有非 code-mode smoke 不回归。

### Phase 1：同步 JS 与 helpers

- 接入 Sobek engine abstraction。
- 支持普通 JS、`text`、`exit`、syntax/runtime error。
- 替换 `renderLocalExecution`，删除模拟解析路径。

测试：表达式、对象序列化、多个 text 顺序、异常、取消、超时。

### Phase 2：单个通用 nested tool Promise

- 动态注入 `tools.*`。
- 支持 function/freeform 两种调用。
- 完成单个 `await tools.exec_command()` 和 `apply_patch`。
- 确保 Router、sandbox、approval、hooks 均生效。

测试：命令成功/失败、patch 成功/失败、参数错误、权限拒绝。

### Phase 3：控制流与并行

- `try/catch/finally`。
- `Promise.all` 和多工具并行。
- 不可并行工具串行化。
- 工具失败后继续执行。

测试：mixed failures、parallel partial failure、恢复命令、无重复执行。

### Phase 4：结构化内容与 MCP

- image/audio/generatedImage。
- MCP structured content、namespaced tools 和 output schema。
- `ALL_TOOLS`、deferred tool discovery。

测试：MCP success/failure、图片转发、tool search 后调用、resume。

### Phase 5：timer、yield、wait、terminate

- event loop timers。
- yield_control、cell incremental output。
- wait/terminate/abort/session shutdown。

测试：长任务、连续 wait、超时、SDK AbortSignal、无泄漏。

### Phase 6：持久化、resume 和全表面集成

- rollout trace 和 item ID 完整对齐。
- fresh-process resume、中断历史、长输出恢复。
- TUI/VS Code 消息顺序和重复检测。
- Linux/Windows 分离脚本和 artifacts。

完成标准：核心 live parity 矩阵通过，已知差异有明确分类与 fixture。

## 12. 测试策略

### 12.1 Sobek 单元测试

- runtime global 白名单/黑名单。
- JS 基础语义和 exception。
- Promise resolve/reject/all。
- timer/cancel/interrupt。
- helper 参数、输出顺序、序列化与限制。
- tools function/freeform/name normalization。
- race、double resolve、shutdown 后 callback。

建议运行：

```sh
go test ./codemode ./tool ./turn ./exec ./session ./model -count=1
go test -race ./codemode ./tool ./turn -count=1
```

### 12.2 离线协议 fixture

建立 Rust rollout/Responses fixture，覆盖：

- `function_call/output`
- `custom_tool_call/output`
- `tool_search_call/output`
- interrupted/orphan tools
- IDs：`fc_`、`fco_`、`ctc_`、`ctco_`、`tsc_`、`tso_`
- code mode nested command/file/MCP 事件映射
- output content items 和 truncation

fixture 不依赖模型，必须可在 CI 稳定重放。

### 12.3 SDK live parity 场景

按顺序执行，前一层通过后再扩大：

1. `streaming-smoke`
2. 同步 `text()`
3. 单命令 code mode
4. 双命令 fresh-process resume
5. `workspace-structured-read`
6. mixed tool failures recovery
7. parallel partial failure recovery
8. apply_patch success/failure
9. long output/truncation
10. tool search + resume
11. MCP + structured content
12. yield/wait/terminate
13. abort/interrupted history
14. VS Code/TUI 消息顺序与去重

每个场景同时比较：

- 进程与 SDK terminal lifecycle
- event 类型与因果顺序
- completed tool semantics
- item ID 引用完整性
- thread ID continuity
- rollout history
- workspace side effects
- 无工具重放、无重复消息

### 12.4 平台测试

测试脚本继续按平台分离：

```text
sdktests/src/platform/linux.ts
sdktests/src/platform/windows.ts
```

Linux 和 Windows 使用相同语义断言，但命令、shell quoting、退出码文本和 sandbox helper 分平台定义。

## 13. CI 与质量门槛

每个阶段至少通过：

- `gofmt`、`go vet`（适用包）和 `git diff --check`
- 相关 Go 单测
- Sobek runtime race tests
- SDK streaming smoke
- 当前双命令 resume 阻断回归
- 新增阶段对应的 Rust/Go live parity

全量 `go test ./...` 的环境依赖失败必须单独列出，不得用宽泛 skip 掩盖。当前已知环境项包括 Linux sandbox helper、网络代理测试和脏工作区 inventory；应分别修复测试环境或明确标记，不混入 code mode parity 判定。

## 14. 安全评审清单

- 无 Node/OS API 暴露。
- 工具桥接必须使用现有 Router/Dispatcher。
- nested tool 继承 sandbox、approval、network、hooks。
- source、output、store、Promise、timer 有上限。
- context cancel 可中断 JS 和工具。
- runtime 结束后无 callback/goroutine/timer 泄漏。
- exception/error 不包含 secret、auth 或完整用户配置。
- diagnostics 默认关闭且不记录敏感 request body。
- Windows/Linux 构建不引入 CGO 依赖；Sobek 固定版本并审查供应链。

## 15. 完成定义

只有满足以下条件才标记完成：

- Go 使用 Sobek 实际执行 JavaScript，不再使用正则/模拟执行器作为默认路径。
- Rust 与 Go 暴露相同的 `exec`/`wait` 基本 schema 和工具命名。
- `await tools.*`、try/catch、Promise.all、helpers、yield/wait/terminate 可用。
- nested tools 不绕过权限并产生 Rust 对齐事件。
- rollout call/output、ID、resume 和中断历史引用完整。
- 双轮 resume、mixed failure、parallel failure、long output、tool search、MCP 场景通过。
- TUI/VS Code 无重复消息、顺序稳定。
- Linux/Windows 对应脚本和核心矩阵通过。
- 未解决差异均有最小 fixture、分类和后续 issue，不以 retry、normalization 或禁用事件掩盖。

## 16. 首个实施批次

建议第一个提交只完成以下内容：

1. 固定 Sobek 依赖并增加 engine abstraction。
2. 实现同步 JS、`text()`、`exit()`、exception 和 context interrupt。
3. 用 Sobek 替换 `renderLocalExecution`，但暂不开放 `tools.*`。
4. 增加单元测试和一个无工具 SDK live parity 场景。
5. 保持当前正则工具路径在显式兼容开关下短期存在，Sobek 稳定后删除。

第二个提交再接通单个 `tools.exec_command` Promise，避免一次修改同时跨越引擎、工具路由、事件和持久化四个边界。
