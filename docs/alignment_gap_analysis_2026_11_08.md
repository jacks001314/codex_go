# Go 版 Codex 相对 Rust 版的缺口审计（2026-11-08）

## 结论

1. **日常同步的缺口是"量"的缺口，不是"类"的缺口**：以 Go 已覆盖的最后一个 Rust 提交 `55897425e8`（2026-11-06）为界，上游 `main` 已经走到 `748ab55ca7`（2026-11-08），中间 **93 个提交尚未审计落地**。其中约 10 条属于发布/CI/测试专用，真正需要 Go 侧判断或实现的是 80 条上下。
2. **结构性硬缺口只剩 5 个，且 4 个是平台原生绑定**：Windows 原生 MXC 沙箱、macOS Secure Enclave 用户验证、`agent_navigation` 关闭代理后的目录切换语义、Guardian V3 证据链；第 5 个 Cloud 服务端所有权已经落地了大半，账本状态需要重审。
3. **控制面本身也开始漂**：`parity/baseline.json` 的锚点停在 2026-10-31 的 `67f0994b`、计数与 `parity/commits.json` 差 4 条；而工程轮次已经跑到 `55897425e8`（#59832）。账本、基线、实做三者不同步，会让"还差什么"这个问题本身答不准。这属于必须先修的基础设施缺口。

## 度量口径

本报告只使用仓库内可复核的事实源，加上一次对上游 `openai/codex` 的只读比对：

| 事实源 | 内容 | 当前读数 |
| --- | --- | --- |
| `parity/commits.json` | 逐提交审计台账 | 3584 条：2955 complete / 619 not_applicable / 6 partial / 4 blocked_precondition |
| `parity/domains.json` | 26 个行为域 | 22 complete / 4 partial |
| `parity/contracts/manifest.json` | Rust 协议/数据 oracle 映射 | 81 个契约，全部 complete |
| `parity.json` | SDK 与实现证据清单 | 232 项：225 done / 3 partial / 4 intentional_difference |
| `parity/baseline.json` | 认证基线 | `certificationReady: false`，锚点 `67f0994b`（2026-10-31） |
| GitHub compare | 上游实时 delta | `67f0994b..main` = 368 个提交；`55897425e8..main` = 93 个提交 |

没有在本环境重跑 `go test ./...` 与 `CODEX_RUST_ROOT` 快照门禁：完整 Go 树未取回，Rust 兄弟 checkout（`/home/jacks/jacks_dev/codex`）是 2026-10-30 的浅克隆，缺少 `67f0994b`。下面的代码级结论都做了定点核对，验证类结论以仓库台账为准。

## 一、上游 delta：93 个未审计提交

按主题粗分（含交叉归类）：

| 主题 | 条数 | 典型内容 |
| --- | --- | --- |
| Guardian 决策与证据链 | 11 | 隐式 API key 校验、按线程目录解析策略、凭据源绑定、只读证据检索工具、订阅工具、模型驱动频道选择、原始证据按源顺序投递 |
| UI tree / 扩展渲染 | 11 | 语义配色、主题感知、行内文本归一化、多行布局与折行、状态行多 footer 行、直接渲染到终端缓冲 |
| 发布 / CI / 测试专用 | 10 | 预发布清理、Bazel action 与分片、Opus 头文件、依赖升级、纯测试用例 |
| 模型 / Provider | 8 | 切模型重算审批 reviewer、受保护模型降级走权限 profile、线程目录模型元数据、子代理保留模型目录、非交互自定义 provider |
| Code Mode / 增量工具 | 7 | gRPC 与输出截断默认开启、增量 code-mode 工具定义、exec 目录增量更新与指标 |
| 线程 / 存储 | 6 | 线程写锁争用、SQLite 写放大、turn 权限归一化为必需 profile、rollout trace 回放缓冲与错误保真 |
| 运行时鉴权 | 5 | realtime、remote-control、Analytics、git attribution 请求接入 runtime auth，终端拒绝不再重试 |
| MCP | 5 | 缓存 stdio server 懒启动、启动错误上抛、网络策略推导与已保存拒绝、刷新时保留调用上下文 |
| 侧栏 / 侧聊 | 4 | tab 悬停与键盘鼠标导航、本地工作区侧聊持久化与关闭、侧聊策略进入首个模型请求 |
| 上下文预算 | 3 | elastic 预算下的 REPL provenance、问答记录、异步 host service-tier 读取 |
| 安全 / Windows | 3 | spawn 日志脱敏、非交互必须显式项目信任、Windows 沙箱文件系统覆盖解析集中化 |
| Cloud | 2 | 云线程绑定本地 GitHub 项目、Cloud 图片消息与草稿交接 |
| 其它 | 6 | 账号 user id、安全提醒、技能显示名、typed prompt Markdown、剪贴板与 composer 重构等 |

明细见附录 A。这里有两个判断：

- **Guardian 是当前最密集的方向**，且这一批几乎全是"证据链"能力（检索工具、订阅、投递顺序、凭据绑定）。Go 侧 `ext/guardian-v3` 至今整体判为 N/A（`update/plan_2026_11_06.md` 中 3 条），差距会随着这类提交继续变大。
- **UI tree / 扩展渲染这一组（11 条）指向 Rust 的扩展 UI 树表面**。Go 没有 `codex_extension_api` / `codex_tools` 适配层，这 11 条大概率仍是结构性 N/A，但需要在账本里显式判定，否则会一直以"未审计"形式挂在 delta 上。

## 二、结构性硬缺口

### 2.1 Windows 原生 MXC 沙箱（`#42841` / `#55378`）

`sandbox/mxc` 只有策略、请求编码和启动器协议，没有原生绑定：`run_windows.go` 校验完请求后直接返回 `native MXC is unavailable on this executor`（`NativeUnavailableMessage`），exec-server 的 `windowsMxc` 能力固定为 false。Rust 侧已经接到 Microsoft MXC 的 `SandboxBackend` / `BaseContainerRunner` / `SecurityEnvironmentApi`。

影响：Windows 上走 MXC 的受控执行路径不可用（fail-closed，安全上没退化，功能上缺失）。补齐需要 cgo/COM 绑定与真实 Windows 执行环境验证，是本仓工作量最大的一项。

### 2.2 macOS 用户验证（`#43624`）

`appserver/user_verification_runtime.go` 明确只提供 `UnsupportedUserVerificationProvider`，所有操作返回 `providerUnavailable`；Rust 的 `platform_macos` 用 P-256 Secure Enclave 密钥 + LAContext 生物识别 + 可取消的文件锁实现同一套 RPC。

影响：macOS 上 user-verification 相关 RPC 全部不可用。需要 cgo 绑定 Security.framework 并在 macOS 真机验证，本环境无法闭环。

### 2.3 关闭代理后的目录切换语义（`#40625`）

Go 没有 Rust 的 `agent_navigation` 线程关闭生命周期与"仅回放"的事件通道，因此无法在目录切换时忽略已关闭代理遗留的 in-progress turn。属于多代理边界语义，触发条件窄，但是真实的协议差异。

### 2.4 Guardian V3 证据链

Rust 的 `ext/guardian-v3`（原始历史证据快照、增量证据采集与显式确认、证据 housekeeping）在 Go 侧全部记为 N/A；Go 只跑 v1/v2 审核路径。叠加第一节的 11 条 Guardian 证据提交，这是"批准安全性"相关面里最大的一块空白。

### 2.5 Extension API 表面

Go 用直接接线（memories / guardian / hooks / plugin）覆盖等价行为，但没有 `codex_extension_api`、`codex_tools`、扩展 UI 树、`ext/queue` 元数据这些适配层。判定为结构性 N/A 是合理的，但它意味着：任何依赖扩展 API 的 Rust 新功能，到了 Go 侧都要单独做行为映射，长期成本会持续累积。

### 2.6 Cloud 会话服务端所有权（`#57267`，需重审）

账本仍记为 `blocked_precondition`，理由是"Go 没有原生 cloud 对话 TUI"。但 11-06 的 round 1316 已经落地 `--remote cloud` 内嵌 Cloud 后端（`cloudAppServerTransport`），`app/cloud_thread_mode.go` 的 `ThreadParamsModeCloud` / `ThreadSettingsOwnerServer` 已在 `thread/start`、`turn/start`、fork 守卫上生效。

仍然缺的部分（`cloud_thread_plan.md` §7 未完成清单）：`configRequirements/read` 的 `-32601` 容忍仅限 Cloud、recap/标题在 Cloud 下停用、模型/权限变更只走 `thread/settings/update` 不写本地 config、跳过本地 workspace/worktree/MCP 初始化、agents overview 用服务端 cwd 与权限、以及端到端 cloud 会话测试。

结论：这条应从"阻塞"改成"partial"，并挂上上述残余清单。

## 三、行为性残留（已对齐，但有尾巴）

**TUI**（`update/plan_2026_11_06.md`）：侧栏 chooser 的动画侧滑与浮动 workspace 快捷面板/预设浮层未移植；side chat 创建时未接 deferred MCP 启动通知与继承权限 profile；侧栏 tab 的鼠标命中测试未接线；Vim 的 `.` 重复只重放删除，不重放插入。

**CLI 与诊断**（`use_alignment.md` "Still open"）：`debug models --bundled` 被忽略、没有在线 catalog 客户端；`debug prompt-input` 仍打印 Go 的 `SessionPrompt` 而不是 Rust 的 `ResponseItem` 形状；`completion` 只覆盖功能项、没有 clap_complete 的完整布局；`doctor` 缺 macOS security 行与 Sparkle/appcast 扫描；`stdio-to-uds` 在 Windows 上不接受普通路径（缺 AF_UNIX transport）；`sandbox --sandbox-state-json` 对空 legacy profile 的归类与 0.166 不同；部分 `apply_patch` 路径的 OS 错误文案仍是 Go 措辞。

**版本号**：源码直接 `go build` 得到 `0.0.0`，Rust 报告发布版本。发布流水线已注入 `-ldflags`，属于构建流程差异，不是代码缺陷。

**远端凭据脱敏**：`remote-shell-snapshot-credential-masking` 仍是 partial——本机已有 bash 5.3 与 zsh 5.9.2 差分，但缺一个真实的 executor→executor 远端 Unix zsh 端到端环境。

## 四、认证与发布类阻塞（非代码）

- **L3 SDK 差分重认证**：受 authorization/provider 门控，`parity.json` 的 `cross-platform-release-gates` 与 `alignment-control` 都卡在这里。
- **macOS 原生 runner**：`.github/workflows/macos-platform-gates.yml` 在 macos-14/15 上"零步骤失败"，是仓库账号计费/配额问题，和 workflow 本身无关。
- **SQLite 六平台原生产物**：`sqlite-runtime-version` 域要求每个支持产物报告 >= 3.51.3 并通过 WAL 恢复，目前原生证据只有 `windows/amd64`。
- **连续运行次数**：基线要求 3 次连续差分 + 3 次连续 SDK 运行，`evidence.cleanSDKRuns` 只记录了 2 次。
- **`sdk-cancellation`**：Rust `bbbf396839` 本身在 PowerShell 下会留下孤儿进程，Go 终止整棵进程树更安全。这条要等上游修，不能在 Go 侧回退。

## 五、控制面卫生（建议先做）

1. **基线锚点（已修复，2026-11-08）**：`parity/commits.json`、`baseline.json`、`domains.json`、`contracts/manifest.json`、`alignment_control_test.go` 已统一推进到 `55897425e8`，3309 → 3584 条，275 条曾只在 `update/plan_2026_11_02…11_06` 里判定的提交已回写。`verified.rustCommit` 仍停在 `67f0994b`，因为它绑定的是 `parity.json` 的 SDK 证据，要等下一轮真实 parity/SDK 运行后才能前移。
2. **计数漂移（已修复）**：`baseline.json` 的计数与 `certificationBlockers` 文本现在由 `commits.json` 生成，`update/ledger_verify_2026_11_08.py` 的 `GATE` 为 PASS。残留：4 条提交只有 10 位 sha 前缀（`8d712cc542`、`45704d8c64`、`803d519ad3`、`24ed4a1b6c`），需要在权威 Rust 检出补齐。
3. **陈旧判定**：`#57267` 已从"无 cloud 载体"演进到"部分落地"，需要重审；`need_aligin.md` 仍停留在 2026-08-20 的口径，`parity.json` 的 `generatedAt` 是 2026-10-28。
4. **文档断链（已修复）**：`README.md` 的 `plan.md` / `desgn.md` / `docs/tui_tech_selection.md` 已改指 `update/`、`parity/README.md` 与 `tui_alignment.md`。

## 六、建议补齐顺序

**P0 — 先把度量修准（1–2 轮）**
账本重锚已完成（Round 1318）：`commits.json` 3584 条、`baseline.json`/`domains.json`/契约清单锚点同步、`#57267` 改判 partial、README 断链修复、`need_aligin.md` 加过期指针。验收物：`update/ledger_verify_2026_11_08.py` GATE PASS；`go test ./parity` 需在完整 Rust 检出上跑一次。

**P1 — 跟上正在扩散的方向（按上游热度）**
1. Guardian 证据链：先把 11 条 Guardian 提交逐条判定；能落的（证据检索工具、订阅、投递顺序、凭据绑定）先落，`ext/guardian-v3` 单独评估。
2. MCP 懒启动与网络策略（5 条）：既有性能收益，也有策略正确性风险。
3. 运行时鉴权覆盖面（5 条）：realtime / remote-control / Analytics / git attribution。
4. Code Mode 增量工具（7 条）与线程/存储性能（6 条）。

**P2 — 平台原生与体验尾巴**
Windows MXC 原生绑定、macOS Secure Enclave、Cloud 服务端所有权收口、TUI 侧栏与侧聊残余、CLI 诊断与 completion 布局。

**外部依赖**
GitHub Actions 计费解锁（macOS runner + SQLite 六平台产物）、L3 SDK 重认证所需的 authorization/provider 门禁、上游 `bbbf396839` 修复。

每条差异落地时按 `djalign` 的硬性规则办：**先找第一个分歧点，缩到最小复现，分类，修复后重跑最小场景，并把差异沉淀成契约（fixture/golden/manifest 条目）**，否则不关闭。

## 附录 A：93 条未审计上游提交（`55897425e8..748ab55ca7`）

| # | commit | 主题 | 分类 |
| --- | --- | --- | --- |
| 1 | `748ab55ca7` | Increase Bazel core unit test shards from 8 to 16 (#60206) | 发布/CI/测试 |
| 2 | `c9b4267c37` | Expose agent tree IDs and environment removal (#60190) | 线程/存储 |
| 3 | `c3936b4b8a` | Allow model overrides for incremental Code Mode tool messages (#60188) | Code Mode/增量工具 |
| 4 | `d960b36694` | Allow creating custom providers during sign-in (#60186) | 模型/Provider |
| 5 | `8044c6867c` | Add keyboard and mouse navigation for sidebar tabs (#60181) | 侧栏/侧聊 |
| 6 | `1455fda735` | Attach typed punctuation to completed skill mentions (#60180) | TUI 其它 |
| 7 | `5226f2fb4b` | Add metrics for incremental exec catalog updates (#60177) | Code Mode/增量工具 |
| 8 | `aa9b9cc94e` | Test exec catalog preservation across session and compaction changes (#60173) | 发布/CI/测试 |
| 9 | `202b590e84` | Preserve display instructions in answer moderation (#60172) | 其它 |
| 10 | `33b3f80a09` | Wire incremental exec tool updates into Code Mode sessions (#60163) | Code Mode/增量工具 |
| 11 | `2bc8e7f545` | Add support for incremental code mode tool definitions (#60157) | Code Mode/增量工具 |
| 12 | `57e64682f8` | Persist local workspace side chats and add tab close controls (#60152) | 侧栏/侧聊 |
| 13 | `57a808a392` | Preserve code mode dispatch errors in rollout traces (#60122) | Code Mode/增量工具 |
| 14 | `43b4274f3f` | Buffer payload JSON reads during rollout trace replay (#60118) | 线程/存储 |
| 15 | `cce9d3228b` | Use runtime authentication for Analytics requests (#60101) | 运行时鉴权 |
| 16 | `efdacaf46f` | Stop retrying terminal runtime authentication denials (#60096) | 运行时鉴权 |
| 17 | `4be90eb069` | Fetch account security reminders through app-server (#60082) | 账号 |
| 18 | `c4f0e09912` | Allow asynchronous model request budget checks (#60080) | 模型/Provider |
| 19 | `f5ac7a757e` | Support asynchronous host service-tier reads and acceptance (#60075) | 其它 |
| 20 | `f4275a2a2e` | Use runtime authentication for remote-control requests (#60073) | 运行时鉴权 |
| 21 | `0ed34eb9ce` | Ensure side-chat policy reaches the first model request (#60067) | 侧栏/侧聊 |
| 22 | `1c414e0d65` | Use thread runtime auth for Guardian Decisions and memory quota checks (#60064) | Guardian |
| 23 | `e6bf88ae02` | Support Cloud image messages and editable draft handoffs (#60061) | Cloud |
| 24 | `d4a341bd52` | Reauthenticate runtime requests before reusing WebSocket access (#60055) | 其它 |
| 25 | `44ccf39457` | Preserve model catalogs when starting child agents (#60049) | 模型/Provider |
| 26 | `358e464528` | Allow plugin starter prompts up to 512 characters (#60048) | 其它 |
| 27 | `b7d2321ef7` | Derive MCP network policy and preserve saved denials (#60046) | MCP |
| 28 | `a90c808408` | Centralize Windows sandbox filesystem override resolution (#60042) | 安全/Windows |
| 29 | `11ca3f7531` | Require explicit project trust in non-interactive runs (#60041) | 安全/Windows |
| 30 | `762f87d5bb` | Restrict legacy incremental tools to Amazon Bedrock providers (#60040) | Code Mode/增量工具 |
| 31 | `af9a98b074` | Add opt-in lazy startup for cached stdio MCP servers (#60039) | MCP |
| 32 | `fb2b06a829` | Surface MCP server startup errors when preparing cached tool calls (#60038) | MCP |
| 33 | `7ccf78dec4` | Preserve MCP sharing discovery when deferring cached stdio startup (#60037) | MCP |
| 34 | `0c58612c3c` | Fix authentication for git-attribution settings requests (#60035) | 其它 |
| 35 | `738ef9f3bb` | Omit sensitive process inputs from spawn trace logs (#60034) | 安全/Windows |
| 36 | `cfc11eb483` | Allow extension status content to use multiple footer rows (#60032) | UI tree/扩展渲染 |
| 37 | `5c7a1602c8` | Unify TUI status line layout for extension content (#60031) | UI tree/扩展渲染 |
| 38 | `7cdb219883` | Render UI trees directly into terminal buffers (#60029) | UI tree/扩展渲染 |
| 39 | `f8df49a196` | Reuse prepared UI trees for footer rendering (#60028) | UI tree/扩展渲染 |
| 40 | `66a56b17a1` | Keep configured status lines on the native renderer (#60027) | UI tree/扩展渲染 |
| 41 | `8e7ccd8472` | Add multiline layout and row wrapping to UI trees (#60026) | UI tree/扩展渲染 |
| 42 | `d749ee847f` | Separate UI tree preparation from line rendering (#60022) | UI tree/扩展渲染 |
| 43 | `1a20f53c33` | Normalize inline UI tree text across styled leaves (#60021) | UI tree/扩展渲染 |
| 44 | `7e980a54fe` | Strip terminal commands from UI tree strings (#60020) | UI tree/扩展渲染 |
| 45 | `2157f98648` | Add theme-aware semantic colors to extension UI trees (#60019) | UI tree/扩展渲染 |
| 46 | `5ff78c5e66` | Propagate turn triggers to usage analytics events (#60018) | 其它 |
| 47 | `2f742f5885` | Disable bundled skills in the prompt-prefix comparison test (#60017) | 发布/CI/测试 |
| 48 | `10cf2b9ea2` | Honor managed runtime authentication modes and expand coverage (#60014) | 运行时鉴权 |
| 49 | `e0aa0434f4` | Adapt multi-agent mode guidance for hosted tools (#60011) | 其它 |
| 50 | `a5a00d6c73` | Add a shared Windows Bazel setup action (#60010) | 发布/CI/测试 |
| 51 | `2497eab251` | Add custom gateway selection to TUI sign-in (#60009) | 模型/Provider |
| 52 | `0389e80f07` | Refactor TUI clipboard and composer input handling (#60008) | TUI 其它 |
| 53 | `7de8a0afc0` | Start cloud threads with the local GitHub project (#60007) | Cloud |
| 54 | `df769889c0` | Reduce SQLite write overhead for thread activity and history (#60006) | 线程/存储 |
| 55 | `01ab354312` | Expose ChatGPT user IDs in `account/read` responses (#60004) | 账号 |
| 56 | `44f1b0e867` | Apply runtime authentication to realtime connections (#60003) | 运行时鉴权 |
| 57 | `09bd19a0de` | Decouple Guardian retrieval from local message-board handles (#60002) | Guardian |
| 58 | `ecef7e0936` | Add bounded original-evidence delivery for Guardian reviewers (#60001) | Guardian |
| 59 | `5f82dc9e59` | Plan Guardian evidence delivery in source order (#60000) | Guardian |
| 60 | `af83299ada` | Add model-driven Guardian channel selection (#59999) | Guardian |
| 61 | `eb485f57ba` | Add atomic bulk channel subscription updates (#59998) | 线程/存储 |
| 62 | `8d0098930e` | Add subscription tools for Guardian reviewers (#59995) | Guardian |
| 63 | `e4bd7871b1` | Add read-only evidence retrieval tools for Guardian reviewers (#59994) | Guardian |
| 64 | `632eee03e9` | Add fixed read views for local message boards (#59993) | 线程/存储 |
| 65 | `8fcabd75a0` | Preserve app invocation context across MCP tool refreshes (#59991) | MCP |
| 66 | `1a2f5babe8` | Add inherited styling to TUI display trees (#59988) | UI tree/扩展渲染 |
| 67 | `cf39544acd` | Resolve startup models using thread creation options (#59987) | 模型/Provider |
| 68 | `78b1dda5f6` | Render typed prompts as Markdown in the TUI (#59983) | TUI 其它 |
| 69 | `6abffd442d` | Test rejection of PNGs with readable dimensions but corrupt pixels (#59980) | 发布/CI/测试 |
| 70 | `1be4eb123e` | Test command completion before network denial grace expires (#59974) | 发布/CI/测试 |
| 71 | `247934cfe5` | Enable gRPC and host output truncation by default for Code Mode (#59973) | Code Mode/增量工具 |
| 72 | `8842c09a7f` | Fix Opus header installation with relative symlink inputs (#59972) | 发布/CI/测试 |
| 73 | `e428b9a16b` | Reduce contention in thread writer lock coordination (#59969) | 线程/存储 |
| 74 | `2889d8e1bf` | Normalize turn context permissions to a required profile (#59963) | 其它 |
| 75 | `4e688da437` | Skip the network denial grace period when remote decisions drain (#59960) | 其它 |
| 76 | `12dc86beb3` | Expose model metadata from each thread's catalog and settings (#59958) | 模型/Provider |
| 77 | `ebe94e25c5` | Bump `ignore` to `0.4.30` (#59955) | 发布/CI/测试 |
| 78 | `0768988e55` | Resolve skill display names in read and installations tools (#59954) | 技能/文档 |
| 79 | `4688e49329` | Route broad Codex setup questions through self-knowledge guidance (#59949) | 其它 |
| 80 | `4980f9585a` | Narrow openai-docs to product Q&A and first-time API setup (#59944) | 技能/文档 |
| 81 | `c1c07853fe` | Use permission profiles for protected-model startup downgrades (#59933) | 模型/Provider |
| 82 | `814347ecd2` | Improve sidebar chat selection and tab hover highlighting (#59928) | 侧栏/侧聊 |
| 83 | `2c502d8b3d` | Bind Guardian Decisions requests to their credential source (#59914) | Guardian |
| 84 | `2210380232` | Resolve Guardian policy from the reviewed thread's catalog (#59913) | Guardian |
| 85 | `ad1dbba32d` | Scope file change approval cancellation to the requesting turn (#59910) | 其它 |
| 86 | `bbf57cec15` | Avoid full image decoding for original-detail context estimates (#59908) | 上下文预算 |
| 87 | `f57966695f` | Validate implicit API keys for Guardian Decisions (#59905) | Guardian |
| 88 | `e3fe1ade2c` | Delete incomplete prereleases by release ID (#59903) | 发布/CI/测试 |
| 89 | `b8725c1896` | Keep verified question/answer records whole under elastic budgets (#59902) | 上下文预算 |
| 90 | `f49ae52079` | Recompute the approval reviewer when switching models (#59893) | 模型/Provider |
| 91 | `b1a0dff158` | Bound REPL provenance costs in elastic context preparation (#59891) | 上下文预算 |
| 92 | `b6f53857fc` | Remove incomplete prereleases after asset upload failures (#59887) | 发布/CI/测试 |
| 93 | `ddd1812a51` | Carry targeted moderation decisions on final-answer messages (#59842) | Guardian |

## 附录 B：证据索引

- `parity/commits.json`：3584 条台账与每条 evidence。
- `parity/domains.json`：4 个 partial 域的接受标准与证据尾部。
- `parity/baseline.json`：认证要求（3 次连续差分/SDK 运行、六平台矩阵、zeroExceptions）。
- `parity/contracts/manifest.json`：81 个契约。
- `parity.json`：225 done / 3 partial / 4 intentional_difference。
- `update/plan_2026_11_06.md`、`update/plan_2026_11_06_round1316.md`、`update/plan_2026_11_06_round1317.md`：当前轮次的 partial 与 N/A 判定。
- `cloud_thread_plan.md`：Cloud 服务端所有权的文件级残余清单。
- `use_alignment.md` "Still open"：与 pinned 0.166 二进制的真实使用差分残余。
- `sandbox/mxc/run_windows.go`、`appserver/user_verification_runtime.go`、`app/cloud_thread_mode.go`、`app/remote_tui_cloud.go`：代码级桩点核对。
- `https://api.github.com/repos/openai/codex/compare/55897425e8...main`（2026-11-08 只读比对，ahead_by = 93）。
