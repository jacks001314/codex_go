# Codex Go deferred-items push plan - 2026-08-18 (sync20 follow-up)

## 目标

不再以"改动面大"为由无限期延迟上游大项。按依赖与价值分批推进，每批落地代码 + 测试 +
证据，逐步把 commits.json 中的 `N/A (deferred)` 升级为可验证的 complete 实现。

## 分批计划

### 批次 A（本日）：不依赖其他大项的独立项
1. **#39043 托管认证后端强制**（config 管线） ✅ 已完成
   - ConfigRequirements 增加 cli_auth_credentials_store / chatgpt_base_url exact 需求；
   - LoadWithOptions 应用托管覆盖（运行时 + 引导认证配置）；
   - configRequirements/read 暴露；config write API 拒绝修改；
   - cloud-managed 层忽略这两个本地需求。
2. **#39092 `codex queue --thread --message`**（cli + app/session） ✅ 已完成
   - CLI 解析（--thread/--message/--remote/配置覆盖）；拒绝图片附件；
   - 会话解析：UUID 或精确名称、歧义拒绝（SessionNameMatch::Unique）；
   - 本地 app-server 路由（探测 daemon，拒绝嵌入式+daemon 并存）与 --remote 路由；
   - 服务器不支持 thread/queue/add 时给出明确升级提示。
3. **#39114 `codex agents` 命令骨架**（cli + app） ✅ 已完成（文本概览；交互式仪表盘待后续）
   - CLI 解析（--remote、拒绝会话级覆盖）；
   - 本地后台 app-server 启动 / --remote 连接 + 活动会话文本概览；
   - 交互式 agents 仪表盘（#39094/#39112）作为后续批次的 UI 增量。

### 批次 B（下一轮）：依赖批次的实现
4. **#39034 跨进程队列派发**：先评估 Go 队列架构（线程 metadata 内嵌 JSON vs Rust 独立
   SQLite 表）；若差异为结构性则沉淀"加载/恢复时派发"行为补齐 + 文档；若可做 revision
   跟踪则加迁移。
5. **#39082 远程 TUI 信任提示**：远程 config 层查询 + config/batchWrite 持久化。
6. **#39060/#39067/#39074 桌面 doctor 诊断**：Go 无桌面产品面，评估 doctor 最小等价
   （OSS app 安装/更新状态检查）或维持文档 N/A。

### 批次 C：结构性 N/A（无基础设施，维持文档）
7. **#39098/#39078 trace context**：Go 无 OTLP span 设施；若引入 telemetry span 管线再做。

## 验收
- 每项：`go test` 目标包 + 全树 0 FAIL；parity/commits.json 状态升级为 complete
  并附实现证据；契约（如有）注册 verifier。
- 本日交付：批次 A 四项（#39043/#39092/#39114/#39034 行为部分）+ 计划记录。
