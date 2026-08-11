---
name: code
description: 在本仓库（codex_go）编写、评审或重构 Go 代码时的编码规范指导。使用场景包括：新增或修改 .go 文件、编写单元测试（go test）、代码评审打回意见、排查 Go 编译/静态检查问题。触发时需确保代码符合 gofmt 与官方/权威 Go 风格（命名、错误处理、注释、接口、并发、测试等），并遵循本仓库多模块结构约定。
---

# Go 编码规范指导

## 使用流程

1. 确定待写/待改代码所属模块，在对应模块目录（含其 `go.mod` 的目录）执行 Go 命令。
2. 编写或修改代码时，遵循下方「核心规范速查」；遇到争议或不熟悉的场景，按「规范文档索引」查阅权威原文。
3. 完成前必须执行验证：`gofmt`、`go vet`、`go test ./...`（见「验证与工具链」）。

## 规范文档索引

权威资料已收录于 `docs/`，正文为英文原文，**争议时以这些文档为准**。索引与查阅建议见 [docs/README.md](docs/README.md)。

| 场景 | 查阅文档 |
| --- | --- |
| 新手学习 Go 惯用法 | `docs/01_official/effective_go.md` |
| 代码评审打回意见 | `docs/01_official/code_review_comments.md` + `test_comments.md` |
| 风格争议裁决 | `docs/02_google/style_decisions.md` → `style_guide.md` |
| 写测试 | `docs/01_official/test_comments.md` + `docs/02_google/best_practices.md`（Testing 章节） |
| 错误处理/并发/性能 | `docs/03_uber/style.md` + `docs/02_google/best_practices.md` |
| 语法语义权威定义 | `docs/01_official/go_spec.md` |

## 核心规范速查

### 关于编程的哲学方法指导

- Go 语言的设计哲学是"简单、清晰、可读、可维护"。在编写代码时，优先考虑可读性和可维护性，而不是过度优化或追求复杂的抽象。遵循官方和权威指南，保持一致的编码风格，有助于团队协作和代码质量。
- 区分变化点和不变点：将可能变化的部分抽象为接口或函数参数，而将不变的部分保持简单和直接。避免过度设计，优先使用简单的结构和函数。
- 代码应自解释：通过清晰的命名、结构和注释，使代码的意图和功能易于理解。避免使用晦涩的缩写或复杂的逻辑，确保代码对其他开发者友好。
- 避免过度优化：在没有明确性能问题的情况下，优先编写清晰和可维护的代码。性能优化应基于实际的性能分析和需求，而不是预先假设的瓶颈。
- 使用标准库和社区认可的库：优先使用 Go 标准库和社区认可的第三方库，避免重复造轮子。确保所使用的库是可靠、维护良好且符合项目需求的。
- 测试驱动开发（TDD）：在编写功能代码之前，先编写测试用例，以确保代码的正确性和可维护性。测试应覆盖各种边界情况和异常情况，确保代码在不同场景下的稳定性。
- 关于抽象：在设计抽象时，确保其必要性和合理性。过度抽象可能导致代码复杂化和难以理解。优先使用简单的函数和结构，只有在确实需要时才引入接口或抽象层。
- 关于接口和范型：接口应定义明确的行为契约，避免过度泛化。使用接口时，确保其用途清晰，并且不会引入不必要的复杂性。范型（Generics）应在确实需要时使用，避免滥用导致代码难以理解。

- 关于生命周期：在设计和实现代码时，明确对象和资源的生命周期，确保资源的正确释放和管理。避免内存泄漏和资源滥用，使用 `defer` 和上下文（`context.Context`）来管理资源的释放和取消操作。

- 关于重复：避免重复代码，通过函数、方法或模块化设计来实现代码复用。遵循 DRY（Don't Repeat Yourself）原则，确保代码的可维护性和可扩展性。将重复代码提取为公共函数或模块，确保代码的一致性和可读性。当在各个模块中需要相同的功能时，优先在 `common/` 中实现通用工具，而不是在业务代码中重复实现。


### 格式

- 所有代码必须通过 `gofmt`（`go fmt` 按包执行）；不要手工对齐，交给 gofmt。
- 推荐 `goimports`：额外负责 import 分组（标准库 / 第三方 / 本仓库）与排序。
- 单文件行数无硬性规定，优先可读性。

### 命名

- 使用 MixedCaps（`mixedCaps`），**不要**使用下划线分隔（`mixed_caps`），常量同样适用。
- 缩写保持全大写：`HTTP`、`URL`、`ID`，如 `ServeHTTP`、`userID`；全小写全大写的 `XmlHttpRequest` 类写法是错的。
- 标识符越短越好，作用域越小越短：`i`、`s`、`err`；不需要 `theThing` 式冗余。
- 包名小写、无下划线、无复数，使用简短名词：`httputil`、`template`。
- 导出标识符以大写开头，且**必须有以该标识符开头的注释**（doc comment）；未导出的可省略。
- Receiver 名 1~2 个字母，全文件一致：`func (c *Client)`，不要 `this`/`self`。
- 错误值变量名约定 `err`；需要区分时用更具体的 `ctx`、`req` 等。
- 测试函数命名 `TestXxx`（`Xxx` 首字母大写）；表驱动测试的测试用例名可读、可定位。

### 注释

- 注释用完整句子，以被描述对象的名字开头、句号结尾：
  `// Request 表示一次 HTTP 请求。`
- 导出符号的 doc comment 用其名称开头（Godoc 约定），如 `// ComputeHash 返回输入的哈希值。`
- 用注释解释"为什么"，不是复述"是什么"；代码本身应能说明"是什么"。
- `TODO`/`FIXME` 注明负责人与问题背景（可选）。

### 错误处理

- 错误字符串**小写开头**（会被包装进更大的错误信息），不带句号结尾。
- 直接返回错误，不要 `log.Fatal`/`panic` 吞掉错误；库代码绝不 panic。
- 判断错误用 `errors.Is` / `errors.As`，不要用 `==` 比较错误值（除非是哨兵错误且明确）。
- 包装错误用 `fmt.Errorf("...: %w", err)` 保留链路；避免双层重复包装。
- 正常路径保持最少缩进（indent error flow）：先处理错误再继续，避免大段 `if err == nil` 嵌套。
- 每个函数返回的 error 都要处理或显式忽略（`_ = f()`）；不要吞错。

### 声明与初始化

- 零值即可用（`var s []string`、`var m map[string]int`、`sync.Mutex` 零值）；nil slice 可安全 `append`。
- 只在需要与 nil 区分或字段可缺省时用指针；结构体字段、slice/map 优先值或引用类型本身。
- 声明局部变量用 `:=`，包级/需要明确零值时用 `var`。
- 字面量优先完整写法：`[]string{}` 而非 `make([]string, 0)`；`map[string]int{}` 同理。
- 不需要用 `make` 预分配时不要预分配；已知容量时 `make([]T, 0, n)` 可避免扩容。

### 函数与方法

- 接收者：值接收者用于不可变/小类型；指针接收者用于需要修改或大结构体；同一类型全文件统一。
- 命名返回参数：仅当返回参数语义需要文档化或 defer 中需要修改时使用；否则裸 return 会增加误解，避免。
- 尽早 `return`，减少嵌套；`defer` 用于资源释放（锁、文件、连接），defer 中不要 return 值。
- 接口参数用最小接口（`interface{ String() string }`），返回具体类型而非接口（"接受接口，返回结构体"）。
- 接口合规性用编译期断言验证：`var _ Interface = (*Impl)(nil)`。
- 所有会阻塞/耗时的函数第一个参数传 `context.Context`，命名 `ctx`。

### 并发

- goroutine 要有明确生命周期：知道它何时退出、由谁负责退出；避免无界泄漏。
- 通过 channel 通信而非共享内存；channel 方向尽量标注（`<-chan` / `chan<-`）。
- 使用 `sync.WaitGroup`、`errgroup` 或 context 取消来协调并发；`go` 语句内不要直接操作外层变量而不加同步。
- 用 `select` + `ctx.Done()` 处理可取消操作；mutex 覆盖范围尽量小。

### 测试

- 优先表驱动测试（table-driven tests）：输入/期望/名称列在表里，循环执行。
- 断言失败信息必须包含输入与期望/实际值，便于定位（"useful test failures"）。
- 辅助函数用 `t.Helper()` 标记，错误信息显示调用点而非辅助函数内部。
- 用 `t.Errorf`（不中断）收集多个失败；无法继续时用 `t.Fatalf`。
- 不引入重量级 assert 框架；标准库 `testing` 足够（Uber/Google 均反对自造断言 DSL）。
- 需要 mock 时优先小接口 + 手写 fake，避免过度 mock 框架。

### 性能（仅在有依据时优化）

- 不要过早优化；先写清晰代码，用 benchmark/profile 定位热点。
- 避免无谓分配：字符串拼接用 `strings.Builder`，`fmt.Sprintf` 慎用于热路径。
- 大结构体/大切片跨函数传递时考虑指针或切片引用，避免复制。

## 本项目约定（recopilot_agent_cluster）

- **多模块结构**：仓库根有 `go.work`（use 根 + `api/`、`common/`、`recopilot_agent_master/`、`recopilot_agent_node/`、`web_backend/`），workspace 模式统一解析依赖。执行 Go 命令前先 `Set-Location` 到对应模块目录；根目录 `go build ./...` 只覆盖根模块（空壳），要全量构建需在各自模块目录执行。
- **Node 模块特例**：`recopilot_agent_node/` 内置 vendor（`codex-sdk-go` 为本地定制补丁，含上游没有的 `Capabilities` 等字段），必须 `GOWORK=off` + `-mod=vendor` 构建/测试，否则 workspace 模式会解析到上游版本导致编译失败或 vendor 不一致报错。
- **Go 版本**：go 1.26.5（各 `go.mod` 均声明 `go 1.26.5`）。
- **目录职责**：
  - `api/proto/`：gRPC 协议定义（`model/`、`route/` 下按领域分包）。
  - `api/client/`：gRPC 客户端薄封装（`agent/`、`control/`、`message/`、`node/`），方法命名与生成的 service 一致，正确传递 `context.Context`。
  - `api/route/`：路由/领域 handler，负责传输层转换，不把 proto 细节泄漏到上层。
  - `web_backend/`：Go Iris 后端（独立模块），handler 使用请求上下文、结构化 JSON 响应与一致的 HTTP 状态码。
  - `common/`：通用工具包，`m*` 前缀（`mlog`、`mjson`、`mhttp`、`mgrpc` 等），新增通用能力优先复用或扩展此处，不要重复造轮子。
- 新增 gRPC 能力时先改 proto → 运行仓库既有生成流程（`gen_grpc.ps1`/`gen_grpc`）→ 在 `api/client` 增加薄封装。
- 错误处理遵循"可诊断映射"：gRPC 错误做可诊断映射并记录上下文；不要向浏览器返回内部堆栈、连接地址或凭据。
- 已有 `common/m*` 工具可满足需求时，禁止在业务代码里另写自实现版本。

## 验证与工具链

```powershell
# 在对应模块目录执行（例如 Set-Location web_backend）
gofmt -l .            # 应无输出（列出未格式化文件）
go vet ./...          # 官方静态检查
go test ./...         # 模块内全部测试
```

- 若 `gofmt -l` 有输出，先运行 `gofmt -w` 或 `go fmt ./...` 修正。
- `staticcheck`（dominikh/go-tools）为可选增强检查，通过即通过，不强制。
- 无法执行的验证必须在交付时明确说明原因。

## 完成标准

- 新代码/改动通过 `gofmt`，`go vet` 无告警，对应模块 `go test ./...` 全部通过。
- 命名、注释、错误处理符合本指南与 docs 权威文档；无 panic 吞错、无裸 goroutine 泄漏。
- 涉及 proto/gRPC 的改动与生成代码、`api/client` 封装保持一致。


