# Codex Go 迁移设计

## 目标

将 `D:\qax\reagent\dev\codex-main\codex-rs` 中的 Rust 版 Codex CLI 逐步迁移为 Go 实现，优先保持命令行行为、配置语义、认证存储、非交互执行、review、TUI、app-server、MCP、插件和沙箱等能力的兼容性。

当前仓库先落地一个可测试的 Go 迁移骨架。它覆盖命令解析、配置覆盖、功能开关、认证文件、`exec` 本地闭环和 `review` git diff 上下文，后续再替换真实 agent/model/runtime/TUI 细节。

## Rust 版结构摘要

当前本机可见的 Rust 源位于 `/home/jacks/jacks_dev/codex-main/codex-rs`。这个 checkout 只包含 `app-server`、`app-server-client`、`app-server-protocol`、`app-server-daemon`、`analytics`、`agent-graph-store`、`agent-identity`、`ansi-escape` 等 crate；`codex-core`、`codex-config`、`codex-login`、`codex-protocol`、`codex-exec-server` 等核心 crate 是 workspace 依赖，但源码不在当前目录。后续迁移时，可见 Rust crate 直接逐文件对照；不可见核心 crate 以可见调用点和测试语义为准，不能凭旧文档臆造行为。

历史 Rust CLI 入口位于 `codex-rs/cli/src/main.rs`，核心职责是：

- 用 `clap` 定义 `codex [OPTIONS] [PROMPT]` 和所有子命令。
- 将 `--enable/--disable` 转换为 `features.<name>=true/false` 配置覆盖。
- 将根级 `-c/--config` 覆盖按优先级传给子命令。
- 分发到 `codex_exec::run_main`、`codex_tui::run_main`、`codex_mcp_server::run_main`、`codex_app_server::run_main_with_transport_options`、登录/登出、插件、MCP、云任务、沙箱、completion、doctor 等模块。

主要迁移边界：

- `cli`: 命令树、根参数、子命令分发。
- `utils/cli`: 共享参数、sandbox/approval 枚举、`-c key=value` 覆盖解析。
- `config`: `config.toml`、profile、权限、模型、MCP、插件、TUI、features 等配置模型。
- `features`: 功能开关注册表、默认值、stage、legacy key。
- `login`: `auth.json`、环境变量、ChatGPT OAuth、API key、access token、keyring。
- `exec`: 非交互 agent 运行、JSONL 输出、review、resume、输出 schema、last message。
- `tui`: 交互 UI、session resume/fork/archive/delete、状态栏、审批弹窗、模型选择、插件/skills/usage 等界面。
- `app-server` 与协议 crate: IDE/桌面端使用的 RPC 服务、线程生命周期、配置管理、执行处理器。
- `mcp-server`、`codex-mcp`、`plugin`: MCP server/client、插件 manifest、marketplace。
- `sandboxing`、`exec-server`、`windows-sandbox-rs`、`linux-sandbox`: 命令执行和平台沙箱。

## Rust/Go 模块对照

当前 Go 包与本机可见 Rust crate/source 的对照顺序如下，编码时先补模块内部细节，最后再接入口集成：

| Go 模块 | Rust 对照 | 当前迁移策略 |
| --- | --- | --- |
| `internal/config` | `codex_config` 调用点、`app-server/src/config_manager*.rs`、`app-server/tests/suite/v2/config_rpc.rs` | 先实现配置层、profile 文件、legacy profile 写入限制、origin/layer 元数据。 |
| `internal/appserver`、`internal/codemodeprotocol` | `app-server/src/*`、`app-server-protocol/src/*`、schema fixtures | 先补协议类型、router、request processor 的纯数据逻辑，再接服务运行时。 |
| `internal/agent` | `agent-graph-store/src/*`、`agent-identity/src/*` | 保持 graph/store/identity 分包，状态、角色、registry 先独立对齐。 |
| `internal/analytics` | `analytics/src/*` | 先补事件/facts/reducer 的数据归类，真实上报后置。 |
| `internal/ansiescape` | `ansi-escape/src/*` | 保持纯解析/清洗工具模块。 |
| `internal/auth`、`internal/loginflow`、`internal/keyring` | `codex_login` 调用点、app-server auth tests | 以可见测试语义补 auth.json、环境变量、keyring 抽象，OAuth 运行流后置。 |
| `internal/model`、`internal/codexapi`、`internal/chatgptapi` | `codex-model-provider`、`codex-chatgpt` 调用点 | 先补 provider/catalog/request/response 数据结构，再替换真实 HTTP runner。 |
| `internal/session`、`internal/rollout`、`internal/history` | `codex-thread-store`、`codex-rollout` 调用点 | 先对齐本地 record/rollout schema 和读写规则。 |
| `internal/sandbox`、`internal/unifiedexec`、`internal/execenv` | `codex-sandboxing`、`codex-exec-server` 调用点 | 先补权限/profile/policy 类型和非平台逻辑，再接平台执行。 |
| `internal/cli`、`internal/app`、`internal/exec`、`cmd/codex` | CLI/app-server client 调用面 | 作为最后集成层，只消费上述模块能力，不在入口处复制业务合并逻辑。 |

## Go 模块规划

已经建立的目录：

- `cmd/codex`: 可执行入口。
- `internal/cli`: Rust 命令面的兼容解析层。
- `internal/config`: `-c key=value` 覆盖解析、`config.toml` 小步读取、features 写入。
- `internal/features`: Rust feature registry 的 Go 映射。
- `internal/auth`: `CODEX_HOME/auth.json` 文件存储和基础状态判断。
- `internal/prompt`: prompt/stdin 解析，供 exec/review 复用。
- `internal/protocol`: exec JSONL/thread event 类型。
- `internal/exec`: 非交互执行、JSONL 事件、schema 校验、last-message 文件、本地 stub agent。
- `internal/review`: review target 解析、git diff 采集、review prompt 拼装。
- `internal/app`: 顶层分发器。

后续扩展目录：

- `internal/tui`: 交互式界面。
- `internal/appserver`: JSON-RPC/app-server 兼容实现。
- `internal/mcp`: MCP 配置、client/server、OAuth。
- `internal/plugin`: 插件 manifest、安装、marketplace。
- `internal/sandbox`: read-only/workspace-write/full-access、Windows restricted token、Linux/macOS backend。
- `internal/model`: OpenAI/OSS provider、streaming、模型 catalog。

## Go 库选择

第一阶段使用标准库，避免在当前受限环境下拉依赖，保证测试可以立即跑通。

后续建议引入：

- CLI: `spf13/cobra`，用于更完整的 nested subcommand、global flag、completion、alias。
- TUI: `charmbracelet/bubbletea` + `bubbles` + `lipgloss`。
- TOML: `github.com/pelletier/go-toml/v2`，用于完整 TOML v1.x 和 strict decode。
- PTY: `github.com/creack/pty`，对齐 Rust `portable-pty`/unified exec。
- SQLite: `modernc.org/sqlite` 或 `github.com/mattn/go-sqlite3`。
- JSON-RPC/WebSocket: 标准库 `net/http` + `gorilla/websocket` 或 `nhooyr.io/websocket`。
- Keyring: `zalando/go-keyring` 或平台原生封装。

## 当前实现边界

已完成：

- Go module 和 `cmd/codex` 入口。
- Rust 命令面的主要子命令识别。
- 根级和 exec 级共享参数解析。
- `-c key=value` 覆盖解析，支持 bool、number、string、array、inline table，并能写入嵌套 map。
- Rust feature key 注册表和 `features list/enable/disable` 基础行为。
- 配置有效值加载：读取 `$CODEX_HOME/config.toml`，应用 `-c key=value`、`--enable`、`--disable`。
- `auth.json` 文件存储，支持 API key/access token 登录状态。
- 环境变量认证识别：`OPENAI_API_KEY`、`CODEX_API_KEY`、`CODEX_ACCESS_TOKEN` 优先于 `auth.json`。
- 根级 `--strict-config` / `--remote` 可用范围校验。
- `debug prompt-input` 本地 JSON 输出骨架。
- `codex exec` 最小本地闭环：prompt/stdin 解析、JSONL 事件、last-message 文件、output schema JSON 校验、本地 stub agent message。
- `codex exec` 已抽出 `internal/model.AgentRunner`，当前默认使用本地 runner，真实 Responses/OSS runner 后续可按同一接口替换。
- `codex exec` / `codex review` 会把 prompt、assistant message、model/provider 元数据持久化到 `$CODEX_HOME/sessions`，为后续 resume/fork/archive 接入同一 thread store 打基础。
- `codex review` / `codex exec review` 最小闭环：解析 `--uncommitted`、`--base`、`--commit`、`--title`、自定义 prompt/stdin，复用 exec stub。
- `review` 已能读取 git diff 上下文：
  - `--uncommitted` 合并 unstaged、staged 和 untracked 文件的 diff；untracked 文件会转成 pseudo-diff。
  - `--base BRANCH` 使用 `git diff --no-ext-diff --binary BRANCH...HEAD`。
  - `--commit SHA` 使用 `git show --no-ext-diff --binary --format=medium SHA`。
  - `-C/--cd` 会传递给 git provider。

仍未实现：

- 真实模型请求和 streaming agent loop；当前 `exec/review` 默认 runner 仍是本地实现，但已经不再硬编码在 exec 编排层。
- 完整 TOML/profile/project `.codex` 配置层。
- 真实 approval/sandbox/PTY 命令执行服务。
- TUI、app-server、MCP、插件、云任务、resume/archive/fork 等运行时能力。

未实现能力会明确返回 `not implemented`，避免伪装成完整 CLI。

## 测试策略

- 单元测试：覆盖每个 internal package 的解析、状态转换和错误路径。
- Integration tests：使用临时 `CODEX_HOME` 跑 `login/status/logout/features/exec/review`。
- Git tests：用临时 git 仓库覆盖 review 的 uncommitted、untracked、commit diff。
- Golden tests：后续补 CLI help、JSONL 事件、TUI 渲染片段、配置合并结果。
- Compatibility tests：将 Rust fixture 输入输出复制到 Go 测试，确保行为逐步对齐。
- Platform tests：Windows sandbox、PTY、path normalization 单独做平台条件测试。

当前全量验证命令：

```powershell
$env:GOCACHE = (Join-Path $env:TEMP 'codex_go_gocache'); go test ./...
```

## 下一步

建议继续沿 `exec/review` 主干推进：

1. 增加 `internal/model` provider 抽象，把当前本地 stub 替换为可插拔 agent/model 层。
2. 补 `exec --output-schema` 的响应约束语义，而不仅是 schema 文件 JSON 校验。
3. 对齐 Rust `exec` 的 resume、session/thread 存储和 JSONL 事件细节。
4. 扩展配置加载，加入 profile、project `.codex`、strict config 和更完整 TOML。
