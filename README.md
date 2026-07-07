# Codex Go

`codex_go` 是 Codex CLI 的 Go 实现，目标是逐步对齐 Rust 版 Codex CLI 的命令行行为、配置语义、认证、模型请求、TUI、app-server、MCP、插件、沙箱和会话能力。

当前仓库是迁移中的工程版本。开发时以 Rust 版本为行为基线，进度和待办以 [plan.md](./plan.md) 为准。

## 环境要求

- Go 1.24.2+
- 推荐使用 Go toolchain 1.24.3
- Windows、Linux 或 macOS

模块信息见 [go.mod](./go.mod)。

## 快速开始

构建 CLI：

```powershell
go build -o .\bin\codex.exe .\cmd\codex
```

直接运行：

```powershell
go run .\cmd\codex -- "解释当前目录的代码结构"
```

非交互执行：

```powershell
go run .\cmd\codex exec "用一句话总结这个项目"
```

登录状态：

```powershell
go run .\cmd\codex login status
```

使用 API key 登录时从 stdin 读取密钥：

```powershell
$env:OPENAI_API_KEY | go run .\cmd\codex login --with-api-key
```

## 常用命令

```text
codex [OPTIONS] [PROMPT]              启动交互式 TUI，或用 PROMPT 开始会话
codex exec [OPTIONS] [PROMPT]         非交互执行
codex review [OPTIONS]                代码审查
codex login                           登录 ChatGPT
codex login --with-api-key            从 stdin 保存 API key
codex login status                    查看登录状态
codex logout                          退出登录
codex features list                   查看功能开关
codex mcp ...                         管理 MCP server 配置和登录
codex plugin ...                      管理插件和 marketplace
codex app-server ...                  启动或管理 app-server
codex mcp-server                      以 MCP server 方式运行 Codex
codex sandbox -- COMMAND              在权限配置下运行命令
codex doctor                          环境诊断
codex completion [SHELL]              生成 shell completion
```

根级常用选项：

```text
-c, --config KEY=VALUE                覆盖配置
--enable FEATURE                      启用功能开关
--disable FEATURE                     禁用功能开关
-m, --model MODEL                     指定模型
-p, --profile PROFILE                 使用配置 profile
-C, --cd DIR                          指定工作目录
--add-dir DIR                         增加可访问目录
--sandbox PROFILE                     指定沙箱策略
--ask-for-approval POLICY             指定审批策略
```

## 测试与检查

包加载检查：

```powershell
go list -buildvcs=false ./...
```

运行全部测试：

```powershell
go test ./...
```

如果本机 Go 缓存目录受限，可以临时指定缓存：

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'codex_go_gocache'
go test ./...
```

格式化：

```powershell
go fmt ./...
```

## 目录结构

```text
cmd/codex                         可执行入口
internal/app                      顶层命令分发和运行时拼装
internal/cli                      命令行解析、参数校验、dispatch alias
internal/config                   配置加载、profile、feature override、权限配置
internal/auth                     登录凭据、auth.json、OAuth/API key/access token
internal/model                    模型 provider、Responses 请求/流式事件、模型 catalog
internal/codexapi                 OpenAI/Codex API 客户端辅助
internal/turn                     agent loop、tool dispatch、turn runtime
internal/tool                     shell/apply_patch/MCP/tool_search/agent 工具运行时
internal/exec                     codex exec/review 的非交互运行
internal/appserver                JSON-RPC app-server、thread/turn/runtime 服务
internal/mcp                      MCP client/server、OAuth、resource、tool runtime
internal/tui                      终端 UI 状态、渲染、组件和 Bubble Tea adapter
internal/session                  会话存储、resume/fork/archive/delete
internal/rollout                  rollout JSONL 记录和恢复
internal/sandbox                  权限 profile、Linux/Windows 沙箱、执行计划
internal/review                   review 目标解析和 git diff 采集
internal/doctor                   环境诊断
internal/plugin                   插件 manifest、marketplace、安装流程
internal/agent                    多 agent graph、身份、registry、工具
internal/utils                    路径、JSON、ANSI、LRU、截断等共享工具
docs                              设计和技术选型文档
```

## 开发约定

- 以 Rust 版 Codex 的行为和协议形状为基线。
- 新功能优先放入已有功能包，避免无必要地新增目录。
- 结构体参数和结构体返回值默认优先使用指针，简单标量按 Go 惯例传值。
- 代码变更后至少运行相关包测试；模块收口时运行 `go list -buildvcs=false ./...` 和 `go test ./...`。
- 迁移进度、验收项和工作日志维护在 [plan.md](./plan.md)。

## 参考文档

- [plan.md](./plan.md)：迁移进度和待办
- [desgn.md](./desgn.md)：迁移设计草案
- [docs/tui_tech_selection.md](./docs/tui_tech_selection.md)：TUI 技术选型
