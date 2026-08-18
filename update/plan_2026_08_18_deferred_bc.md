# Codex Go deferred-items push plan - 批次 B/C (2026-08-18 第二轮)

## 目标
继续推进剩余延迟项：不因改动面大而挂起，每项落地真实代码 + 测试 + 证据。
结构性 N/A（无产品面/无 OTLP 设施）也建立 Go 侧等价载体并明确文档。

## 批次 B（本轮）

### 1. #39060/#39067/#39074 桌面 doctor 诊断 → Go 等价 `doctor/desktop*` ✅ 已完成
- `desktop.app.version`：检测本机桌面应用安装（Windows 注册表/已知路径、macOS /Applications），
  报告版本/运行/日志目录；未安装 → OK 说明检查（Rust 未安装返回 None）。
- `desktop.security` / `desktop.updates`：注册为 doctor check，平台探测注入，
  无 ChatGPT 桌面产品面时明确报告 N/A 原因（不再无声漂移）。
- 平台文件 desktop_windows.go / desktop_darwin.go / desktop_other.go + 注入式单测。

### 2. #39098/#39078 trace context → Go 语义载体 `execserver/trace_context.go` ✅ 已完成（#39098 载体+传播；#39078 span 树为结构性 N/A）
- TraceContext{TraceID, SpanID}：解析/生成/子 span；exec-server 请求进入连接队列时
  填充并跨 dispatch/响应携带（Go 现有 request/response 链），日志与事件暴露 trace id。
- 环境解析保留：appserver 环境解析路径接受并传递 trace context（#39078 语义等价）。
- 与 Rust OTLP span 的差异（histogram/span 树）文档化为结构性差异。

### 3. #39082 远程 TUI 信任 → `app/remote_trust.go` 机制 ✅ 已完成（决策+持久化+启动挂接；git-root 解析待后续）
- 远程 config/read 解析 projects 信任决策（trusted/untrusted/undecided）；
- ensureRemoteProjectTrust：undecided 时经确认回调 → 远程 config/batchWrite 持久化
  （projects.<path>.trust_level = trusted/untrusted）；
- 接入远程 TUI 启动（thread start 前）；单测覆盖决策解析与持久化调用。

## 批次 C（结构性，随批次 B 基建落地）
- #39098/#39078 的 OTLP span 树/直方图：待引入 Go telemetry span 管线后跟进（文档）。

## 验收
- 每项 go test 目标包 + 全树 0 FAIL；commits.json 证据升级；
- 契约注册（desktop-doctor-diagnostics、exec-server-trace-context、
  remote-tui-project-trust）；parity.json 新 done 项；提交推送。
